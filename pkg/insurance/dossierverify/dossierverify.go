// Package dossierverify is the Independent Dossier Verifier the Round 4
// work order requires under the tagline "Don't trust VERIQO. Verify
// VERIQO." — a checker for one case's dossier that does not read and
// trust VERIQO's own cached output fields. It is the library this
// program's two consumers share: cmd/veriqo-dossier-verify (the
// standalone CLI a human runs directly) and cmd/veriqo-readiness (which
// registers RunAll as its own dossier_verification gate, so the
// operational CLI's own logic is exactly what the release gate checks
// — not a second, only-loosely-related implementation).
//
// Honesty note on scope, stated once here rather than left implicit:
// a literal separate organization auditing VERIQO from entirely outside
// this codebase is exactly the class of external dependency this
// program's own governing rules forbid fabricating — it needs a real
// independent party, which no sandboxed session can manufacture. What
// this package honestly IS: it trusts none of a single Drive() run's
// cached fields, and instead re-derives every claim the dossier makes
// from the same public, deterministic, pure-function APIs a real
// external auditor holding only the raw case data would have to use —
//
//   - independent_reproduction: the case is driven TWICE, completely
//     independently, from the same declared scenario input, and the
//     two runs' evidence root hash, quantum figure and dossier are
//     compared byte-for-byte.
//   - evidence_chain_integrity: the evidence manifest's root hash is
//     independently RECOMPUTED from the raw evidence registry via
//     verification.Verify.
//   - quantum_recomputation: the indicative claim value is independently
//     recomputed from its own recorded ComputeInput via the pure
//     function quantum.Compute.
//   - cold_replay: the case's own canonical-snapshot cold replay
//     (export -> discard -> reconstruct -> replay) is run.
//   - no_verdict_field: a structural, reflection-based scan of the
//     actual compiled Dossier type confirms it carries no
//     coverage/liability/settlement verdict field.
package dossierverify

import (
	"fmt"
	"reflect"
	"strings"

	"veriqo/pkg/insurance/casepack"
	"veriqo/pkg/insurance/dossier"
	insevidence "veriqo/pkg/insurance/evidence"
	"veriqo/pkg/insurance/quantum"
	"veriqo/pkg/insurance/verification"
)

// Check is one independently-verified claim about a case's dossier.
type Check struct {
	Name   string `json:"name"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail,omitempty"`
}

// Report is the full independent-verification result for one case.
type Report struct {
	CaseID  string  `json:"case_id"`
	Tagline string  `json:"tagline"`
	Checks  []Check `json:"checks"`
}

// Pass is derived from Checks; there is no settable verdict field here
// either, for exactly the reason this whole package exists.
func (r Report) Pass() bool {
	for _, c := range r.Checks {
		if !c.Pass {
			return false
		}
	}
	return true
}

// AllCaseIDs are the identifiers RunAll verifies: the seven synthetic
// cases plus the golden cross-domain extension.
func AllCaseIDs() []string {
	ids := make([]string, 0, 8)
	for _, id := range casepack.AllCaseIDs() {
		ids = append(ids, string(id))
	}
	return append(ids, "golden")
}

// RunAll runs Verify for every case RunAll knows about and reports
// whether every single one independently verified.
func RunAll() (allPass bool, reports []Report, err error) {
	allPass = true
	for _, id := range AllCaseIDs() {
		r, verr := Verify(id)
		if verr != nil {
			return false, reports, fmt.Errorf("dossierverify: %s: %w", id, verr)
		}
		reports = append(reports, r)
		if !r.Pass() {
			allPass = false
		}
	}
	return allPass, reports, nil
}

// Verify runs every independent check for one case ID and returns the
// assembled Report. It never returns a Report with Pass()==true unless
// every check actually ran and actually agreed.
func Verify(caseID string) (Report, error) {
	if caseID == "golden" {
		return verifyGolden()
	}
	return verifyCase(casepack.CaseID(caseID))
}

