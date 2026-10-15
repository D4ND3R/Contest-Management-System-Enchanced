// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// The functions mirror the Jinja2 environment of CMS,
// cms/server/jinja2_toolbox.py:
// Copyright © 2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2018 Stefano Maggiolo <s.maggiolo@gmail.com>

package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"math"
	"reflect"
	"sort"
	"strings"
	"time"
)

// Test is a Jinja test for all, any and dictselect: whether v passes,
// given the extra arguments of the call. rc is the request state (the
// "today" test needs it).
type Test func(rc *Render, v any, args ...any) (bool, error)

var errTestArgs = errors.New("web: wrong number of test arguments")

// builtinTests are the tests CMS registers plus the Jinja built-in ones
// its templates use with all/any/dictselect.
var builtinTests = map[string]Test{
	"today": func(rc *Render, v any, _ ...any) (bool, error) {
		t, ok := asTime(v)
		if !ok {
			return false, fmt.Errorf("web: today: not a time: %T", v)
		}
		return sameDay(t, rc.Now, rc.tz()), nil
	},
	"contains": func(_ *Render, v any, args ...any) (bool, error) {
		if len(args) != 1 {
			return false, errTestArgs
		}
		return containsValue(v, args[0])
	},
	"endswith": func(_ *Render, v any, args ...any) (bool, error) {
		if len(args) != 1 {
			return false, errTestArgs
		}
		return strings.HasSuffix(pyStr(v), pyStr(args[0])), nil
	},
	"in": func(_ *Render, v any, args ...any) (bool, error) {
		if len(args) != 1 {
			return false, errTestArgs
		}
		return containsValue(args[0], v)
	},
	"none":    func(_ *Render, v any, _ ...any) (bool, error) { return isNil(v), nil },
	"defined": func(_ *Render, v any, _ ...any) (bool, error) { return !isNil(v), nil },
	"true":    func(_ *Render, v any, _ ...any) (bool, error) { b, ok := v.(bool); return ok && b, nil },
	"false":   func(_ *Render, v any, _ ...any) (bool, error) { b, ok := v.(bool); return ok && !b, nil },
	"eq": func(_ *Render, v any, args ...any) (bool, error) {
		if len(args) != 1 {
			return false, errTestArgs
		}
		return looseEqual(v, args[0]), nil
	},
	"ne": func(_ *Render, v any, args ...any) (bool, error) {
		if len(args) != 1 {
			return false, errTestArgs
		}
		return !looseEqual(v, args[0]), nil
	},
}

func init() {
	builtinTests["equalto"] = builtinTests["eq"]
	builtinTests["=="] = builtinTests["eq"]
	builtinTests["!="] = builtinTests["ne"]
}

