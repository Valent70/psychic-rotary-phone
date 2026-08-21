package coverage

import (
	"testing"

	"veriqo/pkg/evidence/ontology"
	"veriqo/pkg/insurance/claim"
	"veriqo/pkg/insurance/evidence"
	"veriqo/pkg/insurance/party"
	"veriqo/pkg/insurance/policy"
)

// mustRecord builds a real, content-addressed evidence.Record (via a
// real ontology.Evidence, exactly like pkg/insurance/evidence's own
// tests do) with the given DocumentType and verification Status. subj
// must be unique per call within a test so distinct records get
// distinct content-addressed EvidenceIDs.
func mustRecord(t *testing.T, subj, docType string, status evidence.Status) evidence.Record {
	t.Helper()
	ev, err := ontology.New(ontology.Evidence{
		Type:       ontology.TypeDocument,
		Subject:    subj,
		Predicate:  "describes",
		Object:     "cargo_condition",
		Source:     "src-" + subj,
		ObservedAt: 1,
		Confidence: 0.9,
		Attributes: map[string]string{"document_hash": "hash-" + subj},
	})
	if err != nil {
		t.Fatalf("ontology.New(%s): %v", subj, err)
	}
	rec, err := evidence.New("CASE-1", ev, party.PartyID("PTY-CLAIMANT"), evidence.OriginClaimant)
	if err != nil {
		t.Fatalf("evidence.New(%s): %v", subj, err)
	}
	rec.DocumentType = docType
	rec.Status = status
	return rec
}

// baseClaim returns a minimal, valid claim.Claim for CASE-1.
func baseClaim(policyVersionID string) claim.Claim {
	return claim.Claim{
		ClaimID:         "CLM-1",
		CaseID:          "CASE-1",
		Type:            claim.TypeCargoDamage,
		ClaimantPartyID: party.PartyID("PTY-CLAIMANT"),
		Status:          claim.StatusRegistered,
		PolicyVersionID: policyVersionID,
	}
}

// basePolicyVersion returns a minimal, valid effective policy.Version
// covering ticks [1000, 3000).
func basePolicyVersion(versionID string) policy.Version {
	return policy.Version{
		PolicyID:      "POL-1",
		VersionID:     versionID,
		PolicyNumber:  "PN-001",
		Insurer:       "Test Insurer",
		Insured:       "Test Insured",
		EffectiveFrom: 1000,
		EffectiveTo:   3000,
		Kind:          policy.KindOriginal,
		Clauses: []policy.Clause{
			{ClauseID: "§2", DocumentID: "DOC-1", SourceHash: "h2", TextSpan: "policy period clause", Version: "V1"},
			{ClauseID: "§4", DocumentID: "DOC-1", SourceHash: "h4", TextSpan: "insured perils clause", Version: "V1"},
			{ClauseID: "§8", DocumentID: "DOC-1", SourceHash: "h8", TextSpan: "exclusions clause", Version: "V1"},
			{ClauseID: "§11", DocumentID: "DOC-1", SourceHash: "h11", TextSpan: "notice clause", Version: "V1"},
			{ClauseID: "§14", DocumentID: "DOC-1", SourceHash: "h14", TextSpan: "quantum clause", Version: "V1"},
		},
	}
}
