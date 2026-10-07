// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package job

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Compilation outcomes (the compilation_outcome column).
const (
	CompilationOK   = "ok"
	CompilationFail = "fail"
)

// Exit statuses of a sandboxed execution: the values of upstream Sandbox
// (EXIT_*), plus ExitSecurityViolation for the seccomp filter of this fork.
const (
	ExitOK                = "ok"
	ExitSandboxError      = "sandbox error"
	ExitSignal            = "signal"
	ExitTimeout           = "timeout"
	ExitTimeoutWall       = "wall timeout"
	ExitMemoryLimit       = "memory limit exceeded"
	ExitNonzeroReturn     = "nonzero return"
	ExitSecurityViolation = "security violation"
)

// ValidExitStatus reports whether s is one of the exit statuses.
func ValidExitStatus(s string) bool {
	switch s {
	case ExitOK, ExitSandboxError, ExitSignal, ExitTimeout, ExitTimeoutWall,
		ExitMemoryLimit, ExitNonzeroReturn, ExitSecurityViolation:
		return true
	}
	return false
}

// Stats are the statistics of the executions of a job (upstream StatsDict,
// the "plus" of a job). Nil pointers are unknown values (NULL columns).
type Stats struct {
	// ExecutionTime is the CPU time in seconds.
	ExecutionTime *float64 `json:"execution_time"`
	// ExecutionWallClockTime is the wall clock time in seconds.
	ExecutionWallClockTime *float64 `json:"execution_wall_clock_time"`
	// ExecutionMemory is the peak memory in bytes.
	ExecutionMemory *int64 `json:"execution_memory"`
	ExitStatus      string `json:"exit_status"`
	// Signal is the signal that killed the program (ExitSignal only).
	Signal int `json:"signal,omitempty"`
	// Stdout and Stderr are the outputs of the compiler (compilations),
	// cleaned with SanitizeOutput.
	Stdout string `json:"stdout,omitempty"`
	Stderr string `json:"stderr,omitempty"`
}

// Durations measure where the worker spent its time on a job.
type Durations struct {
	// Wait is the time from enqueueing to the start of the job.
	Wait time.Duration `json:"wait_ns,omitempty"`
	// Fetch is the time spent getting files from the blob store.
	Fetch time.Duration `json:"fetch_ns,omitempty"`
	// Run is the time spent in the task type (sandboxes, compilers,
	// checkers).
	Run time.Duration `json:"run_ns,omitempty"`
	// Store is the time spent putting outputs in the blob store.
	Store time.Duration `json:"store_ns,omitempty"`
	// Total is the time from the start of the job to the result.
	Total time.Duration `json:"total_ns,omitempty"`
}

// Result is what a worker sends back for a job (upstream: the job returned
// by Worker.execute_job_group, with the fields filled by the task type).
type Result struct {
	// V is the protocol version; Encode sets it.
	V int `json:"v"`
	// JobID, Operation, Attempt and Generation are copied from the job
	// (NewResult); the dispatcher matches them against the database.
	JobID      string    `json:"job_id"`
	Operation  Operation `json:"operation"`
	Attempt    int       `json:"attempt"`
	Generation int64     `json:"generation"`
	// Worker is the name of the worker that ran the job.
	Worker string `json:"worker"`

	// Success is false when the job could not be carried out (sandbox or
	// infrastructure failure, missing file): the dispatcher counts a try
	// and enqueues it again. A contestant's error (compilation error, wrong
	// answer...) is a successful job.
	Success bool `json:"success"`
	// Error explains a failure, for logs and admins.
	Error string `json:"error,omitempty"`
	// Tombstone: the job needed a file whose content is gone
	// (TombstoneDigest; upstream plus["tombstone"]). For an evaluation
	// whose executable is the tombstone the dispatcher recompiles.
	Tombstone bool `json:"tombstone,omitempty"`

	// CompilationOutcome is CompilationOK or CompilationFail (compilations
	// that succeeded).
	CompilationOutcome string `json:"compilation_outcome,omitempty"`
	// Outcome is the outcome of an evaluation, a float as Python prints it
	// (FormatOutcome): "1.0", "0.0", "0.5". User tests may have none.
	Outcome string `json:"outcome,omitempty"`
	// Text is the message for the contestant: an untranslated msgid of the
	// "cms" catalogs with %-style placeholders, then its arguments, e.g.
	// {"Evaluation didn't produce file %s", "output.txt"}. It is
	// translated when shown.
	Text []string `json:"text,omitempty"`
	// AdminText is the message for the admins (checker output).
	AdminText string `json:"admin_text,omitempty"`
	// Stats of the execution (compiler, or contestant program); nil when
	// nothing ran.
	Stats *Stats `json:"plus,omitempty"`
	// Executables produced by a compilation, name -> digest.
	Executables map[string]string `json:"executables,omitempty"`
	// UserOutput is the digest of the output of a user test, if the job
	// asked for it and the program produced it.
	UserOutput string `json:"user_output,omitempty"`
	// Sandboxes are the paths of the sandboxes used on the worker, and
	// SandboxDigests the digests of their archives (path -> digest) when
	// the operation asked for archiving.
	Sandboxes      []string          `json:"sandboxes,omitempty"`
	SandboxDigests map[string]string `json:"sandbox_digests,omitempty"`
	// Durations of the work.
	Durations Durations `json:"durations"`
}

