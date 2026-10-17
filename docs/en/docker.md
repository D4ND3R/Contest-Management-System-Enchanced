# Docker

*Español: [../es/docker.md](../es/docker.md).*

`deploy/docker-compose.yml` runs the whole installation with containers:
PostgreSQL 16, Valkey 8, a migration job, the six services and, with the
`caddy` profile, Caddy with automatic HTTPS. Images are published to GHCR for
amd64 and arm64 on every release: `ghcr.io/d4nd3r/cms` (every service,
distroless, non-root) and `ghcr.io/d4nd3r/cms-worker` (isolate and the
compilers).

## First start

```sh
git clone https://github.com/D4ND3R/Contest-Management-System-Enchanced cms && cd cms
sudo deploy/compose-init.sh          # once: deploy/.env and deploy/secrets/
$EDITOR deploy/.env                  # version, ports, domains, judge concurrency
docker compose -f deploy/docker-compose.yml up -d
docker compose -f deploy/docker-compose.yml run --rm migrate ctl add-admin admin   # prints the password once
```

`compose-init.sh` generates the secrets **once** (secret_key, PostgreSQL and
Valkey passwords, the judge's blob token) in `deploy/secrets/` and never
overwrites them; nothing secret is printed. They reach the containers as
Docker secrets (`CMS_*_FILE`). The only configuration file is
`deploy/compose/cms.yaml` (the scoreboard in `dispatcher.rankings`, which
variables cannot express); everything else is `CMS_*` variables.

With HTTPS: set `CMS_DOMAIN`, `CMS_ADMIN_DOMAIN`, `CMS_RANKING_DOMAIN`,
`CMS_BIND=127.0.0.1` and `CMS_NUM_PROXIES=1` in `.env`, then
`docker compose -f deploy/docker-compose.yml --profile caddy up -d`.

Set the public addresses of the Server page (or do it in the administration):

```sh
docker compose -f deploy/docker-compose.yml run --rm migrate ctl public-urls \
  --contest https://contest.example.org/ --ranking https://ranking.example.org/
```

## The judge

The `worker` container is privileged with its own cgroup namespace (isolate
needs both) and a host with cgroups v2. It gets no `secret_key` and no
database: files come from the blob server of the dispatcher with a derived
token. Check it with
`docker compose -f deploy/docker-compose.yml exec worker cms ctl verify-host`.
Judges on other machines can run the same image (`docker run --privileged
--cgroupns=private …`) or the installer's `--worker-only` mode.

## Backups, upgrades

```sh
# backup into CMS_BACKUP_DIR (./backups by default)
docker compose -f deploy/docker-compose.yml run --rm migrate ctl backup --output /backups/ --keep 14
# upgrade: back up, set CMS_VERSION in .env, then
docker compose -f deploy/docker-compose.yml pull
docker compose -f deploy/docker-compose.yml up -d     # the migrate job runs first
# roll back: stop the services, restore, go back to the previous CMS_VERSION
docker compose -f deploy/docker-compose.yml stop contest-web admin-web ranking-web dispatcher worker monitor
docker compose -f deploy/docker-compose.yml run --rm migrate ctl restore --clean /backups/cms-backup-….tar.gz
```

`cmsctl upgrade` (with automatic rollback) is for systemd installations;
with Compose the steps above are manual. Schedule backups with a host timer
or cron running the backup command.

## Building the images yourself

```sh
make build
docker build -f deploy/docker/Dockerfile -t cms .
docker build -f deploy/docker/Dockerfile.worker -t cms-worker .
```

The release workflow (`.github/workflows/images.yml`) builds one binary per
architecture into `dist/linux_<arch>/` and passes `--build-arg
BINARY_SOURCE=release`.
