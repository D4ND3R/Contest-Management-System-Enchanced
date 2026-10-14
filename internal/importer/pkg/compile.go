// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package pkg

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/checker"
)

// Compiler is the C++ compiler that builds the programs of graders/
// (checker, manager, controller) when the package has only their source,
// as the Polygon and TPS loaders do. The binaries are stored as managers:
// the machine importing must have the architecture of the workers.
var Compiler = "g++"

// CompileTimeout bounds one compilation.
var CompileTimeout = 5 * time.Minute

// compileFlags are those of the TPS loader of CMS; -DCMS is added for a
// comparator, so that a checker written with testlib.h speaks the CMS
// protocol (the patched testlib.h of CMS, as Polygon checkers).
var compileFlags = []string{"-x", "c++", "-std=gnu++20", "-O2", "-static"}

// cppExtensions are the extensions of the C++ sources that are compiled.
var cppExtensions = []string{".cpp", ".cc", ".cxx", ".c++", ".C"}

// headerExtensions are the files of graders/ copied next to a source
// that is compiled (what it may #include).
var headerExtensions = []string{".h", ".hh", ".hpp", ".hxx", ".inc", ".ipp", ".tcc"}

const (
	// maxCompileInput bounds each file given to the compiler.
	maxCompileInput = 16 << 20
	// maxCompilerOutput is how much of the compiler's messages an error
	// keeps.
	maxCompilerOutput = 4000
	// testlibHeader is the name programs include.
	testlibHeader = "testlib.h"
)

// compileMode says how a program is compiled.
type compileMode struct {
	// cms: a comparator (CMS protocol): -DCMS, and the testlib.h of CMS
	// replaces the one of the package.
	cms bool
}

// compileProgram compiles the C++ source graders/<file> of src with the
// headers of graders/ (and testlib.h: the package's, or the patched one of
// CMS if it has none or mode.cms), in a temporary directory, under the
// source's own name, so the binary does not depend on where the package
// is. Binaries are cached by everything the compilation reads.
func compileProgram(src source, file string, mode compileMode) ([]byte, error) {
	files, _, err := src.list(GradersDir)
	if err != nil {
		return nil, err
	}
	inputs := map[string][]byte{}
	for _, f := range files {
		if f != file && !slices.Contains(headerExtensions, filepath.Ext(f)) {
			continue
		}
		data, err := src.read(GradersDir+"/"+f, maxCompileInput)
		if err != nil {
			return nil, err
		}
		inputs[f] = data
	}
	if _, ok := inputs[testlibHeader]; !ok || mode.cms {
		inputs[testlibHeader] = checker.TestlibHeader
	}
	flags := slices.Clone(compileFlags)
	if mode.cms {
		flags = append(flags, "-DCMS")
	}
	key := cacheKey(Compiler, flags, file, inputs)
	if bin, ok := compiled.get(key); ok {
		return bin, nil
	}
	if _, err := exec.LookPath(Compiler); err != nil {
		return nil, fmt.Errorf("the compiler %s is not installed: compile %s/%s yourself and put the executable in %s/",
			Compiler, GradersDir, file, GradersDir)
	}
	tmp, err := os.MkdirTemp("", "cms-package-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	for name, data := range inputs {
		if err := os.WriteFile(filepath.Join(tmp, name), data, 0o644); err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), CompileTimeout)
	defer cancel()
	args := append(flags, "-o", "program", file)
	cmd := exec.CommandContext(ctx, Compiler, args...)
	cmd.Dir = tmp
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("could not compile %s: %s took more than %s", file, Compiler, CompileTimeout)
		}
		return nil, fmt.Errorf("could not compile %s (%s %s): %v\n%s", file, Compiler, strings.Join(args, " "), err, truncate(out.String()))
	}
	bin, err := os.ReadFile(filepath.Join(tmp, "program"))
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
func cacheKey(compiler string, flags []string, file string, inputs map[string][]byte) [32]byte {
	h := sha256.New()
	fmt.Fprintf(h, "%q %q %q\n", compiler, flags, file)
	names := make([]string, 0, len(inputs))
	for n := range inputs {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		fmt.Fprintf(h, "%q %d\n", n, len(inputs[n]))
		h.Write(inputs[n])
	}
	var k [32]byte
	h.Sum(k[:0])
	return k
}

// binCache keeps the last few binaries compiled, so that validating a
// package and then importing it (the preview of the administration)
// compiles once.
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
