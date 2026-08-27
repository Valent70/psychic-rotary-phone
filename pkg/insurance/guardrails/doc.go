// Package guardrails holds no production code. It exists to carry the
// WHOLE-TREE structural assertions that no single insurance package can
// make about itself.
//
// The per-package guardrails are real and stay where they are:
// pkg/insurance/dossier proves a Dossier has no verdict field,
// pkg/insurance/recovery proves a Target has no liability field,
// pkg/insurance/dispute and pkg/insurance/regulatory prove the same for
// their own types. But a per-package test can only see its own package,
// and the failure this project actually needs to prevent is a NEW type,
// in a NEW package, added by someone who did not read the design
// documents.
//
// So the tests here parse every non-test Go file under pkg/insurance and
// assert the four rules the two frozen design documents state as
// absolute, across the entire domain at once:
//
//  1. No exported type anywhere carries a field that could express a
//     coverage, liability, guilt or settlement determination.
//  2. No exported type anywhere carries a single opaque confidence
//     score — the Final Design §39's last forbidden item. Evidence is
//     decomposed into supporting / contradicting / missing, never
//     collapsed into one number with no derivation.
//  3. No package declares one of the forbidden canonical duplicates
//     (InsuranceIdentity, InsuranceEvidenceStore, InsuranceReplayEngine,
//     InsuranceDecisionEngine, InsuranceEvidenceEngine,
//     InsuranceTrustEngine).
//  4. No package hard-codes a named data vendor, a real reported
//     judgment as a rule, or a real company as a classifier target.
//
// A tree scan is deliberately blunt. Where it flags a name that is
// genuinely innocent, the fix has been to RENAME the field rather than
// to add an exception — LegalHold.CoveredEvidence became
// EvidenceInScope for exactly this reason. The allowlist below is
// therefore very short, and every entry states why it is there.
package guardrails
