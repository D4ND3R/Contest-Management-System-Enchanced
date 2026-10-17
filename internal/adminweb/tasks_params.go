// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// The forms of the task type parameters are ported from the TEMPLATEs of
// cms/grading/ParameterTypes.py of CMS:
// Copyright © 2010-2012 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2012-2014 Luca Wehrstedt <luca.wehrstedt@gmail.com>

package adminweb

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/scoretype"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/tasktype"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/web"
)

// taskTypeOptions is the table of parameters of one task type in the
// dataset forms (task.html, add_dataset.html): CMS's loop over
// TASK_TYPES with ParameterType.render.
type taskTypeOptions struct {
	Name string
	Rows []paramRow
}

// paramRow is a parameter: its (translated) name and its form snippet.
type paramRow struct {
	Name string
	HTML template.HTML
}

// taskTypeNames are the task types in CMS's order (its plugin list,
// alphabetical).
func taskTypeNames() []string {
	names := slices.Clone(tasktype.Names())
	sort.Strings(names)
	return names
}

// scoreTypeNames are the score types in CMS's order (alphabetical:
// GroupMin, GroupMul, GroupThreshold, Sum).
func scoreTypeNames() []string {
	names := slices.Clone(scoretype.Names())
	sort.Strings(names)
	return names
}

// taskTypeForms renders the parameter tables of every task type, with the
// values of current (the dataset's parameters, nil: none) for the task
// type selected; prefix is "TaskTypeOptions_<dataset>_" (task page) or
// "TaskTypeOptions_" (new dataset).
func taskTypeForms(l *web.Lang, selected string, current json.RawMessage, prefix string) []taskTypeOptions {
	var values []json.RawMessage
	if len(current) > 0 {
		if err := json.Unmarshal(current, &values); err != nil {
			values = nil
		}
	}
	var out []taskTypeOptions
	for _, name := range taskTypeNames() {
		params, err := tasktype.Params(name)
		if err != nil {
			continue
		}
		opts := taskTypeOptions{Name: name}
		for i, p := range params {
			var v json.RawMessage
			if name == selected && values != nil && i < len(values) {
				v = values[i]
			}
			opts.Rows = append(opts.Rows, paramRow{
				Name: string(l.T(p.Name)),
				HTML: template.HTML(renderParam(l, &p, prefix+name+"_", v)),
			})
		}
		out = append(out, opts)
	}
	return out
}

// renderParam is ParameterType.render: the form snippet of a parameter
// with its previous value (nil is Python's None, which CMS prints as
// "None" in the text fields).
func renderParam(l *web.Lang, p *tasktype.Param, prefix string, prev json.RawMessage) string {
	name := html.EscapeString(prefix + p.ShortName)
	var b strings.Builder
	switch p.Kind {
	case tasktype.ParamBool:
		b.WriteString("\n<input type=\"checkbox\"\n       name=\"" + name + "\"\n")
		if pyTruthy(prev) {
			b.WriteString("checked")
		}
		b.WriteString(" />")
	case tasktype.ParamChoice:
		b.WriteString("\n<select name=\"" + name + "\">\n")
		cur, isString := jsonString(prev)
		for _, c := range p.Choices {
			b.WriteString("  <option value=\"" + html.EscapeString(c.Value) + "\"\n")
			if isString && cur == c.Value {
				b.WriteString("selected")
			}
			b.WriteString(">\n    " + html.EscapeString(string(l.T(c.Description))) + "\n  </option>\n")
		}
		b.WriteString("</select>")
	case tasktype.ParamCollection:
		var items []json.RawMessage
		if len(prev) > 0 && json.Unmarshal(prev, &items) != nil {
			items = nil
		}
		b.WriteString("\n<table>\n")
		for i := range p.Params {
			var v json.RawMessage
			if i < len(items) {
				v = items[i]
			}
			sub := fmt.Sprintf("%s%s_%d_", prefix, p.ShortName, i)
			b.WriteString("  <tr>\n    <td>" + html.EscapeString(string(l.T(p.Params[i].Name))) + "</td>\n    <td>")
			b.WriteString(renderParam(l, &p.Params[i], sub, v))
			b.WriteString("</td>\n  </tr>\n")
		}
		b.WriteString("</table>")
	default:
		b.WriteString("\n<input type=\"text\"\n       name=\"" + name + "\"\n       value=\"" +
			html.EscapeString(pyStr(prev)) + "\" />")
	}
	return b.String()
}

