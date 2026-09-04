package proof

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"veriqo/pkg/canonical/jcs"
	"veriqo/pkg/qualification/nextbest"
)

// The pipeline from a proposition to a decision:
//
//	PROPOSITION
//	     -> PROOF OBJECT
//	          -> SUPPORT | CONTRADICT | UNKNOWN
//	               -> QUALIFICATION
//	                    -> SUFFICIENT              -> FINDING
//	                    -> INSUFFICIENT            -> NEXT BEST EVIDENCE
//	                                                    |
//	                                               AUTHORIZED
//	                                                    |
//	                                                DECISION
//
// Each arrow in that diagram is a function in this file, and each stage
// is a distinct type whose fields are unexported. The types are the
// enforcement: a Decision cannot be constructed from a Finding, only
// from an AuthorizedFinding, and an AuthorizedFinding cannot be
// constructed from anything but a Finding that a real authority signed
// off. Skipping a stage is not a policy violation to be detected after
// the fact — it is code that does not compile.

var (
	ErrNotSealed          = errors.New("proof: the object has not been sealed")
	ErrInsufficient       = errors.New("proof: an insufficient proof object cannot found a finding")
	ErrNoAuthorizer       = errors.New("proof: authorization requires a named authorizer")
	ErrAuthorizerIsAuthor = errors.New("proof: the authorizer may not be the party that generated the proof object")
	ErrZeroFinding        = errors.New("proof: a zero finding cannot be authorized")
	ErrZeroAuthorized     = errors.New("proof: a zero authorized finding cannot become a decision")
	ErrNoDecisionAction   = errors.New("proof: a decision requires an action")
	ErrAdjudication       = errors.New("proof: VERIQO does not adjudicate: a decision may not name a prevailing party")

	// The finding identity invariants. A finding that fails any of
	// these is not a weaker finding; it is not a finding.
	ErrFindingWithoutCase        = errors.New("proof: a finding must name exactly one case")
	ErrFindingWithoutProposition = errors.New("proof: a finding must name exactly one proposition")
	ErrFindingWithoutProof       = errors.New("proof: a finding must name exactly one proof object")
	ErrFindingAuthorityPath      = errors.New("proof: a finding carries an authority path other than the one constructor that may produce it")
	ErrFindingTampered           = errors.New("proof: a finding's contents no longer agree with its hash")
)

// --- Finding ---------------------------------------------------------

// Finding is a conclusion VERIQO is prepared to stand behind. It exists
// only where a sealed Proof Object was found sufficient.
//
// Every field is unexported and every accessor returns a copy. A Finding
// obtained by struct literal is the zero value, and the zero value
// cannot be authorized.
type Finding struct {
	proofHash     string
	propositionID string
	statement     string
	caseID        string
	stance        Stance
	qualification string
	limitations   []string
	atTick        uint64
	authorityPath string
	hash          string
}

// FindingAuthorityPath is the only path by which a finding can come
// into existence: a proof object sealed by proof.Seal, found sufficient
// by the sufficiency rule, and derived by proof.NewFinding.
//
// It is recorded on the finding and covered by the finding's hash so
// that the answer to "who was allowed to say this?" travels with the
// object rather than living in a document about the object. A finding
// whose authority path is anything else does not exist, because
// NewFinding is the only constructor and it writes this constant.
const FindingAuthorityPath = "proof.Seal -> proof.deriveSufficiency -> proof.NewFinding"

// NewFinding derives a finding from a sealed, sufficient proof object.
//
// It re-verifies the canonical hash first: a proof object that was
// altered after sealing cannot found a finding, even if its Sufficiency
// field still reads SUFFICIENT.
func NewFinding(o Object, atTick uint64) (Finding, error) {
	if strings.TrimSpace(o.CanonicalHash) == "" {
		return Finding{}, ErrNotSealed
	}
	if err := VerifyHash(o); err != nil {
		return Finding{}, err
	}
	if o.Sufficiency != Sufficient {
		return Finding{}, fmt.Errorf("%w: sufficiency is %s", ErrInsufficient, o.Sufficiency)
	}
	// Exactly one case and exactly one proposition. A finding that
	// names no case is not attributable to anything: it could be
	// attached to any case later, which is the same as belonging to
	// none. A finding that names no proposition states nothing.
	if strings.TrimSpace(o.Scope.CaseID) == "" {
		return Finding{}, ErrFindingWithoutCase
	}
	if strings.TrimSpace(o.Proposition.ID) == "" {
		return Finding{}, ErrFindingWithoutProposition
	}

	f := Finding{
		proofHash:     o.CanonicalHash,
		propositionID: o.Proposition.ID,
		statement:     o.Proposition.Statement,
		caseID:        o.Scope.CaseID,
		stance:        o.Stance,
		qualification: string(o.Qualification.State),
		limitations:   sortedCopy(o.Limitations),
		atTick:        atTick,
		authorityPath: FindingAuthorityPath,
	}
	h, err := findingHash(f)
	if err != nil {
		return Finding{}, err
	}
	f.hash = h
	return f, nil
}

