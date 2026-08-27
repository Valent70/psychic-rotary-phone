package execution

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"veriqo/veriqo/core/runtime"
)

type fakeEngine struct {
	meta   runtime.Metadata
	fail   *int32
	calls  int32
	invoke func(method string, args map[string]string) (map[string]string, error)
}

func (f *fakeEngine) Metadata() runtime.Metadata  { return f.meta }
func (f *fakeEngine) Start() error                { return nil }
func (f *fakeEngine) Stop() error                 { return nil }
func (f *fakeEngine) HealthCheck() runtime.Status { return runtime.StatusHealthy }
func (f *fakeEngine) Invoke(method string, args map[string]string) (map[string]string, error) {
	atomic.AddInt32(&f.calls, 1)
	if f.fail != nil && atomic.LoadInt32(f.fail) > 0 {
		atomic.AddInt32(f.fail, -1)
		return nil, errors.New("simulated failure")
	}
	if f.invoke != nil {
		return f.invoke(method, args)
	}
	return map[string]string{"ok": "true"}, nil
}

func buildManager(t *testing.T, engines ...*fakeEngine) *runtime.Manager {
	t.Helper()
	m := runtime.NewManager()
	for _, e := range engines {
		if err := m.Register(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.StartAll(); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestLinearPipelineSuccess(t *testing.T) {
	trustE := &fakeEngine{meta: runtime.Metadata{Name: "trust", Capabilities: []runtime.Capability{"trust.certify"}}}
	decisionE := &fakeEngine{meta: runtime.Metadata{Name: "decision", Capabilities: []runtime.Capability{"decision.decide"}}}
	m := buildManager(t, trustE, decisionE)
	o := New(m)

	p := Pipeline{Name: "linear", Steps: []Step{
		{Name: "certify", Capability: "trust.certify", Method: "certify"},
		{Name: "decide", Capability: "decision.decide", Method: "decide", DependsOn: []string{"certify"}},
	}}
	trace, err := o.Execute(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !trace.Succeeded || len(trace.Attempts) != 2 {
		t.Fatalf("unexpected trace: %+v", trace)
	}
}

func TestParallelWaveRunsConcurrently(t *testing.T) {
	a := &fakeEngine{meta: runtime.Metadata{Name: "a", Capabilities: []runtime.Capability{"cap.a"}}}
	b := &fakeEngine{meta: runtime.Metadata{Name: "b", Capabilities: []runtime.Capability{"cap.b"}}}
	m := buildManager(t, a, b)
	o := New(m)

	p := Pipeline{Name: "parallel", Steps: []Step{
		{Name: "s1", Capability: "cap.a", Method: "m"},
		{Name: "s2", Capability: "cap.b", Method: "m"},
	}}
	trace, err := o.Execute(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !trace.Succeeded || len(trace.Attempts) != 2 {
		t.Fatalf("unexpected trace: %+v", trace)
	}
	if a.calls != 1 || b.calls != 1 {
		t.Fatalf("expected both engines called once, got a=%d b=%d", a.calls, b.calls)
	}
}

func TestRetryThenSucceed(t *testing.T) {
	fails := int32(2)
	e := &fakeEngine{meta: runtime.Metadata{Name: "flaky", Capabilities: []runtime.Capability{"cap.x"}}, fail: &fails}
	m := buildManager(t, e)
	o := New(m)

	p := Pipeline{Name: "retry", Steps: []Step{
		{Name: "s1", Capability: "cap.x", Method: "m", MaxRetries: 3},
	}}
	trace, err := o.Execute(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !trace.Succeeded || len(trace.Attempts) != 3 {
		t.Fatalf("unexpected trace: %+v", trace)
	}
}

func TestFailureTriggersCompensation(t *testing.T) {
	good := &fakeEngine{meta: runtime.Metadata{Name: "good", Capabilities: []runtime.Capability{"cap.good"}}}
	fails := int32(99)
	bad := &fakeEngine{meta: runtime.Metadata{Name: "bad", Capabilities: []runtime.Capability{"cap.bad"}}, fail: &fails}
	m := buildManager(t, good, bad)
	o := New(m)

	compensated := false
	p := Pipeline{Name: "saga", Steps: []Step{
		{Name: "step1", Capability: "cap.good", Method: "m", Compensate: func(ctx *ExecutionContext) error {
			compensated = true
			return nil
		}},
		{Name: "step2", Capability: "cap.bad", Method: "m", DependsOn: []string{"step1"}},
	}}
	trace, err := o.Execute(p, nil)
	if err == nil {
		t.Fatal("expected pipeline error")
	}
	if trace.Succeeded || !compensated {
		t.Fatalf("expected failed trace + compensation, got trace=%+v compensated=%v", trace, compensated)
	}
}

func TestCycleDetected(t *testing.T) {
	e := &fakeEngine{meta: runtime.Metadata{Name: "e", Capabilities: []runtime.Capability{"cap"}}}
	m := buildManager(t, e)
	o := New(m)
	p := Pipeline{Name: "cyclic", Steps: []Step{
		{Name: "a", Capability: "cap", Method: "m", DependsOn: []string{"b"}},
		{Name: "b", Capability: "cap", Method: "m", DependsOn: []string{"a"}},
	}}
	_, err := o.Execute(p, nil)
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("expected ErrCycle, got %v", err)
	}
}

func TestContextPropagation(t *testing.T) {
	e := &fakeEngine{
		meta: runtime.Metadata{Name: "e", Capabilities: []runtime.Capability{"cap"}},
		invoke: func(method string, args map[string]string) (map[string]string, error) {
			return map[string]string{"value": fmt.Sprintf("computed-%s", args["seed"])}, nil
		},
	}
	m := buildManager(t, e)
	o := New(m)
	p := Pipeline{Name: "chain", Steps: []Step{
		{Name: "s1", Capability: "cap", Method: "m", Args: func(ctx *ExecutionContext) map[string]string {
			return map[string]string{"seed": ctx.Input["seed"]}
		}},
		{Name: "s2", Capability: "cap", Method: "m", DependsOn: []string{"s1"}, Args: func(ctx *ExecutionContext) map[string]string {
			v, _ := ctx.Get("s1", "value")
			return map[string]string{"seed": v}
		}},
	}}
	trace, err := o.Execute(p, map[string]string{"seed": "root"})
	if err != nil {
		t.Fatal(err)
	}
	last := trace.Attempts[len(trace.Attempts)-1]
	if last.Result["value"] != "computed-computed-root" {
		t.Fatalf("expected chained context propagation, got %v", last.Result)
	}
}
