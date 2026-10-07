//go:build sandbox

// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Run with `make test-sandbox` (root, isolate >= 2.0, cgroups v2).

func isolatePath() string {
	if p := os.Getenv("CMS_ISOLATE"); p != "" {
		return p
	}
	return "isolate"
}

func TestIsolateIsUsable(t *testing.T) {
	ctx := context.Background()
	major, minor, err := IsolateVersion(ctx, isolatePath())
	if err != nil {
		t.Fatal(err)
	}
	if major < 2 {
		t.Fatalf("isolate %d.%d found, 2.0 or newer is required", major, minor)
	}
	box := []string{"--cg", "--box-id=97"}
	_ = exec.Command(isolatePath(), append(box, "--cleanup")...).Run()
	out, err := exec.Command(isolatePath(), append(box, "--init")...).CombinedOutput()
	if err != nil {
		t.Fatalf("isolate --init: %v\n%s", err, out)
	}
	defer exec.Command(isolatePath(), append(box, "--cleanup")...).Run()
	root := strings.TrimSpace(string(out))
	if !filepath.IsAbs(root) {
		t.Fatalf("box path %q is not absolute", root)
	}
	out, err = exec.Command(isolatePath(), append(box, "--time=2", "--mem=65536", "--run", "--", "/bin/sh", "-c", "echo sandboxed")...).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "sandboxed") {
		t.Fatalf("isolate --run: %v\n%s", err, out)
	}
	// A relative --dir is rejected: the worker must always pass absolute paths.
	out, err = exec.Command(isolatePath(), append(box, "--dir=relative/path", "--run", "--", "/bin/true")...).CombinedOutput()
	if err == nil {
		t.Errorf("isolate accepted a relative --dir:\n%s", out)
	}
}
