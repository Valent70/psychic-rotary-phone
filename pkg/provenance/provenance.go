// Package provenance records where evidence came from and how.
//
// # Law 2, enumerated
//
// The specification lists what every piece of evidence must carry:
// source, producer, acquisition method, timestamp, observed_at,
// received_at, hash, version, rights, license, classification, chain
// of custody. This package holds the origin half of that list; custody
// holds the handling half; version holds the identity half.
//
// # The distinction the whole package turns on
//
//	SOURCE    where VERIQO got it
//	PRODUCER  who made the observation
//
// They are different, they are routinely conflated, and conflating
// them is how three feeds reselling one producer's data are counted as
// three observations. The independence engine reads producer, not
// source, and it can only do that because they are separate fields
// here rather than one "origin" string.
//
// # Provenance is a graph, not a field
//
// Material reaches VERIQO through a path: a terminal's system produced
// it, a broker aggregated it, a portal served it, VERIQO pulled it.
// Each hop can transform, delay, or select. A single "source" field
// records the last hop and loses the three that determine whether the
// evidence is independent of anything.
package provenance

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"veriqo/pkg/canonical/jcs"
	"veriqo/pkg/contract"
)

var (
	ErrNoProducer   = errors.New("provenance: no producer; the party that made the observation is unrecorded")
	ErrNoPath       = errors.New("provenance: no acquisition path")
	ErrCycle        = errors.New("provenance: the acquisition path contains a cycle")
	ErrTimeInverted = errors.New("provenance: a hop is timestamped before the one it received from")
	ErrNoTerminus   = errors.New("provenance: the path does not end at VERIQO")
	ErrUnknownRole  = errors.New("provenance: unknown party role")
)

// Role is what a party did to the material.
type Role string

const (
	// Observer made the observation: the sensor operator, the
	// surveyor, the registry that recorded the fact.
	Observer Role = "OBSERVER"
	// Producer turned the observation into the artefact.
	Producer Role = "PRODUCER"
	// Aggregator combined it with other material.
	Aggregator Role = "AGGREGATOR"
	// Distributor passed it on without changing it.
	Distributor Role = "DISTRIBUTOR"
	// Custodian held it.
	Custodian Role = "CUSTODIAN"
	// Recipient is VERIQO. Every path ends here.
	Recipient Role = "RECIPIENT"
)

func Roles() []Role {
	return []Role{Observer, Producer, Aggregator, Distributor, Custodian, Recipient}
}

func (r Role) Valid() bool {
	for _, k := range Roles() {
		if k == r {
			return true
		}
	}
	return false
}

// Transforms reports whether this role may have altered the material.
// A DISTRIBUTOR that altered it was acting as an AGGREGATOR and the
// path is mislabelled.
func (r Role) Transforms() bool { return r == Producer || r == Aggregator }

// Hop is one step in the acquisition path.
type Hop struct {
	PartyID string `json:"party_id"`
	Role    Role   `json:"role"`

	// At is when this party handled the material.
	At time.Time `json:"at"`

	// Interested marks a party with a stake in the matter. It is
	// recorded per hop because a neutral producer's data reaching
	// VERIQO through a claimant's lawyer is not neutral evidence about
	// what the claimant chose to forward.
	Interested bool `json:"interested,omitempty"`

	// Note describes what this party did.
	Note string `json:"note,omitempty"`
}

func (h Hop) Validate() error {
	if strings.TrimSpace(h.PartyID) == "" {
		return errors.New("provenance: a hop names no party")
	}
	if !h.Role.Valid() {
		return fmt.Errorf("%w: %q", ErrUnknownRole, h.Role)
	}
	if h.At.IsZero() {
		return fmt.Errorf("provenance: hop %s (%s) has no instant", h.PartyID, h.Role)
	}
	return nil
}

// Record is the provenance of one evidence version.
type Record struct {
	ID         contract.ID `json:"id"`
	EvidenceID contract.ID `json:"evidence_id"`
	TenantID   string      `json:"tenant_id"`

	// Path is ordered from the observation to VERIQO.
	Path []Hop `json:"path"`

	// ObservedAt is when the world was as the evidence describes.
	// ReceivedAt is when VERIQO got it. The gap between them is what
	// freshness is measured over, and a record that keeps only one
	// cannot answer whether the evidence was current when it arrived.
	ObservedAt *time.Time `json:"observed_at,omitempty"`
	ReceivedAt time.Time  `json:"received_at"`

	// LicenceID names the terms. Rights are evaluated by pkg/rights;
	// this is the pointer that makes that possible.
	LicenceID string `json:"licence_id"`

	// SourceContentHash is the digest of what arrived, before any
	// VERIQO processing. It is the anchor a provider can be asked to
	// confirm against.
	SourceContentHash string `json:"source_content_hash"`
}

