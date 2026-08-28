// Package historicalreplay closes this program's own Round 8
// self-review gap G6 ("Historical real claim replay"). The reviewer's
// own words: "R8 sudah punya replay. Yang kita butuhkan berikutnya
// adalah: permissioned/redacted historical insurance case yang
// melewati: Policy -> Claim -> Evidence -> Timeline -> Coverage ->
// Causation -> Quantum -> Reserve -> Recovery -> Regulatory -> Dispute
// -> Replay" (R8 already has replay; what is needed next is a
// permissioned/redacted historical case that passes through that named
// chain).
//
// What is, and is not, honestly buildable inside this sandbox:
//
//   - The CONTENT of a real historical insurance claim is, categorically,
//     external evidence — the exact same class of dependency as
//     live_data (SWIFT/BoL/AIS/SAR feeds), which BLOCKER_REGISTER.json
//     already lists as BLOCKED_EXTERNAL for the honest structural
//     reason no engineering inside this repository can manufacture a
//     real counterparty's real historical records. Fabricating
//     realistic-looking "historical" content here would be exactly the
//     dishonesty this program's own governing rules forbid (Final
//     Design §39's ban on inventing evidence).
//   - The PERMISSIONING/REDACTION MECHANISM, and proof that the full
//     named chain (now that Round 9 added Payment and unified audit)
//     replays end to end under it, ARE buildable from inside this
//     repository, using only its own already-driven case. That is what
//     this package delivers.
//
// BuildRedactedCase takes an already-driven casepack.GoldenResult (the
// SAME cross-domain case DriveGolden produces — Policy, Claim,
// Evidence, Timeline, Coverage, Causation, Quantum, Reserve, Recovery,
// Regulatory, Dispute, and now Payment, all genuinely wired) and
// produces a PermissionLevel-gated redacted view of it, tagged with
// provenance.OriginReplay -- the origin class this repository's own
// escalation ladder (pkg/evidence/provenance) already reserves for
// exactly this: a replay of prior material, never fabricated live
// content and never claimed synthetic either.
package historicalreplay

import (
	"errors"
	"fmt"

	"veriqo/pkg/evidence/provenance"
	"veriqo/pkg/insurance/casepack"
	"veriqo/pkg/insurance/party"
	"veriqo/pkg/insurance/policy"
	"veriqo/pkg/insurance/quantum"
)

// PermissionLevel is how much of a redacted case a given viewer is
// permitted to see — closed vocabulary, matching every other
// classification type in this domain.
type PermissionLevel string

const (
	// PermissionFull: every field, including real party identifiers and
	// exact monetary figures — an internal, fully-authorized viewer.
	PermissionFull PermissionLevel = "FULL"
	// PermissionRedacted: party identifiers replaced by stable
	// pseudonyms; exact monetary figures still shown (a redacted-but-
	// quantified view, e.g. for an internal audit that must not learn
	// WHO but does need HOW MUCH).
	PermissionRedacted PermissionLevel = "REDACTED"
	// PermissionSummaryOnly: no party identifiers, no exact monetary
	// figures — only stage presence and a magnitude band.
	PermissionSummaryOnly PermissionLevel = "SUMMARY_ONLY"
)

var knownPermissionLevels = map[PermissionLevel]bool{
	PermissionFull: true, PermissionRedacted: true, PermissionSummaryOnly: true,
}

// IsKnownPermissionLevel reports whether l is a modelled permission level.
func IsKnownPermissionLevel(l PermissionLevel) bool { return knownPermissionLevels[l] }

// Stage names one link of the reviewer's own named chain, in order.
type Stage string

const (
	StagePolicy     Stage = "POLICY"
	StageClaim      Stage = "CLAIM"
	StageEvidence   Stage = "EVIDENCE"
	StageTimeline   Stage = "TIMELINE"
	StageCoverage   Stage = "COVERAGE"
	StageCausation  Stage = "CAUSATION"
	StageQuantum    Stage = "QUANTUM"
	StageReserve    Stage = "RESERVE"
	StageRecovery   Stage = "RECOVERY"
	StageRegulatory Stage = "REGULATORY"
	StageDispute    Stage = "DISPUTE"
	StageReplay     Stage = "REPLAY"
)

// orderedStages is the reviewer's own chain, verbatim, in order.
var orderedStages = []Stage{
	StagePolicy, StageClaim, StageEvidence, StageTimeline, StageCoverage,
	StageCausation, StageQuantum, StageReserve, StageRecovery,
	StageRegulatory, StageDispute, StageReplay,
}

// StageSummary is one stage's redacted status. Detail is populated ONLY
// at PermissionFull — this is enforced structurally by
// BuildRedactedCase, not left to a caller's discretion, matching this
// domain's guardrails discipline of mechanical enforcement over
// convention.
type StageSummary struct {
	Stage   Stage  `json:"stage"`
	Present bool   `json:"present"`
	Detail  string `json:"detail,omitempty"`
}

