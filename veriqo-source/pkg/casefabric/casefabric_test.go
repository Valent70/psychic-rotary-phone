package casefabric

import (
	"errors"
	"strings"
	"testing"

	"veriqo/pkg/insurance/casestate"
	"veriqo/pkg/proof"
	"veriqo/pkg/qualification/reverseproof"
	"veriqo/pkg/qualification/state"
)

func ident(domain string) Identity {
	return Identity{CaseID: "CASE-1", TenantID: "tenant-a", Domain: domain,
		ExternalRefs: map[string]string{"claim_no": "CLM-777"}}
}

func openScoped(t *testing.T) *Case {
	t.Helper()
	c, err := Open(ident(DomainInsurance), "analyst-1", 1)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	err = c.SetScope(
		proof.Scope{CaseID: "CASE-1", Matter: "cargo damage claim", Boundaries: []string{"excludes the onward leg"}},
		proof.Jurisdiction{Code: "SG", Forum: "SIAC", GoverningLaw: "English law"},
		proof.TimeWindow{FromTick: 1, ToTick: 500},
		Mission{Statement: "establish whether the cargo was contaminated before loading",
			Intent: "quantify the loss", SetBy: "analyst-1", SetAtTick: 2},
		"analyst-1", 2)
	if err != nil {
		t.Fatalf("SetScope: %v", err)
	}
	return c
}

// sealed builds a sufficient proof object for the given proposition.
func sealed(t *testing.T, propID, caseID string) proof.Object {
	t.Helper()
	claim := reverseproof.Claim{ID: "C-1", Description: "contamination before loading",
		Conditions: []reverseproof.Condition{{ID: "cond-1", Description: "pre-load contamination"}}}
	reqs := []reverseproof.Requirement{{ID: "R-1", ConditionID: "cond-1", Description: "pre-load sample",
		ExpectedIfTrue: "contaminant present", ContradictsIfShows: "clean sample",
		Status: reverseproof.Obtained, DiagnosticValue: 0.9}}
	alts := []reverseproof.AlternativeHypothesis{{ID: "A-1", Description: "contaminated in transit", Tested: true}}
	rs, err := reverseproof.Build(claim, reqs, alts, 10)
	if err != nil {
		t.Fatalf("reverseproof.Build: %v", err)
	}
	gap := reverseproof.Analyze(rs, map[string]bool{"cond-1": true})
	q, err := state.New("C-1", state.Supported, "policy-v1", "qualified", nil, 10)
	if err != nil {
		t.Fatalf("state.New: %v", err)
	}
	o, err := proof.Seal(proof.Object{
		Proposition:  proof.Proposition{ID: propID, Statement: "the cargo was contaminated before loading", SubjectType: "Cargo", SubjectID: "C-9"},
		Scope:        proof.Scope{CaseID: caseID, Matter: "cargo damage claim"},
		Jurisdiction: proof.Jurisdiction{Code: "SG"},
		TimeWindow:   proof.TimeWindow{FromTick: 1, ToTick: 500},
		EvidenceSet:  []proof.EvidenceRef{{EvidenceID: "E-1", EvidenceVersionID: "EV-1-v1", SHA256: "abc", SourceID: "lab-a"}},
		Quality:      proof.Quality{Assessed: true, Grade: "primary"},
		ReverseProof: rs, ReverseProofGap: gap,
		Trust:         proof.TrustAssessment{Assessed: true, EffectiveSourceCount: 2},
		Qualification: q,
		Authority:     proof.Authority{AuthorityID: "auth-1", Role: "analyst", PolicyVersion: "policy-v1"},
		Limitations:   []string{"covers the sampled parcel only"},
		Provenance:    proof.Provenance{GeneratedBy: "pipeline-1", GeneratedAtTick: 10, PipelineVersion: "fref-v1"},
	})
	if err != nil {
		t.Fatalf("proof.Seal: %v", err)
	}
	return o
}

// --- Domain projections ----------------------------------------------

