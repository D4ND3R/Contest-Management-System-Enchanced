// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// The fields of Job and Result and their documentation follow
// cms/grading/Job.py of CMS:
// Copyright © 2012 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2013-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2013 Bernard Blackham <bernard@largestprime.net>
// Copyright © 2013-2018 Stefano Maggiolo <s.maggiolo@gmail.com>

// Package job is the protocol between the dispatcher and the workers: what
// to compile or evaluate (Job) and what came out of it (Result), encoded as
// JSON payloads of the job and result queues (internal/queue, D1).
//
// The fields port cms/grading/Job.py (CompilationJob, EvaluationJob) and
// cms/service/esoperations.py (ESOperation). Fields with the same meaning
// keep the JSON names of upstream's export_to_dict (task_type, files,
// managers, executables, input, output, time_limit, memory_limit, success,
// text, admin_text, outcome, user_output, plus, sandboxes...), so anyone
// who knows CMS can read a payload. The differences:
//
//   - A job carries only its input and a result only its output (upstream
//     sends the same object both ways), plus what this fork needs: a
//     protocol version, a job id, the attempt number and the generation of
//     the result row the job was created for (stale results, D23).
//   - Compilation outcomes are "ok"/"fail" (the value of the
//     compilation_outcome column) instead of compilation_success.
//   - Workers are identified by name, not by shard number.
//
// Workers never touch the database (D12): every file travels as a blob
// digest (internal/blob), and the dispatcher stores results idempotently
// (D21), since queues deliver at least once.
package job

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"unicode/utf8"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
)

// Version is the protocol version written by Encode. Decoders reject other
// versions (ErrVersion); fields added in a compatible way do not change it,
// since decoders ignore unknown fields.
const Version = 1

// Limits checked by Validate. Workers must keep results within them (see
// TruncateUTF8): the dispatcher rejects a result that exceeds them.
const (
	// MaxTimeLimit is the largest time limit of an evaluation, in seconds.
	MaxTimeLimit = 3600.0
	// MinMemoryLimit and MaxMemoryLimit bound the memory limit (bytes);
	// datasets store whole MiB, so anything smaller is a unit mistake.
	MinMemoryLimit = 1 << 20
	MaxMemoryLimit = 1 << 40
	// MaxFiles bounds each file map of a job or result.
	MaxFiles = 1000
	// MaxNameLen bounds file names, task type and language names, ids.
	MaxNameLen = 255
	// MaxParametersLen bounds the task type parameters (JSON).
	MaxParametersLen = 64 << 10
	// MaxTextLen bounds the text of a result (all its items) and its admin
	// text; MaxOutputLen bounds each of stdout and stderr.
	MaxTextLen   = 64 << 10
	MaxOutputLen = 1 << 20
)

// Errors returned (wrapped) by Validate and the decoders.
var (
	// ErrInvalid: a job or result breaks the protocol.
	ErrInvalid = errors.New("job: invalid")
	// ErrVersion: the payload was written by another protocol version (a
	// worker and a dispatcher of different releases).
	ErrVersion = errors.New("job: unsupported protocol version")
)

// Kind is the type of an operation. The values are those of upstream
// ESOperation (COMPILATION, EVALUATION, USER_TEST_COMPILATION,
// USER_TEST_EVALUATION).
type Kind string

// The operation kinds.
const (
	Compilation         Kind = "compile"
	Evaluation          Kind = "evaluate"
	UserTestCompilation Kind = "compile_test"
	UserTestEvaluation  Kind = "evaluate_test"
)

// Valid reports whether k is one of the four kinds.
func (k Kind) Valid() bool {
	switch k {
	case Compilation, Evaluation, UserTestCompilation, UserTestEvaluation:
		return true
	}
	return false
}

// ForSubmission reports whether k works on a submission (not a user test),
// like ESOperation.for_submission.
func (k Kind) ForSubmission() bool { return k == Compilation || k == Evaluation }

// IsCompilation reports whether k compiles (a submission or a user test).
func (k Kind) IsCompilation() bool { return k == Compilation || k == UserTestCompilation }

// IsEvaluation reports whether k evaluates (a submission or a user test).
func (k Kind) IsEvaluation() bool { return k == Evaluation || k == UserTestEvaluation }

