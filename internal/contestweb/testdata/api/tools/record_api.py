#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
"""Runs the API steps (steps.py) against the original CMS and records the
answers as internal/contestweb/testdata/api/cms.json.

usage: record_api.py OUT.json [BASE] [DBURL]
"""
import json, os, re, subprocess, sys, uuid, urllib.request, urllib.error
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from steps import STEPS, SUM, HALF, BOB

out = sys.argv[1]
base = sys.argv[2] if len(sys.argv) > 2 else "http://127.0.0.1:28888"
db = sys.argv[3] if len(sys.argv) > 3 else "postgres://postgres@127.0.0.1:55432/refcms"

def psql(sql, value=False):
    r = subprocess.run(["psql", db, "-Atqc", "SET TIME ZONE 'UTC'; " + sql], capture_output=True, text=True)
    if r.returncode != 0 or "ERROR" in r.stderr:
        raise SystemExit(f"{sql}: {r.stderr}")
    return r.stdout.strip() if value else None

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *a, **k):
        return None

op = urllib.request.build_opener(NoRedirect)

def multipart(form, files):
    b = uuid.uuid4().hex
    parts = []
    for k, v in form.items():
        parts.append(f'--{b}\r\nContent-Disposition: form-data; name="{k}"\r\n\r\n{v}\r\n'.encode())
    for f in files:
        parts.append(f'--{b}\r\nContent-Disposition: form-data; name="{f["field"]}"; filename="{f["filename"]}"\r\n'
                     f'Content-Type: application/octet-stream\r\n\r\n'.encode() + f["content"].encode() + b"\r\n")
    parts.append(f"--{b}--\r\n".encode())
    return b"".join(parts), "multipart/form-data; boundary=" + b

# Start from the rows of the dump: the three submissions only, no token.
psql(f"DELETE FROM submissions WHERE opaque_id NOT IN ({SUM}, {BOB}, {HALF}); DELETE FROM tokens")
saved = {"garbage": "garbage"}
ids = {}   # submission ids returned by submit -> <id:N>
recorded = []
for s in STEPS:
    for q in s.get("sql", []):
        psql(q)
    headers = {}
    if s.get("auth"):
        headers["X-CMS-Authorization"] = saved[s["auth"]]
    data = None
    if s["method"] == "POST":
        data, ctype = multipart(s.get("form", {}), s.get("files", []))
        headers["Content-Type"] = ctype
    req = urllib.request.Request(base + s["path"], data=data, headers=headers, method=s["method"])
    try:
        r = op.open(req)
    except urllib.error.HTTPError as e:
        r = e
    body = r.read().decode()
    status = r.status if hasattr(r, "status") else r.code
    res = {"status": status, "content_type": r.headers.get("Content-Type")}
    try:
        v = json.loads(body)
    except ValueError:
        v = None
        res["body"] = re.sub(r"\s+", " ", body)[:200]
    if isinstance(v, dict):
        if "login_data" in v and isinstance(v["login_data"], str):
            if s.get("save"):
                saved[s["save"]] = v["login_data"]
            if s.get("auth") and v["login_data"] == saved.get(s["auth"]):
                v["login_data"] = "<sent login_data>"
            else:
                v["login_data"] = "<login_data>"
        if "id" in v and "/submit" in s["path"]:
            ids[v["id"]] = v["id"] = "<id:%d>" % len(ids)
        if "list" in v:
            for item in v["list"]:
                item["id"] = ids.get(item["id"], item["id"])
    if v is not None:
        res["json"] = v
    if s.get("check"):
        res["check"] = psql(s["check"], value=True)
    recorded.append(dict(s, cms=res))
    print(f'{s["name"]}: {status} {json.dumps(res.get("json", res.get("body")))[:150]}', file=sys.stderr)
for q in STEPS[0]["sql"]:
    psql(q)
psql(f"DELETE FROM submissions WHERE opaque_id NOT IN ({SUM}, {BOB}, {HALF}); DELETE FROM tokens")
json.dump({"about": "Answers of the JSON API of the original CMS (1.6.dev0, commit 1914a5a) to the steps of "
                    "tools/steps.py, recorded by tools/record_api.py. See README.",
           "steps": recorded}, open(out, "w"), indent=1, ensure_ascii=False)
