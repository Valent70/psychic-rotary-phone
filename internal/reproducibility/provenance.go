package reproducibility

// This file adds PHASE I (P1-14) — Reproducible Build Provenance — to
// the package whose doc comment lives in reproducibility_test.go.
// That comment already states, accurately, that binary equality is the
// one piece of the six-part provenance gap provable without external
// infrastructure. Nothing here changes that assessment; this file adds
// the STORAGE half the program asks for, and is explicit about which
// fields remain unanswerable in this sandbox.
//
// PHASE I (P1-14) — Reproducible Build Provenance — adds the second
// half the program asks for: a place to STORE what produced a binary,
// not only proof that the binary is reproducible.
//
// Reconciliation first, per rule 0. Binary equality already exists and
// is NOT rebuilt. Two adjacent things also already exist and are
// REUSED rather than duplicated:
//
//   - internal/environment.Identity answers OS, arch, compiler,
//     toolchain, hostname, kernel version, container detection and a
//     configuration hash over go.mod+go.sum. Record embeds it instead
//     of restating any of those fields.
//   - internal/sbom.Generate produces the release SBOM. Record cites
//     its hash rather than carrying a second copy of the document.
//
// The program's own scoping instruction is followed literally: "Don't
// over-engineer full SLSA/in-toto if not contractually required, but
// the architecture must be able to store this provenance." So this is
// a storage-and-validation contract, not an attestation framework. It
// deliberately does NOT implement in-toto layouts, DSSE envelopes or
// SLSA level assessment, because nothing in this repository has a
// contractual requirement for them and inventing one would be a large
// surface with no consumer.
//
// The honesty rule this file enforces: a field nobody can truthfully
// fill in stays EMPTY and is reported as UNKNOWN by Completeness. It
// is never defaulted, and never inferred from something adjacent. In
// this sandbox several fields are genuinely unanswerable — there is no
// base image digest for a binary built outside a container, and no
// workflow identity outside CI — and the Record says so rather than
// writing a plausible-looking value.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"veriqo/internal/environment"
)

// SchemaVersion identifies this provenance contract.
const SchemaVersion = "veriqo.build.provenance/v1"

// UnknownValue is what Completeness reports for a field left empty. It
// is a report vocabulary, never a stored value: an unfillable field
// stays "" in the Record itself.
const UnknownValue = "UNKNOWN"

var (
	// ErrIncompleteCore refuses a Record missing a field that is ALWAYS
	// determinable. There is no environment in which a build has no
	// source commit or no artifact digest, so an empty one means the
	// record was assembled wrong, not that the fact was unavailable.
	ErrIncompleteCore = errors.New("reproducibility: build provenance is missing a field that is always determinable")
	// ErrSchemaVersion refuses a record declaring a different contract.
	ErrSchemaVersion = errors.New("reproducibility: record declares a different provenance schema version")
)

// Attestation is a signature over a provenance record made by whoever
// produced the build. It is deliberately minimal: an identity, an
// algorithm, and the signature bytes. Verifying it against a trust
// anchor is pkg/governance/qualification's job (it already owns the
// Ed25519 trust registry), not this package's — duplicating that here
// would create a second trust model for the same problem.
type Attestation struct {
	SignerID  string `json:"signer_id"`
	Algorithm string `json:"algorithm"`
	Signature string `json:"signature"`
	// Statement is what the signature covers: always the Record's own
	// Hash. Named explicitly so a reader never has to guess.
	Statement string `json:"statement"`
}

