package blockers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func registerRepoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repo root %s has no go.mod: %v", root, err)
	}
	return root
}

// TestEveryCapabilityCitesRealCode is what stops this register decaying
// into documentation about code that was renamed or deleted. Every row
// names a package and a symbol; the symbol must genuinely appear as a
// declaration in that package's non-test source.
func TestEveryCapabilityCitesRealCode(t *testing.T) {
	root := registerRepoRoot(t)
	for _, c := range CapabilityRegister() {
		dir := filepath.Join(root, filepath.FromSlash(c.Package))
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Errorf("%s/%s: package %s does not exist: %v", c.GateID, c.Name, c.Package, err)
			continue
		}
		found := false
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(dir, e.Name())) // #nosec G304 -- path built from this file's own hardcoded register
			if err != nil {
				continue
			}
			src := string(raw)
			// A declaration, not a mention: the symbol must appear after
			// one of Go's declaring keywords, or as a method receiver.
			for _, form := range []string{
				"type " + c.Symbol + " ", "type " + c.Symbol + "\t",
				"func " + c.Symbol + "(", "func " + c.Symbol + "[",
				") " + c.Symbol + "(", // method
			} {
				if strings.Contains(src, form) {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			t.Errorf("%s/%s: cites %s.%s, which is not declared in that package's source",
				c.GateID, c.Name, c.Package, c.Symbol)
		}
	}
}

// TestRegisterIsInternallyConsistent: every row complete, and every
// non-exercised row explains its shortfall.
func TestRegisterIsInternallyConsistent(t *testing.T) {
	if problems := ValidateRegister(); len(problems) != 0 {
		for _, p := range problems {
			t.Errorf("register problem: %s", p)
		}
	}
}

// TestEveryBlockedGateHasCapabilityRows keeps the register's coverage
// aligned with the eight gates that actually exist, so a gate cannot be
// silently omitted.
func TestEveryBlockedGateHasCapabilityRows(t *testing.T) {
	covered := map[string]bool{}
	for _, g := range GateIDsWithCapabilities() {
		covered[g] = true
	}
	for _, want := range wantBlockerIDs {
		if !covered[want] {
			t.Errorf("gate %s has no capability rows -- its harness coverage is unstated", want)
		}
	}
	if len(covered) != len(wantBlockerIDs) {
		t.Errorf("register covers %d gates, there are %d blockers", len(covered), len(wantBlockerIDs))
	}
}

// TestEveryGateStillNamesSomethingExternal is the honesty assertion for
// this phase. If any of the eight ever reported zero external-only
// capabilities, that would mean a harness had quietly claimed to cover
// the thing that makes the gate external -- which is precisely the false
// green this programme exists to prevent.
func TestEveryGateStillNamesSomethingExternal(t *testing.T) {
	for _, s := range Summarize() {
		if s.ExternalOnly == 0 {
			t.Errorf("gate %s reports NO external-only capability; an externally-blocked gate must always name "+
				"what no harness can supply", s.GateID)
		}
		if len(s.StillExternal) != s.ExternalOnly {
			t.Errorf("gate %s: %d external-only capabilities but %d named", s.GateID, s.ExternalOnly, len(s.StillExternal))
		}
		if s.Total == 0 {
			t.Errorf("gate %s has no capability rows at all", s.GateID)
		}
	}
}

// TestHarnessCompletenessIsNotGateStatus records the distinction the
// whole register exists to make: a gate can be harness-complete and
// permanently blocked, which is exactly the situation all eight are in.
func TestHarnessCompletenessIsNotGateStatus(t *testing.T) {
	completeAndBlocked := 0
	for _, s := range Summarize() {
		if s.HarnessComplete && s.ExternalOnly > 0 {
			completeAndBlocked++
		}
	}
	if completeAndBlocked == 0 {
		t.Fatal("no gate is both harness-complete and still externally blocked -- that combination is the " +
			"normal, correct state for all eight, so its absence means the register is measuring the wrong thing")
	}
}

// TestHarnessCanNeverQualifyIsStated keeps the invariant assertable
// rather than merely commented.
func TestHarnessCanNeverQualifyIsStated(t *testing.T) {
	s := HarnessCanNeverQualify()
	if s == "" {
		t.Fatal("the invariant is not stated")
	}
	for _, must := range []string{"qualification", "provider", "release"} {
		if !strings.Contains(strings.ToLower(s), must) {
			t.Errorf("the invariant does not mention %q", must)
		}
	}
}

// TestNoCapabilityStatusCanExpressQualification is the structural half
// of the same invariant: the vocabulary itself has no value that could
// be mistaken for a qualified gate.
func TestNoCapabilityStatusCanExpressQualification(t *testing.T) {
	for _, s := range []CapabilityStatus{CapabilityExercised, CapabilityPartial, CapabilityExternalOnly} {
		upper := strings.ToUpper(string(s))
		for _, forbidden := range []string{"QUALIFIED", "VERIFIED", "DONE", "CLOSED", "COMPLETE", "PASS"} {
			if strings.Contains(upper, forbidden) {
				t.Errorf("capability status %q contains %q -- the vocabulary must not be confusable with a gate status",
					s, forbidden)
			}
		}
	}
}

// TestSummarizeIsDeterministic guards the artifact this feeds.
func TestSummarizeIsDeterministic(t *testing.T) {
	a, b := Summarize(), Summarize()
	if len(a) != len(b) {
		t.Fatal("Summarize returned different lengths")
	}
	for i := range a {
		if a[i].GateID != b[i].GateID || a[i].Total != b[i].Total {
			t.Fatalf("Summarize is not deterministic at index %d", i)
		}
	}
	reg1, reg2 := CapabilityRegister(), CapabilityRegister()
	for i := range reg1 {
		if reg1[i].Name != reg2[i].Name || reg1[i].GateID != reg2[i].GateID {
			t.Fatalf("CapabilityRegister is not deterministic at index %d", i)
		}
	}
}
