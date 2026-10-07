// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package store_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/store"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

func TestSubmissionsFilesTokens(t *testing.T) {
	f, _ := newFixture(t)
	ctx, qs := f.ctx, f.q

	c, g := f.contest("ioi")
	a, ds := f.task(c, 0, "aaa")
	b, _ := f.task(c, 1, "bbb")
	p := f.participation(c, g, f.user("alice"), nil)

	s1 := f.submission(p, a, t0.Add(time.Minute))
	s2 := f.submission(p, a, t0.Add(2*time.Minute))
	s3 := f.submission(p, b, t0.Add(3*time.Minute))
	if s1.OpaqueID < 0 || s1.OpaqueID == s2.OpaqueID || s1.Comment != "" || !s1.Official {
		t.Fatalf("CreateSubmission = %+v, %+v", s1, s2)
	}
	// A colliding opaque id returns no row and keeps the transaction usable.
	check(t, f.s.InTx(ctx, func(tx *q.Queries) error {
		_, err := tx.CreateSubmission(ctx, q.CreateSubmissionParams{
			ParticipationID: p.ID, TaskID: a.ID, OpaqueID: s1.OpaqueID, Timestamp: t0,
		})
		wantNoRows(t, "CreateSubmission with a used opaque id", err)
		_, err = tx.GetSubmission(ctx, s1.ID)
		return err
	}))

	if got := must[q.Submission](t)(qs.GetSubmission(ctx, s1.ID)); got.OpaqueID != s1.OpaqueID {
		t.Fatalf("GetSubmission = %+v", got)
	}
	if got := must[q.Submission](t)(qs.GetSubmissionByOpaqueID(ctx, q.GetSubmissionByOpaqueIDParams{
		ParticipationID: p.ID, TaskID: a.ID, OpaqueID: s2.OpaqueID,
	})); got.ID != s2.ID {
		t.Fatalf("GetSubmissionByOpaqueID = %+v", got)
	}
	_, err := qs.GetSubmissionByOpaqueID(ctx, q.GetSubmissionByOpaqueIDParams{
		ParticipationID: p.ID, TaskID: b.ID, OpaqueID: s2.OpaqueID,
	})
	wantNoRows(t, "GetSubmissionByOpaqueID with the wrong task", err)

	// Files.
	n, err := qs.CreateFiles(ctx, q.CreateFilesParams{
		SubmissionID: s1.ID, Filenames: []string{"aaa.%l", "aaa.h"}, Digests: []string{digest("1"), digest("2")},
	})
	wantRows(t, "CreateFiles", n, err, 2)
	if _, err := qs.CreateFiles(ctx, q.CreateFilesParams{
		SubmissionID: s2.ID, Filenames: []string{"a", "b"}, Digests: []string{digest("1")},
	}); err == nil {
		t.Fatal("CreateFiles accepted fewer digests than filenames")
	}
	f3 := must[q.File](t)(qs.CreateFile(ctx, q.CreateFileParams{SubmissionID: s3.ID, Filename: "bbb.%l", Digest: digest("3")}))
	if got := must[q.File](t)(qs.GetFile(ctx, f3.ID)); got != f3 {
		t.Fatalf("GetFile = %+v", got)
	}
	if got := must[[]q.File](t)(qs.ListFilesBySubmission(ctx, s1.ID)); len(got) != 2 || got[0].Filename != "aaa.%l" {
		t.Fatalf("ListFilesBySubmission = %+v", got)
	}
	files := must[[]q.File](t)(qs.ListFilesBySubmissions(ctx, []int32{s3.ID, s1.ID, s2.ID}))
	if len(files) != 3 || files[0].SubmissionID != s1.ID || files[2].SubmissionID != s3.ID {
		t.Fatalf("ListFilesBySubmissions = %+v", files)
	}
	n, err = qs.DeleteFile(ctx, f3.ID)
	wantRows(t, "DeleteFile", n, err, 1)

	// Tokens.
	tk := must[q.Token](t)(qs.CreateToken(ctx, q.CreateTokenParams{SubmissionID: s2.ID, Timestamp: t0.Add(4 * time.Minute)}))
	must[q.Token](t)(qs.CreateToken(ctx, q.CreateTokenParams{SubmissionID: s3.ID, Timestamp: t0.Add(5 * time.Minute)}))
	_, err = qs.CreateToken(ctx, q.CreateTokenParams{SubmissionID: s2.ID, Timestamp: t0})
	if !store.IsUniqueViolation(err, "tokens_submission_id_key") {
		t.Fatalf("second token: %v, want tokens_submission_id_key violation", err)
	}
	if got := must[q.Token](t)(qs.GetToken(ctx, tk.ID)); got.SubmissionID != s2.ID {
		t.Fatalf("GetToken = %+v", got)
	}
	if got := must[q.Token](t)(qs.GetTokenBySubmission(ctx, s2.ID)); got.ID != tk.ID {
		t.Fatalf("GetTokenBySubmission = %+v", got)
	}
	tokens := must[[]q.ListTokensByParticipationRow](t)(qs.ListTokensByParticipation(ctx, p.ID))
	if len(tokens) != 2 || tokens[0].TaskID != a.ID || tokens[1].TaskID != b.ID {
		t.Fatalf("ListTokensByParticipation = %+v", tokens)
	}

	// The contestant's list, the admin details and the submit-time stats.
	f.compile(s2, ds, "ok")
	f.score(s2, ds, 40)
	mine := must[[]q.ListSubmissionsByParticipationTaskRow](t)(qs.ListSubmissionsByParticipationTask(ctx,
		q.ListSubmissionsByParticipationTaskParams{ParticipationID: p.ID, TaskID: a.ID}))
	if len(mine) != 2 || mine[0].Submission.ID != s1.ID || mine[0].CompilationOutcome != nil || mine[0].TokenTimestamp != nil ||
		*mine[1].Score != 40 || *mine[1].EvaluationOutcome != "ok" || mine[1].TokenTimestamp == nil {
		t.Fatalf("ListSubmissionsByParticipationTask = %+v", mine)
	}
	det := must[q.GetSubmissionDetailsRow](t)(qs.GetSubmissionDetails(ctx, s2.ID))
	if det.User.Username != "alice" || det.Task.ID != a.ID || det.Participation.ID != p.ID || det.TokenTimestamp == nil {
		t.Fatalf("GetSubmissionDetails = %+v", det)
	}
	stats := must[q.GetSubmissionStatsRow](t)(qs.GetSubmissionStats(ctx, q.GetSubmissionStatsParams{
		ParticipationID: p.ID, TaskID: a.ID, ContestID: c.ID,
	}))
	if stats.TaskCount != 2 || stats.ContestCount != 3 || !stats.TaskLast.Equal(s2.Timestamp) || !stats.ContestLast.Equal(s3.Timestamp) {
		t.Fatalf("GetSubmissionStats = %+v", stats)
	}
	other := f.participation(c, g, f.user("bob"), nil)
	empty := must[q.GetSubmissionStatsRow](t)(qs.GetSubmissionStats(ctx, q.GetSubmissionStatsParams{
		ParticipationID: other.ID, TaskID: a.ID, ContestID: c.ID,
	}))
	if empty.TaskCount != 0 || empty.ContestCount != 0 || empty.TaskLast != nil || empty.ContestLast != nil {
		t.Fatalf("GetSubmissionStats without submissions = %+v", empty)
	}

	s1 = must[q.Submission](t)(qs.UpdateSubmission(ctx, q.UpdateSubmissionParams{ID: s1.ID, Comment: "plagiarism?", Official: false}))
	if s1.Comment != "plagiarism?" || s1.Official {
		t.Fatalf("UpdateSubmission = %+v", s1)
	}
	n, err = qs.DeleteToken(ctx, tk.ID)
	wantRows(t, "DeleteToken", n, err, 1)
	n, err = qs.DeleteSubmission(ctx, s1.ID)
	wantRows(t, "DeleteSubmission", n, err, 1)
	if got := must[[]q.File](t)(qs.ListFilesBySubmission(ctx, s1.ID)); len(got) != 0 {
		t.Fatalf("files of a deleted submission = %+v", got)
	}
}

