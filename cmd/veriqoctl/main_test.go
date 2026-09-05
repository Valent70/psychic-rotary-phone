package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"veriqo/pkg/verification"
)

// The tests here are about the WIRING, not about the content of the
// reports -- each report's own package tests what it says. What is not
// tested anywhere else is the thing that has actually broken: a report
// that exists but cannot be reached, a subcommand advertised in the
// usage text that does not exist, and a report that changes between two
// runs of a tool whose whole value is that two runs can be diffed.

// TestEveryReportIsReachable holds the two tables together.
//
// This is a regression test for a real defect: "ladder" was added to
// `order` and not to `reports`, so `veriqoctl all` indexed a nil
// function. The failure was a panic in a tool whose purpose is to be
// trustworthy about its own state.
func TestEveryReportIsReachable(t *testing.T) {
	seen := map[string]bool{}
	for _, name := range order {
		if _, ok := reports[name]; !ok {
			t.Errorf("%q is printed by `all` but has no report function; "+
				"`veriqoctl all` would call nil", name)
		}
		if seen[name] {
			t.Errorf("%q appears twice in the print order", name)
		}
		seen[name] = true
	}
	for name := range reports {
		if !seen[name] {
			t.Errorf("report %q exists but `veriqoctl all` never prints it, so it is "+
				"only reachable by somebody who already knows it is there", name)
		}
	}
}

// TestTheUsageTextAdvertisesOnlyReportsThatExist.
//
// The usage text is the only description of this tool most readers will
// ever see. A subcommand listed there and not implemented exits 2 with
// "unknown report", which reads as the reader's mistake rather than
// ours.
func TestTheUsageTextAdvertisesOnlyReportsThatExist(t *testing.T) {
	advertised := advertisedSubcommands(t)
	if len(advertised) == 0 {
		t.Fatal("parsed no subcommands out of the usage text; the parser is wrong")
	}
	for _, name := range advertised {
		switch name {
		case "all", "capsule":
			continue // handled outside the reports map, deliberately
		}
		if _, ok := reports[name]; !ok {
			t.Errorf("the usage text offers %q, which is not a report", name)
		}
	}
	// And the other direction: a report nobody is told about.
	adv := map[string]bool{}
	for _, n := range advertised {
		adv[n] = true
	}
	for name := range reports {
		if !adv[name] {
			t.Errorf("report %q is not listed in the usage text", name)
		}
	}
}

// TestEveryReportProducesSomethingAReaderCanDisagreeWith.
//
// A report that returns an error, or returns nothing, is worse than an
// absent one: `veriqoctl all` continues past it and the reader sees a
// gap between two separator rules without being told a report failed.
func TestEveryReportProducesSomethingAReaderCanDisagreeWith(t *testing.T) {
	for _, name := range order {
		t.Run(name, func(t *testing.T) {
			out, err := reports[name]()
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if strings.TrimSpace(out) == "" {
				t.Fatal("produced no output")
			}
			if !strings.HasSuffix(out, "\n") {
				t.Error("does not end in a newline, so `all` runs it into the separator")
			}
			// A report of one line is a summary, and this tool exists
			// specifically not to emit summaries.
			if n := strings.Count(out, "\n"); n < 3 {
				t.Errorf("%d line(s): too short to carry what it rests on", n)
			}
		})
	}
}

// TestTwoRunsAreByteIdentical is why cmd/veriqoctl has a fixed clock.
//
// The tool's stated value is that a reader can diff yesterday's report
// against today's and see only what actually changed. A report that
// carries time.Now() destroys that: every line differs, and the real
// change hides in the noise.
func TestTwoRunsAreByteIdentical(t *testing.T) {
	for _, name := range order {
		first, err := reports[name]()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		second, err := reports[name]()
		if err != nil {
			t.Fatalf("%s (second run): %v", name, err)
		}
		if first != second {
			t.Errorf("%s differs between two runs in the same process; a diff of two "+
				"reports would show noise instead of change", name)
		}
	}
}

