#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
#
# Generates upstream_golden.json: the output of the score types of the
# original CMS (cms/grading/scoretypes, commit 1914a5a) on the cases below.
# The Go tests compare their results with it. It loads abc.py, Sum.py,
# GroupMin.py, GroupMul.py and GroupThreshold.py from a CMS checkout with
# stubs for the modules they import (database, templates, translations),
# so no CMS dependency has to be installed:
#
#   python3 -I gen_golden.py /path/to/cms > upstream_golden.json

import json
import os
import re
import sys

CMS = sys.argv[1]
D = os.path.join(CMS, "cms", "grading", "scoretypes")


class _Msg:
    def __init__(self, m):
        self.message = m


class _Messages:
    def get(self, k):
        return {"timeout": _Msg("Execution timed out")}[k]


class _Template:
    def render(self, **kw):
        return ""


class _Env:
    def from_string(self, s):
        return _Template()


def load(name, g):
    src = open(os.path.join(D, name)).read()
    src = re.sub(r"^from cms import \([^)]*\)\n", "", src, flags=re.M)
    src = re.sub(r"^from (cms|jinja2|\.)\S* import .*\n", "", src, flags=re.M)
    exec(compile(src, name, "exec"), g)


BASE = dict(
    FEEDBACK_LEVEL_FULL="full", FEEDBACK_LEVEL_RESTRICTED="restricted",
    FEEDBACK_LEVEL_OI_RESTRICTED="oi_restricted",
    SubmissionResult=object, EVALUATION_MESSAGES=_Messages(),
    Translation=object, DEFAULT_TRANSLATION=None,
    GLOBAL_ENVIRONMENT=_Env(), Template=object, __name__="abc",
)
load("abc.py", BASE)
CLASSES = {}
for n in ["Sum", "GroupMin", "GroupMul", "GroupThreshold"]:
    g = dict(BASE, __name__=n)
    load(n + ".py", g)
    CLASSES[n] = g[n]


class Obj:
    def __init__(self, **kw):
        self.__dict__.update(kw)


def submission_result(case):
    dataset = Obj(time_limit=case.get("time_limit"))
    evs = [Obj(codename=e["codename"], outcome=e["outcome"], text=e["text"],
               execution_time=e.get("time"), execution_memory=e.get("memory"),
               dataset=dataset)
           for e in case["evaluations"]]
    evaluated = case.get("evaluated", True)
    return Obj(evaluated=lambda: evaluated, evaluations=evs,
               submission=Obj(task=Obj(score_precision=case["precision"])))


def run(case):
    out = dict(case)
    try:
        st = CLASSES[case["score_type"]](case["parameters"], case["public"],
                                         case["precision"])
    except ValueError as e:
        out["error"] = "new"
        return out
    out["max_score"] = st.max_score
    out["max_public_score"] = st.max_public_score
    out["ranking_headers"] = st.ranking_headers
    if "evaluations" not in case:
        return out
    try:
        res = st.compute_score(submission_result(case))
    except Exception:
        out["error"] = "compute"
        return out
    res = json.loads(json.dumps(res))
    out["score"], out["details"], out["public_score"], \
        out["public_details"], out["ranking_details"] = res
    for which in ["details", "public_details"]:
        for level in ["full", "restricted", "oi_restricted"]:
            details = json.loads(json.dumps(out[which]))
            try:
                v = st.get_json_details(details, level)
            except KeyError:
                # CMS fails on the public details of a subtask with private
                # testcases (it has no score); the Go port leaves the
                # missing keys out. Emulate that: fill them with a marker
                # and remove it from the result.
                out["json_%s_%s_fixed" % (which, level)] = True
                for sub in details:
                    for k in SUBTASK_SCORE_KEYS:
                        sub.setdefault(k, MISSING)
                v = st.get_json_details(details, level)
                for sub in v:
                    for k in SUBTASK_SCORE_KEYS:
                        if sub[k] == MISSING:
                            del sub[k]
            out["json_%s_%s" % (which, level)] = v
    return out


SUBTASK_SCORE_KEYS = ["score_fraction", "score", "max_score"]
MISSING = "<missing>"


