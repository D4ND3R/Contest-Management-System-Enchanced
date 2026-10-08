// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/grading/tasktypes/__init__.py and abc.py of CMS:
// Copyright © 2010-2012 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2012-2014 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2013 Bernard Blackham <bernard@largestprime.net>

// Package tasktype compiles and evaluates submissions and user tests the
// way each task type of CMS does (cms/grading/tasktypes): Batch,
// BatchAndOutput, OutputOnly, Communication, Interactive and TwoSteps,
// with the same parameters (the JSON list stored in
// datasets.task_type_parameters), submission formats, user managers,
// executable names, outcomes, texts and statistics.
//
// A worker runs a job with Execute, which picks the task type of the job
// (Get), compiles or evaluates in sandbox boxes and fills the result. The
// admin site and the loaders use the descriptions: Params, ParseForm,
// and the methods of TaskType (user managers, managers the dataset needs,
// compilation commands shown to contestants).
//
// Errors are never verdicts. A job fails (the dispatcher retries it) when
// the sandbox fails, a file cannot be fetched or stored, an admins'
// program (checker, manager) fails or breaks its protocol, or the dataset
// does not fit the task type (ErrConfiguration: a missing manager, the
// wrong number of files); everything the contestant's program does is a
// result.
package tasktype

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/steps"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/sandbox"
)

// Names of the task types (datasets.task_type, upstream class names).
const (
	NameBatch          = "Batch"
	NameBatchAndOutput = "BatchAndOutput"
	NameOutputOnly     = "OutputOnly"
	NameCommunication  = "Communication"
	NameInteractive    = "Interactive"
	NameTwoSteps       = "TwoSteps"
)

// Names returns the names of the task types, in alphabetical order (how
// CMS lists its plugins).
func Names() []string {
	return []string{NameBatch, NameBatchAndOutput, NameCommunication, NameInteractive, NameOutputOnly, NameTwoSteps}
}

// Valid reports whether name is a task type.
func Valid(name string) bool {
	_, ok := registry[name]
	return ok
}

// Errors returned (wrapped) by this package.
var (
	// ErrUnknown: no task type has that name.
	ErrUnknown = errors.New("tasktype: unknown task type")
	// ErrParameters: the parameters do not match the task type.
	ErrParameters = errors.New("tasktype: invalid parameters")
	// ErrConfiguration: the dataset or the submission does not fit the
	// task type (a missing manager, the wrong number of files or
	// executables, invalid parameters), or a manager or controller broke
	// its protocol. The admins must fix it; upstream
	// set_configuration_error. (A failing checker is reported by the
	// checker package, not wrapped in it.)
	ErrConfiguration = errors.New("tasktype: configuration error")
	// ErrTombstone: the job needs a file whose content is gone
	// (job.TombstoneDigest). Execute sets Result.Tombstone.
	ErrTombstone = errors.New("tasktype: the content of a file is gone (tombstone)")
)

// TaskType is a task type with its parameters. Implementations are
// immutable and safe for concurrent use.
type TaskType interface {
	// Name is the name stored in datasets.task_type ("Batch").
	Name() string
	// DisplayName is the name shown to contestants (upstream
	// TaskType.name: "Batch", "Output only", "Two steps"...).
	DisplayName() string
	// Testable reports whether user tests are possible (if the contest
	// allows them).
	Testable() bool
	// AllowPartialSubmission: a submission may have only some of the
	// files of the submission format; ReusePreviousSubmission: the
	// missing ones are taken from the previous submission.
	AllowPartialSubmission() bool
	ReusePreviousSubmission() bool
	// CompilationCommands returns, by language name, the commands that
	// compile a submission with this submission format, as shown to
	// contestants (upstream get_compilation_commands, with -DEVAL like
	// upstream); nil when there is nothing to compile.
	CompilationCommands(langs *language.Registry, submissionFormat []string) map[string][][]string
	// UserManagers are the managers a contestant provides with a user
	// test ("grader.%l": %l stands for the source extension of the
	// language).
	UserManagers() []string
	// AutoManagers are the managers of the dataset added to the jobs of
	// user tests.
	AutoManagers() []string
	// Managers are the managers the dataset must have ("checker",
	// "manager", "grader.%l": one per allowed language).
	Managers() []string
	// Boxes is the largest number of sandbox boxes a job holds at the
	// same time. A worker running jobs in parallel must reserve them
	// (box_count) or jobs can starve each other.
	Boxes() int
	// Compile and Evaluate run a job of the right kind (Execute picks
	// one) and fill res. An error means the job failed (res is then
	// left for Execute to mark).
	Compile(ctx context.Context, env *Env, j *job.Job, res *job.Result) error
	Evaluate(ctx context.Context, env *Env, j *job.Job, res *job.Result) error
}

type entry struct {
	params func() []Param
	build  func(values []json.RawMessage) (TaskType, error)
}

var registry = map[string]entry{
	NameBatch:          {batchParams, newBatch},
	NameBatchAndOutput: {batchAndOutputParams, newBatchAndOutput},
	NameOutputOnly:     {outputOnlyParams, newOutputOnly},
	NameCommunication:  {communicationParams, newCommunication},
	NameInteractive:    {interactiveParams, newInteractive},
	NameTwoSteps:       {twoStepsParams, newTwoSteps},
}

// Get returns the task type name with the JSON parameters of a dataset
// (upstream get_task_type). The errors wrap ErrUnknown or ErrParameters.
func Get(name string, params json.RawMessage) (TaskType, error) {
	e, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("%w %q", ErrUnknown, name)
	}
	values, err := checkParameters(name, e.params(), params)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrParameters, err)
	}
	t, err := e.build(values)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrParameters, err)
	}
	return t, nil
}

// Params describes the parameters of a task type (upstream
// ACCEPTED_PARAMETERS), for the admin forms.
func Params(name string) ([]Param, error) {
	e, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("%w %q", ErrUnknown, name)
	}
	return e.params(), nil
}

// Env is what the task types need from the worker.
type Env struct {
	// Boxes creates sandbox boxes (SandboxBoxes in a worker).
	Boxes Boxes
	// Store holds every file: inputs, expected outputs, managers,
	// submitted files and executables are read from it; executables and
	// the outputs of user tests are written to it.
	Store blob.Store
	// Languages are the programming languages.
	Languages *language.Registry
	// Steps runs the grading steps with the compilation and trusted
	// limits of the configuration.
	Steps *steps.Config
	// Log receives the messages of the task types (nil: none).
	Log *slog.Logger
}

func (e *Env) log() *slog.Logger {
	if e.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return e.Log
}

// Box is a sandbox box: a *sandbox.Box, or a fake in tests. Task types
// that run programs at the same time (Communication, Interactive,
// TwoSteps) need a *sandbox.Box.
type Box interface {
	steps.Box
	// Dir is the directory of the box on the host (reported in
	// Result.Sandboxes).
	Dir() string
	// Close releases the box; it is idempotent.
	Close() error
}

var _ Box = (*sandbox.Box)(nil)

// Boxes creates boxes; name only helps debugging.
type Boxes interface {
	NewBox(ctx context.Context, name string) (Box, error)
}

// SandboxBoxes creates the boxes of a sandbox.
func SandboxBoxes(s *sandbox.Sandbox) Boxes { return sandboxBoxes{s} }

type sandboxBoxes struct{ s *sandbox.Sandbox }

func (b sandboxBoxes) NewBox(ctx context.Context, name string) (Box, error) {
	box, err := b.s.NewBox(ctx, name)
	if err != nil {
		return nil, err
	}
	return box, nil
}
