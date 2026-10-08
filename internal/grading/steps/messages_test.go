// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package steps

import (
	"bytes"
	"os"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/i18n"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/i18n/extract"
)

// TestMessagesTranslatable: every outcome text is a msgid of the catalogs
// of CMS (locale/cms.pot) or is marked with N, so that `make generate`
// can put it in a catalog of this project; no text is always shown in
// English.
func TestMessagesTranslatable(t *testing.T) {
	data, err := os.ReadFile("../../../locale/cms.pot")
	if err != nil {
		t.Fatal(err)
	}
	pot, err := i18n.ParsePO(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	cms := map[string]bool{}
	for _, e := range pot.Entries {
		cms[e.ID] = true
	}
	dir, err := os.Getwd() // Dir skips ".", a hidden directory
	if err != nil {
		t.Fatal(err)
	}
	c := extract.NewCollector()
	if err := c.Dir(dir); err != nil {
		t.Fatal(err)
	}
	marked := map[string]bool{}
	for _, m := range c.Messages() {
		marked[m.ID] = true
		if cms[m.ID] {
			t.Errorf("%q is in the catalogs of CMS: it needs no N", m.ID)
		}
	}
	ids := []string{ExecutionCompleted}
	for _, col := range []*MessageCollection{CompilationMessages, EvaluationMessages} {
		for _, m := range col.All() {
			ids = append(ids, m.Message, m.HelpText)
		}
	}
	for _, id := range ids {
		if !cms[id] && !marked[id] {
			t.Errorf("%q is in no catalog: mark it with N", id)
		}
	}
	if len(marked) != 6 {
		t.Errorf("%d texts marked with N, want 6", len(marked))
	}
}
