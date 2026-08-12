// Exported, pure replay functions — the actual "independent
// recomputation" logic behind every adapter's IVFReplayFunc. They are
// deliberately factored out of adapters.go and given package-level
// visibility so that cmd/veriqo-verify (a genuinely separate OS
// process/binary — see its doc comment) can call the EXACT SAME code
// path an in-process verification would use, rather than a
// hand-maintained duplicate that could silently drift out of sync and
// produce a false PASS or false FAIL. Each function reads every
// parameter it needs from the EvidencePackage itself (see the
// *ArbitrationParams record types in adapters.go) — none of them close
// over any adapter's live state, which is what makes them safe to call
// from a process that never touched the original engine run.
package engine

import (
	"encoding/json"
	"fmt"

	"veriqo/pkg/moat/contradiction"
	"veriqo/pkg/moat/decision"
	"veriqo/pkg/moat/fusion"
	"veriqo/pkg/moat/kg"
	"veriqo/pkg/verification"
)

// ReplayContradiction independently recomputes a contradiction-arbitration
// claim from a bundle's raw observations plus its embedded
// contradiction.ArbitrationParams record, on a brand-new
// contradiction.ArbitrationEngine.
func ReplayContradiction(pkg verification.EvidencePackage) (string, error) {
	fresh := contradiction.NewArbitrationEngine()
	var params *contradictionArbitrationParams
	for _, r := range pkg.Records {
		switch r.Kind {
		case "contradiction.ArbitrationParams":
			var p contradictionArbitrationParams
			if err := json.Unmarshal(r.Payload, &p); err != nil {
				return "", fmt.Errorf("ivf replay: unmarshal arbitration params: %w", err)
			}
			params = &p
		default:
			var o contradiction.RawObservation
			if err := json.Unmarshal(r.Payload, &o); err != nil {
				return "", fmt.Errorf("ivf replay: unmarshal observation %d: %w", r.Index, err)
			}
			if err := fresh.Observe(o); err != nil {
				return "", fmt.Errorf("ivf replay: observe %d: %w", r.Index, err)
			}
		}
	}
	if params == nil {
		return "", fmt.Errorf("ivf replay: bundle is missing its contradiction.ArbitrationParams record")
	}
	tv, err := fresh.ArbitrateClaim(params.ClaimKey, params.Tick, params.HalfLifeTick)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("value=%s|confidence=%.10f", tv.Value, tv.Confidence), nil
}

// ReplayFusion independently recomputes a fusion arbitration from a
// bundle's raw fusion.Evidence records plus its embedded
// fusion.ArbitrationParams record, on a brand-new fusion.Engine over a
// fresh knowledge graph. Each source is re-registered using the
// reliability/provider EMBEDDED IN EACH EVIDENCE RECORD (copied at
// original submission time — see fusion.Evidence's doc comment), never
// from any live SourceProfile registry, so replay is fully
// self-contained in the bundle.
func ReplayFusion(pkg verification.EvidencePackage) (string, error) {
	fresh := fusion.NewEngine(kg.NewGraph())
	registered := map[fusion.SourceID]bool{}
	var params *fusionArbitrationParams
	var evidences []fusion.Evidence
	for _, r := range pkg.Records {
		if r.Kind == "fusion.ArbitrationParams" {
			var p fusionArbitrationParams
			if err := json.Unmarshal(r.Payload, &p); err != nil {
				return "", fmt.Errorf("ivf replay: unmarshal fusion params: %w", err)
			}
			params = &p
			continue
		}
		var ev fusion.Evidence
		if err := json.Unmarshal(r.Payload, &ev); err != nil {
			return "", fmt.Errorf("ivf replay: unmarshal evidence %d: %w", r.Index, err)
		}
		evidences = append(evidences, ev)
	}
	if params == nil {
		return "", fmt.Errorf("ivf replay: bundle is missing its fusion.ArbitrationParams record")
	}
	claim := fusion.Claim{Subject: params.Subject, Predicate: params.Predicate}
	for _, ev := range evidences {
		if !registered[ev.Source] {
			if err := fresh.RegisterSource(fusion.SourceProfile{ID: ev.Source, BaseReliability: ev.Reliability, Provider: ev.Provider}); err != nil {
				return "", fmt.Errorf("ivf replay: register source %s: %w", ev.Source, err)
			}
			registered[ev.Source] = true
		}
		if _, err := fresh.Submit(ev.Source, claim, ev.Value, ev.Tick); err != nil {
			return "", fmt.Errorf("ivf replay: resubmit evidence: %w", err)
		}
	}
	ar, err := fresh.Arbitrate(params.ActorID, claim, params.Tick)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("winner=%s|confidence=%.10f|contradiction=%v", ar.Winner, ar.WinnerConfidence, ar.Contradiction), nil
}

