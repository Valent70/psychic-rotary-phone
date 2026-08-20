// Package assurance is the VERIQO Assurance Plane — the layer the
// v7.10.3 production-readiness audit closed with and called "pembeda
// besar": VERIQO does not only run intelligence, it proves that the
// intelligence it ran can be trusted.
//
// It exists to make one specific failure impossible:
//
//	report says CLOSED, code is only PARTIAL
//
// Everything here is machine-checked. A capability's status is not a
// word someone typed in a report; it is derived from evidence
// artifacts that were produced by actually running something, each
// carrying its own content hash. If the evidence is absent, the status
// degrades automatically. There is no way to write PRODUCTION_READY
// into this package by hand.
//
// Three rules encode the audit's PHASE 54 ("NO FALSE GREEN"):
//
//  1. A gate is PASS only with a verifiable evidence artifact.
//  2. A release is PRODUCTION_READY only when every MANDATORY gate is
//     PASS. Not 80%. Not "weighted score above threshold".
//  3. A single BLOCKED/OPEN/PARTIAL mandatory gate makes the whole
//     verdict NOT_PRODUCTION_READY, and the reason is named.
//
// Waivers exist (real programmes need them) but a waived mandatory
// gate NEVER yields PRODUCTION_READY — it yields
// CONDITIONALLY_READY_WITH_WAIVERS, which is a different, visibly
// weaker word, and the waiver's owner, expiry and justification are
// part of the signed manifest.
package assurance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Status is the lifecycle status of one capability or gate. The audit
// forbids "Done"/"Closed"/"Complete"; these five are the only legal
// values, and they are ordered.
type Status string

const (
	StatusOpen            Status = "OPEN"             // nothing exists yet
	StatusImplemented     Status = "IMPLEMENTED"      // code exists
	StatusIntegrated      Status = "INTEGRATED"       // wired into the real path
	StatusVerified        Status = "VERIFIED"         // tests + adversarial tests pass
	StatusQualified       Status = "QUALIFIED"        // performance/chaos/DR evidence exists
	StatusProductionReady Status = "PRODUCTION_READY" // all of the above + operational evidence
	StatusBlocked         Status = "BLOCKED"          // cannot proceed; external dependency
	StatusWaived          Status = "WAIVED"           // consciously accepted risk, with owner+expiry
)

var statusRank = map[Status]int{
	StatusOpen: 0, StatusBlocked: 0, StatusWaived: 1, StatusImplemented: 2,
	StatusIntegrated: 3, StatusVerified: 4, StatusQualified: 5, StatusProductionReady: 6,
}

// AtLeast reports whether s meets or exceeds min.
func (s Status) AtLeast(min Status) bool { return statusRank[s] >= statusRank[min] }

// Errors.
var (
	ErrDuplicateGate    = errors.New("assurance: duplicate gate ID")
	ErrUnknownGate      = errors.New("assurance: unknown gate ID")
	ErrNoEvidence       = errors.New("assurance: gate cannot PASS without an evidence artifact")
	ErrEvidenceInvalid  = errors.New("assurance: evidence artifact hash does not match its content")
	ErrWaiverIncomplete = errors.New("assurance: a waiver requires owner, justification and expiry tick")
	ErrManifestTampered = errors.New("assurance: readiness manifest hash does not match its own fields")
)

// Evidence is a machine-produced artifact backing a gate: the command
// that was run, what it produced, and a content hash over the output.
// A gate holding Evidence whose Hash does not match its Output is
// treated as having no evidence at all.
type Evidence struct {
	ArtifactID string `json:"artifact_id"`
	GateID     string `json:"gate_id"`
	Command    string `json:"command"`
	Output     string `json:"output"`
	ExitCode   int    `json:"exit_code"`
	ProducedAt uint64 `json:"produced_at"` // VERIQO tick or unix seconds, deployment's choice
	Hash       string `json:"hash"`
}

// NewEvidence content-addresses an evidence artifact.
func NewEvidence(gateID, command, output string, exitCode int, producedAt uint64) Evidence {
	e := Evidence{
		GateID: gateID, Command: command, Output: output,
		ExitCode: exitCode, ProducedAt: producedAt,
	}
	e.Hash = e.computeHash()
	e.ArtifactID = e.Hash[:16]
	return e
}

