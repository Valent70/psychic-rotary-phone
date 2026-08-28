package registry

import "testing"

func TestTrustAPI_EvaluateCertifyLedger(t *testing.T) {
	api := NewTrustAPI()
	score, err := api.Evaluate("source-a", 0.95, 0.02, 1)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if score.Score <= 0.7 {
		t.Fatalf("expected a high score for good evidence, got %v", score.Score)
	}

	cert, err := api.Certify("source-a", 0.95, 0.02, 2)
	if err != nil {
		t.Fatalf("Certify: %v", err)
	}
	if cert.Score <= 0.7 {
		t.Fatalf("expected certification to clear threshold, got %v", cert.Score)
	}

	ledger, err := api.Ledger()
	if err != nil || len(ledger) != 1 {
		t.Fatalf("expected 1 issued certificate on the ledger, got %d (err=%v)", len(ledger), err)
	}
	msg, err := api.VerifyLedger()
	if err != nil {
		t.Fatalf("VerifyLedger: %v (msg=%s)", err, msg)
	}
}

func TestIntentAPI_VerifyAndCertify(t *testing.T) {
	api, err := NewIntentAPI()
	if err != nil {
		t.Fatalf("NewIntentAPI: %v", err)
	}
	verdict, err := api.Verify("i1", "vessel/IMO1", "FLAG", "FLAG", 0.6, 0.61, 1)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !verdict.ActionMatched {
		t.Fatalf("expected matched action, got %+v", verdict)
	}

	verdict2, err := api.Certify("i2", "vessel/IMO1", "FLAG", "FLAG", 0.6, 0.6, 2)
	if err != nil {
		t.Fatalf("Certify: %v", err)
	}
	if !verdict2.ActionMatched {
		t.Fatalf("expected matched action on certify, got %+v", verdict2)
	}
	ledger, err := api.Ledger()
	if err != nil {
		t.Fatalf("Ledger: %v", err)
	}
	if len(ledger) == 0 {
		t.Fatal("expected at least one certificate issued across Verify/Certify calls")
	}
}

func TestNew_BuildsBothEngines(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if r.Trust == nil || r.Intent == nil {
		t.Fatal("expected both Trust and Intent engines wired")
	}
}
