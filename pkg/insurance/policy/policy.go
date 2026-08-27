// Package policy models an insurance policy as more than "a PDF" —
// blueprint §5-§6. Its P0 requirement (§6) is explicit: a policy can
// exist as Original + Endorsement + Amendment + Renewal + Special
// Clause, and VICE must determine WHICH VERSION WAS EFFECTIVE AT THE
// TIME OF THE INCIDENT — never silently using the latest version.
package policy

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Clause is one extracted policy clause — blueprint §5's own required
// per-clause fields.
type Clause struct {
	ClauseID      string `json:"clause_id"`
	DocumentID    string `json:"document_id"`
	Page          int    `json:"page,omitempty"`
	SourceHash    string `json:"source_hash"`
	TextSpan      string `json:"text_span"`
	Version       string `json:"version"`
	EffectiveDate uint64 `json:"effective_date"`
}

// Version is one version of a policy — the original issuance, or one
// endorsement/amendment/renewal/special-clause revision of it.
type Version struct {
	PolicyID     string `json:"policy_id"`
	VersionID    string `json:"version_id"`
	PolicyNumber string `json:"policy_number"`

	Insurer string `json:"insurer"`
	Insured string `json:"insured"`

	EffectiveFrom uint64 `json:"effective_from"`
	EffectiveTo   uint64 `json:"effective_to"` // 0 = open-ended

	InsuredInterest string   `json:"insured_interest,omitempty"`
	CoveredPerils   []string `json:"covered_perils,omitempty"`
	Exclusions      []string `json:"exclusions,omitempty"`
	Deductible      string   `json:"deductible,omitempty"`
	Limits          string   `json:"limits,omitempty"`
	SubLimits       []string `json:"sub_limits,omitempty"`
	Warranties      []string `json:"warranties,omitempty"`
	Conditions      []string `json:"conditions,omitempty"`

	NoticeRequirements     string `json:"notice_requirements,omitempty"`
	MitigationRequirements string `json:"mitigation_requirements,omitempty"`
	ClaimsProcedure        string `json:"claims_procedure,omitempty"`

	TerritorialScope string `json:"territorial_scope,omitempty"`
	VoyageScope      string `json:"voyage_scope,omitempty"`
	Jurisdiction     string `json:"jurisdiction,omitempty"`
	GoverningLaw     string `json:"governing_law,omitempty"`

	// Kind distinguishes an original policy from what modified it —
	// required so History can reason about supersession, not just
	// chronological ordering.
	Kind Kind `json:"kind"`
	// Supersedes names the VersionID this version amends/renews/
	// endorses, empty only for Kind == KindOriginal.
	Supersedes string `json:"supersedes,omitempty"`

	Clauses []Clause `json:"clauses,omitempty"`

	// Participants records every co-insurer and reinsurer sharing this
	// version's risk — the VERIQO Master Closure Mandate §20
	// ("Reinsurance / Co-insurance") requirement. Empty means a single
	// insurer bears the whole risk, which remains the common case and is
	// never treated as an error. See participation.go.
	Participants []Participant `json:"participants,omitempty"`
}

// Kind classifies a policy Version's relationship to the policy's
// history — blueprint §6's enumerated set (Original / Endorsement /
// Amendment / Renewal / Special Clause).
type Kind string

const (
	KindOriginal      Kind = "ORIGINAL"
	KindEndorsement   Kind = "ENDORSEMENT"
	KindAmendment     Kind = "AMENDMENT"
	KindRenewal       Kind = "RENEWAL"
	KindSpecialClause Kind = "SPECIAL_CLAUSE"
)

var knownKinds = map[Kind]bool{
	KindOriginal: true, KindEndorsement: true, KindAmendment: true,
	KindRenewal: true, KindSpecialClause: true,
}

// IsKnownKind reports whether k is a modelled version kind.
func IsKnownKind(k Kind) bool { return knownKinds[k] }

var (
	ErrEmptyPolicyID              = errors.New("policy: PolicyID must be non-empty")
	ErrEmptyVersionID             = errors.New("policy: VersionID must be non-empty")
	ErrUnknownKind                = errors.New("policy: unknown Kind")
	ErrOriginalNeedsNoSupersedes  = errors.New("policy: KindOriginal must not declare Supersedes")
	ErrNonOriginalNeedsSupersedes = errors.New("policy: non-original versions must declare Supersedes")
	ErrEffectiveToBeforeFrom      = errors.New("policy: EffectiveTo must be 0 (open) or >= EffectiveFrom")
	ErrDuplicateVersionID         = errors.New("policy: VersionID already registered")
	ErrSupersedesUnknown          = errors.New("policy: Supersedes references a VersionID not in this history")
	ErrNoEffectiveVersion         = errors.New("policy: no policy version was effective at the given time — do not default to the latest version")
)

