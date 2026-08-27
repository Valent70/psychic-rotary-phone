package casepack

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"veriqo/pkg/evidence/provenance"
	"veriqo/pkg/governance/envelope"
)

func TestAllSevenCasesExistAndValidate(t *testing.T) {
	all := All()
	if len(all) != 7 {
		t.Fatalf("the Final Design mandates seven synthetic cases, got %d", len(all))
	}
	seen := map[CaseID]bool{}
	for _, c := range all {
		if err := c.Validate(); err != nil {
			t.Fatalf("%s: Validate: %v", c.ID, err)
		}
		if seen[c.ID] {
			t.Fatalf("duplicate case ID %s", c.ID)
		}
		seen[c.ID] = true
	}
	for _, id := range AllCaseIDs() {
		if !seen[id] {
			t.Fatalf("case %s is declared in AllCaseIDs but not built by All()", id)
		}
	}
}

func TestGetReturnsEachCaseAndRefusesUnknown(t *testing.T) {
	for _, id := range AllCaseIDs() {
		c, err := Get(id)
		if err != nil {
			t.Fatalf("Get(%s): %v", id, err)
		}
		if c.ID != id {
			t.Fatalf("Get(%s) returned %s", id, c.ID)
		}
	}
	if _, err := Get("CASE-INS-999"); err == nil {
		t.Fatal("Get must refuse an unknown case ID")
	}
}

// TestEveryCaseDeclaresItsEngineeringPurpose: the Final Design is
// explicit that these cases exist for engineering coverage, not for
// show. A case that does not say what it exercises has no purpose.
func TestEveryCaseDeclaresItsEngineeringPurpose(t *testing.T) {
	for _, c := range All() {
		if len(c.EngineeringCoverage) < 3 {
			t.Fatalf("%s declares only %d coverage items; a synthetic case exists to exercise real paths",
				c.ID, len(c.EngineeringCoverage))
		}
		if len(c.ExpectedOutputs) == 0 {
			t.Fatalf("%s declares no expected outputs", c.ID)
		}
		if strings.TrimSpace(c.Narrative) == "" {
			t.Fatalf("%s has no narrative", c.ID)
		}
	}
}

// ================= The naming constraint ==============================

// realWorldTokens are names that must never appear in this package's
// source. They are drawn from the two design documents' own worked
// examples of what NOT to do — the Final Design §39 forbids
// hard-coding a named AIS vendor, a named real judgment as a rule, and
// a named real company as a bribery classifier target — plus the
// obvious general categories.
//
// The check is deliberately over this package's SOURCE rather than over
// the built Case values: a real name in a comment would be just as much
// a problem as one in a string, and a future author adding a "for
// example, the X case" note is exactly the drift this guards against.
var realWorldTokens = []string{
	// Named AIS / vessel-tracking vendors the design document forbids
	// hard-coding.
	"marinetraffic", "orbcomm", "vesselfinder", "fleetmon", "spire global",
	"exactearth", "windward", "kpler", "lloyd's list", "lloyds list", "ihs markit",
	// The real judgment the design document names as a thing not to
	// hard-code.
	"the polar", "polar judgment", "ukhl", "ewhc", "ewca", "[2021]", "[2020]", "[2022]",
	// The real company the design document names as a thing not to use
	// as a classifier target, plus the obvious peers, so no real trader
	// is named.
	"glencore", "trafigura", "vitol", "gunvor", "cargill", "louis dreyfus",
	"bunge", "adm ", "mercuria", "olam",
	// Real regulators and enforcement bodies.
	"sfo ", "serious fraud office", "doj ", "department of justice",
	"sec ", "ofac", "fca ", "financial conduct authority", "cftc", "finma",
	// Real carriers / operators.
	"maersk", "msc ", "cma cgm", "hapag", "cosco", "evergreen marine",
	"one line", "zim ", "yang ming", "hmm ",
	// Real P&I clubs and insurance markets.
	"gard ", "skuld", "north of england", "steamship mutual", "britannia p&i",
	"lloyd's of london", "lloyds of london",
}

