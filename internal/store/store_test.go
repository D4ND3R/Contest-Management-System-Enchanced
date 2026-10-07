// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/store"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

func TestInTxCommitsAndRollsBack(t *testing.T) {
	f, _ := newFixture(t)
	ctx, s := f.ctx, f.s

	var id int32
	check(t, s.InTx(ctx, func(tx *q.Queries) error {
		c, err := tx.CreateContest(ctx, contestParams("kept"))
		id = c.ID
		return err
	}))
	if _, err := s.Q.GetContest(ctx, id); err != nil {
		t.Fatalf("committed contest: %v", err)
	}

	boom := errors.New("boom")
	err := s.InTx(ctx, func(tx *q.Queries) error {
		c, err := tx.CreateContest(ctx, contestParams("dropped"))
		id = c.ID
		check(t, err)
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("InTx = %v, want boom", err)
	}
	_, err = s.Q.GetContest(ctx, id)
	wantNoRows(t, "contest of a rolled back transaction", err)

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("the panic did not propagate")
			}
		}()
		_ = s.InTx(ctx, func(tx *q.Queries) error {
			c, err := tx.CreateContest(ctx, contestParams("panicked"))
			id = c.ID
			check(t, err)
			panic("boom")
		})
	}()
	_, err = s.Q.GetContest(ctx, id)
	wantNoRows(t, "contest of a panicked transaction", err)
}

func TestInTxRetries(t *testing.T) {
	f, _ := newFixture(t)
	ctx, s := f.ctx, f.s

	for _, code := range []string{"40001", "40P01"} {
		calls := 0
		err := s.InTx(ctx, func(tx *q.Queries) error {
			calls++
			if _, err := tx.CreateContest(ctx, contestParams("c"+code+strconv.Itoa(calls))); err != nil {
				return err
			}
			if calls < 3 {
				return &pgconn.PgError{Code: code}
			}
			return nil
		})
		check(t, err)
		if calls != 3 {
			t.Fatalf("%s: %d calls, want 3", code, calls)
		}
		// Only the last attempt committed.
		if list := must[[]q.Contest](t)(s.Q.ListContests(ctx)); list[len(list)-1].Name != "c"+code+"3" {
			t.Fatalf("%s: contests %+v", code, list)
		}
		check(t, deleteAllContests(t, s))
	}

	// It gives up after TxRetries retries.
	calls := 0
	err := s.InTx(ctx, func(*q.Queries) error {
		calls++
		return &pgconn.PgError{Code: "40001"}
	})
	if !store.Retryable(err) || calls != store.TxRetries+1 {
		t.Fatalf("InTx = %v after %d calls, want a serialization failure after %d", err, calls, store.TxRetries+1)
	}
	// Other errors are not retried.
	calls = 0
	err = s.InTx(ctx, func(*q.Queries) error {
		calls++
		return &pgconn.PgError{Code: "23505"}
	})
	if store.Retryable(err) || calls != 1 {
		t.Fatalf("InTx = %v after %d calls, want one call", err, calls)
	}
	// A cancelled context stops the retries.
	cctx, cancel := context.WithCancel(ctx)
	calls = 0
	err = s.InTx(cctx, func(*q.Queries) error {
		calls++
		cancel()
		return &pgconn.PgError{Code: "40001"}
	})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("InTx = %v after %d calls, want context.Canceled after 1", err, calls)
	}
}

func deleteAllContests(t *testing.T, s *store.Store) error {
	t.Helper()
	_, err := s.Pool.Exec(context.Background(), `DELETE FROM contests WHERE id > 0`)
	return err
}

