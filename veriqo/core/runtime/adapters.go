// adapters.go wraps every existing veriqo/registry.Registry API (Trust,
// Intent, Evidence, Reasoning, Policy, Verification, Replay) into
// runtime.Engine implementations — the "Bridge" the critique calls the
// single most important piece of this work: "kalau benar bridge
// berhasil, berarti Trust/Evidence/Reasoning/Replay/Verification/
// Intent/Policy tidak perlu diubah. Semuanya cukup: adapter."
//
// No business logic lives here — every Invoke case is argument
// marshalling only (string/float64/uint64 <-> map[string]string), then
// a direct call into the same registry.Registry APIs the CLI, REST
// gateway, and generated SDKs already call.
package runtime

import (
	"encoding/json"
	"fmt"
	"strconv"

	"veriqo/veriqo/registry"
)

func parseFloat(args map[string]string, key string) (float64, error) {
	v, ok := args[key]
	if !ok {
		return 0, fmt.Errorf("runtime: missing required arg %q", key)
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("runtime: arg %q: %w", key, err)
	}
	return f, nil
}

func parseUint(args map[string]string, key string) (uint64, error) {
	v, ok := args[key]
	if !ok {
		return 0, fmt.Errorf("runtime: missing required arg %q", key)
	}
	u, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("runtime: arg %q: %w", key, err)
	}
	return u, nil
}

func toJSONResult(key string, v any) (map[string]string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return map[string]string{key: string(b)}, nil
}

// lifecycle tracks Start/Stop state shared by every adapter below,
// since the seven wrapped kernels have no real "not running" state of
// their own (always-live in-memory structures) — without this,
// HealthCheck would unconditionally report healthy even after Stop(),
// silently defeating Manager's lifecycle tracking.
type lifecycle struct{ stopped bool }

func (l *lifecycle) start() error { l.stopped = false; return nil }
func (l *lifecycle) stop() error  { l.stopped = true; return nil }
func (l *lifecycle) health() Status {
	if l.stopped {
		return StatusStopped
	}
	return StatusHealthy
}

// RegisterAll builds one Engine adapter per registry API and registers
// all seven into m.
func RegisterAll(m *Manager, reg *registry.Registry) error {
	adapters := []Engine{
		&trustEngine{reg: reg},
		&intentEngine{reg: reg},
		&evidenceEngine{reg: reg},
		&reasoningEngine{reg: reg},
		&policyEngine{reg: reg},
		&verificationEngine{reg: reg},
		&replayEngine{reg: reg},
	}
	for _, a := range adapters {
		if err := m.Register(a); err != nil {
			return err
		}
	}
	return nil
}

// --- Trust ------------------------------------------------------------

type trustEngine struct {
	reg *registry.Registry
	lifecycle
}

func (e *trustEngine) Metadata() Metadata {
	return Metadata{Name: "trust", Version: "v1", Priority: 1,
		Capabilities: []Capability{"trust.evaluate", "trust.certify", "trust.ledger", "trust.verify_ledger"}}
}
func (e *trustEngine) Start() error        { return e.lifecycle.start() }
func (e *trustEngine) Stop() error         { return e.lifecycle.stop() }
func (e *trustEngine) HealthCheck() Status { return e.lifecycle.health() }
func (e *trustEngine) Invoke(method string, args map[string]string) (map[string]string, error) {
	switch method {
	case "evaluate", "certify":
		winRate, err := parseFloat(args, "win_rate")
		if err != nil {
			return nil, err
		}
		contradictionRate, err := parseFloat(args, "contradiction_rate")
		if err != nil {
			return nil, err
		}
		tick, err := parseUint(args, "tick")
		if err != nil {
			return nil, err
		}
		var score any
		var cerr error
		if method == "evaluate" {
			score, cerr = e.reg.Trust.Evaluate(args["subject_id"], winRate, contradictionRate, tick)
		} else {
			score, cerr = e.reg.Trust.Certify(args["subject_id"], winRate, contradictionRate, tick)
		}
		if cerr != nil {
			return nil, cerr
		}
		return toJSONResult("result", score)
	case "ledger":
		l, err := e.reg.Trust.Ledger()
		if err != nil {
			return nil, err
		}
		return toJSONResult("result", l)
	case "verify_ledger":
		s, err := e.reg.Trust.VerifyLedger()
		if err != nil {
			return nil, err
		}
		return map[string]string{"result": s}, nil
	default:
		return nil, fmt.Errorf("runtime: trust: unknown method %q", method)
	}
}

// --- Intent -------------------------------------------------------------

