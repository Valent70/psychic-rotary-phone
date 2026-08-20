package generator

import "testing"

func TestLoadContracts_ParsesRealContractFiles(t *testing.T) {
	contracts, err := LoadContracts("../contracts")
	if err != nil {
		t.Fatalf("LoadContracts: %v", err)
	}
	wantEngines := []string{"evidence", "intent", "policy", "reasoning", "replay", "trust", "verification"}
	if len(contracts) != len(wantEngines) {
		t.Fatalf("want %d contracts, got %d", len(wantEngines), len(contracts))
	}
	for i, want := range wantEngines {
		if contracts[i].Engine != want {
			t.Fatalf("contract[%d] = %q, want %q (sorted-by-engine-name order)", i, contracts[i].Engine, want)
		}
	}
	for _, c := range contracts {
		if c.Engine == "trust" {
			for _, m := range c.Methods {
				if m.Name == "evaluate" && len(m.Params) != 4 {
					t.Fatalf("trust.evaluate: want 4 params, got %d", len(m.Params))
				}
			}
		}
	}
}

func TestGenerateCLI_ProducesCompilableStructure(t *testing.T) {
	contracts, err := LoadContracts("../contracts")
	if err != nil {
		t.Fatal(err)
	}
	src, err := GenerateCLI(contracts)
	if err != nil {
		t.Fatalf("GenerateCLI: %v", err)
	}
	if len(src) == 0 {
		t.Fatal("expected non-empty generated CLI source")
	}
	got := string(src)
	for _, want := range []string{"cmdTrustEvaluate", "cmdIntentVerify", "\"trust evaluate\":", "package main"} {
		if !contains(got, want) {
			t.Fatalf("generated CLI source missing expected fragment %q", want)
		}
	}
}

func TestGenerateSDK_ProducesCompilableStructure(t *testing.T) {
	contracts, err := LoadContracts("../contracts")
	if err != nil {
		t.Fatal(err)
	}
	src, err := GenerateSDK(contracts)
	if err != nil {
		t.Fatalf("GenerateSDK: %v", err)
	}
	got := string(src)
	for _, want := range []string{"func (c *Client) TrustEvaluate(", "func (c *Client) IntentVerify(", "package sdk"} {
		if !contains(got, want) {
			t.Fatalf("generated SDK source missing expected fragment %q", want)
		}
	}
}

func TestGenerateREST_ProducesCompilableStructure(t *testing.T) {
	contracts, err := LoadContracts("../contracts")
	if err != nil {
		t.Fatal(err)
	}
	src, err := GenerateREST(contracts)
	if err != nil {
		t.Fatalf("GenerateREST: %v", err)
	}
	got := string(src)
	for _, want := range []string{
		"package rest", "func Routes(", "\"/trust/evaluate\":",
		"\"/evidence/add_node\":", "\"/policy/evaluate\":",
		"\"/reasoning/infer\":", "\"/verification/verify\":", "\"/replay/contradiction\":",
	} {
		if !contains(got, want) {
			t.Fatalf("generated REST source missing expected fragment %q", want)
		}
	}
}

func TestGeneratePython_ProducesExpectedMethods(t *testing.T) {
	contracts, err := LoadContracts("../contracts")
	if err != nil {
		t.Fatal(err)
	}
	src, err := GeneratePython(contracts)
	if err != nil {
		t.Fatalf("GeneratePython: %v", err)
	}
	got := string(src)
	for _, want := range []string{
		"class VeriqoClient", "def trust_evaluate(self,",
		"def evidence_add_edge(self, from_, to, relation):", "\"from\": from_,",
	} {
		if !contains(got, want) {
			t.Fatalf("generated Python source missing expected fragment %q", want)
		}
	}
}

func TestGenerateTypeScript_ProducesExpectedMethods(t *testing.T) {
	contracts, err := LoadContracts("../contracts")
	if err != nil {
		t.Fatal(err)
	}
	src, err := GenerateTypeScript(contracts)
	if err != nil {
		t.Fatalf("GenerateTypeScript: %v", err)
	}
	got := string(src)
	for _, want := range []string{
		"export class VeriqoClient", "async trustEvaluate(", "async evidenceAddNode(",
	} {
		if !contains(got, want) {
			t.Fatalf("generated TypeScript source missing expected fragment %q", want)
		}
	}
}

func TestGoMethodName(t *testing.T) {
	cases := map[string]string{
		"verify_ledger": "VerifyLedger",
		"evaluate":      "Evaluate",
		"":              "",
	}
	for in, want := range cases {
		if got := GoMethodName(in); got != want {
			t.Fatalf("GoMethodName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoadContracts_MissingDir(t *testing.T) {
	if _, err := LoadContracts("/no/such/dir"); err == nil {
		t.Fatal("expected an error for a missing contracts directory")
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
