// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported in part from cms/server/admin/jinja2_toolbox.py and
// cms/server/admin/formatting.py of CMS:
// Copyright © 2018 Stefano Maggiolo <s.maggiolo@gmail.com>

package adminweb

import (
	"encoding/json"
	"html/template"
	"strconv"
	"strings"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/web"
)

// funcs are the template functions of the admin site, on top of those of
// internal/web:
//
//	int v                Jinja's int filter (true is 1)
//	form_datetime t      a time as the forms of CMS show it
//	                     ("2026-07-01 09:00:00", str(datetime)) in the
//	                     server time zone; Form.Datetime reads it back
//	form_seconds d       a duration in seconds as CMS's forms show
//	                     timedelta.total_seconds() ("90.0", "" for nil)
//	dataset_attrs live autojudge  CMS format_dataset_attrs: " (Live)",
//	                     " (Background judging)" or ""
//	public_url "contest"|"ranking"  the public address of a site for
//	                     this request ("" if not configured; serverpage.go)
//	parse_authentication s  CMS's safe_parse_authentication: [method,
//	                     password] ("plaintext", "" when unreadable)
//	current_admin        the logged-in admin (for macros)
//	js_translations      the messages of aws_utils.js in the page's
//	                     language (base.html, non-English pages only)
func (s *Server) funcs(cur func() *web.Render) template.FuncMap {
	ctx := func() *Ctx {
		if rc := cur(); rc != nil {
			if c, ok := rc.Extra.(*Ctx); ok {
				return c
			}
		}
		return nil
	}
	tz := func() *time.Location {
		if rc := cur(); rc != nil && rc.TZ != nil {
			return rc.TZ
		}
		return time.UTC
	}
	return template.FuncMap{
		"int": func(v any) int64 {
			switch x := v.(type) {
			case bool:
				if x {
					return 1
				}
				return 0
			case int:
				return int64(x)
			case int32:
				return int64(x)
			case int64:
				return x
			case float64:
				return int64(x)
			}
			return 0
		},
		"form_datetime": func(v any) string {
			var t time.Time
			switch x := v.(type) {
			case time.Time:
				t = x
			case *time.Time:
				if x == nil {
					return ""
				}
				t = *x
			default:
				return ""
			}
			return FormDatetime(t, tz())
		},
		"form_seconds": func(v any) string {
			switch x := v.(type) {
			case time.Duration:
				return FormSeconds(x)
			case *time.Duration:
				if x == nil {
					return ""
				}
				return FormSeconds(*x)
			}
			return ""
		},
		"dataset_attrs": func(live, autojudge bool) string {
			var l *web.Lang
			if rc := cur(); rc != nil {
				l = rc.Lang
			}
			switch {
			case live:
				return " (" + l.T("Live") + ")"
			case autojudge:
				return " (" + l.T("Background judging") + ")"
			}
			return ""
		},
		"public_url": func(site string) string {
			if c := ctx(); c != nil {
				return s.PublicURL(c.R, site)
			}
			return ""
		},
		"parse_authentication": func(auth string) []string {
			m, p, err := web.ParseAuthentication(auth)
			if err != nil {
				return []string{"plaintext", ""}
			}
			return []string{m, p}
		},
		"current_admin": func() *Admin {
			if c := ctx(); c != nil {
				return c.Admin
			}
			return nil
		},
		"js_translations": func() template.JS {
			var l *web.Lang
			if rc := cur(); rc != nil {
				l = rc.Lang
			}
			m := make(map[string]string, len(jsMessages))
			for _, id := range jsMessages {
				if t := l.T(id); t != id {
					m[id] = t
				}
			}
			b, _ := json.Marshal(m)
			return template.JS(b)
		},
	}
}

// FormDatetime is a time as CMS's forms show it (Python's str(datetime):
// microseconds only when not zero) in loc.
func FormDatetime(t time.Time, loc *time.Location) string {
	t = t.In(loc)
	s := t.Format(FormDatetimeLayout)
	if us := t.Nanosecond() / 1000; us != 0 {
		s += "." + strings.TrimPrefix(strconv.Itoa(1000000+us), "1")
	}
	return s
}

// FormSeconds is a duration as CMS's forms show it
// (timedelta.total_seconds(), a Python float: "90.0", "0.5").
func FormSeconds(d time.Duration) string {
	us := d.Microseconds()
	s := strconv.FormatFloat(float64(us)/1e6, 'f', -1, 64)
	if !strings.ContainsAny(s, ".e") {
		s += ".0"
	}
	return s
}

// jsMessages are the texts of static/aws_utils.js and aws_handlers.js
// (CMS.AWSUtils._): js_translations gives their translations to the
// page.
var jsMessages = []string{
	N("Download"),
	N("copy"),
	N("File request failed."),
	N("Private message. "),
	N("Announcement. "),
	N("Reply to your question. "),
	N("New question: "),
	N("To start of contest: "),
	N("To end of contest: "),
	N("To start of analysis: "),
	N("To end of analysis: "),
	N("Unable to invalidate (%(msg)s)."),
	N("N/A"),
	N("Worker disabled"),
	N("%(job)s the <a href=\"%(result_url)s\">result</a> of <a href=\"%(submission_url)s\">submission %(submission)s</a> on <a href=\"%(dataset_url)s\">dataset %(dataset)s</a>"),
	N("%(job)s the result of user_test %(user_test)s on <a href=\"%(dataset_url)s\">dataset %(dataset)s</a>"),
	N("Compiling"),
	N("Evaluating"),
	N(" [%(n)s time(s) in queue]"),
	N(" [testcase: `%(codename)s']"),
	N("%(n)s second(s)"),
	N("%(n)s minute(s), "),
	N("%(n)s hour(s), "),
	N("Unexpected reply `%(status)s'. This should not happen."),
	N("Service not connected."),
	N("You are not authorized to call this method."),
	N("Call to service failed."),
	N("Pages: "),
	N("Hide reply"),
	N("Reply"),
	N("Error: "),
	N("Do you really want to reevaluate?"),
}
