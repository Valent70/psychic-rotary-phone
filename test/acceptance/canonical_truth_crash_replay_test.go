// canonical_truth_crash_replay_test.go is WAVE A item 6 of the
// canonical-truth-path mandate — crash/restart recovery — as a real
// test rather than a description. The mandate's sequence, verbatim:
//
//  1. Start case      7. Restart
//  2. Write evidence  8. Reconstruct ledger
//  3. Write trust     9. Verify root
//  4. Write decision 10. Continue execution
//  5. Crash process  11. Compare with original
//  6. Destroy memory
//
// WHAT "CRASH" MEANS HERE, precisely. Step 5 is a real SIGKILL
// delivered to a real child process by the operating system — not a
// panic, not a simulated fault injection, not an in-process "pretend
// the state is gone". SIGKILL cannot be caught, so no deferred Close(),
// no flush, no cleanup and no test-framework teardown runs in the
// child. Step 6 follows from that for free: the child's address space
// is reclaimed by the kernel, so every in-memory fusion chain, trust
// engine and identity resolver it held is genuinely gone. The only
// thing that can survive is what pkg/storage/wal already fsynced.
//
// The child is this same test binary re-executed with an environment
// variable set (the standard Go helper-process idiom), so the child is
// running genuinely fresh package state, not a goroutine pretending to.
package acceptance

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"veriqo/pkg/canonical"
	"veriqo/pkg/ledger"
	"veriqo/pkg/lifecycle"
	"veriqo/pkg/trust/state"
	"veriqo/veriqo/kernel"
)

// crashPhaseEnv names the environment variable that turns this test
// binary into the crash-test child. Its value is the phase to run.
const crashPhaseEnv = "VERIQO_CANONICAL_TRUTH_CRASH_PHASE"

// crashDirEnv names the durable-ledger directory the child must use.
const crashDirEnv = "VERIQO_CANONICAL_TRUTH_CRASH_DIR"

// handoff is what the child records about the case it ran, so the
// parent knows what the surviving ledger is supposed to contain. It is
// the TEST's own bookkeeping, never VERIQO state: every value in it is
// independently re-derivable by the parent from the WAL alone, which is
// exactly what the parent then checks.
type handoff struct {
	CaseID            string `json:"case_id"`
	ExecutionRootHash string `json:"execution_root_hash"`
	CertificateHash   string `json:"certificate_hash"`
	DurableLedgerHead string `json:"durable_ledger_head"`
	TrustLedgerHead   string `json:"trust_ledger_head"`
	DecisionAction    string `json:"decision_action"`
}

// crashCaseA is the case the child runs before dying. It is fixed data,
// identical in the crash arm and the clean control arm, so any
// difference between the two arms is attributable to the crash and
// nothing else.
func crashCaseA() truthCase {
	c := baseTruthCase()
	c.objective = "crash-recovery case A"
	c.tick = 10
	return c
}

// crashCaseB is the CONTINUATION case, run after recovery. Its sources
// differ from case A's so pkg/moat/fusion's duplicate-submission guard
// does not reject it, and its tick is later so trust decay is exercised
// across the restart boundary rather than short-circuited.
func crashCaseB() truthCase {
	c := baseTruthCase()
	c.objective = "crash-recovery case B (continuation)"
	c.predicate = "DEPARTURE_TIME"
	c.submissions = []canonical.SourceSubmission{
		sub("AIS_FEED_B", "AIS_PROVIDER", "18:00", 0.8),
		sub("PORT_AUTHORITY_LOG_B", "PORT_AUTHORITY", "18:00", 0.9),
	}
	c.tick = 40
	return c
}

