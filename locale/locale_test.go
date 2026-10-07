// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package locale

import (
	"bytes"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/i18n"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/i18n/extract"
)

func parse(t *testing.T, path string) *i18n.File {
	t.Helper()
	data, err := fs.ReadFile(FS, path)
	if err != nil {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		t.Fatal(err)
	}
	f, err := i18n.ParsePO(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return f
}

// TestCatalogsLoad checks every embedded catalog, including the 25 ported
// from CMS: it parses, its Plural-Forms compiles and plural entries have
// as many forms as the language declares.
func TestCatalogsLoad(t *testing.T) {
	paths, err := fs.Glob(FS, "*/LC_MESSAGES/*.po")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) < 26 {
		t.Fatalf("only %d catalogs embedded", len(paths))
	}
	for _, p := range paths {
		f := parse(t, p)
		n := 2
		if pf := f.Header["Plural-Forms"]; pf != "" {
			var err error
			if n, _, err = i18n.ParsePluralForms(pf); err != nil {
				t.Errorf("%s: %v", p, err)
				continue
			}
		}
		for _, e := range f.Entries {
			if e.Plural != "" && !e.Fuzzy && len(e.Str) > 0 && e.Str[0] != "" && len(e.Str) != n {
				t.Errorf("%s: %q has %d plural forms, the language has %d", p, e.ID, len(e.Str), n)
			}
		}
	}
	for _, d := range []string{DomainContest, DomainAdmin} {
		if _, err := i18n.LoadBundle(FS, d); err != nil {
			t.Errorf("bundle %s: %v", d, err)
		}
	}
	b, _ := i18n.LoadBundle(FS, DomainContest)
	if got := b.Catalog("es").T("Correct"); got != "Correcto" {
		t.Errorf("es Correct = %q", got)
	}
	if got := len(b.Languages()); got != 26 {
		t.Errorf("contestant languages: %d, want 26 (English + the 25 of CMS)", got)
	}
}

// TestAdminSpanishIsComplete fails when a text of the administration has
// no Spanish translation. Run `make generate` and translate the new
// entries in es/LC_MESSAGES/admin.po.
func TestAdminSpanishIsComplete(t *testing.T) {
	pot := parse(t, "admin.pot")
	es := parse(t, "es/LC_MESSAGES/admin.po")
	tr := map[string]i18n.Entry{}
	for _, e := range es.Entries {
		tr[e.Context+"\x04"+e.ID] = e
	}
	for _, m := range pot.Entries {
		e, ok := tr[m.Context+"\x04"+m.ID]
		missing := !ok || e.Fuzzy || len(e.Str) == 0
		for _, s := range e.Str {
			missing = missing || s == ""
		}
		if missing {
			t.Errorf("admin text without Spanish translation: %s (%s)", strconv.Quote(m.ID), strings.Join(m.Refs, " "))
		}
	}
}

// TestPOTIsUpToDate fails when the templates or code changed and
// `make generate` was not run.
func TestPOTIsUpToDate(t *testing.T) {
	for domain, dirs := range map[string][]string{
		DomainAdmin: {"../internal/adminweb"},
	} {
		c := extract.NewCollector()
		for _, d := range dirs {
			if _, err := os.Stat(d); err != nil {
				continue
			}
			if err := c.Dir(d); err != nil {
				t.Fatal(err)
			}
		}
		var buf bytes.Buffer
		if err := extract.WritePOT(&buf, domain, c.Messages()); err != nil {
			t.Fatal(err)
		}
		have, err := os.ReadFile(domain + ".pot")
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(have, buf.Bytes()) {
			t.Errorf("locale/%s.pot is stale: run `make generate`", domain)
		}
	}
}
