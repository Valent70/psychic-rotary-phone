#!/usr/bin/env bash
# scripts/verify.sh — Veriqo's supply-chain / CI gate script.
#
# This is the concrete, runnable answer to "CI/security tooling (masih
# blocked offline)": every check below has actually been executed in
# this project's sandbox and passes today. golangci-lint, SPIRE, and OPA
# are NOT invoked here because they require fetching from
# proxy.golang.org / external registries, which this sandbox's network
# policy blocks (confirmed across every session in this project's
# history). This script deliberately does not fake running them; it
# runs everything that IS honestly runnable offline with the Go
# toolchain alone, and documents the offline-blocked steps below.
#
# Usage: ./scripts/verify.sh
set -euo pipefail
cd "$(dirname "$0")/.."

echo "== 1/6  go build ./... =="
go build ./...

echo "== 2/6  go vet ./... =="
go vet ./...

echo "== 3/6  gofmt (must produce no output) =="
UNFORMATTED="$(gofmt -l .)"
if [ -n "$UNFORMATTED" ]; then
  echo "gofmt found unformatted files:"
  echo "$UNFORMATTED"
  exit 1
fi

echo "== 4/6  zero-external-dependency invariant =="
# Every package must depend only on the Go standard library. This is a
# hard architectural invariant enforced across the whole kernel — a
# dependency here would mean an unreviewable supply-chain surface, and
# (mechanically) would fail anyway since proxy.golang.org is blocked.
#
# P4 fix (audit "Hasil_audit_brutal...docx" §19-21): the prior version
# of this check used a hand-maintained regex whitelist of stdlib top-
# level directories, which was INCOMPLETE (it did not recognize real
# stdlib packages such as unique, compress/flate, compress/gzip, mime,
# regexp, go/parser, go/ast, go/format, text/template, ...) and would
# silently rot every time a new package started depending on a stdlib
# path the whitelist's author hadn't thought to add — a false positive
# today, but the exact same class of bug could one day produce a false
# NEGATIVE (a real third-party import matching an over-broad regex
# entry). `go list` itself already knows, authoritatively, which
# import paths are standard library (the .Standard field) — diffing
# against `go list std` (the full stdlib package set for this Go
# toolchain) replaces the manual whitelist with the same ground truth
# the compiler uses, so this check can never drift out of sync with
# the actual Go release again.
STDLIB_SET="$(mktemp)"
trap 'rm -f "$STDLIB_SET"' EXIT
go list std | sort -u > "$STDLIB_SET"
EXTERNAL="$(go list -deps ./... | sort -u | grep -v '^veriqo\(/\|$\)' | comm -23 - "$STDLIB_SET" || true)"
if [ -n "$EXTERNAL" ]; then
  echo "Found non-stdlib dependencies (violates zero-dep invariant):"
  echo "$EXTERNAL"
  exit 1
fi

echo "== 5/6  go test ./... -race -cover =="
go test ./... -race -cover

echo "== 6/6  race-repeat on consensus-critical packages (5x) =="
# Consensus/durability/workflow bugs are often timing-dependent; a
# single -race pass can miss them (this project has twice found real
# bugs only after multi-run repetition — see raft cluster session
# history). Repeat the concurrency-sensitive packages specifically.
go test ./pkg/consensus/... ./pkg/workflow/... ./pkg/platform/audit/... -race -count=5

echo
echo "ALL CHECKS PASSED."
echo
echo "NOT run by this script (require network access this sandbox blocks"
echo "to proxy.golang.org / package registries — see repo README):"
echo "  - golangci-lint (would need to be vendored or fetched)"
echo "  - govulncheck / Go vulnerability database lookups"
echo "  - SPIRE / SPIFFE identity attestation (needs a running SPIRE server)"
echo "  - OPA policy bundle validation (needs the opa binary)"
echo "  - gosec static analysis"
