// canonical_truth_arbitration_normal_test.go is WAVE A item 4 of the
// canonical-truth-path mandate — MULTI-SOURCE TRUTH ARBITRATION.
//
// The mandate's finding was that RWC's corpus is almost entirely
// single-source, so the arbitration machinery had never actually been
// exercised against a world full of conflict: "Akibatnya engine belum
// benar-benar diuji terhadap dunia nyata yang penuh konflik."
//
// The five scenarios below are the mandate's own list (Case A through
// Case E). They are built on the EXISTING canonical/fusion/contradiction
// machinery — there is no second arbitration engine anywhere in this
// file, and every assertion is on an artifact pkg/canonical genuinely
// produced. The mandate is explicit about this: "reusing the existing
// canonical/fusion/contradiction machinery — do not build a second
// arbitration engine."
//
// The five acceptance criteria are asserted per case, not once at the
// end: the winner is explainable, a contradiction is preserved rather
// than resolved away, the authoritative source wins BY POLICY rather
// than by a name check, human review fires where policy calls for it,
// and replay reproduces the result.
package acceptance

import (
	"strings"
	"testing"

	"veriqo/pkg/canonical"
	"veriqo/pkg/lifecycle"
	"veriqo/pkg/moat/entity"
	"veriqo/pkg/moat/fusion"
	"veriqo/pkg/replay"
)

// berthTimeCase builds a four-source berth-arrival-time case. The four
// source SHAPES are the mandate's own: an AIS feed, a port authority
// record, a NOR/SOF (notice of readiness / statement of facts) document,
// and an operational record.
//
// HONEST SCOPE, stated here so no reader mistakes this suite for
// something it is not: these are structurally-shaped SYNTHETIC sources,
// not live feeds. Nothing in this environment can reach a real AIS
// provider (see the eight external blockers, untouched by this round).
// What is real is the ARBITRATION: the fusion weighting, the
// contradiction detection, the trust gating and the decision are the
// production engines operating on multi-source input, which is exactly
// the thing that had never been exercised.
func berthTimeCase(objective string, subs []canonical.SourceSubmission, tick uint64) truthCase {
	return truthCase{
		objective: objective, tenant: "arbitration-tenant",
		aliases: []entity.Alias{
			{Kind: "IMO", Value: "9074729"},
			{Kind: "MMSI", Value: "636014932"},
		},
		predicate: "BERTH_ARRIVAL_TIME", submissions: subs,
		pattern: 0.4, price: 0.4, tick: tick,
	}
}

// assertReplayReproduces is the fifth acceptance criterion, applied to
// every case in this file.
func assertReplayReproduces(t *testing.T, res *lifecycle.Result) {
	t.Helper()
	cert, err := replay.NewEngine().Replay(res.Execution.ReplayPackage)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if err := cert.Assert(); err != nil {
		t.Fatalf("replay did not reproduce the arbitration: %v", err)
	}
}

// assertWinnerIsExplainable is the first acceptance criterion. The
// arbitration must CITE the deciding factor, not merely announce an
// answer.
func assertWinnerIsExplainable(t *testing.T, res *lifecycle.Result) {
	t.Helper()
	arb := res.Canonical.Arbitration
	if len(arb.Explanation) == 0 {
		t.Fatal("the arbitration produced no explanation at all")
	}
	joined := strings.Join(arb.Explanation, "\n")
	if !strings.Contains(joined, arb.Winner) {
		t.Errorf("the explanation never names the winner %q:\n%s", arb.Winner, joined)
	}
	// The explanation chain the DECISION carries must also reach back to
	// the truth stage, so an investigator reading the decision can get to
	// the arbitration without holding the canonical result.
	var sawTruth bool
	for _, l := range res.Execution.Explanation.Chain {
		if string(l.Stage) == "TRUTH" {
			sawTruth = true
			if l.Output != arb.Winner {
				t.Errorf("the decision explanation's TRUTH link says %q, arbitration says %q",
					l.Output, arb.Winner)
			}
		}
	}
	if !sawTruth {
		t.Error("the decision explanation has no TRUTH link back to the arbitration")
	}
}