// TestHelperCanonicalTruthCrashChild is the child process. It is inert
// unless the environment says otherwise, so a normal `go test` run
// skips it.
func TestHelperCanonicalTruthCrashChild(t *testing.T) {
	phase := os.Getenv(crashPhaseEnv)
	if phase == "" {
		t.Skip("not the crash-test child process")
	}
	dir := os.Getenv(crashDirEnv)
	if dir == "" {
		t.Fatalf("%s set to %q but %s is empty", crashPhaseEnv, phase, crashDirEnv)
	}

	k, err := kernel.New()
	if err != nil {
		t.Fatalf("child: kernel.New: %v", err)
	}
	l, rep, err := ledger.Open(ledger.Config{Dir: dir})
	if err != nil {
		t.Fatalf("child: ledger.Open: %v", err)
	}
	if rep.FailedClosed {
		t.Fatalf("child: ledger recovery failed closed: %+v", rep.Findings)
	}
	k.Lifecycle.Ledger = l

	// Steps 1-3: start a case and write real trust into the ledger the
	// canonical pipeline weighs evidence with.
	assessTrust(t, k, "AIS_PROVIDER", 0.9, 1)
	assessTrust(t, k, "PORT_AUTHORITY", 0.95, 1)

	// Steps 2 and 4: the run itself writes EVIDENCE, TRUST and DECISION
	// (and six more kinds) durably before returning.
	res := crashCaseA().run(t, k)

	h := handoff{
		CaseID:            string(res.CaseID),
		ExecutionRootHash: res.Certificate.ExecutionRootHash,
		CertificateHash:   res.Certificate.Hash,
		DurableLedgerHead: res.Certificate.DurableLedgerHead,
		TrustLedgerHead:   res.Canonical.Trust.LedgerHead,
		DecisionAction:    string(res.Canonical.Decision.Action),
	}
	raw, err := json.Marshal(h)
	if err != nil {
		t.Fatalf("child: marshalling handoff: %v", err)
	}
	// The handoff is fsynced explicitly: SIGKILL below gives no chance to
	// flush, and a handoff lost to page cache would make the parent fail
	// for a reason that has nothing to do with VERIQO's own durability.
	f, err := os.Create(filepath.Join(dir, "handoff.json")) // #nosec G304 -- dir is this test's own t.TempDir(), passed to its own child
	if err != nil {
		t.Fatalf("child: creating handoff: %v", err)
	}
	if _, err := f.Write(raw); err != nil {
		t.Fatalf("child: writing handoff: %v", err)
	}
	if err := f.Sync(); err != nil {
		t.Fatalf("child: syncing handoff: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("child: closing handoff: %v", err)
	}

	switch phase {
	case "crash":
		// Step 5: a REAL crash. SIGKILL to our own PID, uncatchable. No
		// ledger Close(), no kernel Shutdown(), no test teardown. Anything
		// still only in this process's memory is lost at this instant.
		if err := syscall.Kill(os.Getpid(), syscall.SIGKILL); err != nil {
			t.Fatalf("child: SIGKILL: %v", err)
		}
		// Unreachable. If the process is somehow still alive, say so
		// loudly rather than exiting 0 and letting the parent believe a
		// crash happened.
		t.Fatal("child: survived SIGKILL")
	case "clean":
		// The control arm: the same work, shut down properly.
		if err := l.Close(); err != nil {
			t.Fatalf("child: closing ledger: %v", err)
		}
		if err := k.Shutdown(); err != nil {
			t.Fatalf("child: kernel shutdown: %v", err)
		}
	default:
		t.Fatalf("child: unknown phase %q", phase)
	}
}

// runCrashChild re-executes this test binary as the child, in the given
// phase, and returns the child's combined output and the exit error.
func runCrashChild(t *testing.T, phase, dir string) ([]byte, error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperCanonicalTruthCrashChild", "-test.v") // #nosec G204 -- os.Args[0] is this test binary itself
	cmd.Env = append(os.Environ(), crashPhaseEnv+"="+phase, crashDirEnv+"="+dir)
	out, err := cmd.CombinedOutput()
	return out, err
}

// readHandoff loads the child's record of what it did.
func readHandoff(t *testing.T, dir string) handoff {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "handoff.json")) // #nosec G304 -- dir is this test's own t.TempDir()
	if err != nil {
		t.Fatalf("reading handoff from %s: %v", dir, err)
	}
	var h handoff
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatalf("decoding handoff: %v", err)
	}
	return h
}