// Operation identifies the work, like upstream ESOperation (same JSON as
// ESOperation.to_dict). It is comparable, so it can key a map.
type Operation struct {
	Type Kind `json:"type"`
	// ObjectID is the submission id or the user test id.
	ObjectID  int32 `json:"object_id"`
	DatasetID int32 `json:"dataset_id"`
	// TestcaseCodename is set for Evaluation only.
	TestcaseCodename string `json:"testcase_codename,omitempty"`
	// ArchiveSandbox asks the worker to archive the sandboxes it used.
	ArchiveSandbox bool `json:"archive_sandbox"`
}

// Key identifies the operation independently of sandbox archiving:
// "compile/12/3", "evaluate/12/3/tc01", "compile_test/7/3"...
func (o Operation) Key() string {
	k := string(o.Type) + "/" + strconv.FormatInt(int64(o.ObjectID), 10) + "/" +
		strconv.FormatInt(int64(o.DatasetID), 10)
	if o.TestcaseCodename != "" {
		k += "/" + o.TestcaseCodename
	}
	return k
}

// String describes the operation as upstream Job.info does ("evaluate
// submission 12 on testcase tc01"), plus the dataset.
func (o Operation) String() string {
	switch o.Type {
	case Compilation:
		return fmt.Sprintf("compile submission %d (dataset %d)", o.ObjectID, o.DatasetID)
	case Evaluation:
		return fmt.Sprintf("evaluate submission %d on testcase %s (dataset %d)", o.ObjectID, o.TestcaseCodename, o.DatasetID)
	case UserTestCompilation:
		return fmt.Sprintf("compile user test %d (dataset %d)", o.ObjectID, o.DatasetID)
	case UserTestEvaluation:
		return fmt.Sprintf("evaluate user test %d (dataset %d)", o.ObjectID, o.DatasetID)
	}
	return fmt.Sprintf("%q on %d (dataset %d)", o.Type, o.ObjectID, o.DatasetID)
}

func (o Operation) validate() error {
	if !o.Type.Valid() {
		return invalid("unknown operation type %q", o.Type)
	}
	if o.ObjectID <= 0 || o.DatasetID <= 0 {
		return invalid("%s: object and dataset ids must be positive", o.Key())
	}
	if o.Type == Evaluation {
		if !validCodename(o.TestcaseCodename) {
			return invalid("%s: invalid testcase codename %q", o.Key(), o.TestcaseCodename)
		}
	} else if o.TestcaseCodename != "" {
		return invalid("%s: only evaluations of submissions have a testcase", o.Key())
	}
	return nil
}

// Job is the input of a compilation or an evaluation (upstream
// CompilationJob / EvaluationJob without their outputs).
type Job struct {
	// V is the protocol version; Encode sets it.
	V int `json:"v"`
	// ID identifies the job in logs and results. The dispatcher uses
	// DedupKey (one id per operation and generation).
	ID        string    `json:"id"`
	Operation Operation `json:"operation"`
	// Attempt is 1 for the first try. The queue message is authoritative:
	// a worker sets Attempt from queue.Message.Attempt after decoding, since
	// a queue retry re-delivers the same payload.
	Attempt int `json:"attempt"`
	// Generation is the generation of the result row (submission or user
	// test result) when the job was created. Invalidating the row bumps
	// it, so the dispatcher can discard results of older jobs (D23).
	Generation int64 `json:"generation"`

	// TaskType names the task type ("Batch", "Communication"...) and
	// TaskTypeParameters are its parameters, as stored in the dataset.
	TaskType           string          `json:"task_type"`
	TaskTypeParameters json.RawMessage `json:"task_type_parameters"`
	// Language is the language name of the submission or user test ("" if
	// it has none, as in OutputOnly tasks).
	Language string `json:"language,omitempty"`
	// MultithreadedSandbox: a language of the contest needs threads.
	MultithreadedSandbox bool `json:"multithreaded_sandbox,omitempty"`
	// KeepSandbox keeps the sandboxes of this job whatever the
	// configuration says.
	KeepSandbox bool `json:"keep_sandbox,omitempty"`

	// Files are the files of the submission or user test, by codename
	// ("sum.%l" for sources) -> digest.
	Files map[string]string `json:"files"`
	// Managers are the files provided by the admins (graders, checkers,
	// managers, headers), by file name -> digest.
	Managers map[string]string `json:"managers"`
	// Executables are the compiled files (evaluations only).
	Executables map[string]string `json:"executables"`

	// Input and Output are the digests of the testcase input and expected
	// output (evaluations; user tests have no output).
	Input  string `json:"input,omitempty"`
	Output string `json:"output,omitempty"`
	// TimeLimit is the CPU time limit in seconds and MemoryLimit the
	// memory limit in bytes, of the evaluation; 0 means none.
	TimeLimit   float64 `json:"time_limit,omitempty"`
	MemoryLimit int64   `json:"memory_limit,omitempty"`
	// OnlyExecution skips the comparison with the expected output and
	// GetOutput asks for the output of the program (both set for user
	// tests).
	OnlyExecution bool `json:"only_execution,omitempty"`
	GetOutput     bool `json:"get_output,omitempty"`

	// Info is a human-readable description (Operation.String if empty).
	Info string `json:"info,omitempty"`
}

