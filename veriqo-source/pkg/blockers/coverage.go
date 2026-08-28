package blockers

// PHASE K (P2-18) — External Qualification Harness, the reconciliation
// and the one thing that was genuinely missing.
//
// Reconciliation first, per rule 0, and it is almost entirely a
// "already closed" answer. Every harness the program lists exists,
// with real code and real tests, from prior rounds:
//
//	scale/100-node/1M-envelope       -> pkg/blockers/scale
//	DR/region-failure/RPO/RTO        -> pkg/blockers/dr
//	soak/72h/restart/memory/CPU      -> pkg/blockers/soak
//	KMS/sign/verify/rotation/revoke  -> pkg/blockers/hsmkms
//	SPIRE/SVID/rotation/trust-bundle -> pkg/blockers/spiffe
//	supply chain / pentest / feeds   -> pkg/blockers/{supplychain,pentest,livedata}
//
// and pkg/blockers/orchestrator.RunAll already drives all eight. None
// of that is rebuilt here.
//
// What did NOT exist is finer-grained honesty about them. The
// orchestrator reports READY_FOR_REAL_QUALIFICATION per BLOCKER, which
// answers "does a harness exist for DR" but not "does the DR harness
// actually exercise failback, or only failover". Those are different
// questions, and only the second one tells an operator what a real
// qualification run will and will not measure.
//
// CapabilityRegister below answers the second question. Every row names
// one capability, the Go symbol that implements it, and whether it is
// covered. A row claiming coverage by a symbol that does not exist in
// the source is a test failure (see coverage_test.go), so this register
// cannot decay into documentation about code that was deleted.
//
// The invariant that matters most is stated once, structurally: NO
// capability row, and no combination of them, can produce a VERIFIED
// status. Harness coverage is a statement about machinery. External
// qualification is a statement about a real environment, and only
// pkg/governance/qualification, holding real signed evidence from a
// registered provider, can make it.

import (
	"sort"
	"strings"
)

// CapabilityStatus is what a harness can honestly say about one named
// capability. Deliberately three values, none of which is "done".
type CapabilityStatus string

const (
	// CapabilityExercised means the harness really drives this
	// capability and asserts on the outcome.
	CapabilityExercised CapabilityStatus = "EXERCISED_BY_HARNESS"
	// CapabilityPartial means the harness touches it but does not
	// establish the full property the blocker's acceptance criterion
	// names. The Gap field says exactly what is short.
	CapabilityPartial CapabilityStatus = "PARTIALLY_EXERCISED"
	// CapabilityExternalOnly means no harness can exercise it: it needs
	// the real environment. Reported so it is visibly out of scope
	// rather than silently absent.
	CapabilityExternalOnly CapabilityStatus = "EXTERNAL_ENVIRONMENT_ONLY"
)

// Capability is one row of the register.
type Capability struct {
	GateID string           `json:"gate_id"`
	Name   string           `json:"name"`
	Status CapabilityStatus `json:"status"`
	// Package and Symbol name the real Go code that exercises this. A
	// row whose Symbol is not present in Package's source fails
	// TestEveryCapabilityCitesRealCode.
	Package string `json:"package"`
	Symbol  string `json:"symbol"`
	// Gap is mandatory for PARTIALLY_EXERCISED and EXTERNAL_ENVIRONMENT_ONLY:
	// a shortfall with no stated reason is not a report.
	Gap string `json:"gap,omitempty"`
}

