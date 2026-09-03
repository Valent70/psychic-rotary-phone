package proof

import (
	"errors"
	"strings"
	"testing"

	"veriqo/pkg/qualification/nextbest"
	"veriqo/pkg/qualification/reverseproof"
	"veriqo/pkg/qualification/state"
)

// completeSet builds a reverse-proof set whose every requirement is
// obtained and whose alternatives are tested, so Analyze reports it
// complete. Tests that want an incomplete one degrade this.
func completeSet(t *testing.T) (reverseproof.RequirementSet, reverseproof.Gap) {
	t.Helper()
	claim := reverseproof.Claim{
		ID: "C-1", Description: "the cargo was contaminated before loading",
		Conditions: []reverseproof.Condition{{ID: "cond-1", Description: "pre-loading contamination"}},
	}
	reqs := []reverseproof.Requirement{{
		ID: "R-1", ConditionID: "cond-1", Description: "pre-load sample analysis",
		ExpectedIfTrue:     "contaminant present in pre-load sample",
		ContradictsIfShows: "clean pre-load sample",
		Status:             reverseproof.Obtained, DiagnosticValue: 0.9,
	}}
	alts := []reverseproof.AlternativeHypothesis{{
		ID: "A-1", Description: "contamination occurred in transit", Tested: true,
	}}
	rs, err := reverseproof.Build(claim, reqs, alts, 10)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	gap := reverseproof.Analyze(rs, map[string]bool{"cond-1": true})
	if !gap.Complete {
		t.Fatalf("fixture should be complete, got %+v", gap)
	}
	return rs, gap
}

// good builds a proof object that ought to seal as SUPPORT/SUFFICIENT.
func good(t *testing.T) Object {
	t.Helper()
	rs, gap := completeSet(t)
	q, err := state.New("C-1", state.Supported, "policy-v1", "qualified on the pre-load sample", nil, 10)
	if err != nil {
		t.Fatalf("state.New: %v", err)
	}
	return Object{
		Proposition: Proposition{ID: "P-1", Statement: "the cargo was contaminated before loading",
			SubjectType: "Cargo", SubjectID: "CARGO-9"},
		Scope:        Scope{CaseID: "CASE-1", Matter: "cargo damage claim"},
		Jurisdiction: Jurisdiction{Code: "SG", Forum: "SIAC", GoverningLaw: "English law"},
		TimeWindow:   TimeWindow{FromTick: 1, ToTick: 100},
		EvidenceSet: []EvidenceRef{
			{EvidenceID: "E-1", EvidenceVersionID: "EV-1-v1", SHA256: "abc123", SourceID: "lab-a"},
		},
		Quality:         Quality{Assessed: true, Grade: "primary"},
		ReverseProof:    rs,
		ReverseProofGap: gap,
		Trust:           TrustAssessment{Assessed: true, EffectiveSourceCount: 2},
		Qualification:   q,
		Authority:       Authority{AuthorityID: "auth-1", Role: "senior-analyst", PolicyVersion: "policy-v1"},
		Disclosure:      DisclosureState{Procedural: 2, Content: 3, Privilege: "NOT_CLAIMED"},
		Limitations:     []string{"the analysis covers the sampled parcel only"},
		Provenance: Provenance{GeneratedBy: "pipeline-1", GeneratedAtTick: 10,
			PipelineVersion: "fref-v1", InputHashes: []string{"abc123"}},
		ReplayReference: "REPLAY-1",
	}
}

func TestSealDerivesSupportAndSufficiency(t *testing.T) {
	o, err := Seal(good(t))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if o.Stance != Support {
		t.Fatalf("expected SUPPORT, got %s", o.Stance)
	}
	if o.Sufficiency != Sufficient {
		t.Fatalf("expected SUFFICIENT, got %s (%s)", o.Sufficiency, InsufficiencyReason(o))
	}
	if o.CanonicalHash == "" {
		t.Fatal("Seal must produce a canonical hash")
	}
}

