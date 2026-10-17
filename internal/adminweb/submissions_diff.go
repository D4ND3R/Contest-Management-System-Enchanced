// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from SubmissionDiffHandler in cms/server/admin/handlers/submission.py
// of CMS (Copyright © 2010-2017 Stefano Maggiolo <s.maggiolo@gmail.com> and
// the CMS contributors) and, for the diff itself, from the SequenceMatcher
// and unified_diff of Python's difflib (Copyright © 2001 Python Software
// Foundation; PSF License, compatible with the AGPL): the same algorithm,
// so the admin sees the hunks CMS shows.

package adminweb

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// Limits of the diff (CMS): larger files are not compared.
const (
	diffMaxBytes = 1000000
	diffMaxLines = 5000
)

// diffFile is one file of the answer of submission_diff.
type diffFile struct {
	Fname  string `json:"fname"`
	Status string `json:"status,omitempty"`
	Diff   string `json:"diff,omitempty"`
}

// diffAnswer is the JSON of submission_diff (aws_utils.js show_diff).
type diffAnswer struct {
	Message *string    `json:"message"`
	Files   []diffFile `json:"files"`
}

// submissionDiffPage is GET /submission_diff/<old>/<new> (CMS
// SubmissionDiffHandler): a unified diff of the files of two
// submissions, as JSON.
func (s *Server) submissionDiffPage(c *Ctx) {
	oldID, newID := mustID(c, "old_id"), mustID(c, "new_id")
	rows, err := s.st.Q.AdminDiffSubmissions(c.Context(), []int32{oldID, newID})
	if err != nil {
		c.Fail(err)
		return
	}
	var subOld, subNew *q.AdminDiffSubmissionsRow
	for i := range rows {
		if rows[i].ID == oldID {
			subOld = &rows[i]
		}
		if rows[i].ID == newID {
			subNew = &rows[i]
		}
	}
	resp := diffAnswer{Files: []diffFile{}}
	send := func() {
		c.W.Header().Set("Content-Type", "application/json; charset=utf-8")
		c.JSON(http.StatusOK, resp)
	}
	if subOld == nil || subNew == nil {
		missing := c.R.PathValue("old_id")
		if subOld != nil {
			missing = c.R.PathValue("new_id")
		}
		msg := fmt.Sprintf("Submission ID %s not found.", missing)
		resp.Message = &msg
		send()
		return
	}
	const single = "submission.%l"
	oldFiles, newFiles := filesOf(subOld), filesOf(subNew)
	var compare []string
	switch {
	case subOld.TaskID == subNew.TaskID:
		compare = subOld.SubmissionFormat
	case len(oldFiles) == 1 && len(newFiles) == 1:
		oldFiles = map[string]string{single: subOld.Digests[0]}
		newFiles = map[string]string{single: subNew.Digests[0]}
		compare = []string{single}
	default:
		msg := "Cannot compare submissions: they are for different tasks and have more than 1 file."
		resp.Message = &msg
		send()
		return
	}
	for _, fname := range compare {
		real := fname
		if strings.Contains(fname, ".%l") {
			if subOld.Language != nil && subNew.Language != nil && *subOld.Language == *subNew.Language {
				real = s.langs.LangFilename(*subOld.Language, fname)
			} else {
				real = strings.ReplaceAll(fname, ".%l", ".txt")
			}
		}
		oldLines, status, err := s.diffLines(c, oldFiles, fname, "old")
		if err != nil {
			c.Fail(err)
			return
		}
		if status != "" {
			resp.Files = append(resp.Files, diffFile{Fname: real, Status: status})
			continue
		}
		newLines, status, err := s.diffLines(c, newFiles, fname, "new")
		if err != nil {
			c.Fail(err)
			return
		}
		if status != "" {
			resp.Files = append(resp.Files, diffFile{Fname: real, Status: status})
			continue
		}
		if slices.Equal(oldLines, newLines) {
			resp.Files = append(resp.Files, diffFile{Fname: real, Status: "No changes"})
			continue
		}
		resp.Files = append(resp.Files, diffFile{Fname: real, Diff: strings.Join(unifiedDiff(oldLines, newLines), "\n")})
	}
	send()
}

// filesOf maps the file names of a submission to their digests.
func filesOf(r *q.AdminDiffSubmissionsRow) map[string]string {
	out := make(map[string]string, len(r.Filenames))
	for i, n := range r.Filenames {
		if i < len(r.Digests) {
			out[n] = r.Digests[i]
		}
	}
	return out
}

