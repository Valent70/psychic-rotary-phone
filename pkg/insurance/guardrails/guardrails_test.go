package guardrails

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// insuranceRoot is the tree every test here scans.
const insuranceRoot = ".."

// field is one exported field on one exported type, with enough context
// for a failure message to be actionable.
type field struct {
	Path    string
	Type    string
	Name    string
	GoType  string
	IsFloat bool
}

// exportedFields parses every non-test Go file under pkg/insurance and
// returns every exported field of every exported struct type.
func exportedFields(t *testing.T) []field {
	t.Helper()
	var out []field
	fset := token.NewFileSet()

	err := filepath.WalkDir(insuranceRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		ast.Inspect(f, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok || !ts.Name.IsExported() {
				return true
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok || st.Fields == nil {
				return true
			}
			for _, fld := range st.Fields.List {
				goType := typeString(fld.Type)
				for _, name := range fld.Names {
					if !name.IsExported() {
						continue
					}
					out = append(out, field{
						Path: path, Type: ts.Name.Name, Name: name.Name,
						GoType: goType, IsFloat: goType == "float64" || goType == "float32",
					})
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("scanning %s: %v", insuranceRoot, err)
	}
	if len(out) < 100 {
		t.Fatalf("the scan found only %d exported fields across pkg/insurance — it is not actually "+
			"reaching the tree, so every assertion below would pass vacuously", len(out))
	}
	return out
}

func typeString(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + typeString(t.X)
	case *ast.SelectorExpr:
		return typeString(t.X) + "." + t.Sel.Name
	case *ast.ArrayType:
		return "[]" + typeString(t.Elt)
	case *ast.MapType:
		return "map[" + typeString(t.Key) + "]" + typeString(t.Value)
	default:
		return "?"
	}
}

// ---- Rule 1: no determination field anywhere ------------------------

// verdictTokens are substrings that, in a field name, would mean the
// insurance domain had acquired somewhere to record a determination it
// must never make.
var verdictTokens = []string{
	"verdict", "liable", "liability", "guilt", "guilty", "atfault",
	"approved", "denied", "denial", "payable", "settlementamount",
	"iscovered", "coveragedecision", "coverageresult", "finaldecision",
	"fraudulent", "isfraud", "bribery",
}

// verdictAllowlist is deliberately tiny. Each entry is a field whose
// name contains a flagged substring but whose whole purpose is to state
// that the determination is NOT made.
var verdictAllowlist = map[string]string{
	// obligation.Assessment.CoverageEffect's type has exactly one value:
	// NOT_DETERMINED_REQUIRES_POLICY_AND_LEGAL_REVIEW. The field exists
	// precisely so a notice assessment must say it determines nothing.
	"obligation.Assessment.CoverageEffect": "one-valued type whose only value is NOT_DETERMINED",
}

func TestNoDeterminationFieldAnywhereInTheInsuranceDomain(t *testing.T) {
	var offenders []string
	for _, f := range exportedFields(t) {
		key := shortPkg(f.Path) + "." + f.Type + "." + f.Name
		if _, allowed := verdictAllowlist[key]; allowed {
			continue
		}
		lower := strings.ToLower(f.Name)
		for _, tok := range verdictTokens {
			if strings.Contains(lower, tok) {
				offenders = append(offenders, f.Path+": "+f.Type+"."+f.Name+" (contains "+tok+")")
				break
			}
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("the insurance domain has acquired somewhere to record a determination it must never "+
			"make:\n  %s", strings.Join(offenders, "\n  "))
	}
}

// TestTheVerdictAllowlistIsStillAccurate proves the allowlist is not
// stale: every entry must still name a field that actually exists.
func TestTheVerdictAllowlistIsStillAccurate(t *testing.T) {
	present := map[string]bool{}
	for _, f := range exportedFields(t) {
		present[shortPkg(f.Path)+"."+f.Type+"."+f.Name] = true
	}
	for key := range verdictAllowlist {
		if !present[key] {
			t.Fatalf("the verdict allowlist exempts %q, which no longer exists — a stale exemption is "+
				"a hole waiting for a new field to fall into", key)
		}
	}
}

// ---- Rule 2: no single opaque confidence score ----------------------

// confidenceAllowlist names any place a float would genuinely be a
// modelled, decomposable input rather than an opaque output score.
//
// It is EMPTY, and that is a finding rather than an omission: the scan
// below reports that no exported type anywhere in pkg/insurance carries
// a float field at all. Per-source reliability exists in this domain,
// but only as a parameter handed to the real pkg/moat arbitration
// engine — it is never stored on an insurance output type.
var confidenceAllowlist = map[string]string{}

func TestNoOpaqueConfidenceScoreAnywhereInTheInsuranceDomain(t *testing.T) {
	var offenders []string
	for _, f := range exportedFields(t) {
		key := shortPkg(f.Path) + "." + f.Type + "." + f.Name
		if _, allowed := confidenceAllowlist[key]; allowed {
			continue
		}
		lower := strings.ToLower(f.Name)
		named := lower == "confidence" || lower == "score" || lower == "probability" ||
			lower == "certaintyscore" || lower == "riskscore" || lower == "trustscore"
		if named || f.IsFloat {
			offenders = append(offenders, f.Path+": "+f.Type+"."+f.Name+" ("+f.GoType+")")
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("the insurance domain has acquired an opaque confidence score. The Final Design §39's "+
			"last forbidden item is \"membuat satu opaque confidence score\"; evidence is decomposed into "+
			"supporting / contradicting / missing, never collapsed into one number with no "+
			"derivation:\n  %s", strings.Join(offenders, "\n  "))
	}
}

func TestTheConfidenceAllowlistIsStillAccurate(t *testing.T) {
	present := map[string]bool{}
	for _, f := range exportedFields(t) {
		present[shortPkg(f.Path)+"."+f.Type+"."+f.Name] = true
	}
	for key := range confidenceAllowlist {
		if !present[key] {
			t.Fatalf("the confidence allowlist exempts %q, which no longer exists", key)
		}
	}
}

// ---- Rule 3: no forbidden canonical duplicate -----------------------

var forbiddenDeclarations = []string{
	"InsuranceIdentity", "InsuranceEvidenceStore", "InsuranceReplayEngine",
	"InsuranceDecisionEngine", "InsuranceEvidenceEngine", "InsuranceTrustEngine",
	"InsuranceCorrelationKey", "InsurancePolicyRegistry", "InsuranceProvenance",
	"InsuranceLineage", "InsuranceVerificationCertificate",
}

func TestNoForbiddenCanonicalDuplicateIsDeclaredAnywhere(t *testing.T) {
	fset := token.NewFileSet()
	var offenders []string
	err := filepath.WalkDir(insuranceRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		for _, decl := range f.Decls {
			switch dd := decl.(type) {
			case *ast.FuncDecl:
				check(&offenders, path, dd.Name.Name)
			case *ast.GenDecl:
				for _, spec := range dd.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						check(&offenders, path, s.Name.Name)
					case *ast.ValueSpec:
						for _, n := range s.Names {
							check(&offenders, path, n.Name)
						}
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scanning: %v", err)
	}
	if len(offenders) > 0 {
		t.Fatalf("pkg/insurance declares a canonical duplicate both design documents forbid by "+
			"name:\n  %s", strings.Join(offenders, "\n  "))
	}
}

func check(offenders *[]string, path, name string) {
	for _, bad := range forbiddenDeclarations {
		if name == bad {
			*offenders = append(*offenders, path+": "+name)
		}
	}
}

// ---- Rule 4: no vendor, judgment or company hard-coded ---------------

// hardCodedTokens are names the Final Design §39 forbids hard-coding,
// scanned across the whole insurance tree rather than just the case
// pack, because the prohibition is about the domain.
var hardCodedTokens = []string{
	"marinetraffic", "orbcomm", "vesselfinder", "fleetmon", "exactearth",
	"the polar", "polar judgment",
	"glencore", "trafigura", "vitol", "gunvor", "mercuria",
	"serious fraud office", "department of justice",
	"maersk", "cma cgm", "hapag", "cosco",
}

func TestNoVendorJudgmentOrCompanyIsHardCodedAnywhere(t *testing.T) {
	var offenders []string
	err := filepath.WalkDir(insuranceRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		// This file necessarily contains the forbidden list itself, as
		// does the case pack's own narrower copy.
		if strings.HasSuffix(path, "guardrails_test.go") || strings.HasSuffix(path, "casepack_test.go") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		lower := strings.ToLower(string(b))
		for _, tok := range hardCodedTokens {
			if strings.Contains(lower, tok) {
				offenders = append(offenders, path+": contains "+tok)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scanning: %v", err)
	}
	if len(offenders) > 0 {
		t.Fatalf("pkg/insurance hard-codes a named vendor, a real reported judgment, or a real company — "+
			"all three are on the Final Design §39 forbidden list:\n  %s", strings.Join(offenders, "\n  "))
	}
}

// ---- The scan's own honesty ------------------------------------------

// TestTheScanReachesEveryInsurancePackage proves the walk actually
// covers the tree. Without this, a broken path would make every
// assertion above pass by seeing nothing.
func TestTheScanReachesEveryInsurancePackage(t *testing.T) {
	// Tracked by WALKED DIRECTORY, not by fields found: pkg/insurance/
	// canonical's Binding deliberately has only unexported fields, and a
	// package with nothing to report is still a package the scan must
	// have visited.
	seen := map[string]bool{}
	err := filepath.WalkDir(insuranceRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			seen[shortPkg(path)] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scanning: %v", err)
	}
	for _, want := range []string{
		"api", "canonical", "case", "causation", "claim", "contradiction",
		"coverage", "deadline", "dispute", "dossier", "evidence", "gap",
		"mitigation", "obligation", "party", "policy", "preservation",
		"quantum", "recovery", "regulatory", "timeline", "verification",
		"casepack",
	} {
		if !seen[want] {
			t.Fatalf("the guardrail scan never reached pkg/insurance/%s, so its types are unchecked", want)
		}
	}
}

// shortPkg maps a scanned path to its package directory name.
func shortPkg(path string) string {
	dir := filepath.Dir(path)
	return filepath.Base(dir)
}
