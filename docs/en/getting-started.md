# Getting started: install and use CMS

> This is the short guide. The **[step-by-step manual](manual/README.md)** explains everything in detail.

*Español: [../es/primeros-pasos.md](../es/primeros-pasos.md).*

A short guide to a working contest on a server with **Ubuntu 26.04 LTS**
(22.04, 24.04 and Debian 12 work too). Each step links to the full guide.

## 1. What you need

- A clean Ubuntu server with `sudo` and at least 2 vCPU and 4 GB of RAM
  ([sizing](installation.md)).
- One of:
  - **a domain** (recommended): three DNS records pointing at the server,
    for example `contest.example.org`, `admin.contest.example.org` and
    `ranking.contest.example.org`; HTTPS certificates are obtained
    automatically;
  - **just the IP address**: the three sites use plain HTTP on ports 8000
    (contestants), 8001 (administration) and 8002 (scoreboard).

## 2. Install

One command downloads the latest published release and installs
everything. With a domain:

```sh
curl -fsSL https://raw.githubusercontent.com/D4ND3R/Contest-Management-System-Enchanced/main/scripts/install.sh \
  | sudo bash -s -- --domain contest.example.org --email you@example.org
```

or with just the IP address:

```sh
curl -fsSL https://raw.githubusercontent.com/D4ND3R/Contest-Management-System-Enchanced/main/scripts/install.sh \
  | sudo bash -s -- --http-ports 8000,8001,8002
```

The binary is checked against the release's `checksums.txt`. To pin a
version: `--version v0.1.0`.

Before changing anything it checks the server (busy ports, a database of
another program, disk, network) and stops with the fix if something is
wrong; `--check` runs only those checks. If a step fails later, it shows
which step, why and how to fix it (everything goes to
`/var/log/cms-install.log`). `--clean` removes any previous installation
(backing it up first) and starts from zero.

The installer sets up PostgreSQL, Valkey, the `isolate` sandbox, the
compilers, the `systemd` services and daily backups (the firewall only if
it was already active). At the end it prints:

- the addresses of the **three sites**;
- the `admin` user and its **password**, shown **only once**: write it
  down somewhere safe (if lost: `sudo cmsctl admin-password admin`).

If anything fails, running the same command again is safe. Useful options:
`--admin-allow 10.0.0.0/8` (only that network may open the administration)
and `--compilers basic` (only C, C++, Python 3 and Java; installs faster).
All of them: `curl -fsSL https://raw.githubusercontent.com/D4ND3R/Contest-Management-System-Enchanced/main/scripts/install.sh | bash -s -- --help`.

## 3. Check that everything works

```sh
sudo cmsctl doctor                         # checks everything and says how to fix what fails
sudo cmsctl verify-host                    # checks the judging machine in depth
```

See [troubleshooting](manual/14-troubleshooting.md).

## 4. Prepare a contest

Log in to the **administration** as `admin` with the password from step 2.

1. **Create the contest**: *Contests* menu → *(create new contest...)*. On
   the contest page set the name, the description, the **start and end
   times** and the allowed languages; save with *Update*.
2. **Add tasks**, in one of two ways:
   - **import a package** (zip in this installation's format, italy_yaml,
     Polygon or TPS): *Tasks* → *(create new task...)* → *Import a task
     package (zip)*; pick the contest in *Add it to the contest*, check the
     preview and confirm ([importing](importing.md));
   - **create it in the browser**: statement, test cases and solutions
     ([creating a problem](creating-a-problem.md)).
   On the task page, *Test submissions* lets you submit the sample solutions to
   check that the judge gives the expected score.
3. **Register the contestants**: *Users* → at the bottom of the page,
   *Import users*. Upload a CSV like this one:

   ```csv
   username,password,first_name,last_name,email
   ana01,ana-secret,Ana,Smith,ana@example.org
   bob02,bob-secret,Bob,Jones,
   ```

   and pick the contest in *Also add them to contest*. If any row is wrong
   nothing is imported and the page says which line to fix. The email is
   optional. Users can also be created one by one with
   *(create new user...)*.

A whole contest (tasks and users) can also be imported from the command
line: `sudo -u cms cmsctl import-contest FOLDER` ([importing](importing.md)).

## 5. During the contest

- Contestants open the **contestant site**, log in with their username and
  password, read the statements, submit solutions and see the results live
  ([contestant site](contestant-site.md)).
- In the administration, inside the contest:
  - *Submissions*: every submission and its evaluation;
  - *Questions*: answer the contestants' questions;
  - *Announcements*: notices for everybody;
  - *Ranking*: the full standings;
  - *Judges* and *Resource usage*: state of the judge and the server.
- The public **scoreboard** is the third address from step 2; it can be
  made secret or frozen at the end ([scoreboard](scoreboard.md)).

## 6. Backups and upgrades

- A **daily backup** is written to `/var/lib/cms/backups`. For one right
  now (for example before the contest):
  `sudo systemctl start cms-backup.service`.
- **Upgrade** to the latest published release:

  ```sh
  sudo cmsctl upgrade
  ```

  It backs up, migrates the database and rolls back by itself if anything
  fails ([upgrades and backups](upgrades-and-backups.md)).

## More

[Installation](installation.md) (judges on other machines, uninstalling) ·
[Operation](operation.md) · [Administration](administration.md) ·
[Configuration](configuration.md) · [Troubleshooting](troubleshooting.md)
