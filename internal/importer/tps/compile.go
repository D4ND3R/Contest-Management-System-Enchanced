// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package tps

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Compiler is the C++ compiler that builds the checker and the manager at
// import time, as upstream does (the binaries are stored as managers, so
// the machine importing must have the architecture of the workers).
var Compiler = "g++"

// CompileTimeout bounds one compilation.
var CompileTimeout = 5 * time.Minute

// compileFlags are those of upstream: g++ -x c++ -std=gnu++20 -O2 -static.
var compileFlags = []string{"-x", "c++", "-std=gnu++20", "-O2", "-static"}

const maxCompilerOutput = 4000

// compile compiles the C++ source at src as upstream does, where it is
// (so that it includes the headers next to it), but writes the binary to
// a temporary directory instead of the package (upstream leaves it next
// to the source as checker/checker or graders/manager). The source is
// compiled under its own name, from its directory, so the binary does not
// depend on where the package is. what names it in errors ("checker").
// Binaries are cached by the content of the source's directory.
func compile(src, what string) ([]byte, error) {
	dir, name := filepath.Split(src)
	key, cacheable, err := dirKey(dir, name, Compiler, compileFlags)
	if err != nil {
		return nil, err
	}
	if cacheable {
		if bin, ok := compiled.get(key); ok {
			return bin, nil
		}
	}
	if _, err := exec.LookPath(Compiler); err != nil {
		return nil, fmt.Errorf("the compiler %s is not installed: upstream compiles the %s of TPS tasks at import time", Compiler, what)
	}
	tmp, err := os.MkdirTemp("", "cms-tps-"+what+"-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	out := filepath.Join(tmp, what)
	ctx, cancel := context.WithTimeout(context.Background(), CompileTimeout)
	defer cancel()
	args := append(append([]string(nil), compileFlags...), "-o", out, name)
	cmd := exec.CommandContext(ctx, Compiler, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var msgs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &msgs, &msgs
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("could not compile the %s: %s took more than %s", what, Compiler, CompileTimeout)
		}
		m := strings.TrimSpace(strings.ReplaceAll(msgs.String(), tmp+string(filepath.Separator), ""))
		if len(m) > maxCompilerOutput {
			m = m[:maxCompilerOutput] + "\n[...]"
		}
		return nil, fmt.Errorf("could not compile the %s (%s %s %s): %v\n%s", what, Compiler, strings.Join(compileFlags, " "), src, err, m)
	}
	bin, err := os.ReadFile(out)
	if err != nil {
		return nil, err
	}
	if cacheable {
		compiled.put(key, bin)
	}
	return bin, nil
}

// maxKeyed is the size of a directory above which compilations are not
// cached (the key hashes everything a source could include).
const maxKeyed = 16 << 20

// dirKey hashes the compiler, its flags, the name of the source and the
// regular files of dir (the source and what it may include). Stale
// binaries of upstream imports (checker, manager) are left out.
func dirKey(dir, source, compiler string, flags []string) (key [32]byte, ok bool, err error) {
	if dir == "" {
		dir = "."
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return key, false, err
	}
	h := sha256.New()
	fmt.Fprintf(h, "%q %q %q\n", compiler, flags, source)
	var names []string
	for _, e := range entries {
		if e.Type().IsRegular() && e.Name() != "checker" && e.Name() != "manager" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var total int64
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			return key, false, err
		}
		total += int64(len(b))
		if total > maxKeyed {
			return key, false, nil
		}
		fmt.Fprintf(h, "%q %d\n", n, len(b))
		h.Write(b)
	}
	h.Sum(key[:0])
	return key, true, nil
}

// binCache keeps the last binaries compiled, so that validating a package
// and then importing it (the preview of the administration) compiles
// once.
type binCache struct {
	mu    sync.Mutex
	order [][32]byte
	bins  map[[32]byte][]byte
}

const cachedBinaries = 8

var compiled = &binCache{bins: map[[32]byte][]byte{}}

func (c *binCache) get(k [32]byte) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.bins[k]
	return b, ok
}

func (c *binCache) put(k [32]byte, b []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.bins[k]; ok {
		return
	}
	if len(c.order) == cachedBinaries {
		delete(c.bins, c.order[0])
		c.order = c.order[1:]
	}
	c.order = append(c.order, k)
	c.bins[k] = b
}