// capabilityRegister is the audited inventory. Each entry was checked
// against the named package's source when written.
var capabilityRegister = []Capability{
	// --- scale_qualification ---------------------------------------
	{GateID: "scale_qualification", Name: "provision N nodes", Status: CapabilityExercised,
		Package: "pkg/blockers/scale", Symbol: "NodeProvider"},
	{GateID: "scale_qualification", Name: "submit and collect 1M evidence envelopes", Status: CapabilityExercised,
		Package: "pkg/blockers/scale", Symbol: "RunQualification"},
	{GateID: "scale_qualification", Name: "exactly-once delivery integrity (loss and duplication detection)",
		Status: CapabilityExercised, Package: "pkg/blockers/scale", Symbol: "FaultInjectingNodeProvider"},
	{GateID: "scale_qualification", Name: "execution on genuinely distinct physical infrastructure",
		Status: CapabilityExternalOnly, Package: "pkg/blockers/scale", Symbol: "NodeProvider",
		Gap: "the HTTP provider ran 100 real containers, but all on one physical host; multi-host/multi-datacenter " +
			"execution is an infrastructure-procurement action no harness can perform"},

	// --- multi_region_dr -------------------------------------------
	{GateID: "multi_region_dr", Name: "create regions", Status: CapabilityExercised,
		Package: "pkg/blockers/dr", Symbol: "RegionProvider"},
	{GateID: "multi_region_dr", Name: "partition/fail the leader region", Status: CapabilityExercised,
		Package: "pkg/blockers/dr", Symbol: "RunQualification"},
	{GateID: "multi_region_dr", Name: "measure RTO and RPO", Status: CapabilityExercised,
		Package: "pkg/blockers/dr", Symbol: "RunQualification"},
	{GateID: "multi_region_dr", Name: "failback: heal the partition and confirm convergence",
		Status: CapabilityExercised, Package: "pkg/blockers/dr", Symbol: "Heal"},
	{GateID: "multi_region_dr", Name: "real cross-datacenter WAN behaviour", Status: CapabilityExternalOnly,
		Package: "pkg/blockers/dr", Symbol: "RegionProvider",
		Gap: "an in-process or single-host transport cannot reproduce WAN latency, partial partitions or " +
			"traffic-manager cutover; real cloud regions are a procurement action"},

	// --- soak_72h ---------------------------------------------------
	{GateID: "soak_72h", Name: "long-running workload with hash-chained checkpoints",
		Status: CapabilityExercised, Package: "pkg/blockers/soak", Symbol: "RunQualification"},
	{GateID: "soak_72h", Name: "memory and CPU sampling", Status: CapabilityExercised,
		Package: "pkg/blockers/soak", Symbol: "ResourceUsage"},
	{GateID: "soak_72h", Name: "restart/resume across process restart", Status: CapabilityExercised,
		Package: "pkg/blockers/soak", Symbol: "LoadState"},
	{GateID: "soak_72h", Name: "run identity binding (host, binary, source)", Status: CapabilityExercised,
		Package: "pkg/blockers/soak", Symbol: "RunIdentity"},
	{GateID: "soak_72h", Name: "a genuinely continuous 72-hour window", Status: CapabilityExternalOnly,
		Package: "pkg/blockers/soak", Symbol: "RunQualificationAt",
		Gap: "the harness accepts any target duration; this environment's session lifecycle cannot honestly stay " +
			"up for 72 continuous hours, which is a property of the host, not of the harness"},

	// --- hsm_kms ----------------------------------------------------
	{GateID: "hsm_kms", Name: "sign and verify through a KeyProvider", Status: CapabilityExercised,
		Package: "pkg/blockers/hsmkms", Symbol: "RunFailureMatrix"},
	{GateID: "hsm_kms", Name: "failure matrix (unavailable, timeout, permission-denied, wrong-key)",
		Status: CapabilityExercised, Package: "pkg/blockers/hsmkms", Symbol: "FailingKeyProvider"},
	{GateID: "hsm_kms", Name: "revocation refuses before the provider is ever touched",
		Status: CapabilityExercised, Package: "pkg/blockers/hsmkms", Symbol: "RunFailureMatrix"},
	{GateID: "hsm_kms", Name: "key rotation without re-encryption", Status: CapabilityExercised,
		Package: "pkg/platform/security/keys", Symbol: "Manager"},
	{GateID: "hsm_kms", Name: "a real HSM or cloud-KMS tenancy", Status: CapabilityExternalOnly,
		Package: "pkg/blockers/hsmkms", Symbol: "FailingKeyProvider",
		Gap: "requires paid cloud credentials; freshly re-tested against real AWS KMS and rejected with " +
			"UnrecognizedClientException (evidence/hsm_kms-real-credential-retest.txt). Also constrained by the " +
			"zero_dependency gate, which rules out adding a cloud SDK"},

	// --- spire_mtls -------------------------------------------------
	{GateID: "spire_mtls", Name: "SVID issuance", Status: CapabilityExercised,
		Package: "pkg/blockers/spiffe", Symbol: "IdentityProvider"},
	{GateID: "spire_mtls", Name: "SVID rotation and watch", Status: CapabilityExercised,
		Package: "pkg/blockers/spiffe", Symbol: "WatchRotation"},
	{GateID: "spire_mtls", Name: "trust bundle verification", Status: CapabilityExercised,
		Package: "pkg/blockers/spiffe", Symbol: "TrustBundle"},
	{GateID: "spire_mtls", Name: "negative identity-binding matrix (expired, revoked, untrusted CA, mismatch)",
		Status: CapabilityExercised, Package: "pkg/blockers/spiffe", Symbol: "RunQualification"},
	{GateID: "spire_mtls", Name: "a production node attestor (cloud-instance-identity / k8s-PSAT / TPM)",
		Status: CapabilityExternalOnly, Package: "pkg/blockers/spiffe", Symbol: "IdentityProvider",
		Gap: "join_token is a test/demo attestor; a production attestor requires real cloud or TPM infrastructure"},

	// --- supply_chain_scan ------------------------------------------
	{GateID: "supply_chain_scan", Name: "dependency discovery", Status: CapabilityExercised,
		Package: "pkg/blockers/supplychain", Symbol: "DiscoverDependencies"},
	{GateID: "supply_chain_scan", Name: "vulnerability query pipeline and policy evaluation",
		Status: CapabilityExercised, Package: "pkg/blockers/supplychain", Symbol: "HTTPVulnerabilityProvider"},
	{GateID: "supply_chain_scan", Name: "a reachable vulnerability database", Status: CapabilityExternalOnly,
		Package: "pkg/blockers/supplychain", Symbol: "VulnerabilityProvider",
		Gap: "vuln.go.dev, osv.dev and the GitHub advisory API all return 403 under this environment's egress " +
			"policy (evidence/supply_chain_scan-vulndb-network-retest.txt) -- a policy denial, not a code gap"},

	// --- pentest ----------------------------------------------------
	{GateID: "pentest", Name: "adversarial probes against real production code", Status: CapabilityExercised,
		Package: "pkg/blockers/pentest", Symbol: "RunAll"},
	{GateID: "pentest", Name: "release-identity preflight", Status: CapabilityExercised,
		Package: "pkg/blockers/pentest", Symbol: "Preflight"},
	{GateID: "pentest", Name: "an independent security vendor's signed report", Status: CapabilityExternalOnly,
		Package: "pkg/blockers/pentest", Symbol: "RunQualification",
		Gap: "independence is the requirement; no self-run probe can satisfy it by construction"},

	// --- live_data --------------------------------------------------
	{GateID: "live_data", Name: "feed connector lifecycle and content-hash dedup", Status: CapabilityExercised,
		Package: "pkg/blockers/livedata", Symbol: "FeedConnector"},
	{GateID: "live_data", Name: "anti-replay defence", Status: CapabilityExercised,
		Package: "pkg/blockers/livedata", Symbol: "RunQualification"},
	{GateID: "live_data", Name: "refusal of a SIMULATED connector's record tagged LIVE",
		Status: CapabilityExercised, Package: "pkg/blockers/livedata", Symbol: "DataMode"},
	{GateID: "live_data", Name: "contracted commercial feeds", Status: CapabilityExternalOnly,
		Package: "pkg/blockers/livedata", Symbol: "FeedConnector",
		Gap: "requires commercial data contracts with AIS/BoL/SAR/trade providers -- a procurement and legal " +
			"action, and one explicitly excluded from this programme's scope"},
}