// DedupKey is the queue deduplication key of the job: its operation and
// generation ("evaluate/12/3/tc01@5"). While a job with this key is queued
// or running, enqueueing the same operation again adds nothing; after an
// invalidation the generation changes and so does the key.
func (j *Job) DedupKey() string {
	return j.Operation.Key() + "@" + strconv.FormatInt(j.Generation, 10)
}

// Describe returns Info, or a description of the operation.
func (j *Job) Describe() string {
	if j.Info != "" {
		return j.Info
	}
	return j.Operation.String()
}

// Validate checks that the job is well formed: known kind, the fields the
// kind needs, valid file names and digests, sane limits. V is not checked
// (Encode sets it, DecodeJob checks it).
func (j *Job) Validate() error {
	if err := j.Operation.validate(); err != nil {
		return err
	}
	k, key := j.Operation.Type, j.Operation.Key()
	switch {
	case !validID(j.ID):
		return invalid("%s: invalid job id %q", key, j.ID)
	case j.Attempt < 1:
		return invalid("%s: attempt %d must be at least 1", key, j.Attempt)
	case j.Generation < 0:
		return invalid("%s: negative generation", key)
	case !validTaskType(j.TaskType):
		return invalid("%s: invalid task type %q", key, j.TaskType)
	case len(j.TaskTypeParameters) == 0 || len(j.TaskTypeParameters) > MaxParametersLen || !json.Valid(j.TaskTypeParameters):
		return invalid("%s: the task type parameters must be JSON of at most %d bytes", key, MaxParametersLen)
	case len(j.Language) > MaxNameLen || !printable(j.Language):
		return invalid("%s: invalid language name %q", key, j.Language)
	case len(j.Info) > MaxTextLen || !utf8.ValidString(j.Info):
		return invalid("%s: invalid info", key)
	}
	if err := checkFiles(key, "files", j.Files, true, true); err != nil {
		return err
	}
	if err := checkFiles(key, "managers", j.Managers, false, true); err != nil {
		return err
	}
	if err := checkFiles(key, "executables", j.Executables, false, true); err != nil {
		return err
	}
	if err := checkLimits(key, j.TimeLimit, j.MemoryLimit); err != nil {
		return err
	}

	if k.IsCompilation() {
		if len(j.Executables) > 0 {
			return invalid("%s: a compilation receives no executables", key)
		}
		if j.Input != "" || j.Output != "" || j.OnlyExecution || j.GetOutput {
			return invalid("%s: a compilation has no input, output or execution flags", key)
		}
		return nil
	}
	if !validDigest(j.Input) {
		return invalid("%s: invalid input digest %q", key, j.Input)
	}
	switch {
	case k == UserTestEvaluation && j.Output != "":
		return invalid("%s: a user test has no expected output", key)
	case k == UserTestEvaluation && !j.OnlyExecution:
		return invalid("%s: a user test is only executed", key)
	case k == Evaluation && !j.OnlyExecution && !validDigest(j.Output):
		return invalid("%s: invalid output digest %q", key, j.Output)
	case k == Evaluation && j.OnlyExecution && j.Output != "" && !validDigest(j.Output):
		return invalid("%s: invalid output digest %q", key, j.Output)
	}
	return nil
}

