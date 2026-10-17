// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/server/admin/handlers/contestranking.py and
// cms/server/admin/templates/ranking.txt of CMS:
// Copyright © 2010-2013 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2012-2014 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2014 Artem Iglikov <artem.iglikov@gmail.com>
// Copyright © 2014 Fabian Gundlach <320pointsguy@gmail.com>
// Copyright © 2015 William Di Luigi <williamdiluigi@gmail.com>

package adminweb

// The ranking of a contest (work A, R6.4): CMS RankingHandler online, as
// text (ranking.txt) and as CSV, read from the task_scores cache (D34:
// one query, no score computed per request), plus the secret link to the
// public scoreboard.
//
// # The secret scoreboard link
//
// The ranking page links to the public scoreboard with a secret that
// lets anyone holding the link see the scoreboard of the contest even
// when it is restricted (R7.4):
//
//	<public address of the scoreboard>?secret=<config.RankingLinkToken(contest name)>
//
// The token is derived from secret_key (internal/config/rankinglink.go),
// so every service computes the same one without storing it (D5); the
// public scoreboard (F7) verifies it with the same function.

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/scoretype"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/web"
)

// ScoreboardLink returns the secret link of a contest given the public
// address of the scoreboard (with its trailing slash); "" when the
// address is unknown.
func ScoreboardLink(cfg *config.Config, rankingURL, contestName string) string {
	if rankingURL == "" {
		return ""
	}
	return rankingURL + "?secret=" + url.QueryEscape(cfg.RankingLinkToken(contestName))
}

// rankingCell is a score of the ranking: Value is the number, Text how
// CMS prints it (str(float)), Partial whether some submission is not
// scored yet.
type rankingCell struct {
	Value   float64
	Text    string
	Partial bool
}

// rankingRow is a participation of the ranking.
type rankingRow struct {
	q.AdminRankingRow
	Scores []rankingCell
	Total  rankingCell
}

// User is "<first name> <last name>".
func (r rankingRow) User() string { return r.FirstName + " " + r.LastName }

// Team is the name of the team ("" without one).
func (r rankingRow) Team() string { return deref(r.TeamName) }

// rankingData is the data of ranking.html.
type rankingData struct {
	*Page
	Tasks     []q.AdminContestTasksRow
	Rows      []rankingRow
	ShowTeams bool
	// SecretLink is the secret link to the scoreboard ("" when the public
	// address of the scoreboard is not configured).
	SecretLink string
}

// rankingFloat is str(x) of a Python float.
func rankingFloat(x float64) string {
	s, _ := web.PyFormat("%s", x)
	return s
}

// loadRanking reads the ranking of the contest of the page (CMS
// RankingHandler's preprocessing: the task scores and the total rounded
// to the score precision of the contest).
func (s *Server) loadRanking(c *Ctx) ([]q.AdminContestTasksRow, []rankingRow, bool, error) {
	tasks, err := s.st.Q.AdminContestTasks(c.Context(), &c.Contest.ID)
	if err != nil {
		return nil, nil, false, err
	}
	rows, err := s.st.Q.AdminRanking(c.Context(), c.Contest.ID)
	if err != nil {
		return nil, nil, false, err
	}
	out := make([]rankingRow, len(rows))
	showTeams := false
	for i, r := range rows {
		showTeams = showTeams || r.TeamID != nil
		row := rankingRow{AdminRankingRow: r, Scores: make([]rankingCell, len(tasks))}
		total, partial := 0.0, false
		for j := range tasks {
			var cell rankingCell
			if j < len(r.Scores) {
				cell.Value = r.Scores[j]
			}
			if j < len(r.Partials) {
				cell.Partial = r.Partials[j]
			}
			cell.Text = rankingFloat(cell.Value)
			row.Scores[j] = cell
			total += cell.Value
			partial = partial || cell.Partial
		}
		total = scoretype.Round(total, int(c.Contest.ScorePrecision))
		row.Total = rankingCell{Value: total, Text: rankingFloat(total), Partial: partial}
		out[i] = row
	}
	return tasks, out, showTeams, nil
}

// rankingLess orders by CMS's sort key (total_score, partial).
func rankingLess(a, b rankingRow) int {
	switch {
	case a.Total.Value < b.Total.Value:
		return -1
	case a.Total.Value > b.Total.Value:
		return 1
	case a.Total.Partial == b.Total.Partial:
		return 0
	case !a.Total.Partial:
		return -1
	}
	return 1
}

