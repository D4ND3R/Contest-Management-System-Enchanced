// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from write_results, write_results_one_row, compilation_ended,
// evaluation_ended, user_test_compilation_ended and
// user_test_evaluation_ended in cms/service/EvaluationService.py of CMS:
// Copyright © 2010-2014 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2013-2015 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2013 Bernard Blackham <bernard@largestprime.net>
// Copyright © 2014 Artem Iglikov <artem.iglikov@gmail.com>
// Copyright © 2016 Luca Versari <veluca93@gmail.com>

package judging

import (
	"context"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// Changes is what Apply, Score and Invalidate changed.
type Changes struct {
	// Next are the operations to enqueue now: evaluations of what got
	// compiled, retries of failed jobs (Retry set), recompilations.
	Next []Operation
	// Submissions are the results whose status changed, with their score
	// once scored (live updates; the scoreboard wants those that are
	// Active and Official).
	Submissions []SubmissionChange
	// UserTests are the user test results whose status changed.
	UserTests []UserTestChange
	// TaskScores are the cached task scores that changed.
	TaskScores []TaskScoreChange
	// Dropped are the results that changed nothing.
	Dropped []Dropped
	// GaveUp are the results that reached their maximum number of tries
	// with this batch.
	GaveUp []GaveUp
	// ScoreErrors are the results that could not be scored (invalid
	// score type parameters, an evaluation without outcome): they stay
	// unscored until the dataset is fixed and they are rescored.
	ScoreErrors []ScoreError
}

// SubmissionChange is a submission result whose status changed.
type SubmissionChange struct {
	SubmissionID    int32
	DatasetID       int32
	ParticipationID int32
	TaskID          int32
	// ContestID is the contest of the task (0: none).
	ContestID int32
	Timestamp time.Time
	Official  bool
	// Active: the dataset is the active dataset of the task.
	Active bool
	Status Status
	// Scored: the result has a score (StatusScored, or
	// StatusCompilationFailed once scored 0); Score, PublicScore and
	// RankingScoreDetails are set then. A result is reported when its
	// status or its score changed.
	Scored              bool
	Score               float64
	PublicScore         float64
	RankingScoreDetails []string
}

// UserTestChange is a user test result whose status changed.
type UserTestChange struct {
	UserTestID      int32
	DatasetID       int32
	ParticipationID int32
	TaskID          int32
	ContestID       int32
	Active          bool
	Status          UserTestStatus
}

// Dropped is a result that changed nothing.
type Dropped struct {
	Operation  job.Operation
	Generation int64
	Attempt    int
	Reason     DropReason
	// Err explains DropInvalid.
	Err error
}

// DropReason says why a result changed nothing.
type DropReason string

// The reasons.
const (
	// DropStale: the result was invalidated while the job was in flight
	// (its generation changed); a new job does the work again.
	DropStale DropReason = "stale"
	// DropDuplicate: the result is already stored (a job delivered twice),
	// or a failure was already counted.
	DropDuplicate DropReason = "duplicate"
	// DropGone: the submission, user test or dataset is gone.
	DropGone DropReason = "gone"
	// DropInvalid: the result breaks the protocol (job.Result.Validate).
	DropInvalid DropReason = "invalid"
)

// ScoreError is a result that could not be scored.
type ScoreError struct {
	ResultKey
	Err error
}

// Apply stores a batch of worker results in one transaction (upstream
// write_results and the *_ended callbacks): compilations with their
// executables, evaluations, user tests, failed attempts. Then it ends the
// evaluations that have every testcase, scores what is ready, updates the
// task score cache and returns the next operations. It is idempotent and
// checks generations: a repeated or stale result is Dropped. The number of
// queries does not depend on the size of the batch.
func (j *Judge) Apply(ctx context.Context, results []*job.Result) (*Changes, error) {
	var ch *Changes
	err := j.inTx(ctx, func(_ pgx.Tx, qq *q.Queries) error {
		ch = &Changes{}
		a := &applier{j: j, qq: qq, ch: ch, now: j.now()}
		var subs, uts []*job.Result
		for _, r := range results {
			if r == nil {
				continue
			}
			if err := r.Validate(); err != nil {
				ch.Dropped = append(ch.Dropped, Dropped{Operation: r.Operation, Generation: r.Generation,
					Attempt: r.Attempt, Reason: DropInvalid, Err: err})
				continue
			}
			if r.Operation.Type.ForSubmission() {
				subs = append(subs, r)
			} else {
				uts = append(uts, r)
			}
		}
		if len(subs) > 0 {
			if err := a.submissions(ctx, subs); err != nil {
				return err
			}
		}
		if len(uts) > 0 {
			if err := a.userTests(ctx, uts); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	j.logChanges(ch)
	return ch, nil
}

// applier holds the state of one Apply or Score transaction.
type applier struct {
	j   *Judge
	qq  *q.Queries
	ch  *Changes
	now time.Time
}

// subState is a locked submission result and what this transaction did
// to it.
type subState struct {
	q.JudgingLockSubmissionResultsRow
	before Status
	// What happened in this transaction.
	compiled      bool            // a compilation outcome was stored
	compileFailed bool            // a failed compilation attempt arrived
	evaluated     map[string]bool // evaluations stored
	evalFailed    map[string]bool // testcases whose evaluation failed
	recompile     bool            // invalidated: the executable is the tombstone
	finished      bool            // the evaluation outcome was set
	scoreChanged  bool            // scored (or unscored by a recompilation)
	score         float64
	publicScore   float64
	ranking       []string
}

func (s *subState) key() ResultKey { return ResultKey{s.SubmissionID, s.DatasetID} }

func (s *subState) status() Status {
	return statusOf(s.CompilationOutcome, s.EvaluationOutcome, s.Scored)
}

func compiledOK(outcome *string) bool { return outcome != nil && *outcome == job.CompilationOK }

// lockSubmissions locks the result rows of keys and returns their state.
func (a *applier) lockSubmissions(ctx context.Context, keys []ResultKey) (map[ResultKey]*subState, []*subState, error) {
	slices.SortFunc(keys, func(x, y ResultKey) int {
		if x.SubmissionID != y.SubmissionID {
			return int(x.SubmissionID - y.SubmissionID)
		}
		return int(x.DatasetID - y.DatasetID)
	})
	keys = slices.Compact(keys)
	var p q.JudgingLockSubmissionResultsParams
	for _, k := range keys {
		p.SubmissionIds = append(p.SubmissionIds, k.SubmissionID)
		p.DatasetIds = append(p.DatasetIds, k.DatasetID)
	}
	rows, err := a.qq.JudgingLockSubmissionResults(ctx, p)
	if err != nil {
		return nil, nil, err
	}
	states := make(map[ResultKey]*subState, len(rows))
	list := make([]*subState, len(rows))
	for i := range rows {
		st := &subState{JudgingLockSubmissionResultsRow: rows[i]}
		st.before = st.status()
		states[st.key()] = st
		list[i] = st
	}
	return states, list, nil
}

func (a *applier) drop(r *job.Result, reason DropReason) {
	a.ch.Dropped = append(a.ch.Dropped, Dropped{Operation: r.Operation, Generation: r.Generation,
		Attempt: r.Attempt, Reason: reason})
}

// statsOf returns the statistics of a result as nullable columns.
func statsOf(r *job.Result) (t, wall *float64, mem *int64, stdout, stderr *string) {
	if r.Stats == nil {
		return nil, nil, nil, nil, nil
	}
	s := r.Stats
	return s.ExecutionTime, s.ExecutionWallClockTime, s.ExecutionMemory, &s.Stdout, &s.Stderr
}

// sandboxColumns returns the sandbox paths of a result and the digests of
// their archives in the same order (upstream get_sandbox_digest_list: none
// unless every sandbox was archived).
func sandboxColumns(r *job.Result) (paths, digests []string) {
	if len(r.Sandboxes) == 0 {
		return nil, nil
	}
	paths = r.Sandboxes
	if len(r.SandboxDigests) == 0 {
		return paths, nil
	}
	digests = make([]string, len(paths))
	for i, p := range paths {
		d, ok := r.SandboxDigests[p]
		if !ok {
			return paths, nil
		}
		digests[i] = d
	}
	return paths, digests
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (a *applier) submissions(ctx context.Context, results []*job.Result) error {
	keys := make([]ResultKey, 0, len(results))
	var tombstoned []ResultKey
	for _, r := range results {
		k := ResultKey{r.Operation.ObjectID, r.Operation.DatasetID}
		keys = append(keys, k)
		if r.Operation.Type == job.Evaluation && r.Tombstone {
			tombstoned = append(tombstoned, k)
		}
	}
	states, list, err := a.lockSubmissions(ctx, keys)
	if err != nil {
		return err
	}
	// Upstream recompiles a result whose evaluation needed a missing file
	// when the executable is the tombstone.
	tombExec := map[ResultKey]bool{}
	if len(tombstoned) > 0 {
		var p q.JudgingSubmissionExecutablesParams
		for _, k := range uniq(tombstoned) {
			p.SubmissionIds = append(p.SubmissionIds, k.SubmissionID)
			p.DatasetIds = append(p.DatasetIds, k.DatasetID)
		}
		rows, err := a.qq.JudgingSubmissionExecutables(ctx, p)
		if err != nil {
			return err
		}
		for _, e := range rows {
			if e.Digest == job.TombstoneDigest {
				tombExec[ResultKey{e.SubmissionID, e.DatasetID}] = true
			}
		}
	}

	var (
		setComp     []q.JudgingSetSubmissionCompilationParams
		setCompRes  []*job.Result
		failComp    []q.JudgingFailSubmissionCompilationParams
		failCompRes []*job.Result
		insEval     []q.JudgingInsertEvaluationParams
		insEvalRes  []*job.Result
		failEval    []q.JudgingFailSubmissionEvaluationParams
		failEvalRes []*job.Result
		executables q.JudgingInsertExecutablesParams
		recompile   q.JudgingInvalidateCompilationsParams
	)
	for _, r := range results {
		k := ResultKey{r.Operation.ObjectID, r.Operation.DatasetID}
		st := states[k]
		switch {
		case st == nil:
			a.drop(r, DropGone)
			continue
		case r.Generation != st.Generation || st.recompile:
			a.drop(r, DropStale)
			continue
		}
		attempt := int32(r.Attempt)
		t, wall, mem, stdout, stderr := statsOf(r)
		paths, digests := sandboxColumns(r)
		switch {
		case r.Operation.Type == job.Compilation && r.Success:
			if st.CompilationOutcome != nil {
				a.drop(r, DropDuplicate)
				continue
			}
			outcome := r.CompilationOutcome
			st.CompilationOutcome, st.compiled = &outcome, true
			st.CompilationTries = max(st.CompilationTries, attempt)
			setComp = append(setComp, q.JudgingSetSubmissionCompilationParams{
				CompilationOutcome: outcome, CompilationText: r.Text, Attempt: attempt,
				CompilationStdout: stdout, CompilationStderr: stderr,
				CompilationTime: t, CompilationWallClockTime: wall, CompilationMemory: mem,
				CompilationSandboxPaths: paths, CompilationSandboxDigests: digests,
				SubmissionID: k.SubmissionID, DatasetID: k.DatasetID, Generation: st.Generation,
			})
			setCompRes = append(setCompRes, r)
			names := make([]string, 0, len(r.Executables))
			for name := range r.Executables {
				names = append(names, name)
			}
			slices.Sort(names)
			for _, name := range names {
				executables.SubmissionIds = append(executables.SubmissionIds, k.SubmissionID)
				executables.DatasetIds = append(executables.DatasetIds, k.DatasetID)
				executables.Filenames = append(executables.Filenames, name)
				executables.Digests = append(executables.Digests, r.Executables[name])
			}
		case r.Operation.Type == job.Compilation:
			if st.CompilationOutcome != nil || st.CompilationTries >= attempt {
				a.drop(r, DropDuplicate)
				continue
			}
			st.CompilationTries, st.compileFailed = attempt, true
			failComp = append(failComp, q.JudgingFailSubmissionCompilationParams{
				Attempt: attempt, SubmissionID: k.SubmissionID, DatasetID: k.DatasetID, Generation: st.Generation,
			})
			failCompRes = append(failCompRes, r)
		case r.Success: // evaluation
			code := r.Operation.TestcaseCodename
			if !compiledOK(st.CompilationOutcome) || st.EvaluationOutcome != nil || st.evaluated[code] {
				a.drop(r, DropDuplicate)
				continue
			}
			if st.evaluated == nil {
				st.evaluated = map[string]bool{}
			}
			st.evaluated[code] = true
			outcome := r.Outcome
			insEval = append(insEval, q.JudgingInsertEvaluationParams{
				Outcome: &outcome, Text: r.Text, AdminText: nonEmpty(r.AdminText),
				ExecutionTime: t, ExecutionWallClockTime: wall, ExecutionMemory: mem,
				EvaluationSandboxPaths: paths, EvaluationSandboxDigests: digests,
				Codename: code, SubmissionID: k.SubmissionID, DatasetID: k.DatasetID, Generation: st.Generation,
			})
			insEvalRes = append(insEvalRes, r)
		default: // failed evaluation
			if !compiledOK(st.CompilationOutcome) || st.EvaluationOutcome != nil {
				a.drop(r, DropDuplicate)
				continue
			}
			if r.Tombstone && tombExec[k] {
				st.recompile = true
				recompile.SubmissionIds = append(recompile.SubmissionIds, k.SubmissionID)
				recompile.DatasetIds = append(recompile.DatasetIds, k.DatasetID)
				continue
			}
			if st.evalFailed == nil {
				st.evalFailed = map[string]bool{}
			}
			st.evalFailed[r.Operation.TestcaseCodename] = true
			if st.EvaluationTries >= attempt {
				// Already counted; the testcase is still retried below
				// (the queue drops the duplicate operation).
				a.drop(r, DropDuplicate)
				continue
			}
			st.EvaluationTries = attempt
			failEval = append(failEval, q.JudgingFailSubmissionEvaluationParams{
				Attempt: attempt, SubmissionID: k.SubmissionID, DatasetID: k.DatasetID, Generation: st.Generation,
			})
			failEvalRes = append(failEvalRes, r)
		}
	}

	// The writes, one round trip each. The rows are locked and were
	// checked above, so a statement that changes nothing is unexpected
	// (a testcase deleted meanwhile): its result counts as a duplicate.
	var be batchErr
	if len(setComp) > 0 {
		a.qq.JudgingSetSubmissionCompilation(ctx, setComp).QueryRow(func(i int, _ int32, err error) {
			if !be.ok(err) {
				a.drop(setCompRes[i], DropDuplicate)
			}
		})
	}
	if be.err == nil && len(executables.SubmissionIds) > 0 {
		be.ok(a.qq.JudgingInsertExecutables(ctx, executables))
	}
	if be.err == nil && len(failComp) > 0 {
		a.qq.JudgingFailSubmissionCompilation(ctx, failComp).QueryRow(func(i int, _ int32, err error) {
			if !be.ok(err) {
				a.drop(failCompRes[i], DropDuplicate)
			}
		})
	}
	if be.err == nil && len(insEval) > 0 {
		a.qq.JudgingInsertEvaluation(ctx, insEval).QueryRow(func(i int, _ int32, err error) {
			if !be.ok(err) {
				a.drop(insEvalRes[i], DropDuplicate)
			}
		})
	}
	if be.err == nil && len(failEval) > 0 {
		a.qq.JudgingFailSubmissionEvaluation(ctx, failEval).QueryRow(func(i int, _ int32, err error) {
			if !be.ok(err) {
				a.drop(failEvalRes[i], DropDuplicate)
			}
		})
	}
	if be.err != nil {
		return be.err
	}
	if len(recompile.SubmissionIds) > 0 {
		rows, err := a.qq.JudgingInvalidateCompilations(ctx, recompile)
		if err != nil {
			return err
		}
		for _, r := range rows {
			st := states[ResultKey{r.SubmissionID, r.DatasetID}]
			a.j.log.Info("compilation invalidated: the executable was the tombstone",
				"submission", r.SubmissionID, "dataset", r.DatasetID)
			st.Generation = r.Generation
			st.CompilationOutcome, st.CompilationTries = nil, 0
			st.EvaluationOutcome, st.EvaluationTries = nil, 0
			st.Scored, st.scoreChanged = false, st.before == StatusScored
		}
	}
	return a.finishSubmissions(ctx, list)
}

// finishSubmissions ends the evaluations that are complete, scores what
// is ready, computes the next operations, refreshes the task scores and
// reports the changes of the locked results.
func (a *applier) finishSubmissions(ctx context.Context, list []*subState) error {
	// Evaluations with every testcase: those that just compiled (a dataset
	// without testcases is complete at once) and those that got an
	// evaluation.
	var fin q.JudgingFinishEvaluationsParams
	for _, st := range list {
		if st.recompile || !compiledOK(st.CompilationOutcome) || st.EvaluationOutcome != nil {
			continue
		}
		if st.compiled || len(st.evaluated) > 0 || st.finishCandidate() {
			fin.SubmissionIds = append(fin.SubmissionIds, st.SubmissionID)
			fin.DatasetIds = append(fin.DatasetIds, st.DatasetID)
			fin.Generations = append(fin.Generations, st.Generation)
		}
	}
	states := make(map[ResultKey]*subState, len(list))
	for _, st := range list {
		states[st.key()] = st
	}
	if len(fin.SubmissionIds) > 0 {
		done, err := a.qq.JudgingFinishEvaluations(ctx, fin)
		if err != nil {
			return err
		}
		ok := job.CompilationOK // evaluation_outcome is always "ok"
		for _, d := range done {
			st := states[ResultKey{d.SubmissionID, d.DatasetID}]
			st.EvaluationOutcome, st.finished = &ok, true
		}
	}

	// Score what is ready: compilation failed or evaluation complete.
	var toScore []*subState
	for _, st := range list {
		ready := st.CompilationOutcome != nil && (!compiledOK(st.CompilationOutcome) || st.EvaluationOutcome != nil)
		if ready && !st.Scored && !st.recompile {
			toScore = append(toScore, st)
		}
	}
	if err := a.score(ctx, toScore); err != nil {
		return err
	}

	// The next operations, on the datasets to judge only.
	var targets []evalTarget
	retry := map[ResultKey]map[string]bool{}
	for _, st := range list {
		k := st.key()
		if !st.ToJudge {
			continue
		}
		switch {
		case st.recompile:
			a.ch.Next = append(a.ch.Next, Operation{
				Operation: job.Operation{Type: job.Compilation, ObjectID: k.SubmissionID, DatasetID: k.DatasetID},
				Priority:  priority(true, st.Active, 0), Timestamp: st.Timestamp,
				Generation: st.Generation, Attempt: 1,
			})
		case st.compileFailed && st.CompilationOutcome == nil:
			if st.CompilationTries >= a.j.tries.Compilation {
				a.giveUp(GaveUp{job.Compilation, k.SubmissionID, k.DatasetID, st.CompilationTries},
					"Maximum number of failures reached for the compilation of a submission")
				continue
			}
			a.ch.Next = append(a.ch.Next, Operation{
				Operation: job.Operation{Type: job.Compilation, ObjectID: k.SubmissionID, DatasetID: k.DatasetID},
				Priority:  priority(true, st.Active, st.CompilationTries), Timestamp: st.Timestamp,
				Generation: st.Generation, Attempt: int(st.CompilationTries) + 1, Retry: true,
			})
		case st.EvaluationOutcome != nil || !compiledOK(st.CompilationOutcome):
		case len(st.evalFailed) > 0 && st.EvaluationTries >= a.j.tries.Evaluation:
			a.giveUp(GaveUp{job.Evaluation, k.SubmissionID, k.DatasetID, st.EvaluationTries},
				"Maximum number of failures reached for the evaluation of a submission")
		case st.compiled || len(st.evalFailed) > 0:
			targets = append(targets, evalTarget{k, st.Timestamp, st.Active, st.EvaluationTries, st.Generation})
			if !st.compiled {
				retry[k] = st.evalFailed
			}
		}
	}
	if len(targets) > 0 {
		ops, _, err := evaluationOps(ctx, a.qq, targets, false, func(k ResultKey, codename string) bool {
			only, ok := retry[k]
			return !ok || only[codename]
		})
		if err != nil {
			return err
		}
		a.ch.Next = append(a.ch.Next, ops...)
	}

	// The task scores of the participants whose official submissions on
	// an active dataset got scored (or lost their score).
	var keys []TaskKey
	for _, st := range list {
		if st.scoreChanged && st.Active && st.Official {
			keys = append(keys, TaskKey{st.ParticipationID, st.TaskID})
		}
	}
	if len(keys) > 0 {
		changes, err := refreshTaskScores(ctx, a.qq, keys)
		if err != nil {
			return err
		}
		a.ch.TaskScores = append(a.ch.TaskScores, changes...)
	}

	for _, st := range list {
		if s := st.status(); s != st.before || st.scoreChanged {
			c := SubmissionChange{
				SubmissionID: st.SubmissionID, DatasetID: st.DatasetID,
				ParticipationID: st.ParticipationID, TaskID: st.TaskID, ContestID: orZero(st.ContestID),
				Timestamp: st.Timestamp, Official: st.Official, Active: st.Active, Status: s, Scored: st.Scored,
			}
			if st.Scored {
				c.Score, c.PublicScore, c.RankingScoreDetails = st.score, st.publicScore, st.ranking
			}
			a.ch.Submissions = append(a.ch.Submissions, c)
		}
	}
	return nil
}

// finishCandidate: a result Score was asked about whose evaluations may
// all be there (upstream finalization in submission_enqueue_operations).
func (s *subState) finishCandidate() bool {
	return !s.compiled && len(s.evaluated) == 0 && len(s.evalFailed) == 0 && !s.compileFailed
}

func (a *applier) giveUp(g GaveUp, msg string) {
	a.ch.GaveUp = append(a.ch.GaveUp, g)
	a.j.log.Error(msg, "operation", string(g.Type), "object", g.ObjectID, "dataset", g.DatasetID, "tries", g.Tries)
}

// utState is a locked user test result and what this transaction did.
type utState struct {
	q.JudgingLockUserTestResultsRow
	before        UserTestStatus
	compiled      bool
	compileFailed bool
	evalFailed    bool
}

func (a *applier) userTests(ctx context.Context, results []*job.Result) error {
	keys := make([]UserTestKey, 0, len(results))
	for _, r := range results {
		keys = append(keys, UserTestKey{r.Operation.ObjectID, r.Operation.DatasetID})
	}
	slices.SortFunc(keys, func(x, y UserTestKey) int {
		if x.UserTestID != y.UserTestID {
			return int(x.UserTestID - y.UserTestID)
		}
		return int(x.DatasetID - y.DatasetID)
	})
	keys = slices.Compact(keys)
	var p q.JudgingLockUserTestResultsParams
	for _, k := range keys {
		p.UserTestIds = append(p.UserTestIds, k.UserTestID)
		p.DatasetIds = append(p.DatasetIds, k.DatasetID)
	}
	rows, err := a.qq.JudgingLockUserTestResults(ctx, p)
	if err != nil {
		return err
	}
	states := make(map[UserTestKey]*utState, len(rows))
	list := make([]*utState, len(rows))
	for i := range rows {
		st := &utState{JudgingLockUserTestResultsRow: rows[i]}
		st.before = userTestStatusOf(st.CompilationOutcome, st.EvaluationOutcome)
		states[UserTestKey{st.UserTestID, st.DatasetID}] = st
		list[i] = st
	}

	var (
		setComp     []q.JudgingSetUserTestCompilationParams
		setCompRes  []*job.Result
		failComp    []q.JudgingFailUserTestCompilationParams
		failCompRes []*job.Result
		setEval     []q.JudgingSetUserTestEvaluationParams
		setEvalRes  []*job.Result
		failEval    []q.JudgingFailUserTestEvaluationParams
		failEvalRes []*job.Result
		executables q.JudgingInsertUserTestExecutablesParams
	)
	for _, r := range results {
		k := UserTestKey{r.Operation.ObjectID, r.Operation.DatasetID}
		st := states[k]
		switch {
		case st == nil:
			a.drop(r, DropGone)
			continue
		case r.Generation != st.Generation:
			a.drop(r, DropStale)
			continue
		}
		attempt := int32(r.Attempt)
		t, wall, mem, stdout, stderr := statsOf(r)
		paths, digests := sandboxColumns(r)
		switch {
		case r.Operation.Type == job.UserTestCompilation && r.Success:
			if st.CompilationOutcome != nil {
				a.drop(r, DropDuplicate)
				continue
			}
			outcome := r.CompilationOutcome
			st.CompilationOutcome, st.compiled = &outcome, true
			setComp = append(setComp, q.JudgingSetUserTestCompilationParams{
				CompilationOutcome: outcome, CompilationText: r.Text, Attempt: attempt,
				CompilationStdout: stdout, CompilationStderr: stderr,
				CompilationTime: t, CompilationWallClockTime: wall, CompilationMemory: mem,
				CompilationSandboxPaths: paths, CompilationSandboxDigests: digests,
				UserTestID: k.UserTestID, DatasetID: k.DatasetID, Generation: st.Generation,
			})
			setCompRes = append(setCompRes, r)
			names := make([]string, 0, len(r.Executables))
			for name := range r.Executables {
				names = append(names, name)
			}
			slices.Sort(names)
			for _, name := range names {
				executables.UserTestIds = append(executables.UserTestIds, k.UserTestID)
				executables.DatasetIds = append(executables.DatasetIds, k.DatasetID)
				executables.Filenames = append(executables.Filenames, name)
				executables.Digests = append(executables.Digests, r.Executables[name])
			}
		case r.Operation.Type == job.UserTestCompilation:
			if st.CompilationOutcome != nil || st.CompilationTries >= attempt {
				a.drop(r, DropDuplicate)
				continue
			}
			st.CompilationTries, st.compileFailed = attempt, true
			failComp = append(failComp, q.JudgingFailUserTestCompilationParams{
				Attempt: attempt, UserTestID: k.UserTestID, DatasetID: k.DatasetID, Generation: st.Generation,
			})
			failCompRes = append(failCompRes, r)
		case r.Success:
			if !compiledOK(st.CompilationOutcome) || st.EvaluationOutcome != nil {
				a.drop(r, DropDuplicate)
				continue
			}
			ok := job.CompilationOK
			st.EvaluationOutcome = &ok
			setEval = append(setEval, q.JudgingSetUserTestEvaluationParams{
				EvaluationText: r.Text, Attempt: attempt, Output: nonEmpty(r.UserOutput),
				ExecutionTime: t, ExecutionWallClockTime: wall, ExecutionMemory: mem,
				EvaluationSandboxPaths: paths, EvaluationSandboxDigests: digests,
				UserTestID: k.UserTestID, DatasetID: k.DatasetID, Generation: st.Generation,
			})
			setEvalRes = append(setEvalRes, r)
		default:
			if !compiledOK(st.CompilationOutcome) || st.EvaluationOutcome != nil || st.EvaluationTries >= attempt {
				a.drop(r, DropDuplicate)
				continue
			}
			st.EvaluationTries, st.evalFailed = attempt, true
			failEval = append(failEval, q.JudgingFailUserTestEvaluationParams{
				Attempt: attempt, UserTestID: k.UserTestID, DatasetID: k.DatasetID, Generation: st.Generation,
			})
			failEvalRes = append(failEvalRes, r)
		}
	}

	var be batchErr
	if len(setComp) > 0 {
		a.qq.JudgingSetUserTestCompilation(ctx, setComp).QueryRow(func(i int, _ int32, err error) {
			if !be.ok(err) {
				a.drop(setCompRes[i], DropDuplicate)
			}
		})
	}
	if be.err == nil && len(executables.UserTestIds) > 0 {
		be.ok(a.qq.JudgingInsertUserTestExecutables(ctx, executables))
	}
	if be.err == nil && len(failComp) > 0 {
		a.qq.JudgingFailUserTestCompilation(ctx, failComp).QueryRow(func(i int, _ int32, err error) {
			if !be.ok(err) {
				a.drop(failCompRes[i], DropDuplicate)
			}
		})
	}
	if be.err == nil && len(setEval) > 0 {
		a.qq.JudgingSetUserTestEvaluation(ctx, setEval).QueryRow(func(i int, _ int32, err error) {
			if !be.ok(err) {
				a.drop(setEvalRes[i], DropDuplicate)
			}
		})
	}
	if be.err == nil && len(failEval) > 0 {
		a.qq.JudgingFailUserTestEvaluation(ctx, failEval).QueryRow(func(i int, _ int32, err error) {
			if !be.ok(err) {
				a.drop(failEvalRes[i], DropDuplicate)
			}
		})
	}
	if be.err != nil {
		return be.err
	}

	for _, st := range list {
		k := UserTestKey{st.UserTestID, st.DatasetID}
		op := Operation{
			Operation: job.Operation{ObjectID: k.UserTestID, DatasetID: k.DatasetID},
			Timestamp: st.Timestamp, Generation: st.Generation,
		}
		switch {
		case !st.ToJudge:
		case st.compiled && compiledOK(st.CompilationOutcome):
			op.Type, op.Priority, op.Attempt = job.UserTestEvaluation, priority(false, st.Active, st.EvaluationTries), int(st.EvaluationTries)+1
			a.ch.Next = append(a.ch.Next, op)
		case st.compileFailed && st.CompilationTries >= a.j.tries.UserTestCompilation:
			a.giveUp(GaveUp{job.UserTestCompilation, k.UserTestID, k.DatasetID, st.CompilationTries},
				"Maximum number of failures reached for the compilation of a user test")
		case st.compileFailed:
			op.Type, op.Priority = job.UserTestCompilation, priority(true, st.Active, st.CompilationTries)
			op.Attempt, op.Retry = int(st.CompilationTries)+1, true
			a.ch.Next = append(a.ch.Next, op)
		case st.evalFailed && st.EvaluationTries >= a.j.tries.UserTestEvaluation:
			a.giveUp(GaveUp{job.UserTestEvaluation, k.UserTestID, k.DatasetID, st.EvaluationTries},
				"Maximum number of failures reached for the evaluation of a user test")
		case st.evalFailed:
			op.Type, op.Priority = job.UserTestEvaluation, priority(false, st.Active, st.EvaluationTries)
			op.Attempt, op.Retry = int(st.EvaluationTries)+1, true
			a.ch.Next = append(a.ch.Next, op)
		}
		if s := userTestStatusOf(st.CompilationOutcome, st.EvaluationOutcome); s != st.before {
			a.ch.UserTests = append(a.ch.UserTests, UserTestChange{
				UserTestID: st.UserTestID, DatasetID: st.DatasetID, ParticipationID: st.ParticipationID,
				TaskID: st.TaskID, ContestID: orZero(st.ContestID), Active: st.Active, Status: s,
			})
		}
	}
	return nil
}

// logChanges logs what upstream logs for each result.
func (j *Judge) logChanges(ch *Changes) {
	for _, d := range ch.Dropped {
		switch d.Reason {
		case DropInvalid:
			j.log.Error("invalid result dropped", "operation", d.Operation.Key(), "err", d.Err)
		case DropGone:
			j.log.Warn("result of a submission, user test or dataset that is gone", "operation", d.Operation.Key())
		default:
			j.log.Info("result dropped", "operation", d.Operation.Key(), "reason", string(d.Reason),
				"generation", d.Generation, "attempt", d.Attempt)
		}
	}
	for _, e := range ch.ScoreErrors {
		j.log.Error("cannot score a submission result", "submission", e.SubmissionID, "dataset", e.DatasetID, "err", e.Err)
	}
}
