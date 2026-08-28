package inference

import (
	"errors"
	"testing"

	"veriqo/pkg/governance/lifecycle"
	"veriqo/pkg/platform/audit"
)

func activeModel(t *testing.T, reg *lifecycle.Registry, modelID, version string, tick uint64) string {
	t.Helper()
	m := lifecycle.Model{ModelID: modelID, Version: version, Type: "classifier", ParametersHash: "p", TrainingDataHash: "d"}
	ev, err := reg.RegisterModel(m, "actor", tick)
	if err != nil {
		t.Fatalf("RegisterModel: %v", err)
	}
	key := ev.Key
	if err := reg.SetCalibration(key, "calib-1"); err != nil {
		t.Fatalf("SetCalibration: %v", err)
	}
	if _, err := reg.TransitionModel(key, lifecycle.ModelValidated, "actor", "", "", tick, nil); err != nil {
		t.Fatalf("-> VALIDATED: %v", err)
	}
	if _, err := reg.TransitionModel(key, lifecycle.ModelCalibrated, "actor", "", "", tick, nil); err != nil {
		t.Fatalf("-> CALIBRATED: %v", err)
	}
	if _, err := reg.TransitionModel(key, lifecycle.ModelApproved, "actor", "approver-1", "looks good", tick, nil); err != nil {
		t.Fatalf("-> APPROVED: %v", err)
	}
	if _, err := reg.TransitionModel(key, lifecycle.ModelActive, "actor", "approver-1", "go live", tick, nil); err != nil {
		t.Fatalf("-> ACTIVE: %v", err)
	}
	return key
}

func TestNewRecorderPanicsOnNilRegistry(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected NewRecorder(nil) to panic")
		}
	}()
	NewRecorder(nil)
}

func TestRecordRefusesUnknownModel(t *testing.T) {
	reg := lifecycle.NewRegistry()
	r := NewRecorder(reg)
	_, err := r.Record("nonexistent@v1", "inputhash", "output", 0.9, "actor", "purpose", 1)
	if !errors.Is(err, ErrModelNotActive) {
		t.Fatalf("expected ErrModelNotActive, got %v", err)
	}
}

func TestRecordRefusesDraftModel(t *testing.T) {
	reg := lifecycle.NewRegistry()
	ev, err := reg.RegisterModel(lifecycle.Model{ModelID: "m1", Version: "v1", Type: "x"}, "actor", 1)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRecorder(reg)
	_, err = r.Record(ev.Key, "inputhash", "output", 0.9, "actor", "", 1)
	if !errors.Is(err, ErrModelNotActive) {
		t.Fatalf("expected ErrModelNotActive for a DRAFT model, got %v", err)
	}
}

func TestRecordAcceptsActiveModel(t *testing.T) {
	reg := lifecycle.NewRegistry()
	key := activeModel(t, reg, "m1", "v1", 1)
	r := NewRecorder(reg)
	trace, err := r.Record(key, "inputhash", "the model says X", 0.87, "cre-causation-engine", "case_resolution", 1)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if trace.ModelKey != key {
		t.Fatalf("expected ModelKey %s, got %s", key, trace.ModelKey)
	}
	if trace.TraceID == "" || trace.Hash == "" {
		t.Fatalf("expected non-empty TraceID/Hash, got %v", trace)
	}
	if err := VerifyTraceHash(trace); err != nil {
		t.Fatalf("VerifyTraceHash: %v", err)
	}
}

func TestRecordRefusesModelDeprecatedAtLaterTick(t *testing.T) {
	reg := lifecycle.NewRegistry()
	key1 := activeModel(t, reg, "m1", "v1", 1)
	// A second version supersedes the first at tick 10, deprecating v1.
	key2 := activeModel(t, reg, "m1", "v2", 10)
	r := NewRecorder(reg)

	// v1 was active at tick 1.
	if _, err := r.Record(key1, "inputhash", "output", 0.9, "actor", "", 1); err != nil {
		t.Fatalf("expected v1 to be recordable at tick 1: %v", err)
	}
	// v1 is deprecated by tick 10 (superseded by v2); v2 is active instead.
	if _, err := r.Record(key1, "inputhash", "output", 0.9, "actor", "", 10); !errors.Is(err, ErrModelNotActive) {
		t.Fatalf("expected v1 to be refused at tick 10 (deprecated), got %v", err)
	}
	if _, err := r.Record(key2, "inputhash", "output", 0.9, "actor", "", 10); err != nil {
		t.Fatalf("expected v2 to be recordable at tick 10: %v", err)
	}
}