// --- CASE A: agreement -----------------------------------------------

// TestAcceptanceCanonicalTruthArbitrationCaseAAgreement: four
// independent sources agree; truth is established with ALL of them
// credited, and no contradiction is manufactured.
func TestAcceptanceCanonicalTruthArbitrationCaseAAgreement(t *testing.T) {
	k := newTruthKernel(t)
	for _, p := range []string{"AIS_PROVIDER", "PORT_AUTHORITY", "SHIPS_AGENT", "TERMINAL_OPERATOR"} {
		assessTrust(t, k, p, 0.9, 1)
	}

	res := berthTimeCase("case A: four sources agree", []canonical.SourceSubmission{
		sub("AIS_FEED", "AIS_PROVIDER", "10:00", 0.80),
		sub("PORT_AUTHORITY_LOG", "PORT_AUTHORITY", "10:00", 0.90),
		sub("NOR_SOF_DOCUMENT", "SHIPS_AGENT", "10:00", 0.70),
		sub("TERMINAL_OPERATIONAL_RECORD", "TERMINAL_OPERATOR", "10:00", 0.75),
	}, 10).run(t, k)

	arb := res.Canonical.Arbitration
	if arb.Winner != "10:00" {
		t.Fatalf("winner = %q, want 10:00", arb.Winner)
	}
	if arb.Contradiction {
		t.Error("four agreeing sources were reported as contradictory")
	}
	if res.Canonical.Truth.ContradictionScore != 0 {
		t.Errorf("contradiction score = %v for unanimous agreement, want 0",
			res.Canonical.Truth.ContradictionScore)
	}

	// ALL FOUR credited: every source must appear on the winning side of
	// the contradiction record, not merely have been counted.
	if arb.EvidenceCount != 4 {
		t.Errorf("evidence count = %d, want 4", arb.EvidenceCount)
	}
	winning := map[string]bool{}
	for _, s := range res.Canonical.Truth.Observation.WinningSources {
		winning[s] = true
	}
	for _, id := range []string{"AIS_FEED", "PORT_AUTHORITY_LOG", "NOR_SOF_DOCUMENT", "TERMINAL_OPERATIONAL_RECORD"} {
		if !winning[id] {
			t.Errorf("%s agreed with the winner but is not credited in WinningSources (%v)",
				id, res.Canonical.Truth.Observation.WinningSources)
		}
	}
	if len(res.Canonical.Truth.Observation.LosingSources) != 0 {
		t.Errorf("unanimous agreement produced losing sources: %v",
			res.Canonical.Truth.Observation.LosingSources)
	}

	// All four providers are TRUSTED, so no review is required — the
	// contrast that makes the review assertions in the other cases mean
	// something.
	if res.HumanReviewRequired {
		t.Errorf("a unanimous case with four trusted providers demanded review: %v",
			res.TrustReviewReasons)
	}

	assertWinnerIsExplainable(t, res)
	assertReplayReproduces(t, res)
}

// --- CASE B: contradiction -------------------------------------------

