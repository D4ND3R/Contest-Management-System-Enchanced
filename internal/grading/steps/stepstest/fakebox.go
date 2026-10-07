// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Package stepstest provides a fake box for the tests of the grading
// steps, checkers and task types: files live in a temporary directory and
// Run calls a function of the test instead of isolate (upstream
// cmstestsuite/unit_tests/grading/steps/fakesandbox.py).
package stepstest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/sandbox"
)

// RunFunc plays a program: it may write files in the box (WriteFile) and
// returns what isolate would.
type RunFunc func(b *FakeBox, spec sandbox.RunSpec) (*sandbox.Result, error)

// FakeBox implements steps.Box on a temporary directory.
type FakeBox struct {
	// Dir is the home of the box.
	Dir string
	// RunFunc plays the programs; nil means every run succeeds at once.
	RunFunc RunFunc

	mu    sync.Mutex
	specs []sandbox.RunSpec
}

// New returns an empty fake box.
func New(t testing.TB, run RunFunc) *FakeBox {
	t.Helper()
	return &FakeBox{Dir: t.TempDir(), RunFunc: run}
}

// OK is the result of a program that exited with 0.
func OK() *sandbox.Result { return &sandbox.Result{Status: sandbox.StatusOK} }

// Specs returns the runs requested so far.
func (b *FakeBox) Specs() []sandbox.RunSpec {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]sandbox.RunSpec(nil), b.specs...)
}

func (b *FakeBox) path(name string) (string, error) {
	if name == "" || filepath.IsAbs(name) && !strings.HasPrefix(name, sandbox.HomeDir+"/") {
		return "", fmt.Errorf("fake box: invalid name %q", name)
	}
	name = strings.TrimPrefix(name, sandbox.HomeDir+"/")
	c := filepath.Clean(name)
	if c == ".." || strings.HasPrefix(c, "../") {
		return "", fmt.Errorf("fake box: %q is outside the box", name)
	}
	return filepath.Join(b.Dir, c), nil
}

// Run records spec and calls RunFunc. Like isolate, it creates the
// redirections of the standard output and error first.
func (b *FakeBox) Run(ctx context.Context, spec sandbox.RunSpec) (*sandbox.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b.mu.Lock()
	b.specs = append(b.specs, spec)
	b.mu.Unlock()
	for _, f := range []string{spec.Stdout, spec.Stderr} {
		if f == "" {
			continue
		}
		if err := b.WriteFile(f, nil); err != nil {
			return nil, err
		}
	}
	if b.RunFunc == nil {
		return OK(), nil
	}
	return b.RunFunc(b, spec)
}

// WriteFile writes a file of the box (for RunFunc: what the program
// writes), replacing it.
func (b *FakeBox) WriteFile(name string, data []byte) error {
	p, err := b.path(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

// CreateFile implements steps.Box.
func (b *FakeBox) CreateFile(name string, data []byte, executable bool) error {
	return b.CreateFileFrom(name, bytes.NewReader(data), executable)
}

// CreateFileFrom implements steps.Box: it fails if the file exists.
func (b *FakeBox) CreateFileFrom(name string, r io.Reader, executable bool) error {
	p, err := b.path(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if executable {
		mode = 0o755
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Open implements steps.Box: regular files only, no final link.
func (b *FakeBox) Open(name string) (*os.File, error) {
	st, err := b.Stat(name)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("fake box: %s is not a regular file", name)
	}
	p, _ := b.path(name)
	return os.Open(p)
}

// ReadFile implements steps.Box.
func (b *FakeBox) ReadFile(name string, max int64) ([]byte, bool, error) {
	f, err := b.Open(name)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	if max < 0 {
		data, err := io.ReadAll(f)
		return data, false, err
	}
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(data)) > max {
		return data[:max], true, nil
	}
	return data, false, nil
}

// Stat implements steps.Box (without following a final link).
func (b *FakeBox) Stat(name string) (fs.FileInfo, error) {
	p, err := b.path(name)
	if err != nil {
		return nil, err
	}
	return os.Lstat(p)
}

// Exists implements steps.Box.
func (b *FakeBox) Exists(name string) bool {
	_, err := b.Stat(name)
	return err == nil
}

// Read returns the content of a file of the box, "" if it is missing.
func (b *FakeBox) Read(name string) string {
	p, err := b.path(name)
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return ""
	}
	return string(data)
}