// findingHash computes a finding's canonical hash over every field that
// carries meaning, the authority path included. Keeping this in one
// function is what lets VerifyIntegrity re-derive the same value: two
// hash expressions that must agree are a duplicate authority on what a
// finding is.
func findingHash(f Finding) (string, error) {
	h, err := jcs.Hash(map[string]any{
		"proof_hash": f.proofHash, "proposition_id": f.propositionID,
		"statement": f.statement, "case_id": f.caseID,
		"stance": f.stance.String(), "qualification": f.qualification,
		"limitations": toAny(f.limitations), "tick": f.atTick,
		"authority_path": f.authorityPath,
	})
	if err != nil {
		return "", fmt.Errorf("proof: finding hash: %w", err)
	}
	return h, nil
}

// VerifyIntegrity re-derives the finding's hash from its own contents
// and reports whether it still agrees.
//
// Within a single process the unexported fields already make a finding
// unforgeable. This exists for findings that have crossed a boundary --
// deserialized from a snapshot, replayed from a ledger, reconstructed by
// a future package -- where the type system's guarantee did not travel
// with the bytes.
func (f Finding) VerifyIntegrity() error {
	if f.IsZero() {
		return ErrZeroFinding
	}
	if f.authorityPath != FindingAuthorityPath {
		return fmt.Errorf("%w: authority path is %q, not %q",
			ErrFindingAuthorityPath, f.authorityPath, FindingAuthorityPath)
	}
	if strings.TrimSpace(f.caseID) == "" {
		return ErrFindingWithoutCase
	}
	if strings.TrimSpace(f.propositionID) == "" {
		return ErrFindingWithoutProposition
	}
	if strings.TrimSpace(f.proofHash) == "" {
		return ErrFindingWithoutProof
	}
	want, err := findingHash(f)
	if err != nil {
		return err
	}
	if want != f.hash {
		return fmt.Errorf("%w: recorded %s, recomputed %s", ErrFindingTampered, f.hash, want)
	}
	return nil
}

func (f Finding) IsZero() bool          { return f.hash == "" }
func (f Finding) ProofHash() string     { return f.proofHash }
func (f Finding) PropositionID() string { return f.propositionID }
func (f Finding) Statement() string     { return f.statement }
func (f Finding) CaseID() string        { return f.caseID }
func (f Finding) Stance() Stance        { return f.stance }
func (f Finding) Qualification() string { return f.qualification }
func (f Finding) Hash() string          { return f.hash }
func (f Finding) AtTick() uint64        { return f.atTick }

// AuthorityPath returns the chain that was permitted to produce this
// finding. It answers "who was allowed to say this?", which is a
// different question from where the underlying evidence came from.
func (f Finding) AuthorityPath() string { return f.authorityPath }

// Limitations returns a copy. A finding's stated limits travel with it
// and cannot be edited off by a caller holding the value.
func (f Finding) Limitations() []string { return append([]string(nil), f.limitations...) }

// --- Next best evidence ----------------------------------------------

// Direction is what an insufficient proof object produces instead of a
// finding: the ranked, rights-filtered set of things worth obtaining
// next, plus the reason the object fell short.
type Direction struct {
	ProofHash string
	// Reason states, in the object's own terms, why it was insufficient.
	Reason string
	// Ranking is the next-best-evidence ranking. Candidates excluded by
	// a hard rights or authority gate appear in Ranking.Excluded, never
	// as a low-scored entry: a step VERIQO is not permitted to take is
	// not a cheap step, it is not a step.
	Ranking nextbest.Ranking
}

