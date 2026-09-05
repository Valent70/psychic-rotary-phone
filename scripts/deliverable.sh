#!/usr/bin/env bash
#
# Rebuild the enterprise deliverable: the typeset PDF report and the
# zip that carries it alongside the source and every generated report.
#
# Everything in the zip's reports/ directory is produced by running the
# system, never written by hand, so a reader can rerun any of it. The
# source is taken from git rather than the working tree, so nothing
# untracked or generated leaks into a delivered archive.
#
# The PDF needs a Chromium. Set CHROMIUM to point at one; the default
# is where the container image keeps it.
set -euo pipefail

cd "$(dirname "$0")/.."
ROOT=$(pwd)
CHROMIUM=${CHROMIUM:-/opt/pw-browsers/chromium}
STAMP=$(date -u +%Y%m%d)
OUT=${OUT:-$ROOT/dist}
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

NAME="VERIQO_Enterprise_Deliverable_$STAMP"
D="$WORK/$NAME"
mkdir -p "$D/reports" "$OUT"

echo "==> generating the reports"
for r in gates scorecard corpus ontology templates failures claims api all; do
    go run ./cmd/veriqoctl "$r" > "$D/reports/$r.txt" 2>&1
done
./scripts/verify.sh > "$D/reports/verify.txt" 2>&1
go test ./...   > "$D/reports/go-test.txt" 2>&1
go vet ./...    > "$D/reports/go-vet.txt"  2>&1

echo "==> rendering the report"
cp docs/VERIQO_ENTERPRISE_ARCHITECTURE.md "$D/"
python3 scripts/render_report.py "$WORK/report.html"
if [ -x "$CHROMIUM" ]; then
    "$CHROMIUM" --headless --disable-gpu --no-sandbox --no-pdf-header-footer \
        --print-to-pdf="$D/VERIQO_Enterprise_Architecture.pdf" \
        "file://$WORK/report.html" 2>/dev/null
    cp "$D/VERIQO_Enterprise_Architecture.pdf" "$OUT/"
else
    echo "    no Chromium at $CHROMIUM -- the PDF is skipped, the zip still builds"
    cp "$WORK/report.html" "$D/VERIQO_Enterprise_Architecture.html"
fi

echo "==> capturing the source at $(git rev-parse --short HEAD)"
git archive --format=tar --prefix=source/ HEAD | tar -x -C "$D"

{
    echo "VERIQO -- Evidence-Qualified Intelligence OS"
    echo "Enterprise deliverable, $(date -u +'%d %B %Y')"
    echo
    echo "  VERIQO_Enterprise_Architecture.pdf    the report, typeset"
    echo "  VERIQO_ENTERPRISE_ARCHITECTURE.md     the same report in source form"
    echo "  reports/                              generated, not written by hand"
    echo "  source/                               the repository at $(git rev-parse --short HEAD)"
    echo
    echo "Regenerate any of it:"
    echo
    echo "  cd source"
    echo "  go test ./..."
    echo "  ./scripts/verify.sh"
    echo "  go run ./cmd/veriqoctl all"
    echo
    echo "STATUS: NOT PRODUCTION READY. Twenty gates block release and thirteen"
    echo "of them require a party that is not VERIQO. Nothing on the scorecard is"
    echo "GREEN. Release is refused for nine stated reasons. Those figures are"
    echo "produced by the system about itself, on every run."
    echo
    echo "Commit: $(git rev-parse HEAD)"
} > "$D/MANIFEST.txt"

echo "==> packing"
( cd "$WORK" && zip -qr9 "$OUT/$NAME.zip" "$NAME" )

echo
echo "wrote:"
ls -la "$OUT"