// TestZeroStanceIsUnknown is the load-bearing default: a proof object
// that never established a stance says so.
func TestZeroStanceIsUnknown(t *testing.T) {
	var s Stance
	if s != Unknown || s.String() != "UNKNOWN" {
		t.Fatalf("the zero Stance must be UNKNOWN, got %s", s)
	}
	var suf Sufficiency
	if suf != NotDetermined {
		t.Fatalf("the zero Sufficiency must be NOT_DETERMINED, got %s", suf)
	}
	var e ExternalStatus
	if e != NotSought || e.Satisfied() {
		t.Fatal("the zero ExternalStatus must be NOT_SOUGHT and unsatisfied")
	}
}

// TestAuthorDeclaredSufficiencyIsOverridden proves an author cannot make
// a conclusion sufficient by writing the word.
func TestAuthorDeclaredSufficiencyIsOverridden(t *testing.T) {
	o := good(t)
	o.Trust.Assessed = false // genuinely insufficient
	o.Sufficiency = Sufficient
	o.Stance = Support

	sealed, err := Seal(o)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if sealed.Sufficiency != Insufficient {
		t.Fatal("a declared sufficiency must be overridden by the derived one")
	}
	if !strings.Contains(InsufficiencyReason(sealed), "trust") {
		t.Fatalf("the reason should name the trust gap, got %q", InsufficiencyReason(sealed))
	}
}

// TestUnresolvedMaterialContradictionDefeatsSufficiency: a conclusion
// standing over an unresolved conflict is not founded.
func TestUnresolvedMaterialContradictionDefeatsSufficiency(t *testing.T) {
	o := good(t)
	o.Contradictions = []Contradiction{
		{ID: "X-1", Between: []string{"EV-1-v1", "EV-2-v1"}, Description: "conflicting sample results", Material: true},
	}
	sealed, _ := Seal(o)
	if sealed.Sufficiency != Insufficient {
		t.Fatal("an unresolved material contradiction must defeat sufficiency")
	}

	// Resolving it by an authorized act restores sufficiency.
	o.Contradictions[0].Resolved = true
	o.Contradictions[0].Resolution = "second sample superseded on chain-of-custody grounds"
	sealed2, _ := Seal(o)
	if sealed2.Sufficiency != Sufficient {
		t.Fatalf("a resolved contradiction should not block: %s", InsufficiencyReason(sealed2))
	}
}

// TestImmaterialContradictionDoesNotDefeat proves the material flag is
// doing real work rather than blocking everything.
func TestImmaterialContradictionDoesNotDefeat(t *testing.T) {
	o := good(t)
	o.Contradictions = []Contradiction{{ID: "X-2", Description: "a typo in a covering letter", Material: false}}
	sealed, _ := Seal(o)
	if sealed.Sufficiency != Sufficient {
		t.Fatalf("an immaterial contradiction must not defeat sufficiency: %s", InsufficiencyReason(sealed))
	}
}

// TestIncompleteReverseProofDefeatsSufficiency: an untested rival
// explanation means the proof was never really attempted.
func TestIncompleteReverseProofDefeatsSufficiency(t *testing.T) {
	o := good(t)
	o.ReverseProofGap.Complete = false
	o.ReverseProofGap.Reason = "alternative A-1 untested"
	sealed, _ := Seal(o)
	if sealed.Sufficiency != Insufficient {
		t.Fatal("an incomplete reverse proof must defeat sufficiency")
	}
	if !strings.Contains(InsufficiencyReason(sealed), "reverse proof") {
		t.Fatalf("the reason should name the reverse proof, got %q", InsufficiencyReason(sealed))
	}
}

// TestContradictedQualificationYieldsContradictStance covers the stance
// deriving from the qualification rather than from the author.
func TestContradictedQualificationYieldsContradictStance(t *testing.T) {
	o := good(t)
	q, err := state.New("C-1", state.Contradicted, "policy-v1", "the pre-load sample was clean", nil, 10)
	if err != nil {
		t.Fatalf("state.New: %v", err)
	}
	o.Qualification = q
	sealed, _ := Seal(o)
	if sealed.Stance != Contradict {
		t.Fatalf("expected CONTRADICT, got %s", sealed.Stance)
	}
	if sealed.Sufficiency != Insufficient {
		t.Fatal("a contradicted proposition cannot found a finding")
	}
}

