package evidence

import (
	"errors"
	"sync"
	"testing"

	"veriqo/pkg/evidence/provenance"
)

// This file is the pkg/insurance/evidence half of the reviewer's
// concurrency audit ask: -race already proves Registry's own sync.RWMutex
// makes every method individually free of data races, but "race-free !=
// state-machine-safe under concurrent transitions" -- these tests run
// real goroutines and assert on the SEMANTIC outcome (exactly what ended
// up true, and that no authority boundary was crossed by the race)
// rather than merely the absence of -race findings. The manifest-package
// equivalents (pkg/evidence/manifest/concurrency_test.go) cover finalize/
// mutate/supersede/duplicate-transition/replay; this file covers the two
// remaining named scenarios that live in THIS registry instead:
// "supersede + submit" and "concurrent rights change".

// TestConcurrentMarkSupersededAndDuplicateSubmitNeverResurrectsEvidence is
// the reviewer's named "supersede + submit" scenario. While MarkSuperseded
// is legitimately retiring record A in favor of B, a swarm of concurrent
// callers each attempt to Submit(A) again -- as if trying to race a
// duplicate/resurrected copy of A back into the registry while its
// supersession is in flight. Submit's own duplicate-EvidenceID check must
// hold regardless of how these interleave with MarkSuperseded's write:
// every duplicate Submit must be refused, and A's own supersession fields
// must never be observed torn or overwritten by a losing Submit attempt.
func TestConcurrentMarkSupersededAndDuplicateSubmitNeverResurrectsEvidence(t *testing.T) {
	reg := NewRegistry()
	recA, err := New("CASE-1", mustEvidence(t, "S1", "src-a", 100), "PTY-1", OriginClaimant)
	if err != nil {
		t.Fatalf("New(A): %v", err)
	}
	if err := reg.Submit(recA); err != nil {
		t.Fatalf("Submit(A): %v", err)
	}
	recB, err := New("CASE-1", mustEvidence(t, "S1", "src-b", 200), "PTY-1", OriginClaimant)
	if err != nil {
		t.Fatalf("New(B): %v", err)
	}
	if err := reg.Submit(recB); err != nil {
		t.Fatalf("Submit(B): %v", err)
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	var supersedeErr error

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		supersedeErr = reg.MarkSuperseded(recA.EvidenceID(), recB.EvidenceID(), "governance-lead-1", "duplicate document, B supersedes A", 300)
	}()

	const attempts = 20
	submitErrs := make([]error, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			submitErrs[i] = reg.Submit(recA)
		}(i)
	}

	close(start)
	wg.Wait()

	if supersedeErr != nil {
		t.Fatalf("MarkSuperseded: %v", supersedeErr)
	}
	for i, err := range submitErrs {
		if !errors.Is(err, ErrDuplicateEvidence) {
			t.Fatalf("concurrent duplicate Submit(A) #%d: expected ErrDuplicateEvidence, got %v", i, err)
		}
	}

	got, ok := reg.Get(recA.EvidenceID())
	if !ok {
		t.Fatal("expected A to still be present in the registry")
	}
	if !got.CorrectionSuperseded {
		t.Fatal("expected A to read CorrectionSuperseded=true regardless of the concurrent duplicate Submit attempts")
	}
	if got.SupersededBy != recB.EvidenceID() {
		t.Fatalf("expected A.SupersededBy=%s, got %q -- a losing duplicate Submit must never overwrite this", recB.EvidenceID(), got.SupersededBy)
	}
	if got.Status != StatusUnverified || got.Rights != provenance.RightsUnknownPendingContract {
		t.Fatalf("expected A's authority-bearing fields to remain exactly what Submit(A) originally set (Status=%s Rights=%s), untouched by any losing duplicate Submit", got.Status, got.Rights)
	}
}

// TestConcurrentSetRightsChangesConvergeToASingleAuthorizedValue is the
// reviewer's named "concurrent rights change" scenario. Multiple
// authorized SetRights calls naming DIFFERENT target rights states race
// against each other, interleaved with calls from an UNTRUSTED authority
// attempting the same. The untrusted calls must be refused no matter how
// they interleave with the legitimate ones (the authority gate itself
// must be race-safe, not just individually correct), and the record's
// final Rights value must land on exactly one of the legitimately
// requested states -- never torn, never a value nobody actually
// requested, never the untrusted caller's forbidden target.
func TestConcurrentSetRightsChangesConvergeToASingleAuthorizedValue(t *testing.T) {
	reg := NewRegistry()
	provReg, authorityID := trustedAuthorityRegistry(t)
	// A second, genuinely trusted authority -- proves the race is about
	// the RECORD's final state converging safely, not about there being
	// only one legitimate caller.
	const authority2ID = "governance-lead-2"
	if err := provReg.Register(provenance.Entry{ID: authority2ID, Kind: provenance.KindReviewer, Name: "Governance Lead 2"}); err != nil {
		t.Fatalf("provenance.Register(authority2): %v", err)
	}
	if err := provReg.GrantTrust(authority2ID, "policy://rights-grant-v1", "", "compliance-officer", 1); err != nil {
		t.Fatalf("provenance.GrantTrust(authority2): %v", err)
	}
	// An untrusted third party -- Registered, but its trust was never
	// granted.
	const untrustedID = "unproven-party"
	if err := provReg.Register(provenance.Entry{ID: untrustedID, Kind: provenance.KindReviewer, Name: "Unproven Party"}); err != nil {
		t.Fatalf("provenance.Register(untrusted): %v", err)
	}

	rec, err := New("CASE-1", mustEvidence(t, "S1", "src", 100), "PTY-1", OriginClaimant)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := reg.Submit(rec); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	const rounds = 15
	var wg sync.WaitGroup
	start := make(chan struct{})

	authorizedErrs := make([]error, 2*rounds)
	for i := 0; i < rounds; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			authorizedErrs[2*i] = reg.SetRights(rec.EvidenceID(), provenance.RightsCustomerFacingAllowed, provReg, authorityID)
		}(i)
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			authorizedErrs[2*i+1] = reg.SetRights(rec.EvidenceID(), provenance.RightsDisputeUseAllowed, provReg, authority2ID)
		}(i)
	}

	untrustedErrs := make([]error, rounds)
	for i := 0; i < rounds; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			untrustedErrs[i] = reg.SetRights(rec.EvidenceID(), provenance.RightsCalibrationAllowed, provReg, untrustedID)
		}(i)
	}

	close(start)
	wg.Wait()

	for i, err := range authorizedErrs {
		if err != nil {
			t.Fatalf("authorized concurrent SetRights #%d: %v", i, err)
		}
	}
	for i, err := range untrustedErrs {
		if !errors.Is(err, ErrRightsGrantNotAuthorized) {
			t.Fatalf("untrusted concurrent SetRights #%d: expected ErrRightsGrantNotAuthorized, got %v", i, err)
		}
	}

	got, ok := reg.Get(rec.EvidenceID())
	if !ok {
		t.Fatal("expected the record to still be present")
	}
	if got.Rights != provenance.RightsCustomerFacingAllowed && got.Rights != provenance.RightsDisputeUseAllowed {
		t.Fatalf("expected the final Rights to be exactly one of the two legitimately-requested values, got %q -- either torn state or the untrusted caller's forbidden target leaked through", got.Rights)
	}
}
