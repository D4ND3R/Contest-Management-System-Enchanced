// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package adminweb

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// notices returns the notifications of the client (the errors of the
// forms are reported there), for failure messages.
func (c *testClient) notices() string {
	return c.Get("/notifications?last_notification=0").Body
}

// queryString returns the single string of a query.
func queryString(t *testing.T, ts *testSite, sql string, args ...any) string {
	t.Helper()
	var s string
	if err := ts.Pool.QueryRow(t.Context(), sql, args...).Scan(&s); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return s
}

// audited fails the test unless the newest audit row is action on the
// target and the number of rows grew by one since before.
func audited(t *testing.T, ts *testSite, before int64, action string, target int64) {
	t.Helper()
	if n := ts.AuditCount(); n != before+1 {
		t.Fatalf("%s: %d audit rows, want %d", action, n, before+1)
	}
	e := ts.LastAudit()
	if e.Action != action || (target != 0 && (e.TargetID == nil || *e.TargetID != target)) {
		t.Fatalf("audit: %s %v, want %s %d", e.Action, e.TargetID, action, target)
	}
	if e.AdminID == nil {
		t.Fatalf("audit %s without its admin", action)
	}
}

// TestContestAndPeopleForms submits the forms of the contest, group,
// user, team and participation pages and checks the rows and the audit
// log.
func TestContestAndPeopleForms(t *testing.T) {
	ts, f := newFixtureSite(t, testOptions{})
	c := ts.Client()
	c.Login("admin")
	cid := itoa(ts.Contest)

	// Contest settings: the form round-trips; a changed description is saved.
	n := ts.AuditCount()
	form := c.mustForm("/contest/"+cid, "contest/"+cid)
	form.Set("description", "Demo, edited")
	if r := c.Post("/contest/"+cid, form); !wantRedirect(r, "contest/"+cid) {
		t.Fatalf("contest: %d %q %s", r.Status, r.Location(), c.notices())
	}
	if ts.QueryInt(`SELECT count(*) FROM contests WHERE id = $1 AND description = 'Demo, edited'`, ts.Contest) != 1 {
		t.Fatalf("the description was not saved: %s", c.notices())
	}
	audited(t, ts, n, "contest.update", int64(ts.Contest))
	// An invalid value changes nothing and is not audited.
	n = ts.AuditCount()
	form.Set("description", "never saved")
	form.Set("max_submission_number", "-3")
	c.Post("/contest/"+cid, form)
	if ts.QueryInt(`SELECT count(*) FROM contests WHERE description = 'never saved'`) != 0 || ts.AuditCount() != n {
		t.Fatal("an invalid contest form was saved")
	}

	// Groups: add, then edit through the group page.
	n = ts.AuditCount()
	if r := c.Post("/contest/"+cid+"/groups/add", url.Values{"name": {"late"}}); r.Status != http.StatusFound {
		t.Fatalf("add group: %d", r.Status)
	}
	gid := ts.QueryInt(`SELECT id FROM groups WHERE contest_id = $1 AND name = 'late'`, ts.Contest)
	audited(t, ts, n, "group.create", gid)
	if c.Post("/contest/"+cid+"/groups/add", url.Values{"name": {""}}); ts.AuditCount() != n+1 {
		t.Fatal("a group without a name was created")
	}
	n = ts.AuditCount()
	gpage := "/contest/" + cid + "/group/" + itoa(gid) + "/edit"
	form = c.mustForm(gpage, "group/"+itoa(gid)+"/edit")
	form.Set("name", "late2")
	form.Set("per_user_time", "3600")
	if r := c.Post(gpage, form); r.Status != http.StatusFound {
		t.Fatalf("edit group: %d", r.Status)
	}
	if ts.QueryInt(`SELECT count(*) FROM groups WHERE id = $1 AND name = 'late2' AND per_user_time = '1 hour'`, gid) != 1 {
		t.Fatalf("the group was not saved: %s", c.notices())
	}
	audited(t, ts, n, "group.update", gid)

	// Users: add, then edit through the user page.
	n = ts.AuditCount()
	r := c.Post("/users/add", url.Values{"first_name": {"Dave"}, "last_name": {"D"}, "username": {"dave"},
		"password": {"pw"}, "method": {"plaintext"}, "email": {""}, "timezone": {""}, "preferred_languages": {""}})
	uid := ts.QueryInt(`SELECT coalesce(max(id), 0) FROM users WHERE username = 'dave'`)
	if uid == 0 || !wantRedirect(r, "user/"+itoa(uid)) {
		t.Fatalf("add user: %d %q %s", r.Status, r.Location(), c.notices())
	}
	audited(t, ts, n, "user.create", uid)
	if c.Post("/users/add", url.Values{"username": {"dave"}, "password": {"x"}}); ts.AuditCount() != n+1 {
		t.Fatal("a duplicated username was accepted")
	}
	n = ts.AuditCount()
	form = c.mustForm("/user/"+itoa(uid), "user/"+itoa(uid))
	form.Set("first_name", "David")
	if r := c.Post("/user/"+itoa(uid), form); r.Status != http.StatusFound {
		t.Fatalf("edit user: %d", r.Status)
	}
	if ts.QueryInt(`SELECT count(*) FROM users WHERE id = $1 AND first_name = 'David' AND username = 'dave'`, uid) != 1 {
		t.Fatalf("the user was not saved: %s", c.notices())
	}
	audited(t, ts, n, "user.update", uid)
	// The round trip kept the password.
	if ts.QueryInt(`SELECT count(*) FROM users WHERE id = $1 AND password = 'plaintext:pw'`, uid) != 1 {
		t.Fatal("editing the user changed the password")
	}

	// Participation: the form round-trips; a team code and extra time are saved.
	pw := queryString(t, ts, `SELECT coalesce(password, '<nil>') FROM participations WHERE id = $1`, f.Part["bob"])
	n = ts.AuditCount()
	ppage := "/contest/" + cid + "/user/" + itoa(f.Users["bob"]) + "/edit"
	form = c.mustForm(ppage, "user/"+itoa(f.Users["bob"])+"/edit")
	form.Set("team", "ita")
	form.Set("extra_time", "120")
	if r := c.Post(ppage, form); r.Status != http.StatusFound {
		t.Fatalf("participation: %d", r.Status)
	}
	if ts.QueryInt(`SELECT count(*) FROM participations WHERE id = $1 AND team_id = $2 AND extra_time = '2 minutes'`,
		f.Part["bob"], f.Team) != 1 {
		t.Fatalf("the participation was not saved: %s", c.notices())
	}
	audited(t, ts, n, "participation.update", int64(f.Part["bob"]))
	form.Set("team", "nope")
	if c.Post(ppage, form); ts.AuditCount() != n+1 {
		t.Fatal("an unknown team was accepted")
	}
	// The password is kept by the round trip.
	if got := queryString(t, ts, `SELECT coalesce(password, '<nil>') FROM participations WHERE id = $1`, f.Part["bob"]); got != pw {
		t.Fatalf("the round trip changed the password: %q, was %q", got, pw)
	}

	// The prep and messaging roles cannot change people or contests.
	for _, name := range []string{"prep", "msg"} {
		o := ts.Client()
		o.Login(name)
		if r := o.Post("/contest/"+cid, form); r.Status != http.StatusForbidden {
			t.Errorf("contest form as %s: %d", name, r.Status)
		}
		if r := o.Post("/users/add", url.Values{"username": {"x" + name}}); r.Status != http.StatusForbidden {
			t.Errorf("add user as %s: %d", name, r.Status)
		}
		if r := o.Post(ppage, form); r.Status != http.StatusForbidden {
			t.Errorf("participation as %s: %d", name, r.Status)
		}
	}
}

