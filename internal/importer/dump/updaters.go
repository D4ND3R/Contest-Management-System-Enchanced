// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cmscontrib/updaters/update_15.py to update_49.py of CMS:
// Copyright © 2014, 2025-2026 Luca Versari <veluca93@gmail.com>
// Copyright © 2015-2019 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2015, 2018 William Di Luigi <williamdiluigi@gmail.com>
// Copyright © 2016 Myungwoo Chun <mc.tamaki@gmail.com>
// Copyright © 2016 Amir Keivan Mohtashami <akmohtashami97@gmail.com>
// Copyright © 2016 Masaki Hara <ackie.h.gmai@gmail.com>
// Copyright © 2017-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2018 Fabian Gundlach <320pointsguy@gmail.com>
// Copyright © 2019-2020 Andrey Vihrov <andrey.vihrov@gmail.com>
// Copyright © 2025 p. randla <prandla@r9.pm>

package dump

import (
	"encoding/json"
	"fmt"
	"math"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/store"
)

// The updaters of CMS adapt a dump written with an older data model to
// the next version, on the decoded objects (cmscontrib/updaters/update_N
// turns version N-1 into N). They are ported here from version 15 on, so
// dumps of version 14 (CMS 1.2) and later can be imported; update runs
// the ones a dump needs, in order. Numbers are json.Number (the decoder
// keeps them as written), lists []any and dictionaries map[string]any.

type objects = map[string]map[string]any

// updater turns a dump of version N-1 into version N.
type updater func(u *updating) error

// updating is the state shared by the updaters of one dump.
type updating struct {
	objs objects
	// order is the order of the keys: the order of the file, then the
	// objects created by the updaters (Python dictionaries keep the order
	// of insertion, and the keys of new objects depend on it).
	order []string
	// meta is the number of keys of contest.json that are not objects
	// ("_version", "_objects"): upstream counts them in len(data).
	meta int
	warn func(format string, args ...any)
	// nextID is the next candidate key for a new object (get_id).
	nextID int
}

// updaters by the version they produce.
var updaters = map[int]updater{
	15: update15, 16: update16, 17: update17, 18: update18, 19: update19,
	20: update20, 21: update21, 22: update22, 23: update23, 24: update24,
	25: update25, 26: update26, 27: update27, 28: update28, 29: update29,
	30: update30, 31: update31, 32: update32, 33: update33, 34: update34,
	35: update35, 36: update36, 37: update37, 38: update38, 39: update39,
	40: update40, 41: update41, 42: update42, 43: update43, 44: update44,
	45: update45, 46: update46, 47: update47, 48: update48, 49: update49,
}

// update brings objs (whose keys are in order, with meta other keys in
// the file) from version from to Version.
func update(objs objects, order []string, meta, from int, warn func(string, ...any)) error {
	u := &updating{objs: objs, order: order, meta: meta, warn: warn}
	for v := from + 1; v <= Version; v++ {
		fn, ok := updaters[v]
		if !ok {
			return fmt.Errorf("%w: no updater to version %d", ErrVersion, v)
		}
		if err := fn(u); err != nil {
			return fmt.Errorf("updating the dump to version %d: %w", v, err)
		}
	}
	return nil
}