func verifyCase(id casepack.CaseID) (Report, error) {
	c, err := casepack.Get(id)
	if err != nil {
		return Report{}, fmt.Errorf("unknown case %q: %w", id, err)
	}

	runA, err := casepack.Drive(c, nil)
	if err != nil {
		return Report{}, fmt.Errorf("first independent run: %w", err)
	}
	runB, err := casepack.Drive(c, nil)
	if err != nil {
		return Report{}, fmt.Errorf("second independent run: %w", err)
	}

	report := Report{CaseID: string(id), Tagline: "Don't trust VERIQO. Verify VERIQO."}

	report.Checks = append(report.Checks, checkIndependentReproduction(
		runA.Manifest.EvidenceRootHash, runB.Manifest.EvidenceRootHash,
		runA.Quantum, runB.Quantum, runA.Dossier, runB.Dossier))

	report.Checks = append(report.Checks, checkEvidenceChainIntegrity(runA.Manifest, runA.Facade.Case().Evidence))
	report.Checks = append(report.Checks, checkQuantumRecomputation(runA.QuantumInput, runA.Quantum))

	coldLive, coldReplayed, coldReport, err := casepack.ColdReplay(c)
	report.Checks = append(report.Checks, checkColdReplay(coldLive, coldReplayed, coldReport, err))

	report.Checks = append(report.Checks, checkNoVerdictField())

	return report, nil
}

func verifyGolden() (Report, error) {
	runA, err := casepack.DriveGolden()
	if err != nil {
		return Report{}, fmt.Errorf("first independent golden run: %w", err)
	}
	runB, err := casepack.DriveGolden()
	if err != nil {
		return Report{}, fmt.Errorf("second independent golden run: %w", err)
	}

	report := Report{CaseID: "GOLDEN", Tagline: "Don't trust VERIQO. Verify VERIQO."}

	report.Checks = append(report.Checks, checkIndependentReproduction(
		runA.Manifest.EvidenceRootHash, runB.Manifest.EvidenceRootHash,
		runA.QuantumWithSalvage, runB.QuantumWithSalvage, runA.Dossier, runB.Dossier))

	report.Checks = append(report.Checks, checkEvidenceChainIntegrity(runA.Manifest, runA.Facade.Case().Evidence))
	// The base figure (unmodified recorded input, salvage baked into the
	// case's own narrative) recomputes against the base Result's own
	// cached Quantum field -- runA.Quantum, inherited from *Result.
	report.Checks = append(report.Checks, checkQuantumRecomputation(runA.QuantumInput, runA.Quantum))
	report.Checks = append(report.Checks, checkGoldenSalvageRecomputation(runA))

	_, coldReport, err := casepack.GoldenColdReplay()
	pass := err == nil && coldReport.Pass()
	detail := "cold-replayed golden case matches the live run"
	if err != nil {
		detail = err.Error()
	} else if !pass {
		detail = fmt.Sprintf("cold replay failures: %v", coldReport.Failures)
	}
	report.Checks = append(report.Checks, Check{Name: "cold_replay", Pass: pass, Detail: detail})

	report.Checks = append(report.Checks, checkNoVerdictField())

	return report, nil
}

func checkIndependentReproduction(
	hashA, hashB string,
	quantA, quantB quantum.Calculation,
	dossA, dossB *dossier.Dossier,
) Check {
	switch {
	case hashA != hashB:
		return Check{Name: "independent_reproduction", Pass: false,
			Detail: fmt.Sprintf("evidence root hash diverged between two independent runs: %s vs %s", hashA, hashB)}
	case quantA.IndicativeClaimValue != quantB.IndicativeClaimValue:
		return Check{Name: "independent_reproduction", Pass: false,
			Detail: fmt.Sprintf("indicative claim value diverged: %s vs %s", quantA.IndicativeClaimValue, quantB.IndicativeClaimValue)}
	case !reflect.DeepEqual(dossA, dossB):
		return Check{Name: "independent_reproduction", Pass: false,
			Detail: "dossier diverged between two independent runs of the same declared input"}
	}
	return Check{Name: "independent_reproduction", Pass: true,
		Detail: fmt.Sprintf("two independent runs agree exactly (evidence_root_hash=%s)", hashA)}
}

func checkEvidenceChainIntegrity(m verification.Manifest, reg *insevidence.Registry) Check {
	if err := verification.Verify(m, reg); err != nil {
		return Check{Name: "evidence_chain_integrity", Pass: false, Detail: err.Error()}
	}
	return Check{Name: "evidence_chain_integrity", Pass: true,
		Detail: fmt.Sprintf("root hash recomputed from %d raw evidence records, matches the manifest", m.EvidenceCount)}
}

func checkQuantumRecomputation(in quantum.ComputeInput, cached quantum.Calculation) Check {
	recomputed, err := quantum.Compute(in)
	if err != nil {
		return Check{Name: "quantum_recomputation", Pass: false, Detail: "recomputation failed: " + err.Error()}
	}
	if recomputed.IndicativeClaimValue != cached.IndicativeClaimValue {
		return Check{Name: "quantum_recomputation", Pass: false,
			Detail: fmt.Sprintf("recomputed %s from the recorded input, cached value was %s",
				recomputed.IndicativeClaimValue, cached.IndicativeClaimValue)}
	}
	return Check{Name: "quantum_recomputation", Pass: true,
		Detail: fmt.Sprintf("recomputed %s from ComputeInput via the pure quantum.Compute function, matches", recomputed.IndicativeClaimValue)}
}