// TestRunTxRetriesRealConflicts makes two SERIALIZABLE transactions
// increment the same counter at the same time: PostgreSQL aborts one with
// a serialization failure, RunTx runs it again and no increment is lost.
func TestRunTxRetriesRealConflicts(t *testing.T) {
	f, _ := newFixture(t)
	ctx, s := f.ctx, f.s
	_, err := s.Q.SetServerSetting(ctx, q.SetServerSettingParams{Key: "test.counter", Value: json.RawMessage(`0`)})
	check(t, err)

	var (
		wg        sync.WaitGroup
		bothRead  sync.WaitGroup
		mu        sync.Mutex
		attempts  int
		errs      = make([]error, 2)
		serialize = pgx.TxOptions{IsoLevel: pgx.Serializable}
	)
	bothRead.Add(2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			first := true
			errs[i] = s.RunTx(ctx, serialize, func(tx pgx.Tx) error {
				mu.Lock()
				attempts++
				mu.Unlock()
				var v int
				if err := tx.QueryRow(ctx, `SELECT value::int FROM server_settings WHERE key = 'test.counter'`).Scan(&v); err != nil {
					return err
				}
				if first {
					// Both read the same value before either writes.
					first = false
					bothRead.Done()
					bothRead.Wait()
				}
				_, err := tx.Exec(ctx, `UPDATE server_settings SET value = to_jsonb($1::int) WHERE key = 'test.counter'`, v+1)
				return err
			})
		}()
	}
	wg.Wait()
	for _, err := range errs {
		check(t, err)
	}
	got := must[q.ServerSetting](t)(s.Q.GetServerSetting(ctx, "test.counter"))
	if string(got.Value) != "2" || attempts != 3 {
		t.Fatalf("counter = %s after %d attempts, want 2 after 3", got.Value, attempts)
	}
}

func TestRetryableAndUniqueViolation(t *testing.T) {
	for _, tc := range []struct {
		err       error
		retryable bool
		unique    bool
	}{
		{nil, false, false},
		{errors.New("x"), false, false},
		{&pgconn.PgError{Code: "40001"}, true, false},
		{fmt.Errorf("wrapped: %w", &pgconn.PgError{Code: "40P01"}), true, false},
		{&pgconn.PgError{Code: "23505", ConstraintName: "users_username_key"}, false, true},
	} {
		if got := store.Retryable(tc.err); got != tc.retryable {
			t.Errorf("Retryable(%v) = %v", tc.err, got)
		}
		if got := store.IsUniqueViolation(tc.err, ""); got != tc.unique {
			t.Errorf("IsUniqueViolation(%v, \"\") = %v", tc.err, got)
		}
	}
	err := &pgconn.PgError{Code: "23505", ConstraintName: "users_username_key"}
	if !store.IsUniqueViolation(err, "users_username_key") || store.IsUniqueViolation(err, "teams_code_key") {
		t.Error("IsUniqueViolation does not match the constraint name")
	}
}

func TestCreateSubmissionDrawsAgainOnCollision(t *testing.T) {
	f, _ := newFixture(t)
	ctx := f.ctx
	c, g := f.contest("ioi")
	tk, _ := f.task(c, 0, "aaa")
	p := f.participation(c, g, f.user("alice"), nil)

	draws := []int64{42, 42, 42, 7}
	store.SetOpaqueIDDraw(t, func() int64 {
		v := draws[0]
		draws = draws[1:]
		return v
	})
	arg := q.CreateSubmissionParams{ParticipationID: p.ID, TaskID: tk.ID, Timestamp: t0, OpaqueID: 99}
	first := must[q.Submission](t)(store.CreateSubmission(ctx, f.q, arg))
	second := must[q.Submission](t)(store.CreateSubmission(ctx, f.q, arg))
	if first.OpaqueID != 42 || second.OpaqueID != 7 || len(draws) != 0 {
		t.Fatalf("opaque ids %d, %d (draws left %v), want 42, then 7 after two collisions", first.OpaqueID, second.OpaqueID, draws)
	}

	store.SetOpaqueIDDraw(t, func() int64 { return 42 })
	if _, err := store.CreateSubmission(ctx, f.q, arg); err == nil {
		t.Fatal("CreateSubmission succeeded although every draw collides")
	}
	// Another participant may reuse the opaque id.
	p2 := f.participation(c, g, f.user("bob"), nil)
	arg.ParticipationID = p2.ID
	if got := must[q.Submission](t)(store.CreateSubmission(ctx, f.q, arg)); got.OpaqueID != 42 {
		t.Fatalf("opaque id %d, want 42", got.OpaqueID)
	}
	if id := store.NewOpaqueID(); id < 0 {
		t.Fatalf("NewOpaqueID = %d", id)
	}
}

