# Task package format

*Español: [../es/formato-de-paquete.md](../es/formato-de-paquete.md).*

This installation has its own format for **one task**: a `problem.yaml`
file that says in plain words how the task is judged, and the files next
to it. A package is a **directory** or a **zip file**, and it can be:

- checked without a database: `cms ctl validate-task PACKAGE`;
- imported: `cms ctl import-task PACKAGE` (the format is detected by its
  `problem.yaml`; `-L problem_yaml` or `-L package` names it);
- produced from a task already in the database:
  `cms ctl export-task TASK DEST` (see [Exporting](#exporting)).

The formats of CMS (italy_yaml, Polygon, TPS) are still read as they are;
see [Packages of CMS](#packages-of-cms).

## Layout

```
sum/
├── problem.yaml             the task (required)
├── statement/
│   ├── en.pdf               a statement per language, PDF or Markdown
│   └── es.md
├── tests/
│   ├── 01.in                the input of test 01
│   ├── 01.out               its expected output
│   └── …
├── graders/                 files the judges need (optional)
│   ├── grader.cpp           grader for C++, one per language
│   ├── grader.py
│   ├── sum.h                headers the contestants' code includes
│   └── checker.cpp          the checker (compiled at import), or its binary "checker"
├── attachments/             files given to contestants (optional)
│   └── examples.zip
└── solutions/               solutions with their expected verdict (optional, recommended)
    ├── correct.ac.cpp
    ├── slow.tle.py
    ├── half.pa.cpp
    └── wrong.wa.c
```

Files and directories whose name starts with a dot (`.gitkeep`,
`.DS_Store`) are ignored everywhere. Other unexpected files are reported
as warnings.

## problem.yaml

Only `name` and `title` are required; everything else has the default of
CMS. A key that the format does not know is an **error** (a typo must not
be ignored), and a key that does not apply to the task type is a warning.

```yaml
name: sum                  # letters, digits, _ and -
title: A plus B
time_limit: 1.5            # seconds
memory_limit: 256          # MiB
input: ""                  # "" = standard input (the default)
output: ""                 # "" = standard output (the default)
checker: whitediff         # whitediff | exact | float | comparator | testlib
scoring:
  type: GroupMin
  subtasks:
    - points: 30
      tests: 01-02
    - points: 70
      tests: 03-10
public_tests: 01, 02       # outcome shown without using a token
languages: [C11 / gcc, C++20 / g++, Python 3 / CPython]
feedback_level: full
```

| Key | Meaning | Default |
|---|---|---|
| `name` | short name of the task (also the name of the source file to submit: `sum.%l`) | required |
| `title` | full title | required |
| `type` | task type: `batch`, `batch_and_output`, `output_only`, `communication`, `interactive`, `two_steps` (CMS names such as `OutputOnly` work too) | `batch` |
| `time_limit` | seconds (not used by `output_only`) | none |
| `memory_limit` | MiB | none |
| `input`, `output` | file names the program reads and writes (batch types); empty means standard input/output | `""` |
| `checker` | how outputs are compared (types that compare outputs); see [Checkers](#checkers) | `whitediff` |
| `grader` | compile the contestant's code with `graders/grader.<ext>` | `true` if `graders/` has a grader |
| `scoring` | score type; see [Scoring](#scoring) | 100 points shared equally among the tests |
| `public_tests` | tests whose outcome everyone sees; see [Choosing tests](#choosing-tests) | none |
| `submission_format` | files a submission has | `[<name>.%l]`, or the outputs of `output_only` |
| `languages` | programming languages allowed for this task, with the names of CMS | every language of the contest |
| `primary_statements` | statement languages highlighted to everyone | the only statement, if there is one |
| `feedback_level` | `full`, `restricted` or `oi_restricted` | `restricted` |
| `score_mode` | `max_tokened_last`, `max` or `max_subtask` | `max_tokened_last` |
| `score_precision` | decimal digits of the scores | `0` |
| `max_submissions`, `max_user_tests` | at most this many per contestant | no limit |
| `min_submission_interval`, `min_user_test_interval` | seconds between two of them | none |
| `tokens` | `mode` (`disabled`, `finite`, `infinite`), `max_number`, `min_interval` (s), `gen_initial`, `gen_number`, `gen_interval` (s), `gen_max` | `disabled`; 2, 2, 1800 |
| `dataset` | description of the dataset created | `Default` |
| `autojudge` | judge this dataset in the background when it is not the active one | `false` |

Keys of particular task types:

| Key | Type | Meaning | Default |
|---|---|---|---|
| `output_only_tests` | `batch_and_output` | tests answered only with an output file | none |
| `output_tests` | `batch_and_output` | tests that may be answered with an output file or by the program | none |
| `attach_inputs` | `output_only`, `batch_and_output` | give the inputs of those tests as attachments `input_<test>.txt` | `true` |
| `processes` | `communication` | number of contestant processes | `1` |
| `user_io` | `communication` | `std_io` or `fifo_io` | `fifo_io` with a grader, `std_io` without |
| `process_limit` | `interactive` | instances of the solution the controller may start | `1` |
| `concurrent` | `interactive` | the instances run at the same time | `true` |
| `controller_memory_limit` | `interactive` | MiB | required |
| `controller_time_limit`, `controller_wall_time_limit` | `interactive` | seconds | required |

`batch` with `output_only_tests` or `output_tests` is `batch_and_output`.
`two_steps` needs `submission_format` (e.g. `[encoder.%l, decoder.%l]`).

### Task types and the files they need

| Type | What happens | In `graders/` |
|---|---|---|
| `batch` | the program reads the input and writes the output | `grader.<ext>` with `grader: true`; a checker for `comparator`/`testlib` |
| `batch_and_output` | like `batch`; some tests are answered with `output_<test>.txt` | as `batch` |
| `output_only` | contestants submit `output_<test>.txt` files | a checker for `comparator`/`testlib` |
| `communication` | the contestant's processes talk with `manager` | `manager` (or `manager.cpp`) |
| `interactive` | `controller` starts and talks with instances of the solution | `controller` (or `controller.cpp`) |
| `two_steps` | two programs connected by a pipe through `manager.<ext>` | `manager.<ext>` (with its headers) |

## Tests

Each test is `tests/<name>.in` with `tests/<name>.out` (CMS stores an
expected output for every test; `communication` and `interactive` give
their manager or controller only the input, so there the `.out` file may
be empty). Names use letters, digits,
`_` and `-`; tests are ordered by name (byte order: `10` comes before `2`,
so number them with leading zeros: `01`, `02`, … `10`). An input without
its output, or an output without its input, is an error.

### Choosing tests

`public_tests`, `output_only_tests`, `output_tests` and the `tests` of a
subtask name tests the same way:

- `all`;
- names: `sample, big-1`;
- numbers: `7` is the test called `7`, or else the only one whose name is
  `7` with leading zeros (`07`, `007`); when two match, use the name;
- ranges of numbers: `3-7`, `01-10`;
- any mix, separated by commas or as a YAML list: `[1, 3-5, sample]`.

A number, range or name that matches no test is an error.

## Checkers

| `checker` | Comparison |
|---|---|
| `whitediff` (also `white_diff`, `diff`) | as CMS: equal ignoring whitespace differences |
| `exact` | byte by byte |
| `float` (also `real`) | token by token; numbers equal within 10⁻⁶, absolute or relative. `{type: float, tolerance: 1e-6}` is accepted; another tolerance needs a comparator |
| `comparator` | the program `graders/checker` with the protocol of CMS: it gets the input, the expected output and the contestant's output as arguments, writes the score (0 to 1) on its standard output and the message on its standard error |
| `testlib` | the program `graders/checker` is a plain testlib checker: arguments input, contestant output, expected output; exit code 0 is accepted, 1, 2, 4 and 8 are wrong, 7 gives the points written on stderr (`quitp`); 3 or any other code means the checker failed (an error of the judge, not a verdict) |

`checker`, `manager` and `controller` are **compiled at import** when
`graders/` has only their C++ source (`checker.cpp`, `.cc` or `.cxx`), with
`g++ -x c++ -std=gnu++20 -O2 -static` next to the headers of `graders/`:

- a `comparator` is compiled with `-DCMS` and the `testlib.h` of CMS (the
  one Polygon packages use), so a checker written with testlib speaks the
  protocol of CMS; it replaces the package's `testlib.h`;
- a `testlib` checker, a manager and a controller use the package's
  `testlib.h` if it has one, or that of CMS.

The machine importing needs `g++` and the architecture of the judges (the
binary is what they run). An executable already in `graders/` (a binary,
or a script starting with `#!`) is used as it is; a file under one of
these names that is neither is reported as a warning.

## Scoring

Without `scoring`, the score type is **Sum**: 100 points shared equally
among the tests.

```yaml
scoring:
  points: 50        # Sum: the total, shared equally
---
scoring:
  per_test: 10      # Sum: the points of each test
```

The group types take subtasks. A subtask scores its points times the
minimum outcome of its tests (`GroupMin`), times their product
(`GroupMul`), or fully when every outcome is above 0 and at most its
threshold and else nothing (`GroupThreshold`, for checkers that output a
cost):

```yaml
scoring:
  type: GroupMin           # GroupMin, GroupMul or GroupThreshold
  subtasks:
    - points: 20
      tests: 01-03         # or: regex: "0[1-3]"   or: count: 3
    - points: 80
      tests: 04-10
      always_show_tests: true
```

- Each subtask names its tests with exactly one of `tests` (see
  [Choosing tests](#choosing-tests)), `regex` (a Python regular expression
  matched at the start of the names; lookahead and back-references are not
  supported) or `count` (the next N tests in name order, as in CMS); every
  subtask uses the same one.
- `GroupThreshold` needs `threshold` in each subtask.
- With restricted feedback, contestants see the tests of a failed subtask
  only up to the first one with the lowest outcome;
  `always_show_tests: true` shows all of them.
- The points must add up to 100, or to `scoring.points` when given (an
  error if they do not); without `scoring.points` a different total is a
  warning.
- A test that is in no subtask does not count: a warning.
- Points are stored as written: `40` is an integer and `40.0` a float, as
  in CMS.

## Graders, headers and other managers

Every file of `graders/` is stored as a **manager** with its name, except:

- `testlib.h`, which is only used to compile;
- the sources of the programs (`checker.<ext>`, `controller.<ext>`, and
  `manager.<ext>` except for `two_steps`), which are compiled as described
  above or kept out.

Graders are named `grader` plus the first source extension of each
language: `grader.cpp` (every C++), `grader.c`, `grader.py` (both Python),
`grader.java`, `grader.pas`, `grader.cs`, `grader.hs`, `grader.php`,
`grader.rs`, `grader.go`, `grader.kt`. A grader under another extension
(`grader.cc`) is never used: a warning. `stub.<ext>` is stored as
`grader.<ext>`, as italy_yaml packages do (having both is an error).
Headers are files such as `sum.h` or `graderlib.pas`. With a grader, a
submission in a language without its grader cannot be judged: a task with
no grader for any of its languages is an error, and importing it into a
contest warns about the languages of the contest that lack one.

## Statements and attachments

`statement/<language>.pdf` or `statement/<language>.md`, where the
language is a code (`en`, `es`, `es_MX`, `zh_TW`) or its English name
(`english.pdf`, `spanish.md`, as italy_yaml). Two statements in one
language are an error; a task without statements is a warning.

Every file of `attachments/` is given to the contestants with its name
(subdirectories are an error: zip them). For `output_only` and the output
tests of `batch_and_output`, the input of each such test is also attached
as `input_<test>.txt`, unless `attach_inputs: false` or `attachments/`
has a file with that name.

## Solutions and expected verdicts

Solutions are named `<name>.<verdict>.<extension>`; the verdict says what
the solution should get:

| Verdict | Meaning |
|---|---|
| `ac` | accepted: full score |
| `pa` | partial: some points, not all |
| `wa` | wrong answer on some test |
| `tle` | time limit exceeded on some test |
| `mle` | memory limit exceeded on some test |
| `re` | runtime error on some test |
| `ce` | does not compile |

For example `correct.ac.cpp`, `brute.tle.py`, `greedy.wa.cpp`,
`half.pa.c`. The verdict is case-insensitive; the language comes from the
extension. For `output_only` tasks a solution is a directory
`<name>.<verdict>/` or a zip `<name>.<verdict>.zip` with the
`output_<test>.txt` files.

Solutions are **not imported** (CMS has no place for them): they document
the package and the validation checks them. It warns when there are no
solutions, when none is expected to be accepted, about a name without a
verdict or with an unknown one, a file whose language is unknown, and a
language that the task does not allow.

## Zip packages

A zip package has `problem.yaml` at its root or inside its only top-level
directory (`sum.zip` with `sum/problem.yaml` works). It is read where it
is: nothing is extracted, except the sources compiled at import, which go
to a temporary directory. Members are stored or deflated.

Before anything is read, the whole archive is checked; the import fails
if a member:

- could escape the directory it would be extracted to ("zip slip"): an
  absolute path, a `..`, `.` or empty component, a backslash, a drive
  letter;
- is a symbolic link, or is encrypted;
- appears twice, or is both a file and a directory.

An archive has at most 200 000 members and 16 GiB of content (as it
declares it); each member is checked against its declared size and CRC-32
while it is read, so an archive that lies about its sizes fails instead
of filling the disk. The metadata of macOS (`__MACOSX/`, `.DS_Store`) is
ignored.

## Validation

`cms ctl validate-task PACKAGE` reads the package and checks it as an
import would, without a database, and fails if there are errors
(`--json` prints the report as JSON). The import does the same checks and
writes nothing when there is an error.

Errors (the task cannot be imported):

- `problem.yaml` missing, invalid, or with unknown keys; no `name` or
  `title`; an invalid name;
- no tests; a test without its output or input; an invalid test name;
- a checker, manager or controller the task type needs that is missing or
  does not compile; a grader required and missing;
- an unknown type, checker, score type or language; invalid limits;
- subtasks whose points do not add up to `scoring.points`; a test range or
  name that matches no test; a count beyond the last test; subtasks mixing
  `tests`, `regex` and `count`; a regular expression that cannot run;
- a key that the type needs and is missing (`submission_format` of
  `two_steps`, the controller limits of `interactive`);
- an unsafe zip.

Warnings (the task can be imported, but check them):

- no statement; no time or memory limit; points of the subtasks that do
  not add up to 100; tests in no subtask;
- no solutions, or none expected to be accepted; solutions without a
  verdict, with an unknown verdict or language, or in a language the task
  does not allow;
- keys that do not apply to the task type; files that are ignored or
  stored but not used (`graders/checker` with `whitediff`, graders with
  `grader: false`, `grader.cc`); a program that does not look executable;
  languages of the contest without a grader.

## Commands

```sh
cms ctl validate-task sum/                 # check a package
cms ctl import-task sum.zip                # create the task
cms ctl import-task -c demo sum/           # … at the end of contest demo
cms ctl import-task -u sum/                # update it, keeping its submissions
cms ctl import-task --dry-run -u sum/      # say what would change, write nothing
cms ctl import-dataset -d "v2" sum/        # add the package as a new dataset
cms ctl export-task sum sum.zip            # export the active dataset
cms ctl export-task -d "v2" sum sum-v2/    # export another dataset to a directory
```

Updating a task (`-u`) follows `cmsImportTask` of CMS: its settings,
statements and attachments become those of the package; a dataset with the
same description is updated in place (testcases by name: a changed
testcase keeps its evaluations, which `cms ctl reevaluate --dataset`
judges again), a new description adds a dataset that is not active.

## Exporting

`cms ctl export-task TASK DEST` writes the task (name or id) with its
active dataset, or the one given with `-d`, as a package: a zip if `DEST`
ends with `.zip` (it must not exist), otherwise a directory (it must not
exist or be empty; it is written next to it and renamed at the end).
`problem.yaml` lists only what differs from the defaults. Importing the
package gives the same rows back (over the same task, `import-task -u`
reports it unchanged), whatever format the task was imported from: tasks
imported from italy_yaml, Polygon or TPS can be moved this way.

Not exported: submissions, the other datasets (the command lists them),
and solutions. A statement is written as `.pdf` if its content is a PDF,
otherwise as `.md`. Managers this format would not import back
(`testlib.h`, `checker.cpp`, `stub.cpp`…) are written and reported as
warnings. Programs (`checker`, `manager`, `controller`) are written as
executables.

## Packages of CMS

`cms ctl import-contest`, `import-task`, `import-users` and `import-team`
read the formats of CMS with the rules of its loaders (autodetected, or
`-L italy_yaml`, `-L polygon_task`, `-L tps_task`…; `cms ctl help
import-task` lists them).

The italy_yaml format (`contest.yaml`, `task.yaml`, `gen/GEN`, `input/`,
`output/`, `sol/`, `check/` or `cor/`, `statement/` or `testo/`, `att/`)
is imported as `cmsImportContest -L italy_yaml` does: the same keys with
their Italian names, the same defaults and the same deductions (the task
type from the files present, subtasks from the `#ST:` lines of `gen/GEN`,
the old token format). The tasks of the demo contest and of the tests were
imported with the original CMS and give the same rows. Differences:

- nothing is written to the package (CMS leaves `.itime` and
  `.import_error` files): an update compares the package with the
  database;
- every problem of the package is reported at once, as an error or a
  warning, where CMS stops at the first one or crashes;
- `analysis_start` is read (CMS ignores it), and `analysis_end` is
  accepted as `analysis_stop`;
- YAML times without a time zone are UTC (CMS: the time zone of the
  machine importing); times may also be written as `"2026-01-01 10:00:00"`;
- a user without a password gets none in the participation, and a random
  one when created (CMS crashes);
- the output-only tests of `BatchAndOutput` are stored sorted;
- unknown values of `feedback_level` and `score_mode` are warnings (CMS
  ignores them silently);
- a task CMS could store but never judge (an unknown language, invalid
  task or score type parameters) is rejected at import.

To move a CMS task to this format, import it and export it:
`cms ctl import-task -L italy_yaml old/sum && cms ctl export-task sum sum/`.
