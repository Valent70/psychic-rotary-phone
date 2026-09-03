// Command veriqo-runtime-evidence executes the canonical evidence-to-
// decision chain and writes down what it actually emitted.
//
// It exists because "article -> code -> test" is not enterprise
// assurance. A test proves a control behaves correctly when exercised
// deliberately; it says nothing about whether the control ran in the
// system as assembled, or left anything behind when it did.
//
// So this command runs the chain for real and records the identity of
// every audit event it produced. pkg/assurance's traceability matrix
// then cites those records by id, and a test resolves every citation
// against this artefact — so a matrix row claiming runtime evidence
// cannot cite a record that was never emitted.
//
// The run is deterministic: logical ticks, fixed identifiers, no
// wall-clock time and no randomness. Two runs produce the same event
// ids, which is what makes the citations stable.
//
//	go run ./cmd/veriqo-runtime-evidence > evidence/RUNTIME_EVIDENCE.json
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"veriqo/pkg/casefabric"
	"veriqo/pkg/contract/event"
	"veriqo/pkg/platform/audit"
	"veriqo/pkg/proof"
	"veriqo/pkg/qualification/independence"
	"veriqo/pkg/qualification/observability"
	"veriqo/pkg/qualification/reverseproof"
	"veriqo/pkg/qualification/state"
)

const (
	caseID  = "RUNTIME-EVIDENCE-CASE-1"
	propID  = "RUNTIME-EVIDENCE-PROP-1"
	claimID = "RUNTIME-EVIDENCE-CLAIM-1"
	digest  = "sha256:runtime-evidence-lab-report"
)

// record is one emitted audit event, as cited by the matrix.
type record struct {
	Index    uint64 `json:"index"`
	EventID  string `json:"event_id"`
	Actor    string `json:"actor"`
	Action   string `json:"action"`
	Hash     string `json:"hash"`
	PrevHash string `json:"prev_hash"`
}

type artefact struct {
	Schema      string   `json:"schema"`
	GeneratedBy string   `json:"generated_by"`
	Note        string   `json:"note"`
	Determinism string   `json:"determinism"`
	Boundary    string   `json:"boundary"`
	CaseID      string   `json:"case_id"`
	ProofHash   string   `json:"proof_hash"`
	DecisionID  string   `json:"decision_hash"`
	LedgerRoot  string   `json:"ledger_root_hash"`
	Records     []record `json:"records"`
}

func main() {
	a, err := run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "veriqo-runtime-evidence:", err)
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(a); err != nil {
		fmt.Fprintln(os.Stderr, "veriqo-runtime-evidence:", err)
		os.Exit(1)
	}
}

