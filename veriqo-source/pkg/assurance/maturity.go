package assurance

import (
	"fmt"
	"sort"
	"strings"
)

// The VERIQO maturity model — permanent law.
//
// A reviewer set this out after reading a round's artefacts rather than
// its report, and the reason it belongs in code rather than in a
// document is the chain it exists to break:
//
//	code exists -> test passes -> marketing says verified
//	 -> investor hears certified
//
// Each arrow in that chain is a small, defensible step, and the end of
// it is a lie nobody told. The model stops it by making the levels
// ordered, un-skippable, and honest about which of them VERIQO can
// reach on its own.
//
//	L0  DESIGNED
//	L1  IMPLEMENTED
//	L2  UNIT VERIFIED
//	L3  INTEGRATION VERIFIED     <- everything internal tops out here
//	L4  REAL-DATA VALIDATED
//	L5  INDEPENDENTLY ASSURED
//	L6  EXTERNALLY QUALIFIED
//	L7  PRODUCTION QUALIFIED
//
// L0-L3 are reachable by writing code. L4-L7 are not reachable by
// writing any amount of code, and RequiresOutsideParty says so per
// level rather than in a caveat somebody can drop.

// Maturity is a level in the model.
//
// L0Designed is the zero value: a capability nobody has classified is
// designed at best, never assumed further along.
type Maturity int

const (
	L0Designed Maturity = iota
	L1Implemented
	L2UnitVerified
	L3IntegrationVerified
	L4RealDataValidated
	L5IndependentlyAssured
	L6ExternallyQualified
	L7ProductionQualified
)

var maturityNames = map[Maturity]string{
	L0Designed: "L0_DESIGNED", L1Implemented: "L1_IMPLEMENTED",
	L2UnitVerified: "L2_UNIT_VERIFIED", L3IntegrationVerified: "L3_INTEGRATION_VERIFIED",
	L4RealDataValidated: "L4_REAL_DATA_VALIDATED", L5IndependentlyAssured: "L5_INDEPENDENTLY_ASSURED",
	L6ExternallyQualified: "L6_EXTERNALLY_QUALIFIED", L7ProductionQualified: "L7_PRODUCTION_QUALIFIED",
}

func (m Maturity) String() string {
	if s, ok := maturityNames[m]; ok {
		return s
	}
	return "L0_DESIGNED"
}

// MaturityLevels returns every level in order.
func MaturityLevels() []Maturity {
	return []Maturity{
		L0Designed, L1Implemented, L2UnitVerified, L3IntegrationVerified,
		L4RealDataValidated, L5IndependentlyAssured, L6ExternallyQualified, L7ProductionQualified,
	}
}

// RequiresOutsideParty reports whether reaching this level needs
// somebody who is not VERIQO. Everything from L4 up does.
//
// L4 is included deliberately. Real data is not VERIQO's to produce: it
// arrives under a commercial agreement with somebody who owns it, and
// treating it as an engineering task is how "we tested it thoroughly"
// becomes "it works in the field".
func (m Maturity) RequiresOutsideParty() bool { return m >= L4RealDataValidated }

// InternalCeiling is the highest level reachable without anybody
// outside VERIQO.
func InternalCeiling() Maturity { return L3IntegrationVerified }

// Requires returns the level that must be reached before this one.
// L0 requires nothing.
func (m Maturity) Requires() (Maturity, bool) {
	if m <= L0Designed {
		return L0Designed, false
	}
	return m - 1, true
}

// EvidenceFor states what it takes to claim a level. A level whose
// evidence a reader cannot check is a level anybody can claim.
func EvidenceFor(m Maturity) string {
	switch m {
	case L1Implemented:
		return "the capability exists in code and builds"
	case L2UnitVerified:
		return "its own tests exercise it, including the refusals"
	case L3IntegrationVerified:
		return "it composes with the rest of the system in an executable end-to-end proof, on fixtures"
	case L4RealDataValidated:
		return "it has run on real rights-aware commercial data under a real data agreement"
	case L5IndependentlyAssured:
		return "a named assessor who is not VERIQO examined it and stated the procedure was correctly applied"
	case L6ExternallyQualified:
		return "an accrediting or qualifying body has qualified it against a published standard"
	case L7ProductionQualified:
		return "it has operated in production, under real load, with real consequences, and the record survives audit"
	default:
		return "the capability is specified; nothing about its behaviour follows"
	}
}

