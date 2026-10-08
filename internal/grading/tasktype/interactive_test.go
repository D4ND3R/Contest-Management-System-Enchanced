// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package tasktype

import (
	"bufio"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
)

func TestParseControllerFeedback(t *testing.T) {
	cases := []struct {
		in    string
		score float64
		text  []string
		admin string
	}{
		{"", 0, nil, ""},
		{"SCORE: 1.000000\nUSER_MESSAGE: OK!\n", 1, []string{"OK!"}, ""},
		{"SCORE: 0.000000\nUSER_MESSAGE: Wrong answer\nADMIN_MESSAGE: Expected 360, got 0\n", 0,
			[]string{"Wrong answer"}, "Expected 360, got 0"},
		{"  USER_MESSAGE:   50% done  \r\nSCORE: 0.5", 0.5, []string{"50%% done"}, ""},
		{"ADMIN_MESSAGE: only admins\n", 0, nil, "only admins"},
		{"USER_MESSAGE:  .\n", 0, []string{"."}, ""},
		// Python's float(): underscores between digits, other scripts.
		{"SCORE: 1_0\n", 10, nil, ""},
		{"SCORE: \u0660.\u0665\n", 0.5, nil, ""},
	}
	for _, c := range cases {
		score, text, admin, err := parseControllerFeedback([]byte(c.in))
		if err != nil || score != c.score || !slices.Equal(text, c.text) || admin != c.admin {
			t.Errorf("%q: %v %q %q %v", c.in, score, text, admin, err)
		}
	}
	for _, in := range []string{
		"SCORE: 1\nSCORE: 1\n",
		"USER_MESSAGE: a\nUSER_MESSAGE: b\n",
		"ADMIN_MESSAGE: a\nADMIN_MESSAGE: b\n",
		"SCORE: lots\n",
		"SCORE: nan\n",
		"SCORE: 0x1p-1\n", // not for Python
		"SCORE: 1e400\n",
		"SCORE: 1\n\n",
		"USER_MESSAGE: \n", // stripped, the prefix loses its space (upstream too)
		"Traceback (most recent call last):\n",
		"USER_MESSAGE: \xff\n",
		"USER_MESSAGE: a\x00b\n",
	} {
		if _, _, _, err := parseControllerFeedback([]byte(in)); err == nil {
			t.Errorf("%q was accepted", in)
		}
	}
}

func TestReadControllerLine(t *testing.T) {
	r := bufio.NewReaderSize(strings.NewReader("START_SOLUTION\n START_SOLUTION \r\nlast"), maxControllerLine)
	for _, want := range []string{"START_SOLUTION\n", " START_SOLUTION \r\n", "last"} {
		line, err := readControllerLine(r)
		if err != nil || line != want {
			t.Fatalf("line %q, %v; want %q", line, err, want)
		}
		if pyStrip(line) != strings.TrimSpace(want) {
			t.Errorf("pyStrip(%q) = %q", line, pyStrip(line))
		}
	}
	if _, err := readControllerLine(r); err != io.EOF {
		t.Errorf("at the end: %v", err)
	}
	r = bufio.NewReaderSize(strings.NewReader(strings.Repeat("x", 2*maxControllerLine)), maxControllerLine)
	if _, err := readControllerLine(r); !errors.Is(err, ErrConfiguration) {
		t.Errorf("long line: %v", err)
	}
	r = bufio.NewReaderSize(strings.NewReader("START\xff\n"), maxControllerLine)
	if _, err := readControllerLine(r); !errors.Is(err, ErrConfiguration) {
		t.Errorf("invalid UTF-8: %v", err)
	}
}