// BuildFusionBundle packages a fusion.Engine's raw evidence for a claim
// into a signed VerificationBundle without requiring the caller to have
// gone through FusionEngineAdapter — the entry point for callers (like
// cmd/veriqo-demo's multi-agent workflow) that drive fusion.Engine
// directly. result must be the ArbitrationResult already computed by
// fe.Arbitrate(actorID, claim, tick) for this exact claim/actorID/tick.
func BuildFusionBundle(fe *fusion.Engine, claim fusion.Claim, actorID string, tick uint64, result fusion.ArbitrationResult) (verification.VerificationBundle, error) {
	ev := fe.EvidenceFor(claim)
	if len(ev) == 0 {
		return verification.VerificationBundle{}, fmt.Errorf("engine: no evidence submitted for claim %q", claim.Key())
	}
	records := make([]verification.EvidenceRecord, 0, len(ev)+1)
	for i, item := range ev {
		payload, err := json.Marshal(item)
		if err != nil {
			return verification.VerificationBundle{}, fmt.Errorf("engine: marshaling fusion evidence %d: %w", i, err)
		}
		records = append(records, verification.EvidenceRecord{Kind: "fusion.Evidence", Index: uint64(i), Payload: payload})
	}
	params, err := json.Marshal(fusionArbitrationParams{Subject: claim.Subject, Predicate: claim.Predicate, ActorID: actorID, Tick: tick})
	if err != nil {
		return verification.VerificationBundle{}, fmt.Errorf("engine: marshaling fusion arbitration params: %w", err)
	}
	records = append(records, verification.EvidenceRecord{Kind: "fusion.ArbitrationParams", Index: uint64(len(ev)), Payload: params})
	records = verification.SortRecordsByIndex(records)
	claimed := fmt.Sprintf("winner=%s|confidence=%.10f|contradiction=%v", result.Winner, result.WinnerConfidence, result.Contradiction)
	rp := verification.ReplayPackage{Evidence: verification.EvidencePackage{Subject: claim.Key(), Records: records}, ClaimedResult: claimed}
	return verification.NewBundle(rp)
}

// ReplayDecision returns a ReplayFunc closed only over the DISCLOSED
// policy (the rule being checked against — not secret, not evidence
// about the subject, so it is the one input this family of functions
// legitimately takes from outside the bundle rather than embedding).
// Everything about the specific decision (actor/subject/values/tick)
// still comes entirely from the bundle's decision.Context record.
func ReplayDecision(policy decision.Policy) verification.ReplayFunc {
	return func(pkg verification.EvidencePackage) (string, error) {
		if len(pkg.Records) == 0 {
			return "", fmt.Errorf("ivf replay: empty decision context")
		}
		var in decisionRawContext
		if err := json.Unmarshal(pkg.Records[0].Payload, &in); err != nil {
			return "", fmt.Errorf("ivf replay: unmarshal decision context: %w", err)
		}
		fresh := decision.NewEngine()
		if err := fresh.RegisterPolicy(policy); err != nil {
			return "", err
		}
		dec, err := fresh.Decide(in.ActorID, in.Subject, in.PolicyName, in.Values, in.Tick)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("action=%s|risk=%.10f", dec.Action, dec.RiskScore), nil
	}
}
