package manifest

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// This file answers the reviewer's explicit "race-free != state-machine-safe
// under concurrent transitions" ask: go test -race already proves no
// unsynchronized memory access occurs (Registry serializes every method
// via r.mu), but that says nothing about whether concurrently-issued,
// individually race-free calls interleave into a semantically WRONG final
// state (two winners for finalize(), a supersede() that outran its own
// parent's finalization, a stale hash surviving concurrent mutation...).
// Every test below runs real goroutines against a shared *Registry,
// starts them from a closed channel to maximize contention, and asserts
// on the semantic outcome -- not merely the absence of a race.

// advanceToReadyForFinalization drives evidenceID through every state up
// to (but not including) FINALIZED, single-threaded, so concurrent tests
// can start their goroutines from a known state right at the contested
// transition.
func advanceToReadyForFinalization(t *testing.T, reg *Registry, evidenceID string, tick uint64) {
	t.Helper()
	if _, err := reg.RecordCustodyEvent(evidenceID, evidenceID+"-received", "PTY-1", CustodyReceived, tick, "received", ""); err != nil {
		t.Fatalf("RecordCustodyEvent(RECEIVED): %v", err)
	}
	if _, err := reg.Advance(evidenceID, StateIngested, tick); err != nil {
		t.Fatalf("Advance to INGESTED: %v", err)
	}
	if _, err := reg.RecordCustodyEvent(evidenceID, evidenceID+"-hashed", "PTY-1", CustodyHashed, tick, "hashed", "sha256:deadbeef"); err != nil {
		t.Fatalf("RecordCustodyEvent(HASHED): %v", err)
	}
	if _, err := reg.Advance(evidenceID, StateIntegrityAssessed, tick); err != nil {
		t.Fatalf("Advance to INTEGRITY_ASSESSED: %v", err)
	}
	if _, err := reg.Advance(evidenceID, StateProvenanceComplete, tick); err != nil {
		t.Fatalf("Advance to PROVENANCE_COMPLETE: %v", err)
	}
	if _, err := reg.RecordCustodyEvent(evidenceID, evidenceID+"-reviewed", "PTY-1", CustodyReviewed, tick, "reviewed", "sha256:deadbeef"); err != nil {
		t.Fatalf("RecordCustodyEvent(REVIEWED): %v", err)
	}
	if _, err := reg.Advance(evidenceID, StateReadyForFinalization, tick); err != nil {
		t.Fatalf("Advance to READY_FOR_FINALIZATION: %v", err)
	}
}

// replayThroughFullLifecycle is advanceThroughFullLifecycle's error-returning
// twin, safe to call from a spawned goroutine -- testing.T's Fatal family
// may only be called from the goroutine actually running the Test
// function, so every concurrency test below drives goroutines with plain
// error returns and asserts on them AFTER wg.Wait(), back on the test
// goroutine.
func replayThroughFullLifecycle(reg *Registry, evidenceID string, tick uint64) (Manifest, error) {
	if _, err := reg.RecordCustodyEvent(evidenceID, evidenceID+"-received", "PTY-1", CustodyReceived, tick, "received", ""); err != nil {
		return Manifest{}, fmt.Errorf("RecordCustodyEvent(RECEIVED): %w", err)
	}
	if _, err := reg.Advance(evidenceID, StateIngested, tick); err != nil {
		return Manifest{}, fmt.Errorf("Advance to INGESTED: %w", err)
	}
	if _, err := reg.RecordCustodyEvent(evidenceID, evidenceID+"-hashed", "PTY-1", CustodyHashed, tick, "hashed", "sha256:deadbeef"); err != nil {
		return Manifest{}, fmt.Errorf("RecordCustodyEvent(HASHED): %w", err)
	}
	if _, err := reg.Advance(evidenceID, StateIntegrityAssessed, tick); err != nil {
		return Manifest{}, fmt.Errorf("Advance to INTEGRITY_ASSESSED: %w", err)
	}
	if _, err := reg.Advance(evidenceID, StateProvenanceComplete, tick); err != nil {
		return Manifest{}, fmt.Errorf("Advance to PROVENANCE_COMPLETE: %w", err)
	}
	if _, err := reg.RecordCustodyEvent(evidenceID, evidenceID+"-reviewed", "PTY-1", CustodyReviewed, tick, "reviewed", "sha256:deadbeef"); err != nil {
		return Manifest{}, fmt.Errorf("RecordCustodyEvent(REVIEWED): %w", err)
	}
	if _, err := reg.Advance(evidenceID, StateReadyForFinalization, tick); err != nil {
		return Manifest{}, fmt.Errorf("Advance to READY_FOR_FINALIZATION: %w", err)
	}
	return reg.Advance(evidenceID, StateFinalized, tick)
}

