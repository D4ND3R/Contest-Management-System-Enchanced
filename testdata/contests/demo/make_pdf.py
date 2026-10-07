#!/usr/bin/env python3
"""Writes the one-page statement PDFs of the demo contest (no dependencies)."""
import os
import sys

STATEMENTS = {
    "sum": ["A plus B", "Read two integers a and b and print a + b.",
            "Subtask 1 (40 points): |a|, |b| <= 1000.",
            "Subtask 2 (60 points): |a|, |b| <= 10^9."],
    "half": ["Half credit", "Read an integer n and print n / 2 rounded down.",
             "Answers off by one get half the points."],
    "quiz": ["Quiz", "Output-only task: answer each question in output_<n>.txt."],
}


def pdf(lines):
    text = "BT /F1 14 Tf 72 760 Td 18 TL " + " ".join(
        "(%s) Tj T*" % l.replace("\\", "\\\\").replace("(", "\\(").replace(")", "\\)") for l in lines) + " ET"
    objs = [
        "<< /Type /Catalog /Pages 2 0 R >>",
        "<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
        "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R "
        "/Resources << /Font << /F1 5 0 R >> >> >>",
        "<< /Length %d >>\nstream\n%s\nendstream" % (len(text), text),
        "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
    ]
    out = b"%PDF-1.4\n"
    offsets = []
    for i, o in enumerate(objs, 1):
        offsets.append(len(out))
        out += b"%d 0 obj\n%s\nendobj\n" % (i, o.encode())
    xref = len(out)
    out += b"xref\n0 %d\n0000000000 65535 f \n" % (len(objs) + 1)
    for off in offsets:
        out += b"%010d 00000 n \n" % off
    out += b"trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n" % (len(objs) + 1, xref)
    return out


root = os.path.dirname(os.path.abspath(sys.argv[0]))
for task, lines in STATEMENTS.items():
    with open(os.path.join(root, task, "statement", "statement.pdf"), "wb") as f:
        f.write(pdf(lines))