// keys returns the keys of the objects in order.
func (u *updating) keys() []string {
	keys := make([]string, 0, len(u.objs))
	seen := make(map[string]bool, len(u.objs))
	for _, k := range u.order {
		if _, ok := u.objs[k]; ok && !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	u.order = keys
	return slices.Clone(keys)
}

// put adds or replaces an object.
func (u *updating) put(k string, v map[string]any) {
	if _, ok := u.objs[k]; !ok {
		u.order = append(u.order, k)
	}
	u.objs[k] = v
}

// resetIDs starts get_id from len(data), as each updater that creates
// objects does.
func (u *updating) resetIDs() { u.nextID = len(u.objs) + u.meta }

// each calls fn with the objects of the given classes (all if none).
func (u *updating) each(fn func(k string, v map[string]any), classes ...string) {
	for _, k := range u.keys() {
		v := u.objs[k]
		if len(classes) == 0 || contains(classes, class(v)) {
			fn(k, v)
		}
	}
}

// newID returns an unused key (get_id).
func (u *updating) newID() string {
	for {
		k := strconv.Itoa(u.nextID)
		if _, ok := u.objs[k]; !ok {
			return k
		}
		u.nextID++
	}
}

func class(v map[string]any) string { s, _ := v["_class"].(string); return s }

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// str returns a string value ("" and false if missing or not a string).
func str(v any) (string, bool) { s, ok := v.(string); return s, ok }

// numberOf returns a number value.
func numberOf(v any) (float64, bool) {
	switch n := v.(type) {
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

// isInt reports whether v is a number written as an integer (a Python
// int).
func isInt(v any) bool {
	switch n := v.(type) {
	case json.Number:
		return !strings.ContainsAny(n.String(), ".eE")
	case int, int64:
		return true
	}
	return false
}

// pyFloat writes a float as Python's json module does.
func pyFloat(f float64) json.Number {
	if f == math.Trunc(f) && math.Abs(f) < 1e16 {
		return json.Number(strconv.FormatFloat(f, 'f', 1, 64))
	}
	return json.Number(strconv.FormatFloat(f, 'g', -1, 64))
}

// stringMap returns a dictionary value (nil if missing).
func stringMap(v any) map[string]any { m, _ := v.(map[string]any); return m }

// list returns a list value (nil if missing).
func list(v any) []any { l, _ := v.([]any); return l }

// update_15: users and contests are separated. Each user becomes a
// participation (keeping its key, which other objects refer to) and a new
// user.
func update15(u *updating) error {
	u.resetIDs()
	for _, k := range u.keys() {
		v := u.objs[k]
		switch class(v) {
		case "User":
			newUser := u.newID()
			part := map[string]any{}
			for _, f := range []string{"contest", "delay_time", "extra_time", "ip", "messages", "usertests",
				"questions", "starting_time", "submissions", "hidden"} {
				if x, ok := v[f]; ok {
					part[f] = x
					delete(v, f)
				}
			}
			part["_class"] = "Participation"
			part["password"] = nil
			part["user"] = newUser
			u.put(k, part)
			v["participations"] = []any{k}
			u.put(newUser, v)
		case "Message", "PrintJob", "Question", "Submission", "UserTest":
			v["participation"] = v["user"]
			delete(v, "user")
		}
	}
	return nil
}

// update_16: Contest.submissions_download_allowed.
func update16(u *updating) error {
	u.each(func(_ string, v map[string]any) { v["submissions_download_allowed"] = true }, "Contest")
	return nil
}

// update_17: Participation.team (upstream writes team_id, which the
// importer ignores: the team is null).
func update17(u *updating) error {
	u.each(func(_ string, v map[string]any) { v["team_id"] = nil }, "Participation")
	return nil
}

// update_18: Contest.ip_autologin.
func update18(u *updating) error {
	u.each(func(_ string, v map[string]any) { v["ip_autologin"] = false }, "Contest")
	return nil
}

// update_19: Contest.block_hidden_participations and ip_restriction.
func update19(u *updating) error {
	u.each(func(_ string, v map[string]any) {
		v["block_hidden_participations"] = false
		v["ip_restriction"] = true
	}, "Contest")
	return nil
}

// update_20: Contest.allow_user_tests.
func update20(u *updating) error {
	u.each(func(_ string, v map[string]any) { v["allow_user_tests"] = true }, "Contest")
	return nil
}

// update_21: Contest.allow_questions.
func update21(u *updating) error {
	u.each(func(_ string, v map[string]any) { v["allow_questions"] = true }, "Contest")
	return nil
}

// update_22: the number of processes of Communication (its parameters
// were a JSON string then).
func update22(u *updating) error {
	var err error
	u.each(func(k string, v map[string]any) {
		if s, _ := str(v["task_type"]); s != "Communication" {
			return
		}
		raw, _ := str(v["task_type_parameters"])
		var params []any
		if e := json.Unmarshal([]byte(raw), &params); e != nil {
			err = fmt.Errorf("dataset %s: task type parameters %q: %w", k, raw, e)
			return
		}
		if len(params) < 1 {
			params = []any{1}
		}
		b, _ := json.Marshal(params)
		v["task_type_parameters"] = string(b)
	}, "Dataset")
	return err
}

// update_23: Participation.unrestricted.
func update23(u *updating) error {
	u.each(func(_ string, v map[string]any) { v["unrestricted"] = false }, "Participation")
	return nil
}

// update_24: Contest.allow_password_authentication.
func update24(u *updating) error {
	u.each(func(_ string, v map[string]any) { v["allow_password_authentication"] = true }, "Contest")
	return nil
}

// update_25: languages are named after the language plugins.
func update25(u *updating) error {
	languageMap := map[string]string{
		"c": "C11 / gcc", "cpp": "C++11 / g++", "pas": "Pascal / fpc", "py": "Python 2 / CPython",
		"php": "PHP", "java": "Java 1.4 / gcj", "hs": "Haskell / ghc",
	}
	warned := map[string]bool{}
	mapLanguage := func(l string) string {
		if m, ok := languageMap[l]; ok {
			return m
		}
		if !warned[l] {
			warned[l] = true
			u.warn("unrecognized language %q, copied without changes", l)
		}
		return l
	}
	u.each(func(_ string, v map[string]any) {
		switch class(v) {
		case "Contest":
			langs := list(v["languages"])
			for i, l := range langs {
				if s, ok := str(l); ok {
					langs[i] = mapLanguage(s)
				}
			}
		case "Submission", "UserTest":
			if s, ok := str(v["language"]); ok {
				v["language"] = mapLanguage(s)
			}
		}
	})
	return nil
}

// encodeCodename encodes s with "A-Za-z0-9_-" (and extra): any other
// character becomes "__<hex code point>" (update_26).
func encodeCodename(s, extra string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 128 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			r == '_' || r == '-' || strings.ContainsRune(extra, r)) {
			b.WriteRune(r)
		} else {
			fmt.Fprintf(&b, "__%x", r)
		}
	}
	return b.String()
}

var sha1OrTombstone = regexp.MustCompile(`^([0-9a-f]{40}|x)$`)

// update_26: codenames and file names use a restricted alphabet; the
// networks of a participation become a list.
func update26(u *updating) error {
	codenames := map[string]string{"Admin": "username", "Contest": "name", "Task": "name", "Testcase": "codename",
		"User": "username", "Team": "code"}
	filenames := map[string]bool{"PrintJob": true, "File": true, "Executable": true, "Attachment": true,
		"SubmissionFormatElement": true, "Manager": true, "UserTestFile": true, "UserTestManager": true,
		"UserTestExecutable": true}
	filenameDicts := map[string][]string{"Submission": {"files"}, "SubmissionResult": {"executables"},
		"Task": {"attachments"}, "Dataset": {"managers"}, "UserTest": {"files", "managers"},
		"UserTestResult": {"executables"}}
	digests := map[string][]string{"PrintJob": {"digest"}, "File": {"digest"}, "Executable": {"digest"},
		"Statement": {"digest"}, "Attachment": {"digest"}, "Manager": {"digest"}, "Testcase": {"input", "output"},
		"UserTest": {"input"}, "UserTestFile": {"digest"}, "UserTestManager": {"digest"},
		"UserTestResult": {"output"}, "UserTestExecutable": {"digest"}}
	var err error
	fail := func(format string, args ...any) {
		if err == nil {
			err = fmt.Errorf(format, args...)
		}
	}
	u.each(func(k string, v map[string]any) {
		cls := class(v)
		if col, ok := codenames[cls]; ok {
			if s, ok := str(v[col]); ok {
				v[col] = encodeCodename(s, "")
				if v[col] == "" {
					fail("%s %s: invalid codename in %s: %q", cls, k, col, s)
				}
			}
		}
		if filenames[cls] {
			if s, ok := str(v["filename"]); ok {
				e := encodeCodename(s, "%.")
				v["filename"] = e
				if e == "" || e == "." || e == ".." {
					fail("%s %s: invalid file name %q", cls, k, s)
				}
			}
		}
		for _, col := range filenameDicts[cls] {
			if m := stringMap(v[col]); m != nil {
				out := make(map[string]any, len(m))
				for name, id := range m {
					e := encodeCodename(name, "%.")
					if e == "" || e == "." || e == ".." {
						fail("%s %s: invalid file name %q in %s", cls, k, name, col)
					}
					out[e] = id
				}
				v[col] = out
			}
		}
		for _, col := range digests[cls] {
			if s, ok := str(v[col]); ok && !sha1OrTombstone.MatchString(s) {
				fail("%s %s: invalid SHA-1 digest in %s: %q", cls, k, col, s)
			}
		}
		if cls == "Participation" {
			if s, ok := str(v["ip"]); ok {
				var nets []any
				for _, n := range strings.Fields(s) {
					if _, e := parseNetwork(n); e != nil {
						fail("Participation %s: invalid network %q", k, n)
					}
					nets = append(nets, n)
				}
				if nets == nil {
					nets = []any{}
				}
				v["ip"] = nets
			}
		}
	})
	return err
}

// parseNetwork parses an address or a network as Python's
// ipaddress.ip_network (strict: no host bits set).
func parseNetwork(s string) (netip.Prefix, error) {
	if !strings.Contains(s, "/") {
		a, err := netip.ParseAddr(s)
		if err != nil {
			return netip.Prefix{}, err
		}
		return netip.PrefixFrom(a, a.BitLen()), nil
	}
	p, err := netip.ParsePrefix(s)
	if err != nil {
		return p, err
	}
	if p.Masked() != p {
		return p, fmt.Errorf("%s has host bits set", s)
	}
	return p, nil
}

// update_27: analysis mode (Submission.official, Contest.analysis_*).
func update27(u *updating) error {
	u.each(func(_ string, v map[string]any) {
		switch class(v) {
		case "Submission":
			v["official"] = true
		case "Contest":
			v["analysis_enabled"] = false
			v["analysis_start"] = v["stop"]
			v["analysis_stop"] = v["stop"]
		}
	})
	return nil
}

// update_28: passwords name their method.
func update28(u *updating) error {
	u.each(func(_ string, v map[string]any) {
		switch class(v) {
		case "User":
			s, _ := str(v["password"])
			v["password"] = "plaintext:" + s
		case "Participation":
			if s, ok := str(v["password"]); ok {
				v["password"] = "plaintext:" + s
			}
		}
	})
	return nil
}

// fixText turns a text column (a JSON string, or a plain string) into a
// list of strings (update_29).
func fixText(t any) []any {
	s, ok := str(t)
	if !ok {
		return []any{}
	}
	var items []any
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	if err := dec.Decode(&items); err != nil || len(items) == 0 {
		items = []any{s}
	}
	out := make([]any, len(items))
	for i, x := range items {
		switch y := x.(type) {
		case string:
			out[i] = y
		case json.Number:
			out[i] = y.String()
		case bool:
			if y {
				out[i] = "True"
			} else {
				out[i] = "False"
			}
		case nil:
			out[i] = "None"
		default:
			b, _ := json.Marshal(y)
			out[i] = string(b)
		}
	}
	out[0] = strings.ReplaceAll(out[0].(string), "%d", "%s")
	return out
}

// loads decodes a JSON string value (json.loads), keeping numbers.
func loads(v any) (any, error) {
	s, ok := str(v)
	if !ok {
		return v, nil
	}
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("invalid JSON %q: %w", s, err)
	}
	return out, nil
}