// TestConcurrentDoubleFinalizeHasExactlyOneWinner is the reviewer's named
// "goroutine A: finalize(); goroutine B: finalize()" scenario. Advance's
// own lock plus its "cur.State == StateFinalized -> ErrFinalizedIsImmutable"
// guard must make this structurally impossible to double-win, not just
// unlikely under -race.
func TestConcurrentDoubleFinalizeHasExactlyOneWinner(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.RegisterDraft(validDraft("EV-1")); err != nil {
		t.Fatalf("RegisterDraft: %v", err)
	}
	advanceToReadyForFinalization(t, reg, "EV-1", 10)

	const n = 20
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := reg.Advance("EV-1", StateFinalized, 10)
			results[i] = err
		}(i)
	}
	close(start)
	wg.Wait()

	successes := 0
	for _, err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrFinalizedIsImmutable):
			// expected loser
		default:
			t.Fatalf("concurrent finalize() failed with an unexpected error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("expected exactly ONE of %d concurrent finalize() calls to win, got %d winners", n, successes)
	}

	latest, err := reg.Latest("EV-1")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if latest.State != StateFinalized {
		t.Fatalf("expected the manifest to end up FINALIZED, got %s", latest.State)
	}
	if err := VerifyManifestHash(latest); err != nil {
		t.Fatalf("the single winning finalize's hash must independently verify: %v", err)
	}
}

// TestConcurrentFinalizeAndCustodyMutateNeverStalesTheHash is the
// reviewer's "goroutine A: finalize(); goroutine B: mutate()" scenario.
// RecordCustodyEvent's own doc comment says the custody LOG stays
// append-only even after FINALIZED (a later ACCESSED/EXPORTED is
// legitimate) but the manifest's own CustodyChainHead field must freeze
// once FINALIZED, or ManifestHash silently goes stale. This proves that
// freeze holds even when the mutation is racing the finalize() call
// itself, not just when it happens safely afterward.
func TestConcurrentFinalizeAndCustodyMutateNeverStalesTheHash(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.RegisterDraft(validDraft("EV-1")); err != nil {
		t.Fatalf("RegisterDraft: %v", err)
	}
	advanceToReadyForFinalization(t, reg, "EV-1", 10)

	var wg sync.WaitGroup
	start := make(chan struct{})
	var finalizeErr error

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		_, finalizeErr = reg.Advance("EV-1", StateFinalized, 10)
	}()

	const mutations = 50
	mutateErrs := make([]error, mutations)
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < mutations; i++ {
			_, err := reg.RecordCustodyEvent("EV-1", fmt.Sprintf("EV-1-accessed-%d", i), "AUDITOR-1", CustodyAccessed, 10, "audit", "")
			mutateErrs[i] = err
		}
	}()

	close(start)
	wg.Wait()

	if finalizeErr != nil {
		t.Fatalf("finalize(): %v", finalizeErr)
	}
	for i, err := range mutateErrs {
		if err != nil {
			t.Fatalf("RecordCustodyEvent(ACCESSED) #%d: %v", i, err)
		}
	}

	latest, err := reg.Latest("EV-1")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if latest.State != StateFinalized {
		t.Fatalf("expected the manifest to end up FINALIZED, got %s", latest.State)
	}
	if err := VerifyManifestHash(latest); err != nil {
		t.Fatalf("finalize()'s hash must survive concurrent custody mutation, got: %v", err)
	}
	if err := reg.VerifyCustodyChain("EV-1"); err != nil {
		t.Fatalf("the append-only custody LOG must still independently verify: %v", err)
	}
	if got, want := len(reg.CustodyChain("EV-1")), 3+mutations; got != want {
		t.Fatalf("expected %d custody events (RECEIVED+HASHED+REVIEWED plus %d ACCESSED), got %d", want, mutations, got)
	}
}

