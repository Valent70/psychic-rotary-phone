// Package rights is the VERIQO rights-aware data access layer.
//
// # The commercial problem this solves
//
// VERIQO's inputs arrive under licences. An AIS feed may be licensed
// for a named customer, in a named territory, for a named purpose, for
// a named period, with redistribution prohibited and derivative works
// permitted only in aggregate. Nothing about that is expressible as a
// classification level, and none of it is discoverable from the bytes.
//
// The failure this prevents is not hypothetical and not technical: it
// is shipping a customer an export that embeds a third party's
// licensed data outside the terms VERIQO holds it under. That is a
// contract breach discovered by the licensor, and no amount of
// security controls addresses it, because the access was authorised
// -- it was just not permitted.
//
// # Six questions, asked separately
//
//	CanUse       may this material be processed at all, for this purpose?
//	CanStore     may it be retained, and for how long?
//	CanDerive    may a derivative work be produced from it?
//	CanDisplay   may it be shown to a person?
//	CanExport    may it leave VERIQO's boundary?
//	CanTrain     may it inform a model?
//
// They are separate because licences separate them. A feed routinely
// permits USE and DISPLAY, permits DERIVE only in aggregate, and
// prohibits EXPORT and TRAIN outright. Collapsing them into one
// "allowed" flag forces the most permissive reading of every licence.
//
// # Derivation is where rights are lost
//
// A derivative inherits the INTERSECTION of its sources' grants. A
// finding built from one permissive and one restrictive source is
// governed by the restrictive one, and Combine implements that rather
// than leaving it to a caller who is looking at only one licence.
package rights

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"veriqo/pkg/policy"
)

var (
	ErrNoLicence          = errors.New("rights: material carries no licence; absence of terms is not permission")
	ErrNotPermitted       = errors.New("rights: not permitted by licence")
	ErrRetentionEnded     = errors.New("rights: the retention period has ended")
	ErrOutOfTerritory     = errors.New("rights: outside the licensed territory")
	ErrPurposeNotLicensed = errors.New("rights: purpose is not among the licensed purposes")
	ErrScopeNotLicensed   = errors.New("rights: the requesting customer is outside the licensed scope")
)

// Use is one of the six questions.
type Use string

const (
	UseProcess Use = "USE"
	UseStore   Use = "STORE"
	UseDerive  Use = "DERIVE"
	UseDisplay Use = "DISPLAY"
	UseExport  Use = "EXPORT"
	UseTrain   Use = "TRAIN"
)

func Uses() []Use {
	return []Use{UseProcess, UseStore, UseDerive, UseDisplay, UseExport, UseTrain}
}

func (u Use) Valid() bool {
	for _, k := range Uses() {
		if k == u {
			return true
		}
	}
	return false
}

// Grant is a permission with optional conditions attached.
type Grant struct {
	Use Use `json:"use"`
	// Condition, when non-empty, is a limit the caller must honour --
	// "aggregate only", "attribution required", "no more than 30 days
	// of history". It is carried as text because licences are text;
	// what matters is that it TRAVELS, so a caller cannot receive a
	// permission without receiving its limit.
	Condition string `json:"condition,omitempty"`
}

// Licence is the terms under which material is held.
type Licence struct {
	ID       string `json:"id"`
	Licensor string `json:"licensor"`

	// Grants is what is permitted. Anything not granted is prohibited:
	// the zero licence permits nothing, because a licence nobody
	// recorded is not a licence permitting everything.
	Grants []Grant `json:"grants"`

	// Purposes limits which declared purposes the material may serve.
	// Empty means every purpose, which is a real licence term and must
	// be stated deliberately rather than arrived at by omission --
	// Validate refuses a licence with grants and no purposes.
	Purposes []policy.Purpose `json:"purposes"`

	// Territories are ISO-3166 alpha-2 codes or region identifiers.
	// Empty means unrestricted, subject to the same rule.
	Territories []string `json:"territories"`

	// CustomerScope names the customers on whose behalf the material
	// may be used. Empty means any customer of VERIQO.
	CustomerScope []string `json:"customer_scope"`

	// Retention bounds storage. A zero RetainUntil means the licence
	// states no limit.
	RetainUntil *time.Time `json:"retain_until,omitempty"`

	// Redistribution is called out separately from EXPORT because they
	// are different acts: exporting to the customer who commissioned
	// the case is not redistribution; publishing is.
	RedistributionPermitted bool `json:"redistribution_permitted"`

	// Attribution, when set, must appear on any derivative or display.
	Attribution string `json:"attribution,omitempty"`
}

