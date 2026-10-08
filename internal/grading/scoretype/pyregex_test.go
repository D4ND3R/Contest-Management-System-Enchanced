// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package scoretype

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// TestCodenameRegex compares the testcases a regular expression selects
// with Python 3.12's re.match (upstream retrieve_target_testcases): the
// expected codenames were computed by Python, nil when Python refuses the
// expression. Expressions RE2 cannot run are refused with a message that
// names the construct.
func TestCodenameRegex(t *testing.T) {
	codenames := []string{"000", "001", "002", "010", "011", "100", "1", "a", "A", "ab", "a_b", "a-b", "abc",
		"x9", "s1-01", "s1-10", "s2_01", "Z"}
	unsupported := map[string]string{
		`00(?=1)`:        "lookahead",
		`00(?!1)`:        "lookahead",
		`(?<=0)0`:        "lookbehind",
		`(?<!0)0`:        "lookbehind",
		`(0)\1`:          "backreferences",
		`(?P<x>0)(?P=x)`: "backreferences",
		`(0)\12`:         "backreferences",
		`(0)?(?(1)0|1)`:  "conditional",
		`(?>0)0`:         "atomic",
		`0*+`:            "possessive",
		`0{1,2}+`:        "possessive",
		`(?x) 0 0`:       "verbose",
		`\N{DIGIT ZERO}`: "named characters",
	}
	cases := []struct {
		pattern string
		python  []string
	}{
		{`0`, []string{"000", "001", "002", "010", "011"}},
		{`00.`, []string{"000", "001", "002"}},
		{`00.\Z`, []string{"000", "001", "002"}},
		{`010`, []string{"010"}},
		{`01$`, []string{}},
		{`0[0-4]$`, []string{}},
		{`\d{2}1`, []string{"001", "011"}},
		{`0{,2}1`, []string{"001", "010", "011", "100", "1"}},
		{`0{,}1`, []string{"001", "010", "011", "100", "1"}},
		{`0{1,}`, []string{"000", "001", "002", "010", "011"}},
		{`a{}`, []string{}},
		{`a{,b}`, []string{}},
		{`a{x}`, []string{}},
		{`[[:digit:]]`, []string{}},
		{`[]a]`, []string{"a", "ab", "a_b", "a-b", "abc"}},
		{`[^]0]`, []string{"100", "1", "a", "A", "ab", "a_b", "a-b", "abc", "x9", "s1-01", "s1-10", "s2_01", "Z"}},
		{`[a-]`, []string{"a", "ab", "a_b", "a-b", "abc"}},
		{`[-a]`, []string{"a", "ab", "a_b", "a-b", "abc"}},
		{`[\w-]`, []string{"000", "001", "002", "010", "011", "100", "1", "a", "A", "ab", "a_b", "a-b", "abc", "x9", "s1-01", "s1-10", "s2_01", "Z"}},
		{`[\d]1`, []string{"010", "011"}},
		{`s\d-\d+`, []string{"s1-01", "s1-10"}},
		{`s1\-`, []string{"s1-01", "s1-10"}},
		{`(?i)a`, []string{"a", "A", "ab", "a_b", "a-b", "abc"}},
		{`(?i:A)b`, []string{"ab", "abc"}},
		{`(?a)\w+$`, []string{"000", "001", "002", "010", "011", "100", "1", "a", "A", "ab", "a_b", "abc", "x9", "s2_01", "Z"}},
		{`(?u)\w`, []string{"000", "001", "002", "010", "011", "100", "1", "a", "A", "ab", "a_b", "a-b", "abc", "x9", "s1-01", "s1-10", "s2_01", "Z"}},
		{`(?#comment)00`, []string{"000", "001", "002"}},
		{`(?P<g>0)1`, []string{"010", "011"}},
		{`(?P<ñ>a)`, []string{"a", "ab", "a_b", "a-b", "abc"}},
		{`\x30\x31`, []string{"010", "011"}},
		{`\u0030`, []string{"000", "001", "002", "010", "011"}},
		{`\U00000030`, []string{"000", "001", "002", "010", "011"}},
		{`\060`, []string{"000", "001", "002", "010", "011"}},
		{`\0`, []string{}},
		{`\101`, []string{"A"}},
		{`\_`, []string{}},
		{`a|b`, []string{"a", "ab", "a_b", "a-b", "abc"}},
		{`(0|1)0`, []string{"000", "001", "002", "100"}},
		{`x\b`, []string{}},
		{`\Aab`, []string{"ab", "abc"}},
		{`s1-(01|10)\Z`, []string{"s1-01", "s1-10"}},
		{`[\x30-\x31]{3}`, []string{"000", "001", "010", "011", "100"}},
		{`[\060-\061]+$`, []string{"000", "001", "010", "011", "100", "1"}},
		{`a\-b`, []string{"a-b"}},
		{`.*1$`, []string{"001", "011", "1", "s1-01", "s2_01"}},
		{`.+_`, []string{"a_b", "s2_01"}},
		{`(?s).`, []string{"000", "001", "002", "010", "011", "100", "1", "a", "A", "ab", "a_b", "a-b", "abc", "x9", "s1-01", "s1-10", "s2_01", "Z"}},
		{`(?m)^a$`, []string{"a"}},
		{`(?-i:a)`, []string{"a", "ab", "a_b", "a-b", "abc"}},
		{`0??0`, []string{"000", "001", "002", "010", "011"}},
		{`0*?1`, []string{"001", "010", "011", "100", "1"}},
		{`[\b]`, []string{}},
		{`\b0`, []string{"000", "001", "002", "010", "011"}},
		{`00(?=1)`, []string{"001"}},
		{`00(?!1)`, []string{"000", "002"}},
		{`(?<=0)0`, []string{}},
		{`(?<!0)0`, []string{"000", "001", "002", "010", "011"}},
		{`(0)\1`, []string{"000", "001", "002"}},
		{`(?P<x>0)(?P=x)`, []string{"000", "001", "002"}},
		{`(0)?(?(1)0|1)`, []string{"000", "001", "002", "100", "1"}},
		{`(?>0)0`, []string{"000", "001", "002"}},
		{`0*+`, []string{"000", "001", "002", "010", "011", "100", "1", "a", "A", "ab", "a_b", "a-b", "abc", "x9", "s1-01", "s1-10", "s2_01", "Z"}},
		{`0{1,2}+`, []string{"000", "001", "002", "010", "011"}},
		{`(?x) 0 0`, []string{"000", "001", "002"}},
		{`\N{DIGIT ZERO}`, []string{"000", "001", "002", "010", "011"}},
		{`(0)\12`, nil},
		{`\Q`, nil},
		{`(`, nil},
		{`)`, nil},
		{`a)|(b`, nil},
		{`*`, nil},
		{`[a`, nil},
		{`\8`, nil},
		{`(?L)a`, nil},
		{`[z-a]`, nil},
		{`\x3`, nil},
		{`a**`, nil},
		{`a(?i)b`, nil},
		{`(a(?i))`, nil},
		{`(?i)(?m)a`, []string{"a", "A", "ab", "a_b", "a-b", "abc"}},
		{`(?#c)(?a)(?i)a`, []string{"a", "A", "ab", "a_b", "a-b", "abc"}},
	}
	for _, c := range cases {
		re, err := compileCodenameRegex(c.pattern)
		if want, ok := unsupported[c.pattern]; ok {
			if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "not supported") {
				t.Errorf("%s: %v, want an error about %s", c.pattern, err, want)
			}
			continue
		}
		if c.python == nil {
			if err == nil {
				t.Errorf("%s: accepted, Python refuses it", c.pattern)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.pattern, err)
			continue
		}
		got := []string{}
		for _, cn := range codenames {
			if re.MatchString(cn) {
				got = append(got, cn)
			}
		}
		if !slices.Equal(got, c.python) {
			t.Errorf("%s (RE2 %s): %q, Python %q", c.pattern, re, got, c.python)
		}
	}
}

// TestGroupPythonRegex: the parameters of a group score type may use
// Python's syntax (\Z), and constructs RE2 cannot run are refused when
// the score type is instantiated (when the dataset is saved).
func TestGroupPythonRegex(t *testing.T) {
	public := map[string]bool{"000": true, "001": true, "002": true, "010": false}
	st, err := New(NameGroupMin, json.RawMessage(`[[50, "00.\\Z"], [50, "010"]]`), public, 2)
	if err != nil {
		t.Fatal(err)
	}
	if st.MaxScore() != 100 || st.MaxPublicScore() != 50 {
		t.Errorf("max score %v, public %v", st.MaxScore(), st.MaxPublicScore())
	}
	_, err = New(NameGroupMin, json.RawMessage(`[[50, "00(?=1)"], [50, "010"]]`), public, 2)
	if err == nil || !strings.Contains(err.Error(), "lookahead assertions (?=…) are not supported") {
		t.Errorf("lookahead: %v", err)
	}
}
