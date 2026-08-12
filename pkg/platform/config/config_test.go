package config

import (
	"errors"
	"testing"
)

const sampleDoc = `
# Veriqo policy configuration (test fixture)
sources:
  - id: AIS_PROVIDER_A
    reliability: 0.9
    provider: SATCOM_ALPHA
  - id: AIS_PROVIDER_B
    reliability: 0.85
    provider: SATCOM_ALPHA
  - id: PORT_AUTHORITY_TPP
    reliability: 0.97

policies:
  - name: dark_vessel_risk
    flag_threshold: 0.4
    escalate_threshold: 0.7
    factors:
      - name: ais_gap_confidence
        weight: 0.4
      - name: sts_transfer_confidence
        weight: 0.35
      - name: cargo_change_confidence
        weight: 0.25
`

func TestParseBasicDocument(t *testing.T) {
	root, err := Parse(sampleDoc)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if root.Get("sources") == nil {
		t.Fatal("expected sources block")
	}
	if root.Get("policies") == nil {
		t.Fatal("expected policies block")
	}
}

func TestLoadSourceProfiles(t *testing.T) {
	root, err := Parse(sampleDoc)
	if err != nil {
		t.Fatal(err)
	}
	sources, err := LoadSourceProfiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 3 {
		t.Fatalf("expected 3 sources, got %d", len(sources))
	}
	if sources[0].ID != "AIS_PROVIDER_A" || sources[0].Provider != "SATCOM_ALPHA" {
		t.Fatalf("unexpected first source: %+v", sources[0])
	}
	if sources[1].BaseReliability != 0.85 {
		t.Fatalf("expected reliability 0.85, got %v", sources[1].BaseReliability)
	}
	if sources[2].Provider != "" {
		t.Fatalf("expected empty provider for source 3, got %q", sources[2].Provider)
	}
}

func TestLoadPolicies(t *testing.T) {
	root, err := Parse(sampleDoc)
	if err != nil {
		t.Fatal(err)
	}
	policies, err := LoadPolicies(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(policies) != 1 {
		t.Fatalf("expected 1 policy, got %d", len(policies))
	}
	p := policies[0]
	if p.Name != "dark_vessel_risk" || p.FlagThreshold != 0.4 || p.EscalateThreshold != 0.7 {
		t.Fatalf("unexpected policy: %+v", p)
	}
	if len(p.Factors) != 3 {
		t.Fatalf("expected 3 factors, got %d", len(p.Factors))
	}
	if p.Factors[0].Name != "ais_gap_confidence" || p.Factors[0].Weight != 0.4 {
		t.Fatalf("unexpected factor 0: %+v", p.Factors[0])
	}
}

func TestLoadDocumentEndToEnd(t *testing.T) {
	sources, policies, err := LoadDocument(sampleDoc)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 3 || len(policies) != 1 {
		t.Fatalf("unexpected counts: %d sources, %d policies", len(sources), len(policies))
	}
}

func TestParseRejectsTabIndentation(t *testing.T) {
	bad := "sources:\n\t- id: X\n"
	if _, err := Parse(bad); !errors.Is(err, ErrTabIndentation) {
		t.Fatalf("expected ErrTabIndentation, got %v", err)
	}
}

func TestParseMalformedLine(t *testing.T) {
	bad := "sources\n  garbage without colon\n"
	if _, err := Parse(bad); err == nil {
		t.Fatal("expected error for malformed line")
	}
}

func TestLoadSourceProfilesMissingReliability(t *testing.T) {
	doc := "sources:\n  - id: X\n"
	root, err := Parse(doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSourceProfiles(root); err == nil {
		t.Fatal("expected error for missing reliability")
	}
}

func TestLoadPoliciesMissingFactors(t *testing.T) {
	doc := "policies:\n  - name: p1\n    flag_threshold: 0.4\n    escalate_threshold: 0.7\n"
	root, err := Parse(doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPolicies(root); err == nil {
		t.Fatal("expected error for missing factors")
	}
}

func TestUnquoteHandlesQuotedScalars(t *testing.T) {
	doc := "sources:\n  - id: \"Quoted ID\"\n    reliability: 0.5\n"
	root, err := Parse(doc)
	if err != nil {
		t.Fatal(err)
	}
	sources, err := LoadSourceProfiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if sources[0].ID != "Quoted ID" {
		t.Fatalf("expected unquoted 'Quoted ID', got %q", sources[0].ID)
	}
}

func TestEmptyDocumentRejected(t *testing.T) {
	if _, err := Parse("   \n # only comment\n"); err != ErrEmptyDocument {
		t.Fatalf("expected ErrEmptyDocument, got %v", err)
	}
}