// update_29: columns that held JSON strings hold JSON values.
func update29(u *updating) error {
	var err error
	set := func(k string, v map[string]any, col string, nullable bool) {
		x, ok := v[col]
		if !ok || x == nil && nullable {
			return
		}
		y, e := loads(x)
		if e != nil && err == nil {
			err = fmt.Errorf("%s %s: %s: %w", class(v), k, col, e)
		}
		v[col] = y
	}
	u.each(func(k string, v map[string]any) {
		switch class(v) {
		case "Dataset":
			set(k, v, "task_type_parameters", false)
			set(k, v, "score_type_parameters", false)
		case "SubmissionResult":
			set(k, v, "score_details", true)
			set(k, v, "public_score_details", true)
			set(k, v, "ranking_score_details", true)
			v["compilation_text"] = fixText(v["compilation_text"])
		case "Evaluation":
			v["text"] = fixText(v["text"])
		case "UserTestResult":
			v["compilation_text"] = fixText(v["compilation_text"])
			v["evaluation_text"] = fixText(v["evaluation_text"])
		case "User":
			if _, ok := v["preferred_languages"]; ok {
				set(k, v, "preferred_languages", false)
			} else {
				v["preferred_languages"] = []any{}
			}
		case "Task":
			set(k, v, "primary_statements", false)
		}
	})
	return err
}

