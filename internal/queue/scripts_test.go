// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package queue

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
)

// Open leaves every script loaded under its real hash, so operations cost
// one EVALSHA (a hash recorded from an unexecuted pipeline was "", which
// made every call a failed EVALSHA plus a full EVAL).
func TestScriptsLoadedWithTheirHash(t *testing.T) {
	c := testutil.Redis(t)
	ctx := context.Background()
	if _, err := Open(ctx, c, "jobs", Options{}); err != nil {
		t.Fatal(err)
	}
	hashes := make([]string, len(allScripts))
	for i, s := range allScripts {
		hashes[i] = s.Hash()
		if len(hashes[i]) != 2*sha1.Size {
			t.Fatalf("script %d: hash %q", i, hashes[i])
		}
		if _, err := hex.DecodeString(hashes[i]); err != nil {
			t.Fatalf("script %d: hash %q", i, hashes[i])
		}
	}
	exists, err := c.Redis().ScriptExists(ctx, hashes...).Result()
	if err != nil {
		t.Fatal(err)
	}
	for i, ok := range exists {
		if !ok {
			t.Errorf("script %d is not loaded under %s", i, hashes[i])
		}
	}
}
