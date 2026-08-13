// Package blockers is the 8-blocker systemization machinery: for each
// of pentest, scale_qualification, multi_region_dr, hsm_kms,
// live_data, soak_72h, spire_mtls and supply_chain_scan, it defines a
// machine-readable contract, a pluggable Provider interface, and a
// status vocabulary that makes one specific mistake structurally
// impossible: mistaking a passing fixture for a real qualification.
//
// The prior rounds' pkg/governance/qualification is NOT superseded or
// duplicated here — that package remains the trust-anchored gate for
// REAL external evidence entering the readiness engine (signed
// provider/reviewer identities, release binding, revocation). This
// package is what runs *before* that: building and proving the
// internal capability so that, the moment real evidence exists,
// closing a gate is a provider swap, not a from-scratch build.
//
// Status vocabulary (deliberately more granular than assurance.Status,
// and deliberately never lets a fixture claim the top of it):
//
//	IMPLEMENTED               -- code exists
//	INTEGRATION_TESTED        -- wired into the real execution path
//	QUALIFICATION_TESTED      -- a fixture/test-double provider ran the
//	                             full measurement + evidence pipeline
//	                             end-to-end and it produced a
//	                             structurally valid result
//	READY_FOR_REAL_QUALIFICATION -- the ceiling for anything this
//	                             package can prove by itself
//	REAL_EVIDENCE_SUBMITTED   -- a real-world provider's evidence has
//	                             been submitted (see
//	                             pkg/governance/qualification)
//	REAL_EVIDENCE_VALIDATED   -- that evidence passed cryptographic +
//	                             release-binding validation
//	VERIFIED                  -- pkg/governance/qualification's
//	                             Registry.VerifyGate succeeded on real
//	                             evidence. This package can NEVER
//	                             produce this status itself.
package blockers

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Status is a blocker's systemization progress. Only the first four
// values can ever be set by this package; the last three belong to
// pkg/governance/qualification and are listed here only so a report
// can render the whole lifecycle in one place.
type Status string

const (
	StatusImplemented               Status = "IMPLEMENTED"
	StatusIntegrationTested         Status = "INTEGRATION_TESTED"
	StatusQualificationTested       Status = "QUALIFICATION_TESTED"
	StatusReadyForRealQualification Status = "READY_FOR_REAL_QUALIFICATION"
	StatusRealEvidenceSubmitted     Status = "REAL_EVIDENCE_SUBMITTED"
	StatusRealEvidenceValidated     Status = "REAL_EVIDENCE_VALIDATED"
	StatusVerified                  Status = "VERIFIED"
)

// fixtureCeiling is the highest status this package is ever permitted
// to assert about a blocker whose evidence came from a fixture
// provider. Enforced in Contract.RecordFixtureRun, not just documented.
const fixtureCeiling = StatusReadyForRealQualification

var statusRank = map[Status]int{
	StatusImplemented: 0, StatusIntegrationTested: 1, StatusQualificationTested: 2,
	StatusReadyForRealQualification: 3, StatusRealEvidenceSubmitted: 4,
	StatusRealEvidenceValidated: 5, StatusVerified: 6,
}

// ErrFixtureCannotVerify is returned if any code path tries to make a
// fixture-sourced result claim REAL_EVIDENCE_* or VERIFIED. This is
// the one invariant this whole package exists to enforce.
var ErrFixtureCannotVerify = errors.New("blockers: a fixture/test-double result can never be recorded above READY_FOR_REAL_QUALIFICATION")

// TestProfile declares what kinds of evidence a blocker's contract
// currently accepts. production_mode is "required_for_verification"
// for every one of the eight blockers, always — see Contract.Validate.
type TestProfile struct {
	FixtureMode   bool `json:"fixture_mode"`
	SyntheticMode bool `json:"synthetic_mode"`
	// ProductionRequiredForVerification is always true: no contract in
	// this system may set it false, because that would be exactly the
	// "invent a local substitute" move every audit in this session
	// forbade.
	ProductionRequiredForVerification bool `json:"production_mode_required_for_verification"`
}

// Contract is the machine-readable requirement for one blocker,
// matching the structure requested across every audit round.
type Contract struct {
	ID                        string      `json:"id"`
	Name                      string      `json:"name"`
	Version                   string      `json:"version"`
	RequiredCapabilities      []string    `json:"required_capabilities"`
	RealWorldDependencies     []string    `json:"real_world_dependencies"`
	TestProfile               TestProfile `json:"test_profile"`
	EvidenceRequirements      []string    `json:"evidence_requirements"`
	FailureConditions         []string    `json:"failure_conditions"`
	AcceptanceCriteria        []string    `json:"acceptance_criteria"`
	RealQualificationRequired bool        `json:"real_qualification_required"`

	Status Status `json:"status"`
}

func (c *Contract) Validate() error {
	if c.ID == "" || c.Name == "" {
		return errors.New("blockers: contract requires id and name")
	}
	if len(c.RealWorldDependencies) == 0 {
		return fmt.Errorf("blockers: %s: a contract with zero real-world dependencies cannot be one of the eight blockers", c.ID)
	}
	if !c.RealQualificationRequired {
		return fmt.Errorf("blockers: %s: real_qualification_required must be true -- this system never grants itself an exemption", c.ID)
	}
	if !c.TestProfile.ProductionRequiredForVerification {
		return fmt.Errorf("blockers: %s: production_mode_required_for_verification must be true", c.ID)
	}
	return nil
}