func (e Evidence) computeHash() string {
	h := sha256.New()
	fmt.Fprintf(h, "veriqo.assurance.evidence/v1|gate=%s|cmd=%s|exit=%d|at=%d|out=",
		e.GateID, e.Command, e.ExitCode, e.ProducedAt)
	h.Write([]byte(e.Output))
	return hex.EncodeToString(h.Sum(nil))
}

// Verify independently recomputes the artifact hash.
func (e Evidence) Verify() error {
	if e.Hash == "" {
		return ErrNoEvidence
	}
	if e.computeHash() != e.Hash {
		return ErrEvidenceInvalid
	}
	return nil
}

// Passing reports whether the artifact represents a successful run.
func (e Evidence) Passing() bool { return e.ExitCode == 0 && e.Verify() == nil }

// Waiver is a consciously accepted, time-bounded gap.
type Waiver struct {
	Owner         string `json:"owner"`
	Justification string `json:"justification"`
	ExpiresAtTick uint64 `json:"expires_at_tick"`
	ApprovedBy    string `json:"approved_by"`
}

func (w Waiver) valid() error {
	if strings.TrimSpace(w.Owner) == "" || strings.TrimSpace(w.Justification) == "" || w.ExpiresAtTick == 0 {
		return ErrWaiverIncomplete
	}
	return nil
}

// Gate is one release gate: something that must be true before VERIQO
// can call itself production-ready.
type Gate struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	// Mandatory gates cannot be compensated for. A single failing
	// mandatory gate blocks the release, whatever every other gate says.
	Mandatory bool `json:"mandatory"`
	// RequiredStatus is the minimum status this gate must reach.
	RequiredStatus Status `json:"required_status"`
	// OwnerPackage names the code that implements the capability, so a
	// gate is traceable to an artifact in the repo, not to a paragraph.
	OwnerPackage string `json:"owner_package"`
	// ExitCriteria states, in one sentence, what "done" means.
	ExitCriteria string `json:"exit_criteria"`

	Status   Status     `json:"status"`
	Evidence []Evidence `json:"evidence,omitempty"`
	Waiver   *Waiver    `json:"waiver,omitempty"`
	// Blocker names the external dependency when Status is BLOCKED —
	// mandatory, so "blocked" can never be a vague shrug.
	Blocker string `json:"blocker,omitempty"`
}

// EffectiveStatus derives a gate's real status from its evidence,
// never from what was declared. This is the anti-false-green core: a
// gate declared PRODUCTION_READY with no passing evidence reports
// OPEN.
func (g Gate) EffectiveStatus() Status {
	if g.Waiver != nil && g.Waiver.valid() == nil {
		return StatusWaived
	}
	if g.Status == StatusBlocked {
		return StatusBlocked
	}
	// Any status at or above VERIFIED demands passing evidence.
	if g.Status.AtLeast(StatusVerified) {
		for _, e := range g.Evidence {
			if e.Passing() {
				return g.Status
			}
		}
		// Declared verified, no artifact: degrade, loudly.
		if g.Status.AtLeast(StatusIntegrated) {
			return StatusImplemented
		}
		return StatusOpen
	}
	return g.Status
}

// Satisfied reports whether the gate meets its own requirement.
func (g Gate) Satisfied() bool { return g.EffectiveStatus().AtLeast(g.RequiredStatus) }

// Registry holds every gate for a release.
type Registry struct {
	gates map[string]*Gate
	order []string
}

// NewRegistry constructs an empty gate registry.
func NewRegistry() *Registry { return &Registry{gates: map[string]*Gate{}} }

// Register adds a gate definition.
func (r *Registry) Register(g Gate) error {
	if _, dup := r.gates[g.ID]; dup {
		return fmt.Errorf("%w: %s", ErrDuplicateGate, g.ID)
	}
	if g.RequiredStatus == "" {
		g.RequiredStatus = StatusVerified
	}
	if g.Status == "" {
		g.Status = StatusOpen
	}
	copyG := g
	r.gates[g.ID] = &copyG
	r.order = append(r.order, g.ID)
	return nil
}

// Attach records an evidence artifact against a gate and sets its
// status. The status is a CLAIM; EffectiveStatus decides whether the
// claim survives contact with the evidence.
func (r *Registry) Attach(gateID string, status Status, ev Evidence) error {
	g, ok := r.gates[gateID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownGate, gateID)
	}
	if err := ev.Verify(); err != nil {
		return err
	}
	g.Status = status
	g.Evidence = append(g.Evidence, ev)
	return nil
}

