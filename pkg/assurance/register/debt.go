package register

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"veriqo/pkg/assurance/state"
	"veriqo/pkg/contract"
)

var (
	ErrNoMissing   = errors.New("assurance/register: a debt does not say what is missing")
	ErrNoOwner     = errors.New("assurance/register: a debt has no owner")
	ErrSelfPayable = errors.New("assurance/register: a debt that needs an outside party names none")
)

// Debt is evidence that is required and does not exist.
//
// # Why an item, not a status
//
// "OPEN" tells a reader that something is unfinished. It does not tell
// them what is missing, why it is missing, who would supply it, what
// it costs, what it blocks, or what the consequence is of shipping
// without it. Those six answers are the difference between a gap a
// buyer can price and a gap that reads as vagueness -- and vagueness
// is what makes an honest position look like an evasive one.
//
// So a debt is an object. It is the mirror image of an assurance
// claim: a claim says what is established and on what evidence; a debt
// says what is NOT established and what evidence would settle it.
type Debt struct {
	ID contract.ID `json:"id"`
	// Missing is the evidence that does not exist.
	Missing string `json:"missing"`
	// Why is why it does not exist. "Nobody has done it" is a
	// different problem from "no accredited firm will assess a system
	// at this stage", and they have different fixes.
	Why string `json:"why"`
	// Owner is who is accountable for closing it. Not who will do the
	// work -- who answers for it not being done.
	Owner string `json:"owner"`
	// ExternalDependency names the outside party required, when one
	// is. Empty means VERIQO can close this alone.
	ExternalDependency string `json:"external_dependency,omitempty"`
	// Class is the evidence class that would settle the debt. It
	// decides whether ExternalDependency is required.
	Class state.Class `json:"class"`
	// Estimate is how long closing it is expected to take, in the
	// coarsest honest unit. "Unknown" is permitted and common.
	Estimate string `json:"estimate,omitempty"`
	// Risk is the consequence of operating without this evidence,
	// stated concretely enough to be argued with.
	Risk string `json:"risk"`
	// BlockedClaims are the assurance claims that cannot advance.
	BlockedClaims []contract.ID `json:"blocked_claims,omitempty"`
	// BlockedGates are the production gates that cannot close.
	BlockedGates []string `json:"blocked_gates,omitempty"`
	// AffectedProducts are the commercial surfaces that inherit the
	// limitation, so a salesperson can see what they may not say.
	AffectedProducts []string `json:"affected_products,omitempty"`
	// Raised is when the debt was recorded.
	Raised time.Time `json:"raised"`
	// Settled, when it has been. A settled debt is kept, not deleted:
	// the history of what was once unproven is part of the record.
	Settled *time.Time `json:"settled,omitempty"`
	// SettledBy cites the evidence that closed it.
	SettledBy contract.ID `json:"settled_by,omitempty"`
}

func (d Debt) Validate() error {
	if strings.TrimSpace(string(d.ID)) == "" {
		return fmt.Errorf("%w: a debt has no id", contract.ErrMalformedID)
	}
	if strings.TrimSpace(d.Missing) == "" {
		return fmt.Errorf("%w: %s", ErrNoMissing, d.ID)
	}
	if strings.TrimSpace(d.Why) == "" {
		return fmt.Errorf("%w: %s does not say why the evidence is missing", ErrNoMissing, d.ID)
	}
	if strings.TrimSpace(d.Owner) == "" {
		return fmt.Errorf("%w: %s", ErrNoOwner, d.ID)
	}
	if strings.TrimSpace(d.Risk) == "" {
		return fmt.Errorf("%w: %s states no risk; a gap with no stated consequence cannot be "+
			"prioritised against any other gap", ErrNoMissing, d.ID)
	}
	if !d.Class.Valid() {
		return fmt.Errorf("assurance/register: %s cites unknown evidence class %q", d.ID, d.Class)
	}
	if d.Class.NeedsIndependentParty() && strings.TrimSpace(d.ExternalDependency) == "" {
		return fmt.Errorf("%w: %s is class %s", ErrSelfPayable, d.ID, d.Class)
	}
	if d.Raised.IsZero() {
		return fmt.Errorf("assurance/register: %s has no raised date", d.ID)
	}
	if d.Settled != nil && strings.TrimSpace(string(d.SettledBy)) == "" {
		return fmt.Errorf("assurance/register: %s is settled and cites no evidence that "+
			"settled it", d.ID)
	}
	return nil
}

// Open reports whether the debt is outstanding.
func (d Debt) Open() bool { return d.Settled == nil }

// SelfPayable reports whether VERIQO can close this debt alone.
func (d Debt) SelfPayable() bool { return !d.Class.NeedsIndependentParty() }

// Age reports how long the debt has been outstanding at an instant.
func (d Debt) Age(at time.Time) time.Duration {
	if d.Settled != nil {
		return d.Settled.Sub(d.Raised)
	}
	return at.Sub(d.Raised)
}

func (d Debt) Describe() string {
	var b strings.Builder
	status := "OPEN"
	if !d.Open() {
		status = "SETTLED by " + string(d.SettledBy)
	}
	fmt.Fprintf(&b, "%s [%s] %s\n", d.ID, status, d.Missing)
	fmt.Fprintf(&b, "  why missing:  %s\n", d.Why)
	fmt.Fprintf(&b, "  owner:        %s\n", d.Owner)
	fmt.Fprintf(&b, "  needs:        %s", d.Class)
	if d.ExternalDependency != "" {
		fmt.Fprintf(&b, " -- %s", d.ExternalDependency)
	} else {
		b.WriteString(" -- VERIQO can close this alone")
	}
	b.WriteString("\n")
	if d.Estimate != "" {
		fmt.Fprintf(&b, "  estimate:     %s\n", d.Estimate)
	}
	fmt.Fprintf(&b, "  risk:         %s\n", d.Risk)
	for _, c := range d.BlockedClaims {
		fmt.Fprintf(&b, "  blocks claim: %s\n", c)
	}
	for _, g := range d.BlockedGates {
		fmt.Fprintf(&b, "  blocks gate:  %s\n", g)
	}
	for _, p := range d.AffectedProducts {
		fmt.Fprintf(&b, "  affects:      %s\n", p)
	}
	return b.String()
}

// SortDebts orders debts by id.
func SortDebts(ds []Debt) {
	sort.Slice(ds, func(i, j int) bool { return ds[i].ID < ds[j].ID })
}