func run() (artefact, error) {
	store := audit.NewAuditStore()

	o, err := sealProof()
	if err != nil {
		return artefact{}, fmt.Errorf("seal: %w", err)
	}

	c, err := buildCase(o)
	if err != nil {
		return artefact{}, fmt.Errorf("case: %w", err)
	}

	// The chain: finding, authorization, decision. Each stage refuses
	// its predecessor's absence, so reaching the decision at all is
	// itself part of what this artefact records.
	f, err := proof.NewFinding(o, 20)
	if err != nil {
		return artefact{}, fmt.Errorf("finding: %w", err)
	}
	auth, err := proof.Authorize(f, o, "partner-1", "partner", "policy-v1", "adopted on review", 30)
	if err != nil {
		return artefact{}, fmt.Errorf("authorize: %w", err)
	}
	d, err := proof.Decide(auth, "refer_to_tribunal", "the evidence package is complete",
		map[string]string{"forum": "SIAC"}, 40)
	if err != nil {
		return artefact{}, fmt.Errorf("decide: %w", err)
	}

	if _, err := c.Resolve("evidence_package_delivered",
		"pre-loading contamination established on the sampled parcel", "partner-1", 41); err != nil {
		return artefact{}, fmt.Errorf("resolve: %w", err)
	}
	if err := c.AddOutcomeLimitations(o.Limitations); err != nil {
		return artefact{}, fmt.Errorf("limitations: %w", err)
	}

	// Everything lands in the one ledger.
	recs, chain, err := casefabric.Mirror(store, c, "policy-v1")
	if err != nil {
		return artefact{}, fmt.Errorf("mirror: %w", err)
	}
	proofRec, err := casefabric.MirrorProof(store, "analyst-1", o)
	if err != nil {
		return artefact{}, fmt.Errorf("mirror proof: %w", err)
	}
	recs = append(recs, proofRec)

	// Re-verify before writing anything down. An artefact generated from
	// a ledger that does not verify would be worse than none.
	if err := (audit.Auditor{}).VerifyChain(store.Snapshot()); err != nil {
		return artefact{}, fmt.Errorf("ledger verification: %w", err)
	}
	if err := event.VerifyChain(chain.Events()); err != nil {
		return artefact{}, fmt.Errorf("event chain verification: %w", err)
	}
	if err := c.VerifyTimeline(); err != nil {
		return artefact{}, fmt.Errorf("timeline verification: %w", err)
	}
	if err := proof.VerifyHash(o); err != nil {
		return artefact{}, fmt.Errorf("proof verification: %w", err)
	}

	out := artefact{
		Schema:      "veriqo.runtime_evidence/v1",
		GeneratedBy: "cmd/veriqo-runtime-evidence",
		Note: "Audit events emitted by an actual execution of the canonical evidence-to-decision chain. " +
			"pkg/assurance's traceability matrix cites these ids; TestEveryRuntimeEvidenceRefResolves " +
			"fails if a row cites a record this run did not emit.",
		Determinism: "Logical ticks, fixed identifiers, no wall-clock time and no randomness. " +
			"Two runs produce identical event ids.",
		Boundary: "The evidence in this run is a fixture. It demonstrates that the chain executes and " +
			"records; it does not demonstrate behaviour on real commercial data, which is the LIVE_DATA " +
			"blocker and remains BLOCKED_EXTERNAL.",
		CaseID:     caseID,
		ProofHash:  o.CanonicalHash,
		DecisionID: d.Hash(),
		LedgerRoot: store.RootHash(),
	}
	for _, r := range recs {
		out.Records = append(out.Records, record{
			Index: r.Index, EventID: eventID(r), Actor: r.Actor,
			Action: r.Action, Hash: r.Hash, PrevHash: r.PrevHash,
		})
	}
	return out, nil
}

// eventID is the citation form used by the traceability matrix: the
// action and the ledger index, which together identify one record
// unambiguously within a run.
func eventID(r audit.AuditRecord) string {
	return fmt.Sprintf("AUDIT-%03d-%s", r.Index, r.Action)
}