func TestRecordRejectsEmptyModelKeyAndInputHash(t *testing.T) {
	reg := lifecycle.NewRegistry()
	r := NewRecorder(reg)
	if _, err := r.Record("", "h", "o", 0.5, "a", "", 1); !errors.Is(err, ErrEmptyModelKey) {
		t.Fatalf("expected ErrEmptyModelKey, got %v", err)
	}
	key := activeModel(t, reg, "m1", "v1", 1)
	if _, err := r.Record(key, "", "o", 0.5, "a", "", 1); !errors.Is(err, ErrEmptyInputHash) {
		t.Fatalf("expected ErrEmptyInputHash, got %v", err)
	}
}

func TestRecordRejectsConfidenceOutOfRange(t *testing.T) {
	reg := lifecycle.NewRegistry()
	key := activeModel(t, reg, "m1", "v1", 1)
	r := NewRecorder(reg)
	for _, c := range []float64{-0.01, 1.01, -5, 5} {
		if _, err := r.Record(key, "h", "o", c, "a", "", 1); !errors.Is(err, ErrConfidenceOutOfRange) {
			t.Fatalf("expected ErrConfidenceOutOfRange for %v, got %v", c, err)
		}
	}
	// Boundary values 0 and 1 are legal.
	if _, err := r.Record(key, "h", "o", 0, "a", "", 1); err != nil {
		t.Fatalf("expected confidence 0 to be legal: %v", err)
	}
	if _, err := r.Record(key, "h", "o", 1, "a", "", 1); err != nil {
		t.Fatalf("expected confidence 1 to be legal: %v", err)
	}
}

func TestRecordIsDeterministicForIdenticalInputs(t *testing.T) {
	reg := lifecycle.NewRegistry()
	key := activeModel(t, reg, "m1", "v1", 1)
	r := NewRecorder(reg)
	t1, err := r.Record(key, "h", "o", 0.5, "a", "p", 5)
	if err != nil {
		t.Fatal(err)
	}
	t2, err := r.Record(key, "h", "o", 0.5, "a", "p", 5)
	if err != nil {
		t.Fatal(err)
	}
	if t1.TraceID != t2.TraceID {
		t.Fatalf("expected identical inputs to produce the identical TraceID, got %s vs %s", t1.TraceID, t2.TraceID)
	}
	if t1.Hash != t2.Hash {
		t.Fatal("expected identical traces to hash identically")
	}
}

func TestRecordDiffersOnDifferentInputHash(t *testing.T) {
	reg := lifecycle.NewRegistry()
	key := activeModel(t, reg, "m1", "v1", 1)
	r := NewRecorder(reg)
	t1, err := r.Record(key, "h1", "o", 0.5, "a", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	t2, err := r.Record(key, "h2", "o", 0.5, "a", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if t1.TraceID == t2.TraceID {
		t.Fatal("expected different input hashes to produce different TraceIDs")
	}
}

func TestTracesAccumulatesInOrder(t *testing.T) {
	reg := lifecycle.NewRegistry()
	key := activeModel(t, reg, "m1", "v1", 1)
	r := NewRecorder(reg)
	if _, err := r.Record(key, "h1", "o1", 0.1, "a", "", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Record(key, "h2", "o2", 0.2, "a", "", 2); err != nil {
		t.Fatal(err)
	}
	traces := r.Traces()
	if len(traces) != 2 || traces[0].InputHash != "h1" || traces[1].InputHash != "h2" {
		t.Fatalf("expected 2 traces in order, got %v", traces)
	}
}

func TestRecordMirrorsToAuditStoreWhenAttached(t *testing.T) {
	reg := lifecycle.NewRegistry()
	key := activeModel(t, reg, "m1", "v1", 1)
	r := NewRecorder(reg)
	store := audit.NewAuditStore()
	r.AttachAuditStore(store)
	if _, err := r.Record(key, "h", "o", 0.5, "a", "", 1); err != nil {
		t.Fatal(err)
	}
	records := store.Snapshot()
	if len(records) != 1 || records[0].Action != "InferenceTrace" {
		t.Fatalf("expected exactly one InferenceTrace audit record, got %v", records)
	}
}

func TestVerifyTraceHashDetectsTampering(t *testing.T) {
	reg := lifecycle.NewRegistry()
	key := activeModel(t, reg, "m1", "v1", 1)
	r := NewRecorder(reg)
	trace, err := r.Record(key, "h", "o", 0.5, "a", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	trace.Confidence = 0.99 // tamper
	if err := VerifyTraceHash(trace); err == nil {
		t.Fatal("expected tampering with Confidence to invalidate the trace hash")
	}
}
