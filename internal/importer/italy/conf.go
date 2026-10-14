// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from the helpers of cmscontrib/loaders/italy_yaml.py of CMS:
// Copyright © 2010-2014 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2013-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>

package italy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
)

// maxYAML bounds the size of contest.yaml and task.yaml (a contest with
// thousands of users is far smaller).
const maxYAML = 64 << 20

// readYAML reads a YAML mapping (upstream load_yaml_from_path). An empty
// file is an empty mapping.
func readYAML(path string) (map[string]any, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return nil, fmt.Errorf("%s is a directory", path)
	}
	if st.Size() > maxYAML {
		return nil, fmt.Errorf("%s is too large (%d bytes)", path, st.Size())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var v any
	if err := yaml.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if v == nil {
		return map[string]any{}, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: expected a mapping of keys to values", path)
	}
	return m, nil
}

// exists reports whether path exists (os.path.exists: following links).
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// isFile reports whether path is a regular file (or a link to one).
func isFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

// notExist reports whether err says that a file does not exist.
func notExist(err error) bool { return errors.Is(err, fs.ErrNotExist) }

// conf is a YAML mapping (contest.yaml, task.yaml, an entry of the users)
// with typed getters. Like upstream load(), a getter takes the key and
// its aliases (the Italian names) and uses the first one present. A key
// present with a null value counts as absent: upstream would store None,
// which is the default of every nullable column and an error for the
// others. A value of the wrong type is an error in the report.
type conf struct {
	m     map[string]any
	where string
	rep   *importer.Report
}

// get returns the first key present, with its value.
func (c conf) get(keys ...string) (string, any, bool) {
	for _, k := range keys {
		if v, ok := c.m[k]; ok {
			return k, v, true
		}
	}
	return "", nil, false
}

// has reports whether one of the keys is present (even with null).
func (c conf) has(keys ...string) bool {
	_, _, ok := c.get(keys...)
	return ok
}

// value returns the value of the first key present, unless null.
func (c conf) value(keys ...string) (string, any, bool) {
	k, v, ok := c.get(keys...)
	if !ok || v == nil {
		return k, nil, false
	}
	return k, v, true
}

func (c conf) bad(key string, v any, want string) {
	c.rep.Errorf(c.where, "%s: expected %s, got %s", key, want, describe(v))
}

// describe shows a YAML value in messages.
func describe(v any) string {
	switch v := v.(type) {
	case string:
		return strconv.Quote(v)
	case map[string]any:
		return "a mapping"
	case []any:
		return "a list"
	case time.Time:
		return v.Format(time.RFC3339)
	}
	return fmt.Sprint(v)
}

// str returns a string (numbers are written as YAML wrote them).
func (c conf) str(keys ...string) (string, bool) {
	k, v, ok := c.value(keys...)
	if !ok {
		return "", false
	}
	switch v := v.(type) {
	case string:
		return v, true
	case int, int64, uint64, float64:
		return fmt.Sprint(v), true
	}
	c.bad(k, v, "a string")
	return "", false
}

// yaml11Bools are the booleans of YAML 1.1 (PyYAML) that YAML 1.2 reads
// as strings.
var yaml11Bools = map[string]bool{
	"yes": true, "Yes": true, "YES": true, "on": true, "On": true, "ON": true,
	"no": false, "No": false, "NO": false, "off": false, "Off": false, "OFF": false,
	"true": true, "True": true, "TRUE": true, "false": false, "False": false, "FALSE": false,
}

// boolean returns a boolean (also yes/no/on/off, and 0 or 1).
func (c conf) boolean(keys ...string) (bool, bool) {
	k, v, ok := c.value(keys...)
	if !ok {
		return false, false
	}
	switch v := v.(type) {
	case bool:
		return v, true
	case string:
		if b, ok := yaml11Bools[v]; ok {
			return b, true
		}
	case int:
		if v == 0 || v == 1 {
			return v == 1, true
		}
	}
	c.bad(k, v, "a boolean")
	return false, false
}

// toInt converts an integer value (also an integral float or a numeric
// string, as Python's int()).
func toInt(v any) (int64, bool) {
	switch v := v.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case uint64:
		if v <= math.MaxInt64 {
			return int64(v), true
		}
	case float64:
		if v == math.Trunc(v) && math.Abs(v) < 1<<53 {
			return int64(v), true
		}
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(strings.ReplaceAll(v, "_", "")), 10, 64)
		return n, err == nil
	}
	return 0, false
}

// toFloat converts a number (also a numeric string, as Python's float()).
func toFloat(v any) (float64, bool) {
	switch v := v.(type) {
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint64:
		return float64(v), true
	case float64:
		return v, !math.IsNaN(v) && !math.IsInf(v, 0)
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f, err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
	}
	return 0, false
}

// integer returns an integer.
func (c conf) integer(keys ...string) (int64, bool) {
	k, v, ok := c.value(keys...)
	if !ok {
		return 0, false
	}
	n, ok := toInt(v)
	if !ok {
		c.bad(k, v, "an integer")
	}
	return n, ok
}