// TestNoRealWorldEntityAppearsInThePack scans this package's own source
// for any real, identifiable company, vessel, vendor, regulator or
// reported judgment. Every name in the pack must be invented.
func TestNoRealWorldEntityAppearsInThePack(t *testing.T) {
	var offenders []string
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		// The test file itself contains the forbidden list by necessity.
		if strings.HasSuffix(path, "casepack_test.go") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		lower := strings.ToLower(string(b))
		for _, tok := range realWorldTokens {
			if strings.Contains(lower, tok) {
				offenders = append(offenders, path+": contains "+tok)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scanning the case pack: %v", err)
	}
	if len(offenders) > 0 {
		t.Fatalf("the synthetic case pack names real-world entities; every name must be invented:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// TestJurisdictionsAreGeneric: the naming constraint requires
// jurisdictions to be generic or invented, never a real named country
// asserted to have a particular law.
func TestJurisdictionsAreGeneric(t *testing.T) {
	for _, c := range All() {
		blob := strings.ToLower(c.Narrative + " " + c.Title)
		for _, real := range []string{
			"united kingdom", "england and wales", "singapore high court",
			"new york law", "hong kong sar", "netherlands", "switzerland",
			"united states", "greece", "japan", "china", "panama", "liberia", "marshall islands",
		} {
			if strings.Contains(blob, real) {
				t.Fatalf("%s names the real jurisdiction %q; jurisdictions must be generic "+
					"(\"Jurisdiction A\") or invented", c.ID, real)
			}
		}
	}
}

// TestNoCaseOutputIsADetermination: every expected output is a status,
// a required action or an explicit refusal to determine — never a
// finding about anyone.
func TestNoCaseOutputIsADetermination(t *testing.T) {
	forbidden := []string{
		"is liable", "was negligent", "committed", "is guilty", "is at fault",
		"caused the", "bribery detected", "fraud detected", "claim denied",
		"coverage denied", "is responsible for",
	}
	for _, c := range All() {
		for _, out := range c.ExpectedOutputs {
			lower := strings.ToLower(out)
			for _, bad := range forbidden {
				if strings.Contains(lower, bad) {
					t.Fatalf("%s expects output %q, which is a determination about a party", c.ID, out)
				}
			}
		}
	}
}

// ================= Synthetic is labelled synthetic ====================

// TestAFixtureCaseCanNeverReportAsLive is the Final Design §39 rule
// ("mencampur synthetic dengan live data", "menyebut historical replay
// sebagai live") enforced structurally.
func TestAFixtureCaseCanNeverReportAsLive(t *testing.T) {
	if Origin != provenance.OriginSynthetic {
		t.Fatalf("the pack's Origin is %q, want SYNTHETIC", Origin)
	}
	if Classification != envelope.ClassificationFixture {
		t.Fatalf("the pack's Classification is %q, want FIXTURE", Classification)
	}

	// The Case type must carry no field through which a case could claim
	// live provenance or a different classification.
	ct := reflect.TypeOf(Case{})
	for i := 0; i < ct.NumField(); i++ {
		name := strings.ToLower(ct.Field(i).Name)
		for _, bad := range []string{"live", "origin", "classification", "provenance", "rights"} {
			if strings.Contains(name, bad) {
				t.Fatalf("Case has field %q — a synthetic case's provenance is a package constant, "+
					"never a per-case field that could claim otherwise", ct.Field(i).Name)
			}
		}
	}

	// And every case's envelope really is a fixture with declared
	// limitations.
	for _, c := range All() {
		env := c.FixtureEnvelope("v1.0.0", "abcdef1",
			strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64), 1, 10_000)
		if err := env.Validate(); err != nil {
			t.Fatalf("%s: the fixture envelope must validate: %v", c.ID, err)
		}
		if !env.IsFixture() {
			t.Fatalf("%s: the envelope must be a FIXTURE", c.ID)
		}
		if len(env.Limitations) == 0 {
			t.Fatalf("%s: a fixture envelope with no declared limitations is exactly what this "+
				"project exists not to produce", c.ID)
		}
	}
}

// TestEverySyntheticRecordCarriesItsOriginInItsContentHash: a record's
// synthetic provenance cannot be separated from the record, because it
// is part of what the content hash is over.
func TestEverySyntheticRecordCarriesItsOriginInItsContentHash(t *testing.T) {
	for _, c := range All() {
		built, err := c.BuildAllEvidence()
		if err != nil {
			t.Fatalf("%s: BuildAllEvidence: %v", c.ID, err)
		}
		for _, rec := range built.Records {
			if rec.Underlying.Attributes["origin_class"] != string(Origin) {
				t.Fatalf("%s: record %s does not carry its synthetic origin", c.ID, rec.EvidenceID())
			}
			if rec.Underlying.Attributes["synthetic_case"] != string(c.ID) {
				t.Fatalf("%s: record %s does not carry its case identity", c.ID, rec.EvidenceID())
			}
			// The attributes are part of the content hash, so removing
			// them would produce a different EvidenceID.
			stripped := rec.Underlying
			stripped.Attributes = map[string]string{}
			if stripped.ComputeID() == rec.Underlying.EvidenceID {
				t.Fatalf("%s: record %s's origin marker is not covered by its content hash",
					c.ID, rec.EvidenceID())
			}
		}
	}
}

// ================= Determinism =========================================

// TestCasesAreDeterministicFixtures: rebuilding a case reproduces
// byte-identical content-addressed EvidenceIDs. Without this the pack
// is not a fixture, it is a random generator.
func TestCasesAreDeterministicFixtures(t *testing.T) {
	for _, id := range AllCaseIDs() {
		a, err := Get(id)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		b, err := Get(id)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if a.ContentHash() != b.ContentHash() {
			t.Fatalf("%s: two builds produced different content hashes", id)
		}
		ba, err := a.BuildAllEvidence()
		if err != nil {
			t.Fatalf("%s: BuildAllEvidence: %v", id, err)
		}
		bb, err := b.BuildAllEvidence()
		if err != nil {
			t.Fatalf("%s: BuildAllEvidence: %v", id, err)
		}
		if len(ba.Records) != len(bb.Records) {
			t.Fatalf("%s: record counts differ", id)
		}
		for i := range ba.Records {
			if ba.Records[i].EvidenceID() != bb.Records[i].EvidenceID() {
				t.Fatalf("%s: record %d has a non-deterministic EvidenceID", id, i)
			}
		}
	}
}

// TestEachCaseHasDistinctEvidence: a shared record between two cases
// would mean one case's evidence silently appearing in another's
// manifest.
func TestEachCaseHasDistinctEvidence(t *testing.T) {
	owner := map[string]CaseID{}
	for _, c := range All() {
		built, err := c.BuildAllEvidence()
		if err != nil {
			t.Fatalf("%s: %v", c.ID, err)
		}
		for _, rec := range built.Records {
			id := rec.EvidenceID()
			if prev, exists := owner[id]; exists {
				t.Fatalf("evidence %s appears in both %s and %s", id, prev, c.ID)
			}
			owner[id] = c.ID
		}
	}
}

func TestContentHashChangesWithContent(t *testing.T) {
	c, err := Get(CasePortCallDemurrage)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	before := c.ContentHash()
	c.Evidence[0].Attributes["stated_time"] = "09:00"
	if c.ContentHash() == before {
		t.Fatal("editing a case's evidence must change its content hash")
	}
}

// ================= Case-specific expectations ==========================

// TestCase004ExpectsOnlyLegalInterpretationRequired: the §34 rule.
func TestCase004ExpectsOnlyLegalInterpretationRequired(t *testing.T) {
	c, err := Get(CaseGeneralAverage)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	joined := strings.Join(c.ExpectedOutputs, " | ")
	if !strings.Contains(joined, "LEGAL_INTERPRETATION_REQUIRED") {
		t.Fatalf("CASE-INS-004 must expect LEGAL_INTERPRETATION_REQUIRED, got %v", c.ExpectedOutputs)
	}
	if !strings.Contains(joined, "NO_VERIQO_LEGAL_DETERMINATION") {
		t.Fatalf("CASE-INS-004 must explicitly expect no legal determination, got %v", c.ExpectedOutputs)
	}
}

// TestCase005ExpectsTheExactSection35Output: the §35 required output,
// verbatim in substance.
func TestCase005ExpectsTheExactSection35Output(t *testing.T) {
	c, err := Get(CaseBriberyRisk)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	joined := strings.Join(c.ExpectedOutputs, " | ")
	for _, want := range []string{"HIGH_RISK_TRANSACTION", "EDD_REQUIRED", "NO_BRIBERY_DETERMINATION"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("CASE-INS-005 must expect %q, got %v", want, c.ExpectedOutputs)
		}
	}
	// Four independent red flags, each with its own evidence.
	flags := map[string]bool{}
	for _, e := range c.Evidence {
		if rf, ok := e.Attributes["red_flag"]; ok {
			flags[rf] = true
		}
	}
	if len(flags) != 4 {
		t.Fatalf("CASE-INS-005 must carry exactly four independently-evidenced red flags, got %d", len(flags))
	}
	// And the case names no real party at all — every participant is
	// role-designated or invented.
	for _, p := range c.Parties {
		if strings.TrimSpace(p.Name) == "" {
			t.Fatalf("party %s has no name", p.ID)
		}
	}
}

// TestCase006ExpectsTheTwoCriticalSeparations: §36's own two rules.
func TestCase006ExpectsTheTwoCriticalSeparations(t *testing.T) {
	c, err := Get(CaseRegulatorySettlement)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	joined := strings.Join(c.ExpectedOutputs, " | ")
	if !strings.Contains(joined, "PROVEN_ALLEGATIONS: none by settlement") {
		t.Fatalf("CASE-INS-006 must expect that a settlement proves nothing, got %v", c.ExpectedOutputs)
	}
	if !strings.Contains(joined, "MONITOR_REQUIRED_NOT_CERTIFIED_COMPLETE") {
		t.Fatalf("CASE-INS-006 must expect monitor-required != monitor-completed, got %v", c.ExpectedOutputs)
	}
}

// TestCase003ExpectsInconsistencyNotFraud: §31's own rule.
func TestCase003ExpectsInconsistencyNotFraud(t *testing.T) {
	c, err := Get(CaseCommodityDocuments)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	joined := strings.Join(c.ExpectedOutputs, " | ")
	if !strings.Contains(joined, "DOCUMENT_INCONSISTENCY") {
		t.Fatalf("CASE-INS-003 must expect DOCUMENT_INCONSISTENCY, got %v", c.ExpectedOutputs)
	}
	if !strings.Contains(joined, "NO_FRAUD_DETERMINATION") {
		t.Fatalf("CASE-INS-003 must explicitly expect no fraud determination, got %v", c.ExpectedOutputs)
	}
	// One restricted document exercises the rights gate.
	restricted := 0
	for _, e := range c.Evidence {
		if e.Rights == provenance.RightsInternalOnly {
			restricted++
		}
	}
	if restricted == 0 {
		t.Fatal("CASE-INS-003 must carry at least one restricted document to exercise the rights gate")
	}
}

// TestEvidenceTypesAreDeclaredForPreservation: a preservation order
// over a case must be able to declare what it covers.
func TestEvidenceTypesAreDeclaredForPreservation(t *testing.T) {
	for _, c := range All() {
		types := c.EvidenceTypes()
		if len(types) == 0 {
			t.Fatalf("%s declares no evidence document types", c.ID)
		}
		for i := 1; i < len(types); i++ {
			if types[i-1] >= types[i] {
				t.Fatalf("%s: EvidenceTypes must be sorted and distinct, got %v", c.ID, types)
			}
		}
	}
}

func TestBuiltEvidenceLookupHelpers(t *testing.T) {
	c, err := Get(CasePortCallDemurrage)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	built, err := c.BuildAllEvidence()
	if err != nil {
		t.Fatalf("BuildAllEvidence: %v", err)
	}
	if built.ID("NOR") == "" {
		t.Fatal("ID(NOR) returned empty")
	}
	ids := built.IDs("NOR", "AIS_ARRIVAL")
	if len(ids) != 2 || ids[0] == ids[1] {
		t.Fatalf("IDs returned %v", ids)
	}
}

func TestValidateCatchesMalformedDescriptors(t *testing.T) {
	c, _ := Get(CasePortCallDemurrage)
	c.Evidence = append(c.Evidence, c.Evidence[0])
	if err := c.Validate(); err == nil {
		t.Fatal("a duplicate evidence key must be refused")
	}
	c2, _ := Get(CasePortCallDemurrage)
	c2.Parties = nil
	if err := c2.Validate(); err == nil {
		t.Fatal("a case with no parties must be refused")
	}
	c3, _ := Get(CasePortCallDemurrage)
	c3.ExpectedOutputs = nil
	if err := c3.Validate(); err == nil {
		t.Fatal("a case with no expected outputs must be refused")
	}
}