// TestAcceptanceCanonicalTruthArbitrationCaseBContradiction: three
// sources disagree on a timestamp (10:00 / 10:17 / 10:30 — the
// mandate's own figures). The contradiction must be DETECTED and
// PRESERVED, not silently resolved by picking one.
//
// "Preserved" is the load-bearing word and is asserted three ways: the
// losing values are still individually recoverable from the evidence
// the engine holds, the contradiction record carries both sides, and
// the decision's own explanation reports the conflict rather than
// presenting the winner as uncontested.
func TestAcceptanceCanonicalTruthArbitrationCaseBContradiction(t *testing.T) {
	k := newTruthKernel(t)
	for _, p := range []string{"AIS_PROVIDER", "PORT_AUTHORITY", "SHIPS_AGENT"} {
		assessTrust(t, k, p, 0.9, 1)
	}

	res := berthTimeCase("case B: three sources disagree", []canonical.SourceSubmission{
		sub("AIS_FEED", "AIS_PROVIDER", "10:00", 0.80),
		sub("PORT_AUTHORITY_LOG", "PORT_AUTHORITY", "10:17", 0.82),
		sub("NOR_SOF_DOCUMENT", "SHIPS_AGENT", "10:30", 0.78),
	}, 10).run(t, k)

	arb := res.Canonical.Arbitration
	if arb.RunnerUp == "" {
		t.Fatal("three disagreeing values produced no runner-up; the disagreement was erased")
	}
	if arb.Winner == arb.RunnerUp {
		t.Fatalf("winner and runner-up are both %q", arb.Winner)
	}
	if !arb.Contradiction {
		t.Errorf("three sources reporting 10:00 / 10:17 / 10:30 at near-equal weight were not "+
			"flagged as contradictory (winner %s at %v, runner-up %s at %v)",
			arb.Winner, arb.WinnerConfidence, arb.RunnerUp, arb.RunnerUpConfidence)
	}
	if res.Canonical.Truth.ContradictionScore <= 0 {
		t.Errorf("contradiction score = %v for three disagreeing sources, want > 0",
			res.Canonical.Truth.ContradictionScore)
	}

	// PRESERVED (1): every submitted value is still individually present
	// in the evidence the engine holds — nothing was discarded to reach
	// an answer.
	held := map[string]bool{}
	for _, ev := range k.Canonical.Fusion.EvidenceFor(fusionClaim(res)) {
		held[ev.Value] = true
	}
	for _, v := range []string{"10:00", "10:17", "10:30"} {
		if !held[v] {
			t.Errorf("value %q was submitted but is no longer retrievable from the evidence set", v)
		}
	}

	// PRESERVED (2): the contradiction record names both sides.
	obs := res.Canonical.Truth.Observation
	if len(obs.WinningSources) == 0 || len(obs.LosingSources) == 0 {
		t.Errorf("the contradiction record does not carry both sides: winning=%v losing=%v",
			obs.WinningSources, obs.LosingSources)
	}
	if !obs.Contradiction {
		t.Error("the contradiction record itself does not record that this claim was contested")
	}

	// PRESERVED (3): the decision explanation reports the conflict.
	conflicting := res.Execution.Explanation.ConflictingEvidence
	if len(conflicting) == 0 {
		t.Error("the decision explanation lists no conflicting evidence for a contested claim")
	} else if !strings.Contains(strings.Join(conflicting, " "), arb.RunnerUp) {
		t.Errorf("the conflict report does not name the losing value %q: %v", arb.RunnerUp, conflicting)
	}

	assertWinnerIsExplainable(t, res)
	assertReplayReproduces(t, res)
}

// --- CASE C: source correction ---------------------------------------

