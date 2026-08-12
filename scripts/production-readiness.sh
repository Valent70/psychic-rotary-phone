#!/usr/bin/env bash
# scripts/production-readiness.sh — PHASE 53/54 of the v7.10.3 audit.
#
# This script does NOT decide anything. It builds the SBOM, then hands
# control to cmd/veriqo-readiness, which runs every gate, captures real
# evidence artifacts, and emits READINESS_MANIFEST.json plus a verdict.
#
# The verdict is PRODUCTION_READY only when every mandatory gate passes.
# Several mandatory gates (independent pentest, 100-node benchmark,
# multi-region DR drill, real HSM/KMS, live data feeds, 72h soak, SPIRE
# mTLS, network-dependent supply-chain scanners) are registered as
# BLOCKED because they cannot be evidenced from a build sandbox. This
# script is therefore EXPECTED to exit 1 locally. That is the design:
# a laptop must never be able to declare a distributed trust system
# production-ready.
#
# Usage: ./scripts/production-readiness.sh [--skip-race]
set -uo pipefail
cd "$(dirname "$0")/.."

SKIP_RACE=""
if [ "${1:-}" = "--skip-race" ]; then SKIP_RACE="--skip-race"; fi

echo "== generating SBOM =="
./scripts/sbom.sh > SBOM.json

COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"

echo "== running production readiness gates =="
go run ./cmd/veriqo-readiness \
  --version "${VERIQO_VERSION:-v7.12.0}" \
  --commit "$COMMIT" \
  --operator "${USER:-ci}" \
  --out READINESS_MANIFEST.json \
  --evidence-dir evidence \
  $SKIP_RACE
STATUS=$?

echo
case $STATUS in
  0) echo "RESULT: PRODUCTION_READY" ;;
  2) echo "RESULT: CONDITIONALLY_READY_WITH_WAIVERS (waived mandatory gates exist)" ;;
  *) echo "RESULT: NOT_PRODUCTION_READY (see READINESS_MANIFEST.json)" ;;
esac
exit $STATUS
