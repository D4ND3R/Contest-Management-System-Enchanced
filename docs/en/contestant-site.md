# Contestant site

*Español: [../es/sitio-concursante.md](../es/sitio-concursante.md).*

The contestant site (`cms contest-web`) is the equivalent of CMS's
ContestWebServer: the same pages, with the same HTML, CSS and JavaScript,
the same URLs and the same JSON API. This guide explains how to configure
it, how contestants log in, how the language is chosen and what a proxy
needs for the live updates.

## URLs

| `contest_web.contest` | What is served |
|---|---|
| empty (default) | `/` lists the contests; each one lives at `/<name>` (`/demo`, `/demo/documentation`…) |
| `demo` | the contest `demo` at `/` (`/`, `/documentation`…) |

In both cases the site also serves:

- `/static/…`: the files of CMS (Bootstrap 2, `cws_utils.js`, icons) and
  `live.js`;
- `/docs/…`: the language documentation, from `contest_web.docs_path`
  (files only; a directory is a 404);
- `/source`: redirects to the source code of the running version (AGPL);
  every page links it in the footer, next to the license;
- `/healthz`: minimal status for load balancers (`{"status":"ok"}`). The
  metrics and the detailed status are on `contest_web.ops_listen`.

Every URL is relative, as in CMS: the site works the same behind a proxy
that publishes it under a sub-path.

## Configuration

Section `contest_web` of `cms.yaml` (every key has its `CMS_CONTEST_WEB_*`
variable; see [Configuration](configuration.md)):

| Key | Default | What it does |
|---|---|---|
| `listen` | `127.0.0.1:8888` | address of the site |
| `ops_listen` | `127.0.0.1:9188` | metrics, detailed status and pprof (do not expose it) |
| `contest` | empty | serve one contest at `/` (see above) |
| `num_proxies` | `0` | trusted proxies in front of the site: with 1, the contestant's address is the last one of `X-Forwarded-For` (and `X-Forwarded-Proto` says whether it came over HTTPS) |
| `cookie_duration` | `3h` | how long a session lasts without activity |
| `max_submission_length` | `100000` | bytes per file of a submission |
| `max_input_length` | `5000000` | bytes of the input of a user test |
| `admin_token` | empty | lets administrators log in as a contestant through the API (`admin_token` of `api/login`); empty disables it |
| `docs_path` | `/usr/share/cms/docs` | directory served at `/docs` |

The site also needs `secret_key` (the same in every service: it signs the
cookies and the CSRF tokens), the database, Valkey (pending notifications
and live events) and the file store (`storage`).

