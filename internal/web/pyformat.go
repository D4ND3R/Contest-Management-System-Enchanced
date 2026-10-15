// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package web

import (
	"errors"
	"fmt"
	"html/template"
	"math"
	"reflect"
	"strconv"
	"strings"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/i18n"
)

// errPyFormat is returned by PyFormat where Python raises TypeError or
// ValueError.
var errPyFormat = errors.New("web: bad %-format")

// PyFormat applies Python's printf-style operator with positional
// arguments (msg % tuple(args)): CMS formats status texts, notifications
// and file names this way ("Evaluation didn't produce file %s",
// "%s.png"|format(icon)). Supported: flags "-+ 0#", width, precision and
// the conversions s r a d i u x X o e E f F g G c and "%%". It fails, as
// Python does, when the number of arguments does not match or a numeric
// conversion gets something that is not a number (status texts are
// strings, so "%d" with a text argument fails in CMS too).
func PyFormat(format string, args ...any) (string, error) {
	var b strings.Builder
	next := 0
	for i := 0; i < len(format); i++ {
		c := format[i]
		if c != '%' {
			b.WriteByte(c)
			continue
		}
		i++
		if i >= len(format) {
			return "", fmt.Errorf("%w: incomplete format", errPyFormat)
		}
		if format[i] == '(' {
			// Mapping keys need a mapping, not positional arguments.
			return "", fmt.Errorf("%w: format requires a mapping", errPyFormat)
		}
		var flags string
		for i < len(format) && strings.IndexByte("-+ 0#", format[i]) >= 0 {
			flags += string(format[i])
			i++
		}
		width, prec := -1, -1
		readNum := func() (int, error) {
			if i < len(format) && format[i] == '*' {
				i++
				if next >= len(args) {
					return 0, fmt.Errorf("%w: not enough arguments", errPyFormat)
				}
				n, ok := asInt(args[next])
				next++
				if !ok {
					return 0, fmt.Errorf("%w: * wants int", errPyFormat)
				}
				return int(n), nil
			}
			start := i
			for i < len(format) && format[i] >= '0' && format[i] <= '9' {
				i++
			}
			if start == i {
				return -1, nil
			}
			return strconv.Atoi(format[start:i])
		}
		var err error
		if width, err = readNum(); err != nil {
			return "", err
		}
		if width < -1 { // "*" with a negative value: left-justify
			flags += "-"
			width = -width
		}
		if i < len(format) && format[i] == '.' {
			i++
			if prec, err = readNum(); err != nil {
				return "", err
			}
			if prec < 0 {
				prec = 0
			}
		}
		// Length modifiers are accepted and ignored, as in Python.
		for i < len(format) && strings.IndexByte("hlL", format[i]) >= 0 {
			i++
		}
		if i >= len(format) {
			return "", fmt.Errorf("%w: incomplete format", errPyFormat)
		}
		verb := format[i]
		if verb == '%' {
			b.WriteByte('%')
			continue
		}
		if next >= len(args) {
			return "", fmt.Errorf("%w: not enough arguments for format string", errPyFormat)
		}
		arg := args[next]
		next++
		s, err := pyConvert(verb, flags, prec, arg)
		if err != nil {
			return "", err
		}
		b.WriteString(pad(s, flags, width, verb))
	}
	if next < len(args) {
		return "", fmt.Errorf("%w: not all arguments converted during string formatting", errPyFormat)
	}
	return b.String(), nil
}