// Validate enforces the path's structure.
func (r Record) Validate() error {
	if r.ID == "" {
		return fmt.Errorf("%w: provenance record has no id", contract.ErrMalformedID)
	}
	if err := r.ID.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(r.TenantID) == "" {
		return errors.New("provenance: not anchored to a tenant")
	}
	if len(r.Path) == 0 {
		return ErrNoPath
	}
	if r.ReceivedAt.IsZero() {
		return errors.New("provenance: no reception instant")
	}
	if strings.TrimSpace(r.LicenceID) == "" {
		return errors.New("provenance: no licence; material held under unknown terms")
	}
	if len(r.SourceContentHash) != 64 {
		return errors.New("provenance: no source content hash; there is nothing a provider " +
			"could be asked to confirm against")
	}

	seen := map[string]bool{}
	var last time.Time
	producerSeen := false
	for i, h := range r.Path {
		if err := h.Validate(); err != nil {
			return err
		}
		if seen[h.PartyID] {
			return fmt.Errorf("%w: %s appears twice", ErrCycle, h.PartyID)
		}
		seen[h.PartyID] = true

		// Time must not run backwards along the path. A hop that
		// handled the material before the party it received it from is
		// a fabricated or mis-transcribed chain.
		if i > 0 && h.At.Before(last) {
			return fmt.Errorf("%w: %s at %s follows %s at %s",
				ErrTimeInverted, h.PartyID, h.At.Format(time.RFC3339),
				r.Path[i-1].PartyID, last.Format(time.RFC3339))
		}
		last = h.At

		if h.Role == Producer || h.Role == Observer {
			producerSeen = true
		}
		if h.Role == Recipient && i != len(r.Path)-1 {
			return fmt.Errorf("provenance: %s is RECIPIENT at position %d of %d; "+
				"VERIQO is the terminus", h.PartyID, i+1, len(r.Path))
		}
	}
	if !producerSeen {
		return fmt.Errorf("%w: %s", ErrNoProducer, r.ID)
	}
	if r.Path[len(r.Path)-1].Role != Recipient {
		return fmt.Errorf("%w: the last hop is %s", ErrNoTerminus, r.Path[len(r.Path)-1].Role)
	}
	if r.ObservedAt != nil && r.ObservedAt.After(r.ReceivedAt) {
		return fmt.Errorf("%w: observed after it was received", ErrTimeInverted)
	}
	return nil
}

// ProducerID returns the party that made the observation.
//
// This is what the independence engine reads. It walks to the FIRST
// observer or producer, not the last party: the point of the field is
// to identify the origin of the observation, and the last transforming
// party is the aggregator that would make two feeds look distinct.
func (r Record) ProducerID() (string, error) {
	if err := r.Validate(); err != nil {
		return "", err
	}
	for _, h := range r.Path {
		if h.Role == Observer || h.Role == Producer {
			return h.PartyID, nil
		}
	}
	return "", ErrNoProducer
}

// SourceID returns the party VERIQO obtained it from: the hop before
// the recipient.
func (r Record) SourceID() (string, error) {
	if err := r.Validate(); err != nil {
		return "", err
	}
	if len(r.Path) < 2 {
		// A path of one hop means VERIQO observed it itself.
		return r.Path[0].PartyID, nil
	}
	return r.Path[len(r.Path)-2].PartyID, nil
}

// PassedThroughAnInterestedParty reports whether any hop had a stake.
//
// It is a whole-path question. A neutral producer's data forwarded by
// a claimant's lawyer went through an interested party, and reading
// only the source or only the producer misses it.
func (r Record) PassedThroughAnInterestedParty() (bool, []string) {
	var who []string
	for _, h := range r.Path {
		if h.Interested {
			who = append(who, fmt.Sprintf("%s (%s)", h.PartyID, h.Role))
		}
	}
	return len(who) > 0, who
}

// TransformingParties names the hops that may have altered the
// material. A path with three of them is not evidence of one thing
// observed once.
func (r Record) TransformingParties() []string {
	var out []string
	for _, h := range r.Path {
		if h.Role.Transforms() {
			out = append(out, h.PartyID)
		}
	}
	return out
}

// Latency is the gap between observation and reception. It is the
// input to the FRESHNESS quality attribute, and it is unavailable --
// not zero -- when nobody recorded when the observation was made.
func (r Record) Latency() (time.Duration, bool) {
	if r.ObservedAt == nil {
		return 0, false
	}
	return r.ReceivedAt.Sub(*r.ObservedAt), true
}

// Digest is the record's own hash.
func (r Record) Digest() (string, error) {
	if err := r.Validate(); err != nil {
		return "", err
	}
	return jcs.Hash(r)
}

// Describe renders the path for a lineage view.
func (r Record) Describe() string {
	parts := make([]string, 0, len(r.Path))
	for _, h := range r.Path {
		s := fmt.Sprintf("%s[%s]", h.PartyID, h.Role)
		if h.Interested {
			s += "*"
		}
		parts = append(parts, s)
	}
	out := strings.Join(parts, " -> ")
	if interested, who := r.PassedThroughAnInterestedParty(); interested {
		sort.Strings(who)
		out += "  (* interested: " + strings.Join(who, ", ") + ")"
	}
	return out
}
