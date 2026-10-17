// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package rankingweb

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
)

// Fixtures in the formats ProxyService sends.
const (
	jsContest = `{"name":"Contest","begin":0,"end":1000,"score_precision":2}`
	jsTeam    = `{"name":"Italy"}`
	jsUser    = `{"f_name":"Ada","l_name":"Lovelace","team":"it"}`
)

func jsTask(contest, mode string) string {
	return `{"name":"Task A","short_name":"a","contest":"` + contest + `","max_score":100.0,` +
		`"score_precision":2,"extra_headers":["s1","s2"],"order":0,"score_mode":"` + mode + `"}`
}

func TestParseValid(t *testing.T) {
	cases := []struct {
		parse func(json.RawMessage) (entity, error)
		raw   string
	}{
		{parseContest, jsContest},
		{parseContest, `{"name":"C","begin":5,"end":5,"score_precision":0,"freeze":3}`},
		{parseContest, `{"name":"C","begin":5,"end":6,"score_precision":0,"freeze":null}`},
		{parseTask, jsTask("c", "max")},
		{parseTask, jsTask("c", "max_subtask")},
		{parseTask, jsTask("c", "max_tokened_last")},
		{parseTask, `{"name":"T","short_name":"t","contest":"c","max_score":1e2,"score_precision":0,"extra_headers":[],"order":-1,"score_mode":"max"}`},
		{parseTeam, jsTeam},
		{parseUser, jsUser},
		{parseUser, `{"f_name":"","l_name":"","team":null}`},
		{parseSubmission, `{"user":"u","task":"t","time":1700000000}`},
		{parseSubchange, `{"submission":"1","time":5}`},
		{parseSubchange, `{"submission":"1","time":5,"score":0.0,"token":false,"extra":[]}`},
		{parseSubchange, `{"submission":"1","time":5,"score":-1.5,"extra":["1","2"]}`},
	}
	for _, c := range cases {
		if _, err := c.parse(json.RawMessage(c.raw)); err != nil {
			t.Errorf("%s: %v", c.raw, err)
		}
	}
}

func TestParseInvalid(t *testing.T) {
	cases := []struct {
		parse func(json.RawMessage) (entity, error)
		raw   string
		want  string
	}{
		{parseContest, `[]`, "Not a dictionary"},
		{parseContest, `"x"`, "Not a dictionary"},
		{parseContest, `{"begin":0,"end":1,"score_precision":0}`, "'name' is missing"},
		{parseContest, `{"name":1,"begin":0,"end":1,"score_precision":0}`, "'name' isn't a string"},
		// Integers are not floats and floats are not integers (isinstance).
		{parseContest, `{"name":"C","begin":0.0,"end":1,"score_precision":0}`, "'begin' isn't an integer"},
		{parseContest, `{"name":"C","begin":0,"end":1e3,"score_precision":0}`, "'end' isn't an integer"},
		{parseContest, `{"name":"C","begin":0,"end":"1","score_precision":0}`, "'end' isn't an integer"},
		{parseContest, `{"name":"C","begin":0,"end":true,"score_precision":0}`, "'end' isn't an integer"},
		{parseContest, `{"name":"C","begin":2,"end":1,"score_precision":0}`, "'begin' is greater than 'end'"},
		{parseContest, `{"name":"C","begin":0,"end":1,"score_precision":-1}`, "'score_precision' is negative"},
		{parseContest, `{"name":"C","begin":0,"end":1,"score_precision":0,"freeze":1.5}`, "'freeze' isn't an integer"},
		{parseTask, strings.Replace(jsTask("c", "max"), "100.0", "100", 1), "'max_score' isn't a float"},
		{parseTask, strings.Replace(jsTask("c", "max"), "100.0", `"100.0"`, 1), "'max_score' isn't a float"},
		{parseTask, strings.Replace(jsTask("c", "max"), `"order":0`, `"order":0.0`, 1), "'order' isn't an integer"},
		{parseTask, strings.Replace(jsTask("c", "max"), `["s1","s2"]`, `["s1",2]`, 1), "'extra_headers' isn't a list of strings"},
		{parseTask, strings.Replace(jsTask("c", "max"), `["s1","s2"]`, `"s1"`, 1), "'extra_headers' isn't a list of strings"},
		{parseTask, strings.Replace(jsTask("c", "max"), `"score_precision":2`, `"score_precision":-2`, 1), "negative"},
		{parseTask, jsTask("c", "sum"), "Unexpected score mode"},
		{parseTask, `{"name":"T"}`, "'short_name' is missing"},
		{parseTeam, `{}`, "'name' is missing"},
		{parseUser, `{"f_name":"a","l_name":"b"}`, "'team' is missing"},
		{parseUser, `{"f_name":"a","l_name":"b","team":1}`, "'team' isn't a string (or null)"},
		{parseSubmission, `{"user":"u","task":"t","time":1.0}`, "'time' isn't an integer (unix timestamp)"},
		{parseSubmission, `{"user":"u","time":1}`, "'task' is missing"},
		{parseSubchange, `{"submission":"1","time":5,"score":50}`, "'score' isn't a float"},
		{parseSubchange, `{"submission":"1","time":5,"score":null}`, "'score' isn't a float"},
		{parseSubchange, `{"submission":"1","time":5,"token":1}`, "'token' isn't a boolean"},
		{parseSubchange, `{"submission":"1","time":5,"token":"true"}`, "'token' isn't a boolean"},
		{parseSubchange, `{"submission":"1","time":5,"extra":[1.0]}`, "'extra' isn't a list of strings"},
		{parseSubchange, `{"submission":1,"time":5}`, "'submission' isn't a string"},
	}
	for _, c := range cases {
		_, err := c.parse(json.RawMessage(c.raw))
		if err == nil || !errors.Is(err, ErrInvalidData) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want invalid data %q, got %v", c.raw, c.want, err)
		}
	}
}

