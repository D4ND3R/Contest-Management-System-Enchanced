#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
"""Golden plural translations of CMS, for locale/ngettext_test.go.

CMS compiles its catalogs with Babel (setup.py: compile_catalog), which
writes untranslated plural entries into the .mo with msgid and
msgid_plural as their forms, and Python's gettext evaluates the plural
expression on whatever number it gets (format_token_rules passes floats
and booleans). This script records what CMS shows for every plural msgid
of cms.pot, every language and a set of counts.

usage: python cms_ngettext.py CMS_LOCALE_DIR CMS_POT OUT.json
  CMS_LOCALE_DIR: cms/locale of an installed CMS (with the compiled .mo)
  run with the Python of that installation (Babel 2.12.1).
"""

import json
import os
import sys
import warnings

import babel.support
from babel.messages.pofile import read_po

COUNTS = [0, 0.5, 1, 1.5, 2, 3, 5, 11, 21, 101, False, True]


def main():
    locale_dir, pot, out = sys.argv[1:4]
    with open(pot, "rb") as f:
        catalog = read_po(f)
    plurals = [m.id for m in catalog if m.id and m.pluralizable]
    result = {"counts": [repr(n) for n in COUNTS], "languages": {}}
    for lang in sorted(os.listdir(locale_dir)):
        mo = os.path.join(locale_dir, lang, "LC_MESSAGES", "cms.mo")
        if not os.path.exists(mo):
            continue
        with open(mo, "rb") as f:
            t = babel.support.Translations(f, domain="cms")
        table = {}
        for one, many in plurals:
            with warnings.catch_warnings():
                warnings.simplefilter("ignore", DeprecationWarning)
                table[one] = [t.ngettext(one, many, n) for n in COUNTS]
        result["languages"][lang] = table
    with open(out, "w") as f:
        json.dump(result, f, ensure_ascii=False, indent=0, sort_keys=True)
        f.write("\n")


if __name__ == "__main__":
    main()
