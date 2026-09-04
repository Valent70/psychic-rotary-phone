package assurance

import (
	"fmt"
	"sort"
	"strings"

	"veriqo/pkg/constitution"
)

// This file is the Constitutional Integration & Proof Audit: every one
// of the thirty articles, traced from the constitution to whatever
// external proof actually exists.
//
// The honest headline is in the numbers Summary() returns rather than in
// any sentence here. Nothing in this matrix reaches QUALIFIED, because
// QUALIFIED requires an outside party to have examined the control and
// no outside party has examined any of them. Recording that as thirty
// EXTERNAL_QUALIFICATION and ASSURANCE_GAP verdicts is the point: it
// distinguishes "we built it and proved it to ourselves" from "somebody
// else checked", which is the distinction every previous round's status
// language blurred.
//
// The refs are real package paths and real test names. A ref that named
// something not in the repository would make the matrix worse than
// having none, so ValidateMatrix and the package tests check the shape,
// and reviewers can check the substance by following them.

// noExternalAssessor is the dependency shared by every article: VERIQO
// has no relationship with an accredited assessor, auditor or
// certification body for evidence-handling controls, so no article can
// reach QUALIFIED regardless of how much more is built.
const noExternalAssessor = "an independent assessor with evidence-handling competence must examine the control; VERIQO has no such engagement"

// noProductionPath is the other recurring blocker. Several controls are
// implemented, called and tested, but VERIQO has never run a real matter
// end to end, so there is no production-path record to point at.
const noProductionPath = "no production deployment exists, so no production-path record of this control exists"

// runtimeEvidenceNote explains what a RuntimeEvidenceRef in this matrix
// is, and what it is not.
//
// The cited ids come from evidence/RUNTIME_EVIDENCE.json, which
// cmd/veriqo-runtime-evidence produces by actually executing the
// canonical evidence-to-decision chain. They are real records emitted by
// a real run, and TestEveryRuntimeEvidenceRefResolves fails if a row
// cites one the run did not emit.
//
// They are not production records. The run's evidence is a fixture: it
// demonstrates the control executes and leaves a trace, not that it
// behaves correctly on real commercial data. That distinction is the
// LIVE_DATA blocker, and it stays BLOCKED_EXTERNAL.
const runtimeEvidenceNote = "emitted by cmd/veriqo-runtime-evidence; see evidence/RUNTIME_EVIDENCE.json"

