package canonical

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"veriqo/pkg/evidence/ontology"
	"veriqo/pkg/evidence/provenance"
	"veriqo/pkg/governance/envelope"
	insevidence "veriqo/pkg/insurance/evidence"
	"veriqo/pkg/insurance/party"
	"veriqo/pkg/insurance/policy"
	"veriqo/pkg/lineage"
	"veriqo/pkg/platform/correlation"
)

func mustRecord(t *testing.T, caseID, source string, observedAt uint64) insevidence.Record {
	t.Helper()
	ev, err := ontology.New(ontology.Evidence{
		Type:       ontology.TypeDocument,
		Subject:    "CARGO-1",
		Predicate:  "describes",
		Object:     "cargo_condition",
		Source:     source,
		ObservedAt: observedAt,
		Confidence: 0.9,
		Attributes: map[string]string{"document_hash": "deadbeef"},
	})
	if err != nil {
		t.Fatalf("ontology.New: %v", err)
	}
	rec, err := insevidence.New(caseID, ev, "PTY-1", insevidence.OriginSurveyor)
	if err != nil {
		t.Fatalf("insevidence.New: %v", err)
	}
	return rec
}

func TestNewRefusesNilLedgerAndEmptyCase(t *testing.T) {
	if _, err := New(nil, "CASE-INS-001"); !errors.Is(err, ErrNilLedger) {
		t.Fatalf("expected ErrNilLedger, got %v", err)
	}
	if _, err := New(lineage.NewLedger(), ""); !errors.Is(err, ErrEmptyCase) {
		t.Fatalf("expected ErrEmptyCase, got %v", err)
	}
}

// TestInsuranceCaseIDIsTheLineageCaseID: one investigation, one CaseID.
func TestInsuranceCaseIDIsTheLineageCaseID(t *testing.T) {
	b, err := New(lineage.NewLedger(), "CASE-INS-001")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if string(b.CaseID()) != "CASE-INS-001" {
		t.Fatalf("CaseID = %q, want the insurance case id verbatim", b.CaseID())
	}
}

// TestAttachEvidenceUsesTheContentAddressedID proves the lineage node's
// Ref is the underlying ontology evidence ID, not a fresh identifier
// this package minted.
func TestAttachEvidenceUsesTheContentAddressedID(t *testing.T) {
	l := lineage.NewLedger()
	b, err := New(l, "CASE-INS-001")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec := mustRecord(t, "CASE-INS-001", "surveyor-report", 100)
	n, err := b.AttachEvidence(rec, 10)
	if err != nil {
		t.Fatalf("AttachEvidence: %v", err)
	}
	if n.Ref != rec.Underlying.EvidenceID {
		t.Fatalf("node Ref = %q, want the underlying ontology EvidenceID %q", n.Ref, rec.Underlying.EvidenceID)
	}
	if n.Kind != lineage.KindEvidence {
		t.Fatalf("node Kind = %q, want EVIDENCE", n.Kind)
	}
	if err := b.VerifyChain(); err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
}

func TestAttachEvidenceRefusesAForeignCase(t *testing.T) {
	b, err := New(lineage.NewLedger(), "CASE-INS-001")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec := mustRecord(t, "CASE-INS-999", "surveyor-report", 100)
	if _, err := b.AttachEvidence(rec, 10); !errors.Is(err, ErrCaseMismatch) {
		t.Fatalf("expected ErrCaseMismatch, got %v", err)
	}
}

// TestAttachPartyRequiresACanonicalEntityRef proves an insurance
// PartyID can never become a lineage ENTITY on its own — that would be
// a second identity authority.
func TestAttachPartyRequiresACanonicalEntityRef(t *testing.T) {
	b, err := New(lineage.NewLedger(), "CASE-INS-001")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := b.AttachParty(party.Party{PartyID: "PTY-1", Name: "Fictional Carrier"}, 5); !errors.Is(err, ErrNoEntityRef) {
		t.Fatalf("expected ErrNoEntityRef, got %v", err)
	}
	n, err := b.AttachParty(party.Party{PartyID: "PTY-1", Name: "Fictional Carrier", EntityRef: "ENT-CANONICAL-1"}, 5)
	if err != nil {
		t.Fatalf("AttachParty: %v", err)
	}
	if n.Ref != "ENT-CANONICAL-1" {
		t.Fatalf("ENTITY node Ref = %q, want the canonical entity ref", n.Ref)
	}
}

func TestAttachPolicyVersionUsesTheResolvedVersionID(t *testing.T) {
	b, err := New(lineage.NewLedger(), "CASE-INS-001")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := b.AttachPolicyVersion(policy.Version{}, 5); err == nil {
		t.Fatal("a policy node with no VersionID must be refused")
	}
	n, err := b.AttachPolicyVersion(policy.Version{PolicyID: "POL-1", VersionID: "POL-1-V2"}, 5)
	if err != nil {
		t.Fatalf("AttachPolicyVersion: %v", err)
	}
	if n.Ref != "POL-1-V2" {
		t.Fatalf("POLICY node Ref = %q, want POL-1-V2", n.Ref)
	}
}

