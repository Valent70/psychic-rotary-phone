package reproducibility

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"veriqo/internal/environment"
)

// completeRecord is a provenance record with every ALWAYS-determinable
// field populated. Contextual fields are left empty on purpose: that is
// this sandbox's honest state, and the tests below assert it is
// reported as UNKNOWN rather than filled in.
func completeRecord() Record {
	return Record{
		SchemaVersion:      SchemaVersion,
		SourceCommit:       "8a085c7deadbeefcafe0123456789abcdef01234",
		SourceHash:         "sha256:1111111111111111111111111111111111111111111111111111111111111111",
		DependencyLockHash: "sha256:2222222222222222222222222222222222222222222222222222222222222222",
		ArtifactDigest:     "sha256:3333333333333333333333333333333333333333333333333333333333333333",
		ArtifactPath:       "bin/veriqo-node",
		ArtifactBytes:      3464091,
		SBOMHash:           "sha256:4444444444444444444444444444444444444444444444444444444444444444",
		Compiler:           "gc",
		BuildFlags:         []string{"-trimpath", "-buildvcs=false", "-ldflags=-s -w"},
		Environment:        environment.Current(),
	}
}

func TestFinalizeStampsAHashAndValidates(t *testing.T) {
	r, err := completeRecord().Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if r.Hash == "" {
		t.Fatal("finalized record carries no hash")
	}
	if err := r.VerifyHash(); err != nil {
		t.Fatalf("VerifyHash: %v", err)
	}
	again, err := completeRecord().Finalize()
	if err != nil {
		t.Fatalf("Finalize (2nd): %v", err)
	}
	if again.Hash != r.Hash {
		t.Fatal("provenance hash is not deterministic")
	}
}

// TestEveryAlwaysDeterminableFieldIsIndividuallyRequired: a field that
// no environment can fail to answer must never be missing, because an
// empty one means the record was assembled wrong rather than that the
// fact was unavailable.
func TestEveryAlwaysDeterminableFieldIsIndividuallyRequired(t *testing.T) {
	blanks := map[string]func(*Record){
		"source_commit":        func(r *Record) { r.SourceCommit = "" },
		"source_hash":          func(r *Record) { r.SourceHash = "" },
		"dependency_lock_hash": func(r *Record) { r.DependencyLockHash = "" },
		"artifact_digest":      func(r *Record) { r.ArtifactDigest = "" },
		"sbom_hash":            func(r *Record) { r.SBOMHash = "" },
		"compiler":             func(r *Record) { r.Compiler = "" },
		"build_flags":          func(r *Record) { r.BuildFlags = nil },
	}
	for name, blank := range blanks {
		r := completeRecord()
		blank(&r)
		if _, err := r.Finalize(); !errors.Is(err, ErrIncompleteCore) {
			t.Errorf("blanking %s was accepted: err = %v", name, err)
		}
	}
}

// TestUnanswerableFieldsAreReportedUnknownNotFabricated is the honesty
// core of this phase.
func TestUnanswerableFieldsAreReportedUnknownNotFabricated(t *testing.T) {
	r, err := completeRecord().Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	c := r.Describe()

	if len(c.Unknown) == 0 {
		t.Fatal("every contextual field is reported known -- in this sandbox that cannot be true, " +
			"so something is being fabricated")
	}
	for _, field := range c.Unknown {
		// The record itself must hold "" -- the UNKNOWN marker is a
		// report vocabulary and must never enter stored provenance.
		if v := r.ValueOrUnknown(field); v != UnknownValue {
			t.Errorf("%s reports %q, want the UNKNOWN marker", field, v)
		}
	}
	// Serialized provenance must not contain the marker either.
	if strings.Contains(strings.Join(c.Unknown, ","), UnknownValue) {
		t.Error("the UNKNOWN marker leaked into the field-name list")
	}
	for _, field := range []string{r.BuilderIdentity, r.WorkflowIdentity, r.BaseImageDigest, r.BaseImageSBOMHash} {
		if field == UnknownValue {
			t.Error("a Record field stores the literal UNKNOWN marker; unanswerable fields must stay empty")
		}
	}
}

// TestContextualFieldsDoNotBlockFinalize records the deliberate
// asymmetry: refusing a record for an unanswerable field would push a
// caller to invent one, which is the outcome this whole file exists to
// prevent.
func TestContextualFieldsDoNotBlockFinalize(t *testing.T) {
	r := completeRecord()
	r.BuilderIdentity = ""
	r.WorkflowIdentity = ""
	r.BaseImageDigest = ""
	r.BaseImageSBOMHash = ""
	r.OSPackageInventoryHash = ""
	if _, err := r.Finalize(); err != nil {
		t.Fatalf("a record with every contextual field honestly empty was refused: %v", err)
	}
}

