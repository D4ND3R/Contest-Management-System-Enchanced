// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2011-2018 Luca Wehrstedt, Stefano Maggiolo and the CMS contributors
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cmsranking/Entity.py, Contest.py, Task.py, Team.py, User.py,
// Submission.py and Subchange.py of CMS (http://cms-dev.github.io/).

package rankingweb

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// The entities of the REST API, with the JSON formats and the validation
// of cmsranking: the dispatcher sends them (ProxyService) and the
// JavaScript of the scoreboard reads them. Types are as strict as Python's
// isinstance checks: an integer field rejects 1.0, a float field rejects 1
// (send 1.0; Float does).

// ErrInvalidData is the error of data that does not validate (CMS
// InvalidData): the request gets 400.
var ErrInvalidData = errors.New("invalid data")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidData, fmt.Sprintf(format, args...))
}

// keyRE limits the keys of the entities (CMS RankingWebServer).
var keyRE = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// ValidKey reports whether key may name an entity.
func ValidKey(key string) bool { return keyRE.MatchString(key) }

// EncodeID returns an entity key for a CMS name, using only A-Za-z0-9_
// (CMS ProxyService encode_id): every other character becomes "_" and
// its code in hexadecimal.
func EncodeID(id string) string {
	var b strings.Builder
	for _, r := range id {
		if r < 128 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			fmt.Fprintf(&b, "_%x", r)
		}
	}
	return b.String()
}

// DecodeID inverts EncodeID for CMS names (letters, digits, "_" and "-",
// whose codes have two hexadecimal digits). Keys that are not such an
// encoding are returned unchanged.
func DecodeID(key string) string {
	if !strings.Contains(key, "_") {
		return key
	}
	var b strings.Builder
	for i := 0; i < len(key); i++ {
		if key[i] != '_' {
			b.WriteByte(key[i])
			continue
		}
		if i+2 >= len(key) {
			return key
		}
		n, err := strconv.ParseUint(key[i+1:i+3], 16, 8)
		if err != nil {
			return key
		}
		b.WriteByte(byte(n))
		i += 2
	}
	return b.String()
}

// Float is a float64 encoded as Python's json does (100.0, never 100),
// which cmsranking requires for its float fields.
type Float float64

// MarshalJSON implements json.Marshaler.
func (f Float) MarshalJSON() ([]byte, error) {
	if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
		return nil, fmt.Errorf("rankingweb: %v is not a JSON number", float64(f))
	}
	return []byte(PyFloat(float64(f))), nil
}

// PyFloat formats f as Python's str(float) does.
func PyFloat(f float64) string {
	if a := math.Abs(f); f == 0 || (a >= 1e-4 && a < 1e16) {
		s := strconv.FormatFloat(f, 'f', -1, 64)
		if !strings.Contains(s, ".") {
			s += ".0"
		}
		return s
	}
	return strconv.FormatFloat(f, 'e', -1, 64)
}

// fields is a decoded JSON object being validated.
type fields map[string]json.RawMessage

func parseObject(raw json.RawMessage) (fields, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return nil, invalid("Not a dictionary")
	}
	var f fields
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, invalid("Not a dictionary")
	}
	return f, nil
}

func (f fields) get(name string) (json.RawMessage, error) {
	v, ok := f[name]
	if !ok {
		return nil, invalid("Field '%s' is missing", name)
	}
	return bytes.TrimSpace(v), nil
}

func (f fields) has(name string) bool { _, ok := f[name]; return ok }

func (f fields) str(name string) (string, error) {
	v, err := f.get(name)
	if err != nil {
		return "", err
	}
	var s string
	if len(v) == 0 || v[0] != '"' || json.Unmarshal(v, &s) != nil {
		return "", invalid("Field '%s' isn't a string", name)
	}
	return s, nil
}

func (f fields) optStr(name string) (*string, error) {
	v, err := f.get(name)
	if err != nil {
		return nil, err
	}
	if string(v) == "null" {
		return nil, nil
	}
	s, err := f.str(name)
	if err != nil {
		return nil, invalid("Field '%s' isn't a string (or null)", name)
	}
	return &s, nil
}

func isNumber(v []byte) bool {
	return len(v) > 0 && (v[0] == '-' || v[0] >= '0' && v[0] <= '9')
}