// Validate refuses licences whose silence would be read as permission.
func (l Licence) Validate() error {
	if strings.TrimSpace(l.ID) == "" || strings.TrimSpace(l.Licensor) == "" {
		return fmt.Errorf("%w: a licence needs an id and a licensor", ErrNoLicence)
	}
	seen := map[Use]bool{}
	for _, g := range l.Grants {
		if !g.Use.Valid() {
			return fmt.Errorf("rights: licence %s grants unknown use %q", l.ID, g.Use)
		}
		if seen[g.Use] {
			return fmt.Errorf("rights: licence %s grants %s twice", l.ID, g.Use)
		}
		seen[g.Use] = true
	}
	// A licence that grants uses must say what purposes they serve.
	// "Unrestricted" is expressible -- by listing every purpose -- and
	// that is the point: it becomes a decision somebody made.
	if len(l.Grants) > 0 && len(l.Purposes) == 0 {
		return fmt.Errorf("%w: licence %s grants uses without naming any purpose",
			ErrPurposeNotLicensed, l.ID)
	}
	for _, p := range l.Purposes {
		if !p.Valid() {
			return fmt.Errorf("rights: licence %s names unknown purpose %q", l.ID, p)
		}
	}
	if _, ok := l.grant(UseTrain); ok {
		// Training is the one use where a mistake is unrecoverable:
		// the material cannot be removed from the weights. It must be
		// granted explicitly alongside MODEL_TRAINING as a purpose.
		if !l.permitsPurpose(policy.ModelTraining) {
			return fmt.Errorf("%w: licence %s grants TRAIN without licensing MODEL_TRAINING",
				ErrPurposeNotLicensed, l.ID)
		}
	}
	return nil
}

func (l Licence) grant(u Use) (Grant, bool) {
	for _, g := range l.Grants {
		if g.Use == u {
			return g, true
		}
	}
	return Grant{}, false
}

func (l Licence) permitsPurpose(p policy.Purpose) bool {
	if len(l.Purposes) == 0 {
		return true
	}
	for _, q := range l.Purposes {
		if q == p {
			return true
		}
	}
	return false
}