// TestEveryInsuranceStateMapsOntoTheFabric is Law 3 made a build
// failure: a domain that adds a state without mapping it breaks here,
// not silently in production.
func TestEveryInsuranceStateMapsOntoTheFabric(t *testing.T) {
	p, ok := Lookup(DomainInsurance)
	if !ok {
		t.Fatal("insurance must be registered with the fabric")
	}
	for _, s := range casestate.States() {
		if _, err := p.Phase(string(s)); err != nil {
			t.Fatalf("insurance state %q maps to no canonical phase: %v", s, err)
		}
	}
	if len(p.StateToPhase) != len(casestate.States()) {
		t.Fatalf("the projection has %d states, casestate has %d: the mapping has drifted",
			len(p.StateToPhase), len(casestate.States()))
	}
}

func TestAllSixDomainsAreRegistered(t *testing.T) {
	got := RegisteredDomains()
	want := []string{DomainCommodity, DomainDispute, DomainInsurance, DomainMaritime, DomainSupplyChain, DomainTradeFinance}
	if len(got) != len(want) {
		t.Fatalf("expected %d domains, got %v", len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("domain %d: expected %q, got %q", i, want[i], got[i])
		}
	}
}

// TestEveryProjectionMapsEveryCanonicalPhaseItClaims guards the reverse
// direction: a projection may not name a phase the fabric does not have.
func TestProjectionsAreValid(t *testing.T) {
	for _, p := range CanonicalProjections() {
		if err := p.Validate(); err != nil {
			t.Fatalf("projection %q: %v", p.Domain, err)
		}
	}
}

// TestDisputeDomainHasNoDecidedState is the positioning boundary at the
// vocabulary level: a dispute case ends by delivering evidence.
func TestDisputeDomainHasNoDecidedState(t *testing.T) {
	p, _ := Lookup(DomainDispute)
	for st := range p.StateToPhase {
		lower := strings.ToLower(st)
		for _, banned := range []string{"decided", "awarded", "judgment", "verdict", "won", "lost"} {
			if strings.Contains(lower, banned) {
				t.Fatalf("dispute state %q implies adjudication", st)
			}
		}
	}
	if p.StateToPhase["EVIDENCE_PACKAGE_DELIVERED"] != PhaseResolved {
		t.Fatal("a dispute case resolves by delivering an evidence package")
	}
}

func TestUnregisteredDomainCannotOpenACase(t *testing.T) {
	if _, err := Open(ident("astrology"), "analyst-1", 1); !errors.Is(err, ErrUnknownDomain) {
		t.Fatalf("expected ErrUnknownDomain, got %v", err)
	}
}

func TestProjectionRequiresPackageAndStates(t *testing.T) {
	if err := Register(Projection{Domain: "x", StateToPhase: map[string]Phase{"A": PhaseOpened}}); err == nil {
		t.Fatal("a projection with no canonical package must be refused")
	}
	if err := Register(Projection{Domain: "x", CanonicalPackage: "p"}); err == nil {
		t.Fatal("a projection mapping no states must be refused")
	}
	if err := Register(Projection{Domain: "x", CanonicalPackage: "p",
		StateToPhase: map[string]Phase{"A": Phase("INVENTED")}}); err == nil {
		t.Fatal("a projection naming an unknown phase must be refused")
	}
}

// --- Lifecycle -------------------------------------------------------

func TestCaseRequiresIdentity(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   Identity
		want error
	}{
		{"no case id", Identity{TenantID: "t", Domain: DomainInsurance}, ErrNoCaseID},
		{"no tenant", Identity{CaseID: "c", Domain: DomainInsurance}, ErrNoTenant},
		{"no domain", Identity{CaseID: "c", TenantID: "t"}, ErrNoDomain},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Open(tc.id, "a", 1); !errors.Is(err, tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, err)
			}
		})
	}
}

func TestEvidenceCannotBeAddedBeforeScoping(t *testing.T) {
	c, _ := Open(ident(DomainInsurance), "analyst-1", 1)
	err := c.AddEvidence([]EvidenceRef{{EvidenceID: "E-1", EvidenceVersionID: "v1", SHA256: "abc"}}, "analyst-1", 3)
	if !errors.Is(err, ErrNotScoped) {
		t.Fatalf("expected ErrNotScoped, got %v", err)
	}
}

