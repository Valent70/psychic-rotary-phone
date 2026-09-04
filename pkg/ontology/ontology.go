// Package ontology is the VERIQO operational ontology.
//
// # What is taken from Palantir and what is not
//
// The operational-ontology idea is right: objects, properties,
// relationships, actions and authority in one model rather than a data
// layer with permissions bolted on. What VERIQO adds is the thing the
// specification is insistent about:
//
//	relationship != fact
//
// A relationship is a relationship PLUS the evidence for it, PLUS the
// period it holds over, PLUS its qualification. An edge with none of
// those is an assertion the graph makes on its own authority, and a
// graph full of them looks exactly like a graph full of facts.
//
// # One ontology, many domains
//
// There is no maritime ontology and no finance ontology. There is one
// ontology in which a Vessel, a BillOfLading, an Invoice and a Payment
// are object types and the edges between them are declared once. That
// is what makes the cross-domain question -- "which payment settled
// the cargo this vessel carried" -- expressible at all.
//
// # Why the schema is validated rather than trusted
//
// An ontology is configuration, and configuration drifts. A
// relationship type whose endpoints no longer exist, or a required
// property nobody populates, degrades silently: queries return fewer
// results and nothing errors. Validate catches both.
package ontology

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"veriqo/pkg/contract"
	"veriqo/pkg/entity"
	"veriqo/pkg/governance/classification"
)

var (
	ErrUnknownObject       = errors.New("ontology: unknown object type")
	ErrUnknownRelationship = errors.New("ontology: unknown relationship type")
	ErrUnknownProperty     = errors.New("ontology: unknown property")
	ErrEndpointMismatch    = errors.New("ontology: relationship endpoints do not match the schema")
	ErrDanglingType        = errors.New("ontology: a relationship names an object type that is not declared")
	ErrDuplicate           = errors.New("ontology: duplicate declaration")
)

// PropertyType is a property's value domain.
type PropertyType string

const (
	Text      PropertyType = "TEXT"
	Number    PropertyType = "NUMBER"
	Timestamp PropertyType = "TIMESTAMP"
	Quantity  PropertyType = "QUANTITY"
	Geo       PropertyType = "GEO"
	Ref       PropertyType = "REF"
	Enum      PropertyType = "ENUM"
)

func (p PropertyType) Valid() bool {
	switch p {
	case Text, Number, Timestamp, Quantity, Geo, Ref, Enum:
		return true
	}
	return false
}

// Property is a declared attribute of an object type.
type Property struct {
	Name string       `json:"name"`
	Type PropertyType `json:"type"`

	// Required means an object of this type without it is incomplete.
	// It does NOT mean the graph refuses it: real evidence is
	// incomplete, and refusing incomplete objects would mean the graph
	// only ever holds what a tidy dataset contains.
	Required bool `json:"required,omitempty"`

	// Classification is the property-level marking. Law 10: security
	// reaches property level, so a beneficial-owner name can be
	// RESTRICTED on an object that is otherwise INTERNAL.
	//
	// An UNSET marking INHERITS the object's. That is deliberate and
	// it is the opposite of defaulting to PUBLIC: a property nobody
	// classified is as protected as the thing it belongs to, so
	// forgetting to mark one cannot disclose it. Only a MORE
	// restrictive marking needs to be written out.
	Classification classification.Marking `json:"classification"`

	// Unit is required for QUANTITY. A quantity with no unit is the
	// most common way two measurements are compared that should not be.
	Unit string `json:"unit,omitempty"`
}

func (p Property) Validate() error {
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("ontology: a property has no name")
	}
	if !p.Type.Valid() {
		return fmt.Errorf("ontology: property %s has unknown type %q", p.Name, p.Type)
	}
	if p.Type == Quantity && strings.TrimSpace(p.Unit) == "" {
		return fmt.Errorf("ontology: quantity property %s declares no unit; two quantities "+
			"with no units are comparable and should not be", p.Name)
	}
	return nil
}

// EffectiveClassification resolves the property's marking against its
// object's: an unset property marking inherits, and a set one must
// dominate.
func (p Property) EffectiveClassification(owner classification.Marking) (classification.Marking, error) {
	return classification.Derive(p.Classification, owner)
}

