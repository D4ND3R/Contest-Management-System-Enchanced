#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
#
# Generates upstream_golden.json: what the original CMS (commit 1914a5a)
# answers on the cases below for the white diff (_white_diff in
# cms/grading/steps/whitediff.py) and for the standard manager output
# (extract_outcome_and_text in cms/grading/steps/trusted.py). The Go tests
# compare their results with it. It loads the two modules from a CMS
# checkout with stubs for what they import, so no CMS dependency is needed:
#
#   python3 -I gen_golden.py /path/to/cms > upstream_golden.json

import base64
import importlib.util
import io
import json
import os
import random
import sys
import types

CMS = sys.argv[1]
S = os.path.join(CMS, "cms", "grading", "steps")


class _Msg:
    def __init__(self, m):
        self.message = m


class _Messages:
    MSGS = {"success": "Output is correct",
            "partial": "Output is partially correct",
            "wrong": "Output isn't correct",
            "nooutput": "Evaluation didn't produce file %s"}

    def get(self, k):
        return _Msg(self.MSGS[k])


def stub(name, **attrs):
    m = types.ModuleType(name)
    m.__dict__.update(attrs)
    sys.modules[name] = m
    return m


stub("cms", config=None)
stub("cms.db")
stub("cms.db.filecacher", FileCacher=object)
stub("cms.grading")
stub("cms.grading.Sandbox", Sandbox=object)
stub("cms.grading.steps")
stub("cms.grading.steps.evaluation", EVALUATION_MESSAGES=_Messages())
stub("cms.grading.steps.utils", generic_step=None)
stub("cms.grading.steps.stats", StatsDict=dict)


def load(name):
    spec = importlib.util.spec_from_file_location(
        "cms.grading.steps." + name, os.path.join(S, name + ".py"))
    mod = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = mod
    spec.loader.exec_module(mod)
    return mod


whitediff = load("whitediff")
trusted = load("trusted")


def b64(b):
    return base64.b64encode(b).decode()


# White diff: the cases of the upstream unit tests plus random ones.
pairs = [
    (b"", b""), (b"1", b"1"), (b"a", b"a"), ("你好".encode(), "你好".encode()),
    (b"1   ", b"1"), (b"   1", b"1"), (b"1 \t\n\x0b\x0c\r", b"1"),
    (b"1\n", b"1"), (b"1\n\n\n\n", b"1"), (b"1\n\n\n\n", b"1\n"),
    (b"1\n\r\r \n  \n\n", b"1   \n\r  "), (b"1 asd\n\n\n", b"   1\tasd  \n"),
    (b"1 2\n\n\n", b"1 2\n"), (b"1\t\r2", b"1 2"),
    (b"1 2", b"1 2 \t\n\x0b\x0c\r"), (b"1 2", b"12"), (b"1 23", b"12 3"),
    (b"1", b"01"), (b"1.0", b"1"), (b"\n1", b"1"), (b"1 2", b"1\n2"),
    (b"1\n\n2", b"1\n2"), (b"1" * 1000, b"1" * 1000 + b"0"),
    (b"hello\x00world", b"helloworld"), (b"2\n", b""), (b"", b"2\n"),
    (b"\n\n", b""), (b"x" * 99 + "é".encode(), b"y"),
    (b"a\xc3\x28b", b"ab"), (b"\xe4\xbd\xa0" * 40, b"z"),
    (b"1 2 3\n4 5 6\n", b"1 2 3\n4 5 7\n"), (b"a b", b"a  b\n\n  \n"),
    (b"  \n", b""), (b"a\r\nb\r\n", b"a\nb\n"), (b"a\rb", b"a\nb"),
    (b"\x0bx", b"x"), (b"x\x00", b"x"),
]
rng = random.Random(49)
alphabet = [b" ", b"\t", b"\n", b"\r", b"\x0b", b"\x0c", b"a", b"b", b"1",
            b"\xff", b"\x00", "é".encode()]
for _ in range(400):
    a = b"".join(rng.choice(alphabet) for _ in range(rng.randint(0, 12)))
    if rng.random() < 0.5:
        b = a
        for _ in range(rng.randint(0, 3)):
            i = rng.randint(0, len(b))
            b = b[:i] + rng.choice(alphabet) + b[i:]
    else:
        b = b"".join(rng.choice(alphabet) for _ in range(rng.randint(0, 12)))
    pairs.append((a, b))
for _ in range(20):
    n = rng.randint(90, 130)
    a = b"".join(rng.choice([b"a", b" ", "é".encode()]) for _ in range(n))
    pairs.append((a, a[:-1] + b"z"))

diff = []
for out, res in pairs:
    eq, admin = whitediff._white_diff(io.BytesIO(out), io.BytesIO(res))
    diff.append({"output": b64(out), "correct": b64(res),
                 "equal": eq, "admin_text": admin})


# Standard manager output.
class FakeSandbox:
    def __init__(self, files):
        self.files = files
        self.stdout_file = "o"
        self.stderr_file = "e"

    def get_file_text(self, name):
        if name not in self.files:
            raise FileNotFoundError(name)
        return io.TextIOWrapper(io.BytesIO(self.files[name]), encoding="utf-8")


manager_cases = [
    (b"0.45\n", "你好.\n".encode()),
    (b"0.45\n", b"Text to return.\nADMIN_MESSAGE: admin"),
    (b"0.45\nNothing\n", b"Text to return.\nto see here"),
    (b"0.45", b"Text to return."),
    (b"   0.45\t \nignored", b"\t  Text to return.\r\n"),
    (b"0.45\n", b"translate:success\n"),
    (b"1\n", b"translate:partial"),
    (b"0\n", b"translate: wrong \n"),
    (b"0\n", b"translate:other\n"),
    (b"not a float\n", b"Text to return.\n"),
    (b"0.45", b"\xc3\x28"),
    (b"\xc3\x28", b"x"),
    (b"0.5\r", b"50% done\rADMIN_MESSAGE: 10% left\r\nADMIN_MESSAGE:  more \nnoise"),
    (b"1e-3\n", b"tab\there\n"),
    (b"0.5\n", b"bad\x07char\n"),
    (b"0.5\n", "¿Correcto?\n".encode()),
    (b"0.5\n", b"\x1ctrimmed\x1d\n"),
    (b"0.5\n", b"ok\nADMIN_MESSAGE: bad\x01\n"),
    (b"", b"text"),
    (b"\n0.5", b"text"),
    (b" -0 ", b""),
    (b"0.5\n", b""),
    (b"0.5\n", b"\nADMIN_MESSAGE: only admin\n"),
]
manager = []
for o, e in manager_cases:
    case = {"stdout": b64(o), "stderr": b64(e)}
    try:
        outcome, text, admin = trusted.extract_outcome_and_text(
            FakeSandbox({"o": o, "e": e}))
        case.update(outcome=outcome, text=text, admin_text=admin)
    except ValueError as err:
        case["error"] = str(err)
    manager.append(case)

json.dump({"white_diff": diff, "manager_output": manager}, sys.stdout,
          indent=1, ensure_ascii=False)
print()