var matrix = []Trace{
	{Article: 1, Control: "Finding requires a sealed proof object with a pinned evidence set",
		Code: true, CodeRef: "veriqo/pkg/proof.NewFinding",
		Called: true, CalledRef: "veriqo/pkg/casefabric.Case.AttachProof",
		Test: true, TestRef: "TestSufficientObjectFoundsAFinding, TestUnpinnedEvidenceIsRefused",
		Evidence: true, EvidenceRef: "casefabric timeline entry kind=proof_attached",
		Replay: true, ReplayRef: "proof.VerifyHash re-derives the object from its components",
		RuntimeEvidence: true, RuntimeEvidenceRef: "AUDIT-009-case.proof_attached, AUDIT-010-proof.sealed (see evidence/RUNTIME_EVIDENCE.json)",
		Qualification: true, QualificationRef: "docs/VERIQO_RED_FLAG_RESPONSE_REPORT.md (AuthorizedFinding gate assessment)",
		ExternalDependency: noExternalAssessor},

	{Article: 2, Control: "Acquisition records provenance without conferring qualification",
		Code: true, CodeRef: "veriqo/pkg/evidence/provenance",
		Called: true, CalledRef: "veriqo/pkg/dataplatform/ingest",
		Test: true, TestRef: "pkg/evidence/provenance tests",
		Evidence: true, EvidenceRef: "audit event family evidence.acquired",
		Replay: false, ExternalDependency: noProductionPath},

	{Article: 3, Control: "Transitive source clustering: same-root data counts once",
		Code: true, CodeRef: "veriqo/pkg/qualification/independence.Cluster",
		Called: true, CalledRef: "independence.EffectiveSourceCount, proof.Object.Trust",
		Test: true, TestRef: "TestSharedUpstreamSourceCannotBecomeCorroborationByAnyRoute, TestUnknownIsNotCountedTowardsCorroboration (test/adversarial)",
		Evidence: false, ExternalDependency: noProductionPath},

	{Article: 4, Control: "Rights are evaluated before contact, not after",
		Code: true, CodeRef: "veriqo/pkg/authz",
		Called: true, CalledRef: "veriqo/pkg/qualification/nextbest.filter, connector guards",
		Test: true, TestRef: "TestRightsDeniedCandidateIsExcludedNotDownweighted",
		Evidence: true, EvidenceRef: "audit event family authz.decision",
		Replay: true, ReplayRef: "policy decisions are recorded with their policy version",
		Qualification: false, ExternalDependency: noExternalAssessor},

	{Article: 5, Control: "Raw bytes are preserved before any transformation",
		Code: true, CodeRef: "veriqo/pkg/evidence/manifest",
		Called: true, CalledRef: "veriqo/pkg/storage/evidence",
		Test: true, TestRef: "pkg/evidence/manifest tests",
		Evidence: true, EvidenceRef: "custody chain head on the evidence version",
		Replay: true, ReplayRef: "content hash re-derivable from stored raw",
		RuntimeEvidence: true, RuntimeEvidenceRef: "AUDIT-003-case.evidence_pinned (see evidence/RUNTIME_EVIDENCE.json)",
		Qualification: true, QualificationRef: "docs/VERIQO_TRUST_AUTHORITY_MODEL_RESPONSE.md INV-001..INV-010",
		ExternalDependency: noExternalAssessor},

	{Article: 6, Control: "A finalized version is structurally unupdatable",
		Code: true, CodeRef: "veriqo/pkg/evidence/manifest.Registry",
		Called: true, CalledRef: "veriqo/pkg/insurance/evidence",
		Test: true, TestRef: "manifest immutability and forged-state tests",
		Evidence: true, EvidenceRef: "custody event on finalization",
		Replay: true, ReplayRef: "veriqo/pkg/replay ManifestAdapter",
		RuntimeEvidence: true, RuntimeEvidenceRef: "AUDIT-003-case.evidence_pinned (see evidence/RUNTIME_EVIDENCE.json)",
		Qualification: true, QualificationRef: "docs/VERIQO_FINAL_AUTHORITY_HARDENING_RESPONSE.md (finalization freeze audit)",
		ExternalDependency: noExternalAssessor},

	{Article: 7, Control: "Historical cases resolve against their historical policy version",
		Code: true, CodeRef: "veriqo/pkg/governance/precedence",
		Called: true, CalledRef: "veriqo/pkg/authz policy resolution",
		Test: true, TestRef: "TestPolicyRetroactivityIsRefusedAndVisible (test/adversarial)",
		Evidence: false, ExternalDependency: noProductionPath},

	{Article: 8, Control: "AI cannot create, alter, qualify or sign evidence",
		Code: true, CodeRef: "veriqo/pkg/ai/gateway.ForbiddenActions",
		Called: true, CalledRef: "veriqo/pkg/ai/gateway.Evaluate",
		Test: true, TestRef: "TestForbiddenActionRefusedEvenWithEveryRight",
		Evidence: true, EvidenceRef: "every AI access emits an event, permitted or refused",
		Replay: true, ReplayRef: "gateway decisions record all ten check dimensions",
		Qualification: true, QualificationRef: "docs/VERIQO_RED_FLAG_RESPONSE_REPORT.md (AI authority boundary assessment)",
		ExternalDependency: noExternalAssessor},

	{Article: 9, Control: "A zero-knowledge proof establishes only its stated predicate",
		Code: false, ExternalDependency: "a ZKP prover and verifier are not built; the article is stated but nothing enforces it at runtime"},

	{Article: 10, Control: "Replay is verifiable without trusting the runtime",
		Code: true, CodeRef: "veriqo/pkg/replay",
		Called: true, CalledRef: "veriqo/pkg/verification, the standalone verifier",
		Test: true, TestRef: "test/acceptance/replay",
		Evidence: true, EvidenceRef: "replay package with verification certificate",
		Replay: true, ReplayRef: "veriqo/pkg/replay.Engine.Replay",
		Qualification: true, QualificationRef: "docs/VERIQO_CORE_TRUST_KERNEL_FREEZE.md, evidence/acceptance.txt",
		ExternalDependency: noExternalAssessor},

	{Article: 11, Control: "Dissent is carried through qualification, never deleted",
		Code: true, CodeRef: "veriqo/pkg/qualification/state.New",
		Called: true, CalledRef: "proof.Object.Qualification",
		Test: true, TestRef: "TestCriticalDissentCannotBeSuppressed, TestArticle11DissentSuppression (test/adversarial)",
		Evidence: false, ExternalDependency: noProductionPath},

	{Article: 12, Control: "The same policy applies to every party absent an authorized exception",
		Code: true, CodeRef: "veriqo/pkg/security/policy",
		Called: true, CalledRef: "veriqo/pkg/authz",
		Test: true, TestRef: "pkg/security/policy symmetry tests",
		Evidence: false, ExternalDependency: noProductionPath},

	{Article: 13, Control: "Party influence on acquisition is recorded",
		Code: true, CodeRef: "veriqo/pkg/qualification/nextbest.Candidate.PartyMediated",
		Called: true, CalledRef: "nextbest.filter",
		Test: true, TestRef: "TestPartyMediatedEvidenceCannotSatisfyIndependence (test/adversarial)",
		Evidence: false, ExternalDependency: noProductionPath},

	{Article: 14, Control: "Conflicts are declared rather than concealed",
		Code: true, CodeRef: "veriqo/pkg/governance/hitl",
		Called: true, CalledRef: "reviewer assignment",
		Test: true, TestRef: "TestUndeclaredConflictIsCaughtAndCommercialNeutralityIsNotFaked (test/adversarial)",
		Evidence: false, ExternalDependency: noProductionPath},

	{Article: 15, Control: "No differential benefit from a dispute outcome",
		Code: false, ExternalDependency: "this is a commercial-structure commitment, not a runtime control; it is attested in pkg/constitution and must be verified by examining VERIQO's contracts and remuneration, which only an outside party can do"},

	{Article: 16, Control: "The platform does not determine legal liability",
		Code: true, CodeRef: "veriqo/pkg/proof.ProhibitedDecisionFields, casefabric.Outcome.Validate",
		Called: true, CalledRef: "proof.Decide, casefabric.Case.Resolve",
		Test: true, TestRef: "TestDecisionMayNotAdjudicate, TestOutcomeMayNotAdjudicate",
		Evidence: true, EvidenceRef: "a refused adjudication is an error, not a logged warning",
		Replay: true, ReplayRef: "the state has no PROVEN or LIABLE value to replay into",
		RuntimeEvidence: true, RuntimeEvidenceRef: "AUDIT-013-case.resolved (see evidence/RUNTIME_EVIDENCE.json)",
		Qualification: true, QualificationRef: "docs/VERIQO_ROUND10_L99_LEVEL3_ASSESSMENT_REPORT.md (adjudication boundary)",
		ExternalDependency: noExternalAssessor},

	{Article: 17, Control: "Redaction never modifies the original",
		Code: true, CodeRef: "veriqo/pkg/evidence/manifest version lineage",
		Called: true, CalledRef: "derivative creation path",
		Test: true, TestRef: "manifest lineage tests",
		Evidence: true, EvidenceRef: "derivative recorded as a new version",
		Replay: true, ReplayRef: "original content hash unchanged across derivation",
		Qualification: true, QualificationRef: "docs/VERIQO_TRUST_AUTHORITY_MODEL_RESPONSE.md (lineage invariants)",
		ExternalDependency: noExternalAssessor},

	{Article: 18, Control: "Redacted content is absent from the derivative's bytes",
		Code: true, CodeRef: "veriqo/pkg/evidence/redaction.Verify (byte-level absence over twelve encodings), " +
			"driven by veriqo/pkg/evidence/redaction/worker (PDF, XLSX and PPTX workers)",
		// Called is now true, and this is what changed it: the workers
		// exist, they produce a real derivative from a real container,
		// and the pipeline verifies it before releasing anything.
		//
		// The check runs over the DECOMPRESSED content of the
		// container, not its bytes. That distinction is the whole
		// integration: PDF, XLSX and PPTX all deflate their content, so
		// a verifier pointed at the container would report absence for
		// a document where nothing had been removed -- a check that
		// cannot fail, which is worse than no check because it produces
		// a record saying it passed.
		//
		// The article moves INTEGRATION_GAP -> ASSURANCE_GAP. What is
		// still missing is named in ExternalDependency: nobody outside
		// VERIQO has tried to recover the redacted content.
		Called: true, CalledRef: "worker.Pipeline.Run, invoked by cmd/veriqo-runtime-evidence",
		Test: true, TestRef: "TestCompressionWouldHaveHiddenTheTerm, TestEachWorkerProducesAVerifiedDerivative, " +
			"TestAnEncryptedPDFIsRefusedNotWarned, TestABinaryAttachmentCarryingTheTermIsRefused, " +
			"TestTheCorpusRunMatchesItsDeclaredDesign, TestNoVariantLeaks, TestL3IsNotClaimed",
		Evidence: true, EvidenceRef: "worker.Release carries the redaction chain, the transformation manifest and a disclosure event",
		Replay: true, ReplayRef: "the derivative is deterministic: two runs over the same original produce identical bytes",
		RuntimeEvidence: true, RuntimeEvidenceRef: "AUDIT-014-redaction.derivative_released",
		// Qualification stays false, deliberately. "Assessed, not
		// merely run" would require somebody to have concluded the
		// control is complete, and it is not: the workers REFUSE the
		// structures where redacted content most plausibly survives
		// (incremental updates, object streams, encrypted documents)
		// rather than handling them. Refusing is the safe behaviour and
		// it is the right behaviour, but a control that declines a large
		// part of its own problem space has not been assessed as
		// adequate, and marking it so to reach a nicer verdict is exactly
		// the move this matrix exists to prevent.
		Qualification: false,
		ExternalDependency: "no adversarial recovery lab outside VERIQO has attempted reconstruction from " +
			"format-specific remnants, and the workers refuse rather than process the structures where such " +
			"remnants live (incremental updates, object streams, encrypted documents, undecoded stream filters); " +
			"refusing is safe but is not the same as having proven those structures can be redacted"},

	{Article: 19, Control: "VERIQO enforces privilege; it does not determine it",
		Code: true, CodeRef: "veriqo/pkg/disclosure/access.PrivilegeStatus",
		Called: true, CalledRef: "access.Evaluate, gateway.Evaluate",
		Test: true, TestRef: "TestPrivilegedMaterialDefaultDeniesToAI, privilege restriction tests",
		Evidence: true, EvidenceRef: "privilege transitions are events",
		Replay: false, ExternalDependency: noProductionPath},

	{Article: 20, Control: "View, export, AI processing and training are separate grants",
		Code: true, CodeRef: "veriqo/pkg/disclosure/access.Rights",
		Called: true, CalledRef: "access.Evaluate",
		Test: true, TestRef: "TestPrivilegedMaterialCannotReachAModelByAnyAIRight, TestThreeAIRightsAreSeparateGrants (test/adversarial)",
		Evidence: true, EvidenceRef: "every disclosure decision emits an event",
		Replay: true, ReplayRef: "decisions record the grant and policy version",
		Qualification: false, ExternalDependency: noExternalAssessor},

	{Article: 21, Control: "A redacted derivative must still pass AI policy",
		Code: true, CodeRef: "veriqo/pkg/ai/gateway.Evaluate",
		Called: true, CalledRef: "the gateway is the only AI entry point",
		Test: true, TestRef: "TestRedactedEvidenceIsNotAutomaticallyAISafe",
		Evidence: true, EvidenceRef: "AI access events",
		Replay: false, ExternalDependency: noProductionPath},

	{Article: 22, Control: "Every derivative is a new immutable version",
		Code: true, CodeRef: "veriqo/pkg/evidence/manifest",
		Called: true, CalledRef: "all derivative creation",
		Test: true, TestRef: "manifest version and supersession tests",
		Evidence: true, EvidenceRef: "version lineage in the custody chain",
		Replay: true, ReplayRef: "veriqo/pkg/lineage",
		RuntimeEvidence: true, RuntimeEvidenceRef: "AUDIT-003-case.evidence_pinned (see evidence/RUNTIME_EVIDENCE.json)",
		Qualification: true, QualificationRef: "docs/VERIQO_AUTHORITY_ROUND_2_CLOSURE_RESPONSE.md (MarkSuperseded lineage audit)",
		ExternalDependency: noExternalAssessor},

	{Article: 23, Control: "Process evidence is itself evidence",
		Code: true, CodeRef: "veriqo/pkg/platform/audit",
		Called: true, CalledRef: "veriqo/pkg/ontology.Registry.AttachAuditStore and every authority path",
		Test: true, TestRef: "test/adversarial constitutional suite",
		Evidence: true, EvidenceRef: "the audit ledger is the canonical record",
		Replay: true, ReplayRef: "veriqo/pkg/insurance/auditlink",
		RuntimeEvidence: true, RuntimeEvidenceRef: "AUDIT-001-case.opened through AUDIT-013-case.resolved (the whole run is in the one ledger, in constitutional order) (see evidence/RUNTIME_EVIDENCE.json)",
		Qualification: true, QualificationRef: "docs/VERIQO_AUTHORITY_ROUND_2_CLOSURE_RESPONSE.md (auditlink canonical authority)",
		ExternalDependency: noExternalAssessor},

	{Article: 24, Control: "Every disclosure emits a ledger event",
		Code: true, CodeRef: "veriqo/pkg/disclosure/access.Decision.EventRequired",
		Called: true, CalledRef: "access.Evaluate sets it on every decision, permitted or refused",
		Test: true, TestRef: "TestEveryAIAccessRequiresAnEvent",
		Evidence: true, EvidenceRef: "disclosure event family",
		Replay: false, ExternalDependency: noProductionPath},

	{Article: 25, Control: "Privilege transitions are immutable events",
		Code: true, CodeRef: "veriqo/pkg/disclosure/access",
		Called: true, CalledRef: "privilege change path",
		Test: true, TestRef: "privilege transition tests",
		Evidence: false, ExternalDependency: noProductionPath},

	{Article: 26, Control: "Policy change is never quietly applied to history",
		Code: true, CodeRef: "veriqo/pkg/governance/precedence",
		Called: true, CalledRef: "policy resolution",
		Test: true, TestRef: "TestPolicyRetroactivityIsRefusedAndVisible (test/adversarial)",
		Evidence: false, ExternalDependency: noProductionPath},

	{Article: 27, Control: "Material AI contribution is recorded and human-reviewed",
		Code: true, CodeRef: "veriqo/pkg/ai/gateway.NewContribution",
		Called: true, CalledRef: "proof.Object.AIAccess validation",
		Test: true, TestRef: "TestMaterialContributionRequiresHumanReviewer, TestMaterialAIContributionRequiresReviewer",
		Evidence: true, EvidenceRef: "contribution records pin prompt, output and evidence versions",
		Replay: true, ReplayRef: "contribution hashes are recomputable",
		Qualification: false, ExternalDependency: noExternalAssessor},

	{Article: 28, Control: "UNKNOWN independence is never treated as INDEPENDENT",
		Code: true, CodeRef: "veriqo/pkg/qualification/independence.Verdict",
		Called: true, CalledRef: "independence.Assess, proof sufficiency",
		Test: true, TestRef: "independence verdict tests, TestSharedUpstreamSourceCannotBecomeCorroborationByAnyRoute, TestUnknownIsNotCountedTowardsCorroboration",
		Evidence: false, ExternalDependency: noProductionPath},

	{Article: 29, Control: "OBSERVED_ABSENT only after the observability gate",
		Code: true, CodeRef: "veriqo/pkg/qualification/observability.AssertObservedAbsent",
		Called: true, CalledRef: "reverseproof.Requirement.AbsenceState",
		Test: true, TestRef: "observability gate tests, TestSourceOutageCannotBecomeAFindingAboutTheWorld (test/adversarial)",
		Evidence: false, ExternalDependency: noProductionPath},

	{Article: 30, Control: "Integrity, provenance, qualification, neutrality and legal determination stay distinct",
		Code: true, CodeRef: "veriqo/pkg/platform/timestamp (integrity vs attestation), pkg/proof (qualification vs decision)",
		Called: true, CalledRef: "timestamp.Assess, proof.Seal",
		Test: true, TestRef: "TestOnlyIndependentAttestationProvesExistenceBefore, TestDescribeNeverOverstates",
		Evidence: true, EvidenceRef: "attestation kind is derived, never asserted",
		Replay: true, ReplayRef: "timestamp.VerifyChain",
		Qualification: false, ExternalDependency: noExternalAssessor},
}

