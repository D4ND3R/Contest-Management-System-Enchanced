# JSON API of the contestant site

*Español: [../es/api.md](../es/api.md).*

The contestant site answers the same JSON API as CMS's ContestWebServer
(CMS's `docs/API.rst`), so the scripts and tools written for CMS work
unchanged: they log in, list the tasks, submit, follow their submissions
and read the details of the results. An administrator can also act on
behalf of a contestant (impersonation), for example to load submissions
from another system.

The API is for programs: its messages are in English and are not
translated. The contestant pages are described in
[Contestant site](contestant-site.md).

## URLs

The paths are relative to the contest, like every page of the site:

| `contest_web.contest` | Login URL |
|---|---|
| empty (default) | `https://contest.example.org/<contest>/api/login` |
| `demo` | `https://contest.example.org/api/login` |

The examples use `$SITE` for the URL of the contest
(`https://contest.example.org/demo`).

## Answers and errors

Every answer is JSON (`Content-Type: application/json; charset=utf-8`).
Errors have a status code other than 200 and an object with an `error`
field (and `details` when a submission is refused):

| Code | When | `error` |
|---|---|---|
| 400 | a Boolean parameter that is not `0` or `1` | `Cannot parse boolean argument <name>` |
| 400 | `api/login` of a contestant already logged in, with another username | `Logged in as <user> but trying to login as <other>` |
| 403 | wrong username, password or admin token | `Login failed` |
| 403 | no login data, or it expired | `An authenticated user is required` |
| 403 | outside the contest (task list, submissions) | `The contest is not open` |
| 403 | before the contest (details) | `Wrong contest phase` |
| 403 | `full_details` without impersonation | `Admin impersonation required` |
| 404 | unknown task | `Task not found` (`Not found` in `submission_list`) |
| 404 | unknown submission, or of someone else | `Submission not found` |
| 422 | submission refused | the reason, with `details` (see [Submit](#submit)) |
| 429 | too many login attempts | `Too many login attempts` (with `Retry-After`) |
| 500 | server error | `Internal Server Error` |

## Authentication

### Logging in

`POST api/login` with the form fields `username` and `password` (URL
encoded or multipart, as an HTML form sends them):

```sh
curl -s -d username=alice -d password=secret "$SITE/api/login"
```

```json
{"login_data": "1.1792065600.eyJ1Ijo…"}
```

Every other call sends that value in the `X-CMS-Authorization` header:

```sh
LOGIN=$(curl -s -d username=alice -d password=secret "$SITE/api/login" | jq -r .login_data)
curl -s -H "X-CMS-Authorization: $LOGIN" "$SITE/api/task_list"
```

What applies to the login form applies to the API: the contest must allow
password authentication, the IP restriction and the block of hidden
participations apply, and so do the login limits (10 attempts in a row
per user, then one every 6 seconds; a 429 says how long to wait). The
login data expires `cookie_duration` (3 hours by default) after the login:
log in again when a call answers 403 `An authenticated user is required`.
Changing the contestant's password invalidates it.

Other ways in, as in CMS:

- **IP autologin**: in a contest with `ip_autologin`, requests from the
  address of a participation are already logged in (no header needed;
  `api/login` answers `{"login_data": "Already-Logged-In"}`).
- **Session cookie**: a browser logged in with the form can call the API
  with its cookie; `api/login` then returns the cookie's value, which also
  works as `X-CMS-Authorization`. POST requests without the header need
  the CSRF token (`_xsrf`), like any form.

A call to `api/login` from someone already logged in returns their login
data (the header they sent, or the cookie), or 400 if `username` names
another user.

### Impersonation

With `contest_web.admin_token` set in `cms.yaml` (empty disables it), an
administrator logs in as any participant by sending `admin_token` instead
of the password:

```sh
ADMIN=$(curl -s -d username=alice -d admin_token="$TOKEN" "$SITE/api/login" | jq -r .login_data)
```

Requests with that login data:

- ignore the IP restriction (but IP autologin, when it matches, comes
  first) and the block of hidden participations, and work even if the
  contest does not allow password authentication;
- may use `full_details`;
- accept these parameters set to `1` (in the URL or in the form):

| Parameter | Calls | Effect |
|---|---|---|
| `override_phase_check` | all | ignores the contest times (for example, submit after the end) |
| `override_official` | `submit` | the submission is official even outside the contest (with `0`, unofficial during it) |
| `override_max_number` | `submit` | ignores the maximum number of submissions |
| `override_min_interval` | `submit` | ignores the minimum time between submissions |

For other participants these parameters have no effect. Keep the token
secret and long (`cms ctl gen-secret` generates one): it opens every
account of every contest of the installation.

## Calls

| Call | When | Answer |
|---|---|---|
| `POST api/login` | always | `{"login_data": "…"}` |
| `GET api/task_list` | competing or analysis mode | the tasks |
| `POST api/<task>/submit` | competing or analysis mode | `{"id": "…"}` |
| `GET api/<task>/submission_list` | competing or analysis mode | the submissions on the task |
| `GET api/<task>/submissions/<id>/details` | from the start on | the details the contestant may see |
| `GET api/<task>/submissions/<id>/full_details` | from the start on, impersonation only | every detail |

"Competing" includes the contestant's own time frame (contests with
`per_user_time`), delays and extra time; unrestricted participants may
call them at any time.

### Task list

`GET api/task_list` lists the tasks in the order of the contest:

```json
{"tasks": [
  {"name": "sum", "statements": ["en", "es"], "submission_format": ["sum.%l"]},
  {"name": "quiz", "statements": ["en"], "submission_format": ["output_000.txt", "output_001.txt"]}
]}
```

`statements` are the languages with a statement (download them from
`tasks/<task>/statements/<language>` with the same header).
`submission_format` lists the files to send: `%l` stands for the extension
of the programming language (`sum.%l` is `sum.cpp`, `sum.py`…); names
without it are output files.

### Submit

`POST api/<task>/submit` with a multipart form: one file per element of the
submission format, in a field named exactly like it, and optionally the
`language` field (as listed on the submit form, for example
`C++17 / g++`; without it the language is guessed from the extension):

```sh
curl -s -H "X-CMS-Authorization: $LOGIN" \
     -F "sum.%l=@sum.cpp" -F "language=C++17 / g++" "$SITE/api/sum/submit"
```

```json
{"id": "4926046975063954292"}
```

Instead of separate files, a single archive (zip, tar, tar.gz or tar.bz2)
can be sent in a field named `submission`. The limits are those of the
form: the maximum number of submissions and the minimum time between them
(of the contest and of the task), `contest_web.max_submission_length` per
file. A refused submission answers 422 with CMS's messages:

```json
{"error": "Submissions too frequent!",
 "details": "Among all tasks, you can submit again after 60 seconds from last submission."}
```

| `error` | `details` (examples) |
|---|---|
| `Too many submissions!` | `You have reached the maximum limit of at most 3 submissions among all tasks.` |
| `Submissions too frequent!` | `For this task, you can submit again after 120 seconds from last submission.` |
| `Invalid submission format!` | `Please select the correct files.` (wrong field names, unknown language, missing files) |
| `Submission too big!` | `Each source file must be at most 100000 bytes long.` |
| `Invalid archive format!` | `The submitted archive could not be opened.` |

The submission is official while the contestant competes and unofficial in
analysis mode, as on the site. The id is the one the pages show (a 63-bit
number as a string: do not convert it to a JavaScript number).

### Submissions of a task

`GET api/<task>/submission_list` lists the contestant's submissions on the
task, oldest first:

```json
{"list": [{"id": "4926046975063954292"}, {"id": "1291403789260491089"}]}
```

The state and the score of a submission are on the pages of the site, with
the same header: `GET tasks/<task>/submissions/<id>` (JSON with `status`,
`public_score`, `task_public_score`…) and
`GET tasks/<task>/submissions/<id>/details` (the HTML the contestant sees).

### Details of a submission

`GET api/<task>/submissions/<id>/details` returns the score details of a
scored submission as the contestant may see them; `{"details": null}`
while it is not scored. Without a token (and outside analysis mode) they
are the details of the public testcases; with a token, or in analysis
mode, the details of every testcase. The task's feedback level filters
them, as on the details page:

| Feedback level | Testcases shown |
|---|---|
| `full` | all, with time and memory |
| `restricted` | in a subtask not fully solved, those up to the first one with the lowest score, the rest only by name; in a solved subtask, all |
| `oi_restricted` | in a subtask not fully solved, only the first one with the lowest score; in a solved subtask, all |

In analysis mode the level is always `full`. Tasks with subtasks
(`GroupMin`, `GroupMul`, `GroupThreshold`) give a list of subtasks:

```json
{"details": [
  {"idx": 0, "score_fraction": 0.0, "score": 0.0, "max_score": 40,
   "testcases": [{"idx": "000", "outcome": "Correct", "text": ["Output is correct"]},
                 {"idx": "001"}]},
  {"idx": 1, "score_fraction": 1.0, "score": 60.0, "max_score": 60,
   "testcases": [{"idx": "002"}, {"idx": "003"}]}
]}
```

`Sum` gives the list of testcases directly. `text` is a message and its
arguments, untranslated (`["Output is correct"]`, or a format such as
`["Execution killed with signal %s", "11"]`). In the public details, a
subtask with private testcases has no `score`, `score_fraction` or
`max_score` (CMS answers 500 there).

`GET api/<task>/submissions/<id>/full_details` (impersonation only) returns
every detail with full feedback, including `time`, `time_limit`,
`time_limit_was_exceeded` and `memory` of each testcase:

```json
{"details": [
  {"idx": 1, "score_fraction": 1.0, "score": 60.0, "max_score": 60,
   "testcases": [{"idx": "003", "outcome": "Correct", "text": ["Execution timed out"],
                  "time": 1.2, "time_limit": 1.0, "time_limit_was_exceeded": true, "memory": null}]}
]}
```

## Differences with CMS

- JSON is compact (no spaces) and numbers keep their value but not always
  their spelling: `40` may come as `40.0` in CMS and as `40` here.
- The public details of a subtask with private testcases are returned
  without the score keys; CMS fails with 500.
- `api/login` from a browser logged in with the cookie returns the cookie;
  CMS fails with 500.
- The API login has the limits of the login form (429).
- Server errors are JSON (`{"error": "Internal Server Error"}`) instead of
  the HTML error page.
- The login data is a signed value with a fingerprint of the password, not
  CMS's (which contains the password): login data of a CMS installation is
  not valid here; log in again.
