# Development

## Requirements

- Go: the version in `go.mod` is downloaded automatically (`GOTOOLCHAIN=auto`).
- For tests and the local stack: **PostgreSQL 16** and **Valkey** or
  **Redis ≥ 7** binaries, or **Docker**. `scripts/infra.sh` picks one
  (`CMS_INFRA=native|docker` to force it).
- For `make test-sandbox` and `make chaos`: root, isolate ≥ 2.0, cgroups v2
  and the compilers (see [Judges](judges.md)), plus PostgreSQL and Valkey
  as for `make test` (started by `scripts/infra.sh` when `CMS_TEST_*` is
  not set). On a machine without cgroups v2: `CMS_SANDBOX_CGROUPS=0 make
  test-sandbox` (memory verdicts are skipped; CI runs them with cgroups
  v2).

## Commands

```sh
make build         # bin/cms and the bin/cmsctl alias
make test          # full suite with -race against disposable PostgreSQL and Valkey
make test-short    # no infrastructure (tests needing it are skipped)
make lint          # gofmt, go vet, license headers
make generate      # generated code and translation templates (.pot)
make dev-native    # whole stack with native binaries; make dev-stop stops it
make dev           # whole stack with Docker Compose; make dev-down removes it
make test-sandbox  # as root: isolate, malicious battery, task types, worker and dispatcher end to end, chaos
make chaos         # as root: only the chaos test of the judging pipeline
make load          # load test of the dispatcher with simulated workers
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

## The judging pipeline

```
contest-web, admin-web, cmsctl      dispatcher (one active, the rest standby)      worker (no database)
  submission + task score (SQL)
  notification ──▶ judging:events ──▶ Pending, BuildJobs ──▶ jobs (5 priorities) ──▶ isolate
                    (Valkey stream)    Apply (one transaction    ◀── results ◀──── result and ACK
                                       per batch of results)                        in one step
                                       └─▶ next jobs, task score cache, live events, scoreboard
```

What makes it survive a `kill -9` of any process (SPEC N3):

- **Notifications** are a Valkey stream read by a consumer group and
  acknowledged once handled: those of a dispatcher that died are read again
  by the next one. A lost notification only delays work: every dispatcher
  sweeps the database for unfinished results when it becomes active and
  every `dispatcher.sweep_interval`.
- **Jobs** (`internal/queue`, D27) are acknowledged by the worker in the
  same atomic step that publishes the result. The jobs of a worker that
  died go back to the queue when it restarts with the same name, or to
  another worker after the visibility timeout (30 s).
- **Results** are acknowledged by the dispatcher after the commit that
  stores them. A result delivered twice changes nothing (the first one
  wins, D21) and the result of a job that was running when its submission
  was invalidated is dropped by its generation (D23).
- **One active dispatcher**: it holds a lease in Valkey (10 s, renewed
  every third); a standby takes over when it dies (at once when it stops
  cleanly) and starts by taking the results and notifications the previous
  one left unacknowledged.
- **Live events** are not durable: those of a dispatcher that dies between
  a commit and their publication are lost (the chaos test counts them). The
  stored results are complete; a page shows them the next time it reloads
  its live regions.

## End-to-end, chaos and load tests

- `TestEndToEnd` (`make test-sandbox`) runs the dispatcher and a worker in
  the test process on real isolate boxes: a Batch task with a GroupMin score
  type, submissions in C++ and Python 3 for every verdict (correct, partial,
  wrong, time limit, signal, exit code, compilation error) and user tests.
  It checks results, scores, the task score cache, empty queues and the live
  events a web process receives through Valkey.
- `TestChaos` (`make chaos`, also part of `make test-sandbox`) builds the
  `cms` binary and runs a dispatcher and two workers as processes. It judges
  44 submissions undisturbed, then again while it kills the dispatcher with
  SIGKILL (while active, and while it holds an unacknowledged batch of
  results) and the workers in the middle of jobs, leaves one worker down for
  40 s and kills the Valkey connections of every process (`CLIENT KILL`).
  Every submission must end judged exactly once per dataset, with the same
  outcomes and scores as the undisturbed run, the task score cache equal,
  the queues empty, no process exiting by itself, and the jobs of the dead
  worker taken again in less than 60 s. The kills are random:
  `CMS_TEST_CHAOS_SEED` (printed by the test) repeats a run and
  `CMS_TEST_CHAOS_SUBMISSIONS` changes its size. On failure the test prints
  the end of every process log.
- `TestLoad` (`make load`) submits 2 submissions/s for
  `CMS_TEST_LOAD_SUSTAINED` seconds (30) and then 20/s for
  `CMS_TEST_LOAD_BURST` seconds (60) to a dispatcher with simulated workers
  that answer at once. It reports the latency from notification to verdict,
  the CPU per job and the database and Valkey round trips of the dispatcher
  per submission; `CMS_TEST_LOAD_CPUPROFILE=cpu.out` writes a profile for
  `go tool pprof`. For the 2 vCPU target run it with `GOMAXPROCS=2 taskset
  -c 0,1 make load` and pin PostgreSQL and Valkey to the same CPUs
  (`taskset -a -p -c 0,1 PID`).
