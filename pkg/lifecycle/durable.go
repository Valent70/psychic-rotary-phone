// durable.go is the lifecycle half of WAVE A item 5 of the
// canonical-truth-path mandate:
//
//	RunUnified -> append event -> WAL -> fsync policy
//	-> durable ledger head -> certificate
//
// The mandate's finding was that pkg/storage/wal, a real and fully
// tested write-ahead log, had exactly one importer in the whole tree
// (cmd/veriqo-readiness) and that nothing on the live execution path
// ever wrote to it. This file is the seam that closes that, and it is
// deliberately thin: pkg/ledger owns the event vocabulary and the
// durability semantics, pkg/storage/wal owns the storage, and this file
// only decides WHICH events one lifecycle run produces and WHEN they
// are written.
//
// ORDERING, and why it is what it is. Events are appended AFTER the
// execution completes, in one batch, not incrementally during it. Two
// reasons, both real:
//
//  1. Several events (FUSION, CONTRADICTION, DECISION) commit to
//     artifacts the run has not produced yet at the moment it starts.
//     Writing a placeholder and rewriting it later would make the
//     ledger a mutable store, which is the one thing an append-only
//     evidence log may not be.
//  2. The execution itself commits to the ledger head it STARTED from
//     (execution.Context.DurableLedgerHead), and the ledger's own
//     DECISION event commits to the execution root the run produced.
//     The two point at each other; neither is derived from the other.
//     Appending mid-run would collapse that into a circular reference.
//
// The cost is stated plainly rather than hidden: a process killed
// between the execution completing and the batch being written leaves
// no record of that run. That is a genuine, bounded loss window, and it
// is the honest tradeoff for a non-circular commitment — the run in
// question also produced no released decision, since release is itself
// one of the events in the batch. WAVE A item 6's crash test exercises
// exactly this boundary.
package lifecycle

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"veriqo/pkg/canonical"
	"veriqo/pkg/execution"
	"veriqo/pkg/ledger"
	"veriqo/pkg/trust/state"
)

