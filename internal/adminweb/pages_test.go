// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package adminweb

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/web"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/web/webtest"
)

// TestTemplatesRender renders every template with zero data (no crash)
// and the core pages with sample data in English and Spanish (no error).
func TestTemplatesRender(t *testing.T) {
	ts := newTestSite(t, testOptions{})
	eng := ts.S.site.Engine
	webtest.RenderPages(t, eng, webtest.Options{})

	admin := &Admin{Admin: q.Admin{ID: 1, Name: "A", Username: "a", Enabled: true, PermissionAll: true}}
	group := &q.Group{ID: 1, Name: "g", Start: demoNow, Stop: demoNow.Add(time.Hour)}
	contest := &Contest{Contest: q.Contest{ID: 1, Name: "c", Description: "C", AllowUserTests: true}, MainGroup: group, Unanswered: 2}
	sidebar := &Sidebar{Contests: []SidebarItem{{1, "c", "C"}}, MoreContests: true, Tasks: []SidebarItem{{2, "t", "T"}}, Users: 3, Teams: 1}
	pages := map[string]func(p *Page) any{
		"overview.html":      func(p *Page) any { return p },
		"error.html":         func(p *Page) any { return errorData{Page: p, StatusCode: 404} },
		"admins.html":        func(p *Page) any { return adminsData{Page: p, Admins: []q.Admin{admin.Admin}} },
		"admin.html":         func(p *Page) any { return adminData{Page: p, Edited: &admin.Admin} },
		"add_admin.html":     func(p *Page) any { return adminData{Page: p} },
		"resourceslist.html": func(p *Page) any { return resourcesListData{Page: p, Machines: []machine{{Host: "h"}}} },
		"resources.html": func(p *Page) any {
			return resourcesListData{Page: p, Machines: []machine{{Host: "h", Services: []serviceStatus{
				{Name: "x", Status: "running", Since: demoNow}, {Name: "y", Status: "down"}}}}}
		},
		"server.html": func(p *Page) any { return serverData{Page: p, Timezone: "UTC", ContestLink: "http://x/"} },
		"audit.html": func(p *Page) any {
			return auditData{Page: p, Rows: []auditRow{{ID: 1, At: demoNow, Admin: "a", Action: "x.y", TargetType: "x",
				TargetID: 3, Contest: "c", ContestID: 1, Details: "{}"}}, OlderURL: "audit?before=1_1"}
		},
		"login.html": func(*Page) any { return loginData{} },
	}
	for _, lang := range []string{"en", "es"} {
		for _, withContest := range []bool{false, true} {
			for name, data := range pages {
				p := &Page{Admin: admin, Timestamp: demoNow, LiveURL: "events", LiveSince: "1"}
				if withContest {
					p.Contest = contest
				} else {
					p.Sidebar = sidebar
				}
				rc := &web.Render{Lang: ts.S.ui.Get(lang), TZ: time.UTC, Now: demoNow, Root: "."}
				var b strings.Builder
				if err := eng.Execute(&b, name, rc, data(p)); err != nil {
					t.Errorf("%s (%s, contest %v): %v", name, lang, withContest, err)
				}
				if lang == "es" && name == "overview.html" && !withContest && !strings.Contains(b.String(), "Administración") {
					t.Errorf("overview in Spanish: %s", b.String())
				}
			}
		}
	}
}

// TestSpanish checks the language choice (Accept-Language) and the
// translations of the scripts.
func TestSpanish(t *testing.T) {
	ts := newTestSite(t, testOptions{})
	c := ts.Client()
	c.Header.Set("Accept-Language", "es-AR,es;q=0.9")
	c.Login("admin")
	r := c.Get("/")
	for _, want := range []string{`<html lang="es">`, "Administración", "Estado de la cola", "Salir",
		`var CMS_TRANSLATIONS = {`, `"Worker disabled":"Juez desactivado"`, "Cola vacía."} {
		if !strings.Contains(r.Body, want) {
			t.Errorf("Spanish overview lacks %q", want)
		}
	}
	if r.Header.Get("Content-Language") != "es" {
		t.Errorf("Content-Language %q", r.Header.Get("Content-Language"))
	}
	c.Header.Set("Accept-Language", "fr")
	if r := c.Get("/"); !strings.Contains(r.Body, `<html lang="en">`) || strings.Contains(r.Body, "CMS_TRANSLATIONS") {
		t.Error("a language without admin catalogs is not English")
	}
}