// TestAcceptanceCanonicalTruthCrashRestartRecovery is the mandate's
// section VII acceptance criterion, all four clauses:
//
//	ledger head survives restart
//	execution root survives restart
//	tampering detected
//	continued execution remains deterministic
func TestAcceptanceCanonicalTruthCrashRestartRecovery(t *testing.T) {
	crashDir := t.TempDir()
	cleanDir := t.TempDir()

	// --- Steps 1-6: run a case, then kill the process outright --------
	out, err := runCrashChild(t, "crash", crashDir)
	if err == nil {
		t.Fatalf("the crash child exited cleanly; it was supposed to be SIGKILLed.\n%s", out)
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("crash child failed for a non-exit reason: %v\n%s", err, out)
	}
	ws, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok {
		t.Fatalf("cannot inspect the crash child's wait status on this platform")
	}
	if !ws.Signaled() || ws.Signal() != syscall.SIGKILL {
		t.Fatalf("crash child did not die by SIGKILL (signaled=%v signal=%v exit=%d).\n%s",
			ws.Signaled(), ws.Signal(), ws.ExitStatus(), out)
	}

	// The control arm: identical work, clean shutdown.
	if out, err := runCrashChild(t, "clean", cleanDir); err != nil {
		t.Fatalf("clean child failed: %v\n%s", err, out)
	}

	crashed := readHandoff(t, crashDir)
	clean := readHandoff(t, cleanDir)

	// The two arms must have produced byte-identical results BEFORE the
	// crash — otherwise a later divergence could be attributed to
	// nondeterminism rather than to recovery.
	if crashed.ExecutionRootHash != clean.ExecutionRootHash {
		t.Fatalf("the two arms diverged before the crash: crash root %s vs clean root %s",
			crashed.ExecutionRootHash, clean.ExecutionRootHash)
	}
	if crashed.DurableLedgerHead != clean.DurableLedgerHead {
		t.Fatalf("the two arms wrote different ledgers: %s vs %s",
			crashed.DurableLedgerHead, clean.DurableLedgerHead)
	}

	// --- Steps 7-9: fresh process, reconstruct, verify the root -------
	// This parent process never shared memory with either child.
	recovered, rep, err := ledger.Open(ledger.Config{Dir: crashDir})
	if err != nil {
		t.Fatalf("reopening the crashed ledger: %v", err)
	}
	defer recovered.Close() //nolint:errcheck // test teardown
	if rep.FailedClosed {
		t.Fatalf("recovery of the crashed ledger failed closed: %+v", rep.Findings)
	}
	if rep.RecoveredRecords == 0 {
		t.Fatal("recovery found no records at all after the crash — nothing was durable")
	}

	// CLAUSE 1: ledger head survives restart.
	gotHead, _ := recovered.Head()
	if gotHead != crashed.DurableLedgerHead {
		t.Errorf("ledger head after restart = %s, want %s (the head the killed process committed to)",
			gotHead, crashed.DurableLedgerHead)
	}
	if err := recovered.Verify(); err != nil {
		t.Errorf("the recovered ledger failed its own verification: %v", err)
	}

	events, err := recovered.EventsForCase(crashed.CaseID)
	if err != nil {
		t.Fatalf("reconstructing events after the crash: %v", err)
	}
	if cov := ledger.KindCoverage(events); !cov.Complete() {
		t.Errorf("the reconstructed ledger is missing event kinds %v", cov.Missing)
	}

	// CLAUSE 2: execution root survives restart. The DECISION event the
	// killed process wrote still names the exact root it produced.
	var sawRoot bool
	for _, ev := range events {
		if ev.Kind == ledger.EventDecision && contains(ev.Detail, crashed.ExecutionRootHash) {
			sawRoot = true
		}
	}
	if !sawRoot {
		t.Errorf("no surviving DECISION event names execution root %s", crashed.ExecutionRootHash)
	}

	// --- Step 10: continue execution from the RECONSTRUCTED state -----
	// Trust is the one thing the killed process held in memory that a
	// continuation genuinely needs, and it is recovered from the durable
	// ledger alone — no fixture re-asserts it here.
	recoveredTrust, err := lifecycle.RecoverTrustState(events)
	if err != nil {
		t.Fatalf("recovering trust state from the durable ledger: %v", err)
	}
	if got := canonical.TrustLedgerHead(recoveredTrust); got != crashed.TrustLedgerHead {
		t.Fatalf("recovered trust head = %s, want %s", got, crashed.TrustLedgerHead)
	}

	continuedAfterCrash := continueAfter(t, recovered, recoveredTrust)

	// The control continuation: same continuation case, same recovery
	// mechanism, over the ledger of the process that never died.
	cleanLedger, _, err := ledger.Open(ledger.Config{Dir: cleanDir})
	if err != nil {
		t.Fatalf("reopening the clean ledger: %v", err)
	}
	defer cleanLedger.Close() //nolint:errcheck // test teardown
	cleanEvents, err := cleanLedger.EventsForCase(clean.CaseID)
	if err != nil {
		t.Fatalf("reading clean events: %v", err)
	}
	cleanTrust, err := lifecycle.RecoverTrustState(cleanEvents)
	if err != nil {
		t.Fatalf("recovering trust from the clean ledger: %v", err)
	}
	continuedClean := continueAfter(t, cleanLedger, cleanTrust)

	// CLAUSE 4: continued execution after recovery remains deterministic.
	if continuedAfterCrash.Certificate.ExecutionRootHash !=
		continuedClean.Certificate.ExecutionRootHash {
		t.Errorf("the continuation after a crash produced execution root %s, "+
			"but the same continuation with no crash produced %s",
			continuedAfterCrash.Certificate.ExecutionRootHash,
			continuedClean.Certificate.ExecutionRootHash)
	}
	if continuedAfterCrash.Certificate.Hash != continuedClean.Certificate.Hash {
		t.Errorf("continuation certificates differ after a crash: %s vs %s",
			continuedAfterCrash.Certificate.Hash, continuedClean.Certificate.Hash)
	}
	if continuedAfterCrash.Certificate.DurableLedgerHead !=
		continuedClean.Certificate.DurableLedgerHead {
		t.Errorf("continuation durable heads differ after a crash: %s vs %s",
			continuedAfterCrash.Certificate.DurableLedgerHead,
			continuedClean.Certificate.DurableLedgerHead)
	}
	// The continuation must genuinely have SEEN the recovered trust: both
	// providers were assessed before the crash and nothing re-asserted
	// them, so a continuation that lost trust would report UNKNOWN.
	for _, id := range []string{"AIS_FEED_B", "PORT_AUTHORITY_LOG_B"} {
		lvl, posture, ok := trustLevelOf(continuedAfterCrash, id)
		if !ok {
			t.Fatalf("the continuation did not evaluate %s", id)
		}
		if posture != canonical.PostureNormal {
			t.Errorf("after recovery, %s is %s/%s — the trust assessed before the crash was lost",
				id, lvl, posture)
		}
	}
}

