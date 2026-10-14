// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cmscontrib/loaders/italy_yaml.py of CMS (the score type of
// get_task):
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2013-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2014-2018 William Di Luigi <williamdiluigi@gmail.com>
// Copyright © 2015-2019 Luca Chiodini <luca@chiodini.org>

package italy

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// Subtask is one "#ST: <points>" block of gen/GEN.
type Subtask struct {
	Points    int
	Testcases int
}

// ParseGEN reads a gen/GEN file (upstream get_task): one line per
// testcase (the command that generates it, or "#COPY: <file>"), blank
// lines and comments, and "#ST: <points>" lines that start a subtask.
// It returns the number of testcases and the subtasks (none if the file
// has no #ST: line).
func ParseGEN(r io.Reader) (testcases int, subtasks []Subtask, err error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 16<<20)
	count := 0
	var points *int
	line := 0
	for sc.Scan() {
		line++
		s := strings.TrimSpace(sc.Text())
		testcase, comment, isComment := strings.Cut(s, "#")
		if !isComment {
			if testcase != "" {
				count++
			}
			continue
		}
		testcase, comment = strings.TrimSpace(testcase), strings.TrimSpace(comment)
		isTestcase := testcase != ""
		isCopy := strings.HasPrefix(comment, "COPY:")
		isSubtask := strings.HasPrefix(comment, "ST:")
		flags := 0
		for _, b := range []bool{isTestcase, isCopy, isSubtask} {
			if b {
				flags++
			}
		}
		if flags > 1 {
			return 0, nil, fmt.Errorf("line %d: no testcase and command in the same line allowed", line)
		}
		if isTestcase || isCopy {
			count++
		}
		if isSubtask {
			if points == nil {
				if count != 0 {
					return 0, nil, fmt.Errorf("line %d: %d testcases before the first #ST: line", line, count)
				}
			} else {
				subtasks = append(subtasks, Subtask{Points: *points, Testcases: count})
			}
			count = 0
			p, err := pyInt(comment[len("ST:"):])
			if err != nil {
				return 0, nil, fmt.Errorf("line %d: invalid points %q", line, strings.TrimSpace(comment[len("ST:"):]))
			}
			points = &p
		}
	}
	if err := sc.Err(); err != nil {
		return 0, nil, err
	}
	if points == nil {
		return count, nil, nil
	}
	subtasks = append(subtasks, Subtask{Points: *points, Testcases: count})
	total := 0
	for _, st := range subtasks {
		total += st.Testcases
	}
	return total, subtasks, nil
}

// pyInt parses an integer as Python's int(): spaces around, a sign,
// underscores between digits.
func pyInt(s string) (int, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "_") || strings.HasSuffix(s, "_") || strings.Contains(s, "__") {
		return 0, strconv.ErrSyntax
	}
	return strconv.Atoi(strings.ReplaceAll(s, "_", ""))
}

// scoreType sets the score type of the dataset and returns the number of
// testcases (n_input): the score_type and score_type_parameters of
// task.yaml when given together with n_input; else GroupMin with the
// subtasks of gen/GEN; else Sum, total_value (default 100) shared among
// the testcases.
func (l *taskLoader) scoreType() (int, bool) {
	c, d, rep := l.c, l.d, l.rep
	where := c.where
	if c.has("score_type") && c.has("score_type_parameters") && c.has("n_input") {
		n, ok := c.integer("n_input")
		if !ok {
			return 0, false
		}
		if s, ok := c.str("score_type"); ok {
			d.ScoreType = s
		}
		_, v, _ := c.get("score_type_parameters")
		raw, err := toJSON(v)
		if err != nil {
			rep.Errorf(where, "score_type_parameters: %v", err)
			return 0, false
		}
		d.ScoreTypeParameters = raw
		return int(n), true
	}
	if c.has("score_type") || c.has("score_type_parameters") {
		rep.Warnf(where, "to override score type data, task.yaml must specify all 'score_type', "+
			"'score_type_parameters' and 'n_input'")
	}

	totalValue := 100.0
	if f, ok := c.number("total_value"); ok {
		totalValue = f
	}
	sum := func(n int) {
		d.ScoreType = "Sum"
		value := 0.0
		if n != 0 {
			value = totalValue / float64(n)
		}
		d.ScoreTypeParameters = json.RawMessage(pyFloat(value))
	}

	genPath := l.path("gen", "GEN")
	f, err := os.Open(genPath)
	if err != nil {
		// No gen/GEN: Sum over n_input testcases.
		if !errors.Is(err, os.ErrNotExist) {
			rep.Warnf(genPath, "%v", err)
		}
		n, ok := c.integer("n_input")
		if !ok {
			if !c.has("n_input") {
				rep.Errorf(where, "n_input is missing (there is no gen/GEN to count the testcases)")
			}
			return 0, false
		}
		sum(int(n))
		return int(n), true
	}
	defer f.Close()
	n, subtasks, err := ParseGEN(f)
	if err != nil {
		rep.Errorf(genPath, "%v", err)
		return 0, false
	}
	if subtasks == nil {
		sum(n)
	} else {
		total := 0
		params := make([]any, len(subtasks))
		for i, st := range subtasks {
			total += st.Points
			params[i] = []any{st.Points, st.Testcases}
		}
		if total != 100 {
			rep.Errorf(genPath, "the points of the subtasks add up to %d, not 100", total)
			return 0, false
		}
		d.ScoreType = "GroupMin"
		d.ScoreTypeParameters = l.params(params...)
	}
	if want, ok := c.integer("n_input"); ok && int(want) != n {
		rep.Errorf(where, "n_input is %d but gen/GEN has %d testcases", want, n)
		return 0, false
	}
	return n, true
}
