// Package findings is where VERIQO's conclusions are minted.
//
// # FC-001 AUTHORITY_DIFFUSION, closed structurally
//
// The finding was that a graph package could mint a Finding. The fix
// is not a permission check at that call site -- the next package
// would have the same power. The fix is that a Finding cannot be
// brought into existence except through Mint, and Mint requires an
// approving principal who holds APPROVE and who is not the proposer.
//
// The mechanism is an unexported field. A Finding constructed as a
// struct literal anywhere else in the module has a zero mint proof,
// Minted() reports false, and every consumer refuses it. This is not a
// convention a reviewer has to notice: the Go type system enforces it,
// because an unexported field cannot be set from another package.
//
// # What a finding must name
//
//	exactly one case      it belongs to one investigation
//	exactly one claim     it concludes one thing
//	exactly one proof     the decomposition it rests on
//	one approving party   who made it binding
//
// "Exactly one" is the discipline. A finding that concluded three
// claims at once would be three findings sharing a limitations
// section, and the weakest of them would be read at the strength of
// the strongest.
package findings

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"veriqo/pkg/authority"
	"veriqo/pkg/canonical/jcs"
	"veriqo/pkg/claim"
	"veriqo/pkg/contract"
	"veriqo/pkg/identity"
	"veriqo/pkg/reverseproof"
	"veriqo/pkg/uncertainty"
)

var (
	ErrNotMinted = errors.New("findings: this value was constructed rather than minted; " +
		"only findings.Mint produces an authoritative finding")
	ErrNoApproval            = errors.New("findings: a finding requires an approving authority")
	ErrNoClaim               = errors.New("findings: a finding must name exactly one claim")
	ErrNoProof               = errors.New("findings: a finding must name the decomposition it rests on")
	ErrNoCase                = errors.New("findings: a finding must belong to exactly one case")
	ErrNoLimitations         = errors.New("findings: a finding must state what it does not establish")
	ErrClaimNotEstablished   = errors.New("findings: the claim this finding would conclude is not established")
	ErrProofRefuted          = errors.New("findings: the decomposition refutes the claim")
	ErrMissingQualifications = errors.New("findings: the confidence vector's qualifications " +
		"are not carried in the finding's limitations")
)

// mintProof is the unexported witness that a Finding went through
// Mint. Its zero value is what makes a struct literal from another
// package inert.
type mintProof struct {
	approver contract.ID
	at       time.Time
	digest   string
}

// Finding is a concluded, approved statement.
type Finding struct {
	ID       contract.ID `json:"id"`
	TenantID string      `json:"tenant_id"`
	CaseID   string      `json:"case_id"`

	ClaimID contract.ID `json:"claim_id"`
	ProofID contract.ID `json:"proof_id"`

	Statement string      `json:"statement"`
	Scope     claim.Scope `json:"scope"`

	// Confidence is the nine-dimensional vector, not a number.
	Confidence uncertainty.Vector `json:"confidence"`

	// Limitations must include every qualification the confidence
	// vector produces. A finding that states a strong conclusion and
	// drops the vector's caveats is the overclaim in one step.
	Limitations []string `json:"limitations"`

	// AlternativeExplanations names what else would produce this
	// evidence. Required: a finding with none has not been tested
	// against the world.
	AlternativeExplanations []string `json:"alternative_explanations"`

	EvidenceRefs []string `json:"evidence_refs"`

	ProposedBy contract.ID `json:"proposed_by"`
	ApprovedBy contract.ID `json:"approved_by"`
	ApprovedAt time.Time   `json:"approved_at"`

	Versions contract.VersionSet `json:"versions"`

	// ReplayReference is how somebody re-derives this finding.
	ReplayReference string `json:"replay_reference"`

	mint mintProof
}

// Minted reports whether this value came from Mint.
//
// Every consumer of a Finding calls this. A value constructed as a
// literal elsewhere -- by a graph package, a domain package, a test --
// answers false, and that is FC-001 closed by construction rather than
// by review.
func (f Finding) Minted() bool {
	return f.mint.approver != "" && !f.mint.at.IsZero() && f.mint.digest != ""
}