func TestCaseEvidenceMustBePinned(t *testing.T) {
	c := openScoped(t)
	if err := c.AddEvidence([]EvidenceRef{{EvidenceID: "E-1", SHA256: "abc"}}, "analyst-1", 3); !errors.Is(err, ErrUnpinnedEvidence) {
		t.Fatalf("expected ErrUnpinnedEvidence for a missing version, got %v", err)
	}
	if err := c.AddEvidence([]EvidenceRef{{EvidenceID: "E-1", EvidenceVersionID: "v1"}}, "analyst-1", 3); !errors.Is(err, ErrUnpinnedEvidence) {
		t.Fatalf("expected ErrUnpinnedEvidence for a missing content hash, got %v", err)
	}
}

// TestQualificationRequiresARivalHypothesis: a case with one story has
// not been investigated.
func TestQualificationRequiresARivalHypothesis(t *testing.T) {
	c := openScoped(t)
	if err := c.AddEvidence([]EvidenceRef{{EvidenceID: "E-1", EvidenceVersionID: "v1", SHA256: "abc"}}, "analyst-1", 3); err != nil {
		t.Fatalf("AddEvidence: %v", err)
	}
	if err := c.BeginQualification("analyst-1", 4); !errors.Is(err, ErrNoHypotheses) {
		t.Fatalf("expected ErrNoHypotheses, got %v", err)
	}
}

func TestFullLifecycleToResolution(t *testing.T) {
	c := openScoped(t)
	mustNoErr(t, c.AddEvidence([]EvidenceRef{{EvidenceID: "E-1", EvidenceVersionID: "EV-1-v1", SHA256: "abc", SourceID: "lab-a"}}, "analyst-1", 3))
	mustNoErr(t, c.AddHypothesis(Hypothesis{ID: "H-1", Description: "contaminated in transit"}, "analyst-1", 4))
	if c.Phase() != PhaseHypothesesFormed {
		t.Fatalf("expected HYPOTHESES_FORMED, got %s", c.Phase())
	}
	mustNoErr(t, c.RegisterClaim(Claim{ID: "CL-1", Material: true,
		Proposition: proof.Proposition{ID: "P-1", Statement: "the cargo was contaminated before loading"}}, "analyst-1", 5))
	mustNoErr(t, c.TestHypothesis("H-1", "excluded by the pre-load sample", "analyst-1", 6))
	mustNoErr(t, c.BeginQualification("analyst-1", 7))
	mustNoErr(t, c.AttachProof("CL-1", sealed(t, "P-1", "CASE-1"), "analyst-1", 8))

	o, err := c.Resolve("evidence_package_delivered", "pre-loading contamination established on the sampled parcel", "analyst-1", 9)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(o.EstablishedClaimIDs) != 1 || len(o.UnestablishedClaimIDs) != 0 {
		t.Fatalf("unexpected outcome claims: %+v", o)
	}
	if c.Phase() != PhaseResolved {
		t.Fatalf("expected RESOLVED, got %s", c.Phase())
	}
	if err := c.VerifyTimeline(); err != nil {
		t.Fatalf("VerifyTimeline: %v", err)
	}
}

// TestResolutionBlockedByAnUnprovenMaterialClaim is the fabric's core
// discipline: a case cannot resolve past what it failed to establish.
func TestResolutionBlockedByAnUnprovenMaterialClaim(t *testing.T) {
	c := openScoped(t)
	mustNoErr(t, c.AddEvidence([]EvidenceRef{{EvidenceID: "E-1", EvidenceVersionID: "v1", SHA256: "abc"}}, "a", 3))
	mustNoErr(t, c.AddHypothesis(Hypothesis{ID: "H-1", Description: "alternative", Tested: true}, "a", 4))
	mustNoErr(t, c.RegisterClaim(Claim{ID: "CL-1", Material: true,
		Proposition: proof.Proposition{ID: "P-1", Statement: "contaminated before loading"}}, "a", 5))
	mustNoErr(t, c.BeginQualification("a", 6))

	if _, err := c.Resolve("delivered", "", "a", 7); !errors.Is(err, ErrClaimUnproven) {
		t.Fatalf("expected ErrClaimUnproven, got %v", err)
	}
	if got := c.UnprovenMaterialClaims(); len(got) != 1 || got[0] != "CL-1" {
		t.Fatalf("UnprovenMaterialClaims should name CL-1, got %v", got)
	}
}

