# 3. First access and admin accounts

*Español: [Primer acceso y cuentas de administrador](../../es/manual/03-primer-acceso.md)*

How to log in to the administration for the first time, find your way
around the menu, create accounts for your team with just the permissions
they need, and recover access if a password is lost.

## Contents

1. [Logging in for the first time](#1-logging-in-for-the-first-time)
2. [Changing the administration language](#2-changing-the-administration-language)
3. [A tour of the menu](#3-a-tour-of-the-menu)
4. [Accounts for your team and their permissions](#4-accounts-for-your-team-and-their-permissions)
5. [Changing your password](#5-changing-your-password)
6. [Recovering access (lost password)](#6-recovering-access-lost-password)
7. [The Server page](#7-the-server-page)
8. [Before going on: check the installation](#8-before-going-on-check-the-installation)
9. [What if…](#9-what-if)

---

## 1. Logging in for the first time

1. Open the address of the **administration** that the installer showed:
   `https://admin.your-domain` or `http://SERVER-IP:8001/`.
2. Type the user `admin` (or the one you gave with `--admin-user`) and the
   password the installer showed.
3. Press *Login*.

At the top right you will see *Hello, Admin*: your name is a link to the
list of administrators.

> The session closes by itself after a while without opening pages.
> Several failed attempts in a row hold back new attempts from that
> address for a few seconds: it is a protection against attacks, not an
> error.

## 2. Changing the administration language

The administration has no language selector: it uses your browser's
preferred language. To see it in another language (for example Spanish),
put that language first in the browser's language settings and reload the
page. More details in [chapter 1](01-concepts.md#the-interface-language).

## 3. A tour of the menu

The menu on the left has two parts:

**At the top, always visible:**

| Menu | What it is for |
|---|---|
| *Overview* | general status: pending submissions, judges, queue, notices |
| *Resource usage* | CPU, memory and disk of each service |
| *Judges* | status of the machines that evaluate ([ch. 9](09-during-the-contest.md)) |
| *Server* | public addresses of the sites (section 7) |
| *Audit log* | who changed what and when |
| *Contests* | choose a contest or *(create a contest...)* ([ch. 4](04-contests.md)) |
| *Tasks* | all the tasks ([ch. 5](05-tasks.md)) |
| *Users* | all the people, import CSV ([ch. 8](08-users-and-teams.md)) |
| *Teams* | teams or delegations |

**When you choose a contest**, its pages appear: *General*, *Ranking*,
*Submissions*, *User tests*, *Users*, *Groups*, *Tasks*,
*Announcements*, *Questions* and *Audit log*
([ch. 4](04-contests.md) and [ch. 9](09-during-the-contest.md)).

The pages **update by themselves**: an indicator next to your name is
green when live updates work, yellow while it is connecting and red if the
connection was lost (the page is then polled every so often, and it still
works).

## 4. Accounts for your team and their permissions

The recommended approach is that **each person has their own account**:
that way the [audit log](09-during-the-contest.md) says who made each
change, and you can give each person only what they need.

| Permission | Can | Example |
|---|---|---|
| *All permissions* | everything | contest coordinator |
| *Messaging permissions* | reply to, ignore and take questions; send announcements and private messages | whoever handles questions during the exam |
| *Problem preparer permissions* | create and modify tasks, statements, attachments, datasets and testcases, and test solutions; not users or contests | the author of a task |
| none | see everything without changing anything | an observer |

**To create an account:**

1. Press your name (top, *Hello, …*): the *Admins list* opens.
2. Press *Add a new admin*.
3. Fill in the name, the username (letters, digits, `_` and `-`) and a
   password, tick the permissions and press *Create*.
4. Give the person the username and password through a secure channel;
   they will be able to change the password themselves (section 5).

You can also do it from the console:

```sh
sudo cmsctl add-admin --name "María Pérez" --role messaging maria
```

It generates a password and shows it only once. `--role` can be `all`,
`messaging`, `tasks` or `readonly`.

**Disabling or deleting an account:** on the administrator's page, untick
*Enabled* so they cannot log in, or use *Delete this admin*. CMS does not
allow leaving the site without any enabled administrator with all
permissions.

## 5. Changing your password

1. Press your name and then your username in the list.
2. Type the new password in the password field. If you leave it empty, the
   previous one is kept.
3. Press *Update*.

Changing the password closes the other open sessions of that account.

## 6. Recovering access (lost password)

The first administrator's password is shown **only once** at installation
and is stored nowhere. If it is lost, from the server:

```sh
sudo cmsctl admin-password admin
```

It generates a new password for `admin` and shows it only once:

```
password: Ab3dEf6hIj9kLm2nOp5q
The password is shown only now: store it safely.
```

- Replace `admin` with the relevant user. If you do not remember the
  users, running the command with a name that does not exist lists them.
- `--enable` also re-enables a disabled account.
- To set a password of your choice without it staying in the console
  history: `sudo cmsctl admin-password admin --password-file -` and type it
  (finish with Enter and Ctrl+D).

If there is no administrator left, create a new one:
`sudo cmsctl add-admin admin`.

> **Never share passwords by chat or in screenshots.** If a password was
> exposed, change it (section 5).

## 7. The Server page

*Server* stores the public addresses of the contestant site and the
scoreboard. The administration uses them to show links (for example, "open
the scoreboard"). The installer configures them by itself; change them if
you later set up a domain or change ports. Details in
[../administration.md](../administration.md#server-page).

## 8. Before going on: check the installation

Before preparing the first contest, check that everything works:

```sh
sudo cmsctl doctor
```

All the lines should be `PASS` (a `WARN` saying "there are no contests
yet" is normal). Each `FAIL` or `WARN` comes with a `fix:` line with the
solution. See [chapter 14](14-troubleshooting.md).

## 9. What if…

**…the administration page does not open.** Check the address and the
port; run `sudo cmsctl doctor`. If you use a firewall, the port must be
open ([ch. 2](02-installation.md#9-the-firewall)).

**…it says *Failed to log in.*** Wrong username or password. Recover the
password with `sudo cmsctl admin-password USERNAME`.

**…the administration appears in a language you do not want.** Set the
browser's language (section 2).

**…a colleague cannot do something ("forbidden").** They lack the
permission: grant it from their administrator page.

---

**Next:** [4. Contests](04-contests.md) ·
**Previous:** [2. Installation](02-installation.md) ·
[Index](README.md)
