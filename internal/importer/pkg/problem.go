// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package pkg

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// problem is problem.yaml. Every key but name and title is optional;
// unknown keys are errors (a typo must not be ignored silently).
type problem struct {
	Name        string   `yaml:"name"`
	Title       string   `yaml:"title"`
	Dataset     string   `yaml:"dataset,omitempty"`
	Autojudge   bool     `yaml:"autojudge,omitempty"`
	Type        string   `yaml:"type,omitempty"`
	TimeLimit   *float64 `yaml:"time_limit,omitempty"`
	MemoryLimit *int64   `yaml:"memory_limit,omitempty"`

	// Batch and BatchAndOutput.
	Input  string `yaml:"input,omitempty"`
	Output string `yaml:"output,omitempty"`
	// Batch, BatchAndOutput, OutputOnly and TwoSteps.
	Checker *checkerSpec `yaml:"checker,omitempty"`
	// Batch, BatchAndOutput, Communication and Interactive.
	Grader *bool `yaml:"grader,omitempty"`
	// Communication.
	Processes *int   `yaml:"processes,omitempty"`
	UserIO    string `yaml:"user_io,omitempty"`
	// Interactive.
	ProcessLimit            *int    `yaml:"process_limit,omitempty"`
	Concurrent              *bool   `yaml:"concurrent,omitempty"`
	ControllerMemoryLimit   *number `yaml:"controller_memory_limit,omitempty"`
	ControllerTimeLimit     *number `yaml:"controller_time_limit,omitempty"`
	ControllerWallTimeLimit *number `yaml:"controller_wall_time_limit,omitempty"`
	// BatchAndOutput.
	OutputOnlyTests *selection `yaml:"output_only_tests,omitempty"`
	OutputTests     *selection `yaml:"output_tests,omitempty"`
	// OutputOnly and BatchAndOutput: give the inputs of the tests
	// answered with an output file as attachments (default true).
	AttachInputs *bool `yaml:"attach_inputs,omitempty"`

	Scoring     *scoring   `yaml:"scoring,omitempty"`
	PublicTests *selection `yaml:"public_tests,omitempty"`

	// Lists where absent (the default) differs from empty.
	SubmissionFormat  *[]string `yaml:"submission_format,omitempty"`
	Languages         *[]string `yaml:"languages,omitempty"`
	PrimaryStatements *[]string `yaml:"primary_statements,omitempty"`
	FeedbackLevel     string    `yaml:"feedback_level,omitempty"`
	ScoreMode         string    `yaml:"score_mode,omitempty"`
	ScorePrecision    int32     `yaml:"score_precision,omitempty"`

	MaxSubmissions        *int32   `yaml:"max_submissions,omitempty"`
	MaxUserTests          *int32   `yaml:"max_user_tests,omitempty"`
	MinSubmissionInterval *float64 `yaml:"min_submission_interval,omitempty"`
	MinUserTestInterval   *float64 `yaml:"min_user_test_interval,omitempty"`
	Tokens                *tokens  `yaml:"tokens,omitempty"`
}

// tokens are the token rules of the task; durations in seconds.
type tokens struct {
	Mode        string   `yaml:"mode,omitempty"`
	MaxNumber   *int32   `yaml:"max_number,omitempty"`
	MinInterval *float64 `yaml:"min_interval,omitempty"`
	GenInitial  *int32   `yaml:"gen_initial,omitempty"`
	GenNumber   *int32   `yaml:"gen_number,omitempty"`
	GenInterval *float64 `yaml:"gen_interval,omitempty"`
	GenMax      *int32   `yaml:"gen_max,omitempty"`
}

// scoring is the score type: Sum, or a group score type with subtasks.
type scoring struct {
	Type string `yaml:"type,omitempty"`
	// Points: with Sum, the total shared equally among the tests (default
	// 100); with subtasks, what their points must add up to (a warning
	// when they do not add up to 100 and Points is not given).
	Points *number `yaml:"points,omitempty"`
	// PerTest: with Sum, the points of each test (instead of Points).
	PerTest  *number   `yaml:"per_test,omitempty"`
	Subtasks []subtask `yaml:"subtasks,omitempty"`
}