// TestDanglingUpstreamIsRefused: a lineage with a hole in it is not a
// lineage. This is pkg/lineage's own rule, and this test proves the
// insurance binding inherits it rather than routing around it.
func TestDanglingUpstreamIsRefused(t *testing.T) {
	b, err := New(lineage.NewLedger(), "CASE-INS-001")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := b.AttachEvent("EVT-1", 10, "an-evidence-id-that-was-never-registered"); err == nil {
		t.Fatal("expected a dangling-upstream refusal")
	}
}

func TestBindCorrelationRegistersTheRealIdentifiers(t *testing.T) {
	b, err := New(lineage.NewLedger(), "CASE-INS-001")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	k := correlation.Key{
		IntentID:                  "INT-1",
		ExecutionID:               "EXE-1",
		EvidencePackageID:         "EVP-1",
		EntityID:                  "ENT-1",
		DecisionID:                "DEC-1",
		ReplayPackageID:           "RPL-1",
		VerificationCertificateID: "VC-1",
	}
	nodes, err := b.BindCorrelation(k, 1)
	if err != nil {
		t.Fatalf("BindCorrelation: %v", err)
	}
	if len(nodes) == 0 {
		t.Fatal("BindCorrelation registered no nodes")
	}
	seen := map[string]bool{}
	for _, n := range nodes {
		seen[n.Ref] = true
	}
	for _, want := range []string{"INT-1", "ENT-1", "EVP-1", "DEC-1", "RPL-1", "VC-1"} {
		if !seen[want] {
			t.Fatalf("correlation identifier %q was not registered on the case lineage", want)
		}
	}
	if err := b.VerifyChain(); err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
}

// TestCompletenessIsDerivedNotDeclared: a partially-built insurance case
// honestly reports Complete=false, and there is no way to say otherwise.
func TestCompletenessIsDerivedNotDeclared(t *testing.T) {
	b, err := New(lineage.NewLedger(), "CASE-INS-001")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec := mustRecord(t, "CASE-INS-001", "surveyor-report", 100)
	if _, err := b.AttachEvidence(rec, 10); err != nil {
		t.Fatalf("AttachEvidence: %v", err)
	}
	comp := b.Completeness()
	if comp.Complete {
		t.Fatal("a case holding only one evidence node must not report Complete")
	}
	if len(comp.MissingKinds) == 0 {
		t.Fatal("Completeness must name what is missing")
	}
}

// ---- External evidence: the three fail-closed doors -----------------

func fixtureEnvelope() envelope.Envelope {
	arts := []envelope.Artifact{{Name: "report.json", Hash: strings.Repeat("a", 64), Bytes: 12}}
	return envelope.Envelope{
		ContractVersion:  envelope.ContractVersion,
		GateID:           "live_data",
		Release:          "v1.0.0",
		Commit:           "abcdef1",
		SourceHash:       strings.Repeat("b", 64),
		BinaryHash:       strings.Repeat("c", 64),
		SBOMHash:         strings.Repeat("d", 64),
		Environment:      "ci-sandbox",
		Measurement:      map[string]string{"records": "1"},
		Artifacts:        arts,
		ArtifactRootHash: envelope.ArtifactRoot(arts),
		ProviderID:       "PROV-1",
		ReviewerID:       "REV-1",
		ValidFrom:        1,
		ValidUntil:       1000,
		Limitations:      []string{"synthetic fixture; establishes nothing about a real feed"},
		OriginKind:       provenance.OriginSynthetic,
		RightsState:      provenance.RightsInternalOnly,
		Attestation:      provenance.AttestationSelfAsserted,
		Classification:   envelope.ClassificationFixture,
	}
}

func TestExternalEvidenceRequiresAnEnvelope(t *testing.T) {
	b, err := New(lineage.NewLedger(), "CASE-INS-001")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec := mustRecord(t, "CASE-INS-001", "port-feed", 100)
	if _, err := b.AttachExternalEvidence(rec, envelope.Envelope{}, provenance.UseInternalOnly, 10); !errors.Is(err, ErrEnvelopeRequired) {
		t.Fatalf("expected ErrEnvelopeRequired, got %v", err)
	}
}

// TestFixtureEnvelopeCannotEnterACaseAsExternalEvidence is the
// "jangan mencampur synthetic dengan live data" rule, enforced rather
// than documented: a FIXTURE-classified envelope is refused outright.
func TestFixtureEnvelopeCannotEnterACaseAsExternalEvidence(t *testing.T) {
	b, err := New(lineage.NewLedger(), "CASE-INS-001")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec := mustRecord(t, "CASE-INS-001", "port-feed", 100)
	env := fixtureEnvelope()
	if !env.IsFixture() {
		t.Fatal("test setup: expected a fixture envelope")
	}
	if _, err := b.AttachExternalEvidence(rec, env, provenance.UseInternalOnly, 10); !errors.Is(err, ErrFixtureNotExternal) {
		t.Fatalf("expected ErrFixtureNotExternal, got %v", err)
	}
}