// TestInconclusiveYieldsUnknownNotSupport is the honest-default case.
func TestInconclusiveYieldsUnknownNotSupport(t *testing.T) {
	for _, st := range []state.State{state.Inconclusive, state.InsufficientEvidence, state.NotObservable, state.NotCollectable} {
		o := good(t)
		q, err := state.New("C-1", st, "policy-v1", "rationale", nil, 10)
		if err != nil {
			t.Fatalf("state.New(%s): %v", st, err)
		}
		o.Qualification = q
		sealed, _ := Seal(o)
		if sealed.Stance != Unknown {
			t.Fatalf("%s must yield UNKNOWN, got %s", st, sealed.Stance)
		}
	}
}

// --- Validation ------------------------------------------------------

func TestUnpinnedEvidenceIsRefused(t *testing.T) {
	o := good(t)
	o.EvidenceSet[0].EvidenceVersionID = ""
	if _, err := Seal(o); !errors.Is(err, ErrUnpinnedEvidence) {
		t.Fatalf("expected ErrUnpinnedEvidence, got %v", err)
	}
	o = good(t)
	o.EvidenceSet[0].SHA256 = ""
	if _, err := Seal(o); !errors.Is(err, ErrUnpinnedEvidence) {
		t.Fatalf("an evidence reference with no content hash must be refused, got %v", err)
	}
}

// TestLimitationsAreMandatory: a conclusion that states no limits is
// claiming there are none, which is never true.
func TestLimitationsAreMandatory(t *testing.T) {
	o := good(t)
	o.Limitations = nil
	if _, err := Seal(o); !errors.Is(err, ErrLimitationsRequired) {
		t.Fatalf("expected ErrLimitationsRequired, got %v", err)
	}
}

func TestReverseProofIsMandatory(t *testing.T) {
	o := good(t)
	o.ReverseProof = reverseproof.RequirementSet{}
	if _, err := Seal(o); !errors.Is(err, ErrNoReverseProof) {
		t.Fatalf("expected ErrNoReverseProof, got %v", err)
	}
}

func TestMaterialAIContributionRequiresReviewer(t *testing.T) {
	o := good(t)
	o.AIAccess = AIAccessState{ModelTouched: true, MaterialContribution: true, ContributionIDs: []string{"AC-1"}}
	if _, err := Seal(o); !errors.Is(err, ErrUnreviewedAI) {
		t.Fatalf("expected ErrUnreviewedAI, got %v", err)
	}
	o.AIAccess.HumanReviewerID = "analyst-1"
	if _, err := Seal(o); err != nil {
		t.Fatalf("a reviewed contribution should seal: %v", err)
	}
}

func TestOtherMandatoryComponents(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Object)
		want error
	}{
		{"no proposition", func(o *Object) { o.Proposition.Statement = "" }, ErrNoProposition},
		{"no scope", func(o *Object) { o.Scope.CaseID = "" }, ErrNoScope},
		{"no jurisdiction", func(o *Object) { o.Jurisdiction.Code = "" }, ErrNoJurisdiction},
		{"bad window", func(o *Object) { o.TimeWindow = TimeWindow{FromTick: 9, ToTick: 2} }, ErrBadTimeWindow},
		{"no evidence", func(o *Object) { o.EvidenceSet = nil }, ErrNoEvidence},
		{"no authority", func(o *Object) { o.Authority.AuthorityID = "" }, ErrNoAuthority},
		{"no policy version", func(o *Object) { o.Authority.PolicyVersion = "" }, ErrNoAuthority},
		{"no provenance", func(o *Object) { o.Provenance.PipelineVersion = "" }, ErrNoProvenance},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := good(t)
			tc.mut(&o)
			if _, err := Seal(o); !errors.Is(err, tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, err)
			}
		})
	}
}

// TestExternalQualificationMustNameItsQualifier stops an anonymous
// blessing being recorded as external qualification.
func TestExternalQualificationMustNameItsQualifier(t *testing.T) {
	for _, st := range []ExternalStatus{Qualified, Refused} {
		o := good(t)
		o.ExternalQualification = ExternalQualification{Status: st}
		if _, err := Seal(o); err == nil {
			t.Fatalf("%s with no QualifierID must be refused", st)
		}
	}
	o := good(t)
	o.ExternalQualification = ExternalQualification{Status: Qualified, QualifierID: "lloyds-register", Reference: "LR-99"}
	if _, err := Seal(o); err != nil {
		t.Fatalf("a named qualifier should seal: %v", err)
	}
}

