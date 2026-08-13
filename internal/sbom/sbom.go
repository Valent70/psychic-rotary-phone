// Package sbom closes a real defect the V7.12.1 Palantir-style audit
// found by actually reading the shipped artifact rather than trusting
// this repository's own claims about it: SBOM.json was a static file
// hand-committed once, hardcoding version "v7.12.0" and
// vcs.commit "unknown" forever after — including inside every release
// certificate this repository has since signed, each of which embeds
// SBOMHash computed over that stale content. A signed certificate
// whose SBOM hash commits to the wrong release is not a minor
// cosmetic bug; it is exactly the kind of release-identity mismatch
// the audit's Rule 4 and Rule 6 exist to catch.
//
// Generate produces a CycloneDX-shaped SBOM from the running release's
// actual identity — version, git commit, Go toolchain — every time
// cmd/veriqo-readiness runs, so SBOM.json can never again drift from
// the release it describes.
package sbom

import (
	"encoding/json"
	"errors"
	"runtime"
	"runtime/debug"
)

// ErrUnidentifiedRelease is returned by Generate when either version
// or gitCommit is empty: an SBOM that cannot name its own release is
// exactly the "vcs.commit: unknown" defect this package exists to
// eliminate.
var ErrUnidentifiedRelease = errors.New("sbom: cannot generate an SBOM for an unidentified release (version and git commit are both required)")

// Component is one CycloneDX component entry. VERIQO's own go.mod has
// zero external module requirements (enforced by the zero_dependency
// gate), so the only component that can ever legitimately appear here
// is the module itself plus the Go toolchain it was built with — an
// SBOM listing anything else would mean the zero-dependency invariant
// had silently broken, which build_dependencies below would refuse to
// paper over.
type Component struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Property is a free-form CycloneDX metadata property.
type Property struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Document is the SBOM this package produces.
type Document struct {
	BOMFormat   string      `json:"bomFormat"`
	SpecVersion string      `json:"specVersion"`
	Metadata    Metadata    `json:"metadata"`
	Components  []Component `json:"components"`
}

type Metadata struct {
	Component  Component  `json:"component"`
	Properties []Property `json:"properties"`
}

// Generate builds the SBOM for one specific, named release. version
// and gitCommit are the exact release identity this document commits
// to; passing "" for either is refused, because an SBOM that does not
// identify its own release is exactly the "vcs.commit: unknown" defect
// this package exists to eliminate.
func Generate(version, gitCommit string) (Document, error) {
	if version == "" || gitCommit == "" {
		return Document{}, ErrUnidentifiedRelease
	}
	deps, err := buildDependencies()
	if err != nil {
		return Document{}, err
	}
	return Document{
		BOMFormat:   "CycloneDX",
		SpecVersion: "1.5",
		Metadata: Metadata{
			Component: Component{Type: "application", Name: "veriqo", Version: version},
			Properties: []Property{
				{Name: "go.version", Value: runtime.Version()},
				{Name: "vcs.commit", Value: gitCommit},
			},
		},
		Components: deps,
	}, nil
}

// buildDependencies reads the actual build info Go embeds in the
// binary (via debug.ReadBuildInfo) rather than trusting go.mod at face
// value, so a dependency added without updating this package would
// still show up here. Per the zero_dependency gate, this must always
// return zero external modules; if it ever returns more than the
// main module itself, something changed that the readiness pipeline's
// separate zero_dependency check will also catch.
func buildDependencies() ([]Component, error) {
	out := []Component{}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return out, nil
	}
	for _, dep := range info.Deps {
		out = append(out, Component{Type: "library", Name: dep.Path, Version: dep.Version})
	}
	return out, nil
}

// JSON renders the document deterministically.
func (d Document) JSON() ([]byte, error) {
	return json.MarshalIndent(d, "", "  ")
}