// TestTeamsImport checks the CSV import of teams: good files create and
// rename teams, a file with any bad row changes nothing.
func TestTeamsImport(t *testing.T) {
	ts, _ := newFixtureSite(t, testOptions{})
	c := ts.Client()
	c.Login("admin")
	imp := func(data string) *testResponse {
		t.Helper()
		r := c.PostFiles("/teams/import", nil, upload{"teams_file", "teams.csv", []byte(data)})
		if !wantRedirect(r, "teams") {
			t.Fatalf("import: %d %q", r.Status, r.Location())
		}
		return r
	}
	n := ts.AuditCount()
	imp("code,name\nita, Italia \nfra,France\n\nesp,\n")
	if got := ts.QueryInt(`SELECT count(*) FROM teams`); got != 3 {
		t.Fatalf("%d teams, want 3", got)
	}
	if ts.QueryInt(`SELECT count(*) FROM teams WHERE (code, name) IN (('ita', 'Italia'), ('fra', 'France'), ('esp', 'esp'))`) != 3 {
		t.Fatal("the teams were not created or renamed")
	}
	audited(t, ts, n, "team.import", 0)
	if d := ts.LastAudit().Details; d["created"] != float64(2) || d["updated"] != float64(1) {
		t.Fatalf("audit details: %v", d)
	}
	for _, bad := range []string{
		"ger,Germany\nb@d,Bad\n",          // invalid code
		"ger,Germany\nger,Again\n",        // repeated code
		"ger,Germany\nonly-one-column\n",  // wrong number of columns
		"ger,Germany\nx,y,z\n",            // too many columns
		"ger,Germany\n\"unterminated,x\n", // CSV syntax
		"g\"er,Germany\n",                 // bare quote in the first field
		"",                                // no teams
		"code,name\n",                     // only the header
	} {
		n := ts.AuditCount()
		imp(bad)
		if ts.QueryInt(`SELECT count(*) FROM teams WHERE code = 'ger'`) != 0 || ts.AuditCount() != n {
			t.Fatalf("a bad file changed the teams: %q", bad)
		}
		if body := c.notices(); !strings.Contains(body, "Invalid field") {
			t.Fatalf("no error for %q: %s", bad, body)
		}
	}
	// Without a file.
	if r := c.PostFiles("/teams/import", nil); !wantRedirect(r, "teams") {
		t.Fatalf("no file: %d", r.Status)
	}
	o := ts.Client()
	o.Login("prep")
	if r := o.PostFiles("/teams/import", nil, upload{"teams_file", "t.csv", []byte("ger,G\n")}); r.Status != http.StatusForbidden {
		t.Fatalf("import as prep: %d", r.Status)
	}
}

