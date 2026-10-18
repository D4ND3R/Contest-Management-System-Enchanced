# 8. Users, teams and participations

*Español: [Usuarios, equipos y participaciones](../../es/manual/08-usuarios-y-equipos.md)*

How to register the contestants (one by one or all at once with a CSV),
how to group them into teams and how to adjust each one's participation in
a contest (extra time, hidden, unrestricted…).

## Contents

1. [Three ideas: user, participation, team](#1-three-ideas-user-participation-team)
2. [Importing users from a CSV (recommended)](#2-importing-users-from-a-csv-recommended)
3. [Preparing the CSV in a spreadsheet](#3-preparing-the-csv-in-a-spreadsheet)
4. [Creating a user by hand](#4-creating-a-user-by-hand)
5. [The fields of a user](#5-the-fields-of-a-user)
6. [Adding users to a contest](#6-adding-users-to-a-contest)
7. [The participation: extra time, hidden, unrestricted…](#7-the-participation-extra-time-hidden-unrestricted)
8. [Teams](#8-teams)
9. [Handing out the credentials](#9-handing-out-the-credentials)
10. [Deleting users](#10-deleting-users)
11. [Common mistakes](#11-common-mistakes)

---

## 1. Three ideas: user, participation, team

- A **user** is a person: first name, last name, username and password. It
  exists once even if the person takes part in many contests.
- A **participation** is "this user is in this contest", with their group
  (schedule), their team and their own settings (extra time, etc.). **A
  user without a participation in a contest cannot log in to that
  contest.**
- A **team** groups users, for example by school or region: the
  scoreboard shows the team's flag or name.

## 2. Importing users from a CSV (recommended)

1. Prepare a CSV file with one row per user and the columns
   `username,password,first_name,last_name` and, optionally, `email`:

   ```csv
   username,password,first_name,last_name,email
   ana01,Kq7-mzt-42,Ana,Pérez,ana@colegio.cl
   beto02,Rv3-plx-91,Alberto,Soto,
   carla03,Tm8-dqe-55,Carla,Núñez,
   ```

   - The first row (the one starting with `username`) is optional and is
     ignored.
   - The email is optional (it can be empty, or the column can be absent).
2. In the administration: *Users* → at the end of the list, *Import
   users*.
3. In *CSV file* choose the file.
4. In *Also add them to contest* choose the contest (or *None* to only
   create the users).
5. Press *Import*.

Result: "Users imported: 3 user(s) created, 0 updated, 3 added to the
contest."

Rules:

- **All or nothing**: if any row has an error, none is imported and the
  message says which lines to fix (for example "line 3: invalid username
  "Ana Pérez" (letters, digits, _ and - only)").
- The **username** only allows letters without accents, digits, `_` and
  `-`. No spaces or `ñ`.
- The **password** cannot be empty. It is stored in plain text (as in the
  original CMS's importers), so you can hand it again to a contestant who
  forgot it.
- A username that already exists **is updated** with the new data (useful
  for correcting a list).
- Users added to the contest enter their main group. Those who were
  already taking part stay the same.
- At most 10 000 rows per file.

## 3. Preparing the CSV in a spreadsheet

In Excel, LibreOffice Calc or Google Sheets:

1. Columns A to E: `username`, `password`, `first_name`, `last_name`,
   `email`.
2. **Generating random passwords**: in column B, for example
   `=CONCATENATE(CHAR(RANDBETWEEN(65,90)),RANDBETWEEN(1000,9999),CHAR(RANDBETWEEN(97,122)))`
   (in a spreadsheet set to Spanish, the functions are `CONCATENAR`, `CAR`
   and `ALEATORIO.ENTRE`, and the argument separator is `;`). Then **copy
   and paste as values** so they do not change every time.
3. **Generating usernames** from the name: better a fixed code (`d4oi001`,
   `d4oi002`…) than deriving it from the name (accents and spaces are not
   allowed).
4. Save as **UTF-8 comma-delimited CSV**:
   - Excel: *Save As* → *CSV UTF-8 (Comma delimited)*.
   - LibreOffice: *Save As* → *Text CSV*, character set *Unicode (UTF-8)*,
     field delimiter `,`.
   - Google Sheets: *File* → *Download* → *Comma-separated values*.

> **Careful:** Excel with some regional settings (Spanish, for example)
> sometimes saves with a **semicolon** (`;`) as the separator. Open the
> file with a text editor and check that the fields are separated by
> commas. If you see `;`, change the separator when saving.

## 4. Creating a user by hand

*Users* → *(create new user...)*: fill in the fields from section 5 and
create it. Then add it to the contest (section 6).

## 5. The fields of a user

| Field | What it is | Example |
|---|---|---|
| *First name* | the name that is shown | `Ana` |
| *Last name* | last name | `Pérez` |
| *Username* | for logging in; letters, digits, `_`, `-` | `ana01` |
| Password | plain text (*Plain text*) or encrypted (*Hashed (bcrypt)*) | |
| *E-mail* | optional | |
| *Timezone* | to show times in their local time | `America/Santiago` |
| *Preferred languages* | language codes of the statements highlighted for them, from most to least preferred | `es, en` |

With an encrypted (bcrypt) password nobody can read it again: if the
contestant forgets it, a new one has to be set.

## 6. Adding users to a contest

- **When importing the CSV**, with *Also add them to contest*.
- **From the contest**: the contest's *Users* page → *Add a new user:* →
  choose → *Add user*.
- **From the user**: their page → *Add participation* → choose the
  contest.

The contest's *Users* list shows all the participants and their group.
Clicking one opens their participation.

## 7. The participation: extra time, hidden, unrestricted…

On the participation's page (*Participation information*):

| Field | What it is for | When to use it |
|---|---|---|
| *Group* | their schedule ([ch. 4](04-contests.md#9-groups)) | they take the exam in another shift |
| *Team* | the team they represent | delegations |
| Participation password | a password only for this contest (empty = the user's). **Warning: it is stored in plain text.** | contests with different passwords |
| *Hidden participation* | does not appear in the ranking or on the scoreboard | guests, tests, out of competition |
| *Unrestricted participation* | ignores the time and submission limits | the organizers' test accounts |
| *IP address or subnet* | can only log in from that IP/network (if the contest restricts by IP) | labs with a fixed IP |
| *Delay* | their contest starts and ends later | arrived late for a justified reason |
| *Extra time* | additional time at the end | power outage, special needs |

Example: a contestant lost 10 minutes because of a power outage. In their
participation, *Extra time* = 10 minutes → *Update*. Their counter adjusts
immediately.

## 8. Teams

*Teams* → *(create new team...)*: a short **code** (letters, digits, `_`,
`-`; for example `CL-RM`) and a **name** (`Región Metropolitana`).

For many teams, at the end of the list use *Import teams* with a CSV
`code,name`:

```csv
code,name
CL-RM,Región Metropolitana
CL-VA,Valparaíso
```

Then assign the team in each participation. The scoreboard flag is
configured in the scoreboard ([ch. 11](11-scoreboard.md)).

## 9. Handing out the credentials

CMS does not print credentials. The practical way:

1. Keep the spreadsheet you made the CSV from.
2. Use mail merge (Word, Docs) to print one card per contestant with their
   username, password and the site address, or send them by email.
3. Hand them out together with the contestants' handout from
   [chapter 10](10-contestant-site.md#handout-for-the-contestants).

## 10. Deleting users

*Users* → select → *Remove*. It asks for confirmation and **also deletes
their participations and submissions**. To take them out of one contest
only, use *Remove from contest* on the contest's *Users* page.

## 11. Common mistakes

| Problem | Cause | Fix |
|---|---|---|
| "invalid username" when importing | spaces, accents or `ñ` in the username | use codes (`ana01`) |
| "expected 4 or 5 columns" | `;` separator or commas inside a name | save with commas; a name with a comma goes in quotes `"Pérez, Ana"` |
| The first line gives an error | a header with other column names | the header must start with `username`, or remove it |
| The contestant cannot log in | they have no participation in that contest, or the participation's password is different | check the contest's *Users* page |
| A test account shows up on the scoreboard | *Hidden participation* was not ticked | tick it: it disappears from the scoreboard in seconds |

---

**Next:** [9. During the contest](09-during-the-contest.md) ·
**Previous:** [7. Importing](07-importing.md) ·
[Index](README.md)