// update_30 decoded the status of print jobs, which update_47 removes.
func update30(*updating) error { return nil }

// update_31: the submission format is a list of file names.
func update31(u *updating) error {
	var elements []string
	u.each(func(k string, v map[string]any) {
		switch class(v) {
		case "SubmissionFormatElement":
			elements = append(elements, k)
		case "Task":
			var format []any
			for _, id := range list(v["submission_format"]) {
				if s, ok := str(id); ok {
					format = append(format, u.objs[s]["filename"])
				}
			}
			if format == nil {
				format = []any{}
			}
			v["submission_format"] = format
		}
	})
	for _, k := range elements {
		delete(u.objs, k)
	}
	return nil
}

// update_32: limits are positive, or null for no limit.
func update32(u *updating) error {
	u.each(func(k string, v map[string]any) {
		for _, col := range []string{"time_limit", "memory_limit"} {
			limit, ok := numberOf(v[col])
			if !ok {
				v[col] = nil
				continue
			}
			if limit == 0 {
				v[col] = nil
			} else if limit < 0 {
				u.warn("dataset %s: the %s %v was negative: no limit is enforced now", k, strings.ReplaceAll(col, "_", " "), v[col])
				v[col] = nil
			}
		}
	}, "Dataset")
	return nil
}

// isGroupScoreDetails reports whether score details come from a Group*
// score type (update_33).
func isGroupScoreDetails(details any) bool {
	subtasks, ok := details.([]any)
	if !ok {
		return false
	}
	outcomes := map[string]bool{"Correct": true, "Not correct": true, "Partially correct": true}
	for _, st := range subtasks {
		subtask, ok := st.(map[string]any)
		if !ok {
			return false
		}
		visible := false
		switch {
		case hasKeys(subtask, "idx", "testcases", "score", "max_score"):
			if _, ok := numberOf(subtask["score"]); !ok {
				return false
			}
			if _, ok := numberOf(subtask["max_score"]); !ok {
				return false
			}
			visible = true
		case !hasKeys(subtask, "idx", "testcases"):
			return false
		}
		if !isInt(subtask["idx"]) {
			return false
		}
		testcases, ok := subtask["testcases"].([]any)
		if !ok {
			return false
		}
		for _, t := range testcases {
			tc, ok := t.(map[string]any)
			if !ok {
				return false
			}
			switch {
			case hasKeys(tc, "idx", "outcome", "text", "time", "memory"):
				o, _ := str(tc["outcome"])
				text, ok := tc["text"].([]any)
				if !outcomes[o] || !ok {
					return false
				}
				for _, s := range text {
					if _, ok := s.(string); !ok {
						return false
					}
				}
				for _, f := range []string{"time", "memory"} {
					if _, ok := numberOf(tc[f]); tc[f] != nil && !ok {
						return false
					}
				}
			case !hasKeys(tc, "idx") || visible:
				return false
			}
			if _, ok := tc["idx"].(string); !ok {
				return false
			}
		}
	}
	return true
}