// TestUsersImport checks the CSV import of users: a good file creates and
// updates users and adds them to the contest, a file with any bad row
// changes nothing.
func TestUsersImport(t *testing.T) {
	ts, _ := newFixtureSite(t, testOptions{})
	c := ts.Client()
	c.Login("admin")
	cid := itoa(ts.Contest)
	imp := func(data, contest string) {
		t.Helper()
		r := c.PostFiles("/users/import", url.Values{"contest_id": {contest}}, upload{"users_file", "users.csv", []byte(data)})
		if !wantRedirect(r, "users") {
			t.Fatalf("import: %d %q", r.Status, r.Location())
		}
	}
	n := ts.AuditCount()
	imp("username,password,first_name,last_name,email\nimp1,pw1,Ana,Uno,ana@example.com\nimp2, pw2 ,Bea,Dos\n\n", cid)
	if ts.QueryInt(`SELECT count(*) FROM users WHERE username IN ('imp1', 'imp2')`) != 2 {
		t.Fatal("the users were not created")
	}
	if ts.QueryInt(`SELECT count(*) FROM users WHERE (username, password, first_name, last_name, email) = ('imp1', 'plaintext:pw1', 'Ana', 'Uno', 'ana@example.com')`) != 1 ||
		ts.QueryInt(`SELECT count(*) FROM users WHERE username = 'imp2' AND password = 'plaintext:pw2' AND email IS NULL`) != 1 {
		t.Fatal("wrong user values")
	}
	if ts.QueryInt(`SELECT count(*) FROM participations p JOIN users u ON u.id = p.user_id
		JOIN contests c ON c.id = p.contest_id AND c.main_group_id = p.group_id
		WHERE u.username IN ('imp1', 'imp2') AND p.contest_id = `+cid) != 2 {
		t.Fatal("the users were not added to the contest")
	}
	audited(t, ts, n, "user.import", 0)
	// Again, without a contest: updates, adds nothing.
	imp("imp1,new,Ana,Nueva\n", "")
	if ts.QueryInt(`SELECT count(*) FROM users WHERE username = 'imp1' AND password = 'plaintext:new' AND last_name = 'Nueva' AND email IS NULL`) != 1 {
		t.Fatal("the user was not updated")
	}
	if d := ts.LastAudit().Details; d["created"] != float64(0) || d["updated"] != float64(1) {
		t.Fatalf("audit details: %v", d)
	}
	// Excel's "CSV UTF-8" starts with a byte order mark: the header is
	// still recognised.
	imp("\xef\xbb\xbfusername,password,first_name,last_name\nimp3,pw3,Eva,Tres\n", "")
	if ts.QueryInt(`SELECT count(*) FROM users WHERE username = 'imp3'`) != 1 {
		t.Fatalf("a CSV with a byte order mark was not imported: %s", c.notices())
	}
	for _, bad := range []string{
		"zed,pw,Z,Z\nb@d,pw,B,B\n",       // invalid username
		"zed,pw,Z,Z\nzed,pw,Z,Z\n",       // repeated username
		"zed,pw,Z,Z\nyo,,Y,Y\n",          // empty password
		"zed,pw,Z,Z\nyo,pw,Y\n",          // too few columns
		"zed,pw,Z,Z\nyo,pw,Y,Y,e,x\n",    // too many columns
		"zed,pw,Z,Z\n\"unterminated,x\n", // CSV syntax
		"",                               // no users
	} {
		n := ts.AuditCount()
		imp(bad, cid)
		if ts.QueryInt(`SELECT count(*) FROM users WHERE username = 'zed'`) != 0 || ts.AuditCount() != n {
			t.Fatalf("a bad file changed the users: %q", bad)
		}
		if body := c.notices(); !strings.Contains(body, "Invalid field") {
			t.Fatalf("no error for %q: %s", bad, body)
		}
	}
	if r := c.PostFiles("/users/import", url.Values{"contest_id": {"x"}}, upload{"users_file", "u.csv", []byte("zed,pw,Z,Z\n")}); r.Status != http.StatusBadRequest {
		t.Fatalf("bad contest id: %d", r.Status)
	}
	o := ts.Client()
	o.Login("prep")
	if r := o.PostFiles("/users/import", nil, upload{"users_file", "u.csv", []byte("zed,pw,Z,Z\n")}); r.Status != http.StatusForbidden {
		t.Fatalf("import as prep: %d", r.Status)
	}
}