// NextBest builds the direction for an insufficient proof object.
//
// It refuses a sufficient one. Producing "what to do next" for a
// conclusion that is already founded would invite treating next-best
// evidence as optional enrichment, when its role is to say what is
// missing.
func NextBest(o Object, candidates []nextbest.Candidate) (Direction, error) {
	if strings.TrimSpace(o.CanonicalHash) == "" {
		return Direction{}, ErrNotSealed
	}
	if o.Sufficiency == Sufficient {
		return Direction{}, errors.New("proof: a sufficient proof object does not need next-best evidence")
	}
	r, err := nextbest.Rank(candidates)
	if err != nil {
		return Direction{}, err
	}
	return Direction{ProofHash: o.CanonicalHash, Reason: InsufficiencyReason(o), Ranking: r}, nil
}

// InsufficiencyReason states why an object is not sufficient, in the
// order the sufficiency test applies. It returns an empty string for a
// sufficient object.
func InsufficiencyReason(o Object) string {
	if o.Sufficiency == Sufficient {
		return ""
	}
	switch {
	case o.Stance != Support:
		return fmt.Sprintf("stance is %s: the qualified evidence does not support the proposition", o.Stance)
	case len(o.MaterialContradictions()) > 0:
		ids := make([]string, 0, len(o.MaterialContradictions()))
		for _, c := range o.MaterialContradictions() {
			ids = append(ids, c.ID)
		}
		sort.Strings(ids)
		return "unresolved material contradictions: " + strings.Join(ids, ", ")
	case !o.Trust.Assessed:
		return "source trust was never assessed"
	case o.Trust.EffectiveSourceCount < 1:
		return "no effective independent source after clustering"
	case !o.Quality.Assessed:
		return "evidence quality was never assessed"
	case o.AIAccess.MaterialContribution && strings.TrimSpace(o.AIAccess.HumanReviewerID) == "":
		return "a material AI contribution has no human reviewer"
	case !o.ReverseProofGap.Complete:
		if o.ReverseProofGap.Reason != "" {
			return "reverse proof incomplete: " + o.ReverseProofGap.Reason
		}
		return "reverse proof incomplete"
	default:
		return "sufficiency was not determined"
	}
}

// --- Authorization ---------------------------------------------------

// AuthorizedFinding is a Finding an entitled authority has adopted.
//
// The separation matters: VERIQO producing a finding is an epistemic
// act, and a human authority adopting it is a governance act. Collapsing
// them would let the system authorize itself.
type AuthorizedFinding struct {
	finding        Finding
	authorizerID   string
	authorizerRole string
	policyVersion  string
	rationale      string
	atTick         uint64
	hash           string
}

// Authorize adopts a finding.
//
// The authorizer may not be the party recorded as generating the proof
// object. That is not ceremony: a pipeline that both produces and adopts
// its own conclusions has no authority boundary at all, and the check is
// the boundary.
func Authorize(f Finding, o Object, authorizerID, authorizerRole, policyVersion, rationale string, atTick uint64) (AuthorizedFinding, error) {
	if f.IsZero() {
		return AuthorizedFinding{}, ErrZeroFinding
	}
	if strings.TrimSpace(authorizerID) == "" || strings.TrimSpace(policyVersion) == "" {
		return AuthorizedFinding{}, ErrNoAuthorizer
	}
	if f.proofHash != o.CanonicalHash {
		return AuthorizedFinding{}, errors.New("proof: the finding does not belong to this proof object")
	}
	if authorizerID == o.Provenance.GeneratedBy {
		return AuthorizedFinding{}, ErrAuthorizerIsAuthor
	}

	a := AuthorizedFinding{
		finding: f, authorizerID: authorizerID, authorizerRole: authorizerRole,
		policyVersion: policyVersion, rationale: rationale, atTick: atTick,
	}
	h, err := jcs.Hash(map[string]any{
		"finding_hash": f.hash, "authorizer": authorizerID, "role": authorizerRole,
		"policy_version": policyVersion, "rationale": rationale, "tick": atTick,
	})
	if err != nil {
		return AuthorizedFinding{}, fmt.Errorf("proof: authorization hash: %w", err)
	}
	a.hash = h
	return a, nil
}