// TestAnImmaterialClaimDoesNotBlockResolution proves the material flag
// does real work.
func TestImmaterialClaimDoesNotBlockResolution(t *testing.T) {
	c := openScoped(t)
	mustNoErr(t, c.AddEvidence([]EvidenceRef{{EvidenceID: "E-1", EvidenceVersionID: "v1", SHA256: "abc"}}, "a", 3))
	mustNoErr(t, c.AddHypothesis(Hypothesis{ID: "H-1", Description: "alt", Tested: true}, "a", 4))
	mustNoErr(t, c.RegisterClaim(Claim{ID: "CL-2", Material: false,
		Proposition: proof.Proposition{ID: "P-2", Statement: "the surveyor arrived late"}}, "a", 5))
	mustNoErr(t, c.BeginQualification("a", 6))

	o, err := c.Resolve("no_further_action", "", "a", 7)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// The unproven claim is still reported, with equal prominence.
	if len(o.UnestablishedClaimIDs) != 1 {
		t.Fatalf("an unproven immaterial claim must still be reported, got %+v", o)
	}
}

func TestUntestedHypothesisBlocksResolution(t *testing.T) {
	c := openScoped(t)
	mustNoErr(t, c.AddEvidence([]EvidenceRef{{EvidenceID: "E-1", EvidenceVersionID: "v1", SHA256: "abc"}}, "a", 3))
	mustNoErr(t, c.AddHypothesis(Hypothesis{ID: "H-1", Description: "untested rival"}, "a", 4))
	mustNoErr(t, c.BeginQualification("a", 5))
	if _, err := c.Resolve("delivered", "", "a", 6); err == nil {
		t.Fatal("a case with an untested rival hypothesis must not resolve")
	}
	if got := c.UntestedHypotheses(); len(got) != 1 {
		t.Fatalf("UntestedHypotheses should name H-1, got %v", got)
	}
}

// --- Proof binding ---------------------------------------------------

func TestProofFromAnotherCaseIsRefused(t *testing.T) {
	c := readyForProof(t)
	if err := c.AttachProof("CL-1", sealed(t, "P-1", "CASE-OTHER"), "a", 8); !errors.Is(err, ErrProofWrongCase) {
		t.Fatalf("expected ErrProofWrongCase, got %v", err)
	}
}

func TestProofForAnotherPropositionIsRefused(t *testing.T) {
	c := readyForProof(t)
	if err := c.AttachProof("CL-1", sealed(t, "P-999", "CASE-1"), "a", 8); !errors.Is(err, ErrProofWrongClaim) {
		t.Fatalf("expected ErrProofWrongClaim, got %v", err)
	}
}

func TestTamperedProofIsRefused(t *testing.T) {
	c := readyForProof(t)
	o := sealed(t, "P-1", "CASE-1")
	o.Proposition.Statement = "the cargo was contaminated after loading"
	if err := c.AttachProof("CL-1", o, "a", 8); err == nil {
		t.Fatal("a proof object altered after sealing must not attach to a claim")
	}
}

func TestProofForAnUnknownClaimIsRefused(t *testing.T) {
	c := readyForProof(t)
	if err := c.AttachProof("CL-NOPE", sealed(t, "P-1", "CASE-1"), "a", 8); !errors.Is(err, ErrUnknownClaim) {
		t.Fatalf("expected ErrUnknownClaim, got %v", err)
	}
}

// TestInsufficientProofDoesNotProveAClaim: attaching an insufficient
// object records the truth rather than establishing the claim.
func TestInsufficientProofDoesNotProveAClaim(t *testing.T) {
	c := readyForProof(t)
	o := sealed(t, "P-1", "CASE-1")

	// Rebuild it insufficient by removing the quality assessment.
	o.Quality.Assessed = false
	reSealed, err := proof.Seal(o)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	mustNoErr(t, c.AttachProof("CL-1", reSealed, "a", 8))

	claims := c.Claims()
	if claims[0].Proven() {
		t.Fatal("an insufficient proof object must not prove a claim")
	}
	if _, err := c.Resolve("delivered", "", "a", 9); !errors.Is(err, ErrClaimUnproven) {
		t.Fatalf("expected ErrClaimUnproven, got %v", err)
	}
}

// --- Adjudication boundary -------------------------------------------