// TestAcceptanceCanonicalTruthArbitrationCaseCCorrection: a source's
// initial submission is later corrected. The OLD evidence must be
// preserved — never mutated, never deleted — and a NEW truth recomputed
// from the correction, with both states independently inspectable.
//
// The correction is modelled the way this repository already models one:
// a second case at a later tick, with the corrected value declared as
// DERIVING FROM the original submission through the provenance graph.
// That is a real edge in a real graph, not a flag on a struct — and it
// is what makes the two states independently inspectable, since each
// case has its own certificate, its own execution root and its own
// durable ledger entry.
func TestAcceptanceCanonicalTruthArbitrationCaseCCorrection(t *testing.T) {
	dir := t.TempDir()
	k := newTruthKernel(t)
	l := withDurableLedger(t, k, dir)
	assessTrust(t, k, "PORT_AUTHORITY", 0.9, 1)
	assessTrust(t, k, "AIS_PROVIDER", 0.9, 1)

	// The original submission.
	original := berthTimeCase("case C: original submission", []canonical.SourceSubmission{
		sub("PORT_AUTHORITY_LOG", "PORT_AUTHORITY", "10:00", 0.90),
		sub("AIS_FEED", "AIS_PROVIDER", "10:00", 0.80),
	}, 10).run(t, k)
	if original.Canonical.Arbitration.Winner != "10:00" {
		t.Fatalf("original winner = %q, want 10:00", original.Canonical.Arbitration.Winner)
	}

	// The correction. A NEW source ID that explicitly SUPERSEDES the one
	// it corrects. The original is not overwritten — in an append-only
	// evidence store there is nothing to overwrite — it is RETIRED from
	// the new arbitration while remaining fully readable.
	corrected := berthTimeCase("case C: corrected submission", []canonical.SourceSubmission{
		{
			SourceID: "PORT_AUTHORITY_LOG_CORRECTION_1", Value: "10:42",
			BaseReliability: 0.90, Provider: "PORT_AUTHORITY",
			UpstreamID: "PORT_AUTHORITY_LOG",
			Supersedes: []string{"PORT_AUTHORITY_LOG", "AIS_FEED"},
			SupersedesReason: "port authority reissued the berth log after a gate-clock " +
				"correction; the AIS fix that agreed with the original is retired with it",
		},
		sub("AIS_FEED_2", "AIS_PROVIDER", "10:42", 0.80),
	}, 20).run(t, k)

	// The correction genuinely retired the prior evidence, and said so.
	if len(corrected.Canonical.Supersessions) != 2 {
		t.Fatalf("the correction retired %d pieces of prior evidence, want 2: %+v",
			len(corrected.Canonical.Supersessions), corrected.Canonical.Supersessions)
	}
	if corrected.Canonical.Certificate.SupersededCount != 2 {
		t.Errorf("the certificate records %d supersessions, want 2",
			corrected.Canonical.Certificate.SupersededCount)
	}
	for _, sup := range corrected.Canonical.Supersessions {
		if sup.Reason == "" {
			t.Error("a supersession was recorded with no reason")
		}
		if sup.BySourceID != "PORT_AUTHORITY_LOG_CORRECTION_1" {
			t.Errorf("supersession attributed to %q", sup.BySourceID)
		}
	}

	// A NEW truth, recalculated — not the old one edited.
	if corrected.Canonical.Arbitration.Winner != "10:42" {
		t.Fatalf("corrected winner = %q, want 10:42", corrected.Canonical.Arbitration.Winner)
	}
	if corrected.Canonical.Certificate.Hash == original.Canonical.Certificate.Hash {
		t.Error("the correction produced the same canonical certificate as the original")
	}

	// OLD EVIDENCE PRESERVED: the original claim's evidence is still
	// there, unchanged, and still says 10:00.
	originalClaim := fusionClaim(original)
	var sawOriginal bool
	for _, ev := range k.Canonical.Fusion.EvidenceFor(originalClaim) {
		if ev.Source == "PORT_AUTHORITY_LOG" {
			sawOriginal = true
			if ev.Value != "10:00" {
				t.Errorf("the original submission was MUTATED to %q; corrections must never "+
					"rewrite history", ev.Value)
			}
		}
	}
	if !sawOriginal {
		t.Error("the original submission was DELETED by the correction")
	}

	// BOTH STATES INDEPENDENTLY INSPECTABLE: each case has its own
	// certificate, and each verifies on its own.
	if err := canonical.VerifyCertificate(original.Canonical.Certificate); err != nil {
		t.Errorf("the original certificate no longer verifies after a correction: %v", err)
	}
	if err := canonical.VerifyCertificate(corrected.Canonical.Certificate); err != nil {
		t.Errorf("the corrected certificate does not verify: %v", err)
	}

	// An UNEXPLAINED correction is refused: a supersession with no reason
	// is not a correction, it is a deletion with extra steps.
	unexplained := berthTimeCase("case C: unexplained correction", []canonical.SourceSubmission{
		{
			SourceID: "PORT_AUTHORITY_LOG_CORRECTION_2", Value: "11:11",
			BaseReliability: 0.90, Provider: "PORT_AUTHORITY",
			Supersedes: []string{"PORT_AUTHORITY_LOG_CORRECTION_1"},
		},
	}, 30)
	if _, err := unexplained.runErr(k); err == nil {
		t.Error("a supersession with no reason was accepted")
	}

	// The durable ledger holds both, in order, and the correction's
	// SOURCE_PROVENANCE event names the record it derives from.
	events, err := l.Events()
	if err != nil {
		t.Fatalf("reading the durable ledger: %v", err)
	}
	var sawDerivation bool
	for _, ev := range events {
		if ev.Subject == "PORT_AUTHORITY_LOG_CORRECTION_1" &&
			strings.Contains(ev.Ref, "upstream=PORT_AUTHORITY_LOG") {
			sawDerivation = true
		}
	}
	if !sawDerivation {
		t.Error("the durable ledger does not record that the correction derives from the original")
	}

	// The provenance graph agrees: the correction is NOT independent of
	// what it corrects, which is the whole reason the derivation is
	// declared rather than implied.
	shared, err := k.Canonical.Provenance.PairShared(
		"PORT_AUTHORITY_LOG_CORRECTION_1", "PORT_AUTHORITY_LOG")
	if err != nil {
		t.Fatalf("PairShared: %v", err)
	}
	if !shared {
		t.Error("a correction and the submission it corrects were assessed as independent sources")
	}

	assertWinnerIsExplainable(t, corrected)
	assertReplayReproduces(t, corrected)
}