// Validate checks v's own internal consistency (not its relationship
// to other versions — History.Add does that).
func (v Version) Validate() error {
	if v.PolicyID == "" {
		return ErrEmptyPolicyID
	}
	if v.VersionID == "" {
		return ErrEmptyVersionID
	}
	if !IsKnownKind(v.Kind) {
		return fmt.Errorf("%w: %q", ErrUnknownKind, v.Kind)
	}
	if v.Kind == KindOriginal && v.Supersedes != "" {
		return ErrOriginalNeedsNoSupersedes
	}
	if v.Kind != KindOriginal && v.Supersedes == "" {
		return ErrNonOriginalNeedsSupersedes
	}
	if v.EffectiveTo != 0 && v.EffectiveTo < v.EffectiveFrom {
		return ErrEffectiveToBeforeFrom
	}
	if err := validateParticipants(v.Participants); err != nil {
		return err
	}
	return nil
}

// History is the full, ordered set of versions for ONE policy — the
// blueprint's own "Original + Endorsement + Amendment + Renewal +
// Special Clause" chain.
type History struct {
	mu       sync.RWMutex
	policyID string
	versions map[string]Version // VersionID -> Version
	order    []string
}

// NewHistory returns an empty policy history for policyID.
func NewHistory(policyID string) (*History, error) {
	if policyID == "" {
		return nil, ErrEmptyPolicyID
	}
	return &History{policyID: policyID, versions: make(map[string]Version)}, nil
}

// Add registers a new version in this policy's history. Refuses a
// version whose PolicyID doesn't match this history, a duplicate
// VersionID, or a Supersedes reference to a version not already in
// this history (fail-closed: you cannot amend something VICE has
// never seen).
func (h *History) Add(v Version) error {
	if err := v.Validate(); err != nil {
		return err
	}
	if v.PolicyID != h.policyID {
		return fmt.Errorf("policy: version's PolicyID %q does not match this history's PolicyID %q", v.PolicyID, h.policyID)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.versions[v.VersionID]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateVersionID, v.VersionID)
	}
	if v.Supersedes != "" {
		if _, ok := h.versions[v.Supersedes]; !ok {
			return fmt.Errorf("%w: %s", ErrSupersedesUnknown, v.Supersedes)
		}
	}
	h.versions[v.VersionID] = v
	h.order = append(h.order, v.VersionID)
	return nil
}

// Get returns the version for versionID.
func (h *History) Get(versionID string) (Version, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	v, ok := h.versions[versionID]
	return v, ok
}

// All returns every version in registration order.
func (h *History) All() []Version {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]Version, 0, len(h.order))
	for _, id := range h.order {
		out = append(out, h.versions[id])
	}
	return out
}

// EffectiveAt is the P0 operation the blueprint names explicitly
// (§6): given a tick (e.g. the incident time), return the version
// whose [EffectiveFrom, EffectiveTo) window covers it. If more than
// one version's window covers the tick (an endorsement narrowing an
// otherwise-still-open original, say), the version with the LATEST
// EffectiveFrom wins — the most specific, most recently effective
// change governs, matching how policy endorsements actually layer in
// practice. Returns ErrNoEffectiveVersion, never the latest version by
// default, when nothing covers the tick.
func (h *History) EffectiveAt(tick uint64) (Version, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	var candidates []Version
	for _, id := range h.order {
		v := h.versions[id]
		if tick < v.EffectiveFrom {
			continue
		}
		if v.EffectiveTo != 0 && tick >= v.EffectiveTo {
			continue
		}
		candidates = append(candidates, v)
	}
	if len(candidates) == 0 {
		return Version{}, ErrNoEffectiveVersion
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].EffectiveFrom > candidates[j].EffectiveFrom
	})
	return candidates[0], nil
}

// Latest returns the version with the highest EffectiveFrom,
// regardless of whether it covers any particular tick. Callers must
// use this ONLY for "what is the current state of the policy today"
// questions, never as a substitute for EffectiveAt when reasoning
// about a historical incident (blueprint §6: "Tidak boleh memakai
// policy terbaru secara otomatis").
func (h *History) Latest() (Version, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if len(h.order) == 0 {
		return Version{}, false
	}
	best := h.versions[h.order[0]]
	for _, id := range h.order[1:] {
		v := h.versions[id]
		if v.EffectiveFrom > best.EffectiveFrom {
			best = v
		}
	}
	return best, true
}

// Count returns the number of versions in the history.
func (h *History) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.versions)
}