// rankingPage is GET /contest/<id>/ranking[/<format>] (CMS
// RankingHandler): "txt" and "csv" are downloads, anything else the page.
func (s *Server) rankingPage(c *Ctx) {
	if !c.ContestFromPath() {
		return
	}
	tasks, rows, showTeams, err := s.loadRanking(c)
	if err != nil {
		c.Fail(err)
		return
	}
	switch c.R.PathValue("format") {
	case "txt":
		body := rankingText(c.Contest.ScorePrecision, tasks, rows, showTeams)
		c.download("text/plain", "ranking.txt", body)
		return
	case "csv":
		body := rankingCSV(tasks, rows, showTeams)
		c.download("text/csv", "ranking.csv", body)
		return
	}
	p, err := c.Page()
	if err != nil {
		c.Fail(err)
		return
	}
	c.Render("ranking.html", rankingData{Page: p, Tasks: tasks, Rows: rows, ShowTeams: showTeams,
		SecretLink: ScoreboardLink(s.cfg, s.PublicURL(c.R, SiteRanking), c.Contest.Name)})
}

// download sends a file to save.
func (c *Ctx) download(ctype, name string, body []byte) {
	h := c.W.Header()
	h.Set("Content-Type", ctype)
	h.Set("Content-Disposition", `attachment; filename="`+name+`"`)
	h.Set("Cache-Control", "no-cache, must-revalidate")
	c.W.WriteHeader(http.StatusOK)
	_, _ = c.W.Write(body)
}

// rankingText is CMS's ranking.txt: fixed-width columns, sorted by
// total score (Jinja's sort, then reverse: equal totals come out in the
// opposite order of the page).
func rankingText(precision int32, tasks []q.AdminContestTasksRow, rows []rankingRow, showTeams bool) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "%20s %30s", "Username", "User")
	if showTeams {
		fmt.Fprintf(&b, "%30s", "Team")
	}
	b.WriteByte(' ')
	for _, t := range tasks {
		fmt.Fprintf(&b, "%14s ", t.Name)
	}
	fmt.Fprintf(&b, "%8s\n", "Global")
	sorted := slices.Clone(rows)
	slices.SortStableFunc(sorted, rankingLess)
	slices.Reverse(sorted)
	star := func(p bool) byte {
		if p {
			return '*'
		}
		return ' '
	}
	for _, r := range sorted {
		if r.Hidden {
			continue
		}
		fmt.Fprintf(&b, "%20s %30s", r.Username, r.User())
		if showTeams {
			fmt.Fprintf(&b, "%30s", r.Team())
		}
		b.WriteByte(' ')
		for i, t := range tasks {
			fmt.Fprintf(&b, "%13.*f%c ", int(t.ScorePrecision), r.Scores[i].Value, star(r.Scores[i].Partial))
		}
		fmt.Fprintf(&b, "%7.*f%c\n", int(precision), r.Total.Value, star(r.Total.Partial))
	}
	return b.Bytes()
}

// rankingCSV is CMS's CSV ranking: sorted by total score, descending
// (Python's stable sorted(reverse=True)), with a "P" column after each
// score marking the partial ones.
func rankingCSV(tasks []q.AdminContestTasksRow, rows []rankingRow, showTeams bool) []byte {
	var b bytes.Buffer
	head := []string{"Username", "User"}
	if showTeams {
		head = append(head, "Team")
	}
	for _, t := range tasks {
		head = append(head, t.Name, "P")
	}
	head = append(head, "Global", "P")
	writeCSVRow(&b, head)
	sorted := slices.Clone(rows)
	slices.SortStableFunc(sorted, func(a, b rankingRow) int { return -rankingLess(a, b) })
	mark := func(p bool) string {
		if p {
			return "*"
		}
		return ""
	}
	for _, r := range sorted {
		if r.Hidden {
			continue
		}
		rec := []string{r.Username, r.User()}
		if showTeams {
			rec = append(rec, r.Team())
		}
		for _, s := range r.Scores {
			rec = append(rec, s.Text, mark(s.Partial))
		}
		rec = append(rec, r.Total.Text, mark(r.Total.Partial))
		writeCSVRow(&b, rec)
	}
	return b.Bytes()
}

// writeCSVRow writes a row as Python's csv.writer (excel dialect,
// QUOTE_MINIMAL): fields with the delimiter, a quote or a line break are
// quoted (quotes doubled); rows end with "\r\n".
func writeCSVRow(b *bytes.Buffer, rec []string) {
	for i, f := range rec {
		if i > 0 {
			b.WriteByte(',')
		}
		if strings.ContainsAny(f, ",\"\r\n") {
			b.WriteByte('"')
			b.WriteString(strings.ReplaceAll(f, `"`, `""`))
			b.WriteByte('"')
		} else {
			b.WriteString(f)
		}
	}
	b.WriteString("\r\n")
}