// The entities round-trip: what is stored is what was sent (CMS get()).
func TestEntityRoundTrip(t *testing.T) {
	for _, c := range []struct {
		parse func(json.RawMessage) (entity, error)
		raw   string
	}{
		{parseContest, jsContest},
		{parseTask, jsTask("c", "max")},
		{parseUser, jsUser},
		{parseUser, `{"f_name":"a","l_name":"b","team":null}`},
		{parseSubmission, `{"user":"u","task":"t","time":7}`},
		{parseSubchange, `{"submission":"1","time":5,"score":50.0,"extra":["10.0"]}`},
		{parseSubchange, `{"submission":"1","time":5,"token":true}`},
	} {
		e, err := c.parse(json.RawMessage(c.raw))
		if err != nil {
			t.Fatal(err)
		}
		if got := string(encode(e)); got != c.raw {
			t.Errorf("round trip:\n got %s\nwant %s", got, c.raw)
		}
	}
}

func TestPyFloat(t *testing.T) {
	cases := map[float64]string{
		0: "0.0", 100: "100.0", -3: "-3.0", 0.5: "0.5", 33.33: "33.33", 1e-4: "0.0001",
		1e-5: "1e-05", 1e16: "1e+16", 1234567890123456: "1234567890123456.0", 1.0 / 3: "0.3333333333333333",
	}
	for f, want := range cases {
		if got := PyFloat(f); got != want {
			t.Errorf("PyFloat(%v) = %q, want %q", f, got, want)
		}
	}
	b, err := json.Marshal(map[string]Float{"a": 100})
	if err != nil || string(b) != `{"a":100.0}` {
		t.Errorf("Float marshals to %s, %v", b, err)
	}
	if _, err := json.Marshal(Float(math.NaN())); err == nil {
		t.Error("NaN marshalled")
	}
	// What Float writes parses as a float.
	if _, err := parseSubchange(json.RawMessage(`{"submission":"1","time":5,"score":` + PyFloat(1e16) + `}`)); err != nil {
		t.Error(err)
	}
}

func TestEncodeID(t *testing.T) {
	cases := map[string]string{
		"alice": "alice", "Bob42": "Bob42", "ioi-2026": "ioi_2d2026", "a_b": "a_5fb",
		"a.b": "a_2eb", "": "", "è": "_e8", "日": "_65e5",
	}
	for in, want := range cases {
		got := EncodeID(in)
		if got != want {
			t.Errorf("EncodeID(%q) = %q, want %q", in, got, want)
		}
		if got != "" && !ValidKey(got) {
			t.Errorf("EncodeID(%q) = %q is not a valid key", in, got)
		}
	}
	// CMS contest names (letters, digits, "_" and "-") round-trip.
	for _, name := range []string{"ioi-2026_day1", "plain", "__", "-_-"} {
		if got := DecodeID(EncodeID(name)); got != name {
			t.Errorf("DecodeID(EncodeID(%q)) = %q", name, got)
		}
	}
	for _, key := range []string{"no_zz", "end_", "end_4"} {
		if got := DecodeID(key); got != key {
			t.Errorf("DecodeID(%q) = %q, want it unchanged", key, got)
		}
	}
}

func TestValidKey(t *testing.T) {
	for _, k := range []string{"a", "A_9", "123"} {
		if !ValidKey(k) {
			t.Errorf("%q rejected", k)
		}
	}
	for _, k := range []string{"", "a-b", "a b", "a/b", "../x", "é", "a\n"} {
		if ValidKey(k) {
			t.Errorf("%q accepted", k)
		}
	}
}