// Encode validates the job and returns its JSON with the current Version.
func (j *Job) Encode() ([]byte, error) {
	if err := j.Validate(); err != nil {
		return nil, err
	}
	c := *j
	c.V = Version
	return json.Marshal(&c)
}

// DecodeJob parses and validates a job. Unknown fields are ignored (added
// by a newer release in a compatible way); another version is ErrVersion.
func DecodeJob(b []byte) (*Job, error) {
	var j Job
	if err := decode(b, &j); err != nil {
		return nil, err
	}
	if err := j.Validate(); err != nil {
		return nil, err
	}
	return &j, nil
}

// decode unmarshals b into v after checking its version.
func decode(b []byte, v any) error {
	var head struct {
		V *int `json:"v"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if head.V == nil {
		return fmt.Errorf("%w: no version", ErrVersion)
	}
	if *head.V != Version {
		return fmt.Errorf("%w: %d (this release speaks %d)", ErrVersion, *head.V, Version)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return nil
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalid}, args...)...)
}

// TombstoneDigest stands for a file whose content is gone (CMS
// Digest.TOMBSTONE: dumps exported without files). A worker that needs it
// reports a failed result with Tombstone set.
const TombstoneDigest = "x"

// validDigest accepts blob digests and the tombstone.
func validDigest(d string) bool { return d == TombstoneDigest || blob.ValidDigest(d) }

// realDigest accepts blob digests only (files a worker produced).
func realDigest(d string) bool { return blob.ValidDigest(d) }

// checkFiles validates a file map: names as in the database (codenames
// may end in ".%l" when codenames is set) and digests.
func checkFiles(key, what string, m map[string]string, codenames, tombstone bool) error {
	if len(m) > MaxFiles {
		return invalid("%s: more than %d %s", key, MaxFiles, what)
	}
	for name, d := range m {
		if !validFilename(name, codenames) {
			return invalid("%s: invalid name in %s: %q", key, what, name)
		}
		if ok := realDigest(d) || tombstone && d == TombstoneDigest; !ok {
			return invalid("%s: invalid digest of %s %q: %q", key, what, name, d)
		}
	}
	return nil
}

func checkLimits(key string, t float64, m int64) error {
	if math.IsNaN(t) || t < 0 || t > MaxTimeLimit {
		return invalid("%s: the time limit %v is not in [0, %v] seconds", key, t, MaxTimeLimit)
	}
	if m != 0 && (m < MinMemoryLimit || m > MaxMemoryLimit) {
		return invalid("%s: the memory limit %d is not in [%d, %d] bytes", key, m, int64(MinMemoryLimit), int64(MaxMemoryLimit))
	}
	return nil
}

// validFilename accepts the file names of the database: [A-Za-z0-9_.-]+,
// not "." or "..", and, for codenames, an optional ".%l" suffix.
func validFilename(s string, codename bool) bool {
	if codename && len(s) > 3 && s[len(s)-3:] == ".%l" {
		s = s[:len(s)-3]
	}
	if s == "" || len(s) > MaxNameLen || s == "." || s == ".." {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !alnum(c) && c != '_' && c != '.' && c != '-' {
			return false
		}
	}
	return true
}

// validCodename accepts testcase codenames: [A-Za-z0-9_-]+.
func validCodename(s string) bool {
	if s == "" || len(s) > MaxNameLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; !alnum(c) && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

// validTaskType accepts [A-Za-z0-9_]+.
func validTaskType(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; !alnum(c) && c != '_' {
			return false
		}
	}
	return true
}

// validID accepts printable ASCII without spaces.
func validID(s string) bool {
	if s == "" || len(s) > MaxNameLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; c <= ' ' || c >= 0x7f {
			return false
		}
	}
	return true
}

// printable accepts valid UTF-8 without control characters.
func printable(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < ' ' || r == 0x7f || r >= 0x80 && r < 0xa0 {
			return false
		}
	}
	return true
}

func alnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
