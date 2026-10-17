// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package adminweb

import (
	"net/http"
	"net/url"
	"testing"
)

// TestTaskAndDatasetForms submits the task settings and the dataset and
// testcase actions as a problem preparer, and checks the rows and the
// audit log; the messaging role may not.
func TestTaskAndDatasetForms(t *testing.T) {
	ts, f := newFixtureSite(t, testOptions{})
	c := ts.Client()
	c.Login("prep")
	for _, tk := range []string{"sum", "half", "quiz"} {
		// Task settings: every task's form round-trips.
		tid := itoa(f.Tasks[tk])
		n := ts.AuditCount()
		form := c.mustForm("/task/"+tid, "task/"+tid)
		form.Set("title", "Title of "+tk)
		if r := c.Post("/task/"+tid, form); !wantRedirect(r, "task/"+tid) {
			t.Fatalf("task %s: %d %q", tk, r.Status, r.Location())
		}
		if ts.QueryInt(`SELECT count(*) FROM tasks WHERE id = $1 AND title = $2`, f.Tasks[tk], "Title of "+tk) != 1 {
			t.Fatalf("task %s was not saved: %s", tk, c.notices())
		}
		if ts.AuditCount() == n || ts.LastAudit().Action != "task.update" && ts.LastAudit().Action != "dataset.update" {
			t.Fatalf("task %s: no audit (%d rows)", tk, ts.AuditCount()-n)
		}
		if ts.QueryInt(`SELECT count(*) FROM audit_log WHERE id > (SELECT max(id) - $1 FROM audit_log) AND action = 'task.update'`,
			ts.AuditCount()-n) != 1 {
			t.Fatalf("task %s: no task.update", tk)
		}
	}
	// A changed time limit of the dataset.
	sid := itoa(f.Tasks["sum"])
	dsid := itoa(f.Datasets["sum"])
	form := c.mustForm("/task/"+sid, "task/"+sid)
	form.Set("time_limit_"+dsid, "2.5")
	c.Post("/task/"+sid, form)
	if ts.QueryInt(`SELECT count(*) FROM datasets WHERE id = $1 AND time_limit = 2.5`, f.Datasets["sum"]) != 1 {
		t.Fatalf("the time limit was not saved: %s", c.notices())
	}
	if ts.LastAudit().Action != "dataset.update" {
		t.Fatalf("time limit audit: %s", ts.LastAudit().Action)
	}

	// Clone the dataset of sum with its results.
	n := ts.AuditCount()
	form = c.mustForm("/dataset/"+dsid+"/clone", "dataset/"+dsid+"/clone")
	form.Set("description", "Second")
	form.Set("clone_results", "on")
	if r := c.Post("/dataset/"+dsid+"/clone", form); !wantRedirect(r, "task/"+sid) {
		t.Fatalf("clone: %d %q %s", r.Status, r.Location(), c.notices())
	}
	clone := ts.QueryInt(`SELECT id FROM datasets WHERE task_id = $1 AND description = 'Second'`, f.Tasks["sum"])
	audited(t, ts, n, "dataset.create", clone)
	if a, b := ts.QueryInt(`SELECT count(*) FROM testcases WHERE dataset_id = $1`, clone),
		ts.QueryInt(`SELECT count(*) FROM testcases WHERE dataset_id = $1`, f.Datasets["sum"]); a != b || a == 0 {
		t.Fatalf("clone: %d testcases, want %d", a, b)
	}
	if ts.QueryInt(`SELECT count(*) FROM submission_results WHERE dataset_id = $1 AND score = 100`, clone) != 1 {
		t.Fatal("clone: the results were not cloned")
	}
	if ts.QueryInt(`SELECT count(*) FROM evaluations WHERE dataset_id = $1`, clone) == 0 {
		t.Fatal("clone: the evaluations were not cloned")
	}
	// The same description again is refused.
	if c.Post("/dataset/"+dsid+"/clone", form); ts.AuditCount() != n+1 {
		t.Fatal("a repeated dataset description was accepted")
	}

	// Rename it.
	n = ts.AuditCount()
	cpath := "/dataset/" + itoa(clone)
	if r := c.Post(cpath+"/rename", url.Values{"description": {"Third"}}); !wantRedirect(r, "task/"+sid) {
		t.Fatalf("rename: %d %q", r.Status, r.Location())
	}
	if ts.QueryInt(`SELECT count(*) FROM datasets WHERE id = $1 AND description = 'Third'`, clone) != 1 {
		t.Fatal("the dataset was not renamed")
	}
	audited(t, ts, n, "dataset.rename", clone)
	taken := queryString(t, ts, `SELECT description FROM datasets WHERE id = $1`, f.Datasets["sum"])
	if c.Post(cpath+"/rename", url.Values{"description": {taken}}); ts.AuditCount() != n+1 {
		t.Fatal("renaming to a taken description was accepted")
	}

	// Activate it, with a message to alice.
	n = ts.AuditCount()
	if r := c.Get(cpath + "/activate"); r.Status != http.StatusOK {
		t.Fatalf("activate page: %d", r.Status)
	}
	r := c.Post(cpath+"/activate", url.Values{"message_subject": {"Rejudged"}, "message_text": {"New data"},
		"notify_" + itoa(f.Part["alice"]): {"on"}})
	if !wantRedirect(r, "task/"+sid) {
		t.Fatalf("activate: %d %q", r.Status, r.Location())
	}
	if ts.QueryInt(`SELECT active_dataset_id FROM tasks WHERE id = $1`, f.Tasks["sum"]) != clone {
		t.Fatal("the dataset is not live")
	}
	audited(t, ts, n, "dataset.activate", clone)
	if ts.QueryInt(`SELECT count(*) FROM messages WHERE subject = 'Rejudged' AND participation_id = $1`, f.Part["alice"]) != 1 {
		t.Fatal("activate: the message was not sent")
	}

	// Add a testcase to the clone, then delete it.
	n = ts.AuditCount()
	r = c.PostFiles(cpath+"/testcases/add", url.Values{"codename": {"extra"}, "public": {"on"}},
		upload{"input", "in.txt", []byte("1 2\n")}, upload{"output", "out.txt", []byte("3\n")})
	if !wantRedirect(r, "task/"+sid) {
		t.Fatalf("add testcase: %d %q %s", r.Status, r.Location(), c.notices())
	}
	tc := ts.QueryInt(`SELECT id FROM testcases WHERE dataset_id = $1 AND codename = 'extra' AND public`, clone)
	audited(t, ts, n, "testcase.create", clone)
	// The same codename again fails and is not audited.
	c.PostFiles(cpath+"/testcases/add", url.Values{"codename": {"extra"}},
		upload{"input", "in.txt", []byte("1\n")}, upload{"output", "out.txt", []byte("1\n")})
	if ts.AuditCount() != n+1 {
		t.Fatal("a repeated codename was accepted")
	}
	// Deleting it through another dataset is not found.
	if r := c.Delete("/dataset/" + dsid + "/testcase/" + itoa(tc) + "/delete"); r.Status != http.StatusNotFound {
		t.Fatalf("testcase of another dataset: %d", r.Status)
	}
	n = ts.AuditCount()
	if r := c.Delete(cpath + "/testcase/" + itoa(tc) + "/delete"); r.Status != http.StatusOK || r.Body != "./"+sid {
		t.Fatalf("delete testcase: %d %q", r.Status, r.Body)
	}
	if ts.QueryInt(`SELECT count(*) FROM testcases WHERE id = $1`, tc) != 0 {
		t.Fatal("the testcase was not deleted")
	}
	audited(t, ts, n, "testcase.delete", clone)

	// The messaging and read-only roles cannot prepare tasks.
	for _, name := range []string{"msg", "ro"} {
		o := ts.Client()
		o.Login(name)
		if r := o.Post("/task/"+sid, form); r.Status != http.StatusForbidden {
			t.Errorf("task form as %s: %d", name, r.Status)
		}
		if r := o.Post(cpath+"/rename", url.Values{"description": {"x"}}); r.Status != http.StatusForbidden {
			t.Errorf("rename as %s: %d", name, r.Status)
		}
		if r := o.Delete(cpath + "/testcase/" + itoa(f.Testcase) + "/delete"); r.Status != http.StatusForbidden {
			t.Errorf("delete testcase as %s: %d", name, r.Status)
		}
	}
}
