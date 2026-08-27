package sdk

import (
	"testing"

	"veriqo/veriqo/registry"
)

func TestClient_TrustAndIntentRoundTrip(t *testing.T) {
	reg, err := registry.New()
	if err != nil {
		t.Fatalf("registry.New: %v", err)
	}
	c := NewClient(reg)

	score, err := c.TrustEvaluate("sdk-source", 0.9, 0.05, 1)
	if err != nil {
		t.Fatalf("TrustEvaluate: %v", err)
	}
	if score.Score <= 0.7 {
		t.Fatalf("expected a high score, got %v", score.Score)
	}

	verdict, err := c.IntentVerify("sdk-intent-1", "vessel/X", "FLAG", "FLAG", 0.5, 0.5, 1)
	if err != nil {
		t.Fatalf("IntentVerify: %v", err)
	}
	if !verdict.ActionMatched {
		t.Fatalf("expected matched action, got %+v", verdict)
	}
}
