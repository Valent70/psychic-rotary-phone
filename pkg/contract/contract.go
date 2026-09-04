// Package contract holds the vocabulary every other VERIQO package
// shares: identifiers, versions, and the distinction between a result
// and a refusal.
//
// # Why a refusal is not a failure
//
// This is the distinction the whole system is organised around, and it
// is stated here because every layer needs it and no layer may
// redefine it:
//
//	SUCCEEDED  the operation ran and produced what it was asked for
//	REFUSED    the operation was declined by design; the system is
//	           working, and the answer is that it will not do this
//	FAILED     the operation was attempted and broke; this is a defect
//	UNRESOLVED the operation ran and reached no determination; this is
//	           an answer, and it is not a failure either
//
// Collapsing REFUSED into FAILED makes correct behaviour look like a
// bug and invites somebody to "fix" it. Collapsing FAILED into REFUSED
// hides a defect behind a safe-looking word. The mutation suite
// attacks both directions.
package contract

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Version is a monotonic revision of a governed artefact: a policy, an
// ontology, a model, a prompt, an algorithm.
//
// Every material conclusion records the versions it was produced
// under, because "the same input" is only meaningful alongside "the
// same versions".
type Version struct {
	Component string `json:"component"`
	Revision  uint64 `json:"revision"`
	Digest    string `json:"digest,omitempty"`
}

func (v Version) String() string {
	if v.Digest != "" {
		return fmt.Sprintf("%s@%d/%s", v.Component, v.Revision, shortDigest(v.Digest))
	}
	return fmt.Sprintf("%s@%d", v.Component, v.Revision)
}

// Zero reports whether the version was never set. A zero version in a
// replay manifest means nobody recorded what produced the result.
func (v Version) Zero() bool { return v.Component == "" && v.Revision == 0 }

func shortDigest(d string) string {
	if len(d) <= 12 {
		return d
	}
	return d[:12]
}

// VersionSet is the full set of versions a deterministic execution ran
// under. Replay compares these, not just the inputs.
type VersionSet struct {
	Ontology  Version `json:"ontology"`
	Policy    Version `json:"policy"`
	Algorithm Version `json:"algorithm"`
	Model     Version `json:"model,omitempty"`
	Prompt    Version `json:"prompt,omitempty"`
	Corpus    Version `json:"corpus,omitempty"`
}

// Complete reports whether the four always-required versions are set.
// Model and Prompt are required only when an AI component took part,
// which the caller knows and this type does not.
func (vs VersionSet) Complete() bool {
	return !vs.Ontology.Zero() && !vs.Policy.Zero() && !vs.Algorithm.Zero()
}

// Missing names the unset required versions, for an error message that
// tells a caller what to record rather than that something is wrong.
func (vs VersionSet) Missing() []string {
	var m []string
	if vs.Ontology.Zero() {
		m = append(m, "ontology")
	}
	if vs.Policy.Zero() {
		m = append(m, "policy")
	}
	if vs.Algorithm.Zero() {
		m = append(m, "algorithm")
	}
	return m
}

// Outcome is the four-valued result of any VERIQO operation.
type Outcome string

const (
	Succeeded  Outcome = "SUCCEEDED"
	Refused    Outcome = "REFUSED"
	Failed     Outcome = "FAILED"
	Unresolved Outcome = "UNRESOLVED"
)

// IsDefect reports whether the outcome indicates something is broken.
// Only FAILED does. This method exists so no caller writes
// `outcome != Succeeded` and treats a designed refusal as a bug.
func (o Outcome) IsDefect() bool { return o == Failed }

// IsDetermination reports whether a conclusion was reached. UNRESOLVED
// is an answer but not a determination, and REFUSED is neither.
func (o Outcome) IsDetermination() bool { return o == Succeeded }

func (o Outcome) Valid() bool {
	switch o {
	case Succeeded, Refused, Failed, Unresolved:
		return true
	}
	return false
}

// Errors shared across the system.
var (
	ErrNotAuthorized  = errors.New("veriqo: not authorized")
	ErrCrossTenant    = errors.New("veriqo: cross-tenant access refused")
	ErrImmutable      = errors.New("veriqo: record is immutable; supersede it with a new version")
	ErrUnversioned    = errors.New("veriqo: operation requires a complete version set")
	ErrNondeterminism = errors.New("veriqo: nondeterministic input on a deterministic path")
	ErrMalformedID    = errors.New("veriqo: malformed identifier")
)

// ID is a namespaced, deterministic identifier.
//
// It is deliberately NOT a UUID. A random identifier makes two runs of
// the same deterministic computation produce different records, which
// breaks replay comparison at the point where it matters most. IDs are
// derived from content, or supplied by the caller and validated here.
type ID string

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*:[A-Za-z0-9][A-Za-z0-9._~-]{0,127}$`)

// NewID validates and builds `kind:local`.
func NewID(kind, local string) (ID, error) {
	id := ID(kind + ":" + local)
	if err := id.Validate(); err != nil {
		return "", err
	}
	return id, nil
}

func (i ID) Validate() error {
	if !idPattern.MatchString(string(i)) {
		return fmt.Errorf("%w: %q is not `kind:local`", ErrMalformedID, string(i))
	}
	return nil
}

func (i ID) Kind() string {
	k, _, _ := strings.Cut(string(i), ":")
	return k
}

func (i ID) Local() string {
	_, l, _ := strings.Cut(string(i), ":")
	return l
}

func (i ID) String() string { return string(i) }

// Interval is a half-open time interval [From, To). A nil To means
// "open, as far as anyone recorded" -- which is NOT the same as
// "forever", and callers must not read it that way.
type Interval struct {
	From time.Time  `json:"from"`
	To   *time.Time `json:"to,omitempty"`
}

// Contains reports whether t falls in the interval.
func (iv Interval) Contains(t time.Time) bool {
	if t.Before(iv.From) {
		return false
	}
	if iv.To == nil {
		return true
	}
	return t.Before(*iv.To)
}

// Open reports whether the interval has no recorded end.
func (iv Interval) Open() bool { return iv.To == nil }

// Overlaps reports whether two intervals share any instant.
func (iv Interval) Overlaps(o Interval) bool {
	if iv.To != nil && !o.From.Before(*iv.To) {
		return false
	}
	if o.To != nil && !iv.From.Before(*o.To) {
		return false
	}
	return true
}

// Valid reports whether the interval is coherent.
func (iv Interval) Valid() bool {
	if iv.From.IsZero() {
		return false
	}
	if iv.To != nil && !iv.From.Before(*iv.To) {
		return false
	}
	return true
}

// Clock is the only source of time on a governed path.
//
// time.Now() is banned in deterministic code: a replay that calls it
// cannot reproduce its own inputs. Passing a Clock makes the
// dependency visible and lets replay supply the recorded instant.
type Clock interface {
	Now() time.Time
}

// FixedClock returns a Clock that always reports t. Replay uses it.
type FixedClock time.Time

func (f FixedClock) Now() time.Time { return time.Time(f) }
