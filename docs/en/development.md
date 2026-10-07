# Development

## Requirements

- Go: the version in `go.mod` is downloaded automatically (`GOTOOLCHAIN=auto`).
- For tests and the local stack: **PostgreSQL 16** and **Valkey** or
  **Redis ≥ 7** binaries, or **Docker**. `scripts/infra.sh` picks one
  (`CMS_INFRA=native|docker` to force it).
- For `make test-sandbox`: root, isolate ≥ 2.0 and cgroups v2.

## Commands

```sh
make build         # bin/cms and the bin/cmsctl alias
make test          # full suite with -race against disposable PostgreSQL and Valkey
make test-short    # no infrastructure (tests needing it are skipped)
make lint          # gofmt, go vet, license headers
make generate      # generated code and translation templates (.pot)
make dev-native    # whole stack with native binaries; make dev-stop stops it
make dev           # whole stack with Docker Compose; make dev-down removes it
```

`make dev-native` writes `.dev/cms.yaml` once (with a generated
`secret_key`), applies the migrations and starts the six services;
`scripts/dev.sh logs [service]` shows their logs. The sites are at
<http://127.0.0.1:8888> (contestants), <http://127.0.0.1:8889>
(administration) and <http://127.0.0.1:8890> (scoreboard).

## Test infrastructure

`scripts/infra.sh up` starts PostgreSQL on port 55432 and Valkey on 56379
(data in `.infra/`, no durability) and writes `.infra/env`:

```sh
scripts/infra.sh up
source .infra/env
go test ./internal/db/...
scripts/infra.sh down   # stops and deletes the data
```

In tests, `testutil.DB(t)` returns a new database cloned from an already
migrated template (dropped at the end) and `testutil.Redis(t)` a client with
its own key prefix. Without the `CMS_TEST_*` variables those tests are
skipped; `make test` and CI make them mandatory (`CMS_TEST_REQUIRE_INFRA=1`).

## Translations

Every visible text goes through `T("…")`, `Tn("one", "many", n)` or
`Tc("context", "…")` in Go and `{{T "…"}}` in templates. The msgids of the
contestant site are the ones of CMS, with their `%(name)s` placeholders.
After adding texts:

```sh
make generate                      # updates locale/*.pot
$EDITOR locale/es/LC_MESSAGES/admin.po   # the admin must be fully translated to Spanish
```

## Migrations

Files `internal/db/migrations/NNNN_name.sql`, numbered without gaps, are
**never edited** once applied (they are checksummed). Every `CREATE INDEX`
has a comment above it justifying the index. `cms migrate` applies them;
`cms migrate status` lists the pending ones.
