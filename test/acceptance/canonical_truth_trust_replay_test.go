// canonical_truth_trust_replay_test.go is WAVE A item 7 of the
// canonical-truth-path mandate — "Replay must include trust, identity,
// and policy, not just evidence/decision" — plus the half of item 2's
// acceptance criterion that is specifically about replay:
//
//	tamper trust ledger -> execution root changes
//	Ini wajib masuk replay.
//
// The distinction this file exists to prove is between a replay that
// CARRIES trust and a replay that RE-DERIVES it. Carrying is worthless:
// a package that ships a trust root hash and then compares that hash to
// itself has verified nothing, which is exactly the tautology PHASE 10
// removed from ReplayID (see pkg/replay's package comment). Re-deriving
// means the replay engine rebuilds a trust state machine from the
// recorded transition ledger, verifies its hash chain, recomputes every
// per-source posture from scratch, and only then compares. Every
// assertion below is about the second thing.
package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"veriqo/pkg/canonical"
	"veriqo/pkg/execution"
	"veriqo/pkg/replay"
	"veriqo/pkg/trust/state"
)

// TestAcceptanceCanonicalTruthReplayRederivesTrustFromTheLedger proves
// the replay package carries the trust LEDGER (not merely a head) and
// that a fresh engine reaches the same verdict from it.
func TestAcceptanceCanonicalTruthReplayRederivesTrustFromTheLedger(t *testing.T) {
	k := newTruthKernel(t)
	assessTrust(t, k, "AIS_PROVIDER", 0.9, 1)
	assessTrust(t, k, "PORT_AUTHORITY", 0.95, 1)
	res := baseTruthCase().run(t, k)

	pkg := res.Execution.ReplayPackage
	if len(pkg.Execution.TrustLedger) < 2 {
		t.Fatalf("the replay package carries %d trust transitions; two assessments were recorded, "+
			"so it is not carrying the ledger", len(pkg.Execution.TrustLedger))
	}
	if pkg.Execution.TrustHalfLife == 0 {
		t.Error("the replay package carries no trust decay half-life; effective scores cannot be reproduced")
	}

	// A genuinely independent rebuild: no pointer to the live engine.
	rebuilt, err := state.Rebuild(pkg.Execution.TrustLedger,
		pkg.Execution.TrustHalfLife, pkg.Execution.TrustNeutralPrior)
	if err != nil {
		t.Fatalf("rebuilding trust from the replay package: %v", err)
	}
	if got, want := canonical.TrustLedgerHead(rebuilt), res.Canonical.Trust.LedgerHead; got != want {
		t.Fatalf("rebuilt trust head %s != the head the run committed to %s", got, want)
	}
	for _, subject := range []string{"AIS_PROVIDER", "PORT_AUTHORITY"} {
		live := k.Canonical.TrustState.StateAt(subject, 10)
		cold := rebuilt.StateAt(subject, 10)
		if live.Level != cold.Level || live.Score != cold.Score ||
			live.EffectiveScore != cold.EffectiveScore {
			t.Errorf("%s: live %s/%v/%v vs cold-rebuilt %s/%v/%v",
				subject, live.Level, live.Score, live.EffectiveScore,
				cold.Level, cold.Score, cold.EffectiveScore)
		}
	}

	// And the full cold replay matches.
	cert, err := replay.NewEngine().Replay(pkg)
	if err != nil {
		t.Fatalf("cold replay: %v", err)
	}
	if err := cert.Assert(); err != nil {
		t.Fatalf("cold replay diverged: %v", err)
	}
}

