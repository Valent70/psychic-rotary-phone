#!/usr/bin/env bash
# scripts/sbom.sh — PHASE 31/32: emit a minimal CycloneDX-shaped SBOM.
#
# VERIQO has zero external module dependencies, so its SBOM is short by
# construction. It is emitted anyway, for two reasons: the release
# certificate commits to its hash, and the day a dependency IS added,
# the SBOM diff makes that visible in review rather than invisible.
set -euo pipefail
cd "$(dirname "$0")/.."

MODULE="$(go list -m)"
GOVER="$(go version | awk '{print $3}')"
COMMIT="$(git rev-parse HEAD 2>/dev/null || echo unknown)"
DEPS="$(go list -m all | tail -n +2)"

echo "{"
echo "  \"bomFormat\": \"CycloneDX\","
echo "  \"specVersion\": \"1.5\","
echo "  \"metadata\": {"
echo "    \"component\": { \"type\": \"application\", \"name\": \"$MODULE\", \"version\": \"${VERIQO_VERSION:-$(cat VERSION)}\" },"
echo "    \"properties\": ["
echo "      { \"name\": \"go.version\", \"value\": \"$GOVER\" },"
echo "      { \"name\": \"vcs.commit\", \"value\": \"$COMMIT\" }"
echo "    ]"
echo "  },"
echo "  \"components\": ["
FIRST=1
if [ -n "$DEPS" ]; then
  while IFS= read -r line; do
    NAME="$(echo "$line" | awk '{print $1}')"
    VER="$(echo "$line" | awk '{print $2}')"
    [ -z "$NAME" ] && continue
    if [ $FIRST -eq 0 ]; then echo "    ,"; fi
    FIRST=0
    echo "    { \"type\": \"library\", \"name\": \"$NAME\", \"version\": \"$VER\" }"
  done <<< "$DEPS"
fi
echo "  ]"
echo "}"
