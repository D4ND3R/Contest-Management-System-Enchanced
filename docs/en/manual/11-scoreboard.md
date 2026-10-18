# 11. The public scoreboard

*Español: [El scoreboard público](../../es/manual/11-scoreboard.md)*

The **scoreboard** (public standings table) shows the contestants' scores
live. It is the same as the original CMS's. This chapter explains who can
see it, how to freeze it at the end, how to give it a logo and flags, and
what to do if it shows something it should not. The technical reference is
in [../scoreboard.md](../scoreboard.md).

## Contents

1. [What it shows and how it updates](#1-what-it-shows-and-how-it-updates)
2. [Changing the scoreboard's configuration](#2-changing-the-scoreboards-configuration)
3. [Who can see it](#3-who-can-see-it)
4. [Freezing the scoreboard at the end](#4-freezing-the-scoreboard-at-the-end)
5. [Logo, flags and photos](#5-logo-flags-and-photos)
6. [What is deleted disappears by itself](#6-what-is-deleted-disappears-by-itself)
7. [Starting the scoreboard from scratch](#7-starting-the-scoreboard-from-scratch)
8. [What if…](#8-what-if)

---

## 1. What it shows and how it updates

- One row per contestant (visible user with a participation in the
  contest), with their team, the score of each task and the total.
- Clicking a contestant shows the history of their submissions.
- It updates by itself as soon as a submission is scored.

The following do not appear: **hidden** participations, **unofficial**
submissions and contests without a schedule. The administration pages have
their own complete *Ranking*
([ch. 9](09-during-the-contest.md#9-the-administrations-ranking)).

The scoreboard is always in English, as in the original CMS.

## 2. Changing the scoreboard's configuration

The configuration is in `/etc/cms/cms.yaml` (the installer keeps it when
reinstalling). To change it:

```sh
sudo nano /etc/cms/cms.yaml          # or the editor you prefer
sudo cmsctl config check             # checks that the file is valid
sudo systemctl restart cms-ranking-web cms-dispatcher
```

The two sections that matter:

```yaml
ranking_web:
  visibility: public      # public, secret or admins (section 3)
  show_id_column: false   # show the column with the username
dispatcher:
  rankings:
    - url: http://127.0.0.1:8002/
      freeze: 0           # freeze (section 4)
      prune: true         # delete what no longer exists (section 6)
```

## 3. Who can see it

| `visibility` | Who sees it |
|---|---|
| `public` (default) | anyone with the address |
| `secret` | only whoever has the contest's **secret link**, and the administrators |
| `admins` | only administrators |

**The secret link** is on the contest's *Ranking* page, in the
administration: "Secret link to the public scoreboard (whoever has it can
see the scoreboard)". Share it only with those who need to see it (for
example, the coaches). For it to appear, the scoreboard's public address
must be set on the *Server* page
([ch. 3](03-first-steps.md#7-the-server-page)).

## 4. Freezing the scoreboard at the end

It is common to "freeze" the scoreboard for the last hour: the public stops
seeing the changes and the final result is revealed at the ceremony.

1. Before that last hour, in `/etc/cms/cms.yaml`:
   ```yaml
   dispatcher:
     rankings:
       - url: http://127.0.0.1:8002/
         freeze: 1h
   ```
2. `sudo systemctl restart cms-dispatcher`.
3. From one hour before the end, the public does not see the new scores.
   The administrators see everything.
4. To reveal: `freeze: 0` and `sudo systemctl restart cms-dispatcher`.
   Everyone sees the final scores instantly.

## 5. Logo, flags and photos

The files go in `/var/lib/cms/ranking`:

| What | File |
|---|---|
| logo | `/var/lib/cms/ranking/logo.png` (200×160 recommended) |
| a team's flag | `/var/lib/cms/ranking/flags/<team code>.png` |
| a contestant's photo | `/var/lib/cms/ranking/faces/<username>.png` |

Example:

```sh
sudo cp logo.png /var/lib/cms/ranking/logo.png
sudo mkdir -p /var/lib/cms/ranking/flags
sudo cp chile.png /var/lib/cms/ranking/flags/CL.png
sudo chown -R cms:cms /var/lib/cms/ranking
```

If the code or the username has characters that are not letters or
digits, the file name encodes them (`-` is `_2d`): `CL-RM` →
`CL_2dRM.png`.

## 6. What is deleted disappears by itself

If you delete a contest, a task, a user or a team in the administration,
or mark a participation as **hidden**, it disappears from the scoreboard
**by itself in less than a minute** (the scoreboard synchronizes with the
database every 30 seconds). Nothing needs to be done.

This can be turned off (`prune: false`) if one scoreboard shows contests
from several installations.

## 7. Starting the scoreboard from scratch

If the scoreboard ended up in a strange state (for example, after tests),
you can empty it: CMS sends it everything that exists again. Nothing is
lost, because the real data is in the database.

```sh
sudo systemctl stop cms-ranking-web
sudo mkdir -p /var/lib/cms/ranking-viejo
sudo mv /var/lib/cms/ranking/state.json /var/lib/cms/ranking/journal.jsonl /var/lib/cms/ranking-viejo/ 2>/dev/null
sudo systemctl start cms-ranking-web
sudo systemctl restart cms-dispatcher
```

The logo, flags and photos are kept. When you have checked that it looks
right: `sudo rm -rf /var/lib/cms/ranking-viejo`.

## 8. What if…

**…the scoreboard is empty.** Does the contest have a schedule? Are there
non-hidden participations? Does the dispatcher reach the scoreboard?
`sudo cmsctl doctor` checks it ("scoreboard push" line).

**…a deleted contest still appears.** Wait a minute. If it persists, check
that `prune` is not `false` and that the dispatcher is working (`sudo
cmsctl doctor`); as a last resort, section 7.

**…a test user appears.** Mark their participation as hidden.

**…"refuses the dispatcher's credentials" in doctor.** The scoreboard and
the dispatcher must have the same `secret_key` (the norm if the installer
installed them).

---

**Next:** [12. After the contest](12-after-the-contest.md) ·
**Previous:** [10. The contestant site](10-contestant-site.md) ·
[Index](README.md)
