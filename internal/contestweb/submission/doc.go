// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Package submission is what the contestant site does with the files of a
// submission or a user test before inserting it (CMS
// cms/server/contest/submission): read them from the form or from an
// archive (ExtractFilesFromForm, ExtractFilesFromArchive), tell which
// element of the submission format each one is for and in which language
// (MatchFilesAndLanguage), apply the limits on the number of submissions
// and on the time between them (CheckSubmission, CheckUserTest, with the
// grace period of IsLastMinutes), fill in the files a partial submission
// lacks from the previous one (PreviousDigests) and store the files in
// the blob store (AcceptSubmission, AcceptUserTest).
//
// The package does not touch the database: the site reads the counts and
// the previous submission (Stats, Previous), and inserts the result
// (Accepted) in a transaction. Refusals are *Unacceptable values with
// the msgids of CMS, which the pages translate in their notifications
// and the API returns in English.
//
// Differences with CMS: archives may be zip, tar, tar.gz or tar.bz2 (not
// tar.xz, which Python's tarfile also reads); the files of a form are
// read only after the checks that do not need their content, so a file
// over the size limit is never held in memory; the field order of a form
// (which only matters for the comment listing the file names) follows
// the submission format, since Go's multipart forms do not keep it; no
// local copy of the files is written (submit_local_copy): the blob store
// is durable.
package submission
