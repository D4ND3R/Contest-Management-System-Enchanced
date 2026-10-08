// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/grading/steps/trusted.py (extract_outcome_and_text) of
// CMS:
// Copyright © 2010-2015 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2013 Bernard Blackham <bernard@largestprime.net>
// Copyright © 2013-2014 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2014 Fabian Gundlach <320pointsguy@gmail.com>
// Copyright © 2016 Myungwoo Chun <mc.tamaki@gmail.com>
// Copyright © 2016 Amir Keivan Mohtashami <akmohtashami97@gmail.com>

package checker

import (
	"fmt"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/steps"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
)

// adminPrefix starts the lines of the standard error of a manager that
// are for the admins only.
const adminPrefix = "ADMIN_MESSAGE:"

// translatePrefix asks for a stock message: "translate:success",
// "translate:partial" or "translate:wrong".
const translatePrefix = "translate:"

// maxMessage bounds the text and the admin text taken from a manager
// (before escaping, which at most doubles them), so that results stay
// within the limits of the judge protocol.
const maxMessage = job.MaxTextLen / 2

// ParseManagerOutput reads the standard manager output (upstream
// extract_outcome_and_text): the first line of stdout is the outcome, a
// finite number as Python's float() reads it (PyFloat); the first line of stderr is the text for the
// contestant; the following lines of stderr starting with
// "ADMIN_MESSAGE:" are joined into the admin text, and the others are
// ignored. A text "translate:success", "translate:partial" or
// "translate:wrong" becomes the stock message, translated when shown.
// Lines end at \n, \r or \r\n and are trimmed. Percent signs of the texts
// are doubled (they are formatted later) and control characters are
// refused.
//
// An error means the manager broke the protocol: not UTF-8, not a number,
// control characters (the admins must fix it).
func ParseManagerOutput(stdout, stderr []byte) (*Outcome, error) {
	first, _, _ := strings.Cut(normalizeNewlines(stdout), "\n")
	if !utf8.ValidString(first) {
		return nil, fmt.Errorf("manager stdout (outcome) is not valid UTF-8")
	}
	outcome := pyStrip(first)
	if !utf8.Valid(stderr) {
		return nil, fmt.Errorf("manager stderr (text) is not valid UTF-8")
	}
	lines := strings.Split(normalizeNewlines(stderr), "\n")
	text, err := sanitizeMessage(pyStrip(lines[0]))
	if err != nil {
		return nil, fmt.Errorf("manager stderr (text) is malformed: %w", err)
	}
	var admin []string
	for _, line := range lines[1:] {
		line = pyStrip(line)
		rest, ok := strings.CutPrefix(line, adminPrefix)
		if !ok {
			continue // unknown special command (upstream logs it)
		}
		msg, err := sanitizeMessage(pyStrip(rest))
		if err != nil {
			return nil, fmt.Errorf("manager stderr (admin message) is malformed: %w", err)
		}
		admin = append(admin, msg)
	}

	v, err := PyFloat(outcome)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return nil, fmt.Errorf("wrong outcome `%s' from manager: not a finite number", job.TruncateUTF8(outcome, 64))
	}
	if rest, ok := strings.CutPrefix(text, translatePrefix); ok {
		switch key := pyStrip(rest); key {
		case steps.EvaluationSuccess, steps.EvaluationPartial, steps.EvaluationWrong:
			text = steps.EvaluationMessages.Get(key).Message
		}
		// Otherwise the text stays as it is (upstream logs a warning).
	}
	return &Outcome{
		Outcome:   v,
		Text:      []string{text},
		AdminText: job.TruncateUTF8(strings.Join(admin, " "), job.MaxTextLen),
	}, nil
}

// normalizeNewlines turns \r\n and \r into \n (Python's universal
// newlines, how upstream reads the files).
func normalizeNewlines(b []byte) string {
	s := string(b)
	if !strings.ContainsRune(s, '\r') {
		return s
	}
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

// pyStrip trims white space like Python's str.strip(): Unicode white
// space plus the separators U+001C-U+001F.
func pyStrip(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || r >= 0x1c && r <= 0x1f
	})
}

// sanitizeMessage refuses control characters (upstream's class
// [\x00-\x08\x0a-\x1f\x7f-\xbf], which also catches Latin-1 punctuation)
// and doubles percent signs, so that the text is not taken as a format.
func sanitizeMessage(s string) (string, error) {
	for _, r := range s {
		if r <= 0x08 || r >= 0x0a && r <= 0x1f || r >= 0x7f && r <= 0xbf {
			return "", fmt.Errorf("invalid character in outcome: 0x%02x", r)
		}
	}
	return strings.ReplaceAll(job.TruncateUTF8(s, maxMessage), "%", "%%"), nil
}
