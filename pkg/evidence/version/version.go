// Package version is the VERIQO evidence fabric's central object.
//
// # Law 9: evidence is versioned, never changed
//
//	Tidak boleh: Evidence -> changed
//	melainkan:   Evidence v1, v2, v3
//
// This is not a preference about mutability. It is what makes a
// conclusion reviewable: a finding cites an evidence VERSION, and if
// that version could be edited afterwards, the citation would say
// nothing about what the analyst actually saw. Every derivative --
// a normalisation, an OCR pass, a redaction -- is a new version with a
// parent, not an update.
//
// # Why the original bytes are never touched
//
// Normalisation, redaction and extraction all produce something more
// convenient than the original. Each of them also loses information,
// and which information was lost is exactly what a dispute turns on.
// So the raw acquisition is version 1 and stays byte-identical
// forever; everything else descends from it and says how.
//
// # What a version is not allowed to be missing
//
// The specification's minimum field set is enforced rather than
// documented. A version with no source, no acquisition mode, no
// provenance reference or no rights class is not a weaker record --
// it is a record whose gaps will be filled in by whoever reads it.
package version

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"veriqo/pkg/canonical/jcs"
	"veriqo/pkg/contract"
	"veriqo/pkg/governance/classification"
	"veriqo/pkg/provenance/temporal"
)

var (
	ErrNoSource        = errors.New("evidence: no source")
	ErrNoHash          = errors.New("evidence: no content hash")
	ErrHashMismatch    = errors.New("evidence: the content does not match the recorded hash")
	ErrNoProvenance    = errors.New("evidence: no provenance reference")
	ErrNoRights        = errors.New("evidence: no rights class; material held under unknown terms")
	ErrNoCustody       = errors.New("evidence: no custody reference")
	ErrRootHasParent   = errors.New("evidence: version 1 is the acquisition and has no parent")
	ErrDerivedNoParent = errors.New("evidence: a derived version must name its parent")
	ErrOriginalAltered = errors.New("evidence: the raw acquisition may not be superseded in place")
	ErrTimeInverted    = errors.New("evidence: observed after it was acquired")
	ErrNoTransform     = errors.New("evidence: a derived version must say what was done to produce it")
)

// Mode is how material was obtained. It is part of provenance and it
// bears directly on quality: a screenshot supplied by an interested
// party and a signed API response are different evidence about the
// same fact.
type Mode string

const (
	// APIPull: retrieved by VERIQO from a provider's interface.
	APIPull Mode = "API_PULL"
	// FeedPush: delivered to VERIQO by a provider.
	FeedPush Mode = "FEED_PUSH"
	// PartySubmission: supplied by a party to the matter. The mode
	// most likely to be selective, and the one most often recorded as
	// though it were neutral.
	PartySubmission Mode = "PARTY_SUBMISSION"
	// PublicRetrieval: fetched from a public source.
	PublicRetrieval Mode = "PUBLIC_RETRIEVAL"
	// PhysicalCollection: collected in person.
	PhysicalCollection Mode = "PHYSICAL_COLLECTION"
	// SystemGenerated: produced by VERIQO from other evidence.
	SystemGenerated Mode = "SYSTEM_GENERATED"
)

func Modes() []Mode {
	return []Mode{APIPull, FeedPush, PartySubmission, PublicRetrieval,
		PhysicalCollection, SystemGenerated}
}

func (m Mode) Valid() bool {
	for _, k := range Modes() {
		if k == m {
			return true
		}
	}
	return false
}

// FromAnInterestedParty reports whether the material passed through a
// party with a stake in the outcome. It is a separate question from
// whether the material is authentic, and conflating them is how a
// perfectly preserved copy of one side's assertion becomes
// independent evidence.
func (m Mode) FromAnInterestedParty() bool { return m == PartySubmission }

// Transform is what produced a derived version.
type Transform string

const (
	Acquisition   Transform = "ACQUISITION"
	Normalization Transform = "NORMALIZATION"
	Extraction    Transform = "EXTRACTION"
	OCR           Transform = "OCR"
	Redaction     Transform = "REDACTION"
	Translation   Transform = "TRANSLATION"
	Enrichment    Transform = "ENRICHMENT"
)

func (t Transform) Valid() bool {
	switch t {
	case Acquisition, Normalization, Extraction, OCR, Redaction, Translation, Enrichment:
		return true
	}
	return false
}

// Lossy reports whether the transform discards information present in
// its parent. Every lossy transform must state what it dropped, which
// is why this is a property of the transform rather than a per-call
// judgement.
func (t Transform) Lossy() bool {
	switch t {
	case Normalization, Extraction, OCR, Redaction, Translation:
		return true
	}
	return false
}

