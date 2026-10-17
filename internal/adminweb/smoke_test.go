// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package adminweb

import (
	"net/http"
	"strings"
	"testing"
)

// fixturePath fills the path values of a route with the rows of the
// fixture.
func fixturePath(ts *testSite, f *fixture, rt Route) string {
	values := map[string]string{
		"contest_id": itoa(ts.Contest), "user_id": itoa(f.Users["alice"]), "group_id": itoa(f.Group),
		"task_id": itoa(f.Tasks["sum"]), "dataset_id": itoa(f.Datasets["sum"]), "testcase_id": itoa(f.Testcase),
		"team_id": itoa(f.Team), "admin_id": itoa(ts.Admins["admin"]), "submission_id": itoa(f.Scored),
		"file_id": itoa(f.FileID), "old_id": itoa(f.Unofficial), "new_id": itoa(f.Scored),
		"user_test_id": itoa(f.UserTest), "digest": f.FileDigest, "filename": "sum.cpp", "format": "txt",
		"shard": "0", "question_id": itoa(f.Question), "announcement_id": itoa(f.Announcement),
	}
	if strings.HasPrefix(rt.Path, "/user_test_file/") {
		values["file_id"] = itoa(f.UserTestFile)
	}
	p := strings.TrimSuffix(rt.Path, "{$}")
	return wildcardsRe.ReplaceAllStringFunc(p, func(m string) string {
		return values[wildcardsRe.FindStringSubmatch(m)[1]]
	})
}

// TestEveryPageRenders renders every GET page of the route table (and
// the variants of the pages that depend on the data) with the fixture,
// as an admin with every permission, a read-only admin, a problem
// preparer and a messaging admin (and in Spanish).
func TestEveryPageRenders(t *testing.T) {
	ts, f := newFixtureSite(t, testOptions{})
	cid := itoa(ts.Contest)
	extra := []string{
		"/contest/" + cid + "/ranking/csv",
		"/contest/" + cid + "/submissions?page=1",
		"/submission/" + itoa(f.Compiling), "/submission/" + itoa(f.Failed), "/submission/" + itoa(f.Unofficial),
		"/submission/" + itoa(f.Scored) + "/" + itoa(f.Datasets["sum"]),
		"/submission_diff/" + itoa(f.Compiling) + "/" + itoa(f.Scored),
		"/user/" + itoa(f.Users["bob"]), "/user/" + itoa(f.Users["carol"]),
		"/contest/" + cid + "/user/" + itoa(f.Users["bob"]) + "/edit",
	}
	for _, tk := range []string{"half", "quiz"} {
		extra = append(extra, "/task/"+itoa(f.Tasks[tk]), "/dataset/"+itoa(f.Datasets[tk]),
			"/task/"+itoa(f.Tasks[tk])+"/prepare", "/task/"+itoa(f.Tasks[tk])+"/problem.yaml",
			"/task/"+itoa(f.Tasks[tk])+"/files", "/task/"+itoa(f.Tasks[tk])+"/test_submissions")
	}
	for _, who := range []string{"admin", "ro", "prep", "msg", "admin/es"} {
		name, lang, _ := strings.Cut(who, "/")
		c := ts.Client()
		if lang != "" {
			c.Header.Set("Accept-Language", lang)
		}
		c.Login(name)
		a, err := ts.S.st.Q.GetAdminByUsername(t.Context(), name)
		if err != nil {
			t.Fatal(err)
		}
		admin := &Admin{Admin: a}
		check := func(path string, need Perm) {
			t.Helper()
			r := c.Get(path)
			want := http.StatusOK
			if !admin.Can(need) {
				want = http.StatusForbidden
			}
			if r.Status != want {
				body := r.Body
				if len(body) > 600 {
					body = body[:600]
				}
				t.Errorf("GET %s as %s: %d, want %d\n%s", path, who, r.Status, want, body)
			}
		}
		for _, rt := range ts.S.routeTab {
			if rt.Method != "GET" || rt.Perm == PermPublic || strings.HasSuffix(rt.Path, "/events") {
				continue
			}
			check(fixturePath(ts, f, rt), rt.Perm)
		}
		for _, p := range extra {
			need := PermRead
			if strings.HasSuffix(p, "/files") {
				need = PermTasks
			}
			check(p, need)
		}
	}
}