func (f fields) integer(name, what string) (int64, error) {
	v, err := f.get(name)
	if err != nil {
		return 0, err
	}
	if !isNumber(v) || bytes.ContainsAny(v, ".eE") {
		return 0, invalid("Field '%s' isn't an integer%s", name, what)
	}
	n, err := strconv.ParseInt(string(v), 10, 64)
	if err != nil {
		return 0, invalid("Field '%s' isn't an integer%s", name, what)
	}
	return n, nil
}

func (f fields) float(name string) (float64, error) {
	v, err := f.get(name)
	if err != nil {
		return 0, err
	}
	if !isNumber(v) || !bytes.ContainsAny(v, ".eE") {
		return 0, invalid("Field '%s' isn't a float", name)
	}
	x, err := strconv.ParseFloat(string(v), 64)
	if err != nil || math.IsInf(x, 0) {
		return 0, invalid("Field '%s' isn't a float", name)
	}
	return x, nil
}

func (f fields) strList(name string) ([]string, error) {
	v, err := f.get(name)
	if err != nil {
		return nil, err
	}
	var items []json.RawMessage
	if len(v) == 0 || v[0] != '[' || json.Unmarshal(v, &items) != nil {
		return nil, invalid("Field '%s' isn't a list of strings", name)
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		var s string
		it = bytes.TrimSpace(it)
		if len(it) == 0 || it[0] != '"' || json.Unmarshal(it, &s) != nil {
			return nil, invalid("Field '%s' isn't a list of strings", name)
		}
		out = append(out, s)
	}
	return out, nil
}

// entity is one of the stored types.
type entity interface {
	// consistent reports whether the entities it references exist.
	consistent(st *state) bool
}

// Contest is a contest (cmsranking Contest). Freeze is an addition of
// this project (R7.3): the time from which the scores are hidden from the
// public until the contest is sent again without it.
type Contest struct {
	Name           string `json:"name"`
	Begin          int64  `json:"begin"`
	End            int64  `json:"end"`
	ScorePrecision int64  `json:"score_precision"`
	Freeze         *int64 `json:"freeze,omitempty"`
}

func parseContest(raw json.RawMessage) (entity, error) {
	f, err := parseObject(raw)
	if err != nil {
		return nil, err
	}
	c := &Contest{}
	if c.Name, err = f.str("name"); err != nil {
		return nil, err
	}
	if c.Begin, err = f.integer("begin", ""); err != nil {
		return nil, err
	}
	if c.End, err = f.integer("end", ""); err != nil {
		return nil, err
	}
	if c.Begin > c.End {
		return nil, invalid("Field 'begin' is greater than 'end'")
	}
	if c.ScorePrecision, err = f.integer("score_precision", ""); err != nil {
		return nil, err
	}
	if c.ScorePrecision < 0 {
		return nil, invalid("Field 'score_precision' is negative")
	}
	if f.has("freeze") {
		if v, _ := f.get("freeze"); string(v) != "null" {
			fr, err := f.integer("freeze", " (unix timestamp)")
			if err != nil {
				return nil, err
			}
			c.Freeze = &fr
		}
	}
	return c, nil
}

func (*Contest) consistent(*state) bool { return true }

// The score modes of the tasks (cmscommon.constants).
const (
	ScoreModeMax            = "max"
	ScoreModeMaxSubtask     = "max_subtask"
	ScoreModeMaxTokenedLast = "max_tokened_last"
)

// Task is a task (cmsranking Task).
type Task struct {
	Name           string   `json:"name"`
	ShortName      string   `json:"short_name"`
	Contest        string   `json:"contest"`
	MaxScore       Float    `json:"max_score"`
	ScorePrecision int64    `json:"score_precision"`
	ExtraHeaders   []string `json:"extra_headers"`
	Order          int64    `json:"order"`
	ScoreMode      string   `json:"score_mode"`
}