// NewResult returns an empty result for j, carrying its identity.
func (j *Job) NewResult(worker string) *Result {
	return &Result{
		JobID:      j.ID,
		Operation:  j.Operation,
		Attempt:    j.Attempt,
		Generation: j.Generation,
		Worker:     worker,
	}
}

// Fail marks the result as an infrastructure failure, dropping any
// outcome or file it had.
func (r *Result) Fail(err error) {
	r.Success = false
	r.Error = "unknown error"
	if err != nil {
		r.Error = TruncateUTF8(SanitizeOutput([]byte(err.Error())), MaxTextLen)
	}
	r.CompilationOutcome, r.Outcome, r.UserOutput = "", "", ""
	r.Executables = nil
}

// OutcomeValue parses Outcome; ok is false if there is none.
func (r *Result) OutcomeValue() (v float64, ok bool, err error) {
	if r.Outcome == "" {
		return 0, false, nil
	}
	v, err = parseOutcome(r.Outcome)
	return v, err == nil, err
}

// Validate checks that the result is well formed for its operation.
func (r *Result) Validate() error {
	if err := r.Operation.validate(); err != nil {
		return err
	}
	k, key := r.Operation.Type, r.Operation.Key()
	switch {
	case !validID(r.JobID):
		return invalid("%s: invalid job id %q", key, r.JobID)
	case r.Attempt < 1:
		return invalid("%s: attempt %d must be at least 1", key, r.Attempt)
	case r.Generation < 0:
		return invalid("%s: negative generation", key)
	case r.Worker == "" || len(r.Worker) > MaxNameLen || !printable(r.Worker):
		return invalid("%s: invalid worker name %q", key, r.Worker)
	case !storable(r.Error, MaxTextLen) || !storable(r.AdminText, MaxTextLen):
		return invalid("%s: the error or admin text is too long, not UTF-8 or has NUL bytes", key)
	case r.Durations.Wait < 0 || r.Durations.Fetch < 0 || r.Durations.Run < 0 || r.Durations.Store < 0 || r.Durations.Total < 0:
		return invalid("%s: negative duration", key)
	case r.Tombstone && r.Success:
		return invalid("%s: a job that needed a missing file cannot succeed", key)
	}
	n := 0
	for _, t := range r.Text {
		n += len(t)
		if !storable(t, MaxTextLen) {
			return invalid("%s: the text has invalid UTF-8 or NUL bytes", key)
		}
	}
	if n > MaxTextLen {
		return invalid("%s: the text is longer than %d bytes", key, MaxTextLen)
	}
	if r.Stats != nil {
		if err := r.Stats.validate(key); err != nil {
			return err
		}
	}
	if len(r.Sandboxes) > MaxFiles || len(r.SandboxDigests) > MaxFiles {
		return invalid("%s: too many sandboxes", key)
	}
	for _, p := range r.Sandboxes {
		if p == "" || len(p) > 4096 || !printable(p) {
			return invalid("%s: invalid sandbox path %q", key, p)
		}
	}
	for p, d := range r.SandboxDigests {
		if p == "" || len(p) > 4096 || !printable(p) || !realDigest(d) {
			return invalid("%s: invalid sandbox archive %q: %q", key, p, d)
		}
	}
	if err := checkFiles(key, "executables", r.Executables, false, false); err != nil {
		return err
	}
	if r.UserOutput != "" && !realDigest(r.UserOutput) {
		return invalid("%s: invalid user output digest %q", key, r.UserOutput)
	}

	if !r.Success {
		if r.CompilationOutcome != "" || r.Outcome != "" || len(r.Executables) > 0 || r.UserOutput != "" {
			return invalid("%s: a failed job has no outcome, executables or output", key)
		}
		return nil
	}
	if r.Error != "" {
		return invalid("%s: a successful job has no error", key)
	}
	if k.IsCompilation() {
		switch {
		case r.CompilationOutcome != CompilationOK && r.CompilationOutcome != CompilationFail:
			return invalid("%s: invalid compilation outcome %q", key, r.CompilationOutcome)
		case r.CompilationOutcome == CompilationFail && len(r.Executables) > 0:
			return invalid("%s: a failed compilation has no executables", key)
		case r.Outcome != "" || r.UserOutput != "":
			return invalid("%s: a compilation has no evaluation outcome or output", key)
		}
		return nil
	}
	switch {
	case r.CompilationOutcome != "":
		return invalid("%s: an evaluation has no compilation outcome", key)
	case len(r.Executables) > 0:
		return invalid("%s: an evaluation produces no executables", key)
	case k == Evaluation && r.Outcome == "":
		return invalid("%s: an evaluation needs an outcome", key)
	case k == Evaluation && r.UserOutput != "":
		return invalid("%s: only user tests return their output", key)
	}
	if r.Outcome != "" {
		if _, err := parseOutcome(r.Outcome); err != nil {
			return invalid("%s: %v", key, err)
		}
	}
	return nil
}

