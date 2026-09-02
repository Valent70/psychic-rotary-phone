// Package event defines the canonical VERIQO event envelope
// (MIP-001 §10) and event taxonomy (§11).
//
// WHY THIS IS NOT A SECOND LEDGER. MIP §7 forbids creating a duplicate
// authoritative engine, and names exactly this trap: a system that
// grows a ledger, an audit-ledger, a disclosure-ledger, a
// privilege-ledger and a redaction-ledger has five sources of truth
// and therefore none. This package adds NO storage, NO append path and
// NO second chain. It defines the canonical SHAPE of an event and the
// hash discipline that binds one to its predecessor; the authoritative
// store remains pkg/platform/audit.
//
// The envelope's contribution over a bare audit record is the set of
// fields that make an event replayable and policy-attributable:
// PolicyVersion and ConfigurationHash (so a historical event can be
// re-evaluated under the policy that governed it -- Articles 7 and
// 26), Purpose and ActorType (so AI influence is never anonymous --
// Articles 8 and 27), and ReplayReference (Article 10).
//
// DETERMINISM. PayloadHash and EventHash are computed over
// JCS-canonicalized content (pkg/canonical/jcs), so the same event
// hashes identically on any machine at any later date. No wall-clock
// value enters a hash: OccurredAt and RecordedAt are carried as
// logical ticks, not timestamps, for the same reason the rest of the
// kernel avoids the system clock.
package event

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"veriqo/pkg/canonical/jcs"
)

var (
	ErrEmptyEventID       = errors.New("event: EventID must be non-empty")
	ErrEmptyTenantID      = errors.New("event: TenantID must be non-empty")
	ErrEmptyEventType     = errors.New("event: EventType must be non-empty")
	ErrUnknownFamily      = errors.New("event: EventType family is not in the canonical taxonomy")
	ErrEmptyAggregate     = errors.New("event: AggregateType and AggregateID must be non-empty")
	ErrEmptyActor         = errors.New("event: ActorID and ActorType must be non-empty")
	ErrUnknownActorType   = errors.New("event: ActorType is not one of the canonical actor types")
	ErrEmptyPolicyVersion = errors.New("event: PolicyVersion must be non-empty -- an event with no governing policy cannot be replayed under Article 7")
	ErrChainBroken        = errors.New("event: PreviousEventHash does not match the prior event's EventHash")
	ErrSequenceGap        = errors.New("event: SequenceNo is not strictly increasing by one")
	ErrHashMismatch       = errors.New("event: recomputed EventHash does not match the recorded one")
	ErrPayloadMismatch    = errors.New("event: recomputed PayloadHash does not match the recorded one")
)

// ActorType distinguishes who caused an event. AI is a first-class,
// separately-named actor type precisely so that an AI-caused event can
// never be indistinguishable from a human one (Article 27).
const (
	ActorHuman   = "HUMAN"
	ActorService = "SERVICE"
	ActorAI      = "AI"
	ActorSystem  = "SYSTEM"
)

// ActorTypes returns the canonical actor types.
func ActorTypes() []string {
	return []string{ActorHuman, ActorService, ActorAI, ActorSystem}
}

// families is the canonical event taxonomy (MIP §11). An EventType
// must begin with one of these followed by a dot.
var families = []string{
	"case", "issue", "claim", "evidence", "acquisition", "source",
	"provenance", "integrity", "independence", "observability",
	"hypothesis", "contradiction", "trust", "qualification", "dissent",
	"privilege", "protective_order", "disclosure", "redaction", "ai",
	"conflict", "policy", "workflow", "replay", "verification",
}

// Families returns the canonical event families.
func Families() []string {
	out := make([]string, len(families))
	copy(out, families)
	return out
}

// FamilyOf returns the family prefix of an event type, and whether it
// is a recognised family.
func FamilyOf(eventType string) (string, bool) {
	i := strings.Index(eventType, ".")
	if i <= 0 || i == len(eventType)-1 {
		return "", false
	}
	fam := eventType[:i]
	for _, f := range families {
		if f == fam {
			return fam, true
		}
	}
	return fam, false
}

