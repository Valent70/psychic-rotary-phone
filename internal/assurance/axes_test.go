package assurance

import "testing"

// axisPassing / axisFailing are this file's own artifact helpers,
// deliberately distinct from gate_test.go's passingEvidence(gate) so
// each test can name the exact command an axis's evidence came from.
func axisPassing(t *testing.T, gateID, cmd string) Evidence {
	t.Helper()
	return NewEvidence(gateID, cmd, "ok", 0, 42)
}

func axisFailing(t *testing.T, gateID, cmd string) Evidence {
	t.Helper()
	return NewEvidence(gateID, cmd, "boom", 1, 42)
}

// blockedGateRegistry reproduces the exact shape the eight externally
// blocked mandatory gates have in cmd/veriqo-readiness: registered
// mandatory, marked BLOCKED with a named blocker, and — new in PHASE
// E3 — carrying real engineering and internal-qualification evidence
// that used to have nowhere to be reported.
func blockedGateRegistry(t *testing.T) *Registry {
	t.Helper()
	r := NewRegistry()
	if err := r.Register(Gate{
		ID: "scale_qualification", Description: "100-node / 1M-envelope benchmark",
		Mandatory: true, RequiredStatus: StatusQualified, OwnerPackage: "external",
		ExitCriteria:       "p50/p95/p99 at 100 nodes",
		ExternalDependency: "physical multi-host infrastructure at production scale",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := r.Block("scale_qualification", "physical multi-host infrastructure at production scale"); err != nil {
		t.Fatalf("block: %v", err)
	}
	if err := r.AttachEngineering("scale_qualification",
		axisPassing(t, "scale_qualification", "go test ./pkg/blockers/scale/")); err != nil {
		t.Fatalf("attach engineering: %v", err)
	}
	if err := r.AttachInternal("scale_qualification",
		axisPassing(t, "scale_qualification", "docker multi-container scale drill")); err != nil {
		t.Fatalf("attach internal: %v", err)
	}
	return r
}

// TestBlockedGateReportsEngineeringAndInternalSeparatelyFromExternal is
// PHASE E3's own worked example, verbatim from the program text:
// scale_qualification -> engineering PASS, internal PASS, external
// BLOCKED, final BLOCKED_EXTERNAL.
func TestBlockedGateReportsEngineeringAndInternalSeparatelyFromExternal(t *testing.T) {
	r := blockedGateRegistry(t)
	g, _ := r.Get("scale_qualification")
	a := g.Axes()

	if a.Engineering != AxisPass {
		t.Errorf("Engineering = %q, want PASS", a.Engineering)
	}
	if a.Internal != AxisInternalQualified {
		t.Errorf("Internal = %q, want INTERNAL_QUALIFIED", a.Internal)
	}
	if a.External != AxisBlockedExternal {
		t.Errorf("External = %q, want BLOCKED_EXTERNAL", a.External)
	}
	if a.Final != AxisBlockedExternal {
		t.Errorf("Final = %q, want BLOCKED_EXTERNAL", a.Final)
	}
	if a.ExternalDependency == "" {
		t.Error("a BLOCKED_EXTERNAL axis with no named dependency is exactly the vague shrug this package forbids")
	}
}

// TestAxisSeparationNeverAdvancesTheGateItself is the hard invariant of
// this whole phase: adding axis reporting must not move any gate's
// status, satisfaction or the release verdict by so much as one step.
func TestAxisSeparationNeverAdvancesTheGateItself(t *testing.T) {
	r := blockedGateRegistry(t)
	g, _ := r.Get("scale_qualification")

	if got := g.EffectiveStatus(); got != StatusBlocked {
		t.Fatalf("EffectiveStatus = %q, want BLOCKED -- engineering evidence must never advance a blocked gate", got)
	}
	if g.Satisfied() {
		t.Fatal("a blocked gate reported Satisfied after engineering evidence was attached")
	}
	a := r.Assess()
	if a.Verdict != VerdictNotProductionReady {
		t.Fatalf("Verdict = %q, want NOT_PRODUCTION_READY", a.Verdict)
	}
	if len(a.BlockedMandatory) != 1 || a.BlockedMandatory[0] != "scale_qualification" {
		t.Fatalf("BlockedMandatory = %v, want [scale_qualification]", a.BlockedMandatory)
	}
	if a.MandatoryPassing != 0 {
		t.Fatalf("MandatoryPassing = %d, want 0", a.MandatoryPassing)
	}
}

// TestInternalQualifiedIsNeverExternalQualified is the honesty rule
// PHASE H states and this phase enforces structurally: no amount of
// in-sandbox evidence moves the EXTERNAL axis.
func TestInternalQualifiedIsNeverExternalQualified(t *testing.T) {
	r := blockedGateRegistry(t)
	for i := 0; i < 5; i++ {
		if err := r.AttachInternal("scale_qualification",
			NewEvidence("scale_qualification", "another in-sandbox drill", "ok", 0, uint64(100+i))); err != nil {
			t.Fatalf("attach internal: %v", err)
		}
	}
	g, _ := r.Get("scale_qualification")
	if a := g.Axes(); a.External != AxisBlockedExternal {
		t.Fatalf("External = %q after five internal drills, want BLOCKED_EXTERNAL", a.External)
	}
}

func TestGateWithNoExternalDependencyReportsNotApplicable(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(Gate{
		ID: "build", Mandatory: true, RequiredStatus: StatusVerified,
		OwnerPackage: "./...", ExitCriteria: "zero build errors",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := r.Attach("build", StatusVerified, axisPassing(t, "build", "go build ./...")); err != nil {
		t.Fatalf("attach: %v", err)
	}
	a, _ := r.Get("build")
	ax := a.Axes()
	if ax.Engineering != AxisPass {
		t.Errorf("Engineering = %q, want PASS", ax.Engineering)
	}
	if ax.Internal != AxisNotApplicable {
		t.Errorf("Internal = %q, want NOT_APPLICABLE", ax.Internal)
	}
	if ax.External != AxisNotApplicable {
		t.Errorf("External = %q, want NOT_APPLICABLE", ax.External)
	}
	if ax.Final != AxisReady {
		t.Errorf("Final = %q, want READY", ax.Final)
	}
}

func TestFailingGateIsNotReadyAndNotBlockedExternal(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(Gate{
		ID: "unit", Mandatory: true, RequiredStatus: StatusVerified,
		OwnerPackage: "./...", ExitCriteria: "go test ./... passes",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := r.Attach("unit", StatusImplemented, axisFailing(t, "unit", "go test ./...")); err != nil {
		t.Fatalf("attach: %v", err)
	}
	g, _ := r.Get("unit")
	ax := g.Axes()
	if ax.Engineering != AxisFail {
		t.Fatalf("Engineering = %q, want FAIL", ax.Engineering)
	}
	if ax.Final != AxisNotReady {
		t.Fatalf("Final = %q, want NOT_READY -- a real engineering failure must never hide behind BLOCKED_EXTERNAL", ax.Final)
	}
}

func TestAxisWithNoEvidenceIsNotRunNotPass(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(Gate{
		ID: "soak_72h", Mandatory: true, RequiredStatus: StatusQualified,
		OwnerPackage: "./...", ExitCriteria: "72h continuous run",
		ExternalDependency: "a host that can stay up for 72 continuous hours",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := r.Block("soak_72h", "a host that can stay up for 72 continuous hours"); err != nil {
		t.Fatalf("block: %v", err)
	}
	g, _ := r.Get("soak_72h")
	ax := g.Axes()
	if ax.Engineering != AxisNotRun {
		t.Errorf("Engineering = %q, want NOT_RUN -- absence of evidence is never PASS", ax.Engineering)
	}
	if ax.Internal != AxisNotRun {
		t.Errorf("Internal = %q, want NOT_RUN", ax.Internal)
	}
	if ax.Final != AxisBlockedExternal {
		t.Errorf("Final = %q, want BLOCKED_EXTERNAL", ax.Final)
	}
}

// TestTamperedAxisEvidenceDegradesRatherThanPasses applies the same
// no-false-green rule the main Evidence path already enforces to the
// new axes: an artifact whose hash no longer matches its content counts
// as no artifact at all.
func TestTamperedAxisEvidenceDegradesRatherThanPasses(t *testing.T) {
	r := blockedGateRegistry(t)
	g := r.gates["scale_qualification"]
	g.EngineeringEvidence[0].Output = "ok (edited after hashing)"
	if ax := g.Axes(); ax.Engineering == AxisPass {
		t.Fatal("a tampered engineering artifact still reported PASS")
	}
}

func TestAxesReportCountsAndNamesBlockedMandatoryGates(t *testing.T) {
	r := blockedGateRegistry(t)
	if err := r.Register(Gate{
		ID: "build", Mandatory: true, RequiredStatus: StatusVerified,
		OwnerPackage: "./...", ExitCriteria: "zero build errors",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := r.Attach("build", StatusVerified, axisPassing(t, "build", "go build ./...")); err != nil {
		t.Fatalf("attach: %v", err)
	}
	rep := r.Axes()
	if len(rep.Gates) != 2 {
		t.Fatalf("got %d gates, want 2", len(rep.Gates))
	}
	if rep.Gates[0].GateID != "build" {
		t.Fatalf("axes report is not in stable gate-ID order: %v", rep.Gates[0].GateID)
	}
	if rep.EngineeringPassing != 2 {
		t.Errorf("EngineeringPassing = %d, want 2 (a blocked gate's harness still passes)", rep.EngineeringPassing)
	}
	if rep.InternalQualified != 1 {
		t.Errorf("InternalQualified = %d, want 1", rep.InternalQualified)
	}
	if rep.ExternalQualified != 0 {
		t.Errorf("ExternalQualified = %d, want 0 -- nothing here has real external evidence", rep.ExternalQualified)
	}
	if len(rep.BlockedExternalMandatory) != 1 || rep.BlockedExternalMandatory[0] != "scale_qualification" {
		t.Errorf("BlockedExternalMandatory = %v", rep.BlockedExternalMandatory)
	}
}

func TestWaivedGateNeverReportsReadyOnTheFinalAxis(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(Gate{
		ID: "vuln_db", Mandatory: true, RequiredStatus: StatusVerified,
		OwnerPackage: "./...", ExitCriteria: "vulnerability DB queried",
		ExternalDependency: "an egress policy that permits vuln.go.dev",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := r.Waive("vuln_db", Waiver{
		Owner: "release-eng", Justification: "org egress policy denies the feed", ExpiresAtTick: 999,
	}); err != nil {
		t.Fatalf("waive: %v", err)
	}
	g, _ := r.Get("vuln_db")
	if ax := g.Axes(); ax.Final != AxisWaived {
		t.Fatalf("Final = %q, want WAIVED", ax.Final)
	}
}

// TestCanonicalStatusDistinguishesReadyFromTrulyBlocked is Round 4's own
// worked example: two mandatory gates both BLOCKED_EXTERNAL on the FINAL
// axis, but one has a real in-sandbox qualification drill behind it and
// the other genuinely cannot run one without the external dependency
// itself. The canonical taxonomy is required to tell them apart under
// two different names, not collapse them into one.
func TestCanonicalStatusDistinguishesReadyFromTrulyBlocked(t *testing.T) {
	r := blockedGateRegistry(t) // scale_qualification: engineering PASS, internal QUALIFIED
	if err := r.Register(Gate{
		ID: "hsm_kms", Mandatory: true, RequiredStatus: StatusQualified,
		OwnerPackage: "external", ExitCriteria: "production KMS tenancy provisioned",
		ExternalDependency: "a real HSM/KMS tenancy nobody in this sandbox can provision",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := r.Block("hsm_kms", "a real HSM/KMS tenancy nobody in this sandbox can provision"); err != nil {
		t.Fatalf("block: %v", err)
	}
	if err := r.AttachEngineering("hsm_kms", axisPassing(t, "hsm_kms", "go test ./pkg/blockers/hsmkms/")); err != nil {
		t.Fatalf("attach engineering: %v", err)
	}
	// Deliberately no AttachInternal: an HSM/KMS internal drill needs the
	// HSM/KMS itself, so it cannot be attempted at all in this sandbox.

	sc, _ := r.Get("scale_qualification")
	if got := sc.Axes().Canonical; got != CanonicalReadyForExternalQualification {
		t.Fatalf("scale_qualification canonical = %q, want READY_FOR_EXTERNAL_QUALIFICATION", got)
	}
	hk, _ := r.Get("hsm_kms")
	if got := hk.Axes().Canonical; got != CanonicalBlockedExternal {
		t.Fatalf("hsm_kms canonical = %q, want BLOCKED_EXTERNAL", got)
	}

	rep := r.Axes()
	if rep.ReadyForExternalQualification != 1 || rep.BlockedExternal != 1 {
		t.Fatalf("rollup = ready:%d blocked:%d, want 1 and 1", rep.ReadyForExternalQualification, rep.BlockedExternal)
	}
	if total := rep.VerifiedInternal + rep.ReadyForExternalQualification + rep.BlockedExternal + rep.NotReady; total != len(rep.Gates) {
		t.Fatalf("canonical buckets sum to %d, want exactly %d (every gate lands in exactly one bucket)", total, len(rep.Gates))
	}
}

// TestCanonicalStatusNeverCallsAWaiverReady proves a waived gate lands in
// NOT_READY under the canonical taxonomy too, not a fifth invented word.
func TestCanonicalStatusNeverCallsAWaiverReady(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(Gate{
		ID: "vuln_db", Mandatory: true, RequiredStatus: StatusVerified,
		OwnerPackage: "./...", ExitCriteria: "vulnerability DB queried",
		ExternalDependency: "an egress policy that permits vuln.go.dev",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := r.Waive("vuln_db", Waiver{
		Owner: "release-eng", Justification: "org egress policy denies the feed", ExpiresAtTick: 999,
	}); err != nil {
		t.Fatalf("waive: %v", err)
	}
	g, _ := r.Get("vuln_db")
	if got := g.Axes().Canonical; got != CanonicalNotReady {
		t.Fatalf("canonical = %q, want NOT_READY", got)
	}
}

// TestCanonicalStatusDistinguishesExternallyQualifiedFromVerifiedInternal
// is Round 5's own explicit rule: "Tidak boleh menyamakan Internal
// Verification = External Qualification". A gate with NO external
// dependency that reaches READY must report VERIFIED_INTERNAL; a gate
// that HAD a named external dependency and was closed with real,
// EXTERNAL_QUALIFIED evidence must report a DIFFERENT value,
// EXTERNALLY_QUALIFIED, never silently merged into the first.
func TestCanonicalStatusDistinguishesExternallyQualifiedFromVerifiedInternal(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(Gate{
		ID: "build", Mandatory: true, RequiredStatus: StatusVerified,
		OwnerPackage: "./...", ExitCriteria: "zero build errors",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := r.Attach("build", StatusVerified, axisPassing(t, "build", "go build ./...")); err != nil {
		t.Fatalf("attach: %v", err)
	}

	if err := r.Register(Gate{
		ID: "pentest", Mandatory: true, RequiredStatus: StatusQualified,
		OwnerPackage: "external", ExitCriteria: "independent penetration test",
		ExternalDependency: "an independent security vendor",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	// A real, signed external qualification submission closed this gate
	// -- the only way EffectiveStatus can honestly reach QUALIFIED with
	// a named ExternalDependency still set.
	if err := r.Attach("pentest", StatusQualified, axisPassing(t, "pentest", "external:vendor-signed-report")); err != nil {
		t.Fatalf("attach: %v", err)
	}

	build, _ := r.Get("build")
	if got := build.Axes().Canonical; got != CanonicalVerifiedInternal {
		t.Fatalf("build canonical = %q, want VERIFIED_INTERNAL", got)
	}
	pentest, _ := r.Get("pentest")
	if got := pentest.Axes().Canonical; got != CanonicalExternallyQualified {
		t.Fatalf("pentest canonical = %q, want EXTERNALLY_QUALIFIED", got)
	}
	if pentest.Axes().Canonical == build.Axes().Canonical {
		t.Fatal("an externally-qualified gate must never report the same canonical value as a no-dependency gate")
	}

	rep := r.Axes()
	if rep.VerifiedInternal != 1 || rep.ExternallyQualifiedCount != 1 {
		t.Fatalf("rollup = verified:%d externally_qualified:%d, want 1 and 1", rep.VerifiedInternal, rep.ExternallyQualifiedCount)
	}
	if total := rep.VerifiedInternal + rep.ReadyForExternalQualification + rep.ExternallyQualifiedCount + rep.BlockedExternal + rep.NotReady; total != len(rep.Gates) {
		t.Fatalf("canonical buckets sum to %d, want exactly %d", total, len(rep.Gates))
	}
}

func TestAttachAxisEvidenceRefusesAnUnknownGateAndATamperedArtifact(t *testing.T) {
	r := blockedGateRegistry(t)
	if err := r.AttachEngineering("no_such_gate", axisPassing(t, "no_such_gate", "x")); err == nil {
		t.Error("AttachEngineering accepted an unknown gate")
	}
	bad := axisPassing(t, "scale_qualification", "x")
	bad.Hash = "0000"
	if err := r.AttachInternal("scale_qualification", bad); err == nil {
		t.Error("AttachInternal accepted an artifact whose hash does not match its content")
	}
}

// TestReadinessManifestCarriesTheAxes proves the separation actually
// reaches the artifact operators read, not just the in-process type.
func TestReadinessManifestCarriesTheAxes(t *testing.T) {
	r := blockedGateRegistry(t)
	m := BuildReadinessManifest(r, AcceptanceManifest{}, ReleaseCertificate{Version: "v0", GitCommit: "abc"})
	if len(m.Axes.Gates) != 1 {
		t.Fatalf("manifest carries %d axis rows, want 1", len(m.Axes.Gates))
	}
	if m.Axes.Gates[0].Final != AxisBlockedExternal {
		t.Fatalf("manifest axis Final = %q, want BLOCKED_EXTERNAL", m.Axes.Gates[0].Final)
	}
	if m.Assessment.Verdict != VerdictNotProductionReady {
		t.Fatalf("adding axes changed the verdict to %q", m.Assessment.Verdict)
	}
	raw, err := m.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("empty manifest JSON")
	}
}
