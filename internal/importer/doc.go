// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Package importer brings contests, tasks, users and teams from packages
// on disk into the database (SPEC R8): what cmscontrib/loaders and the
// cmsImport* scripts of CMS do. It has the parts every format shares:
//
//   - the model (Contest, Task, Dataset, User, Team, Participation...),
//     with one field per column of CMS and the defaults of CMS (NewContest,
//     NewTask, NewGroup), mirroring the objects upstream loaders build.
//     File contents are referenced lazily (File: a path, a function that
//     opens it, bytes or a digest), so a package with gigabytes of
//     testcases streams to the blob store and never sits in memory;
//   - the Loader interfaces (ContestLoader, TaskLoader, UserLoader,
//     TeamLoader) and a Registry with autodetection (upstream
//     choose_loader). Each format lives in a subpackage
//     (internal/importer/<format>) that registers its loader in Default
//     from init; the commands import the subpackages for that effect;
//   - the validation (ValidateTask, ValidateContest...): the database
//     constraints of CMS and what the judge can run (task and score types
//     with their parameters, managers, languages), as a Report of errors
//     and warnings with their location, for `cms ctl validate-task` and
//     the zip import preview of the administration;
//   - the Writer, which stores a model: validation first (nothing is
//     written with errors), then the files in the blob store (hashed with
//     SHA-256: CMS digests are SHA-1, D19), then every row in one
//     transaction, with a bounded number of statements whatever the number
//     of testcases or participants (N2). The update rules are those of
//     ImportContest, ImportTask, ImportUser and ImportTeam of CMS (see
//     WriteContest and WriteTask). With Writer.DryRun the files are only
//     hashed and the transaction is rolled back: the Result says what an
//     import would change (--dry-run, the preview of the administration);
//   - ImportContest, ImportTask, ImportUsers, ImportTeams and
//     ImportDataset, which run a loader and the writer, reading only the
//     tasks they need.
//
// Differences with upstream:
//
//   - Nothing is written to the package directories: upstream loaders
//     leave .itime and .import_error files to detect changes; here an
//     update compares the package with the database and changes only
//     what differs (the result lists it).
//   - Everything an import does is one transaction, also for users and
//     teams (upstream commits them one by one), and errors that upstream
//     finds while writing (a missing user, a task of another contest) or
//     never (an unknown language or task type, invalid parameters, a
//     missing checker) abort the whole import.
//   - ImportContest can create the users and teams of the package
//     (ContestOptions.ImportUsers); ImportUsers updates existing users
//     with UserOptions.Update; WriteTask can add an existing task that
//     belongs to no contest to the contest given.
//   - After an update the task score cache is recomputed when the score
//     mode or precision changed, and the result carries notifications for
//     the dispatcher (judging.Event) to send after the commit.
package importer
