package passport

import (
	"errors"
	"strings"
	"testing"
)

func route() Route {
	return Route{
		Overturns: "the finding that 1,500 MT was short-delivered",
		Steps: []Step{
			{N: 1, Action: "obtain the discharge port's own ullage report for the same tanks",
				Party: "the receiver, from the terminal", Produces: "a second discharge figure",
				Effect: "a figure differing by more than the tolerance removes the " +
					"arithmetic the finding rests on", Cost: "days"},
			{N: 2, Action: "obtain a certified density measurement for the parcel at both ports",
				Party: "an independent inspector", Produces: "the basis both figures were taken on",
				Effect: "incomparable bases mean the difference is not a quantity difference " +
					"and the finding is withdrawn", Cost: "weeks"},
			{N: 3, Action: "produce the vessel's ballast records for the voyage",
				Party: "the owner", Produces: "an alternative explanation for the draught change",
				Effect: "demotes the supporting draught observation to unqualified",
				Cost:   "days"},
			{N: 4, Action: "commission an independent re-survey of the remaining cargo",
				Party: "either party", Produces: "a third measurement",
				Effect: "a third figure agreeing with the discharge survey strengthens the " +
					"finding; one agreeing with the loading survey overturns it",
				Cost: "weeks and material expense", Blocked: "the cargo has been discharged " +
					"and commingled; no re-survey is possible"},
		},
		IfAllFail: "the arithmetic stands on the two surveys presented. That is not proof " +
			"of short delivery: it means no evidence available to either party contradicts " +
			"the figures, which is a much weaker statement",
	}
}

// TestARouteMustSayWhatSurvivingItEstablishes.
//
// A route describing only refutation implies that surviving it proves
// the claim, and it does not.
func TestARouteMustSayWhatSurvivingItEstablishes(t *testing.T) {
	r := route()
	r.IfAllFail = ""
	if err := r.Validate(); !errors.Is(err, ErrNoRoute) {
		t.Fatalf("a route with no if-all-fail validated: %v", err)
	}
	if !strings.Contains(route().IfAllFail, "not proof") {
		t.Fatal("the fixture's if-all-fail overstates what survival establishes")
	}
	if err := route().Validate(); err != nil {
		t.Fatalf("a complete route was refused: %v", err)
	}
}

// TestAFindingWithNoRouteIsRefused.
func TestAFindingWithNoRouteIsRefused(t *testing.T) {
	r := route()
	r.Steps = nil
	err := r.Validate()
	if !errors.Is(err, ErrNoRoute) {
		t.Fatalf("a route with no steps validated: %v", err)
	}
	if !strings.Contains(err.Error(), "opposite of defensible") {
		t.Fatalf("the refusal does not say why: %v", err)
	}
}

// TestEveryStepNamesAPartyAndAnEffect. A step nobody can perform, or
// whose success changes nothing, is not a route.
func TestEveryStepNamesAPartyAndAnEffect(t *testing.T) {
	for _, mut := range []func(*Step){
		func(s *Step) { s.Party = "" },
		func(s *Step) { s.Effect = "" },
		func(s *Step) { s.Action = "" },
		func(s *Step) { s.Produces = "" },
	} {
		r := route()
		mut(&r.Steps[0])
		if err := r.Validate(); err == nil {
			t.Fatalf("an incomplete step validated: %+v", r.Steps[0])
		}
	}
}

// TestAGapInTheNumberingIsRefused. A route with a gap cannot be
// walked.
func TestAGapInTheNumberingIsRefused(t *testing.T) {
	r := route()
	r.Steps[2].N = 9
	if err := r.Validate(); err == nil {
		t.Fatal("a route skipping a step number validated")
	}
	r = route()
	r.Steps[2].N = 2
	if err := r.Validate(); err == nil {
		t.Fatal("two steps with the same number validated")
	}
}

// TestABlockedStepStaysInTheRoute.
//
// A recipient is entitled to know that a way of refuting the finding
// is closed to them. Dropping it would make the route look shorter and
// easier than it is.
func TestABlockedStepStaysInTheRoute(t *testing.T) {
	r := route()
	if len(r.Steps) != 4 {
		t.Fatalf("%d steps", len(r.Steps))
	}
	if len(r.Open()) != 3 {
		t.Fatalf("%d open steps", len(r.Open()))
	}
	blocked := r.Blocked()
	if len(blocked) != 1 || blocked[0].N != 4 {
		t.Fatalf("blocked = %v", blocked)
	}
	if !strings.Contains(r.Render(), "BLOCKED:  the cargo has been discharged") {
		t.Fatalf("the rendering hides the blocked step:\n%s", r.Render())
	}
	if !r.Walkable() {
		t.Fatal("a route with three open steps reported unwalkable")
	}
}

// TestARouteWithEveryStepBlockedSaysSoLoudly.
//
// A finding nobody can currently challenge is a serious property, and
// it must be visible rather than inferred from an empty list.
func TestARouteWithEveryStepBlockedSaysSoLoudly(t *testing.T) {
	r := route()
	for i := range r.Steps {
		r.Steps[i].Blocked = "no longer possible"
	}
	if r.Walkable() {
		t.Fatal("a route with every step blocked reported walkable")
	}
	out := r.Render()
	if !strings.Contains(out, "EVERY STEP IS BLOCKED") {
		t.Fatalf("the rendering does not surface it:\n%s", out)
	}
	if !strings.Contains(out, "not a strength of it") {
		t.Fatalf("the rendering lets unfalsifiability read as strength:\n%s", out)
	}
}

// TestTheRouteIsOrderedCheapestFirst, so a recipient who runs out of
// budget has still done the most informative thing available.
func TestTheRouteIsOrderedCheapestFirst(t *testing.T) {
	steps := route().Open()
	for i := 1; i < len(steps); i++ {
		if steps[i].N < steps[i-1].N {
			t.Fatal("Open() does not return steps in order")
		}
	}
	if steps[0].Cost != "days" {
		t.Fatalf("the first step costs %q; the route is not ordered cheapest-first",
			steps[0].Cost)
	}
}

// TestTheRenderingIsUsableByARecipient. Every field a recipient needs
// to act must appear.
func TestTheRenderingIsUsableByARecipient(t *testing.T) {
	out := route().Render()
	for _, want := range []string{"DISPROOF ROUTE", "to overturn:", "who:", "produces:",
		"effect:", "cost:", "If every step is taken and none succeeds:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the rendering omits %q:\n%s", want, out)
		}
	}
}