// Matrix returns the constitutional integration and proof audit.
func Matrix() []Trace { return append([]Trace(nil), matrix...) }

// Row is one assessed article.
type Row struct {
	Article int
	Title   string
	Class   string
	Control string
	Verdict Verdict
	// Explanation is the sentence a reader acts on.
	Explanation string
	// Err is set when the trace itself is malformed.
	Err error
}

// Assemble assesses every trace and joins it to the article it serves.
//
// It refuses to return a partial matrix: an article with no trace is
// reported as OPEN with an explanation, never omitted. A traceability
// matrix that silently skips the rows nobody filled in is the failure
// mode this whole exercise exists to prevent.
func Assemble() ([]Row, error) {
	byArticle := map[int]Trace{}
	for _, t := range matrix {
		if _, dup := byArticle[t.Article]; dup {
			return nil, fmt.Errorf("assurance: article %d has more than one trace", t.Article)
		}
		byArticle[t.Article] = t
	}

	arts := constitution.Articles()
	rows := make([]Row, 0, len(arts))
	for _, a := range arts {
		t, ok := byArticle[a.Number]
		if !ok {
			rows = append(rows, Row{
				Article: a.Number, Title: a.Title, Class: a.Class.String(), Verdict: Open,
				Explanation: fmt.Sprintf("Article %d, %s: OPEN. No control has been traced for this article.", a.Number, a.Title),
			})
			continue
		}
		v, err := Assess(t)
		rows = append(rows, Row{
			Article: a.Number, Title: a.Title, Class: a.Class.String(), Control: t.Control,
			Verdict: v, Explanation: Explain(t), Err: err,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Article < rows[j].Article })
	return rows, nil
}

// Summary counts the verdicts.
type Summary struct {
	Open                  int
	IntegrationGap        int
	AssuranceGap          int
	ExternalQualification int
	Qualified             int
	Total                 int
}

// Summarize counts the matrix.
func Summarize(rows []Row) Summary {
	s := Summary{Total: len(rows)}
	for _, r := range rows {
		switch r.Verdict {
		case Open:
			s.Open++
		case IntegrationGap:
			s.IntegrationGap++
		case AssuranceGap:
			s.AssuranceGap++
		case ExternalQualification:
			s.ExternalQualification++
		case Qualified:
			s.Qualified++
		}
	}
	return s
}

// Headline states the summary in the terms the architecture uses, and
// refuses to express it as a completion percentage.
func (s Summary) Headline() string {
	return fmt.Sprintf(
		"%d articles: %d OPEN, %d INTEGRATION_GAP, %d ASSURANCE_GAP, %d EXTERNAL_QUALIFICATION, %d QUALIFIED. "+
			"QUALIFIED requires an outside party to have examined the control.",
		s.Total, s.Open, s.IntegrationGap, s.AssuranceGap, s.ExternalQualification, s.Qualified)
}

// Render produces the matrix as a plain-text table, for reports and for
// anyone reading it in a terminal.
func Render(rows []Row) string {
	var b strings.Builder
	b.WriteString("ART  VERDICT                 CONTROL\n")
	b.WriteString(strings.Repeat("-", 100) + "\n")
	for _, r := range rows {
		control := r.Control
		if control == "" {
			control = "(no control traced)"
		}
		if len(control) > 66 {
			control = control[:63] + "..."
		}
		b.WriteString(fmt.Sprintf("%-4d %-23s %s\n", r.Article, r.Verdict, control))
	}
	b.WriteString(strings.Repeat("-", 100) + "\n")
	b.WriteString(Summarize(rows).Headline() + "\n")
	return b.String()
}

// --- The two axes, per capability ------------------------------------

// capabilities is VERIQO's status on both axes.
//
// Read the two columns against each other. Almost everything is high on
// the engineering axis and low on the assurance axis, and that shape —
// not any single figure — is VERIQO's actual position: a great deal has
// been built and proved internally; nothing has been examined by anyone
// else.
var capabilities = []Status{
	{"Evidence Constitution (30 executable articles)", AdversarialTested, InternallyProved,
		"no independent assessor has examined the article checks"},
	{"Epistemic Qualification Fabric", AdversarialTested, InternallyProved,
		"qualification methodology has not been reviewed by an evidential-standards body"},
	{"Proof Object and pipeline", AdversarialTested, InternallyProved,
		"no tribunal or expert has been asked whether a VERIQO proof object is admissible or useful"},
	{"Case Resolution Fabric", AdversarialTested, InternallyProved,
		"no real matter has been run through the fabric end to end"},
	{"Forward-Reverse Execution Fabric", UnitTested, InternallyProved,
		"the contract binds stages to packages; it has not been run against a production execution"},
	{"Trust and Evidence Control Plane", ReplayVerified, InternallyProved,
		"custody and ledger integrity have not been audited by an outside party"},
	{"Disclosure two-dimensional model", AdversarialTested, InternallyProved,
		"no counsel or court has confirmed the procedural/content model matches disclosure practice"},
	{"AI Evidence Gateway", AdversarialTested, InternallyProved,
		"no AI-governance auditor has examined the gateway"},
	{"Temporal attestation distinction", UnitTested, SelfAsserted,
		"VERIQO holds no TSA relationship, so no independent attestation has ever been obtained"},
	{"Replay and independent verification", ReplayVerified, InternallyProved,
		"the verifier has been run by VERIQO only; no third party has replayed a package"},
	{"Redaction assurance (byte-level absence)", Designed, Unproved,
		"the redaction workers and the adversarial recovery lab are not built"},
	{"Zero-knowledge proofs", Designed, Unproved,
		"no prover or verifier exists"},
	{"External source acquisition (IEAP)", Designed, Unproved,
		"real source agreements, licences and connectors do not exist"},
	{"Payment settlement", Implemented, SelfAsserted,
		"no bank rail is connected; settlement evidence has never been reconciled against a real payment"},
	{"Operational neutrality (Article 15)", Designed, Unproved,
		"a commercial commitment, verifiable only by examining VERIQO's contracts and remuneration"},
}

// Capabilities returns the two-axis status of every capability.
func Capabilities() []Status { return append([]Status(nil), capabilities...) }

// AxisReport renders both axes side by side, and states the gap between
// them in words rather than collapsing it into a figure.
func AxisReport() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%-48s %-20s %s\n", "CAPABILITY", "ENGINEERING", "ASSURANCE"))
	b.WriteString(strings.Repeat("-", 100) + "\n")
	engineeringComplete, externallyValidated := 0, 0
	for _, s := range capabilities {
		name := s.Capability
		if len(name) > 47 {
			name = name[:44] + "..."
		}
		b.WriteString(fmt.Sprintf("%-48s %-20s %s\n", name, s.Engineering, s.Assurance))
		if s.Engineering >= AdversarialTested {
			engineeringComplete++
		}
		if s.Assurance >= ExternallyValidated {
			externallyValidated++
		}
	}
	b.WriteString(strings.Repeat("-", 100) + "\n")
	b.WriteString(fmt.Sprintf(
		"%d of %d capabilities are adversarially tested or better on the engineering axis.\n"+
			"%d of %d have been examined by anyone outside VERIQO.\n"+
			"These are different axes and neither substitutes for the other.\n",
		engineeringComplete, len(capabilities), externallyValidated, len(capabilities)))
	return b.String()
}