// Record is everything known about how one binary was produced. Every
// field the program enumerates has a home here.
type Record struct {
	SchemaVersion string `json:"schema_version"`

	// --- always determinable ---------------------------------------
	// SourceCommit is the git commit the tree was at.
	SourceCommit string `json:"source_commit"`
	// SourceHash is the content hash of the source tree itself, which
	// differs from SourceCommit when the tree is dirty. Both are kept:
	// a commit alone cannot detect an uncommitted change.
	SourceHash string `json:"source_hash"`
	// DependencyLockHash is a hash over go.mod + go.sum. This
	// repository has zero external module requirements, which is itself
	// the strongest possible dependency-lock statement, but the hash is
	// recorded anyway so the day that changes is visible.
	DependencyLockHash string `json:"dependency_lock_hash"`
	// ArtifactDigest is the SHA-256 of the produced binary.
	ArtifactDigest string `json:"artifact_digest"`
	ArtifactPath   string `json:"artifact_path"`
	ArtifactBytes  int64  `json:"artifact_bytes"`
	// SBOMHash cites internal/sbom's document rather than embedding it.
	SBOMHash string `json:"sbom_hash"`
	// Compiler and BuildFlags describe how it was compiled.
	Compiler   string   `json:"compiler"`
	BuildFlags []string `json:"build_flags"`
	// Environment is internal/environment.Identity, embedded whole.
	Environment environment.Identity `json:"environment"`

	// --- determinable only in the right context --------------------
	// These stay EMPTY when unanswerable. Completeness reports them as
	// UNKNOWN; nothing here guesses.

	// BuilderIdentity names who or what ran the build: a CI runner id, a
	// service account, a developer workstation. Empty outside a context
	// that can state it.
	BuilderIdentity string `json:"builder_identity,omitempty"`
	// WorkflowIdentity names the automation that invoked the build: a
	// workflow file plus run id. Empty outside CI.
	WorkflowIdentity string `json:"workflow_identity,omitempty"`
	// BaseImageDigest and BaseImageSBOMHash describe the container the
	// build ran in. Both empty for a build that did not run in one --
	// which is a real, honest answer, not a gap.
	BaseImageDigest   string `json:"base_image_digest,omitempty"`
	BaseImageSBOMHash string `json:"base_image_sbom_hash,omitempty"`
	// OSPackageInventoryHash is a hash over the OS package list of the
	// build environment. Empty when no package manager inventory is
	// available, which is the case for a scratch or distroless image and
	// for any environment this process cannot enumerate.
	OSPackageInventoryHash string `json:"os_package_inventory_hash,omitempty"`
	// OSPackageCount records how many packages that hash covers, so a
	// reader can tell "an empty inventory was hashed" from "a real
	// inventory of 312 packages was hashed".
	OSPackageCount int `json:"os_package_count,omitempty"`

	// Attestation is optional and attached after Hash is computed.
	Attestation *Attestation `json:"attestation,omitempty"`

	Hash string `json:"hash"`
}

// coreFields are the fields that are always determinable. Kept as one
// list so Validate and Completeness cannot disagree about which is
// which.
func (r Record) coreFields() map[string]string {
	return map[string]string{
		"source_commit":        r.SourceCommit,
		"source_hash":          r.SourceHash,
		"dependency_lock_hash": r.DependencyLockHash,
		"artifact_digest":      r.ArtifactDigest,
		"sbom_hash":            r.SBOMHash,
		"compiler":             r.Compiler,
	}
}

// contextualFields are the fields that are legitimately unanswerable in
// some environments.
func (r Record) contextualFields() map[string]string {
	return map[string]string{
		"builder_identity":          r.BuilderIdentity,
		"workflow_identity":         r.WorkflowIdentity,
		"base_image_digest":         r.BaseImageDigest,
		"base_image_sbom_hash":      r.BaseImageSBOMHash,
		"os_package_inventory_hash": r.OSPackageInventoryHash,
	}
}

// Validate refuses a record missing an always-determinable field, and
// refuses a foreign schema version. It deliberately does NOT refuse an
// empty contextual field: doing so would push a caller to invent one.
func (r Record) Validate() error {
	if r.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: %q", ErrSchemaVersion, r.SchemaVersion)
	}
	var missing []string
	for name, value := range r.coreFields() {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, name)
		}
	}
	if len(r.BuildFlags) == 0 {
		missing = append(missing, "build_flags")
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("%w: %s", ErrIncompleteCore, strings.Join(missing, ", "))
	}
	return nil
}