// TestAcceptanceCanonicalTruthReplayDetectsATamperedTrustLedger is the
// mandate's explicit clause. Two distinct tampers are exercised, because
// they are caught by two different mechanisms and a system that caught
// only one would have a real hole:
//
//   - an INCONSISTENT tamper (edit a field, leave the hashes) breaks the
//     chain and is refused outright by state.Rebuild;
//   - a CONSISTENT tamper (edit a field AND recompute every hash after
//     it, the work a competent attacker actually does) produces a valid
//     chain with a DIFFERENT head, so the recomputed trust evaluation no
//     longer matches the committed one and the replay diverges.
func TestAcceptanceCanonicalTruthReplayDetectsATamperedTrustLedger(t *testing.T) {
	k := newTruthKernel(t)
	assessTrust(t, k, "AIS_PROVIDER", 0.9, 1)
	assessTrust(t, k, "PORT_AUTHORITY", 0.95, 1)
	res := baseTruthCase().run(t, k)

	// Round-trip through bytes so each variant is an independent value.
	raw, err := res.Execution.ReplayPackage.Marshal()
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}

	t.Run("inconsistent tamper is refused outright", func(t *testing.T) {
		var pkg replay.ReplayPackage
		if err := json.Unmarshal(raw, &pkg); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		// Raise a revoked-adjacent subject's recorded score without
		// touching the hash — the naive tamper.
		pkg.Execution.TrustLedger[0].ToScore = 0.999
		_, err := replay.NewEngine().Replay(pkg)
		if err == nil {
			t.Fatal("a replay over a trust ledger with a broken hash chain returned a verdict")
		}
		if !errors.Is(err, replay.ErrTrustLedgerTampered) {
			t.Fatalf("error = %v, want ErrTrustLedgerTampered", err)
		}
	})

	t.Run("consistent tamper diverges the replay", func(t *testing.T) {
		var pkg replay.ReplayPackage
		if err := json.Unmarshal(raw, &pkg); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		// The competent tamper: change the assessment AND rebuild the
		// whole chain so every index, prev-hash and content hash agrees.
		// state.Rebuild will accept this ledger. What it cannot do is make
		// the head match what the original execution committed to.
		tampered := rechainTrust(t, pkg.Execution.TrustLedger, func(ts []state.Transition) {
			ts[0].ToScore = 0.10 // silently demote a provider the run trusted
			ts[0].Reason = "tampered: fabricated downgrade"
		})
		pkg.Execution.TrustLedger = tampered

		if _, err := state.Rebuild(tampered, pkg.Execution.TrustHalfLife,
			pkg.Execution.TrustNeutralPrior); err != nil {
			t.Fatalf("the consistent tamper did not produce a valid chain, so this subtest is "+
				"not exercising what it claims: %v", err)
		}

		cert, err := replay.NewEngine().Replay(pkg)
		if err != nil {
			// Acceptable: refused rather than mismatched. Either is detection.
			return
		}
		if cert.Match {
			t.Fatal("a replay over a rewritten trust ledger reported MATCH — the trust posture " +
				"the decision was made under is not actually being re-derived")
		}
		if cert.DivergedStage != replay.StageTrust {
			t.Errorf("divergence localised to %q, want %q", cert.DivergedStage, replay.StageTrust)
		}
	})
}

// rechainTrust applies a mutation to a copy of a trust ledger and then
// recomputes the whole hash chain so it verifies — the attacker with
// full write access, modelled honestly.
//
// It re-derives each transition's hash through pkg/trust/state's OWN
// serialization by round-tripping a rebuilt engine, rather than
// duplicating hashTransition here: a local copy of that function would
// drift from the real one and this test would then be checking a
// straw man.
func rechainTrust(t *testing.T, in []state.Transition, mutate func([]state.Transition)) []state.Transition {
	t.Helper()
	out := append([]state.Transition(nil), in...)
	mutate(out)

	// Replay the mutated transitions into a fresh engine through the
	// package's own governed append path, which recomputes Index,
	// PrevHash and Hash exactly the way the production ledger does.
	eng := state.NewEngine(1000, 0.5)
	for _, tr := range out {
		switch tr.Kind {
		case state.TransitionRevoke:
			if _, err := eng.Revoke(tr.Subject, tr.ActorID, tr.Reason, tr.EvidenceRefs, tr.Tick); err != nil {
				t.Fatalf("rechain revoke: %v", err)
			}
		case state.TransitionEscalate:
			if _, err := eng.Escalate(tr.Subject, tr.ActorID, tr.Reason, tr.EvidenceRefs, tr.Tick); err != nil {
				t.Fatalf("rechain escalate: %v", err)
			}
		default:
			if _, err := eng.Assess(tr.Subject, tr.ActorID, tr.PolicyName, tr.Reason,
				tr.ToScore, tr.EvidenceRefs, tr.Tick, 0); err != nil {
				t.Fatalf("rechain assess: %v", err)
			}
		}
	}
	return eng.Ledger()
}

