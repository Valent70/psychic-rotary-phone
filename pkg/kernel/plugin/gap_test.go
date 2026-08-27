//go:build linux

// Session update — closes the #1 roadmap item from the audit
// follow-up report (§4): pkg/kernel/plugin 77.5% -> higher, via
// real (non-synthetic) scenarios only:
//
//   - Registry.IDs(), previously 0% covered despite being public API.
//   - SandboxRunner.RunOnce error branches that the existing suite
//     never exercised: a binary that fails to start at all (ENOENT),
//     a plugin that writes malformed JSON, and a plugin that exits
//     zero without ever writing a line (clean-exit crash, not a
//     kill).
//   - A real concurrent-holder race check: many goroutines running
//     RunOnce under *distinct* holders at once, proving
//     CreateGroup/AddPID/RemoveGroup on one holder never interferes
//     with another — the "race on cgroup cleanup" item named in the
//     audit follow-up roadmap. Run under `go test -race`.
package plugin

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// --- Registry.IDs() -----------------------------------------------

func TestRegistryIDs_EmptyAndPopulated(t *testing.T) {
	r := NewRegistry(newFakeHost())
	if got := r.IDs(); len(got) != 0 {
		t.Fatalf("want empty IDs on fresh registry, got %v", got)
	}

	if err := r.Drop(goodPlugin{id: "zeta"}); err != nil {
		t.Fatalf("drop zeta: %v", err)
	}
	if err := r.Drop(goodPlugin{id: "alpha"}); err != nil {
		t.Fatalf("drop alpha: %v", err)
	}
	// badPlugin fails Register but Drop still records it before
	// activation fails, so it must still show up in IDs().
	_ = r.Drop(badPlugin{})

	got := r.IDs()
	want := []string{"alpha", "bad", "zeta"}
	if len(got) != len(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("want sorted %v, got %v", want, got)
		}
	}
}

// --- RunOnce error branches -----------------------------------------

func TestSandboxRunner_StartFailure_BinaryNotFound(t *testing.T) {
	runner := NewSandboxRunner("veriqo-plugin-test")
	if !runner.Available() {
		t.Skip("cgroup v1 not writable in this environment")
	}
	var resp struct{}
	err := runner.RunOnce(context.Background(), "missing-holder",
		"/nonexistent/path/to/plugin-binary-that-does-not-exist",
		SandboxLimits{MemoryBytes: 32 * 1024 * 1024, Timeout: 2 * time.Second},
		struct{}{}, &resp)
	if err == nil {
		t.Fatal("want an error when the plugin binary does not exist, got nil")
	}
}

const badJSONPluginSource = `package main
import ("bufio";"os")
func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	os.Stdout.WriteString("{not valid json\n")
}
`

func TestSandboxRunner_MalformedResponse(t *testing.T) {
	runner := NewSandboxRunner("veriqo-plugin-test")
	if !runner.Available() {
		t.Skip("cgroup v1 not writable in this environment")
	}
	bin := buildHelperBinary(t, "badjsonplugin", badJSONPluginSource)

	var resp struct{ X int }
	err := runner.RunOnce(context.Background(), "badjson-holder", bin,
		SandboxLimits{MemoryBytes: 32 * 1024 * 1024, Timeout: 3 * time.Second},
		struct{}{}, &resp)
	if err == nil {
		t.Fatal("want unmarshal error for malformed plugin response, got nil")
	}
}

const silentExitPluginSource = `package main
import ("bufio";"os")
func main() {
	// Consume the request line (so the parent's stdin write succeeds
	// and we genuinely exercise the "process exited without ever
	// writing a response line" branch, not an unrelated broken-pipe
	// write error) and then exit cleanly without responding.
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
}
`

func TestSandboxRunner_CleanExitWithoutResponse(t *testing.T) {
	runner := NewSandboxRunner("veriqo-plugin-test")
	if !runner.Available() {
		t.Skip("cgroup v1 not writable in this environment")
	}
	bin := buildHelperBinary(t, "silentexitplugin", silentExitPluginSource)

	var resp struct{}
	err := runner.RunOnce(context.Background(), "silent-holder", bin,
		SandboxLimits{MemoryBytes: 32 * 1024 * 1024, Timeout: 3 * time.Second},
		struct{}{}, &resp)
	if !errors.Is(err, ErrSandboxCrashed) {
		t.Fatalf("want ErrSandboxCrashed for a plugin that exits cleanly without ever responding, got %v", err)
	}
}

// --- concurrent distinct-holder safety (run with -race) ------------

func TestSandboxRunner_ConcurrentDistinctHolders_NoCrossTalk(t *testing.T) {
	runner := NewSandboxRunner("veriqo-plugin-test")
	if !runner.Available() {
		t.Skip("cgroup v1 not writable in this environment")
	}
	bin := buildHelperBinary(t, "echoplugin_concurrent", echoPluginSource)

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	results := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var resp struct{ Doubled int }
			holder := "concurrent-holder"
			err := runner.RunOnce(context.Background(), holderName(holder, i), bin,
				SandboxLimits{MemoryBytes: 32 * 1024 * 1024, CPUCores: 1, Timeout: 5 * time.Second},
				struct{ Value int }{Value: i}, &resp)
			errs[i] = err
			results[i] = resp.Doubled
		}(i)
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: RunOnce error: %v", i, errs[i])
		}
		if results[i] != i*2 {
			t.Fatalf("goroutine %d: want %d, got %d (cross-talk between concurrent cgroup holders)", i, i*2, results[i])
		}
	}
}

func holderName(prefix string, i int) string {
	digits := "0123456789"
	if i < 10 {
		return prefix + "-" + string(digits[i])
	}
	return prefix + "-x"
}
