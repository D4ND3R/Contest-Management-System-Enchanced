// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package store_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/multitracer"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/db"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
)

// TestMain checks, after a full run, that every query of queries/*.sql
// ran at least once against PostgreSQL.
func TestMain(m *testing.M) {
	code := m.Run()
	if code == 0 && fullRun() {
		missing, err := queriesNotRun()
		if err != nil {
			fmt.Fprintln(os.Stderr, "checking that every query ran:", err)
			code = 1
		} else if len(missing) > 0 {
			fmt.Fprintf(os.Stderr, "FAIL: these queries never ran in the tests: %s\n", strings.Join(missing, ", "))
			code = 1
		}
	}
	os.Exit(code)
}

// fullRun reports whether the tests ran against a database without a
// -run or -skip filter.
func fullRun() bool {
	if os.Getenv(testutil.EnvDatabaseURL) == "" {
		return false
	}
	for _, name := range []string{"test.run", "test.skip"} {
		if f := flag.Lookup(name); f != nil && f.Value.String() != "" {
			return false
		}
	}
	return true
}

// recorder is a pgx tracer that notes the sqlc queries that run: sqlc
// starts the SQL of each query with "-- name: <Name> :<kind>". A COPY has
// no SQL: it is noted as "copy:<table>".
type recorder struct {
	mu   sync.Mutex
	seen map[string]bool
}

var executed = &recorder{seen: map[string]bool{}}

var sqlcName = regexp.MustCompile(`^-- name: (\w+) :`)

func (r *recorder) note(key string) {
	r.mu.Lock()
	r.seen[key] = true
	r.mu.Unlock()
}

func (r *recorder) noteSQL(sql string) {
	if m := sqlcName.FindStringSubmatch(sql); m != nil {
		r.note(m[1])
	}
}

func (r *recorder) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	r.noteSQL(d.SQL)
	return ctx
}
func (r *recorder) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
func (r *recorder) TraceBatchStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchStartData) context.Context {
	return ctx
}
func (r *recorder) TraceBatchQuery(_ context.Context, _ *pgx.Conn, d pgx.TraceBatchQueryData) {
	r.noteSQL(d.SQL)
}
func (r *recorder) TraceBatchEnd(context.Context, *pgx.Conn, pgx.TraceBatchEndData) {}
func (r *recorder) TraceCopyFromStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceCopyFromStartData) context.Context {
	r.note("copy:" + strings.Join(d.TableName, "."))
	return ctx
}
func (r *recorder) TraceCopyFromEnd(context.Context, *pgx.Conn, pgx.TraceCopyFromEndData) {}

var (
	queryHeader = regexp.MustCompile(`(?m)^-- name: (\w+) :(\w+)`)
	insertInto  = regexp.MustCompile(`INSERT INTO (\w+)`)
)

// queriesNotRun lists the queries of queries/*.sql that the recorder did
// not see.
func queriesNotRun() ([]string, error) {
	files, err := filepath.Glob(filepath.Join("queries", "*.sql"))
	if err != nil || len(files) == 0 {
		return nil, fmt.Errorf("no query files found: %v", err)
	}
	executed.mu.Lock()
	defer executed.mu.Unlock()
	var missing []string
	for _, f := range files {
		// The queries of the judging core, of the importer and of the
		// contestant site are exercised by the tests of internal/judging,
		// internal/importer and internal/contestweb.
		if b := filepath.Base(f); strings.HasPrefix(b, "judging") || strings.HasPrefix(b, "import") ||
			strings.HasPrefix(b, "contestweb") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		text := string(data)
		heads := queryHeader.FindAllStringSubmatchIndex(text, -1)
		for i, h := range heads {
			name, kind := text[h[2]:h[3]], text[h[4]:h[5]]
			key := name
			if kind == "copyfrom" {
				end := len(text)
				if i+1 < len(heads) {
					end = heads[i+1][0]
				}
				m := insertInto.FindStringSubmatch(text[h[1]:end])
				if m == nil {
					return nil, fmt.Errorf("%s: no INSERT INTO in %s", f, name)
				}
				key = "copy:" + m[1]
			}
			if !executed.seen[key] {
				missing = append(missing, name)
			}
		}
	}
	sort.Strings(missing)
	return missing, nil
}

