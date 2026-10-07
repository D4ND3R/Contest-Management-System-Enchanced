// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package stepstest

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/sandbox"
)

// The tests that run isolate (go test -tags sandbox, make test-sandbox)
// read the same variables as those of internal/sandbox:
// CMS_SANDBOX_CGROUPS=0 runs isolate without --cg (machines without
// cgroups v2), CMS_ISOLATE names the binary and CMS_SANDBOX_TEST_BOX_BASE
// the first box id.

// Cgroups tells whether the tests run isolate with cgroups.
func Cgroups() bool {
	v := os.Getenv("CMS_SANDBOX_CGROUPS")
	if v == "" {
		return true
	}
	b, err := strconv.ParseBool(v)
	return err != nil || b
}

// Sandbox returns a real sandbox for a test, or skips it when not root
// or without isolate. With seccomp, the test binary is the launcher: its
// TestMain must call sandbox.MaybeRunLauncher first.
func Sandbox(t testing.TB, seccomp bool) *sandbox.Sandbox {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("the sandbox tests run as root (make test-sandbox)")
	}
	isolate := os.Getenv("CMS_ISOLATE")
	if isolate == "" {
		isolate = "isolate"
	}
	if _, err := exec.LookPath(isolate); err != nil {
		t.Skipf("isolate not installed: %v", err)
	}
	base := 900
	if v, err := strconv.Atoi(os.Getenv("CMS_SANDBOX_TEST_BOX_BASE")); err == nil {
		base = v
	}
	s, err := sandbox.New(sandbox.Config{
		IsolatePath:    isolate,
		Cgroups:        Cgroups(),
		BoxIDBase:      base,
		BoxCount:       64,
		WorkDir:        t.TempDir(),
		MaxFileSizeKiB: 64 << 10,
		Seccomp:        seccomp,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Box returns a new box of s, closed at the end of the test.
func Box(t testing.TB, s *sandbox.Sandbox) *sandbox.Box {
	t.Helper()
	b, err := s.NewBox(context.Background(), t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := b.Close(); err != nil {
			t.Errorf("closing the box: %v", err)
		}
	})
	return b
}