// buildDurableEvents derives the full event set for one completed
// lifecycle run. Every Ref below is a real identifier some subsystem
// genuinely produced for THIS call — never a label this function
// invented — the same discipline recordLineage already follows.
//
// All nine kinds pkg/ledger models are produced by every run, which is
// what makes ledger.KindCoverage(...).Complete() a meaningful check
// rather than a formality:
//
//   - EVIDENCE, SOURCE_PROVENANCE: one pair per submitted source.
//   - IDENTITY: the identity-ledger head entity resolution ran against.
//   - TRUST: the trust evaluation root that gated the evidence.
//   - FUSION, CONTRADICTION: the arbitration and contradiction records.
//   - DECISION: the native action plus the execution root behind it.
//   - REVIEWER: whether human review is required, and why. A run that
//     needs no review still emits this event, with the negative — "no
//     review required" is itself an auditable disposition, and an absent
//     event would be indistinguishable from a lost one.
//   - RELEASE: whether the decision is releasable now, or withheld
//     pending that review.
func buildDurableEvents(res *Result, caseIn canonical.CaseInput, execRes *execution.Result, trustState *state.Engine) []ledger.Event {
	caseID := string(res.CaseID)
	base := func(kind ledger.EventKind, subject, ref, detail string) ledger.Event {
		return ledger.Event{
			Kind: kind, CaseID: caseID, ExecutionID: res.Correlation.ExecutionID,
			CorrelationID: res.Correlation.EvidencePackageID, Tenant: res.Intent.Tenant,
			Actor: res.Intent.ActorID, Subject: subject, Ref: ref, Detail: detail,
			PolicyVersion: caseIn.Policy.Name, Tick: caseIn.Tick,
		}
	}

	canon := res.Canonical
	evs := make([]ledger.Event, 0, 2*len(caseIn.Submissions)+7)

	for _, s := range caseIn.Submissions {
		id := string(s.SourceID)
		evs = append(evs, base(ledger.EventEvidence, id,
			caseIn.Subject+"|"+caseIn.Predicate+"|"+id,
			"submission value hashed into evidence package "+res.Correlation.EvidencePackageID))
		// SOURCE_PROVENANCE records the declared origin posture of this
		// submission: its provider, its declared upstream (empty when the
		// caller declared none — an honest empty, since an undeclared
		// upstream is exactly what provenance.StatusUnknown is about), and
		// the independence status the canonical run computed over the set.
		upstream := s.UpstreamID
		if upstream == "" {
			upstream = "UNDECLARED"
		}
		evs = append(evs, base(ledger.EventSourceProvenance, id,
			"provider="+canonical.TrustSubjectFor(s)+"|upstream="+upstream,
			"independence status "+string(canon.Provenance.Status)+
				" over "+fmtInt(canon.Provenance.PairCount)+" compared pairs; verified_independent="+
				fmtBool(canon.Provenance.IsVerifiedIndependent())))
	}

	trustEvent := base(ledger.EventTrust, canon.Trust.CaseSubject,
		nonEmpty(canon.Trust.RootHash, "NO_TRUST_EVALUATION"),
		"trust policy "+canon.Trust.PolicyHash+" against ledger head "+
			nonEmpty(canon.Trust.LedgerHead, "none")+"; excluded="+
			fmtInt(len(canon.Trust.Excluded))+" review_required="+fmtBool(canon.Trust.ReviewRequired))
	// The TRUST event carries the whole transition ledger, not only its
	// head — see TrustSnapshot's doc comment. A marshalling failure here
	// is not survivable: a durable ledger that silently dropped the one
	// piece of state recovery needs would be worse than no ledger, so the
	// snapshot is omitted only when there is genuinely no trust engine,
	// and any other failure surfaces as a REVIEWER-visible detail rather
	// than a silent empty payload.
	if trustState != nil {
		halfLife, prior := trustState.Params()
		snap := TrustSnapshot{
			Transitions: trustState.Ledger(), HalfLife: halfLife,
			NeutralPrior: prior, LedgerHead: canonical.TrustLedgerHead(trustState),
		}
		if raw, err := json.Marshal(snap); err == nil {
			trustEvent.Payload = raw
		} else {
			trustEvent.Detail += "; TRUST SNAPSHOT UNAVAILABLE: " + err.Error()
		}
	}

	evs = append(evs,
		base(ledger.EventIdentity, string(res.EntityID),
			nonEmpty(res.Correlation.EntityIdentityLedgerHead, "NO_IDENTITY_LEDGER"),
			"entity resolved under "+execRes.Trace.Context.IdentityResolutionVersion),
		trustEvent,
		base(ledger.EventFusion, canon.Truth.ClaimKey,
			nonEmpty(canon.Certificate.ArbitrationHash, canon.Certificate.Hash),
			"winner "+canon.Arbitration.Winner+" over "+fmtInt(canon.Arbitration.EvidenceCount)+" evidence items"),
		base(ledger.EventContradiction, canon.Truth.ClaimKey,
			nonEmpty(canon.Truth.Hash, "NO_CONTRADICTION_RECORD"),
			"contradiction="+fmtBool(canon.Arbitration.Contradiction)+
				" score="+fmtFloat(canon.Truth.ContradictionScore)),
		base(ledger.EventDecision, caseIn.Subject,
			nonEmpty(res.Correlation.DecisionID, canon.Certificate.Hash),
			"action "+string(canon.Decision.Action)+" at risk "+fmtFloat(canon.Decision.RiskScore)+
				" under execution root "+execRes.ExecutionRootHash),
	)

	reviewRef := "NO_REVIEW_REQUIRED"
	reviewDetail := "trust and identity both resolved without escalation"
	if res.HumanReviewRequired {
		reviewRef = "HUMAN_REVIEW_REQUIRED"
		reviewDetail = "review required: " + joinOr(res.TrustReviewReasons,
			"identity resolved outside the canonical authority")
	}
	releaseRef := "RELEASABLE"
	releaseDetail := "no outstanding review condition on this decision"
	if res.HumanReviewRequired {
		releaseRef = "WITHHELD_PENDING_REVIEW"
		releaseDetail = "decision withheld: " + reviewDetail
	}
	evs = append(evs,
		base(ledger.EventReviewer, caseIn.Subject, reviewRef, reviewDetail),
		base(ledger.EventRelease, caseIn.Subject, releaseRef, releaseDetail),
	)
	return evs
}

