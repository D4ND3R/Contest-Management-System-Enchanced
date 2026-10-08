//go:build sandbox

// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestBoxKeep: a kept box stays on disk, also when its id is used again.
func TestBoxKeep(t *testing.T) {
	s := newTestSandbox(t, func(c *Config) { c.BoxCount = 1 })
	ctx := context.Background()
	b, err := s.NewBox(ctx, "kept")
	if err != nil {
		t.Fatal(err)
	}
	if err := b.CreateFile("data.txt", []byte("x"), false); err != nil {
		t.Fatal(err)
	}
	b.Keep()
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	other, err := s.NewBox(ctx, "other") // the same id
	if err != nil {
		t.Fatal(err)
	}
	if other.ID() != b.ID() {
		t.Fatalf("ids %d and %d", b.ID(), other.ID())
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(b.Home(), "data.txt")); err != nil {
		t.Errorf("the kept box is gone: %v", err)
	}
	if _, err := os.Stat(other.Dir()); !os.IsNotExist(err) {
		t.Errorf("the other box was not deleted: %v", err)
	}
}
