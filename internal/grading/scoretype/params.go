// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package scoretype

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// selector is how a subtask names its testcases.
type selector int

const (
	// selCount: the next n testcases in codename order.
	selCount selector = iota + 1
	// selRegex: the testcases whose codename matches a regular expression
	// at its start (Python re.match; see compileCodenameRegex).
	selRegex
	// selList: a list of codenames.
	selList
)

// groupParam is one subtask of the parameters of a group score type, given
// as [max_score, testcases(, threshold)] or as {"max_score": …,
// "testcases": …, "threshold": …, "always_show_testcases": …}.
type groupParam struct {
	maxScore   float64
	sel        selector
	count      int
	regex      string
	list       []string
	threshold  float64
	alwaysShow bool
}

// parseGroupParams decodes the parameters of a group score type;
// withThreshold requires the threshold of GroupThreshold. Unlike CMS,
// booleans are not numbers, and a missing threshold is an error here
// rather than when scoring.
func parseGroupParams(raw json.RawMessage, withThreshold bool) ([]groupParam, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '[' {
		return nil, errors.New("the parameters must be a list of subtasks")
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	params := make([]groupParam, len(items))
	for i, item := range items {
		item = bytes.TrimSpace(item)
		var maxScore, testcases, threshold, always json.RawMessage
		switch {
		case len(item) > 0 && item[0] == '[':
			var a []json.RawMessage
			if err := json.Unmarshal(item, &a); err != nil {
				return nil, fmt.Errorf("subtask %d: %w", i, err)
			}
			if len(a) < 2 || withThreshold && len(a) < 3 {
				if withThreshold {
					return nil, fmt.Errorf("subtask %d: expected [max_score, testcases, threshold]", i)
				}
				return nil, fmt.Errorf("subtask %d: expected [max_score, testcases]", i)
			}
			maxScore, testcases = a[0], a[1]
			if withThreshold {
				threshold = a[2]
			}
		case len(item) > 0 && item[0] == '{':
			var o map[string]json.RawMessage
			if err := json.Unmarshal(item, &o); err != nil {
				return nil, fmt.Errorf("subtask %d: %w", i, err)
			}
			var ok bool
			if maxScore, ok = o["max_score"]; !ok {
				return nil, fmt.Errorf("subtask %d: missing max_score", i)
			}
			if testcases, ok = o["testcases"]; !ok {
				return nil, fmt.Errorf("subtask %d: missing testcases", i)
			}
			if threshold, ok = o["threshold"]; withThreshold && !ok {
				return nil, fmt.Errorf("subtask %d: missing threshold", i)
			}
			always = o["always_show_testcases"]
		default:
			return nil, fmt.Errorf("subtask %d: expected a list or an object, got %s", i, item)
		}

		p := &params[i]
		var ok bool
		if p.maxScore, _, ok = number(maxScore); !ok {
			return nil, fmt.Errorf("subtask %d: the max score must be a number, got %s", i, maxScore)
		}
		if withThreshold {
			if p.threshold, _, ok = number(threshold); !ok {
				return nil, fmt.Errorf("subtask %d: the threshold must be a number, got %s", i, threshold)
			}
		}
		if always != nil && !isNull(bytes.TrimSpace(always)) {
			if err := json.Unmarshal(always, &p.alwaysShow); err != nil {
				return nil, fmt.Errorf("subtask %d: always_show_testcases must be a boolean, got %s", i, always)
			}
		}
		if err := p.parseTestcases(bytes.TrimSpace(testcases)); err != nil {
			return nil, fmt.Errorf("subtask %d: %w", i, err)
		}
	}
	for i := range params {
		if params[i].sel != params[0].sel {
			return nil, errors.New("in the score type parameters, the second value of each element " +
				"must have the same type (int, unicode or list of strings)")
		}
	}
	return params, nil
}

func (p *groupParam) parseTestcases(raw json.RawMessage) error {
	bad := fmt.Errorf("the testcases must be a number of testcases, a regular expression or a list of codenames, got %s", raw)
	if len(raw) == 0 {
		return bad
	}
	switch raw[0] {
	case '"':
		p.sel = selRegex
		return json.Unmarshal(raw, &p.regex)
	case '[':
		p.sel = selList
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return err
		}
		p.list = make([]string, len(items))
		for i, it := range items {
			it = bytes.TrimSpace(it)
			if len(it) == 0 || it[0] != '"' {
				return bad
			}
			if err := json.Unmarshal(it, &p.list[i]); err != nil {
				return err
			}
		}
		return nil
	}
	if _, isInt, ok := number(raw); ok && isInt {
		n, err := strconv.Atoi(string(raw))
		if err != nil || n < 0 {
			return fmt.Errorf("invalid number of testcases %s", raw)
		}
		p.sel, p.count = selCount, n
		return nil
	}
	return bad
}

// number decodes a JSON number; isInt reports whether it is written as an
// integer (a Python int). Strings, booleans and null are not numbers.
func number(raw json.RawMessage) (f float64, isInt, ok bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '-' && (raw[0] < '0' || raw[0] > '9') {
		return 0, false, false
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, false, false
	}
	f, err := n.Float64()
	if err != nil {
		return 0, false, false
	}
	return f, !bytes.ContainsAny(raw, ".eE"), true
}

// targets returns the codenames of the testcases of each subtask
// (upstream retrieve_target_testcases), given the codenames of the dataset
// sorted. Unlike CMS, a subtask without testcases is an error, and so is a
// count that goes past the last testcase (CMS silently gave the subtask
// fewer testcases, or failed when scoring).
func targets(params []groupParam, public map[string]bool, codenames []string) ([][]string, error) {
	out := make([][]string, len(params))
	next := 0
	for i := range params {
		p := &params[i]
		switch p.sel {
		case selCount:
			if next+p.count > len(codenames) {
				return nil, fmt.Errorf("subtask %d needs testcases %d to %d, but the dataset has %d testcases",
					i, next+1, next+p.count, len(codenames))
			}
			out[i] = codenames[next : next+p.count : next+p.count]
			next += p.count
		case selRegex:
			// Python's re.match, anchored at the start.
			re, err := compileCodenameRegex(p.regex)
			if err != nil {
				return nil, fmt.Errorf("subtask %d: invalid regular expression %q: %w", i, p.regex, err)
			}
			for _, c := range codenames {
				if re.MatchString(c) {
					out[i] = append(out[i], c)
				}
			}
			if len(out[i]) == 0 {
				return nil, fmt.Errorf("no testcase matches against the regexp '%s'", p.regex)
			}
		case selList:
			for _, c := range p.list {
				if _, ok := public[c]; !ok {
					return nil, fmt.Errorf("subtask %d: unknown testcase %q", i, c)
				}
			}
			out[i] = p.list
		}
		if len(out[i]) == 0 {
			return nil, fmt.Errorf("subtask %d has no testcases", i)
		}
	}
	return out, nil
}
