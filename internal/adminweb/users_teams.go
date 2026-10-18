// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from TeamListHandler, RemoveTeamHandler, TeamHandler and
// AddTeamHandler in cms/server/admin/handlers/user.py of CMS:
// Copyright © 2010-2015 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2012-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2026 Tobias Lenz <t_lenz94@web.de>
// Copyright © 2026 Chuyang Wang <mail@chuyang-wang.de>

package adminweb

// Teams (work A): the list, creation, edition, removal, and the import of
// teams from a CSV file (R6.1, new).
//
// The CSV import: one team per row, "code,name" (a first row "code,name"
// is a header and is skipped; extra columns are an error). Codes follow
// the rule of the database (letters, digits, "_" and "-"); an empty name
// takes the code. A code already in the database keeps its team and gets
// the new name. The import is all or nothing: any bad row (bad code,
// repeated code, wrong number of columns) is reported with its line
// number and nothing is written.

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// teamsData is the data of teams.html.
type teamsData struct {
	*Page
	Teams []q.Team
	Pager *Pager
}

// teamsPage is GET /teams (CMS TeamListHandler).
func (s *Server) teamsPage(c *Ctx) {
	n, err := s.st.Q.AdminTeamsCount(c.Context())
	if err != nil {
		c.Fail(err)
		return
	}
	pg := c.Pager(n, peoplePageSize)
	rows, err := s.st.Q.AdminTeamsPage(c.Context(), q.AdminTeamsPageParams{Lim: int32(pg.Size), Off: int32(pg.Offset())})
	if err != nil {
		c.Fail(err)
		return
	}
	p, err := c.Page()
	if err != nil {
		c.Fail(err)
		return
	}
	c.Render("teams.html", teamsData{Page: p, Teams: rows, Pager: pg})
}

// teamsPost is POST /teams: the Remove button opens the confirmation.
func (s *Server) teamsPost(c *Ctx) {
	id, _ := c.Arg("team_id")
	op, _ := c.Arg("operation")
	if c.isOp(op, "Remove") {
		if _, err := strconv.ParseInt(id, 10, 32); err != nil {
			c.BadRequest()
			return
		}
		c.Redirect(c.URL("teams", id, "remove"))
		return
	}
	c.NotifyError(N("Invalid operation"), op)
	c.Redirect(c.URL("contests"))
}

// removeTeamData is the data of team_remove.html.
type removeTeamData struct {
	*Page
	Team           q.Team
	Participations int64
}

// removeTeamPage is GET /teams/<id>/remove (CMS RemoveTeamHandler).
func (s *Server) removeTeamPage(c *Ctx) {
	id, _ := c.PathID("team_id")
	t, err := s.st.Q.GetTeam(c.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		c.NotFound()
		return
	}
	if err != nil {
		c.Fail(err)
		return
	}
	n, err := s.st.Q.AdminTeamParticipationCount(c.Context(), &id)
	if err != nil {
		c.Fail(err)
		return
	}
	p, err := c.Page()
	if err != nil {
		c.Fail(err)
		return
	}
	c.Render("team_remove.html", removeTeamData{Page: p, Team: t, Participations: n})
}

