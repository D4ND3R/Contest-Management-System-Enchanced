# Administration

*Español: [../es/administracion.md](../es/administracion.md).*

The administration (`cms admin-web`) is CMS's AdminWebServer: the same
pages, with the same HTML, CSS and JavaScript, plus the new pages of this
fork (problem preparation, judges, server and audit log). This guide
explains how it is configured, who can do what, how every change is
recorded and what differs from CMS.

## Configuration

Section `admin_web` of `cms.yaml` (every key has its `CMS_ADMIN_WEB_*`
variable; see [Configuration](configuration.md)):

| Key | Default | What it does |
|---|---|---|
| `listen` | `127.0.0.1:8889` | address of the site |
| `ops_listen` | `127.0.0.1:9189` | metrics, detailed health and pprof (do not expose it) |
| `num_proxies` | `0` | trusted proxies in front of the site (client address and HTTPS, as on the contestant site) |
| `cookie_duration` | `10h` | how long an idle session lasts |

It also needs `secret_key` (the same in every service), the database,
Valkey (notifications, live events, queues and judges) and the file store
(`storage`).

The site serves `/static/…` (CMS's files and `live.js`), `/source` (the
code of the running version, linked in the footer of every page) and
`/healthz`. Every URL is relative, as in CMS.

## Admins and permissions

Each admin has a username, a name, a password (bcrypt; the `plaintext:`
ones imported from CMS work too), an enabled switch and three
permissions:

| Permission | Can |
|---|---|
| All permissions (`permission_all`) | everything |
| Messaging (`permission_messaging`) | answer, ignore and claim questions; send announcements and private messages |
| Problem preparer (`permission_tasks`, new) | create and change tasks, statements, attachments, datasets, managers and testcases of any contest, and try submissions on them; not users, contests, their settings, nor deleting tasks |
| none | see every page without changing anything |

A disabled admin cannot log in. At least one enabled admin with every
permission always remains: the site does not let you disable, demote or
delete the last one. Everybody can change their own name, username and
password on the **Admins** page.

Until the installer exists (F9), create the first admin in the database:

```sql
INSERT INTO admins (name, username, authentication, enabled, permission_all)
VALUES ('Admin', 'admin', 'plaintext:a-long-password', true, true);
```

and then set a new password on the admin's page (it is stored with
bcrypt).

## Sessions and login

- A session lasts `cookie_duration` after the last page opened (pages that
  update themselves do not extend it, except the notifications, as in
  CMS). The cookie is `HttpOnly` and `SameSite=Lax`.
- Disabling an admin, deleting them or changing their password ends their
  sessions at the next request.
- Login attempts are limited per address and per username (10 failures in
  a row, then one every 6 s) and bcrypt checks go through a queue with one
  per core (D42): a brute force attack gets 429 without slowing the rest
  of the server.
- Every form carries the CSRF token; requests from other sites are
  refused.

## Audit log

Every change made in the administration is recorded in the `audit_log`
table, in the same transaction as the change (if the change is not saved,
neither is the entry): who (username and address), when, which action
(`contest.update`, `task.create`, `submission.reevaluate`…), on which
object and contest, with the fields that changed (old and new value).
Passwords and secrets show as `[redacted]`.

The **Audit log** page (main menu, and the contest's own in the menu of
each contest) lists the newest entries first, 50 at a time, filtered by
contest, admin and object (type and id).

## Server page

The **Server** page stores in the database (`server_settings`):

- **Time zone**: the one of every date of the three sites (D6). Empty uses
  `timezone` of `cms.yaml`. The other sites read it every 30 seconds.
- **Contestant site address** and **Scoreboard address**: the
  administration does not know the name contestants and spectators use, so
  links to those sites are built from these values. A full address
  (`https://contest.example.org/`) is used as is; `:8888` means "the host
  of this page on port 8888", which works in installations by ports
  whether the server is reached by name or by IP.