// funcs returns the template functions bound to cur (see Engine).
//
// Request state (Render):
//
//	T "msgid"                 translation (template.HTML: catalog text is
//	Tn "one" "many" n         trusted markup, as Jinja's {% trans %})
//	Tc "context" "msgid"
//	lang                      identifier of the language (<html lang>)
//	translation               the *Lang
//	now, timezone             request time, server time zone
//	url "a" "b"               relative URL below the root (CMS url())
//	static "css/x.css"        static URL with ?h=<hash> (CMS static_url)
//	nonce                     CSP nonce for <script nonce="…">
//	xsrf_form_html            hidden _xsrf input (template.HTML)
//	xsrf_token                the masked token
//	format_datetime t, format_time t, format_datetime_smart t,
//	format_timedelta d, format_duration seconds ["long"], format_size n,
//	format_decimal x, format_locale code, format_status_text status,
//	today t, mime_name mimetype (CMS translation.translate_mimetype),
//	all list ["test" args…], any list ["test" args…],
//	dictselect map ["test" args…] ["by=value"]
//
// No request state:
//
//	Format msg "name" value…  "%(name)s" placeholders; msg is trusted if
//	                          it is template.HTML (T's result), values
//	                          are escaped; returns template.HTML
//	format "%s.png" x         Python's "%" with positional arguments
//	markdown text             CMS markdown filter (template.HTML)
//	make_timestamp t          seconds since the epoch (float)
//	mimetype filename         MIME type from the name ("" if unknown)
//	mime_icon mimetype        generic icon name (img/mimetypes/<icon>.png)
//	dict "k" v…, list a b…, add, sub, mul, div, join sep list,
//	replace old new s, default def v, tojson v
func (e *Engine) funcs(cur func() *Render) template.FuncMap {
	rcOf := func() *Render {
		if rc := cur(); rc != nil {
			return rc
		}
		return zeroRender()
	}
	fm := template.FuncMap{
		"T": func(msgid string) template.HTML {
			return template.HTML(rcOf().lang().T(msgid))
		},
		"Tn": func(msgid, plural string, n any) template.HTML {
			return template.HTML(rcOf().lang().Tn(msgid, plural, int(toInt64(n))))
		},
		"Tc": func(ctx, msgid string) template.HTML {
			return template.HTML(rcOf().lang().Tc(ctx, msgid))
		},
		"Format": FormatHTML,
		"lang": func() string {
			if l := rcOf().lang(); l != nil {
				return l.ID
			}
			return "en"
		},
		"translation": func() *Lang { return rcOf().lang() },
		"now":         func() time.Time { return rcOf().Now },
		"timezone":    func() *time.Location { return rcOf().tz() },
		"url": func(components ...any) string {
			return rcOf().root().Join(components...)
		},
		"static": func(parts ...string) string {
			return e.static.URL(rcOf().root(), strings.Join(parts, "/"))
		},
		"nonce": func() string { return rcOf().Nonce },
		"xsrf_form_html": func() template.HTML {
			tok := rcOf().XSRFToken()
			if tok == "" {
				return ""
			}
			// Tornado's xsrf_form_html, byte for byte.
			return template.HTML(`<input type="hidden" name="_xsrf" value="` + template.HTMLEscapeString(tok) + `"/>`)
		},
		"xsrf_token": func() string { return rcOf().XSRFToken() },
		"format_datetime": func(v any) string {
			t, ok := asTime(v)
			if !ok {
				return ""
			}
			rc := rcOf()
			return rc.locale().FormatDatetime(t, rc.tz())
		},
		"format_time": func(v any) string {
			t, ok := asTime(v)
			if !ok {
				return ""
			}
			rc := rcOf()
			return rc.locale().FormatTime(t, rc.tz())
		},
		"format_datetime_smart": func(v any) string {
			t, ok := asTime(v)
			if !ok {
				return ""
			}
			rc := rcOf()
			return rc.locale().FormatDatetimeSmart(t, rc.Now, rc.tz())
		},
		"format_timedelta": func(v any) string {
			d, ok := asDuration(v)
			if !ok {
				return ""
			}
			return rcOf().locale().FormatTimedelta(d)
		},
		"format_duration": func(v any, length ...string) string {
			s, ok := asSeconds(v)
			if !ok {
				return ""
			}
			l := "short"
			if len(length) > 0 && length[0] != "" {
				l = length[0]
			}
			return rcOf().locale().FormatDuration(s, l)
		},
		"format_size": func(v any) string {
			if isNil(v) {
				return ""
			}
			return rcOf().locale().FormatSize(toInt64(v))
		},
		"format_decimal": func(v any) string {
			if isNil(v) {
				return ""
			}
			f, _ := asFloat(deref(v))
			return rcOf().locale().FormatDecimal(f)
		},
		"format_locale": func(code string) string { return rcOf().locale().FormatLocale(code) },
		"format_status_text": func(status any) string {
			return FormatStatusText(toStrings(status), rcOf().lang())
		},
		"today": func(v any) (bool, error) {
			return builtinTests["today"](rcOf(), v)
		},
		"mime_name": func(mimetype string) string {
			id := "en"
			if l := rcOf().lang(); l != nil {
				id = l.ID
			}
			return MimeComment(mimetype, id)
		},
		"all": func(list any, args ...any) (bool, error) {
			return e.quantify(rcOf(), list, true, args)
		},
		"any": func(list any, args ...any) (bool, error) {
			return e.quantify(rcOf(), list, false, args)
		},
		"dictselect": func(d any, args ...any) (any, error) {
			return e.dictselect(rcOf(), d, args)
		},

		"format":         PyFormat,
		"markdown":       func(text any) template.HTML { return Markdown(strOf(text)) },
		"make_timestamp": MakeTimestamp,
		"mimetype":       MimeType,
		"mime_icon":      MimeIcon,
		"dict":           dict,
		"list":           func(items ...any) []any { return items },
		"add":            func(a, b any) (any, error) { return arith(a, b, '+') },
		"sub":            func(a, b any) (any, error) { return arith(a, b, '-') },
		"mul":            func(a, b any) (any, error) { return arith(a, b, '*') },
		"div":            func(a, b any) (any, error) { return arith(a, b, '/') },
		"join": func(sep string, list any) string {
			var parts []string
			_ = iterate(list, func(v any) error { parts = append(parts, pyStr(v)); return nil })
			return strings.Join(parts, sep)
		},
		"replace": func(old, new string, s any) string { return strings.ReplaceAll(strOf(s), old, new) },
		"default": func(def, v any) any {
			if isNil(v) {
				return def
			}
			return v
		},
		"tojson": func(v any) (template.JS, error) {
			b, err := json.Marshal(v)
			return template.JS(b), err
		},
	}
	if e.siteFns != nil {
		for k, v := range e.siteFns(cur) {
			fm[k] = v
		}
	}
	return fm
}

