package provenance

import (
	"errors"
	"testing"
)

func buildThreeHopChain() *Chain {
	c := NewChain("manifest-42")
	c.Append("provider-a", "PRODUCED", "hash-v1", 1)
	c.Append("provider-b", "TRANSFORMED", "hash-v2", 2)
	c.Append("provider-c", "VALIDATED", "hash-v2", 3)
	return c
}

func TestChainAppendLinksHopsAndVerifies(t *testing.T) {
	c := buildThreeHopChain()
	hops := c.Hops()
	if len(hops) != 3 {
		t.Fatalf("expected 3 hops, got %d", len(hops))
	}
	if hops[0].PrevHash != "" {
		t.Fatalf("expected first hop's PrevHash to be empty, got %q", hops[0].PrevHash)
	}
	for i := 1; i < len(hops); i++ {
		if hops[i].PrevHash != hops[i-1].Hash {
			t.Fatalf("hop %d: PrevHash %q does not match hop %d's Hash %q", i, hops[i].PrevHash, i-1, hops[i-1].Hash)
		}
	}
	if c.Head() != hops[len(hops)-1].Hash {
		t.Fatalf("expected Head() to be the last hop's hash")
	}
	if err := c.VerifyChain(); err != nil {
		t.Fatalf("expected an untampered chain to verify, got %v", err)
	}
}

func TestChainIndicesAreSequential(t *testing.T) {
	c := buildThreeHopChain()
	for i, h := range c.Hops() {
		if h.Index != uint64(i) {
			t.Errorf("hop %d: expected Index %d, got %d", i, i, h.Index)
		}
	}
}

func TestVerifyChainDetectsTamperedContentHash(t *testing.T) {
	c := buildThreeHopChain()
	// Directly mutate internal state to simulate an at-rest tamper --
	// e.g. a persisted chain edited outside this package's API.
	c.hops[1].ContentHash = "attacker-modified-hash"
	if err := c.VerifyChain(); !errors.Is(err, ErrTamperDetected) {
		t.Fatalf("expected ErrTamperDetected for a tampered content hash, got %v", err)
	}
}

func TestVerifyChainDetectsBrokenPrevHashLink(t *testing.T) {
	c := buildThreeHopChain()
	c.hops[2].PrevHash = "attacker-forged-prev-hash"
	if err := c.VerifyChain(); !errors.Is(err, ErrTamperDetected) {
		t.Fatalf("expected ErrTamperDetected for a broken PrevHash link, got %v", err)
	}
}

func TestVerifyChainDetectsReorderedHops(t *testing.T) {
	c := buildThreeHopChain()
	hops := c.hops
	hops[1], hops[2] = hops[2], hops[1]
	c.hops = hops
	if err := c.VerifyChain(); !errors.Is(err, ErrTamperDetected) {
		t.Fatalf("expected ErrTamperDetected for reordered hops, got %v", err)
	}
}

func TestEmptyChainVerifiesTrivially(t *testing.T) {
	c := NewChain("empty-subject")
	if err := c.VerifyChain(); err != nil {
		t.Fatalf("expected an empty chain to verify, got %v", err)
	}
	if c.Head() != "" {
		t.Fatalf("expected an empty chain's Head() to be empty, got %q", c.Head())
	}
}

func TestHopsReturnsACopy(t *testing.T) {
	c := buildThreeHopChain()
	hops := c.Hops()
	hops[0].ContentHash = "mutated-by-caller"
	if err := c.VerifyChain(); err != nil {
		t.Fatalf("expected mutating the returned slice to leave the chain's internal state untouched, got %v", err)
	}
}

func TestProviderFields(t *testing.T) {
	p := Provider{ProviderID: "p-1", Name: "Example Data Co", ContractReference: "CTR-2026-001"}
	if p.ProviderID != "p-1" || p.Name != "Example Data Co" || p.ContractReference != "CTR-2026-001" {
		t.Fatalf("unexpected Provider fields: %+v", p)
	}
}
