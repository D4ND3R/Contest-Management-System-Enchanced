// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package checker

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
)

// Exact compares two files byte by byte. When they differ, adminText is
// "Contestant output too long" or "too short" (one is a prefix of the
// other) or "Expected `x`, found `y` on line N" with the raw lines, like
// WhiteDiff. The files are streamed.
func Exact(output, correct io.Reader) (equal bool, adminText string, err error) {
	ro := bufio.NewReaderSize(output, bufSize)
	rc := bufio.NewReaderSize(correct, bufSize)
	line := 1
	var cur []byte // the current line so far (at most shownLen+1 bytes)
	for {
		po, err := peekAll(ro)
		if err != nil {
			return false, "", err
		}
		pc, err := peekAll(rc)
		if err != nil {
			return false, "", err
		}
		n := min(len(po), len(pc))
		if n == 0 {
			switch {
			case len(po) == 0 && len(pc) == 0:
				return true, "", nil
			case len(po) > 0:
				return false, tooLong, nil
			default:
				return false, tooShort, nil
			}
		}
		i := 0
		if bytes.Equal(po[:n], pc[:n]) {
			i = n
		} else {
			for po[i] == pc[i] {
				i++
			}
		}
		same := po[:i]
		if k := bytes.LastIndexByte(same, '\n'); k >= 0 {
			line += bytes.Count(same, []byte{'\n'})
			cur = cur[:0]
			same = same[k+1:]
		}
		cur = appendCapped(cur, same)
		if _, err := ro.Discard(i); err != nil {
			return false, "", err
		}
		if _, err := rc.Discard(i); err != nil {
			return false, "", err
		}
		if i < n {
			// The lines differ: show them from their start.
			lo, err := readLineCapped(ro, bytes.Clone(cur))
			if err != nil {
				return false, "", err
			}
			lc, err := readLineCapped(rc, bytes.Clone(cur))
			if err != nil {
				return false, "", err
			}
			return false, mismatchText(lc, lo, line), nil
		}
	}
}

// readLineCapped appends to dst the rest of the current line of r (without
// the \n), stopping when dst has shownLen+1 bytes.
func readLineCapped(r *bufio.Reader, dst []byte) ([]byte, error) {
	for len(dst) <= shownLen {
		c, err := r.ReadByte()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if c == '\n' {
			break
		}
		dst = append(dst, c)
	}
	return dst, nil
}

// peekAll returns the buffered bytes of r, filling the buffer first; empty
// at the end of the file.
func peekAll(r *bufio.Reader) ([]byte, error) {
	b, err := r.Peek(r.Size())
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return b, nil
}

// appendCapped appends b to dst without growing it beyond shownLen+1
// bytes (enough to know that a line must be cut).
func appendCapped(dst, b []byte) []byte {
	if room := shownLen + 1 - len(dst); room > 0 {
		dst = append(dst, b[:min(room, len(b))]...)
	}
	return dst
}

// maxToken bounds the part of a token RealNumbers parses as a number;
// longer tokens are compared byte by byte.
const maxToken = 4096

// number is what RealNumbers accepts as a number: decimal notation with an
// optional exponent (no "inf", "nan" or hexadecimal floats).
var number = regexp.MustCompile(`^[+-]?([0-9]+\.?[0-9]*|\.[0-9]+)([eE][+-]?[0-9]+)?$`)

// RealNumbers compares two files token by token (tokens are separated by
// white space; lines do not matter). Where the correct output has a
// number, the contestant's token must be a number within the tolerance:
// |found - expected| <= absTol, or <= relTol × |expected|. Other tokens
// must be identical. adminText is "Contestant output too long", "too
// short" or "Expected `x`, found `y` on line N" (N is the line of the
// contestant's token).
func RealNumbers(output, correct io.Reader, absTol, relTol float64) (equal bool, adminText string, err error) {
	if err := checkTolerance(absTol, relTol); err != nil {
		return false, "", err
	}
	out := &tokenReader{r: bufio.NewReaderSize(output, bufSize), line: 1}
	res := &tokenReader{r: bufio.NewReaderSize(correct, bufSize), line: 1}
	for {
		okO, err := out.skipSpace()
		if err != nil {
			return false, "", err
		}
		okR, err := res.skipSpace()
		if err != nil {
			return false, "", err
		}
		switch {
		case !okO && !okR:
			return true, "", nil
		case !okR:
			return false, tooLong, nil
		case !okO:
			return false, tooShort, nil
		}
		line := out.line
		a, err := out.read(maxToken)
		if err != nil {
			return false, "", err
		}
		b, err := res.read(maxToken)
		if err != nil {
			return false, "", err
		}
		same := bytes.Equal(a, b)
		if same && (out.inToken || res.inToken) {
			// Long tokens: the rest must be identical too.
			if same, err = sameRest(out, res); err != nil {
				return false, "", err
			}
		}
		if !same && !out.inToken && !res.inToken && number.Match(b) && number.Match(a) {
			same = closeEnough(a, b, absTol, relTol)
		}
		if !same {
			return false, mismatchText(b, a, line), nil
		}
	}
}

func checkTolerance(absTol, relTol float64) error {
	for _, t := range []float64{absTol, relTol} {
		if t < 0 || math.IsNaN(t) || math.IsInf(t, 0) {
			return fmt.Errorf("checker: invalid tolerance %g (finite, not negative)", t)
		}
	}
	return nil
}

func closeEnough(found, expected []byte, absTol, relTol float64) bool {
	f, err1 := strconv.ParseFloat(string(found), 64)
	e, err2 := strconv.ParseFloat(string(expected), 64)
	if err1 != nil || err2 != nil { // out of range
		return false
	}
	d := math.Abs(f - e)
	return d <= absTol || d <= relTol*math.Abs(e)
}

// sameRest compares the rest of the current tokens of a and b.
func sameRest(a, b *tokenReader) (bool, error) {
	for {
		x, okX, err := a.nextByte()
		if err != nil {
			return false, err
		}
		y, okY, err := b.nextByte()
		if err != nil {
			return false, err
		}
		if okX != okY || x != y {
			return false, nil
		}
		if !okX {
			return true, nil
		}
	}
}

// tokenReader splits a file in tokens separated by white space.
type tokenReader struct {
	r       *bufio.Reader
	line    int  // line of the next byte
	inToken bool // the current token has more bytes
	buf     []byte
}

// skipSpace moves to the start of the next token; false at the end of
// the file.
func (t *tokenReader) skipSpace() (bool, error) {
	for t.inToken { // finish the previous token
		if _, ok, err := t.nextByte(); err != nil {
			return false, err
		} else if !ok {
			break
		}
	}
	for {
		c, err := t.r.ReadByte()
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if c == '\n' {
			t.line++
		}
		if !isWhite(c) {
			t.inToken = true
			return true, t.r.UnreadByte()
		}
	}
}

// read returns up to max bytes of the current token (valid until the
// next call); inToken stays set when the token is longer.
func (t *tokenReader) read(max int) ([]byte, error) {
	tok := t.buf[:0]
	defer func() { t.buf = tok[:0] }()
	for len(tok) < max {
		c, ok, err := t.nextByte()
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		tok = append(tok, c)
	}
	return tok, nil
}

// nextByte returns the next byte of the current token, ok false at its
// end.
func (t *tokenReader) nextByte() (byte, bool, error) {
	if !t.inToken {
		return 0, false, nil
	}
	c, err := t.r.ReadByte()
	if errors.Is(err, io.EOF) {
		t.inToken = false
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if isWhite(c) {
		t.inToken = false
		return 0, false, t.r.UnreadByte()
	}
	return c, true, nil
}