func TestExternalEvidenceRefusesRightsThatDoNotPermitTheUse(t *testing.T) {
	b, err := New(lineage.NewLedger(), "CASE-INS-001")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec := mustRecord(t, "CASE-INS-001", "port-feed", 100)
	rec.Rights = provenance.RightsRevoked

	env := fixtureEnvelope()
	env.Classification = envelope.ClassificationLive
	env.OriginKind = provenance.OriginRealExternalAuthorized
	env.Attestation = provenance.AttestationThirdPartyAttested
	env.RightsState = provenance.RightsDisputeUseAllowed
	env.Limitations = nil

	if _, err := b.AttachExternalEvidence(rec, env, provenance.UseDispute, 10); !errors.Is(err, ErrRightsDeny) {
		t.Fatalf("expected ErrRightsDeny for a REVOKED record, got %v", err)
	}
}

func TestExternalEvidenceAttachesEnvelopeAndRecordTogether(t *testing.T) {
	b, err := New(lineage.NewLedger(), "CASE-INS-001")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec := mustRecord(t, "CASE-INS-001", "port-feed", 100)
	rec.Rights = provenance.RightsDisputeUseAllowed

	env := fixtureEnvelope()
	env.Classification = envelope.ClassificationLive
	env.OriginKind = provenance.OriginRealExternalAuthorized
	env.Attestation = provenance.AttestationThirdPartyAttested
	env.RightsState = provenance.RightsDisputeUseAllowed
	env.Limitations = nil

	nodes, err := b.AttachExternalEvidence(rec, env, provenance.UseDispute, 10)
	if err != nil {
		t.Fatalf("AttachExternalEvidence: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("expected an envelope node and an evidence node, got %d", len(nodes))
	}
	if nodes[0].Ref != env.ID() {
		t.Fatalf("first node Ref = %q, want the envelope's content-addressed ID", nodes[0].Ref)
	}
	if len(nodes[1].Upstream) != 1 || nodes[1].Upstream[0] != env.ID() {
		t.Fatalf("the evidence node must declare the envelope as upstream, got %v", nodes[1].Upstream)
	}
	if err := b.VerifyChain(); err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
}

// ---- The forbidden-construct scan -----------------------------------

// forbiddenNames are the constructs both design documents name
// explicitly as things that must never be created. The scan below is
// over the whole insurance tree, not just this package, because the
// prohibition is about the domain, not about one file.
var forbiddenNames = []string{
	"InsuranceIdentity",
	"InsuranceEvidenceStore",
	"InsuranceReplayEngine",
	"InsuranceDecisionEngine",
	"InsuranceEvidenceEngine",
	"InsuranceTrustEngine",
	"InsuranceCorrelationKey",
	"InsurancePolicyRegistry",
	"InsuranceProvenance",
}

// TestNoForbiddenCanonicalDuplicateExists parses every non-test Go file
// under pkg/insurance and asserts that no top-level type, function or
// variable carries one of the forbidden names. It reads the source
// rather than trusting a comment, so the guarantee survives a future
// author who has not read the design documents.
func TestNoForbiddenCanonicalDuplicateExists(t *testing.T) {
	root := filepath.Join("..", "..", "insurance")
	fset := token.NewFileSet()
	var offenders []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				checkForbidden(&offenders, path, d.Name.Name)
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						checkForbidden(&offenders, path, s.Name.Name)
					case *ast.ValueSpec:
						for _, n := range s.Names {
							checkForbidden(&offenders, path, n.Name)
						}
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scanning pkg/insurance: %v", err)
	}
	if len(offenders) > 0 {
		t.Fatalf("forbidden canonical duplicates declared in pkg/insurance:\n  %s", strings.Join(offenders, "\n  "))
	}
}

func checkForbidden(offenders *[]string, path, name string) {
	for _, bad := range forbiddenNames {
		if name == bad {
			*offenders = append(*offenders, path+": "+name)
		}
	}
}

// TestInsuranceDeclaresNoSecondRightsVocabulary proves the insurance
// tree does not declare its own RightsState-shaped enum. The functional
// spec §22 lists rights values in different words from
// pkg/evidence/provenance's; adopting those words as a parallel Go type
// would be exactly the duplicate provenance model §3 forbids.
func TestInsuranceDeclaresNoSecondRightsVocabulary(t *testing.T) {
	root := filepath.Join("..", "..", "insurance")
	fset := token.NewFileSet()
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				if ts.Name.Name == "RightsState" || ts.Name.Name == "Rights" {
					offenders = append(offenders, path+": "+ts.Name.Name)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scanning pkg/insurance: %v", err)
	}
	if len(offenders) > 0 {
		t.Fatalf("pkg/insurance declares its own rights vocabulary instead of consuming "+
			"pkg/evidence/provenance.RightsState:\n  %s", strings.Join(offenders, "\n  "))
	}
}