// MintRequest is what Mint needs.
type MintRequest struct {
	ID     contract.ID
	CaseID string

	Claim claim.Claim
	Proof *reverseproof.Proof

	Confidence  uncertainty.Vector
	Limitations []string

	Proposer identity.Principal
	Approver identity.Principal
	// ApproverGrant is the grant under which the approval is made.
	ApproverGrant authority.Grant

	At time.Time
}

// Mint is the ONLY way to create an authoritative Finding.
//
// It layers the checks in the order a reviewer would: who is
// approving, whether they may, whether the material supports a
// conclusion at all, and whether the conclusion carries its limits.
func Mint(req MintRequest) (Finding, error) {
	if req.ID == "" {
		return Finding{}, fmt.Errorf("%w: finding has no id", contract.ErrMalformedID)
	}
	if err := req.ID.Validate(); err != nil {
		return Finding{}, err
	}
	if strings.TrimSpace(req.CaseID) == "" {
		return Finding{}, ErrNoCase
	}
	if req.At.IsZero() {
		return Finding{}, errors.New("findings: no approval instant; the finding could not be replayed")
	}

	// --- authority ---------------------------------------------------
	if err := authority.Check(req.Approver, req.ApproverGrant, authority.Approve); err != nil {
		return Finding{}, fmt.Errorf("%w: %v", ErrNoApproval, err)
	}
	if err := authority.CheckSeparation(req.Proposer, req.Approver); err != nil {
		return Finding{}, fmt.Errorf("%w: %v", ErrNoApproval, err)
	}
	if err := req.Approver.Active(req.At); err != nil {
		return Finding{}, fmt.Errorf("%w: %v", ErrNoApproval, err)
	}
	if req.Proposer.TenantID != req.Approver.TenantID {
		return Finding{}, fmt.Errorf("%w: proposer and approver are in different tenants",
			contract.ErrCrossTenant)
	}

	// --- the material ------------------------------------------------
	if err := req.Claim.Validate(); err != nil {
		return Finding{}, fmt.Errorf("%w: %v", ErrNoClaim, err)
	}
	if req.Claim.TenantID != req.Approver.TenantID {
		return Finding{}, fmt.Errorf("%w: the claim is in %s", contract.ErrCrossTenant, req.Claim.TenantID)
	}
	if req.Claim.CaseID != req.CaseID {
		return Finding{}, fmt.Errorf("%w: the claim belongs to case %s, the finding to %s",
			ErrNoCase, req.Claim.CaseID, req.CaseID)
	}
	if !req.Claim.Status.Establishes() {
		return Finding{}, fmt.Errorf("%w: %s is %s", ErrClaimNotEstablished, req.Claim.ID, req.Claim.Status)
	}
	if req.Proof == nil {
		return Finding{}, ErrNoProof
	}
	if req.Proof.ClaimID != req.Claim.ID {
		return Finding{}, fmt.Errorf("%w: %s decomposes %s, not %s",
			ErrNoProof, req.Proof.ID, req.Proof.ClaimID, req.Claim.ID)
	}
	if v := req.Proof.Verdict(); v == reverseproof.VerdictRefuted {
		return Finding{}, fmt.Errorf("%w: %s", ErrProofRefuted, req.Proof.ID)
	}

	// --- the limits --------------------------------------------------
	if err := req.Confidence.Validate(); err != nil {
		return Finding{}, err
	}
	limits := append([]string(nil), req.Limitations...)
	limits = append(limits, req.Claim.Limitations...)

	// The confidence vector's qualifications MUST be carried. A strong
	// conclusion that drops them is the overclaim in one step.
	for _, q := range req.Confidence.Qualifications() {
		limits = appendUnique(limits, q)
	}
	// The decomposition's own boundary travels too.
	if req.Proof.Completeness() < 1 {
		limits = appendUnique(limits, fmt.Sprintf(
			"the reverse proof is %.0f%% complete: %d necessary condition(s) were not examined",
			req.Proof.Completeness()*100, len(unexamined(req.Proof))))
	}
	for _, a := range req.Proof.Assumptions() {
		limits = appendUnique(limits, fmt.Sprintf(
			"condition %s (%s) is ASSUMED, not evidenced: %s", a.ID, a.Must, a.Note))
	}
	if len(limits) == 0 {
		return Finding{}, ErrNoLimitations
	}
	sort.Strings(limits)

	alternatives := mergeStrings(req.Claim.AlternativeHypotheses, req.Proof.Alternatives)
	if len(alternatives) == 0 {
		return Finding{}, errors.New("findings: a finding must name what else would produce " +
			"this evidence; one that names nothing has not been tested against the world")
	}

	f := Finding{
		ID: req.ID, TenantID: req.Approver.TenantID, CaseID: req.CaseID,
		ClaimID: req.Claim.ID, ProofID: req.Proof.ID,
		Statement: req.Claim.Statement, Scope: req.Claim.Scope,
		Confidence:  req.Confidence,
		Limitations: limits, AlternativeExplanations: alternatives,
		EvidenceRefs: sortedUnique(req.Claim.SupportingEvidence),
		ProposedBy:   req.Proposer.ID, ApprovedBy: req.Approver.ID, ApprovedAt: req.At,
		Versions: req.Claim.Versions,
		ReplayReference: fmt.Sprintf("finding/%s/claim/%s/proof/%s@%s",
			req.ID, req.Claim.ID, req.Proof.ID, req.At.UTC().Format(time.RFC3339)),
	}
	digest, err := jcs.Hash(publicView(f))
	if err != nil {
		return Finding{}, err
	}
	f.mint = mintProof{approver: req.Approver.ID, at: req.At, digest: digest}
	return f, nil
}

