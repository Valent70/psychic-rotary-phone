package policy

import (
	"errors"
	"testing"
)

func TestNewHistoryRejectsEmptyPolicyID(t *testing.T) {
	if _, err := NewHistory(""); !errors.Is(err, ErrEmptyPolicyID) {
		t.Fatalf("expected ErrEmptyPolicyID, got %v", err)
	}
}

func TestAddOriginalVersion(t *testing.T) {
	h, _ := NewHistory("POL-1")
	v := Version{PolicyID: "POL-1", VersionID: "V1", Kind: KindOriginal, EffectiveFrom: 100}
	if err := h.Add(v); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if h.Count() != 1 {
		t.Fatalf("expected count 1, got %d", h.Count())
	}
}

func TestValidateRejectsOriginalWithSupersedes(t *testing.T) {
	v := Version{PolicyID: "POL-1", VersionID: "V1", Kind: KindOriginal, Supersedes: "V0"}
	if err := v.Validate(); !errors.Is(err, ErrOriginalNeedsNoSupersedes) {
		t.Fatalf("expected ErrOriginalNeedsNoSupersedes, got %v", err)
	}
}

func TestValidateRejectsNonOriginalWithoutSupersedes(t *testing.T) {
	v := Version{PolicyID: "POL-1", VersionID: "V2", Kind: KindEndorsement}
	if err := v.Validate(); !errors.Is(err, ErrNonOriginalNeedsSupersedes) {
		t.Fatalf("expected ErrNonOriginalNeedsSupersedes, got %v", err)
	}
}

func TestAddRejectsSupersedesUnknownVersion(t *testing.T) {
	h, _ := NewHistory("POL-1")
	v := Version{PolicyID: "POL-1", VersionID: "V2", Kind: KindEndorsement, Supersedes: "V1-NEVER-ADDED", EffectiveFrom: 100}
	if err := h.Add(v); !errors.Is(err, ErrSupersedesUnknown) {
		t.Fatalf("expected ErrSupersedesUnknown, got %v", err)
	}
}

func TestAddRejectsDuplicateVersionID(t *testing.T) {
	h, _ := NewHistory("POL-1")
	v := Version{PolicyID: "POL-1", VersionID: "V1", Kind: KindOriginal, EffectiveFrom: 100}
	h.Add(v)
	if err := h.Add(v); !errors.Is(err, ErrDuplicateVersionID) {
		t.Fatalf("expected ErrDuplicateVersionID, got %v", err)
	}
}

func TestAddRejectsMismatchedPolicyID(t *testing.T) {
	h, _ := NewHistory("POL-1")
	v := Version{PolicyID: "POL-OTHER", VersionID: "V1", Kind: KindOriginal, EffectiveFrom: 100}
	if err := h.Add(v); err == nil {
		t.Fatal("expected error for mismatched PolicyID")
	}
}

// TestEffectiveAtSelectsHistoricalVersionNotLatest is the P0 test
// blueprint §6 explicitly demands: an original policy plus a LATER
// endorsement exist, and the incident falls within the ORIGINAL's
// window, before the endorsement took effect. EffectiveAt must return
// the original, not the endorsement, even though the endorsement is
// the "latest" version.
func TestEffectiveAtSelectsHistoricalVersionNotLatest(t *testing.T) {
	h, _ := NewHistory("POL-1")
	original := Version{
		PolicyID: "POL-1", VersionID: "V1", Kind: KindOriginal,
		EffectiveFrom: 1000, EffectiveTo: 5000,
		Deductible: "USD 50,000",
	}
	endorsement := Version{
		PolicyID: "POL-1", VersionID: "V2", Kind: KindEndorsement, Supersedes: "V1",
		EffectiveFrom: 5000, // takes effect AFTER the incident below
		Deductible:    "USD 75,000",
	}
	if err := h.Add(original); err != nil {
		t.Fatalf("Add original: %v", err)
	}
	if err := h.Add(endorsement); err != nil {
		t.Fatalf("Add endorsement: %v", err)
	}

	incidentTick := uint64(3000) // inside the original's window, before the endorsement
	got, err := h.EffectiveAt(incidentTick)
	if err != nil {
		t.Fatalf("EffectiveAt: %v", err)
	}
	if got.VersionID != "V1" {
		t.Fatalf("expected the ORIGINAL version (V1) to be effective at tick %d, got %s (deductible %s) -- "+
			"using the latest version for a historical incident is exactly what blueprint §6 forbids",
			incidentTick, got.VersionID, got.Deductible)
	}

	// Sanity: an incident AFTER the endorsement's effective date must
	// resolve to the endorsement.
	got2, err := h.EffectiveAt(6000)
	if err != nil {
		t.Fatalf("EffectiveAt: %v", err)
	}
	if got2.VersionID != "V2" {
		t.Fatalf("expected V2 to be effective at tick 6000, got %s", got2.VersionID)
	}
}

