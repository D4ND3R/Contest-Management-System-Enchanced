#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
#
# The lookups reproduced here are those of cmscommon/mimetypes.py and
# Translation.translate_mimetype (cms/locale/locale.py) of CMS:
# Copyright © 2012 Luca Wehrstedt <luca.wehrstedt@gmail.com>
# Copyright © 2010-2015 Stefano Maggiolo <s.maggiolo@gmail.com>

"""Generate internal/web/mimedata/mime.json and its golden test data.

CMS guesses the MIME type of attachments and submitted files from their
names, picks a generic icon for it and names it in the interface language
through the freedesktop.org shared-mime-info database of the host (pyxdg).
This script dumps what those functions read, as CMS itself computes it, so
the Go port (internal/web/mime.go) needs no database at runtime:

  - the glob tables of pyxdg's GlobDB (literal names, extensions, other
    globs), already resolved to canonical types and to the first match
    pyxdg would return;
  - the aliases and the generic icons of the reachable types;
  - the comment of every reachable type in each CMS interface language
    (only where it differs from English).

It also writes internal/web/testdata/mime_golden.json with the outputs of
CMS's own functions for a list of file names, which the Go tests compare.

Run it with the Python of a CMS installation (pyxdg, babel, cms), with
CMS_CONFIG pointing to any valid cms.toml:

    CMS_CONFIG=/path/cms.toml /path/to/cms/venv/bin/python \
        internal/web/mimedata/extract.py

The output is committed; it is not part of `make generate`.
"""

import fnmatch
import json
import pathlib
import re
import sys

import xdg.Mime

from cmscommon import mimetypes as cms_mimetypes
from cms.locale import get_translations

REPO = pathlib.Path(__file__).resolve().parents[3]
OUT = REPO / "internal" / "web" / "mimedata" / "mime.json"
GOLDEN = REPO / "internal" / "web" / "testdata" / "mime_golden.json"


def first(entries):
    mt, _weight = entries[0]
    return str(mt)


def main():
    xdg.Mime.update_cache()
    g = xdg.Mime.globs
    data = {
        "cased_literals": {k: str(v[0]) for k, v in sorted(g.cased_literals.items())},
        "literals": {k: str(v[0]) for k, v in sorted(g.literals.items())},
        "cased_exts": {k: first(v) for k, v in sorted(g.cased_exts.items()) if v},
        "exts": {k: first(v) for k, v in sorted(g.exts.items()) if v},
        # pyxdg compiles the remaining globs with fnmatch, case-insensitive
        # unless the glob is case-sensitive, in this order.
        "globs": [],
    }
    original = {}
    for mtype, globs in g.allglobs.items():
        for _weight, pattern, flags in globs:
            cased = "cs" in flags
            original[(fnmatch.translate(pattern), cased)] = pattern
    for regex, mt, _weight in g.globs:
        cased = not (regex.flags & re.I)
        data["globs"].append([original[(regex.pattern, cased)], cased, str(mt)])

    types = set()
    for table in ("cased_literals", "literals", "cased_exts", "exts"):
        types.update(data[table].values())
    types.update(mt for _, _, mt in data["globs"])

    aliases = {}
    for alias, target in sorted(xdg.Mime.aliases.items()):
        aliases[alias] = target
    data["aliases"] = aliases

    icons = {}
    for t in sorted(types | set(aliases)):
        icon = cms_mimetypes.get_icon_for_type(t)
        media = t.split("/")[0]
        if icon != media + "-x-generic":
            icons[t] = icon
    data["icons"] = icons

    translations = get_translations()
    english = translations["en"]
    comments = {}
    for t in sorted(types):
        entry = {"": english.translate_mimetype(t)}
        for code, tr in sorted(translations.items()):
            if code == "en":
                continue
            c = tr.translate_mimetype(t)
            # The Go side falls back to the base language ("es" for
            # "es-CL") and then to English.
            base = code.split("-")[0]
            fallback = entry.get(base, entry[""]) if base in translations else entry[""]
            if c != fallback:
                entry[code] = c
        comments[t] = entry
    data["comments"] = comments

    OUT.write_text(json.dumps(data, ensure_ascii=False, sort_keys=True, indent=0) + "\n")

    names = ["Makefile", "makefile", "README", "readme.txt", "core", "a.tar.gz",
             "A.TAR.GZ", "noext", ".bashrc", "x.C", "x.c", "x.cpp", "x.cc", "x.cxx",
             "x.h", "x.hpp", "x.PDF", "x.pdf", "x.py", "x.pas", "x.pp", "x.java",
             "x.txt", "x.in", "x.out", "x.ans", "x.zip", "x.ZIP", "x.png", "x.jpg",
             "x.html", "x.md", "x.odt", "x.docx", "x.xlsx", "x.csv", "x.json",
             "x.rs", "x.go", "x.hs", "x.ml", "x.kt", "x.js", "x.php", "x.rb", "x.cs",
             "x.sh", "x.tex", "x.ps", "x.svg", "x.mp3", "x.mp4", "x.ttf", "x.7z",
             "statement.it.pdf", "grader.c", "x.", "x..pdf", "Makefile.am",
             "CMakeLists.txt", "x.desktop", "x~", "#x#", "x.bak", "x.tar.bz2",
             "x.exe", "x.ics", "x.vcf", "x.ppt", "x.eot"]
    explicit = len(names)
    for ext in sorted(data["exts"]):
        names.append("file." + ext)
    for ext in sorted(data["cased_exts"]):
        names.append("file." + ext)
    names.extend(sorted(data["literals"]))
    names.extend(sorted(data["cased_literals"]))
    golden = []
    langs = ["es", "it", "zh-Hant-TW", "ja", "es-CL", "en", "ar"]
    for i, n in enumerate(names):
        mt = cms_mimetypes.get_type_for_file_name(n)
        row = {"name": n, "type": mt}
        if mt is not None:
            row["icon"] = cms_mimetypes.get_icon_for_type(mt)
            if i < explicit:
                row["comments"] = {code: translations[code].translate_mimetype(mt) for code in langs}
        golden.append(row)
    for alias in ["application/x-pdf", "text/x-c", "application/x-zip-compressed", "foo/bar"]:
        golden.append({"name": None, "type": alias,
                       "icon": cms_mimetypes.get_icon_for_type(alias),
                       "comments": {code: translations[code].translate_mimetype(alias) for code in langs}})
    GOLDEN.write_text(json.dumps(golden, ensure_ascii=False, indent=0) + "\n")
    print("types:", len(types), "golden rows:", len(golden), file=sys.stderr)


if __name__ == "__main__":
    main()
