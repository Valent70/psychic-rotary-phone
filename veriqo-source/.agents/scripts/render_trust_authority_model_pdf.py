"""Render the Trust Authority Model & Invariant Contract Response report
to a dependency-free, text-based PDF -- same renderer as
render_final_authority_hardening_pdf.py and its predecessors, pointed at
the new source/output paths. This report's source markdown is pure
ASCII, but the desymbolize step is kept for robustness against any
Unicode punctuation that creeps in."""

from pathlib import Path
import re
import textwrap


ROOT = Path(__file__).resolve().parents[2]
SOURCE = ROOT / "docs" / "VERIQO_TRUST_AUTHORITY_MODEL_RESPONSE.md"
OUTPUT = ROOT / "docs" / "VERIQO_TRUST_AUTHORITY_MODEL_RESPONSE.pdf"

SUBSTITUTIONS = {
    "\U0001f7e2": "[GREEN]",
    "\U0001f7e1": "[YELLOW]",
    "\U0001f534": "[RED]",
    "\U0001f7e0": "[ORANGE]",
    "→": "->",
    "↓": "v",
    "≠": "!=",
    "‘": "'",
    "’": "'",
    "“": '"',
    "”": '"',
    "–": "-",
    "—": "--",
}


def desymbolize(text):
    for unicode_char, ascii_equivalent in SUBSTITUTIONS.items():
        text = text.replace(unicode_char, ascii_equivalent)
    return text


def report_lines():
    lines = []
    in_code_block = False
    for raw in SOURCE.read_text(encoding="utf-8").splitlines():
        line = desymbolize(raw.rstrip())
        if line.strip().startswith("```"):
            in_code_block = not in_code_block
            continue
        if in_code_block:
            lines.append(line[:92])
            continue
        line = line.strip()
        if not line:
            lines.append("")
            continue
        line = re.sub(r"!\[([^\]]*)\]\([^)]+\)", r"\1", line)
        line = re.sub(r"\[([^\]]+)\]\([^)]+\)", r"\1", line)
        line = line.replace("**", "").replace("`", "")
        if line.startswith("#"):
            line = line.lstrip("#").strip().upper()
        if line.startswith("|"):
            line = line.replace("|", "  ")
        lines.extend(textwrap.wrap(line, width=92) or [""])
    return lines


def escape(value):
    return value.replace("\\", "\\\\").replace("(", "\\(").replace(")", "\\)")


def make_pdf(lines):
    page_height = 792
    top = 756
    bottom = 48
    leading = 12
    per_page = (top - bottom) // leading
    pages = [lines[i : i + per_page] for i in range(0, len(lines), per_page)]
    objects = []

    def add(value):
        objects.append(value)
        return len(objects)

    pages_id = add(None)
    font_id = add("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
    mono_font_id = add("<< /Type /Font /Subtype /Type1 /BaseFont /Courier >>")
    page_ids = []
    content_ids = []
    for page_lines in pages:
        commands = ["BT", "/F2 8 Tf", f"54 {top} Td"]
        for index, line in enumerate(page_lines):
            if index:
                commands.append(f"0 -{leading} Td")
            commands.append(f"({escape(line)}) Tj")
        commands.append("ET")
        content = "\n".join(commands).encode("latin-1", "replace")
        content_ids.append(add(f"<< /Length {len(content)} >>\nstream\n{content.decode('latin-1')}\nendstream"))
        page_ids.append(add(None))

    kids = " ".join(f"{page_id} 0 R" for page_id in page_ids)
    objects[pages_id - 1] = f"<< /Type /Pages /Kids [{kids}] /Count {len(page_ids)} >>"
    for page_id, content_id in zip(page_ids, content_ids):
        objects[page_id - 1] = (
            f"<< /Type /Page /Parent {pages_id} 0 R /MediaBox [0 0 612 {page_height}] "
            f"/Resources << /Font << /F1 {font_id} 0 R /F2 {mono_font_id} 0 R >> >> "
            f"/Contents {content_id} 0 R >>"
        )
    catalog_id = add(f"<< /Type /Catalog /Pages {pages_id} 0 R >>")

    output = bytearray(b"%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
    offsets = [0]
    for number, value in enumerate(objects, 1):
        offsets.append(len(output))
        output.extend(f"{number} 0 obj\n".encode())
        output.extend(value.encode("latin-1", "replace"))
        output.extend(b"\nendobj\n")
    xref = len(output)
    output.extend(f"xref\n0 {len(objects) + 1}\n".encode())
    output.extend(b"0000000000 65535 f \n")
    for offset in offsets[1:]:
        output.extend(f"{offset:010d} 00000 n \n".encode())
    output.extend(
        f"trailer\n<< /Size {len(objects) + 1} /Root {catalog_id} 0 R >>\n"
        f"startxref\n{xref}\n%%EOF\n".encode()
    )
    return bytes(output), len(pages)


data, page_count = make_pdf(report_lines())
OUTPUT.write_bytes(data)
print(f"wrote {OUTPUT} ({page_count} pages)")