// ObjectType is a declared kind of thing.
type ObjectType struct {
	Name string      `json:"name"`
	Kind entity.Kind `json:"kind"`
	// Domains names the customer-facing views this type appears in. A
	// type may appear in several -- a Vessel is in the maritime view
	// and the insurance view -- which is the point of one graph.
	Domains    []string   `json:"domains"`
	Properties []Property `json:"properties"`

	Classification classification.Marking `json:"classification"`
}

func (o ObjectType) Validate() error {
	if strings.TrimSpace(o.Name) == "" {
		return errors.New("ontology: an object type has no name")
	}
	if !o.Kind.Valid() {
		return fmt.Errorf("ontology: object type %s has unknown entity kind %q", o.Name, o.Kind)
	}
	if len(o.Domains) == 0 {
		return fmt.Errorf("ontology: object type %s belongs to no domain view", o.Name)
	}
	if !o.Classification.Valid() {
		return fmt.Errorf("%w: object type %s", classification.ErrNotClassified, o.Name)
	}
	seen := map[string]bool{}
	for _, p := range o.Properties {
		if err := p.Validate(); err != nil {
			return fmt.Errorf("ontology: %s: %w", o.Name, err)
		}
		if seen[p.Name] {
			return fmt.Errorf("%w: %s.%s", ErrDuplicate, o.Name, p.Name)
		}
		seen[p.Name] = true
		// A property may not be classified BELOW its object: reading
		// the object would otherwise disclose it. An unset marking is
		// not below -- it inherits.
		if _, err := p.EffectiveClassification(o.Classification); err != nil {
			return fmt.Errorf("ontology: %s.%s: %w", o.Name, p.Name, err)
		}
	}
	return nil
}

// Property returns a declared property.
func (o ObjectType) Property(name string) (Property, bool) {
	for _, p := range o.Properties {
		if p.Name == name {
			return p, true
		}
	}
	return Property{}, false
}

// Cardinality bounds a relationship.
type Cardinality string

const (
	OneToOne   Cardinality = "ONE_TO_ONE"
	OneToMany  Cardinality = "ONE_TO_MANY"
	ManyToOne  Cardinality = "MANY_TO_ONE"
	ManyToMany Cardinality = "MANY_TO_MANY"
)

func (c Cardinality) Valid() bool {
	switch c {
	case OneToOne, OneToMany, ManyToOne, ManyToMany:
		return true
	}
	return false
}

// RelationshipType is a declared edge.
type RelationshipType struct {
	Name string `json:"name"`
	From string `json:"from"`
	To   string `json:"to"`

	Cardinality Cardinality `json:"cardinality"`
	Domains     []string    `json:"domains"`

	// Temporal marks a relationship that must carry a validity period.
	// Ownership, charter, employment and flag are all temporal; a
	// system that stores them without a period asserts that today's
	// owner always owned it.
	Temporal bool `json:"temporal"`

	// Symmetric relationships hold in both directions.
	Symmetric bool `json:"symmetric,omitempty"`

	Classification classification.Marking `json:"classification"`
}

func (r RelationshipType) Validate() error {
	if strings.TrimSpace(r.Name) == "" {
		return errors.New("ontology: a relationship type has no name")
	}
	if r.From == "" || r.To == "" {
		return fmt.Errorf("ontology: relationship %s has no endpoints", r.Name)
	}
	if !r.Cardinality.Valid() {
		return fmt.Errorf("ontology: relationship %s has unknown cardinality %q", r.Name, r.Cardinality)
	}
	if len(r.Domains) == 0 {
		return fmt.Errorf("ontology: relationship %s belongs to no domain view", r.Name)
	}
	if !r.Classification.Valid() {
		return fmt.Errorf("%w: relationship %s", classification.ErrNotClassified, r.Name)
	}
	if r.Symmetric && r.From != r.To {
		return fmt.Errorf("ontology: relationship %s is symmetric between different types "+
			"(%s, %s), which cannot hold in both directions", r.Name, r.From, r.To)
	}
	return nil
}

// Ontology is a versioned schema.
type Ontology struct {
	Version       contract.Version
	objects       map[string]ObjectType
	relationships map[string]RelationshipType
}