Only admins with every permission can change it.

## Live updates

Next to the admin name there is a green, yellow or red indicator
(connected, reconnecting, offline), as on the contestant site. Pages get
live events (`/events` and `/contest/<id>/events`): new questions show the
notification at once and update the counter of the menu, and the overview
reloads the queue and the judges when they change. Offline, pages poll as
CMS does (notifications every 15 s, overview every 5 s). Behind a proxy,
`*/events` must not be compressed or buffered (see [what a proxy
needs](contestant-site.md#what-a-proxy-needs)).

## Pages

| Page | What it shows |
|---|---|
| Overview (`/`, `/contest/<id>/overview`) | status of the contest's submissions, job queue (the first 100 of each priority), judges and what each one runs |
| Resource usage | the services seen from this server: the administration, PostgreSQL, Valkey, the active dispatcher and the judges of each machine. CPU, memory and disk statistics are the `monitor` service's (its metrics); CMS plotted them with ResourceService, which does not exist here |
| Admins | list, creation, edition and deletion of admins |
| Server, Audit log | see above |

The other pages (contests, groups, users, teams, tasks, datasets,
submissions, communication, ranking, judges and problem preparation) are
documented as they are completed.

## Differences with CMS

- Translatable texts (English and Spanish, chosen by `Accept-Language`);
  in English the pages are CMS's.
- Problem preparer permission, Server page, audit log, live indicator and
  a menu with Judges, Server and Audit log.
- Inline handlers (`onclick=`) moved to `aws_handlers.js` for the content
  security policy (D10).
- There is no LogService: the "Logs" section of the overview says that
  each service writes to its standard output (`journalctl`, `docker
  compose logs`). There is no default `secret_key` notice: there is no
  default key (D5).
- Judges are identified by name (CMS numbers them).
- "Operation successful." notifications belong to each admin (CMS gives
  them to whoever polls first).
- Form dates are in the server time zone (CMS uses UTC).

## Contests, users, teams and communication

- **Contests**: the list, creation (just the name; a `default` group is
  created as the main group), removal (with everything in it, its tasks
  too) and the settings page with every option of the original:
  localizations, languages, downloads, questions, user tests, unofficial
  submissions before analysis, login (hidden participations, password,
  registration, IP restriction and autologin), tokens, limits, score
  decimal places and the times of the main group. Times are shown and read
  in the server time zone (the Server page), which the labels name.
- **Groups**: each group has its start, stop, per-contestant length and
  analysis mode. A new group copies the times of the main group. A group
  with users and the main group cannot be deleted.
- **Contest tasks**: add a task without a contest, remove it and move it
  (up, down, to the top, to the bottom). Needs the "all" permission.
- **Users**: paginated list (100 per page), creation, edition (the
  password in plain text or hashed with bcrypt; with a hashed password,
  leaving the field empty keeps it), removal (with their participations
  and submissions) and their participations.
- **Teams**: list, creation, edition, removal (participations stay
  without a team) and **CSV import** on the teams page: one row per team
  `code,name` (a first row `code,name` is skipped; an empty name takes the
  code). An existing code gets the new name. If any row is wrong (bad or
  repeated code, too many or too few columns) nothing is imported and the
  notification lists the failing lines.
- **Participations**: add users to a contest in a group; the page of a
  participation changes the group, team, own password (empty = the
  user's), hidden, unrestricted, IP addresses or subnets, first login
  time, delay and extra time; it shows its submissions, questions and
  private messages.
- **Communication per contest**: announcements (Markdown, with preview),
  questions (quick answers or text, claim/unclaim, ignore/unignore) and
  private messages. Needs the "all" or "messaging" permission. The
  unanswered questions badge of the menu, the questions page and each
  participation page update live; contestants get announcements, messages
  and answers at once.
- **Ranking**: the table (from the `task_scores` cache, nothing
  recomputed), the text and CSV downloads in the original's format, and
  the **secret link to the public scoreboard**:
  `<scoreboard address>?secret=<token>`. The token is
  `config.RankingLinkToken(contest name)`: the first 16 bytes (in hex) of
  `DeriveKey(secret_key, "ranking-web/secret-link", contest name)`. It is
  the same in every service, changes when `secret_key` or the contest name
  changes, and is never stored. The scoreboard address is set on the
  Server page.

Every change goes to the audit log and publishes the matching live events
(contest, participation and scoreboard changes).

## Tasks and datasets

The task and dataset pages are the original CMS ones: task list, add and
remove (removing needs **all**), the task page with all its options,
statements and attachments, datasets (add, clone with or without results,
rename, delete, make live with the preview of the score changes and the
message to the contestants, background judging), task and score types
with their parameter forms, managers and testcases (add one, add from a
zip with name templates, delete, download all as a zip). The problem
preparer (`permission_tasks`) can do all of it except removing tasks and
moving them between contests. Differences with CMS: the activation preview
shows at most 5000 changed submissions; the testcases of a zip are written
in one transaction.

Preparing problems in the browser (importing packages with a preview,
`problem.yaml`, files one by one, testcases one by one with generated
outputs, a generator, test submissions and the list of what is missing) is
in [Creating a problem](creating-a-problem.md).

## Submissions, user tests and reevaluation

- **Contest submissions** (`/contest/<id>/submissions`): the list of the
  original, 50 per page, newest first, with a filter form (R6.3): task,
  user (username), status (compiling, compilation failed, evaluating,
  scoring, scored), official and language. Filters combine and are kept
  across pages. A page reads at most `tasks × (offset + 50)` submissions
  through the indexes (D24) with a fixed number of queries whatever the
  size of the contest; the status, official and language filters walk the
  submissions of each task until the page is full.
- **Submissions of a dataset** (`/dataset/<id>`): the same submissions with
  their results on that dataset (dataset selector as in the original).
- **Submission page**: files (downloadable), language, token, official
  (*Make official / unofficial*; it also recomputes the task score and
  tells the scoreboard), status and score, latency, compilation and
  evaluation failures, maximum time and memory, compilation output, the
  evaluation of each testcase with the admin text (*Admin Details*), and
  the score details as the contestant sees them. *Rerun and archive*
  recompiles or reevaluates one testcase asking the worker to archive the
  sandbox.
- **Diff submissions...**: two submissions of the table or by id; the
  unified diff is Python's `difflib` one (files up to 1 MB and 5000
  lines).
- **User tests** of the contest and the page of each test (input, output,
  compilation).
- **Reevaluation**: the C (recompile), E (reevaluate) and S (rescore)
  buttons of the original on a submission, a dataset, a participation and
  the whole contest. They do what CMS's `invalidate_submission` does: drop
  that part of the results in one transaction (`internal/judging`), record
  the action in the audit log (`submission.recompile`,
  `submission.reevaluate`, `submission.rescore`) and notify the
  dispatcher. As in CMS, reevaluating needs full permissions: the problem
  preparer does not reevaluate contestants' submissions.

The submission and user test tables update by themselves (R6.9): the rows
are live regions reloaded on the contest's `submission` and `user_test`
events, and a submission page reloads when its result changes.

## Judges

The **Judges** page (main menu) shows the workers (name, host, state,
current jobs, done and failed, last seen, version), the queue by priority
(waiting, in progress, oldest wait, retries and dead letters), the jobs in
progress, the next jobs and the dead letters. With full permissions an
admin can disable or enable a worker (it takes no new jobs and finishes
its current ones; it stays disabled across restarts), requeue a stuck job
in progress (another worker takes it at once), drop a job from the queue
(the dispatcher enqueues it again if it is still needed) and requeue or
delete a dead letter. Every action is audited. The page follows the
dispatcher's `judging` and `worker` events; see also
[judges.md](judges.md).
