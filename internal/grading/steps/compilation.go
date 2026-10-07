// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/grading/steps/compilation.py of CMS:
// Copyright © 2010-2015 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2013 Bernard Blackham <bernard@largestprime.net>
// Copyright © 2013-2014 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2014 Fabian Gundlach <320pointsguy@gmail.com>
// Copyright © 2016 Myungwoo Chun <mc.tamaki@gmail.com>
// Copyright © 2016 Amir Keivan Mohtashami <akmohtashami97@gmail.com>

package steps

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"syscall"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/sandbox"
)

// CompilationResult is the outcome of a compilation the sandbox carried
// out.
type CompilationResult struct {
	// OK: the compilation produced an executable (upstream
	// compilation_success); otherwise it is the contestant's fault.
	OK bool
	// Text is the message for the contestant: a msgid of
	// CompilationMessages and its arguments.
	Text []string
	// Stats of the commands, with their outputs.
	Stats job.Stats
}

// Compile runs compilation commands (from lang.CompilationCommands) in
// the box, which already holds the sources (upstream compilation_step).
// The commands get the compilation limits of the configuration (CPU time,
// wall clock time 2×CPU+1 s, memory, sandbox.compilation.max_processes
// processes), and what lang needs: its directories, environment and
// files, and file locks. lang may be nil. Compilers never run under the
// seccomp filter.
//
// The error is non-nil only when the sandbox failed (retry the job).
func (c *Config) Compile(ctx context.Context, box Box, lang *language.Language, cmds [][]string) (*CompilationResult, error) {
	spec := sandbox.RunSpec{
		CPUTime:   c.Compilation.MaxTimeSec,
		WallTime:  wallLimit(c.Compilation.MaxTimeSec),
		MemoryKiB: c.memoryKiB(c.Compilation.MaxMemoryKiB*1024, lang),
		Processes: c.Compilation.MaxProcesses,
	}
	if lang != nil {
		spec.Dirs = mounts(lang.Compilation.Dirs)
		spec.Env = lang.Compilation.Env
		spec.AllowFileLocks = lang.Compilation.FileLocks
		if err := copyHostFiles(box, lang.Compilation.Files); err != nil {
			return nil, fmt.Errorf("compilation (%s): %w", lang.Name, err)
		}
	}
	r, err := genericStep(ctx, box, "compilation", cmds, spec, true, c.log())
	if err != nil {
		return nil, err
	}
	res := &CompilationResult{Stats: r.stats}
	switch r.stats.ExitStatus {
	case job.ExitOK:
		res.OK = true
		res.Text = []string{CompilationMessages.Get(CompilationSuccess).Message}
	case job.ExitNonzeroReturn:
		res.Text = []string{CompilationMessages.Get(CompilationFail).Message}
	case job.ExitTimeout, job.ExitTimeoutWall:
		res.Text = []string{CompilationMessages.Get(CompilationTimeout).Message}
	case job.ExitMemoryLimit:
		res.Text = []string{CompilationMessages.Get(CompilationMemoryLimit).Message}
	case job.ExitSignal:
		res.Text = []string{CompilationMessages.Get(CompilationSignal).Message, strconv.Itoa(r.stats.Signal)}
	case job.ExitSecurityViolation:
		// Compilers do not run under the filter; should it ever happen,
		// the compiler was killed by SIGSYS.
		res.Stats.ExitStatus, res.Stats.Signal = job.ExitSignal, int(syscall.SIGSYS)
		res.Text = []string{CompilationMessages.Get(CompilationSignal).Message, strconv.Itoa(int(syscall.SIGSYS))}
	default:
		return nil, fmt.Errorf("compilation: unexpected exit status %q", r.stats.ExitStatus)
	}
	return res, nil
}

// copyHostFiles copies host files into the box (language
// compilation.files); missing host files are skipped (the language is not
// installed: its compiler will say so).
func copyHostFiles(box Box, files map[string]string) error {
	for name, host := range files {
		data, err := os.ReadFile(host)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if err := box.CreateFile(name, data, false); err != nil {
			return err
		}
	}
	return nil
}
