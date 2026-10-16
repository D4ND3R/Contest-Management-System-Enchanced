#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
#
# Records what the original CMS answers in the states of scenarios.json
# (captured.json.gz, read by TestCMSTaskPages in tasks_test.go).
#
#   capture.py http://127.0.0.1:28888 postgres://postgres@127.0.0.1:55432/refcms
#
# The first argument is the contestant site of a CMS whose database (the
# second argument, changed with psql) holds the demo contest of
# testdata/dumps/demo. The rows of the scenarios' user are replaced.

import gzip
import http.cookiejar
import json
import os
import re
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request
from datetime import datetime, timedelta, timezone

HERE = os.path.dirname(os.path.abspath(__file__))


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *a, **k):
        return None


def client():
    jar = http.cookiejar.CookieJar()
    return urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar), NoRedirect), jar


def get(op, url, data=None, lang=None):
    headers = {"Accept-Language": lang or "en"}
    try:
        r = op.open(urllib.request.Request(url, data=data, headers=headers))
    except urllib.error.HTTPError as e:
        r = e
    return r.status if hasattr(r, "status") else r.code, dict(r.headers), r.read()


def login(base, user):
    op, jar = client()
    get(op, base + "/demo")
    xsrf = [c.value for c in jar if c.name == "_xsrf"][0]
    st, h, _ = get(op, base + "/demo/login", urllib.parse.urlencode(
        {"_xsrf": xsrf, "username": user, "password": "demo"}).encode())
    assert st == 302 and "login_error" not in h.get("Location", ""), (st, h)
    return op


def resolve(sql, now):
    def rep(m):
        t = now + timedelta(seconds=int(m.group(1)))
        return t.strftime("%Y-%m-%d %H:%M:%S+00")
    return [re.sub(r"\{NOW([+-]\d+)\}", rep, q) for q in sql]


def expand(sql, scenarios):
    out = []
    for q in sql:
        if q.startswith("@"):
            out += expand(scenarios[q[1:]]["sql"], scenarios)
        else:
            out.append(q)
    return out


def psql(dburl, sql):
    subprocess.run(["psql", "-q", "-v", "ON_ERROR_STOP=1", dburl], check=True,
                   input="BEGIN;\n" + "".join(q + ";\n" for q in sql) + "COMMIT;\n", text=True)


def main():
    base, dburl = sys.argv[1], sys.argv[2]
    spec = json.load(open(os.path.join(HERE, "scenarios.json")))
    byname = {s["name"]: s for s in spec["scenarios"]}
    out = {"base": spec["base"], "user": spec["user"], "scenarios": []}
    for sc in spec["scenarios"]:
        now = datetime.now(timezone.utc).replace(microsecond=0)
        sql = resolve(expand(sc["sql"], byname), now)
        psql(dburl, spec["base"] + sql)
        op = login(base, spec["user"])
        rec = {"name": sc["name"], "now": now.isoformat(), "sql": sql, "pages": []}
        for p in sc["pages"]:
            st, h, body = get(op, base + p["path"], lang=p.get("lang"))
            page = {"file": p["file"], "path": p["path"], "lang": p.get("lang", ""), "status": st,
                    "content_type": h.get("Content-Type", ""),
                    "content_disposition": h.get("Content-Disposition", ""),
                    "location": h.get("Location", "")}
            if not p.get("headers_only"):
                page["body"] = body.decode("utf-8")
            rec["pages"].append(page)
            print(sc["name"], p["file"], st, len(body))
        out["scenarios"].append(rec)
    psql(dburl, spec["base"])
    with gzip.GzipFile(os.path.join(HERE, "captured.json.gz"), "wb", mtime=0) as f:
        f.write(json.dumps(out, indent=1, ensure_ascii=False).encode("utf-8"))


main()