// checkGoldenSalvageRecomputation independently redoes the exact two
// recomputations golden.go performs (salvage forced to zero, and
// salvage set to the case's own registered net value) from the
// recorded QuantumInput, and checks both against the GoldenResult's
// own cached fields plus the invariant golden_test.go enforces: the
// figure must drop by EXACTLY the salvage net value, no more, no less.
func checkGoldenSalvageRecomputation(gr *casepack.GoldenResult) Check {
	without := gr.QuantumInput
	without.CalculationID = "VERIFY-WITHOUT-SALVAGE"
	without.Salvage = quantum.EvidenceBackedAmount{}
	withoutCalc, err := quantum.Compute(without)
	if err != nil {
		return Check{Name: "golden_salvage_recomputation", Pass: false, Detail: "recomputing without salvage: " + err.Error()}
	}

	with := gr.QuantumInput
	with.CalculationID = "VERIFY-WITH-SALVAGE"
	with.Salvage = gr.SalvageNetValue
	withCalc, err := quantum.Compute(with)
	if err != nil {
		return Check{Name: "golden_salvage_recomputation", Pass: false, Detail: "recomputing with salvage: " + err.Error()}
	}

	if withoutCalc.IndicativeClaimValue != gr.QuantumWithoutSalvage.IndicativeClaimValue {
		return Check{Name: "golden_salvage_recomputation", Pass: false,
			Detail: fmt.Sprintf("without-salvage recomputation %s does not match cached %s",
				withoutCalc.IndicativeClaimValue, gr.QuantumWithoutSalvage.IndicativeClaimValue)}
	}
	if withCalc.IndicativeClaimValue != gr.QuantumWithSalvage.IndicativeClaimValue {
		return Check{Name: "golden_salvage_recomputation", Pass: false,
			Detail: fmt.Sprintf("with-salvage recomputation %s does not match cached %s",
				withCalc.IndicativeClaimValue, gr.QuantumWithSalvage.IndicativeClaimValue)}
	}
	diff := withoutCalc.IndicativeClaimValue - withCalc.IndicativeClaimValue
	if diff != gr.SalvageNetValue.Amount {
		return Check{Name: "golden_salvage_recomputation", Pass: false,
			Detail: fmt.Sprintf("recomputed drop %s does not equal the salvage net value %s", diff, gr.SalvageNetValue.Amount)}
	}
	return Check{Name: "golden_salvage_recomputation", Pass: true,
		Detail: fmt.Sprintf("independently recomputed both figures from ComputeInput; drop of exactly %s matches the salvage net value", diff)}
}

func checkColdReplay(live, replayed *casepack.Result, report casepack.ColdReplayReport, err error) Check {
	if err != nil {
		return Check{Name: "cold_replay", Pass: false, Detail: err.Error()}
	}
	if !report.Pass() {
		return Check{Name: "cold_replay", Pass: false, Detail: fmt.Sprintf("%v", report.Failures)}
	}
	_ = live
	_ = replayed
	return Check{Name: "cold_replay", Pass: true,
		Detail: fmt.Sprintf("snapshot %s replayed cold reproduces the live result exactly", report.SnapshotID)}
}

// checkNoVerdictField is the same structural rule
// dossier.TestDossierHasNoVerdictField enforces in CI, run here as a
// live operational check against the actual compiled type rather than
// only inside a unit test a reader has to trust was run.
func checkNoVerdictField() Check {
	forbidden := []string{
		"verdict", "decision", "approved", "rejected", "denied",
		"settlement", "payable", "liable", "finalamount", "finaldecision",
	}
	typ := reflect.TypeOf(dossier.Dossier{})
	for i := 0; i < typ.NumField(); i++ {
		name := strings.ToLower(typ.Field(i).Name)
		for _, f := range forbidden {
			if strings.Contains(name, f) {
				return Check{Name: "no_verdict_field", Pass: false,
					Detail: fmt.Sprintf("field %q matches forbidden term %q", typ.Field(i).Name, f)}
			}
		}
	}
	return Check{Name: "no_verdict_field", Pass: true,
		Detail: fmt.Sprintf("scanned %d fields on the compiled Dossier type, none is a coverage/liability/settlement verdict", typ.NumField())}
}
