// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// SanitizeOutput is ported from execution_stats in
// cms/grading/steps/stats.py of CMS:
// Copyright © 2010-2015 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2013 Bernard Blackham <bernard@largestprime.net>
// Copyright © 2013-2014 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2014 Fabian Gundlach <320pointsguy@gmail.com>
// Copyright © 2016 Myungwoo Chun <mc.tamaki@gmail.com>
// Copyright © 2016 Amir Keivan Mohtashami <akmohtashami97@gmail.com>

package job

import (
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// FormatOutcome formats an outcome as Python's str(float) does, which is
// how upstream task types store it ("1.0", "0.0", "0.5", "1e-05"): the
// shortest representation that parses back to v, in positional notation
// with at least one decimal unless the decimal exponent is below -4 or
// above 15.
func FormatOutcome(v float64) string {
	switch {
	case math.IsNaN(v):
		return "nan"
	case math.IsInf(v, 1):
		return "inf"
	case math.IsInf(v, -1):
		return "-inf"
	}
	e := strconv.FormatFloat(v, 'e', -1, 64) // d.ddde±XX
	exp, _ := strconv.Atoi(e[strings.LastIndexByte(e, 'e')+1:])
	if v != 0 && (exp < -4 || exp >= 16) {
		return e
	}
	s := strconv.FormatFloat(v, 'f', -1, 64)
	if !strings.ContainsRune(s, '.') {
		s += ".0"
	}
	return s
}

// SanitizeOutput turns the output of a sandboxed program (compiler
// messages) into text that can be stored and shown, like upstream
// execution_stats: invalid UTF-8 and the characters U+0000-U+0008,
// U+000B, U+000C, U+000E-U+001F and U+007F-U+00BF become U+FFFD (the
// same character class as upstream, which also catches Latin-1
// punctuation), then surrounding white space is trimmed. Upstream reads
// at most 1024 bytes of each output; callers choose their own bound.
func SanitizeOutput(b []byte) string {
	var sb strings.Builder
	sb.Grow(len(b))
	for len(b) > 0 {
		r, n := utf8.DecodeRune(b)
		b = b[n:]
		if r == utf8.RuneError && n <= 1 || replacedRune(r) {
			r = utf8.RuneError
		}
		sb.WriteRune(r)
	}
	return strings.TrimFunc(sb.String(), unicode.IsSpace)
}

// replacedRune is upstream's class [\x00-\x08\x0b\x0c\x0e-\x1f\x7f-\xbf].
func replacedRune(r rune) bool {
	return r <= 0x08 || r == 0x0b || r == 0x0c || r >= 0x0e && r <= 0x1f || r >= 0x7f && r <= 0xbf
}

// TruncateUTF8 returns the longest prefix of s of at most n bytes that
// does not cut a UTF-8 sequence.
func TruncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 0 {
		return ""
	}
	i := n
	// Back up to the start of the sequence that crosses the bound.
	for i > 0 && i > n-utf8.UTFMax && !utf8.RuneStart(s[i]) {
		i--
	}
	return s[:i]
}
