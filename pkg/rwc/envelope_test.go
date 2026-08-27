package rwc

import (
	"errors"
	"testing"

	"veriqo/pkg/governance/envelope"
	"veriqo/pkg/governance/qualification"
)

func testIdentity() ReleaseIdentity {
	return ReleaseIdentity{
		Release: "v0.0.0-test", Commit: "0123456789abcdef",
		SourceHash: "sha256:source", BinaryHash: "sha256:binary", SBOMHash: "sha256:sbom",
	}
}

func testArtifacts() []envelope.Artifact {
	return []envelope.Artifact{
		{Name: "manifest.json", Hash: "sha256:aaa", Bytes: 100},
		{Name: "decision_results.json", Hash: "sha256:bbb", Bytes: 200},
	}
}

// TestCorpusEnvelopeIsStructurallyValid checks the envelope this package
// emits satisfies the real contract's own fail-closed structural rules —
// including the one that matters most here, that a FIXTURE envelope must
// declare at least one limitation.
func TestCorpusEnvelopeIsStructurallyValid(t *testing.T) {
	env := CorpusEnvelope(testIdentity(), testArtifacts(),
		map[string]string{"cases_executed": "10"}, 1, 1000)
	if err := env.Validate(); err != nil {
		t.Fatalf("CorpusEnvelope does not satisfy the envelope contract: %v", err)
	}
	if env.Classification != envelope.ClassificationFixture {
		t.Errorf("classification is %s, want FIXTURE", env.Classification)
	}
	if len(env.Limitations) == 0 {
		t.Error("a FIXTURE envelope with no declared limitation is exactly the artifact this repository refuses to produce")
	}
	if env.ID() == "" {
		t.Error("envelope has no content-addressed ID")
	}
}

// TestCorpusEnvelopeCannotClaimLive proves the under-claim is not
// accidental: flipping only the Classification to LIVE must be refused
// by the real validator, because REAL_DERIVED_BENCHMARK is not an origin
// that can carry a LIVE claim.
func TestCorpusEnvelopeCannotClaimLive(t *testing.T) {
	env := CorpusEnvelope(testIdentity(), testArtifacts(),
		map[string]string{"cases_executed": "10"}, 1, 1000)
	env.Classification = envelope.ClassificationLive

	v := envelope.Validator{Release: envelope.Release{
		Commit: testIdentity().Commit, SourceHash: testIdentity().SourceHash,
	}}
	_, err := v.Check(env, 10)
	if !errors.Is(err, envelope.ErrLiveWithoutRealOrigin) {
		t.Fatalf("a LIVE claim over a REAL_DERIVED_BENCHMARK origin must be refused with "+
			"ErrLiveWithoutRealOrigin; got %v", err)
	}
}

// TestCorpusEnvelopeCannotQualifyABlockedGate is the assertion the whole
// envelope wiring exists for. RWC v2 declares GateID=live_data, and the
// real validator must still refuse it — because this package registers
// no trust anchor for its provider and deliberately never will. If a
// future change registered one, this test fails and the claim
// "RWC v2 cannot close live_data" would have to be re-examined instead
// of quietly becoming false.
func TestCorpusEnvelopeCannotQualifyABlockedGate(t *testing.T) {
	env := CorpusEnvelope(testIdentity(), testArtifacts(),
		map[string]string{"cases_executed": "10"}, 1, 1000)

	id := testIdentity()
	v := envelope.Validator{
		Release: envelope.Release{
			Version: id.Release, Commit: id.Commit, SourceHash: id.SourceHash,
			BinaryHash: id.BinaryHash, SBOMHash: id.SBOMHash,
		},
		Trust: qualification.NewTrustRegistry(),
	}

	verdict, err := v.Check(env, 10)
	if err == nil {
		t.Fatal("an RWC v2 corpus envelope was ACCEPTED for a blocked gate; " +
			"this bundle must never be able to qualify live_data")
	}
	if !errors.Is(err, envelope.ErrUnknownProvider) {
		t.Fatalf("expected refusal for an unregistered provider (ErrUnknownProvider), got %v", err)
	}
	if verdict.Accepted {
		t.Error("verdict reports Accepted=true alongside a refusal error")
	}
	if verdict.EffectiveKind != envelope.ClassificationFixture {
		t.Errorf("effective kind is %s, want FIXTURE", verdict.EffectiveKind)
	}
}

// TestReleaseIdentityRefusesToBeIncomplete pins the fail-closed rule the
// command relies on: an incomplete release identity is reported as
// incomplete, never padded out.
func TestReleaseIdentityRefusesToBeIncomplete(t *testing.T) {
	full := testIdentity()
	if !full.Complete() {
		t.Fatal("a fully-populated release identity reports Complete()=false")
	}
	for name, mutate := range map[string]func(*ReleaseIdentity){
		"release":     func(r *ReleaseIdentity) { r.Release = "" },
		"commit":      func(r *ReleaseIdentity) { r.Commit = "" },
		"source_hash": func(r *ReleaseIdentity) { r.SourceHash = "" },
		"binary_hash": func(r *ReleaseIdentity) { r.BinaryHash = "" },
		"sbom_hash":   func(r *ReleaseIdentity) { r.SBOMHash = "" },
	} {
		id := testIdentity()
		mutate(&id)
		if id.Complete() {
			t.Errorf("release identity missing %s still reports Complete()=true", name)
		}
	}
}
