#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
#
# Generates details_html_golden.json.gz: the HTML of the score details that
# the original CMS renders (ScoreType.get_html_details with the TEMPLATE of
# Sum and of the group score types, cms/grading/scoretypes) for the details
# of upstream_golden.json and some handmade ones, at every feedback level,
# in a few languages. details_html_test.go compares RenderDetails with it.
#
# It needs CMS installed (it imports cms.grading.scoretypes and
# cms.locale) and its configuration:
#
#   CMS_CONFIG=/path/to/cms.toml /path/to/cms-venv/bin/python -I \
#       gen_details_html.py upstream_golden.json | gzip -9 -n > details_html_golden.json.gz

import json
import sys

from cms.grading.scoretypes import get_score_type_class
from cms.locale import get_translations

LANGS = ["en", "es", "it", "ja", "ar", "fr", "uk"]

translations = get_translations()

with open(sys.argv[1]) as f:
    upstream = json.load(f)


def tc(idx, outcome=None, text=None, time=None, memory=None, **kw):
    d = {"idx": idx}
    if outcome is not None:
        d["outcome"] = outcome
        d["text"] = text if text is not None else ["Output is correct"]
        d["time"] = time
        d["memory"] = memory
    d.update(kw)
    return d


# Handmade details: what CMS stores, including values written by older
# versions (missing keys, nulls) and text that needs escaping.
handmade = [
    ("Sum", "Sum/handmade-mixed", 10, {"a": True, "b": False, "c": True}, [
        tc("a", "Correct", ["Output is correct"], 0.0123, 1048576),
        {"idx": "b"},
        tc("c", "Not correct", ["Execution killed with signal %s", "11"], None, None),
        tc("<d>", "Partially correct", ["Output is partially correct", "x"], 1.5, 300 << 20),
        {"idx": "e", "outcome": "Correct", "text": ["Output is correct"]},
        tc("f", "Not correct", ["Evaluation didn't produce file %s", "<out & \"x\">"], 0.5, 1),
        tc("g", "Not correct", [], 0.5, 1),
        tc("h", "Not correct", [""], 0.5, 1),
    ]),
    ("Sum", "Sum/handmade-no-timing", 10, {"a": True}, [
        {"idx": "a", "outcome": "Correct", "text": ["Output is correct"]},
        {"idx": "b", "outcome": "Not correct", "text": ["Execution timed out"]},
        {"idx": "c"},
    ]),
    ("GroupMin", "GroupMin/handmade", [[30, 2], [70, 2]], {"1": True, "2": True, "3": True, "4": True}, [
        {"idx": 0, "score_fraction": 1.0, "score": 30, "max_score": 30, "testcases": [
            tc("1", "Correct", ["Output is correct"], 0.25, 2048,
               time_limit=1.0, time_limit_was_exceeded=False,
               show_in_restricted_feedback=True, show_in_oi_restricted_feedback=True),
            tc("2", "Correct", ["Output is correct"], None, None,
               time_limit=None, time_limit_was_exceeded=False,
               show_in_restricted_feedback=True, show_in_oi_restricted_feedback=False),
        ]},
        {"idx": 1, "score_fraction": 0.0, "score": 0, "max_score": 70, "testcases": [
            tc("3", "Not correct", ["Execution timed out"], 1.2, 4096,
               time_limit=1.0, time_limit_was_exceeded=True,
               show_in_restricted_feedback=True, show_in_oi_restricted_feedback=True),
            tc("4<x>", "Not correct", ["Execution killed with signal %s", "9"], 0.1, None,
               time_limit=1.0, time_limit_was_exceeded=False,
               show_in_restricted_feedback=False, show_in_oi_restricted_feedback=False),
            {"idx": "5"},
        ]},
        {"idx": 2, "score_fraction": 0.5, "score": 17.5, "max_score": 35, "testcases": [
            tc("6", "Partially correct", ["Output is partially correct"], 0.3, 5000,
               show_in_restricted_feedback=True),
            {"idx": "7", "outcome": "Correct", "text": None, "show_in_restricted_feedback": True,
             "show_in_oi_restricted_feedback": True},
        ]},
        {"idx": 3, "score_fraction": 0.25, "score": 0.1234567, "max_score": 0.5, "testcases": []},
    ]),
]

cases = []


def add(name, st_name, params, public, details, kind):
    cls = get_score_type_class(st_name)
    st = cls(params, public, 2)
    levels = ["full", "restricted", "oi_restricted"]
    for level in levels:
        # Every language for the handmade details, English for the others.
        for lang in LANGS if "/handmade" in name else ["en"]:
            html = st.get_html_details(details, level, translation=translations[lang])
            cases.append({
                "name": "%s/%s/%s/%s" % (name, kind, level, lang),
                "score_type": st_name,
                "details": details,
                "feedback_level": level,
                "lang": lang,
                "html": html,
            })


for c in upstream:
    if c.get("error") or "details" not in c:
        continue
    params = c["parameters"]
    public = c["public"]
    add(c["name"], c["score_type"], params, public, c["details"], "details")
    add(c["name"], c["score_type"], params, public, c["public_details"], "public")

for st_name, name, params, public, details in handmade:
    add(name, st_name, params, public, details, "details")

# Not a list (CMS renders the dict as it is for Sum; the group types
# fail), and null.
for st_name, params, public in (("Sum", 10, {"a": True}), ("GroupMin", [[100, 1]], {"a": True})):
    for details in (None,):
        add(st_name + "/null", st_name, params, public, details, "details")

json.dump(cases, sys.stdout, indent=0, ensure_ascii=False, sort_keys=True)
sys.stdout.write("\n")