type intentEngine struct {
	reg *registry.Registry
	lifecycle
}

func (e *intentEngine) Metadata() Metadata {
	return Metadata{Name: "intent", Version: "v1", Priority: 1,
		Capabilities: []Capability{"intent.verify", "intent.certify", "intent.ledger"}}
}
func (e *intentEngine) Start() error        { return e.lifecycle.start() }
func (e *intentEngine) Stop() error         { return e.lifecycle.stop() }
func (e *intentEngine) HealthCheck() Status { return e.lifecycle.health() }
func (e *intentEngine) Invoke(method string, args map[string]string) (map[string]string, error) {
	switch method {
	case "verify", "certify":
		expectedRisk, err := parseFloat(args, "expected_risk")
		if err != nil {
			return nil, err
		}
		observedRisk, err := parseFloat(args, "observed_risk")
		if err != nil {
			return nil, err
		}
		tick, err := parseUint(args, "tick")
		if err != nil {
			return nil, err
		}
		var verdict any
		var verr error
		if method == "verify" {
			verdict, verr = e.reg.Intent.Verify(args["intent_id"], args["subject"], args["expected_action"], args["observed_action"], expectedRisk, observedRisk, tick)
		} else {
			verdict, verr = e.reg.Intent.Certify(args["intent_id"], args["subject"], args["expected_action"], args["observed_action"], expectedRisk, observedRisk, tick)
		}
		if verr != nil {
			return nil, verr
		}
		return toJSONResult("result", verdict)
	case "ledger":
		l, err := e.reg.Intent.Ledger()
		if err != nil {
			return nil, err
		}
		return toJSONResult("result", l)
	default:
		return nil, fmt.Errorf("runtime: intent: unknown method %q", method)
	}
}

// --- Evidence -------------------------------------------------------------

type evidenceEngine struct {
	reg *registry.Registry
	lifecycle
}

func (e *evidenceEngine) Metadata() Metadata {
	return Metadata{Name: "evidence", Version: "v1", Priority: 0,
		Capabilities: []Capability{"evidence.add_node", "evidence.add_edge", "evidence.lineage", "evidence.influenced", "evidence.stats"}}
}
func (e *evidenceEngine) Start() error        { return e.lifecycle.start() }
func (e *evidenceEngine) Stop() error         { return e.lifecycle.stop() }
func (e *evidenceEngine) HealthCheck() Status { return e.lifecycle.health() }
func (e *evidenceEngine) Invoke(method string, args map[string]string) (map[string]string, error) {
	switch method {
	case "add_node":
		id, err := e.reg.Evidence.AddNode(args["kind"], args["payload"])
		if err != nil {
			return nil, err
		}
		return map[string]string{"id": id}, nil
	case "add_edge":
		id, err := e.reg.Evidence.AddEdge(args["from"], args["to"], args["relation"])
		if err != nil {
			return nil, err
		}
		return map[string]string{"id": id}, nil
	case "lineage":
		nodes, err := e.reg.Evidence.Lineage(args["id"])
		if err != nil {
			return nil, err
		}
		return toJSONResult("result", nodes)
	case "influenced":
		nodes, err := e.reg.Evidence.Influenced(args["id"])
		if err != nil {
			return nil, err
		}
		return toJSONResult("result", nodes)
	case "stats":
		s, err := e.reg.Evidence.Stats()
		if err != nil {
			return nil, err
		}
		return map[string]string{"result": s}, nil
	default:
		return nil, fmt.Errorf("runtime: evidence: unknown method %q", method)
	}
}

// --- Reasoning -------------------------------------------------------------

type reasoningEngine struct {
	reg *registry.Registry
	lifecycle
}

func (e *reasoningEngine) Metadata() Metadata {
	return Metadata{Name: "reasoning", Version: "v1", Priority: 2,
		Capabilities: []Capability{"reasoning.assert", "reasoning.infer", "reasoning.query", "reasoning.explain"}}
}
func (e *reasoningEngine) Start() error        { return e.lifecycle.start() }
func (e *reasoningEngine) Stop() error         { return e.lifecycle.stop() }
func (e *reasoningEngine) HealthCheck() Status { return e.lifecycle.health() }
func (e *reasoningEngine) Invoke(method string, args map[string]string) (map[string]string, error) {
	switch method {
	case "assert":
		s, err := e.reg.Reasoning.Assert(args["subject"], args["predicate"], args["object"])
		if err != nil {
			return nil, err
		}
		return map[string]string{"result": s}, nil
	case "infer":
		n, err := e.reg.Reasoning.Infer()
		if err != nil {
			return nil, err
		}
		return map[string]string{"new_facts": strconv.FormatUint(n, 10)}, nil
	case "query":
		facts, err := e.reg.Reasoning.Query(args["subject"], args["predicate"], args["object"])
		if err != nil {
			return nil, err
		}
		return toJSONResult("result", facts)
	case "explain":
		d, err := e.reg.Reasoning.Explain(args["subject"], args["predicate"], args["object"])
		if err != nil {
			return nil, err
		}
		return toJSONResult("result", d)
	default:
		return nil, fmt.Errorf("runtime: reasoning: unknown method %q", method)
	}
}