// Completeness is the honest coverage report: which provenance fields
// this record actually carries, and which are UNKNOWN.
type Completeness struct {
	SchemaVersion string `json:"schema_version"`
	// Known and Unknown partition the contextual fields. Core fields are
	// not listed: Validate already refuses a record missing one, so they
	// are known by construction.
	Known   []string `json:"known"`
	Unknown []string `json:"unknown"`
	// Attested reports whether an attestation is attached. It is NOT a
	// claim the attestation verifies -- that is a trust-anchor check
	// pkg/governance/qualification performs.
	Attested bool `json:"attested"`
	// Fraction is len(Known) / total contextual fields, reported so a
	// gate can trend it. It is deliberately not a pass threshold: in
	// this sandbox several fields are unanswerable for real reasons, and
	// a threshold would create pressure to fabricate them.
	Fraction float64 `json:"fraction"`
}

// Describe reports what this record knows and what it does not.
func (r Record) Describe() Completeness {
	c := Completeness{SchemaVersion: r.SchemaVersion, Attested: r.Attestation != nil}
	fields := r.contextualFields()
	for name, value := range fields {
		if strings.TrimSpace(value) == "" {
			c.Unknown = append(c.Unknown, name)
		} else {
			c.Known = append(c.Known, name)
		}
	}
	sort.Strings(c.Known)
	sort.Strings(c.Unknown)
	if len(fields) > 0 {
		c.Fraction = float64(len(c.Known)) / float64(len(fields))
	}
	return c
}

// ValueOrUnknown returns a contextual field's value, or the UNKNOWN
// marker when it is empty. This is the ONLY place the marker appears;
// it never enters the Record itself.
func (r Record) ValueOrUnknown(field string) string {
	v, ok := r.contextualFields()[field]
	if !ok {
		if core, isCore := r.coreFields()[field]; isCore {
			return core
		}
		return UnknownValue
	}
	if strings.TrimSpace(v) == "" {
		return UnknownValue
	}
	return v
}

