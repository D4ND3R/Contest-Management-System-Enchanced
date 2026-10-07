// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/grading/steps/whitediff.py of CMS:
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
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// whites are the white space characters of the white diff: the Unicode
// White_Space characters in the ASCII range (upstream _WHITES).
var whites = [256]bool{' ': true, '\t': true, '\n': true, '\v': true, '\f': true, '\r': true}

func isWhite(c byte) bool { return whites[c] }

// sameTokens reports whether a and b have the same tokens (runs of bytes
// that are not white space), that is the same canonical form.
func sameTokens(a, b []byte) bool {
	i, j := 0, 0
	for {
		for i < len(a) && whites[a[i]] {
			i++
		}
		for j < len(b) && whites[b[j]] {
			j++
		}
		if i == len(a) || j == len(b) {
			return i == len(a) && j == len(b)
		}
		si, sj := i, j
		for i < len(a) && !whites[a[i]] {
			i++
		}
		for j < len(b) && !whites[b[j]] {
			j++
		}
		if !bytes.Equal(a[si:i], b[sj:j]) {
			return false
		}
	}
}

// shownLen bounds how much of a line the admin text quotes (upstream
// LENGTH_LIMIT).
const shownLen = 100

// bufSize is the read buffer of each compared file.
const bufSize = 64 << 10

// Admin texts of the comparisons (upstream; not translated).
const (
	tooLong  = "Contestant output too long"
	tooShort = "Contestant output too short"
)

// WhiteDiff compares two files line by line, ignoring white space
// (upstream _white_diff): they are equal if for every i, line i of output
// equals line i of correct, where two lines are equal if they differ only
// by the number or kind of white space characters (space, \t, \n, \v, \f,
// \r), and trailing lines made only of white space do not count. A line
// is a sequence of bytes ending with \n or at the end of the file.
//
// When they differ, adminText tells where, like upstream: "Contestant
// output too long", "Contestant output too short" or "Expected `x`, found
// `y` on line N" (each line canonicalized, cut at 100 bytes plus "...").
// The files are streamed: memory does not grow with their size or the
// length of their lines. err reports a read error only.
func WhiteDiff(output, correct io.Reader) (equal bool, adminText string, err error) {
	out := &lineReader{r: bufio.NewReaderSize(output, bufSize)}
	res := &lineReader{r: bufio.NewReaderSize(correct, bufSize)}
	for line := 1; ; line++ {
		outEOF, err := out.atEOF()
		if err != nil {
			return false, "", err
		}
		resEOF, err := res.atEOF()
		if err != nil {
			return false, "", err
		}
		switch {
		case outEOF && resEOF:
			return true, "", nil
		case outEOF || resEOF:
			// Only one file finished: fine if the other has only blanks.
			if !outEOF {
				blank, err := out.blankLine()
				if err != nil {
					return false, "", err
				}
				if !blank {
					return false, tooLong, nil
				}
			}
			if !resEOF {
				blank, err := res.blankLine()
				if err != nil {
					return false, "", err
				}
				if !blank {
					return false, tooShort, nil
				}
			}
		default:
			// Fast path, for lines that are whole in the buffers and equal
			// (the reading below byte by byte also builds the admin text).
			if lo, lr := out.bufferedLine(), res.bufferedLine(); lo > 0 && lr > 0 {
				a, _ := out.r.Peek(lo)
				b, _ := res.r.Peek(lr)
				if lo == lr && bytes.Equal(a, b) || sameTokens(a, b) {
					_, _ = out.r.Discard(lo)
					_, _ = res.r.Discard(lr)
					continue
				}
			}
			out.startLine()
			res.startLine()
			for {
				a, okA, err := out.next()
				if err != nil {
					return false, "", err
				}
				b, okB, err := res.next()
				if err != nil {
					return false, "", err
				}
				if !okA && !okB {
					break // same canonical line
				}
				if okA != okB || a != b {
					lout, err := out.shownLine()
					if err != nil {
						return false, "", err
					}
					lres, err := res.shownLine()
					if err != nil {
						return false, "", err
					}
					return false, mismatchText(lres, lout, line), nil
				}
			}
		}
	}
}

