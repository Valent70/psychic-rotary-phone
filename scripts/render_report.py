#!/usr/bin/env python3
"""Render the architecture report to a print-ready HTML page.

Called by scripts/deliverable.sh, which turns the result into a PDF.
Kept separate so the styling can be changed without touching the shell.
"""
import markdown, pathlib, sys, html

src = pathlib.Path("docs/VERIQO_ENTERPRISE_ARCHITECTURE.md").read_text()
body = markdown.markdown(src, extensions=["tables", "fenced_code", "attr_list"])

css = """
@page { size: A4; margin: 20mm 18mm 18mm 18mm; }
* { box-sizing: border-box; }
body { font-family: "DejaVu Serif", Georgia, "Times New Roman", serif;
       font-size: 10.2pt; line-height: 1.52; color: #14181d; margin: 0; }
h1 { font-family: "DejaVu Sans", Helvetica, Arial, sans-serif;
     font-size: 27pt; letter-spacing: -0.02em; margin: 0 0 2mm 0; color: #0b1015; }
h2 { font-family: "DejaVu Sans", Helvetica, Arial, sans-serif;
     font-size: 14pt; margin: 9mm 0 3mm 0; padding-bottom: 1.6mm;
     border-bottom: 1.6px solid #0b1015; color: #0b1015;
     break-after: avoid; page-break-after: avoid; }
h3 { font-family: "DejaVu Sans", Helvetica, Arial, sans-serif;
     font-size: 11pt; margin: 6mm 0 2mm 0; color: #22303d;
     break-after: avoid; page-break-after: avoid; }
h1 + h2 { margin-top: 4mm; border-bottom: none; font-size: 12.5pt;
          font-weight: 500; color: #46545f; }
p { margin: 0 0 3mm 0; text-align: justify; hyphens: auto; }
strong { color: #000; }
em { color: #2c3b47; }
hr { border: 0; border-top: 0.7px solid #c4ccd3; margin: 7mm 0; }
table { border-collapse: collapse; width: 100%; margin: 3mm 0 5mm 0;
        font-size: 8.9pt; break-inside: avoid; page-break-inside: avoid; }
th { background: #0b1015; color: #fff; text-align: left; padding: 1.9mm 2.4mm;
     font-family: "DejaVu Sans", Helvetica, sans-serif; font-size: 8.4pt;
     font-weight: 600; letter-spacing: 0.02em; }
td { padding: 1.7mm 2.4mm; border-bottom: 0.6px solid #dde3e8; vertical-align: top; }
tr:nth-child(even) td { background: #f6f8fa; }
code { font-family: "DejaVu Sans Mono", "Courier New", monospace; font-size: 8.6pt;
       background: #eef2f5; padding: 0.4mm 1mm; border-radius: 2px; color: #16222c; }
pre { background: #0d1319; color: #d7e2ea; padding: 3.5mm 4mm; border-radius: 3px;
      overflow-x: auto; font-size: 7.9pt; line-height: 1.42; margin: 3mm 0 5mm 0;
      break-inside: avoid; page-break-inside: avoid; }
pre code { background: none; color: inherit; padding: 0; font-size: inherit; }
ul, ol { margin: 0 0 3mm 0; padding-left: 6mm; }
li { margin-bottom: 1.4mm; }
"""

out = f"""<!doctype html><html><head><meta charset="utf-8">
<title>VERIQO Enterprise Architecture</title><style>{css}</style></head>
<body>{body}</body></html>"""
dest = sys.argv[1] if len(sys.argv) > 1 else "report.html"
pathlib.Path(dest).write_text(out)
print("wrote", dest, len(out), "bytes")