// Envelope is the canonical event envelope. Field order and names
// follow MIP §10 exactly.
type Envelope struct {
	EventID       string `json:"event_id"`
	TenantID      string `json:"tenant_id"`
	CaseID        string `json:"case_id,omitempty"`
	EventType     string `json:"event_type"`
	AggregateType string `json:"aggregate_type"`
	AggregateID   string `json:"aggregate_id"`
	SequenceNo    uint64 `json:"sequence_no"`

	ActorID   string `json:"actor_id"`
	ActorType string `json:"actor_type"`
	Purpose   string `json:"purpose,omitempty"`

	PolicyDecisionID  string `json:"policy_decision_id,omitempty"`
	PolicyVersion     string `json:"policy_version"`
	ConfigurationHash string `json:"configuration_hash,omitempty"`

	PayloadHash       string `json:"payload_hash"`
	PreviousEventHash string `json:"previous_event_hash"`
	EventHash         string `json:"event_hash"`

	// OccurredAt and RecordedAt are LOGICAL ticks, not wall-clock
	// timestamps -- see this package's doc comment. OccurredAt is when
	// the thing happened; RecordedAt is when VERIQO learned of it. They
	// differ for externally-sourced evidence, and the difference is
	// itself probative.
	OccurredAt uint64 `json:"occurred_at"`
	RecordedAt uint64 `json:"recorded_at"`

	AuthoritySignature string `json:"authority_signature,omitempty"`
	ReplayReference    string `json:"replay_reference,omitempty"`
}

// GenesisHash is the PreviousEventHash of the first event in a chain.
const GenesisHash = "GENESIS"

// HashPayload computes the canonical payload hash. A nil payload
// hashes the empty canonical object rather than being left blank, so
// "no payload" is still a definite, verifiable value.
func HashPayload(payload any) (string, error) {
	if payload == nil {
		payload = map[string]any{}
	}
	h, err := jcs.Hash(payload)
	if err != nil {
		return "", fmt.Errorf("event: hashing payload: %w", err)
	}
	return h, nil
}

// hashableView is the envelope minus its own EventHash. A hash can
// never be part of what it hashes -- the same hash-then-bind rule the
// dossier package follows.
type hashableView struct {
	EventID           string `json:"event_id"`
	TenantID          string `json:"tenant_id"`
	CaseID            string `json:"case_id"`
	EventType         string `json:"event_type"`
	AggregateType     string `json:"aggregate_type"`
	AggregateID       string `json:"aggregate_id"`
	SequenceNo        uint64 `json:"sequence_no"`
	ActorID           string `json:"actor_id"`
	ActorType         string `json:"actor_type"`
	Purpose           string `json:"purpose"`
	PolicyDecisionID  string `json:"policy_decision_id"`
	PolicyVersion     string `json:"policy_version"`
	ConfigurationHash string `json:"configuration_hash"`
	PayloadHash       string `json:"payload_hash"`
	PreviousEventHash string `json:"previous_event_hash"`
	OccurredAt        uint64 `json:"occurred_at"`
	RecordedAt        uint64 `json:"recorded_at"`
}

// ComputeEventHash derives the envelope's own hash over every field
// except EventHash and AuthoritySignature. The signature is excluded
// for the same reason: a signature is made OVER the hash, so it cannot
// be inside it.
func ComputeEventHash(e Envelope) (string, error) {
	v := hashableView{
		EventID: e.EventID, TenantID: e.TenantID, CaseID: e.CaseID,
		EventType: e.EventType, AggregateType: e.AggregateType, AggregateID: e.AggregateID,
		SequenceNo: e.SequenceNo, ActorID: e.ActorID, ActorType: e.ActorType,
		Purpose: e.Purpose, PolicyDecisionID: e.PolicyDecisionID,
		PolicyVersion: e.PolicyVersion, ConfigurationHash: e.ConfigurationHash,
		PayloadHash: e.PayloadHash, PreviousEventHash: e.PreviousEventHash,
		OccurredAt: e.OccurredAt, RecordedAt: e.RecordedAt,
	}
	canon, err := jcs.Canonicalize(v)
	if err != nil {
		return "", fmt.Errorf("event: canonicalizing envelope: %w", err)
	}
	sum := sha256.Sum256(canon)
	return hex.EncodeToString(sum[:]), nil
}

// Validate checks the envelope's structural invariants. It does not
// check the chain -- that is Chain.Append's and VerifyChain's job.
func Validate(e Envelope) error {
	switch {
	case e.EventID == "":
		return ErrEmptyEventID
	case e.TenantID == "":
		return ErrEmptyTenantID
	case e.EventType == "":
		return ErrEmptyEventType
	case e.AggregateType == "" || e.AggregateID == "":
		return ErrEmptyAggregate
	case e.ActorID == "" || e.ActorType == "":
		return ErrEmptyActor
	case e.PolicyVersion == "":
		return ErrEmptyPolicyVersion
	}
	if _, ok := FamilyOf(e.EventType); !ok {
		return fmt.Errorf("%w: %q (families: %s)", ErrUnknownFamily, e.EventType, strings.Join(families, ", "))
	}
	valid := false
	for _, at := range ActorTypes() {
		if e.ActorType == at {
			valid = true
			break
		}
	}
	if !valid {
		return fmt.Errorf("%w: %q (types: %s)", ErrUnknownActorType, e.ActorType, strings.Join(ActorTypes(), ", "))
	}
	return nil
}