// ComputeHash content-addresses everything except the attestation,
// which by construction signs this hash and therefore cannot be part
// of it.
func (r Record) ComputeHash() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", SchemaVersion)
	names := make([]string, 0, 16)
	all := map[string]string{}
	for k, v := range r.coreFields() {
		all[k] = v
	}
	for k, v := range r.contextualFields() {
		all[k] = v
	}
	all["artifact_path"] = r.ArtifactPath
	all["artifact_bytes"] = fmt.Sprintf("%d", r.ArtifactBytes)
	all["os_package_count"] = fmt.Sprintf("%d", r.OSPackageCount)
	for k := range all {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(&b, "%s=%s\n", n, all[n])
	}
	flags := append([]string(nil), r.BuildFlags...)
	sort.Strings(flags)
	for _, f := range flags {
		fmt.Fprintf(&b, "flag=%s\n", f)
	}
	if envHash, err := r.Environment.Hash(); err == nil {
		fmt.Fprintf(&b, "environment=%s\n", envHash)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Finalize validates the record and stamps its hash. It is the only
// supported way to produce a complete Record.
func (r Record) Finalize() (Record, error) {
	r.SchemaVersion = SchemaVersion
	if err := r.Validate(); err != nil {
		return Record{}, err
	}
	r.Hash = r.ComputeHash()
	return r, nil
}

// WithAttestation attaches a signature over this record's own hash.
// The Statement field is set from the record rather than from the
// caller, so an attestation can never claim to cover something other
// than what it actually covers.
func (r Record) WithAttestation(a Attestation) (Record, error) {
	if r.Hash == "" {
		return Record{}, errors.New("reproducibility: attach an attestation only to a finalized record")
	}
	a.Statement = r.Hash
	r.Attestation = &a
	return r, nil
}

// VerifyHash independently recomputes the record hash. An attestation
// over a record whose hash no longer matches its content is worthless,
// so this is the first check any consumer should make.
func (r Record) VerifyHash() error {
	if r.Hash == "" {
		return errors.New("reproducibility: record is not finalized")
	}
	if r.ComputeHash() != r.Hash {
		return errors.New("reproducibility: record hash does not match its own content")
	}
	if r.Attestation != nil && r.Attestation.Statement != r.Hash {
		return errors.New("reproducibility: attestation covers a different statement than this record's hash")
	}
	return nil
}

// --- assembly helpers -------------------------------------------------

// DigestFile returns the SHA-256 of a file plus its size. It is how
// ArtifactDigest and ArtifactBytes are obtained.
func DigestFile(path string) (digest string, size int64, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, err
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- path is a build artifact this same process just produced or was pointed at by an operator
	if err != nil {
		return "", 0, err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), info.Size(), nil
}

// HashFiles returns a deterministic hash over the contents of several
// files, used for DependencyLockHash (go.mod + go.sum). A file that
// does not exist contributes its absence to the hash rather than being
// skipped silently, so "go.sum was deleted" is a visible change.
func HashFiles(paths ...string) string {
	h := sha256.New()
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)
	for _, p := range sorted {
		fmt.Fprintf(h, "|path=%s|", p)
		raw, err := os.ReadFile(p) // #nosec G304 -- fixed, caller-supplied build-metadata paths
		if err != nil {
			h.Write([]byte("ABSENT"))
			continue
		}
		h.Write(raw)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// BuilderFromEnvironment reads a builder identity from the well-known
// CI environment variables, returning "" when none is set. Returning
// "" rather than a hostname fallback is deliberate: a developer
// workstation's hostname is not a builder identity in the sense this
// field means, and writing one would make an UNKNOWN look answered.
func BuilderFromEnvironment() string {
	for _, key := range []string{"RUNNER_NAME", "CI_RUNNER_DESCRIPTION", "BUILDKITE_AGENT_NAME"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return ""
}

// WorkflowFromEnvironment reads a workflow identity from the well-known
// CI environment variables, returning "" outside CI for the same reason
// BuilderFromEnvironment does.
func WorkflowFromEnvironment() string {
	workflow := strings.TrimSpace(os.Getenv("GITHUB_WORKFLOW"))
	runID := strings.TrimSpace(os.Getenv("GITHUB_RUN_ID"))
	switch {
	case workflow != "" && runID != "":
		return workflow + "#" + runID
	case workflow != "":
		return workflow
	default:
		return ""
	}
}

// OSPackageInventory returns a deterministic hash over the build
// environment's OS package list, plus how many packages it covers.
//
// It reads dpkg's and apk's on-disk databases directly rather than
// shelling out, keeping this package a pure Go reader with no
// external-process dependency — the same discipline
// internal/environment.readKernelVersion already follows. When neither
// database exists (a scratch image, a non-Linux host, or an
// environment this process cannot enumerate) it returns ("", 0), which
// Describe reports as UNKNOWN rather than as an empty inventory.
func OSPackageInventory() (hash string, count int) {
	for _, source := range []struct {
		path   string
		prefix string
	}{
		{"/var/lib/dpkg/status", "Package: "},
		{"/lib/apk/db/installed", "P:"},
	} {
		raw, err := os.ReadFile(source.path) // #nosec G304 -- fixed, well-known package-database paths, not derived from input
		if err != nil {
			continue
		}
		var names []string
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(line, source.prefix) {
				names = append(names, strings.TrimSpace(strings.TrimPrefix(line, source.prefix)))
			}
		}
		if len(names) == 0 {
			continue
		}
		sort.Strings(names)
		h := sha256.New()
		fmt.Fprintf(h, "veriqo.os-package-inventory/v1|source=%s|", source.path)
		for _, n := range names {
			h.Write([]byte(n))
			h.Write([]byte{0})
		}
		return "sha256:" + hex.EncodeToString(h.Sum(nil)), len(names)
	}
	return "", 0
}
