# Public scoreboard

*Español: [../es/scoreboard.md](../es/scoreboard.md).*

`cms ranking-web` is the public scoreboard (CMS RankingWebServer): the
cmsranking page and JavaScript, unchanged except for jQuery served locally,
the "Source code" link (AGPL) and the inline handlers moved to JavaScript
for the Content-Security-Policy. Like the original it has no access to the
database or Valkey: the active dispatcher pushes the data over HTTP (CMS
ProxyService), so it can run on a separate public host.

## Configuration

`ranking_web` in `cms.yaml` (all keys in `config/cms.sample.yaml`; every
key can also be set with `CMS_RANKING_WEB_*`):

| Key | Default | Meaning |
|---|---|---|
| `listen` | `127.0.0.1:8890` | address of the site |
| `ops_listen` | `127.0.0.1:9190` | `/metrics`, `/healthz`, pprof |
| `num_proxies` | `0` | reverse proxies in front (client address, HTTPS) |
| `data_dir` | `/var/lib/cms/ranking` | state, logo, faces and flags |
| `username` | `cms` | user of the writes (HTTP basic) |
| `password` | empty | password of the writes; empty: derived from `secret_key` |
| `visibility` | `public` | `public`, `secret` or `admins` (below) |
| `show_id_column` | `false` | show the username column |

`secret_key` is required and must be the one of the other services: it
derives the secret links, the administrator links, the cookie key and the
default password.

The dispatcher sends to every entry of `dispatcher.rankings`:

```yaml
dispatcher:
  rankings:
    - url: http://127.0.0.1:8890/   # as the dispatcher reaches the ranking
      username: ""                  # empty: cms
      password: ""                  # empty: derived from secret_key
      contests: [ioi2026_day1]      # empty: every contest
      prune: true                   # delete what is no longer sent; false: shared ranking
      freeze: 1h                    # 0: no freeze
  ranking_refresh: 30s
```

With the same `secret_key` on both sides no credentials are needed. To
talk to an upstream CMS RankingWebServer (or to let an upstream
ProxyService or `cmsRWSHelper` write here) set the same username and
password on both sides; credentials in the URL
(`http://user:pass@host:8890/`) are accepted too. `cms ctl config show`
hides the passwords.

What is sent is what CMS sends: the contest (description, start and end of
the main group, score precision), its tasks (title, maximum score and
subtask headers of the active dataset, score mode), the users and teams of
the visible participations, and the official submissions with their score
on the active dataset (with the subtask details) and their tokens. Hidden
participations and unofficial submissions are never sent. Scores go out as
soon as they are stored; contests, tasks, users and teams are read again
every `ranking_refresh` and only what changed is sent.

Nothing is lost: when a dispatcher becomes the active one (start,
failover), after every error (retrying with backoff from 1 s to 1 min) and
every 10 minutes, everything is sent again; the scoreboard ignores entities
that did not change. Metrics: `cms_dispatcher_ranking_up{ranking}` and
`cms_dispatcher_ranking_requests_total{ranking,result}`.

Unlike CMS, the scoreboard mirrors the database: at each refresh
(`ranking_refresh`, 30 s by default) the dispatcher deletes from the ranking
the contests, tasks, teams and users it no longer sends (removed in the
admin site, or users whose participations are all hidden); ranking-web also
deletes what depends on them. For a ranking fed by several installations,
set `prune: false` in its `dispatcher.rankings` entry: then nothing is
deleted automatically and, as in CMS, deletions are made with the REST API:

```sh
curl -u cms:PASSWORD -X DELETE http://127.0.0.1:8890/users/USERNAME
```

Keys are CMS names with characters other than letters and digits encoded as
`_` and their hexadecimal code (`-` is `_2d`, `_` is `_5f`).

## Data

`data_dir` holds `state.json` (a snapshot, written atomically) and
`journal.jsonl` (the writes since the snapshot, each one written and synced
to disk before it is acknowledged). Restarts lose nothing; to back up, copy
the directory while the service is stopped, or copy `state.json` and
`journal.jsonl` in that order. A `data_dir` with the layout of a CMS
`lib_dir` (`contests/`, `tasks/`... with one JSON file per entity) is
imported on the first start.

The logo goes in `data_dir/logo.png` (or `.jpg`, `.gif`, `.bmp`; 200×160
recommended), flags in `data_dir/flags/<team key>.png` and photos in
`data_dir/faces/<user key>.png`.

## Visibility

- **public**: everybody sees the scoreboard (as CMS).
- **secret**: only whoever opened the secret link of one of its contests,
  and administrators. The admin site prints the link on the ranking page
  of the contest: `<scoreboard address>?secret=<token>`, where the token is
  derived from `secret_key` and the contest name (it changes if either
  changes). The scoreboard address is configured on the Server page of the
  admin site; in port-based installations (`":8890"`) the link uses the
  host the administrator is browsing. Whoever opens it gets a cookie (12 h)
  and is sent to the address without the token.
- **admins**: only administrators. An administrator link is
  `<scoreboard address>?admin=<token>` with a token from
  `config.RankingAdminToken` (signed with a key derived from `secret_key`,
  valid for 10 minutes); the browser keeps the access for 12 h in a signed
  cookie. Clients with the write credentials (HTTP basic) also count as
  administrators.

Static files (JavaScript, styles, default images) are public in every
mode; the page, the data, the events, the logo, faces and flags are not.
Submissions and subchanges (`/submissions/`, `/subchanges/`) are only
readable by administrators in every mode.

## Freeze

With `freeze: 1h` in a `dispatcher.rankings` entry, the contests sent to
that ranking carry `"freeze": <end − 1 h>` (an extension of the CMS
contest format; a CMS RankingWebServer ignores it). The public then does
not see the scores, tokens or submissions made from that moment on;
submissions made before keep counting when they are scored later.
Administrators see everything. To unfreeze, set `freeze: 0` and restart the
dispatchers: the contest is sent again without the field and every viewer
receives the new scores at once. Without restarting:

```sh
curl -u cms:PASSWORD -X PUT -H 'Content-Type: application/json' \
  -d '{"name":"…","begin":…,"end":…,"score_precision":0}' \
  http://127.0.0.1:8890/contests/CONTEST_KEY
```

(the dispatcher will put the freeze back at its next refresh unless its
configuration changed too).

Different rankings may have different freezes: for example a public one
with `freeze: 1h` and an internal one without freeze.

## Reverse proxies and live updates

The page receives the changes as server-sent events on `/events` (the same
events as CMS: `contest`, `task`, `team`, `user`, `score`, and `reload`
when the page must be reloaded). The server sends a ping right away and
every 15 s, and `X-Accel-Buffering: no`. Behind a proxy:

- do not buffer or compress `*/events` (nginx: `proxy_buffering off` or the
  header above, and leave the path out of `gzip`; Caddy: exclude it from
  `encode`);
- the read timeout of the proxy must be longer than 15 s;
- serve it over HTTP/2 (TLS at the proxy) so that each viewer's open stream
  does not use one of the 6 HTTP/1.1 connections of the browser;
- a prefix works (`location ^~ /ranking/ { proxy_pass http://127.0.0.1:8890/; }`):
  every address of the page is relative;
- set `num_proxies` so that cookies are `Secure` behind HTTPS.

Responses of the data (`/scores`, `/history` and the lists) are computed
once per change and shared by every viewer.