// Version is one immutable revision of a piece of evidence.
type Version struct {
	ID         contract.ID `json:"id"`
	EvidenceID contract.ID `json:"evidence_id"`
	Version    uint64      `json:"version"`

	SHA256    string `json:"sha256"`
	MediaType string `json:"media_type"`
	SizeBytes int64  `json:"size_bytes"`

	SourceID   string `json:"source_id"`
	ProducerID string `json:"producer_id"`
	TenantID   string `json:"tenant_id"`

	// AcquiredAt is when VERIQO received it. ObservedAt is when the
	// world was as the evidence describes. They are different
	// questions and a system that keeps only one of them cannot answer
	// "was this known at the time".
	AcquiredAt time.Time  `json:"acquired_at"`
	ObservedAt *time.Time `json:"observed_at,omitempty"`

	Mode           Mode                   `json:"acquisition_mode"`
	RightsClass    string                 `json:"rights_class"`
	Classification classification.Marking `json:"classification"`
	Validity       temporal.Validity      `json:"validity"`

	ParentVersion *contract.ID `json:"parent_version,omitempty"`
	Transform     Transform    `json:"transform"`
	// TransformNote says what a lossy transform dropped. Required for
	// every lossy transform: a derivative that does not say what it
	// lost is presented as equivalent to its parent.
	TransformNote string `json:"transform_note,omitempty"`

	ProvenanceRef string `json:"provenance_ref"`
	CustodyRef    string `json:"custody_ref"`
	QualityRef    string `json:"quality_ref,omitempty"`
}

// Validate enforces the field set the specification requires.
func (v Version) Validate() error {
	if v.ID == "" || v.EvidenceID == "" {
		return fmt.Errorf("%w: a version needs an id and an evidence id", contract.ErrMalformedID)
	}
	if err := v.ID.Validate(); err != nil {
		return err
	}
	if err := v.EvidenceID.Validate(); err != nil {
		return err
	}
	if v.Version == 0 {
		return errors.New("evidence: version numbering starts at 1; 0 is the unset value")
	}
	if len(v.SHA256) != 64 {
		return fmt.Errorf("%w: %q is not a sha-256 digest", ErrNoHash, v.SHA256)
	}
	if strings.TrimSpace(v.SourceID) == "" {
		return ErrNoSource
	}
	if strings.TrimSpace(v.TenantID) == "" {
		return errors.New("evidence: not anchored to a tenant")
	}
	if !v.Mode.Valid() {
		return fmt.Errorf("evidence: unknown acquisition mode %q", v.Mode)
	}
	if strings.TrimSpace(v.RightsClass) == "" {
		return ErrNoRights
	}
	if !v.Classification.Valid() {
		return fmt.Errorf("%w: evidence carries no classification", classification.ErrNotClassified)
	}
	if v.AcquiredAt.IsZero() {
		return errors.New("evidence: no acquisition instant")
	}
	if v.ObservedAt != nil && v.ObservedAt.After(v.AcquiredAt) {
		return fmt.Errorf("%w: observed %s, acquired %s",
			ErrTimeInverted, v.ObservedAt.Format(time.RFC3339), v.AcquiredAt.Format(time.RFC3339))
	}
	if strings.TrimSpace(v.ProvenanceRef) == "" {
		return ErrNoProvenance
	}
	if strings.TrimSpace(v.CustodyRef) == "" {
		return ErrNoCustody
	}
	if !v.Transform.Valid() {
		return fmt.Errorf("%w: %q", ErrNoTransform, v.Transform)
	}

	// The root/derivative rules.
	if v.Version == 1 {
		if v.ParentVersion != nil {
			return fmt.Errorf("%w: %s names parent %s", ErrRootHasParent, v.ID, *v.ParentVersion)
		}
		if v.Transform != Acquisition {
			return fmt.Errorf("evidence: version 1 of %s is the acquisition, not a %s",
				v.EvidenceID, v.Transform)
		}
	} else {
		if v.ParentVersion == nil {
			return fmt.Errorf("%w: %s", ErrDerivedNoParent, v.ID)
		}
		if v.Transform == Acquisition {
			return fmt.Errorf("evidence: %s is version %d and claims to be the acquisition",
				v.ID, v.Version)
		}
		if v.Transform.Lossy() && strings.TrimSpace(v.TransformNote) == "" {
			return fmt.Errorf("evidence: %s applied the lossy transform %s and does not say "+
				"what it dropped; a derivative that omits that is presented as equivalent "+
				"to its parent", v.ID, v.Transform)
		}
	}
	return nil
}

// Digest is the version record's own hash, over everything except the
// digest itself. It is what the custody chain and the ledger commit to.
func (v Version) Digest() (string, error) {
	if err := v.Validate(); err != nil {
		return "", err
	}
	return jcs.Hash(v)
}

// VerifyContent checks bytes against the recorded digest.
//
// This is the only way a caller should establish that material is what
// a version says it is. Comparing sizes, names or media types is not.
func (v Version) VerifyContent(b []byte) error {
	got := jcs.HashBytes(b)
	if got != v.SHA256 {
		return fmt.Errorf("%w: %s records %s, content hashes to %s",
			ErrHashMismatch, v.ID, short(v.SHA256), short(got))
	}
	return nil
}

func short(h string) string {
	if len(h) <= 12 {
		return h
	}
	return h[:12]
}

