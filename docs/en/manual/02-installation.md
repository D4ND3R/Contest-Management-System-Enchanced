# 2. Installation

*Español: [Instalación](../../es/manual/02-instalacion.md)*

This chapter explains how to install CMS on a server, what the installer
does, how to read its messages when something fails, how to do a **clean
install** that wipes every trace of a previous one, and how to uninstall.

## Contents

1. [Before you start](#1-before-you-start)
2. [Choosing the mode: domain or ports](#2-choosing-the-mode-domain-or-ports)
3. [Checking the server without installing anything](#3-checking-the-server-without-installing-anything)
4. [Installing](#4-installing)
5. [What the installer does, step by step](#5-what-the-installer-does-step-by-step)
6. [The final summary](#6-the-final-summary)
7. [When the installation fails](#7-when-the-installation-fails)
8. [Clean install (`--clean`)](#8-clean-install---clean)
9. [The firewall](#9-the-firewall)
10. [All the options](#10-all-the-options)
11. [Judges on other machines](#11-judges-on-other-machines)
12. [Uninstalling](#12-uninstalling)
13. [What if…](#13-what-if)

---

## 1. Before you start

**Operating system:** Ubuntu 22.04, 24.04 or 26.04 LTS, or Debian 12, with
systemd. On 26.04 the C# compiler does not exist and is skipped; the other
languages work.

**Minimum hardware:** 2 vCPU and 4 GB of RAM for a contest of about 100
people with one judge. Every judge that evaluates in parallel needs a core
of its own. Sizing details are in [../installation.md](../installation.md).

**Access:** a user with `sudo`. The installer runs as root.

**What gets installed:** PostgreSQL (database), Valkey (queues), isolate
(the sandbox where the contestants' programs run), the compilers, Caddy
(only with a domain), the CMS systemd services and a daily backup. If you
already have PostgreSQL or Valkey/Redis installed, they are reused.

> **Servers with other applications.** The installer never uninstalls
> anything and, since this version, does not turn the firewall on if it was
> off (it could block your other services). It does need the ports it uses
> to be free: it checks that before touching anything (section 3).

## 2. Choosing the mode: domain or ports

| | With a domain (`--domain`) | With ports (`--http-ports`) |
|---|---|---|
| Addresses | `https://concurso.ejemplo.org`, `https://admin.concurso.ejemplo.org`, `https://ranking.concurso.ejemplo.org` | `http://IP:8000`, `http://IP:8001`, `http://IP:8002` |
| Encryption | HTTPS with automatic certificates (Caddy) | plain HTTP |
| Requirements | three DNS records pointing to the server; ports 80 and 443 free | the three ports free |
| Recommended for | contests over the internet | local networks, tests |

With a domain, first create the three DNS records (type A, or AAAA for
IPv6) pointing to the server's IP. The installer warns you if they do not
point correctly.

## 3. Checking the server without installing anything

Before installing, you can ask the installer to only check the server:

```sh
curl -fsSL https://raw.githubusercontent.com/D4ND3R/Contest-Management-System-Enchanced/main/scripts/install.sh \
  | sudo bash -s -- --check --http-ports 8000,8001,8002
```

It prints a table with one line per check, for example:

```
==> preflight checks
  PASS  operating system         ubuntu 26.04
  PASS  disk (/var)              180 GiB free
  FAIL  port 6379 (Valkey)       redis-server (pid 1450) already listens; cms installs valkey-server, which needs the port
                                 fix: if nothing else uses it: sudo systemctl disable --now redis-server (--clean does not remove other software)
  PASS  PostgreSQL               running
  ...
  1 check(s) FAILED, 0 warning(s). Nothing was changed: fix what the
  'fix:' lines say and run the same command again.
```

- **PASS**: fine.
- **WARN**: it can be installed, but you should fix it (for example,
  missing cgroups v2: the judge could not limit memory precisely).
- **FAIL**: nothing is installed until it is fixed. The `fix:` line says
  exactly what to do.

This same check always runs at the start of a normal installation: if
there is any FAIL, the installer stops **before changing anything**.

What is checked: that it runs as root, the operating system, that systemd
is process 1, the architecture, the free space in `/var` (at least about 3
GiB), the memory, internet access (when something has to be downloaded),
that apt is not busy with another process, that the ports are free or
belong to CMS, that port 6379 is not used by another Redis/Valkey, that
PostgreSQL exists or can be installed, that there is no `cms` database
belonging to another program, cgroups v2 and the domain's DNS.

## 4. Installing

With a domain:

```sh
curl -fsSL https://raw.githubusercontent.com/D4ND3R/Contest-Management-System-Enchanced/main/scripts/install.sh \
  | sudo bash -s -- --domain concurso.ejemplo.org --email tu@correo.org
```

With just the IP:

```sh
curl -fsSL https://raw.githubusercontent.com/D4ND3R/Contest-Management-System-Enchanced/main/scripts/install.sh \
  | sudo bash -s -- --http-ports 8000,8001,8002
```

It takes between 5 and 20 minutes depending on the connection (the
compilers are the heaviest part; `--compilers basic` installs only C, C++,
Python 3 and Java and is much faster).

Everything shown on screen is also kept in `/var/log/cms-install.log`
(without passwords).

**Running the same command again is safe**: it only does what is missing,
does not delete data and does not change the administrator's password. It
is the normal way to finish an installation that failed, once you have
fixed the cause.

## 5. What the installer does, step by step

Each step is announced with a `==>` line. Knowing what each one does helps
you understand an error:

| Step (`==>`) | What it does |
|---|---|
| `preflight checks` | the check from section 3 |
| `installing base packages` | basic packages with apt (curl, gnupg…) |
| `installing …/cms` | downloads the `cms` program of the latest published version and verifies its checksum |
| `creating the cms user and directories` | creates the `cms` system user and `/etc/cms`, `/var/lib/cms`, `/var/cache/cms` |
| `installing isolate…` | installs the sandbox |
| `installing compilers` | installs the language compilers |
| `installing PostgreSQL` | installs/configures PostgreSQL, creates the `cms` user and database |
| `installing valkey-server` | installs/configures Valkey with a password and persistence |
| `writing /etc/cms/cms.yaml` | writes the configuration (the keys are generated only once) |
| `applying the database migrations` | creates the tables |
| `writing the systemd units` / `starting the services` | creates and starts the six services |
| `configuring the firewall` | see section 9 |
| judge verification | `cms ctl verify-host`: checks that the sandbox works |
| doctor | `cms ctl doctor`: checks the whole installation ([ch. 14](14-troubleshooting.md)) |

## 6. The final summary

When it finishes, a box like this is shown:

```
======================================================================
 cms v0.2.0 installed
======================================================================
 Contestant site:  http://203.0.113.10:8000/
 Administration:   http://203.0.113.10:8001/
 Scoreboard:       http://203.0.113.10:8002/
 ...
 First administrator: admin
 Password:            Xxxxxxxxxxxxxxxxxxxx
 It is shown only now and stored nowhere in clear (not in the log): write it down.
```

- **The three addresses**: contestant site, administration and public
  scoreboard.
- **The `admin` user and its password**: they are shown **only once** and
  are not stored anywhere (not even in the log). Write it down somewhere
  safe. If you lose it: `sudo cmsctl admin-password admin` generates a new
  one ([ch. 3](03-first-steps.md)).
- If it says **`installed with problems`**, something is not right: the
  lines above (`WARNING`, the result of `doctor`) say what and how to fix
  it.

On a virtual machine or a server without cgroups v2 you will see `Judge
check: FAILED`: the sites work, but the judge cannot evaluate safely
([ch. 14](14-troubleshooting.md#the-judge-does-not-work)).

## 7. When the installation fails

If any step fails, the installer stops and shows a **failure report**:

```
######################################################################
  INSTALLATION FAILED
######################################################################
  Step:       installing base packages
  Command:    apt-get -o DPkg::Lock::Timeout=600 "$@"
  Exit code:  100 (line 452, in apt_get)
  Last lines of output:
    | E: Failed to fetch https://security.ubuntu.com/ubuntu/pool/main/g/gnupg2/gpg_2.4.4-2ubuntu17.6_amd64.deb ...
    | E: Unable to fetch some archives, maybe run apt-get update or try with --fix-missing?

  DIAGNOSIS
    The package mirrors cannot be reached.
    Fix: check the network and the apt sources (sudo apt-get update shows which
         one fails) and run the installer again.

  Full log:   /var/log/cms-install.log
  Running the same command again is safe once the cause is fixed.
  --clean backs up and removes every trace of cms, then installs from zero.
######################################################################
```

How to read it:

1. **Step**: which step it was in (the table in section 5).
2. **Command / Exit code**: the exact command that failed.
3. **Last lines of output**: the last thing that command printed.
4. **DIAGNOSIS**: the probable cause and **the fix**. The installer
   recognizes the most common problems: apt busy with another
   installation, no network or DNS, a blocking proxy, full disk, a busy
   port (and which program holds it), a `cms` database belonging to another
   program, PostgreSQL password errors, Valkey/Redis that does not start
   (with its log), a service that does not start (with its log), a failed
   download or checksum, unsupported system, failed migration.
5. Fix it and **run the same command again**.

If the diagnosis is not enough, the full log is in
`/var/log/cms-install.log`. To ask for help, share the box and the last 50
lines of the log (`sudo tail -n 50 /var/log/cms-install.log`): they do not
contain passwords.

The most frequent real problems and their solutions are in
[chapter 14](14-troubleshooting.md).

## 8. Clean install (`--clean`)

A **clean install** completely wipes any previous CMS installation
(configuration, data, services, database, leftover files) and installs
again from scratch, so you start from a healthy base. Use it when:

- an installation was left half-done and you do not know what state it is
  in;
- you want to start over without the test contests and users;
- there is an old `cms` database (for example from the original CMS) that
  prevents the installation.

```sh
curl -fsSL https://raw.githubusercontent.com/D4ND3R/Contest-Management-System-Enchanced/main/scripts/install.sh \
  | sudo bash -s -- --clean --http-ports 8000,8001,8002
```

1. It shows **exactly what it is going to delete** and asks for
   confirmation (type `yes`). With `--yes` it does not ask.
2. It **backs up first** the `cms` database and `/etc/cms` in
   `/var/backups/cms/pre-clean-DATE…` (with `--no-backup` it does not back
   up).
3. It deletes: the CMS services and timers, `/etc/cms`, `/var/lib/cms`
   (task and submission files, scheduled backups), `/var/cache/cms`, the
   `cms` program, the PostgreSQL database and `cms` user, the CMS
   configuration and keys in Valkey, leftover isolate boxes, the CMS site
   in Caddy, the CMS ufw rules, its sysctl file and the `cms` system user.
4. It **never** uninstalls packages or touches other programs: PostgreSQL,
   Valkey, Caddy, isolate and the compilers are reused.
5. It installs from scratch and shows a new administrator password.

> **Warning:** `--clean` deletes all contests, users and submissions. The
> backup in `/var/backups/cms` lets you recover them
> ([ch. 13](13-maintenance.md#restoring-a-backup)).

## 9. The firewall

- If **ufw was already active**, the installer adds the CMS rules (SSH
  first, then the sites' ports or 80/443 with a domain).
- If **ufw was off**, it leaves it off and at the end shows the commands
  to open the ports in case you ever turn it on. This way it does not
  block other services on the server.
- `--firewall` turns it on anyway (after allowing SSH); `--no-firewall`
  does not touch it at all.
- `--admin-allow 10.0.0.0/8` makes only that network able to open the
  administration (recommended if the organizers are on a known network).

## 10. All the options

| Option | What it does |
|---|---|
| `--domain NAME` | HTTPS with Caddy: contestants at NAME, administration at admin.NAME, scoreboard at ranking.NAME |
| `--admin-domain`, `--ranking-domain` | other names for the administration and the scoreboard |
| `--email EMAIL` | email for the certificates |
| `--http-ports C,A,R` | HTTP on those ports (default 8000,8001,8002) |
| `--check` | only check, change nothing |
| `--clean` | clean install (section 8) |
| `--no-backup` | with `--clean` or `--purge`: do not back up before deleting |
| `--yes` | do not ask (needed with `--clean`/`--purge` without a terminal) |
| `--version vX.Y.Z` | install that version (default, the latest) |
| `--binary PATH` | install that `cms` program instead of downloading it |
| `--compilers all\|basic\|none` | judge languages (default all) |
| `--judge-concurrency N` | parallel evaluations of the local judge (default 1; at most one per physical core) |
| `--admin-user NAME` | name of the first administrator (default `admin`) |
| `--admin-allow CIDR` | only that network can open the administration |
| `--firewall` / `--no-firewall` | section 9 |
| `--allow-judges CIDR` | judges on other machines of that network |
| `--backup-keep N` | daily backups to keep (14; 0 disables) |
| `--backup-calendar SPEC` | time of the backups (systemd OnCalendar format) |
| `--worker-only` and its options | install only a judge (section 11) |
| `--uninstall`, `--purge` | section 12 |
| `--force-os` | try on an unsupported distribution |

## 11. Judges on other machines

For large contests you can add machines that only evaluate. See
[../installation.md](../installation.md#extra-judges) and
[../judges.md](../judges.md).

## 12. Uninstalling

- `--uninstall`: removes the services and the program; it **keeps** the
  configuration, the data and the database (you can reinstall later).
- `--uninstall --purge`: also deletes the database, the data and the
  configuration (it backs up first in `/var/backups/cms`, except with
  `--no-backup`).

```sh
curl -fsSL https://raw.githubusercontent.com/D4ND3R/Contest-Management-System-Enchanced/main/scripts/install.sh \
  | sudo bash -s -- --uninstall --purge
```

## 13. What if…

**…the installer says port 6379 is busy with `redis-server`.** Another
Redis already uses that port. If you do not need it:
`sudo systemctl disable --now redis-server` and install again. If another
application uses it, see [chapter 14](14-troubleshooting.md).

**…it says there is a `cms` database that this installer did not create.**
It belongs to another program (for example, the original CMS). If you do
not need it, use `--clean` (it backs it up before deleting it).

**…it does not show the administrator's password.** The administrator
already existed from a previous installation: `sudo cmsctl admin-password
admin`.

**…it ends with `installed with problems`.** Read the `WARNING` lines and
the result of `doctor` right above the summary, or run `sudo cmsctl
doctor` ([ch. 14](14-troubleshooting.md)).

**…the SSH connection dropped halfway through the installation.** Run the
same command again.

---

**Next:** [3. First access and admin accounts](03-first-steps.md) ·
**Previous:** [1. Concepts](01-concepts.md) ·
[Index](README.md)
