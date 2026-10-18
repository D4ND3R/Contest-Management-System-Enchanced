// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

//go:build unix

package fsowner

import (
	"os"
	"path/filepath"
	"testing"
)

// nobody is the user the tests give their directories to.
const nobody = 65534

// RequireRoot skips tests that change owners.
func requireRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("needs root (chown)")
	}
}

// OwnedDir returns a new directory owned by nobody.
func ownedDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(dir, nobody, nobody); err != nil {
		t.Fatal(err)
	}
	return dir
}

func uidOf(t *testing.T, p string) int {
	t.Helper()
	uid, _, ok := ownerOf(p)
	if !ok {
		t.Fatalf("stat %s failed", p)
	}
	return uid
}

func TestOf(t *testing.T) {
	requireRoot(t)
	dir := ownedDir(t)
	o := Of(dir)
	if o == nil || o.UID != nobody || o.GID != nobody {
		t.Fatalf("Of(owned by nobody) = %+v", o)
	}
	if o := Of(t.TempDir()); o != nil {
		t.Errorf("Of(owned by root) = %+v, want nil", o)
	}
	if o := Of(filepath.Join(dir, "missing")); o != nil {
		t.Errorf("Of(missing) = %+v, want nil", o)
	}
	old := geteuid
	geteuid = func() int { return 1000 }
	defer func() { geteuid = old }()
	if o := Of(dir); o != nil {
		t.Errorf("Of() not as root = %+v, want nil", o)
	}
	var none *Owner
	if err := none.Apply(dir); err != nil {
		t.Error(err)
	}
}

func TestMkdirAllAsRoot(t *testing.T) {
	requireRoot(t)
	dir := ownedDir(t)
	deep := filepath.Join(dir, "a", "b")
	if err := MkdirAll(deep, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(dir, "a"), deep} {
		if uid := uidOf(t, p); uid != nobody {
			t.Errorf("%s: owner %d, want %d", p, uid, nobody)
		}
	}
	// Under a directory of root nothing changes.
	rootDir := filepath.Join(t.TempDir(), "x", "y")
	if err := MkdirAll(rootDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if uid := uidOf(t, rootDir); uid != 0 {
		t.Errorf("%s: owner %d, want root", rootDir, uid)
	}
}

func TestApplyTree(t *testing.T) {
	requireRoot(t)
	dir := ownedDir(t)
	sub := filepath.Join(t.TempDir(), "export")
	if err := os.MkdirAll(filepath.Join(sub, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(sub, "tests", "001.in")
	if err := os.WriteFile(f, []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Of(dir).ApplyTree(sub); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{sub, filepath.Join(sub, "tests"), f} {
		if uid := uidOf(t, p); uid != nobody {
			t.Errorf("%s: owner %d, want %d", p, uid, nobody)
		}
	}
}