// RunResult is one qualification run's outcome, always tagged with
// exactly how it was produced.
type RunResult struct {
	BlockerID      string            `json:"blocker_id"`
	Mode           string            `json:"mode"` // "FIXTURE", "SIMULATED", or "REAL"
	Pass           bool              `json:"pass"`
	Measurements   map[string]string `json:"measurements"`
	FailureReason  string            `json:"failure_reason,omitempty"`
	ProducedAtTick uint64            `json:"produced_at_tick"`
}

func (r RunResult) isFixtureSourced() bool {
	return r.Mode == "FIXTURE" || r.Mode == "SIMULATED"
}

// MarkImplemented and MarkIntegrationTested are the two status steps
// that precede any qualification run -- set once, by hand, when the
// corresponding code is actually written and wired, not computed from
// any test result.
func (c *Contract) MarkImplemented()       { c.setIfHigher(StatusImplemented) }
func (c *Contract) MarkIntegrationTested() { c.setIfHigher(StatusIntegrationTested) }

func (c *Contract) setIfHigher(s Status) {
	if statusRank[c.Status] < statusRank[s] {
		c.Status = s
	}
}

// RecordFixtureRun advances a contract's status from a fixture or
// simulated run's result. It refuses, unconditionally, to set
// anything above READY_FOR_REAL_QUALIFICATION -- the one rule this
// package exists to make impossible to violate by accident, no matter
// how convincingly a fixture run passes.
func (c *Contract) RecordFixtureRun(result RunResult) error {
	if !result.isFixtureSourced() {
		return fmt.Errorf("blockers: %s: RecordFixtureRun called with mode %q, which is not fixture-sourced -- use pkg/governance/qualification for real evidence", c.ID, result.Mode)
	}
	if !result.Pass {
		return fmt.Errorf("blockers: %s: fixture run failed: %s", c.ID, result.FailureReason)
	}
	next := StatusQualificationTested
	if statusRank[next] > statusRank[fixtureCeiling] {
		// Unreachable given the const above, but kept as a structural
		// assertion: if fixtureCeiling is ever edited to something
		// lower than QUALIFICATION_TESTED, this must fail loudly rather
		// than silently grant a status this function isn't allowed to.
		return ErrFixtureCannotVerify
	}
	c.setIfHigher(next)
	return nil
}

// PromoteToReadyForRealQualification is the explicit, separate step
// (never implicit in RecordFixtureRun) that marks a blocker as having
// completed everything this package can prove: implementation,
// integration, and a passing fixture qualification. It still cannot
// set VERIFIED or either REAL_EVIDENCE_* status.
func (c *Contract) PromoteToReadyForRealQualification() error {
	if statusRank[c.Status] < statusRank[StatusQualificationTested] {
		return fmt.Errorf("blockers: %s: cannot promote to READY_FOR_REAL_QUALIFICATION from %s", c.ID, c.Status)
	}
	c.Status = StatusReadyForRealQualification
	return nil
}

// JSON renders the contract deterministically.
func (c Contract) JSON() ([]byte, error) { return json.MarshalIndent(c, "", "  ") }

// registryFile is the on-disk shape of docs/governance/production-blockers.json.
type registryFile struct {
	Version  string     `json:"version"`
	Blockers []Contract `json:"blockers"`
}

// Registry holds the eight blocker contracts keyed by ID, loaded and
// validated together so a malformed or self-exempting entry anywhere in
// the file fails the whole load rather than silently omitting one
// blocker from every downstream report.
type Registry struct {
	Version   string
	contracts map[string]*Contract
}

// wantBlockerIDs is the fixed set every registry load must contain --
// exactly the eight blockers named across every audit round in this
// project, no more and no fewer.
var wantBlockerIDs = []string{
	"pentest", "scale_qualification", "multi_region_dr", "hsm_kms",
	"live_data", "soak_72h", "spire_mtls", "supply_chain_scan",
}

// LoadRegistry parses and validates a production-blockers.json document.
// It fails if any contract is invalid, if any of the eight required
// blocker IDs is missing, or if an ID is duplicated.
func LoadRegistry(data []byte) (*Registry, error) {
	var f registryFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("blockers: parse registry: %w", err)
	}
	reg := &Registry{Version: f.Version, contracts: make(map[string]*Contract, len(f.Blockers))}
	for i := range f.Blockers {
		c := f.Blockers[i]
		if err := c.Validate(); err != nil {
			return nil, fmt.Errorf("blockers: registry entry %d: %w", i, err)
		}
		if _, dup := reg.contracts[c.ID]; dup {
			return nil, fmt.Errorf("blockers: registry: duplicate id %q", c.ID)
		}
		reg.contracts[c.ID] = &c
	}
	for _, id := range wantBlockerIDs {
		if _, ok := reg.contracts[id]; !ok {
			return nil, fmt.Errorf("blockers: registry: missing required blocker %q", id)
		}
	}
	return reg, nil
}

// Get returns the contract for id, or nil if not present.
func (r *Registry) Get(id string) *Contract { return r.contracts[id] }

// IDs returns the eight blocker IDs in the fixed canonical order.
func (r *Registry) IDs() []string {
	ids := make([]string, len(wantBlockerIDs))
	copy(ids, wantBlockerIDs)
	return ids
}