func pyConvert(verb byte, flags string, prec int, arg any) (string, error) {
	switch verb {
	case 's', 'r', 'a':
		var s string
		if verb == 's' {
			s = pyStr(arg)
		} else {
			s = pyRepr(arg)
		}
		if prec >= 0 && prec < len([]rune(s)) {
			s = string([]rune(s)[:prec])
		}
		return s, nil
	case 'd', 'i', 'u':
		f, ok := asFloat(arg)
		if !ok {
			return "", fmt.Errorf("%w: %%%c format: a real number is required", errPyFormat, verb)
		}
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return "", fmt.Errorf("%w: cannot convert float to integer", errPyFormat)
		}
		n, isInt := asInt(arg)
		if !isInt {
			n = int64(f) // truncation toward zero, as int()
		}
		return sign(strconv.FormatInt(abs64(n), 10), n < 0, flags), nil
	case 'x', 'X', 'o':
		n, ok := asInt(arg)
		if !ok {
			return "", fmt.Errorf("%w: %%%c format: an integer is required", errPyFormat, verb)
		}
		base := 16
		if verb == 'o' {
			base = 8
		}
		s := strconv.FormatInt(abs64(n), base)
		if strings.Contains(flags, "#") {
			s = "0" + string(verb) + s
		}
		if verb == 'X' {
			s = strings.ToUpper(s)
		}
		return sign(s, n < 0, flags), nil
	case 'e', 'E', 'f', 'F', 'g', 'G':
		f, ok := asFloat(arg)
		if !ok {
			return "", fmt.Errorf("%w: must be real number", errPyFormat)
		}
		if prec < 0 {
			prec = 6
		}
		var s string
		switch verb {
		case 'g', 'G':
			if prec == 0 {
				prec = 1
			}
			if !strings.Contains(flags, "#") {
				// Go writes e+06 with two exponent digits like Python and
				// drops the trailing zeros as Python's %g does.
				s = strconv.FormatFloat(math.Abs(f), 'g', prec, 64)
				break
			}
			// Alternate form: the trailing zeros stay.
			e := strconv.FormatFloat(math.Abs(f), 'e', prec-1, 64)
			exp, _ := strconv.Atoi(e[strings.IndexByte(e, 'e')+1:])
			if exp < -4 || exp >= prec {
				s = e
			} else {
				s = strconv.FormatFloat(math.Abs(f), 'f', prec-1-exp, 64)
			}
			if !strings.Contains(s, ".") {
				if i := strings.IndexByte(s, 'e'); i >= 0 {
					s = s[:i] + "." + s[i:]
				} else {
					s += "."
				}
			}
		default:
			s = strconv.FormatFloat(math.Abs(f), verb|0x20, prec, 64)
		}
		if math.IsInf(f, 0) {
			s = "inf"
		} else if math.IsNaN(f) {
			s = "nan"
		}
		if verb == 'E' || verb == 'G' || verb == 'F' {
			s = strings.ToUpper(s)
		}
		return sign(s, math.Signbit(f) && !math.IsNaN(f), flags), nil
	case 'c':
		if n, ok := asInt(arg); ok {
			return string(rune(n)), nil
		}
		if s, ok := arg.(string); ok && len([]rune(s)) == 1 {
			return s, nil
		}
		return "", fmt.Errorf("%w: %%c requires int or char", errPyFormat)
	}
	return "", fmt.Errorf("%w: unsupported format character %q", errPyFormat, verb)
}

func sign(digits string, neg bool, flags string) string {
	switch {
	case neg:
		return "-" + digits
	case strings.Contains(flags, "+"):
		return "+" + digits
	case strings.Contains(flags, " "):
		return " " + digits
	}
	return digits
}

func pad(s, flags string, width int, verb byte) string {
	n := len([]rune(s))
	if width <= n {
		return s
	}
	fill := width - n
	switch {
	case strings.Contains(flags, "-"):
		return s + strings.Repeat(" ", fill)
	case strings.Contains(flags, "0") && strings.IndexByte("diuxXoeEfFgG", verb) >= 0:
		// Zeros go after the sign (and the 0x prefix).
		prefix := ""
		if s != "" && strings.IndexByte("+- ", s[0]) >= 0 {
			prefix, s = s[:1], s[1:]
		}
		if len(s) >= 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X' || s[1] == 'o') {
			prefix, s = prefix+s[:2], s[2:]
		}
		return prefix + strings.Repeat("0", fill) + s
	}
	return strings.Repeat(" ", fill) + s
}

