# 5. Tasks

*Español: [Problemas](../../es/manual/05-problemas.md)*

A **task** is what the contestants solve: a statement, attached files and,
inside, the testcases and the way to evaluate them. This chapter explains
every part of a task's page, the *datasets*, the testcases and how to check
that the task evaluates correctly before the contest. Task types and score
types have their own [chapter 6](06-task-and-score-types.md); importing
packages is covered in [chapter 7](07-importing.md).

## Contents

1. [Two ways: import or create](#1-two-ways-import-or-create)
2. [Creating a task in the browser](#2-creating-a-task-in-the-browser)
3. [The task page](#3-the-task-page)
4. [Task data](#4-task-data)
5. [Statements and attachments](#5-statements-and-attachments)
6. [Submission format](#6-submission-format)
7. [The task's submissions, tests and tokens](#7-the-tasks-submissions-tests-and-tokens)
8. [Score and feedback](#8-score-and-feedback)
9. [Datasets](#9-datasets)
10. [Limits, task type and score type](#10-limits-task-type-and-score-type)
11. [Managers: graders, checkers and other files](#11-managers-graders-checkers-and-other-files)
12. [Testcases](#12-testcases)
13. [Testing the task with solutions](#13-testing-the-task-with-solutions)
14. [Changing a task during the contest](#14-changing-a-task-during-the-contest)
15. [Deleting a task](#15-deleting-a-task)
16. [A complete example](#16-a-complete-example)
17. [What if…](#17-what-if)

---

## 1. Two ways: import or create

| | Import a package | Create in the browser |
|---|---|---|
| When | you already have the task prepared (Polygon, TPS, CMS's Italian format, or the own format) | you prepare it from scratch |
| How | [ch. 7](07-importing.md) | sections 2 to 13 of this chapter |
| Advantage | a single step, repeatable | you need no external tools |

In both cases the result is the same: a task with its dataset, which you
can keep editing on this page.

## 2. Creating a task in the browser

1. Menu *Tasks* → *(create new task...)*.
2. Type the **short name** (no spaces, for example `suma`) and press the
   button to create it. It is created with an empty dataset.
3. The task page opens. From there, the *Problem preparation* link leads
   to a guided page that shows *What is missing* for the task to be
   evaluated (statement, testcases, checker…) and lets you write or
   generate testcases. It is described step by step in
   [../creating-a-problem.md](../creating-a-problem.md).
4. For the contestants to see it, add it to a contest: in the contest,
   page *Tasks* → choose the task → add
   ([ch. 4](04-contests.md#10-the-tasks-of-a-contest)).

## 3. The task page

It opens from the *Tasks* menu or from a contest's list of tasks. It has
two big parts:

- **At the top, the task's configuration** (what the contestants see and
  the submission rules): data, statements, attachments, submission format,
  tokens, score. It is saved with *Update* at the bottom of the form.
- **Below, the datasets**: how it is evaluated (task type, limits,
  managers, testcases, score type).

> **Important:** *Update* saves only the form at the top. The files
> (statements, attachments, managers, testcases) are uploaded with their
> own buttons and are saved immediately.

## 4. Task data

| Field | What it is | Example |
|---|---|---|
| *Name* | short, unique identifier; it appears in URLs, on the scoreboard and in file names | `suma` |
| *Title* | the title the contestants see | `Suma de dos números` |

Changing a task's name during a contest confuses the contestants and the
scoreboard: decide the name beforehand.

## 5. Statements and attachments

**Statements**: one per language, in PDF (or whatever format you use).
Each one is identified by a language code (`es`, `en`, `es_CL`…).

1. In *Statements*, choose the language code and the file, and upload it.
2. Tick *Primary?* on the languages that should be highlighted for all
   contestants (normally the contest's official language).
3. If you upload another file with the same language, it replaces the
   previous one.

Each contestant sees the primary statements and those in their preferred
languages highlighted (a user field, [ch. 8](08-users-and-teams.md)).

**Attachments**: files the contestant can download from the task page:
input and output examples, a sample grader, code templates, an image. Use
*Add files*. The file name is the one the contestant will see.

## 6. Submission format

*Submission format* is the list of files the contestant must submit. For a
normal task it is a single file:

```
suma.%l
```

`%l` is replaced with the extension of the chosen language (`suma.cpp`,
`suma.py`…). Some task types use other formats (for example, OutputOnly
asks for one output file per testcase): see
[chapter 6](06-task-and-score-types.md).

## 7. The task's submissions, tests and tokens

These fields are the same as the contest's ([ch. 4](04-contests.md)), but
apply only to this task. Empty means "no limit of its own" (the contest's
applies, if any).

| Field | What it does |
|---|---|
| *Allowed programming languages* | restricts the contest's languages for this task; empty = all of the contest's |
| *Maximum number of submissions* | how many submissions each contestant can make on this task |
| *Maximum number of user tests* | the same for tests |
| *Minimum interval between submissions* | mandatory wait between two submissions (prevents the judge from being used as a debugger) |
| *Minimum interval between user tests* | the same for tests |
| *Tokens parameters* | same as in the contest; the contestant must meet the contest's rules **and** the task's |

## 8. Score and feedback

**Feedback level**: how much of each submission the contestant sees.

| Option | What they see |
|---|---|
| *Full* | every public testcase with its result, time and memory |
| *Restricted* | in a subtask that is not fully solved, the public testcases up to the first one with a worse result |
| *OI Restricted* | only that testcase |

With *Full*, a contestant could deduce data about the testcases (for
example, from the time); in olympiads *Restricted* is usually used.

**Score options**:

| Field | What it does |
|---|---|
| *Score decimal places* | how many decimals the score is shown with (0 = integers) |
| *Score mode* | how several submissions are combined (below) |

Score modes:

| Option | The contestant's score on the task | Typical use |
|---|---|---|
| *Use the sum over each subtask of the best result for that subtask across all submissions (IOI 2017-)* | for each subtask, the best result from any submission; then they are added up | current IOI and most olympiads |
| *Use best among all submissions (IOI 2013-2016)* | the best complete submission | school contests |
| *Use best among tokened and last submissions (IOI 2010-2012)* | the best among the tokened submissions and the last one | contests with tokens |

## 9. Datasets

A **dataset** is a complete version of "how a task is evaluated": task
type, limits, managers, testcases and score type. A task can have several;
**one is the live one**: the one that gives the official score.

Why have several? To fix a task **during the contest** without losing
anything: if you discover an incorrect testcase, you clone the dataset,
fix it, check how the results change and only then make it live. All the
submissions are reevaluated with the new one and the scoreboard updates.

Actions of each dataset (links next to its name):

| Action | What it does |
|---|---|
| *Clone* | creates a copy to modify; you can also copy the managers and the evaluation results already computed |
| *Rename* | changes the description |
| *Make Live ...* | makes it the live dataset; first it shows which scores would change (with the results already computed) and lets you notify the affected contestants with a message |
| *Enable background judging* / *Disable background judging* | whether a non-live dataset evaluates the submissions in the background (to compare before making it live) |
| *View results* | the results of the submissions with that dataset |
| *Delete* | deletes a non-live dataset |
| *Create a new dataset* | an empty one |

## 10. Limits, task type and score type

In each dataset:

| Field | What it is | Example |
|---|---|---|
| *Time limit* | maximum CPU time per testcase, in seconds | `1` |
| *Memory limit* | maximum memory per testcase, in MiB | `256` |
| *Task type* | how it is compiled and run (Batch, OutputOnly…) | `Batch` |
| *Task type settings* | its parameters | compile alone, standard input, exact comparison |
| *Score Type* | how the score is computed (Sum, GroupMin…) | `GroupMin` |
| *Score Parameters* | the subtasks and their points | `[[30, 5], [70, 10]]` |

All this is explained in detail, with examples, in
[chapter 6](06-task-and-score-types.md).

## 11. Managers: graders, checkers and other files

The **managers** are files from the organizers that take part in the
evaluation and that the contestant does not see:

| File | What it is for |
|---|---|
| `checker` | program that decides whether an output is correct (when comparing exactly is not enough) |
| `grader.cpp`, `grader.c`, `grader.py`… | main program the solution is compiled with when the contestant only implements functions; **one per allowed language** |
| `suma.h` and similar | headers to compile with the grader |
| `manager` | the organizers' program in Communication tasks |

They are uploaded in the dataset's *Managers* section. The names matter:
see [chapter 6](06-task-and-score-types.md) for the name each task type
expects.

> **Frequent mistake:** the contest allows C, C++ and Pascal, but the task
> only has `grader.cpp`. Submissions in C and Pascal cannot be evaluated.
> Restrict the task's or the contest's languages to those that have a
> grader, or add the missing graders. `sudo cmsctl doctor` warns about
> this problem.

## 12. Testcases

Each testcase has a **name** (*Codename*, for example `01` or `sub1_03`),
an **input**, a **correct output** and the **Public** flag: public
testcases show their result to the contestant (according to the feedback
level).

**Adding one:** *Add a testcase*: name, input file and output file.

**Adding many at once** (the usual way): *Add multiple testcases*.

1. Prepare a **zip containing only files** (no folders), for example
   `input.01`, `output.01`, `input.02`, `output.02`…
2. Give the *Input file names template* (default `input.*`) and the output
   one (`output.*`). The `*` is the testcase's name. If your files are
   called `01.in` and `01.out`, use `*.in` and `*.out`.
3. Tick *Public* if you want all of them to be public, and *Overwrite
   existing testcases* if you are fixing testcases that already existed.
4. Press *Upload testcases*.

**Downloading:** *Download all testcases* downloads a zip with all of them.

The testcase names matter for subtasks by regular expression
([ch. 6](06-task-and-score-types.md)): choose names that group, such as
`s1_01`, `s1_02`, `s2_01`.

## 13. Testing the task with solutions

**Never use a task in a contest without testing it.** The task's *Test
submissions* page lets you submit solutions as if you were a contestant,
without them counting for anyone:

1. Open the task → *Test submissions*.
2. Upload one or several solutions (*Solutions*). The expected verdict goes
   as the **second extension** of the name: `correcta.ac.cpp`,
   `lenta.tle.cpp`, `mala.wa.py`. The codes are `ac` (full score), `pa`
   (partial), `wa` (wrong answer on some testcase), `tle` (time limit
   exceeded), `mle` (memory limit exceeded), `re` (runtime error) and `ce`
   (does not compile). For files without a code, the *Expected verdict
   (for names without one)* is used.
3. Press *Submit* and wait for the results in the table: score, result of
   each testcase and whether it matches what was expected. The *Problem
   preparation* page shows "Every test submission gets its expected
   verdict" when everything matches.

What to test at a minimum:

- a correct solution → full score;
- a slow solution → time limit exceeded on the large testcases;
- an incorrect solution → partial or zero score;
- if there are subtasks, a solution that solves only the first one →
  exactly the points of that subtask.

If a test submission stays on "Evaluating…" for more than a minute, the
judge is not working: `sudo cmsctl doctor`
([ch. 14](14-troubleshooting.md)).

## 14. Changing a task during the contest

- **Fixing a testcase:** *Clone* the dataset → replace the testcase in the
  copy → check with *View results* → *Make Live ...*. All the submissions
  are reevaluated and the scoreboard updates.
- **Changing the statement:** upload the new file and publish an
  announcement ([ch. 9](09-during-the-contest.md)) so the contestants know.
- **Changing time or memory limits:** the same as fixing a testcase (new
  dataset), so you can compare before making it live.

## 15. Deleting a task

From the *Tasks* list, choosing the task and the delete option. A
confirmation is requested showing how many submissions will be deleted.
**It deletes the task with all its datasets and all its submissions**,
with no way back (except by restoring a backup). To take it out of one
contest only, remove it from the contest's *Tasks* page.

## 16. A complete example

Task "Suma" (read two numbers, print the sum), with two subtasks: small
numbers (30 points) and large ones (70 points).

1. *Tasks* → *(create new task...)* → name `suma`.
2. Title: `Suma de dos números`; *Submission format*: `suma.%l`; *Score
   decimal places* 0; IOI 2017- mode. *Update*.
3. Statement: upload `suma-es.pdf` with language `es` and mark it primary.
4. Dataset: *Time limit* `1`, *Memory limit* `256`, type `Batch` with
   compile alone, standard input and output and exact comparison.
5. Testcases: a zip with `s1_01.in`, `s1_01.out`, … `s1_05.out`,
   `s2_01.in` … `s2_10.out`; templates `*.in` and `*.out`; mark the first
   two as public by hand afterwards.
6. Score type `GroupMin` with parameters
   `[[30, "s1_.*"], [70, "s2_.*"]]` ([ch. 6](06-task-and-score-types.md)).
7. *Test submissions*: `correcta.ac.cpp` (must give 100), `chica.pa.cpp`
   (only small numbers: 30), `mala.wa.cpp` (0).
8. Add it to the contest.

## 17. What if…

**…the submissions say "Compilation failed" and the solution compiles on
my computer.** Look at *Compilation output* in the submission's details.
Frequent causes: the grader or the language's header is missing, or the
file name does not match the submission format.

**…everything gives zero.** Check that the correct outputs correspond to
the inputs (same testcase names) and the type of comparison (trailing
spaces, Windows line endings: use a checker or the comparison that ignores
them, [ch. 6](06-task-and-score-types.md)).

**…the maximum score is not the expected one.** Check the score type's
parameters: the regular expressions must cover all the testcases.

**…"no grader for C11 / gcc…" when importing.** See section 11.

---

**Next:** [6. Task and score types](06-task-and-score-types.md) ·
**Previous:** [4. Contests](04-contests.md) ·
[Index](README.md)
