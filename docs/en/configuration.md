# Configuration

Every service of an installation reads the **same** `cms.yaml` file. It is
looked up in this order:

1. `--config FILE` (before the subcommand: `cms --config /path/cms.yaml contest-web`);
2. the `CMS_CONFIG` variable;
3. `./cms.yaml` and `/etc/cms/cms.yaml`.

Without a file the defaults plus the environment are used (handy for
containers). [`config/cms.sample.yaml`](../../config/cms.sample.yaml)
documents every option with its default value.

## Environment variables

Every option is overridden by `CMS_` plus its path in upper case, with `_`
between levels; the environment wins over the file:

| Option | Variable |
|---|---|
| `database.url` | `CMS_DATABASE_URL` |
| `contest_web.listen` | `CMS_CONTEST_WEB_LISTEN` |
| `sandbox.trusted.max_time_sec` | `CMS_SANDBOX_TRUSTED_MAX_TIME_SEC` |

Appending `_FILE` reads the value from a file (Docker secrets):
`CMS_SECRET_KEY_FILE=/run/secrets/cms_secret`. Full list:
`cms ctl config env`.

Durations are written `90s`, `3h`, `1h30m`; a bare number means seconds. An
unknown key in the file is an error, so typos never go unnoticed.

## The secret key (`secret_key`)

It signs cookies and CSRF tokens and derives the keys of the secret
scoreboard links. It must be **the same in every service**; the three sites
refuse to start without it. Generate it **once** per installation:

```sh
cms ctl gen-secret
```

## File storage

Statements, test cases, submissions and executables are stored outside the
database, named by the SHA-256 of their content (`storage.backend`):

- `local`: a directory of the machine (`storage.local.path`);
- `s3`: an S3-compatible bucket (AWS, MinIO, Garage…);
- `remote`: the blob server of the dispatcher, for judges on other machines,
  which never connect to the database. They need `storage.remote.url` and
  `storage.remote.token`; get the token on the server with
  `cms ctl blob-token`. It is derived from `secret_key`, so judges never hold
  the secret key.

With `s3` and `remote` every service keeps an on-disk cache
(`storage.cache_dir/<service>`, up to `storage.cache_size_mb`).

## Checking the configuration

```sh
cms ctl config check   # validates, warns if secret_key is missing
cms ctl config show    # effective configuration, secrets hidden
```

## Default ports

| Service | Site | Operations (metrics, health, pprof) |
|---|---|---|
| contest-web | 127.0.0.1:8888 | 127.0.0.1:9188 |
| admin-web | 127.0.0.1:8889 | 127.0.0.1:9189 |
| ranking-web | 127.0.0.1:8890 | 127.0.0.1:9190 |
| dispatcher | — | 127.0.0.1:9191 |
| worker | — | 127.0.0.1:9192 |
| monitor | — | 127.0.0.1:9193 |

Operations ports expose `/metrics` (Prometheus), `/healthz` with the detail
of every dependency and `/debug/pprof/`: keep them on localhost or a
management network. Public sites only answer `{"status":"ok"}` on
`/healthz`.

## Timezone

`timezone` is the initial timezone of the server. There is **one timezone
for the whole installation**; once the administration changes it on the
Server page, the value stored in the database replaces the file's.

## Source code (AGPL)

Every site links to the code of the running version at `/source`. If you
modify the program, set `source_url` to your repository.