// Chain is an in-memory, append-only sequence of envelopes with hash
// linkage. It is a CONSTRUCTION AND VERIFICATION helper, not a store:
// durability belongs to the existing WAL and audit ledger.
type Chain struct {
	events []Envelope
}

// NewChain returns an empty chain.
func NewChain() *Chain { return &Chain{} }

// Len reports how many events the chain holds.
func (c *Chain) Len() int { return len(c.events) }

// Head returns the last event's hash, or GenesisHash when empty.
func (c *Chain) Head() string {
	if len(c.events) == 0 {
		return GenesisHash
	}
	return c.events[len(c.events)-1].EventHash
}

// Append validates the envelope, binds it to the chain head, computes
// its hashes, and appends it. The caller supplies the payload; its
// hash is computed here rather than trusted, so a caller cannot record
// a payload hash that does not match the payload.
//
// SequenceNo and PreviousEventHash are ASSIGNED by the chain, not read
// from the caller's envelope. A caller who could choose its own
// sequence number or predecessor could fork the chain silently.
func (c *Chain) Append(e Envelope, payload any) (Envelope, error) {
	ph, err := HashPayload(payload)
	if err != nil {
		return Envelope{}, err
	}
	e.PayloadHash = ph
	e.SequenceNo = uint64(len(c.events))
	e.PreviousEventHash = c.Head()
	e.EventHash = ""

	if err := Validate(e); err != nil {
		return Envelope{}, err
	}
	h, err := ComputeEventHash(e)
	if err != nil {
		return Envelope{}, err
	}
	e.EventHash = h
	c.events = append(c.events, e)
	return e, nil
}

// Events returns a copy of the chain.
func (c *Chain) Events() []Envelope {
	out := make([]Envelope, len(c.events))
	copy(out, c.events)
	return out
}

// VerifyChain re-derives every event hash from genesis and confirms
// the linkage. It is a pure function over a slice, so an independent
// verifier can run it on an exported event log without any VERIQO
// runtime -- Article 10.
func VerifyChain(events []Envelope) error {
	prev := GenesisHash
	for i, e := range events {
		if e.SequenceNo != uint64(i) {
			return fmt.Errorf("%w: event %d carries sequence %d", ErrSequenceGap, i, e.SequenceNo)
		}
		if e.PreviousEventHash != prev {
			return fmt.Errorf("%w: event %d (%s) expects previous %q, chain has %q",
				ErrChainBroken, i, e.EventID, e.PreviousEventHash, prev)
		}
		recorded := e.EventHash
		e.EventHash = ""
		got, err := ComputeEventHash(e)
		if err != nil {
			return err
		}
		if got != recorded {
			return fmt.Errorf("%w: event %d (%s)", ErrHashMismatch, i, e.EventID)
		}
		prev = recorded
	}
	return nil
}

// VerifyPayload re-derives an event's payload hash from the payload
// the caller believes it recorded. This is the check that catches a
// payload swapped after the fact: the chain can be internally perfect
// while the payload no longer matches its recorded hash.
func VerifyPayload(e Envelope, payload any) error {
	got, err := HashPayload(payload)
	if err != nil {
		return err
	}
	if got != e.PayloadHash {
		return fmt.Errorf("%w: event %s", ErrPayloadMismatch, e.EventID)
	}
	return nil
}

// FamilyCounts summarizes a chain by event family -- the shape a
// Common Fact Pack's process-evidence section reports.
func FamilyCounts(events []Envelope) map[string]int {
	out := map[string]int{}
	for _, e := range events {
		if fam, ok := FamilyOf(e.EventType); ok {
			out[fam]++
		}
	}
	return out
}

// AIEvents returns every event caused by an AI actor. Article 27's
// auditability rests on this being answerable from the log alone.
func AIEvents(events []Envelope) []Envelope {
	var out []Envelope
	for _, e := range events {
		if e.ActorType == ActorAI {
			out = append(out, e)
		}
	}
	return out
}

// PolicyVersions returns the distinct policy versions a chain ran
// under, sorted. More than one across a single case is the signal
// Article 26 exists to make visible.
func PolicyVersions(events []Envelope) []string {
	seen := map[string]bool{}
	for _, e := range events {
		if e.PolicyVersion != "" {
			seen[e.PolicyVersion] = true
		}
	}
	out := make([]string, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
