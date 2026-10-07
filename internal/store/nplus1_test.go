// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package store_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/store"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
)

// listCase builds n rows that a list shows and returns the call that
// lists them and the number of rows it returned.
type listCase struct {
	name  string
	build func(f *fixture, n int) func() (int, error)
}

// contestWithSubmissions builds a contest with n participants, each with a
// scored, tokened submission and a question.
func contestWithSubmissions(f *fixture, n int) (q.Contest, q.Task, q.Dataset, []q.Participation, []q.Submission) {
	c, g := f.contest("ioi")
	tk, ds := f.task(c, 0, "aaa")
	team := must[q.Team](f.t)(f.q.CreateTeam(f.ctx, q.CreateTeamParams{Code: "ARG", Name: "Argentina"}))
	var ps []q.Participation
	var subs []q.Submission
	for i := range n {
		p := f.participation(c, g, f.user(fmt.Sprintf("user%02d", i)), &team.ID)
		s := f.submission(p, tk, t0.Add(time.Duration(i)*time.Minute))
		f.compile(s, ds, "ok")
		f.score(s, ds, float64(i))
		must[q.Token](f.t)(f.q.CreateToken(f.ctx, q.CreateTokenParams{SubmissionID: s.ID, Timestamp: s.Timestamp}))
		ps = append(ps, p)
		subs = append(subs, s)
	}
	return c, tk, ds, ps, subs
}