// TestAdmins checks the admins pages: creation, edition (with the problem
// preparer bit), deletion and the protection of the last admin with every
// permission.
func TestAdmins(t *testing.T) {
	ts := newTestSite(t, testOptions{})
	c := ts.Client()
	c.Login("admin")
	if r := c.Get("/admins"); r.Status != 200 || !strings.Contains(r.Body, `<a href="./admin/`+itoa(ts.Admins["prep"])+`">prep</a>`) {
		t.Fatalf("admins: %d", r.Status)
	}
	if r := c.Post("/admins/add", url.Values{"username": {"new"}, "name": {"New"}, "password": {""}}); r.Location() != "../admins/add" {
		t.Fatalf("empty password: %q", r.Location())
	}
	if r := c.Post("/admins/add", url.Values{"username": {"bad name"}, "name": {"New"}, "password": {"pw"}, "enabled": {"on"}}); r.Location() != "../admins/add" {
		t.Fatalf("invalid username: %q", r.Location())
	}
	r := c.Post("/admins/add", url.Values{"username": {"new"}, "name": {"New"}, "password": {"pw"},
		"permission_tasks": {"on"}, "enabled": {"on"}})
	if r.Location() != "../admins" {
		t.Fatalf("add: %d %q", r.Status, r.Location())
	}
	id := int32(ts.QueryInt(`SELECT id FROM admins WHERE username = 'new' AND permission_tasks AND NOT permission_all AND enabled
		AND authentication LIKE 'bcrypt:%'`))
	if e := ts.LastAudit(); e.Action != "admin.create" || e.Details["created"].(map[string]any)["authentication"] != redacted {
		t.Fatalf("audit: %+v", e)
	}
	nc := ts.Client()
	nr := nc.Post("/login", url.Values{"username": {"new"}, "password": {"pw"}})
	if nr.Location() != "." {
		t.Fatalf("the new admin cannot log in: %q", nr.Location())
	}
	if r := c.Get("/admin/" + itoa(id)); !strings.Contains(r.Body, `<input type="checkbox" name="permission_tasks" checked />`) {
		t.Fatalf("edit page: %s", r.Body)
	}
	// Leaving the password empty keeps it; the checkboxes are read.
	r = c.Post("/admin/"+itoa(id), url.Values{"username": {"new"}, "name": {"Renamed"}, "password": {""}, "permission_messaging": {"on"}})
	if r.Location() != "../admins" {
		t.Fatalf("edit: %q", r.Location())
	}
	if ts.QueryInt(`SELECT count(*) FROM admins WHERE id = $1 AND name = 'Renamed' AND permission_messaging
		AND NOT permission_tasks AND NOT enabled AND authentication LIKE 'bcrypt:%'`, id) != 1 {
		t.Fatal("edit not applied")
	}
	// The last enabled admin with every permission stays.
	r = c.Post("/admin/"+itoa(ts.Admins["admin"]), url.Values{"username": {"admin"}, "name": {"A"}, "enabled": {"on"}})
	if r.Location() != "../admin/"+itoa(ts.Admins["admin"]) || ts.QueryInt(`SELECT count(*) FROM admins WHERE username = 'admin' AND permission_all`) != 1 {
		t.Fatalf("demoting the last admin: %q", r.Location())
	}
	if r := c.Delete("/admin/" + itoa(ts.Admins["admin"])); r.Body != "../admins" ||
		ts.QueryInt(`SELECT count(*) FROM admins WHERE username = 'admin'`) != 1 {
		t.Fatalf("deleting the last admin: %q", r.Body)
	}
	if r := c.Delete("/admin/" + itoa(id)); r.Status != 200 || r.Body != "../admins" ||
		ts.QueryInt(`SELECT count(*) FROM admins WHERE id = $1`, id) != 0 {
		t.Fatalf("delete: %d %q", r.Status, r.Body)
	}
	if e := ts.LastAudit(); e.Action != "admin.delete" {
		t.Fatalf("audit: %+v", e)
	}
	if r := c.Get("/admin/999"); r.Status != 404 {
		t.Fatalf("unknown admin: %d", r.Status)
	}
	// Changing one's own password keeps the session.
	r = c.Post("/admin/"+itoa(ts.Admins["admin"]), url.Values{"username": {"admin"}, "name": {"A"}, "password": {"other"},
		"enabled": {"on"}, "permission_all": {"on"}})
	if r.Location() != "../admins" || c.Get("/admins").Status != 200 {
		t.Fatalf("own password: %q", r.Location())
	}
	// The notifications report the outcomes.
	n := c.Get("/notifications?last_notification=0")
	if !strings.Contains(n.Body, `"Operation successful."`) || !strings.Contains(n.Body, `"Operation failed."`) ||
		!strings.Contains(n.Body, "At least one enabled admin") {
		t.Fatalf("notifications: %s", n.Body)
	}
	if n := c.Get("/notifications?last_notification=0"); n.Body != "[]" {
		t.Fatalf("notifications are taken once: %s", n.Body)
	}
}

