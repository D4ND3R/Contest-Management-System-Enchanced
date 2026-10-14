// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package polygon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/checker"
)

// Compiler is the C++ compiler that builds the checkers at import time,
// as upstream does (the binary is stored as the "checker" manager, so the
// machine importing must have the architecture of the workers).
var Compiler = "g++"

// CompileTimeout bounds the compilation of one checker.
var CompileTimeout = 5 * time.Minute

// checkerFlags are those of upstream: g++ -x c++ -O2 -static -DCMS. With
// -DCMS the patched testlib.h writes the outcome in the CMS format.
var checkerFlags = []string{"-x", "c++", "-O2", "-static", "-DCMS"}

// maxCompilerOutput is how much of the compiler's messages an error
// keeps.
const maxCompilerOutput = 4000

// compileChecker compiles the testlib checker at src as upstream does:
// in a temporary directory, next to the testlib.h of CMS (the testlib-cms
// patch), which replaces the testlib.h of the package. The source is
// compiled under a fixed name, so the binary does not depend on where it
// was built. Binaries are cached by the content of the source.
func compileChecker(src string) ([]byte, error) {
	source, err := os.ReadFile(src)
	if err != nil {
		return nil, err
	}
	key := cacheKey(Compiler, checkerFlags, checker.TestlibHeader, source)
	if bin, ok := compiled.get(key); ok {
		return bin, nil
	}
	if _, err := exec.LookPath(Compiler); err != nil {
		return nil, fmt.Errorf("the compiler %s is not installed: upstream compiles the checker of Polygon tasks at import time", Compiler)
	}
	tmp, err := os.MkdirTemp("", "cms-polygon-checker-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	if err := os.WriteFile(filepath.Join(tmp, "testlib.h"), checker.TestlibHeader, 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(tmp, "check.cpp"), source, 0o644); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), CompileTimeout)
	defer cancel()
	args := append(append([]string(nil), checkerFlags...), "-o", "check", "check.cpp")
	cmd := exec.CommandContext(ctx, Compiler, args...)
	cmd.Dir = tmp
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("could not compile the checker: %s took more than %s", Compiler, CompileTimeout)
		}
		return nil, fmt.Errorf("could not compile the checker (%s %s): %v\n%s", Compiler, strings.Join(args, " "), err, truncate(out.String()))
	}
	bin, err := os.ReadFile(filepath.Join(tmp, "check"))
	if err != nil {
		return nil, err
	}
	compiled.put(key, bin)
	return bin, nil
}

func truncate(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxCompilerOutput {
		s = s[:maxCompilerOutput] + "\n[...]"
	}
	return s
}

// cacheKey hashes what a compilation depends on.
func cacheKey(compiler string, flags []string, parts ...[]byte) [32]byte {
	h := sha256.New()
	fmt.Fprintf(h, "%q %q\n", compiler, flags)
	for _, p := range parts {
		fmt.Fprintf(h, "%d\n", len(p))
		h.Write(p)
	}
	var k [32]byte
	h.Sum(k[:0])
	return k
}

// binCache keeps the last few binaries compiled, so that validating a
// package and then importing it (the preview of the administration), or
// importing a contest whose tasks share a checker, compiles once.
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