// newStore returns a Store on a fresh migrated database whose pool counts
// its queries and records them for TestMain.
func newStore(t testing.TB) (*store.Store, *db.QueryCounter) {
	t.Helper()
	qc := new(db.QueryCounter)
	pool := testutil.DBWithConfig(t, func(cfg *pgxpool.Config) {
		qc.Install(cfg)
		cfg.ConnConfig.Tracer = multitracer.New(cfg.ConnConfig.Tracer, executed)
	})
	return store.New(pool), qc
}

// t0 is the start of the test contests.
var t0 = time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)

// digest returns the SHA-256 of s in hex, a valid digest column value.
func digest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func ptr[T any](v T) *T { return &v }

func must[T any](t testing.TB) func(T, error) T {
	return func(v T, err error) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

func check(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// wantRows checks the rows affected by an :execrows query.
func wantRows(t testing.TB, what string, n int64, err error, want int64) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	if n != want {
		t.Fatalf("%s: %d rows affected, want %d", what, n, want)
	}
}

// wantNoRows checks that a :one query found nothing.
func wantNoRows(t testing.TB, what string, err error) {
	t.Helper()
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("%s: got error %v, want pgx.ErrNoRows", what, err)
	}
}

// fixture builds rows for the tests with the generated queries.
type fixture struct {
	t   testing.TB
	ctx context.Context
	s   *store.Store
	q   *q.Queries
}

func newFixture(t testing.TB) (*fixture, *db.QueryCounter) {
	s, qc := newStore(t)
	return &fixture{t: t, ctx: context.Background(), s: s, q: s.Q}, qc
}

// contestParams are the values the CMS admin uses for a new contest.
func contestParams(name string) q.CreateContestParams {
	return q.CreateContestParams{
		Name:                        name,
		Description:                 "Contest " + name,
		Languages:                   []string{"C11 / gcc", "C++20 / g++"},
		SubmissionsDownloadAllowed:  true,
		AllowQuestions:              true,
		AllowUserTests:              true,
		AllowPasswordAuthentication: true,
		IpRestriction:               true,
		TokenMode:                   "infinite",
		TokenGenInitial:             2,
		TokenGenNumber:              2,
		TokenGenInterval:            30 * time.Minute,
	}
}

func groupParams(contestID int32, name string) q.CreateGroupParams {
	return q.CreateGroupParams{
		ContestID:     contestID,
		Name:          name,
		Start:         t0,
		Stop:          t0.Add(5 * time.Hour),
		AnalysisStart: t0.Add(6 * time.Hour),
		AnalysisStop:  t0.Add(8 * time.Hour),
	}
}

func taskParams(contestID *int32, num *int32, name string) q.CreateTaskParams {
	return q.CreateTaskParams{
		ContestID:        contestID,
		Num:              num,
		Name:             name,
		Title:            "Task " + name,
		SubmissionFormat: []string{name + ".%l"},
		TokenMode:        "disabled",
		TokenGenInitial:  2,
		TokenGenNumber:   2,
		TokenGenInterval: 30 * time.Minute,
		FeedbackLevel:    "restricted",
		ScoreMode:        "max_tokened_last",
	}
}

func datasetParams(taskID int32, description string) q.CreateDatasetParams {
	return q.CreateDatasetParams{
		TaskID:              taskID,
		Description:         description,
		TimeLimit:           ptr(1.0),
		MemoryLimit:         ptr(int64(256 << 20)),
		TaskType:            "Batch",
		TaskTypeParameters:  []byte(`["alone", ["", ""], "diff"]`),
		ScoreType:           "Sum",
		ScoreTypeParameters: []byte(`100`),
	}
}

