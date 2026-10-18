# Importing contests, tasks and CMS dumps

*Español: [../es/importar.md](../es/importar.md).*

The `sudo cmsctl import-*` commands replace CMS's `cmsImportContest`,
`cmsImportTask`, `cmsImportUser`, `cmsImportTeam`, `cmsImportDataset` and
`cmsDumpImporter`, with the same options. They need the database and blob
store settings (the same `cms.yaml` as the services).

Every import:

- **validates first** and writes nothing if there are errors (unknown task
  or score type, invalid parameters, unknown language, missing managers,
  missing files…);
- stores the files in the blob store and writes every row in **one
  transaction**: everything is imported or nothing is;
- never writes into the package directory;
- accepts `--dry-run`: checks everything and prints what would change,
  without writing;
- notifies the dispatcher to judge what is new.

## Formats

| Format | `-L` | What it carries |
|---|---|---|
| italy_yaml (CMS) | `italy_yaml` | contest (`contest.yaml`), task (`task.yaml`, `gen/GEN`), users, teams |
| Polygon | `polygon` (`polygon_task`, `polygon_contest`, `polygon_user`) | task (`problem.xml`, `cms_conf.py`), contest (`contest.xml`), users (`contestants.txt`) |
| TPS | `tps` (`tps_task`) | task (`problem.json`) |
| Own format | `problem_yaml` (`package`) | task (`problem.yaml`), directory or zip; see [Package format](package-format.md) |

The path may be a directory or a `.zip`, `.tar.gz`/`.tgz` or `.tar` file
(extracted to a temporary directory; if it holds a single directory, that
one is used). Without `-L` the format is detected; if more than one recognizes the
directory, choose it with `-L`.

Checkers and managers given as C++ source (Polygon, TPS, own format) **are
compiled at import time** with CMS's options (Polygon uses CMS's patched
`testlib.h`). The importing machine needs `g++` and the same architecture
as the judges. Polygon's `cms_conf.py` is read as Python literals and never
run.

## Commands

```sh
# check a task without a database
sudo cmsctl validate-task path/to/task
sudo cmsctl validate-contest path/to/contest

# import a contest with its tasks (-i) and its users
sudo cmsctl import-contest -i --users path/to/contest

# update the tasks of an existing contest keeping the submissions
sudo cmsctl import-contest -U path/to/contest

# import a task and append it to a contest
sudo cmsctl import-task -c mycontest path/to/task

# update a task (submissions are kept)
sudo cmsctl import-task -u path/to/task

# add the judging data as a new, inactive dataset
sudo cmsctl import-dataset -d "fixed tests" path/to/task

# users and teams
sudo cmsctl import-users -c mycontest path/to/contest
sudo cmsctl import-team path/to/contest

# export a task in the own format
sudo cmsctl export-task sum sum.zip
```

`sudo cmsctl help <command>` explains every option.

### Updating a task

As in CMS, a dataset with the description of an existing one is updated in
place (its testcases by name: a changed testcase keeps its evaluations) and
a dataset with a new description is added **inactive**. The active dataset
and the submissions are kept. If the judging data of the active dataset
changed, reevaluate it:

```sh
sudo cmsctl reevaluate --dataset DATASET_ID --level evaluation
```

CMS uses `.itime` files inside the package to know what changed; here the
package is compared with the database.

## Moving from CMS (dumps)

On the CMS installation:

```sh
cmsDumpExporter -c CONTEST_ID contest.tar.gz
```

On this one:

```sh
sudo cmsctl import-dump --dry-run contest.tar.gz   # check
sudo cmsctl import-dump contest.tar.gz
```

- Directories, zip and tar (`.tar`, `.tar.gz`, `.tar.bz2`) of data model
  versions 14 (CMS 1.2) to 49 are accepted; update older ones first with
  CMS's `cmsDumpUpdater`.
- Contests, groups, tasks, datasets, users, teams, participations,
  announcements, questions, messages, submissions with tokens, results and
  evaluations, and user tests are imported. Scores are computed from the
  dump's results and the dispatcher judges whatever is missing.
- Every file is checked against its name (SHA-1) and stored by SHA-256. A
  dump exported **without files** (`cmsDumpExporter -F`) cannot be
  imported: export it again without `-F`.
- A dump exported with `-c` also has the other contests its contestants
  took part in, but not their files: those tasks and submissions are
  imported with the files marked as deleted, with a warning. To keep them,
  export every contest needed with `-c`.
- If the database already has contests, tasks, users or teams with the same
  names, the dump is refused. Options:
  - `-d`: first deletes the contests, tasks, users and teams of the
    database (not the admins or the server settings), like
    `cmsDumpImporter -d`;
  - `--reuse-users`: uses the existing users and teams (to import, one
    after the other, dumps of contests with the same contestants).
- `--dry-run -d` deletes nothing and blocks no one: the dump is checked
  against empty copies of the tables.
- `-G` (no results: everything is judged again), `-S` (no submissions), `-U`
  (no user tests), `-X` (no users or what depends on them), as in
  `cmsDumpImporter`.
- Admins appear in a dump only as authors of announcements and answers:
  they are matched by username, and missing ones are created disabled.
