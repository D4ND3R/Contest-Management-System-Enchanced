# Contest Management System — Go rewrite

A fork of [CMS](https://github.com/cms-dev/cms), the contest system of the
International Olympiad in Informatics, with the whole backend rewritten in
Go. It keeps the architecture, the data model, the behaviour and **the user
interface** of CMS (same pages, CSS, JavaScript and translations), and
removes what makes CMS hard to install and operate: one binary, one-command
installation, durable job queues, live updates everywhere.

> **Status: phase F0 (foundations).** The services start, connect and report
> their health, but the sites have no pages yet. See [PROGRESS.md](PROGRESS.md)
> and the roadmap in [SPEC.md](SPEC.md).

*Documentación en español: [docs/es](docs/es/README.md).*

## Architecture

One binary, `cms`, with a subcommand per service:

| Command | Replaces in CMS |
|---|---|
| `cms contest-web` | ContestWebServer (contestant site and `/api`) |
| `cms admin-web` | AdminWebServer |
| `cms ranking-web` | RankingWebServer |
| `cms dispatcher` | EvaluationService, ScoringService, ProxyService |
| `cms worker` | Worker (isolate + seccomp; no database access) |
| `cms monitor` | Checker, ResourceService, LogService |
| `cms ctl` / `cmsctl` | cmscontrib |
| `cms migrate` | cmsInitDB / DumpUpdater |

PostgreSQL holds the state (explicit SQL, embedded forward-only migrations),
Valkey Streams hold the job queues and Valkey pub/sub carries live events.
Why: [DECISIONS.md](DECISIONS.md).

## Development

Requirements: Go (the toolchain in `go.mod` is downloaded automatically),
and either PostgreSQL 16 + Valkey/Redis binaries or Docker.

```sh
make build        # bin/cms and bin/cmsctl
make test         # full suite against disposable PostgreSQL and Valkey
make lint
make dev-native   # whole stack with native binaries (make dev-stop)
make dev          # whole stack with Docker Compose
```

More in [docs/en/development.md](docs/en/development.md).

## License

GNU Affero General Public License v3 or later, like CMS: see
[LICENSE.txt](LICENSE.txt) and [AUTHORS.txt](AUTHORS.txt). Every site links
to the source code of the running version (`/source`); if you run a
modified version, set `source_url` to your repository.
