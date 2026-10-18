# 10. The contestant site

*Español: [El sitio del concursante](../../es/manual/10-sitio-del-concursante.md)*

What the contestants see and do. It helps you explain the system to them
before the contest (at the end there is a sheet to print) and understand
their questions. The technical reference is in
[../contestant-site.md](../contestant-site.md).

## Contents

1. [Logging in](#1-logging-in)
2. [The top bar](#2-the-top-bar)
3. [The overview](#3-the-overview)
4. [A task's page](#4-a-tasks-page)
5. [Submitting a solution](#5-submitting-a-solution)
6. [Seeing the results](#6-seeing-the-results)
7. [Tokens](#7-tokens)
8. [Testing](#8-testing)
9. [Communication: questions and announcements](#9-communication-questions-and-announcements)
10. [Per-contestant schedules and analysis mode](#10-per-contestant-schedules-and-analysis-mode)
11. [Handout for the contestants](#handout-for-the-contestants)

---

## 1. Logging in

1. The contestant opens the site's address (`https://your-domain` or
   `http://IP:8000`).
2. If there are several active contests, they choose one in *Choose a
   contest*.
3. They type their *Username* and *Password* and press *Login*.

If the contest allows self-registration, *Register* appears
([ch. 4](04-contests.md)).

## 2. The top bar

- The **time**: *Time left:* during the contest, or how long until it
  starts.
- The interface **language** (selector).
- *Logout*.

The counter uses the server's time: it does not matter if the computer's
clock is wrong.

## 3. The overview

*Overview*: the contest's status ("The contest is in progress"), the tasks
with their time and memory limits and the current score of each.

## 4. A task's page

Each task has its page in the menu:

- *Download task statement*: the PDF, in the main language and in the
  contestant's preferred ones.
- *Attachments*: examples, templates, sample graders.
- The form to submit and the list of previous submissions.

## 5. Submitting a solution

1. On the task's page, the *Submit a solution* section.
2. Choose the file from their computer and the language.
3. Press *Submit*.

If the task asks for several files, there is one field per file. A zip
with all of them can also be submitted.

Limits that may appear: maximum number of submissions, minimum time
between submissions. If one is exceeded, the site says so and does not
accept the submission.

## 6. Seeing the results

In *Previous submissions* each submission shows its status
(*Compiling...*, *Evaluating...*, *Scoring...*) and then its score. It
updates by itself, without reloading.

*details* opens the compilation result and, depending on the task's
feedback level, the result of the public testcases: correct, incorrect,
time limit exceeded, memory limit exceeded, runtime error…

If it says *Compilation failed*, the detail shows the *Compilation output*
with the compiler's error.

## 7. Tokens

If the contest uses **tokens**, a contestant can "spend" a token on a
submission to see its full result (all the testcases, not only the public
ones). Tokens are limited and are generated over time
([ch. 4](04-contests.md#6-tokens)). In the list of submissions, the option
to use a token appears on each eligible submission.

## 8. Testing

*Testing* lets the contestant run their solution with an input of their
own without it counting as a submission: they upload their program and an
input file, and see the output, the time and the memory. Useful for
checking that it compiles and runs on the server. It may have limits on
the number of tests.

## 9. Communication: questions and announcements

*Communication*:

- **Announcements** from the organizers to everyone.
- **Questions**: write a subject and a text and press *Ask question*. The
  answer appears right there with a notification.
- Private **messages** from the organizers.

When an announcement, an answer or a message arrives, a notification
appears on any page of the site.

## 10. Per-contestant schedules and analysis mode

- If the contest has a **per-contestant window** (each one has X hours
  from when they start), the contestant sees *Start!* and their time
  starts running when they press it.
- In **analysis mode** (after the contest) they can keep submitting to
  practice; those submissions do not count for the official result.

## Handout for the contestants

You can print this sheet and hand it out with the credentials.

---

> **How to use the contest system**
>
> **Address:** ______________________________
>
> **Your username:** ______________ **Your password:** ______________
>
> 1. Open the address in the browser and log in with your username and
>    password.
> 2. At the top you see the **time left**. The clock is the server's.
> 3. In the menu, each **task** has its own page: download the statement
>    and the attachments.
> 4. To **submit**: on the task's page choose your file and the language,
>    and press *Submit*. The file name can be anything.
> 5. Your submission appears in *Previous submissions*: wait a few seconds
>    and you will see the score. Press *details* to see the result of the
>    compilation and of the public testcases.
> 6. **Testing** is for trying your program with your own input without
>    using up submissions.
> 7. Questions about the statement? Go to **Communication**, write your
>    question and press *Ask question*. Also read the **announcements**.
> 8. You can submit several times: your score on each task is computed
>    according to the contest's rules (normally, the best you achieved on
>    each subtask), so submitting a worse version does not take points
>    away from you.
> 9. Do not share your password. When you finish, press *Logout*.

---

**Next:** [11. The public scoreboard](11-scoreboard.md) ·
**Previous:** [9. During the contest](09-during-the-contest.md) ·
[Index](README.md)
