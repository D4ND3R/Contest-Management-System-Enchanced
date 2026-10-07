// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Package i18n translates every visible text. Catalogs are gettext .po
// files, the format CMS uses, so its 25 translations are reused as they
// are: msgids (including Python-style "%(name)s" placeholders) are kept
// identical to the original. Adding a language is adding one file.
package i18n

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Entry is one message of a .po or .pot file.
type Entry struct {
	Context string
	ID      string
	Plural  string
	Str     []string // msgstr, or msgstr[0..n-1] for plural entries
	Fuzzy   bool
	Refs    []string // "#:" source references
}

// File is a parsed .po/.pot file.
type File struct {
	Header  map[string]string
	Entries []Entry // the header entry (empty msgid) excluded
}

// ParsePO reads a .po or .pot file. Obsolete entries (#~) are dropped.
func ParsePO(r io.Reader) (*File, error) {
	f := &File{Header: map[string]string{}}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 4<<20)

	var (
		cur     Entry
		started bool   // cur has a msgid
		field   string // field receiving continuation lines
		index   int
		lineNo  int
		pending []string // flags/refs read before the msgid of the next entry
		fuzzy   bool
	)
	flush := func() error {
		if !started {
			return nil
		}
		if cur.ID == "" && cur.Context == "" {
			if len(cur.Str) > 0 {
				for _, l := range strings.Split(cur.Str[0], "\n") {
					if k, v, ok := strings.Cut(l, ":"); ok {
						f.Header[strings.TrimSpace(k)] = strings.TrimSpace(v)
					}
				}
			}
		} else {
			f.Entries = append(f.Entries, cur)
		}
		cur, started, field, pending, fuzzy = Entry{}, false, "", nil, false
		return nil
	}
	appendTo := func(s string) error {
		switch field {
		case "msgctxt":
			cur.Context += s
		case "msgid":
			cur.ID += s
		case "msgid_plural":
			cur.Plural += s
		case "msgstr":
			for len(cur.Str) <= index {
				cur.Str = append(cur.Str, "")
			}
			cur.Str[index] += s
		default:
			return fmt.Errorf("line %d: string outside of a field", lineNo)
		}
		return nil
	}

	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "":
			continue
		case strings.HasPrefix(line, "#~"):
			continue
		case strings.HasPrefix(line, "#,"):
			if started && field == "msgstr" {
				if err := flush(); err != nil {
					return nil, err
				}
			}
			for _, fl := range strings.Split(line[2:], ",") {
				if strings.TrimSpace(fl) == "fuzzy" {
					fuzzy = true
				}
			}
			continue
		case strings.HasPrefix(line, "#:"):
			if started && field == "msgstr" {
				if err := flush(); err != nil {
					return nil, err
				}
			}
			pending = append(pending, strings.Fields(line[2:])...)
			continue
		case strings.HasPrefix(line, "#"):
			if started && field == "msgstr" {
				if err := flush(); err != nil {
					return nil, err
				}
			}
			continue
		case strings.HasPrefix(line, `"`):
			s, err := unquote(line)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", lineNo, err)
			}
			if err := appendTo(s); err != nil {
				return nil, err
			}
			continue
		}

		kw, rest, _ := strings.Cut(line, " ")
		rest = strings.TrimSpace(rest)
		s, err := unquote(rest)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo, err)
		}
		switch {
		case kw == "msgctxt" || kw == "msgid":
			// A new entry starts after the msgstr of the previous one;
			// "msgctxt" followed by "msgid" belong to the same entry.
			if started && field == "msgstr" {
				if err := flush(); err != nil {
					return nil, err
				}
			}
			if !started {
				cur = Entry{Refs: pending, Fuzzy: fuzzy}
				started = true
			}
			field = kw
		case kw == "msgid_plural":
			field = kw
		case kw == "msgstr":
			field, index = "msgstr", 0
		case strings.HasPrefix(kw, "msgstr[") && strings.HasSuffix(kw, "]"):
			n, err := strconv.Atoi(kw[len("msgstr[") : len(kw)-1])
			if err != nil || n < 0 || n > 16 {
				return nil, fmt.Errorf("line %d: bad plural index %q", lineNo, kw)
			}
			field, index = "msgstr", n
		default:
			return nil, fmt.Errorf("line %d: unknown keyword %q", lineNo, kw)
		}
		if err := appendTo(s); err != nil {
			return nil, err
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return f, nil
}

func unquote(s string) (string, error) {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return "", fmt.Errorf("expected a quoted string, got %q", s)
	}
	s = s[1 : len(s)-1]
	if !strings.Contains(s, `\`) {
		return s, nil
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 == len(s) {
			b.WriteByte(c)
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case '"', '\\':
			b.WriteByte(s[i])
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String(), nil
}

// Quote escapes s as a .po string literal.
func Quote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`, "\r", `\r`)
	return `"` + r.Replace(s) + `"`
}
