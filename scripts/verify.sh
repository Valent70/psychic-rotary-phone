#!/usr/bin/env bash
#
# VERIQO verification.
#
# This script states what it ran AND what it could not. The second half
# is the point: a verification script that printed only its successes
# would be the artefact this system exists to refuse.

set -uo pipefail

cd "$(dirname "$0")/.."

pass=0
fail=0
skipped=()

run() {
    local name="$1"; shift
    printf '  %-42s ' "$name"
    if out=$("$@" 2>&1); then
        printf 'PASS\n'
        pass=$((pass + 1))
    else
        printf 'FAIL\n'
        printf '%s\n' "$out" | sed 's/^/      /'
        fail=$((fail + 1))
    fi
}

skip() {
    skipped+=("$1 -- $2")
}

echo "VERIQO verification"
echo

echo "build and static analysis"
run "go build ./..."                 go build ./...
run "go vet ./..."                   go vet ./...
run "gofmt (no unformatted files)"   bash -c '[ -z "$(gofmt -l pkg cmd test)" ] || { gofmt -l pkg cmd test; exit 1; }'

echo
echo "tests"
run "go test ./..."                  go test ./...
run "go test -race ./pkg/ledger/"    go test -race -count=1 ./pkg/ledger/
run "go test -race ./pkg/resilience/" go test -race -count=1 ./pkg/resilience/
run "go test -race ./pkg/graph/"     go test -race -count=1 ./pkg/graph/

echo
echo "determinism (the same inputs must produce the same bytes)"
run "corpus run is byte-identical"   bash -c '
    a=$(go run ./cmd/veriqoctl corpus) || exit 1
    b=$(go run ./cmd/veriqoctl corpus) || exit 1
    [ "$a" = "$b" ]'
run "every report is byte-identical" bash -c '
    a=$(go run ./cmd/veriqoctl all) || exit 1
    b=$(go run ./cmd/veriqoctl all) || exit 1
    [ "$a" = "$b" ]'

echo
echo "honesty checks (these fail if the system starts overstating itself)"
run "the scorecard refuses its own release" bash -c '
    go run ./cmd/veriqoctl scorecard | grep -q "RELEASE NOT PERMITTED"'
run "no gate is satisfied"           bash -c '
    ! go run ./cmd/veriqoctl gates | grep -qE "^ +\*? *G[0-9]+ +SATISFIED"'
run "coverage carries the word ESTIMATE" bash -c '
    go run ./cmd/veriqoctl corpus | grep -q "ESTIMATE"'
run "the self-doubt register states who attacked" bash -c '
    go run ./cmd/veriqoctl claims | grep -q "weakest form of survival"'

# --- What this script did NOT run ---------------------------------------
#
# Each of these needs something this environment does not have. They are
# listed rather than silently omitted, because a reader comparing this
# output against a production claim needs to know the difference.

skip "golangci-lint"          "not installed in this environment"
skip "govulncheck"            "requires the vulnerability database (gate G8)"
skip "gosec"                  "not installed in this environment"
skip "SBOM generation"        "no signing or attestation service configured (gate G19)"
skip "SPIRE/SPIFFE attestation" "no SPIRE deployment (gate G7)"
skip "OPA bundle validation"  "no OPA bundle configured"
skip "72-hour soak"           "the longest run here is seconds (gate G6)"
skip "multi-host qualification" "concurrency is exercised in-process only (gate G5)"
skip "multi-region failover"  "single-region environment (gate G3)"
skip "disaster recovery timing" "no backup target configured (gate G11)"
skip "independent penetration test" "requires a firm that is not VERIQO (gate G4)"
skip "external document corpus" "VERIQO_CORPUS_DIR is empty (gate G9)"

echo
echo "NOT RUN in this environment:"
for s in "${skipped[@]}"; do
    echo "  - $s"
done

echo
echo "-------------------------------------------------------------------"
printf '%d passed, %d failed, %d not run.\n' "$pass" "$fail" "${#skipped[@]}"
echo
echo "Passing this script means the code builds, the tests hold and the"
echo "system still refuses to overstate itself. It does not mean VERIQO is"
echo "production ready: run 'veriqoctl gates' for what is missing, and"
echo "'veriqoctl scorecard' for why the release is refused."

[ "$fail" -eq 0 ] || exit 1
