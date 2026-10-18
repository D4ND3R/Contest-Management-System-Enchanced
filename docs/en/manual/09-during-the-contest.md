# 9. During the contest

*Español: [Durante el concurso](../../es/manual/09-durante-el-concurso.md)*

What to watch and what to do while the contest is running: the
submissions, the questions, the announcements, the most common incidents
and a checklist for contest day.

## Contents

1. [Contest day checklist](#1-contest-day-checklist)
2. [The Overview: the status at a glance](#2-the-overview-the-status-at-a-glance)
3. [The submissions](#3-the-submissions)
4. [A submission's detail](#4-a-submissions-detail)
5. [Reevaluating](#5-reevaluating)
6. [Questions (clarifications)](#6-questions-clarifications)
7. [Announcements](#7-announcements)
8. [Private messages](#8-private-messages)
9. [The administration's ranking](#9-the-administrations-ranking)
10. [Judges and resource usage](#10-judges-and-resource-usage)
11. [Audit log](#11-audit-log)
12. [Changing schedules or granting time during the contest](#12-changing-schedules-or-granting-time-during-the-contest)
13. [Incidents: what if…](#13-incidents-what-if)

---

## 1. Contest day checklist

**The day before**

- [ ] `sudo cmsctl doctor` without any `FAIL`.
- [ ] Every task tested with *Test submissions* ([ch. 5](05-tasks.md#13-testing-the-task-with-solutions)).
- [ ] Contest schedule and time zone checked ([ch. 4](04-contests.md)).
- [ ] Allowed languages = those that have a grader.
- [ ] All contestants with a participation ([ch. 8](08-users-and-teams.md)); test accounts as hidden.
- [ ] A test user logged in to the contestant site, saw the statements and submitted a solution.
- [ ] A backup made: `sudo systemctl start cms-backup.service` ([ch. 13](13-maintenance.md)).
- [ ] Credentials printed or sent.

**30 minutes before**

- [ ] `sudo cmsctl doctor` again.
- [ ] The *Overview* shows the judges connected and the queue empty.
- [ ] Whoever answers questions has an account with messaging permissions.

**At the start**

- [ ] The contestants see "The contest is in progress" and the remaining time.
- [ ] The first submissions arrive and are evaluated in seconds.

**During**

- [ ] Look at the *Overview* and *Questions* every few minutes.
- [ ] Answer questions quickly; if the answer is useful to everyone, publish an announcement.

**At the end**

- [ ] No pending submissions in the *Overview*.
- [ ] Unanswered questions: answer them or ignore them.
- [ ] Follow [chapter 12](12-after-the-contest.md).

## 2. The Overview: the status at a glance

*Overview*, at the top of the menu, shows:

- **Submission status**: how many are compiling, being evaluated, scored
  or had problems ("Cannot compile (please check)", "Cannot evaluate
  (please check)": review those submissions).
- **Judge status** and **queue status**: whether there are evaluations
  waiting and since when.
- **Logs**: warnings and errors from the services.

Everything updates by itself. If the queue grows and does not go down, see
section 10.

## 3. The submissions

The contest's *Submissions* page: all the submissions, newest on top, with
user, task, time, status and score. You can filter by task, user, status,
language and whether they are official.

Submission statuses:

| Status | What it means |
|---|---|
| *Compiling...* | in the queue or being compiled |
| *Compilation failed* | it did not compile; the contestant sees the compiler's output |
| *Evaluating...* | running the testcases |
| *Scoring...* | computing the score |
| *Scored* | finished, with its score |

A submission that stays in *Compiling* or *Evaluating* for more than a
couple of minutes indicates a problem with the judges (section 13).

## 4. A submission's detail

Clicking a submission you see:

- the submitted files (downloadable);
- the **compilation**: result, compiler output, time, memory;
- the **evaluation** testcase by testcase: result, time, memory, the
  checker's message; both as the administrator sees it and as the
  contestant sees it;
- the score and, if one was used, the token;
- *Make unofficial*: the submission stops counting toward the score (for
  example, if it was submitted by mistake to another task and it was
  agreed to void it).

## 5. Reevaluating

Reevaluating compiles and/or runs submissions again. You do it when you
changed something in the task (a testcase, a checker, a limit) or when a
judge failed.

| From | Button | Affects |
|---|---|---|
| a submission's detail | *Reevaluate* on the compilation or on a testcase | that submission |
| a user's participation | reevaluate | all their submissions |
| a task's dataset | *Reevaluate all submissions for this dataset* | all the task's submissions with that dataset |
| the contest's *Submissions* | *Reevaluate all submissions in this contest (for all datasets)* | the whole contest (it can take a while) |

If you only changed the testcases or the checker, reevaluating the
**evaluation** is enough; if you changed the grader or the languages, the
**compilation**. To fix a task with comparable results before applying
them, use a new dataset
([ch. 5](05-tasks.md#14-changing-a-task-during-the-contest)).

The scores and the scoreboard update by themselves when it finishes.

## 6. Questions (clarifications)

Contestants ask from *Communication* on their site. The questions arrive
at the contest's *Questions* with a notification.

To answer one:

1. *Claim* the question, so that another organizer does not answer it at
   the same time. *Unclaim* releases it.
2. Choose a *Precompiled answer* — "Yes", "No", "Invalid Question (not a
   Yes/No Question)" or "No Comment/Please refer to task statement" — or
   write an *Alternative answer*. The precompiled ones are stored in
   English (as in CMS) and each contestant sees them in their language.
3. *Reply*. The contestant sees it on their page with a notification.

*Ignore* removes it from the pending list without answering (repeated or
out-of-place questions); *Unignore* brings it back.

Tips:

- Answer the minimum, and the same to everyone. "No Comment" is valid if
  the answer is in the statement.
- If the doubt concerns everyone (an error in the statement), publish an
  **announcement**.

## 7. Announcements

The contest's *Announcements*: *Subject* and *Text* → *Add announcement*.
All the contestants see it with a notification. It can be deleted with
*Delete*.

Example: "Correction in the task Baches: in example 2 the output is 7, not
8. The statement has already been corrected."

## 8. Private messages

To write to a single contestant: their participation → *Messages* →
*Subject*, *Text* → *Send*. They see it as a notification.

## 9. The administration's ranking

The contest's *Ranking*: the complete table with all the scores (hidden
ones too), per task and total. It can be sorted by column and downloaded
as CSV or text. The **secret link** to the public scoreboard is there too
([ch. 11](11-scoreboard.md)).

## 10. Judges and resource usage

*Judges*: each judge (worker) with its status (*Running*, *Idle*,
*Broken*, *Disabled*…), what it is evaluating, how long since it last
reported and the evaluation queue. A *Broken* judge means its sandbox does
not work (the *Reason* column says why). From here you can disable a judge
or requeue jobs.

*Resource usage*: CPU, memory and disk of each service. If the disk fills
up, nothing works: make sure space remains.

## 11. Audit log

*Audit log*: every change made in the administration, with who, when and
what. Useful to reconstruct what happened ("who changed the schedule?").

## 12. Changing schedules or granting time during the contest

- **To one contestant**: *Extra time* or *Delay* in their participation
  ([ch. 8](08-users-and-teams.md#7-the-participation-extra-time-hidden-unrestricted)).
- **To a group** (a shift): the group's schedule ([ch. 4](04-contests.md#9-groups)).
- **To everyone**: the contest's end on its page. Publish an announcement.

The contestants' counters update by themselves.

## 13. Incidents: what if…

**…a contestant cannot log in.**
1. Are they using the right username and password? Look at them on their
   user.
2. Do they have a participation in this contest? The contest's *Users*.
3. Does the contest restrict by IP and are they connecting from another
   network?
4. Do they have their own password on the participation?

**…submissions stay in "Evaluating...".**
1. `sudo cmsctl doctor`: look at the "Valkey and judges" section.
2. If it says "no judge is connected" or a judge is *Broken*:
   `sudo systemctl restart cms-worker` and `sudo journalctl -u cms-worker -n 50`.
3. Nothing is lost: when the judge comes back, it evaluates what was
   pending.

**…a testcase is wrong.** New dataset, fix, compare, make it live
([ch. 5](05-tasks.md#14-changing-a-task-during-the-contest)), and an
announcement.

**…the server restarted.** Everything survives restarts: the queues, the
submissions and the results. When it comes back, the services start by
themselves and carry on where they were. Check it with `sudo cmsctl
doctor`.

**…a contestant had a power outage.** Give them *Extra time* in their
participation.

**…the administration page is slow.** Look at *Resource usage*. Avoid
reevaluating the whole contest during the exam if it is not necessary.

---

**Next:** [10. The contestant site](10-contestant-site.md) ·
**Previous:** [8. Users, teams and participations](08-users-and-teams.md) ·
[Index](README.md)
