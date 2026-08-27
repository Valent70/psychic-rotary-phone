// This test is the strongest available proof against the "ilusi
// komputer" (illusion of computer) concern raised in the consolidated
// audit: it does not merely call Go functions in the same process. It
// writes a real VerificationBundle to a real file on disk, then
// launches cmd/veriqo-verify — a SEPARATE COMPILED BINARY, in its own
// OS process, with its own memory space — via os/exec, exactly the way
// an outside auditor with no access to Veriqo's running system would
// use it. The only channel of information from this test process to
// the verifier process is the bundle file's bytes.
package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"veriqo/pkg/engine"
	"veriqo/pkg/moat/fusion"
)

// buildVeriqoVerify compiles cmd/veriqo-verify once per test run into a
// temp binary, returning its path. Building once (rather than `go run`
// per sub-test) keeps this fast and also exercises that the binary
// actually compiles standalone.
func buildVeriqoVerify(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve test source location")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	binPath := filepath.Join(t.TempDir(), "veriqo-verify")
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/veriqo-verify")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("building cmd/veriqo-verify: %v\n%s", err, out)
	}
	return binPath
}

func TestIVFCrossProcess_FusionPass(t *testing.T) {
	bin := buildVeriqoVerify(t)

	adapter := engine.NewFusionEngineAdapter("veriqo-demo")
	must(t, adapter.RegisterSource(fusion.SourceProfile{ID: "AIS-A", BaseReliability: 0.9}))
	must(t, adapter.RegisterSource(fusion.SourceProfile{ID: "SAT-B", BaseReliability: 0.85, Provider: "orbital-corp"}))
	claim := fusion.Claim{Subject: "VESSEL:IMOXPROC", Predicate: "BENEFICIAL_OWNER"}
	if _, err := adapter.Submit("AIS-A", claim, "ACME-HOLDINGS", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Submit("SAT-B", claim, "ACME-HOLDINGS", 11); err != nil {
		t.Fatal(err)
	}
	k := engine.NewKernel()
	must(t, k.Register(adapter))
	rec, err := k.RunAndVerify("fusion", engine.Context{Subject: "VESSEL:IMOXPROC|BENEFICIAL_OWNER", Tick: 20})
	must(t, err)
	if rec.Bundle == nil {
		t.Fatal("expected a bundle")
	}

	bundlePath := filepath.Join(t.TempDir(), "bundle.json")
	writeJSON(t, bundlePath, rec.Bundle)

	cmd := exec.Command(bin, "-bundle", bundlePath, "-domain", "fusion")
	out, err := cmd.CombinedOutput()
	t.Logf("veriqo-verify output:\n%s", out)
	if err != nil {
		t.Fatalf("external validator process exited non-zero on a genuine bundle: %v", err)
	}
	if !contains(string(out), "VERDICT          : PASSED") {
		t.Fatalf("expected PASSED verdict in external validator output, got:\n%s", out)
	}
}

func TestIVFCrossProcess_TamperedBundleFails(t *testing.T) {
	bin := buildVeriqoVerify(t)

	adapter := engine.NewFusionEngineAdapter("veriqo-demo")
	must(t, adapter.RegisterSource(fusion.SourceProfile{ID: "AIS-A", BaseReliability: 0.9}))
	claim := fusion.Claim{Subject: "VESSEL:IMOXPROC2", Predicate: "FLAG_STATE"}
	if _, err := adapter.Submit("AIS-A", claim, "PANAMA", 10); err != nil {
		t.Fatal(err)
	}
	k := engine.NewKernel()
	must(t, k.Register(adapter))
	rec, err := k.RunAndVerify("fusion", engine.Context{Subject: "VESSEL:IMOXPROC2|FLAG_STATE", Tick: 20})
	must(t, err)

	// An attacker forges a better-looking claimed result without being
	// able to recompute a matching RootHash.
	tampered := *rec.Bundle
	tampered.ReplayPackage.ClaimedResult = "winner=LIBERIA|confidence=0.9999999999|contradiction=false"

	bundlePath := filepath.Join(t.TempDir(), "tampered.json")
	writeJSON(t, bundlePath, &tampered)

	cmd := exec.Command(bin, "-bundle", bundlePath, "-domain", "fusion")
	out, err := cmd.CombinedOutput()
	t.Logf("veriqo-verify output:\n%s", out)
	if err == nil {
		t.Fatal("expected the external validator process to exit non-zero on a tampered bundle")
	}
	if !contains(string(out), "FAILED") {
		t.Fatalf("expected FAILED verdict in external validator output, got:\n%s", out)
	}
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
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
