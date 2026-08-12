package contradiction

import (
	"testing"
)

func TestIngestBasicAndScore(t *testing.T) {
	e := NewEngine()
	rec, err := e.Ingest(Observation{
		ClaimKey: "VESSEL:1|FLAG_STATE", Winner: "PANAMA", WinnerConfidence: 0.9,
		RunnerUp: "LIBERIA", RunnerUpConfidence: 0.85, Contradiction: true,
		WinningSources: []string{"AIS_A"}, LosingSources: []string{"AIS_B"}, Tick: 1,
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if rec.ContradictionScore < 0.9 {
		t.Fatalf("expected high contradiction score for near-equal confidences, got %f", rec.ContradictionScore)
	}
	if rec.ConfidenceDelta != 0 {
		t.Fatalf("first observation should have zero delta, got %f", rec.ConfidenceDelta)
	}
}

func TestConfidenceDeltaAcrossTicks(t *testing.T) {
	e := NewEngine()
	e.Ingest(Observation{ClaimKey: "C1", Winner: "A", WinnerConfidence: 0.5, Tick: 1})
	rec2, _ := e.Ingest(Observation{ClaimKey: "C1", Winner: "A", WinnerConfidence: 0.8, Tick: 2})
	if rec2.ConfidenceDelta != 0.30000000000000004 && (rec2.ConfidenceDelta < 0.299 || rec2.ConfidenceDelta > 0.301) {
		t.Fatalf("expected delta ~0.3, got %f", rec2.ConfidenceDelta)
	}
	evo := e.TruthEvolution("C1")
	if len(evo) != 2 {
		t.Fatalf("expected 2 evolution records, got %d", len(evo))
	}
}

func TestActiveConflictsThreshold(t *testing.T) {
	e := NewEngine()
	e.Ingest(Observation{ClaimKey: "SETTLED", Winner: "A", WinnerConfidence: 0.95, RunnerUp: "B", RunnerUpConfidence: 0.1, Tick: 1})
	e.Ingest(Observation{ClaimKey: "CONTESTED", Winner: "A", WinnerConfidence: 0.55, RunnerUp: "B", RunnerUpConfidence: 0.5, Tick: 1})
	active := e.ActiveConflicts(0.6)
	if len(active) != 1 || active[0].ClaimKey != "CONTESTED" {
		t.Fatalf("expected only CONTESTED claim active, got %+v", active)
	}
}

func TestSourceStandingsWinLoss(t *testing.T) {
	e := NewEngine()
	e.Ingest(Observation{ClaimKey: "C1", Winner: "A", WinnerConfidence: 0.9, WinningSources: []string{"S1"}, LosingSources: []string{"S2"}, Tick: 1})
	e.Ingest(Observation{ClaimKey: "C2", Winner: "A", WinnerConfidence: 0.9, WinningSources: []string{"S1"}, LosingSources: []string{"S2"}, Tick: 2})
	e.Ingest(Observation{ClaimKey: "C3", Winner: "B", WinnerConfidence: 0.9, WinningSources: []string{"S2"}, LosingSources: []string{"S1"}, Tick: 3})

	standings := e.SourceStandings()
	byID := map[string]SourceStanding{}
	for _, s := range standings {
		byID[s.SourceID] = s
	}
	if byID["S1"].Wins != 2 || byID["S1"].Losses != 1 {
		t.Fatalf("S1 standing wrong: %+v", byID["S1"])
	}
	if byID["S2"].Wins != 1 || byID["S2"].Losses != 2 {
		t.Fatalf("S2 standing wrong: %+v", byID["S2"])
	}
	if byID["S1"].CurrentScore <= byID["S2"].CurrentScore {
		t.Fatalf("S1 should have higher current score than S2: %+v vs %+v", byID["S1"], byID["S2"])
	}
}

func TestAdversarialPairs(t *testing.T) {
	e := NewEngine()
	for i := 0; i < 5; i++ {
		e.Ingest(Observation{ClaimKey: "C", Winner: "A", WinnerConfidence: 0.9,
			WinningSources: []string{"S1"}, LosingSources: []string{"S2"}, Tick: uint64(i)})
	}
	pairs := e.AdversarialPairs(3)
	if len(pairs) != 1 || pairs[0].CoOccurrences != 5 {
		t.Fatalf("expected one adversarial pair with 5 co-occurrences, got %+v", pairs)
	}
	if pairs[0].SourceA != "S1" && pairs[0].SourceA != "S2" {
		t.Fatalf("unexpected pair members: %+v", pairs[0])
	}
}

func TestEvidenceDecay(t *testing.T) {
	base := 0.9
	fresh := EvidenceDecay(base, 0, 10)
	if fresh != base {
		t.Fatalf("zero ticks since seen should not decay: got %f", fresh)
	}
	halfLife := EvidenceDecay(base, 10, 10)
	if halfLife < base*0.49 || halfLife > base*0.51 {
		t.Fatalf("expected ~half of base at one half-life, got %f (base=%f)", halfLife, base)
	}
	veryStale := EvidenceDecay(base, 1000, 10)
	if veryStale > 0.001 {
		t.Fatalf("expected near-zero standing for very stale evidence, got %f", veryStale)
	}
}

func TestVerifyChainDetectsTamper(t *testing.T) {
	e := NewEngine()
	e.Ingest(Observation{ClaimKey: "C1", Winner: "A", WinnerConfidence: 0.7, Tick: 1})
	e.Ingest(Observation{ClaimKey: "C2", Winner: "B", WinnerConfidence: 0.6, Tick: 2})
	if err := e.VerifyChain(); err != nil {
		t.Fatalf("expected clean chain, got %v", err)
	}
	// tamper
	e.log[0].Observation.WinnerConfidence = 0.99
	if err := e.VerifyChain(); err == nil {
		t.Fatalf("expected VerifyChain to detect tamper")
	}
}

func TestRebuildIsDeterministic(t *testing.T) {
	e1 := NewEngine()
	e1.Ingest(Observation{ClaimKey: "C1", Winner: "A", WinnerConfidence: 0.7, RunnerUp: "B", RunnerUpConfidence: 0.6,
		WinningSources: []string{"S1"}, LosingSources: []string{"S2"}, Tick: 1})
	e1.Ingest(Observation{ClaimKey: "C1", Winner: "A", WinnerConfidence: 0.8, RunnerUp: "B", RunnerUpConfidence: 0.3,
		WinningSources: []string{"S1", "S3"}, LosingSources: []string{"S2"}, Tick: 2})
	e1.Ingest(Observation{ClaimKey: "C2", Winner: "X", WinnerConfidence: 0.5, Tick: 3})

	snap := e1.Snapshot()
	e2, err := Rebuild(snap)
	if err != nil {
		t.Fatalf("rebuild failed: %v", err)
	}
	if e1.Head() != e2.Head() {
		t.Fatalf("head mismatch after rebuild: %s vs %s", e1.Head(), e2.Head())
	}
	s1 := e1.SourceStandings()
	s2 := e2.SourceStandings()
	if len(s1) != len(s2) {
		t.Fatalf("standings length mismatch: %d vs %d", len(s1), len(s2))
	}
	for i := range s1 {
		if s1[i] != s2[i] {
			t.Fatalf("standing mismatch at %d: %+v vs %+v", i, s1[i], s2[i])
		}
	}
	if err := e2.VerifyChain(); err != nil {
		t.Fatalf("rebuilt chain should verify clean: %v", err)
	}
}

func TestIngestRejectsEmptyClaimKey(t *testing.T) {
	e := NewEngine()
	if _, err := e.Ingest(Observation{ClaimKey: "", Winner: "A", WinnerConfidence: 0.5}); err == nil {
		t.Fatalf("expected error for empty claim key")
	}
}
