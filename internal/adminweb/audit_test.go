// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package adminweb

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// testCtx returns a Ctx of the site for admin, as a handler gets it.
func testCtx(t *testing.T, ts *testSite, admin string) *Ctx {
	t.Helper()
	r := httptest.NewRequest("POST", "/x", nil)
	r.RemoteAddr = "192.0.2.7:1234"
	c, ok := ts.S.newCtx(httptest.NewRecorder(), r, Route{Perm: PermAll})
	if !ok {
		t.Fatal("newCtx failed")
	}
	a, err := ts.S.st.Q.GetAdminByUsername(t.Context(), admin)
	if err != nil {
		t.Fatal(err)
	}
	c.Admin = &Admin{Admin: a}
	return c
}

// TestWriteIsAudited checks that a change and its audit entry commit or
// roll back together, that a change without an entry is refused, and
// that live events go out only after the commit.
func TestWriteIsAudited(t *testing.T) {
	ts := newTestSite(t, testOptions{})
	c := testCtx(t, ts, "admin")
	ctx := t.Context()
	team := func(code string) int64 {
		return ts.QueryInt(`SELECT count(*) FROM teams WHERE code = $1`, code)
	}

	// No entry: refused and rolled back.
	err := c.Write(func(tx *Tx) error {
		_, err := tx.Q.CreateTeam(tx.Ctx, q.CreateTeamParams{Code: "t1", Name: "One"})
		tx.Publish(live.ContestChange{Contest: 1}.Event())
		return err
	})
	if !errors.Is(err, errNoAudit) || team("t1") != 0 || len(ts.Events.Events()) != 0 {
		t.Fatalf("write without audit: %v, team %d, events %d", err, team("t1"), len(ts.Events.Events()))
	}

	// The handler fails after the entry: nothing is kept.
	before := ts.AuditCount()
	boom := errors.New("boom")
	err = c.Write(func(tx *Tx) error {
		row, err := tx.Q.CreateTeam(tx.Ctx, q.CreateTeamParams{Code: "t2", Name: "Two"})
		if err != nil {
			return err
		}
		if err := tx.Audit(AuditEntry{Action: "team.create", TargetType: "team", TargetID: int64(row.ID), After: row}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) || team("t2") != 0 || ts.AuditCount() != before {
		t.Fatalf("failed write: %v, team %d, audit %d→%d", err, team("t2"), before, ts.AuditCount())
	}

	// A database error rolls back the entry too.
	err = c.Write(func(tx *Tx) error {
		if err := tx.Audit(AuditEntry{Action: "team.create", TargetType: "team"}); err != nil {
			return err
		}
		_, err := tx.Q.CreateTeam(tx.Ctx, q.CreateTeamParams{Code: "bad code!", Name: "x"})
		return err
	})
	if err == nil || ts.AuditCount() != before {
		t.Fatalf("constraint violation: %v, audit %d→%d", err, before, ts.AuditCount())
	}

	// Success: both, then the events.
	var id int32
	err = c.Write(func(tx *Tx) error {
		row, err := tx.Q.CreateTeam(tx.Ctx, q.CreateTeamParams{Code: "t3", Name: "Three"})
		if err != nil {
			return err
		}
		id = row.ID
		tx.Publish(live.ContestChange{Contest: int64(ts.Contest)}.Event())
		return tx.Audit(AuditEntry{Action: "team.create", TargetType: "team", TargetID: int64(row.ID),
			ContestID: ts.Contest, After: row, Details: map[string]any{"source": "test", "token": "s3cret"}})
	})
	if err != nil || team("t3") != 1 || ts.AuditCount() != before+1 || len(ts.Events.Events()) != 1 {
		t.Fatalf("write: %v, team %d, audit %d, events %d", err, team("t3"), ts.AuditCount(), len(ts.Events.Events()))
	}
	e := ts.LastAudit()
	created, _ := e.Details["created"].(map[string]any)
	if e.Action != "team.create" || e.TargetType != "team" || e.TargetID == nil || *e.TargetID != int64(id) ||
		e.ContestID == nil || *e.ContestID != ts.Contest || e.AdminID == nil || *e.AdminID != ts.Admins["admin"] ||
		e.Details["admin"] != "admin" || created["code"] != "t3" || e.Details["source"] != "test" ||
		e.Details["token"] != redacted {
		t.Fatalf("entry: %+v", e)
	}
	ip := ts.QueryInt(`SELECT count(*) FROM audit_log WHERE ip = '192.0.2.7'`)
	if ip != 1 {
		t.Fatalf("address not recorded")
	}

	// An update keeps the changed fields only; secrets are redacted.
	err = c.Write(func(tx *Tx) error {
		old, err := tx.Q.GetAdmin(ctx, ts.Admins["ro"])
		if err != nil {
			return err
		}
		upd := old
		upd.Name, upd.Authentication = "Renamed", "plaintext:other"
		return tx.Audit(AuditEntry{Action: "admin.update", TargetType: "admin", Before: old, After: upd})
	})
	if err != nil {
		t.Fatal(err)
	}
	ch, _ := ts.LastAudit().Details["changes"].(map[string]any)
	if len(ch) != 2 || ch["name"] == nil || ch["authentication"].([]any)[1] != redacted {
		t.Fatalf("changes: %v", ch)
	}
}

// TestAuditPage checks the audit page: entries of the server page and of
// the admins, filters and keyset pagination.
func TestAuditPage(t *testing.T) {
	ts := newTestSite(t, testOptions{})
	c := ts.Client()
	c.Login("admin")
	if r := c.Post("/server", url.Values{"timezone": {"Europe/Rome"}, "public_url_contest": {":8888"}}); r.Status != http.StatusFound {
		t.Fatalf("server: %d", r.Status)
	}
	e := ts.LastAudit()
	ch, _ := e.Details["changes"].(map[string]any)
	if e.Action != "server_settings.update" || ch["timezone"] == nil || ch["public_url.contest"] == nil {
		t.Fatalf("server entry: %+v", e)
	}
	for i := range 60 {
		ts.Exec(`INSERT INTO audit_log (at, admin_id, action, target_type, target_id, contest_id)
			VALUES ($4::timestamptz - make_interval(secs => $1), $2, 'task.update', 'task', $1, $3)`,
			i+1, ts.Admins["prep"], ts.Contest, demoNow)
	}
	r := c.Get("/audit")
	if r.Status != 200 || !strings.Contains(r.Body, "server_settings.update") || !strings.Contains(r.Body, "Europe/Rome") ||
		!strings.Contains(r.Body, "Older entries") || strings.Contains(r.Body, "Newest entries") {
		t.Fatalf("audit page: %d", r.Status)
	}
	if n := strings.Count(r.Body, "<td>task.update</td>"); n != auditPageSize-1 {
		t.Fatalf("first page: %d task entries", n)
	}
	older := regexp1(r.Body, `href="(audit\?before=[^"]+)"`)
	r = c.Get("/" + strings.ReplaceAll(older, "&amp;", "&"))
	if n := strings.Count(r.Body, "<td>task.update</td>"); n != 11 || !strings.Contains(r.Body, "Newest entries") ||
		strings.Contains(r.Body, "Older entries") {
		t.Fatalf("second page: %d task entries", n)
	}
	r = c.Get("/audit?admin_id=" + itoa(ts.Admins["admin"]))
	if strings.Contains(r.Body, "task.update") || !strings.Contains(r.Body, "server_settings.update") {
		t.Fatal("admin filter")
	}
	r = c.Get("/contest/" + itoa(ts.Contest) + "/audit?target_type=task&target_id=7")
	if n := strings.Count(r.Body, "<td>task.update</td>"); n != 1 || !strings.Contains(r.Body, `<h1 class="child">demo</h1>`) {
		t.Fatalf("contest page with a target: %d", n)
	}
}

func TestSnakeCase(t *testing.T) {
	for in, want := range map[string]string{"TimeLimit": "time_limit", "ID": "id", "ContestID": "contest_id",
		"PermissionAll": "permission_all", "Ip": "ip", "already_snake": "already_snake", "HTMLBody": "html_body",
		"Score2Details": "score2_details"} {
		if got := snakeCase(in); got != want {
			t.Errorf("%s: %s, want %s", in, got, want)
		}
	}
}
