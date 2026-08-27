package registry

import (
	"path/filepath"
	"testing"
)

// TestPersistence_SurvivesAcrossSeparateRegistryInstances is the
// concrete proof for the gap the prior report named explicitly:
// "setiap invokasi CLI membangun Registry baru di memori; state tidak
// bertahan lintas proses CLI." Two independently-constructed Registry
// values (standing in for two separate CLI/gateway process
// invocations) share one state file; the second must see everything
// the first committed, including that the trust/policy hash chains
// continue as ONE chain rather than restarting.
func TestPersistence_SurvivesAcrossSeparateRegistryInstances(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")

	// "Process 1"
	reg1, err := New(WithStatePath(statePath))
	if err != nil {
		t.Fatalf("New (process 1): %v", err)
	}
	if _, err := reg1.Trust.Certify("source-alpha", 0.95, 0.02, 1); err != nil {
		t.Fatalf("Trust.Certify: %v", err)
	}
	if _, err := reg1.Policy.Evaluate("FLAG", "vessel/IMO1", "FLAG", 0.9, "true"); err != nil {
		t.Fatalf("Policy.Evaluate: %v", err)
	}
	if _, err := reg1.Reasoning.Assert("alice", "reports_to", "bob"); err != nil {
		t.Fatalf("Reasoning.Assert: %v", err)
	}
	nodeID, err := reg1.Evidence.AddNode("raw_observation", "hello")
	if err != nil {
		t.Fatalf("Evidence.AddNode: %v", err)
	}
	if err := reg1.SaveState(); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	// "Process 2" — a brand new Registry, same state path.
	reg2, err := New(WithStatePath(statePath))
	if err != nil {
		t.Fatalf("New (process 2): %v", err)
	}
	trustLedger, err := reg2.Trust.Ledger()
	if err != nil || len(trustLedger) != 1 {
		t.Fatalf("process 2 trust ledger = %d entries, err=%v; want 1 restored", len(trustLedger), err)
	}
	policyLedger, err := reg2.Policy.Ledger()
	if err != nil || len(policyLedger) != 1 {
		t.Fatalf("process 2 policy ledger = %d entries, err=%v; want 1 restored", len(policyLedger), err)
	}
	facts, err := reg2.Reasoning.Query("alice", "reports_to", "?x")
	if err != nil || len(facts) != 1 {
		t.Fatalf("process 2 reasoning facts = %d, err=%v; want 1 restored", len(facts), err)
	}
	lineage, err := reg2.Evidence.Lineage(nodeID)
	if err != nil {
		t.Fatalf("process 2 Evidence.Lineage(%s): %v", nodeID, err)
	}
	_ = lineage // node with no ancestry restores fine; presence confirmed via Stats below.
	stats, err := reg2.Evidence.Stats()
	if err != nil || stats != "nodes=1 edges=0" {
		t.Fatalf("process 2 Evidence.Stats() = %q, err=%v; want nodes=1 edges=0 (node restored, no edges)", stats, err)
	}

	// Chain continuity: certifying again in "process 2" must produce
	// index 2 chained onto process 1's index 1, NOT a fresh index 1 —
	// proving the restored chain is the SAME chain, not a reset one.
	if _, err := reg2.Trust.Certify("source-alpha", 0.95, 0.02, 2); err != nil {
		t.Fatalf("Trust.Certify (process 2): %v", err)
	}
	trustLedger2, err := reg2.Trust.Ledger()
	if err != nil || len(trustLedger2) != 2 {
		t.Fatalf("process 2 trust ledger after new cert = %d, err=%v; want 2", len(trustLedger2), err)
	}
	if trustLedger2[1].Index != 2 || trustLedger2[1].PrevHash != trustLedger2[0].Hash {
		t.Fatalf("chain did not continue correctly: %+v", trustLedger2)
	}
	if _, err := reg2.Trust.VerifyLedger(); err != nil {
		t.Fatalf("VerifyLedger after cross-process continuation: %v", err)
	}
}

func TestPersistence_FirstRunAtNewPathIsNotAnError(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "does", "not", "exist", "yet.json")
	reg, err := New(WithStatePath(statePath))
	if err != nil {
		t.Fatalf("New with a not-yet-existing state path should succeed (first run): %v", err)
	}
	if !reg.HasPersistence() {
		t.Fatal("HasPersistence() = false, want true")
	}
}

func TestPersistence_SaveStateWithoutOptionErrors(t *testing.T) {
	reg, err := New() // no WithStatePath
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if reg.HasPersistence() {
		t.Fatal("HasPersistence() = true for a Registry built without WithStatePath")
	}
	if err := reg.SaveState(); err == nil {
		t.Fatal("SaveState() on a non-persistent Registry should return an error, not silently succeed")
	}
}

func TestPersistence_TamperedStateFileIsRejected(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	reg1, err := New(WithStatePath(statePath))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := reg1.Trust.Certify("source-a", 0.95, 0.02, 1); err != nil {
		t.Fatalf("Certify: %v", err)
	}
	if err := reg1.SaveState(); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	// Corrupt the persisted trust certificate's hash directly on disk.
	data, err := reg1.store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	corrupted := []byte(replaceOnce(string(data), `"Score":`, `"Score_TAMPERED":`))
	if err := reg1.store.Persist(corrupted); err != nil {
		t.Fatalf("Persist corrupted: %v", err)
	}

	if _, err := New(WithStatePath(statePath)); err == nil {
		t.Fatal("restoring from a corrupted/tampered state file should fail loudly, not silently succeed")
	}
}

func replaceOnce(s, old, new string) string {
	i := indexOf(s, old)
	if i < 0 {
		return s
	}
	return s[:i] + new + s[i+len(old):]
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
