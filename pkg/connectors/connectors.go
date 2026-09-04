// Package connectors is the provider-agnostic evidence source
// abstraction.
//
// # The rule
//
//	Claude jangan hard-code Kpler/AIS/provider tertentu.
//
// No provider name appears in the core. A source is described by its
// CAPABILITIES and its LICENCE, and the core reasons about those. The
// practical consequence is that replacing an AIS provider is a
// configuration change, and -- more importantly for a neutral platform
// -- that a customer can be told what kind of source underpins a
// conclusion without being told which vendor, which is often
// commercially constrained.
//
// # Every acquisition is rights-checked before it happens
//
// A connector that fetched first and checked the licence afterwards
// would already hold the data. So Acquire takes the purpose and the
// customer, and the source refuses before the call goes out.
//
// # Discovery is not acquisition
//
// Discover says what a source HAS. Acquire obtains it. They are
// separate because they have different licence implications: many
// feeds permit a metadata search under terms that do not permit
// retrieving the underlying record, and a single method would collapse
// that distinction.
package connectors

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"veriqo/pkg/policy"
	"veriqo/pkg/provenance"
	"veriqo/pkg/rights"
)

var (
	ErrNotCapable    = errors.New("connectors: the source does not offer this capability")
	ErrNotLicensed   = errors.New("connectors: the licence does not permit this acquisition")
	ErrNoLicence     = errors.New("connectors: the source declares no licence")
	ErrUnknownSource = errors.New("connectors: unknown source")
	ErrDuplicate     = errors.New("connectors: duplicate source id")
	ErrNoProducer    = errors.New("connectors: a source must name the producer behind it")
	ErrRateLimited   = errors.New("connectors: the source's rate limit is exhausted")
)

// Capability is a kind of evidence a source can supply. It is a
// vocabulary of EVIDENCE KINDS, not of vendors.
type Capability string

const (
	VesselPosition    Capability = "VESSEL_POSITION"
	VesselRegistry    Capability = "VESSEL_REGISTRY"
	SyntheticAperture Capability = "SAR_OBSERVATION"
	ElectroOptical    Capability = "EO_OBSERVATION"
	Weather           Capability = "WEATHER_OBSERVATION"
	PortCallRecord    Capability = "PORT_CALL_RECORD"
	CompanyRegistry   Capability = "COMPANY_REGISTRY"
	SanctionsList     Capability = "SANCTIONS_LIST"
	CommodityPrice    Capability = "COMMODITY_PRICE"
	TradeDocument     Capability = "TRADE_DOCUMENT"
	BankRecord        Capability = "BANK_RECORD"
	ThreatIndicator   Capability = "THREAT_INDICATOR"
	PublishedResearch Capability = "PUBLISHED_RESEARCH"
)

func Capabilities() []Capability {
	return []Capability{VesselPosition, VesselRegistry, SyntheticAperture, ElectroOptical,
		Weather, PortCallRecord, CompanyRegistry, SanctionsList, CommodityPrice,
		TradeDocument, BankRecord, ThreatIndicator, PublishedResearch}
}

func (c Capability) Valid() bool {
	for _, x := range Capabilities() {
		if x == c {
			return true
		}
	}
	return false
}

// SourceCapabilities describes what a source can do and under what
// terms, without naming a product.
type SourceCapabilities struct {
	SourceID string       `json:"source_id"`
	Kinds    []Capability `json:"kinds"`

	// ProducerID is who actually makes the observations behind this
	// source. It is REQUIRED and it is the field the independence
	// engine reads: two sources reselling one producer are one
	// observation, and a source that will not name its producer
	// cannot be assessed for independence at all.
	ProducerID string `json:"producer_id"`

	// AcquisitionMode describes how the source obtains what it sells,
	// which bears on independence and on quality.
	AcquisitionMode string `json:"acquisition_mode"`

	// Licence governs everything obtained from it.
	Licence rights.Licence `json:"licence"`

	// Coverage states what the source does and does not see. A source
	// that claims total coverage is claiming something no source has,
	// and a caller needs the gaps to interpret an absence.
	CoverageNotes []string `json:"coverage_notes"`

	// LatencyTypical is the usual delay between observation and
	// availability. It feeds the FRESHNESS quality attribute.
	LatencyTypical time.Duration `json:"latency_typical"`

	// RateLimit bounds calls per window; zero means unspecified, which
	// Validate refuses.
	RateLimit       int           `json:"rate_limit"`
	RateLimitWindow time.Duration `json:"rate_limit_window"`
}

