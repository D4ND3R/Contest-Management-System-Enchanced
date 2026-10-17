# Upgrades and backups

*Español: [../es/actualizacion-y-respaldos.md](../es/actualizacion-y-respaldos.md).*

## Upgrading

```sh
sudo cmsctl upgrade                     # latest release
sudo cmsctl upgrade --version v1.3.0    # a given release
sudo cmsctl upgrade --binary ./bin/cms  # a local build
```

Steps, in order:

| Step | What happens |
|---|---|
| fetch | downloads `cms_linux_<arch>.tar.gz` of the release, checks it against `checksums.txt`, and checks that the new binary runs |
| backup | `cms ctl backup` of the database and configuration to `<data_dir>/backups/…-pre-upgrade.tar.gz` (blobs are only ever added, so they are not needed to go back) |
| stop | `systemctl stop` of the enabled `cms-*.service` units |
| install | the current binary is kept as `cms.prev` and replaced |
| migrate | the **new** binary applies its migrations |
| start | `systemctl start` of the units |
| health | every unit active and its `/healthz` (operations listener) answering 200 within `--health-timeout` (2 min) |

**Automatic rollback.** If any step fails, the command stops the units, puts
the previous binary back, restores the database from the backup of this run
(when the migration step was reached), starts the units again, checks them,
and exits with an error saying it rolled back. Migrations are forward only
(D3): going back always means restoring the backup. Anything written between
the start of the new version and the rollback (seconds) is lost.

If the rollback itself fails the message lists the manual steps: put
`cms.prev` back, `cmsctl restore --clean <backup>`, `systemctl start cms.target`.

**Rehearsing a failure** (do it before the contest, on a test machine):

```sh
sudo CMS_UPGRADE_FAIL_AT=migrate cmsctl upgrade --binary ./cms-new --force
```

makes the upgrade fail right after the named step (`fetch`, `backup`, `stop`,
`install`, `migrate`, `start` or `health`), which exercises the rollback.

Options: `--backup-dir`, `--units` (default: the enabled `cms-*.service`),
`--ignore-health worker` (a judge whose sandbox is known to be unhealthy),
`--no-database` (judging-only machines; implied when the only unit is
`cms-worker`), `--no-services` (manual setups), `--release-url` (a mirror).
Upgrade the main machine first, then the judges.

## Backups

```sh
cmsctl backup                       # <data_dir>/backups/cms-backup-<UTC time>.tar.gz
cmsctl backup --output /mnt/usb/    # another directory (or a file name)
cmsctl backup --keep 14             # then delete all but the newest 14
cmsctl backup --no-blobs            # without the files (e.g. a versioned S3 bucket)
```

One `.tar.gz` holds:

| Entry | Contents |
|---|---|
| `manifest.json` | format, cms version, schema version and the checksums of its migrations, tables and their columns |
| `db/<table>.copy` | every table in PostgreSQL `COPY` text format, all read in **one snapshot** |
| `blobs/<sha256>` | every file of the blob store (local or S3) |
| `config/cms.yaml` | the configuration file |
| `SHA256SUMS` | the SHA-256 of every other entry |

`COPY` in Go rather than `pg_dump`: no client tools that must match the
server version, and it works from the distroless image of the Docker stack.
`task_scores` is not saved (it is computed from the rest and rebuilt on
restore). Services may keep running during a backup. The archive contains
every secret of `cms.yaml`: it is created with mode 0600; store it like the
server itself.

The installer adds `cms-backup.timer` (daily at 03:17, 14 kept):

```sh
systemctl list-timers cms-backup.timer
sudo systemctl start cms-backup.service      # one now
journalctl -u cms-backup.service
```

Each backup is a full copy: copy them off the machine (rsync, object storage).
During a contest an hourly schedule is reasonable: re-run the installer with
`--backup-calendar hourly --backup-keep 48`.

## Restoring

```sh
sudo systemctl stop cms.target
sudo -u cms cmsctl restore --verify-only /var/lib/cms/backups/cms-backup-….tar.gz
sudo -u cms cmsctl restore --clean /var/lib/cms/backups/cms-backup-….tar.gz
sudo systemctl start cms.target
```

The archive is read twice: first every checksum is verified (a damaged
archive changes nothing), then it is loaded in **one transaction**: with
`--clean` the current tables, functions and types are dropped (without it the
database must be empty, e.g. a new one); the migrations of the binary rebuild
the schema of the backup; the tables seeded by migrations are emptied,
user triggers disabled and foreign keys dropped while every table is loaded;
then triggers come back, foreign keys are added again (which checks every
row), identity sequences move past the restored ids, newer migrations are
applied (a backup of an older version is upgraded) and the score cache is
recomputed. Files go to the configured blob store. Any failure leaves the
database as it was.

Run it as the `cms` user (it owns the blob directory). It refuses while other
sessions use the database (stop the services; `--force` overrides).
`--config-to /etc/cms/cms.yaml.restored` extracts the archived configuration
(on a new machine, take `secret_key` from it: cookies, secret scoreboard
links and judge tokens derive from it). A backup must be restored by a
binary whose migrations include the backup's: same or newer version.

**Moving to a new machine:** install there, stop its services, restore with
`--clean --config-to`, put the old `secret_key` (and anything else you
changed) in its `cms.yaml`, start.