func (s *Stats) validate(key string) error {
	nonneg := func(p *float64) bool { return p == nil || !math.IsNaN(*p) && !math.IsInf(*p, 0) && *p >= 0 }
	switch {
	case !nonneg(s.ExecutionTime) || !nonneg(s.ExecutionWallClockTime):
		return invalid("%s: execution times must be finite and not negative", key)
	case s.ExecutionMemory != nil && *s.ExecutionMemory < 0:
		return invalid("%s: negative memory", key)
	case !ValidExitStatus(s.ExitStatus):
		return invalid("%s: unknown exit status %q", key, s.ExitStatus)
	case s.Signal < 0 || s.Signal > 0 && s.ExitStatus != ExitSignal:
		return invalid("%s: signal %d with exit status %q", key, s.Signal, s.ExitStatus)
	case !storable(s.Stdout, MaxOutputLen) || !storable(s.Stderr, MaxOutputLen):
		return invalid("%s: stdout or stderr too long, not UTF-8 or with NUL bytes", key)
	}
	return nil
}

// storable reports whether s fits a text column: valid UTF-8, no NUL
// (PostgreSQL rejects both) and at most max bytes.
func storable(s string, max int) bool {
	return len(s) <= max && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}

// parseOutcome parses an outcome; it must be a finite number.
func parseOutcome(s string) (float64, error) {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, invalid("the outcome %q is not a finite number", s)
	}
	return v, nil
}

// Encode validates the result and returns its JSON with the current
// Version.
func (r *Result) Encode() ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	c := *r
	c.V = Version
	return json.Marshal(&c)
}

// DecodeResult parses and validates a result. Unknown fields are ignored;
// another version is ErrVersion.
func DecodeResult(b []byte) (*Result, error) {
	var r Result
	if err := decode(b, &r); err != nil {
		return nil, err
	}
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return &r, nil
}

// Ptr returns a pointer to v (to fill Stats).
func Ptr[T any](v T) *T { return &v }