var listCases = []listCase{
	{"ListParticipationsByContest", func(f *fixture, n int) func() (int, error) {
		c, _, _, _, _ := contestWithSubmissions(f, n)
		return func() (int, error) {
			rows, err := f.q.ListParticipationsByContest(f.ctx, c.ID)
			return len(rows), err
		}
	}},
	{"ListParticipationsByUser", func(f *fixture, n int) func() (int, error) {
		u := f.user("alice")
		for i := range n {
			c, g := f.contest(fmt.Sprintf("c%02d", i))
			f.participation(c, g, u, nil)
		}
		return func() (int, error) {
			rows, err := f.q.ListParticipationsByUser(f.ctx, u.ID)
			return len(rows), err
		}
	}},
	{"ListTasksByContest", func(f *fixture, n int) func() (int, error) {
		c, _ := f.contest("ioi")
		for i := range n {
			f.task(c, int32(i), fmt.Sprintf("t%02d", i))
		}
		return func() (int, error) {
			rows, err := f.q.ListTasksByContest(f.ctx, c.ID)
			return len(rows), err
		}
	}},
	{"ListTasks", func(f *fixture, n int) func() (int, error) {
		c, _ := f.contest("ioi")
		for i := range n {
			f.task(c, int32(i), fmt.Sprintf("t%02d", i))
		}
		return func() (int, error) {
			rows, err := f.q.ListTasks(f.ctx)
			return len(rows), err
		}
	}},
	{"ListActiveDatasetsByContest", func(f *fixture, n int) func() (int, error) {
		c, _ := f.contest("ioi")
		for i := range n {
			f.task(c, int32(i), fmt.Sprintf("t%02d", i))
		}
		return func() (int, error) {
			rows, err := f.q.ListActiveDatasetsByContest(f.ctx, c.ID)
			return len(rows), err
		}
	}},
	{"ListStatementsByContest", func(f *fixture, n int) func() (int, error) {
		c, _ := f.contest("ioi")
		for i := range n {
			tk, _ := f.task(c, int32(i), fmt.Sprintf("t%02d", i))
			must[q.Statement](f.t)(f.q.CreateStatement(f.ctx, q.CreateStatementParams{TaskID: tk.ID, Language: "en", Digest: digest(tk.Name)}))
		}
		return func() (int, error) {
			rows, err := f.q.ListStatementsByContest(f.ctx, c.ID)
			return len(rows), err
		}
	}},
	{"ListTestcasesByDataset", func(f *fixture, n int) func() (int, error) {
		c, _ := f.contest("ioi")
		_, ds := f.task(c, 0, "aaa")
		for i := range n {
			f.testcase(ds, fmt.Sprintf("%03d", i), i%2 == 0)
		}
		return func() (int, error) {
			rows, err := f.q.ListTestcasesByDataset(f.ctx, ds.ID)
			return len(rows), err
		}
	}},
	{"ListEvaluations", func(f *fixture, n int) func() (int, error) {
		c, g := f.contest("ioi")
		tk, ds := f.task(c, 0, "aaa")
		s := f.submission(f.participation(c, g, f.user("alice"), nil), tk, t0)
		f.compile(s, ds, "ok")
		for i := range n {
			tc := f.testcase(ds, fmt.Sprintf("%03d", i), false)
			_, err := f.q.CreateEvaluation(f.ctx, q.CreateEvaluationParams{SubmissionID: s.ID, DatasetID: ds.ID, TestcaseID: tc.ID, Outcome: ptr("1.0")})
			check(f.t, err)
		}
		return func() (int, error) {
			rows, err := f.q.ListEvaluations(f.ctx, q.ListEvaluationsParams{SubmissionID: s.ID, DatasetID: ds.ID})
			return len(rows), err
		}
	}},
	{"ListSubmissions (store, contest)", func(f *fixture, n int) func() (int, error) {
		c, _, _, _, _ := contestWithSubmissions(f, n)
		return func() (int, error) {
			rows, err := f.s.ListSubmissions(f.ctx, store.SubmissionFilter{ContestID: c.ID, Limit: 100})
			for _, r := range rows {
				if r.Result == nil || r.TokenTimestamp == nil || r.Username == "" {
					return 0, fmt.Errorf("incomplete row %+v", r)
				}
			}
			return len(rows), err
		}
	}},
	{"ListSubmissionsByParticipationTask", func(f *fixture, n int) func() (int, error) {
		c, g := f.contest("ioi")
		tk, ds := f.task(c, 0, "aaa")
		p := f.participation(c, g, f.user("alice"), nil)
		for i := range n {
			s := f.submission(p, tk, t0.Add(time.Duration(i)*time.Minute))
			f.compile(s, ds, "ok")
		}
		return func() (int, error) {
			rows, err := f.q.ListSubmissionsByParticipationTask(f.ctx, q.ListSubmissionsByParticipationTaskParams{ParticipationID: p.ID, TaskID: tk.ID})
			return len(rows), err
		}
	}},
	{"ListFilesBySubmissions", func(f *fixture, n int) func() (int, error) {
		_, _, _, _, subs := contestWithSubmissions(f, n)
		var ids []int32
		for _, s := range subs {
			_, err := f.q.CreateFiles(f.ctx, q.CreateFilesParams{SubmissionID: s.ID, Filenames: []string{"aaa.%l"}, Digests: []string{digest("x")}})
			check(f.t, err)
			ids = append(ids, s.ID)
		}
		return func() (int, error) {
			rows, err := f.q.ListFilesBySubmissions(f.ctx, ids)
			return len(rows), err
		}
	}},
	{"ListScoredResultsByContest", func(f *fixture, n int) func() (int, error) {
		c, _, _, _, _ := contestWithSubmissions(f, n)
		return func() (int, error) {
			rows, err := f.q.ListScoredResultsByContest(f.ctx, c.ID)
			return len(rows), err
		}
	}},
	{"ListQuestionsByContest", func(f *fixture, n int) func() (int, error) {
		c, _, _, ps, _ := contestWithSubmissions(f, n)
		for i, p := range ps {
			must[q.Question](f.t)(f.q.CreateQuestion(f.ctx, q.CreateQuestionParams{
				ParticipationID: p.ID, QuestionTimestamp: t0.Add(time.Duration(i) * time.Second), Subject: "s", Text: "t",
			}))
		}
		return func() (int, error) {
			rows, err := f.q.ListQuestionsByContest(f.ctx, c.ID)
			return len(rows), err
		}
	}},
	{"ListAnnouncementsByContest", func(f *fixture, n int) func() (int, error) {
		c, _ := f.contest("ioi")
		admin := must[q.Admin](f.t)(f.q.CreateAdmin(f.ctx, q.CreateAdminParams{Name: "a", Username: "a", Authentication: "plaintext:a", Enabled: true}))
		for i := range n {
			must[q.Announcement](f.t)(f.q.CreateAnnouncement(f.ctx, q.CreateAnnouncementParams{
				ContestID: c.ID, AdminID: &admin.ID, Timestamp: t0.Add(time.Duration(i) * time.Second), Subject: "s", Text: "t",
			}))
		}
		return func() (int, error) {
			rows, err := f.q.ListAnnouncementsByContest(f.ctx, c.ID)
			return len(rows), err
		}
	}},
	{"ListUserTestsByContest", func(f *fixture, n int) func() (int, error) {
		c, _, ds, ps, _ := contestWithSubmissions(f, n)
		tk := must[q.Task](f.t)(f.q.GetTask(f.ctx, ds.TaskID))
		for i, p := range ps {
			ut := must[q.UserTest](f.t)(f.q.CreateUserTest(f.ctx, q.CreateUserTestParams{
				ParticipationID: p.ID, TaskID: tk.ID, Timestamp: t0.Add(time.Duration(i) * time.Second), Input: digest("in"),
			}))
			_, err := f.q.CreateUserTestResult(f.ctx, q.CreateUserTestResultParams{UserTestID: ut.ID, DatasetID: ds.ID})
			check(f.t, err)
		}
		return func() (int, error) {
			rows, err := f.q.ListUserTestsByContest(f.ctx, q.ListUserTestsByContestParams{ContestID: c.ID, PageSize: 100})
			return len(rows), err
		}
	}},
	{"ListAuditLog", func(f *fixture, n int) func() (int, error) {
		admin := must[q.Admin](f.t)(f.q.CreateAdmin(f.ctx, q.CreateAdminParams{Name: "a", Username: "a", Authentication: "plaintext:a", Enabled: true}))
		for i := range n {
			must[q.AuditLog](f.t)(f.q.CreateAuditEntry(f.ctx, q.CreateAuditEntryParams{
				At: t0.Add(time.Duration(i) * time.Second), AdminID: &admin.ID, Action: "user.create", TargetType: "user",
			}))
		}
		return func() (int, error) {
			rows, err := f.q.ListAuditLog(f.ctx, q.ListAuditLogParams{PageSize: 100})
			return len(rows), err
		}
	}},
}