**Time zone.** Every time is shown in the time zone of the server: the one
of `cms.yaml` (`timezone`) or the one chosen on the Server page of the
administration, which the site reads again every 30 seconds. CMS's time
zones per user and per contest are imported but do not change what is
shown. Where CMS shows the time of a submission or of a message, the site
shows the **contest time** (how long after the contestant's start), with
the full time on mouse over.

## How contestants log in

The login form is on the contest page. What applies is set on the contest
(administration) and on each participation:

- **Password.** `Allow password authentication`
  (`allow_password_authentication`) enables the form. Passwords are stored
  as in CMS: `bcrypt:<hash>` or `plaintext:<text>`. A participation with
  its own password uses it instead of the user's.
- **Registration.** With `allow_registration` a "Register" link appears:
  it creates a new user (with a bcrypt password) or lets an existing user
  join the contest with their password. When there are teams, choosing one
  is mandatory.
- **IP restriction** (`ip_restriction`): a participation with addresses
  (`ip`, networks such as `10.0.0.0/24`) can only log in from them; without
  addresses, from anywhere; with an empty list, from nowhere.
- **IP autologin** (`ip_autologin`): whoever comes from the exact address
  (`/32`) of a participation is logged in without the form. If two
  participations have the same address neither is (it is a configuration
  error, and it is logged).
- **Hidden participations** (`block_hidden_participations`): hidden
  participations cannot log in.
- **Unrestricted** (`unrestricted` on the participation): sees the tasks
  and submits outside the contest times.

The session is a signed `<contest>_login` cookie, `HttpOnly` and
`SameSite=Lax` (and `Secure` over HTTPS). As in CMS every page renews it:
it expires after `cookie_duration` **without activity** (background
requests, such as the notifications, do not renew it). Changing the
contestant's password ends their sessions. "Logout" deletes it.

Login attempts are limited: 10 in a row per user and contest, then one
every 6 seconds (answer 429), plus a generous limit per address, since a
whole lab may share one address.

## Contest times

The times are in the **group** of each participation (the main group of
the contest by default), as in CMS:

- start and stop; a contestant with `delay_time` starts and stops later,
  and `extra_time` extends their stop;
- **per-user time frame** (`per_user_time`): the contestant chooses when to
  start with the "Start!" button and gets that much time, never past the
  stop of the contest;
- **analysis mode** (its own start and stop): after the contest, tasks can
  be seen and solved again outside the competition.

The overview explains the phase of each contestant, and the clock on the
left counts down the time left.

## Interface language

The translations are those of CMS (25 languages) plus this project's. The
language is chosen as in CMS:

1. the selector of the top bar stores a `language` cookie;
2. otherwise the best language of the browser (`Accept-Language`);
3. otherwise the first available one.

A contest may limit the languages (`allowed_localizations`, by prefix: `es`
includes `es` and `es_CL`). With a single language there is no selector.

## Live updates

Pages update themselves: an announcement, a message or an answer shows up
at once, and submission results refresh without reloading. Next to the
contestant's name there is an indicator:

| Color | Meaning |
|---|---|
| green | connected: news arrive at once |
| yellow | connecting or reconnecting (for example after a server restart) |
| red | disconnected: the page checks for news every 15–30 seconds, as CMS does, and keeps trying to connect (every 1 to 60 seconds) |

Pages never depend on events alone: when some are lost the page reloads
what it shows. A tab hidden for more than a minute closes its connection
and opens it again when shown.

### What a proxy needs

The events come from `GET <contest>/events` (server-sent events: an HTTP
response that never ends). The server sends a `ping` as soon as it opens
and then every 15 seconds, and answers with `X-Accel-Buffering: no` and
`Cache-Control: no-cache, no-transform`. The proxy must:

- **not buffer** the response (nginx honors the header; in other proxies,
  disable buffering for `*/events`);
- **not compress** it: leave `*/events` out of gzip/zstd;
- **not close** idle connections before 15 seconds (60 or more is better);
- preferably speak **HTTP/2** to the browsers: with HTTP/1.1 every tab
  takes one of the 6 connections per server.

Caddy:

```
contest.example.org {
	@noevents not path */events
	encode @noevents zstd gzip
	reverse_proxy 127.0.0.1:8888
}
```

(Caddy's `reverse_proxy` does not buffer server-sent events.)

nginx:

```
location / {
    proxy_pass http://127.0.0.1:8888;
    proxy_http_version 1.1;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
}
location ~ /events$ {
    proxy_pass http://127.0.0.1:8888;
    proxy_http_version 1.1;
    proxy_set_header Connection "";
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_buffering off;
    gzip off;
    proxy_read_timeout 1h;
}
```

With a proxy in front, set `num_proxies: 1` so the site sees the real
addresses of the contestants (IP restriction and autologin, login limits).

## Tasks, submissions and tests

**Description.** The page of each task shows its statements, its limits,
its type, the compilation commands of its languages, its token rules and
its attachments (with their type and size), as in CMS. CMS only has PDF
statements; here a statement can also be **Markdown** (shown inside the
site, without raw HTML) or **LaTeX** source (downloaded as `.tex`): the
site tells them apart by their content. With several statements the
task's primary ones and those in the contestant's preferred languages are
shown first.

**Submitting.** One file per element of the submission format, with the
language chosen in the list (choosing a file narrows the list to the
languages of its extension, as in CMS) or, for tasks of several files, a
`.zip` with all of them. The limits are CMS's, with its messages:

| Setting (contest or task) | Effect |
|---|---|
| `max_submission_number` | submissions in total (contest) or on the task |
| `min_submission_interval` | time between submissions; it does not apply in the last `min_submission_interval_grace_period` of the contestant's time |
| `contest_web.max_submission_length` | bytes per file |

The limits are checked again when the submission is stored, so
simultaneous submissions cannot get past them. Submissions are official
while the contestant competes; in analysis mode (and after their time, if
`allow_unofficial_submission_before_analysis_mode`) they are unofficial
and do not count.

**Submissions.** The submissions page lists the latest 500 of the task,
newest first. Their status and scores update themselves (as in CMS, and
at once with live updates). "details" shows the score by subtask and the
testcases the task's `feedback_level` allows (`full`, `restricted`,
`oi_restricted`; everything in analysis mode or once a token was played)
and the compilation output. Files can be downloaded if the contest allows
it (`submissions_download_allowed`).

**Tokens.** The rules of CMS: contest and task tokens, initial number,
generation, maximum, total and minimum interval. A token shows the full
result of a submission and makes it count for the score. Two simultaneous
requests never play more tokens than are available.

**Tests.** The Testing page (`allow_user_tests`) runs a solution on an
input of the contestant for the tasks whose type allows it (not output
only), with `max_user_test_number`, `min_user_test_interval` and
`contest_web.max_input_length`. Tests are numbered per task from the
oldest; the page lists the latest 200 of each task.

## Communication

The Communication page shows the announcements of the contest, the
contestant's questions with their answers, and the private messages of
the staff, newest first. Announcements, answers and messages are
Markdown; questions are plain text. They appear without reloading: a
notification pops up and the page's lists refresh themselves (on that page
the "unread" counter stays at zero). When the news is the first of its
kind (the page had no list of messages, for example), the page reloads
within 10 seconds instead, waiting while a question is being written. The
form to ask questions appears when the contest allows questions
(`allow_questions`):

