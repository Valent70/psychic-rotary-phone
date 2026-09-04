// Package entity is VERIQO's real-world object model.
//
// # Why identifiers are typed rather than a map of strings
//
// An IMO number, an MMSI, a LEI and a call sign are not interchangeable
// keys. They have different issuing authorities, different lifetimes,
// and -- decisively -- different reassignment behaviour:
//
//	IMO       permanent, never reassigned
//	MMSI      reassigned on reflagging; the SAME number identifies
//	          different vessels at different times
//	Call sign reassigned
//	LEI       permanent for a legal entity, but entities merge
//
// A system that treats them as equally strong keys will merge two
// vessels that shared an MMSI five years apart. That is not a
// hypothetical: reflagging is routine, and the merge is silent,
// permanent and corrupts every conclusion downstream of it.
//
// So each identifier carries its authority and its validity interval,
// and Strength() states how much weight a match on it may carry.
//
// # An entity is a hypothesis about the world
//
// The objects here are not facts. "Vessel X" is VERIQO's current best
// account of a thing observed through evidence, and every attribute on
// it is held with a temporal scope and an evidence reference. That is
// why there is no plain `Name string` field.
package entity

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"veriqo/pkg/contract"
)

var (
	ErrNoKind        = errors.New("entity: no kind")
	ErrNoIdentifier  = errors.New("entity: an entity with no identifier and no attributes is nothing")
	ErrNoEvidence    = errors.New("entity: an attribute with no evidence reference is an assertion")
	ErrBadInterval   = errors.New("entity: invalid temporal scope")
	ErrUnknownScheme = errors.New("entity: unknown identifier scheme")
)

// Kind is what sort of thing an entity is. The set spans every domain
// because there is ONE graph: a Vessel and a BankAccount are related
// through a Payment, and separating them into per-domain models is
// what makes that relationship unrepresentable.
type Kind string

const (
	Vessel       Kind = "VESSEL"
	Organisation Kind = "ORGANISATION"
	Person       Kind = "PERSON"
	Facility     Kind = "FACILITY"
	CargoLot     Kind = "CARGO_LOT"
	Voyage       Kind = "VOYAGE"
	Document     Kind = "DOCUMENT"
	Contract     Kind = "CONTRACT"
	Account      Kind = "ACCOUNT"
	Payment      Kind = "PAYMENT"
	Incident     Kind = "INCIDENT"
	Policy       Kind = "POLICY"
	Location     Kind = "LOCATION"
)

func Kinds() []Kind {
	return []Kind{Vessel, Organisation, Person, Facility, CargoLot, Voyage,
		Document, Contract, Account, Payment, Incident, Policy, Location}
}

func (k Kind) Valid() bool {
	for _, x := range Kinds() {
		if x == k {
			return true
		}
	}
	return false
}

// Scheme is an identifier namespace.
type Scheme string

const (
	IMO      Scheme = "IMO"
	MMSI     Scheme = "MMSI"
	CallSign Scheme = "CALL_SIGN"
	LEI      Scheme = "LEI"
	Duns     Scheme = "DUNS"
	VAT      Scheme = "VAT"
	UNLOCODE Scheme = "UNLOCODE"
	IBAN     Scheme = "IBAN"
	BIC      Scheme = "BIC"
	BLNumber Scheme = "BL_NUMBER"
	Internal Scheme = "INTERNAL"
)

func Schemes() []Scheme {
	return []Scheme{IMO, MMSI, CallSign, LEI, Duns, VAT, UNLOCODE, IBAN, BIC, BLNumber, Internal}
}

func (s Scheme) Valid() bool {
	for _, x := range Schemes() {
		if x == s {
			return true
		}
	}
	return false
}

// Strength is how much a match on this scheme may contribute.
type Strength int

const (
	// Weak: the identifier is reassigned, or is not issued by a
	// registry at all. A match is a signal and never a conclusion.
	Weak Strength = iota
	// Strong: issued by a registry and stable, but reassignable or
	// ambiguous across mergers.
	Strong
	// Definitive: permanent and never reassigned. A match is
	// conclusive FOR THE PERIOD BOTH IDENTIFIERS WERE VALID -- which
	// is a qualification, not a formality.
	Definitive
)

// Strength states how much weight a match carries.
//
// MMSI is deliberately WEAK. It is the identifier most present in
// maritime data and the one most often treated as a primary key, and
// the reassignment behaviour makes that wrong in exactly the cases
// that matter: two vessels, five years apart, one number.
func (s Scheme) Strength() Strength {
	switch s {
	case IMO, LEI, IBAN:
		return Definitive
	case Duns, VAT, UNLOCODE, BIC, BLNumber:
		return Strong
	case MMSI, CallSign, Internal:
		return Weak
	}
	return Weak
}

// Reassignable reports whether the same value may, over time, identify
// different things. This is the property that makes a temporal
// overlap check mandatory before a match on it means anything.
func (s Scheme) Reassignable() bool {
	switch s {
	case MMSI, CallSign, Internal, VAT:
		return true
	}
	return false
}

// Identifier is a scheme-qualified key with the period it applied.
type Identifier struct {
	Scheme Scheme            `json:"scheme"`
	Value  string            `json:"value"`
	Scope  contract.Interval `json:"scope"`
	// Authority is who issued or recorded it.
	Authority string `json:"authority,omitempty"`
	// EvidenceRefs are the evidence versions this identifier was read
	// from. An identifier with none is somebody's assertion.
	EvidenceRefs []string `json:"evidence_refs"`
}