// quantify implements all (every=true) and any.
func (e *Engine) quantify(rc *Render, list any, every bool, args []any) (bool, error) {
	test, targs, err := e.testOf(args)
	if err != nil {
		return false, err
	}
	result := every
	stop := errors.New("stop")
	err = iterate(list, func(v any) error {
		ok, err := e.apply(rc, test, v, targs)
		if err != nil {
			return err
		}
		if ok != every {
			result = !every
			return stop
		}
		return nil
	})
	if err != nil && err != stop {
		return false, err
	}
	return result, nil
}

// dictselect keeps the items of a map whose key (or value, with a
// trailing "by=value" argument) passes the test (CMS dictselect).
func (e *Engine) dictselect(rc *Render, d any, args []any) (any, error) {
	by := "key"
	if n := len(args); n > 0 {
		if s, ok := args[n-1].(string); ok && strings.HasPrefix(s, "by=") {
			by = strings.TrimPrefix(s, "by=")
			args = args[:n-1]
		}
	}
	if by != "key" && by != "value" {
		return nil, fmt.Errorf("web: dictselect: invalid value of \"by\": %s", by)
	}
	test, targs, err := e.testOf(args)
	if err != nil {
		return nil, err
	}
	m := reflect.ValueOf(d)
	if m.Kind() != reflect.Map {
		return nil, fmt.Errorf("web: dictselect: not a map: %T", d)
	}
	out := reflect.MakeMapWithSize(m.Type(), m.Len())
	it := m.MapRange()
	for it.Next() {
		v := it.Key()
		if by == "value" {
			v = it.Value()
		}
		ok, err := e.apply(rc, test, v.Interface(), targs)
		if err != nil {
			return nil, err
		}
		if ok {
			out.SetMapIndex(it.Key(), it.Value())
		}
	}
	return out.Interface(), nil
}

func (e *Engine) testOf(args []any) (Test, []any, error) {
	if len(args) == 0 {
		return nil, nil, nil
	}
	name, ok := args[0].(string)
	if !ok {
		return nil, nil, fmt.Errorf("web: test name must be a string, not %T", args[0])
	}
	t := e.tests[name]
	if t == nil {
		return nil, nil, fmt.Errorf("web: no test named %q", name)
	}
	return t, args[1:], nil
}

func (e *Engine) apply(rc *Render, t Test, v any, args []any) (bool, error) {
	if t == nil {
		ok, _ := template.IsTrue(v)
		return ok, nil
	}
	return t(rc, v, args...)
}

// iterate calls fn for the elements of a slice or array, or the keys of
// a map in sorted order (Python iterates dict keys).
func iterate(list any, fn func(any) error) error {
	v := reflect.ValueOf(list)
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			if err := fn(v.Index(i).Interface()); err != nil {
				return err
			}
		}
	case reflect.Map:
		keys := v.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return lessValue(keys[i], keys[j]) })
		for _, k := range keys {
			if err := fn(k.Interface()); err != nil {
				return err
			}
		}
	case reflect.Invalid:
		return nil
	default:
		return fmt.Errorf("web: cannot iterate over %s", v.Type())
	}
	return nil
}

// lessValue orders map keys: numbers by value, the rest by their text.
func lessValue(a, b reflect.Value) bool {
	fa, okA := asFloat(a.Interface())
	fb, okB := asFloat(b.Interface())
	if okA && okB {
		return fa < fb
	}
	return pyStr(a.Interface()) < pyStr(b.Interface())
}

// containsValue is Python's "item in container": substring for strings,
// key for maps, element for slices.
func containsValue(container, item any) (bool, error) {
	v := reflect.ValueOf(container)
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return false, nil
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.String:
		return strings.Contains(v.String(), pyStr(item)), nil
	case reflect.Map:
		for _, k := range v.MapKeys() {
			if looseEqual(k.Interface(), item) {
				return true, nil
			}
		}
		return false, nil
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			if looseEqual(v.Index(i).Interface(), item) {
				return true, nil
			}
		}
		return false, nil
	case reflect.Invalid:
		return false, nil
	}
	return false, fmt.Errorf("web: \"in\" needs a string, map or list, not %s", v.Type())
}

// looseEqual compares as Python does for the values templates use:
// numbers by value, string types by content.
func looseEqual(a, b any) bool {
	a, b = deref(a), deref(b)
	if fa, ok := asFloat(a); ok {
		if fb, ok := asFloat(b); ok {
			return fa == fb
		}
		return false
	}
	ra, rb := reflect.ValueOf(a), reflect.ValueOf(b)
	if ra.Kind() == reflect.String && rb.Kind() == reflect.String {
		return ra.String() == rb.String()
	}
	return reflect.DeepEqual(a, b)
}

