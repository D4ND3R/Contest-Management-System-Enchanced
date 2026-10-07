// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// The exit status classification is ported from cms/grading/Sandbox.py
// (get_exit_status and translate_box_exitcode) of CMS:
// Copyright © 2010-2015 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2014 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2026 Luca Versari <veluca93@gmail.com>

package sandbox

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// Status is the main reason why a program ended (CMS get_exit_status).
type Status int

// The statuses, in the order CMS checks them: a sandbox error first, then
// time, memory, signals and exit codes.
const (
	StatusOK                Status = iota // exit code 0
	StatusRuntimeError                    // non-zero exit code
	StatusSignal                          // killed by a signal
	StatusTimedOut                        // CPU time limit
	StatusWallTimedOut                    // wall clock limit
	StatusMemoryLimit                     // killed by the OOM killer of its cgroup
	StatusSecurityViolation               // killed by the seccomp filter
	StatusInternalError                   // the sandbox failed: retry the job
)

// String returns the exit status names of CMS (Sandbox.EXIT_*), plus
// "security violation".
func (s Status) String() string {
	switch s {
	case StatusOK:
		return "ok"
	case StatusRuntimeError:
		return "nonzero return"
	case StatusSignal:
		return "signal"
	case StatusTimedOut:
		return "timeout"
	case StatusWallTimedOut:
		return "wall timeout"
	case StatusMemoryLimit:
		return "memory limit exceeded"
	case StatusSecurityViolation:
		return "security violation"
	case StatusInternalError:
		return "sandbox error"
	}
	return "status(" + strconv.Itoa(int(s)) + ")"
}

// Result is what isolate reported about a run (its meta file).
type Result struct {
	Status Status
	// ExitCode is the exit code of a program that exited (0 otherwise).
	ExitCode int
	// Signal is the signal that killed the program (0 if none).
	Signal int
	// CPUTime and WallTime are in seconds.
	CPUTime, WallTime float64
	// MemoryKiB is the peak memory of the cgroup with cgroups (it
	// includes the page cache of the files the program wrote, like in
	// CMS), and the peak resident set of the program without them.
	MemoryKiB int64
	// Killed is set when isolate killed the program (limits, Kill).
	Killed bool
	// Message is isolate's message ("Time limit exceeded", ...), for logs.
	Message string
}

// SIGSYS is what SECCOMP_RET_KILL_PROCESS kills with.
const sigsys = int(syscall.SIGSYS)

// maxMetaSize bounds the meta file read (it has a dozen short lines).
const maxMetaSize = 64 << 10

// result builds the Result of a finished process.
func (p *Process) result() (*Result, error) {
	if p.cancelled.Load() {
		return nil, fmt.Errorf("sandbox: run interrupted: %w", context.Cause(p.ctx))
	}
	code := 0
	if p.waitErr != nil {
		var ee *exec.ExitError
		if !errors.As(p.waitErr, &ee) {
			return internalError(fmt.Sprintf("waiting for isolate: %v", p.waitErr))
		}
		code = ee.ExitCode() // -1 when isolate itself died on a signal
	}
	meta, err := readMeta(p.metaPath)
	if err != nil {
		return internalError(fmt.Sprintf("isolate exit status %d, no meta file (%v): %s", code, err, strings.TrimSpace(p.stderr.String())))
	}
	res, err := classify(meta, code, p.seccomp)
	if err != nil && res != nil && res.Message == "" {
		res.Message = strings.TrimSpace(p.stderr.String())
	}
	return res, err
}

func internalError(msg string) (*Result, error) {
	return &Result{Status: StatusInternalError, Message: msg}, errors.New("sandbox error: " + msg)
}

// meta is a parsed isolate meta file: key -> values (keys may repeat).
type meta map[string][]string

func (m meta) get(key string) (string, bool) {
	v, ok := m[key]
	if !ok || len(v) == 0 {
		return "", false
	}
	return v[0], true
}

func readMeta(path string) (meta, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxMetaSize))
	if err != nil {
		return nil, err
	}
	return parseMeta(data)
}

func parseMeta(data []byte) (meta, error) {
	m := meta{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("malformed meta line %q", line)
		}
		m[k] = append(m[k], v)
	}
	return m, sc.Err()
}

// classify turns the meta file and the exit status of isolate into a
// Result, like CMS get_exit_status: isolate exits with 0 (the program
// succeeded), 1 (the program failed: TO, SG or RE) or anything else
// together with status XX (isolate failed).
func classify(m meta, isolateExit int, seccomp bool) (*Result, error) {
	res := &Result{}
	res.Message, _ = m.get("message")
	var errs []string
	num := func(key string, dst any) {
		v, ok := m.get(key)
		if !ok {
			return
		}
		var err error
		switch d := dst.(type) {
		case *int:
			*d, err = strconv.Atoi(v)
		case *int64:
			*d, err = strconv.ParseInt(v, 10, 64)
		case *float64:
			*d, err = strconv.ParseFloat(v, 64)
		}
		if err != nil {
			errs = append(errs, fmt.Sprintf("meta %s:%s", key, v))
		}
	}
	num("time", &res.CPUTime)
	num("time-wall", &res.WallTime)
	num("exitcode", &res.ExitCode)
	num("exitsig", &res.Signal)
	if _, ok := m["cg-mem"]; ok {
		num("cg-mem", &res.MemoryKiB)
	} else {
		num("max-rss", &res.MemoryKiB)
	}
	_, res.Killed = m["killed"]
	statuses := m["status"]
	has := func(s string) bool {
		for _, x := range statuses {
			if x == s {
				return true
			}
		}
		return false
	}
	switch {
	case isolateExit != 0 && isolateExit != 1, has("XX"), len(errs) > 0:
		res.Status = StatusInternalError
		msg := res.Message
		if len(errs) > 0 {
			msg = strings.Join(append(errs, msg), "; ")
		}
		return res, fmt.Errorf("sandbox error: isolate exit status %d: %s", isolateExit, msg)
	case has("TO"):
		if strings.Contains(res.Message, "wall") {
			res.Status = StatusWallTimedOut
		} else {
			res.Status = StatusTimedOut
		}
	case len(m["cg-oom-killed"]) > 0:
		// The OOM killer killed the program (SG) or one of its
		// processes (the program then usually fails): either way the
		// root cause is the memory limit.
		res.Status = StatusMemoryLimit
	case has("SG"):
		if seccomp && res.Signal == sigsys {
			res.Status = StatusSecurityViolation
		} else {
			res.Status = StatusSignal
		}
	case has("RE"):
		res.Status = StatusRuntimeError
	case isolateExit == 1 || len(statuses) > 0:
		// Exit status 1 without a known status, or an unknown status.
		res.Status = StatusInternalError
		return res, fmt.Errorf("sandbox error: unexpected isolate result (exit status %d, status %q, %s)",
			isolateExit, statuses, res.Message)
	default:
		res.Status = StatusOK
	}
	return res, nil
}
