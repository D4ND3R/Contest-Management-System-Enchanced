// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/grading/steps/stats.py of CMS:
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
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/sandbox"
)

// ExitStatus maps a sandbox status to the exit status of the judge
// protocol (upstream Sandbox.EXIT_*).
func ExitStatus(s sandbox.Status) string {
	switch s {
	case sandbox.StatusOK:
		return job.ExitOK
	case sandbox.StatusRuntimeError:
		return job.ExitNonzeroReturn
	case sandbox.StatusSignal:
		return job.ExitSignal
	case sandbox.StatusTimedOut:
		return job.ExitTimeout
	case sandbox.StatusWallTimedOut:
		return job.ExitTimeoutWall
	case sandbox.StatusMemoryLimit:
		return job.ExitMemoryLimit
	case sandbox.StatusSecurityViolation:
		return job.ExitSecurityViolation
	}
	return job.ExitSandboxError
}

// ExecutionStats returns the statistics of a run (upstream execution_stats
// without the outputs): CPU and wall clock time in seconds, peak memory in
// bytes, exit status and, for ExitSignal only, the signal.
func ExecutionStats(res *sandbox.Result) job.Stats {
	cpu, wall, mem := res.CPUTime, res.WallTime, res.MemoryKiB*1024
	st := job.Stats{
		ExecutionTime:          &cpu,
		ExecutionWallClockTime: &wall,
		ExecutionMemory:        &mem,
		ExitStatus:             ExitStatus(res.Status),
	}
	if res.Status == sandbox.StatusSignal {
		st.Signal = res.Signal
	}
	return st
}

// MergeStats merges the statistics of two executions (upstream
// merge_execution_stats). first may be nil: second is then copied.
//
//   - CPU times are added;
//   - memory is added (concurrent) or the maximum (sequential);
//   - wall clock times are the maximum (concurrent) or added (sequential);
//   - the exit status (and signal) is the first that is not OK;
//   - outputs are joined with a "\n===\n" line.
//
// Upstream joins the outputs present in either; job.Stats cannot tell an
// empty output from a missing one, so here an output takes part when
// either is non-empty (compilations, which collect every output, join
// them exactly like upstream: see Compile).
func MergeStats(first *job.Stats, second job.Stats, concurrent bool) job.Stats {
	if first == nil {
		return copyStats(second)
	}
	ret := copyStats(*first)
	ret.ExecutionTime = sumFloat(ret.ExecutionTime, second.ExecutionTime)
	if concurrent {
		ret.ExecutionWallClockTime = maxFloat(ret.ExecutionWallClockTime, second.ExecutionWallClockTime)
		ret.ExecutionMemory = sumInt(ret.ExecutionMemory, second.ExecutionMemory)
	} else {
		ret.ExecutionWallClockTime = sumFloat(ret.ExecutionWallClockTime, second.ExecutionWallClockTime)
		ret.ExecutionMemory = maxInt(ret.ExecutionMemory, second.ExecutionMemory)
	}
	if first.ExitStatus == job.ExitOK {
		ret.ExitStatus = second.ExitStatus
		ret.Signal = 0
		if second.ExitStatus == job.ExitSignal {
			ret.Signal = second.Signal
		}
	}
	if ret.Stdout != "" || second.Stdout != "" {
		ret.Stdout = ret.Stdout + outputSeparator + second.Stdout
	}
	if ret.Stderr != "" || second.Stderr != "" {
		ret.Stderr = ret.Stderr + outputSeparator + second.Stderr
	}
	return ret
}

// outputSeparator separates the outputs of successive commands.
const outputSeparator = "\n===\n"

// copyStats copies s, pointers included (the merge never aliases its
// inputs).
func copyStats(s job.Stats) job.Stats {
	if s.ExecutionTime != nil {
		s.ExecutionTime = job.Ptr(*s.ExecutionTime)
	}
	if s.ExecutionWallClockTime != nil {
		s.ExecutionWallClockTime = job.Ptr(*s.ExecutionWallClockTime)
	}
	if s.ExecutionMemory != nil {
		s.ExecutionMemory = job.Ptr(*s.ExecutionMemory)
	}
	return s
}

func sumFloat(x, y *float64) *float64 {
	switch {
	case x == nil:
		return y
	case y == nil:
		return x
	}
	return job.Ptr(*x + *y)
}

func maxFloat(x, y *float64) *float64 {
	switch {
	case x == nil:
		return y
	case y == nil:
		return x
	}
	return job.Ptr(max(*x, *y))
}

func sumInt(x, y *int64) *int64 {
	switch {
	case x == nil:
		return y
	case y == nil:
		return x
	}
	return job.Ptr(*x + *y)
}

func maxInt(x, y *int64) *int64 {
	switch {
	case x == nil:
		return y
	case y == nil:
		return x
	}
	return job.Ptr(max(*x, *y))
}