// TestConcurrentFinalizeAndSupersedeNeverSupersedesAnUnfinalizedParent is
// the reviewer's "goroutine A: finalize(); goroutine C: supersede()"
// scenario. Supersede requires parent.State == StateFinalized; whichever
// goroutine loses the race for r.mu must see a consistent, non-torn view
// of that state, never a partially-applied finalize().
func TestConcurrentFinalizeAndSupersedeNeverSupersedesAnUnfinalizedParent(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.RegisterDraft(validDraft("EV-1")); err != nil {
		t.Fatalf("RegisterDraft: %v", err)
	}
	advanceToReadyForFinalization(t, reg, "EV-1", 10)

	next := validDraft("EV-1")
	next.AcquisitionRecord = "correction: re-collected under chain-of-custody PTY-2"

	var wg sync.WaitGroup
	start := make(chan struct{})
	var finalizeErr, supersedeErr error

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		_, finalizeErr = reg.Advance("EV-1", StateFinalized, 10)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		_, supersedeErr = reg.Supersede(next, 11)
	}()
	close(start)
	wg.Wait()

	if finalizeErr != nil {
		t.Fatalf("finalize(): %v", finalizeErr)
	}

	if supersedeErr != nil {
		if !errors.Is(supersedeErr, ErrParentNotFinalized) {
			t.Fatalf("expected ErrParentNotFinalized when supersede() loses the race against finalize(), got: %v", supersedeErr)
		}
		// supersede() ran before finalize() reached the lock; retried
		// AFTER finalize() has definitely completed, it must now succeed
		// legitimately -- the race cost it a retry, not correctness.
		if _, err := reg.Supersede(next, 11); err != nil {
			t.Fatalf("Supersede, retried after finalize() completed: %v", err)
		}
	}

	versions := reg.Versions("EV-1")
	if versions[0].State != StateSuperseded {
		t.Fatalf("expected version 1 to read SUPERSEDED once version 2 exists, got %s", versions[0].State)
	}
	if len(versions) != 2 || versions[1].State != StateDraft {
		t.Fatalf("expected exactly 2 versions with version 2 at DRAFT, got %d versions", len(versions))
	}
	// version 1's own ManifestHash was computed once, at the moment it
	// was FINALIZED, and Supersede never rewrites it -- only State
	// flips to SUPERSEDED afterward, exactly as LAW-04 promises ("the
	// historical version remains queryable and cryptographically
	// verifiable"). Re-verify it by asking what it would independently
	// verify AS OF the moment it was finalized (State flipped back to
	// FINALIZED on a copy) -- proving Supersede never touched any field
	// the hash covers, without going through VerifyManifestHash's own
	// State==FINALIZED precondition on the now-SUPERSEDED copy.
	asOfFinalization := versions[0]
	asOfFinalization.State = StateFinalized
	if err := VerifyManifestHash(asOfFinalization); err != nil {
		t.Fatalf("version 1's FINALIZED-time ManifestHash must survive the race to SUPERSEDED unchanged: %v", err)
	}
}