func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// asInt returns integer kinds (and bools, as Python) as int64.
func asInt(v any) (int64, bool) {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int(), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return int64(rv.Uint()), true
	case reflect.Bool:
		if rv.Bool() {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}

// asFloat returns numeric kinds as float64; strings are not numbers.
func asFloat(v any) (float64, bool) {
	if n, ok := asInt(v); ok {
		return float64(n), true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Float32, reflect.Float64:
		return rv.Float(), true
	}
	return 0, false
}

// pyStr formats a value as Python's str() would for the types templates
// pass: floats keep a ".0" when integral (str(2.0) == "2.0").
func pyStr(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case string:
		return x
	case bool:
		if x {
			return "True"
		}
		return "False"
	case float64:
		return pyFloatStr(x)
	case float32:
		return pyFloatStr(float64(x))
	case fmt.Stringer:
		return x.String()
	case template.HTML:
		return string(x)
	}
	return fmt.Sprint(v)
}

// pyFloatStr is Python's repr of a float: the shortest digits that
// round-trip, positional between 1e-4 and 1e16 (with ".0" when
// integral), exponent notation with at least two exponent digits
// outside.
func pyFloatStr(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	case math.IsNaN(f):
		return "nan"
	}
	if a := math.Abs(f); a != 0 && (a >= 1e16 || a < 1e-4) {
		s := strconv.FormatFloat(f, 'e', -1, 64)
		mant, exp, _ := strings.Cut(s, "e")
		sign := exp[0]
		exp = strings.TrimLeft(exp[1:], "0")
		if len(exp) < 2 {
			exp = strings.Repeat("0", 2-len(exp)) + exp
		}
		return mant + "e" + string(sign) + exp
	}
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}

func pyRepr(v any) string {
	if s, ok := v.(string); ok {
		q := "'"
		if strings.Contains(s, "'") && !strings.Contains(s, `"`) {
			q = `"`
		}
		r := strings.NewReplacer(`\`, `\\`, "\n", `\n`, "\r", `\r`, "\t", `\t`, q, `\`+q).Replace(s)
		return q + r + q
	}
	return pyStr(v)
}

// FormatNamed fills the "%(name)s" placeholders of msg with the
// key/value pairs kv (i18n.Format with the arguments of a template call).
func FormatNamed(msg string, kv ...any) string {
	return i18n.Format(msg, pairsToMap(kv))
}

// FormatHTML is FormatNamed with the semantics of Jinja's {% trans %}
// blocks under autoescaping, for messages that contain markup such as
// "Logged in as <strong>%(first_name)s ...</strong>": a template.HTML
// msg (what T returns) is trusted markup from the catalogs, while a plain
// string msg is escaped first; the values are HTML-escaped unless they
// are template.HTML, and numbers stay numbers for %(n)d.
func FormatHTML(msg any, kv ...any) template.HTML {
	var text string
	switch m := msg.(type) {
	case template.HTML:
		text = string(m)
	default:
		text = template.HTMLEscapeString(pyStr(deref(msg)))
	}
	m := pairsToMap(kv)
	for k, v := range m {
		v = deref(v)
		switch x := v.(type) {
		case template.HTML:
			m[k] = string(x)
		case nil:
			m[k] = "None"
		default:
			if _, isNum := asFloat(x); isNum {
				if _, isBool := x.(bool); !isBool {
					m[k] = x
					continue
				}
			}
			m[k] = template.HTMLEscapeString(pyStr(x))
		}
	}
	return template.HTML(i18n.Format(text, m))
}

func pairsToMap(kv []any) map[string]any {
	m := make(map[string]any, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		m[fmt.Sprint(kv[i])] = kv[i+1]
	}
	return m
}