func TestDescribeReportsAHigherFractionWhenMoreIsKnown(t *testing.T) {
	sparse, err := completeRecord().Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	rich := completeRecord()
	rich.BuilderIdentity = "gh-runner-ubuntu-latest-7"
	rich.WorkflowIdentity = "reproducible-build#12345"
	rich.BaseImageDigest = "sha256:abcd"
	rich.BaseImageSBOMHash = "sha256:efgh"
	rich.OSPackageInventoryHash = "sha256:ijkl"
	rich.OSPackageCount = 312
	richFinal, err := rich.Finalize()
	if err != nil {
		t.Fatalf("Finalize (rich): %v", err)
	}

	if richFinal.Describe().Fraction <= sparse.Describe().Fraction {
		t.Fatal("a record knowing strictly more did not report a higher completeness fraction")
	}
	if richFinal.Describe().Fraction != 1 {
		t.Fatalf("a record with every contextual field populated reports %.4f, want 1", richFinal.Describe().Fraction)
	}
	if len(richFinal.Describe().Unknown) != 0 {
		t.Fatalf("a fully-populated record still reports unknowns: %v", richFinal.Describe().Unknown)
	}
}

// TestProvenanceHashDetectsEveryStoredField makes the content-addressing
// real: an attestation over this hash must cover everything.
func TestProvenanceHashDetectsEveryStoredField(t *testing.T) {
	base, err := completeRecord().Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	mutations := map[string]func(*Record){
		"source_commit":             func(r *Record) { r.SourceCommit = "other" },
		"source_hash":               func(r *Record) { r.SourceHash = "other" },
		"dependency_lock_hash":      func(r *Record) { r.DependencyLockHash = "other" },
		"artifact_digest":           func(r *Record) { r.ArtifactDigest = "other" },
		"artifact_path":             func(r *Record) { r.ArtifactPath = "other" },
		"artifact_bytes":            func(r *Record) { r.ArtifactBytes = 99 },
		"sbom_hash":                 func(r *Record) { r.SBOMHash = "other" },
		"compiler":                  func(r *Record) { r.Compiler = "gccgo" },
		"build_flags":               func(r *Record) { r.BuildFlags = []string{"-race"} },
		"builder_identity":          func(r *Record) { r.BuilderIdentity = "someone" },
		"workflow_identity":         func(r *Record) { r.WorkflowIdentity = "wf#1" },
		"base_image_digest":         func(r *Record) { r.BaseImageDigest = "sha256:x" },
		"base_image_sbom_hash":      func(r *Record) { r.BaseImageSBOMHash = "sha256:y" },
		"os_package_inventory_hash": func(r *Record) { r.OSPackageInventoryHash = "sha256:z" },
		"os_package_count":          func(r *Record) { r.OSPackageCount = 7 },
		"environment":               func(r *Record) { r.Environment.Hostname = "a-different-host" },
	}
	for name, mutate := range mutations {
		r := completeRecord()
		mutate(&r)
		final, err := r.Finalize()
		if err != nil {
			t.Fatalf("Finalize after mutating %s: %v", name, err)
		}
		if final.Hash == base.Hash {
			t.Errorf("changing %s did not change the provenance hash", name)
		}
	}
}

// TestAttestationCoversTheRecordItSaysItCovers stops the classic
// mismatch where a signature claims to cover one statement and is
// attached to another.
func TestAttestationCoversTheRecordItSaysItCovers(t *testing.T) {
	r, err := completeRecord().Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	attested, err := r.WithAttestation(Attestation{
		SignerID: "release-eng", Algorithm: "ed25519", Signature: "abcd",
		// A caller-supplied Statement must be overwritten, not trusted.
		Statement: "something-else-entirely",
	})
	if err != nil {
		t.Fatalf("WithAttestation: %v", err)
	}
	if attested.Attestation.Statement != r.Hash {
		t.Fatalf("attestation statement = %q, want the record hash %q", attested.Attestation.Statement, r.Hash)
	}
	if err := attested.VerifyHash(); err != nil {
		t.Fatalf("VerifyHash: %v", err)
	}
	if !attested.Describe().Attested {
		t.Error("Describe does not report the attestation")
	}

	// Tampering with the record after attestation must be detected.
	tampered := attested
	tampered.SourceCommit = "a-different-commit"
	if err := tampered.VerifyHash(); err == nil {
		t.Fatal("a record edited after attestation still verified")
	}

	// So must re-pointing the attestation at a different statement.
	repointed := attested
	sig := *attested.Attestation
	sig.Statement = "sha256:some-other-record"
	repointed.Attestation = &sig
	if err := repointed.VerifyHash(); err == nil {
		t.Fatal("an attestation re-pointed at a different statement still verified")
	}
}