// TrustSnapshot is the reconstructable trust state a durable TRUST
// event carries in its Payload. It is the trust ledger ITSELF plus the
// decay parameters, not a summary — the same choice, for the same
// reason, that pkg/replay.ExecutionRecord.TrustLedger makes: a head
// alone lets a recovering process verify a hash against nothing.
//
// Decay parameters travel with it because trust decay is applied at
// READ time and never recorded in a transition (see
// pkg/trust/state.Rebuild), so they are not recoverable from the
// transitions.
type TrustSnapshot struct {
	Transitions  []state.Transition `json:"transitions"`
	HalfLife     uint64             `json:"half_life"`
	NeutralPrior float64            `json:"neutral_prior"`
	// LedgerHead is the head the writing process computed, carried so a
	// recovering process can check its own rebuild against what was
	// actually committed rather than only against itself.
	LedgerHead string `json:"ledger_head"`
}

// ErrNoRecoverableTrust is returned by RecoverTrustState when a durable
// ledger holds no TRUST event carrying a snapshot — i.e. there is
// nothing to recover, which is different from recovering an empty
// trust history.
var ErrNoRecoverableTrust = errors.New("lifecycle: durable ledger holds no recoverable trust snapshot")

// RecoverTrustState rebuilds a trust engine from a durable ledger's
// events ALONE — the mandate's section VII step 8 ("reconstruct
// ledger") made real for the one piece of state a crashed process
// genuinely loses.
//
// It takes the LATEST TRUST snapshot in the ledger, verifies the whole
// transition chain through pkg/trust/state.Rebuild (which refuses a
// tampered ledger outright), and additionally checks the rebuilt head
// against the head the writing process committed — so a snapshot whose
// transitions were replaced with an internally-consistent but different
// chain is caught too.
func RecoverTrustState(events []ledger.Event) (*state.Engine, error) {
	var snap *TrustSnapshot
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Kind != ledger.EventTrust || len(events[i].Payload) == 0 {
			continue
		}
		var s TrustSnapshot
		if err := json.Unmarshal(events[i].Payload, &s); err != nil {
			return nil, fmt.Errorf("lifecycle: decoding trust snapshot at ledger event %d: %w", i, err)
		}
		snap = &s
		break
	}
	if snap == nil {
		return nil, ErrNoRecoverableTrust
	}
	eng, err := state.Rebuild(snap.Transitions, snap.HalfLife, snap.NeutralPrior)
	if err != nil {
		return nil, fmt.Errorf("lifecycle: rebuilding trust from the durable ledger: %w", err)
	}
	if got := canonical.TrustLedgerHead(eng); got != snap.LedgerHead {
		return nil, fmt.Errorf("%w: rebuilt trust head %s does not match the committed head %s",
			state.ErrChainBroken, got, snap.LedgerHead)
	}
	return eng, nil
}

// UseTrustState installs a trust engine on BOTH the canonical pipeline
// and the execution engine this Orchestrator owns.
//
// It exists because those two must never disagree, and a recovering
// deployment setting only one of them would produce a DAG that reports
// a trust posture the decision was not actually made under. Before this
// method, the only way to install a recovered engine was to assign two
// separate fields on two separate objects and hope; that is exactly the
// class of silent fragmentation veriqo/kernel.New's shared-pointer
// discipline exists to prevent.
func (o *Orchestrator) UseTrustState(e *state.Engine) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.Pipeline.TrustState = e
	if o.Execution != nil {
		o.Execution.Trust = e
	}
}

// recordDurable appends this run's whole event set and returns the
// durable ledger head afterwards. A failure here fails the RunUnified
// call: a deployment that has attached a durable ledger has declared
// that a decision it cannot record is a decision it will not make.
func (o *Orchestrator) recordDurable(res *Result, caseIn canonical.CaseInput, execRes *execution.Result) (string, error) {
	head, _, err := o.Ledger.AppendAll(buildDurableEvents(res, caseIn, execRes, o.Pipeline.TrustState))
	if err != nil {
		return head, fmt.Errorf("lifecycle: durable ledger: %w", err)
	}
	return head, nil
}

func nonEmpty(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func joinOr(lines []string, fallback string) string {
	if len(lines) == 0 {
		return fallback
	}
	out := lines[0]
	for _, l := range lines[1:] {
		out += "; " + l
	}
	return out
}

func fmtInt(v int) string   { return strconv.Itoa(v) }
func fmtBool(v bool) string { return strconv.FormatBool(v) }
func fmtFloat(v float64) string {
	return strconv.FormatFloat(v, 'g', 17, 64)
}
