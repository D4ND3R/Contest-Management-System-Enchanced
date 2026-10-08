// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/grading/steps/messages.py, compilation.py and
// evaluation.py of CMS:
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
)

// HumanMessage is an outcome message for contestants (upstream
// HumanMessage). Message and HelpText are msgids of the "cms" catalogs,
// translated when shown; the help texts are listed on the documentation
// page of the contestant site. The msgids missing from the catalogs of
// CMS are marked with N.
type HumanMessage struct {
	Shorthand string
	Message   string
	HelpText  string
}

// N marks a msgid without translating it (gettext N_): outcome texts are
// stored as msgids and translated when shown. `make generate`
// (internal/i18n/extract) collects the strings passed to N, which must be
// single literals; the msgids of the catalogs of CMS are not marked.
func N(msgid string) string { return msgid }

// MessageCollection is an ordered set of messages (upstream
// MessageCollection).
type MessageCollection struct {
	order []string
	byKey map[string]HumanMessage
}

// NewMessageCollection builds a collection; shorthands must be unique.
func NewMessageCollection(msgs ...HumanMessage) *MessageCollection {
	c := &MessageCollection{byKey: make(map[string]HumanMessage, len(msgs))}
	for _, m := range msgs {
		if _, dup := c.byKey[m.Shorthand]; dup {
			panic("steps: duplicate message " + m.Shorthand)
		}
		c.byKey[m.Shorthand] = m
		c.order = append(c.order, m.Shorthand)
	}
	return c
}

// Get returns the message with the given shorthand. An unknown shorthand
// is a programming error (upstream raises KeyError): it panics.
func (c *MessageCollection) Get(shorthand string) HumanMessage {
	m, ok := c.byKey[shorthand]
	if !ok {
		panic("steps: trying to get a non-existing message `" + shorthand + "'")
	}
	return m
}

// Lookup returns the message with the given shorthand, if any.
func (c *MessageCollection) Lookup(shorthand string) (HumanMessage, bool) {
	m, ok := c.byKey[shorthand]
	return m, ok
}

// All returns the messages in their order.
func (c *MessageCollection) All() []HumanMessage {
	out := make([]HumanMessage, len(c.order))
	for i, k := range c.order {
		out[i] = c.byKey[k]
	}
	return out
}

// Shorthands of CompilationMessages.
const (
	CompilationSuccess     = "success"
	CompilationFail        = "fail"
	CompilationTimeout     = "timeout"
	CompilationMemoryLimit = "memorylimit"
	CompilationSignal      = "signal"
)

// CompilationMessages are the outcomes of a compilation (upstream
// COMPILATION_MESSAGES).
var CompilationMessages = NewMessageCollection(
	HumanMessage{CompilationSuccess,
		"Compilation succeeded",
		"Your submission successfully compiled to an executable."},
	HumanMessage{CompilationFail,
		"Compilation failed",
		"Your submission did not compile correctly."},
	HumanMessage{CompilationTimeout,
		"Compilation timed out",
		"Your submission exceeded the time limit while compiling. " +
			"This might be caused by an excessive use of C++ " +
			"templates, for example."},
	// The two messages below are newer than the catalogs of CMS.
	HumanMessage{CompilationMemoryLimit,
		N("Compilation memory limit exceeded"),
		N("Your submission exceeded the memory limit while compiling. This might be caused by an excessive use of C++ templates, or too large global variables, for example.")},
	HumanMessage{CompilationSignal,
		N("Compilation killed with signal %s"),
		N("Your submission was killed with the specified signal. This might be caused by a bug in the compiler, for example.")},
)

// Shorthands of EvaluationMessages.
const (
	EvaluationSuccess     = "success"
	EvaluationPartial     = "partial"
	EvaluationWrong       = "wrong"
	EvaluationNoOutput    = "nooutput"
	EvaluationTimeout     = "timeout"
	EvaluationWallTimeout = "walltimeout"
	EvaluationMemoryLimit = "memorylimit"
	EvaluationSignal      = "signal"
	EvaluationReturnCode  = "returncode"
	// EvaluationSecurity is new in this fork: the seccomp filter killed
	// the program (its msgids are not in the catalogs of CMS).
	EvaluationSecurity = "security"
)

// EvaluationMessages are the outcomes of an evaluation (upstream
// EVALUATION_MESSAGES, plus EvaluationSecurity).
var EvaluationMessages = NewMessageCollection(
	HumanMessage{EvaluationSuccess,
		"Output is correct",
		"Your submission ran and gave the correct answer"},
	HumanMessage{EvaluationPartial,
		"Output is partially correct",
		"Your submission ran and gave the partially correct answer"},
	HumanMessage{EvaluationWrong,
		"Output isn't correct",
		"Your submission ran, but gave the wrong answer"},
	HumanMessage{EvaluationNoOutput,
		"Evaluation didn't produce file %s",
		"Your submission ran, but did not write on the correct output file"},
	HumanMessage{EvaluationTimeout,
		"Execution timed out",
		"Your submission used too much CPU time."},
	HumanMessage{EvaluationWallTimeout,
		"Execution timed out (wall clock limit exceeded)",
		"Your submission used too much total time. This might " +
			"be triggered by undefined code, or buffer overflow, " +
			"for example. Note that in this case the CPU time " +
			"visible in the submission details might be much smaller " +
			"than the time limit."},
	HumanMessage{EvaluationMemoryLimit,
		"Memory limit exceeded",
		"Your submission used too much memory."},
	HumanMessage{EvaluationSignal,
		"Execution killed by signal",
		"The evaluation was killed by a signal."},
	HumanMessage{EvaluationReturnCode,
		"Execution failed because the return code was nonzero",
		"Your submission failed because it exited with a return " +
			"code different from 0."},
	HumanMessage{EvaluationSecurity,
		N("Execution killed because of a forbidden system call"),
		N("Your submission tried to use a system call that is not allowed during the evaluation, such as opening network connections or tracing other processes.")},
)

// ExecutionCompleted is the text of a user test that ran correctly
// (upstream N_ in the task types).
const ExecutionCompleted = "Execution completed successfully"

// EvaluationText returns the message for contestants about an evaluation
// that did not end normally (upstream human_evaluation_message): nothing
// for a success (the task type writes its own) or a sandbox error (the
// submission stays "evaluating"), and never the exit code ("too much
// information").
func EvaluationText(st *job.Stats) []string {
	key := ""
	switch st.ExitStatus {
	case job.ExitTimeout:
		key = EvaluationTimeout
	case job.ExitTimeoutWall:
		key = EvaluationWallTimeout
	case job.ExitSignal:
		key = EvaluationSignal
	case job.ExitMemoryLimit:
		key = EvaluationMemoryLimit
	case job.ExitNonzeroReturn:
		key = EvaluationReturnCode
	case job.ExitSecurityViolation:
		key = EvaluationSecurity
	default: // ok, sandbox error
		return nil
	}
	return []string{EvaluationMessages.Get(key).Message}
}
