// Package mission implements Veriqo's Mission Controller — gap #3 in
// "Kritik_terbesar_nomor_1.docx": "Mission Controller masih kecil ...
// Saya ingin nanti ada Global State, Engine Health, System Status,
// Execution Timeline, Dependency Graph, Live Graph, Critical Alert,
// Recovery, Prediction."
//
// Controller is a thin, real facade over the pieces that already exist
// and do the actual work — pkg/kernel/runtime (boot/monitor/recover),
// pkg/kernel/execgraph (dependency graph, exposed live through
// Runtime.Graph()), and pkg/kernel/resource (quota utilization). It
// does not duplicate their logic; it composes and surfaces it under
// the 9 named views, plus a documented-as-heuristic Prediction signal.
package mission

import (
	"sort"

	"veriqo/pkg/kernel/execgraph"
	"veriqo/pkg/kernel/resource"
	"veriqo/pkg/kernel/runtime"
	"veriqo/pkg/kernel/selfheal"
)

// GlobalState is the whole-system snapshot the critique's first bullet
// asks for.
type GlobalState struct {
	Phase        runtime.Phase
	EngineHealth map[string]selfheal.Health
	Healthy      int
	Degraded     int
	Down         int
}

// Alert is one Critical Alert entry — raised for any component
// currently reporting Down.
type Alert struct {
	Component string
	Severity  string // "CRITICAL" for Down, "WARNING" for Degraded
}

// Prediction is a bounded, explicitly-heuristic risk signal — NOT a
// trained model. Honest scope: it counts recent recovery-log GAVE_UP/
// RECOVERED entries per component as a proxy for flapping risk. A real
// forecasting model is an explicit open item.
type Prediction struct {
	Component string
	AtRisk    bool
	Reason    string
}

// Controller is the Mission Controller.
type Controller struct {
	rt        *runtime.Runtime
	resources *resource.Manager
}

// New builds a Controller over an already-booted Runtime and a
// Resource Manager (may be nil if resource tracking isn't in use).
func New(rt *runtime.Runtime, resources *resource.Manager) *Controller {
	return &Controller{rt: rt, resources: resources}
}

// GlobalState returns the whole-system snapshot.
func (c *Controller) GlobalState() GlobalState {
	health := c.rt.Monitor()
	gs := GlobalState{Phase: c.rt.Phase(), EngineHealth: health}
	for _, h := range health {
		switch h {
		case selfheal.Healthy:
			gs.Healthy++
		case selfheal.Degraded:
			gs.Degraded++
		case selfheal.Down:
			gs.Down++
		}
	}
	return gs
}

// EngineHealth returns the live per-engine health map (same data as
// GlobalState.EngineHealth, exposed directly per the critique's
// separately-named bullet).
func (c *Controller) EngineHealth() map[string]selfheal.Health {
	return c.rt.Monitor()
}

// SystemStatus reduces GlobalState to one overall word: RUNNING if
// nothing is Down or Degraded, DEGRADED if some components are
// Degraded but none Down, CRITICAL if any component is Down.
func (c *Controller) SystemStatus() string {
	gs := c.GlobalState()
	switch {
	case gs.Down > 0:
		return "CRITICAL"
	case gs.Degraded > 0:
		return "DEGRADED"
	default:
		return "RUNNING"
	}
}

// ExecutionTimeline returns the live execution graph's recorded status
// transitions, in occurrence order.
func (c *Controller) ExecutionTimeline() []execgraph.Event {
	return c.rt.Graph().Timeline()
}

// DependencyGraph / LiveGraph both expose the same underlying live
// execgraph.Graph — DependencyGraph for static lineage queries,
// LiveGraph for the same object under the critique's second name, so
// callers can use whichever term matches their mental model without
// this package maintaining two parallel structures.
func (c *Controller) DependencyGraph() *execgraph.Graph { return c.rt.Graph() }
func (c *Controller) LiveGraph() *execgraph.Graph       { return c.rt.Graph() }

// CriticalAlert returns one Alert per component currently Down or
// Degraded, sorted by component name.
func (c *Controller) CriticalAlert() []Alert {
	health := c.rt.Monitor()
	var out []Alert
	for name, h := range health {
		switch h {
		case selfheal.Down:
			out = append(out, Alert{Component: name, Severity: "CRITICAL"})
		case selfheal.Degraded:
			out = append(out, Alert{Component: name, Severity: "WARNING"})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Component < out[j].Component })
	return out
}

// Recovery drives self-healing for every currently unhealthy component,
// via the underlying Runtime, and returns which recoveries succeeded.
func (c *Controller) Recovery(tick uint64) map[string]bool {
	return c.rt.Recover(tick)
}

// Prediction scans the self-heal watcher's recovery log and flags any
// component with 2 or more GAVE_UP entries as AtRisk — a simple,
// explicitly-documented count-based heuristic, not a forecasting
// model.
func (c *Controller) Prediction() []Prediction {
	log := c.rt.Watcher().Log()
	gaveUpCount := make(map[string]int)
	for _, rec := range log {
		if rec.Action == selfheal.ActionGaveUp {
			gaveUpCount[rec.Unit]++
		}
	}
	var out []Prediction
	names := make([]string, 0, len(gaveUpCount))
	for n := range gaveUpCount {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		count := gaveUpCount[n]
		out = append(out, Prediction{
			Component: n,
			AtRisk:    count >= 2,
			Reason:    "gave-up recovery attempts observed",
		})
	}
	return out
}

// ResourceUtilization exposes fractional utilization per resource.Kind,
// if a Resource Manager is attached; returns nil otherwise.
func (c *Controller) ResourceUtilization(kinds ...resource.Kind) map[resource.Kind]float64 {
	if c.resources == nil {
		return nil
	}
	out := make(map[resource.Kind]float64, len(kinds))
	for _, k := range kinds {
		out[k] = c.resources.Utilization(k)
	}
	return out
}