// submissionsFixture builds one submission in each status:
//
//	s1 alice aaa  +1m  compiling (no result); scored on the second dataset
//	s2 alice aaa  +2m  compilation failed
//	s3 bob   aaa  +3m  evaluating
//	s4 bob   bbb  +4m  scoring
//	s5 carol aaa  +5m  scored, tokened
//	s6 carol aaa  +5m  scored (same time as s5: the id breaks the tie)
//
// plus a scored submission in another contest.
type submissionsFixture struct {
	c                      q.Contest
	a, b                   q.Task
	dsA, dsA2              q.Dataset
	alice, bob, carol      q.Participation
	s1, s2, s3, s4, s5, s6 q.Submission
}

func newSubmissionsFixture(f *fixture) *submissionsFixture {
	x := &submissionsFixture{}
	var g q.Group
	x.c, g = f.contest("ioi")
	x.a, x.dsA = f.task(x.c, 0, "aaa")
	var dsB q.Dataset
	x.b, dsB = f.task(x.c, 1, "bbb")
	x.dsA2 = must[q.Dataset](f.t)(f.q.CreateDataset(f.ctx, datasetParams(x.a.ID, "second")))
	x.alice = f.participation(x.c, g, f.user("alice"), nil)
	x.bob = f.participation(x.c, g, f.user("bob"), nil)
	x.carol = f.participation(x.c, g, f.user("carol"), nil)

	x.s1 = f.submission(x.alice, x.a, t0.Add(1*time.Minute))
	f.compile(x.s1, x.dsA2, "ok")
	f.score(x.s1, x.dsA2, 10)
	x.s2 = f.submission(x.alice, x.a, t0.Add(2*time.Minute))
	f.compile(x.s2, x.dsA, "fail")
	x.s3 = f.submission(x.bob, x.a, t0.Add(3*time.Minute))
	f.compile(x.s3, x.dsA, "ok")
	x.s4 = f.submission(x.bob, x.b, t0.Add(4*time.Minute))
	f.compile(x.s4, dsB, "ok")
	_, err := f.q.SetSubmissionEvaluation(f.ctx, q.SetSubmissionEvaluationParams{SubmissionID: x.s4.ID, DatasetID: dsB.ID, EvaluationOutcome: ptr("ok"), Attempt: 1})
	check(f.t, err)
	x.s5 = f.submission(x.carol, x.a, t0.Add(5*time.Minute))
	f.compile(x.s5, x.dsA, "ok")
	f.score(x.s5, x.dsA, 100)
	must[q.Token](f.t)(f.q.CreateToken(f.ctx, q.CreateTokenParams{SubmissionID: x.s5.ID, Timestamp: t0.Add(6 * time.Minute)}))
	x.s6 = f.submission(x.carol, x.a, t0.Add(5*time.Minute))
	f.compile(x.s6, x.dsA, "ok")
	f.score(x.s6, x.dsA, 50)

	other, og := f.contest("other")
	ot, ods := f.task(other, 0, "zzz")
	os := f.submission(f.participation(other, og, f.user("dave"), nil), ot, t0.Add(time.Hour))
	f.compile(os, ods, "ok")
	f.score(os, ods, 1)
	return x
}

func ids(rows []store.SubmissionRow) []int32 {
	var out []int32
	for _, r := range rows {
		out = append(out, r.Submission.ID)
	}
	return out
}

func subIDs(subs ...q.Submission) []int32 {
	var out []int32
	for _, s := range subs {
		out = append(out, s.ID)
	}
	return out
}

