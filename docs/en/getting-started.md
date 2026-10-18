# Getting started: install and use CMS

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

## 2. Build the program

Until a release is published on GitHub, build the binary on the server
itself (it takes a couple of minutes):

```sh
sudo apt-get update && sudo apt-get install -y git make curl
curl -fsSL https://go.dev/dl/go1.27.1.linux-amd64.tar.gz | sudo tar -C /usr/local -xz
export PATH=/usr/local/go/bin:$PATH

git clone https://github.com/D4ND3R/Contest-Management-System-Enchanced.git cms
cd cms
make build            # writes the program to bin/cms
```

(On an ARM server use `go1.27.1.linux-arm64.tar.gz`.)

If the repository is private, `git clone` asks for your GitHub username and,
as the password, a *personal access token* (or use an SSH key and the
address `git@github.com:D4ND3R/Contest-Management-System-Enchanced.git`).

## 3. Install

From the `cms` folder, with a domain:

```sh
sudo bash scripts/install.sh --binary bin/cms --domain contest.example.org --email you@example.org
```

or with just the IP address:

```sh
sudo bash scripts/install.sh --binary bin/cms --http-ports 8000,8001,8002
```

The installer sets up PostgreSQL, Valkey, the `isolate` sandbox, the
compilers, the `systemd` services, the firewall and daily backups. At the
end it prints:

- the addresses of the **three sites**;
- the `admin` user and its **password**, shown **only once**: write it
  down somewhere safe.

If anything fails, running the same command again is safe. Useful options:
`--admin-allow 10.0.0.0/8` (only that network may open the administration)
and `--compilers basic` (only C, C++, Python 3 and Java; installs faster).
All of them: `sudo bash scripts/install.sh --help`.

## 4. Check that everything works

```sh
sudo cmsctl verify-host                    # the judge can run programs
systemctl status cms.target 'cms-*'        # every service "active"
```

If `verify-host` reports a problem, see [troubleshooting](troubleshooting.md).

## 5. Prepare a contest

Log in to the **administration** as `admin` with the password from step 3.

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

## 6. During the contest

- Contestants open the **contestant site**, log in with their username and
  password, read the statements, submit solutions and see the results live
  ([contestant site](contestant-site.md)).
- In the administration, inside the contest:
  - *Submissions*: every submission and its evaluation;
  - *Questions*: answer the contestants' questions;
  - *Announcements*: notices for everybody;
  - *Ranking*: the full standings;
  - *Judges* and *Resource usage*: state of the judge and the server.
- The public **scoreboard** is the third address from step 3; it can be
  made secret or frozen at the end ([scoreboard](scoreboard.md)).

## 7. Backups and upgrades

- A **daily backup** is written to `/var/lib/cms/backups`. For one right
  now (for example before the contest):
  `sudo systemctl start cms-backup.service`.
- **Upgrade** to a new version of the code:

  ```sh
  cd cms && git pull && make build
  sudo cmsctl upgrade --binary bin/cms
  ```

  It backs up, migrates the database and rolls back by itself if anything
  fails ([upgrades and backups](upgrades-and-backups.md)).

## More

[Installation](installation.md) (judges on other machines, uninstalling) ·
[Operation](operation.md) · [Administration](administration.md) ·
[Configuration](configuration.md) · [Troubleshooting](troubleshooting.md)