func (c SourceCapabilities) Validate() error {
	if strings.TrimSpace(c.SourceID) == "" {
		return errors.New("connectors: a source has no id")
	}
	if strings.TrimSpace(c.ProducerID) == "" {
		return fmt.Errorf("%w: %s. A source that does not name its producer cannot be "+
			"assessed for independence against any other source", ErrNoProducer, c.SourceID)
	}
	if len(c.Kinds) == 0 {
		return fmt.Errorf("connectors: source %s offers nothing", c.SourceID)
	}
	for _, k := range c.Kinds {
		if !k.Valid() {
			return fmt.Errorf("connectors: source %s offers unknown capability %q", c.SourceID, k)
		}
	}
	if err := c.Licence.Validate(); err != nil {
		return fmt.Errorf("%w: %s: %v", ErrNoLicence, c.SourceID, err)
	}
	if len(c.CoverageNotes) == 0 {
		return fmt.Errorf("connectors: source %s states no coverage limits; a source that "+
			"sees everything does not exist, and a caller needs the gaps to interpret an "+
			"absence", c.SourceID)
	}
	if c.RateLimit <= 0 || c.RateLimitWindow <= 0 {
		return fmt.Errorf("connectors: source %s declares no rate limit; an unbounded "+
			"connector is a way to be cut off mid-case", c.SourceID)
	}
	if strings.TrimSpace(c.AcquisitionMode) == "" {
		return fmt.Errorf("connectors: source %s does not say how it obtains what it sells",
			c.SourceID)
	}
	return nil
}

// Offers reports whether the source supplies a capability.
func (c SourceCapabilities) Offers(k Capability) bool {
	for _, x := range c.Kinds {
		if x == k {
			return true
		}
	}
	return false
}

// Query is a discovery request.
type Query struct {
	Capability Capability
	// Subject identifies what is being asked about, in the source's
	// own terms.
	Subject  string
	From, To time.Time
	Purpose  policy.Purpose
	Customer string
}

// Availability is what a discovery found.
type Availability struct {
	SourceID string
	// Available says whether the source holds anything matching.
	Available bool
	// Count is how many records, when the source reports it.
	Count int
	// CoverageGap names a known gap over the requested window, so an
	// absence can be read correctly.
	CoverageGap string
	// AcquisitionPermitted says whether the licence would permit
	// actually obtaining it. Discovery and acquisition are separately
	// licensed, so this can be false while Available is true.
	AcquisitionPermitted bool
	PermissionNote       string
}

// AcquisitionRequest is a request to obtain material.
type AcquisitionRequest struct {
	Capability Capability
	Subject    string
	From, To   time.Time

	Purpose   policy.Purpose
	Customer  string
	Territory string
	At        time.Time

	// CaseID ties the acquisition to an investigation, so nothing is
	// obtained outside one.
	CaseID string
}

func (r AcquisitionRequest) Validate() error {
	if !r.Capability.Valid() {
		return fmt.Errorf("connectors: unknown capability %q", r.Capability)
	}
	if strings.TrimSpace(r.CaseID) == "" {
		return errors.New("connectors: an acquisition must be tied to a case; material " +
			"obtained outside one has no purpose to be limited by")
	}
	if !r.Purpose.Valid() {
		return errors.New("connectors: an acquisition must declare its purpose")
	}
	if r.At.IsZero() {
		return errors.New("connectors: an acquisition needs an instant")
	}
	return nil
}

// Acquired is what a source returned.
type Acquired struct {
	SourceID string
	// Content is the raw bytes, unmodified.
	Content []byte
	// MediaType describes them.
	MediaType string
	// ObservedAt is when the world was as the material describes;
	// nil when the source does not say, which is different from
	// "the same as retrieval".
	ObservedAt  *time.Time
	RetrievedAt time.Time

	// Path is the provenance hops the source itself declares. A source
	// that resells another producer's data is expected to say so here,
	// and one that does not is recorded as not having said so.
	Path []provenance.Hop

	// Permission is the rights decision that permitted this, carried
	// so the evidence version can record it.
	Permission rights.Permission
}

// EvidenceSource is what every provider implements.
//
// The interface is small on purpose. A wider one -- with methods for
// subscriptions, backfills, provider-specific filters -- would push
// provider concepts into the core, which is the thing the abstraction
// exists to prevent.
type EvidenceSource interface {
	// Capabilities describes the source. It takes no context because
	// it must be answerable without a network call: a planner needs to
	// know what could supply a missing observation before deciding to
	// ask.
	Capabilities() SourceCapabilities

	// Discover reports what the source holds, without obtaining it.
	Discover(ctx context.Context, q Query) (Availability, error)

	// Acquire obtains material. It must check the licence BEFORE
	// fetching.
	Acquire(ctx context.Context, req AcquisitionRequest) (Acquired, error)

	// Validate lets a source check material it previously supplied,
	// which is how a provider can be asked to confirm an artefact a
	// dispute turns on.
	Validate(ctx context.Context, sourceContentHash string) (bool, error)
}