// TestNoReportClaimsAQualificationVeriqoDoesNotHave.
//
// This is the overclaim check applied to the reporting tool itself. The
// words below are the ones an executive reader skims for, and none of
// them is true of this system: no external party has assessed it.
func TestNoReportClaimsAQualificationVeriqoDoesNotHave(t *testing.T) {
	banned := []string{
		"CERTIFIED", "ACCREDITED", "SOC 2 COMPLIANT", "ISO 27001 CERTIFIED",
		"INDEPENDENTLY VERIFIED", "PRODUCTION READY", "PRODUCTION-READY",
	}
	for _, name := range order {
		out, err := reports[name]()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		upper := strings.ToUpper(out)
		for _, word := range banned {
			// The reports are allowed -- required, even -- to say what
			// is NOT the case. Only an unqualified claim is a defect,
			// and every legitimate use here is preceded by a negation.
			for _, idx := range indices(upper, word) {
				if negatedBefore(upper, idx) {
					continue
				}
				t.Errorf("%s claims %q without a negation in front of it: %q",
					name, word, context(out, idx))
			}
		}
	}
}

// TestTheCapsuleVerifierAgreesWithTheCapsuleThisToolWrites.
//
// The end-to-end promise printed by `veriqoctl capsule`: "check it with
// veriqo-verify". If that ever stops holding, the capsule is a document
// that asks to be trusted, which is the one thing it exists not to be.
func TestTheCapsuleVerifierAgreesWithTheCapsuleThisToolWrites(t *testing.T) {
	dir := t.TempDir()
	if err := writeCapsule([]string{"capsule", dir}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("the capsule directory is empty")
	}
	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err != nil {
		t.Fatalf("no manifest: %v", err)
	}

	b, err := verification.Open(dir)
	if err != nil {
		t.Fatalf("the capsule this tool just wrote does not match its own manifest: %v", err)
	}
	r := verification.Verify(b, verification.Options{})
	if !r.Verified() {
		t.Fatalf("veriqo-verify rejects the capsule veriqoctl wrote:\n%s", r.Render())
	}
}

// TestCapsuleWithoutADirectoryIsRefusedRatherThanGuessed.
func TestCapsuleWithoutADirectoryIsRefusedRatherThanGuessed(t *testing.T) {
	for _, args := range [][]string{{"capsule"}, {"capsule", "a", "b"}} {
		if err := writeCapsule(args); err == nil {
			t.Errorf("%v was accepted; a capsule written to a guessed path is a capsule "+
				"nobody can find", args)
		}
	}
}

// TestTheFixedInstantIsFixed.
func TestTheFixedInstantIsFixed(t *testing.T) {
	if !zeroInstant().Equal(zeroInstant()) {
		t.Fatal("zeroInstant() is not fixed")
	}
	if zeroInstant().Location() == nil || zeroInstant().Location().String() != "UTC" {
		t.Error("the fixed instant is not in UTC, so the report depends on the host's zone")
	}
}

// advertisedSubcommands reads the subcommand names out of the usage
// text, which is the contract with the reader.
func advertisedSubcommands(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(usage, "\n") {
		if !strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "   ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := fields[0]
		if name != strings.ToLower(name) || strings.ContainsAny(name, "-.,:") {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func indices(hay, needle string) []int {
	var out []int
	for i := 0; ; {
		j := strings.Index(hay[i:], needle)
		if j < 0 {
			return out
		}
		out = append(out, i+j)
		i += j + len(needle)
	}
}

// negatedBefore reports whether the 120 characters before an offset
// deny the claim that follows. The window is generous on purpose: a
// false positive here costs a reader nothing, and a false negative
// lets an unqualified claim through.
func negatedBefore(upper string, idx int) bool {
	start := idx - 120
	if start < 0 {
		start = 0
	}
	before := upper[start:idx]
	for _, n := range []string{"NOT ", "NO ", "NEVER ", "WITHOUT ", "CANNOT ", "NEITHER ",
		"UNTIL ", "ABSENT", "PENDING", "IS NOT", "NONE ", "'", "\"", "REQUIRES ", "BEFORE ",
		// These prefixes mark the word as naming a validator VERIQO has
		// to BUY -- "needs an accredited penetration testing firm" is
		// the opposite of a claim to be accredited, and a check that
		// could not tell the two apart would push the system towards
		// deleting the honest sentence.
		"NEEDS", "NEED:", "BLOCKED", "REQUIRED", "MUST ", "BUY", "PROCURE", "OWNER:"} {
		if strings.Contains(before, n) {
			return true
		}
	}
	return false
}

func context(s string, idx int) string {
	start, end := idx-60, idx+60
	if start < 0 {
		start = 0
	}
	if end > len(s) {
		end = len(s)
	}
	return strings.ReplaceAll(s[start:end], "\n", " ")
}
