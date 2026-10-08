//go:build sandbox

// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package sandbox

import (
	"bufio"
	"context"
	"os"
	"strings"
	"testing"
)

// TestPipesAndInheritedFiles connects two boxes and the worker with
// pipes, like the Interactive task type: the "controller" talks to the
// worker on its standard streams and to the "solution" through the
// descriptors 3 and 4 it inherits; the solution reads and writes its
// standard streams.
func TestPipesAndInheritedFiles(t *testing.T) {
	s := newTestSandbox(t, nil)
	ctrl := newTestBox(t, s)
	sol := newTestBox(t, s)
	ctx := context.Background()

	pipe := func() (*os.File, *os.File) {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { r.Close(); w.Close() })
		return r, w
	}
	c2uR, c2uW := pipe()
	u2cR, u2cW := pipe()
	inR, inW := pipe()   // worker -> controller stdin
	outR, outW := pipe() // controller stdout -> worker

	pc, err := ctrl.Start(ctx, RunSpec{
		Argv: []string{"/bin/sh", "-c",
			`echo ready; read go; echo 21 >&3; exec 3>&-; read x <&4; echo "$x" > result.txt; echo "done $x"`},
		StdinFile:  inR,
		StdoutFile: outW,
		ExtraFiles: []*os.File{c2uW, u2cR},
		CPUTime:    2,
		WallTime:   10,
	})
	if err != nil {
		t.Fatal(err)
	}
	// The worker keeps only its own ends.
	for _, f := range []*os.File{inR, outW, c2uW, u2cR} {
		f.Close()
	}
	ps, err := sol.Start(ctx, RunSpec{
		Argv:           []string{"/bin/sh", "-c", `read x; echo $((x*2))`},
		StdinFile:      c2uR,
		StdoutFile:     u2cW,
		CPUTime:        2,
		WallTime:       10,
		Seccomp:        true,
		RestrictWrites: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	c2uR.Close()
	u2cW.Close()

	out := bufio.NewReader(outR)
	if line, err := out.ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("controller said %q, %v", line, err)
	}
	if _, err := inW.WriteString("go\n"); err != nil {
		t.Fatal(err)
	}
	inW.Close()
	if line, err := out.ReadString('\n'); err != nil || line != "done 42\n" {
		t.Fatalf("controller said %q, %v", line, err)
	}
	if rs, err := ps.Wait(); err != nil || rs.Status != StatusOK {
		t.Fatalf("solution: %+v %v", rs, err)
	}
	if rc, err := pc.Wait(); err != nil || rc.Status != StatusOK {
		t.Fatalf("controller: %+v %v", rc, err)
	}
	data, _, _ := ctrl.ReadFile("result.txt", 100)
	if strings.TrimSpace(string(data)) != "42" {
		t.Errorf("result %q", data)
	}
}

func TestExtraFilesNeedTrustedPrograms(t *testing.T) {
	s := newTestSandbox(t, nil)
	b := newTestBox(t, s)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if _, err := b.Run(context.Background(), RunSpec{
		Argv: []string{"/bin/true"}, ExtraFiles: []*os.File{w}, Seccomp: true,
	}); err == nil {
		t.Fatal("extra files with seccomp were accepted")
	}
}