// --- Policy -------------------------------------------------------------

type policyEngine struct {
	reg *registry.Registry
	lifecycle
}

func (e *policyEngine) Metadata() Metadata {
	return Metadata{Name: "policy", Version: "v1", Priority: 2, DependsOn: []string{"trust"},
		Capabilities: []Capability{"policy.evaluate", "policy.ledger", "policy.verify_chain"}}
}
func (e *policyEngine) Start() error        { return e.lifecycle.start() }
func (e *policyEngine) Stop() error         { return e.lifecycle.stop() }
func (e *policyEngine) HealthCheck() Status { return e.lifecycle.health() }
func (e *policyEngine) Invoke(method string, args map[string]string) (map[string]string, error) {
	switch method {
	case "evaluate":
		trustScore, err := parseFloat(args, "trust_score")
		if err != nil {
			return nil, err
		}
		decision, err := e.reg.Policy.Evaluate(args["action"], args["subject"], args["risk_action"], trustScore, args["ontology_valid"])
		if err != nil {
			return nil, err
		}
		return map[string]string{"result": decision}, nil
	case "ledger":
		l, err := e.reg.Policy.Ledger()
		if err != nil {
			return nil, err
		}
		return toJSONResult("result", l)
	case "verify_chain":
		s, err := e.reg.Policy.VerifyChain()
		if err != nil {
			return nil, err
		}
		return map[string]string{"result": s}, nil
	default:
		return nil, fmt.Errorf("runtime: policy: unknown method %q", method)
	}
}

// --- Verification -------------------------------------------------------------

type verificationEngine struct {
	reg *registry.Registry
	lifecycle
}

func (e *verificationEngine) Metadata() Metadata {
	return Metadata{Name: "verification", Version: "v1", Priority: 3,
		Capabilities: []Capability{"verification.verify", "verification.verify_certificate"}}
}
func (e *verificationEngine) Start() error        { return e.lifecycle.start() }
func (e *verificationEngine) Stop() error         { return e.lifecycle.stop() }
func (e *verificationEngine) HealthCheck() Status { return e.lifecycle.health() }
func (e *verificationEngine) Invoke(method string, args map[string]string) (map[string]string, error) {
	switch method {
	case "verify":
		s, err := e.reg.Verification.Verify(args["domain"], args["subject"], args["records_json"], args["claimed_result"])
		if err != nil {
			return nil, err
		}
		return map[string]string{"result": s}, nil
	case "verify_certificate":
		s, err := e.reg.Verification.VerifyCertificate(args["certificate_json"])
		if err != nil {
			return nil, err
		}
		return map[string]string{"result": s}, nil
	default:
		return nil, fmt.Errorf("runtime: verification: unknown method %q", method)
	}
}

// --- Replay -------------------------------------------------------------

type replayEngine struct {
	reg *registry.Registry
	lifecycle
}

func (e *replayEngine) Metadata() Metadata {
	return Metadata{Name: "replay", Version: "v1", Priority: 3,
		Capabilities: []Capability{"replay.contradiction", "replay.fusion"}}
}
func (e *replayEngine) Start() error        { return e.lifecycle.start() }
func (e *replayEngine) Stop() error         { return e.lifecycle.stop() }
func (e *replayEngine) HealthCheck() Status { return e.lifecycle.health() }
func (e *replayEngine) Invoke(method string, args map[string]string) (map[string]string, error) {
	switch method {
	case "contradiction":
		s, err := e.reg.Replay.Contradiction(args["subject"], args["records_json"])
		if err != nil {
			return nil, err
		}
		return map[string]string{"result": s}, nil
	case "fusion":
		s, err := e.reg.Replay.Fusion(args["subject"], args["records_json"])
		if err != nil {
			return nil, err
		}
		return map[string]string{"result": s}, nil
	default:
		return nil, fmt.Errorf("runtime: replay: unknown method %q", method)
	}
}