func (i Identifier) Validate() error {
	if !i.Scheme.Valid() {
		return fmt.Errorf("%w: %q", ErrUnknownScheme, i.Scheme)
	}
	if strings.TrimSpace(i.Value) == "" {
		return fmt.Errorf("entity: %s identifier has no value", i.Scheme)
	}
	if !i.Scope.Valid() {
		return fmt.Errorf("%w: %s=%s", ErrBadInterval, i.Scheme, i.Value)
	}
	if len(i.EvidenceRefs) == 0 {
		return fmt.Errorf("%w: %s=%s", ErrNoEvidence, i.Scheme, i.Value)
	}
	return nil
}

// Matches reports whether two identifiers are the same key AND their
// validity periods overlap.
//
// The overlap requirement is the whole point for a reassignable
// scheme. Two vessels that carried MMSI 123456789 in 2015 and 2023
// have equal values and disjoint scopes, and calling that a match is
// how the silent merge happens.
func (i Identifier) Matches(o Identifier) bool {
	if i.Scheme != o.Scheme || !strings.EqualFold(i.Value, o.Value) {
		return false
	}
	if i.Scheme.Reassignable() {
		return i.Scope.Overlaps(o.Scope)
	}
	return true
}

func (i Identifier) String() string {
	return fmt.Sprintf("%s:%s", i.Scheme, i.Value)
}

// Attribute is a temporally scoped, evidence-backed property.
//
// There is no plain-field alternative on Entity, deliberately. A
// vessel's name is not a fact about the vessel; it is a fact about a
// period, read from a document, and both of those have to travel with
// it or the graph starts asserting things nobody can trace.
type Attribute struct {
	Name  string            `json:"name"`
	Value string            `json:"value"`
	Scope contract.Interval `json:"scope"`

	EvidenceRefs []string `json:"evidence_refs"`
	// Contested marks an attribute for which contradicting evidence
	// exists. It is not removed when contested -- both readings stay,
	// and the contradiction engine reports them.
	Contested bool `json:"contested,omitempty"`
}

func (a Attribute) Validate() error {
	if strings.TrimSpace(a.Name) == "" {
		return errors.New("entity: an attribute has no name")
	}
	if !a.Scope.Valid() {
		return fmt.Errorf("%w: attribute %s", ErrBadInterval, a.Name)
	}
	if len(a.EvidenceRefs) == 0 {
		return fmt.Errorf("%w: attribute %s", ErrNoEvidence, a.Name)
	}
	return nil
}

// Entity is VERIQO's current account of a real-world object.
type Entity struct {
	ID       contract.ID `json:"id"`
	Kind     Kind        `json:"kind"`
	TenantID string      `json:"tenant_id"`

	Identifiers []Identifier `json:"identifiers"`
	Attributes  []Attribute  `json:"attributes"`

	// MergedFrom records entity ids folded into this one. It is kept
	// so a merge can be explained and, if it was wrong, unwound --
	// which is only possible if the constituents were never destroyed.
	MergedFrom []contract.ID `json:"merged_from,omitempty"`
}

func (e Entity) Validate() error {
	if e.ID == "" {
		return fmt.Errorf("%w: entity has no id", contract.ErrMalformedID)
	}
	if err := e.ID.Validate(); err != nil {
		return err
	}
	if !e.Kind.Valid() {
		return fmt.Errorf("%w: %q", ErrNoKind, e.Kind)
	}
	if strings.TrimSpace(e.TenantID) == "" {
		return errors.New("entity: not anchored to a tenant")
	}
	if len(e.Identifiers) == 0 && len(e.Attributes) == 0 {
		return ErrNoIdentifier
	}
	for _, i := range e.Identifiers {
		if err := i.Validate(); err != nil {
			return err
		}
	}
	for _, a := range e.Attributes {
		if err := a.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// IdentifiersOf returns the entity's identifiers in a scheme.
func (e Entity) IdentifiersOf(s Scheme) []Identifier {
	var out []Identifier
	for _, i := range e.Identifiers {
		if i.Scheme == s {
			out = append(out, i)
		}
	}
	return out
}

// AttributeAt returns the value of an attribute at an instant.
//
// It returns (value, true) only when exactly one scoped value covers
// that instant. Two overlapping values is a contradiction, not a
// tie-break, and returning either one would hide it.
func (e Entity) AttributeAt(name string, t time.Time) (string, bool) {
	var found []Attribute
	for _, a := range e.Attributes {
		if a.Name == name && a.Scope.Contains(t) {
			found = append(found, a)
		}
	}
	if len(found) != 1 {
		return "", false
	}
	return found[0].Value, true
}

// ConflictingAttributes returns names whose scoped values overlap with
// different values. These are contradictions inside a single entity,
// and they are reported rather than resolved here.
func (e Entity) ConflictingAttributes() []string {
	seen := map[string]bool{}
	var out []string
	for i := 0; i < len(e.Attributes); i++ {
		for j := i + 1; j < len(e.Attributes); j++ {
			a, b := e.Attributes[i], e.Attributes[j]
			if a.Name != b.Name || a.Value == b.Value {
				continue
			}
			if a.Scope.Overlaps(b.Scope) && !seen[a.Name] {
				seen[a.Name] = true
				out = append(out, a.Name)
			}
		}
	}
	sort.Strings(out)
	return out
}

// EvidenceRefs collects every evidence version this entity rests on.
// A finding that cites the entity has to cite these, which is Law 1.
func (e Entity) EvidenceRefs() []string {
	seen := map[string]bool{}
	var out []string
	add := func(refs []string) {
		for _, r := range refs {
			if !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
	}
	for _, i := range e.Identifiers {
		add(i.EvidenceRefs)
	}
	for _, a := range e.Attributes {
		add(a.EvidenceRefs)
	}
	sort.Strings(out)
	return out
}