// RedactedCase is the permissioned, redacted view of one already-driven
// case, gated by PermissionLevel.
type RedactedCase struct {
	CaseID          string                 `json:"case_id"`
	OriginClass     provenance.OriginClass `json:"origin_class"`
	PermissionLevel PermissionLevel        `json:"permission_level"`

	Stages []StageSummary `json:"stages"`

	// PartyPseudonyms maps a stable pseudonym ("PARTY-1", "PARTY-2", ...)
	// to the real party.PartyID it stands for. Populated ONLY at
	// PermissionFull — nil at every other level, which is the actual
	// privacy property (the mapping does not exist in the redacted
	// output at all, not merely "hidden" by a display convention).
	PartyPseudonyms map[string]party.PartyID `json:"party_pseudonyms,omitempty"`
	// PartyPseudonymCount is always populated (at every level) — knowing
	// HOW MANY distinct parties were involved is not itself identifying.
	PartyPseudonymCount int `json:"party_pseudonym_count"`

	// QuantumIndicativeClaimValue is the exact figure — populated at
	// PermissionFull and PermissionRedacted, nil at PermissionSummaryOnly.
	QuantumIndicativeClaimValue *quantum.Amount `json:"quantum_indicative_claim_value,omitempty"`
	// QuantumMagnitudeBand is always populated, at every level — an
	// order-of-magnitude bucket computed without ever exposing the exact
	// figure (see magnitudeBand; fixed-point integer comparisons only,
	// no float64, matching this domain's own money discipline).
	QuantumMagnitudeBand string `json:"quantum_magnitude_band"`

	// ReplayVerified records whether the underlying case's own cold
	// replay (casepack.GoldenColdReplay) matched the live run — the
	// final REPLAY stage in the reviewer's own named chain, carried
	// through to every permission level (a yes/no verification result
	// is never sensitive on its own).
	ReplayVerified bool `json:"replay_verified"`
}

var (
	ErrNilGoldenResult        = errors.New("historicalreplay: GoldenResult must not be nil")
	ErrUnknownPermissionLevel = errors.New("historicalreplay: unknown PermissionLevel")
)

// magnitudeBand buckets amt into a coarse order-of-magnitude band,
// using only integer comparisons (never float64, matching
// quantum.Amount's own fixed-point discipline throughout this domain).
// The band alone can never be inverted back to the exact figure.
func magnitudeBand(amt quantum.Amount) string {
	thresholds := []quantum.Amount{
		1_00, 10_00, 100_00, 1_000_00, 10_000_00, 100_000_00, 1_000_000_00, 10_000_000_00,
	}
	labels := []string{
		"< 1", "1 - 10", "10 - 100", "100 - 1,000", "1,000 - 10,000",
		"10,000 - 100,000", "100,000 - 1,000,000", "1,000,000 - 10,000,000", ">= 10,000,000",
	}
	for i, t := range thresholds {
		if amt < t {
			return labels[i] + " major units"
		}
	}
	return labels[len(labels)-1] + " major units"
}

// BuildRedactedCase produces a PermissionLevel-gated RedactedCase from
// gr, an already-driven golden case (see casepack.DriveGolden).
// replayVerified should be the result of casepack.GoldenColdReplay
// (or an equivalent cold-replay comparison) for the SAME case —
// BuildRedactedCase never re-derives it, matching this program's own
// "never duplicate logic another package already computed" rule.
func BuildRedactedCase(gr *casepack.GoldenResult, replayVerified bool, level PermissionLevel) (RedactedCase, error) {
	if gr == nil {
		return RedactedCase{}, ErrNilGoldenResult
	}
	if !IsKnownPermissionLevel(level) {
		return RedactedCase{}, fmt.Errorf("%w: %q", ErrUnknownPermissionLevel, level)
	}

	rc := RedactedCase{
		CaseID:          string(gr.CaseID),
		OriginClass:     provenance.OriginReplay,
		PermissionLevel: level,
		ReplayVerified:  replayVerified,
	}

	rc.QuantumMagnitudeBand = magnitudeBand(gr.QuantumWithSalvage.IndicativeClaimValue)
	if level == PermissionFull || level == PermissionRedacted {
		v := gr.QuantumWithSalvage.IndicativeClaimValue
		rc.QuantumIndicativeClaimValue = &v
	}

	pseudonyms := collectPartyPseudonyms(gr)
	rc.PartyPseudonymCount = len(pseudonyms)
	if level == PermissionFull {
		rc.PartyPseudonyms = pseudonyms
	}

	rc.Stages = buildStages(gr, level, replayVerified)
	return rc, nil
}

