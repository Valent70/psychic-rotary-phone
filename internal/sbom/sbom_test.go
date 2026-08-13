package sbom

import (
	"errors"
	"strings"
	"testing"
)

func TestGenerateRefusesAnUnidentifiedRelease(t *testing.T) {
	if _, err := Generate("", "abc123"); !errors.Is(err, ErrUnidentifiedRelease) {
		t.Fatalf("expected ErrUnidentifiedRelease for empty version, got %v", err)
	}
	if _, err := Generate("v7.12.1", ""); !errors.Is(err, ErrUnidentifiedRelease) {
		t.Fatalf("expected ErrUnidentifiedRelease for empty commit, got %v", err)
	}
}

func TestGenerateEmbedsTheExactReleaseIdentity(t *testing.T) {
	doc, err := Generate("v7.12.1", "077b823abcdef")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Metadata.Component.Version != "v7.12.1" {
		t.Fatalf("expected component version v7.12.1, got %s", doc.Metadata.Component.Version)
	}
	var gotCommit string
	for _, p := range doc.Metadata.Properties {
		if p.Name == "vcs.commit" {
			gotCommit = p.Value
		}
	}
	if gotCommit != "077b823abcdef" {
		t.Fatalf("expected vcs.commit 077b823abcdef, got %q -- this is exactly the audit-caught defect (stale/'unknown' commit)", gotCommit)
	}
}

func TestDifferentReleasesProduceDifferentSBOMs(t *testing.T) {
	a, err := Generate("v7.12.1", "commitA")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Generate("v7.12.1", "commitB")
	if err != nil {
		t.Fatal(err)
	}
	ja, _ := a.JSON()
	jb, _ := b.JSON()
	if string(ja) == string(jb) {
		t.Fatal("SBOMs for two different commits must not be byte-identical")
	}
}

func TestJSONIsWellFormedCycloneDX(t *testing.T) {
	doc, err := Generate("v7.12.1", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := doc.JSON()
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, `"bomFormat": "CycloneDX"`) {
		t.Fatalf("expected CycloneDX bomFormat, got: %s", s)
	}
	if strings.Contains(s, "unknown") {
		t.Fatalf("a generated SBOM must never contain the literal 'unknown' commit placeholder: %s", s)
	}
}
