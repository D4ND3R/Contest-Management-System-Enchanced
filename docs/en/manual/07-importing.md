# 7. Importing contests, tasks and users

*Español: [Importar concursos, problemas y usuarios](../../es/manual/07-importar.md)*

If your tasks are already prepared in a known format, importing them is
much faster and safer than creating them by hand. This chapter explains
the formats, how to import from the administration and from the console,
how to update an already imported task and the most frequent errors. The
complete reference is in [../importing.md](../importing.md) and the own
format in [../package-format.md](../package-format.md).

## Contents

1. [What can be imported and from where](#1-what-can-be-imported-and-from-where)
2. [The formats](#2-the-formats)
3. [The Italian format (italy_yaml) inside](#3-the-italian-format-italy_yaml-inside)
4. [Importing a task from the administration](#4-importing-a-task-from-the-administration)
5. [Importing a whole contest from the console](#5-importing-a-whole-contest-from-the-console)
6. [Checking before importing](#6-checking-before-importing)
7. [Updating an already imported task or contest](#7-updating-an-already-imported-task-or-contest)
8. [Users and teams](#8-users-and-teams)
9. [Migrating from the original CMS](#9-migrating-from-the-original-cms)
10. [Warnings when importing](#10-warnings-when-importing)
11. [What if…](#11-what-if)

---

## 1. What can be imported and from where

| What | Administration | Console (`sudo cmsctl …`) |
|---|---|---|
| a task (zip) | yes | `import-task` |
| a contest with its tasks and users | no | `import-contest` |
| users (CSV) | yes ([ch. 8](08-users-and-teams.md)) | — |
| users from a contest package | no | `import-users` |
| teams | yes, CSV | `import-team` |
| a dump from the original CMS | no | `import-dump` |

The console accepts **a folder or a compressed file** (`.zip`, `.tar.gz`,
`.tgz` or `.tar`). If the archive contains a single folder with the
contest inside, that folder is used.

All imports **check everything first** and write nothing if they find an
error: either everything is imported, or nothing. And they notify the
judge so it evaluates what is new.

## 2. The formats

| Format | Recognized by | Brings |
|---|---|---|
| **italy_yaml** (the original CMS's) | `contest.yaml` (contest) or `task.yaml` (task) | contest, tasks, users, teams |
| **Polygon** (Codeforces) | `contest.xml` and `problems/` (contest) or `problem.xml` (task) | contest, tasks, users |
| **TPS** | `problem.json` | task |
| **Own format** (`problem_yaml`) | `problem.yaml` | task; it is what *Download as package* produces |

The format is detected automatically. If it is not recognized, the message
says which file each format looks for:

```
cms: error: couldn't autodetect the loader, please specify it with -L: no loader of contests
accepted /var/tmp/x (loaders of contests: italy_yaml wants contest.yaml;
polygon wants contest.xml and problems/)
```

To force a format: `-L italy_yaml`, `-L polygon`, `-L tps` or
`-L problem_yaml`.

## 3. The Italian format (italy_yaml) inside

It is the most common format in olympiads that use CMS. A contest is a
folder:

```
D4OI2026/
├── contest.yaml          ← the contest
├── baches/               ← one task per folder
│   ├── task.yaml         ← task data
│   ├── statement/
│   │   └── statement.pdf ← statement
│   ├── input/            ← input0.txt, input1.txt…
│   ├── output/           ← output0.txt, output1.txt…
│   ├── sol/              ← grader.cpp, headers, solutions
│   ├── check/            ← checker (optional)
│   ├── gen/GEN           ← subtasks (optional)
│   └── att/              ← attachments (optional)
├── estalagmitas/
└── xibalba/
```

`contest.yaml` (example):

```yaml
name: D4OI2026              # short name
description: Olimpiada D4 2026
start: 1767225600           # start (Unix seconds, UTC)
stop: 1767243600            # end
languages:                  # allowed languages
  - "C++17 / g++"
tasks:                      # task folders, in order
  - baches
  - estalagmitas
users:                      # optional
  - username: ana01
    first_name: Ana
    last_name: Pérez
    password: clave-ana
```

`task.yaml` (example):

```yaml
name: baches
title: Baches
time_limit: 1.0
memory_limit: 256
n_input: 25            # number of testcases
public_testcases: "0,1"
infile: ""             # empty = standard input
outfile: ""
score_precision: 0
```

> **Watch out for `languages`**: if `contest.yaml` does not include it, the
> contest allows the default languages (C, C++ and Pascal). If your tasks
> only have `grader.cpp`, add `languages: ["C++17 / g++"]` or fix it later
> on the contest page ([section 10](#10-warnings-when-importing)).

## 4. Importing a task from the administration

1. *Tasks* → *(create new task...)* → *Import a task package (zip)*.
2. Choose the zip and, if you want, the format (*detect* recognizes it
   automatically).
3. Tick *Update the task if it exists* if it is a new version of a task
   that is already there.
4. In *Add it to the contest:* choose the contest (optional).
5. Press *Preview*: it shows what is going to be created, the warnings and
   the errors. **Nothing is saved yet.**
6. If it looks right, confirm.

## 5. Importing a whole contest from the console

For whole contests (with tasks and users) the server console is used.
Upload the zip to the server (for example with `scp D4OI2026.zip
user@server:`) and:

```sh
sudo cmsctl import-contest -i --users D4OI2026.zip
```

- `-i`: also imports the tasks that do not exist yet.
- `--users`: creates the users and teams that `contest.yaml` brings.
  Without this option, the users must already exist.
- It works the same with a folder: `sudo cmsctl import-contest -i --users
  ~/D4OI2026`.

Typical output:

```
created contest D4OI2026 (id 1): 1 group
created task baches (id 1): 1 statement; 7 attachments; 1 dataset; 25 testcases
created task estalagmitas (id 2): 1 statement; 3 attachments; 1 dataset; 36 testcases
the dispatcher was notified
```

Afterwards, in the administration: review the contest (schedule,
languages) and test each task with *Test submissions*
([ch. 5](05-tasks.md#13-testing-the-task-with-solutions)).

> **Always with `sudo`.** `cmsctl` needs to read `/etc/cms/cms.yaml`,
> which only root and the `cms` group can read. Without `sudo` you will see
> an error "cannot read /etc/cms/cms.yaml: permission denied: run the
> command with sudo". With `sudo`, the stored files end up owned by the
> `cms` user, as they should.

## 6. Checking before importing

```sh
sudo cmsctl validate-contest D4OI2026.zip     # without touching the database
sudo cmsctl import-contest -i --users --dry-run D4OI2026.zip   # with the database, without writing
```

`--dry-run` shows exactly what would be created or changed and ends with
"dry run: nothing was written".

## 7. Updating an already imported task or contest

| I want to | Command |
|---|---|
| update the tasks of an existing contest without losing submissions | `sudo cmsctl import-contest -U D4OI2026.zip` |
| update the contest's settings and groups | `sudo cmsctl import-contest -u D4OI2026.zip` |
| update a task | `sudo cmsctl import-task -u baches.zip` |
| import a task and append it to the end of a contest | `sudo cmsctl import-task -c D4OI2026 nuevo.zip` |
| add corrected testcases as a new, inactive dataset | `sudo cmsctl import-dataset -d "casos corregidos" baches.zip` |

The submissions and the live dataset are kept. If the live dataset's
testcases changed, reevaluate ([ch. 9](09-during-the-contest.md)).

## 8. Users and teams

The simplest is the CSV from the administration
([ch. 8](08-users-and-teams.md)). From a contest package:

```sh
sudo cmsctl import-users -c D4OI2026 D4OI2026.zip
sudo cmsctl import-team D4OI2026.zip
```

## 9. Migrating from the original CMS

If you have contests in an installation of the original CMS, export them
there with `cmsDumpExporter` and import them here with `sudo cmsctl
import-dump` (it keeps submissions, results, questions…). Details in
[../importing.md](../importing.md#moving-from-cms-dumps).

## 10. Warnings when importing

Warnings (`warning:`) do not prevent importing, but you should read them:

```
warning: /var/tmp/x/baches/sol: grader only for C++11 / g++, C++14 / g++, C++17 / g++, C++20 / g++:
submissions in other languages cannot be judged
warning: task baches: dataset "Default": no grader.%l for C11 / gcc, Pascal / fpc: submissions in these
languages cannot be judged; allow only the languages the task supports (the contest's languages in the
admin site, or "languages:" in contest.yaml) or add the missing grader files
```

It means: the task only brings a grader for C++, but the contest allows C
and Pascal. A submission in C or Pascal would fail to compile. Solution:
on the contest page, leave only the C++ languages allowed
([ch. 4](04-contests.md)), or add `languages:` to `contest.yaml` before
importing.

## 11. What if…

**…"couldn't autodetect the loader".** The zip has no `contest.yaml` (nor
`contest.xml`) at the root or inside a single folder. Check its contents
with `unzip -l file.zip`. If it is a single task, use `import-task`
instead of `import-contest`.

**…"password authentication failed for user "cms"".** You ran the command
without `sudo`. Use `sudo cmsctl …`.

**…"already exists in the database".** It was already imported: use
`-u`/`-U` to update it, or delete it first in the administration.

**…"users not found in the database".** The contest brings participants
that do not exist: add `--users` or import them beforehand.

**…the contest appears but without a schedule or with extra languages.**
Fix it on the contest page ([ch. 4](04-contests.md)).

---

**Next:** [8. Users, teams and participations](08-users-and-teams.md) ·
**Previous:** [6. Task types and score types](06-task-and-score-types.md) ·
[Index](README.md)