// TestListsRunOneQuery checks N2 (no N+1): every list returns its N rows
// with exactly one query, for N = 1 and N = 20.
func TestListsRunOneQuery(t *testing.T) {
	for _, lc := range listCases {
		for _, n := range []int{1, 20} {
			t.Run(fmt.Sprintf("%s/N=%d", lc.name, n), func(t *testing.T) {
				t.Parallel()
				f, qc := newFixture(t)
				list := lc.build(f, n)
				qc.Reset()
				got, err := list()
				check(t, err)
				if got != n {
					t.Fatalf("%d rows, want %d", got, n)
				}
				if c := qc.Count(); c != 1 {
					t.Fatalf("%d queries for %d rows, want 1", c, n)
				}
			})
		}
	}
}

// TestQueryCounter checks what the counter counts: statements, a
// transaction's BEGIN and COMMIT, a batch and a COPY once each.
func TestQueryCounter(t *testing.T) {
	pool, qc := testutil.CountingDB(t)
	s := store.New(pool)
	ctx := t.Context()

	c := must[q.Contest](t)(s.Q.CreateContest(ctx, contestParams("ioi")))
	if qc.Count() != 1 {
		t.Fatalf("one statement counted %d", qc.Count())
	}
	qc.Reset()
	check(t, s.InTx(ctx, func(tx *q.Queries) error {
		_, err := tx.GetContest(ctx, c.ID)
		return err
	}))
	if qc.Count() != 3 {
		t.Fatalf("BEGIN, a query, COMMIT counted %d, want 3", qc.Count())
	}
	tk := must[q.Task](t)(s.Q.CreateTask(ctx, taskParams(&c.ID, ptr(int32(0)), "aaa")))
	ds := must[q.Dataset](t)(s.Q.CreateDataset(ctx, datasetParams(tk.ID, "d")))

	qc.Reset()
	var rows []q.CreateTestcasesParams
	for i := range 50 {
		rows = append(rows, q.CreateTestcasesParams{DatasetID: ds.ID, Codename: fmt.Sprint(i), Input: digest("i"), Output: digest("o")})
	}
	if n := must[int64](t)(s.Q.CreateTestcases(ctx, rows)); n != 50 || qc.Count() != 1 {
		t.Fatalf("COPY of %d rows counted %d, want 1", n, qc.Count())
	}

	g := must[q.Group](t)(s.Q.CreateGroup(ctx, groupParams(c.ID, "g")))
	u := must[q.User](t)(s.Q.CreateUser(ctx, q.CreateUserParams{FirstName: "a", LastName: "b", Username: "u", Password: "plaintext:"}))
	p := must[q.Participation](t)(s.Q.CreateParticipation(ctx, q.CreateParticipationParams{ContestID: c.ID, UserID: u.ID, GroupID: g.ID}))
	sub := must[q.Submission](t)(store.CreateSubmission(ctx, s.Q, q.CreateSubmissionParams{ParticipationID: p.ID, TaskID: tk.ID, Timestamp: t0}))
	_, err := s.Q.SetSubmissionCompilation(ctx, q.SetSubmissionCompilationParams{SubmissionID: sub.ID, DatasetID: ds.ID, CompilationOutcome: ptr("ok"), Attempt: 1})
	check(t, err)
	tcs := must[[]q.Testcase](t)(s.Q.ListTestcasesByDataset(ctx, ds.ID))
	var batch []q.CreateEvaluationsBatchParams
	for _, tc := range tcs {
		batch = append(batch, q.CreateEvaluationsBatchParams{SubmissionID: sub.ID, DatasetID: ds.ID, TestcaseID: tc.ID})
	}
	qc.Reset()
	var batchErr error
	s.Q.CreateEvaluationsBatch(ctx, batch).Exec(func(_ int, err error) {
		if err != nil {
			batchErr = err
		}
	})
	check(t, batchErr)
	if qc.Count() != 1 {
		t.Fatalf("a batch of %d statements counted %d, want 1", len(batch), qc.Count())
	}
}
