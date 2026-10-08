// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package judging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/db"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
)

// t0 is the start of the test contests.
var t0 = time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)

// fx is a test contest on a fresh database, with a local blob store for
// the files.
type fx struct {
	t       testing.TB
	ctx     context.Context
	pool    *pgxpool.Pool
	qc      *db.QueryCounter
	j       *Judge
	blobs   *blob.Local
	contest int32
	group   int32
	n       int
}

func newFx(t testing.TB) *fx {
	t.Helper()
	pool, qc := testDB(t)
	blobs, err := blob.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &fx{t: t, ctx: context.Background(), pool: pool, qc: qc, blobs: blobs}
	f.j = New(pool, Options{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:    func() time.Time { return t0.Add(5 * time.Hour) },
	})
	f.contest = f.id(`INSERT INTO contests (name, description, languages) VALUES ('ioi', 'IOI', '{"C++17 / g++","Java / JDK"}') RETURNING id`)
	f.group = f.id(`INSERT INTO groups (name, contest_id, start, stop) VALUES ('main', $1, $2, $3) RETURNING id`,
		f.contest, t0, t0.Add(5*time.Hour))
	f.exec(`UPDATE contests SET main_group_id = $1 WHERE id = $2`, f.group, f.contest)
	return f
}

func (f *fx) exec(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.pool.Exec(f.ctx, sql, args...); err != nil {
		f.t.Fatalf("%s: %v", sql, err)
	}
}

func (f *fx) id(sql string, args ...any) int32 {
	f.t.Helper()
	var id int32
	if err := f.pool.QueryRow(f.ctx, sql, args...).Scan(&id); err != nil {
		f.t.Fatalf("%s: %v", sql, err)
	}
	return id
}

func scalar[T any](f *fx, sql string, args ...any) T {
	f.t.Helper()
	var v T
	if err := f.pool.QueryRow(f.ctx, sql, args...).Scan(&v); err != nil {
		f.t.Fatalf("%s: %v", sql, err)
	}
	return v
}

// put stores content in the blob store and returns its digest.
func (f *fx) put(content string) string {
	f.t.Helper()
	d, err := f.blobs.PutBytes(f.ctx, []byte(content))
	if err != nil {
		f.t.Fatal(err)
	}
	return d
}

// tc describes a testcase.
type tc struct {
	codename string
	public   bool
}

// dsOpts describes a dataset.
type dsOpts struct {
	description string
	autojudge   bool
	taskType    string
	params      string
	scoreType   string
	scoreParams string
	testcases   []tc
	managers    map[string]string // filename -> content
	timeLimit   float64
	memoryLimit int64
}

// taskOpts describes a task and its active dataset.
type taskOpts struct {
	name      string
	scoreMode string
	precision int
	format    []string
	dsOpts
}

// tk is a task created by task.
type tk struct {
	id      int32
	dataset int32 // the active dataset
}

func (o *dsOpts) defaults() {
	if o.taskType == "" {
		o.taskType = "Batch"
	}
	if o.params == "" {
		o.params = `["alone", ["", ""], "diff"]`
	}
	if o.scoreType == "" {
		o.scoreType = "Sum"
	}
	if o.scoreParams == "" {
		o.scoreParams = "10"
	}
	if o.testcases == nil {
		o.testcases = []tc{{"000", true}, {"001", false}, {"002", false}}
	}
	if o.timeLimit == 0 {
		o.timeLimit = 1
	}
	if o.memoryLimit == 0 {
		o.memoryLimit = 256 << 20
	}
}

// task creates a task of the contest with its active dataset.
func (f *fx) task(o taskOpts) tk {
	f.t.Helper()
	f.n++
	if o.name == "" {
		o.name = fmt.Sprintf("task%d", f.n)
	}
	if o.scoreMode == "" {
		o.scoreMode = "max"
	}
	if o.format == nil {
		o.format = []string{o.name + ".%l"}
	}
	id := f.id(`INSERT INTO tasks (num, contest_id, name, title, submission_format, score_mode, score_precision)
		VALUES ($1, $2, $3, $3, $4, $5, $6) RETURNING id`, f.n, f.contest, o.name, o.format, o.scoreMode, o.precision)
	if o.description == "" {
		o.description = "default"
	}
	ds := f.dataset(id, o.dsOpts)
	f.exec(`UPDATE tasks SET active_dataset_id = $1 WHERE id = $2`, ds, id)
	return tk{id, ds}
}

