// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/grading/steps/utils.py of CMS:
// Copyright © 2010-2015 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2013 Bernard Blackham <bernard@largestprime.net>
// Copyright © 2013-2014 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2014 Fabian Gundlach <320pointsguy@gmail.com>
// Copyright © 2016 Myungwoo Chun <mc.tamaki@gmail.com>
// Copyright © 2016 Amir Keivan Mohtashami <akmohtashami97@gmail.com>

// Package steps runs the standard grading steps in a sandbox box
// (upstream cms/grading/steps): compilations (Compile), evaluations of
// contestant programs (Evaluate, StartEvaluation) and trusted programs
// such as checkers and managers (RunTrusted), with the limits, the
// statistics (job.Stats) and the messages for contestants of CMS.
//
// Every function returns an error when the sandbox failed (isolate error,
// cancelled context, unreadable box): the job must be retried, it is never
// a verdict. Everything the program did (compilation errors, time and
// memory limits, signals, forbidden system calls) is a result.
package steps

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/sandbox"
)

// Box is what the steps use of a *sandbox.Box (tests use fakes).
type Box interface {
	Run(ctx context.Context, spec sandbox.RunSpec) (*sandbox.Result, error)
	CreateFile(name string, data []byte, executable bool) error
	CreateFileFrom(name string, r io.Reader, executable bool) error
	Open(name string) (*os.File, error)
	ReadFile(name string, max int64) ([]byte, bool, error)
	Stat(name string) (fs.FileInfo, error)
	Exists(name string) bool
}

var _ Box = (*sandbox.Box)(nil)

// Config holds the settings of the steps (cms.yaml).
type Config struct {
	// Compilation bounds compilations (sandbox.compilation) and Trusted
	// the trusted programs: checkers, managers (sandbox.trusted).
	Compilation, Trusted config.SandboxLimit
	// Cgroups is sandbox.cgroups. Without cgroups (development only)
	// memory is limited as address space, which runtimes such as the JVM
	// reserve by the gigabyte: their languages (LargeAddressSpace) then
	// run without a memory limit.
	Cgroups bool
	// Log receives debug messages (nil: none).
	Log *slog.Logger
}

// ConfigFrom extracts the settings of the steps from the configuration.
func ConfigFrom(c *config.Config) Config {
	return Config{
		Compilation: c.Sandbox.Compilation,
		Trusted:     c.Sandbox.Trusted,
		Cgroups:     c.Sandbox.Cgroups,
	}
}

func (c *Config) log() *slog.Logger {
	if c.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return c.Log
}

// MaxCompilerOutput bounds what is kept of the standard output and error of
// each compilation command, in bytes (upstream reads 1024 bytes with
// get_file_to_string).
const MaxCompilerOutput = 1024

// wallLimit is the wall clock limit CMS gives a CPU time limit.
func wallLimit(cpu float64) float64 {
	if cpu <= 0 {
		return 0
	}
	return 2*cpu + 1
}

// runResult is the outcome of genericStep.
type runResult struct {
	stats job.Stats
	// stdout and stderr are the redirections of the last command run,
	// exitCode its exit code.
	stdout, stderr string
	exitCode       int
}

// genericStep runs commands one after the other in the box (upstream
// generic_step), each with base plus its argv and its own output files
// (<name>_stdout_<n>.txt, <name>_stderr_<n>.txt), stopping after the first
// that does not end normally. The statistics are merged sequentially;
// with collect, the outputs of the commands (at most MaxCompilerOutput
// bytes of each, sanitized) are joined with "\n===\n".
func genericStep(ctx context.Context, box Box, name string, cmds [][]string, base sandbox.RunSpec, collect bool, log *slog.Logger) (*runResult, error) {
	if len(cmds) == 0 {
		return nil, fmt.Errorf("step %s: no commands", name)
	}
	var (
		stats         *job.Stats
		outs, errOuts []string
		r             runResult
	)
	for i, argv := range cmds {
		spec := base
		spec.Argv = argv
		spec.Stdout = fmt.Sprintf("%s_stdout_%d.txt", name, i)
		spec.Stderr = fmt.Sprintf("%s_stderr_%d.txt", name, i)
		res, err := box.Run(ctx, spec)
		if err == nil && (res == nil || res.Status == sandbox.StatusInternalError) {
			err = errors.New("sandbox error")
		}
		if err != nil {
			return nil, fmt.Errorf("step %s, command %d of %d (%s): %w", name, i+1, len(cmds), argv[0], err)
		}
		log.Debug("step command", "step", name, "argv", argv, "status", res.Status.String(),
			"cpu", res.CPUTime, "wall", res.WallTime, "memory_kib", res.MemoryKiB)
		this := ExecutionStats(res)
		if collect {
			outs = append(outs, readOutput(box, spec.Stdout))
			errOuts = append(errOuts, readOutput(box, spec.Stderr))
		}
		merged := MergeStats(stats, this, false)
		stats = &merged
		r.stdout, r.stderr, r.exitCode = spec.Stdout, spec.Stderr, res.ExitCode
		if stats.ExitStatus != job.ExitOK {
			break
		}
	}
	r.stats = *stats
	if collect {
		r.stats.Stdout = strings.Join(outs, outputSeparator)
		r.stats.Stderr = strings.Join(errOuts, outputSeparator)
	}
	return &r, nil
}

// readOutput returns the beginning of an output file of a command, as text
// (upstream safe_get_str). A file the program removed or replaced (a
// compile-time program, as Template Haskell can run, may do it) counts as
// empty.
func readOutput(box Box, name string) string {
	data, _, err := box.ReadFile(name, MaxCompilerOutput)
	if err != nil {
		return ""
	}
	return job.SanitizeOutput(data)
}

// mounts turns the directories of a language (absolute paths or glob
// patterns) into read-only, optional mounts at the same paths. Patterns
// only match directories (isolate mounts nothing else).
func mounts(patterns []string) []sandbox.Dir {
	var dirs []sandbox.Dir
	for _, p := range patterns {
		if !strings.ContainsAny(p, `*?[\`) {
			dirs = append(dirs, sandbox.Dir{Inside: p, Optional: true})
			continue
		}
		matches, _ := filepath.Glob(p) // the pattern was validated
		for _, m := range matches {
			if st, err := os.Stat(m); err == nil && st.IsDir() && !strings.ContainsAny(m, ":=") {
				dirs = append(dirs, sandbox.Dir{Inside: m, Optional: true})
			}
		}
	}
	return dirs
}

// memoryKiB converts a memory limit in bytes to KiB for the sandbox
// (rounding down like upstream), or 0 (no limit) for a language with a
// large address space without cgroups.
func (c *Config) memoryKiB(bytes int64, lang *language.Language) int64 {
	if lang != nil && lang.LargeAddressSpace && !c.Cgroups {
		return 0
	}
	return bytes / 1024
}
