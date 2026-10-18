# 4. Contests

*Español: [Concursos](../../es/manual/04-concursos.md)*

A **contest** brings together everything the contestants go through during
a competition: the schedule, the tasks, who can log in and how, how many
times they can submit and what information they get about their
submissions. This chapter goes, field by field, through a contest's
configuration page, the groups (different schedules for different
contestants), the contest's list of tasks and how to delete it. At the end
there are two configurations ready to copy.

## Contents

1. [Before you start](#1-before-you-start)
2. [Creating a contest](#2-creating-a-contest)
3. [How the contest page works](#3-how-the-contest-page-works)
4. [Contest information](#4-contest-information)
5. [Logging in](#5-logging-in)
6. [Tokens](#6-tokens)
7. [Contest schedule and analysis mode](#7-contest-schedule-and-analysis-mode)
8. [Limits](#8-limits)
9. [Groups](#9-groups)
10. [The tasks of a contest](#10-the-tasks-of-a-contest)
11. [Deleting a contest](#11-deleting-a-contest)
12. [Ready-to-use configurations](#12-ready-to-use-configurations)
13. [Common mistakes](#13-common-mistakes)
14. [What if…](#14-what-if)

---

## 1. Before you start

You need:

- The administration installed and working ([chapter 2](02-installation.md)).
- An administrator with **All permissions**. Administrators without that
  permission see the pages in this chapter, but the buttons to save,
  delete or reorder appear disabled ([chapter 3](03-first-steps.md)).
- **The server's time zone correctly configured**, on the administration's
  *Server* page. All the times typed on the contest page are interpreted
  in that zone, and the labels name it: for example *Start time (in
  America/Santiago)*. If the zone is wrong, the contest will start at a
  different time. Configure it **before** typing the schedules
  ([chapter 3](03-first-steps.md) and
  [Administration](../administration.md#server-page)).

The concepts of contest, task, user, participation and group are explained
in [chapter 1](01-concepts.md).

> **About screen names.** This manual names each menu, button and field
> with its label in the English interface. If your browser prefers another
> language (for example Spanish), the administration appears translated
> and the labels will read differently.

---

## 2. Creating a contest

Example for this chapter: **OI 2026**, a 5-hour olympiad with 3 tasks and
30 contestants.

1. In the left sidebar, under **Contests**, click
   **(create new contest...)**.
2. The **New contest** page opens. In **Name:** type a short name, for
   example `oi2026`.
3. Click the form's submit button (it has no text of its own: the browser
   shows it as *Submit*).
4. If everything went well the notification **Operation successful.**
   appears and the **Contest configuration** page of the new contest
   opens.

The name can only have **letters without accents, numbers, underscore
(`_`) and hyphen (`-`)**, no spaces, and it cannot be repeated. `OI 2026`
or `olimpíada` do not work; `oi2026`, `oi-2026` or `OI_2026` do.

### What the system creates for you

When the contest is created the system also creates its **main group**,
called `default`, with these values:

| What | Initial value |
|---|---|
| Description | same as the name (`oi2026`) |
| Start | January 1, 2000, 00:00:00 UTC |
| End | January 1, 2100, 00:00:00 UTC |
| Allowed languages | C11 / gcc, C++20 / g++ and Pascal / fpc |
| Tokens | *Infinite* mode (see [section 6](#6-tokens)) |

> **Important: a newly created contest is already open.** With a start in
> the year 2000 and an end in 2100, any contestant who has a participation
> can log in and see the tasks as soon as they are added. **Set the real
> schedule before adding tasks and contestants.**

On the page they appear converted to the server's zone; for example, in
`America/Santiago` they show as `1999-12-31 21:00:00` and
`2099-12-31 21:00:00`.

When you enter a contest, the sidebar changes: at the top the contest's
name appears with an **Administration** link to go back, and below it the
contest's menu: **General** (the configuration page), **Ranking**,
**Submissions**, **User tests** (only if they are allowed), **Users**,
**Groups**, **Tasks**, **Announcements**, **Questions** and **Audit log**.

---

## 3. How the contest page works

The **Contest configuration** page has a single form with five blocks:
*Contest information*, *Logging in*, *Tokens parameters*, *Main group
settings* (with the schedule and analysis mode) and *Limits*. At the
bottom there are three buttons:

| Button | What it does |
|---|---|
| **Update** | saves the **whole** form at once |
| **Reset** | **does not delete anything**: it returns the form to the saved values, discarding what you typed and did not save |
| **Remove** | opens the confirmation to delete the contest ([section 11](#11-deleting-a-contest)) |

Each information icon next to a field shows help when you hover over it.

What happens when you click **Update**:

- If some value cannot be read, **nothing** is saved and **Invalid
  field(s).** appears with the detail in English (for example `Can't cast
  14/11/2026 to datetime.`).
- If the values can be read but are not valid for the database (a zero
  where a positive number is required, an end before the start…), nothing
  is saved and **Operation failed.** appears with a message such as `new
  row for relation "contests" violates check constraint "…"`.
  [Section 14](#14-what-if) translates each one.
- If everything is fine: **Operation successful.** The changes apply
  immediately on the contestant site and are recorded in the **Audit log**
  with the previous and the new value.

### Field formats

| Field type | Format | Example |
|---|---|---|
| Date and time | `YYYY-MM-DD HH:MM:SS`, 24 hours, **with seconds**, in the server's zone | `2026-11-14 09:00:00` |
| Duration in seconds | number (decimals accepted) | `10800` = 3 hours |
| Duration in minutes | number (only the *Token generation period*) | `60` |
| Integer | no dots or commas | `50` |
| Checkbox | ticked = yes | |

`2026-11-14 09:00` (without seconds) or `14/11/2026 09:00:00` give
**Invalid field(s)**.

> **Empty is not the same as zero.** In the optional fields (the limits,
> *Maximum number of tokens*, *Maximum accumulated tokens*, *Length of the
> contest*), **empty means "no limit"**. A `0` in most of them is an error.
> The mandatory fields, on the other hand (*Score decimal places*,
> *Minimum interval between tokens*, *Initial number of tokens*, *Token
> generation number*, *Token generation period*, start and end), cannot be
> left empty.

---

## 4. Contest information

First block, **Contest information**.

| Field | What it does | Default | Example |
|---|---|---|---|
| **Name** | short name and identifier | the one you typed when creating | `oi2026` |
| **Description** | name the contestants and the public see | same as the name | `Olimpiada de Informática 2026` |
| **Allowed localizations** | interface languages of the contestant site | empty (all) | `es` |
| **Allowed programming languages** | languages in which submissions are accepted | C11 / gcc, C++20 / g++, Pascal / fpc | C11 / gcc, C++20 / g++, Python 3 / CPython |
| **Submissions download allowed** | the contestant can download the files they submitted | ticked | ticked |
| **Allow questions** | the contestant can ask questions | ticked | ticked |
| **Allow user tests** | the contestant can try their program with their own inputs | ticked | ticked |
| **Score decimal places** | rounding of the total score | `0` | `0` or `2` |
| **Allow unofficial submission before analysis mode** | submit out of competition once their own time has ended | unticked | unticked |
| **Timezone** | it is saved, but **does not change** how times are shown | empty | empty |

### Name

> **Watch out for this field.** It is the **short name**, not the title
> the contestants see. That one is the *Description*.

The rules are those from the moment of creation: letters without accents,
numbers, `_` and `-`, unique. What it is used for:

- It is the contest's address on the contestant site:
  `https://concurso.ejemplo.org/oi2026` (when the site serves several
  contests; see [Contestant site](../contestant-site.md#urls)).
- It is the name of the contestants' session cookie for that contest.
- It takes part in the secret link to the scoreboard
  ([chapter 11](11-scoreboard.md)).

**Do not change it after publishing the contest**: it changes the address,
all the connected contestants are left out (they must log in again) and
the secret link to the scoreboard stops working. If `contest_web.contest`
was configured in `cms.yaml` with this name (to serve a single contest), it
must be changed there too.

### Description

It is the "real" name: it appears as the contestant site's title, in the
top bar, in the list of contests that contestants see and as the contest's
name on the scoreboard. It can have spaces, accents and any character.
When the contest is created it equals the name: **always change it**.

### Allowed localizations

It limits the languages of the contestant site's **interface** (menus,
buttons, messages). It has nothing to do with the statements or with
programming languages. You type a list of codes separated by commas.

- **Empty**: the contestant sees the site in their browser's language (if
  the translation exists) and can change it with the selector.
- **`es`**: always in Spanish, even if the browser is in English. `es`
  includes the variants, such as `es-CL`.
- **`es, en`**: Spanish or English depending on the browser. If the
  browser asks for neither, English is used.
- **`es-CL`**: only the Chilean variant. Write it with a **hyphen**:
  `es_CL` (with an underscore) is not recognized.

> **Common mistake:** a code that is misspelled or for a language without
> a translation (for example `sp` or `es_CL`) gives no error when saving,
> but the site ends up in **English**. After saving, open the contestant
> site and check the language.

### Allowed programming languages

One checkbox per language available in the installation, in alphabetical
order: C# / Mono, C++11 / g++, C++14 / g++, C++17 / g++, C++20 / g++,
C11 / gcc, Go, Haskell / ghc, Java / JDK, Kotlin / JDK, PHP, Pascal / fpc,
Python 3 / CPython, Python 3 / PyPy and Rust (plus those added in
`languages_dir`; see [Judges](../judges.md#languages)).

- Contestants can only submit in the ticked languages (each task can
  restrict its own list further; see [chapter 5](05-tasks.md)).
- **If you tick none**, no solution that needs a language can be
  submitted.
- **The checkbox installs nothing.** The judge needs the compiler. With
  the `--compilers basic` installation only C, C++, Python 3 and Java are
  installed: **Pascal / fpc is ticked by default and will not be
  installed**. Untick every language whose compiler is not on the judges
  ([chapter 2](02-installation.md)).
- The field's help warns: *All allowed languages must be supported by the
  tasks, for example in tasks with a manager*. If a task uses a *grader*
  or *manager* written for certain languages, do not allow others
  ([chapter 6](06-task-and-score-types.md)).
- **C# / Mono, Go, Java / JDK and Kotlin / JDK need several threads.** If
  you tick any of them, **all** the programs in the contest, in any
  language, run with permission to create many processes and threads (up
  to 1000). If you do not need them, do not tick them.

### Submissions download allowed

Ticked, each contestant can download from their list of submissions the
files they submitted (useful if they lose their local copy). Unticked, the
download links do not appear and downloading is not available. It only
affects the contestant's own files.

### Allow questions

Ticked, the contestant site's **Communication** page shows the form to ask
questions, which the staff answers from **Questions**
([chapter 9](09-during-the-contest.md)). Unticked, the form disappears;
announcements and staff messages are still visible.

### Allow user tests

Ticked, the contestant site shows the **Testing** menu, where the
contestant runs their program with an input of their own, with no score.
It only works while they are competing and on tasks whose type supports it
([chapter 6](06-task-and-score-types.md)). Unticked, that menu disappears,
and so does **User tests** in the contest's menu in the administration.
Each test occupies the judge like a submission: on a small server,
consider limiting them ([section 8](#8-limits)).

### Score decimal places

Number of decimals with which each contestant's **total score** (the sum
of their tasks) is shown in the administration's **Ranking** and on the
scoreboard. `0` shows `287`; `2` shows `287.35`. Each task has its own
decimals field for its score ([chapter 5](05-tasks.md)). The score is
stored unrounded: you can change this value at any time without
reevaluating anything. It cannot be empty or negative.

### Allow unofficial submission before analysis mode

When a contestant's time has ended, normally they can no longer submit.
With this box ticked they can keep submitting, but as **unofficial
submissions**: they are evaluated and the contestant sees the result, but
they **do not count** toward their score or the ranking. It applies in two
situations:

- their **personal window** has ended
  ([section 7](#per-contestant-window-usaco-style-contests)) but the
  contest is still open for others;
- the contest has ended and **analysis mode** is enabled but has not
  started yet.

Without analysis mode, once the contest has ended nobody can submit, even
if the box is ticked.

### Timezone

The field's help says it is the zone for showing times to the contestants,
but **in this version the field is saved and changes nothing**: all the
dates on the three sites use a single zone, the server's (the **Server**
page). It exists to keep the data when importing CMS contests. Leave it
empty. If you type something, it must be a valid zone name
(`America/Santiago`, `America/Mexico_City`, `Europe/Madrid`); otherwise
`Unknown time zone …` appears and nothing is saved.

---

## 5. Logging in

Second block, **Logging in**: how the contestants log in to the site.
Some options use data of each **participation** (IP address, hidden),
which are edited on the participation's page
([chapter 8](08-users-and-teams.md)).

| Field | What it does | Default |
|---|---|---|
| **Block hidden participations** | participations marked as hidden cannot log in | unticked |
| **Allow password authentication** | log in with username and password | ticked |
| **Allow registration** | the contestants create their own account | unticked |
| **IP based login restriction** | whoever has an assigned IP can only log in from it | **ticked** |
| **IP based autologin** | whoever arrives from their assigned IP logs in without a password | unticked |

### Block hidden participations

A **hidden** participation (the *Hidden participation* box on the
participation's page) does not appear on the public scoreboard. It is used
for the staff's test accounts or for contestants out of competition. With
this box ticked, those accounts **cannot log in** to the contestant site.
Tick it on contest day if you do not want the test accounts to be used;
leave it unticked if there are "out of competition" contestants who must
be able to take part without appearing on the scoreboard.

### Allow password authentication

It is the normal way to log in. The password is the user's or, if the
participation has its own, that one
([chapter 8](08-users-and-teams.md)). If you untick it, the login form
still appears but **nobody can log in with a password** (the site answers
*Failed to log in.*): it only makes sense together with IP-based
autologin.

### Allow registration

Ticked, below the login form appears **Don't have an account? Register**.
The contestant can:

- create a new user (first name, last name, a username of up to 50
  characters with letters without accents, numbers, `_` and `-`, a
  password of at least 6 characters and an optional email), or
- join an existing user to the contest, with their password.

In both cases they end up in the **main group**. If **teams** have been
created, choosing one is mandatory. Use it in open contests; in an
olympiad with a closed list of contestants leave it unticked and load the
users yourself ([chapter 8](08-users-and-teams.md)). Registered people log
in with a password: do not untick *Allow password authentication*.

### IP based login restriction

It comes **ticked**. It only affects participations that have something in
*IP address or subnet*: those can only log in (and stay logged in) from
those addresses. Addresses (`10.0.5.21`) and networks (`10.0.5.0/24`)
separated by commas are accepted. A participation without addresses can
log in from anywhere.

Example: in OI 2026 each contestant has a fixed computer in the lab
`10.0.5.0/24`. With each one's address loaded in their participation,
nobody can log in with someone else's account from another machine.

### IP based autologin

Ticked, whoever arrives from the **exact** address of a participation logs
in without typing anything. Conditions:

- The participation must have a **single address**, not a network
  (`10.0.5.21`, not `10.0.5.0/24`).
- If two participations have the same address, **neither** logs in
  automatically (it is recorded in the site's log).
- If there is no match, the contestant can still log in with a password
  (if allowed).

For a lab where each seat has a fixed IP: load each seat's IP, tick *IP
based autologin* and, if you do not want passwords, untick *Allow password
authentication*.

> **Behind a proxy.** The IP-based options need the site to see the
> contestant's real address. The installation with a domain already
> configures it (`num_proxies: 1`); if you add another proxy in front,
> adjust `num_proxies`
> ([Contestant site](../contestant-site.md#configuration)). If all the
> contestants go out to the internet through the same address (a NAT), the
> IP-based options are useless.

---

## 6. Tokens

### What they are and what they are for

Normally the contestant sees a **partial** result of each submission: that
of the public testcases, according to the task's feedback level
([chapter 5](05-tasks.md)). A **token** is a "chip" that the contestant
plays on a submission to see its **full result**. In addition, with the
score mode *Use best among tokened and last submissions (IOI 2010-2012)*,
only the tokened submissions and the last one count.

There are two sets of tokens that are combined: the **contest's** (common
to all tasks, configured here) and **each task's** (on the task's page,
[chapter 5](05-tasks.md)). To play a token on a submission the contestant
needs one from each set.

### Token mode

| Option | Meaning |
|---|---|
| **Disabled** | there are no tokens in the whole contest |
| **Finite** | limited quantity, according to the parameters below |
| **Infinite** | the contest does not limit; whatever each task says rules |

By default the contest comes in **Infinite** and each new task in
**Disabled**: the result is that **there are no tokens**. To use them, the
contest and the tasks have to be in *Finite* or *Infinite*. If either side
is in *Disabled*, the contestant sees no tokens.

Usual combinations:

| Contest | Tasks | Result for the contestant |
|---|---|---|
| Disabled | any | no tokens |
| Infinite | Disabled | no tokens (default value) |
| Finite | Infinite | one set of tokens **shared among all the tasks** |
| Infinite | Finite | a **different set for each task** |
| Infinite | Infinite | unlimited tokens: they see all the full results |
| Finite | Finite | two sets at once; confusing, avoid it |

> **Modern olympiads (IOI style since 2017):** they do not use tokens. Put
> the contest in **Disabled** and, in each task, the **Score mode**
> *Use the sum over each subtask of the best result for that subtask
> across all submissions (IOI 2017-)*. If you leave the default mode
> (*…tokened and last*) without tokens, **each task's score will be that
> of the last submission**, even if an earlier one was better
> ([chapter 6](06-task-and-score-types.md)).

### Parameters (only with *Finite*)

| Field | What it does | Unit | Default | Can be empty |
|---|---|---|---|---|
| **Maximum number of tokens** | how many tokens they can use in total in the whole contest | tokens | empty (no cap) | yes |
| **Minimum interval between tokens** | mandatory wait after using one | **seconds** | `0` | no (`0` = no wait) |
| **Initial number of tokens** | how many they have when their contest time starts | tokens | `2` | no |
| **Token generation number** | how many they receive in each generation | tokens | `2` | no (`0` = never receives more) |
| **Token generation period** | how often they are generated | **minutes** | `30` | no (greater than 0) |
| **Maximum accumulated tokens** | cap on tokens kept at one time; whatever is generated above it is lost | tokens | empty (no cap) | yes |

Rules worth knowing:

- Generations are counted from the contestant's **start of time**: the
  start of the contest (of their group) or, in contests with a personal
  window, the moment they pressed **Start!**.
- The *Maximum accumulated tokens*, if you set it, cannot be less than the
  *Initial number* (otherwise **Operation failed.** with `contests_check`),
  even if the mode is not *Finite*.
- *Maximum number of tokens* and *Maximum accumulated tokens* do not accept
  `0`: for "none", use the *Disabled* mode.
- Tokens can only be played **while the contestant is competing**: not in
  analysis mode or afterwards.
- A contestant with **extra time** keeps receiving tokens during that
  time. If you do not want them to have more than the others, set a
  *Maximum number of tokens*.
- **Do not change the rules during the contest**: the available tokens are
  recomputed with the new rules over everything already played, and the
  result can surprise the contestants.

### A complete example

OI 2026 (from 09:00 to 14:00) uses a set of tokens **shared among all the
tasks**:

| Where | Field | Value |
|---|---|---|
| Contest | Token mode | Finite |
| Contest | Maximum number of tokens | `6` |
| Contest | Minimum interval between tokens | `600` (10 minutes) |
| Contest | Initial number of tokens | `2` |
| Contest | Token generation number | `1` |
| Contest | Token generation period | `60` (one hour) |
| Contest | Maximum accumulated tokens | `4` |
| Each task | Token mode | Infinite |

On the contestant site's **Overview** page, the contestant reads exactly:

> You have a set of tokens shared among all tasks. You start with 2
> tokens. Every 60 minutes you get another token, up to a maximum of 4
> tokens. You can use a token every 600 seconds and no more than 6 tokens
> in total.

How it evolves for Ana:

| Time | What happens | Tokens available |
|---|---|---|
| 09:00 | the contest starts | 2 |
| 09:40 | Ana plays a token | 1 |
| 09:45 | she wants to play another: she must wait until 09:50 (600 s interval) | 1, waiting |
| 10:00 | 1 is generated | 2 |
| 11:00 | 1 is generated | 3 |
| 12:00 | 1 is generated | 4 (reached the accumulated maximum) |
| 13:00 | another would be generated, but she already has 4: it is lost | 4 |
| 13:00–13:30 | she plays 4 tokens, one every 10 minutes | 0 (she used 5 of the 6 in total) |

On each task's submissions page the contestant sees how many tokens they
have, since when they can play the next one and when they will receive a
new one.

---

## 7. Contest schedule and analysis mode

The **Main group settings** block shows the link to the **Main group**
(when the contest is created it is called `default`) and its schedules.
The schedules belong to the **group**, not to the contest: each
participation follows its group's schedules ([section 9](#9-groups)). What
you type here is the main group's schedule, which is also the one used by
the administration's ranking, the scoreboard and visitors who have not
logged in yet.

### Contest time

| Field | What it does | Default | Example |
|---|---|---|---|
| **Start time (in *zone*)** | from when one can compete | 2000-01-01 00:00:00 UTC | `2026-11-14 09:00:00` |
| **End time (in *zone*)** | until when | 2100-01-01 00:00:00 UTC | `2026-11-14 14:00:00` |
| **Length of the contest** | personal window, in **seconds**; empty = traditional contest | empty | empty, or `10800` |

What the contestant sees depending on the time:

- **Before the start**: they can log in and read messages, but the page
  says *The contest hasn't started yet.* and they do not see the tasks.
- **Between start and end**: they see the statements, submit, run tests
  and play tokens. A clock on the left counts down the time they have
  left.
- **After the end**: they still see statements and their submissions, but
  cannot submit (except in analysis mode or with the unofficial
  submissions option).

The start cannot be later than the end. Both are mandatory (*No main group
start time specified.* if you leave the start empty).

To give **everyone** more time during the contest, change the *End time*
and click **Update**. To give time to **a single contestant** (they had a
power outage, for example), use *Extra time* or *Delay* on their
participation ([chapter 9](09-during-the-contest.md)).

### Per-contestant window (USACO-style contests)

If you type a **Length of the contest**, the contest gets **personal
windows**: between the start time and the end time, each contestant
chooses when to start and from that moment has that number of seconds.

1. The contestant logs in between the start and the end and sees the
   **Start!** button on the *Overview*.
2. Pressing it starts their window: they see the tasks and can submit.
   Their start time is stored in their participation (*Time of first
   login*).
3. The window ends when the length is reached or at the contest's **end
   time**, whichever comes first. Whoever starts late has less time.
4. Whoever never presses *Start!* before the end can no longer compete.

Example: a selection round that can be taken at any moment of the weekend,
3 hours per person: start `2026-11-20 00:00:00`, end `2026-11-22 23:59:59`,
length `10800`.

> **Common mistakes.** The length is in **seconds**: `3` or `180` are not
> 3 hours. A `0` is accepted but leaves a window of zero seconds. For a
> traditional contest (everyone at the same time), leave the field
> **empty**.

### Analysis mode

After the contest, analysis mode reopens the system so the contestants can
practice and review: they can submit, the submissions are evaluated as
usual and they see the **full result** of all their submissions, but
everything submitted is **unofficial** (it does not change the ranking).
There are no tokens or user tests.

| Field | What it does | Default |
|---|---|---|
| **Enabled** | turns analysis mode on | unticked |
| **Analysis mode start time (in *zone*)** | from when | 2100-01-01 00:00:00 UTC |
| **Analysis mode end time (in *zone*)** | until when | 2100-01-01 00:00:00 UTC |

Rules:

- The analysis start **cannot be earlier than the contest's end time**,
  and the analysis end cannot be earlier than its start. This is required
  **even if the mode is disabled**: if you move the contest's end time
  past an analysis start that is already written, **Operation failed.**
  (`groups_check1`). Fix both times at once.
- For a contestant with extra time or delay, the analysis starts when
  their time ends. If you are going to give extra time, leave a margin
  between the contest's end and the analysis start.
- Analysis submissions count toward the *Maximum number of submissions*
  ([section 8](#8-limits)).

Example for OI 2026: enabled, from `2026-11-14 16:00:00` to
`2026-11-21 23:59:59`.

---

## 8. Limits

Last block, **Limits**. All are optional: **empty = no limit**. They do not
accept `0` (it gives **Operation failed.**).

| Field | What it does | Unit | Example |
|---|---|---|---|
| **Maximum number of submissions** | submissions per contestant in the whole contest, adding up all tasks | submissions | `100` |
| **Maximum number of user tests** | tests per contestant in the whole contest | tests | `200` |
| **Minimum interval between submissions** | mandatory wait after each submission, on any task | seconds | `30` |
| **Minimum submission interval grace period** | in the last *N* seconds of the contestant's time, the interval above is not required | seconds | `300` |
| **Minimum interval between user tests** | mandatory wait between tests | seconds | `30` |

How they apply:

- Each task has its own limits of the same kind
  ([chapter 5](05-tasks.md)). A submission is accepted only if it meets
  the contest's **and** the task's.
- The maximum number counts **all** the contestant's submissions in the
  contest, unofficial ones too (for example, those from analysis mode).
- The interval is measured from the contestant's last submission on
  **any** task of the contest.
- The grace period only affects the interval between **submissions** (not
  tests or the maximum number). It is counted up to the end of each
  contestant's time, including their extra time.
- **Unrestricted participations** (*Unrestricted participation*, on the
  participation's page) have no limits or schedule: useful for staff who
  test the contest.

The contestant's *Overview* reports the maximum number of submissions and
tests (for example, *You can submit at most 100 solutions during this
contest.*). When a submission is rejected, the contestant sees the reason;
for example, with a 30-second interval:

> **Submissions too frequent!** Among all tasks, you can submit again
> after 30 seconds from last submission.

Why limit: the minimum interval prevents someone from saturating the judge
with consecutive submissions (on a 2 vCPU server with 30 contestants, a
few submissions per second already form a queue); the grace period
prevents that wait from costing someone their last submission in the final
minutes.

---

## 9. Groups

### What a group is

A **group** is a set of participations that share a **schedule**: start,
end, personal window length and analysis mode. Each participation belongs
to a single group of its contest. The tasks, the login rules, the tokens
and the limits are the same for all groups: the only thing that changes is
the schedule.

When to use them:

- **Two shifts**: one site takes the exam from 09:00 to 14:00 and another
  from 15:00 to 20:00.
- **A site that starts later** because of a transport problem.
- **A different time zone**: CMS times are absolute, so a site in another
  zone that must start "at 9 o'clock its time" needs its own group with
  the schedule converted.

### The main group

Every contest has a **main group**. When the contest is created it is
`default`. It is special because:

- it is the group proposed when adding a user to the contest, the one that
  users imported by CSV with *Also add them to contest* receive and the
  one for those who register themselves;
- its schedules are the ones used by the administration's ranking and the
  scoreboard, and the ones visitors who have not logged in yet see;
- its schedule is also edited from the contest's page.

### Groups list

Contest menu → **Groups**. The **Groups list** page shows each group with
its **Start (in *zone*)**, **End (in *zone*)**, **Max. contest length (in
seconds)**, **Analysis**, **Analysis start** and **Analysis end**. The
main one carries the **(main group)** mark. The name column says
**Name**, as on the contest page.

### Creating a group with another schedule

Example: OI 2026 has a site, "Sur", that starts an hour later.

1. Contest menu → **Groups**.
2. In **Add a new group named** type `sede-sur` and click **Add group**.
   The new group is created with a **copy of the main group's schedules**
   and its page opens.
3. On the **Group sede-sur in oi2026** page, **Group information**
   section, change **Start time** to `2026-11-14 10:00:00` and **End
   time** to `2026-11-14 15:00:00`. The fields are the same as those of
   the contest schedule ([section 7](#7-contest-schedule-and-analysis-mode)),
   plus the name.
4. Click **Update**.

The group's name is free, but it cannot be repeated within the contest.

> **If the group has analysis mode**, remember that its start cannot be
> earlier than the end **of that group**: when pushing back `sede-sur`'s
> end, also check its analysis times.

### Putting contestants in a group

**One by one:**

1. Contest menu → **Users**.
2. For a user who is not yet in the contest: in **Add a new user:** choose
   the user in **Select a new user**, choose the group in the list next to
   it (the main one comes selected) and click **Add user**.
3. To **move** someone who is already in: click their username in the
   **Users list**. On their participation's page, **Participation
   information** section, change **Group** and click **Update**.

**Many at once** (CSV import always uses the main group):

1. In **Groups**, select `sede-sur` and click **Make main group**.
2. On the global **Users** page, **Import users** section, upload the Sur
   site's CSV with **Also add them to contest** = `oi2026` and click
   **Import** ([chapter 8](08-users-and-teams.md)).
3. Go back to **Groups**, select `default` and click **Make main group**.

> Users who already had a participation in the contest are not moved when
> importing: they stay in their group.

Each group's page lists its users (**Users**, with username, first name
and last name; up to 1000).

### Changing the main group

In **Groups**, select the group and click **Make main group**. It does not
move any contestant: it only changes which group is proposed by default
and which schedule the ranking and the scoreboard use.

### Deleting a group

In **Groups**, select the group and click **Delete selected group**. You
cannot:

- if it has participations: **Cannot delete group containing users**. Move
  its contestants first;
- if it is the main one: **Cannot delete a contest's main group.** Make
  another group the main one first.

If you click a button without selecting a group, **Invalid field(s)**
appears with `Please select a valid group`.

---

## 10. The tasks of a contest

Contest menu → **Tasks**. The page is titled **Tasks list of oi2026**.

> In this table the first column is the task's short **Name** and the
> second its **Title**.

### Adding tasks

A task belongs to **a single contest** at a time. The **Add a new task:**
list only offers the tasks that are **in no contest**.

1. Create or import the tasks ([chapter 5](05-tasks.md) and
   [chapter 7](07-importing.md)). When importing a package you can choose
   the contest directly and skip this step.
2. In **Add a new task:** choose the task in **Select a new task** and
   click **Add task**.
3. The task is added **at the end** of the list. Repeat for each one.

If the task you are looking for does not appear, it is in another contest:
remove it from that contest first (below).

### Ordering

The list's order is the order in which the contestants see the tasks on
the site and the order of the rankings' columns. To move one:

1. Select the task with the round button on its row.
2. In **Move selected task:** click **to the top**, **up by 1**, **down by
   1** or **to the bottom**.

Example for OI 2026: add `suma`, `grafos` and `dp` in that order; if you
want `dp` to be first, select it and click **to the top**.

### Removing a task from the contest

Select the task and click **Remove from contest** (under **Remove selected
task from the contest:**). The task is **not deleted**: it is left without
a contest, with all its data, and can be added to this or another contest.
To delete a task completely, use the task's page
([chapter 5](05-tasks.md)).

Adding, removing and reordering tasks is recorded in the **Audit log**. Do
it **before** the contest: during the contest the contestants would see
the list change.

---

## 11. Deleting a contest

1. On the contest page (**General**), click **Remove**, on the right, at
   the bottom. You can also do it from the **Contests list**: select the
   contest and click **Remove** next to **Edit selected contest:**.
2. **Remove Contest** opens, saying how much will be deleted, for example:

   > Are you sure you want to remove contest oi2026 from CMS, including
   > all related data?
   > This will remove 30 participation(s) and 412 submission(s).
   > This operation cannot be undone.

3. Click **Yes, remove** to confirm or **No** to go back.

What is deleted and what is not:

| Deleted | Not deleted |
|---|---|
| the contest and its groups | the **users** (they keep existing for other contests) |
| all its participations, with their submissions, user tests, tokens, questions and messages | the **teams** |
| its announcements | the tasks you **removed from the contest earlier** |
| **the tasks still in the contest**, with statements, attachments, datasets and testcases | the administrators |
| | the **Audit log** entries (they stay, without the link to the contest) |

> **Tasks are deleted with the contest.** If you want to keep a task (to
> reuse it next year, for example), remove it from the contest **before**
> deleting it ([section 10](#10-the-tasks-of-a-contest)). There is no
> recycle bin: make a backup before deleting anything important
> ([chapter 13](13-maintenance.md)).

Only an administrator with **All permissions** can delete; for the others
the page says **You do not have permission to remove a contest.**

---

## 12. Ready-to-use configurations

Values not mentioned stay as they come by default. The schedules assume
the server's zone is that of the contest's location.

### 5-hour olympiad (OI 2026: 3 tasks, 30 contestants)

Modern IOI style: no tokens, best result per subtask, lab with fixed
computers and analysis at the end.

| Block | Field | Value | Why |
|---|---|---|---|
| Information | Name | `oi2026` | short address |
| Information | Description | `Olimpiada de Informática 2026` | what everyone sees |
| Information | Allowed localizations | `es` | the whole interface in Spanish |
| Information | Allowed programming languages | C11 / gcc, C++20 / g++, Python 3 / CPython | the ones taught; **untick Pascal / fpc** if it is not installed |
| Information | Submissions download allowed | ticked | |
| Information | Allow questions | ticked | |
| Information | Allow user tests | ticked | |
| Information | Score decimal places | `0` | integer scores |
| Information | Allow unofficial submission before analysis mode | unticked | |
| Information | Timezone | empty | has no effect |
| Logging in | Block hidden participations | ticked | the test accounts are not used on contest day |
| Logging in | Allow password authentication | ticked | |
| Logging in | Allow registration | unticked | closed list of contestants |
| Logging in | IP based login restriction | ticked | with each seat's IP on their participation |
| Logging in | IP based autologin | unticked | |
| Tokens | Token mode | Disabled | |
| Schedule | Start time | `2026-11-14 09:00:00` | |
| Schedule | End time | `2026-11-14 14:00:00` | 5 hours |
| Schedule | Length of the contest | empty | everyone at the same time |
| Analysis mode | Enabled | ticked | practice after the contest |
| Analysis mode | Start | `2026-11-14 16:00:00` | 2-hour margin in case of extra time |
| Analysis mode | End | `2026-11-21 23:59:59` | one week |
| Limits | Maximum number of submissions | empty | |
| Limits | Maximum number of user tests | empty | |
| Limits | Minimum interval between submissions | `30` | protects the judge |
| Limits | Grace period | `300` | no wait in the last 5 minutes |
| Limits | Minimum interval between user tests | `30` | |

In addition, in **each task**: *Token mode* = Disabled and *Score mode* =
*Use the sum over each subtask of the best result for that subtask across
all submissions (IOI 2017-)* ([chapter 5](05-tasks.md)).

### 3-hour school tournament

One school, 3 easy tasks, students who use Python or C++. No fixed IPs, no
analysis mode.

| Block | Field | Value | Why |
|---|---|---|---|
| Information | Name | `escolar2026` | |
| Information | Description | `Torneo Escolar de Programación 2026` | |
| Information | Allowed localizations | `es` | |
| Information | Allowed programming languages | C++20 / g++, Python 3 / CPython | **untick** C11 / gcc and Pascal / fpc |
| Information | Submissions download allowed | ticked | |
| Information | Allow questions | ticked | |
| Information | Allow user tests | unticked | less load and one less screen to explain |
| Information | Score decimal places | `0` | |
| Logging in | Block hidden participations | unticked | |
| Logging in | Allow password authentication | ticked | |
| Logging in | Allow registration | unticked | load the students by CSV |
| Logging in | IP based login restriction | ticked | with no IPs on the participations it restricts nothing |
| Logging in | IP based autologin | unticked | |
| Tokens | Token mode | Disabled | |
| Schedule | Start time | `2026-11-20 15:00:00` | |
| Schedule | End time | `2026-11-20 18:00:00` | 3 hours |
| Schedule | Length of the contest | empty | |
| Analysis mode | Enabled | unticked | |
| Limits | Maximum number of submissions | `60` | 20 per task on average |
| Limits | Minimum interval between submissions | `20` | |
| Limits | Grace period | `120` | |
| Limits | The others | empty | |

In each task: *Token mode* = Disabled and *Score mode* = *Use best among
all submissions (IOI 2013-2016)*, which is easy to explain to the
students.

If you prefer the students to register themselves: tick **Allow
registration** and, after the start time, untick it so nobody else joins.

---

## 13. Common mistakes

| Mistake | Consequence | How to avoid it |
|---|---|---|
| Not setting the schedule when creating the contest | it stays open (2000–2100): contestants see the tasks ahead of time | type start and end as soon as you create it |
| Typing the schedules with the wrong server zone | the contest starts hours earlier or later | look at the zone in the label (*in America/…*); change it on **Server** beforehand |
| Name with spaces or accents | **Operation failed.** (`contests_name_check`) | only letters without accents, numbers, `_`, `-` |
| Date without seconds or in another format | **Invalid field(s).** (`Can't cast … to datetime.`) | `YYYY-MM-DD HH:MM:SS` |
| `0` in a limit to mean "no limit" | **Operation failed.** | leave the field empty |
| Leaving a token field or the decimals empty | **Invalid field(s).** (`Can't cast  to int.` or `to timedelta.`) | type `0` or another number |
| Mixing up units | intervals 60 times longer or shorter | *Token generation period* in **minutes**; everything else in **seconds** |
| Leaving a language without a compiler ticked (Pascal / fpc by default) | submissions in that language fail | tick only languages installed on the judges |
| Contest tokens in *Infinite* expecting tokens to exist | they do not appear: the tasks come in *Disabled* | configure the tasks too |
| Default score mode without tokens | the **last** submission counts, not the best | choose the score mode on each task |
| Clicking **Reset** thinking it deletes something | it only discards what is unsaved | |
| Relying on the contest's *Timezone* field | it changes nothing | use the **Server** page |
| Renaming an already published contest | the address changes, sessions are closed, the scoreboard's secret link changes | do not rename it; change the *Description* |
| Deleting a contest with tasks you wanted to keep | the tasks are deleted | remove them from the contest first |

---

## 14. What if…

### …"Operation failed." appears when saving

The English text names the rule that was not met:

| Message (`…constraint "X"`) | Cause | Solution |
|---|---|---|
| `contests_name_check` | the name has disallowed characters | use only `A-Z`, `a-z`, `0-9`, `_`, `-` |
| `duplicate key … "contests_name_key"` | a contest with that name already exists | choose another |
| `contests_max_submission_number_check`, `contests_max_user_test_number_check` | maximum number `0` or negative | empty or a positive number |
| `contests_min_submission_interval_check`, `contests_min_submission_interval_grace_period_check`, `contests_min_user_test_interval_check` | interval or grace period `0` or negative | empty or a positive number |
| `contests_token_max_number_check`, `contests_token_gen_max_check` | *Maximum number of tokens* or *Maximum accumulated tokens* at `0` | empty or positive |
| `contests_check` | *Maximum accumulated tokens* less than the *Initial number of tokens* | raise the maximum or lower the initial |
| `contests_token_gen_interval_check` | *Token generation period* `0` | a positive number |
| `contests_token_gen_initial_check`, `contests_token_gen_number_check`, `contests_token_min_interval_check`, `contests_score_precision_check` | negative number | `0` or more |
| `groups_check` | the start time is later than the end time | fix the times |
| `groups_check1` | the end time is later than the analysis mode start (even if it is disabled) | move the analysis start and end after the contest's end |
| `groups_check2` | the analysis end is earlier than its start | fix the analysis times |
| `groups_per_user_time_check` | negative *Length of the contest* | empty or positive |
| `duplicate key … "groups_contest_id_name_key"` | there is already a group with that name in the contest | choose another name |

Nothing was saved: fix the field and click **Update** again. The rest of
what you typed is still in the form.

### …"Invalid field(s)." appears

| Message | Cause |
|---|---|
| `Can't cast X to datetime.` | date in another format or without seconds |
| `Can't cast  to datetime.` / `No main group start time specified.` / `No main group stop time specified.` | start or end empty |
| `Can't cast X to int.` | number with letters, dots, or empty in a mandatory field |
| `Can't cast X to timedelta.` | non-numeric duration, or empty in *Minimum interval between tokens* or *Token generation period* |
| `Unknown time zone X.` | invalid contest *Timezone*: leave it empty |
| `No contest name specified.` | empty name |

### …the contestants see "The contest hasn't started yet." and it should have started already

1. Check **Start time** on the contest page and the zone the label
   indicates.
2. Check in **Groups** which group the contestant belongs to and that
   group's start.
3. If the contest has a **Length of the contest**, the contestant must
   press **Start!** on the *Overview*.
4. Check that their participation has no **Delay**.

### …a contestant cannot log in ("Failed to log in.")

Check, in this order: that they have a participation in the contest (the
contest's **Users** menu); that **Allow password authentication** is
ticked; if **IP based login restriction** is ticked, the *IP address or
subnet* of their participation; if **Block hidden participations** is
ticked, that their participation is not hidden. More causes in
[chapter 14](14-troubleshooting.md).

### …tokens do not appear

The contest and the tasks must be in *Finite* or *Infinite*; with either
one in *Disabled* there are no tokens. Remember that new tasks come in
*Disabled*.

### …the task I want to add does not appear in "Select a new task"

It is in another contest. Open it in that contest's **Tasks list**, click
**Remove from contest** and try again.

### …I cannot delete a group

It has participations (move them to another group from each
participation's page) or it is the main group (make another one the main
group first).

### …the contestants cannot submit in a language

Check that it is ticked in **Allowed programming languages** and in the
task's list, and that the compiler is installed on the judges
([Judges](../judges.md)). If submissions are accepted but fail to compile,
see [chapter 14](14-troubleshooting.md).

### …the contestant site appears in English

Check **Allowed localizations**: empty lets the browser choose; a
misspelled code (`es_CL`, `sp`) leaves the site in English. Use `es` or
`es-CL`.

---

**Next:** [5. Tasks](05-tasks.md) ·
**Previous:** [3. First access and admin accounts](03-first-steps.md) ·
[Index](README.md)