// hasKeys reports whether m has exactly these keys.
func hasKeys(m map[string]any, keys ...string) bool {
	if len(m) != len(keys) {
		return false
	}
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			return false
		}
	}
	return true
}

// convertScoreDetails replaces the scores of the subtasks of Group*
// details with fractions of their maximum score, and reports whether it
// had to guess (update_33).
func convertScoreDetails(details any) bool {
	if !isGroupScoreDetails(details) {
		return false
	}
	inaccurate := false
	for _, st := range details.([]any) {
		subtask := st.(map[string]any)
		if _, ok := subtask["score"]; !ok {
			continue
		}
		maxScore, _ := numberOf(subtask["max_score"])
		if maxScore == 0 {
			all, any0 := true, false
			for _, t := range subtask["testcases"].([]any) {
				o, _ := str(t.(map[string]any)["outcome"])
				all = all && o == "Correct"
				any0 = any0 || o == "Not correct"
			}
			switch {
			case all:
				subtask["score_fraction"] = pyFloat(1)
			case any0:
				subtask["score_fraction"] = pyFloat(0)
			default:
				subtask["score_fraction"] = pyFloat(0.5)
			}
			inaccurate = true
		} else {
			score, _ := numberOf(subtask["score"])
			subtask["score_fraction"] = pyFloat(score / maxScore)
		}
		delete(subtask, "score")
	}
	return inaccurate
}

// update_33: subtasks report their score as a fraction of their maximum.
func update33(u *updating) error {
	inaccurate := false
	u.each(func(_ string, v map[string]any) {
		if convertScoreDetails(v["score_details"]) {
			inaccurate = true
		}
		if convertScoreDetails(v["public_score_details"]) {
			inaccurate = true
		}
	}, "SubmissionResult")
	if inaccurate {
		u.warn("some subtasks have a maximum score of zero: they used to be reported as successful even if " +
			"some of their testcases failed; the old data may not have been converted accurately: rescore " +
			"the affected submissions to be safe")
	}
	return nil
}

// update_34 and update_35 only changed column types.
func update34(*updating) error { return nil }
func update35(*updating) error { return nil }