// isNil reports whether v is nil or a nil pointer, map, slice or
// interface (Python's None).
func isNil(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Interface, reflect.Func, reflect.Chan:
		return rv.IsNil()
	}
	return false
}

// strOf is the text of a value for string functions: "" for nil.
func strOf(v any) string {
	v = deref(v)
	if v == nil {
		return ""
	}
	return pyStr(v)
}

// deref follows pointers (nil stays nil).
func deref(v any) any {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}
	if !rv.IsValid() {
		return nil
	}
	return rv.Interface()
}

func toInt64(v any) int64 {
	v = deref(v)
	if n, ok := asInt(v); ok {
		return n
	}
	if f, ok := asFloat(v); ok {
		return int64(f)
	}
	return 0
}

// asTime accepts time.Time and *time.Time.
func asTime(v any) (time.Time, bool) {
	switch x := v.(type) {
	case time.Time:
		return x, true
	case *time.Time:
		if x != nil {
			return *x, true
		}
	}
	return time.Time{}, false
}

func sameDay(a, b time.Time, tz *time.Location) bool {
	ya, ma, da := a.In(tz).Date()
	yb, mb, db := b.In(tz).Date()
	return ya == yb && ma == mb && da == db
}

// asDuration accepts time.Duration (and pointers) and numbers of
// seconds.
func asDuration(v any) (time.Duration, bool) {
	v = deref(v)
	if d, ok := v.(time.Duration); ok {
		return d, true
	}
	if f, ok := asFloat(v); ok {
		return time.Duration(math.Round(f * float64(time.Second))), true
	}
	return 0, false
}

// asSeconds accepts numbers of seconds and time.Duration.
func asSeconds(v any) (float64, bool) {
	v = deref(v)
	if d, ok := v.(time.Duration); ok {
		return d.Seconds(), true
	}
	return asFloat(v)
}

// Timestamp is a number of seconds since the Unix epoch, printed as
// Python prints the float of CMS's make_timestamp ("1792020600.0",
// "1792020600.000123"), in text and as a number in <script>.
type Timestamp float64

// String implements fmt.Stringer (Python's repr of a float).
func (t Timestamp) String() string { return pyFloatStr(float64(t)) }

// MarshalJSON writes the number as String does (html/template uses it
// inside scripts).
func (t Timestamp) MarshalJSON() ([]byte, error) { return []byte(t.String()), nil }

// MakeTimestamp returns the seconds since the Unix epoch of a time, with
// microseconds (CMS make_timestamp; the JavaScript of the pages reads
// it).
func MakeTimestamp(v any) (Timestamp, error) {
	t, ok := asTime(v)
	if !ok {
		return 0, fmt.Errorf("web: make_timestamp: not a time: %T", v)
	}
	return Timestamp(float64(t.Unix()) + float64(t.Nanosecond()/1000)/1e6), nil
}

// toStrings converts a status text ([]string, []any, *[]string) to
// []string; anything else is nil (formatted as "N/A").
func toStrings(v any) []string {
	switch x := deref(v).(type) {
	case []string:
		return x
	case []any:
		out := make([]string, len(x))
		for i, e := range x {
			out[i] = pyStr(e)
		}
		return out
	}
	return nil
}

func dict(kv ...any) (map[string]any, error) {
	if len(kv)%2 != 0 {
		return nil, errors.New("web: dict needs key/value pairs")
	}
	m := make(map[string]any, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		k, ok := kv[i].(string)
		if !ok {
			return nil, fmt.Errorf("web: dict key must be a string, not %T", kv[i])
		}
		m[k] = kv[i+1]
	}
	return m, nil
}

// arith adds, subtracts, multiplies or divides two numbers: integers
// give an int (except division), anything else a float64.
func arith(a, b any, op byte) (any, error) {
	a, b = deref(a), deref(b)
	ia, aInt := asInt(a)
	ib, bInt := asInt(b)
	if aInt && bInt && op != '/' {
		switch op {
		case '+':
			return int(ia + ib), nil
		case '-':
			return int(ia - ib), nil
		case '*':
			return int(ia * ib), nil
		}
	}
	fa, ok1 := asFloat(a)
	fb, ok2 := asFloat(b)
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("web: arithmetic on %T and %T", a, b)
	}
	switch op {
	case '+':
		return fa + fb, nil
	case '-':
		return fa - fb, nil
	case '*':
		return fa * fb, nil
	}
	if fb == 0 {
		return nil, errors.New("web: division by zero")
	}
	return fa / fb, nil
}
