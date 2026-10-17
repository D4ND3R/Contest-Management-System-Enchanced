# Installation

*Español: [../es/instalacion.md](../es/instalacion.md).*

One command installs everything on a clean **Ubuntu 22.04 / 24.04** or
**Debian 12** machine (amd64 or arm64): PostgreSQL, Valkey (or Redis ≥ 7),
isolate, the compilers, the `cms` binary, one systemd unit per service, the
firewall, scheduled backups and the first administrator. For containers see
[Docker](docker.md).

## Requirements and sizing

| Part | Minimum for a contest like SPEC N1 (500 contestants) |
|---|---|
| Web side: the three sites, dispatcher, monitor, PostgreSQL and Valkey | **2 vCPU, 4 GB RAM**, 20 GB SSD + the test data and submissions |
| Each judge | **one physical core per parallel job** (`worker.concurrency`), 1–2 GB RAM per job, cgroups v2, SMT off for stable times |
| Network | ports 80/443 (domain mode) or the three site ports; judges on other machines reach Valkey (6379) and the blob server (8891) |

- Judging on the web machine is fine for small contests and rehearsals
  (`--judge-concurrency 1`); for a real contest put the judges on their own
  machines (or reserve cores) so that a busy site never changes measured times.
- Disk: blobs (statements, test cases, submissions, executables) grow with the
  contest; backups are full copies, kept 14 by default.
- The judge needs **cgroups v2** (Ubuntu 22.04+, Debian 12+ have it by default).
  `cms ctl verify-host` reports what to fix.

## Mode 1: a domain with HTTPS

```sh
curl -fsSL https://raw.githubusercontent.com/D4ND3R/Contest-Management-System-Enchanced/main/scripts/install.sh \
  | sudo bash -s -- --domain contest.example.org --email you@example.org
```

The three sites get their own names (DNS A/AAAA records must point to the
machine before the first visit, when Caddy obtains the certificates):

| Site | Address |
|---|---|
| Contestant site | `https://contest.example.org/` |
| Administration | `https://admin.contest.example.org/` (`--admin-domain` to change) |
| Scoreboard | `https://ranking.contest.example.org/` (`--ranking-domain`) |

Subdomains rather than paths: like CMS, every page uses absolute paths
(`/tasks/…`, `/static/…`), so each site needs the root of its host, and
separate hosts keep the cookies of the administration away from the other
sites. The services listen on localhost; Caddy terminates TLS, compresses
everything except the live events (`*/events`) and forwards to them
(`num_proxies: 1`). `--admin-allow 10.0.0.0/8` lets only that network open
the administration.

## Mode 2: ports, no domain

```sh
curl -fsSL …/scripts/install.sh | sudo bash -s -- --http-ports 8000,8001,8002
```

Contestant site, administration and scoreboard on those ports (plain HTTP:
use it on a closed network, such as a contest hall). The administration
learns the other sites as `:8000` and `:8002` on its Server page, which means
"the host this admin page was opened from, on that port", so links work
whether the machine is reached by name or by IP. `--admin-allow CIDR`
restricts the admin port in the firewall.

## Extra judges

On the main machine allow the judges' network (it opens Valkey and the blob
server only to it):

```sh
… | sudo bash -s -- --domain contest.example.org --allow-judges 10.0.0.0/24
```

The summary names two files: `/etc/cms/judges/blob-token` and
`/etc/cms/judges/valkey-url`. They are secrets: copy them to each judging
machine (for example with `scp`) and run there:

```sh
… | sudo bash -s -- --worker-only --dispatcher http://10.0.0.1:8891 \
      --blob-token-file blob-token --valkey-url-file valkey-url --judge-concurrency 4
```

A judging machine gets isolate, the compilers and `cms-worker.service` only.
It never receives `secret_key` nor database access (D12, D25). Each machine
appears on the Judges page of the administration under its host name. Use a
private network or a VPN between the machines: Valkey traffic is not
encrypted.

## What the installer does

1. Packages: PostgreSQL (with `conf.d/cms.conf` sized for 4 GB), Valkey
   (or Redis ≥ 7; the Redis repository on Ubuntu 22.04, which only ships
   6.0) with a password, `appendonly` and `noeviction` (queues must never be
   evicted), isolate from its authors' repository (built from the same
   release on arm64), compilers (`--compilers all|basic|none`), Caddy in
   domain mode, ufw.
2. The `cms` binary from the latest GitHub release, verified against
   `checksums.txt` (`--version vX.Y.Z`, or `--binary PATH` for a local
   build), in `/usr/local/bin`, plus the `cmsctl` link.
3. Secrets, generated **once**: `secret_key` (in `cms.yaml`), the database
   and Valkey passwords (`/etc/cms/secrets/`), the token of the local judge.
   The scoreboard credentials are derived from `secret_key`, so the
   dispatcher and ranking-web agree without another secret.
4. `/etc/cms/cms.yaml`, **only if it does not exist**. Settings that depend on
   the mode (listen addresses, proxies, blob server) go to `/etc/cms/cms.env`,
   rewritten on every run.
5. `cms migrate`, the first administrator (`--admin-user`, default `admin`)
   with a **generated password printed once** at the end, and the public
   addresses of the Server page.
6. systemd units `cms-contest-web`, `cms-admin-web`, `cms-ranking-web`,
   `cms-dispatcher`, `cms-monitor` (user `cms`, sandboxed: read-only system,
   no devices, no new privileges, restricted system calls) and `cms-worker`
   (root, as isolate requires), grouped in `cms.target`;
   `cms-backup.timer` (daily, 14 kept: `--backup-keep`, `--backup-calendar`).
7. ufw: SSH (the port sshd uses) first, then 80/443 or the three ports.
8. `cms ctl verify-host` and a summary with the addresses.

The local judge reads files through the blob server of the dispatcher on
localhost, like a remote judge: it never writes in the blob directory of the
`cms` user.

## Running it again

Re-running is safe and is how you change the mode or the options: secrets,
`cms.yaml`, the administrator and the database are kept; units, the Caddy
site, firewall rules and `cms.env` are rewritten. Without `--domain`,
`--http-ports` or `--worker-only` the previous mode is reused
(`/etc/cms/install.env`). The binary is replaced only with `--version` or
`--binary`: upgrade with [`cmsctl upgrade`](upgrades-and-backups.md).

If the scoreboard port changes, update `dispatcher.rankings` in `cms.yaml`
(the installer warns).

## Files

| Path | Contents |
|---|---|
| `/usr/local/bin/cms`, `cmsctl` | the binary (and `cms.prev`, the previous one after an upgrade) |
| `/etc/cms/cms.yaml` | configuration (root:cms 0640; contains secrets) |
| `/etc/cms/cms.env`, `worker.env` | settings written by the installer on every run |
| `/etc/cms/secrets/` | generated passwords and tokens |
| `/var/lib/cms/blobs`, `ranking`, `backups` | files, scoreboard state, backups |
| `/var/lib/cms/worker`, `/var/cache/cms` | judge work directory, caches |

## Uninstall

```sh
… | sudo bash -s -- --uninstall
```

stops and removes the units, the binary and the Caddy site, and **keeps**
`/etc/cms`, `/var/lib/cms` (with the backups) and the database, so a later
installation picks them up. To delete them too:

```sh
… | sudo bash -s -- --uninstall --purge --yes
```

PostgreSQL, Valkey, Caddy, isolate and the compilers stay installed (remove
them with apt if nothing else uses them); firewall rules stay as well.