// TestOutcomeMayNotAdjudicate is the same boundary as pkg/proof's,
// enforced where a case ends.
func TestOutcomeMayNotAdjudicate(t *testing.T) {
	for _, bad := range []string{"award", "verdict", "judgment", "winner"} {
		o := Outcome{Disposition: bad, Summary: "x"}
		if err := o.Validate(); !errors.Is(err, ErrAdjudication) {
			t.Fatalf("disposition %q must be refused, got %v", bad, err)
		}
	}
	o := Outcome{Disposition: "referred_to_tribunal", Summary: "the winner is the claimant"}
	if err := o.Validate(); !errors.Is(err, ErrAdjudication) {
		t.Fatal("an adjudicatory summary must be refused")
	}
	ok := Outcome{Disposition: "evidence_package_delivered", Summary: "contamination established on the sampled parcel"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("a non-adjudicatory outcome must be accepted: %v", err)
	}
}

func TestResolvingWithAnAdjudicatoryDispositionIsRefused(t *testing.T) {
	c := readyForProof(t)
	mustNoErr(t, c.AttachProof("CL-1", sealed(t, "P-1", "CASE-1"), "a", 8))
	if _, err := c.Resolve("verdict", "", "a", 9); !errors.Is(err, ErrAdjudication) {
		t.Fatalf("expected ErrAdjudication, got %v", err)
	}
}

// --- Domain sync -----------------------------------------------------

func TestDomainStateSyncMovesTheCanonicalPhase(t *testing.T) {
	c := openScoped(t)
	if err := c.SyncDomainState("EVIDENCE_EXCHANGED", "a", 5); err != nil {
		t.Fatalf("SyncDomainState: %v", err)
	}
	if c.Phase() != PhaseEvidenceGathering {
		t.Fatalf("expected EVIDENCE_GATHERING, got %s", c.Phase())
	}
}

// TestUnmappedDomainStateIsRefused is the bypass attempt: a domain
// inventing a state it never registered.
func TestUnmappedDomainStateIsRefused(t *testing.T) {
	c := openScoped(t)
	if err := c.SyncDomainState("SETTLED_QUIETLY", "a", 5); !errors.Is(err, ErrUnmappedState) {
		t.Fatalf("expected ErrUnmappedState, got %v", err)
	}
	if c.Phase() != PhaseScoped {
		t.Fatal("a refused sync must not move the phase")
	}
}

func TestDomainSyncStillObeysTheCanonicalLifecycle(t *testing.T) {
	c := openScoped(t)
	// PAYMENT_EXECUTED maps to RESOLVED, which is not reachable from SCOPED.
	if err := c.SyncDomainState("PAYMENT_EXECUTED", "a", 5); !errors.Is(err, ErrBadTransition) {
		t.Fatalf("a domain must not jump the canonical lifecycle, got %v", err)
	}
}

// --- Timeline integrity ----------------------------------------------

func TestTimelineIsAppendOnlyAndChained(t *testing.T) {
	c := openScoped(t)
	mustNoErr(t, c.AddEvidence([]EvidenceRef{{EvidenceID: "E-1", EvidenceVersionID: "v1", SHA256: "abc"}}, "a", 3))
	tl := c.Timeline()
	if len(tl) < 3 {
		t.Fatalf("expected open, scope and evidence entries, got %d", len(tl))
	}
	for i := 1; i < len(tl); i++ {
		if tl[i].PriorHash != tl[i-1].EntryHash {
			t.Fatalf("timeline entry %d is not chained", i)
		}
	}
	if err := c.VerifyTimeline(); err != nil {
		t.Fatalf("VerifyTimeline: %v", err)
	}
}

// TestTimelineCopyCannotCorruptTheCase closes the accessor-mutation
// route: editing a returned timeline must not alter the case.
func TestAccessorsReturnCopies(t *testing.T) {
	c := openScoped(t)
	c.Timeline()[0].Description = "rewritten"
	if c.Timeline()[0].Description == "rewritten" {
		t.Fatal("Timeline must return a copy")
	}
	c.Identity().ExternalRefs["claim_no"] = "forged"
	if c.Identity().ExternalRefs["claim_no"] == "forged" {
		t.Fatal("Identity must return a copy")
	}
	s, _, _ := c.Scope()
	s.Boundaries[0] = "no boundaries"
	s2, _, _ := c.Scope()
	if s2.Boundaries[0] == "no boundaries" {
		t.Fatal("Scope must return a copy")
	}
}

