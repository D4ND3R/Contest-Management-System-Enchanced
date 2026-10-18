// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package adminweb

// The import of users from a CSV file (R6.1, new; built like the import of
// teams in users_teams.go).
//
// One user per row, "username,password,first_name,last_name[,email]" (a
// first row starting with "username" is a header and is skipped).
// Usernames follow the rule of the database; the password is stored in
// plain text, as the importers of CMS do (the admin can change the method
// per user later). A username already in the database keeps its user and
// gets the new values. Optionally every user of the file is added to a
// contest, in its main group. The import is all or nothing: any bad row
// is reported with its line number and nothing is written.

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/web"
)

// userRow is a row of a CSV import of users.
type userRow struct {
	Line                                   int
	Username, Password, First, Last, Email string
}

// parseUsersCSV reads the users of a CSV file (see the top of this file):
// the rows, or the problems found (each with its line), never both.
func parseUsersCSV(r io.Reader) ([]userRow, []string) {
	cr := csv.NewReader(skipBOM(r))
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	var rows []userRow
	var problems []string
	seen := map[string]int{}
	report := func(line int, format string, args ...any) {
		problems = append(problems, fmt.Sprintf("line %d: ", line)+fmt.Sprintf(format, args...))
	}
	for first := true; len(problems) < teamsImportMaxErrors; first = false {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			var pe *csv.ParseError
			if errors.As(err, &pe) {
				report(pe.Line, "%v", pe.Err)
				break
			}
			problems = append(problems, err.Error())
			break
		}
		// FieldPos panics after an error: only on a record read.
		line, _ := cr.FieldPos(0)
		if len(rec) == 1 && strings.TrimSpace(rec[0]) == "" {
			continue
		}
		for i := range rec {
			rec[i] = strings.TrimSpace(rec[i])
		}
		if first && strings.EqualFold(rec[0], "username") {
			continue
		}
		if len(rec) != 4 && len(rec) != 5 {
			report(line, "expected 4 or 5 columns (username,password,first_name,last_name[,email]), found %d", len(rec))
			continue
		}
		u := userRow{Line: line, Username: rec[0], Password: rec[1], First: rec[2], Last: rec[3]}
		if len(rec) == 5 {
			u.Email = rec[4]
		}
		if !codeRe.MatchString(u.Username) {
			report(line, "invalid username %q (letters, digits, _ and - only)", u.Username)
			continue
		}
		if u.Password == "" {
			report(line, "empty password for %q", u.Username)
			continue
		}
		if prev, ok := seen[u.Username]; ok {
			report(line, "username %q repeated (line %d)", u.Username, prev)
			continue
		}
		seen[u.Username] = line
		if len(rows) >= teamsImportMaxRows {
			report(line, "too many users (at most %d)", teamsImportMaxRows)
			break
		}
		rows = append(rows, u)
	}
	if len(problems) > 0 {
		return nil, problems
	}
	return rows, nil
}

// importUsersPost is POST /users/import (new, R6.1): creates or updates
// the users of a CSV file and optionally adds them to a contest, all or
// nothing.
func (s *Server) importUsersPost(c *Ctx) {
	fallback := c.URL("users")
	var contestID int32
	if v, _ := c.Arg("contest_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 32)
		if err != nil {
			c.BadRequest()
			return
		}
		contestID = int32(id)
	}
	fh := c.File("users_file")
	if fh == nil {
		c.NotifyError(N("Invalid field(s)"), "No file specified.")
		c.Redirect(fallback)
		return
	}
	f, err := OpenUpload(fh)
	if err != nil {
		c.Fail(err)
		return
	}
	rows, problems := parseUsersCSV(f)
	_ = f.Close()
	if len(problems) > 0 {
		c.NotifyError(N("Invalid field(s)"), strings.Join(problems, "; "))
		c.Redirect(fallback)
		return
	}
	if len(rows) == 0 {
		c.NotifyError(N("Invalid field(s)"), "The file has no users.")
		c.Redirect(fallback)
		return
	}
	p := q.AdminImportUsersParams{
		Usernames:  make([]string, len(rows)),
		Passwords:  make([]string, len(rows)),
		FirstNames: make([]string, len(rows)),
		LastNames:  make([]string, len(rows)),
		Emails:     make([]string, len(rows)),
	}
	for i, r := range rows {
		pw, err := web.HashPassword(r.Password, "plaintext")
		if err != nil {
			c.Fail(err)
			return
		}
		p.Usernames[i], p.Passwords[i], p.FirstNames[i], p.LastNames[i], p.Emails[i] = r.Username, pw, r.First, r.Last, r.Email
	}
	var created, updated int
	var added int64
	err = c.Write(func(tx *Tx) error {
		res, err := tx.Q.AdminImportUsers(tx.Ctx, p)
		if err != nil {
			return err
		}
		created, updated, added = 0, 0, 0
		ids := make([]int32, len(res))
		for i, r := range res {
			ids[i] = r.ID
			if r.Created {
				created++
			} else {
				updated++
			}
		}
		details := map[string]any{"file": fh.Filename, "created": created, "updated": updated, "usernames": p.Usernames}
		if contestID != 0 {
			added, err = tx.Q.AdminAddMainGroupParticipations(tx.Ctx, q.AdminAddMainGroupParticipationsParams{UserIds: ids, ContestID: contestID})
			if err != nil {
				return err
			}
			details["contest_id"] = contestID
			details["participations"] = added
		}
		return tx.Audit(AuditEntry{Action: "user.import", TargetType: "user", Details: details})
	})
	if err != nil {
		c.Commit(err)
		c.Redirect(fallback)
		return
	}
	c.Notify(N("Users imported"), N("%(created)s user(s) created, %(updated)s updated, %(added)s added to the contest."),
		map[string]any{"created": created, "updated": updated, "added": added})
	c.Redirect(fallback)
}
