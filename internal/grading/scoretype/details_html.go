// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from get_html_details in cms/grading/scoretypes/abc.py of CMS:
// Copyright © 2010-2012 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2013-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>

package scoretype

import (
	"bytes"
	"embed"
	"encoding/json"
	"html/template"
	"io"
	"regexp"
	"strings"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/i18n"
)

// Translation is the interface language the score details are rendered
// in (the translation passed to CMS's get_html_details). The contestant
// site and the administration adapt their language to it.
type Translation interface {
	// T translates a msgid of the "cms" catalogs.
	T(msgid string) string
	// FormatDecimal, FormatDuration (seconds, CMS's short format) and
	// FormatSize format as CMS's Translation does.
	FormatDecimal(x float64) string
	FormatDuration(seconds float64) string
	FormatSize(n int64) string
	// FormatStatusText formats the text of an evaluation (CMS
	// format_status_text): a msgid and its arguments; nil or malformed
	// texts give the translation of "N/A".
	FormatStatusText(text []string) string
}

// Messages of the details (the msgids the templates use, for the
// extraction of translations: they are all in the "cms" catalogs).
const (
	// MsgUnavailable is what CMS shows instead of details it cannot
	// read (NULL details, details of an unexpected shape).
	MsgUnavailable = "Score details temporarily unavailable."
	msgSubtask     = "Subtask %(index)s"
)

//go:embed templates/*.html
var templateFiles embed.FS

// detailsTemplates are the templates of the details: "details_group.html"
// (GroupMin, GroupMul, GroupThreshold) and "details_sum.html" (Sum).
var detailsTemplates = mustParseTemplates()

// controlLine matches a line holding only control actions (if, else,
// end, range, variable declarations...): the line's indentation and
// newline are removed, so that it vanishes from the output, as with the
// trim_blocks and lstrip_blocks options of CMS's Jinja environment.
var controlLine = regexp.MustCompile(`(?m)^[ \t]*((?:\{\{\s*(?:(?:if|else|end|range|with|break|continue)\b|\$\w*\s*:?=)[^}]*\}\})+)[ \t]*\n`)

// leadingComment is the license comment at the top of a template.
var leadingComment = regexp.MustCompile(`(?s)\A\{\{/\*.*?\*/\}\}\n`)

func mustParseTemplates() *template.Template {
	root := template.New("details")
	entries, err := templateFiles.ReadDir("templates")
	if err != nil {
		panic(err)
	}
	for _, e := range entries {
		b, err := templateFiles.ReadFile("templates/" + e.Name())
		if err != nil {
			panic(err)
		}
		src := leadingComment.ReplaceAllString(string(b), "")
		src = controlLine.ReplaceAllString(src, "$1")
		// The TEMPLATE strings of CMS end without a newline.
		src = strings.TrimSuffix(src, "\n")
		template.Must(root.New(e.Name()).Parse(src))
	}
	return root
}

// htmlData is what the templates of the details see: the details and the
// language (methods, so that the parsed templates need no per-request
// functions).
type htmlData struct {
	*Details
	tr Translation
}

// T is a {% trans %} block of CMS: the translation is trusted HTML.
func (d *htmlData) T(msgid string) template.HTML { return template.HTML(d.tr.T(msgid)) }

// Outcome is CMS's {{ _(tc["outcome"]) }}: translated and escaped.
func (d *htmlData) Outcome(o string) string { return d.tr.T(o) }

// Subtask is the title of a subtask ("Subtask 0").
func (d *htmlData) Subtask(idx int) template.HTML {
	return template.HTML(i18n.Format(d.tr.T(msgSubtask), map[string]any{"index": idx}))
}

// Index is CMS's loop.index (from 1).
func (d *htmlData) Index(i int) int { return i + 1 }

func (d *htmlData) Decimal(x float64) string { return d.tr.FormatDecimal(x) }

func (d *htmlData) Duration(x *float64) string {
	if x == nil {
		return ""
	}
	return d.tr.FormatDuration(*x)
}

func (d *htmlData) Size(x *int64) string {
	if x == nil {
		return ""
	}
	return d.tr.FormatSize(*x)
}

func (d *htmlData) StatusText(t []string) string { return d.tr.FormatStatusText(t) }

// RenderDetails writes the HTML of score details already filtered for
// the feedback level (ScoreType.JSONDetails), as CMS's get_html_details
// renders the TEMPLATE of the score type: the subtasks and their
// testcases for the group score types, the testcases for Sum. Nil
// details write the translation of MsgUnavailable, as CMS does for NULL
// details.
func RenderDetails(w io.Writer, d *Details, tr Translation) error {
	if d == nil {
		_, err := io.WriteString(w, tr.T(MsgUnavailable))
		return err
	}
	name := "details_sum.html"
	if d.Grouped {
		name = "details_group.html"
	}
	return detailsTemplates.ExecuteTemplate(w, name, &htmlData{Details: d, tr: tr})
}

// HTMLDetails returns the HTML of stored score details (score_details or
// public_score_details) shown with a feedback level: CMS's
// get_html_details. Details that are NULL or cannot be read give the
// translation of MsgUnavailable, as in CMS.
func HTMLDetails(st ScoreType, raw json.RawMessage, level FeedbackLevel, tr Translation) template.HTML {
	unavailable := template.HTML(tr.T(MsgUnavailable))
	d, err := st.JSONDetails(raw, level)
	if err != nil || d == nil {
		return unavailable
	}
	var b bytes.Buffer
	if err := RenderDetails(&b, d, tr); err != nil {
		return unavailable
	}
	return template.HTML(b.String())
}

// FormatScore is the score of a submission as CMS shows it in the
// contestant's tables (ScoreType.format_score): "score / max_score" with
// the decimal format of the language.
func FormatScore(score, maxScore float64, tr Translation) string {
	return tr.FormatDecimal(score) + " / " + tr.FormatDecimal(maxScore)
}