// TestResultsAreIdempotent replays every worker result twice, as a
// redelivered job would: the second write must change nothing.
func TestResultsAreIdempotent(t *testing.T) {
	f, _ := newFixture(t)
	ctx, qs := f.ctx, f.q

	c, g := f.contest("ioi")
	a, ds := f.task(c, 0, "aaa")
	auto := must[q.Dataset](t)(qs.CreateDataset(ctx, datasetParams(a.ID, "autojudge")))
	tc1, tc2 := f.testcase(ds, "001", true), f.testcase(ds, "002", false)
	p := f.participation(c, g, f.user("alice"), nil)
	s := f.submission(p, a, t0.Add(time.Minute))
	key := q.GetSubmissionResultParams{SubmissionID: s.ID, DatasetID: ds.ID}

	n, err := qs.CreateSubmissionResult(ctx, q.CreateSubmissionResultParams{SubmissionID: s.ID, DatasetID: ds.ID})
	wantRows(t, "CreateSubmissionResult", n, err, 1)
	n, err = qs.CreateSubmissionResult(ctx, q.CreateSubmissionResultParams{SubmissionID: s.ID, DatasetID: ds.ID})
	wantRows(t, "CreateSubmissionResult again", n, err, 0)
	if st := store.ResultStatus(must[q.SubmissionResult](t)(qs.GetSubmissionResult(ctx, key))); st != store.StatusCompiling {
		t.Fatalf("status of a new result = %s", st)
	}

	// A failed attempt only counts a try, once even if its result is
	// delivered twice (a dispatcher that dies before the ACK); then the
	// real result of the second attempt; then a duplicate of it, ignored.
	comp := q.SetSubmissionCompilationParams{SubmissionID: s.ID, DatasetID: ds.ID, Attempt: 1}
	n, err = qs.SetSubmissionCompilation(ctx, comp)
	wantRows(t, "failed compilation attempt", n, err, 1)
	n, err = qs.SetSubmissionCompilation(ctx, comp)
	wantRows(t, "redelivered failed compilation attempt", n, err, 0)
	if r := must[q.SubmissionResult](t)(qs.GetSubmissionResult(ctx, key)); r.CompilationTries != 1 || r.CompilationOutcome != nil {
		t.Fatalf("after a failed compilation attempt delivered twice: %+v", r)
	}
	comp.Attempt = 2
	comp.CompilationOutcome = ptr("ok")
	comp.CompilationText = []string{"OK"}
	comp.CompilationStdout = ptr("")
	comp.CompilationTime = ptr(0.25)
	comp.CompilationMemory = ptr(int64(1 << 20))
	comp.CompilationShard = ptr(int32(3))
	comp.CompilationSandboxPaths = []string{"/tmp/box"}
	n, err = qs.SetSubmissionCompilation(ctx, comp)
	wantRows(t, "compilation", n, err, 1)
	dup := comp
	dup.CompilationOutcome = ptr("fail")
	n, err = qs.SetSubmissionCompilation(ctx, dup)
	wantRows(t, "duplicate compilation", n, err, 0)
	r := must[q.SubmissionResult](t)(qs.GetSubmissionResult(ctx, key))
	if *r.CompilationOutcome != "ok" || r.CompilationTries != 2 || *r.CompilationShard != 3 || store.ResultStatus(r) != store.StatusEvaluating {
		t.Fatalf("after the compilation: %+v", r)
	}
	// SetSubmissionCompilation creates the row of another dataset too.
	n, err = qs.SetSubmissionCompilation(ctx, q.SetSubmissionCompilationParams{
		SubmissionID: s.ID, DatasetID: auto.ID, CompilationOutcome: ptr("fail"), Attempt: 1,
	})
	wantRows(t, "compilation on the autojudge dataset", n, err, 1)

	for range 2 {
		n, err = qs.CreateExecutable(ctx, q.CreateExecutableParams{SubmissionID: s.ID, DatasetID: ds.ID, Filename: "aaa", Digest: digest("exe")})
		check(t, err)
	}
	wantRows(t, "duplicate CreateExecutable", n, err, 0)
	exes := must[[]q.Executable](t)(qs.ListExecutables(ctx, q.ListExecutablesParams{SubmissionID: s.ID, DatasetID: ds.ID}))
	if len(exes) != 1 {
		t.Fatalf("ListExecutables = %+v", exes)
	}
	if got := must[q.Executable](t)(qs.GetExecutable(ctx, exes[0].ID)); got != exes[0] {
		t.Fatalf("GetExecutable = %+v", got)
	}

	// Evaluations: single, then a batch that repeats it.
	ev := q.CreateEvaluationParams{
		SubmissionID: s.ID, DatasetID: ds.ID, TestcaseID: tc1.ID, Outcome: ptr("1.0"),
		Text: []string{"Output is correct"}, ExecutionTime: ptr(0.1), ExecutionMemory: ptr(int64(1 << 20)),
	}
	n, err = qs.CreateEvaluation(ctx, ev)
	wantRows(t, "CreateEvaluation", n, err, 1)
	if left := must[[]q.Testcase](t)(qs.ListUnevaluatedTestcases(ctx, q.ListUnevaluatedTestcasesParams{
		SubmissionID: s.ID, DatasetID: ds.ID,
	})); len(left) != 1 || left[0].ID != tc2.ID {
		t.Fatalf("ListUnevaluatedTestcases = %+v", left)
	}
	batch := []q.CreateEvaluationsBatchParams{
		{SubmissionID: s.ID, DatasetID: ds.ID, TestcaseID: tc1.ID, Outcome: ptr("0.0")},
		{SubmissionID: s.ID, DatasetID: ds.ID, TestcaseID: tc2.ID, Outcome: ptr("0.5"), AdminText: ptr("partial")},
	}
	var batchErr error
	qs.CreateEvaluationsBatch(ctx, batch).Exec(func(_ int, err error) {
		if err != nil && batchErr == nil {
			batchErr = err
		}
	})
	check(t, batchErr)
	evs := must[[]q.ListEvaluationsRow](t)(qs.ListEvaluations(ctx, q.ListEvaluationsParams{SubmissionID: s.ID, DatasetID: ds.ID}))
	if len(evs) != 2 || evs[0].Codename != "001" || *evs[0].Evaluation.Outcome != "1.0" || !evs[0].Public ||
		*evs[1].Evaluation.Outcome != "0.5" || evs[1].Evaluation.Text == nil {
		t.Fatalf("ListEvaluations = %+v (the first result must win)", evs)
	}
	if got := must[q.Evaluation](t)(qs.GetEvaluation(ctx, evs[1].Evaluation.ID)); *got.AdminText != "partial" {
		t.Fatalf("GetEvaluation = %+v", got)
	}

	// The same for the evaluation: a failed attempt counted once, a late
	// failure of an older attempt ignored, then the outcome.
	evalFail := q.SetSubmissionEvaluationParams{SubmissionID: s.ID, DatasetID: ds.ID, Attempt: 2}
	n, err = qs.SetSubmissionEvaluation(ctx, evalFail)
	wantRows(t, "failed evaluation attempt", n, err, 1)
	n, err = qs.SetSubmissionEvaluation(ctx, evalFail)
	wantRows(t, "redelivered failed evaluation attempt", n, err, 0)
	evalFail.Attempt = 1
	n, err = qs.SetSubmissionEvaluation(ctx, evalFail)
	wantRows(t, "late failure of an older evaluation attempt", n, err, 0)
	if r := must[q.SubmissionResult](t)(qs.GetSubmissionResult(ctx, key)); r.EvaluationTries != 2 || store.ResultStatus(r) != store.StatusEvaluating {
		t.Fatalf("after failed evaluation attempts: %+v", r)
	}
	evalOK := q.SetSubmissionEvaluationParams{SubmissionID: s.ID, DatasetID: ds.ID, EvaluationOutcome: ptr("ok"), Attempt: 3}
	n, err = qs.SetSubmissionEvaluation(ctx, evalOK)
	wantRows(t, "SetSubmissionEvaluation", n, err, 1)
	n, err = qs.SetSubmissionEvaluation(ctx, evalOK)
	wantRows(t, "duplicate SetSubmissionEvaluation", n, err, 0)
	if st := store.ResultStatus(must[q.SubmissionResult](t)(qs.GetSubmissionResult(ctx, key))); st != store.StatusScoring {
		t.Fatalf("status after the evaluation = %s", st)
	}
	n, err = qs.SetSubmissionScore(ctx, scoreParams(s, ds, 75))
	wantRows(t, "SetSubmissionScore", n, err, 1)
	n, err = qs.SetSubmissionScore(ctx, scoreParams(s, ds, 10))
	wantRows(t, "duplicate SetSubmissionScore", n, err, 0)
	r = must[q.SubmissionResult](t)(qs.GetSubmissionResult(ctx, key))
	if *r.Score != 75 || store.ResultStatus(r) != store.StatusScored || r.EvaluationTries != 3 || !r.ScoredAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("after scoring: %+v", r)
	}
	// The failed compilation on the other dataset is scored without evaluation.
	n, err = qs.SetSubmissionScore(ctx, scoreParams(s, auto, 0))
	wantRows(t, "SetSubmissionScore after a failed compilation", n, err, 1)

	results := must[[]q.ListSubmissionResultsBySubmissionRow](t)(qs.ListSubmissionResultsBySubmission(ctx, s.ID))
	if len(results) != 2 || !results[0].Active || results[1].Active || results[1].DatasetDescription != "autojudge" ||
		store.ResultStatus(results[1].SubmissionResult) != store.StatusCompilationFailed {
		t.Fatalf("ListSubmissionResultsBySubmission = %+v", results)
	}
	if got := must[[]q.SubmissionResult](t)(qs.ListSubmissionResultsByDataset(ctx, ds.ID)); len(got) != 1 || *got[0].Score != 75 {
		t.Fatalf("ListSubmissionResultsByDataset = %+v", got)
	}

	// Rescoring keeps the first scoring time.
	n, err = qs.InvalidateSubmissionScore(ctx, q.InvalidateSubmissionScoreParams{SubmissionID: s.ID, DatasetID: ds.ID})
	wantRows(t, "InvalidateSubmissionScore", n, err, 1)
	later := scoreParams(s, ds, 80)
	later.ScoredAt = t0.Add(2 * time.Hour)
	n, err = qs.SetSubmissionScore(ctx, later)
	wantRows(t, "SetSubmissionScore after invalidating", n, err, 1)
	r = must[q.SubmissionResult](t)(qs.GetSubmissionResult(ctx, key))
	if *r.Score != 80 || !r.ScoredAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("after rescoring: score %v at %v", *r.Score, r.ScoredAt)
	}
	n, err = qs.InvalidateDatasetScores(ctx, ds.ID)
	wantRows(t, "InvalidateDatasetScores", n, err, 1)

	// Writing a score makes the result scored even with nil details (a
	// Go port of Sum returns no ranking details): they are stored empty,
	// never NULL, or the result would stay "scoring" while the rankings
	// already count it.
	bare := scoreParams(s, ds, 60)
	bare.ScoreDetails, bare.PublicScoreDetails, bare.RankingScoreDetails = nil, nil, nil
	n, err = qs.SetSubmissionScore(ctx, bare)
	wantRows(t, "SetSubmissionScore with nil details", n, err, 1)
	r = must[q.SubmissionResult](t)(qs.GetSubmissionResult(ctx, key))
	if st := store.ResultStatus(r); st != store.StatusScored || string(r.ScoreDetails) != "[]" ||
		string(r.PublicScoreDetails) != "[]" || r.RankingScoreDetails == nil || len(r.RankingScoreDetails) != 0 {
		t.Fatalf("after scoring with nil details: status %s, %+v", st, r)
	}
	n, err = qs.SetSubmissionScore(ctx, scoreParams(s, ds, 10))
	wantRows(t, "SetSubmissionScore after scoring with nil details", n, err, 0)
	n, err = qs.InvalidateDatasetScores(ctx, ds.ID)
	wantRows(t, "InvalidateDatasetScores", n, err, 1)

	// Re-evaluation deletes the evaluations; recompilation also the executables.
	n, err = qs.InvalidateSubmissionEvaluation(ctx, q.InvalidateSubmissionEvaluationParams{SubmissionID: s.ID, DatasetID: ds.ID})
	wantRows(t, "InvalidateSubmissionEvaluation", n, err, 1)
	r = must[q.SubmissionResult](t)(qs.GetSubmissionResult(ctx, key))
	if store.ResultStatus(r) != store.StatusEvaluating || r.EvaluationTries != 0 {
		t.Fatalf("after InvalidateSubmissionEvaluation: %+v", r)
	}
	if got := must[[]q.ListEvaluationsRow](t)(qs.ListEvaluations(ctx, q.ListEvaluationsParams{SubmissionID: s.ID, DatasetID: ds.ID})); len(got) != 0 {
		t.Fatalf("evaluations left: %+v", got)
	}
	n, err = qs.CreateEvaluation(ctx, ev)
	wantRows(t, "CreateEvaluation after invalidating", n, err, 1)
	n, err = qs.InvalidateSubmissionCompilation(ctx, q.InvalidateSubmissionCompilationParams{SubmissionID: s.ID, DatasetID: ds.ID})
	wantRows(t, "InvalidateSubmissionCompilation", n, err, 1)
	r = must[q.SubmissionResult](t)(qs.GetSubmissionResult(ctx, key))
	if store.ResultStatus(r) != store.StatusCompiling || r.CompilationTries != 0 || r.CompilationText == nil || r.CompilationShard != nil {
		t.Fatalf("after InvalidateSubmissionCompilation: %+v", r)
	}
	if got := must[[]q.Executable](t)(qs.ListExecutables(ctx, q.ListExecutablesParams{SubmissionID: s.ID, DatasetID: ds.ID})); len(got) != 0 {
		t.Fatalf("executables left: %+v", got)
	}

	f.compile(s, ds, "ok")
	n, err = qs.CreateExecutable(ctx, q.CreateExecutableParams{SubmissionID: s.ID, DatasetID: ds.ID, Filename: "aaa", Digest: digest("exe")})
	wantRows(t, "CreateExecutable", n, err, 1)
	n, err = qs.CreateEvaluation(ctx, ev)
	wantRows(t, "CreateEvaluation", n, err, 1)
	n, err = qs.DeleteEvaluations(ctx, q.DeleteEvaluationsParams{SubmissionID: s.ID, DatasetID: ds.ID})
	wantRows(t, "DeleteEvaluations", n, err, 1)
	n, err = qs.DeleteExecutables(ctx, q.DeleteExecutablesParams{SubmissionID: s.ID, DatasetID: ds.ID})
	wantRows(t, "DeleteExecutables", n, err, 1)
	n, err = qs.DeleteSubmissionResult(ctx, q.DeleteSubmissionResultParams{SubmissionID: s.ID, DatasetID: ds.ID})
	wantRows(t, "DeleteSubmissionResult", n, err, 1)
	_, err = qs.GetSubmissionResult(ctx, key)
	wantNoRows(t, "GetSubmissionResult after delete", err)
}

