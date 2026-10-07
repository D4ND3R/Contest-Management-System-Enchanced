//go:build sandbox

// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package checker

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/steps"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/steps/stepstest"
)

// Run with `make test-sandbox` (root, isolate); CMS_SANDBOX_CGROUPS=0
// without cgroups v2.

// testlibChecker is a Polygon-style checker: correct, half the points
// when off by one, wrong otherwise.
const testlibChecker = `#include "testlib.h"
int main(int argc, char* argv[]) {
    registerTestlibCmd(argc, argv);
    long long want = ans.readLong();
    long long got = ouf.readLong();
    if (got == want) quitf(_ok, "correct %lld", got);
    if (got == want + 1) quitp(0.5, "off by one");
    quitf(_wa, "expected %lld, found %lld", want, got);
}
`

// compileTestlib compiles the checker with TestlibHeader on the host, the
// way upstream's Polygon loader does (with -DCMS for the CMS protocol).
func compileTestlib(t *testing.T, cms bool) []byte {
	t.Helper()
	if _, err := exec.LookPath("g++"); err != nil {
		t.Skip("g++ not installed")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "testlib.h"), TestlibHeader, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "check.cpp"), []byte(testlibChecker), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"-x", "c++", "-O2", "-static", "-o", filepath.Join(dir, "check"), filepath.Join(dir, "check.cpp")}
	if cms {
		args = append(args, "-DCMS")
	}
	if out, err := exec.Command("g++", args...).CombinedOutput(); err != nil {
		t.Fatalf("g++: %v\n%s", err, out)
	}
	data, err := os.ReadFile(filepath.Join(dir, "check"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func trustedConfig() *steps.Config {
	c := steps.ConfigFrom(config.Default())
	c.Cgroups = stepstest.Cgroups()
	return &c
}

func TestCheckerPrograms(t *testing.T) {
	s := stepstest.Sandbox(t, false)
	cfg := trustedConfig()
	// A comparator written as a shell script: CMS protocol, extra
	// argument.
	script := []byte("#!/bin/sh\n" +
		"if [ \"$4\" != batch ]; then echo 'no extra argument' >&2; exit 2; fi\n" +
		"if [ \"$(cat \"$2\")\" = \"$(cat \"$3\")\" ]; then echo 1; echo translate:success >&2; " +
		"else echo 0; echo 'Not 100% right' >&2; echo 'ADMIN_MESSAGE: differs' >&2; fi\n")
	cmsChecker := compileTestlib(t, true)
	plainChecker := compileTestlib(t, false)

	for _, tc := range []struct {
		name    string
		kind    Kind
		program []byte
		output  string
		outcome float64
		text    string
		admin   string
	}{
		{"script correct", KindComparator, script, "42\n", 1, "Output is correct", ""},
		{"script wrong", KindComparator, script, "41\n", 0, "Not 100%% right", "differs"},
		{"testlib-cms correct", KindComparator, cmsChecker, "42\n", 1, "Output is correct", ""},
		{"testlib-cms partial", KindComparator, cmsChecker, "43\n", 0.5, "Output is partially correct", ""},
		{"testlib-cms wrong", KindComparator, cmsChecker, "7\n", 0, "Output isn't correct", ""},
		{"testlib-cms garbage", KindComparator, cmsChecker, "abc\n", 0, "Output isn't correct", ""},
		{"testlib correct", KindTestlib, plainChecker, "42\n", 1, "Output is correct", "ok correct 42"},
		{"testlib partial", KindTestlib, plainChecker, "43\n", 0.5, "Output is partially correct", "points 0.50 off by one"},
		{"testlib wrong", KindTestlib, plainChecker, "7\n", 0, "Output isn't correct", "wrong answer expected 42, found 7"},
		{"testlib garbage", KindTestlib, plainChecker, "abc\n", 0, "Output isn't correct", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			box := stepstest.Box(t, s)
			if err := box.CreateFile("user_output.txt", []byte(tc.output), false); err != nil {
				t.Fatal(err)
			}
			o, err := Checker{Kind: tc.kind}.Check(context.Background(), cfg, box, Files{
				UserOutput:    "user_output.txt",
				Program:       bytes.NewReader(tc.program),
				Input:         strings.NewReader("20 22\n"),
				CorrectOutput: strings.NewReader("42\n"),
				ExtraArgs:     []string{"batch"},
			})
			if err != nil {
				t.Fatal(err)
			}
			if o.Outcome != tc.outcome || !slices.Equal(o.Text, []string{tc.text}) ||
				(tc.admin != "" || tc.kind == KindComparator) && o.AdminText != tc.admin {
				t.Errorf("%+v, want %v %q %q", o, tc.outcome, tc.text, tc.admin)
			}
		})
	}

	// A checker that crashes is an error, not a verdict.
	box := stepstest.Box(t, s)
	_ = box.CreateFile("user_output.txt", []byte("42\n"), false)
	_, err := Checker{Kind: KindComparator}.Check(context.Background(), cfg, box, Files{
		UserOutput:    "user_output.txt",
		Program:       strings.NewReader("#!/bin/sh\nkill -SEGV $$\n"),
		Input:         strings.NewReader(""),
		CorrectOutput: strings.NewReader(""),
	})
	if err == nil {
		t.Error("crashing checker: want an error")
	}
}

// TestComparisonInBox reads the user output from a real box.
func TestComparisonInBox(t *testing.T) {
	s := stepstest.Sandbox(t, false)
	box := stepstest.Box(t, s)
	_ = box.CreateFile("output.txt", []byte("1   2\n\n"), false)
	o, err := Checker{Kind: KindDiff}.Check(context.Background(), trustedConfig(), box, Files{
		UserOutput: "output.txt", CorrectOutput: strings.NewReader("1 2"),
	})
	if err != nil || o.Outcome != 1 {
		t.Errorf("%+v %v", o, err)
	}
}