// CheckAcquisition is the rights gate every source must apply.
//
// It is a package-level function rather than a method so that a
// connector implementation cannot forget it and cannot subtly differ:
// every source calls this, and the test suite checks that they do by
// checking the refusals.
func CheckAcquisition(c SourceCapabilities, req AcquisitionRequest) (rights.Permission, error) {
	if err := c.Validate(); err != nil {
		return rights.Permission{}, err
	}
	if err := req.Validate(); err != nil {
		return rights.Permission{}, err
	}
	if !c.Offers(req.Capability) {
		return rights.Permission{}, fmt.Errorf("%w: %s does not supply %s",
			ErrNotCapable, c.SourceID, req.Capability)
	}
	p, err := rights.Check(c.Licence, rights.Context{
		Use: rights.UseProcess, Purpose: req.Purpose,
		Territory: req.Territory, Customer: req.Customer, At: req.At,
	})
	if err != nil {
		return p, fmt.Errorf("%w: %v", ErrNotLicensed, err)
	}
	// Storing is a separate permission from processing, and an
	// acquisition that cannot be stored is one whose result cannot
	// enter the evidence fabric.
	if _, err := rights.Check(c.Licence, rights.Context{
		Use: rights.UseStore, Purpose: req.Purpose,
		Territory: req.Territory, Customer: req.Customer, At: req.At,
	}); err != nil {
		return p, fmt.Errorf("%w: the material may be processed and not stored, so it "+
			"cannot enter the evidence fabric: %v", ErrNotLicensed, err)
	}
	return p, nil
}

// CheckDiscovery is the lighter gate for a metadata search.
//
// Many feeds permit a search under terms that do not permit retrieving
// the record. Collapsing the two into one check would either forbid
// legitimate discovery or permit unlicensed acquisition.
func CheckDiscovery(c SourceCapabilities, q Query) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if !c.Offers(q.Capability) {
		return fmt.Errorf("%w: %s does not supply %s", ErrNotCapable, c.SourceID, q.Capability)
	}
	return nil
}

// Registry holds the configured sources.
type Registry struct {
	sources map[string]EvidenceSource
}

func NewRegistry() *Registry {
	return &Registry{sources: map[string]EvidenceSource{}}
}

// Add registers a source.
func (r *Registry) Add(s EvidenceSource) error {
	c := s.Capabilities()
	if err := c.Validate(); err != nil {
		return err
	}
	if _, dup := r.sources[c.SourceID]; dup {
		return fmt.Errorf("%w: %s", ErrDuplicate, c.SourceID)
	}
	r.sources[c.SourceID] = s
	return nil
}

// Get returns a source.
func (r *Registry) Get(id string) (EvidenceSource, error) {
	s, ok := r.sources[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownSource, id)
	}
	return s, nil
}

// Offering returns every source that supplies a capability, sorted.
func (r *Registry) Offering(k Capability) []EvidenceSource {
	var out []EvidenceSource
	for _, s := range r.sources {
		if s.Capabilities().Offers(k) {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Capabilities().SourceID < out[j].Capabilities().SourceID
	})
	return out
}

// IndependentProducers returns the distinct producers behind the
// sources offering a capability.
//
// This is the question a planner actually has: not "how many sources
// could supply this" but "how many INDEPENDENT observations could I
// obtain". Five sources reselling one producer answer the first
// question with five and the second with one.
func (r *Registry) IndependentProducers(k Capability) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range r.Offering(k) {
		p := s.Capabilities().ProducerID
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// Report renders the configured sources without naming products
// beyond their declared ids.
func (r *Registry) Report() string {
	var b strings.Builder
	b.WriteString("EVIDENCE SOURCES\n")
	for _, k := range Capabilities() {
		ss := r.Offering(k)
		if len(ss) == 0 {
			continue
		}
		producers := r.IndependentProducers(k)
		fmt.Fprintf(&b, "  %-20s %d source(s), %d distinct producer(s)\n",
			k, len(ss), len(producers))
		if len(producers) < len(ss) {
			fmt.Fprintf(&b, "    NOTE: %d source(s) resolve to %d producer(s); they do not "+
				"corroborate one another\n", len(ss), len(producers))
		}
		for _, s := range ss {
			c := s.Capabilities()
			fmt.Fprintf(&b, "    %-24s producer %s, %s, typical latency %s\n",
				c.SourceID, c.ProducerID, c.AcquisitionMode, c.LatencyTypical)
			for _, n := range c.CoverageNotes {
				fmt.Fprintf(&b, "      coverage: %s\n", n)
			}
		}
	}
	return b.String()
}
