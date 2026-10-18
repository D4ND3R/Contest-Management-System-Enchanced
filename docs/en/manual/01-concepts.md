# 1. Concepts

*Español: [Conceptos](../../es/manual/01-conceptos.md)*

This chapter explains what is worth understanding before you touch
anything: what CMS does, what its parts are, what happens to a submission
from the moment a contestant presses *Submit* until its score shows up on
the scoreboard, and what every word you will see on the screens means. The
other chapters say **how** to do each thing; this one says **what it is**
and **why it works that way**. The [glossary](#glossary) also works as a
quick reference.

The whole manual uses the same example: the contest **OI 2026**, with 3
tasks (`suma`, `caminos` and `salidas`) and 30 contestants (`oi01` …
`oi30`).

## Contents

1. [What CMS is and what it is not](#what-cms-is-and-what-it-is-not)
2. [The three sites](#the-three-sites)
3. [The services working behind the scenes](#the-services-working-behind-the-scenes)
4. [The journey of a submission](#the-journey-of-a-submission)
5. [How the pieces fit together](#how-the-pieces-fit-together)
6. [Glossary](#glossary)
7. [The interface language](#the-interface-language)
8. [Times and time zones](#times-and-time-zones)
9. [The life cycle of a contest](#the-life-cycle-of-a-contest)
10. [What if…](#what-if)

---

## What CMS is and what it is not

**CMS** (*Contest Management System*) is the system the IOI (International
Olympiad in Informatics) and many national olympiads use to run their
programming contests. Contestants read statements and submit source code;
an automatic judge compiles it, runs it in an isolated box against
testcases, computes the score and publishes it.

This installation is a version of CMS whose server was rewritten in Go. It
keeps the pages, the behaviour and the data model of the original CMS
(<https://github.com/cms-dev/cms>): anyone who knows CMS will recognise it.
It adds, among other things, a one-command installer, an administration
translated into Spanish, pages to prepare and test tasks from the browser,
a judges page, an audit log and live updates on the three sites.

**What CMS does:**

- lets each contestant log in (username and password, or by IP address);
- receives submissions and judges them in an isolated box (*sandbox*), with
  time and memory limits;
- computes scores per testcase and per subtask, with the rules the IOI used
  in different years;
- handles communication with the contestants: questions, announcements and
  private messages;
- publishes a live scoreboard.

**What CMS does not do** (worth knowing before you start):

- **It does not write the tasks.** Statements, testcases and solutions are
  prepared by your committee. The administration helps you upload them,
  generate outputs and test solutions ([Tasks](05-tasks.md)), but the
  content is yours.
- **It does not control the contestants' computers or network.** Stopping
  them from copying, cutting internet access or watching the room is the
  organisers' job. CMS can only limit which IP addresses each contestant
  logs in from ([Users and teams](08-users-and-teams.md)).
- **It is not an open online judge.** Each contest decides whether people
  can sign up by themselves (*Allow registration*, off by default); usually
  the organiser loads the contestants.
- **It is not an ICPC-style contest.** A contestant's score is the sum of
  their scores on each task, as in the IOI; no time penalties are counted.

## The three sites

An installation has **three websites**, each with its own address:

| Site | Who uses it | What for | Installation with a domain | Installation by ports |
|---|---|---|---|---|
| **Contestant site** | contestants | log in, read statements, submit, see results, ask questions | `https://contest.example.org/` | `http://IP:8000/` |
| **Administration** | organisers, jury, staff | create contests, tasks and users; follow the judging; answer questions; see the ranking | `https://admin.contest.example.org/` | `http://IP:8001/` |
| **Public scoreboard** | public, coaches, families | see the live standings | `https://ranking.contest.example.org/` | `http://IP:8002/` |

The installer prints the actual addresses of your installation at the end
([Installation](02-installation.md)).

**How they relate:**

- **The contestant site and the administration use the same database.**
  What you change in the administration (an announcement, the times, a
  task) the contestants see on the next page they open, and at once if the
  page is open, thanks to the live updates.
- **The scoreboard does not read the database.** The *dispatcher* (see
  below) sends it over HTTP the contest, the tasks, the visible users and
  teams and the scores of the official submissions; the scoreboard keeps
  its own copy (in `/var/lib/cms/ranking` on a normal installation). That
  is why it can run on another server, and why it only shows what the
  dispatcher sent it: scores arrive as soon as they are computed, and
  changes of names, users or tasks within 30 seconds (`ranking_refresh`).
  If the dispatcher stops, the scoreboard keeps showing the last data it
  received.
- **The administration does not know which address contestants and the
  public use.** Links to the other two sites (for example the secret link
  to the scoreboard) are built from what you set on the *Server* page:
  *Contestant site address* and *Scoreboard address*.
- On the contestant site, the home page lists the contests and each one
  lives at `/<short name>`: OI 2026 is at
  `https://contest.example.org/oi2026`. Several contests can exist at the
  same time (for example a practice contest and the official one).

**To find your way around the screens:**

- Main menu of the administration: *Overview*, *Resource usage*, *Judges*,
  *Server*, *Audit log*, and the lists *Contests*, *Tasks*, *Users* and
  *Teams*.
- Inside a contest the menu changes: *General*, *Ranking*, *Submissions*,
  *User tests* (only if the contest allows them), *Users*, *Groups*,
  *Tasks*, *Announcements*, *Questions* and *Audit log*.
- Contestant menu: *Overview*, *Communication*, for each task *Statement*
  and *Submissions*, *Documentation* and *Testing* (user tests, if
  allowed). Tasks appear in the menu only once the contestant's time has
  started, listed by their short name.

## The services working behind the scenes

Behind the three sites there are several programs (services). You do not
need to handle them every day, but understanding them explains almost every
problem that may come up.

| Piece | systemd unit | In plain words | If it stops… |
|---|---|---|---|
| **PostgreSQL** | `postgresql` | the database: contests, tasks, users, submissions, results, questions and the audit log | nothing works: it is the central piece |
| **Valkey** (Redis-compatible) | `valkey-server` (or `redis-server`) | fast shared memory: the judge's job queues, live events and notifications | judging and live updates stop |
| **File store** | — (a directory, `/var/lib/cms/blobs`) | statements, testcases, submitted files and executables, stored by their SHA-256 fingerprint | files cannot be read or stored |
| **Contestant site** | `cms-contest-web` | the contestants' website | contestants cannot get in |
| **Administration** | `cms-admin-web` | the staff's website | the staff cannot get in; the contest goes on |
| **Scoreboard** | `cms-ranking-web` | the public website with the standings | the public cannot see the standings; the contest goes on |
| **Dispatcher** | `cms-dispatcher` | the coordinator: decides what to compile and evaluate, hands the work to the judges by priority, stores the results, computes the scores and sends them to the scoreboard | submissions are received but wait |
| **Judge** (*worker*) | `cms-worker` | compiles and runs the programs inside *isolate*, the isolated box; never touches the database; there can be several, on this and other machines | submissions stay at *Compiling...* or *Evaluating...* |
| **Monitor** | `cms-monitor` | checks that the database and Valkey answer and publishes that state for monitoring | nothing visible to contestants |

A few key ideas:

- **Nothing is lost on a restart.** Each service restarts by itself if it
  crashes (systemd does it), the queues are durable and results are stored
  so that repeating them changes nothing. If a judge stops in the middle of
  a job, the job goes back to the queue and another judge takes it. Besides,
  the dispatcher looks through the database every minute
  (`sweep_interval`) for pending work, so a lost notification only delays
  the judging ([Maintenance](13-maintenance.md)).
- **One active dispatcher.** If there is more than one, one works and the
  others wait to replace it; a normal installation has one.
- **The judges are the ones measuring times.** For a real contest they
  should run on dedicated cores or machines, so that the load of the
  websites does not change the measured times. `sudo cmsctl verify-host`
  checks that a machine is fit to judge ([Installation](02-installation.md),
  [Judges](../judges.md)).
- On installations with a domain, **Caddy** serves HTTPS in front of the
  three sites.
- The daily backups are made by `cms-backup` (a systemd timer)
  ([Maintenance](13-maintenance.md)).

## The journey of a submission

What happens from the moment `oi07` submits a solution to `suma` until the
score shows up on the scoreboard. In parentheses, what the contestant sees.

```
 contestant ──Submit──▶ contestant site ──stores──▶ PostgreSQL + files
                               │ notifies (Valkey)
                               ▼
                          dispatcher ──jobs──▶ queue (Valkey) ──▶ judge(s) in isolate
                               ▲                                        │
                               └─────────────── results ◀───────────────┘
                               │ stores results and score (PostgreSQL)
         ┌─────────────────────┼─────────────────────────┐
         ▼                     ▼                         ▼
  contestant's page     administration pages      public scoreboard
       (live)                 (live)               (HTTP, live)
```

1. **The submission.** On the *Submissions* page of `suma`, `oi07` picks the
   file and the language (for example *C++20 / g++*) under *Submit a
   solution* and presses *Submit*.
2. **Receiving it.** The contestant site checks that the contestant may
   submit (within their time, below the maximum number of submissions, not
   submitting too often, the file not larger than 100 000 bytes by
   default). It stores the files in the file store and the submission in
   the database, and marks it **official** if `oi07` is competing. Then it
   notifies the dispatcher through Valkey. (*Submission received*.)
3. **Compilation.** The dispatcher creates a compilation job for the active
   dataset of `suma` (and for every dataset with background judging) and
   puts it in the queue; contestant submissions have the highest priority.
   A free judge takes it, downloads the files and compiles inside the box.
   (*Compiling...*.) If it does not compile, the submission ends here with 0
   points. (*Compilation failed*; the compiler message is under *details*.)
4. **Evaluation.** With the executable ready, the dispatcher creates **one
   job per testcase**. Each judge runs the program on the testcase input,
   with the time and memory limits of the dataset, and checks the output
   (with *white diff* or with the checker). Each testcase gives an outcome
   between 0 and 1 and a message (*Output is correct*, *Execution timed
   out*…). With several judges, the testcases of one submission are
   evaluated in parallel. (*Evaluating...*.)
5. **Scoring.** Once every testcase is done, the score type computes the
   submission's **score** (all testcases) and its **public score** (public
   testcases only), and the task's score mode updates the score of `oi07`
   on `suma`. (*Scoring...*, then *Evaluated*.)
6. **The screens.** The result reaches `oi07`'s page and the administration
   pages (*Submissions*, *Ranking*) without reloading.
7. **The scoreboard.** If the submission is official and `oi07`'s
   participation is not hidden, the dispatcher sends the score to the
   scoreboard right away, and the scoreboard shows it live to every
   spectator (unless it is frozen, see [Scoreboard](11-scoreboard.md)).

> **Careful: the contestant and the scoreboard do not see the same
> number.** The contestant sees the **public score** of each submission
> (public testcases only), and the full score only after playing a token
> on that submission or in analysis mode. The administration and the
> scoreboard show the **full score**. If you want contestants to see their
> whole result (as in today's IOI), mark every testcase as public
> ([Task and score types](06-task-and-score-types.md)). If instead your
> contest has private testcases, a public scoreboard reveals results the
> contestants do not know: make it secret or freeze it.

If a submission stays at *Compiling...* or *Evaluating...*, it is almost
always because no judge is working: look at the *Judges* page of the
administration and at the [Troubleshooting](14-troubleshooting.md) chapter.
From the console, `sudo cmsctl judging-status --contest oi2026` counts the
submissions of each task by status.

## How the pieces fit together

```
Users (oi01 … oi30)        Teams (esc-norte …)        Admins
   │  (global: shared by every contest)
   ▼
Contest oi2026 ("OI 2026")
├── Groups: default (main), …               ← the times
├── Tasks: suma, caminos, salidas
│     ├── Statements (es, en) and Attachments
│     └── Datasets: "Default" (active), …
│           ├── limits, task type, score type
│           ├── Managers (grader.cpp, checker…)
│           └── Testcases (01 public, 02, 03 …)
├── Participations (user + group [+ team])
│     ├── Submissions → result per dataset → evaluation per testcase
│     ├── User tests
│     └── Questions and private Messages
└── Announcements
```

Rules worth remembering:

- **Users, teams and admins are global.** A user exists once and takes
  part in the contests where they have a participation (one per contest).
- **A task belongs to one contest** (or to none while it is being
  prepared).
- **Short names are unique in the whole installation**: there cannot be two
  contests `oi2026`, two tasks `suma` (even in different contests), two
  users `oi07` or two teams `esc-norte`. Only letters, digits, `_` and `-`
  are allowed.

## Glossary

- [Contest and times](#contest-and-times): contest, group, main group,
  per-user time, delay and extra time, analysis mode.
- [Tasks and test data](#tasks-and-test-data): task, statement, attachment,
  dataset, active dataset, testcase, public testcase, subtask, manager,
  grader, stub, checker or comparator.
- [How judging works](#how-judging-works): task type, score type, language,
  submission, official submission, compilation, evaluation, scoring, score
  mode, user test, token, feedback level.
- [People](#people): user, participation, team, hidden participation,
  unrestricted participation.
- [Communication](#communication): question (clarification), announcement,
  private message.
- [Results](#results): ranking, scoreboard.
- [Administration](#administration): admin, permissions, audit log.

### Contest and times

**Contest**. A set of tasks, participants and rules: times, languages,
limits, tokens, how people log in. It has a **short name**, used in the
address and in commands (`oi2026`), and a **description**, which is the
title contestants see in the top bar ("OI 2026"). It is created in
*Contests* → *(create new contest...)* and configured in *General*
([Contests](04-contests.md)).

> **Careful:** a newly created contest runs from 1 January 2000 to
> 1 January 2100, that is, it is **open from the first moment**. Set the
> real times before adding contestants.

**Group** and **main group**. In CMS the times do not belong to the contest
but to **groups** of participants: each group has a start, an end, a
per-user time and an analysis mode. Every contest is born with a group
`default`, its main group; its times are edited in *General*, section
*Main group settings*. Extra groups are useful for shifts: for example,
OI 2026 could have a group `morning` (9:00 to 14:00) and another one
`afternoon` (15:00 to 20:00). They are managed in *Groups*. The main group
is the one the administration and the scoreboard use as the contest's
times. The main group and a group with participants cannot be deleted.

**Per-user time**. If the group has a *Length of the contest* (in seconds),
each contestant decides when to start with the *Start!* button and gets
that much time, without going past the end of the group (USACO style).
Example: a contest open for a whole week with 3 hours (10 800 s) each.
Empty means everybody competes from the start to the end.

**Delay and extra time**. Settings of a single participation, in seconds:
*Delay* moves their whole time window later and *Extra time* extends its
end. They compensate incidents: if a lab lost power for 10 minutes, the
affected contestants get 600 seconds of extra time
([During the contest](09-during-the-contest.md)).

**Analysis mode**. An optional period after the end of the contest in which
contestants log in again, see the full result of all their submissions and
can keep submitting; those submissions are **not official** and change no
score. It is enabled on the group: *Analysis mode* → *Enabled*, with its
start and end, which cannot start before the end of the contest. Example:
OI 2026 ends at 14:00 and analysis runs from 16:00 to 20:00
([After the contest](12-after-the-contest.md)).

### Tasks and test data

**Task**. What contestants must solve. It has a **short name** (`suma`),
which contestants see in the menu, and a **title** ("Sum of two numbers"),
which they see on the statement. It holds statements, attachments,
submission rules (languages, limits, tokens, feedback, score mode) and one
or more datasets. It is created in *Tasks* → *(create new task...)* or by
importing a package ([Tasks](05-tasks.md), [Importing](07-importing.md)).
The default **submission format** is `suma.%l`, where `%l` stands for the
language's extension.

**Statement**. The text of the task, one per language, with a code such as
`es`, `en` or `es_MX`. It can be a PDF (as in CMS), Markdown (shown inside
the site) or LaTeX source (downloaded). The ones you mark as primary
(*Primary?*) are highlighted for everyone. In the administration they are
in the *Statements* section of the task; contestants see them under
*Statement*.

**Attachment**. A file contestants download from the task page: sample
inputs, a grader to test on their own computer, a template. Section
*Attachments*.

**Dataset** and **active dataset**. A dataset is a version of the data a
task is judged with: time and memory limits, task type, score type,
managers and testcases. A task can have several; the **active** one gives
the official scores. Why more than one? To fix things safely during the
contest: *Clone* the dataset, fix the wrong testcase in the copy, use
*Enable background judging* so that submissions are also judged there
without affecting anyone and, when everything is right, *Make Live ...*,
which shows which scores change before you confirm. Every new task is born
with a dataset `Default` that is already active.

**Testcase** and **public testcase**. A pair of input and expected output
with a name (`01`, `02`…; number them with leading zeros because they are
sorted as text: `10` comes before `2`). A testcase marked *Public* is one
whose outcome the contestant sees without spending tokens; the outcome of
the others is only visible with a token or in analysis mode. By default a
testcase is **not** public. Contestants never see the inputs or outputs of
the testcases, only their outcome.

**Subtask**. A group of testcases worth some points and graded together,
for example "subtask 1: N ≤ 1000, 30 points, testcases 01 to 05". It only
exists with the group score types (GroupMin, GroupMul, GroupThreshold).
Contestants see it as *Subtask 1* in the details of their submission. A
subtask counts towards the public score only if **all** its testcases are
public.

**Manager**. Any file the jury adds to the dataset to compile or evaluate:
a grader, a checker, the manager of a Communication task, the controller of
an Interactive one, headers such as `suma.h`. Section *Managers* of the
task.

**Grader**. A jury program compiled **together** with the contestant's code:
it reads the input, calls the functions the contestant wrote and writes the
output. This way the contestant only implements a function (for example
`long long suma(int a, int b)`). One grader is needed for each allowed
language (`grader.cpp`, `grader.c`, `grader.py`, `grader.java`…). It is
used by choosing, in the *Compilation* parameter of the task type,
*Submissions are compiled with a grader* instead of *Submissions are
self-sufficient*.

**Stub**. In olympiad jargon, the skeleton or simplified grader handed to
contestants so they can test on their computer (usually as an attachment).
CMS has no "stub" field: if an italy_yaml package has `sol/stub.<ext>`, it
is stored as the grader `grader.<ext>`. In user tests of a task with a
grader, contestants upload their own grader; that is why you should give
them one.

**Checker** or **comparator**. What decides whether the contestant's output
is right. It is chosen in the *Output evaluation* parameter of the task
type:

| Option | What it does |
|---|---|
| *Outputs compared with white diff* | equal except for whitespace differences; the choice of a new task |
| *Outputs compared byte by byte* | identical byte by byte |
| *Outputs compared token by token, numbers with a tolerance of 1e-6* | for answers with decimals |
| *Outputs are compared by a comparator* | a jury program called `checker` (CMS protocol): it receives the input, the correct output and the contestant's output, and writes a score between 0 and 1 and a message |
| *Outputs are checked by a testlib checker* | a checker written with testlib, the Codeforces/Polygon standard |

### How judging works

**Task type**. How a submission is compiled and run
([details](06-task-and-score-types.md)):

| Type | In a few words | Example in OI 2026 |
|---|---|---|
| Batch | a program reads the input and writes the output, alone or with a grader | `suma`, `caminos` |
| BatchAndOutput | like Batch, but some testcases can be answered by submitting the output file directly | — |
| OutputOnly | no code is submitted: the contestant submits output files (`output_<testcase>.txt`), and missing ones are taken from their previous submission | `salidas` |
| Communication | the contestant's program talks to a jury manager that keeps the input secret | guessing games |
| Interactive | a jury controller starts instances of the solution and talks to them (a recent addition to CMS, less tested) | — |
| TwoSteps | two programs joined by a pipe; CMS advises against it for new tasks (use Communication) | — |

**Score type**. How each testcase's outcome (between 0 and 1) becomes
points. Fields *Score Type* and *Score Parameters* of the dataset:

| Type | Rule | Parameters (example) |
|---|---|---|
| Sum | each testcase is worth the parameter times its outcome | `5`: 5 points per testcase |
| GroupMin | by subtasks: each one is worth its points times the **worst** outcome of its testcases | `[[30, 5], [70, 15]]`: 30 points the first 5 testcases, 70 the next 15 |
| GroupMul | by subtasks: points times the product of the outcomes | like GroupMin |
| GroupThreshold | by subtasks: all the points if every outcome is greater than 0 and does not exceed a threshold (for checkers that return a cost) | `[[30, 5, 1.5], …]` |

> **Careful:** a task created with *(create new task...)* starts with **Sum
> with parameter 100, that is, 100 points per testcase**: with 20
> testcases the maximum would be 2000. Change the parameter (5 so that 20
> testcases add up to 100) or use subtasks. A package in this
> installation's own format (`problem.yaml`) without scoring, instead,
> spreads 100 points over its testcases ([Importing](07-importing.md),
> [Package format](../package-format.md)).

**Language**. A programming language with its compilation and execution
commands. Included: C11 / gcc, C++11 / g++, C++14 / g++, C++17 / g++,
C++20 / g++, C# / Mono, Go, Haskell / ghc, Java / JDK, Kotlin / JDK,
Pascal / fpc, PHP, Python 3 / CPython, Python 3 / PyPy and Rust. Each
contest chooses its own in *Allowed programming languages* and each task
can restrict them further. Contestants see the exact commands on the task
description (*Compilation commands*).

> **Careful:** a new contest allows only **C11 / gcc, C++20 / g++ and
> Pascal / fpc**. If you want Python or Java, tick them. And every allowed
> language needs its compiler installed on **every** judge: with the
> reduced set of compilers (`--compilers basic`: C, C++, Python 3 and Java)
> Pascal is not installed ([Installation](02-installation.md)).

**Submission**. The code a contestant sends for a task, with its time,
language and files. It is judged on the active dataset (and on those with
background judging). Contestants submit and follow their submissions under
*Submissions*; the staff sees them in the contest's *Submissions*, with
filters by task, user, status, official and language.

**Official and unofficial submission**. **Official** is a submission made
while the contestant is competing: it counts for the score and the
scoreboard. **Unofficial** is one made in analysis mode, or after the
contestant's time if the contest allows it (*Allow unofficial submission
before analysis mode*): it is judged but does not count. Contestants see
them in separate tables (*Official submissions* / *Unofficial
submissions*). From the page of a submission, the administration can switch
it with *Make official* or *Make unofficial*.

**Compilation**. The first step: the judge compiles the code in the
isolated box with the language's commands. Statuses: *Compiling...* and,
if it fails, *Compilation failed*, worth 0 points.

**Evaluation**. Running the compiled program on each testcase, inside the
box, with the *Time limit* and *Memory limit* of the dataset. Each testcase
produces an outcome (from 0 to 1) and a message. Status: *Evaluating...*.

**Scoring**. Computing the submission's score with the score type, once all
testcases are evaluated. Statuses: *Scoring...* and *Evaluated*. The
contest score is the sum of the scores on each task, rounded to *Score
decimal places*.

**Score mode**. How the scores of the submissions become the contestant's
score on the task. Field *Score mode* of the task:

| Option | Task score |
|---|---|
| *Use best among tokened and last submissions (IOI 2010-2012)* | the best among the tokened submissions and the last one; **the default** |
| *Use best among all submissions (IOI 2013-2016)* | the best submission |
| *Use the sum over each subtask of the best result for that subtask across all submissions (IOI 2017-)* | for each subtask, the best result of any submission, added up |

> **Careful:** with the default mode and no tokens, **only the last
> submission counts**: a contestant who sends a worse solution at the end
> loses points. For a contest in the style of today's IOI, choose the third
> option.

**User test**. The contestant runs their program on the server with their
own input and gets the output back, without comparing it with anything and
without a score: useful to measure times on the real machine or to rule
out compilation errors. Contestant page *Testing*, button *Submit a test*;
in the administration, *User tests*. They are enabled with *Allow user
tests* (on by default). They do not exist for OutputOnly tasks.

**Token**. A permission a contestant spends on one of their submissions to
see its full result (every testcase and the real score); with the default
score mode, that submission also starts to count. There are contest tokens
and tokens of each task, and using one requires both. Each set can be
*Disabled*, *Infinite* or *Finite*; with finite ones you set, for example,
2 tokens at the start and 2 more every 30 minutes. By default the contest
has infinite tokens and each task has them disabled, so **there are no
tokens** until you enable them on the task. Contestants play one with
*Play!* in their submissions table, which then says *Played*
([Contests](04-contests.md)).

**Feedback level**. How much detail contestants see of the testcases they
may see (the public ones, or all of them after playing a token). Field
*Feedback level* of the task:

| Option | What the contestant sees |
|---|---|
| *Full* | every testcase with its outcome, time and memory |
| *Restricted*, the default | in a subtask that is not fully solved, the testcases up to the first one with the worst outcome |
| *OI Restricted* | in a subtask that is not fully solved, only the first testcase with the worst outcome (the IOI rule) |

In analysis mode contestants always see everything.

### People

**User**. A person with a username (`oi07`), first name, last name,
password and an optional e-mail. Users are **global**: the same user can
take part in several contests. They are managed in *Users* of the main
menu, one by one or by importing a CSV (*Import users*)
([Users and teams](08-users-and-teams.md)).

**Participation**. A user's enrolment in a contest. It stores their group,
their team, an optional password of their own for that contest, the IP
addresses they may log in from, their delay and extra time, whether it is
hidden or unrestricted, and everything they do in the contest:
submissions, user tests, questions and messages. Without a participation a
user cannot enter that contest (unless the contest allows registration).
They are managed in *Users* inside the contest's menu. Example: `oi07` has
a participation in `practice` and another one in `oi2026`.

**Team**. A group of contestants with a code and a name (`esc-norte`,
"Escuela del Norte"): a delegation, a school, a region. It is shown on the
scoreboard (with its flag, if you upload one) and, if the contest allows
registration, contestants choose theirs. It does not add up scores: the
table is still per person. Menu *Teams*.

**Hidden participation**. It is judged as usual, but it does not appear in
the administration's *Ranking*, in its downloads or on the public
scoreboard. Useful for staff test accounts or contestants competing out of
competition. If the contest also has *Block hidden participations*, they
cannot log in. The test submissions made from the administration use a
special hidden user, `_preparer`, created automatically.

**Unrestricted participation**. It can see the tasks and submit outside the
contest times, without the limits on the number and frequency of
submissions and user tests. Useful for the jury to try the contestant site
before the start. Its submissions made outside its time window are not
official.

### Communication

**Question** or **clarification**. A private question from a contestant to
the jury, from *Communication*: subject (up to 50 characters), text (up to
2000) and *Ask question*. The staff sees it at once in the contest's
*Questions*, which shows in the menu how many are still unanswered. There
they can *Reply* with a *Precompiled answer:* or with their own text,
*Claim* the question so nobody else answers it at the same time, or
*Ignore* it. Only the contestant who asked sees the answer; if it is
useful to everyone, also post an announcement. Questions are enabled with
*Allow questions* (on by default)
([During the contest](09-during-the-contest.md)).

**Announcement**. A notice for every contestant of a contest, written in
Markdown, from *Announcements* → *Add announcement*. It appears at once on
the contestant site with a notification. Example: "In `caminos`,
N ≤ 100 000 (the statement said 10 000)".

**Private message**. A notice from the staff to **one** contestant, from
the page of their participation, section *Messages* → *Send*. The
contestant cannot reply to it (that is what questions are for). Example:
"We gave you 10 minutes of extra time because of the power cut".

### Results

**Ranking**. The *Ranking* page of each contest in the administration: the
table with the full scores of every non-hidden participant on each task,
which can be downloaded as text or CSV and carries the secret link to the
scoreboard ([After the contest](12-after-the-contest.md)).

**Scoreboard** (public ranking). The third site: the live standings for the
public, with the full scores of the official submissions of non-hidden
participations. It can be public, secret (only with a link) or for admins
only, and it can be **frozen**: the public stops seeing the changes of the
last part of the contest while the staff still sees everything
([Scoreboard](11-scoreboard.md)).

### Administration

**Admin** and **permissions**. The staff accounts used to enter the
administration, separate from the contestant users. Each one has one of
these levels:

| Permission | Can | `cmsctl add-admin --role` |
|---|---|---|
| *All permissions* | everything | `all` |
| *Messaging permissions* | reply to, claim and ignore questions; send announcements and messages | `messaging` |
| *Problem preparer permissions* | create and change tasks, their datasets and testcases, and test them; does not touch users or contests nor delete tasks | `tasks` |
| none | see every page without changing anything | `readonly` |

There is always at least one enabled admin with all permissions. Accounts
are managed by clicking your name at the top of the left menu (*Hello, …*)
([First steps](03-first-steps.md)).

**Audit log**. Every change made from the administration is recorded: who,
from which address, when, what changed (old and new value). There is a
general one in the main menu and one per contest
([Maintenance](13-maintenance.md)).

## The interface language

Each site chooses its language in a different way:

| Site | Languages | How it is chosen |
|---|---|---|
| Contestant site | the 25 of CMS (among them Spanish `es` and Chilean Spanish `es_CL`) plus English | 1) the selector in the top bar, if the contestant picked one; 2) otherwise the browser's preferred language; 3) otherwise the first available one |
| Administration | Spanish and English | only the browser's preferred language; there is no selector |
| Scoreboard | English | always English, as in CMS |

**To see the administration in Spanish**, put Spanish first in your
browser's language settings and reload the page. Any variant works
(`es-AR`, `es-MX`…).

**For contestants to see their site in Spanish**, nothing is needed if
their browsers prefer Spanish. To make sure, write `es` in the contest's
*Allowed localizations*: only the languages starting with that code are
offered. `es` keeps both Spanish variants (`es` and `es_CL`), so the
selector still appears; it only disappears when a single language is
left.

Good to know:

- The **interface** is translated, not the content: statements, titles,
  announcements and answers are shown as you wrote them.
- The Spanish chapters of this manual use the `es` labels. A browser set to
  Chilean Spanish gets the `es_CL` variant, with a few different words (for
  example *Vista general* instead of *Carátula* for *Overview*).
- Some words stay in English in Spanish too, as in CMS: *Testing*,
  *Ranking*, *Dataset*, *Managers* and the names of the task and score
  types.
- The precompiled answers to questions (*Yes*, *No*, *Invalid Question (not
  a Yes/No Question)*, *No Comment/Please refer to task statement*) reach
  the contestant **in English**, as in CMS. For another language, answer
  with your own text.

## Times and time zones

**There is one time zone for the whole installation.** Dates are stored
internally in UTC and the sites show them in the server's zone:

1. At installation, `timezone` in `/etc/cms/cms.yaml` takes the machine's
   zone (or UTC if it cannot find it).
2. In the administration, *Server* → *Time zone* replaces it (for example
   `America/Santiago` or `America/Mexico_City`); empty goes back to the one
   in `cms.yaml`. Changes reach the sites within 30 seconds.

What it means in practice:

- **In the administration**, times are typed and shown in that zone, and
  the label says so: *Start time (in America/Santiago)*. The format is
  `YYYY-MM-DD HH:MM:SS`, for example `2026-11-14 09:00:00`.
- **On the contestant site**, the start and the end are shown in that zone,
  next to *Server time:* and the countdown. Submissions and messages show
  the **contest time** (how long since the contestant started) and the full
  time when hovering the mouse.
- **The contest page has a *Timezone* field**, and users have one too: they
  are kept for compatibility with CMS but **do not change what is shown**.
  The one that counts is the *Server* one.
- In an online contest with people from several countries, everybody sees
  the times in the server's zone: say so in an announcement.
- Times are compared with the server's clock: keep it synchronised (NTP)
  before the contest.

## The life cycle of a contest

An end-to-end checklist, with the chapter that explains each step.

**1. Prepare** (days or weeks before)

- [ ] Install and check the server and the judges (`sudo cmsctl
  verify-host`) → [Installation](02-installation.md)
- [ ] Log in to the administration, create the staff accounts with the
  right permission, check *Server* (time zone and addresses) →
  [First steps](03-first-steps.md)
- [ ] Create the contest and **set the times before adding contestants**;
  choose languages, localizations, questions, user tests and tokens →
  [Contests](04-contests.md)
- [ ] Load the tasks (from the browser or by importing packages), with
  statements, testcases, public testcases, task and score type, score mode
  and feedback → [Tasks](05-tasks.md),
  [Task and score types](06-task-and-score-types.md),
  [Importing](07-importing.md)
- [ ] Load users, teams and participations (groups, IPs, hidden staff
  accounts) → [Users and teams](08-users-and-teams.md)
- [ ] Decide the visibility and the freeze of the scoreboard →
  [Scoreboard](11-scoreboard.md)

**2. Test** (the rehearsal)

- [ ] Submit correct, partial, slow and wrong solutions to each task (*Test
  submissions*) and check they get what is expected → [Tasks](05-tasks.md)
- [ ] Enter the contestant site with a hidden, unrestricted test account:
  read, submit, ask a question, answer it →
  [Contestant site](10-contestant-site.md),
  [During the contest](09-during-the-contest.md)
- [ ] Look at the scoreboard with that test account visible →
  [Scoreboard](11-scoreboard.md)
- [ ] Make a backup before contest day → [Maintenance](13-maintenance.md)

**3. Run** (contest day)

- [ ] Follow *Overview*, *Submissions* and *Judges*; answer *Questions*;
  post *Announcements*; compensate incidents with extra time →
  [During the contest](09-during-the-contest.md)
- [ ] If something breaks → [Troubleshooting](14-troubleshooting.md)

**4. Close** (afterwards)

- [ ] Unfreeze the scoreboard, check and download the final ranking, open
  analysis mode, back up and archive →
  [After the contest](12-after-the-contest.md),
  [Maintenance](13-maintenance.md)

## What if…

**…contestants see 0 points although their solution is correct.** They see
the **public** score, which only counts public testcases (and, with
subtasks, only the subtasks whose testcases are all public). If no testcase
is public, that score tells them nothing. Mark testcases as public or
enable tokens ([Task and score types](06-task-and-score-types.md)).

**…the scoreboard shows a different score from the one the contestant
sees.** That is expected: the scoreboard shows the full score and the
contestant the public one (see [The journey of a
submission](#the-journey-of-a-submission)). If it is also frozen, the
public does not see the latest changes.

**…a contestant does not appear on the scoreboard.** Their participation is
hidden, or it was added less than 30 seconds ago. Only official
submissions count.

**…submissions stay at *Compiling...* or *Evaluating...*.** No judge is
working (stopped, disabled or without the language's compiler). Check
*Judges* and [Troubleshooting](14-troubleshooting.md).

**…the contest shows as open although I did not open it.** A new contest is
open from 2000 to 2100: set its times in *General*
([Contests](04-contests.md)).

**…times look shifted.** Check *Server* → *Time zone*; the contest's
*Timezone* field is not used ([Times and time zones](#times-and-time-zones)).

**…the administration is in English (or in Spanish) and I want the other
one.** Change the order of the languages in your browser
([The interface language](#the-interface-language)).

**…I cannot create a task `suma` in another contest.** Task short names are
unique in the whole installation: use another one (`suma2026`).

**…a contestant cannot choose Python.** The contest does not allow it (a new
contest only has C11, C++20 and Pascal) or the task restricts it. Tick it
in *Allowed programming languages* and make sure the judges have Python
installed.

**…a contestant lost points by sending a worse solution at the end.** The
task uses the default score mode (best among tokened submissions and the
last one). Changing the task's *Score mode* recomputes that task's scores
at once; if you do it during a contest, say so in an announcement. Best is
to choose the IOI 2017- rule when preparing the task.
