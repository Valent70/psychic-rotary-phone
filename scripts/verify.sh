#!/usr/bin/env bash
#
# VERIQO verification.
#
# This script states what it ran AND what it could not. The second half
# is the point: a verification script that printed only its successes
# would be the artefact this system exists to refuse.

set -uo pipefail

cd "$(dirname "$0")/.."

# The capsule is built into a temporary directory and removed on exit,
# so a verification run leaves nothing behind for a later run to verify
# by accident.
TMPDIR_CAPSULE=$(mktemp -d)
export TMPDIR_CAPSULE
trap 'rm -rf "$TMPDIR_CAPSULE" "$TMPDIR_CAPSULE.b" "$TMPDIR_CAPSULE.rev"' EXIT

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
# These are graded H1 to H4 in pkg/assurance/honesty. Most of them are
# H1: claim-language screening, defeated by paraphrase. Calling them
# "honesty verification" would be an overclaim about an overclaim
# detector, which is the most self-undermining thing this script could
# do -- so they are called what they are.
echo "overclaim checks -- see 'veriqoctl honesty' for what each one can and cannot catch"
run "the scorecard refuses its own release" bash -c '
    go run ./cmd/veriqoctl scorecard | grep -q "RELEASE NOT PERMITTED"'
run "no gate is satisfied"           bash -c '
    ! go run ./cmd/veriqoctl gates | grep -qE "^ +\*? *G[0-9]+ +SATISFIED"'
run "coverage carries the word ESTIMATE" bash -c '
    go run ./cmd/veriqoctl corpus | grep -q "ESTIMATE"'
run "the self-doubt register states who attacked" bash -c '
    go run ./cmd/veriqoctl claims | grep -q "weakest form of survival"'
run "nothing is above INTERNALLY_ASSURED" bash -c '
    ! go run ./cmd/veriqoctl assurance | grep -qE "EXTERNALLY_(TESTED|VALIDATED)$|OPERATIONALLY_PROVEN$|PRODUCTION_QUALIFIED$"'
run "every mandatory gate rests on VERIQO alone" bash -c '
    go run ./cmd/veriqoctl assurance | grep -q "20 mandatory gate(s) rest entirely on VERIQO"'
run "readiness offers no aggregate figure" bash -c '
    ! go run ./cmd/veriqoctl readiness | grep -q "%"'
run "every readiness status names its blocking party" bash -c '
    out=$(go run ./cmd/veriqoctl readiness) || exit 1
    printf "%s" "$out" | grep -q "SECURITY         -> PENDING_EXTERNAL" &&
    printf "%s" "$out" | grep -q "LEGAL            -> PENDING_COUNSEL" &&
    printf "%s" "$out" | grep -q "DATA_RIGHTS      -> PENDING_PARTNER" &&
    printf "%s" "$out" | grep -q "PRODUCTION       -> NOT_QUALIFIED"'
run "nothing remaining is movable by the builder alone" bash -c '
    go run ./cmd/veriqoctl readiness | grep -q "Nothing remaining is movable by the builder alone"'
run "every evidence debt has an owner and a risk" bash -c '
    out=$(go run ./cmd/veriqoctl debt) || exit 1
    o=$(printf "%s" "$out" | grep -c "owner:")
    r=$(printf "%s" "$out" | grep -c "risk:")
    [ "$o" -ge 11 ] && [ "$o" -eq "$r" ]'

echo
echo "the auditor capsule, checked by the independent verifier"
run "the capsule builds" bash -c '
    rm -rf "$TMPDIR_CAPSULE" && go run ./cmd/veriqoctl capsule "$TMPDIR_CAPSULE" >/dev/null'
run "the capsule is byte-identical between builds" bash -c '
    rm -rf "$TMPDIR_CAPSULE.b" && go run ./cmd/veriqoctl capsule "$TMPDIR_CAPSULE.b" >/dev/null
    diff -r "$TMPDIR_CAPSULE" "$TMPDIR_CAPSULE.b" >/dev/null'
run "veriqo-verify passes every step on the capsule" bash -c '
    echo "[]" > "$TMPDIR_CAPSULE.rev"
    out=$(go run ./cmd/veriqo-verify -revocations "$TMPDIR_CAPSULE.rev" "$TMPDIR_CAPSULE" 2>&1) || {
        printf "%s\n" "$out"; exit 1; }
    ! printf "%s" "$out" | grep -q "UNVERIFIABLE"'
run "the capsule claims exactly INTERNALLY_ASSURED" bash -c '
    go run ./cmd/veriqo-verify "$TMPDIR_CAPSULE" 2>&1 |
        grep -q "derived qualification: INTERNALLY_ASSURED"'
run "the verifier states what it cannot establish" bash -c '
    go run ./cmd/veriqo-verify "$TMPDIR_CAPSULE" 2>&1 |
        grep -q "key authenticity cannot be established"'
run "capsule verification is not platform qualification" bash -c '
    out=$(go run ./cmd/veriqo-verify "$TMPDIR_CAPSULE" 2>&1)
    printf "%s" "$out" | grep -q "It is NOT a qualification of VERIQO" &&
    printf "%s" "$out" | grep -q "NOT EXTERNALLY QUALIFIED"'

echo
echo "the checks on the checks"
run "no honesty check is described above its level" bash -c '
    go run ./cmd/veriqoctl honesty >/dev/null'
run "the check suite does not reach H5" bash -c '
    go run ./cmd/veriqoctl honesty | grep -q "Not performed at all: INDEPENDENT_EXTERNAL_REVIEW"'
run "the levels are grouped, never counted as a fraction" bash -c '
    out=$(go run ./cmd/veriqoctl honesty) || exit 1
    printf "%s" "$out" | grep -q "INTERNAL CLAIM SCREENING" &&
    printf "%s" "$out" | grep -q "EXTERNAL CLAIM VALIDATION" &&
    ! printf "%s" "$out" | grep -qE "[0-9]/[0-9]|[0-9] of [0-9]"'
run "the epistemic firewall states its four inequalities" bash -c '
    go run ./cmd/veriqoctl firewall | grep -q "unreadable != verified"'
run "the three metric registers are not combined" bash -c '
    out=$(go run ./cmd/veriqoctl metrics) || exit 1
    printf "%s" "$out" | grep -q "no total below" &&
    printf "%s" "$out" | grep -q "The assurance register is EMPTY"'

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
skip "independent canonicaliser"  "veriqo-verify used VERIQO's own JCS; a defect in it is invisible (ED-011)"
skip "external anchor check"      "no anchor exists, deliberately (ED-003)"
skip "independent red team"       "requires a team that did not write the defence (gates G15-G18)"
skip "H5 external review of claims" "no party outside VERIQO has read any claim in this repository"

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