// TestExternalQualificationDefaultsToNotSought is the honesty default:
// nothing is externally qualified until an outside party says so.
func TestExternalQualificationDefaultsToNotSought(t *testing.T) {
	o, _ := Seal(good(t))
	if o.ExternalQualification.Status != NotSought || o.ExternalQualification.Status.Satisfied() {
		t.Fatal("external qualification must default to NOT_SOUGHT and unsatisfied")
	}
	// A sufficient internal conclusion is still not externally qualified.
	if o.Sufficiency == Sufficient && o.ExternalQualification.Status.Satisfied() {
		t.Fatal("internal sufficiency must never imply external qualification")
	}
}

// --- Hashing ---------------------------------------------------------

func TestHashIsStableAndDetectsMutation(t *testing.T) {
	o, _ := Seal(good(t))
	again, _ := Seal(good(t))
	if o.CanonicalHash != again.CanonicalHash {
		t.Fatal("sealing identical objects must produce identical hashes")
	}
	if err := VerifyHash(o); err != nil {
		t.Fatalf("VerifyHash on an untouched object: %v", err)
	}

	for _, tc := range []struct {
		name string
		mut  func(*Object)
	}{
		{"statement", func(o *Object) { o.Proposition.Statement = "something else" }},
		{"evidence hash", func(o *Object) { o.EvidenceSet[0].SHA256 = "deadbeef" }},
		{"limitations", func(o *Object) { o.Limitations = []string{"none"} }},
		{"authority", func(o *Object) { o.Authority.AuthorityID = "impostor" }},
		{"disclosure content", func(o *Object) { o.Disclosure.Content = 5 }},
		{"replay reference", func(o *Object) { o.ReplayReference = "REPLAY-2" }},
		{"external status", func(o *Object) { o.ExternalQualification.Status = Qualified }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tampered, _ := Seal(good(t))
			tc.mut(&tampered)
			if err := VerifyHash(tampered); err == nil {
				t.Fatalf("mutating %s must break the canonical hash", tc.name)
			}
		})
	}
}

// TestSignatureIsNotHashed proves signing does not invalidate the hash
// it signs.
func TestSignatureIsNotHashed(t *testing.T) {
	o, _ := Seal(good(t))
	o.Signature = "ed25519:signature-bytes"
	if err := VerifyHash(o); err != nil {
		t.Fatalf("applying a signature must not break the hash it covers: %v", err)
	}
}

func TestUnsealedObjectFailsVerification(t *testing.T) {
	if err := VerifyHash(good(t)); err == nil {
		t.Fatal("an unsealed object must fail hash verification")
	}
}

// --- Pipeline --------------------------------------------------------

func TestSufficientObjectFoundsAFinding(t *testing.T) {
	o, _ := Seal(good(t))
	f, err := NewFinding(o, 20)
	if err != nil {
		t.Fatalf("NewFinding: %v", err)
	}
	if f.IsZero() || f.ProofHash() != o.CanonicalHash {
		t.Fatal("the finding must bind to its proof object")
	}
	if len(f.Limitations()) == 0 {
		t.Fatal("the proof object's limitations must travel with the finding")
	}
}

func TestInsufficientObjectFoundsNoFinding(t *testing.T) {
	o := good(t)
	o.Quality.Assessed = false
	sealed, _ := Seal(o)
	if _, err := NewFinding(sealed, 20); !errors.Is(err, ErrInsufficient) {
		t.Fatalf("expected ErrInsufficient, got %v", err)
	}
}

// TestTamperedObjectFoundsNoFinding: re-verification at the finding
// boundary catches an object edited after sealing, even one whose
// Sufficiency field still reads SUFFICIENT.
func TestTamperedObjectFoundsNoFinding(t *testing.T) {
	o, _ := Seal(good(t))
	o.Proposition.Statement = "the cargo was contaminated after loading"
	if _, err := NewFinding(o, 20); err == nil {
		t.Fatal("a proof object altered after sealing must not found a finding")
	}
}

func TestUnsealedObjectFoundsNoFinding(t *testing.T) {
	if _, err := NewFinding(good(t), 20); !errors.Is(err, ErrNotSealed) {
		t.Fatalf("expected ErrNotSealed, got %v", err)
	}
}