// contest creates a contest and its main group.
func (f *fixture) contest(name string) (q.Contest, q.Group) {
	f.t.Helper()
	c := must[q.Contest](f.t)(f.q.CreateContest(f.ctx, contestParams(name)))
	g := must[q.Group](f.t)(f.q.CreateGroup(f.ctx, groupParams(c.ID, "default")))
	n, err := f.q.SetContestMainGroup(f.ctx, q.SetContestMainGroupParams{ID: c.ID, MainGroupID: &g.ID})
	wantRows(f.t, "SetContestMainGroup", n, err, 1)
	c.MainGroupID = &g.ID
	return c, g
}

func (f *fixture) user(username string) q.User {
	f.t.Helper()
	return must[q.User](f.t)(f.q.CreateUser(f.ctx, q.CreateUserParams{
		FirstName: "First " + username,
		LastName:  "Last " + username,
		Username:  username,
		Password:  "plaintext:" + username,
	}))
}

func (f *fixture) participation(c q.Contest, g q.Group, u q.User, team *int32) q.Participation {
	f.t.Helper()
	return must[q.Participation](f.t)(f.q.CreateParticipation(f.ctx, q.CreateParticipationParams{
		ContestID: c.ID, UserID: u.ID, GroupID: g.ID, TeamID: team,
	}))
}

// task creates a task of contest c at position num with an active
// dataset.
func (f *fixture) task(c q.Contest, num int32, name string) (q.Task, q.Dataset) {
	f.t.Helper()
	tk := must[q.Task](f.t)(f.q.CreateTask(f.ctx, taskParams(&c.ID, &num, name)))
	ds := must[q.Dataset](f.t)(f.q.CreateDataset(f.ctx, datasetParams(tk.ID, "default")))
	tk = must[q.Task](f.t)(f.q.SetTaskActiveDataset(f.ctx, q.SetTaskActiveDatasetParams{ID: tk.ID, ActiveDatasetID: &ds.ID}))
	return tk, ds
}

func (f *fixture) testcase(ds q.Dataset, codename string, public bool) q.Testcase {
	f.t.Helper()
	return must[q.Testcase](f.t)(f.q.CreateTestcase(f.ctx, q.CreateTestcaseParams{
		DatasetID: ds.ID, Codename: codename, Public: public,
		Input: digest("in " + codename), Output: digest("out " + codename),
	}))
}

func (f *fixture) submission(p q.Participation, tk q.Task, at time.Time) q.Submission {
	f.t.Helper()
	return must[q.Submission](f.t)(store.CreateSubmission(f.ctx, f.q, q.CreateSubmissionParams{
		ParticipationID: p.ID, TaskID: tk.ID, Timestamp: at, Language: ptr("C++20 / g++"), Official: true,
	}))
}

// compile stores the compilation outcome of s on ds (first attempt).
func (f *fixture) compile(s q.Submission, ds q.Dataset, outcome string) {
	f.t.Helper()
	n, err := f.q.SetSubmissionCompilation(f.ctx, q.SetSubmissionCompilationParams{
		SubmissionID: s.ID, DatasetID: ds.ID, CompilationOutcome: &outcome, Attempt: 1,
	})
	wantRows(f.t, "SetSubmissionCompilation", n, err, 1)
}

// score stores the evaluation outcome and a score of s on ds.
func (f *fixture) score(s q.Submission, ds q.Dataset, score float64) {
	f.t.Helper()
	n, err := f.q.SetSubmissionEvaluation(f.ctx, q.SetSubmissionEvaluationParams{
		SubmissionID: s.ID, DatasetID: ds.ID, EvaluationOutcome: ptr("ok"), Attempt: 1,
	})
	wantRows(f.t, "SetSubmissionEvaluation", n, err, 1)
	n, err = f.q.SetSubmissionScore(f.ctx, scoreParams(s, ds, score))
	wantRows(f.t, "SetSubmissionScore", n, err, 1)
}

func scoreParams(s q.Submission, ds q.Dataset, score float64) q.SetSubmissionScoreParams {
	return q.SetSubmissionScoreParams{
		SubmissionID: s.ID, DatasetID: ds.ID,
		Score: score, ScoreDetails: []byte(`[]`),
		PublicScore: score, PublicScoreDetails: []byte(`[]`),
		RankingScoreDetails: []string{fmt.Sprint(score)},
		ScoredAt:            t0.Add(time.Hour),
	}
}