// subtask is a group of tests; exactly one of Tests, Regex and Count
// names its tests, and every subtask must use the same one.
type subtask struct {
	Points *number    `yaml:"points"`
	Tests  *selection `yaml:"tests,omitempty"`
	// Regex is a Python regular expression matched at the start of the
	// test names (re.match).
	Regex *string `yaml:"regex,omitempty"`
	// Count takes the next Count tests in name order (CMS's own form).
	Count *int `yaml:"count,omitempty"`
	// Threshold is the threshold of GroupThreshold.
	Threshold       *number `yaml:"threshold,omitempty"`
	AlwaysShowTests *bool   `yaml:"always_show_tests,omitempty"`
}

// number is a YAML number that remembers whether it was an integer, so
// that the JSON parameters keep it as CMS does: 40 stays 40, and 40.0
// (a Python float) stays 40.0. It is written to JSON as Python writes
// it (MarshalJSON) and to YAML so that it reads back the same.
type number struct {
	isInt bool
	i     int64
	f     float64
}

func intNumber(i int64) *number     { return &number{isInt: true, i: i, f: float64(i)} }
func floatNumber(f float64) *number { return &number{f: f} }

func (n *number) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		switch node.ShortTag() {
		case "!!int":
			if err := node.Decode(&n.i); err == nil {
				n.isInt, n.f = true, float64(n.i)
				return nil
			}
		case "!!float":
			if err := node.Decode(&n.f); err == nil && !math.IsNaN(n.f) && !math.IsInf(n.f, 0) {
				return nil
			}
		}
	}
	return fmt.Errorf("line %d: expected a number, got %q", node.Line, node.Value)
}

func (n number) MarshalYAML() (any, error) {
	if n.isInt {
		return n.i, nil
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!float", Value: pyFloat(n.f)}, nil
}

func (n number) MarshalJSON() ([]byte, error) {
	if n.isInt {
		return strconv.AppendInt(nil, n.i, 10), nil
	}
	if math.IsNaN(n.f) || math.IsInf(n.f, 0) {
		return nil, fmt.Errorf("%v is not a number for JSON", n.f)
	}
	return []byte(pyFloat(n.f)), nil
}

// pyFloat formats a float as Python's repr (what CMS writes in the JSON
// parameters): the shortest representation, with ".0" when integral, and
// an exponent below 1e-4 and from 1e16.
func pyFloat(f float64) string {
	if f == 0 {
		if math.Signbit(f) {
			return "-0.0"
		}
		return "0.0"
	}
	e := strconv.FormatFloat(f, 'e', -1, 64)
	exp := 0
	if i := strings.IndexByte(e, 'e'); i >= 0 {
		exp, _ = strconv.Atoi(e[i+1:])
	}
	if exp < -4 || exp >= 16 {
		return e
	}
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}

// checkerSpec is how outputs are checked: a name, or {type, tolerance}.
type checkerSpec struct {
	Type      string   `yaml:"type"`
	Tolerance *float64 `yaml:"tolerance,omitempty"`
}

func (c *checkerSpec) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		c.Type = node.Value
		return nil
	}
	type plain checkerSpec
	var p plain
	if err := decodeStrict(node, &p); err != nil {
		return err
	}
	*c = checkerSpec(p)
	return nil
}

func (c checkerSpec) MarshalYAML() (any, error) {
	if c.Tolerance == nil {
		return c.Type, nil
	}
	type plain checkerSpec
	return plain(c), nil
}

// selection names tests: "all", or items separated by commas (or a YAML
// list of items). An item is a test name, a number (the test with that
// name, or the only one whose name is that number with leading zeros:
// 7 is "07") or a range of numbers "3-7".
type selection struct {
	all   bool
	items []string
}

func (s *selection) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		if node.ShortTag() == "!!null" {
			return nil
		}
		s.addItems(node.Value)
		return nil
	case yaml.SequenceNode:
		for _, n := range node.Content {
			if n.Kind != yaml.ScalarNode {
				return fmt.Errorf("line %d: expected test names or numbers", n.Line)
			}
			s.addItems(n.Value)
		}
		return nil
	}
	return fmt.Errorf("line %d: expected \"all\", test names, numbers or ranges", node.Line)
}

func (s *selection) addItems(v string) {
	for _, it := range strings.Split(v, ",") {
		it = strings.TrimSpace(it)
		switch {
		case it == "":
		case it == "all":
			s.all = true
		default:
			s.items = append(s.items, it)
		}
	}
}

func (s selection) MarshalYAML() (any, error) { return s.String(), nil }

