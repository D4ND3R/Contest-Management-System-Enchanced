# 14. Troubleshooting

*Español: [Solución de problemas](../../es/manual/14-solucion-de-problemas.md)*

First, always:

```sh
sudo cmsctl doctor
```

This chapter explains how to read its result and how to solve the most
frequent real problems (we ran into many of them installing on real
servers). For installation failures, the installer shows its own diagnosis
([ch. 2](02-installation.md#7-when-the-installation-fails)).

## Contents

1. [Reading the result of `doctor`](#1-reading-the-result-of-doctor)
2. [Installation](#2-installation)
3. [`cmsctl` and permissions](#3-cmsctl-and-permissions)
4. [Importing](#4-importing)
5. [The judge does not work](#the-judge-does-not-work)
6. [The sites do not open](#6-the-sites-do-not-open)
7. [The scoreboard](#7-the-scoreboard)
8. [Access to the administration](#8-access-to-the-administration)
9. [Asking for help](#9-asking-for-help)

---

## 1. Reading the result of `doctor`

```
Configuration
  PASS  configuration: /etc/cms/cms.yaml
Database
  PASS  PostgreSQL: connected
  PASS  migrations: schema version 4, up to date
  PASS  administrators: 2 (1 with every permission)
Valkey and judges
  PASS  Valkey: connected
  FAIL  judge d4server: takes no jobs: sandbox: ... Cannot open /run/isolate/cgroup
        fix: sudo cmsctl verify-host shows what the sandbox needs
  FAIL  queue: 12 job(s) waiting for a judge, the oldest for 6m10s
        fix: submissions are not being judged: see the judges above and sudo journalctl -u cms-worker -n 50
Services
  PASS  cms-contest-web: active and enabled
  ...
Contests
  WARN  contest D4OI2026: task baches has no grader for C11 / gcc, Pascal / fpc (allow only the languages it supports on the contest page)

24 checks: 2 failed, 1 warnings
```

- It is grouped: configuration, database, Valkey and judges, services,
  health, storage, machine, contests, firewall.
- Each `FAIL` or `WARN` comes with its `fix:` with the command or action.
- Fix them **from top to bottom**: a database or Valkey problem causes
  others further down.
- Run `doctor` with `sudo`: without it, it cannot see the logs or the
  firewall.
- It exits with code 1 if there is any `FAIL` (useful in scripts) and
  `--json` gives the result for other tools.

## 2. Installation

**"Job for valkey-server.service failed" / port 6379 is busy.** Another
Redis or Valkey uses the port. Now the preliminary check detects it and
says which one:
```sh
sudo ss -ltnp 'sport = :6379'                # who is using it
sudo systemctl disable --now redis-server    # if it is a redis-server you do not use
```
If another application of yours needs that Redis, do not stop it: write to
us (for now CMS needs Valkey on the machine's port 6379).

**"a PostgreSQL database named "cms" already exists and was not made by this
installer" or "column "checksum" does not exist".** There is a `cms`
database belonging to another program (for example, the original CMS). If
you do not need it:
```sh
curl -fsSL …/install.sh | sudo bash -s -- --clean --http-ports 8000,8001,8002
```
`--clean` backs it up in `/var/backups/cms/` before deleting it. If
another application uses it, do not delete it: the installer does not
touch it nor change the password of PostgreSQL's `cms` user.

**The installation finished but the services are not active** (`systemctl
list-units 'cms*'` only shows `cms.target`). Run the installer again with
the same options; now, if a step fails, it says so with the failure report
instead of stopping silently.

**It did not show the administrator's password.** The administrator
already existed: `sudo cmsctl admin-password admin`.

**Apt busy ("Could not get lock").** Another package installation is in
progress (for example, automatic updates). Wait a few minutes and run the
installer again.

**No network / proxy.** The diagnosis says so ("The package mirrors cannot
be reached"). Try `sudo apt-get update` and `curl -I https://github.com`.

**I want to start from scratch.** `--clean` ([ch. 2](02-installation.md#8-clean-install---clean)).

## 3. `cmsctl` and permissions

**"cannot read /etc/cms/cms.yaml: permission denied: run the command with
sudo".** Use `sudo cmsctl …`. The configuration has keys and only root and
the `cms` group can read it.

**"password authentication failed for user "cms"".** The command did not
find the configuration and used the factory one. Use `sudo cmsctl …` (or
`--config /etc/cms/cms.yaml`).

**`doctor` says the store has files belonging to another owner.** Some
command was run as root with a previous version and the services cannot
read them:
```sh
sudo chown -R cms:cms /var/lib/cms/blobs
```
Since this version `sudo cmsctl` leaves the files owned by `cms`.

## 4. Importing

**"couldn't autodetect the loader … loaders of contests: italy_yaml wants
contest.yaml; polygon wants contest.xml and problems/".** The file does not
have the structure of a contest at the root or inside a single folder.
Check with `unzip -l file.zip`. If it is a single task, use `import-task`.

**"users not found in the database".** Add `--users` or create the users
beforehand.

**Grader warnings ("grader only for C++…", "no grader.%l for C11 / gcc,
Pascal / fpc").** The contest allows languages for which the task has no
grader. On the contest page leave only the languages with a grader
([ch. 7](07-importing.md#10-warnings-when-importing)).

**The users' CSV gives "invalid username".** No spaces, accents or `ñ` in
the usernames ([ch. 8](08-users-and-teams.md#11-common-mistakes)).

## The judge does not work

Symptoms: submissions stay in *Compiling…* or *Evaluating…*; `doctor` says
"no judge is connected", "takes no jobs" or "job(s) waiting for a judge".

1. Look at the judge:
   ```sh
   sudo systemctl status cms-worker
   sudo journalctl -u cms-worker -n 50 --no-pager
   ```
2. Check the machine: `sudo cmsctl verify-host`. The most common `FAIL`s:
   - **cgroups v2 missing** ("Cannot open /run/isolate/cgroup"): the
     system boots with cgroups v1 (common on some VPSs or containers). On
     Ubuntu 22.04 or later v2 comes by default; if not, add
     `systemd.unified_cgroup_hierarchy=1` to the kernel line in
     `/etc/default/grub`, `sudo update-grub` and reboot. Some VPS
     providers do not allow it: choose another type of server.
   - **isolate not installed**: run the installer again.
3. Restart the judge: `sudo systemctl restart cms-worker`.
4. The pending submissions are evaluated by themselves when it comes back;
   nothing is lost.

The `WARN`s from `verify-host` (swap, hyperthreading, CPU frequency,
hugepages) do not prevent evaluation: they only make the times less
precise. For an important contest, apply them
([ch. 13](13-maintenance.md#10-before-an-important-contest)).

## 6. The sites do not open

1. `sudo cmsctl doctor`: "Health" and "Services" sections.
2. Is the service active? `sudo systemctl restart cms-contest-web` (or
   `cms-admin-web`, `cms-ranking-web`).
3. The firewall? If ufw is active, `doctor` warns if a port needs to be
   opened: `sudo ufw allow 8000/tcp`.
4. Another firewall (the cloud provider's)? Open the ports in its panel
   too.
5. With a domain: does the DNS point to the server? Are ports 80 and 443
   open? The certificates are requested on the first visit.

## 7. The scoreboard

**It still shows a deleted contest.** Since this version it disappears by
itself in less than a minute. If not: `sudo cmsctl doctor` ("scoreboard
push" line) and, as a last resort, empty it
([ch. 11](11-scoreboard.md#7-starting-the-scoreboard-from-scratch)).

**It is empty.** The contest needs a schedule and non-hidden
participations.

**Test people appear.** Mark their participations as hidden.

## 8. Access to the administration

**I lost the password.** `sudo cmsctl admin-password admin`.

**There is no administrator left.** `sudo cmsctl add-admin admin`.

**"Failed to log in." many times.** After several failed attempts, new
attempts are held back for a few seconds: wait and try again with the
correct password.

## 9. Asking for help

Gather this information (it contains no passwords):

```sh
sudo cmsctl doctor > doctor.txt 2>&1
sudo tail -n 80 /var/log/cms-install.log > installation.txt     # if the installation failed
sudo journalctl -u 'cms-*' --since "30 min ago" --no-pager | grep -v -i -e password -e secret > services.txt
cms version
```

Never share `/etc/cms/cms.yaml`, the backups or passwords.

---

**Previous:** [13. Maintenance and `cmsctl` reference](13-maintenance.md) ·
[Index](README.md)