// --- CASE D: source revocation ---------------------------------------

// TestAcceptanceCanonicalTruthArbitrationCaseDRevocation: a provider
// trusted at submission time is later revoked. A NEW decision made
// after revocation cannot use that provider's evidence to reach its
// outcome — and decisions ALREADY MADE are not silently rewritten.
//
// The second half is asserted explicitly because the mandate asks it to
// be checked rather than assumed ("check what this repo's existing
// trust/revocation semantics already guarantee here"). What this
// repository guarantees is structural rather than procedural: a
// certificate is a hash over the values that produced it, and
// pkg/trust/state ledgers are append-only, so a later revocation has no
// mechanism by which it COULD reach back into an issued certificate.
// The test states that as a checkable fact.
func TestAcceptanceCanonicalTruthArbitrationCaseDRevocation(t *testing.T) {
	k := newTruthKernel(t)
	assessTrust(t, k, "AIS_PROVIDER", 0.9, 1)
	assessTrust(t, k, "PORT_AUTHORITY", 0.9, 1)

	// A decision made while both providers are trusted.
	before := berthTimeCase("case D: before revocation", []canonical.SourceSubmission{
		sub("AIS_FEED", "AIS_PROVIDER", "10:00", 0.80),
		sub("PORT_AUTHORITY_LOG", "PORT_AUTHORITY", "10:17", 0.90),
	}, 10).run(t, k)

	beforeCertHash := before.Canonical.Certificate.Hash
	beforeRoot := before.Certificate.ExecutionRootHash
	beforeEvidence := before.Canonical.Arbitration.EvidenceCount
	if beforeEvidence != 2 {
		t.Fatalf("the pre-revocation decision saw %d evidence items, want 2", beforeEvidence)
	}
	var aisTrusted bool
	for _, s := range before.Canonical.Trust.Sources {
		if s.SourceID == "AIS_FEED" && s.Posture == canonical.PostureNormal {
			aisTrusted = true
		}
	}
	if !aisTrusted {
		t.Fatal("AIS_FEED was not admitted at NORMAL posture before revocation; " +
			"this test needs a genuinely trusted starting state")
	}

	// The provider is revoked, through the governed terminal path.
	revokeTrust(t, k, "AIS_PROVIDER", 15)

	// A NEW decision, after the revocation.
	// A distinct PREDICATE, so this is a genuinely new claim rather than
	// more evidence on the one already arbitrated. Evidence is
	// append-only per claim (see fusion's own design invariants), so
	// reusing the predicate would arbitrate over the union of both cases
	// and the assertions below would be measuring the wrong thing.
	afterCase := berthTimeCase("case D: after revocation", []canonical.SourceSubmission{
		sub("AIS_FEED_2", "AIS_PROVIDER", "10:00", 0.80),
		sub("PORT_AUTHORITY_LOG_2", "PORT_AUTHORITY", "10:17", 0.90),
	}, 20)
	afterCase.predicate = "BERTH_DEPARTURE_TIME"
	after := afterCase.run(t, k)

	// The revoked provider's evidence CANNOT influence the new outcome:
	// it is not downweighted, it is not present.
	if len(after.Canonical.Trust.Excluded) != 1 || after.Canonical.Trust.Excluded[0] != "AIS_FEED_2" {
		t.Fatalf("the revoked provider's source was not excluded: excluded=%v",
			after.Canonical.Trust.Excluded)
	}
	if after.Canonical.Arbitration.EvidenceCount != 1 {
		t.Errorf("the post-revocation arbitration saw %d evidence items, want 1 — the revoked "+
			"provider's submission reached the engine", after.Canonical.Arbitration.EvidenceCount)
	}
	if after.Canonical.Arbitration.Winner != "10:17" {
		t.Errorf("post-revocation winner = %q; the surviving source said 10:17",
			after.Canonical.Arbitration.Winner)
	}
	// Revocation is a review condition, not a silent filter.
	if !after.HumanReviewRequired {
		t.Error("a case with a revoked provider did not require human review")
	}

	// OLDER DECISIONS ARE NOT REWRITTEN. The already-issued certificate
	// still verifies against its own fields, and still commits to the
	// pre-revocation trust head.
	if before.Canonical.Certificate.Hash != beforeCertHash ||
		before.Certificate.ExecutionRootHash != beforeRoot {
		t.Error("an already-issued certificate changed after a later revocation")
	}
	if err := canonical.VerifyCertificate(before.Canonical.Certificate); err != nil {
		t.Errorf("the pre-revocation certificate stopped verifying after a revocation: %v", err)
	}
	if before.Canonical.Certificate.TrustLedgerHead ==
		after.Canonical.Certificate.TrustLedgerHead {
		t.Error("the pre- and post-revocation decisions claim the same trust ledger head")
	}
	// Replay of the OLD decision still reproduces the OLD posture,
	// because the replay package carries the trust ledger as it stood
	// then — not as it stands now.
	assertReplayReproduces(t, before)
	assertReplayReproduces(t, after)
}

