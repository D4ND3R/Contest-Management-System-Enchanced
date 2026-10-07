// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package scoring

import (
	"bytes"
	"encoding/json"
	"strconv"
)

// parseSubtaskScores reads the idx and score of each subtask from score
// details (upstream: dict((st["idx"], st["score"]) for st in details));
// ok is false if they are not a list of objects that all have both keys.
//
// It scans the JSON instead of decoding it: details hold every testcase,
// and max_subtask reads them for every submission of every participant
// (encoding/json took 4 ms for 50 submissions of 100 testcases, this
// scanner takes about 1 ms: BenchmarkTaskScoreMaxSubtask). The
// details come from a jsonb column, so they are valid JSON; anything
// unexpected makes ok false.
func parseSubtaskScores(details []byte) (m subtaskScores, ok bool) {
	s := scanner{b: details}
	if !s.eat('[') {
		return m, false
	}
	if s.eat(']') {
		return m, s.end()
	}
	for {
		idx, score, ok := s.object()
		if !ok || idx == nil || score == nil {
			return m, false
		}
		k, ok := parseKey(idx)
		if !ok {
			return m, false
		}
		// CMS fails later on a score that is not a number; count the
		// details as not grouped instead.
		v, err := strconv.ParseFloat(string(score), 64)
		if err != nil {
			return m, false
		}
		m.set(k, v)
		if s.eat(',') {
			continue
		}
		if s.eat(']') {
			return m, s.end()
		}
		return m, false
	}
}

// scanner walks a JSON document.
type scanner struct {
	b []byte
	i int
}

func (s *scanner) ws() {
	for s.i < len(s.b) {
		switch s.b[s.i] {
		case ' ', '\t', '\n', '\r':
			s.i++
		default:
			return
		}
	}
}

// eat skips spaces and then c, if it is next.
func (s *scanner) eat(c byte) bool {
	s.ws()
	if s.i < len(s.b) && s.b[s.i] == c {
		s.i++
		return true
	}
	return false
}

// end reports whether only spaces are left.
func (s *scanner) end() bool {
	s.ws()
	return s.i == len(s.b)
}

// object reads an object and returns the raw values of its keys "idx" and
// "score" (nil if missing; the last one wins, as in Python). ok is false
// if the next value is not an object.
func (s *scanner) object() (idx, score []byte, ok bool) {
	if !s.eat('{') {
		return nil, nil, false
	}
	if s.eat('}') {
		return nil, nil, true
	}
	for {
		s.ws()
		start := s.i
		if s.i >= len(s.b) || s.b[s.i] != '"' || !s.skipString() {
			return nil, nil, false
		}
		key := s.b[start+1 : s.i-1]
		if bytes.IndexByte(key, '\\') >= 0 {
			var k string
			if json.Unmarshal(s.b[start:s.i], &k) != nil {
				return nil, nil, false
			}
			key = []byte(k)
		}
		if !s.eat(':') {
			return nil, nil, false
		}
		s.ws()
		vstart := s.i
		if !s.skipValue() {
			return nil, nil, false
		}
		switch string(key) {
		case "idx":
			idx = s.b[vstart:s.i]
		case "score":
			score = s.b[vstart:s.i]
		}
		if s.eat(',') {
			continue
		}
		if s.eat('}') {
			return idx, score, true
		}
		return nil, nil, false
	}
}

// skipString skips the string that starts at s.i.
func (s *scanner) skipString() bool {
	end, ok := stringEnd(s.b, s.i)
	s.i = end
	return ok
}

// stringEnd returns the index after the string that starts at b[open]
// (a quote): it jumps from quote to quote and stops at the first one that
// is not escaped (an even number of backslashes precede it).
func stringEnd(b []byte, open int) (int, bool) {
	i := open + 1
	for {
		j := bytes.IndexByte(b[i:], '"')
		if j < 0 {
			return len(b), false
		}
		i += j
		backslashes := 0
		for k := i - 1; k > open && b[k] == '\\'; k-- {
			backslashes++
		}
		i++
		if backslashes%2 == 0 {
			return i, true
		}
	}
}

// structural marks the bytes containerEnd stops at.
var structural = [256]bool{'"': true, '[': true, ']': true, '{': true, '}': true}

// skipValue skips the value that starts at s.i.
func (s *scanner) skipValue() bool {
	if s.i >= len(s.b) {
		return false
	}
	switch s.b[s.i] {
	case '"':
		return s.skipString()
	case '[', '{':
		end, ok := containerEnd(s.b, s.i)
		s.i = end
		return ok
	}
	// A number, true, false or null.
	start := s.i
	for s.i < len(s.b) {
		switch s.b[s.i] {
		case ',', ']', '}', ' ', '\t', '\n', '\r':
			return s.i > start
		}
		s.i++
	}
	return s.i > start
}

// containerEnd returns the index after the list or object that starts at
// b[i]. It only looks at strings and brackets (on local variables: this is
// the hot loop of max_subtask, which skips the testcases of every
// subtask).
func containerEnd(b []byte, i int) (int, bool) {
	depth := 0
	for i < len(b) {
		c := b[i]
		if !structural[c] {
			i++
			continue
		}
		switch c {
		case '"':
			// Inline: the strings of the details are short, and a call to
			// stringEnd costs more than walking them.
			i++
			for i < len(b) && b[i] != '"' {
				if b[i] == '\\' {
					i++
				}
				i++
			}
			if i >= len(b) {
				return len(b), false
			}
			i++
		case '[', '{':
			depth++
			i++
		default:
			depth--
			i++
			if depth == 0 {
				return i, true
			}
		}
	}
	return len(b), false
}
