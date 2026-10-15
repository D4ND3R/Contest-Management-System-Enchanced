// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package locale

import (
	"encoding/json"
	"os"
	"strconv"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/i18n"
)

// TestCMSNgettext compares the plural messages of the "cms" catalogs
// with what CMS shows: testdata/cms_ngettext.json was recorded with
// tools/i18n/cms_ngettext.py from the compiled catalogs of an installed
// CMS (Babel 2.12.1, Python's gettext), for every plural msgid of cms.pot
// and counts with fractions and booleans as format_token_rules passes
// them.
func TestCMSNgettext(t *testing.T) {
	data, err := os.ReadFile("testdata/cms_ngettext.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden struct {
		Counts    []string                       `json:"counts"`
		Languages map[string]map[string][]string `json:"languages"`
	}
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatal(err)
	}
	counts := make([]float64, len(golden.Counts))
	for i, c := range golden.Counts {
		switch c {
		case "False":
			counts[i] = 0
		case "True":
			counts[i] = 1
		default:
			if counts[i], err = strconv.ParseFloat(c, 64); err != nil {
				t.Fatal(err)
			}
		}
	}
	pot := parse(t, "cms.pot")
	plurals := map[string]string{}
	for _, e := range pot.Entries {
		if e.Plural != "" {
			plurals[e.ID] = e.Plural
		}
	}
	b, err := i18n.LoadBundle(FS, DomainContest)
	if err != nil {
		t.Fatal(err)
	}
	if len(golden.Languages) != 25 {
		t.Errorf("%d languages in the golden file", len(golden.Languages))
	}
	checked := 0
	for lang, table := range golden.Languages {
		cat := b.Catalog(lang)
		if cat == nil || cat.Lang == "en" {
			t.Errorf("%s: no catalog", lang)
			continue
		}
		for msgid, want := range table {
			plural, ok := plurals[msgid]
			if !ok {
				t.Errorf("%s: %q is not a plural message of cms.pot", lang, msgid)
				continue
			}
			for i, n := range counts {
				checked++
				if got := cat.TnFloat(msgid, plural, n); got != want[i] {
					t.Errorf("%s: ngettext(%q, %s) = %q, CMS %q", lang, msgid, golden.Counts[i], got, want[i])
				}
			}
		}
	}
	if checked < 25*10*len(counts) {
		t.Errorf("only %d translations checked", checked)
	}
}