// int32 returns an integer that fits a column of PostgreSQL.
func (c conf) int32(keys ...string) (int32, bool) {
	n, ok := c.integer(keys...)
	if !ok {
		return 0, false
	}
	if n < math.MinInt32 || n > math.MaxInt32 {
		k, _, _ := c.get(keys...)
		c.rep.Errorf(c.where, "%s: %d is out of range", k, n)
		return 0, false
	}
	return int32(n), true
}

// number returns a number.
func (c conf) number(keys ...string) (float64, bool) {
	k, v, ok := c.value(keys...)
	if !ok {
		return 0, false
	}
	f, ok := toFloat(v)
	if !ok {
		c.bad(k, v, "a number")
	}
	return f, ok
}

// seconds returns a number of seconds as a duration (upstream
// make_timedelta), rounded to microseconds like Python's timedelta.
func (c conf) seconds(keys ...string) (time.Duration, bool) {
	f, ok := c.number(keys...)
	if !ok {
		return 0, false
	}
	return secondsToDuration(f), true
}

func secondsToDuration(f float64) time.Duration {
	return time.Duration(math.RoundToEven(f*1e6)) * time.Microsecond
}

// timeLayouts are the strings accepted as times besides YAML timestamps
// (which arrive as time.Time) and Unix times.
var timeLayouts = []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05.999999999", "2006-01-02T15:04:05.999999999", "2006-01-02"}

// datetime returns a time (upstream parse_datetime): a YAML timestamp or a
// Unix time. Timestamps without a zone are UTC (upstream: the local time
// of the machine running the import).
func (c conf) datetime(keys ...string) (time.Time, bool) {
	k, v, ok := c.value(keys...)
	if !ok {
		return time.Time{}, false
	}
	switch v := v.(type) {
	case time.Time:
		return v.UTC(), true
	case string:
		for _, l := range timeLayouts {
			if t, err := time.Parse(l, strings.TrimSpace(v)); err == nil {
				return t.UTC(), true
			}
		}
	default:
		if f, ok := toFloat(v); ok {
			sec, frac := math.Modf(f)
			return time.Unix(int64(sec), int64(math.Round(frac*1e6))*1e3).UTC(), true
		}
	}
	c.bad(k, v, "a time (a YAML timestamp or a Unix time)")
	return time.Time{}, false
}

// list returns a list.
func (c conf) list(keys ...string) ([]any, bool) {
	k, v, ok := c.value(keys...)
	if !ok {
		return nil, false
	}
	l, ok := v.([]any)
	if !ok {
		c.bad(k, v, "a list")
	}
	return l, ok
}

// strings returns a list of strings.
func (c conf) strings(keys ...string) ([]string, bool) {
	k, _, _ := c.get(keys...)
	l, ok := c.list(keys...)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(l))
	for _, v := range l {
		switch v := v.(type) {
		case string:
			out = append(out, v)
		case int, int64, uint64, float64:
			out = append(out, fmt.Sprint(v))
		default:
			c.bad(k, v, "a list of strings")
			return nil, false
		}
	}
	return out, true
}

// mapping returns the i-th element of a list as a conf.
func (c conf) mapping(v any, where string) (conf, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		c.rep.Errorf(where, "expected a mapping, got %s", describe(v))
		return conf{}, false
	}
	return conf{m: m, where: where, rep: c.rep}, true
}

// toJSON encodes a YAML value as JSON the way Python's json.dumps encodes
// what PyYAML read: floats keep their decimal point ("50.0"), mapping
// keys are sorted.
func toJSON(v any) (json.RawMessage, error) {
	var b bytes.Buffer
	if err := writeJSON(&b, v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func writeJSON(b *bytes.Buffer, v any) error {
	switch v := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(v))
	case int:
		b.WriteString(strconv.Itoa(v))
	case int64:
		b.WriteString(strconv.FormatInt(v, 10))
	case uint64:
		b.WriteString(strconv.FormatUint(v, 10))
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("%v cannot be stored", v)
		}
		b.WriteString(pyFloat(v))
	case string:
		s, _ := json.Marshal(v)
		b.Write(s)
	case time.Time:
		s, _ := json.Marshal(v.UTC().Format(time.RFC3339Nano))
		b.Write(s)
	case []any:
		b.WriteByte('[')
		for i, e := range v {
			if i > 0 {
				b.WriteString(", ")
			}
			if err := writeJSON(b, e); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteString(", ")
			}
			s, _ := json.Marshal(k)
			b.Write(s)
			b.WriteString(": ")
			if err := writeJSON(b, v[k]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	default:
		return fmt.Errorf("unsupported value %v", v)
	}
	return nil
}

// pyFloat formats a float as Python's repr: the shortest representation,
// with ".0" when integral, and an exponent below 1e-4 and from 1e16.
func pyFloat(f float64) string {
	if f == 0 {
		if math.Signbit(f) {
			return "-0.0"
		}
		return "0.0"
	}
	exp := int(math.Floor(math.Log10(math.Abs(f))))
	// Log10 may be off by one near powers of ten: use the digits.
	e := strconv.FormatFloat(f, 'e', -1, 64)
	if i := strings.IndexByte(e, 'e'); i >= 0 {
		if n, err := strconv.Atoi(e[i+1:]); err == nil {
			exp = n
		}
	}
	if exp < -4 || exp >= 16 {
		return e
	}
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.ContainsAny(s, ".") {
		s += ".0"
	}
	return s
}