var (
	// ErrLevelSkipped is returned when a claim jumps a level.
	ErrLevelSkipped = fmt.Errorf("assurance: a capability may not skip a maturity level")
)

// MaturityClaim is one capability's claimed level and the evidence for
// it.
type MaturityClaim struct {
	Capability string
	Level      Maturity
	// EvidenceRef is what a reader follows to check the claim.
	EvidenceRef string
	// Blocker names what stands between this level and the next.
	// Required below L7 — a capability that names no next step is either
	// finished or unexamined, and only one of those is possible here.
	Blocker string
}

// Validate refuses a claim that cannot be checked.
//
// The level-skipping rule is enforced through the claim's own evidence:
// a claim at L5 must reference the assurance that L5 means, and a claim
// at L4 or above must name the outside party involved. It cannot check
// that the LOWER levels were passed — no data structure can — which is
// why ValidateMaturityLadder checks the ladder as a whole instead.
func (c MaturityClaim) Validate() error {
	if strings.TrimSpace(c.Capability) == "" {
		return fmt.Errorf("assurance: a maturity claim must name its capability")
	}
	if c.Level > L0Designed && strings.TrimSpace(c.EvidenceRef) == "" {
		return fmt.Errorf("assurance: %q claims %s with no evidence reference", c.Capability, c.Level)
	}
	if c.Level < L7ProductionQualified && strings.TrimSpace(c.Blocker) == "" {
		return fmt.Errorf("assurance: %q claims %s and names nothing standing in the way of %s",
			c.Capability, c.Level, c.Level+1)
	}
	return nil
}

// maturityClaims is VERIQO's position, per capability.
//
// Read the Level column down: nothing is above L3. That is not modesty
// and not a placeholder — L4 upward each require an outside party, and
// no outside party has been engaged.
var maturityClaims = []MaturityClaim{
	{"Evidence Constitution (30 executable articles)", L3IntegrationVerified,
		"pkg/constitution + test/adversarial; cited in the constitutional proof audit",
		"L4 needs the article checks run against real acquisition, not fixtures (LIVE_DATA)"},
	{"Epistemic Qualification Fabric", L3IntegrationVerified,
		"pkg/qualification/*; exercised in the six-domain integration proof",
		"L4 needs real sources, where UNKNOWN and DEPENDENT verdicts actually arise"},
	{"Proof Object and pipeline", L3IntegrationVerified,
		"pkg/proof; the chain runs for all six domains",
		"L5 needs an assessor to examine the qualification procedure (INDEPENDENT_ASSURANCE)"},
	{"Case Resolution Fabric", L3IntegrationVerified,
		"pkg/casefabric; nine phase contracts, six domain projections",
		"L4 needs a real matter carried end to end"},
	{"Forward-Reverse Execution Fabric", L3IntegrationVerified,
		"pkg/fref; the coupled sequence gates resolution in every domain",
		"L4 needs a production execution to verify against, not a constructed one"},
	{"Trust and Evidence Control Plane", L3IntegrationVerified,
		"pkg/evidence/manifest, pkg/platform/audit; replay-verified",
		"L5 needs custody and ledger integrity audited by an outside party"},
	{"Disclosure two-dimensional model", L3IntegrationVerified,
		"pkg/disclosure/access; projections tested in the case proof graph",
		"L5 needs counsel to confirm the model matches disclosure practice"},
	{"AI Evidence Gateway", L3IntegrationVerified,
		"pkg/ai/gateway; ten check dimensions, forbidden actions refused first",
		"L5 needs an AI-governance auditor"},
	{"Case Proof Graph", L3IntegrationVerified,
		"pkg/caseproofgraph; built, verified and projected in all six domains",
		"L4 needs a real case; L5 needs a tribunal to say whether the graph is useful"},
	{"Constitutional sequencing law", L3IntegrationVerified,
		"pkg/fref sequence + the runtime event order verifier",
		"L4 needs a production event stream to verify"},
	{"Semantic authority audit", L2UnitVerified,
		"pkg/assurance/authority.go; one authority per decision, asserted",
		"L3 needs the audit checked against a running system rather than the source"},
	{"Temporal attestation distinction", L2UnitVerified,
		"pkg/platform/timestamp; chain and TSA are different types",
		"L4 needs a real RFC 3161 token (EXTERNAL_TSA)"},
	{"Redaction evidence chain", L2UnitVerified,
		"pkg/evidence/redaction; byte-level absence verified over the derivative",
		"L3 needs the PDF/XLSX/PPTX workers that produce real derivatives (Article 18 remains OPEN)"},
	{"Payment settlement", L1Implemented,
		"pkg/insurance/payment; lifecycle and reconciliation modelled",
		"L2+ needs a bank rail; reconciliation has never met a real confirmation"},
	{"External source acquisition (IEAP)", L0Designed,
		"docs/architecture/REAL_WORLD_EVIDENCE_GAP.md; 35 evidence classes declared",
		"L1 needs real source agreements and licences (LIVE_DATA)"},
	{"Zero-knowledge proofs", L0Designed,
		"Article 9, recorded OPEN in the constitutional proof audit",
		"L1 needs a prover and a verifier; neither exists"},
	{"Operational neutrality (Article 15)", L0Designed,
		"Article 15, recorded OPEN; a commercial-structure commitment",
		"verifiable only by examining VERIQO's contracts and remuneration"},
}

