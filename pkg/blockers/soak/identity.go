// This file adds PART 10's required evidence fields on top of the
// existing Start/Checkpoint/Monitor/Finalize/GenerateEvidence
// machinery in soak.go: an immutable run ID, host identity, source and
// binary hashes, resumable on-disk state (so a fresh process picking
// up after a crash can be proven to be resuming the SAME run rather
// than starting a new one), and real CPU/IO resource accounting. It
// reuses internal/environment for host identity and internal/sourcehash
// for the source-tree hash -- both already exist and are already the
// real, audited answer to "what host is this" and "what source did
// this run against" -- rather than reinventing either.
package soak

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"syscall"

	"veriqo/internal/environment"
	"veriqo/internal/sourcehash"
)

// RunIdentity binds one soak run's evidence to an immutable identity:
// a run ID generated once (at the first Start()/StartResumable() call
// for this run and never changed afterward, even across a detected
// restart), the host it ran on, and -- once ComputeSourceIdentity or
// StartResumable(_, sourceRoot) has been called -- the exact source
// tree and running binary it executed against.
type RunIdentity struct {
	RunID            string               `json:"run_id"`
	Host             environment.Identity `json:"host"`
	HostIdentityHash string               `json:"host_identity_hash"`
	SourceRoot       string               `json:"source_root,omitempty"`
	SourceHash       string               `json:"source_hash,omitempty"`
	SourceFileCount  int                  `json:"source_file_count,omitempty"`
	BinaryPath       string               `json:"binary_path,omitempty"`
	BinaryHash       string               `json:"binary_hash,omitempty"`
	StartUnix        int64                `json:"start_unix"`
	StartTime        string               `json:"start_time"`
	// RestartCount is 0 for a run that has never been resumed from
	// persisted state, and incremented by StartResumable every time it
	// finds and reuses an existing on-disk PersistedState for this same
	// run -- a real, checkable fact (see StartResumable's own doc
	// comment for how the "same run" identity is proven, not asserted).
	RestartCount int `json:"restart_count"`
}

// newRunID generates a fresh, immutable run identifier from 16 real
// random bytes -- collision-improbable the same way a UUIDv4 is,
// without adding a UUID dependency this zero-dependency module doesn't
// otherwise need.
func newRunID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("soak: generate run id: %w", err)
	}
	return "soak-run-" + hex.EncodeToString(b[:]), nil
}

// hostIdentity reports the real host identity of the calling process,
// reusing internal/environment's already-audited Current() rather than
// re-deriving hostname/OS/kernel facts a second way. HostIdentityHash
// is a deterministic content hash over that same identity, letting
// evidence consumers compare "is this the same host" with one string
// instead of a full struct diff.
func hostIdentity() (environment.Identity, string) {
	id := environment.Current()
	h, err := id.Hash()
	if err != nil {
		// environment.Identity.Hash() only fails if json.Marshal fails,
		// which cannot happen for this struct's own field types --
		// effectively unreachable, but left as an honest empty string
		// rather than a fabricated hash on the error path.
		h = ""
	}
	return id, h
}

// binaryIdentity hashes the currently-running executable's own bytes
// -- a real binary-identity fact distinct from the source-tree hash
// (the same source can produce different binaries across Go versions
// or build flags; this is "what actually executed", not "what was
// compiled from"). os.Executable's own documentation notes the result
// is not guaranteed following e.g. self-replacement; a failure here is
// therefore a real, non-fatal limitation, not swallowed silently --
// callers see BinaryPath/BinaryHash simply stay empty.
func binaryIdentity() (path, hash string, err error) {
	path, err = os.Executable()
	if err != nil {
		return "", "", err
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- path is this process's own resolved executable path (os.Executable), not external input
	if err != nil {
		return path, "", err
	}
	sum := sha256.Sum256(raw)
	return path, hex.EncodeToString(sum[:]), nil
}

// ComputeSourceIdentity hashes root's source tree (via
// internal/sourcehash, the same Merkle-style tree hash the release
// certificate uses) and the currently-running binary, and records both
// on this harness's identity. It is separate from Start() deliberately:
// Start() is called pervasively by short unit tests and must stay fast
// and side-effect-light, while a real qualification run (RunQualification,
// or a caller like cmd/veriqo-soak-run) opts into the heavier, genuinely
// meaningful source/binary identity explicitly.
//
// Honest scope: root defaults to "." throughout this package's own
// RunQualification (matching internal/environment.Current()'s own
// documented "caller's working directory is the repo root" convention)
// -- which is only the FULL repository tree when the calling process's
// working directory genuinely is the repo root. A caller that wants a
// meaningful source hash from a different working directory (e.g. `go
// test` run from inside this package's own directory) must pass a real
// repo root explicitly.
func (h *Harness) ComputeSourceIdentity(root string) error {
	res, err := sourcehash.Compute(root)
	if err != nil {
		return fmt.Errorf("soak: compute source identity: %w", err)
	}
	path, hash, binErr := binaryIdentity()
	h.mu.Lock()
	h.identity.SourceRoot = root
	h.identity.SourceHash = res.RootHash
	h.identity.SourceFileCount = res.FileCount
	if binErr == nil {
		h.identity.BinaryPath = path
		h.identity.BinaryHash = hash
	}
	h.mu.Unlock()
	return nil
}

// Identity returns a snapshot of this harness's run identity as
// currently known.
func (h *Harness) Identity() RunIdentity {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.identity
}

// ResourceUsage is a point-in-time CPU/IO reading pulled from the real
// OS resource-usage accounting (getrusage(2), this package's Linux
// target platform) rather than derived or estimated from wall-clock
// timing -- Go's runtime package alone exposes goroutine/heap stats
// but not CPU or block-IO counters, so this package reaches one level
// lower, through the stdlib syscall package, for exactly those two.
type ResourceUsage struct {
	CPUUserSeconds   float64 `json:"cpu_user_seconds"`
	CPUSystemSeconds float64 `json:"cpu_system_seconds"`
	BlockInputOps    int64   `json:"block_input_ops"`
	BlockOutputOps   int64   `json:"block_output_ops"`
}

func readResourceUsage() (ResourceUsage, error) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return ResourceUsage{}, fmt.Errorf("soak: getrusage: %w", err)
	}
	return ResourceUsage{
		CPUUserSeconds:   timevalSeconds(ru.Utime),
		CPUSystemSeconds: timevalSeconds(ru.Stime),
		BlockInputOps:    ru.Inblock,
		BlockOutputOps:   ru.Oublock,
	}, nil
}