// removeTeamDelete is DELETE /teams/<id>/remove: the participations of
// the team stay, without a team.
func (s *Server) removeTeamDelete(c *Ctx) {
	id, _ := c.PathID("team_id")
	err := c.Write(func(tx *Tx) error {
		old, err := tx.Q.GetTeam(tx.Ctx, id)
		if err != nil {
			return err
		}
		n, err := tx.Q.AdminDetachTeam(tx.Ctx, &id)
		if err != nil {
			return err
		}
		if _, err := tx.Q.DeleteTeam(tx.Ctx, id); err != nil {
			return err
		}
		return tx.Audit(AuditEntry{Action: "team.delete", TargetType: "team", TargetID: int64(id), Before: old,
			Details: map[string]any{"participations": n}})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		c.NotFound()
		return
	}
	c.Commit(err)
	c.WriteText("../../teams")
}

// addTeamPage is GET /teams/add.
func (s *Server) addTeamPage(c *Ctx) {
	p, err := c.Page()
	if err != nil {
		c.Fail(err)
		return
	}
	c.Render("add_team.html", p)
}

// teamAttrs reads the form of a team over t.
func (c *Ctx) teamAttrs(t *q.Team) error {
	f := c.Form()
	f.String(&t.Code, "code")
	f.String(&t.Name, "name")
	if t.Code == "" {
		f.Fail("No team code specified.")
	}
	return f.Err()
}

// addTeamPost is POST /teams/add (CMS AddTeamHandler): back to the form
// for the next team.
func (s *Server) addTeamPost(c *Ctx) {
	fallback := c.URL("teams", "add")
	var t q.Team
	if err := c.teamAttrs(&t); err != nil {
		c.NotifyError(N("Invalid field(s)"), err.Error())
		c.Redirect(fallback)
		return
	}
	err := c.Write(func(tx *Tx) error {
		created, err := tx.Q.CreateTeam(tx.Ctx, q.CreateTeamParams{Code: t.Code, Name: t.Name})
		if err != nil {
			return err
		}
		return tx.Audit(AuditEntry{Action: "team.create", TargetType: "team", TargetID: int64(created.ID), After: created})
	})
	c.Commit(err)
	c.Redirect(fallback)
}

// teamData is the data of team.html.
type teamData struct {
	*Page
	Team q.Team
}

// teamPage is GET /team/<id> (CMS TeamHandler.get).
func (s *Server) teamPage(c *Ctx) {
	id, _ := c.PathID("team_id")
	t, err := s.st.Q.GetTeam(c.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		c.NotFound()
		return
	}
	if err != nil {
		c.Fail(err)
		return
	}
	p, err := c.Page()
	if err != nil {
		c.Fail(err)
		return
	}
	c.Render("team.html", teamData{Page: p, Team: t})
}

// teamPost is POST /team/<id> (CMS TeamHandler.post).
func (s *Server) teamPost(c *Ctx) {
	id, _ := c.PathID("team_id")
	page := c.URL("team", id)
	err := c.Write(func(tx *Tx) error {
		old, err := tx.Q.GetTeam(tx.Ctx, id)
		if err != nil {
			return err
		}
		t := old
		if err := c.teamAttrs(&t); err != nil {
			return &FieldError{Msg: err.Error()}
		}
		upd, err := tx.Q.UpdateTeam(tx.Ctx, q.UpdateTeamParams{ID: id, Code: t.Code, Name: t.Name})
		if err != nil {
			return err
		}
		return tx.Audit(AuditEntry{Action: "team.update", TargetType: "team", TargetID: int64(id), Before: old, After: upd})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		c.NotFound()
		return
	}
	var fe *FieldError
	if errors.As(err, &fe) {
		c.NotifyError(N("Invalid field(s)"), fe.Msg)
		c.Redirect(page)
		return
	}
	c.Commit(err)
	c.Redirect(page)
}

// Bounds of a CSV import of teams.
const (
	teamsImportMaxRows   = 10000
	teamsImportMaxErrors = 20
)

// codeRe is the rule of team codes (teams_code_check).
var codeRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// teamRow is a row of a CSV import.
type teamRow struct {
	Line       int
	Code, Name string
}

// parseTeamsCSV reads the teams of a CSV file (see the top of this file):
// the rows, or the problems found (each with its line), never both.
func parseTeamsCSV(r io.Reader) ([]teamRow, []string) {
	cr := csv.NewReader(skipBOM(r))
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	var rows []teamRow
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
		if first && len(rec) == 2 && strings.EqualFold(rec[0], "code") && strings.EqualFold(rec[1], "name") {
			continue
		}
		if len(rec) != 2 {
			report(line, "expected 2 columns (code,name), found %d", len(rec))
			continue
		}
		code, name := rec[0], rec[1]
		if !codeRe.MatchString(code) {
			report(line, "invalid team code %q (letters, digits, _ and - only)", code)
			continue
		}
		if prev, ok := seen[code]; ok {
			report(line, "team code %q repeated (line %d)", code, prev)
			continue
		}
		seen[code] = line
		if name == "" {
			name = code
		}
		if len(rows) >= teamsImportMaxRows {
			report(line, "too many teams (at most %d)", teamsImportMaxRows)
			break
		}
		rows = append(rows, teamRow{Line: line, Code: code, Name: name})
	}
	if len(problems) > 0 {
		return nil, problems
	}
	return rows, nil
}

// skipBOM drops the UTF-8 byte order mark that spreadsheets write at the
// start of a "CSV UTF-8" file (Excel does), which would otherwise be part
// of the first field.
func skipBOM(r io.Reader) io.Reader {
	br := bufio.NewReader(r)
	if b, err := br.Peek(3); err == nil && bytes.Equal(b, []byte("\xef\xbb\xbf")) {
		_, _ = br.Discard(3)
	}
	return br
}

// importTeamsPost is POST /teams/import (new, R6.1): creates or renames
// the teams of a CSV file, all or nothing.
func (s *Server) importTeamsPost(c *Ctx) {
	fallback := c.URL("teams")
	fh := c.File("teams_file")
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
	rows, problems := parseTeamsCSV(f)
	_ = f.Close()
	if len(problems) > 0 {
		c.NotifyError(N("Invalid field(s)"), strings.Join(problems, "; "))
		c.Redirect(fallback)
		return
	}
	if len(rows) == 0 {
		c.NotifyError(N("Invalid field(s)"), "The file has no teams.")
		c.Redirect(fallback)
		return
	}
	codes := make([]string, len(rows))
	names := make([]string, len(rows))
	for i, r := range rows {
		codes[i], names[i] = r.Code, r.Name
	}
	var created, updated int
	err = c.Write(func(tx *Tx) error {
		res, err := tx.Q.AdminImportTeams(tx.Ctx, q.AdminImportTeamsParams{Codes: codes, Names: names})
		if err != nil {
			return err
		}
		created, updated = 0, 0
		for _, r := range res {
			if r.Created {
				created++
			} else {
				updated++
			}
		}
		return tx.Audit(AuditEntry{Action: "team.import", TargetType: "team",
			Details: map[string]any{"file": fh.Filename, "created": created, "updated": updated, "codes": codes}})
	})
	if err != nil {
		c.Commit(err)
		c.Redirect(fallback)
		return
	}
	c.Notify(N("Teams imported"), N("%(created)s team(s) created, %(updated)s updated."),
		map[string]any{"created": created, "updated": updated})
	c.Redirect(fallback)
}
