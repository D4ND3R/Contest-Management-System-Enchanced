//go:build sandbox

// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package dispatcher

import (
	"context"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/kv"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// The contest of the end-to-end and chaos tests (make test-sandbox): one
// Batch task "sum" (read two integers, print their sum) inserted through
// internal/store as the administration does, judged by real workers in
// isolate, with a solution for every verdict in C++ and Python 3.
//
// The active dataset "main" has five testcases and a GroupMin score type
// (40 points for the public group 1_*, 60 for 2_*); with alt set, the task
// also has a dataset "alt" judged in the background (autojudge) with four
// testcases worth 25 points each (Sum). The partial solutions add in 32
// bits, so they fail exactly the testcases whose sum does not fit.

const (
	langCPP = "C++17 / g++"
	langPy  = "Python 3 / CPython"
	// sumFormat is the submission format of the task.
	sumFormat = "sum.%l"
)

// sumTestcase is a testcase of the task: its input and the sum.
type sumTestcase struct {
	codename string
	a, b     int64
	public   bool
}

func (tc sumTestcase) input() string  { return fmt.Sprintf("%d %d\n", tc.a, tc.b) }
func (tc sumTestcase) output() string { return fmt.Sprintf("%d\n", tc.a+tc.b) }

var (
	mainTestcases = []sumTestcase{
		{"1_01", 1, 2, true}, {"1_02", 3, 4, true},
		{"2_01", 2000000000, 1000000000, false}, {"2_02", 123456789, 987654321, false}, {"2_03", -5, 5, false},
	}
	altTestcases = []sumTestcase{
		{"a", 2, 2, true}, {"b", 2000000000, 2000000000, false}, {"c", 0, 0, false}, {"d", -7, -8, false},
	}
)

// sumVerdict is what a solution does on every testcase.
type sumVerdict string

const (
	vCorrect sumVerdict = "correct"
	vPartial sumVerdict = "partial" // 32-bit sum
	vWrong   sumVerdict = "wrong"
	vTLE     sumVerdict = "tle"
	vSignal  sumVerdict = "signal" // killed by SIGSEGV
	vExit    sumVerdict = "exit"   // nonzero exit status
	vCE      sumVerdict = "ce"     // does not compile
)

// solution is a source of the task.
type solution struct {
	name    string
	lang    string
	verdict sumVerdict
	source  string
}

var solutions = []solution{
	{"cpp_ok", langCPP, vCorrect, `#include <cstdio>
int main() { long long a, b; if (scanf("%lld %lld", &a, &b) != 2) return 1; printf("%lld\n", a + b); }
`},
	{"py_ok", langPy, vCorrect, `a, b = map(int, input().split())
print(a + b)
`},
	{"cpp_partial", langCPP, vPartial, `#include <cstdio>
int main() { int a, b; if (scanf("%d %d", &a, &b) != 2) return 1; printf("%d\n", (int)((unsigned)a + (unsigned)b)); }
`},
	{"py_partial", langPy, vPartial, `a, b = map(int, input().split())
print((a + b + 2**31) % 2**32 - 2**31)
`},
	{"cpp_wrong", langCPP, vWrong, `#include <cstdio>
int main() { long long a, b; if (scanf("%lld %lld", &a, &b) != 2) return 1; printf("%lld\n", a + b + 1); }
`},
	{"py_wrong", langPy, vWrong, `a, b = map(int, input().split())
print(a + b - 1)
`},
	{"cpp_tle", langCPP, vTLE, `int main() { volatile unsigned long x = 0; for (;;) x++; }
`},
	{"cpp_segv", langCPP, vSignal, `int main() { volatile int *p = 0; *p = 1; return 0; }
`},
	{"py_exit", langPy, vExit, `a, b = map(int, input().split())
raise ValueError("no sum today")
`},
	{"cpp_ce", langCPP, vCE, `int main( { return 0; }
`},
	{"py_ce", langPy, vCE, `def main(:
    pass
`},
}

// solutionNamed returns a solution of the table.
func solutionNamed(t testing.TB, name string) solution {
	t.Helper()
	for _, s := range solutions {
		if s.name == name {
			return s
		}
	}
	t.Fatalf("no solution %q", name)
	return solution{}
}

// variant returns the source with a comment that makes it unique, so that
// every submission compiles for real (the compilation cache of the workers
// only skips identical compilations).
func (s solution) variant(n int) string {
	if n == 0 {
		return s.source
	}
	if s.lang == langPy {
		return s.source + fmt.Sprintf("# variant %d\n", n)
	}
	return s.source + fmt.Sprintf("// variant %d\n", n)
}

// expect is the outcome of a solution on a testcase: the outcome and the
// start of the first text of the evaluation.
func (s solution) expect(tc sumTestcase) (outcome float64, text string) {
	switch s.verdict {
	case vCorrect:
		return 1, "Output is correct"
	case vPartial:
		if sum := tc.a + tc.b; sum >= math.MinInt32 && sum <= math.MaxInt32 {
			return 1, "Output is correct"
		}
		return 0, "Output isn't correct"
	case vWrong:
		return 0, "Output isn't correct"
	case vTLE:
		return 0, "Execution timed out"
	case vSignal:
		return 0, "Execution killed by signal"
	case vExit:
		return 0, "Execution failed because the return code was nonzero"
	}
	panic("no evaluation for " + s.name)
}

// score returns the score and public score of a solution on "main" (alt
// false) or "alt".
func (s solution) score(alt bool) (score, public float64) {
	if s.verdict == vCE {
		return 0, 0
	}
	if alt {
		for _, tc := range altTestcases {
			o, _ := s.expect(tc)
			score += 25 * o
			if tc.public {
				public += 25 * o
			}
		}
		return score, public
	}
	group := func(prefix string) float64 {
		m := 1.0
		for _, tc := range mainTestcases {
			if strings.HasPrefix(tc.codename, prefix) {
				o, _ := s.expect(tc)
				m = min(m, o)
			}
		}
		return m
	}
	g1, g2 := 40*group("1_"), 60*group("2_")
	return g1 + g2, g1
}

// sumContest is the contest in a test database.
type sumContest struct {
	t     testing.TB
	ctx   context.Context
	st    *store.Store
	kv    *kv.Client
	blobs blob.Store

	contest q.Contest
	task    q.Task
	// main is the active dataset; alt (ID 0 without it) the background
	// one.
	main, alt q.Dataset
	parts     map[string]q.Participation
}

func must[T any](t testing.TB) func(T, error) T {
	return func(v T, err error) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

func ptr[T any](v T) *T { return &v }

// newSumContest creates the contest, its task and datasets and one
// participation per user, storing the testcases in blobs.
func newSumContest(t testing.TB, pool *pgxpool.Pool, kvc *kv.Client, blobs blob.Store, alt bool, users ...string) *sumContest {
	t.Helper()
	ctx := context.Background()
	c := &sumContest{t: t, ctx: ctx, st: store.New(pool), kv: kvc, blobs: blobs, parts: map[string]q.Participation{}}
	put := func(s string) string { return must[string](t)(blobs.PutBytes(ctx, []byte(s))) }
	err := c.st.InTx(ctx, func(qq *q.Queries) error {
		var err error
		c.contest, err = qq.CreateContest(ctx, q.CreateContestParams{
			Name: "e2e", Description: "End to end", Languages: []string{langCPP, langPy},
			AllowUserTests: true, AllowPasswordAuthentication: true, TokenMode: "disabled",
			TokenGenInitial: 2, TokenGenNumber: 2, TokenGenInterval: 30 * time.Minute,
		})
		if err != nil {
			return err
		}
		start := time.Now().Add(-time.Hour)
		g, err := qq.CreateGroup(ctx, q.CreateGroupParams{ContestID: c.contest.ID, Name: "main", Start: start,
			Stop: start.Add(5 * time.Hour), AnalysisStart: start.Add(6 * time.Hour), AnalysisStop: start.Add(7 * time.Hour)})
		if err != nil {
			return err
		}
		if _, err := qq.SetContestMainGroup(ctx, q.SetContestMainGroupParams{ID: c.contest.ID, MainGroupID: &g.ID}); err != nil {
			return err
		}
		c.task, err = qq.CreateTask(ctx, q.CreateTaskParams{
			ContestID: &c.contest.ID, Num: ptr[int32](0), Name: "sum", Title: "Sum",
			SubmissionFormat: []string{sumFormat}, TokenMode: "disabled", TokenGenInitial: 2, TokenGenNumber: 2,
			TokenGenInterval: 30 * time.Minute, FeedbackLevel: "full", ScoreMode: "max",
		})
		if err != nil {
			return err
		}
		dataset := func(desc string, autojudge bool, limit float64, scoreType, params string, tcs []sumTestcase) (q.Dataset, error) {
			ds, err := qq.CreateDataset(ctx, q.CreateDatasetParams{
				TaskID: c.task.ID, Description: desc, Autojudge: autojudge, TimeLimit: &limit,
				MemoryLimit: ptr(int64(256 << 20)), TaskType: "Batch",
				TaskTypeParameters: []byte(`["alone", ["", ""], "diff"]`),
				ScoreType:          scoreType, ScoreTypeParameters: []byte(params),
			})
			if err != nil {
				return ds, err
			}
			rows := make([]q.CreateTestcasesParams, 0, len(tcs))
			for _, tc := range tcs {
				rows = append(rows, q.CreateTestcasesParams{DatasetID: ds.ID, Codename: tc.codename, Public: tc.public,
					Input: put(tc.input()), Output: put(tc.output())})
			}
			_, err = qq.CreateTestcases(ctx, rows)
			return ds, err
		}
		if c.main, err = dataset("main", false, 1, "GroupMin", `[[40, 2], [60, 3]]`, mainTestcases); err != nil {
			return err
		}
		if alt {
			if c.alt, err = dataset("alt", true, 0.5, "Sum", `25`, altTestcases); err != nil {
				return err
			}
		}
		if _, err := qq.SetTaskActiveDataset(ctx, q.SetTaskActiveDatasetParams{ID: c.task.ID, ActiveDatasetID: &c.main.ID}); err != nil {
			return err
		}
		for _, name := range users {
			u, err := qq.CreateUser(ctx, q.CreateUserParams{FirstName: name, LastName: name, Username: name,
				Password: "plaintext:" + name})
			if err != nil {
				return err
			}
			p, err := qq.CreateParticipation(ctx, q.CreateParticipationParams{ContestID: c.contest.ID, UserID: u.ID, GroupID: g.ID})
			if err != nil {
				return err
			}
			c.parts[name] = p
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// datasets returns the datasets that judge submissions, with their
// testcases.
func (c *sumContest) datasets() map[int32][]sumTestcase {
	m := map[int32][]sumTestcase{c.main.ID: mainTestcases}
	if c.alt.ID != 0 {
		m[c.alt.ID] = altTestcases
	}
	return m
}

// submit does what the contestant site does on a submission: it stores
// the file, inserts the submission and refreshes the task score of the
// participant in one transaction, then notifies the dispatcher.
func (c *sumContest) submit(user string, sol solution, variant int) int32 {
	c.t.Helper()
	p, ok := c.parts[user]
	if !ok {
		c.t.Fatalf("no participant %q", user)
	}
	digest := must[string](c.t)(c.blobs.PutBytes(c.ctx, []byte(sol.variant(variant))))
	var id int32
	err := c.st.RunTx(c.ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		qq := c.st.Q.WithTx(tx)
		lang := sol.lang
		s, err := store.CreateSubmission(c.ctx, qq, q.CreateSubmissionParams{ParticipationID: p.ID, TaskID: c.task.ID,
			Timestamp: time.Now(), Language: &lang, Official: true})
		if err != nil {
			return err
		}
		if _, err := qq.CreateFiles(c.ctx, q.CreateFilesParams{SubmissionID: s.ID, Filenames: []string{sumFormat},
			Digests: []string{digest}}); err != nil {
			return err
		}
		_, err = judging.RefreshTaskScores(c.ctx, tx, []judging.TaskKey{{ParticipationID: p.ID, TaskID: c.task.ID}})
		id = s.ID
		return err
	})
	if err != nil {
		c.t.Fatal(err)
	}
	if err := judging.Notify(c.ctx, c.kv, judging.Event{Kind: judging.EventSubmission, SubmissionIDs: []int32{id}}); err != nil {
		c.t.Fatal(err)
	}
	return id
}

// userTest does what the contestant site does on a user test.
func (c *sumContest) userTest(user string, sol solution, input string) int32 {
	c.t.Helper()
	p := c.parts[user]
	src := must[string](c.t)(c.blobs.PutBytes(c.ctx, []byte(sol.source)))
	in := must[string](c.t)(c.blobs.PutBytes(c.ctx, []byte(input)))
	var id int32
	err := c.st.InTx(c.ctx, func(qq *q.Queries) error {
		lang := sol.lang
		ut, err := qq.CreateUserTest(c.ctx, q.CreateUserTestParams{ParticipationID: p.ID, TaskID: c.task.ID,
			Timestamp: time.Now(), Language: &lang, Input: in})
		if err != nil {
			return err
		}
		id = ut.ID
		_, err = qq.CreateUserTestFiles(c.ctx, q.CreateUserTestFilesParams{UserTestID: ut.ID,
			Filenames: []string{sumFormat}, Digests: []string{src}})
		return err
	})
	if err != nil {
		c.t.Fatal(err)
	}
	if err := judging.Notify(c.ctx, c.kv, judging.Event{Kind: judging.EventUserTest, UserTestIDs: []int32{id}}); err != nil {
		c.t.Fatal(err)
	}
	return id
}

// readBlob returns the content of a blob.
func (c *sumContest) readBlob(digest string) string {
	c.t.Helper()
	rc, err := c.blobs.Open(c.ctx, digest)
	if err != nil {
		c.t.Fatal(err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		c.t.Fatal(err)
	}
	return string(b)
}

// judged is the stored state of a submission on a dataset.
type judged struct {
	Compilation *string
	CompTries   int32
	EvalTries   int32
	Evaluated   bool
	Score       *float64
	Public      *float64
	// Evaluations by testcase codename: outcome and first text.
	Evaluations map[string][2]string
	// Rows counts the evaluation rows (one per testcase, never more).
	Rows int
}

// snapshot reads the results of every submission of the task on every
// dataset in two queries: submission id -> dataset id -> state.
func (c *sumContest) snapshot() map[int32]map[int32]*judged {
	c.t.Helper()
	out := map[int32]map[int32]*judged{}
	rows, err := c.st.Pool.Query(c.ctx, `SELECT sr.submission_id, sr.dataset_id, sr.compilation_outcome, sr.compilation_tries,
			sr.evaluation_tries, sr.evaluation_outcome IS NOT NULL, sr.score, sr.public_score
		FROM submission_results sr JOIN submissions s ON s.id = sr.submission_id WHERE s.task_id = $1`, c.task.ID)
	if err != nil {
		c.t.Fatal(err)
	}
	for rows.Next() {
		var sub, ds int32
		j := &judged{Evaluations: map[string][2]string{}}
		if err := rows.Scan(&sub, &ds, &j.Compilation, &j.CompTries, &j.EvalTries, &j.Evaluated, &j.Score, &j.Public); err != nil {
			c.t.Fatal(err)
		}
		if out[sub] == nil {
			out[sub] = map[int32]*judged{}
		}
		out[sub][ds] = j
	}
	if err := rows.Err(); err != nil {
		c.t.Fatal(err)
	}
	rows, err = c.st.Pool.Query(c.ctx, `SELECT e.submission_id, e.dataset_id, t.codename, coalesce(e.outcome, ''), coalesce(e.text[1], '')
		FROM evaluations e JOIN testcases t ON t.id = e.testcase_id JOIN submissions s ON s.id = e.submission_id
		WHERE s.task_id = $1`, c.task.ID)
	if err != nil {
		c.t.Fatal(err)
	}
	for rows.Next() {
		var sub, ds int32
		var codename, outcome, text string
		if err := rows.Scan(&sub, &ds, &codename, &outcome, &text); err != nil {
			c.t.Fatal(err)
		}
		j := out[sub][ds]
		if j == nil {
			c.t.Fatalf("evaluation of submission %d on dataset %d without a result", sub, ds)
		}
		j.Rows++
		j.Evaluations[codename] = [2]string{outcome, text}
	}
	if err := rows.Err(); err != nil {
		c.t.Fatal(err)
	}
	return out
}

// checkJudged checks the state of a submission of sol on every dataset:
// compiled once (or failed), one evaluation per testcase with the
// expected outcome, scored as expected, no failed attempt. It returns the
// problems found.
func (c *sumContest) checkJudged(state map[int32]*judged, sol solution) []string {
	var errs []string
	for ds, tcs := range c.datasets() {
		j := state[ds]
		if j == nil {
			errs = append(errs, fmt.Sprintf("dataset %d: no result", ds))
			continue
		}
		wantComp := "ok"
		if sol.verdict == vCE {
			wantComp = "fail"
		}
		if j.Compilation == nil || *j.Compilation != wantComp {
			errs = append(errs, fmt.Sprintf("dataset %d: compilation %v, want %s", ds, deref(j.Compilation), wantComp))
		}
		// compilation_tries is the highest attempt seen (D23; upstream
		// counts only the failed ones), evaluation_tries the highest
		// attempt that failed: one attempt each, none failed.
		if j.CompTries != 1 || j.EvalTries != 0 {
			errs = append(errs, fmt.Sprintf("dataset %d: compilation attempts %d (want 1), failed evaluation attempts %d",
				ds, j.CompTries, j.EvalTries))
		}
		alt := ds != c.main.ID
		score, public := sol.score(alt)
		if j.Score == nil || math.Abs(*j.Score-score) > 1e-9 || j.Public == nil || math.Abs(*j.Public-public) > 1e-9 {
			errs = append(errs, fmt.Sprintf("dataset %d: score %v public %v, want %v and %v", ds, deref(j.Score), deref(j.Public), score, public))
		}
		if sol.verdict == vCE {
			if j.Rows != 0 || j.Evaluated {
				errs = append(errs, fmt.Sprintf("dataset %d: %d evaluations of a compilation error", ds, j.Rows))
			}
			continue
		}
		if !j.Evaluated || j.Rows != len(tcs) {
			errs = append(errs, fmt.Sprintf("dataset %d: evaluated %v with %d evaluation rows for %d testcases", ds, j.Evaluated, j.Rows, len(tcs)))
		}
		for _, tc := range tcs {
			o, text := sol.expect(tc)
			got, ok := j.Evaluations[tc.codename]
			if !ok {
				errs = append(errs, fmt.Sprintf("dataset %d: testcase %s not evaluated", ds, tc.codename))
				continue
			}
			if f, err := strconv.ParseFloat(got[0], 64); err != nil || f != o || !strings.HasPrefix(got[1], text) {
				errs = append(errs, fmt.Sprintf("dataset %d testcase %s: outcome %q %q, want %v %q", ds, tc.codename, got[0], got[1], o, text))
			}
		}
	}
	return errs
}

func deref[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}

// cachedScore is a row of the task score cache.
type cachedScore struct {
	Dataset                *int32
	Score, Public, Tokened float64
	Partial                bool
}

// taskScores reads the task score cache of the task by username.
func (c *sumContest) taskScores() map[string]cachedScore {
	c.t.Helper()
	rows, err := c.st.Pool.Query(c.ctx, `SELECT u.username, ts.dataset_id, ts.score, ts.public_score, ts.tokened_score, ts.partial
		FROM task_scores ts JOIN participations p ON p.id = ts.participation_id JOIN users u ON u.id = p.user_id
		WHERE ts.task_id = $1`, c.task.ID)
	if err != nil {
		c.t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]cachedScore{}
	for rows.Next() {
		var name string
		var s cachedScore
		if err := rows.Scan(&name, &s.Dataset, &s.Score, &s.Public, &s.Tokened, &s.Partial); err != nil {
			c.t.Fatal(err)
		}
		out[name] = s
	}
	return out
}

// wantTaskScores is the task score cache expected after judging: per
// participant, the best score on the active dataset (score mode max).
func (c *sumContest) wantTaskScores(submitted map[string][]solution) map[string]cachedScore {
	out := map[string]cachedScore{}
	for user, sols := range submitted {
		s := cachedScore{Dataset: &c.main.ID}
		for _, sol := range sols {
			score, public := sol.score(false)
			s.Score, s.Public = max(s.Score, score), max(s.Public, public)
		}
		out[user] = s
	}
	return out
}

// unfinished counts the results of the task that are not scored yet.
func (c *sumContest) unfinished() int {
	var n int
	err := c.st.Pool.QueryRow(c.ctx, `SELECT
			(SELECT count(*) FROM submissions s CROSS JOIN datasets d
				WHERE s.task_id = $1 AND d.task_id = $1 AND (d.id = $2 OR d.autojudge)
				AND NOT EXISTS (SELECT 1 FROM submission_results sr WHERE sr.submission_id = s.id AND sr.dataset_id = d.id
					AND sr.score IS NOT NULL AND sr.public_score IS NOT NULL))`, c.task.ID, c.main.ID).Scan(&n)
	if err != nil {
		c.t.Fatal(err)
	}
	return n
}

// sortedKeys returns the keys of a map in order.
func sortedKeys[K interface{ ~int32 | ~string }, V any](m map[K]V) []K {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