func (s selection) String() string {
	if s.all {
		return "all"
	}
	return strings.Join(s.items, ", ")
}

// empty reports whether the selection names nothing.
func (s *selection) empty() bool { return s == nil || !s.all && len(s.items) == 0 }

// resolve returns the tests (of names, sorted) that the selection names,
// in that order, and a message for each item that names none.
func (s *selection) resolve(names []string) (out []string, bad []string) {
	if s == nil {
		return nil, nil
	}
	if s.all {
		return append([]string(nil), names...), nil
	}
	exact := make(map[string]bool, len(names))
	byNumber := map[int][]string{}
	for _, n := range names {
		exact[n] = true
		if v, ok := numeric(n); ok {
			byNumber[v] = append(byNumber[v], n)
		}
	}
	chosen := map[string]bool{}
	number := func(v int) {
		if s := strconv.Itoa(v); exact[s] {
			chosen[s] = true
			return
		}
		switch m := byNumber[v]; len(m) {
		case 0:
			bad = append(bad, fmt.Sprintf("there is no test %d", v))
		case 1:
			chosen[m[0]] = true
		default:
			bad = append(bad, fmt.Sprintf("test %d is ambiguous (%s): use its name", v, strings.Join(m, ", ")))
		}
	}
	for _, it := range s.items {
		if exact[it] {
			chosen[it] = true
			continue
		}
		if v, ok := numeric(it); ok {
			number(v)
			continue
		}
		if a, b, ok := numberRange(it); ok {
			if a > b || b-a > 1_000_000 {
				bad = append(bad, fmt.Sprintf("invalid range %q", it))
				continue
			}
			for v := a; v <= b; v++ {
				number(v)
			}
			continue
		}
		bad = append(bad, fmt.Sprintf("there is no test %q", it))
	}
	for _, n := range names {
		if chosen[n] {
			out = append(out, n)
		}
	}
	return out, bad
}

// numeric parses a name made only of digits.
func numeric(s string) (int, bool) {
	if s == "" || len(s) > 9 {
		return 0, false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	v, err := strconv.Atoi(s)
	return v, err == nil
}

// numberRange parses "a-b" (spaces allowed around the dash).
func numberRange(s string) (int, int, bool) {
	a, b, ok := strings.Cut(s, "-")
	if !ok {
		return 0, 0, false
	}
	x, ok1 := numeric(strings.TrimSpace(a))
	y, ok2 := numeric(strings.TrimSpace(b))
	return x, y, ok1 && ok2
}

// selectionOf returns the shortest selection that resolves to chosen
// among names (both sorted): "all", ranges of numbered tests, names.
func selectionOf(chosen, names []string) *selection {
	if len(chosen) == 0 {
		return nil
	}
	if len(chosen) == len(names) {
		return &selection{all: true}
	}
	var nums []struct {
		v    int
		name string
	}
	var others []string
	for _, c := range chosen {
		if v, ok := numeric(c); ok {
			nums = append(nums, struct {
				v    int
				name string
			}{v, c})
		} else {
			others = append(others, c)
		}
	}
	// Numbered tests by value, in runs of consecutive values.
	for i := 1; i < len(nums); i++ {
		for j := i; j > 0 && nums[j].v < nums[j-1].v; j-- {
			nums[j], nums[j-1] = nums[j-1], nums[j]
		}
	}
	var items []string
	for i := 0; i < len(nums); {
		j := i
		for j+1 < len(nums) && nums[j+1].v == nums[j].v+1 {
			j++
		}
		if j-i >= 2 {
			items = append(items, nums[i].name+"-"+nums[j].name)
		} else {
			for k := i; k <= j; k++ {
				items = append(items, nums[k].name)
			}
		}
		i = j + 1
	}
	s := &selection{items: append(items, others...)}
	if got, bad := s.resolve(names); len(bad) == 0 && equal(got, chosen) {
		return s
	}
	return &selection{items: append([]string(nil), chosen...)}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// decodeStrict decodes a node rejecting unknown keys.
func decodeStrict(node *yaml.Node, v any) error {
	data, err := yaml.Marshal(node)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("line %d: %w", node.Line, err)
	}
	return nil
}

// parseProblem decodes problem.yaml.
func parseProblem(data []byte) (*problem, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var p problem
	if err := dec.Decode(&p); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("problem.yaml is empty")
		}
		return nil, err
	}
	return &p, nil
}