func TestListSubmissionsFilters(t *testing.T) {
	f, _ := newFixture(t)
	ctx, s := f.ctx, f.s
	x := newSubmissionsFixture(f)

	for _, tc := range []struct {
		name   string
		filter store.SubmissionFilter
		want   []q.Submission
	}{
		{"contest", store.SubmissionFilter{ContestID: x.c.ID}, []q.Submission{x.s6, x.s5, x.s4, x.s3, x.s2, x.s1}},
		{"task", store.SubmissionFilter{TaskID: x.a.ID}, []q.Submission{x.s6, x.s5, x.s3, x.s2, x.s1}},
		{"participation", store.SubmissionFilter{ParticipationID: x.bob.ID}, []q.Submission{x.s4, x.s3}},
		{"participation and task", store.SubmissionFilter{ParticipationID: x.bob.ID, TaskID: x.b.ID}, []q.Submission{x.s4}},
		{"contest and task", store.SubmissionFilter{ContestID: x.c.ID, TaskID: x.b.ID}, []q.Submission{x.s4}},
		{"compiling", store.SubmissionFilter{ContestID: x.c.ID, Status: store.StatusCompiling}, []q.Submission{x.s1}},
		{"compilation failed", store.SubmissionFilter{ContestID: x.c.ID, Status: store.StatusCompilationFailed}, []q.Submission{x.s2}},
		{"evaluating", store.SubmissionFilter{ContestID: x.c.ID, Status: store.StatusEvaluating}, []q.Submission{x.s3}},
		{"scoring", store.SubmissionFilter{ContestID: x.c.ID, Status: store.StatusScoring}, []q.Submission{x.s4}},
		{"scored", store.SubmissionFilter{ContestID: x.c.ID, Status: store.StatusScored}, []q.Submission{x.s6, x.s5}},
		{"dataset", store.SubmissionFilter{DatasetID: x.dsA2.ID}, []q.Submission{x.s6, x.s5, x.s3, x.s2, x.s1}},
		{"dataset and status", store.SubmissionFilter{DatasetID: x.dsA2.ID, Status: store.StatusScored}, []q.Submission{x.s1}},
		{"dataset and other task", store.SubmissionFilter{DatasetID: x.dsA2.ID, TaskID: x.b.ID}, nil},
		{"everything", store.SubmissionFilter{}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := must[[]store.SubmissionRow](t)(s.ListSubmissions(ctx, tc.filter))
			want := subIDs(tc.want...)
			if tc.name == "everything" {
				if len(rows) != 7 {
					t.Fatalf("%d rows, want 7", len(rows))
				}
				return
			}
			if got := ids(rows); !slices.Equal(got, want) {
				t.Fatalf("ids %v, want %v", got, want)
			}
			n := must[int64](t)(s.CountSubmissions(ctx, tc.filter))
			if n != int64(len(want)) {
				t.Fatalf("CountSubmissions = %d, want %d", n, len(want))
			}
			for _, r := range rows {
				if tc.filter.Status != "" && r.Status() != tc.filter.Status {
					t.Fatalf("row %d has status %s", r.Submission.ID, r.Status())
				}
			}
		})
	}

	// The fields of a row.
	rows := must[[]store.SubmissionRow](t)(s.ListSubmissions(ctx, store.SubmissionFilter{TaskID: x.a.ID}))
	s5 := rows[1]
	if s5.Submission.ID != x.s5.ID || s5.Username != "carol" || s5.FirstName != "First carol" || s5.UserID == 0 ||
		s5.TaskName != "aaa" || s5.TaskTitle != "Task aaa" || *s5.ContestID != x.c.ID || *s5.DatasetID != x.dsA.ID ||
		s5.TokenTimestamp == nil || !s5.TokenTimestamp.Equal(t0.Add(6*time.Minute)) ||
		*s5.Result.Score != 100 || *s5.Result.PublicScore != 100 || s5.Result.CompilationTries != 1 ||
		s5.Result.EvaluationTries != 1 || !s5.Result.Scored || s5.Status() != store.StatusScored ||
		s5.Submission.OpaqueID != x.s5.OpaqueID || *s5.Submission.Language != "C++20 / g++" {
		t.Fatalf("row = %+v, result %+v", s5, s5.Result)
	}
	s1 := rows[4]
	if s1.Result != nil || s1.Status() != store.StatusCompiling || s1.TokenTimestamp != nil {
		t.Fatalf("row of a submission without result = %+v", s1)
	}
	// On the second dataset the same submission is scored.
	rows = must[[]store.SubmissionRow](t)(s.ListSubmissions(ctx, store.SubmissionFilter{DatasetID: x.dsA2.ID}))
	if last := rows[len(rows)-1]; *last.DatasetID != x.dsA2.ID || last.Status() != store.StatusScored || *last.Result.Score != 10 {
		t.Fatalf("row on the second dataset = %+v", last)
	}

	// Inside a transaction.
	check(t, s.RunTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		rows, err := store.ListSubmissions(ctx, tx, store.SubmissionFilter{ParticipationID: x.alice.ID})
		if err == nil && len(rows) != 2 {
			err = fmt.Errorf("%d rows in the transaction, want 2", len(rows))
		}
		if err == nil {
			var n int64
			n, err = store.CountSubmissions(ctx, tx, store.SubmissionFilter{ParticipationID: x.alice.ID})
			if err == nil && n != 2 {
				err = fmt.Errorf("count %d in the transaction, want 2", n)
			}
		}
		return err
	}))

	// Bad filters.
	if _, err := s.ListSubmissions(ctx, store.SubmissionFilter{Status: "done"}); err == nil {
		t.Fatal("ListSubmissions accepted an unknown status")
	}
	if _, err := s.CountSubmissions(ctx, store.SubmissionFilter{Status: "done"}); err == nil {
		t.Fatal("CountSubmissions accepted an unknown status")
	}
	for _, limit := range []int{-1, store.MaxPageSize + 1} {
		if _, err := s.ListSubmissions(ctx, store.SubmissionFilter{Limit: limit}); err == nil {
			t.Fatalf("ListSubmissions accepted limit %d", limit)
		}
	}
}