// TestZeroFindingCannotBeAuthorized closes the struct-literal bypass:
// a Finding{} built by hand carries no hash and is refused.
func TestZeroFindingCannotBeAuthorized(t *testing.T) {
	o, _ := Seal(good(t))
	if _, err := Authorize(Finding{}, o, "auth-2", "partner", "policy-v1", "adopted", 30); !errors.Is(err, ErrZeroFinding) {
		t.Fatalf("expected ErrZeroFinding, got %v", err)
	}
}

// TestAuthorizerMayNotBeTheAuthor is the authority boundary: a pipeline
// that adopts its own conclusions has no boundary at all.
func TestAuthorizerMayNotBeTheAuthor(t *testing.T) {
	o, _ := Seal(good(t))
	f, _ := NewFinding(o, 20)
	if _, err := Authorize(f, o, "pipeline-1", "service", "policy-v1", "self-adopted", 30); !errors.Is(err, ErrAuthorizerIsAuthor) {
		t.Fatalf("expected ErrAuthorizerIsAuthor, got %v", err)
	}
}

func TestFindingFromAnotherProofObjectIsRefused(t *testing.T) {
	o1, _ := Seal(good(t))
	other := good(t)
	other.Proposition.ID = "P-2"
	o2, _ := Seal(other)
	f1, _ := NewFinding(o1, 20)
	if _, err := Authorize(f1, o2, "auth-2", "partner", "policy-v1", "adopted", 30); err == nil {
		t.Fatal("authorizing a finding against a different proof object must be refused")
	}
}

func TestAuthorizationRequiresAuthorizerAndPolicy(t *testing.T) {
	o, _ := Seal(good(t))
	f, _ := NewFinding(o, 20)
	if _, err := Authorize(f, o, "", "partner", "policy-v1", "", 30); !errors.Is(err, ErrNoAuthorizer) {
		t.Fatalf("expected ErrNoAuthorizer, got %v", err)
	}
	if _, err := Authorize(f, o, "auth-2", "partner", "", "", 30); !errors.Is(err, ErrNoAuthorizer) {
		t.Fatal("authorization with no policy version must be refused")
	}
}

func TestZeroAuthorizedCannotDecide(t *testing.T) {
	if _, err := Decide(AuthorizedFinding{}, "pay", "", nil, 40); !errors.Is(err, ErrZeroAuthorized) {
		t.Fatalf("expected ErrZeroAuthorized, got %v", err)
	}
}

// TestDecisionMayNotAdjudicate is the positioning boundary made
// executable: VERIQO supports the decision-maker, it is not one.
func TestDecisionMayNotAdjudicate(t *testing.T) {
	o, _ := Seal(good(t))
	f, _ := NewFinding(o, 20)
	a, err := Authorize(f, o, "auth-2", "partner", "policy-v1", "adopted", 30)
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	for _, banned := range ProhibitedDecisionFields() {
		if _, err := Decide(a, "refer", "", map[string]string{banned: "claimant"}, 40); !errors.Is(err, ErrAdjudication) {
			t.Fatalf("attribute %q must be refused as adjudication, got %v", banned, err)
		}
	}
	// Case-insensitively, too.
	if _, err := Decide(a, "refer", "", map[string]string{"Prevailing_Party": "claimant"}, 40); !errors.Is(err, ErrAdjudication) {
		t.Fatal("the adjudication check must be case-insensitive")
	}
}

func TestFullPipelineProducesTraceableLineage(t *testing.T) {
	o, _ := Seal(good(t))
	f, _ := NewFinding(o, 20)
	a, _ := Authorize(f, o, "auth-2", "partner", "policy-v1", "adopted", 30)
	d, err := Decide(a, "refer_to_tribunal", "the evidence package is complete", map[string]string{"forum": "SIAC"}, 40)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	dh, ah, fh, ph := d.Lineage()
	if dh == "" || ah == "" || fh == "" || ph != o.CanonicalHash {
		t.Fatalf("the decision must trace back to the proof object hash, got %q/%q/%q/%q", dh, ah, fh, ph)
	}
}