func TestEffectiveAtReturnsErrorWhenNothingCovers(t *testing.T) {
	h, _ := NewHistory("POL-1")
	h.Add(Version{PolicyID: "POL-1", VersionID: "V1", Kind: KindOriginal, EffectiveFrom: 1000, EffectiveTo: 2000})
	if _, err := h.EffectiveAt(500); !errors.Is(err, ErrNoEffectiveVersion) {
		t.Fatalf("expected ErrNoEffectiveVersion for a tick before any version's window, got %v", err)
	}
	if _, err := h.EffectiveAt(2500); !errors.Is(err, ErrNoEffectiveVersion) {
		t.Fatalf("expected ErrNoEffectiveVersion for a tick after the only version's window closed, got %v", err)
	}
}

func TestEffectiveAtOpenEndedVersion(t *testing.T) {
	h, _ := NewHistory("POL-1")
	h.Add(Version{PolicyID: "POL-1", VersionID: "V1", Kind: KindOriginal, EffectiveFrom: 1000, EffectiveTo: 0})
	got, err := h.EffectiveAt(999999)
	if err != nil {
		t.Fatalf("EffectiveAt: %v", err)
	}
	if got.VersionID != "V1" {
		t.Fatalf("expected V1 (open-ended) to cover a far-future tick, got %s", got.VersionID)
	}
}

func TestLatestDoesNotEqualEffectiveAtForHistoricalIncident(t *testing.T) {
	h, _ := NewHistory("POL-1")
	h.Add(Version{PolicyID: "POL-1", VersionID: "V1", Kind: KindOriginal, EffectiveFrom: 1000, EffectiveTo: 5000})
	h.Add(Version{PolicyID: "POL-1", VersionID: "V2", Kind: KindRenewal, Supersedes: "V1", EffectiveFrom: 5000})

	latest, ok := h.Latest()
	if !ok || latest.VersionID != "V2" {
		t.Fatalf("expected Latest() to be V2, got %v ok=%v", latest.VersionID, ok)
	}
	historical, err := h.EffectiveAt(2000)
	if err != nil {
		t.Fatalf("EffectiveAt: %v", err)
	}
	if historical.VersionID == latest.VersionID {
		t.Fatal("Latest() and EffectiveAt(historical tick) must NOT coincide in this scenario -- that would mean the historical lookup silently used the latest version")
	}
}

func TestUnknownKindRejected(t *testing.T) {
	v := Version{PolicyID: "POL-1", VersionID: "V1", Kind: Kind("NOT_A_KIND")}
	if err := v.Validate(); !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("expected ErrUnknownKind, got %v", err)
	}
}

func TestEffectiveToBeforeFromRejected(t *testing.T) {
	v := Version{PolicyID: "POL-1", VersionID: "V1", Kind: KindOriginal, EffectiveFrom: 1000, EffectiveTo: 500}
	if err := v.Validate(); !errors.Is(err, ErrEffectiveToBeforeFrom) {
		t.Fatalf("expected ErrEffectiveToBeforeFrom, got %v", err)
	}
}
