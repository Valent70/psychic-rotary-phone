package evidence

import (
	"testing"

	"veriqo/pkg/evidence/provenance"
)

func mustRecord(t *testing.T) Record {
	t.Helper()
	ev := mustEvidence(t, "S1", "src", 100)
	r, err := New("CASE-1", ev, "PTY-001", OriginClaimant)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r
}

func TestCorroborationDefaultsToUnknown(t *testing.T) {
	r := mustRecord(t)
	if got := r.Corroboration(); got != CorroborationUnknown {
		t.Fatalf("Corroboration() = %q, want UNKNOWN for a freshly-constructed record", got)
	}
}

func TestCorroborationMirrorsStatusCorroboratedAndContradicted(t *testing.T) {
	r := mustRecord(t)

	r.Status = StatusCorroborated
	if got := r.Corroboration(); got != CorroborationCorroborated {
		t.Fatalf("Corroboration() = %q, want CORROBORATED", got)
	}

	r.Status = StatusContradicted
	if got := r.Corroboration(); got != CorroborationContradicted {
		t.Fatalf("Corroboration() = %q, want CONTRADICTED", got)
	}
}

func TestCorroborationReportsSuperseded(t *testing.T) {
	r := mustRecord(t)
	r.Status = StatusCorroborated // even a corroborated record is superseded once replaced
	r.CorrectionSuperseded = true
	r.SupersededBy = "EV-NEWER"
	if got := r.Corroboration(); got != CorroborationSuperseded {
		t.Fatalf("Corroboration() = %q, want SUPERSEDED (supersession must outrank a stale CORROBORATED status)", got)
	}
}

func TestCorroborationReportsRevokedAboveEverythingElse(t *testing.T) {
	r := mustRecord(t)
	r.Status = StatusCorroborated
	r.CorrectionSuperseded = true
	r.Rights = provenance.RightsRevoked
	if got := r.Corroboration(); got != CorroborationRevoked {
		t.Fatalf("Corroboration() = %q, want REVOKED (revocation must outrank supersession and status)", got)
	}
}

func TestCorroborationIsPureAndDeterministic(t *testing.T) {
	r := mustRecord(t)
	r.Status = StatusCorroborated
	a := r.Corroboration()
	b := r.Corroboration()
	if a != b {
		t.Fatalf("Corroboration() diverged across two calls on the same record: %q vs %q", a, b)
	}
}

func TestKnownCorroborationStatusesExhaustive(t *testing.T) {
	want := []CorroborationStatus{
		CorroborationContradicted, CorroborationCorroborated, CorroborationRevoked,
		CorroborationSuperseded, CorroborationUnknown,
	}
	got := KnownCorroborationStatuses()
	if len(got) != len(want) {
		t.Fatalf("expected %d known corroboration statuses, got %d: %v", len(want), len(got), got)
	}
	for _, s := range want {
		if !IsKnownCorroborationStatus(s) {
			t.Fatalf("expected %q to be a known corroboration status", s)
		}
	}
}