// TestNotifications checks the new questions of the notifications.
func TestNotifications(t *testing.T) {
	ts := newTestSite(t, testOptions{})
	c := ts.Client()
	c.Login("ro")
	ts.Exec(`INSERT INTO questions (question_timestamp, subject, text, participation_id)
		SELECT $1, 'Subj', 'Body', id FROM participations WHERE contest_id = $2 LIMIT 1`, demoNow, ts.Contest)
	r := c.Get("/notifications?last_notification=0")
	if r.Status != 200 || !strings.Contains(r.Body, `"type":"new_question"`) || !strings.Contains(r.Body, `"subject":"Subj"`) ||
		!strings.Contains(r.Body, fmt.Sprintf(`"contest_id":%d`, ts.Contest)) {
		t.Fatalf("notifications: %s", r.Body)
	}
	after := fmt.Sprint(demoNow.Unix())
	if r := c.Get("/notifications?last_notification=" + after); r.Body != "[]" {
		t.Fatalf("after the question: %s", r.Body)
	}
	if r := c.Get("/notifications?last_notification=x"); r.Status != 400 {
		t.Fatalf("bad argument: %d", r.Status)
	}
	// The badge of the contest menu.
	if r := c.Get("/contest/" + itoa(ts.Contest) + "/overview"); !strings.Contains(r.Body, "\n                1\n") {
		t.Fatalf("unanswered badge: %s", r.Body)
	}
}

// TestServerPage checks the Server page and the public addresses.
func TestServerPage(t *testing.T) {
	ts := newTestSite(t, testOptions{})
	c := ts.Client()
	c.Login("admin")
	for _, bad := range []url.Values{{"timezone": {"Mars/Base"}}, {"public_url_contest": {"ftp://x"}},
		{"public_url_ranking": {":99999"}}, {"public_url_contest": {"http://x/?a=1"}}} {
		before := ts.AuditCount()
		if r := c.Post("/server", bad); r.Location() != "./server" || ts.AuditCount() != before {
			t.Fatalf("%v accepted", bad)
		}
	}
	r := c.Post("/server", url.Values{"timezone": {"America/Argentina/Buenos_Aires"},
		"public_url_contest": {":8888"}, "public_url_ranking": {"https://ranking.example.org"}})
	if r.Location() != "./server" {
		t.Fatalf("post: %q", r.Location())
	}
	page := c.Get("/server")
	for _, want := range []string{`value="America/Argentina/Buenos_Aires"`, `href="http://127.0.0.1:8888/"`,
		`href="https://ranking.example.org/"`, "(in use: America/Argentina/Buenos_Aires)"} {
		if !strings.Contains(page.Body, want) {
			t.Errorf("server page lacks %q", want)
		}
	}
	req := httptest.NewRequest("GET", "http://admin.example.org:8889/", nil)
	if got := ts.S.PublicURL(req, SiteContest); got != "http://admin.example.org:8888/" {
		t.Errorf("PublicURL contest: %q", got)
	}
	if got := ts.S.PublicURL(req, SiteRanking); got != "https://ranking.example.org/" {
		t.Errorf("PublicURL ranking: %q", got)
	}
	req = httptest.NewRequest("GET", "http://[::1]:8889/", nil)
	if got := resolvePublicURL(":8888", req); got != "http://[::1]:8888/" {
		t.Errorf("IPv6: %q", got)
	}
	// The time zone of the pages.
	if ts.S.tz.Location().String() != "America/Argentina/Buenos_Aires" {
		t.Errorf("location: %v", ts.S.tz.Location())
	}
	// Empty values delete the settings.
	c.Post("/server", url.Values{})
	if n := ts.QueryInt(`SELECT count(*) FROM server_settings`); n != 0 {
		t.Errorf("%d settings left", n)
	}
	if r := c.Get("/server"); r.Status != 200 || !strings.Contains(r.Body, "(in use: UTC)") {
		t.Errorf("server page after clearing: %d", r.Status)
	}
}