// publicView is what the digest covers: everything except the mint
// witness itself.
func publicView(f Finding) any {
	f.mint = mintProof{}
	return f
}

// Digest returns the finding's content hash. It refuses an unminted
// value rather than hashing it, so a fabricated finding cannot acquire
// a digest that looks authoritative.
func (f Finding) Digest() (string, error) {
	if !f.Minted() {
		return "", ErrNotMinted
	}
	return f.mint.digest, nil
}

// Verify re-derives the digest and checks it, which detects a finding
// whose fields were edited after minting.
func (f Finding) Verify() error {
	if !f.Minted() {
		return ErrNotMinted
	}
	want, err := jcs.Hash(publicView(f))
	if err != nil {
		return err
	}
	if want != f.mint.digest {
		return fmt.Errorf("findings: %s was edited after it was minted", f.ID)
	}
	if f.mint.approver != f.ApprovedBy {
		return fmt.Errorf("findings: %s records approver %s and was minted by %s",
			f.ID, f.ApprovedBy, f.mint.approver)
	}
	return nil
}

// Require is the guard every consumer calls before using a finding.
func Require(f Finding) error {
	if !f.Minted() {
		return fmt.Errorf("%w: %s", ErrNotMinted, f.ID)
	}
	return f.Verify()
}

// Report renders the finding with its limits and alternatives, which
// is the form a customer receives.
func (f Finding) Report() string {
	var b strings.Builder
	fmt.Fprintf(&b, "FINDING %s\n", f.ID)
	fmt.Fprintf(&b, "  %s\n", f.Statement)
	fmt.Fprintf(&b, "  scope: %s\n", f.Scope)
	w, l := f.Confidence.Weakest()
	fmt.Fprintf(&b, "  weakest confidence dimension: %s (%s)\n", w, l)
	b.WriteString("  limitations:\n")
	for _, lim := range f.Limitations {
		fmt.Fprintf(&b, "    - %s\n", lim)
	}
	b.WriteString("  alternative explanations considered:\n")
	for _, a := range f.AlternativeExplanations {
		fmt.Fprintf(&b, "    - %s\n", a)
	}
	fmt.Fprintf(&b, "  proposed by %s, approved by %s at %s\n",
		f.ProposedBy, f.ApprovedBy, f.ApprovedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "  replay: %s\n", f.ReplayReference)
	return b.String()
}

func unexamined(p *reverseproof.Proof) []reverseproof.Condition {
	_, un := p.Missing()
	return un
}

func appendUnique(xs []string, v string) []string {
	for _, x := range xs {
		if x == v {
			return xs
		}
	}
	return append(xs, v)
}

func mergeStrings(a, b []string) []string {
	out := append([]string(nil), a...)
	for _, v := range b {
		out = appendUnique(out, v)
	}
	sort.Strings(out)
	return out
}

func sortedUnique(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}