func sealProof() (proof.Object, error) {
	claim := reverseproof.Claim{
		ID: claimID, Description: "the cargo was contaminated before loading",
		Conditions: []reverseproof.Condition{{ID: "cond-1", Description: "pre-load contamination"}},
	}
	reqs := []reverseproof.Requirement{
		{ID: "R-1", ConditionID: "cond-1", Description: "pre-load sample analysis",
			ExpectedIfTrue: "contaminant present", ContradictsIfShows: "clean sample",
			Status: reverseproof.Obtained, DiagnosticValue: 0.9},
		{ID: "R-2", ConditionID: "cond-1", Description: "terminal CCTV of the loading window",
			ExpectedIfTrue: "contamination visible at loading", ContradictsIfShows: "clean loading",
			Status: reverseproof.Unobtainable, DiagnosticValue: 0.4},
	}
	alts := []reverseproof.AlternativeHypothesis{
		{ID: "A-1", Description: "contaminated in transit", Tested: true},
	}
	rs, err := reverseproof.Build(claim, reqs, alts, 10)
	if err != nil {
		return proof.Object{}, err
	}
	gap := reverseproof.Analyze(rs, map[string]bool{"cond-1": true})

	// The absence was gated, not assumed.
	absence, err := observability.Evaluate(observability.Assessment{
		Subject: "terminal CCTV of the loading window", SourceID: "terminal-cctv",
		Conditions: observability.AllConditionsMet(), Material: true, Tick: 9,
	})
	if err != nil {
		return proof.Object{}, err
	}
	if !absence.State.CarriesEvidentialWeight() {
		return proof.Object{}, fmt.Errorf("gated absence carries no weight: %s", absence.Reason)
	}

	sources := []independence.Source{
		{ID: "lab-a", Attributes: map[independence.Dimension]string{
			independence.RootOrigin: "lab-a", independence.OrganizationalControl: "lab-a-holdings",
			independence.ProviderPipeline: "lab-a-lims", independence.Collector: "lab-a",
			independence.AcquisitionPath: "direct"}},
		{ID: "surveyor-b", Attributes: map[independence.Dimension]string{
			independence.RootOrigin: "surveyor-b", independence.OrganizationalControl: "surveyor-b-llp",
			independence.ProviderPipeline: "surveyor-b-field", independence.Collector: "surveyor-b",
			independence.AcquisitionPath: "direct"}},
	}
	effective, err := independence.EffectiveSourceCount(sources)
	if err != nil {
		return proof.Object{}, err
	}

	q, err := state.New(claimID, state.Supported, "policy-v1", "two independent sources agree", nil, 10)
	if err != nil {
		return proof.Object{}, err
	}

	return proof.Seal(proof.Object{
		Proposition: proof.Proposition{ID: propID,
			Statement: "the cargo was contaminated before loading", SubjectType: "Cargo", SubjectID: "CARGO-9"},
		Scope:        proof.Scope{CaseID: caseID, Matter: "cargo damage claim"},
		Jurisdiction: proof.Jurisdiction{Code: "SG", Forum: "SIAC", GoverningLaw: "English law"},
		TimeWindow:   proof.TimeWindow{FromTick: 1, ToTick: 500},
		EvidenceSet: []proof.EvidenceRef{
			{EvidenceID: "E-1", EvidenceVersionID: "EV-1-v1", SHA256: digest, SourceID: "lab-a"},
			{EvidenceID: "E-2", EvidenceVersionID: "EV-2-v1", SHA256: "sha256:surveyor", SourceID: "surveyor-b"},
		},
		Quality:         proof.Quality{Assessed: true, Grade: "primary"},
		ReverseProof:    rs,
		ReverseProofGap: gap,
		MissingEvidence: []proof.MissingEvidence{
			{ConditionID: "cond-1", Description: "terminal CCTV", Obtainable: false,
				Reason: "retention expired before the dispute arose; searched and observed absent"},
		},
		Trust: proof.TrustAssessment{Assessed: true, EffectiveSourceCount: effective,
			Verdicts: map[string]independence.Verdict{"lab-a:surveyor-b": independence.Independent}},
		Qualification: q,
		Authority:     proof.Authority{AuthorityID: "analyst-1", Role: "senior-analyst", PolicyVersion: "policy-v1"},
		Disclosure:    proof.DisclosureState{Procedural: 2, Content: 3, Privilege: "NOT_CLAIMED"},
		Limitations: []string{
			"covers the sampled parcel only",
			"temporal ordering is chain-attested, not independently attested",
		},
		Provenance: proof.Provenance{GeneratedBy: "fref-pipeline", GeneratedAtTick: 10,
			PipelineVersion: "fref-v1", InputHashes: []string{digest}},
		ReplayReference: "REPLAY-RUNTIME-EVIDENCE-1",
	})
}

func buildCase(o proof.Object) (*casefabric.Case, error) {
	c, err := casefabric.Open(casefabric.Identity{
		CaseID: caseID, TenantID: "tenant-a", Domain: casefabric.DomainInsurance,
		ExternalRefs: map[string]string{"claim_no": "CLM-RUNTIME-1"},
	}, "analyst-1", 1)
	if err != nil {
		return nil, err
	}
	steps := []func() error{
		func() error {
			return c.SetScope(o.Scope, o.Jurisdiction, o.TimeWindow, casefabric.Mission{
				Statement: "establish whether the cargo was contaminated before loading",
				Intent:    "quantify the loss", SetBy: "analyst-1", SetAtTick: 2}, "analyst-1", 2)
		},
		func() error { return c.AddEvidence(o.EvidenceSet, "analyst-1", 3) },
		func() error {
			return c.AddHypothesis(casefabric.Hypothesis{
				ID: "H-1", Description: "contaminated in transit"}, "analyst-1", 4)
		},
		func() error {
			return c.RegisterClaim(casefabric.Claim{
				ID: claimID, Material: true, Proposition: o.Proposition}, "analyst-1", 5)
		},
		func() error {
			return c.TestHypothesis("H-1", "excluded by the pre-load sample", "analyst-1", 6)
		},
		func() error { return c.BeginQualification("analyst-1", 7) },
		func() error { return c.AttachProof(claimID, o, "analyst-1", 8) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return nil, err
		}
	}
	return c, nil
}