// MaturityClaims returns VERIQO's position.
func MaturityClaims() []MaturityClaim { return append([]MaturityClaim(nil), maturityClaims...) }

// ValidateMaturityLadder checks the model itself and every claim
// against it.
func ValidateMaturityLadder() error {
	// The ladder must be contiguous: every level but L0 has exactly one
	// predecessor, and no level is unreachable.
	for _, m := range MaturityLevels() {
		prev, has := m.Requires()
		if m == L0Designed {
			if has {
				return fmt.Errorf("assurance: L0 must require nothing")
			}
			continue
		}
		if !has || prev != m-1 {
			return fmt.Errorf("assurance: level %s does not follow %s", m, m-1)
		}
		if strings.TrimSpace(EvidenceFor(m)) == "" {
			return fmt.Errorf("assurance: level %s states no evidence requirement", m)
		}
	}

	seen := map[string]bool{}
	for _, c := range maturityClaims {
		if seen[c.Capability] {
			return fmt.Errorf("assurance: capability %q claims a level twice", c.Capability)
		}
		seen[c.Capability] = true
		if err := c.Validate(); err != nil {
			return err
		}
		if c.Level.RequiresOutsideParty() {
			return fmt.Errorf("assurance: %q claims %s, which requires an outside party; none has been engaged",
				c.Capability, c.Level)
		}
	}
	return nil
}

// HighestClaimed returns the highest level any capability claims.
func HighestClaimed() Maturity {
	highest := L0Designed
	for _, c := range maturityClaims {
		if c.Level > highest {
			highest = c.Level
		}
	}
	return highest
}

// MaturityReport renders the ladder and VERIQO's position on it.
func MaturityReport() string {
	var b strings.Builder
	b.WriteString("VERIQO maturity model — no level may be skipped\n\n")
	for _, m := range MaturityLevels() {
		marker := "  "
		if m == InternalCeiling() {
			marker = "<-"
		}
		outside := ""
		if m.RequiresOutsideParty() {
			outside = "  [requires an outside party]"
		}
		b.WriteString(fmt.Sprintf("  %-26s %s %s%s\n", m, marker, EvidenceFor(m), outside))
	}

	byLevel := map[Maturity][]string{}
	for _, c := range maturityClaims {
		byLevel[c.Level] = append(byLevel[c.Level], c.Capability)
	}
	b.WriteString("\nVERIQO's position:\n\n")
	for _, m := range MaturityLevels() {
		caps := byLevel[m]
		if len(caps) == 0 {
			continue
		}
		sort.Strings(caps)
		b.WriteString(fmt.Sprintf("  %-26s %d capability(ies)\n", m, len(caps)))
		for _, c := range caps {
			b.WriteString("      " + c + "\n")
		}
	}
	b.WriteString(fmt.Sprintf("\nHighest level claimed: %s. The internal ceiling is %s.\n",
		HighestClaimed(), InternalCeiling()))
	b.WriteString("Nothing claims L4 or above, because every level from L4 up requires\n")
	b.WriteString("somebody who is not VERIQO, and none has been engaged.\n")
	return b.String()
}
