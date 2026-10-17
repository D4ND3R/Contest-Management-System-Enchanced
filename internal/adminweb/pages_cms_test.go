// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package adminweb

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestCMSPages compares pages with the ones the original CMS sends
// (testdata/cms, captured from the reference installation with the demo
// contest), after removing what changes on every request (CSRF tokens,
// static hashes) and the deliberate differences of this fork:
//
//   - the problem preparer checkbox of the admin form (R6.5);
//   - the delete link uses data-ajax-delete instead of onclick (D10);
//   - the Logs section says where the logs are (no LogService, D16).
func TestCMSPages(t *testing.T) {
	ts := newTestSite(t, testOptions{})
	ts.Exec(`UPDATE admins SET name = 'admin' WHERE username = 'admin'`)
	c := ts.Client()
	norm := func(s string) string {
		s = regexp.MustCompile(`name="_xsrf" value="[^"]*"`).ReplaceAllString(s, `name="_xsrf" value="X"`)
		return regexp.MustCompile(`\?h=[0-9a-f]+`).ReplaceAllString(s, "")
	}
	core := func(s string) string {
		if i := strings.Index(s, `<div id="core">`); i >= 0 {
			return s[strings.LastIndex(s[:i], "\n")+1:]
		}
		return s
	}
	compare := func(name, got string, fixes ...string) {
		t.Helper()
		data, err := os.ReadFile("testdata/cms/" + name)
		if err != nil {
			t.Fatal(err)
		}
		want := norm(string(data))
		for i := 0; i+1 < len(fixes); i += 2 {
			want = strings.ReplaceAll(want, fixes[i], fixes[i+1])
		}
		if got = norm(got); got != want {
			t.Errorf("%s differs from CMS:\n--- got\n%s\n--- want\n%s", name, got, want)
		}
	}
	compare("login.html", c.Get("/login").Body)
	c.Login("admin")
	cid := itoa(ts.Contest)
	// The last loading row of the overview is the Logs table.
	loading := `<tr><td style="text-align: center;" colspan="5"><img src="../../static/loading.gif" alt="loading..." /></td></tr>`
	logs := `<tr><td colspan="5">Each service writes its logs to its standard output: see journalctl or docker compose logs.</td></tr>`
	got := core(c.Get("/contest/" + cid + "/overview").Body)
	if i := strings.LastIndex(got, logs); i >= 0 {
		got = got[:i] + loading + got[i+len(logs):]
	}
	compare("contest_overview_core.html", got, "contest/4/", "contest/"+cid+"/")
	compare("admin_core.html", core(c.Get("/admin/"+itoa(ts.Admins["admin"])).Body),
		`<a onclick="CMS.AWSUtils.ajax_delete('../admin/1'); ">`, `<a data-ajax-delete="../admin/`+itoa(ts.Admins["admin"])+`">`,
		`action="../admin/1"`, `action="../admin/`+itoa(ts.Admins["admin"])+`"`,
		`    <tr>
      <td>Enabled</td>`, `    <tr>
      <td>Problem preparer permissions</td>
      <td>
        <input type="checkbox" name="permission_tasks"  />
      </td>
    </tr>
    <tr>
      <td>Enabled</td>`)
}