func (a AuthorizedFinding) IsZero() bool           { return a.hash == "" }
func (a AuthorizedFinding) Finding() Finding       { return a.finding }
func (a AuthorizedFinding) AuthorizerID() string   { return a.authorizerID }
func (a AuthorizedFinding) AuthorizerRole() string { return a.authorizerRole }
func (a AuthorizedFinding) PolicyVersion() string  { return a.policyVersion }
func (a AuthorizedFinding) Rationale() string      { return a.rationale }
func (a AuthorizedFinding) Hash() string           { return a.hash }
func (a AuthorizedFinding) AtTick() uint64         { return a.atTick }

// --- Decision --------------------------------------------------------

// prohibitedDecisionFields are the words that would turn a VERIQO
// decision into an adjudication. They are checked as data rather than
// left to editorial discipline, because the boundary is the product.
var prohibitedDecisionFields = []string{
	"prevailing_party", "winner", "liable_party", "at_fault",
	"award", "judgment", "verdict", "ruling",
}

// ProhibitedDecisionFields returns the adjudicatory fields a VERIQO
// decision may never carry. Exported so tests and reviewers can assert
// against the same list the constructor enforces.
func ProhibitedDecisionFields() []string {
	return append([]string(nil), prohibitedDecisionFields...)
}

// Decision is an action VERIQO takes on the strength of an authorized
// finding. It is an operational act — pay, refuse, escalate, refer,
// preserve — never a determination of who wins.
//
// VERIQO supports dispute resolution by furnishing facts, evidence,
// timelines, contradictions, causation hypotheses, quantum and proof
// obligations. The arbitrator, court or authorized decision-maker
// decides. See docs/architecture/DISPUTE_EVIDENCE_SUPPORT.md.
type Decision struct {
	authorized AuthorizedFinding
	action     string
	rationale  string
	attributes map[string]string
	atTick     uint64
	hash       string
}

// Decide produces a decision from an authorized finding.
//
// Attributes carrying an adjudicatory field are refused outright. A
// caller that wants to record who should win is asking VERIQO to be the
// tribunal, and this constructor is where that request is denied.
func Decide(a AuthorizedFinding, action, rationale string, attributes map[string]string, atTick uint64) (Decision, error) {
	if a.IsZero() {
		return Decision{}, ErrZeroAuthorized
	}
	if strings.TrimSpace(action) == "" {
		return Decision{}, ErrNoDecisionAction
	}
	for k := range attributes {
		lower := strings.ToLower(strings.TrimSpace(k))
		for _, banned := range prohibitedDecisionFields {
			if lower == banned {
				return Decision{}, fmt.Errorf("%w: attribute %q", ErrAdjudication, k)
			}
		}
	}

	attrs := make(map[string]string, len(attributes))
	keys := make([]string, 0, len(attributes))
	for k, v := range attributes {
		attrs[k] = v
		keys = append(keys, k)
	}
	sort.Strings(keys)
	canonAttrs := make(map[string]any, len(keys))
	for _, k := range keys {
		canonAttrs[k] = attrs[k]
	}

	d := Decision{authorized: a, action: action, rationale: rationale, attributes: attrs, atTick: atTick}
	h, err := jcs.Hash(map[string]any{
		"authorized_hash": a.hash, "action": action, "rationale": rationale,
		"attributes": canonAttrs, "tick": atTick,
	})
	if err != nil {
		return Decision{}, fmt.Errorf("proof: decision hash: %w", err)
	}
	d.hash = h
	return d, nil
}

func (d Decision) IsZero() bool                  { return d.hash == "" }
func (d Decision) Authorized() AuthorizedFinding { return d.authorized }
func (d Decision) Action() string                { return d.action }
func (d Decision) Rationale() string             { return d.rationale }
func (d Decision) Hash() string                  { return d.hash }
func (d Decision) AtTick() uint64                { return d.atTick }

// Attributes returns a copy, so a caller holding a Decision cannot add
// an adjudicatory attribute after the constructor refused one.
func (d Decision) Attributes() map[string]string {
	out := make(map[string]string, len(d.attributes))
	for k, v := range d.attributes {
		out[k] = v
	}
	return out
}

// Lineage walks a decision back to the proof object hash it rests on.
// A decision that cannot produce this chain is not traceable, and
// nothing in this package can produce such a decision.
func (d Decision) Lineage() (decisionHash, authorizedHash, findingHash, proofHash string) {
	return d.hash, d.authorized.hash, d.authorized.finding.hash, d.authorized.finding.proofHash
}