// dataset adds a dataset to a task.
func (f *fx) dataset(task int32, o dsOpts) int32 {
	f.t.Helper()
	o.defaults()
	if o.description == "" {
		f.n++
		o.description = fmt.Sprintf("dataset %d", f.n)
	}
	ds := f.id(`INSERT INTO datasets (task_id, description, autojudge, time_limit, memory_limit,
			task_type, task_type_parameters, score_type, score_type_parameters)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
		task, o.description, o.autojudge, o.timeLimit, o.memoryLimit, o.taskType, o.params, o.scoreType, o.scoreParams)
	for _, c := range o.testcases {
		f.exec(`INSERT INTO testcases (dataset_id, codename, public, input, output) VALUES ($1, $2, $3, $4, $5)`,
			ds, c.codename, c.public, f.put("input "+c.codename), f.put("output "+c.codename))
	}
	for name, content := range o.managers {
		f.exec(`INSERT INTO managers (dataset_id, filename, digest) VALUES ($1, $2, $3)`, ds, name, f.put(content))
	}
	return ds
}

// participant creates a user and their participation in the contest.
func (f *fx) participant(username string) int32 {
	f.t.Helper()
	u := f.id(`INSERT INTO users (first_name, last_name, username, password) VALUES ($1, $1, $1, 'plaintext:x') RETURNING id`, username)
	return f.id(`INSERT INTO participations (contest_id, user_id, group_id) VALUES ($1, $2, $3) RETURNING id`,
		f.contest, u, f.group)
}

// submit creates a submission of p to task at t0+at with one source file.
func (f *fx) submit(p int32, task tk, at time.Duration) int32 {
	f.t.Helper()
	f.n++
	name := scalar[string](f, `SELECT submission_format[1] FROM tasks WHERE id = $1`, task.id)
	s := f.id(`INSERT INTO submissions (opaque_id, participation_id, task_id, timestamp, language)
		VALUES ($1, $2, $3, $4, 'C++17 / g++') RETURNING id`, f.n, p, task.id, t0.Add(at))
	f.exec(`INSERT INTO files (submission_id, filename, digest) VALUES ($1, $2, $3)`,
		s, name, f.put(fmt.Sprintf("int main() { return %d; }", f.n)))
	return s
}

// userTest creates a user test of p on task with one source file and the
// given managers.
func (f *fx) userTest(p int32, task tk, at time.Duration, managers map[string]string) int32 {
	f.t.Helper()
	f.n++
	name := scalar[string](f, `SELECT submission_format[1] FROM tasks WHERE id = $1`, task.id)
	u := f.id(`INSERT INTO user_tests (participation_id, task_id, timestamp, language, input)
		VALUES ($1, $2, $3, 'C++17 / g++', $4) RETURNING id`, p, task.id, t0.Add(at), f.put(fmt.Sprintf("test input %d", f.n)))
	f.exec(`INSERT INTO user_test_files (user_test_id, filename, digest) VALUES ($1, $2, $3)`,
		u, name, f.put(fmt.Sprintf("int main() { return %d; }", f.n)))
	for name, content := range managers {
		f.exec(`INSERT INTO user_test_managers (user_test_id, filename, digest) VALUES ($1, $2, $3)`, u, name, f.put(content))
	}
	return u
}

// token plays a token on a submission and refreshes its task score, as the
// contestant site does.
func (f *fx) token(sub int32, at time.Duration) []TaskScoreChange {
	f.t.Helper()
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	var k TaskKey
	if err := tx.QueryRow(f.ctx, `INSERT INTO tokens (submission_id, timestamp) VALUES ($1, $2)
		RETURNING (SELECT participation_id FROM submissions WHERE id = $1), (SELECT task_id FROM submissions WHERE id = $1)`,
		sub, t0.Add(at)).Scan(&k.ParticipationID, &k.TaskID); err != nil {
		f.t.Fatal(err)
	}
	ch, err := RefreshTaskScores(f.ctx, tx, []TaskKey{k})
	if err != nil {
		f.t.Fatal(err)
	}
	if err := tx.Commit(f.ctx); err != nil {
		f.t.Fatal(err)
	}
	return ch
}

// --- a simulated worker ---

// verdict decides the result of a job: an outcome for evaluations
// ("1.0"), a compilation outcome ("ok"/"fail") for compilations, or ""
// for an infrastructure failure.
type verdict func(j *job.Job) string

// allCorrect compiles everything and gives every testcase 1.0.
func allCorrect(j *job.Job) string {
	if j.Operation.Type.IsCompilation() {
		return job.CompilationOK
	}
	return "1.0"
}

// resultOf is what a worker returns for j with the verdict v.
func resultOf(j *job.Job, v string) *job.Result {
	r := j.NewResult("worker-1")
	r.Durations.Total = time.Millisecond
	if v == "" {
		r.Fail(fmt.Errorf("sandbox broken"))
		return r
	}
	r.Success = true
	r.Stats = &job.Stats{ExecutionTime: job.Ptr(0.25), ExecutionWallClockTime: job.Ptr(0.5),
		ExecutionMemory: job.Ptr(int64(8 << 20)), ExitStatus: job.ExitOK}
	switch j.Operation.Type {
	case job.Compilation, job.UserTestCompilation:
		r.CompilationOutcome = v
		if v == job.CompilationOK {
			r.Text = []string{"Compilation succeeded"}
			r.Executables = map[string]string{"main": digestOf(j.Operation.Key() + "@" + fmt.Sprint(j.Generation))}
		} else {
			r.Text = []string{"Compilation failed"}
		}
		r.Stats.Stdout, r.Stats.Stderr = "out", "err"
	case job.Evaluation:
		r.Outcome = v
		r.Text = []string{"Output is correct"}
		if v != "1.0" {
			r.Text = []string{"Output isn't correct"}
		}
		r.AdminText = "checker said " + v
	case job.UserTestEvaluation:
		r.Text = []string{"Execution completed successfully"}
		r.UserOutput = digestOf("output of " + j.Operation.Key())
	}
	return r
}

func digestOf(s string) string { return blob.Digest([]byte(s)) }

// judge runs the dispatcher loop on ops until nothing is left: build the
// jobs, run them with v, apply the results. It returns every change.
func (f *fx) judge(ops []Operation, v verdict) []*Changes {
	f.t.Helper()
	var all []*Changes
	for round := 0; len(ops) > 0; round++ {
		if round > 50 {
			f.t.Fatal("the judging loop does not end")
		}
		b, err := f.j.BuildJobs(f.ctx, ops)
		if err != nil {
			f.t.Fatal(err)
		}
		if len(b.Jobs) == 0 {
			break
		}
		results := make([]*job.Result, len(b.Jobs))
		for i, jb := range b.Jobs {
			results[i] = resultOf(jb, v(jb))
		}
		ch, err := f.j.Apply(f.ctx, results)
		if err != nil {
			f.t.Fatal(err)
		}
		all = append(all, ch)
		ops = ch.Next
	}
	return all
}

// judgeAll judges everything pending for the submissions (and user tests).
func (f *fx) judgeAll(t Targets, v verdict) []*Changes {
	f.t.Helper()
	w, err := f.j.Pending(f.ctx, t)
	if err != nil {
		f.t.Fatal(err)
	}
	all := f.judge(w.Ops, v)
	if len(w.ToScore) > 0 {
		ch, err := f.j.Score(f.ctx, w.ToScore)
		if err != nil {
			f.t.Fatal(err)
		}
		all = append(all, ch)
	}
	return all
}

// result returns the state of a submission result.
type resultState struct {
	CompilationOutcome *string
	CompilationTries   int32
	EvaluationOutcome  *string
	EvaluationTries    int32
	Generation         int64
	Score              *float64
	PublicScore        *float64
	Evaluations        int
	Executables        int
}

func (f *fx) result(sub, ds int32) *resultState {
	f.t.Helper()
	var r resultState
	err := f.pool.QueryRow(f.ctx, `SELECT compilation_outcome, compilation_tries, evaluation_outcome, evaluation_tries,
			generation, score, public_score,
			(SELECT count(*) FROM evaluations e WHERE e.submission_id = sr.submission_id AND e.dataset_id = sr.dataset_id),
			(SELECT count(*) FROM executables e WHERE e.submission_id = sr.submission_id AND e.dataset_id = sr.dataset_id)
		FROM submission_results sr WHERE submission_id = $1 AND dataset_id = $2`, sub, ds).Scan(
		&r.CompilationOutcome, &r.CompilationTries, &r.EvaluationOutcome, &r.EvaluationTries,
		&r.Generation, &r.Score, &r.PublicScore, &r.Evaluations, &r.Executables)
	if err != nil {
		return nil
	}
	return &r
}

func (r *resultState) status() Status {
	if r == nil {
		return StatusCompiling
	}
	return statusOf(r.CompilationOutcome, r.EvaluationOutcome, r.Score != nil)
}

// cached returns the cached task score of p on task (nil: no row).
func (f *fx) cached(p int32, task tk) *TaskScore {
	f.t.Helper()
	var ts TaskScore
	var ds *int32
	err := f.pool.QueryRow(f.ctx, `SELECT dataset_id, score, public_score, tokened_score, partial
		FROM task_scores WHERE participation_id = $1 AND task_id = $2`, p, task.id).Scan(
		&ds, &ts.Score, &ts.PublicScore, &ts.TokenedScore, &ts.Partial)
	if err != nil {
		return nil
	}
	ts.DatasetID = orZero(ds)
	return &ts
}

// opKeys returns the keys of operations, sorted, with their priority.
func opKeys(ops []Operation) []string {
	out := make([]string, len(ops))
	for i, o := range ops {
		out[i] = fmt.Sprintf("%s p%d a%d", o.Key(), o.Priority, o.Attempt)
		if o.Retry {
			out[i] += " retry"
		}
	}
	slices.Sort(out)
	return out
}

func sameKeys(t *testing.T, what string, got, want []string) {
	t.Helper()
	got, want = slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(want))
	if !slices.Equal(got, want) {
		t.Errorf("%s:\n got  %s\n want %s", what, strings.Join(got, ", "), strings.Join(want, ", "))
	}
}