def ev(codename, outcome, text=("Output is correct",), time=0.5,
       memory=1048576):
    return {"codename": codename, "outcome": outcome, "text": list(text),
            "time": time, "memory": memory}


TO = ("Execution timed out",)
WALL = ("Execution timed out (wall clock limit exceeded)",)
WA = ("Output isn't correct",)

PUB7 = {"0_0": True, "1_0": True, "1_1": True, "2_0": True, "2_1": False,
        "3_0": False, "3_1": False}
PUB4 = {"0": False, "1": True, "2": False, "3": False}
PUB_ALL = {"a": True, "b": True, "c": True, "d": True, "e": True}


def evs7(**outcomes):
    out = []
    for c in sorted(PUB7):
        o = outcomes.get("t" + c, "1.0")
        text = TO if o == "TO" else WA if o == "0.0" else \
            ("Output is partially correct",) if o not in ("1.0",) else \
            ("Output is correct",)
        out.append(ev(c, "0.0" if o == "TO" else o, text))
    return out


CASES = []


def case(name, **kw):
    kw["name"] = name
    CASES.append(kw)


GROUP4 = [[0, "0_*"], [10.5, "1_*"], [30.5, "2_*"], [59, "3_*"]]
for st in ["GroupMin", "GroupMul"]:
    case(st + "/all-correct", score_type=st, parameters=GROUP4, public=PUB7,
         precision=2, time_limit=1.0, evaluations=evs7())
    case(st + "/partial", score_type=st, parameters=GROUP4, public=PUB7,
         precision=2, time_limit=1.0,
         evaluations=evs7(t1_0="0.0", t1_1="0.0", t3_0="0.5", t3_1="0.1"))
    case(st + "/timeouts", score_type=st, parameters=GROUP4, public=PUB7,
         precision=4, time_limit=2.5,
         evaluations=evs7(t1_1="TO", t2_0="0.3", t2_1="TO"))
    case(st + "/counts", score_type=st,
         parameters=[[0, 1], [10.5, 2], [30.5, 2], [59, 2]], public=PUB7,
         precision=1, time_limit=None,
         evaluations=evs7(t1_0="0.25", t2_1="0.0"))
    case(st + "/not-evaluated", score_type=st, parameters=GROUP4,
         public=PUB7, precision=2, evaluated=False, evaluations=[])

case("GroupMin/restricted-order", score_type="GroupMin",
     parameters=[[100, "."]], public=PUB_ALL, precision=0, time_limit=1,
     evaluations=[ev("a", "1.0"), ev("b", "0.5", ("Partial",)),
                  ev("c", "0.25", ("Partial",)), ev("d", "0.25", ("Partial",)),
                  ev("e", "1.0")])
case("GroupMin/restricted-first-lowest-first", score_type="GroupMin",
     parameters=[[100, "."]], public=PUB_ALL, precision=0, time_limit=1,
     evaluations=[ev("a", "0.0", WA), ev("b", "0.0", WA), ev("c", "1.0"),
                  ev("d", "0.0", TO), ev("e", "1.0")])
case("GroupMin/list-unsorted", score_type="GroupMin",
     parameters=[[30, ["e", "c", "a"]], [70, ["d", "b"]]],
     public={"a": True, "b": False, "c": True, "d": True, "e": True},
     precision=2, time_limit=1,
     evaluations=[ev("a", "0.5", ("Partial",)), ev("b", "1.0"),
                  ev("c", "0.25", ("Partial",)), ev("d", "0.0", WALL),
                  ev("e", "0.75", ("Partial",), time=None, memory=None)])
case("GroupMin/dict-params", score_type="GroupMin",
     parameters=[{"max_score": 40, "testcases": "1_.*"},
                 {"max_score": 60, "testcases": "[23]_.*",
                  "always_show_testcases": True}],
     public=PUB7, precision=2, time_limit=1,
     evaluations=evs7(t1_0="0.0", t2_0="0.5", t3_1="0.0"))
case("GroupMin/dict-always-show-all-public", score_type="GroupMin",
     parameters=[{"max_score": 40, "testcases": "1_.*",
                  "always_show_testcases": True},
                 {"max_score": 60, "testcases": "0_.*"}],
     public=PUB7, precision=2, time_limit=1,
     evaluations=evs7(t1_0="0.5", t0_0="0.0"))