// TestDecisionAttributesAreCopied stops an adjudicatory attribute being
// added after the constructor refused one.
func TestDecisionAttributesAreCopied(t *testing.T) {
	o, _ := Seal(good(t))
	f, _ := NewFinding(o, 20)
	a, _ := Authorize(f, o, "auth-2", "partner", "policy-v1", "adopted", 30)
	d, _ := Decide(a, "refer", "", map[string]string{"forum": "SIAC"}, 40)

	got := d.Attributes()
	got["winner"] = "claimant"
	if _, leaked := d.Attributes()["winner"]; leaked {
		t.Fatal("Attributes must return a copy")
	}
}

// TestFindingLimitationsAreCopied is the same defence one stage down.
func TestFindingLimitationsAreCopied(t *testing.T) {
	o, _ := Seal(good(t))
	f, _ := NewFinding(o, 20)
	f.Limitations()[0] = "no limitations"
	if f.Limitations()[0] == "no limitations" {
		t.Fatal("Limitations must return a copy")
	}
}

// --- Next best evidence ----------------------------------------------

func candidate(id string, rights bool) nextbest.Candidate {
	return nextbest.Candidate{
		ID: id, SourceID: "src-" + id, Description: "obtain " + id,
		RightsGranted: rights, AuthorityGranted: true, SourcePermitted: true, WithinCaseScope: true,
		DiagnosticValue: 0.8, Independence: 0.7, Relevance: 0.9, Freshness: 0.6,
		AcquisitionFeasibility: 0.7, Cost: 1, Latency: 1, RightsRisk: 1,
	}
}

func TestInsufficientObjectYieldsNextBestEvidence(t *testing.T) {
	o := good(t)
	o.Quality.Assessed = false
	sealed, _ := Seal(o)

	d, err := NextBest(sealed, []nextbest.Candidate{candidate("A", true), candidate("B", true)})
	if err != nil {
		t.Fatalf("NextBest: %v", err)
	}
	if d.ProofHash != sealed.CanonicalHash {
		t.Fatal("the direction must bind to its proof object")
	}
	if len(d.Ranking.Ranked) != 2 {
		t.Fatalf("expected two ranked candidates, got %d", len(d.Ranking.Ranked))
	}
	if !strings.Contains(d.Reason, "quality") {
		t.Fatalf("the reason should name the quality gap, got %q", d.Reason)
	}
}

// TestRightsDeniedCandidateIsExcludedNotDownweighted is §21: a step
// VERIQO is not permitted to take is not a cheap step.
func TestRightsDeniedCandidateIsExcludedNotDownweighted(t *testing.T) {
	o := good(t)
	o.Quality.Assessed = false
	sealed, _ := Seal(o)

	d, err := NextBest(sealed, []nextbest.Candidate{candidate("allowed", true), candidate("denied", false)})
	if err != nil {
		t.Fatalf("NextBest: %v", err)
	}
	for _, r := range d.Ranking.Ranked {
		if r.ID == "denied" {
			t.Fatal("a rights-denied candidate must be excluded, never ranked")
		}
	}
	if len(d.Ranking.Excluded) != 1 {
		t.Fatalf("expected the denied candidate in Excluded, got %d", len(d.Ranking.Excluded))
	}
}

func TestSufficientObjectNeedsNoNextBest(t *testing.T) {
	o, _ := Seal(good(t))
	if _, err := NextBest(o, []nextbest.Candidate{candidate("A", true)}); err == nil {
		t.Fatal("a sufficient object must not produce next-best evidence")
	}
}

func TestInsufficiencyReasonIsEmptyWhenSufficient(t *testing.T) {
	o, _ := Seal(good(t))
	if InsufficiencyReason(o) != "" {
		t.Fatalf("a sufficient object has no insufficiency reason, got %q", InsufficiencyReason(o))
	}
}

// TestUnobtainableEvidenceIsDistinguished: "we could not get it" and
// "it cannot be got" are different facts about a conclusion.
func TestUnobtainableEvidenceIsDistinguished(t *testing.T) {
	o := good(t)
	o.MissingEvidence = []MissingEvidence{
		{ConditionID: "cond-1", Description: "CCTV from the terminal", Obtainable: true},
		{ConditionID: "cond-1", Description: "the destroyed original manifest", Obtainable: false,
			Reason: "the document was destroyed before the dispute arose"},
	}
	sealed, _ := Seal(o)
	un := sealed.UnobtainableEvidence()
	if len(un) != 1 || un[0].Reason == "" {
		t.Fatalf("unobtainable evidence must be separable and reasoned, got %+v", un)
	}
}
