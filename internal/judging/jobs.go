// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// The content of the jobs follows CompilationJob.from_submission,
// CompilationJob.from_user_test, EvaluationJob.from_submission and
// EvaluationJob.from_user_test in cms/grading/Job.py of CMS:
// Copyright © 2012 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2013-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2013 Bernard Blackham <bernard@largestprime.net>
// Copyright © 2013-2018 Stefano Maggiolo <s.maggiolo@gmail.com>

package judging

import (
	"context"
	"fmt"
	"strings"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/tasktype"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// Built is the outcome of BuildJobs.
type Built struct {
	// Jobs are ready to encode and enqueue; Ops[i] is the operation of
	// Jobs[i], with the generation and attempt of the job: enqueue Jobs[i]
	// with priority Ops[i].Priority, attempt Jobs[i].Attempt and dedup key
	// Jobs[i].DedupKey(), after a backoff if Ops[i].Retry.
	Jobs []*job.Job
	Ops  []Operation
	// Skipped are the operations that need no job.
	Skipped []Skipped
}

// Skipped is an operation BuildJobs did not build a job for.
type Skipped struct {
	Operation Operation
	Reason    SkipReason
	// Err explains SkipInvalid.
	Err error
}

// SkipReason says why an operation needs no job.
type SkipReason string

// The reasons.
const (
	// SkipDone: the result does not need the operation any more (already
	// compiled or evaluated, or it reached its maximum number of tries).
	SkipDone SkipReason = "done"
	// SkipGone: the submission, user test, dataset or testcase is gone.
	SkipGone SkipReason = "gone"
	// SkipInvalid: the job cannot be built (a manager the task type needs
	// is missing, invalid parameters...): the dataset must be fixed.
	SkipInvalid SkipReason = "invalid"
)

// BuildJobs builds the jobs of operations (upstream Job.from_submission
// and Job.from_user_test) in a constant number of queries: one per kind
// of data (results, files, managers, testcases, executables), whatever the
// number of operations. It first creates the missing result rows, so every
// job carries the generation of an existing row, and checks that each
// operation is still needed: an operation computed before its result
// arrived, or before an invalidation, is skipped or gets the current
// generation and attempt.
func (j *Judge) BuildJobs(ctx context.Context, ops []Operation) (*Built, error) {
	b := &Built{}
	var subOps, utOps []Operation
	seen := make(map[string]bool, len(ops))
	for _, op := range ops {
		if seen[op.Key()] {
			continue
		}
		seen[op.Key()] = true
		if op.Type.ForSubmission() {
			subOps = append(subOps, op)
		} else {
			utOps = append(utOps, op)
		}
	}
	// The managers of every dataset involved, in one query.
	var datasets []int32
	for _, op := range ops {
		datasets = append(datasets, op.DatasetID)
	}
	var managers map[int32]map[string]string
	if len(ops) > 0 {
		rows, err := j.store.Q.JudgingDatasetManagers(ctx, uniq(datasets))
		if err != nil {
			return nil, err
		}
		managers = make(map[int32]map[string]string)
		for _, r := range rows {
			if managers[r.DatasetID] == nil {
				managers[r.DatasetID] = map[string]string{}
			}
			managers[r.DatasetID][r.Filename] = r.Digest
		}
	}
	if len(subOps) > 0 {
		if err := j.buildSubmissionJobs(ctx, b, subOps, managers); err != nil {
			return nil, err
		}
	}
	if len(utOps) > 0 {
		if err := j.buildUserTestJobs(ctx, b, utOps, managers); err != nil {
			return nil, err
		}
	}
	return b, nil
}

// add validates and appends a job, or records why it cannot be built.
func (b *Built) add(op Operation, jb *job.Job) {
	jb.ID = jb.DedupKey()
	if err := jb.Validate(); err != nil {
		b.Skipped = append(b.Skipped, Skipped{Operation: op, Reason: SkipInvalid, Err: err})
		return
	}
	op.Generation, op.Attempt, op.Retry = jb.Generation, jb.Attempt, jb.Attempt > 1
	b.Jobs = append(b.Jobs, jb)
	b.Ops = append(b.Ops, op)
}

func (b *Built) skip(op Operation, reason SkipReason, err error) {
	b.Skipped = append(b.Skipped, Skipped{Operation: op, Reason: reason, Err: err})
}

func orZero[T any](p *T) T {
	var z T
	if p == nil {
		return z
	}
	return *p
}

func (j *Judge) buildSubmissionJobs(ctx context.Context, b *Built, ops []Operation, managers map[int32]map[string]string) error {
	qq := j.store.Q
	var pairs, evalPairs q.JudgingSubmissionJobDataParams
	var tcs q.JudgingTestcasesParams
	seenPair := map[ResultKey]bool{}
	seenEval := map[ResultKey]bool{}
	for _, op := range ops {
		k := ResultKey{op.ObjectID, op.DatasetID}
		if !seenPair[k] {
			seenPair[k] = true
			pairs.SubmissionIds = append(pairs.SubmissionIds, k.SubmissionID)
			pairs.DatasetIds = append(pairs.DatasetIds, k.DatasetID)
		}
		if op.Type == job.Evaluation {
			tcs.DatasetIds = append(tcs.DatasetIds, op.DatasetID)
			tcs.Codenames = append(tcs.Codenames, op.TestcaseCodename)
			if !seenEval[k] {
				seenEval[k] = true
				evalPairs.SubmissionIds = append(evalPairs.SubmissionIds, k.SubmissionID)
				evalPairs.DatasetIds = append(evalPairs.DatasetIds, k.DatasetID)
			}
		}
	}
	if err := qq.JudgingEnsureSubmissionResults(ctx, q.JudgingEnsureSubmissionResultsParams(pairs)); err != nil {
		return err
	}
	data, err := qq.JudgingSubmissionJobData(ctx, pairs)
	if err != nil {
		return err
	}
	results := make(map[ResultKey]*q.JudgingSubmissionJobDataRow, len(data))
	for i := range data {
		results[ResultKey{data[i].SubmissionID, data[i].DatasetID}] = &data[i]
	}
	fileRows, err := qq.JudgingSubmissionFiles(ctx, uniq(pairs.SubmissionIds))
	if err != nil {
		return err
	}
	files := map[int32]map[string]string{}
	for _, f := range fileRows {
		if files[f.SubmissionID] == nil {
			files[f.SubmissionID] = map[string]string{}
		}
		files[f.SubmissionID][f.Filename] = f.Digest
	}

	type tcKey struct {
		dataset  int32
		codename string
	}
	testcases := map[tcKey]*q.JudgingTestcasesRow{}
	executables := map[ResultKey]map[string]string{}
	missing := map[tcKey]map[int32]bool{} // testcase -> submissions without its evaluation
	if len(evalPairs.SubmissionIds) > 0 {
		tcRows, err := qq.JudgingTestcases(ctx, tcs)
		if err != nil {
			return err
		}
		for i := range tcRows {
			testcases[tcKey{tcRows[i].DatasetID, tcRows[i].Codename}] = &tcRows[i]
		}
		exRows, err := qq.JudgingSubmissionExecutables(ctx, q.JudgingSubmissionExecutablesParams(evalPairs))
		if err != nil {
			return err
		}
		for _, e := range exRows {
			k := ResultKey{e.SubmissionID, e.DatasetID}
			if executables[k] == nil {
				executables[k] = map[string]string{}
			}
			executables[k][e.Filename] = e.Digest
		}
		mRows, err := qq.JudgingMissingEvaluations(ctx, q.JudgingMissingEvaluationsParams(evalPairs))
		if err != nil {
			return err
		}
		for _, m := range mRows {
			k := tcKey{m.DatasetID, m.Codename}
			if missing[k] == nil {
				missing[k] = map[int32]bool{}
			}
			missing[k][m.SubmissionID] = true
		}
	}

	for _, op := range ops {
		r := results[ResultKey{op.ObjectID, op.DatasetID}]
		if r == nil {
			b.skip(op, SkipGone, nil)
			continue
		}
		jb := &job.Job{
			Operation:            op.Operation,
			Generation:           r.Generation,
			TaskType:             r.TaskType,
			TaskTypeParameters:   r.TaskTypeParameters,
			Language:             orZero(r.Language),
			MultithreadedSandbox: j.langs.RequiresMultithreading(r.ContestLanguages),
			Files:                files[op.ObjectID],
			Managers:             managers[op.DatasetID],
		}
		if jb.Files == nil {
			jb.Files = map[string]string{}
		}
		if jb.Managers == nil {
			jb.Managers = map[string]string{}
		}
		switch op.Type {
		case job.Compilation:
			if r.CompilationOutcome != nil || r.CompilationTries >= MaxCompilationTries {
				b.skip(op, SkipDone, nil)
				continue
			}
			jb.Attempt = int(r.CompilationTries) + 1
			jb.Info = fmt.Sprintf("compile submission %d", op.ObjectID)
		case job.Evaluation:
			tc := testcases[tcKey{op.DatasetID, op.TestcaseCodename}]
			switch {
			case tc == nil:
				b.skip(op, SkipGone, nil)
				continue
			case orZero(r.CompilationOutcome) != job.CompilationOK || r.EvaluationOutcome != nil ||
				r.EvaluationTries >= MaxEvaluationTries || !missing[tcKey{op.DatasetID, op.TestcaseCodename}][op.ObjectID]:
				b.skip(op, SkipDone, nil)
				continue
			}
			jb.Attempt = int(r.EvaluationTries) + 1
			jb.Executables = executables[ResultKey{op.ObjectID, op.DatasetID}]
			if jb.Executables == nil {
				jb.Executables = map[string]string{}
			}
			jb.Input, jb.Output = tc.Input, tc.Output
			jb.TimeLimit, jb.MemoryLimit = orZero(r.TimeLimit), orZero(r.MemoryLimit)
			jb.Info = fmt.Sprintf("evaluate submission %d on testcase %s", op.ObjectID, op.TestcaseCodename)
		default:
			b.skip(op, SkipInvalid, fmt.Errorf("%s is not an operation on a submission", op.Type))
			continue
		}
		b.add(op, jb)
	}
	return nil
}

func (j *Judge) buildUserTestJobs(ctx context.Context, b *Built, ops []Operation, managers map[int32]map[string]string) error {
	qq := j.store.Q
	var pairs q.JudgingUserTestJobDataParams
	var evalPairs q.JudgingUserTestExecutablesParams
	seen := map[UserTestKey]bool{}
	for _, op := range ops {
		k := UserTestKey{op.ObjectID, op.DatasetID}
		if seen[k] {
			continue
		}
		seen[k] = true
		pairs.UserTestIds = append(pairs.UserTestIds, k.UserTestID)
		pairs.DatasetIds = append(pairs.DatasetIds, k.DatasetID)
		if op.Type == job.UserTestEvaluation {
			evalPairs.UserTestIds = append(evalPairs.UserTestIds, k.UserTestID)
			evalPairs.DatasetIds = append(evalPairs.DatasetIds, k.DatasetID)
		}
	}
	if err := qq.JudgingEnsureUserTestResults(ctx, q.JudgingEnsureUserTestResultsParams(pairs)); err != nil {
		return err
	}
	data, err := qq.JudgingUserTestJobData(ctx, pairs)
	if err != nil {
		return err
	}
	results := make(map[UserTestKey]*q.JudgingUserTestJobDataRow, len(data))
	for i := range data {
		results[UserTestKey{data[i].UserTestID, data[i].DatasetID}] = &data[i]
	}
	ids := uniq(pairs.UserTestIds)
	fileRows, err := qq.JudgingUserTestFiles(ctx, ids)
	if err != nil {
		return err
	}
	files := map[int32]map[string]string{}
	for _, f := range fileRows {
		if files[f.UserTestID] == nil {
			files[f.UserTestID] = map[string]string{}
		}
		files[f.UserTestID][f.Filename] = f.Digest
	}
	mRows, err := qq.JudgingUserTestManagers(ctx, ids)
	if err != nil {
		return err
	}
	userManagers := map[int32]map[string]string{}
	for _, m := range mRows {
		if userManagers[m.UserTestID] == nil {
			userManagers[m.UserTestID] = map[string]string{}
		}
		userManagers[m.UserTestID][m.Filename] = m.Digest
	}
	executables := map[UserTestKey]map[string]string{}
	if len(evalPairs.UserTestIds) > 0 {
		exRows, err := qq.JudgingUserTestExecutables(ctx, evalPairs)
		if err != nil {
			return err
		}
		for _, e := range exRows {
			k := UserTestKey{e.UserTestID, e.DatasetID}
			if executables[k] == nil {
				executables[k] = map[string]string{}
			}
			executables[k][e.Filename] = e.Digest
		}
	}

	for _, op := range ops {
		r := results[UserTestKey{op.ObjectID, op.DatasetID}]
		if r == nil {
			b.skip(op, SkipGone, nil)
			continue
		}
		jb := &job.Job{
			Operation:            op.Operation,
			Generation:           r.Generation,
			TaskType:             r.TaskType,
			TaskTypeParameters:   r.TaskTypeParameters,
			Language:             orZero(r.Language),
			MultithreadedSandbox: j.langs.RequiresMultithreading(r.ContestLanguages),
			Files:                files[op.ObjectID],
		}
		if jb.Files == nil {
			jb.Files = map[string]string{}
		}
		switch op.Type {
		case job.UserTestCompilation:
			if r.CompilationOutcome != nil || r.CompilationTries >= MaxUserTestCompilationTries {
				b.skip(op, SkipDone, nil)
				continue
			}
			jb.Attempt = int(r.CompilationTries) + 1
			jb.Info = fmt.Sprintf("compile user test %d", op.ObjectID)
		case job.UserTestEvaluation:
			if orZero(r.CompilationOutcome) != job.CompilationOK || r.EvaluationOutcome != nil ||
				r.EvaluationTries >= MaxUserTestEvaluationTries {
				b.skip(op, SkipDone, nil)
				continue
			}
			jb.Attempt = int(r.EvaluationTries) + 1
			jb.Executables = executables[UserTestKey{op.ObjectID, op.DatasetID}]
			if jb.Executables == nil {
				jb.Executables = map[string]string{}
			}
			jb.Input = r.Input
			jb.TimeLimit, jb.MemoryLimit = orZero(r.TimeLimit), orZero(r.MemoryLimit)
			jb.OnlyExecution, jb.GetOutput = true, true
			jb.Info = fmt.Sprintf("evaluate user test %d", op.ObjectID)
		default:
			b.skip(op, SkipInvalid, fmt.Errorf("%s is not an operation on a user test", op.Type))
			continue
		}
		var err error
		jb.Managers, err = j.userTestManagers(r, userManagers[op.ObjectID], managers[op.DatasetID], op.Type.IsCompilation())
		if err != nil {
			b.skip(op, SkipInvalid, err)
			continue
		}
		b.add(op, jb)
	}
	return nil
}

// userTestManagers are the managers of a user test job (upstream
// CompilationJob.from_user_test and EvaluationJob.from_user_test): those
// sent by the contestant, the auto managers of the task type from the
// dataset and, to compile, the headers of the dataset in the language.
func (j *Judge) userTestManagers(r *q.JudgingUserTestJobDataRow, user, dataset map[string]string, compilation bool) (map[string]string, error) {
	tt, err := tasktype.Get(r.TaskType, r.TaskTypeParameters)
	if err != nil {
		return nil, err
	}
	lang, _ := j.langs.Lookup(orZero(r.Language))
	out := make(map[string]string, len(user)+len(dataset))
	for name, d := range user {
		out[name] = d
	}
	for _, name := range tt.AutoManagers() {
		if strings.HasSuffix(name, ".%l") && lang != nil {
			name = strings.TrimSuffix(name, ".%l") + lang.SourceExtension()
		}
		d, ok := dataset[name]
		if !ok {
			return nil, fmt.Errorf("the dataset has no manager %q, needed by user tests of %s tasks", name, tt.Name())
		}
		out[name] = d
	}
	if compilation && lang != nil {
		for name, d := range dataset {
			for _, h := range lang.HeaderExtensions {
				if strings.HasSuffix(name, h) {
					out[name] = d
				}
			}
		}
	}
	return out, nil
}