// jsonString decodes a JSON string.
func jsonString(raw json.RawMessage) (string, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '"' {
		return "", false
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

// pyTruthy is Python's bool() of a JSON value (None is false).
func pyTruthy(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	switch {
	case len(raw) == 0, string(raw) == "null", string(raw) == "false":
		return false
	case raw[0] == '"':
		s, _ := jsonString(raw)
		return s != ""
	case raw[0] == '[':
		return string(raw) != "[]" && !isEmptyJSON(raw, '[')
	case raw[0] == '{':
		return !isEmptyJSON(raw, '{')
	case string(raw) == "true":
		return true
	}
	f, err := strconv.ParseFloat(string(raw), 64)
	return err != nil || f != 0
}

func isEmptyJSON(raw json.RawMessage, open byte) bool {
	s := strings.TrimSpace(string(raw[1 : len(raw)-1]))
	return raw[0] == open && s == ""
}

// pyStr is Python's str() of a JSON value as json.loads gives it ("None"
// for nothing, floats as Python prints them).
func pyStr(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	switch {
	case len(raw) == 0 || string(raw) == "null":
		return "None"
	case string(raw) == "true":
		return "True"
	case string(raw) == "false":
		return "False"
	case raw[0] == '"':
		s, _ := jsonString(raw)
		return s
	case raw[0] == '[' || raw[0] == '{':
		return pyJSON(raw)
	}
	return pyNumber(string(raw))
}

// pyNumber prints a JSON number as Python prints what json.loads made of
// it: integers as they are, others as floats ("1.0", "1e-05").
func pyNumber(s string) string {
	if !strings.ContainsAny(s, ".eE") {
		return s
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return s
	}
	return job.FormatOutcome(f)
}

// pyJSON is Python's json.dumps(json.loads(raw), sort_keys=True) (Jinja's
// tojson): ", " and ": " separators, sorted keys, non-ASCII characters
// as \uXXXX, floats as Python prints them. Invalid JSON is returned as
// it is.
func pyJSON(raw json.RawMessage) string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return string(raw)
	}
	var b strings.Builder
	writePyJSON(&b, v)
	return b.String()
}

func writePyJSON(b *strings.Builder, v any) {
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(x))
	case json.Number:
		s := pyNumber(x.String())
		switch s {
		case "inf":
			s = "Infinity"
		case "-inf":
			s = "-Infinity"
		case "nan":
			s = "NaN"
		}
		b.WriteString(s)
	case string:
		writePyString(b, x)
	case []any:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteString(", ")
			}
			writePyJSON(b, e)
		}
		b.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteString(", ")
			}
			writePyString(b, k)
			b.WriteString(": ")
			writePyJSON(b, x[k])
		}
		b.WriteByte('}')
	}
}

// writePyString writes a JSON string as json.dumps with ensure_ascii.
func writePyString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			switch {
			case r > 0xffff:
				r1, r2 := utf16Pair(r)
				fmt.Fprintf(b, `\u%04x\u%04x`, r1, r2)
			case r < 0x20 || r > 0x7e:
				fmt.Fprintf(b, `\u%04x`, r)
			default:
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

func utf16Pair(r rune) (rune, rune) {
	if r > utf8.MaxRune {
		r = utf8.RuneError
	}
	r -= 0x10000
	return 0xd800 + (r>>10)&0x3ff, 0xdc00 + r&0x3ff
}

// toJSONForceEscape is Jinja's x|tojson|forceescape: the JSON with <, >,
// & and ' as \u escapes, then HTML-escaped (quotes become &#34;).
func toJSONForceEscape(raw json.RawMessage) string {
	s := pyJSON(raw)
	s = strings.NewReplacer("<", `<`, ">", `>`, "&", `&`, "'", `'`).Replace(s)
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&#34;", "'", "&#39;").Replace(s)
}

// pyFloatStr is str(float) for a column that may be NULL ("" then).
func pyFloatStr(f *float64) string {
	if f == nil {
		return ""
	}
	return job.FormatOutcome(*f)
}

// paramTexts are the texts of the task type parameters (internal/grading/
// tasktype), translated in the forms; listed here so that `make generate`
// extracts them.
var paramTexts = []string{
	N("Compilation"),
	N("Submissions are self-sufficient"),
	N("Submissions are compiled with a grader"),
	N("I/O (blank for stdin/stdout)"),
	N("Input file"),
	N("Output file"),
	N("Output evaluation"),
	N("Outputs compared with white diff"),
	N("Outputs are compared by a comparator"),
	N("Outputs compared byte by byte"),
	N("Outputs compared token by token, numbers with a tolerance of 1e-6"),
	N("Outputs are checked by a testlib checker"),
	N("Comma-separated list of output only testcases"),
	N("Number of Processes"),
	N("User I/O"),
	N("User processes read from stdin and write to stdout"),
	N("User processes read from and write to fifos, whose paths are given as arguments"),
	N("Process limit"),
	N("Concurrent solutions"),
	N("Controller memory limit (MB)"),
	N("Controller time limit (s)"),
	N("Controller wall time limit (s)"),
}