case("GroupMin/rounding", score_type="GroupMin",
     parameters=[[33.33, 2], [33.33, 2], [33.34, 3]], public=PUB7,
     precision=2, time_limit=1,
     evaluations=evs7(t0_0="0.333333", t1_0="0.6666667", t2_1="0.125",
                      t3_0="0.12345"))
case("GroupMin/rounding-half-even", score_type="GroupMin",
     parameters=[[1, 1], [1, 1], [1, 1], [1, 1], [1, 1], [1, 1], [1, 1]],
     public=PUB7, precision=2, time_limit=1,
     evaluations=evs7(t0_0="0.125", t1_0="0.375", t1_1="2.675",
                      t2_0="0.005", t2_1="0.015", t3_0="0.335",
                      t3_1="0.30000000000000004"))
case("GroupMin/precision-zero", score_type="GroupMin",
     parameters=[[50, 3], [50, 4]], public=PUB7, precision=0, time_limit=1,
     evaluations=evs7(t0_0="0.5", t2_0="0.33"))
case("GroupMin/subtask-score-headers", score_type="GroupMin",
     parameters=[[0.0001, 1], [1000000, 1], [123456, 1], [1234567, 1],
                 [0.00001, 1], [100, 1], [1e16, 1]],
     public=PUB7, precision=6)

THR = [[0, "0_*", 0], [10.5, "1_*", 10], [30.5, "2_*", 20],
       {"max_score": 59, "testcases": "3_*", "threshold": 30}]
case("GroupThreshold/below", score_type="GroupThreshold", parameters=THR,
     public=PUB7, precision=2, time_limit=1,
     evaluations=[ev(c, "5.5") for c in sorted(PUB7)])
case("GroupThreshold/mixed", score_type="GroupThreshold", parameters=THR,
     public=PUB7, precision=2, time_limit=1,
     evaluations=[ev(c, o) for c, o in zip(sorted(PUB7), [
         "0.0", "12.5", "10", "0.0", "20", "30", "30.5"])])

case("Sum/all-correct", score_type="Sum", parameters=10.5, public=PUB4,
     precision=2, time_limit=1,
     evaluations=[ev(c, "1.0") for c in sorted(PUB4)])
case("Sum/partial", score_type="Sum", parameters=33.333333333333336,
     public=PUB4, precision=0, time_limit=None,
     evaluations=[ev("0", "0.0", WA), ev("1", "0.2", ("Partial",)),
                  ev("2", "1.0", time=None, memory=None),
                  ev("3", "0.0", TO)])
case("Sum/not-evaluated", score_type="Sum", parameters=5, public=PUB4,
     precision=2, evaluated=False, evaluations=[])
case("Sum/no-testcases", score_type="Sum", parameters=5, public={},
     precision=2, evaluations=[])

for name, st, params in [
        ("GroupMin/invalid-int", "GroupMin", [1]),
        ("GroupMin/invalid-scalar", "GroupMin", 1),
        ("GroupMin/invalid-empty-item", "GroupMin", [[]]),
        ("GroupMin/invalid-short-item", "GroupMin", [[1]]),
        ("GroupMin/invalid-max-score", "GroupMin", [["a", 10]]),
        ("GroupMin/invalid-testcases", "GroupMin", [[100, 1.5]]),
        ("GroupMin/invalid-mixed", "GroupMin", [[40, 10], [40, "1_*"]]),
        ("GroupMin/invalid-no-match", "GroupMin", [[100, "9_*"]]),
        ("GroupMin/invalid-precision", "GroupMin", [[10.555, 2]]),
        ("GroupMin/invalid-dict-missing", "GroupMin", [{"max_score": 1}]),
        ("Sum/invalid-list", "Sum", []),
        ("Sum/invalid-string", "Sum", "1")]:
    case(name, score_type=st, parameters=params, public=PUB7, precision=2)
case("GroupMin/empty", score_type="GroupMin", parameters=[], public=PUB7,
     precision=2, time_limit=1, evaluations=evs7())

# One case per line.
print("[")
print(",\n".join(json.dumps(run(c), separators=(",", ":")) for c in CASES))
print("]")