// diffLines reads a file of a submission as lines (CMS get_file), or the
// status that replaces its diff.
func (s *Server) diffLines(c *Ctx, files map[string]string, fname, which string) ([]string, string, error) {
	digest, ok := files[fname]
	if !ok {
		return nil, "File not present in " + which + " submission", nil
	}
	rc, err := s.blobs.Open(c.Context(), digest)
	if errors.Is(err, blob.ErrNotFound) {
		return nil, capitalize(which) + " file is not in the file store", nil
	}
	if err != nil {
		return nil, "", err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, diffMaxBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > diffMaxBytes {
		return nil, capitalize(which) + " file is too big to diff", nil
	}
	lines := pySplitLines(decodeReplace(data))
	if len(lines) > diffMaxLines {
		return nil, capitalize(which) + " file has too many lines to diff", nil
	}
	return lines, "", nil
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// decodeReplace is Python's bytes.decode(errors="replace"): every
// invalid byte becomes U+FFFD.
func decodeReplace(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var sb strings.Builder
	for len(b) > 0 {
		r, n := utf8.DecodeRune(b)
		if r == utf8.RuneError && n <= 1 {
			sb.WriteRune(utf8.RuneError)
			b = b[1:]
			continue
		}
		sb.WriteRune(r)
		b = b[n:]
	}
	return sb.String()
}

// pySplitLines is Python's str.splitlines(): the line boundaries are
// \n, \r, \r\n, \v, \f, \x1c, \x1d, \x1e, \x85,   and  , and
// they are not kept.
func pySplitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch r {
		case '\n', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
			out = append(out, s[start:i])
			i += n
			start = i
			continue
		case '\r':
			out = append(out, s[start:i])
			i += n
			if i < len(s) && s[i] == '\n' {
				i++
			}
			start = i
			continue
		}
		i += n
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// opcode is a difflib opcode (tag, i1, i2, j1, j2).
type opcode struct {
	tag            byte // 'r'eplace, 'd'elete, 'i'nsert, 'e'qual
	i1, i2, j1, j2 int
}

// match is a matching block of difflib (i, j, size).
type match struct{ i, j, k int }

// seqMatcher is difflib.SequenceMatcher(None, a, b) on lines (autojunk on,
// no junk function).
type seqMatcher struct {
	a, b []string
	b2j  map[string][]int
}

func newSeqMatcher(a, b []string) *seqMatcher {
	m := &seqMatcher{a: a, b: b, b2j: map[string][]int{}}
	for i, elt := range b {
		m.b2j[elt] = append(m.b2j[elt], i)
	}
	// Popular elements (more than 1% of b, for b of 200 lines or more)
	// are removed from b2j (autojunk); unlike junk they may still extend
	// a match.
	if n := len(b); n >= 200 {
		ntest := n/100 + 1
		for elt, idxs := range m.b2j {
			if len(idxs) > ntest {
				delete(m.b2j, elt)
			}
		}
	}
	return m
}

// longestMatch is find_longest_match(alo, ahi, blo, bhi).
func (m *seqMatcher) longestMatch(alo, ahi, blo, bhi int) match {
	besti, bestj, bestsize := alo, blo, 0
	j2len := map[int]int{}
	for i := alo; i < ahi; i++ {
		newj2len := map[int]int{}
		for _, j := range m.b2j[m.a[i]] {
			if j < blo {
				continue
			}
			if j >= bhi {
				break
			}
			k := j2len[j-1] + 1
			newj2len[j] = k
			if k > bestsize {
				besti, bestj, bestsize = i-k+1, j-k+1, k
			}
		}
		j2len = newj2len
	}
	for besti > alo && bestj > blo && m.a[besti-1] == m.b[bestj-1] {
		besti, bestj, bestsize = besti-1, bestj-1, bestsize+1
	}
	for besti+bestsize < ahi && bestj+bestsize < bhi && m.a[besti+bestsize] == m.b[bestj+bestsize] {
		bestsize++
	}
	return match{besti, bestj, bestsize}
}

// matchingBlocks is get_matching_blocks().
func (m *seqMatcher) matchingBlocks() []match {
	la, lb := len(m.a), len(m.b)
	type span struct{ alo, ahi, blo, bhi int }
	queue := []span{{0, la, 0, lb}}
	var blocks []match
	for len(queue) > 0 {
		sp := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		x := m.longestMatch(sp.alo, sp.ahi, sp.blo, sp.bhi)
		if x.k > 0 {
			blocks = append(blocks, x)
			if sp.alo < x.i && sp.blo < x.j {
				queue = append(queue, span{sp.alo, x.i, sp.blo, x.j})
			}
			if x.i+x.k < sp.ahi && x.j+x.k < sp.bhi {
				queue = append(queue, span{x.i + x.k, sp.ahi, x.j + x.k, sp.bhi})
			}
		}
	}
	slices.SortFunc(blocks, func(x, y match) int {
		if x.i != y.i {
			return x.i - y.i
		}
		if x.j != y.j {
			return x.j - y.j
		}
		return x.k - y.k
	})
	var out []match
	i1, j1, k1 := 0, 0, 0
	for _, b := range blocks {
		if i1+k1 == b.i && j1+k1 == b.j {
			k1 += b.k
			continue
		}
		if k1 > 0 {
			out = append(out, match{i1, j1, k1})
		}
		i1, j1, k1 = b.i, b.j, b.k
	}
	if k1 > 0 {
		out = append(out, match{i1, j1, k1})
	}
	return append(out, match{la, lb, 0})
}

// opcodes is get_opcodes().
func (m *seqMatcher) opcodes() []opcode {
	var out []opcode
	i, j := 0, 0
	for _, b := range m.matchingBlocks() {
		var tag byte
		switch {
		case i < b.i && j < b.j:
			tag = 'r'
		case i < b.i:
			tag = 'd'
		case j < b.j:
			tag = 'i'
		}
		if tag != 0 {
			out = append(out, opcode{tag, i, b.i, j, b.j})
		}
		i, j = b.i+b.k, b.j+b.k
		if b.k > 0 {
			out = append(out, opcode{'e', b.i, i, b.j, j})
		}
	}
	return out
}

// groupedOpcodes is get_grouped_opcodes(n).
func (m *seqMatcher) groupedOpcodes(n int) [][]opcode {
	codes := m.opcodes()
	if len(codes) == 0 {
		codes = []opcode{{'e', 0, 1, 0, 1}}
	}
	if c := &codes[0]; c.tag == 'e' {
		c.i1, c.j1 = max(c.i1, c.i2-n), max(c.j1, c.j2-n)
	}
	if c := &codes[len(codes)-1]; c.tag == 'e' {
		c.i2, c.j2 = min(c.i2, c.i1+n), min(c.j2, c.j1+n)
	}
	var groups [][]opcode
	var group []opcode
	for _, c := range codes {
		if c.tag == 'e' && c.i2-c.i1 > 2*n {
			group = append(group, opcode{'e', c.i1, min(c.i2, c.i1+n), c.j1, min(c.j2, c.j1+n)})
			groups = append(groups, group)
			group = nil
			c.i1, c.j1 = max(c.i1, c.i2-n), max(c.j1, c.j2-n)
		}
		group = append(group, c)
	}
	if len(group) > 0 && !(len(group) == 1 && group[0].tag == 'e') {
		groups = append(groups, group)
	}
	return groups
}

// formatRangeUnified is difflib's _format_range_unified.
func formatRangeUnified(start, stop int) string {
	beginning, length := start+1, stop-start
	if length == 1 {
		return strconv.Itoa(beginning)
	}
	if length == 0 {
		beginning--
	}
	return strconv.Itoa(beginning) + "," + strconv.Itoa(length)
}

// unifiedDiff is difflib.unified_diff(a, b, lineterm="") without its
// first two lines ("---" and "+++"), as CMS sends it.
func unifiedDiff(a, b []string) []string {
	var out []string
	for _, group := range newSeqMatcher(a, b).groupedOpcodes(3) {
		first, last := group[0], group[len(group)-1]
		out = append(out, "@@ -"+formatRangeUnified(first.i1, last.i2)+" +"+formatRangeUnified(first.j1, last.j2)+" @@")
		for _, c := range group {
			if c.tag == 'e' {
				for _, line := range a[c.i1:c.i2] {
					out = append(out, " "+line)
				}
				continue
			}
			if c.tag == 'r' || c.tag == 'd' {
				for _, line := range a[c.i1:c.i2] {
					out = append(out, "-"+line)
				}
			}
			if c.tag == 'r' || c.tag == 'i' {
				for _, line := range b[c.j1:c.j2] {
					out = append(out, "+"+line)
				}
			}
		}
	}
	return out
}