// CapabilityRegister returns the audited inventory, deterministically
// ordered by gate then capability name.
func CapabilityRegister() []Capability {
	out := append([]Capability(nil), capabilityRegister...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].GateID != out[j].GateID {
			return out[i].GateID < out[j].GateID
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// CapabilitySummary is the per-gate roll-up an operator reads first.
type CapabilitySummary struct {
	GateID       string `json:"gate_id"`
	Exercised    int    `json:"exercised"`
	Partial      int    `json:"partial"`
	ExternalOnly int    `json:"external_only"`
	Total        int    `json:"total"`
	// HarnessComplete means every capability a harness COULD cover is
	// covered. It says nothing whatsoever about the gate's status: a
	// gate can be harness-complete and permanently BLOCKED, which is
	// exactly the situation all eight are in.
	HarnessComplete bool `json:"harness_complete"`
	// StillExternal names what no harness can ever supply.
	StillExternal []string `json:"still_external,omitempty"`
}

// Summarize rolls the register up per gate.
func Summarize() []CapabilitySummary {
	byGate := map[string]*CapabilitySummary{}
	var order []string
	for _, c := range CapabilityRegister() {
		s, ok := byGate[c.GateID]
		if !ok {
			s = &CapabilitySummary{GateID: c.GateID, HarnessComplete: true}
			byGate[c.GateID] = s
			order = append(order, c.GateID)
		}
		s.Total++
		switch c.Status {
		case CapabilityExercised:
			s.Exercised++
		case CapabilityPartial:
			s.Partial++
			s.HarnessComplete = false
		case CapabilityExternalOnly:
			s.ExternalOnly++
			s.StillExternal = append(s.StillExternal, c.Name)
		}
	}
	sort.Strings(order)
	out := make([]CapabilitySummary, 0, len(order))
	for _, g := range order {
		out = append(out, *byGate[g])
	}
	return out
}

// HarnessCanNeverQualify states the invariant this whole file exists to
// keep visible. It is a function rather than a comment so a test can
// assert on it, and it takes no arguments because there is no input
// that changes the answer.
//
// Harness coverage is a statement about machinery. External
// qualification is a statement about a real environment. Only
// pkg/governance/qualification, holding real signed evidence from a
// registered provider bound to this exact release, can make the second
// one — and nothing in this package can call it.
func HarnessCanNeverQualify() string {
	return "a harness exercises machinery; it never qualifies a gate. Advancing any of the eight blockers " +
		"requires real external evidence validated by pkg/governance/qualification against a registered " +
		"provider and reviewer, bound to this exact release. No value in this package participates in that."
}

// GateIDsWithCapabilities returns every gate the register covers.
func GateIDsWithCapabilities() []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range capabilityRegister {
		if !seen[c.GateID] {
			seen[c.GateID] = true
			out = append(out, c.GateID)
		}
	}
	sort.Strings(out)
	return out
}

// Validate checks the register's own internal consistency: every row
// names a gate, a capability, a package and a symbol, and every
// non-EXERCISED row states its gap.
func ValidateRegister() []string {
	var problems []string
	for _, c := range capabilityRegister {
		switch {
		case strings.TrimSpace(c.GateID) == "":
			problems = append(problems, "a capability row has no gate id")
		case strings.TrimSpace(c.Name) == "":
			problems = append(problems, c.GateID+": a capability row has no name")
		case strings.TrimSpace(c.Package) == "":
			problems = append(problems, c.GateID+"/"+c.Name+": no package named")
		case strings.TrimSpace(c.Symbol) == "":
			problems = append(problems, c.GateID+"/"+c.Name+": no symbol named")
		}
		switch c.Status {
		case CapabilityExercised:
		case CapabilityPartial, CapabilityExternalOnly:
			if strings.TrimSpace(c.Gap) == "" {
				problems = append(problems,
					c.GateID+"/"+c.Name+": status "+string(c.Status)+" with no stated gap")
			}
		default:
			problems = append(problems, c.GateID+"/"+c.Name+": unknown status "+string(c.Status))
		}
	}
	sort.Strings(problems)
	return problems
}