func TestAttestationRequiresAFinalizedRecord(t *testing.T) {
	if _, err := completeRecord().WithAttestation(Attestation{SignerID: "x"}); err == nil {
		t.Fatal("an attestation was attached to an unfinalized record")
	}
}

func TestForeignSchemaVersionIsRefused(t *testing.T) {
	r := completeRecord()
	r.SchemaVersion = "veriqo.build.provenance/v2"
	if err := r.Validate(); !errors.Is(err, ErrSchemaVersion) {
		t.Fatalf("err = %v, want ErrSchemaVersion", err)
	}
}

// --- assembly helpers -------------------------------------------------

func TestDigestFileIsContentAddressed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "artifact.bin")
	if err := os.WriteFile(path, []byte("hello veriqo"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	d1, n1, err := DigestFile(path)
	if err != nil {
		t.Fatalf("DigestFile: %v", err)
	}
	if n1 != 12 {
		t.Fatalf("size = %d, want 12", n1)
	}
	if err := os.WriteFile(path, []byte("hello veriqO"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	d2, _, err := DigestFile(path)
	if err != nil {
		t.Fatalf("DigestFile (2nd): %v", err)
	}
	if d1 == d2 {
		t.Fatal("a one-byte change produced the same digest")
	}
	if _, _, err := DigestFile(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("a missing artifact was digested successfully")
	}
}

// TestHashFilesTreatsAbsenceAsAChange records why a missing file is
// hashed as ABSENT rather than skipped: "go.sum was deleted" must be a
// visible change, not an invisible one.
func TestHashFilesTreatsAbsenceAsAChange(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "go.mod")
	b := filepath.Join(dir, "go.sum")
	if err := os.WriteFile(a, []byte("module veriqo\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(b, []byte("checksums\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	both := HashFiles(a, b)

	if err := os.Remove(b); err != nil {
		t.Fatalf("remove: %v", err)
	}
	missing := HashFiles(a, b)
	if both == missing {
		t.Fatal("deleting go.sum did not change the dependency lock hash")
	}
	// Order must not matter.
	if HashFiles(a, b) != HashFiles(b, a) {
		t.Fatal("the dependency lock hash depends on argument order")
	}
}

// TestCIIdentityHelpersReturnEmptyOutsideCI is the deliberate
// no-fallback rule: a developer workstation's hostname is not a builder
// identity, and writing one would make an UNKNOWN look answered.
func TestCIIdentityHelpersReturnEmptyOutsideCI(t *testing.T) {
	for _, key := range []string{"RUNNER_NAME", "CI_RUNNER_DESCRIPTION", "BUILDKITE_AGENT_NAME", "GITHUB_WORKFLOW", "GITHUB_RUN_ID"} {
		t.Setenv(key, "")
	}
	if got := BuilderFromEnvironment(); got != "" {
		t.Errorf("BuilderFromEnvironment = %q outside CI, want empty", got)
	}
	if got := WorkflowFromEnvironment(); got != "" {
		t.Errorf("WorkflowFromEnvironment = %q outside CI, want empty", got)
	}

	t.Setenv("RUNNER_NAME", "gh-runner-7")
	t.Setenv("GITHUB_WORKFLOW", "reproducible-build")
	t.Setenv("GITHUB_RUN_ID", "12345")
	if got := BuilderFromEnvironment(); got != "gh-runner-7" {
		t.Errorf("BuilderFromEnvironment = %q", got)
	}
	if got := WorkflowFromEnvironment(); got != "reproducible-build#12345" {
		t.Errorf("WorkflowFromEnvironment = %q", got)
	}
}

// TestOSPackageInventoryIsHonestAboutAvailability accepts either
// outcome and asserts each is internally consistent: a hash with a
// count, or neither. What it refuses is a hash covering zero packages,
// which would look like a real inventory and be an empty one.
func TestOSPackageInventoryIsHonestAboutAvailability(t *testing.T) {
	hash, count := OSPackageInventory()
	switch {
	case hash == "" && count == 0:
		t.Log("no OS package database is readable here; the field stays UNKNOWN, which is the honest answer")
	case hash != "" && count > 0:
		t.Logf("OS package inventory covers %d packages", count)
	default:
		t.Fatalf("inconsistent inventory: hash=%q count=%d -- a hash over zero packages looks like a real "+
			"inventory and is not one", hash, count)
	}
}