// --- CASE E: reliability conflict ------------------------------------

// TestAcceptanceCanonicalTruthArbitrationCaseEReliabilityConflict: a
// low-reliability source disagrees with an authoritative one. The
// authoritative source must win according to a REAL, INSPECTABLE POLICY
// — the mandate is explicit that this must not be a hard-coded name
// check.
//
// The policy here is the composition of two declared, inspectable
// mechanisms that already exist and are already hashed into the
// certificate:
//
//  1. SourceSubmission.BaseReliability — the declared reliability the
//     fusion engine weighs by, visible in DependencyEvaluation.Base.
//  2. canonical.TrustPolicy — the governed trust posture, which
//     multiplies that weight, visible in TrustEvaluation.
//
// Neither consults a source's NAME. The test proves that by swapping
// which source ID holds the authoritative role and showing the outcome
// follows the reliability and trust, not the identifier.
func TestAcceptanceCanonicalTruthArbitrationCaseEReliabilityConflict(t *testing.T) {
	run := func(t *testing.T, authoritativeID, weakID string) *lifecycle.Result {
		t.Helper()
		k := newTruthKernel(t)
		// The authoritative source's PROVIDER is trust-assessed; the weak
		// one's has never been assessed and is additionally declared at a
		// much lower reliability. Both are policy inputs, not names.
		assessTrust(t, k, "PORT_AUTHORITY", 0.95, 1)
		return berthTimeCase("case E: reliability conflict", []canonical.SourceSubmission{
			sub(authoritativeID, "PORT_AUTHORITY", "10:17", 0.95),
			sub(weakID, "ANONYMOUS_TIP_LINE", "23:59", 0.10),
		}, 10).run(t, k)
	}

	res := run(t, "PORT_AUTHORITY_LOG", "UNVETTED_TIP")
	if res.Canonical.Arbitration.Winner != "10:17" {
		t.Fatalf("the authoritative source lost: winner = %q", res.Canonical.Arbitration.Winner)
	}

	// WINS BY POLICY, NOT BY NAME: swap which source ID carries the
	// authoritative role. If anything were keying off the identifier, the
	// outcome would follow the name instead of the reliability.
	swapped := run(t, "UNVETTED_TIP", "PORT_AUTHORITY_LOG")
	if swapped.Canonical.Arbitration.Winner != "10:17" {
		t.Errorf("swapping the source IDs changed the winner to %q — something is keying off the "+
			"source NAME rather than its declared reliability and trust posture",
			swapped.Canonical.Arbitration.Winner)
	}

	// The deciding factors are INSPECTABLE, per source, on the result.
	dep := res.Canonical.Dependency
	if dep.Base["PORT_AUTHORITY_LOG"] <= dep.Base["UNVETTED_TIP"] {
		t.Errorf("declared reliabilities are not what the test assumes: authoritative=%v weak=%v",
			dep.Base["PORT_AUTHORITY_LOG"], dep.Base["UNVETTED_TIP"])
	}
	if dep.Effective["PORT_AUTHORITY_LOG"] <= dep.Effective["UNVETTED_TIP"] {
		t.Errorf("the effective weights do not favour the authoritative source: %v vs %v",
			dep.Effective["PORT_AUTHORITY_LOG"], dep.Effective["UNVETTED_TIP"])
	}
	var authPosture, weakPosture canonical.TrustPosture
	for _, s := range res.Canonical.Trust.Sources {
		switch s.SourceID {
		case "PORT_AUTHORITY_LOG":
			authPosture = s.Posture
		case "UNVETTED_TIP":
			weakPosture = s.Posture
		}
	}
	if authPosture != canonical.PostureNormal {
		t.Errorf("the trust-assessed provider's posture is %s, want NORMAL", authPosture)
	}
	if weakPosture != canonical.PostureRestricted {
		t.Errorf("the never-assessed provider's posture is %s, want RESTRICTED", weakPosture)
	}

	// HUMAN REVIEW FIRES where policy calls for it: one source's provider
	// has never been assessed, so this decision may not be released
	// unreviewed even though the authoritative source won.
	if !res.HumanReviewRequired {
		t.Error("a case containing a never-assessed provider did not require human review")
	}
	if len(res.TrustReviewReasons) == 0 {
		t.Error("review was required with no reason naming the source that caused it")
	} else if !strings.Contains(strings.Join(res.TrustReviewReasons, " "), "UNVETTED_TIP") {
		t.Errorf("the review reasons do not name the unvetted source: %v", res.TrustReviewReasons)
	}

	assertWinnerIsExplainable(t, res)
	assertReplayReproduces(t, res)
}

// fusionClaim rebuilds the fusion claim key a completed run used, so a
// test can ask the engine for the evidence it still holds.
func fusionClaim(res *lifecycle.Result) fusion.Claim {
	return fusion.Claim{
		Subject:   res.Canonical.Certificate.Subject,
		Predicate: res.Canonical.Certificate.Predicate,
	}
}
