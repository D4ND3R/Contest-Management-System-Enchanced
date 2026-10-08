// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// The priorities and the maximum numbers of tries are those of
// cms/service/esoperations.py of CMS:
// Copyright © 2010-2014 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2016 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2013-2015 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2013 Bernard Blackham <bernard@largestprime.net>
// Copyright © 2014 Artem Iglikov <artem.iglikov@gmail.com>

package judging

import (
	"fmt"
	"strconv"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
)

// Maximum numbers of failed attempts of an operation before the
// dispatcher gives up (upstream MAX_*_TRIES).
const (
	MaxCompilationTries         = 3
	MaxEvaluationTries          = 3
	MaxUserTestCompilationTries = 3
	MaxUserTestEvaluationTries  = 3
)

// The priorities of upstream esoperations (PriorityQueue.PRIORITY_*), as
// priorities of job.JobQueue: 0 is served first.
const (
	// PriorityHigh: the first compilation of a submission or user test on
	// the active dataset.
	PriorityHigh = job.PriorityHigh
	// PriorityMedium: a compilation that failed before, and the first
	// round of evaluations, on the active dataset.
	PriorityMedium = job.PriorityMedium
	// PriorityLow: evaluations that failed before, on the active dataset.
	PriorityLow = job.PriorityLow
	// PriorityExtraLow: everything on a dataset that is not the active one
	// (autojudge datasets).
	PriorityExtraLow = job.PriorityExtraLow
)

// Operation is work for a worker (upstream ESOperation with its priority
// and timestamp), with what the dispatcher needs to enqueue it.
type Operation struct {
	// Type, ObjectID (submission or user test), DatasetID,
	// TestcaseCodename (evaluations of submissions) and ArchiveSandbox.
	job.Operation
	// Priority in job.JobQueue (Priority* constants).
	Priority int
	// Timestamp of the submission or user test: upstream orders the
	// operations of a priority by it.
	Timestamp time.Time
	// Generation of the result row when the operation was computed (0 if
	// there was no row); BuildJobs uses the current one.
	Generation int64
	// Attempt is the attempt the job will be: the tries of the result
	// plus one.
	Attempt int
	// Retry: the operation failed before (Attempt > 1). The dispatcher
	// should enqueue it with a backoff delay (queue.Queue.Backoff).
	Retry bool
}

// DedupKey is the queue deduplication key of the operation, the same as
// job.Job.DedupKey of its job: "evaluate/12/3/tc01@5". While a job with
// this key is queued or running, enqueueing the operation again adds
// nothing; an invalidation changes the generation and so the key.
func (o Operation) DedupKey() string {
	return o.Key() + "@" + strconv.FormatInt(o.Generation, 10)
}

// priority returns the priority of an operation as upstream
// submission_get_operations and user_test_get_operations: tries is the
// compilation tries for a compilation (or 0 when there is no result), the
// evaluation tries for an evaluation.
func priority(compilation, active bool, tries int32) int {
	switch {
	case !active:
		return PriorityExtraLow
	case compilation && tries == 0:
		return PriorityHigh
	case compilation, tries == 0:
		return PriorityMedium
	}
	return PriorityLow
}

// ResultKey identifies a submission result (or a user test result: the
// object is then the user test).
type ResultKey struct {
	SubmissionID int32
	DatasetID    int32
}

func (k ResultKey) String() string { return fmt.Sprintf("%d(%d)", k.SubmissionID, k.DatasetID) }

// UserTestKey identifies a user test result.
type UserTestKey struct {
	UserTestID int32
	DatasetID  int32
}

// TaskKey identifies a row of the task score cache: a participant on a
// task.
type TaskKey struct {
	ParticipationID int32
	TaskID          int32
}

// GaveUp is a result whose operation reached its maximum number of tries:
// it is not scheduled again until an admin reevaluates it.
type GaveUp struct {
	Type      job.Kind
	ObjectID  int32
	DatasetID int32
	Tries     int32
}

// Status is the state of a submission result, as upstream
// SubmissionResult.get_status (the values of store.SubmissionStatus).
type Status string

// The statuses. A result that gave up stays StatusCompiling or
// StatusEvaluating, as in CMS.
const (
	StatusCompiling         Status = "compiling"
	StatusCompilationFailed Status = "compilation_failed"
	StatusEvaluating        Status = "evaluating"
	StatusScoring           Status = "scoring"
	StatusScored            Status = "scored"
)

// statusOf is upstream get_status.
func statusOf(compilationOutcome, evaluationOutcome *string, scored bool) Status {
	switch {
	case compilationOutcome == nil:
		return StatusCompiling
	case *compilationOutcome != job.CompilationOK:
		return StatusCompilationFailed
	case evaluationOutcome == nil:
		return StatusEvaluating
	case !scored:
		return StatusScoring
	}
	return StatusScored
}

// UserTestStatus is the state of a user test result (upstream
// UserTestResult.get_status).
type UserTestStatus string

// The statuses of user tests.
const (
	UserTestCompiling         UserTestStatus = "compiling"
	UserTestCompilationFailed UserTestStatus = "compilation_failed"
	UserTestEvaluating        UserTestStatus = "evaluating"
	UserTestEvaluated         UserTestStatus = "evaluated"
)

func userTestStatusOf(compilationOutcome, evaluationOutcome *string) UserTestStatus {
	switch {
	case compilationOutcome == nil:
		return UserTestCompiling
	case *compilationOutcome != job.CompilationOK:
		return UserTestCompilationFailed
	case evaluationOutcome == nil:
		return UserTestEvaluating
	}
	return UserTestEvaluated
}