// TestCommunicationForms checks announcements, questions and messages,
// and that the messaging role may use them while prep and ro may not.
func TestCommunicationForms(t *testing.T) {
	ts, f := newFixtureSite(t, testOptions{})
	cid := itoa(ts.Contest)
	qpath := "/contest/" + cid + "/question/" + itoa(f.Question)
	for _, name := range []string{"prep", "ro"} {
		o := ts.Client()
		o.Login(name)
		if r := o.Post("/contest/"+cid+"/announcements/add", url.Values{"subject": {"x"}, "text": {"y"}}); r.Status != http.StatusForbidden {
			t.Errorf("announcement as %s: %d", name, r.Status)
		}
		if r := o.Post(qpath+"/reply", url.Values{"reply_question_quick_answer": {"yes"}}); r.Status != http.StatusForbidden {
			t.Errorf("reply as %s: %d", name, r.Status)
		}
		if r := o.Delete("/contest/" + cid + "/announcement/" + itoa(f.Announcement)); r.Status != http.StatusForbidden {
			t.Errorf("delete announcement as %s: %d", name, r.Status)
		}
	}
	c := ts.Client()
	c.Login("msg")

	n := ts.AuditCount()
	if r := c.Post("/contest/"+cid+"/announcements/add", url.Values{"subject": {"Break"}, "text": {"10 minutes"}}); r.Status != http.StatusFound {
		t.Fatalf("add announcement: %d", r.Status)
	}
	aid := ts.QueryInt(`SELECT id FROM announcements WHERE subject = 'Break'`)
	audited(t, ts, n, "announcement.create", aid)
	if ts.LastAudit().ContestID == nil {
		t.Fatal("announcement audit without its contest")
	}
	n = ts.AuditCount()
	if r := c.Delete("/contest/" + cid + "/announcement/" + itoa(aid)); r.Status != http.StatusOK {
		t.Fatalf("delete announcement: %d", r.Status)
	}
	if ts.QueryInt(`SELECT count(*) FROM announcements WHERE id = $1`, aid) != 0 {
		t.Fatal("the announcement was not deleted")
	}
	audited(t, ts, n, "announcement.delete", aid)
	// An announcement of another contest is not found.
	if r := c.Delete("/contest/999999/announcement/" + itoa(f.Announcement)); r.Status != http.StatusNotFound {
		t.Fatalf("announcement of another contest: %d", r.Status)
	}

	// Claim, release, ignore, unignore, reply.
	steps := []struct {
		path   string
		form   url.Values
		action string
		check  string
	}{
		{"/claim", url.Values{"claim": {"yes"}}, "question.claim", `admin_id IS NOT NULL`},
		{"/claim", url.Values{"claim": {"no"}}, "question.unclaim", `admin_id IS NULL`},
		{"/ignore", url.Values{"ignore": {"yes"}}, "question.ignore", `ignored`},
		{"/ignore", url.Values{"ignore": {"no"}}, "question.unignore", `NOT ignored`},
		{"/reply", url.Values{"reply_question_quick_answer": {"other"}, "reply_question_text": {"N <= 10^9"}},
			"question.reply", `reply_text = 'N <= 10^9' AND reply_subject = '' AND reply_timestamp IS NOT NULL`},
		{"/reply", url.Values{"reply_question_quick_answer": {"yes"}}, "question.reply",
			`reply_subject = 'Yes' AND reply_text = ''`},
	}
	for _, s := range steps {
		n := ts.AuditCount()
		if r := c.Post(qpath+s.path, s.form); r.Status != http.StatusFound {
			t.Fatalf("%s: %d", s.action, r.Status)
		}
		if ts.QueryInt(`SELECT count(*) FROM questions WHERE id = $1 AND `+s.check, f.Question) != 1 {
			t.Fatalf("%s: the question is not %s", s.action, s.check)
		}
		audited(t, ts, n, s.action, int64(f.Question))
	}
	// An answered question cannot be claimed (CMS answers 405).
	if r := c.Post(qpath+"/claim", url.Values{"claim": {"yes"}}); r.Status != http.StatusMethodNotAllowed {
		t.Fatalf("claiming an answered question: %d", r.Status)
	}
	// A question of another contest is not found.
	if r := c.Post("/contest/999999/question/"+itoa(f.Question)+"/ignore", url.Values{"ignore": {"yes"}}); r.Status != http.StatusNotFound {
		t.Fatalf("question of another contest: %d", r.Status)
	}

	// A private message.
	n = ts.AuditCount()
	mpath := "/contest/" + cid + "/user/" + itoa(f.Users["carol"]) + "/message"
	if r := c.Post(mpath, url.Values{"message_subject": {"Seat"}, "message_text": {"Move"}}); r.Status != http.StatusFound {
		t.Fatalf("message: %d", r.Status)
	}
	mid := ts.QueryInt(`SELECT id FROM messages WHERE subject = 'Seat' AND participation_id = $1`, f.Part["carol"])
	audited(t, ts, n, "message.create", mid)
}
