// Package trust implements Veriqo's Trust Governance Layer — closing
// the gap named in "Hal_yang_sangat_krusial.docx" section 3: "Belum
// terlihat adanya Trust Kernel yang menjadi pusat seluruh keputusan
// terkait kepercayaan sistem", with the requested pipeline:
//
//	Trust Policy -> Trust Validator -> Trust Rule -> Trust Evidence
//	-> Trust Score -> Trust Certificate
//
// This package is deliberately independent of any specific source of
// trust evidence (contradiction.SourceStandings, audit records, etc.)
// — TrustEvidence is a generic named-metric bag, so any subsystem can
// feed it without this package importing them and risking a cycle.
package trust

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
)

var (
	ErrUnknownPolicy      = errors.New("trust: unknown policy")
	ErrNoRulesInPolicy    = errors.New("trust: policy has no rules")
	ErrCertificateExpired = errors.New("trust: certificate subject/score no longer matches current record")
)

// TrustEvidence is Stage 4: the raw, named inputs a TrustRule evaluates
// — e.g. {"win_rate": 0.9, "contradiction_rate": 0.05, "tenure_ticks": 500}.
// Deliberately a flat map so any subsystem (contradiction ledger, audit
// log, fusion source profile) can supply evidence without this package
// depending on their types.
type TrustEvidence map[string]float64

// TrustRule is Stage 3: one named, weighted evaluation function over
// TrustEvidence, producing a partial score in [0,1].
type TrustRule struct {
	Name   string
	Weight float64
	// Evaluate maps raw evidence to a normalized [0,1] partial score.
	// Kept as a pure function reference (not a closure over external
	// state) so rules stay replay-deterministic.
	Evaluate func(TrustEvidence) float64
}

// TrustPolicy is Stage 1: a named bundle of TrustRules plus the
// threshold a subject must clear to be issued a certificate.
type TrustPolicy struct {
	Name             string
	Rules            []TrustRule
	CertifyThreshold float64
}

// Validate is Stage 2 (Trust Validator): checks a policy is well-formed
// before it can be registered/used — weights positive, at least one
// rule, no duplicate rule names.
func (p TrustPolicy) Validate() error {
	if len(p.Rules) == 0 {
		return ErrNoRulesInPolicy
	}
	seen := make(map[string]bool, len(p.Rules))
	for _, r := range p.Rules {
		if r.Weight <= 0 {
			return fmt.Errorf("trust: rule %q has non-positive weight %f", r.Name, r.Weight)
		}
		if r.Evaluate == nil {
			return fmt.Errorf("trust: rule %q has no Evaluate function", r.Name)
		}
		if seen[r.Name] {
			return fmt.Errorf("trust: duplicate rule name %q", r.Name)
		}
		seen[r.Name] = true
	}
	return nil
}

// TrustScore is Stage 5: the weighted-average output of evaluating every
// rule in a policy against a subject's evidence, plus the full
// per-rule breakdown (matches the explainability discipline used across
// the rest of this repo — decision.ExplainBreakdown, contradiction's
// source ledger).
type TrustScore struct {
	SubjectID  string
	PolicyName string
	Score      float64
	RuleScores map[string]float64
	Tick       uint64
}

// TrustCertificate is Stage 6: a hash-committed statement that a subject
// cleared a policy's CertifyThreshold at a given tick — the artifact an
// external party (or Veriqo's own decision layer) can check without
// re-running the full rule evaluation.
type TrustCertificate struct {
	Index      uint64
	SubjectID  string
	PolicyName string
	Score      float64
	Threshold  float64
	Tick       uint64
	PrevHash   string
	Hash       string
}

// Kernel is the Trust Kernel: the single registry of policies and the
// hash-chained ledger of every certificate ever issued, per the audit
// doc's explicit ask for a central authority over trust decisions.
type Kernel struct {
	mu         sync.RWMutex
	policies   map[string]TrustPolicy
	ledger     []TrustCertificate
	head       string
	latestByID map[string]TrustScore // most recent score per subject (any policy)
}

func NewKernel() *Kernel {
	return &Kernel{
		policies:   make(map[string]TrustPolicy),
		latestByID: make(map[string]TrustScore),
	}
}