// TestConcurrentDuplicateAdvanceTransitionHasExactlyOneWinner is the
// reviewer's "duplicate transition" scenario: many goroutines racing to
// make the SAME structural transition (DRAFT -> INGESTED) exactly once.
// Only the first to reach the lock may legitimately move State; every
// later attempt must see the post-transition State and be refused as
// ErrInvalidTransition, not silently re-apply the same transition again.
func TestConcurrentDuplicateAdvanceTransitionHasExactlyOneWinner(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.RegisterDraft(validDraft("EV-1")); err != nil {
		t.Fatalf("RegisterDraft: %v", err)
	}
	if _, err := reg.RecordCustodyEvent("EV-1", "EV-1-received", "PTY-1", CustodyReceived, 10, "received", ""); err != nil {
		t.Fatalf("RecordCustodyEvent(RECEIVED): %v", err)
	}

	const n = 20
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := reg.Advance("EV-1", StateIngested, 10)
			results[i] = err
		}(i)
	}
	close(start)
	wg.Wait()

	successes := 0
	for _, err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrInvalidTransition):
			// expected loser: it observed State already == INGESTED
		default:
			t.Fatalf("duplicate Advance(INGESTED) failed with an unexpected error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("expected exactly ONE of %d concurrent duplicate DRAFT->INGESTED transitions to win, got %d", n, successes)
	}

	latest, err := reg.Latest("EV-1")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if latest.State != StateIngested {
		t.Fatalf("expected the manifest to land at INGESTED exactly once, got %s", latest.State)
	}
}

// TestConcurrentReplayDuringLiveMutationIsUnaffected is the reviewer's
// "replay during mutation" scenario, made concrete against this codebase:
// an independent replay of EV-1's full lifecycle against a brand-new
// Registry ("Node B", exactly as TestReplayReproducesIdenticalFinalizedState
// already proves for the sequential case) runs concurrently with ongoing,
// legitimate mutation (ACCESSED custody events) of the ORIGINAL, already-
// finalized manifest on the live Registry ("Node A"). Neither must
// interfere with the other: the replay must reproduce the exact same
// FINALIZED ManifestHash Node A computed before any live mutation began,
// and Node A's own FINALIZED hash must remain independently verifiable
// throughout.
func TestConcurrentReplayDuringLiveMutationIsUnaffected(t *testing.T) {
	liveReg := NewRegistry()
	if _, err := liveReg.RegisterDraft(validDraft("EV-1")); err != nil {
		t.Fatalf("RegisterDraft: %v", err)
	}
	finalized := advanceThroughFullLifecycle(t, liveReg, "EV-1", 10)

	var wg sync.WaitGroup
	start := make(chan struct{})

	var replayed Manifest
	var replayErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		replayReg := NewRegistry()
		if _, err := replayReg.RegisterDraft(validDraft("EV-1")); err != nil {
			replayErr = fmt.Errorf("replay RegisterDraft: %w", err)
			return
		}
		replayed, replayErr = replayThroughFullLifecycle(replayReg, "EV-1", 10)
	}()

	const mutations = 30
	mutateErrs := make([]error, mutations)
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < mutations; i++ {
			_, err := liveReg.RecordCustodyEvent("EV-1", fmt.Sprintf("EV-1-live-accessed-%d", i), "AUDITOR-1", CustodyAccessed, 10, "audit", "")
			mutateErrs[i] = err
		}
	}()

	close(start)
	wg.Wait()

	if replayErr != nil {
		t.Fatalf("replay: %v", replayErr)
	}
	for i, err := range mutateErrs {
		if err != nil {
			t.Fatalf("live RecordCustodyEvent(ACCESSED) #%d: %v", i, err)
		}
	}

	if replayed.ManifestHash != finalized.ManifestHash {
		t.Fatalf("replay's ManifestHash must equal the original finalized ManifestHash regardless of concurrent live mutation: replay=%s original=%s", replayed.ManifestHash, finalized.ManifestHash)
	}
	if err := VerifyManifestHash(replayed); err != nil {
		t.Fatalf("replayed manifest must independently verify: %v", err)
	}

	live, err := liveReg.Latest("EV-1")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if err := VerifyManifestHash(live); err != nil {
		t.Fatalf("live manifest's FINALIZED hash must still verify after concurrent mutation: %v", err)
	}
	if got, want := len(liveReg.CustodyChain("EV-1")), 3+mutations; got != want {
		t.Fatalf("expected %d live custody events (3 lifecycle + %d ACCESSED), got %d", want, mutations, got)
	}
}