func (l Licence) permitsTerritory(t string) bool {
	if len(l.Territories) == 0 {
		return true
	}
	for _, x := range l.Territories {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}

func (l Licence) permitsCustomer(c string) bool {
	if len(l.CustomerScope) == 0 {
		return true
	}
	for _, x := range l.CustomerScope {
		if strings.EqualFold(x, c) {
			return true
		}
	}
	return false
}

// Context is what the caller is trying to do.
type Context struct {
	Use       Use
	Purpose   policy.Purpose
	Territory string
	Customer  string
	At        time.Time

	// Redistributing marks an act of publication rather than delivery
	// to the commissioning customer.
	Redistributing bool
}

// Permission is the answer, with the conditions that travel with it.
type Permission struct {
	Permitted   bool     `json:"permitted"`
	Licence     string   `json:"licence"`
	Conditions  []string `json:"conditions,omitempty"`
	Attribution string   `json:"attribution,omitempty"`
	Reason      string   `json:"reason,omitempty"`
}

// Check answers one of the six questions for one licence.
//
// Every refusal names which term refused, because a licensing refusal
// that a customer cannot explain to their counterparty is a support
// ticket rather than a control.
func Check(l Licence, ctx Context) (Permission, error) {
	if err := l.Validate(); err != nil {
		return Permission{Reason: err.Error()}, err
	}
	if !ctx.Use.Valid() {
		return Permission{}, fmt.Errorf("rights: unknown use %q", ctx.Use)
	}
	if ctx.At.IsZero() {
		return Permission{}, errors.New("rights: no instant supplied; retention cannot be evaluated")
	}
	deny := func(err error, detail string) (Permission, error) {
		e := fmt.Errorf("%w: %s (licence %s from %s)", err, detail, l.ID, l.Licensor)
		return Permission{Licence: l.ID, Reason: e.Error()}, e
	}

	g, ok := l.grant(ctx.Use)
	if !ok {
		return deny(ErrNotPermitted, fmt.Sprintf("%s is not granted", ctx.Use))
	}
	if !l.permitsPurpose(ctx.Purpose) {
		return deny(ErrPurposeNotLicensed, fmt.Sprintf("purpose %s", ctx.Purpose))
	}
	if ctx.Territory != "" && !l.permitsTerritory(ctx.Territory) {
		return deny(ErrOutOfTerritory, fmt.Sprintf("territory %s", ctx.Territory))
	}
	if ctx.Customer != "" && !l.permitsCustomer(ctx.Customer) {
		return deny(ErrScopeNotLicensed, fmt.Sprintf("customer %s", ctx.Customer))
	}
	// Retention governs STORE, and it also governs everything that
	// requires the material to still be held: you cannot derive from
	// what you were required to delete.
	if l.RetainUntil != nil && !ctx.At.Before(*l.RetainUntil) {
		return deny(ErrRetentionEnded, "retention ended "+l.RetainUntil.Format(time.RFC3339))
	}
	if ctx.Redistributing && !l.RedistributionPermitted {
		return deny(ErrNotPermitted, "redistribution is prohibited")
	}

	p := Permission{Permitted: true, Licence: l.ID, Attribution: l.Attribution}
	if g.Condition != "" {
		p.Conditions = append(p.Conditions, g.Condition)
	}
	if l.Attribution != "" && (ctx.Use == UseDisplay || ctx.Use == UseDerive || ctx.Use == UseExport) {
		p.Conditions = append(p.Conditions, "attribution required: "+l.Attribution)
	}
	sort.Strings(p.Conditions)
	return p, nil
}

// Combine produces the licence a derivative is held under: the
// INTERSECTION of its sources.
//
// This is where rights are lost, and losing them is correct. A finding
// assembled from a permissive public registry and a restrictive
// commercial feed is governed by the commercial feed. An
// implementation that took the union, or that took the first licence,
// would produce a derivative more permissive than one of its inputs --
// which is the mechanism by which licensed data ends up in an export
// that its licensor never permitted.
func Combine(id string, sources ...Licence) (Licence, error) {
	if len(sources) == 0 {
		return Licence{}, fmt.Errorf("%w: a derivative with no source licences", ErrNoLicence)
	}
	for _, s := range sources {
		if err := s.Validate(); err != nil {
			return Licence{}, err
		}
	}
	out := Licence{
		ID:                      id,
		Licensor:                combinedLicensor(sources),
		RedistributionPermitted: true,
	}

	// Uses: granted only where EVERY source grants it. Conditions
	// accumulate -- an aggregate-only condition on one source binds the
	// derivative even though the other source had none.
	for _, u := range Uses() {
		conds := map[string]bool{}
		all := true
		for _, s := range sources {
			g, ok := s.grant(u)
			if !ok {
				all = false
				break
			}
			if g.Condition != "" {
				conds[g.Condition] = true
			}
		}
		if !all {
			continue
		}
		var list []string
		for c := range conds {
			list = append(list, c)
		}
		sort.Strings(list)
		out.Grants = append(out.Grants, Grant{Use: u, Condition: strings.Join(list, "; ")})
	}

	out.Purposes = intersectPurposes(sources)
	out.Territories = intersectStrings(sources, func(l Licence) []string { return l.Territories })
	out.CustomerScope = intersectStrings(sources, func(l Licence) []string { return l.CustomerScope })

	for _, s := range sources {
		if !s.RedistributionPermitted {
			out.RedistributionPermitted = false
		}
		if s.RetainUntil != nil {
			if out.RetainUntil == nil || s.RetainUntil.Before(*out.RetainUntil) {
				t := *s.RetainUntil
				out.RetainUntil = &t
			}
		}
		if s.Attribution != "" {
			if out.Attribution == "" {
				out.Attribution = s.Attribution
			} else if !strings.Contains(out.Attribution, s.Attribution) {
				out.Attribution += "; " + s.Attribution
			}
		}
	}

	// A derivative that grants nothing is a legitimate outcome: it
	// says these sources cannot lawfully be combined for any use. It
	// must still be a valid licence object, so the purpose requirement
	// is waived exactly when there is nothing to permit.
	if len(out.Grants) > 0 && len(out.Purposes) == 0 {
		out.Grants = nil
	}
	return out, nil
}

func combinedLicensor(sources []Licence) string {
	seen := map[string]bool{}
	var names []string
	for _, s := range sources {
		if !seen[s.Licensor] {
			seen[s.Licensor] = true
			names = append(names, s.Licensor)
		}
	}
	sort.Strings(names)
	return strings.Join(names, " + ")
}

func intersectPurposes(sources []Licence) []policy.Purpose {
	var out []policy.Purpose
	for _, p := range policy.Purposes() {
		ok := true
		for _, s := range sources {
			if !s.permitsPurpose(p) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, p)
		}
	}
	return out
}

// intersectStrings intersects a set-valued field where an EMPTY list
// means "unrestricted". That asymmetry is the whole subtlety: an empty
// list must not intersect to empty, because empty means the opposite
// of empty here.
func intersectStrings(sources []Licence, get func(Licence) []string) []string {
	var acc []string
	started := false
	for _, s := range sources {
		v := get(s)
		if len(v) == 0 {
			continue // unrestricted: narrows nothing
		}
		if !started {
			acc = append([]string(nil), v...)
			started = true
			continue
		}
		var next []string
		for _, a := range acc {
			for _, b := range v {
				if strings.EqualFold(a, b) {
					next = append(next, a)
				}
			}
		}
		acc = next
	}
	sort.Strings(acc)
	return acc
}
