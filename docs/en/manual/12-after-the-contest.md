# 12. After the contest

*Español: [Después del concurso](../../es/manual/12-despues-del-concurso.md)*

Closing a contest properly: check that everything is evaluated, publish
and export the results, keep the evidence, let the contestants keep
practicing and leave the system ready for the next one.

## Contents

1. [Checking that everything is evaluated](#1-checking-that-everything-is-evaluated)
2. [Answering what is pending](#2-answering-what-is-pending)
3. [Publishing the results](#3-publishing-the-results)
4. [Exporting the results](#4-exporting-the-results)
5. [Keeping the evidence: backup](#5-keeping-the-evidence-backup)
6. [Letting them practice: analysis mode](#6-letting-them-practice-analysis-mode)
7. [Archiving a task for reuse](#7-archiving-a-task-for-reuse)
8. [Preparing the next contest](#8-preparing-the-next-contest)
9. [Personal data](#9-personal-data)

---

## 1. Checking that everything is evaluated

Before announcing results, no submission should be pending:

- The *Overview* shows no submissions compiling or being evaluated, and
  none "Cannot compile/evaluate (please check)".
- From the console:

  ```sh
  sudo cmsctl judging-status
  ```

  counts each task's submissions by status. All must be scored (*scored*)
  or with a failed compilation. "Gave up" are submissions that failed
  because of judge problems: reevaluate them
  ([ch. 9](09-during-the-contest.md#5-reevaluating)).

## 2. Answering what is pending

In *Questions*, answer or ignore the pending questions, so that no
contestants are left waiting.

## 3. Publishing the results

- If the scoreboard was **frozen**, unfreeze it
  ([ch. 11](11-scoreboard.md#4-freezing-the-scoreboard-at-the-end)) at the
  moment of the award ceremony.
- If it was in `secret` mode, switch it to `public` or share the secret
  link.

## 4. Exporting the results

In the administration, the contest's *Ranking*: "Download as csv, text".
The CSV opens in any spreadsheet and has one row per contestant with the
score of each task and the total, for the minutes or the certificate.

## 5. Keeping the evidence: backup

Make a complete backup (database, files and configuration) right at the
end:

```sh
sudo cmsctl backup --label final-d4oi2026
```

It is left in `/var/lib/cms/backups/`. Copy it **off the server**:

```sh
scp user@server:/var/lib/cms/backups/cms-backup-*final-d4oi2026*.tar.gz .
```

The backup contains the configuration's keys: keep it with the same care
as the server. Restoring it: [ch. 13](13-maintenance.md#restoring-a-backup).

## 6. Letting them practice: analysis mode

**Analysis mode** lets the contestants keep submitting after the contest,
without affecting the official results. It is turned on on the contest's
page with its start and end dates
([ch. 4](04-contests.md#7-contest-schedule-and-analysis-mode)).

## 7. Archiving a task for reuse

On the task's page, *Download as a package* downloads a zip with the whole
task in the own format, ready to import into another contest or another
installation. From the console:

```sh
sudo cmsctl export-task baches baches.zip
```

## 8. Preparing the next contest

- **A new contest with other tasks**: create another contest; the users
  are reused.
- **Deleting the test contests**: in *Contests*, choose the contest and
  delete it. **It also deletes its participations and submissions.** It
  disappears from the scoreboard by itself
  ([ch. 11](11-scoreboard.md#6-what-is-deleted-disappears-by-itself)).
- **Starting everything from scratch** (no contests, users or
  submissions): a clean install with `--clean`
  ([ch. 2](02-installation.md#8-clean-install---clean)), which backs up
  before deleting.

## 9. Personal data

CMS stores names, emails, plain-text passwords (if they were imported that
way), IP addresses and the code the contestants submitted. After the
contest:

- keep the final backup in a safe place with restricted access;
- delete the contestants you no longer need;
- if you publish the ranking, publish only what is necessary.

---

**Next:** [13. Maintenance and cmsctl reference](13-maintenance.md) ·
**Previous:** [11. The public scoreboard](11-scoreboard.md) ·
[Index](README.md)
