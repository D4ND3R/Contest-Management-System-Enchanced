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