func parseTask(raw json.RawMessage) (entity, error) {
	f, err := parseObject(raw)
	if err != nil {
		return nil, err
	}
	t := &Task{}
	if t.Name, err = f.str("name"); err != nil {
		return nil, err
	}
	if t.ShortName, err = f.str("short_name"); err != nil {
		return nil, err
	}
	if t.Contest, err = f.str("contest"); err != nil {
		return nil, err
	}
	ms, err := f.float("max_score")
	if err != nil {
		return nil, err
	}
	t.MaxScore = Float(ms)
	if t.ScorePrecision, err = f.integer("score_precision", ""); err != nil {
		return nil, err
	}
	if t.ScorePrecision < 0 {
		return nil, invalid("Field 'score_precision' is negative")
	}
	if t.ExtraHeaders, err = f.strList("extra_headers"); err != nil {
		return nil, err
	}
	if t.ScoreMode, err = f.str("score_mode"); err != nil {
		return nil, err
	}
	switch t.ScoreMode {
	case ScoreModeMax, ScoreModeMaxSubtask, ScoreModeMaxTokenedLast:
	default:
		// CMS accepts it and fails when scoring.
		return nil, invalid("Unexpected score mode '%s'", t.ScoreMode)
	}
	if t.Order, err = f.integer("order", ""); err != nil {
		return nil, err
	}
	return t, nil
}

func (t *Task) consistent(st *state) bool {
	_, ok := st.items[kContest][t.Contest]
	return ok
}

// Team is a team (cmsranking Team).
type Team struct {
	Name string `json:"name"`
}

func parseTeam(raw json.RawMessage) (entity, error) {
	f, err := parseObject(raw)
	if err != nil {
		return nil, err
	}
	t := &Team{}
	if t.Name, err = f.str("name"); err != nil {
		return nil, err
	}
	return t, nil
}

func (*Team) consistent(*state) bool { return true }

// User is a user (cmsranking User).
type User struct {
	FName string  `json:"f_name"`
	LName string  `json:"l_name"`
	Team  *string `json:"team"`
}

func parseUser(raw json.RawMessage) (entity, error) {
	f, err := parseObject(raw)
	if err != nil {
		return nil, err
	}
	u := &User{}
	if u.FName, err = f.str("f_name"); err != nil {
		return nil, err
	}
	if u.LName, err = f.str("l_name"); err != nil {
		return nil, err
	}
	if u.Team, err = f.optStr("team"); err != nil {
		return nil, err
	}
	return u, nil
}

func (u *User) consistent(st *state) bool {
	if u.Team == nil {
		return true
	}
	_, ok := st.items[kTeam][*u.Team]
	return ok
}

// Submission is a submission (cmsranking Submission).
type Submission struct {
	User string `json:"user"`
	Task string `json:"task"`
	Time int64  `json:"time"`
}

func parseSubmission(raw json.RawMessage) (entity, error) {
	f, err := parseObject(raw)
	if err != nil {
		return nil, err
	}
	s := &Submission{}
	if s.User, err = f.str("user"); err != nil {
		return nil, err
	}
	if s.Task, err = f.str("task"); err != nil {
		return nil, err
	}
	if s.Time, err = f.integer("time", " (unix timestamp)"); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Submission) consistent(st *state) bool {
	_, task := st.items[kTask][s.Task]
	_, user := st.items[kUser][s.User]
	return task && user
}

// Subchange is a change of the status of a submission (cmsranking
// Subchange): its score, its token or its details from Time on. Absent
// fields (nil) do not change.
type Subchange struct {
	Submission string    `json:"submission"`
	Time       int64     `json:"time"`
	Score      *Float    `json:"score,omitempty"`
	Token      *bool     `json:"token,omitempty"`
	Extra      *[]string `json:"extra,omitempty"`
}

func parseSubchange(raw json.RawMessage) (entity, error) {
	f, err := parseObject(raw)
	if err != nil {
		return nil, err
	}
	s := &Subchange{}
	if s.Submission, err = f.str("submission"); err != nil {
		return nil, err
	}
	if s.Time, err = f.integer("time", " (unix timestamp)"); err != nil {
		return nil, err
	}
	if f.has("score") {
		x, err := f.float("score")
		if err != nil {
			return nil, err
		}
		v := Float(x)
		s.Score = &v
	}
	if f.has("token") {
		v, _ := f.get("token")
		var b bool
		if (string(v) != "true" && string(v) != "false") || json.Unmarshal(v, &b) != nil {
			return nil, invalid("Field 'token' isn't a boolean")
		}
		s.Token = &b
	}
	if f.has("extra") {
		l, err := f.strList("extra")
		if err != nil {
			return nil, err
		}
		s.Extra = &l
	}
	return s, nil
}

func (s *Subchange) consistent(st *state) bool {
	_, ok := st.items[kSubmission][s.Submission]
	return ok
}
