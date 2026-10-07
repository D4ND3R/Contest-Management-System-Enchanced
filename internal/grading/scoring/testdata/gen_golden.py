#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
#
# Generates upstream_golden.json: task_score() of the original CMS
# (cms/grading/scoring.py, commit 1914a5a) on random submissions, with
# stand-ins for the database objects. The Go tests compare TaskScore with
# it. Run with the Python of the default CMS image (3.12 or later, whose
# sum() is compensated):
#
#   python3 -I gen_golden.py /path/to/cms > upstream_golden.json

import json
import random
import re
import sys
from collections import namedtuple

src = open(sys.argv[1] + "/cms/grading/scoring.py").read()
src = re.sub(r"^from .*\n", "", src, flags=re.M)
src = re.sub(r"^\s+SCORE_MODE.*\n", "", src, flags=re.M)
g = {"namedtuple": namedtuple, "Dataset": object, "Participation": object,
     "Task": object, "Submission": object, "SCORE_MODE_MAX": "max",
     "SCORE_MODE_MAX_SUBTASK": "max_subtask",
     "SCORE_MODE_MAX_TOKENED_LAST": "max_tokened_last"}
exec(src, g)
task_score = g["task_score"]


class Obj:
    def __init__(self, **kw):
        self.__dict__.update(kw)


def details(rng, score):
    kind = rng.random()
    if kind < 0.15:
        return []
    if kind < 0.25:
        return [{"idx": "%02d" % i} for i in range(3)]
    if kind < 0.3:
        # Public details of a subtask with private testcases.
        return [{"idx": 0, "score": round(score, 2), "testcases": []},
                {"idx": 1, "testcases": [{"idx": "a"}]}]
    n = rng.randint(1, 4)
    idx = list(range(n))
    rng.shuffle(idx)
    return [{"idx": i, "score": round(rng.random() * 25, rng.randint(0, 4))}
            for i in idx]


def case(rng):
    subs = []
    for _ in range(rng.randint(0, 7)):
        s = {"timestamp": rng.randint(0, 5), "official": rng.random() < 0.85,
             "tokened": rng.random() < 0.4}
        r = rng.random()
        if r < 0.1:
            s["result"] = None
        elif r < 0.2:
            s["result"] = {"scored": False}
        else:
            score = rng.choice([0.0, round(rng.random() * 100, rng.randint(0, 6)), 100.0])
            pscore = rng.choice([0.0, round(rng.random() * score, rng.randint(0, 6))])
            s["result"] = {"scored": True, "score": score, "score_details": details(rng, score),
                           "public_score": pscore, "public_score_details": details(rng, pscore)}
        subs.append(s)
    return {"submissions": subs, "score_mode": rng.choice(["max", "max_subtask", "max_tokened_last"]),
            "score_precision": rng.randint(0, 4)}


def run(c):
    task = Obj(score_precision=c["score_precision"], score_mode=c["score_mode"], active_dataset="ds")
    subs = []
    for s in c["submissions"]:
        r = s["result"]
        if r is not None:
            sr = Obj(scored=(lambda v: lambda: v)(r["scored"]), score=r.get("score"),
                     score_details=r.get("score_details"), public_score=r.get("public_score"),
                     public_score_details=r.get("public_score_details"))
        else:
            sr = None
        subs.append(Obj(task=task, official=s["official"], timestamp=s["timestamp"],
                        tokened=(lambda v: lambda: v)(s["tokened"]),
                        get_result=(lambda v: lambda ds: v)(sr)))
    participation = Obj(submissions=subs)
    out = dict(c)
    for view, kw in [("full", {}), ("public", {"public": True}), ("only_tokened", {"only_tokened": True})]:
        score, partial = task_score(participation, task, **kw)
        out[view] = [score, partial]
    return out


rng = random.Random(49)
print("[")
print(",\n".join(json.dumps(run(case(rng)), separators=(",", ":")) for _ in range(200)))
print("]")