// mismatchText is upstream's "Expected `...`, found `...` on line N".
func mismatchText(expected, found []byte, line int) string {
	return fmt.Sprintf("Expected `%s`, found `%s` on line %d", showLine(expected), showLine(found), line)
}

// showLine cuts a line to shownLen bytes plus "..." and decodes it like
// Python's decode("utf-8", errors="backslashreplace"), with NUL escaped
// too (PostgreSQL cannot store it).
func showLine(b []byte) string {
	cut := len(b) > shownLen
	if cut {
		b = b[:shownLen]
	}
	var sb strings.Builder
	for len(b) > 0 {
		r, n := utf8.DecodeRune(b)
		switch {
		case r == utf8.RuneError && n <= 1:
			fmt.Fprintf(&sb, `\x%02x`, b[0])
			n = 1
		case r == 0:
			sb.WriteString(`\x00`)
		default:
			sb.Write(b[:n])
		}
		b = b[n:]
	}
	if cut {
		sb.WriteString("...")
	}
	return sb.String()
}

// lineReader reads a file line by line, giving the bytes of each line in
// canonical form: white space runs become one space, none at the ends
// (upstream _white_diff_canonicalize).
type lineReader struct {
	r *bufio.Reader
	// State of the current line.
	pendingSpace bool // white space after a token
	started      bool // a token was seen
	done         bool // the line (or the file) ended
	// shown holds the first shownLen+1 canonical bytes of the line.
	shown []byte
}

// atEOF reports whether the file has no more bytes (upstream: readline
// returns an empty string).
func (l *lineReader) atEOF() (bool, error) {
	_, err := l.r.Peek(1)
	if errors.Is(err, io.EOF) {
		return true, nil
	}
	return false, err
}

// bufferedLine returns the length of the current line with its \n when
// it is all in the buffer, or 0.
func (l *lineReader) bufferedLine() int {
	b, _ := l.r.Peek(l.r.Buffered())
	return bytes.IndexByte(b, '\n') + 1
}

func (l *lineReader) startLine() {
	l.pendingSpace, l.started, l.done = false, false, false
	l.shown = l.shown[:0]
}

// next returns the next canonical byte of the current line, or ok false at
// its end (the \n is consumed).
func (l *lineReader) next() (c byte, ok bool, err error) {
	for !l.done {
		c, err = l.r.ReadByte()
		if errors.Is(err, io.EOF) {
			l.done = true
			return 0, false, nil
		}
		if err != nil {
			return 0, false, err
		}
		if c == '\n' {
			l.done = true
			return 0, false, nil
		}
		if isWhite(c) {
			if l.started {
				l.pendingSpace = true
			}
			continue
		}
		if l.pendingSpace {
			l.pendingSpace = false
			_ = l.r.UnreadByte()
			c = ' '
		}
		l.started = true
		if len(l.shown) <= shownLen {
			l.shown = append(l.shown, c)
		}
		return c, true, nil
	}
	return 0, false, nil
}

// shownLine returns the canonical line as far as the admin text needs it
// (reading at most shownLen+1 canonical bytes more).
func (l *lineReader) shownLine() ([]byte, error) {
	for len(l.shown) <= shownLen {
		_, ok, err := l.next()
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
	}
	return l.shown, nil
}

// blankLine consumes the current line and reports whether it has only
// white space; it stops at the first other byte.
func (l *lineReader) blankLine() (bool, error) {
	for {
		c, err := l.r.ReadByte()
		if errors.Is(err, io.EOF) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		if c == '\n' {
			return true, nil
		}
		if !isWhite(c) {
			return false, nil
		}
	}
}
