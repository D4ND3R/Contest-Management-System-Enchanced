// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package adminweb

// The audit page (R6.6; new): the changes made through the
// administration, newest first, 50 per page (keyset pagination: "Older
// entries" continues after the last row), filtered by contest, admin and
// target (type and id) with the arguments contest_id, admin_id,
// target_type and target_id. /contest/<id>/audit is the page of one
// contest, with the contest menu.

import (
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// auditPageSize is the number of entries per page.
const auditPageSize = 50

// auditRow is an entry of the audit page.
type auditRow struct {
	ID         int64
	At         time.Time
	Admin      string
	Action     string
	TargetType string
	// TargetID and ContestID are 0 when not set.
	TargetID  int64
	Contest   string
	ContestID int32
	IP        string
	// Details are the details, one line per change or value.
	Details string
}

// auditData is the data of audit.html.
type auditData struct {
	*Page
	Rows []auditRow
	// The filters (zero: none) and the choices of the selects.
	ContestID, AdminID int32
	TargetType         string
	TargetID           string
	Contests           []q.AdminCoreContestNamesRow
	Admins             []q.Admin
	// OlderURL continues after the last row ("" on the last page);
	// NewestURL goes back to the first page ("" on it).
	OlderURL, NewestURL string
}

// auditPage is GET /audit and /contest/<id>/audit.
func (s *Server) auditPage(c *Ctx) {
	contestPage := c.R.PathValue("contest_id") != ""
	if contestPage && !c.ContestFromPath() {
		return
	}
	params := q.AdminCoreAuditLogParams{PageSize: auditPageSize + 1}
	d := auditData{}
	query := url.Values{}
	if c.Contest != nil {
		d.ContestID = c.Contest.ID
	} else if v, err := strconv.ParseInt(c.ArgDefault("contest_id", ""), 10, 32); err == nil && v > 0 {
		d.ContestID = int32(v)
		query.Set("contest_id", strconv.Itoa(int(v)))
	}
	if d.ContestID != 0 {
		params.ContestID = &d.ContestID
	}
	if v, err := strconv.ParseInt(c.ArgDefault("admin_id", ""), 10, 32); err == nil && v > 0 {
		d.AdminID = int32(v)
		params.AdminID = &d.AdminID
		query.Set("admin_id", strconv.Itoa(int(v)))
	}
	if v := c.ArgDefault("target_type", ""); v != "" {
		d.TargetType = v
		params.TargetType = &d.TargetType
		query.Set("target_type", v)
	}
	if v := c.ArgDefault("target_id", ""); v != "" {
		d.TargetID = v
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			params.TargetID = &n
			query.Set("target_id", v)
		}
	}
	before := c.ArgDefault("before", "")
	if at, id, ok := parseAuditCursor(before); ok {
		params.BeforeAt, params.BeforeID = &at, &id
	}
	rows, err := s.st.Q.AdminCoreAuditLog(c.Context(), params)
	if err != nil {
		c.Fail(err)
		return
	}
	more := len(rows) > auditPageSize
	if more {
		rows = rows[:auditPageSize]
	}
	for _, r := range rows {
		al := r.AuditLog
		row := auditRow{ID: al.ID, At: al.At, Action: al.Action, TargetType: al.TargetType,
			TargetID: deref(al.TargetID), ContestID: deref(al.ContestID),
			Contest: deref(r.ContestName), IP: auditIP(al.Ip)}
		var details map[string]any
		_ = json.Unmarshal(al.Details, &details)
		row.Admin = deref(r.AdminUsername)
		if row.Admin == "" {
			if name, ok := details["admin"].(string); ok {
				row.Admin = name
			}
		}
		delete(details, "admin")
		row.Details = auditSummary(details)
		d.Rows = append(d.Rows, row)
	}
	self := "audit"
	if more {
		last := rows[len(rows)-1].AuditLog
		oq := url.Values{}
		for k, v := range query {
			oq[k] = v
		}
		oq.Set("before", formatAuditCursor(last.At, last.ID))
		d.OlderURL = self + "?" + oq.Encode()
	}
	if before != "" {
		d.NewestURL = self
		if len(query) > 0 {
			d.NewestURL += "?" + query.Encode()
		}
	}
	if d.Contests, err = s.st.Q.AdminCoreContestNames(c.Context()); err != nil {
		c.Fail(err)
		return
	}
	if d.Admins, err = s.st.Q.AdminCoreAdmins(c.Context()); err != nil {
		c.Fail(err)
		return
	}
	if d.Page, err = c.Page(); err != nil {
		c.Fail(err)
		return
	}
	c.Render("audit.html", d)
}

// formatAuditCursor and parseAuditCursor encode the position after a row:
// "<unix microseconds>_<id>".
func formatAuditCursor(at time.Time, id int64) string {
	return strconv.FormatInt(at.UnixMicro(), 10) + "_" + strconv.FormatInt(id, 10)
}

func parseAuditCursor(s string) (time.Time, int64, bool) {
	a, b, ok := strings.Cut(s, "_")
	if !ok {
		return time.Time{}, 0, false
	}
	us, err1 := strconv.ParseInt(a, 10, 64)
	id, err2 := strconv.ParseInt(b, 10, 64)
	if err1 != nil || err2 != nil {
		return time.Time{}, 0, false
	}
	return time.UnixMicro(us).UTC(), id, true
}

// auditSummary shows the details of an entry: one line per changed field
// ("name: "a" → "b""), then the created or deleted object and the other
// details as compact JSON; "" when empty.
func auditSummary(d map[string]any) string {
	var lines []string
	compact := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			return "?"
		}
		return string(b)
	}
	if ch, ok := d["changes"].(map[string]any); ok {
		for _, k := range sortedKeys(ch) {
			if pair, ok := ch[k].([]any); ok && len(pair) == 2 {
				lines = append(lines, k+": "+compact(pair[0])+" → "+compact(pair[1]))
			} else {
				lines = append(lines, k+": "+compact(ch[k]))
			}
		}
		delete(d, "changes")
	}
	for _, k := range sortedKeys(d) {
		lines = append(lines, k+": "+compact(d[k]))
	}
	return strings.Join(lines, "\n")
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
