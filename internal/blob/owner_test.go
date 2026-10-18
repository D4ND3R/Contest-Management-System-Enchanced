// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

//go:build unix

package blob

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestLocalAsRootKeepsTheOwner: blobs stored by root (sudo cmsctl
// import-contest) into a store of another user belong to that user, so
// the services can read them.
func TestLocalAsRootKeepsTheOwner(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root (chown)")
	}
	const nobody = 65534
	data := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(data, nobody, nobody); err != nil {
		t.Fatal(err)
	}
	s, err := NewLocal(filepath.Join(data, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	d, err := s.PutBytes(ctx, []byte("secret test data"))
	if err != nil {
		t.Fatal(err)
	}
	d2, _, err := s.Put(ctx, strings.NewReader("another file"))
	if err != nil {
		t.Fatal(err)
	}
	root := s.Root()
	paths := []string{root, filepath.Join(root, tmpName)}
	for _, x := range []string{d, d2} {
		paths = append(paths, filepath.Join(root, x[:2]), filepath.Join(root, x[:2], x[2:4]), s.path(x))
	}
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if uid := st.Sys().(*syscall.Stat_t).Uid; uid != nobody {
			t.Errorf("%s: owner %d, want %d", p, uid, nobody)
		}
	}
}