// TestPageQueries checks that the global and contest pages run a bounded
// number of queries, whatever the number of contests, tasks and users.
func TestPageQueries(t *testing.T) {
	ts := newTestSite(t, testOptions{Counting: true})
	c := ts.Client()
	c.Login("admin")
	count := func(path string) int64 {
		ts.Counter.Reset()
		if r := c.Get(path); r.Status != http.StatusOK {
			t.Fatalf("%s: %d", path, r.Status)
		}
		return ts.Counter.Count()
	}
	paths := []string{"/", "/contest/" + itoa(ts.Contest) + "/overview", "/admins", "/server", "/audit", "/resourceslist"}
	before := map[string]int64{}
	for _, p := range paths {
		before[p] = count(p)
	}
	for i := range 30 {
		ts.Exec(`INSERT INTO contests (name, description) VALUES ($1, 'x')`, fmt.Sprintf("c%02d", i))
		ts.Exec(`INSERT INTO tasks (name, title) VALUES ($1, 'x')`, fmt.Sprintf("t%02d", i))
		ts.Exec(`INSERT INTO users (username, first_name, last_name, password) VALUES ($1, 'a', 'b', 'plaintext:x')`, fmt.Sprintf("u%02d", i))
		ts.Exec(`INSERT INTO teams (code, name) VALUES ($1, 'x')`, fmt.Sprintf("tm%02d", i))
		ts.Exec(`INSERT INTO questions (question_timestamp, subject, text, participation_id)
			SELECT $1, 's', 't', id FROM participations WHERE contest_id = $2 LIMIT 1`, demoNow, ts.Contest)
		ts.Exec(`INSERT INTO audit_log (at, action, target_type) VALUES ($1, 'x.y', 'x')`, demoNow)
	}
	for _, p := range paths {
		after := count(p)
		if after != before[p] {
			t.Errorf("%s: %d queries with 30 more rows, %d before", p, after, before[p])
		}
		if after > 5 {
			t.Errorf("%s: %d queries", p, after)
		}
	}
	// The sidebar shows five contests and "show more".
	r := c.Get("/")
	if n := strings.Count(r.Body, `<a class="menu_link bold" href="./contest/`); n != 5 || !strings.Contains(r.Body, "(show the 33 users...)") ||
		!strings.Contains(r.Body, "(show the 30 teams...)") {
		t.Errorf("sidebar: %d contests", n)
	}
}

// TestFilesAndMarkdown checks file/<digest>/<name> and render_markdown.
func TestFilesAndMarkdown(t *testing.T) {
	ts := newTestSite(t, testOptions{})
	d, err := ts.Blobs.PutBytes(t.Context(), []byte("hello\n"))
	if err != nil {
		t.Fatal(err)
	}
	c := ts.Client()
	if r := c.Get("/file/" + d + "/a.txt"); r.Status != http.StatusFound {
		t.Fatalf("logged out: %d", r.Status)
	}
	c.Login("ro")
	r := c.Get("/file/" + d + "/input_01")
	if r.Status != 200 || r.Body != "hello\n" || !strings.HasPrefix(r.Header.Get("Content-Type"), "text/plain") ||
		r.Header.Get("Content-Disposition") != `attachment; filename=input_01` {
		t.Fatalf("file: %d %q %q", r.Status, r.Header.Get("Content-Type"), r.Header.Get("Content-Disposition"))
	}
	if r := c.Get("/file/" + strings.Repeat("0", 64) + "/a"); r.Status != 404 {
		t.Fatalf("missing file: %d", r.Status)
	}
	r = c.Post("/render_markdown", url.Values{"input": {"*hi* <script>x</script>"}})
	if r.Status != 200 || r.Body != "<p><em>hi</em> &lt;script&gt;x&lt;/script&gt;</p>\n" {
		t.Fatalf("markdown: %d %q", r.Status, r.Body)
	}
}

// TestResources checks the Resource usage pages.
func TestResources(t *testing.T) {
	ts := newTestSite(t, testOptions{})
	c := ts.Client()
	c.Login("ro")
	ts.KV.Redis().Set(t.Context(), ts.KV.Key("dispatcher:lease"), "host-a:42#7", time.Minute)
	if r := c.Get("/resourceslist"); r.Status != 200 || !strings.Contains(r.Body, `href="./resources/0"`) {
		t.Fatalf("resourceslist: %d", r.Status)
	}
	r := c.Get("/resources/all")
	for _, want := range []string{"admin-web", "PostgreSQL", "Valkey", "dispatcher", "host-a:42", "Running"} {
		if !strings.Contains(r.Body, want) {
			t.Errorf("resources lacks %q", want)
		}
	}
	if r := c.Get("/resources/7"); r.Status != http.StatusFound || r.Location() != "../resourceslist" {
		t.Errorf("unknown machine: %d %q", r.Status, r.Location())
	}
	if r := c.Get("/resources/7/" + itoa(ts.Contest)); r.Location() != "../../contest/"+itoa(ts.Contest)+"/resourceslist" {
		t.Errorf("unknown machine of a contest: %q", r.Location())
	}
}