// update_36: file names cannot contain "%" except in a trailing ".%l".
func update36(u *updating) error {
	warn := false
	checkFilename := func(s string) string {
		if strings.Contains(s, "%") {
			warn = true
			return strings.ReplaceAll(s, "%", "__")
		}
		return s
	}
	checkSchema := func(s string) string {
		if base, ok := strings.CutSuffix(s, ".%l"); ok {
			if strings.Contains(base, "%") {
				warn = true
				return strings.ReplaceAll(base, "%", "__") + ".%l"
			}
			return s
		}
		return checkFilename(s)
	}
	filenameFields := map[string]bool{"Executable": true, "UserTestManager": true, "UserTestExecutable": true,
		"PrintJob": true, "Attachment": true, "Manager": true}
	filenameDicts := map[string]string{"SubmissionResult": "executables", "UserTest": "managers",
		"UserTestResult": "executables", "Task": "attachments", "Dataset": "managers"}
	schemaFields := map[string]bool{"File": true, "UserTestFile": true}
	schemaDicts := map[string]string{"Submission": "files", "UserTest": "files"}
	renameKeys := func(v map[string]any, col string, fn func(string) string) {
		if m := stringMap(v[col]); m != nil {
			out := make(map[string]any, len(m))
			for name, id := range m {
				out[fn(name)] = id
			}
			v[col] = out
		}
	}
	u.each(func(_ string, v map[string]any) {
		cls := class(v)
		if filenameFields[cls] {
			if s, ok := str(v["filename"]); ok {
				v["filename"] = checkFilename(s)
			}
		}
		if col, ok := filenameDicts[cls]; ok {
			renameKeys(v, col, checkFilename)
		}
		if schemaFields[cls] {
			if s, ok := str(v["filename"]); ok {
				v["filename"] = checkSchema(s)
			}
		}
		if col, ok := schemaDicts[cls]; ok {
			renameKeys(v, col, checkSchema)
		}
		if cls == "Task" {
			format := list(v["submission_format"])
			for i, s := range format {
				if x, ok := str(s); ok {
					format[i] = checkSchema(x)
				}
			}
		}
	})
	if warn {
		u.warn("some file names contained '%%', which is now forbidden: it was replaced by '__'")
	}
	return nil
}

// update_37: Task.feedback_level; old tasks showed full feedback.
func update37(u *updating) error {
	u.each(func(_ string, v map[string]any) { v["feedback_level"] = "full" }, "Task")
	return nil
}

// update_38: parameters compilation and user_io of Communication.
func update38(u *updating) error {
	u.each(func(_ string, v map[string]any) {
		if s, _ := str(v["task_type"]); s != "Communication" {
			return
		}
		if params := list(v["task_type_parameters"]); len(params) == 1 {
			v["task_type_parameters"] = append(params, "stub", "fifo_io")
		}
	}, "Dataset")
	return nil
}

// update_39: the admin of questions, messages and announcements.
func update39(u *updating) error {
	u.each(func(_ string, v map[string]any) { v["admin"] = nil }, "Question", "Message", "Announcement")
	return nil
}

// update_40: memory limits are in bytes instead of MiB.
func update40(u *updating) error {
	u.each(func(_ string, v map[string]any) {
		m := v["memory_limit"]
		if m == nil {
			return
		}
		if isInt(m) {
			if n, err := strconv.ParseInt(fmt.Sprint(m), 10, 64); err == nil {
				v["memory_limit"] = json.Number(strconv.FormatInt(n*1024*1024, 10))
				return
			}
		}
		if f, ok := numberOf(m); ok {
			v["memory_limit"] = pyFloat(f * 1024 * 1024)
		}
	}, "Dataset")
	return nil
}