// Chain is the ordered version history of one piece of evidence.
type Chain struct {
	EvidenceID contract.ID
	versions   []Version
}

// NewChain starts a chain from an acquisition.
func NewChain(root Version) (*Chain, error) {
	if err := root.Validate(); err != nil {
		return nil, err
	}
	if root.Version != 1 {
		return nil, fmt.Errorf("evidence: a chain starts at version 1, not %d", root.Version)
	}
	return &Chain{EvidenceID: root.EvidenceID, versions: []Version{root}}, nil
}

// Derive appends a new version descending from the current head.
//
// It refuses a version that claims to supersede the raw acquisition:
// Law 9 says evidence is added to, never replaced, and the original
// bytes are what a dispute is ultimately about.
func (c *Chain) Derive(v Version) error {
	if err := v.Validate(); err != nil {
		return err
	}
	if v.EvidenceID != c.EvidenceID {
		return fmt.Errorf("evidence: %s belongs to %s, not %s", v.ID, v.EvidenceID, c.EvidenceID)
	}
	if v.TenantID != c.versions[0].TenantID {
		return fmt.Errorf("%w: %s", contract.ErrCrossTenant, v.ID)
	}
	head := c.versions[len(c.versions)-1]
	if v.Version != head.Version+1 {
		return fmt.Errorf("evidence: %s is version %d; the chain is at %d",
			v.ID, v.Version, head.Version)
	}
	if *v.ParentVersion != head.ID {
		return fmt.Errorf("evidence: %s names parent %s; the chain head is %s",
			v.ID, *v.ParentVersion, head.ID)
	}

	// The derivative may not be classified below its parent: an
	// extraction from RESTRICTED material is RESTRICTED whatever the
	// extractor labelled it.
	if _, err := classification.Derive(v.Classification, head.Classification); err != nil {
		return fmt.Errorf("evidence: %s: %w", v.ID, err)
	}
	c.versions = append(c.versions, v)
	return nil
}

// Root returns the raw acquisition, which is never superseded.
func (c *Chain) Root() Version { return c.versions[0] }

// Head returns the latest version.
func (c *Chain) Head() Version { return c.versions[len(c.versions)-1] }

// Versions returns a copy of the history.
func (c *Chain) Versions() []Version { return append([]Version(nil), c.versions...) }

// At returns a specific version. A finding cites a version, and this
// is how a reviewer retrieves exactly what the analyst saw.
func (c *Chain) At(n uint64) (Version, error) {
	if n == 0 || n > uint64(len(c.versions)) {
		return Version{}, fmt.Errorf("evidence: %s has no version %d", c.EvidenceID, n)
	}
	return c.versions[n-1], nil
}

// Lineage renders the transform path from acquisition to head, with
// what each lossy step dropped.
//
// This is the "why did VERIQO say this" trail at the evidence layer:
// a reader follows it and sees not only what was done but what each
// step cost.
func (c *Chain) Lineage() []string {
	out := make([]string, 0, len(c.versions))
	for _, v := range c.versions {
		line := fmt.Sprintf("v%d %s (%s, %s)", v.Version, v.Transform, v.MediaType, short(v.SHA256))
		if v.Transform.Lossy() {
			line += " -- dropped: " + v.TransformNote
		}
		out = append(out, line)
	}
	return out
}

// Losses collects everything the chain has dropped.
//
// A finding built on version 4 of a document is built on something
// that has been normalised, OCR'd and redacted, and the limitations
// section has to say so. Collecting it here means no caller has to
// remember to walk the chain.
func (c *Chain) Losses() []string {
	var out []string
	for _, v := range c.versions {
		if v.Transform.Lossy() && v.TransformNote != "" {
			out = append(out, fmt.Sprintf("v%d %s: %s", v.Version, v.Transform, v.TransformNote))
		}
	}
	return out
}

// InterestedPartyOrigin reports whether the chain's ACQUISITION passed
// through a party to the matter.
//
// It reads the root rather than the head deliberately. Processing does
// not launder provenance: a party's submission that VERIQO normalised
// and OCR'd is still a party's submission, and a system that reported
// the head's SYSTEM_GENERATED mode would say otherwise.
func (c *Chain) InterestedPartyOrigin() bool {
	return c.Root().Mode.FromAnInterestedParty()
}

// EffectiveClassification is the join over the whole chain: whatever
// any version carried, the material as a whole carries.
func (c *Chain) EffectiveClassification() (classification.Marking, error) {
	ms := make([]classification.Marking, 0, len(c.versions))
	for _, v := range c.versions {
		ms = append(ms, v.Classification)
	}
	return classification.Join(ms...)
}

// SortVersions orders a slice deterministically, for rendering.
func SortVersions(vs []Version) {
	sort.Slice(vs, func(i, j int) bool {
		if vs[i].EvidenceID != vs[j].EvidenceID {
			return vs[i].EvidenceID < vs[j].EvidenceID
		}
		return vs[i].Version < vs[j].Version
	})
}
