# 13. Maintenance and `cmsctl` reference

*Español: [Mantenimiento y referencia de `cmsctl`](../../es/manual/13-mantenimiento.md)*

How to take care of the server: the services, the logs, the backups, the
upgrades, and all the `cmsctl` commands. Technical references:
[../operation.md](../operation.md) and
[../upgrades-and-backups.md](../upgrades-and-backups.md).

## Contents

1. [Golden rules](#1-golden-rules)
2. [The services](#2-the-services)
3. [Viewing the logs](#3-viewing-the-logs)
4. [Checking health: `doctor` and `verify-host`](#4-checking-health-doctor-and-verify-host)
5. [Backups](#5-backups)
6. [Restoring a backup](#restoring-a-backup)
7. [Upgrading to a new version](#7-upgrading-to-a-new-version)
8. [Files and folders](#8-files-and-folders)
9. [`cmsctl` reference](#9-cmsctl-reference)
10. [Before an important contest](#10-before-an-important-contest)

---

## 1. Golden rules

- Commands are run **with `sudo`**: `sudo cmsctl …`.
- **Never share** passwords or the contents of `/etc/cms/cms.yaml` (it
  holds the keys). To show the configuration without secrets:
  `sudo cmsctl config show`.
- **Back up before** any big change and **copy the backups off** the
  server.
- Do not upgrade the system or CMS on contest day.

## 2. The services

CMS is six systemd services, grouped in `cms.target`:

| Service | What it does |
|---|---|
| `cms-contest-web` | contestant site |
| `cms-admin-web` | administration |
| `cms-ranking-web` | public scoreboard |
| `cms-dispatcher` | distributes the evaluations, computes scores, feeds the scoreboard |
| `cms-worker` | the judge: compiles and runs in the sandbox |
| `cms-monitor` | watches the health of the services and the machine |

```sh
systemctl status cms.target 'cms-*'          # status of all
sudo systemctl restart cms-worker            # restart one
sudo systemctl restart cms.target            # restart all
sudo systemctl stop cms.target               # stop everything
```

They start by themselves when the server boots. **Restarting is safe** at
any time: no submissions or results are lost.

## 3. Viewing the logs

```sh
sudo journalctl -u cms-worker -n 100 --no-pager      # last 100 lines
sudo journalctl -u cms-dispatcher -f                 # live (Ctrl+C to exit)
sudo journalctl -u 'cms-*' --since "10 min ago"      # all, last 10 minutes
```

The logs are in JSON format: look for `"level":"ERROR"` or
`"level":"WARN"`. Before sharing them, filter out the lines with keys:
`… | grep -v -i -e password -e secret`.

## 4. Checking health: `doctor` and `verify-host`

```sh
sudo cmsctl doctor
```

It checks the whole installation and says what is wrong and how to fix it
([ch. 14](14-troubleshooting.md)). Always use it first.

```sh
sudo cmsctl verify-host
```

It checks the judge's machine in depth: cgroups v2, isolate, swap,
hyperthreading, CPU frequency. `WARN`s affect the precision of the times;
`FAIL`s prevent safe evaluation.

## 5. Backups

**Automatic:** every day at 03:17 one is saved in
`/var/lib/cms/backups/` and the last 14 are kept (change this with
`--backup-keep` and `--backup-calendar` when installing).

**Manual, now:**

```sh
sudo systemctl start cms-backup.service              # same as the automatic one
sudo cmsctl backup --label before-the-contest        # with a name
ls -lh /var/lib/cms/backups/
```

A backup is a single `.tar.gz` file with the database, the task and
submission files, and the configuration. It can be made with the services
running.

**Copy it off the server** (from your computer):

```sh
scp user@server:/var/lib/cms/backups/cms-backup-*.tar.gz .
```

(If `scp` cannot read it, first on the server:
`sudo cp /var/lib/cms/backups/cms-backup-XXX.tar.gz ~ && sudo chown $USER ~/cms-backup-XXX.tar.gz`.)

## Restoring a backup

Restoring **replaces all the current data** with the backup's.

```sh
sudo systemctl stop cms.target
sudo -u cms cmsctl --config /etc/cms/cms.yaml restore --clean /var/lib/cms/backups/cms-backup-XXX.tar.gz
sudo systemctl start cms.target
sudo cmsctl doctor
```

- It first verifies the whole file; if it is damaged it touches nothing.
- `--clean` deletes the current database before loading the backup;
  without it, the database must be empty.
- Details and special cases: [../upgrades-and-backups.md](../upgrades-and-backups.md#restoring).

The backups made by the installer's `--clean` (`/var/backups/cms/pre-clean-…sql.gz`)
are PostgreSQL dumps: they are restored with
`gunzip -c FILE.sql.gz | sudo -u postgres psql cms` onto an empty `cms`
database.

## 7. Upgrading to a new version

```sh
sudo cmsctl upgrade
```

1. It downloads the latest published version and verifies its checksum.
2. It makes a backup.
3. It stops the services, replaces the program, updates the database and
   starts again.
4. It checks that all the services are healthy.
5. **If any step fails, it rolls everything back by itself** (previous
   program, restored database) and says so.

Useful options: `--version v0.2.0` (a specific version), `--binary PATH`
(a program already downloaded).

After upgrading, it is a good idea to run the installer again with the
same options (it applies new configuration improvements; it is safe):

```sh
curl -fsSL https://raw.githubusercontent.com/D4ND3R/Contest-Management-System-Enchanced/main/scripts/install.sh \
  | sudo bash -s -- --http-ports 8000,8001,8002
```

## 8. Files and folders

| Path | Contents |
|---|---|
| `/etc/cms/cms.yaml` | configuration (with keys; only root and the `cms` group) |
| `/etc/cms/cms.env` | the services' variables |
| `/var/lib/cms/blobs/` | task and submission files |
| `/var/lib/cms/backups/` | automatic and manual backups |
| `/var/lib/cms/ranking/` | scoreboard data, logo, flags, photos |
| `/var/cache/cms/` | caches (can be deleted) |
| `/var/log/cms-install.log` | the installer's log |
| `/var/backups/cms/` | backups made before `--clean` and `--purge` |
| `/usr/local/bin/cms`, `cmsctl` | the program |

## 9. `cmsctl` reference

All are used as `sudo cmsctl COMMAND [options]`. `sudo cmsctl help
COMMAND` explains each one.

**Diagnostics**

| Command | What it is for |
|---|---|
| `doctor` | check the whole installation ([ch. 14](14-troubleshooting.md)) |
| `verify-host` | check the judge's machine in depth |
| `judging-status` | how many submissions are in each status, per task |
| `config check` | validate `/etc/cms/cms.yaml` |
| `config show` | show the effective configuration (without secrets) |

**Administrators**

| Command | What it is for |
|---|---|
| `add-admin USER` | create an administrator (`--role all\|messaging\|tasks\|readonly`, `--name`) |
| `admin-password USER` | new password for an administrator (`--enable` re-enables it) |
| `public-urls` | view or set the public addresses (*Server* page) |

**Import and export** ([ch. 7](07-importing.md))

| Command | What it is for |
|---|---|
| `import-contest PATH` | import a contest (folder or zip); `-i` tasks, `--users` users, `-u`/`-U` update, `--dry-run` |
| `import-task PATH` | import a task; `-c CONTEST` to add it to one; `-u` update |
| `import-dataset PATH` | add a new dataset to a task |
| `import-users`, `import-team` | users and teams from a package |
| `validate-contest`, `validate-task` | check a package without importing |
| `export-task NAME FILE.zip` | export a task |
| `import-dump FILE` | import a dump from the original CMS |

**Evaluation**

| Command | What it is for |
|---|---|
| `reevaluate --contest X --level compilation\|evaluation\|score` | recompile, reevaluate or recompute scores; also `--task`, `--user`, `--dataset`, `--testcase` |

**Backups and versions**

| Command | What it is for |
|---|---|
| `backup` | complete backup (`--label`, `--output`, `--keep N`) |
| `restore FILE` | restore (`--clean`) |
| `upgrade` | upgrade with automatic rollback |

**Others**

| Command | What it is for |
|---|---|
| `gen-secret` | generate a new `secret_key` |
| `blob-token` | token for judges on other machines |

## 10. Before an important contest

- A week before: upgrade (`sudo cmsctl upgrade`) and do a complete
  rehearsal with test users.
- The day before: backup, `sudo cmsctl doctor`, `sudo cmsctl
  verify-host`.
- For steadier times on the judge: `sudo swapoff -a` and the CPU's
  `performance` mode (`verify-host` shows the commands).
- Keep at hand: this manual, SSH access to the server and an
  administrator's password.

---

**Next:** [14. Troubleshooting](14-troubleshooting.md) ·
**Previous:** [12. After the contest](12-after-the-contest.md) ·
[Index](README.md)