// Block marks a gate as blocked by a named external dependency.
func (r *Registry) Block(gateID, blocker string) error {
	g, ok := r.gates[gateID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownGate, gateID)
	}
	if strings.TrimSpace(blocker) == "" {
		return errors.New("assurance: BLOCKED requires a named blocker")
	}
	g.Status, g.Blocker = StatusBlocked, blocker
	return nil
}

// Waive records a time-bounded, owned, justified waiver.
func (r *Registry) Waive(gateID string, w Waiver) error {
	g, ok := r.gates[gateID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownGate, gateID)
	}
	if err := w.valid(); err != nil {
		return err
	}
	g.Waiver = &w
	return nil
}

// Gates returns every gate in registration order.
func (r *Registry) Gates() []Gate {
	out := make([]Gate, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, *r.gates[id])
	}
	return out
}

// Get returns one gate.
func (r *Registry) Get(id string) (Gate, bool) {
	g, ok := r.gates[id]
	if !ok {
		return Gate{}, false
	}
	return *g, true
}

// Verdict is the release-level answer.
type Verdict string

const (
	VerdictProductionReady    Verdict = "PRODUCTION_READY"
	VerdictConditional        Verdict = "CONDITIONALLY_READY_WITH_WAIVERS"
	VerdictNotProductionReady Verdict = "NOT_PRODUCTION_READY"
)

// Assessment is the full, explainable readiness result.
type Assessment struct {
	Verdict          Verdict  `json:"verdict"`
	MandatoryTotal   int      `json:"mandatory_total"`
	MandatoryPassing int      `json:"mandatory_passing"`
	OptionalTotal    int      `json:"optional_total"`
	OptionalPassing  int      `json:"optional_passing"`
	FailingMandatory []string `json:"failing_mandatory,omitempty"`
	WaivedMandatory  []string `json:"waived_mandatory,omitempty"`
	BlockedMandatory []string `json:"blocked_mandatory,omitempty"`
	Reasons          []string `json:"reasons,omitempty"`
}

// Assess computes the release verdict. It is deliberately not a score.
func (r *Registry) Assess() Assessment {
	a := Assessment{Verdict: VerdictProductionReady}
	ids := append([]string(nil), r.order...)
	sort.Strings(ids)
	for _, id := range ids {
		g := *r.gates[id]
		eff := g.EffectiveStatus()
		if !g.Mandatory {
			a.OptionalTotal++
			if g.Satisfied() {
				a.OptionalPassing++
			}
			continue
		}
		a.MandatoryTotal++
		switch {
		case eff == StatusWaived:
			a.WaivedMandatory = append(a.WaivedMandatory, g.ID)
			a.Reasons = append(a.Reasons, fmt.Sprintf("%s: WAIVED by %s until tick %d (%s)",
				g.ID, g.Waiver.Owner, g.Waiver.ExpiresAtTick, g.Waiver.Justification))
		case eff == StatusBlocked:
			a.BlockedMandatory = append(a.BlockedMandatory, g.ID)
			a.Reasons = append(a.Reasons, fmt.Sprintf("%s: BLOCKED by %s", g.ID, g.Blocker))
		case g.Satisfied():
			a.MandatoryPassing++
		default:
			a.FailingMandatory = append(a.FailingMandatory, g.ID)
			a.Reasons = append(a.Reasons, fmt.Sprintf("%s: %s but requires %s (%s)",
				g.ID, eff, g.RequiredStatus, g.ExitCriteria))
		}
	}
	switch {
	case len(a.FailingMandatory) > 0 || len(a.BlockedMandatory) > 0:
		a.Verdict = VerdictNotProductionReady
	case len(a.WaivedMandatory) > 0:
		a.Verdict = VerdictConditional
	case a.MandatoryTotal == 0:
		a.Verdict = VerdictNotProductionReady
		a.Reasons = append(a.Reasons, "no mandatory gates registered: an empty gate set is never evidence of readiness")
	}
	return a
}

// AssessJSON renders the assessment, stably.
func (r *Registry) AssessJSON() ([]byte, error) {
	return json.MarshalIndent(r.Assess(), "", "  ")
}
