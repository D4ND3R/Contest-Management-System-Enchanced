#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
#
# Generates upstream_golden.json: the compilation and evaluation commands of
# the languages of the original CMS (cms/grading/languages, commit 1914a5a)
# on the cases below. The Go tests compare the YAML languages with it. It
# loads cms/grading/language.py and the language modules from a CMS checkout
# with a stub for the package they import, so no CMS dependency is needed:
#
#   python3 -I gen_golden.py /path/to/cms > upstream_golden.json

import importlib.util
import json
import os
import sys
import types

CMS = sys.argv[1]
G = os.path.join(CMS, "cms", "grading")


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    mod = importlib.util.module_from_spec(spec)
    sys.modules[name] = mod
    spec.loader.exec_module(mod)
    return mod


language = load("cms_language", os.path.join(G, "language.py"))
cms = types.ModuleType("cms")
grading = types.ModuleType("cms.grading")
grading.Language = language.Language
grading.CompiledLanguage = language.CompiledLanguage
cms.grading = grading
sys.modules["cms"] = cms
sys.modules["cms.grading"] = grading

COMPILE = [
    (["foo.%s"], "foo"),
    (["grader.%s", "foo.%s"], "foo"),
    (["grader.%s", "foo.%s", "bar.%s"], "bar_foo"),
    (["dir/grader.%s", "dir/foo.%s"], "with space"),
]
EVAL = [
    ("foo", None, None),
    ("foo", "grader", []),
    ("foo", "Main", ["a", "b c"]),
]

out = {}
D = os.path.join(G, "languages")
for f in sorted(os.listdir(D)):
    if not f.endswith(".py") or f.startswith("__"):
        continue
    mod = load("lang_" + f[:-3], os.path.join(D, f))
    for cls in vars(mod).values():
        if not (isinstance(cls, type) and issubclass(cls, language.Language)) \
                or cls in (language.Language, language.CompiledLanguage):
            continue
        lang = cls()
        ext = lang.source_extension[1:]
        exe_ext = lang.executable_extension
        cases = {"compile": [], "evaluate": []}
        for sources, exe in COMPILE:
            srcs = [s % ext for s in sources]
            for for_eval in (True, False):
                cases["compile"].append({
                    "sources": srcs, "executable": exe + exe_ext,
                    "for_evaluation": for_eval,
                    "commands": lang.get_compilation_commands(
                        srcs, exe + exe_ext, for_evaluation=for_eval)})
        # PHP copies the main source unless it has the executable's name.
        if exe_ext == lang.source_extension:
            srcs = ["foo" + exe_ext]
            cases["compile"].append({
                "sources": srcs, "executable": "foo" + exe_ext,
                "for_evaluation": True,
                "commands": lang.get_compilation_commands(srcs, "foo" + exe_ext)})
        for exe, main, args in EVAL:
            cases["evaluate"].append({
                "executable": exe + exe_ext, "main": main, "args": args,
                "commands": lang.get_evaluation_commands(
                    exe + exe_ext, main=main, args=args)})
        out[lang.name] = {
            "source_extensions": lang.source_extensions,
            "header_extensions": lang.header_extensions,
            "object_extensions": lang.object_extensions,
            "executable_extension": lang.executable_extension,
            "requires_multithreading": lang.requires_multithreading,
            "cases": cases,
        }

json.dump(out, sys.stdout, indent=1, sort_keys=True, ensure_ascii=False)
print()