func TestScoredResultsByContest(t *testing.T) {
	f, _ := newFixture(t)
	ctx, qs := f.ctx, f.q

	c, g := f.contest("ioi")
	a, dsA := f.task(c, 0, "aaa")
	b, dsB := f.task(c, 1, "bbb")
	other, og := f.contest("other")
	x, dsX := f.task(other, 0, "xxx")
	pa := f.participation(c, g, f.user("alice"), nil)
	pb := f.participation(c, g, f.user("bob"), nil)
	po := f.participation(other, og, f.user("carol"), nil)

	s1 := f.submission(pa, a, t0.Add(time.Minute))
	f.compile(s1, dsA, "ok")
	f.score(s1, dsA, 30)
	s2 := f.submission(pa, a, t0.Add(2*time.Minute))
	f.compile(s2, dsA, "ok")
	f.score(s2, dsA, 100)
	must[q.Token](t)(qs.CreateToken(ctx, q.CreateTokenParams{SubmissionID: s2.ID, Timestamp: t0.Add(3 * time.Minute)}))
	s3 := f.submission(pb, b, t0.Add(time.Minute))
	f.compile(s3, dsB, "ok")
	f.score(s3, dsB, 50)
	f.submission(pb, a, t0.Add(4*time.Minute)) // not scored: not listed
	s5 := f.submission(po, x, t0)
	f.compile(s5, dsX, "ok")
	f.score(s5, dsX, 1)

	rows := must[[]q.ListScoredResultsByContestRow](t)(qs.ListScoredResultsByContest(ctx, c.ID))
	if len(rows) != 3 {
		t.Fatalf("ListScoredResultsByContest = %+v, want 3 rows", rows)
	}
	if rows[0].SubmissionID != s1.ID || rows[0].Tokened || rows[1].SubmissionID != s2.ID || !rows[1].Tokened ||
		*rows[1].Score != 100 || rows[2].SubmissionID != s3.ID || rows[2].ParticipationID != pb.ID {
		t.Fatalf("ListScoredResultsByContest = %+v", rows)
	}

	// A result with a score but not every score column (a row imported
	// from elsewhere) is "scoring", as in CMS: not ranked, and
	// SetSubmissionScore completes it.
	_, err := f.s.Pool.Exec(ctx, `UPDATE submission_results SET ranking_score_details = NULL
WHERE submission_id = $1 AND dataset_id = $2`, s3.ID, dsB.ID)
	check(t, err)
	key := q.GetSubmissionResultParams{SubmissionID: s3.ID, DatasetID: dsB.ID}
	if st := store.ResultStatus(must[q.SubmissionResult](t)(qs.GetSubmissionResult(ctx, key))); st != store.StatusScoring {
		t.Fatalf("status of a result without ranking details = %s", st)
	}
	if rows := must[[]q.ListScoredResultsByContestRow](t)(qs.ListScoredResultsByContest(ctx, c.ID)); len(rows) != 2 {
		t.Fatalf("ListScoredResultsByContest with a partly scored result = %+v, want 2 rows", rows)
	}
	n, err := qs.SetSubmissionScore(ctx, scoreParams(s3, dsB, 50))
	wantRows(t, "SetSubmissionScore of a partly scored result", n, err, 1)
	if rows := must[[]q.ListScoredResultsByContestRow](t)(qs.ListScoredResultsByContest(ctx, c.ID)); len(rows) != 3 {
		t.Fatalf("ListScoredResultsByContest after completing the score = %+v, want 3 rows", rows)
	}
}