var (
	codenameRE       = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	filenameRE       = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	filenameSchemaRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+(\.%l)?$`)
)

// update_41: codenames, file names, file name schemas and digests satisfy
// the constraints of the database (only checked: invalid data is an
// error).
func update41(u *updating) error {
	codenames := map[string][]string{"Contest": {"name"}, "Task": {"name"}, "Testcase": {"codename"},
		"Admin": {"username"}, "User": {"username"}, "Team": {"code"}}
	filenames := map[string]bool{"Executable": true, "UserTestManager": true, "UserTestExecutable": true,
		"PrintJob": true, "Attachment": true, "Manager": true}
	schemas := map[string]bool{"File": true, "UserTestFile": true}
	digests := map[string][]string{"Statement": {"digest"}, "Attachment": {"digest"}, "Manager": {"digest"},
		"Testcase": {"input", "output"}, "UserTest": {"input"}, "UserTestFile": {"digest"},
		"UserTestManager": {"digest"}, "UserTestResult": {"output"}, "UserTestExecutable": {"digest"},
		"File": {"digest"}, "Executable": {"digest"}, "PrintJob": {"digest"}}
	bad := map[string][]string{}
	note := func(what, cls, attr string) {
		f := cls + "." + attr
		if !contains(bad[what], f) {
			bad[what] = append(bad[what], f)
		}
	}
	u.each(func(_ string, v map[string]any) {
		cls := class(v)
		for _, attr := range codenames[cls] {
			if s, _ := str(v[attr]); !codenameRE.MatchString(s) {
				note("codenames", cls, attr)
			}
		}
		if filenames[cls] {
			if s, _ := str(v["filename"]); !filenameRE.MatchString(s) || s == "." || s == ".." {
				note("filenames", cls, "filename")
			}
		}
		if schemas[cls] {
			if s, _ := str(v["filename"]); !filenameSchemaRE.MatchString(s) || s == "." || s == ".." {
				note("filename schemas", cls, "filename")
			}
		}
		if cls == "Task" {
			for _, x := range list(v["submission_format"]) {
				if s, _ := str(x); !filenameSchemaRE.MatchString(s) || s == "." || s == ".." {
					note("filename schemas", cls, "submission_format")
				}
			}
		}
		for _, attr := range digests[cls] {
			if x := v[attr]; x != nil {
				if s, _ := str(x); !sha1OrTombstone.MatchString(s) {
					note("digests", cls, attr)
				}
			}
		}
	})
	if len(bad) == 0 {
		return nil
	}
	var msgs []string
	for _, what := range []string{"codenames", "filenames", "filename schemas", "digests"} {
		if len(bad[what]) > 0 {
			msgs = append(msgs, fmt.Sprintf("invalid %s in %s", what, strings.Join(bad[what], ", ")))
		}
	}
	return fmt.Errorf("some data is invalid: %s", strings.Join(msgs, "; "))
}

// update_42: Contest.allow_registration.
func update42(u *updating) error {
	u.each(func(_ string, v map[string]any) { v["allow_registration"] = false }, "Contest")
	return nil
}

// update_43: "Java 1.4 / gcj" is removed in favour of "Java / JDK"; the
// executables of the submissions in it are dropped (they are compiled
// again).
func update43(u *updating) error {
	const gcj, jdk = "Java 1.4 / gcj", "Java / JDK"
	var remove []string
	u.each(func(k string, v map[string]any) {
		switch class(v) {
		case "Contest":
			langs := list(v["languages"])
			out := make([]any, 0, len(langs)+1)
			found, hasJDK := false, false
			for _, l := range langs {
				switch l {
				case gcj:
					if !found {
						found = true
						continue
					}
				case jdk:
					hasJDK = true
				}
				out = append(out, l)
			}
			if found && !hasJDK {
				out = append(out, jdk)
			}
			v["languages"] = out
		case "Submission", "UserTest":
			if l, _ := str(v["language"]); l != gcj {
				return
			}
			u.warn("%s %s uses %s, updated to %s", class(v), k, gcj, jdk)
			v["language"] = jdk
			for _, r := range list(v["results"]) {
				rk, _ := str(r)
				res := u.objs[rk]
				if res == nil {
					continue
				}
				for _, e := range stringMap(res["executables"]) {
					if ek, ok := str(e); ok {
						remove = append(remove, ek)
					}
				}
				res["executables"] = map[string]any{}
			}
		}
	})
	for _, k := range remove {
		delete(u.objs, k)
	}
	return nil
}

// update_44: executables have the extension of their language.
func update44(u *updating) error {
	ext := map[string]string{"C# / Mono": ".exe", "Java / JDK": ".jar", "PHP": ".php",
		"Python 2 / CPython": ".zip", "Python 3 / CPython": ".pyz"}
	u.each(func(k string, v map[string]any) {
		sk, _ := str(v["submission"])
		s := u.objs[sk]
		if s == nil {
			return
		}
		l, _ := str(s["language"])
		old, _ := str(v["filename"])
		name := old + ext[l]
		v["filename"] = name
		rk, _ := str(v["submission_result"])
		if res := u.objs[rk]; res != nil {
			if m := stringMap(res["executables"]); m != nil {
				delete(m, old)
				m[name] = k
			}
		}
	}, "Executable")
	return nil
}

// update_45: Submission.opaque_id, random and unique.
func update45(u *updating) error {
	used := map[string]bool{}
	u.each(func(_ string, v map[string]any) {
		for {
			if id, ok := v["opaque_id"]; ok && id != nil && !used[fmt.Sprint(id)] {
				used[fmt.Sprint(id)] = true
				return
			}
			v["opaque_id"] = json.Number(strconv.FormatInt(store.NewOpaqueID(), 10))
		}
	}, "Submission")
	return nil
}

// update_46: the *_sandbox columns (paths joined by ":") become
// *_sandbox_paths lists.
func update46(u *updating) error {
	convert := func(v map[string]any, key string) {
		old, ok := v[key]
		delete(v, key)
		var paths any
		if s, isStr := str(old); ok && isStr {
			if s == "" {
				paths = []any{}
			} else {
				var l []any
				for _, p := range strings.Split(s, ":") {
					l = append(l, p)
				}
				paths = l
			}
		}
		v[key+"_paths"] = paths
	}
	u.each(func(_ string, v map[string]any) {
		switch class(v) {
		case "SubmissionResult":
			convert(v, "compilation_sandbox")
		case "Evaluation":
			convert(v, "evaluation_sandbox")
		case "UserTestResult":
			convert(v, "compilation_sandbox")
			convert(v, "evaluation_sandbox")
		}
	})
	return nil
}

// update_47: print jobs are removed.
func update47(u *updating) error {
	var remove []string
	u.each(func(k string, v map[string]any) {
		switch class(v) {
		case "PrintJob":
			remove = append(remove, k)
		case "Participation":
			delete(v, "printjobs")
		}
	})
	for _, k := range remove {
		delete(u.objs, k)
	}
	return nil
}

// update_48: the times of a contest move to its main group, "default",
// where its participations go.
func update48(u *updating) error {
	u.resetIDs()
	contestToGroup := map[string]string{}
	for _, k := range u.keys() {
		v := u.objs[k]
		if class(v) != "Contest" {
			continue
		}
		g := map[string]any{"_class": "Group", "name": "default", "contest": k}
		for _, prop := range []string{"start", "stop", "analysis_enabled", "analysis_start", "analysis_stop", "per_user_time"} {
			if x, ok := v[prop]; ok {
				g[prop] = x
			}
		}
		gk := u.newID()
		u.put(gk, g)
		v["main_group"] = gk
		contestToGroup[k] = gk
	}
	u.each(func(_ string, v map[string]any) {
		c, _ := str(v["contest"])
		if g, ok := contestToGroup[c]; ok {
			v["group"] = g
		}
	}, "Participation")
	return nil
}

// update_49: the stub of Communication and Interactive is called grader
// (managers stub.%l become grader.%l, the parameter "stub" becomes
// "grader").
func update49(u *updating) error {
	taskTypes := map[string]string{}
	tasks := map[string]bool{}
	u.each(func(k string, v map[string]any) {
		tt, _ := str(v["task_type"])
		taskTypes[k] = tt
		if tt != "Communication" && tt != "Interactive" {
			return
		}
		if t, ok := str(v["task"]); ok {
			tasks[t] = true
		}
		if params := list(v["task_type_parameters"]); len(params) >= 2 && params[1] == "stub" {
			params[1] = "grader"
		}
	}, "Dataset")
	type owned struct{ owner, name string }
	existing := map[owned]bool{}
	u.each(func(_ string, v map[string]any) {
		name, _ := str(v["filename"])
		switch class(v) {
		case "Manager":
			d, _ := str(v["dataset"])
			existing[owned{"d" + d, name}] = true
		case "UserTestManager":
			t, _ := str(v["user_test"])
			existing[owned{"u" + t, name}] = true
		}
	}, "Manager", "UserTestManager")
	var err error
	u.each(func(_ string, v map[string]any) {
		var owner, ownerKey, what string
		switch class(v) {
		case "Manager":
			ownerKey, _ = str(v["dataset"])
			if tt := taskTypes[ownerKey]; tt != "Communication" && tt != "Interactive" {
				return
			}
			owner, what = "d"+ownerKey, "dataset"
		case "UserTestManager":
			ownerKey, _ = str(v["user_test"])
			t, _ := str(u.objs[ownerKey]["task"])
			if !tasks[t] {
				return
			}
			owner, what = "u"+ownerKey, "user test"
		}
		name, _ := str(v["filename"])
		rest, ok := strings.CutPrefix(name, "stub.")
		if !ok {
			return
		}
		newName := "grader." + rest
		if existing[owned{owner, newName}] {
			if err == nil {
				err = fmt.Errorf("%s %s contains both %s and %s", what, ownerKey, name, newName)
			}
			return
		}
		v["filename"] = newName
		existing[owned{owner, newName}] = true
	}, "Manager", "UserTestManager")
	return err
}
