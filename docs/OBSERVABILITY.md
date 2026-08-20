# Veriqo Kernel — Observability

## Metrics

`pkg/platform/observability.Registry` is a dependency-free Counter/Gauge
registry with a Prometheus-text `Snapshot()` exporter — no external
client library, safe for the sandbox's zero-dependency invariant.
Business-specific metric names (e.g. `workflow_steps_total`,
`risk_score_latency_seconds`) are registered by callers at the call
site; none are pre-registered in the package itself today (see
`docs/SLOs.md` "What is NOT yet instrumented" for the concrete list this
repository does not yet wire up).

## Tracing

`pkg/platform/telemetry` defines the tracing seam every kernel layer's
public entrypoints are meant to accept a `context.Context` and start a
span through:

```go
func (e *Engine) Arbitrate(ctx context.Context, actorID string, claim Claim, tick uint64) (ArbitrationResult, error) {
    ctx, span := telemetry.StartSpan(ctx, "fusion.arbitrate",
        telemetry.Attribute{Key: "claim", Value: claim.Key()})
    defer span.End()
    // ... existing logic unchanged ...
}
```

**Current state, stated honestly:** `telemetry.NoopTracer` is the only
tracer ever installed in this repository — no real
`go.opentelemetry.io/otel` SDK has been vendored, because this sandbox
has no network access to `proxy.golang.org` (confirmed blocked every
session of this project; the allowlist covers only a small set of Go
toolchain / package-index domains, none of which are the OTel module
path). The existing public function signatures in `pkg/moat/fusion`,
`pkg/moat/causal`, `pkg/moat/decision`, `pkg/moat/digitaltwin`, and
`pkg/workflow` have **not** all been retrofitted with a `context.Context`
parameter in this session — that mechanical, call-site-by-call-site
change across ~15 packages was judged lower-value than shipping the
seam itself plus the net-new MOAT intelligence layers the audit doc
prioritized higher (Truth & Contradiction Engine, hierarchical Bayes,
temporal graph, decision intelligence, digital twin simulation). It is
listed as an explicit, concrete open item in `README.md`.

## Production wiring path

1. Vendor `go.opentelemetry.io/otel` + `otlptracegrpc` (or your
   preferred exporter) in an environment with real module-proxy access.
2. Implement `telemetry.Tracer` with a thin adapter over
   `otel.Tracer.Start`.
3. Call `telemetry.SetGlobalTracer(adapter)` once at process startup in
   `cmd/veriqo-demo`/`cmd/veriqo-node`/`cmd/veriqo-api` `main()`.
4. Every already-instrumented `telemetry.StartSpan` call site
   immediately starts producing real spans — zero further code changes.
5. Point the exporter at the collector described in
   `configs/prod/otel.yaml`.