func TestUserTests(t *testing.T) {
	f, _ := newFixture(t)
	ctx, qs := f.ctx, f.q

	c, g := f.contest("ioi")
	a, ds := f.task(c, 0, "aaa")
	b, _ := f.task(c, 1, "bbb")
	p := f.participation(c, g, f.user("alice"), nil)

	var tests []q.UserTest
	for i, tk := range []q.Task{a, a, b} {
		tests = append(tests, must[q.UserTest](t)(qs.CreateUserTest(ctx, q.CreateUserTestParams{
			ParticipationID: p.ID, TaskID: tk.ID, Timestamp: t0.Add(time.Duration(i) * time.Minute),
			Language: ptr("C++20 / g++"), Input: digest(fmt.Sprint("input", i)),
		})))
	}
	ut := tests[0]
	if got := must[q.UserTest](t)(qs.GetUserTest(ctx, ut.ID)); got.Input != ut.Input {
		t.Fatalf("GetUserTest = %+v", got)
	}
	det := must[q.GetUserTestDetailsRow](t)(qs.GetUserTestDetails(ctx, ut.ID))
	if det.User.Username != "alice" || det.Task.ID != a.ID || det.Participation.ID != p.ID {
		t.Fatalf("GetUserTestDetails = %+v", det)
	}

	n, err := qs.CreateUserTestFiles(ctx, q.CreateUserTestFilesParams{
		UserTestID: ut.ID, Filenames: []string{"aaa.%l"}, Digests: []string{digest("src")},
	})
	wantRows(t, "CreateUserTestFiles", n, err, 1)
	files := must[[]q.UserTestFile](t)(qs.ListUserTestFiles(ctx, ut.ID))
	if len(files) != 1 {
		t.Fatalf("ListUserTestFiles = %+v", files)
	}
	if got := must[q.UserTestFile](t)(qs.GetUserTestFile(ctx, files[0].ID)); got != files[0] {
		t.Fatalf("GetUserTestFile = %+v", got)
	}
	n, err = qs.CreateUserTestManagers(ctx, q.CreateUserTestManagersParams{
		UserTestID: ut.ID, Filenames: []string{"grader.cpp", "aaa.h"}, Digests: []string{digest("g"), digest("h")},
	})
	wantRows(t, "CreateUserTestManagers", n, err, 2)
	managers := must[[]q.UserTestManager](t)(qs.ListUserTestManagers(ctx, ut.ID))
	if len(managers) != 2 || managers[0].Filename != "aaa.h" {
		t.Fatalf("ListUserTestManagers = %+v", managers)
	}
	if got := must[q.UserTestManager](t)(qs.GetUserTestManager(ctx, managers[1].ID)); got != managers[1] {
		t.Fatalf("GetUserTestManager = %+v", got)
	}

	// Results, idempotent as the submission ones.
	key := q.GetUserTestResultParams{UserTestID: ut.ID, DatasetID: ds.ID}
	n, err = qs.CreateUserTestResult(ctx, q.CreateUserTestResultParams{UserTestID: ut.ID, DatasetID: ds.ID})
	wantRows(t, "CreateUserTestResult", n, err, 1)
	n, err = qs.CreateUserTestResult(ctx, q.CreateUserTestResultParams{UserTestID: ut.ID, DatasetID: ds.ID})
	wantRows(t, "CreateUserTestResult again", n, err, 0)
	// A failed attempt counts once however many times it is delivered.
	comp := q.SetUserTestCompilationParams{UserTestID: ut.ID, DatasetID: ds.ID, Attempt: 1}
	for i, want := range []int64{1, 0} {
		n, err = qs.SetUserTestCompilation(ctx, comp)
		wantRows(t, fmt.Sprintf("failed SetUserTestCompilation, delivery %d", i+1), n, err, want)
	}
	comp = q.SetUserTestCompilationParams{UserTestID: ut.ID, DatasetID: ds.ID, CompilationOutcome: ptr("ok"), CompilationText: []string{"OK"}, Attempt: 2}
	n, err = qs.SetUserTestCompilation(ctx, comp)
	wantRows(t, "SetUserTestCompilation", n, err, 1)
	n, err = qs.SetUserTestCompilation(ctx, comp)
	wantRows(t, "duplicate SetUserTestCompilation", n, err, 0)
	for range 2 {
		n, err = qs.CreateUserTestExecutable(ctx, q.CreateUserTestExecutableParams{UserTestID: ut.ID, DatasetID: ds.ID, Filename: "aaa", Digest: digest("exe")})
		check(t, err)
	}
	wantRows(t, "duplicate CreateUserTestExecutable", n, err, 0)
	exes := must[[]q.UserTestExecutable](t)(qs.ListUserTestExecutables(ctx, q.ListUserTestExecutablesParams{UserTestID: ut.ID, DatasetID: ds.ID}))
	if len(exes) != 1 {
		t.Fatalf("ListUserTestExecutables = %+v", exes)
	}
	if got := must[q.UserTestExecutable](t)(qs.GetUserTestExecutable(ctx, exes[0].ID)); got != exes[0] {
		t.Fatalf("GetUserTestExecutable = %+v", got)
	}
	failed := q.SetUserTestEvaluationParams{UserTestID: ut.ID, DatasetID: ds.ID, Attempt: 1}
	for i, want := range []int64{1, 0} {
		n, err = qs.SetUserTestEvaluation(ctx, failed)
		wantRows(t, fmt.Sprintf("failed SetUserTestEvaluation, delivery %d", i+1), n, err, want)
	}
	eval := q.SetUserTestEvaluationParams{
		UserTestID: ut.ID, DatasetID: ds.ID, EvaluationOutcome: ptr("ok"), EvaluationText: []string{"Execution completed successfully"},
		Output: ptr(digest("output")), ExecutionTime: ptr(0.01), ExecutionMemory: ptr(int64(4 << 20)), Attempt: 2,
	}
	n, err = qs.SetUserTestEvaluation(ctx, eval)
	wantRows(t, "SetUserTestEvaluation", n, err, 1)
	n, err = qs.SetUserTestEvaluation(ctx, eval)
	wantRows(t, "duplicate SetUserTestEvaluation", n, err, 0)
	r := must[q.UserTestResult](t)(qs.GetUserTestResult(ctx, key))
	if *r.Output != digest("output") || r.EvaluationTries != 2 || r.CompilationTries != 2 {
		t.Fatalf("GetUserTestResult = %+v", r)
	}
	if got := must[[]q.ListUserTestResultsRow](t)(qs.ListUserTestResults(ctx, ut.ID)); len(got) != 1 || got[0].DatasetDescription != "default" {
		t.Fatalf("ListUserTestResults = %+v", got)
	}

	mine := must[[]q.ListUserTestsByParticipationTaskRow](t)(qs.ListUserTestsByParticipationTask(ctx,
		q.ListUserTestsByParticipationTaskParams{ParticipationID: p.ID, TaskID: a.ID}))
	if len(mine) != 2 || mine[0].UserTest.ID != ut.ID || *mine[0].Output != digest("output") || mine[1].CompilationOutcome != nil {
		t.Fatalf("ListUserTestsByParticipationTask = %+v", mine)
	}
	page := must[[]q.ListUserTestsByContestRow](t)(qs.ListUserTestsByContest(ctx, q.ListUserTestsByContestParams{ContestID: c.ID, PageSize: 2}))
	if len(page) != 2 || page[0].UserTest.ID != tests[2].ID || page[0].TaskName != "bbb" || page[1].Username != "alice" {
		t.Fatalf("ListUserTestsByContest = %+v", page)
	}
	last := page[1].UserTest
	page = must[[]q.ListUserTestsByContestRow](t)(qs.ListUserTestsByContest(ctx, q.ListUserTestsByContestParams{
		ContestID: c.ID, BeforeTimestamp: &last.Timestamp, BeforeID: &last.ID, PageSize: 2,
	}))
	if len(page) != 1 || page[0].UserTest.ID != ut.ID || *page[0].EvaluationOutcome != "ok" {
		t.Fatalf("ListUserTestsByContest, second page = %+v", page)
	}
	stats := must[q.GetUserTestStatsRow](t)(qs.GetUserTestStats(ctx, q.GetUserTestStatsParams{
		ParticipationID: p.ID, TaskID: a.ID, ContestID: c.ID,
	}))
	if stats.TaskCount != 2 || stats.ContestCount != 3 || !stats.TaskLast.Equal(tests[1].Timestamp) || !stats.ContestLast.Equal(tests[2].Timestamp) {
		t.Fatalf("GetUserTestStats = %+v", stats)
	}

	n, err = qs.DeleteUserTestExecutables(ctx, q.DeleteUserTestExecutablesParams{UserTestID: ut.ID, DatasetID: ds.ID})
	wantRows(t, "DeleteUserTestExecutables", n, err, 1)
	n, err = qs.DeleteUserTestResult(ctx, q.DeleteUserTestResultParams{UserTestID: ut.ID, DatasetID: ds.ID})
	wantRows(t, "DeleteUserTestResult", n, err, 1)
	n, err = qs.DeleteUserTestFile(ctx, files[0].ID)
	wantRows(t, "DeleteUserTestFile", n, err, 1)
	n, err = qs.DeleteUserTestManager(ctx, managers[0].ID)
	wantRows(t, "DeleteUserTestManager", n, err, 1)
	n, err = qs.DeleteUserTest(ctx, ut.ID)
	wantRows(t, "DeleteUserTest", n, err, 1)
	if got := must[[]q.UserTestManager](t)(qs.ListUserTestManagers(ctx, ut.ID)); len(got) != 0 {
		t.Fatalf("managers of a deleted user test = %+v", got)
	}
}