// RegisterPolicy is Stage 1+2: validate and register a TrustPolicy.
func (k *Kernel) RegisterPolicy(p TrustPolicy) error {
	if err := p.Validate(); err != nil {
		return err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.policies[p.Name] = p
	return nil
}

// Evaluate is Stage 3+4+5: run every rule in the named policy against
// the supplied TrustEvidence, producing a TrustScore. Pure computation —
// does not itself issue a certificate (see Certify).
func (k *Kernel) Evaluate(subjectID, policyName string, evidence TrustEvidence, tick uint64) (TrustScore, error) {
	k.mu.RLock()
	policy, ok := k.policies[policyName]
	k.mu.RUnlock()
	if !ok {
		return TrustScore{}, fmt.Errorf("%w: %s", ErrUnknownPolicy, policyName)
	}

	ruleScores := make(map[string]float64, len(policy.Rules))
	var weightedSum, weightTotal float64
	for _, r := range policy.Rules {
		s := r.Evaluate(evidence)
		if s < 0 {
			s = 0
		}
		if s > 1 {
			s = 1
		}
		ruleScores[r.Name] = s
		weightedSum += r.Weight * s
		weightTotal += r.Weight
	}
	score := 0.0
	if weightTotal > 0 {
		score = weightedSum / weightTotal
	}

	ts := TrustScore{SubjectID: subjectID, PolicyName: policyName, Score: score, RuleScores: ruleScores, Tick: tick}
	k.mu.Lock()
	k.latestByID[subjectID] = ts
	k.mu.Unlock()
	return ts, nil
}

// Certify is Stage 6: evaluates (via Evaluate) and, if the score meets
// the policy's CertifyThreshold, appends a hash-chained TrustCertificate
// to the kernel's ledger. Returns the score regardless of whether
// certification succeeded, plus a nil *TrustCertificate if it did not.
func (k *Kernel) Certify(subjectID, policyName string, evidence TrustEvidence, tick uint64) (TrustScore, *TrustCertificate, error) {
	score, err := k.Evaluate(subjectID, policyName, evidence, tick)
	if err != nil {
		return TrustScore{}, nil, err
	}
	k.mu.RLock()
	policy := k.policies[policyName]
	k.mu.RUnlock()

	if score.Score < policy.CertifyThreshold {
		return score, nil, nil
	}

	k.mu.Lock()
	defer k.mu.Unlock()
	idx := uint64(len(k.ledger)) + 1
	prev := k.head
	cert := TrustCertificate{
		Index: idx, SubjectID: subjectID, PolicyName: policyName,
		Score: score.Score, Threshold: policy.CertifyThreshold, Tick: tick, PrevHash: prev,
	}
	cert.Hash = hashCertificate(cert)
	k.ledger = append(k.ledger, cert)
	k.head = cert.Hash
	return score, &cert, nil
}

func hashCertificate(c TrustCertificate) string {
	b := []byte(fmt.Sprintf("idx=%d|subject=%s|policy=%s|score=%.10f|threshold=%.10f|tick=%d|prev=%s|",
		c.Index, c.SubjectID, c.PolicyName, c.Score, c.Threshold, c.Tick, c.PrevHash))
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Ledger returns a defensive copy of every certificate ever issued, in
// issuance order.
func (k *Kernel) Ledger() []TrustCertificate {
	k.mu.RLock()
	defer k.mu.RUnlock()
	out := make([]TrustCertificate, len(k.ledger))
	copy(out, k.ledger)
	return out
}

// CertificatesFor returns every certificate issued to a given subject,
// in issuance order — the "trust history" query surface.
func (k *Kernel) CertificatesFor(subjectID string) []TrustCertificate {
	k.mu.RLock()
	defer k.mu.RUnlock()
	var out []TrustCertificate
	for _, c := range k.ledger {
		if c.SubjectID == subjectID {
			out = append(out, c)
		}
	}
	return out
}

// VerifyLedger re-derives every certificate's hash from its fields and
// confirms chain linkage — the same tamper-evidence discipline as every
// other ledger in this repository.
func (k *Kernel) VerifyLedger() error {
	k.mu.RLock()
	defer k.mu.RUnlock()
	prev := ""
	for i, c := range k.ledger {
		if c.Index != uint64(i)+1 {
			return fmt.Errorf("trust: index gap at %d", i)
		}
		if c.PrevHash != prev {
			return fmt.Errorf("trust: broken chain linkage at index %d", c.Index)
		}
		recomputed := hashCertificate(c)
		if recomputed != c.Hash {
			return fmt.Errorf("trust: hash mismatch at index %d", c.Index)
		}
		prev = recomputed
	}
	return nil
}

// RestoreLedger reloads a previously-persisted ledger into a freshly
// constructed Kernel so certificates issued in an earlier process
// continue the SAME hash chain instead of starting a new one — the
// concrete capability a durability layer needs to make this Kernel's
// hash-chained state survive a process restart. The full chain is
// independently re-verified (same check as VerifyLedger) before it is
// adopted; a tampered or corrupt persisted ledger is rejected, not
// silently trusted. Honest limitation: this restores the ledger (the
// tamper-evident source of truth) but not the latestByID convenience
// cache, which simply repopulates itself as Evaluate/Certify are
// called again.
func (k *Kernel) RestoreLedger(certs []TrustCertificate) error {
	prev := ""
	for i, c := range certs {
		if c.Index != uint64(i)+1 {
			return fmt.Errorf("trust: restore: index gap at position %d (index=%d)", i, c.Index)
		}
		if c.PrevHash != prev {
			return fmt.Errorf("trust: restore: broken chain linkage at index %d", c.Index)
		}
		if hashCertificate(c) != c.Hash {
			return fmt.Errorf("trust: restore: hash mismatch at index %d", c.Index)
		}
		prev = c.Hash
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.ledger = append([]TrustCertificate{}, certs...)
	k.head = prev
	return nil
}

// --- built-in rule constructors: common trust signals -------------------

// RuleFromEvidenceKey builds a TrustRule that reads a single named key
// directly out of TrustEvidence (already normalized to [0,1] by the
// caller) — the common case, e.g. "win_rate" fed straight from
// contradiction.SourceStanding.CurrentScore.
func RuleFromEvidenceKey(name string, weight float64, evidenceKey string) TrustRule {
	return TrustRule{
		Name: name, Weight: weight,
		Evaluate: func(e TrustEvidence) float64 { return e[evidenceKey] },
	}
}

// RuleInverse builds a TrustRule that scores 1-value for a named key —
// for evidence keys that are BAD when high (e.g.
// "contradiction_rate": a source that's frequently on the losing side
// of an arbitration should score LOWER trust as that rate rises).
func RuleInverse(name string, weight float64, evidenceKey string) TrustRule {
	return TrustRule{
		Name: name, Weight: weight,
		Evaluate: func(e TrustEvidence) float64 { return 1 - e[evidenceKey] },
	}
}

// SortedRuleNames is a small determinism helper for callers building
// reports over a TrustScore.RuleScores map.
func SortedRuleNames(scores map[string]float64) []string {
	out := make([]string, 0, len(scores))
	for k := range scores {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
