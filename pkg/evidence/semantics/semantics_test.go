package semantics

import (
	"testing"

	"veriqo/pkg/canonical"
	"veriqo/pkg/evidence/ontology"
	"veriqo/pkg/moat/contradiction"
	"veriqo/pkg/moat/fusion"
	"veriqo/pkg/moat/kg"
)

// TestFromOntologyIsLossless proves the ontology projection carries
// forward exactly the real fields the source record has -- no
// fabrication, no silent drop.
func TestFromOntologyIsLossless(t *testing.T) {
	e := ontology.Evidence{
		EvidenceID: "ev-123", Type: ontology.TypeObservation,
		Subject: "VESSEL:IMO9999999", Predicate: "FLAG_STATE", Object: "PANAMA",
		Source: "sat-provider-x", ObservedAt: 42, Confidence: 0.87,
	}
	v := FromOntology(e)
	if v.Identity != e.EvidenceID {
		t.Fatalf("Identity = %q, want %q", v.Identity, e.EvidenceID)
	}
	if v.ClaimKey != "VESSEL:IMO9999999|FLAG_STATE" {
		t.Fatalf("ClaimKey = %q, want subject|predicate form", v.ClaimKey)
	}
	if v.Value != e.Object || v.Source != e.Source || v.Reliability != e.Confidence || v.Tick != e.ObservedAt {
		t.Fatalf("projection lost or altered a real field: %+v vs source %+v", v, e)
	}
	if v.OriginKind != OriginOntology {
		t.Fatalf("OriginKind = %q, want %q", v.OriginKind, OriginOntology)
	}
}

// TestFromFusionIsLosslessAndAgreesWithClaimKey proves the fusion
// projection over a REAL fusion.Evidence (produced by the real
// Engine.Submit, not a hand-built fixture) is faithful, and that its
// ClaimKey matches the SAME fusion.Claim.Key() a live caller would
// compute -- the two must never silently drift apart.
func TestFromFusionIsLosslessAndAgreesWithClaimKey(t *testing.T) {
	eng := fusion.NewEngine(kg.NewGraph())
	if err := eng.RegisterSource(fusion.SourceProfile{ID: "src-a", BaseReliability: 0.8}); err != nil {
		t.Fatalf("RegisterSource: %v", err)
	}
	claim := fusion.Claim{Subject: "VESSEL:IMO8888888", Predicate: "AIS_POSITION"}
	fe, err := eng.Submit("src-a", claim, "singapore", 7)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	v := FromFusion(fe)
	if v.Identity != fe.ID {
		t.Fatalf("Identity = %q, want %q", v.Identity, fe.ID)
	}
	if v.ClaimKey != claim.Key() {
		t.Fatalf("ClaimKey = %q, want %q (must agree with a live fusion.Claim.Key())", v.ClaimKey, claim.Key())
	}
	if v.Value != fe.Value || v.Source != string(fe.Source) || v.Reliability != fe.Reliability || v.Tick != fe.Tick {
		t.Fatalf("projection lost or altered a real field: %+v vs source %+v", v, fe)
	}
	if v.OriginKind != OriginFusion {
		t.Fatalf("OriginKind = %q, want %q", v.OriginKind, OriginFusion)
	}
}

// TestFromContradictionIsLossless proves the contradiction projection
// is faithful over a real contradiction.Evidence value (RawObservation
// + a decayed Weight, exactly the shape the arbitration engine itself
// produces).
func TestFromContradictionIsLossless(t *testing.T) {
	e := contradiction.Evidence{
		RawObservation: contradiction.RawObservation{
			ClaimKey: "VESSEL:IMO7777777|SANCTIONS_STATUS", SourceID: "port-authority",
			Value: "CLEAR", SourceReliability: 0.9, Tick: 12,
		},
		Weight: 0.81, // a real decayed value, not equal to SourceReliability -- proves Reliability reads Weight, not the raw field
	}
	v := FromContradiction(e)
	if v.ClaimKey != e.ClaimKey || v.Value != e.Value || v.Source != e.SourceID || v.Tick != e.Tick {
		t.Fatalf("projection lost or altered a real field: %+v vs source %+v", v, e)
	}
	if v.Reliability != e.Weight {
		t.Fatalf("Reliability = %v, want the DECAYED Weight %v, not the raw SourceReliability %v",
			v.Reliability, e.Weight, e.SourceReliability)
	}
	if v.OriginKind != OriginContradiction {
		t.Fatalf("OriginKind = %q, want %q", v.OriginKind, OriginContradiction)
	}
}

// TestFromSourceSubmissionIsLosslessAndAgreesWithRunCanonicalsOwnClaim
// proves the source-submission projection is faithful, and that its
// ClaimKey matches EXACTLY what pkg/canonical.RunCanonical itself
// builds internally (claim := fusion.Claim{Subject: in.Subject,
// Predicate: in.Predicate}) for the same CaseInput -- so a view built
// from a submission a caller is about to pass to RunCanonical already
// carries the same claim identity RunCanonical will use.
func TestFromSourceSubmissionIsLosslessAndAgreesWithRunCanonicalsOwnClaim(t *testing.T) {
	in := canonical.CaseInput{
		Entity: "VESSEL:IMO6666666", Subject: "VESSEL:IMO6666666", Predicate: "BENEFICIAL_OWNER",
	}
	sub := canonical.SourceSubmission{SourceID: "registry-feed", Value: "ACME HOLDINGS", BaseReliability: 0.7}

	v := FromSourceSubmission(sub, in.Subject, in.Predicate)
	wantClaimKey := fusion.Claim{Subject: in.Subject, Predicate: in.Predicate}.Key()
	if v.ClaimKey != wantClaimKey {
		t.Fatalf("ClaimKey = %q, want %q (must agree with RunCanonical's own internal claim)", v.ClaimKey, wantClaimKey)
	}
	if v.Value != sub.Value || v.Source != string(sub.SourceID) || v.Reliability != sub.BaseReliability {
		t.Fatalf("projection lost or altered a real field: %+v vs source %+v", v, sub)
	}
	if v.Identity != "" {
		t.Fatalf("Identity = %q, want empty -- SourceSubmission assigns no per-evidence ID of its own", v.Identity)
	}
	if v.OriginKind != OriginSourceSubmission {
		t.Fatalf("OriginKind = %q, want %q", v.OriginKind, OriginSourceSubmission)
	}
}

// TestOriginKindsAreAllDistinctAndAlwaysSet proves every projection
// honestly discloses which of the four real subsystems it came from,
// and that no two origins share a label (a consumer joining views from
// several subsystems must be able to tell them apart).
func TestOriginKindsAreAllDistinctAndAlwaysSet(t *testing.T) {
	kinds := []OriginKind{OriginOntology, OriginFusion, OriginContradiction, OriginSourceSubmission}
	seen := map[OriginKind]bool{}
	for _, k := range kinds {
		if k == "" {
			t.Fatal("an OriginKind constant is empty")
		}
		if seen[k] {
			t.Fatalf("OriginKind %q is not unique", k)
		}
		seen[k] = true
	}
}