// --- Reopening -------------------------------------------------------

func TestReopeningClearsTheOutcomeButKeepsTheRecord(t *testing.T) {
	c := readyForProof(t)
	mustNoErr(t, c.AttachProof("CL-1", sealed(t, "P-1", "CASE-1"), "a", 8))
	if _, err := c.Resolve("evidence_package_delivered", "", "a", 9); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	mustNoErr(t, c.Close("delivered", "a", 10))
	before := len(c.Timeline())

	mustNoErr(t, c.Reopen("a new laboratory report has emerged", "a", 11))
	if _, ok := c.Outcome(); ok {
		t.Fatal("reopening must clear the outcome")
	}
	if len(c.Timeline()) <= before {
		t.Fatal("reopening must be recorded, and the prior record retained")
	}
	if err := c.VerifyTimeline(); err != nil {
		t.Fatalf("the timeline must survive reopening: %v", err)
	}
}

func TestReopeningRequiresAReason(t *testing.T) {
	c := openScoped(t)
	mustNoErr(t, c.Close("withdrawn", "a", 5))
	if err := c.Reopen("", "a", 6); err == nil {
		t.Fatal("reopening with no reason must be refused")
	}
}

func TestSuspensionRequiresACause(t *testing.T) {
	c := openScoped(t)
	if err := c.Suspend("", "a", 5); !errors.Is(err, ErrSuspendNeedsCause) {
		t.Fatalf("expected ErrSuspendNeedsCause, got %v", err)
	}
	mustNoErr(t, c.Suspend("legal hold pending", "a", 5))
	if c.Phase() != PhaseSuspended {
		t.Fatalf("expected SUSPENDED, got %s", c.Phase())
	}
}

// --- Transitions -----------------------------------------------------

func TestCanTransitionRejectsPhaseJumps(t *testing.T) {
	if CanTransition(PhaseOpened, PhaseResolved) {
		t.Fatal("a case must not jump from OPENED to RESOLVED")
	}
	if CanTransition(PhaseClosed, PhaseUnderQualification) {
		t.Fatal("a closed case must be reopened before further work")
	}
	if !CanTransition(PhaseClosed, PhaseReopened) {
		t.Fatal("a closed case must be reopenable")
	}
	if !CanTransition(PhaseSuspended, PhaseEvidenceGathering) {
		t.Fatal("a suspended case must be resumable")
	}
}

func TestDuplicateClaimIsRefused(t *testing.T) {
	c := readyForProof(t)
	err := c.RegisterClaim(Claim{ID: "CL-1", Proposition: proof.Proposition{ID: "P-1", Statement: "x"}}, "a", 9)
	if !errors.Is(err, ErrDuplicateClaim) {
		t.Fatalf("expected ErrDuplicateClaim, got %v", err)
	}
}

func TestOutcomeLimitationsAreDeduplicatedAndSorted(t *testing.T) {
	c := readyForProof(t)
	mustNoErr(t, c.AttachProof("CL-1", sealed(t, "P-1", "CASE-1"), "a", 8))
	if _, err := c.Resolve("evidence_package_delivered", "", "a", 9); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	mustNoErr(t, c.AddOutcomeLimitations([]string{"b limit", "a limit", "b limit", "  "}))
	o, _ := c.Outcome()
	if len(o.Limitations) != 2 || o.Limitations[0] != "a limit" {
		t.Fatalf("limitations must be deduplicated and sorted, got %v", o.Limitations)
	}
}

// --- helpers ---------------------------------------------------------

func readyForProof(t *testing.T) *Case {
	t.Helper()
	c := openScoped(t)
	mustNoErr(t, c.AddEvidence([]EvidenceRef{{EvidenceID: "E-1", EvidenceVersionID: "EV-1-v1", SHA256: "abc", SourceID: "lab-a"}}, "a", 3))
	mustNoErr(t, c.AddHypothesis(Hypothesis{ID: "H-1", Description: "contaminated in transit", Tested: true}, "a", 4))
	mustNoErr(t, c.RegisterClaim(Claim{ID: "CL-1", Material: true,
		Proposition: proof.Proposition{ID: "P-1", Statement: "the cargo was contaminated before loading"}}, "a", 5))
	mustNoErr(t, c.BeginQualification("a", 7))
	return c
}

func mustNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