// TestAcceptanceCanonicalTruthReplayCoversPolicyAndDurableLedger closes
// the remaining two identities item 7 names. A replay that reproduced
// evidence and decision but silently accepted a DIFFERENT policy would
// be verifying the wrong thing.
func TestAcceptanceCanonicalTruthReplayCoversPolicyAndDurableLedger(t *testing.T) {
	dir := t.TempDir()
	k := newTruthKernel(t)
	withDurableLedger(t, k, dir)
	assessTrust(t, k, "AIS_PROVIDER", 0.9, 1)
	assessTrust(t, k, "PORT_AUTHORITY", 0.95, 1)
	res := baseTruthCase().run(t, k)

	pkg := res.Execution.ReplayPackage
	if pkg.Execution.PolicyHash != truthPolicy.Hash() {
		t.Errorf("replay package policy hash = %s, want %s",
			pkg.Execution.PolicyHash, truthPolicy.Hash())
	}
	if pkg.Execution.DurableLedgerHead == "" {
		t.Error("replay package carries no durable ledger head")
	}

	for name, mutate := range map[string]func(p *replay.ReplayPackage){
		"policy hash": func(p *replay.ReplayPackage) {
			p.Execution.PolicyHash = "0000000000000000000000000000000000000000000000000000000000000000"
		},
		"durable ledger head": func(p *replay.ReplayPackage) {
			p.Execution.DurableLedgerHead = "0000000000000000000000000000000000000000000000000000000000000000"
		},
		"identity ledger head": func(p *replay.ReplayPackage) {
			p.Execution.IdentityLedgerHead = "0000000000000000000000000000000000000000000000000000000000000000"
		},
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := pkg.Marshal()
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var mutated replay.ReplayPackage
			if err := json.Unmarshal(raw, &mutated); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			mutate(&mutated)
			cert, err := replay.NewEngine().Replay(mutated)
			if err != nil {
				return // refused outright: still detection
			}
			if cert.Match {
				t.Fatalf("changing the %s did not change the replay verdict — that identity is "+
					"carried but not verified", name)
			}
		})
	}
}

// TestAcceptanceCanonicalTruthDAGReplayReproducesTheTrustNode is the
// whole-DAG counterpart: pkg/execution.ReplayDAG rebuilds every node,
// TRUST_STATE included, from serialized bytes and a fresh engine.
func TestAcceptanceCanonicalTruthDAGReplayReproducesTheTrustNode(t *testing.T) {
	k := newTruthKernel(t)
	assessTrust(t, k, "AIS_PROVIDER", 0.9, 1)
	assessTrust(t, k, "PORT_AUTHORITY", 0.95, 1)
	res := baseTruthCase().run(t, k)

	raw, err := res.Execution.ExportReplay()
	if err != nil {
		t.Fatalf("ExportReplay: %v", err)
	}

	// A fresh engine over a fresh pipeline — but the SAME trust ledger,
	// installed the way a recovering deployment would install it. A cold
	// DAG replay of a trust-gated execution cannot match without it, and
	// that is the point: trust is load-bearing, so a replayer that lacks
	// it must fail rather than quietly agree.
	fresh := execution.NewEngine(nil)
	rebuilt, err := state.Rebuild(res.Execution.ReplayPackage.Execution.TrustLedger,
		res.Execution.ReplayPackage.Execution.TrustHalfLife,
		res.Execution.ReplayPackage.Execution.TrustNeutralPrior)
	if err != nil {
		t.Fatalf("rebuilding trust: %v", err)
	}
	fresh.Pipeline.TrustState = rebuilt
	fresh.Trust = rebuilt
	fresh.Identity = k.Identity

	verdict, err := execution.ReplayDAG(context.Background(), raw, fresh)
	if err != nil {
		t.Fatalf("ReplayDAG: %v", err)
	}
	if err := verdict.Assert(); err != nil {
		t.Fatalf("whole-DAG cold replay diverged: %v", err)
	}

	// The negative control: the same replay WITHOUT the trust ledger must
	// diverge, and must localise the divergence to TRUST_STATE. If it
	// matched, trust would not actually be inside the root.
	blind := execution.NewEngine(nil)
	blind.Identity = k.Identity
	blindVerdict, err := execution.ReplayDAG(context.Background(), raw, blind)
	if err == nil && blindVerdict.Matched {
		t.Fatal("a replayer with NO trust history reproduced the execution root exactly; " +
			"trust is not participating in the root hash after all")
	}
	if err == nil && blindVerdict.DivergentStage != execution.StageTrust {
		t.Errorf("a trust-blind replay diverged first at %s, want TRUST_STATE",
			blindVerdict.DivergentStage)
	}
}
