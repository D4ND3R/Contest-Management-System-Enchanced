// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/grading/steps/trusted.py (trusted_step) of CMS:
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

	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/sandbox"
)

// TrustedResult is the outcome of trusted commands.
type TrustedResult struct {
	// OK: every command ended normally. Otherwise the admins' program
	// or the configuration is broken (not the contestant's fault).
	OK    bool
	Stats job.Stats
	// Stdout and Stderr are the files of the box holding the standard
	// output and error of the last command (the standard manager output),
	// ExitCode its exit code (testlib checkers answer with it).
	Stdout, Stderr string
	ExitCode       int
}

// RunTrusted runs trusted commands (checkers, managers written by the
// admins) in the box (upstream trusted_step): sandboxed anyway, to bound
// the resources a mistake can use, with the trusted limits of the
// configuration (sandbox.trusted), without the seccomp filter. The box
// keeps their outputs (TrustedResult.Stdout, Stderr).
//
// The error is non-nil only when the sandbox failed (retry the job).
func (c *Config) RunTrusted(ctx context.Context, box Box, cmds [][]string) (*TrustedResult, error) {
	spec := sandbox.RunSpec{
		CPUTime:   c.Trusted.MaxTimeSec,
		WallTime:  wallLimit(c.Trusted.MaxTimeSec),
		MemoryKiB: c.Trusted.MaxMemoryKiB,
		Processes: c.Trusted.MaxProcesses,
	}
	r, err := genericStep(ctx, box, "trusted", cmds, spec, false, c.log())
	if err != nil {
		return nil, err
	}
	res := &TrustedResult{OK: r.stats.ExitStatus == job.ExitOK, Stats: r.stats, Stdout: r.stdout, Stderr: r.stderr, ExitCode: r.exitCode}
	if !res.OK {
		c.log().Warn("trusted step ended with an error (usually a bug of a manager or a configuration issue)",
			"exit_status", r.stats.ExitStatus, "signal", r.stats.Signal)
	}
	return res, nil
}
