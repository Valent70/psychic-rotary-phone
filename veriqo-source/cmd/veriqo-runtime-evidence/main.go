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
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"veriqo/pkg/canonical/jcs"
	"veriqo/pkg/casefabric"
	"veriqo/pkg/contract/event"
	"veriqo/pkg/evidence/redaction"
	"veriqo/pkg/evidence/redaction/worker"
	"veriqo/pkg/fref"
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

	// REVERSE_PROOF runs before the case reaches qualification, so the
	// closure gates the verdict rather than auditing it afterwards.
	fwd, rev, closure, err := runBothDirections(o)
	if err != nil {
		return artefact{}, fmt.Errorf("directions: %w", err)
	}
	if !closure.Holds {
		return artefact{}, fmt.Errorf("reverse closure does not hold: %s", closure.Explain())
	}
	_, _ = fwd, rev

	c, err := buildCase(o, o.Proposition.ID, closure.Holds)
	if err != nil {
		return artefact{}, fmt.Errorf("case: %w", err)
	}

	// The chain, in constitutional order. Every step is recorded on the
	// case timeline at the point it happens, so the ledger this produces
	// is lawful by construction rather than by a later sort.
	//
	// The order matters and was got wrong once: an earlier version of
	// this command emitted case.resolved before proof.sealed, which made
	// the reverse direction look like a retrospective audit. See
	// fref.CanonicalSequence.

	// FINDING.
	f, err := proof.NewFinding(o, 20)
	if err != nil {
		return artefact{}, fmt.Errorf("finding: %w", err)
	}
	if err := c.RecordFinding(f.Hash(), o.CanonicalHash, "analyst-1", 20); err != nil {
		return artefact{}, fmt.Errorf("record finding: %w", err)
	}

	// AUTHORIZED_DECISION.
	auth, err := proof.Authorize(f, o, "partner-1", "partner", "policy-v1", "adopted on review", 30)
	if err != nil {
		return artefact{}, fmt.Errorf("authorize: %w", err)
	}
	d, err := proof.Decide(auth, "refer_to_tribunal", "the evidence package is complete",
		map[string]string{"forum": "SIAC"}, 40)
	if err != nil {
		return artefact{}, fmt.Errorf("decide: %w", err)
	}
	if err := c.RecordAuthorizedDecision(d, "partner-1", 40); err != nil {
		return artefact{}, fmt.Errorf("record decision: %w", err)
	}

	// CASE_RESOLUTION, which now consumes the finalized chain rather
	// than asserting it.
	gate := casefabric.ResolutionGate{
		Decision: d, ReverseClosureHolds: closure.Holds,
		ClosureSubject: o.Proposition.ID, ClosureExplanation: closure.Explain(),
	}
	if _, err := c.Resolve(gate, "evidence_package_delivered",
		"pre-loading contamination established on the sampled parcel", "partner-1", 41); err != nil {
		return artefact{}, fmt.Errorf("resolve: %w", err)
	}
	if err := c.AddOutcomeLimitations(o.Limitations); err != nil {
		return artefact{}, fmt.Errorf("limitations: %w", err)
	}

	// LEDGER: the whole timeline in one lawful stream, with the proof
	// record emitted where the proof entered the case.
	recs, chain, err := casefabric.Mirror(store, c, "policy-v1",
		map[string]proof.Object{claimID: o})
	if err != nil {
		return artefact{}, fmt.Errorf("mirror: %w", err)
	}

	// ARTICLE 18, on a live path.
	//
	// The redaction verifier existed and nothing called it, which is
	// what INTEGRATION_GAP meant. Here a real derivative is produced by
	// a real worker from a real container, verified byte-level over its
	// decompressed content, and its disclosure appended to this same
	// ledger. The event lands after the resolution because disclosing a
	// redacted derivative is not an epistemic act: it changes nothing
	// the case concluded, and the post-resolution mutation ban is about
	// evidence and claims, not about who may later be shown what.
	redactionRec, err := releaseRedactedDerivative(store)
	if err != nil {
		return artefact{}, fmt.Errorf("redaction release: %w", err)
	}
	recs = append(recs, redactionRec)

	// Re-verify before writing anything down. An artefact generated from
	// a ledger that does not verify would be worse than none, and one
	// whose events are out of constitutional order is what produced the
	// defect this command was rewritten to prevent.
	if err := (audit.Auditor{}).VerifyChain(store.Snapshot()); err != nil {
		return artefact{}, fmt.Errorf("ledger verification: %w", err)
	}
	var actions []string
	for _, r := range store.Snapshot() {
		actions = append(actions, r.Action)
	}
	if v := fref.VerifyEventOrder(actions); len(v) > 0 {
		return artefact{}, fmt.Errorf("the emitted ledger violates the constitutional sequence: %s", v[0])
	}
	// Order is not enough: a ledger can be perfectly ordered and still
	// have skipped a gate entirely.
	if g := fref.VerifyEventGates(actions); len(g) > 0 {
		return artefact{}, fmt.Errorf("the emitted ledger skipped a constitutional gate: %s", g[0])
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

// runBothDirections runs the forward and reverse executions and closes
// them over the same evidence.
//
// Both directions run to completion before anything is founded on the
// proof object. That is the constitutional order: the reverse direction
// is a gate, and a gate that runs after the decision is a rubber stamp.
func runBothDirections(o proof.Object) (*fref.Execution, *fref.Execution, fref.Closure, error) {
	fwd, err := fref.NewExecution(fref.Forward, o.Proposition.ID)
	if err != nil {
		return nil, nil, fref.Closure{}, err
	}
	for i, s := range fref.Order(fref.Forward) {
		b, _ := fref.BindingFor(s)
		if err := fwd.Complete(s, b.Package, uint64(i+1), "h-"+string(s), ""); err != nil {
			return nil, nil, fref.Closure{}, err
		}
	}
	rev, err := fref.NewExecution(fref.Reverse, o.Proposition.ID)
	if err != nil {
		return nil, nil, fref.Closure{}, err
	}
	for i, s := range fref.Order(fref.Reverse) {
		b, _ := fref.BindingFor(s)
		if err := rev.Complete(s, b.Package, uint64(i+1), "h-"+string(s), ""); err != nil {
			return nil, nil, fref.Closure{}, err
		}
	}
	var ids []string
	for _, e := range o.EvidenceSet {
		ids = append(ids, e.EvidenceVersionID)
	}
	closure, err := fref.Close(fwd, rev, ids, ids)
	return fwd, rev, closure, err
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

func buildCase(o proof.Object, closureSubject string, closureHolds bool) (*casefabric.Case, error) {
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
		// REVERSE_PROOF, before qualification. The reverse direction
		// fixes what would have to be shown BEFORE the verdict is
		// reached, which is the ordering the sequencing audit restored.
		func() error {
			return c.RecordReverseClosure(closureSubject, closureHolds, "analyst-1", 7)
		},
		func() error { return c.BeginQualification("analyst-1", 8) },
		func() error { return c.AttachProof(claimID, o, "analyst-1", 9) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// releaseRedactedDerivative runs the Article 18 pipeline over a real
// container and appends the disclosure event to the ledger.
//
// The fixture is built here rather than read from disk so that this
// command stays hermetic and deterministic, and so that a reader can
// see the forbidden term go in. The container is genuinely compressed:
// the term does not appear anywhere in its bytes, which is why the
// pipeline verifies the decompressed view instead.
func releaseRedactedDerivative(store *audit.AuditStore) (audit.AuditRecord, error) {
	const forbidden = "Acme Holdings Ltd"
	original := runtimeWorkbook(forbidden)
	if bytes.Contains(original, []byte(forbidden)) {
		return audit.AuditRecord{}, fmt.Errorf(
			"the fixture stores the term uncompressed, so verifying it would prove nothing")
	}

	rel, err := worker.NewPipeline().Run(worker.Request{
		Kind:                worker.KindXLSX,
		Original:            original,
		OriginalVersionID:   "EV-RUNTIME-1",
		DerivativeVersionID: "EV-RUNTIME-1-R1",
		PinnedOriginalHash:  redaction.Hash(original),
		ForbiddenTerms:      []string{forbidden},
	})
	if err != nil {
		return audit.AuditRecord{}, err
	}

	e := rel.LedgerEvent()
	payload, err := jcs.Canonicalize(map[string]any{
		"original_version_id":   e.OriginalVersionID,
		"derivative_version_id": e.DerivativeVersionID,
		"original_hash":         e.OriginalHash,
		"derivative_hash":       e.DerivativeHash,
		"terms_removed":         e.TermsRemoved,
		"encodings_checked":     e.EncodingsChecked,
		"worker":                e.Worker,
	})
	if err != nil {
		return audit.AuditRecord{}, fmt.Errorf("canonicalizing the disclosure event: %w", err)
	}
	return store.Append("compliance-1", e.Action, string(payload))
}

// runtimeWorkbook builds a minimal but real OOXML workbook whose cell
// text lives in the shared string table, deflated.
func runtimeWorkbook(text string) []byte {
	parts := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
			`<Default Extension="xml" ContentType="application/xml"/></Types>`,
		"xl/sharedStrings.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="2" uniqueCount="2">` +
			`<si><t>` + text + `</t></si><si><t>Parcel A, sampled pre-loading</t></si></sst>`,
		"xl/worksheets/sheet1.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>` +
			`<row r="1"><c r="A1" t="s"><v>0</v></c></row>` +
			`<row r="2"><c r="A2" t="s"><v>1</v></c></row></sheetData></worksheet>`,
	}
	names := make([]string, 0, len(parts))
	for n := range parts {
		names = append(names, n)
	}
	sort.Strings(names)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range names {
		hdr := &zip.FileHeader{Name: n, Method: zip.Deflate}
		hdr.Modified = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return nil
		}
		if _, err := w.Write([]byte(parts[n])); err != nil {
			return nil
		}
	}
	if err := zw.Close(); err != nil {
		return nil
	}
	return buf.Bytes()
}