// New builds and validates an ontology.
func New(v contract.Version, objects []ObjectType, relationships []RelationshipType) (*Ontology, error) {
	if v.Zero() {
		return nil, fmt.Errorf("%w: an unversioned ontology cannot be replayed", contract.ErrUnversioned)
	}
	o := &Ontology{Version: v,
		objects:       make(map[string]ObjectType, len(objects)),
		relationships: make(map[string]RelationshipType, len(relationships))}

	for _, t := range objects {
		if err := t.Validate(); err != nil {
			return nil, err
		}
		if _, dup := o.objects[t.Name]; dup {
			return nil, fmt.Errorf("%w: object type %s", ErrDuplicate, t.Name)
		}
		o.objects[t.Name] = t
	}
	for _, r := range relationships {
		if err := r.Validate(); err != nil {
			return nil, err
		}
		if _, dup := o.relationships[r.Name]; dup {
			return nil, fmt.Errorf("%w: relationship type %s", ErrDuplicate, r.Name)
		}
		// The check that catches schema drift: an edge whose endpoints
		// no longer exist degrades silently -- queries just return
		// less.
		if _, ok := o.objects[r.From]; !ok {
			return nil, fmt.Errorf("%w: %s starts at %s", ErrDanglingType, r.Name, r.From)
		}
		if _, ok := o.objects[r.To]; !ok {
			return nil, fmt.Errorf("%w: %s ends at %s", ErrDanglingType, r.Name, r.To)
		}
		o.relationships[r.Name] = r
	}
	return o, nil
}

// ObjectType looks up a type.
func (o *Ontology) ObjectType(name string) (ObjectType, error) {
	t, ok := o.objects[name]
	if !ok {
		return ObjectType{}, fmt.Errorf("%w: %q", ErrUnknownObject, name)
	}
	return t, nil
}

// RelationshipType looks up an edge type.
func (o *Ontology) RelationshipType(name string) (RelationshipType, error) {
	r, ok := o.relationships[name]
	if !ok {
		return RelationshipType{}, fmt.Errorf("%w: %q", ErrUnknownRelationship, name)
	}
	return r, nil
}

// CheckEdge validates that an edge is declared and correctly directed.
func (o *Ontology) CheckEdge(relName, fromType, toType string) error {
	r, err := o.RelationshipType(relName)
	if err != nil {
		return err
	}
	if r.From == fromType && r.To == toType {
		return nil
	}
	if r.Symmetric && r.From == toType && r.To == fromType {
		return nil
	}
	return fmt.Errorf("%w: %s is declared %s -> %s, used %s -> %s",
		ErrEndpointMismatch, relName, r.From, r.To, fromType, toType)
}

// ObjectTypes returns every type, sorted.
func (o *Ontology) ObjectTypes() []ObjectType {
	out := make([]ObjectType, 0, len(o.objects))
	for _, t := range o.objects {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// RelationshipTypes returns every edge type, sorted.
func (o *Ontology) RelationshipTypes() []RelationshipType {
	out := make([]RelationshipType, 0, len(o.relationships))
	for _, r := range o.relationships {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Domains returns every declared domain view.
func (o *Ontology) Domains() []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range o.objects {
		for _, d := range t.Domains {
			if !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
		}
	}
	sort.Strings(out)
	return out
}

// View returns the object and relationship types visible in a domain.
//
// This is what makes "one graph, several customer-facing modes"
// concrete: the maritime user and the insurance user are looking at
// the same objects through different projections, not at two graphs
// that have to be kept consistent.
func (o *Ontology) View(domain string) ([]ObjectType, []RelationshipType) {
	var objs []ObjectType
	for _, t := range o.ObjectTypes() {
		if contains(t.Domains, domain) {
			objs = append(objs, t)
		}
	}
	var rels []RelationshipType
	for _, r := range o.RelationshipTypes() {
		if contains(r.Domains, domain) {
			rels = append(rels, r)
		}
	}
	return objs, rels
}

// CrossDomainRelationships returns the edges that join two different
// domain views.
//
// These are the edges that make VERIQO more than a set of vertical
// products: the B/L that links a cargo lot to an invoice, the invoice
// that links to a payment. A deployment with none of them has built
// separate systems in one repository.
func (o *Ontology) CrossDomainRelationships() []RelationshipType {
	var out []RelationshipType
	for _, r := range o.RelationshipTypes() {
		from, okF := o.objects[r.From]
		to, okT := o.objects[r.To]
		if !okF || !okT {
			continue
		}
		if !sharesAll(from.Domains, to.Domains) {
			out = append(out, r)
		}
	}
	return out
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// sharesAll reports whether the two domain sets are identical.
func sharesAll(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	as := append([]string(nil), a...)
	bs := append([]string(nil), b...)
	sort.Strings(as)
	sort.Strings(bs)
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}
