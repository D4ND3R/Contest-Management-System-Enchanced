// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Command i18n-extract writes the .pot template of a translation domain
// from the Go code and templates of the given directories. It is a
// development tool run by `make generate`; it is not part of the cms binary.
//
//	go run ./cmd/i18n-extract -domain admin -out locale/admin.pot internal/adminweb
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/i18n/extract"
)

func main() {
	domain := flag.String("domain", "", "translation domain (cms, admin)")
	out := flag.String("out", "", "output .pot file")
	flag.Parse()
	if *domain == "" || *out == "" || flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: i18n-extract -domain NAME -out FILE.pot DIR...")
		os.Exit(2)
	}
	c := extract.NewCollector()
	for _, dir := range flag.Args() {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue // a site not written yet
		}
		if err := c.Dir(dir); err != nil {
			fmt.Fprintln(os.Stderr, "i18n-extract:", err)
			os.Exit(1)
		}
	}
	var buf bytes.Buffer
	if err := extract.WritePOT(&buf, *domain, c.Messages()); err != nil {
		fmt.Fprintln(os.Stderr, "i18n-extract:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, buf.Bytes(), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "i18n-extract:", err)
		os.Exit(1)
	}
}