// continueAfter builds a fresh kernel over an already-recovered ledger
// and trust engine, and runs the continuation case through it.
func continueAfter(t *testing.T, l *ledger.Ledger, trust *state.Engine) *lifecycle.Result {
	t.Helper()
	k := newTruthKernel(t)
	k.Lifecycle.Ledger = l
	// UseTrustState installs the recovered engine on BOTH the canonical
	// pipeline and the execution engine; setting only one would give the
	// DAG a different opinion from the decision, which is precisely the
	// fragmentation that method exists to prevent.
	k.Lifecycle.UseTrustState(trust)
	return crashCaseB().run(t, k)
}

// contains is strings.Contains, named locally so this file needs no
// import purely for one substring check in an error path.
func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		indexOf(haystack, needle) >= 0
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}

// TestAcceptanceCanonicalTruthTamperedDurableLedgerIsDetected is clause
// 3 of the mandate's section VII acceptance criterion, and the reason
// the durable ledger is a hash chain rather than a log file.
func TestAcceptanceCanonicalTruthTamperedDurableLedgerIsDetected(t *testing.T) {
	dir := t.TempDir()
	k := newTruthKernel(t)
	l := withDurableLedger(t, k, dir)
	assessTrust(t, k, "AIS_PROVIDER", 0.9, 1)
	assessTrust(t, k, "PORT_AUTHORITY", 0.95, 1)
	res := baseTruthCase().run(t, k)
	if err := l.Close(); err != nil {
		t.Fatalf("closing ledger: %v", err)
	}

	// Find the segment file and flip the bytes of a DECISION event's
	// recorded action, in place, preserving the file's length — the
	// realistic tamper: an operator with disk access editing a record to
	// say something else.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("listing %s: %v", dir, err)
	}
	var segment string
	for _, e := range entries {
		if e.Name() != "handoff.json" && !e.IsDir() {
			segment = filepath.Join(dir, e.Name())
			break
		}
	}
	if segment == "" {
		t.Fatal("no WAL segment file was created on disk at all")
	}
	raw, err := os.ReadFile(segment) // #nosec G304 -- this test's own t.TempDir()
	if err != nil {
		t.Fatalf("reading segment: %v", err)
	}
	needle := []byte(`"action ` + string(res.Canonical.Decision.Action))
	idx := indexOfBytes(raw, needle)
	if idx < 0 {
		t.Fatalf("could not locate the DECISION event's action text in the segment to tamper with")
	}
	// Same length, different content — a CRC/length check alone would
	// still catch this one, which is why the assertion below is about
	// detection, not about which layer detects it.
	copy(raw[idx:idx+len(needle)], []byte(`"aktion `+string(res.Canonical.Decision.Action)))
	if err := os.WriteFile(segment, raw, 0o600); err != nil {
		t.Fatalf("writing tampered segment: %v", err)
	}

	// Reopening must not silently serve the altered record.
	tampered, rep, openErr := ledger.Open(ledger.Config{Dir: dir})
	if openErr == nil && tampered != nil {
		defer tampered.Close() //nolint:errcheck // test teardown
	}
	detected := openErr != nil || (rep != nil && (rep.FailedClosed || len(rep.Findings) > 0))
	if !detected && tampered != nil {
		// If WAL-level recovery did not flag it, the ledger's own
		// per-event content hash must.
		if _, err := tampered.Events(); err != nil {
			detected = true
		} else if err := tampered.Verify(); err != nil {
			detected = true
		}
	}
	if !detected {
		t.Fatal("a tampered durable ledger record was served without any detection at all")
	}
}

func indexOfBytes(h, n []byte) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if string(h[i:i+len(n)]) == string(n) {
			return i
		}
	}
	return -1
}
