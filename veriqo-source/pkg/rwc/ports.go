package rwc

// Port is a physical/operational constraint table for one port, entered
// directly from the RWC-001 corpus data supplied in the brief. All
// values are literal transcriptions of the supplied figures — no value
// here was invented or estimated.
//
// Zero-value float fields (e.g. a port with no stated MaxLOA) mean "not
// stated in the corpus", not "no constraint" — EvaluateVesselAtPort
// (constraints.go) treats an unstated numeric constraint as
// non-evaluable rather than silently passing it.
type Port struct {
	Name string

	MaxLOA           float64 // metres, 0 = not stated
	MaxBeam          float64 // metres, 0 = not stated
	MaxDraftAbsolute float64 // metres, absolute maximum (e.g. "max draft 8.00m")
	// MaxDraftOperational is the operationally-binding recommended max,
	// when the corpus states one distinct from the absolute max (e.g.
	// Akonikien "recommended operational max 7.50m"). 0 = none stated
	// separate from MaxDraftAbsolute.
	MaxDraftOperational float64
	RequiresGeared      bool
	// TideDependent marks a port where the corpus itself states the
	// usable channel/berth depth varies with tide in a way not resolved
	// by a single static number (e.g. Douala's "channel draft 6.20m +
	// tide", "tide range 0.3-2.9m"). A vessel evaluated at a
	// TideDependent port whose draft is within MaxDraftOperational/
	// MaxDraftAbsolute is still marked UNRESOLVED (not PASS or FAIL) on
	// the draft dimension, because the corpus itself says real-time
	// tide confirmation is required and this system has no live tide
	// feed (see docs/RWC_V2_NATIVE_INTEGRATION_BASELINE.md §1 — no network egress).
	TideDependent bool

	LoadingRate   string // e.g. "5,000 MT PWWD SHINC" — descriptive, not constraint-checked numerically
	DischargeRate string
	Notes         []string
}

// Ports is every port from the RWC-001 corpus, keyed by name exactly as
// given in the brief.
var Ports = map[string]Port{
	"BATA": {
		Name: "Bata", MaxLOA: 300, MaxBeam: 40, MaxDraftAbsolute: 14.5,
		LoadingRate: "5,000 MT PWWD SHINC",
		Notes:       []string{"min UKC 1m", "daylight berthing", "max wind 4 Bft"},
	},
	"AKONIKIEN": {
		Name: "Akonikien", MaxLOA: 150, MaxDraftAbsolute: 8.00, MaxDraftOperational: 7.50,
		RequiresGeared: true,
		Notes: []string{
			"freshwater port", "pilot from Bata", "tug if required",
			"berthing only at high tide", "clinker loading 4,000 MT PWWD SHINC",
			"cement loading 1,500 MT PWWD SHINC",
		},
	},
	"TEMA": {
		Name: "Tema", MaxLOA: 220, MaxDraftAbsolute: 8.2, // conservative: berth 13/14 draft 8.2m (berth 3 is 10m but crane wire constraint applies at the shallower/geared berths)
		DischargeRate: "4,000 MT PWWD SHINC",
		Notes: []string{
			"berth 3 draft 10m", "berth 13/14 draft 8.2m", "channel 12m",
			"UKC 0.5m", "crane wire must accommodate 14m hopper height",
		},
	},
	"OWENDO": {
		Name: "Owendo", MaxDraftAbsolute: 10.80,
		DischargeRate: "4,000 MT PWWD SHINC",
	},
	"LOME": {
		Name: "Lome", MaxLOA: 210, MaxDraftAbsolute: 10.0, MaxDraftOperational: 11.5,
		DischargeRate: "4,000 MT PWWD SHINC",
		Notes: []string{
			"max draft 10m / 11.5m FWD/AFT per corpus (two figures supplied)",
			"11m approved by authority according to supplied source",
		},
	},
	"DOUALA": {
		Name: "Douala", MaxDraftOperational: 8.5, TideDependent: true,
		DischargeRate: "4,000 MT PWWD SHINC",
		Notes: []string{
			"Wouri fresh water", "channel draft 6.20m + tide",
			"tide range 0.3-2.9m", "recommended max arrival draft 8.5m",
		},
	},
}

// Voyage legs listed in the RWC-001 corpus, from Akonikien.
var RWC001VoyageLegs = []string{"TEMA", "LOME", "DOUALA", "OWENDO", "LUANDA"}