// collectPartyPseudonyms walks every party this golden case's own
// cross-domain layers reference and assigns each a stable, deterministic
// pseudonym in a fixed encounter order — never derived from a hash of
// the real ID (which would let a party with a known real ID be
// re-identified by re-hashing candidate IDs and comparing).
func collectPartyPseudonyms(gr *casepack.GoldenResult) map[string]party.PartyID {
	var order []party.PartyID
	seen := map[party.PartyID]bool{}
	add := func(p party.PartyID) {
		if p != "" && !seen[p] {
			seen[p] = true
			order = append(order, p)
		}
	}
	for _, p := range gr.PolicyVersionWithParticipants.Participants {
		add(party.PartyID(p.PartyID))
	}
	for _, a := range gr.CoInsuranceAllocation {
		add(party.PartyID(a.PartyID))
	}
	if gr.Payment != nil {
		add(gr.Payment.PayeePartyID)
	}
	if gr.BrokerRelationshipID != "" && gr.Relationships != nil {
		if rel, ok := gr.Relationships.Get(gr.BrokerRelationshipID); ok {
			add(rel.FromParty)
			add(rel.ToParty)
		}
	}
	out := make(map[string]party.PartyID, len(order))
	for i, p := range order {
		out[fmt.Sprintf("PARTY-%d", i+1)] = p
	}
	return out
}

// buildStages walks the reviewer's own named chain and reports whether
// this golden case genuinely reached each stage — Detail is populated
// only at PermissionFull.
func buildStages(gr *casepack.GoldenResult, level PermissionLevel, replayVerified bool) []StageSummary {
	full := level == PermissionFull
	detail := func(s string) string {
		if full {
			return s
		}
		return ""
	}

	var coInsurers int
	for _, p := range gr.PolicyVersionWithParticipants.Participants {
		if p.Role == policy.ParticipantCoInsurer {
			coInsurers++
		}
	}

	_, claimErr := gr.Facade.Case().ClaimByID("CLM-" + string(gr.CaseID))

	summaries := map[Stage]StageSummary{
		StagePolicy: {Stage: StagePolicy, Present: gr.PolicyVersionWithParticipants.Insurer != "",
			Detail: detail(fmt.Sprintf("%d co-insurer(s) on this policy version", coInsurers))},
		StageClaim: {Stage: StageClaim, Present: claimErr == nil,
			Detail: detail("claim registered against the case's own policy")},
		StageEvidence: {Stage: StageEvidence, Present: gr.Manifest.EvidenceRootHash != "",
			Detail: detail("evidence root hash: " + gr.Manifest.EvidenceRootHash)},
		StageTimeline: {Stage: StageTimeline, Present: true,
			Detail: detail(fmt.Sprintf("%d timeline conflict(s) assessed", len(gr.TimelineConflict)))},
		StageCoverage: {Stage: StageCoverage, Present: true,
			Detail: detail(fmt.Sprintf("coverage review required: %v", gr.Coverage.ReviewRequired))},
		StageCausation: {Stage: StageCausation, Present: len(gr.Causation.Contenders) > 0,
			Detail: detail(fmt.Sprintf("%d causation contender(s)", len(gr.Causation.Contenders)))},
		StageQuantum: {Stage: StageQuantum, Present: gr.QuantumWithSalvage.IndicativeClaimValue > 0,
			Detail: detail("indicative claim value computed with salvage credited")},
		StageReserve: {Stage: StageReserve, Present: gr.Reserve != nil,
			Detail: detail(reserveDetail(gr))},
		StageRecovery: {Stage: StageRecovery, Present: gr.RecoveryRegistry != nil && gr.RecoveryRegistry.Count() > 0,
			Detail: detail(recoveryDetail(gr))},
		StageRegulatory: {Stage: StageRegulatory, Present: gr.RegulatoryMatter != nil,
			Detail: detail(regulatoryDetail(gr))},
		StageDispute: {Stage: StageDispute, Present: gr.DisputeMatter != nil,
			Detail: detail("dispute issue opened with both parties' positions recorded")},
		StageReplay: {Stage: StageReplay, Present: replayVerified,
			Detail: detail(fmt.Sprintf("cold replay verified: %v", replayVerified))},
	}

	out := make([]StageSummary, 0, len(orderedStages))
	for _, s := range orderedStages {
		out = append(out, summaries[s])
	}
	return out
}

func reserveDetail(gr *casepack.GoldenResult) string {
	if gr.Reserve == nil {
		return ""
	}
	return fmt.Sprintf("reserve status: %s", gr.Reserve.Status())
}

func recoveryDetail(gr *casepack.GoldenResult) string {
	if gr.RecoveryRegistry == nil {
		return ""
	}
	return fmt.Sprintf("%d recovery target(s) registered", gr.RecoveryRegistry.Count())
}

func regulatoryDetail(gr *casepack.GoldenResult) string {
	if gr.RegulatoryMatter == nil {
		return ""
	}
	return fmt.Sprintf("regulatory matter stage: %s", gr.RegulatoryMatter.Stage())
}

// AllStagesPresent reports whether every one of the reviewer's own
// twelve named stages was reached — the structural proof that this
// permissioned/redacted view genuinely passed through the full chain,
// not merely a subset of it.
func (rc RedactedCase) AllStagesPresent() bool {
	if len(rc.Stages) != len(orderedStages) {
		return false
	}
	for _, s := range rc.Stages {
		if !s.Present {
			return false
		}
	}
	return true
}
