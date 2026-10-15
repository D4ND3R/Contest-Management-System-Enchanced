// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Command i18n-extract writes the .pot template of a translation domain
// from the Go code and templates of the given directories. It is a
// development tool run by `make generate`; it is not part of the cms binary.
//
//	go run ./cmd/i18n-extract -domain admin -out locale/admin.pot internal/adminweb
//
// -skip FILE.pot (repeatable) leaves out the messages that FILE.pot already
// has: the contestant site uses the msgids of CMS, which stay in cms.pot
// (with their 25 translations), and only its new strings go to admin.pot.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/i18n"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/i18n/extract"
)

// fileList is a repeatable string flag.
type fileList []string

func (f *fileList) String() string     { return strings.Join(*f, ",") }
func (f *fileList) Set(v string) error { *f = append(*f, v); return nil }

func main() {
	domain := flag.String("domain", "", "translation domain (cms, admin)")
	out := flag.String("out", "", "output .pot file")
	var skip fileList
	flag.Var(&skip, "skip", "leave out the msgids already in this .pot file (repeatable)")
	flag.Parse()
	if *domain == "" || *out == "" || flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: i18n-extract -domain NAME -out FILE.pot [-skip OTHER.pot] DIR...")
		os.Exit(2)
	}
	var skipped []*i18n.File
	for _, p := range skip {
		f, err := readPO(p)
		if err != nil {
			fail(err)
		}
		skipped = append(skipped, f)
	}
	c := extract.NewCollector()
	for _, dir := range flag.Args() {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue // a site not written yet
		}
		if err := c.Dir(dir); err != nil {
			fail(err)
		}
	}
	var buf bytes.Buffer
	if err := extract.WritePOT(&buf, *domain, extract.Without(c.Messages(), skipped...)); err != nil {
		fail(err)
	}
	if err := os.WriteFile(*out, buf.Bytes(), 0o644); err != nil {
		fail(err)
	}
}

func readPO(path string) (*i18n.File, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	f, err := i18n.ParsePO(fh)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "i18n-extract:", err)
	os.Exit(1)
}