- the subject has at most 50 characters and the text at most 2000, as in
  CMS (a longer question is refused with "Question too long!");
- the subject suggests the names of the tasks once the contest has started;
- a contestant can ask 10 questions in a row, then one every 20 seconds
  ("Too many questions!"), so that nobody floods the staff.

The staff sees new questions at once in the administration.

Programs (scripts, tools written for CMS) use the [JSON API](api.md).

## Security

- Every form carries a CSRF token (`_xsrf`, as in CMS) and requests from
  other sites are refused.
- Strict content security policy: only the site's scripts and the pages'
  own, with their nonce; CMS's inline handlers (`onchange=`…) are in
  `static/cws_handlers.js`.
- Cookies are not visible to JavaScript (`HttpOnly`), except the CSRF
  token's, as in CMS.

## Differences with CMS

- The site's notifications ("Submission received"…) are kept in Valkey:
  every replica of the site sees them, not only the one that made them.
- The session cookie holds neither the password nor its hash, but a signed
  fingerprint; it lasts `cookie_duration` after the last page.
- Login attempts are rate limited; registration is limited per address;
  a contestant can ask 10 questions in a row, then one every 20 seconds.
- Live updates and their indicator (the lists of the Communication page
  refresh themselves); a link to the source code in the footer.
- One time zone; contest time instead of the time of day.
- Statements in Markdown and LaTeX besides PDF.
- Lists are bounded: the latest 500 submissions and 200 tests of each
  task (older ones keep their URLs).
- The public testcases of a subtask that also has private ones are shown
  ("details" and the API); CMS fails there with "Score details
  temporarily unavailable.".
- The times of the submission rows are written in the page's language
  (CMS writes them in English).