func TestListSubmissionsPages(t *testing.T) {
	f, qc := newFixture(t)
	ctx, s := f.ctx, f.s
	x := newSubmissionsFixture(f)
	want := subIDs(x.s6, x.s5, x.s4, x.s3, x.s2, x.s1)

	for _, size := range []int{1, 2, 4, 6, 10} {
		var got []int32
		filter := store.SubmissionFilter{ContestID: x.c.ID, Limit: size}
		pages := 0
		for {
			qc.Reset()
			rows := must[[]store.SubmissionRow](t)(s.ListSubmissions(ctx, filter))
			if qc.Count() != 1 {
				t.Fatalf("a page ran %d queries", qc.Count())
			}
			if len(rows) > size {
				t.Fatalf("page of %d rows with limit %d", len(rows), size)
			}
			got = append(got, ids(rows)...)
			if len(rows) < size {
				break
			}
			pages++
			cur := rows[len(rows)-1].Cursor()
			filter.Before = &cur
		}
		if !slices.Equal(got, want) {
			t.Fatalf("pages of %d: %v, want %v", size, got, want)
		}
	}
	// The default page size.
	for i := range store.DefaultPageSize {
		f.submission(x.alice, x.b, t0.Add(time.Duration(i)*time.Second))
	}
	rows := must[[]store.SubmissionRow](t)(s.ListSubmissions(ctx, store.SubmissionFilter{ContestID: x.c.ID}))
	if len(rows) != store.DefaultPageSize {
		t.Fatalf("default page has %d rows, want %d", len(rows), store.DefaultPageSize)
	}
	if n := must[int64](t)(s.CountSubmissions(ctx, store.SubmissionFilter{ContestID: x.c.ID})); n != int64(store.DefaultPageSize+6) {
		t.Fatalf("CountSubmissions = %d", n)
	}
}

func TestStatusOf(t *testing.T) {
	ok, fail := ptr("ok"), ptr("fail")
	for _, tc := range []struct {
		comp, eval *string
		scored     bool
		want       store.SubmissionStatus
	}{
		{nil, nil, false, store.StatusCompiling},
		{fail, nil, true, store.StatusCompilationFailed},
		{ok, nil, false, store.StatusEvaluating},
		{ok, ok, false, store.StatusScoring},
		{ok, ok, true, store.StatusScored},
	} {
		if got := store.StatusOf(tc.comp, tc.eval, tc.scored); got != tc.want {
			t.Errorf("StatusOf(%v, %v, %v) = %s, want %s", tc.comp, tc.eval, tc.scored, got, tc.want)
		}
		if !tc.want.Valid() {
			t.Errorf("%s is not Valid", tc.want)
		}
	}
	if store.SubmissionStatus("x").Valid() {
		t.Error(`"x" is Valid`)
	}
	// A result missing one of the five score columns is not scored (CMS).
	r := q.SubmissionResult{CompilationOutcome: ok, EvaluationOutcome: ok, Score: ptr(1.0), ScoreDetails: []byte(`[]`),
		PublicScore: ptr(1.0), PublicScoreDetails: []byte(`[]`)}
	if got := store.ResultStatus(r); got != store.StatusScoring {
		t.Errorf("ResultStatus without ranking details = %s", got)
	}
	r.RankingScoreDetails = []string{}
	if got := store.ResultStatus(r); got != store.StatusScored {
		t.Errorf("ResultStatus = %s", got)
	}
}