func timevalSeconds(tv syscall.Timeval) float64 {
	return float64(tv.Sec) + float64(tv.Usec)/1e6
}

// ErrDroppedEvent is a sentinel a Workload can wrap (errors.Is) to
// signal a non-fatal, deliberately-skipped iteration -- e.g. a
// synthetic rate limiter or backpressure event -- that RunOnce counts
// as a dropped event rather than a hard error: the run's error count
// stays untouched and the RunQualification loop does not break, but
// the drop is genuinely counted and appears in every Checkpoint and
// the final Report.
var ErrDroppedEvent = errors.New("soak: iteration dropped (non-fatal)")

// PersistedState is the on-disk resumable snapshot of a soak run: its
// identity plus the checkpoint chain accumulated so far. SaveState
// writes it, LoadState/StartResumable read it back -- the mechanism
// that makes restart detection a genuine, checkable fact rather than
// an unused field: a fresh process pointed at the same path picks up
// the SAME RunID and the SAME hash-chained checkpoint history a prior
// (e.g. crashed) process instance had already accumulated.
type PersistedState struct {
	Identity    RunIdentity  `json:"identity"`
	Checkpoints []Checkpoint `json:"checkpoints"`
	LastHash    string       `json:"last_hash"`
}

// SaveState writes this harness's current identity and checkpoint
// chain to path, atomically (write-to-temp-then-rename) so a process
// killed mid-write never leaves a torn/partially-written state file
// for the next instance to trip over.
func (h *Harness) SaveState(path string) error {
	h.mu.Lock()
	state := PersistedState{
		Identity:    h.identity,
		Checkpoints: append([]Checkpoint(nil), h.checkpoints...),
		LastHash:    h.lastHash,
	}
	h.mu.Unlock()

	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("soak: marshal state: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil { // #nosec G304 -- path is a caller-supplied local state-file path for this harness's own run, not external input
		return fmt.Errorf("soak: write state: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("soak: rename state into place: %w", err)
	}
	return nil
}

// LoadState reads a previously-saved PersistedState from path.
func LoadState(path string) (PersistedState, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- path is a caller-supplied local state-file path for this harness's own run, not external input
	if err != nil {
		return PersistedState{}, err
	}
	var state PersistedState
	if err := json.Unmarshal(raw, &state); err != nil {
		return PersistedState{}, fmt.Errorf("soak: parse state %s: %w", path, err)
	}
	if state.Identity.RunID == "" {
		return PersistedState{}, fmt.Errorf("soak: state %s has no run id", path)
	}
	return state, nil
}

// StartResumable begins (or resumes) a run persisted at statePath. If
// statePath already holds a valid PersistedState -- e.g. because a
// prior process instance crashed mid-run and this fresh process is
// picking the same run back up -- the SAME RunID and the full prior
// checkpoint chain are loaded (so VerifyChain over the combined
// old+new checkpoint sequence still verifies: the chain genuinely
// continues, this is not merely a label), RestartCount is incremented,
// and the harness's iteration/error/dropped-event counters resume from
// the last checkpoint rather than restarting at zero. If statePath does
// not exist or does not parse, a brand-new run identity is generated
// exactly as Start() would, and its initial state is written to
// statePath so a LATER restart of this same run can, in turn, be
// detected.
//
// sourceRoot is passed to ComputeSourceIdentity; pass "" to skip it
// (fast path, matching bare Start()).
func (h *Harness) StartResumable(statePath, sourceRoot string) (resumed bool, err error) {
	h.Start() // baseline goroutines/start time, and a fresh RunID+host identity if none exists yet

	if state, loadErr := LoadState(statePath); loadErr == nil {
		h.mu.Lock()
		h.identity.RunID = state.Identity.RunID
		h.identity.RestartCount = state.Identity.RestartCount + 1
		h.checkpoints = append([]Checkpoint(nil), state.Checkpoints...)
		h.lastHash = state.LastHash
		if n := len(h.checkpoints); n > 0 {
			last := h.checkpoints[n-1]
			h.iterations = last.Iterations
			h.errors = last.Errors
			h.dropped = last.DroppedEvents
		}
		h.mu.Unlock()
		resumed = true
	}

	if sourceRoot != "" {
		if idErr := h.ComputeSourceIdentity(sourceRoot); idErr != nil {
			err = idErr
		}
	}
	if saveErr := h.SaveState(statePath); saveErr != nil && err == nil {
		err = saveErr
	}
	return resumed, err
}
