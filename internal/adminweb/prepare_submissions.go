// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package adminweb

// Test submissions of the problem preparers (R6.2; new): solutions with
// their expected verdict in the name (correct.ac.cpp, as solutions/ of a
// package), submitted on the test participation (prepare.go), judged by
// the dispatcher and the workers like any submission but not official,
// and shown with the outcome of every testcase and whether it is the
// expected one.

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/contestweb/submission"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/scoretype"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer/pkg"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// testCommentPrefix starts the comment of a test submission; the name of
// the solution follows (its expected verdict is in the name).
const testCommentPrefix = "test: "

// maxTestSubmissions is how many test submissions the page shows.
const maxTestSubmissions = 50

// maxSolutionSize bounds a solution file (or a zip of outputs).
const maxSolutionSize = 64 << 20

// expectedVerdict returns the expected verdict in a solution name
// ("correct.ac.cpp" -> "ac", "outputs.wa.zip" -> "wa"), "" if none.
func expectedVerdict(name string) string {
	base := strings.TrimSuffix(name, path.Ext(name))
	v := strings.ToLower(strings.TrimPrefix(path.Ext(base), "."))
	if slices.Contains(pkg.Verdicts, v) {
		return v
	}
	return ""
}

// testSubmission is a test submission as the page shows it.
type testSubmission struct {
	ID        int32
	Name      string
	Language  string
	Expected  string
	Verdict   string
	Status    string
	Score     string
	Done      bool
	Matches   bool
	Cells     []verdictCell
	Timestamp string
}

// verdictCell is the outcome of a testcase.
type verdictCell struct {
	Outcome string
	Text    string
	Time    string
	Memory  string
	// Class is the CSS class of CMS's submission details (correct,
	// partiallycorrect, notcorrect).
	Class string
}

// testSubmissions loads the latest test submissions of a task with their
// results on a dataset (three queries).
func (s *Server) testSubmissions(c *Ctx, t *q.Task, ds *q.Dataset) ([]testSubmission, error) {
	ctx := c.Context()
	uid, err := s.prepUserID(ctx)
	if err != nil || uid == 0 {
		return nil, err
	}
	rows, err := s.st.Q.AdminPrepSubmissions(ctx, q.AdminPrepSubmissionsParams{DatasetID: ds.ID, TaskID: t.ID, UserID: uid, MaxRows: maxTestSubmissions})
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	tcs, err := s.st.Q.AdminDatasetTestcases(ctx, ds.ID)
	if err != nil {
		return nil, err
	}
	ids := make([]int32, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	evals, err := s.st.Q.AdminPrepEvaluations(ctx, q.AdminPrepEvaluationsParams{DatasetID: ds.ID, SubmissionIds: ids})
	if err != nil {
		return nil, err
	}
	type key struct{ sub, tc int32 }
	byKey := map[key]q.AdminPrepEvaluationsRow{}
	for _, e := range evals {
		byKey[key{e.SubmissionID, e.TestcaseID}] = e
	}
	public := map[string]bool{}
	for _, tc := range tcs {
		public[tc.Codename] = tc.Public
	}
	maxScore := -1.0
	if st, err := scoretype.New(ds.ScoreType, ds.ScoreTypeParameters, public, int(t.ScorePrecision)); err == nil {
		maxScore = st.MaxScore()
	}
	var out []testSubmission
	for _, r := range rows {
		ts := testSubmission{ID: r.ID, Language: deref(r.Language), Timestamp: FormDatetime(r.Timestamp, c.RC.TZ)}
		ts.Name, _ = strings.CutPrefix(r.Comment, testCommentPrefix)
		ts.Expected = expectedVerdict(ts.Name)
		kinds := map[string]bool{}
		for _, tc := range tcs {
			e, ok := byKey[key{r.ID, tc.ID}]
			cell := verdictCell{}
			if ok {
				cell.Outcome = deref(e.Outcome)
				if len(e.Text) > 0 {
					cell.Text = c.T(e.Text[0])
					kinds[verdictKind(e.Text[0])] = true
				}
				if e.ExecutionTime != nil {
					cell.Time = strconv.FormatFloat(*e.ExecutionTime, 'f', 3, 64) + " s"
				}
				if e.ExecutionMemory != nil {
					cell.Memory = strconv.FormatInt(*e.ExecutionMemory/(1<<20), 10) + " MiB"
				}
				switch f, err := strconv.ParseFloat(cell.Outcome, 64); {
				case err != nil:
				case f >= 1:
					cell.Class = "correct"
				case f > 0:
					cell.Class = "partiallycorrect"
				default:
					cell.Class = "notcorrect"
				}
			}
			ts.Cells = append(ts.Cells, cell)
		}
		switch {
		case !r.HasResult || r.CompilationOutcome == nil:
			ts.Status = c.T("Compiling...")
		case *r.CompilationOutcome != "ok":
			ts.Status, ts.Verdict, ts.Done = c.T("Compilation failed"), pkg.VerdictCompilation, true
		case r.Score == nil:
			ts.Status = c.T("Evaluating...")
		default:
			ts.Done = true
			ts.Score = pyFloatStr(r.Score)
			if maxScore >= 0 {
				ts.Score += " / " + pyFloatStr(&maxScore)
			}
			full := maxScore >= 0 && *r.Score >= maxScore-1e-9
			switch {
			case full:
				ts.Verdict = pkg.VerdictAccepted
			case kinds[pkg.VerdictTimeLimit]:
				ts.Verdict = pkg.VerdictTimeLimit
			case kinds[pkg.VerdictMemoryLimit]:
				ts.Verdict = pkg.VerdictMemoryLimit
			case kinds[pkg.VerdictRuntime]:
				ts.Verdict = pkg.VerdictRuntime
			case *r.Score > 0:
				ts.Verdict = pkg.VerdictPartial
			default:
				ts.Verdict = pkg.VerdictWrongAnswer
			}
			ts.Status = c.T("Scored")
			// The expected verdict holds if it describes the result:
			// "wa", "tle", "mle" and "re" need some testcase with that
			// outcome and not the full score; "pa" some points but not
			// all.
			switch ts.Expected {
			case pkg.VerdictAccepted:
				ts.Matches = full
			case pkg.VerdictPartial:
				ts.Matches = !full && *r.Score > 0
			case pkg.VerdictWrongAnswer, pkg.VerdictTimeLimit, pkg.VerdictMemoryLimit, pkg.VerdictRuntime:
				ts.Matches = !full && kinds[ts.Expected]
			}
		}
		if ts.Verdict == pkg.VerdictCompilation {
			ts.Matches = ts.Expected == pkg.VerdictCompilation
		}
		out = append(out, ts)
	}
	return out, nil
}

// verdictKind classifies the message of an evaluation (its msgid, the
// first item of evaluations.text) as an expected verdict.
func verdictKind(msg string) string {
	switch {
	case strings.HasPrefix(msg, "Execution timed out"):
		return pkg.VerdictTimeLimit
	case msg == "Memory limit exceeded":
		return pkg.VerdictMemoryLimit
	case strings.HasPrefix(msg, "Execution killed") || strings.HasPrefix(msg, "Execution failed"):
		return pkg.VerdictRuntime
	case msg == "Output is correct" || msg == "Execution completed successfully":
		return pkg.VerdictAccepted
	}
	return pkg.VerdictWrongAnswer
}

// testSubmissionsData is the data of test_submissions.html.
type testSubmissionsData struct {
	*Page
	Task        *q.Task
	Datasets    []q.Dataset
	Dataset     *q.Dataset
	Judged      bool
	Testcases   []q.Testcase
	Submissions []testSubmission
	Verdicts    []string
}

// testSubmissionsPage is GET /task/<id>/test_submissions.
func (s *Server) testSubmissionsPage(c *Ctx) {
	t, ok := c.loadTask(true)
	if !ok {
		return
	}
	ctx := c.Context()
	datasets, err := s.st.Q.AdminTaskDatasets(ctx, t.ID)
	if err != nil {
		c.Fail(err)
		return
	}
	d := testSubmissionsData{Task: t, Datasets: datasets, Dataset: pickDataset(c, datasets, t), Verdicts: pkg.Verdicts}
	if d.Dataset != nil {
		d.Judged = judgedDataset(t, d.Dataset)
		if d.Testcases, err = s.st.Q.AdminDatasetTestcases(ctx, d.Dataset.ID); err != nil {
			c.Fail(err)
			return
		}
		if d.Submissions, err = s.testSubmissions(c, t, d.Dataset); err != nil {
			c.Fail(err)
			return
		}
	}
	if d.Page, err = c.Page(); err != nil {
		c.Fail(err)
		return
	}
	c.Render("test_submissions.html", d)
}

// solution is a solution to submit: its name (with the expected verdict)
// and its files by element of the submission format.
type solution struct {
	Name     string
	Language *string
	Files    map[string][]byte
}

// testSubmissionsPost is POST /task/<id>/test_submissions: every file
// sent becomes a test submission (a zip is the files of one submission,
// for output-only tasks or formats with several files).
func (s *Server) testSubmissionsPost(c *Ctx) {
	t, ok := c.loadTask(false)
	if !ok {
		return
	}
	back := c.URL("task", t.ID, "test_submissions")
	expected, _ := c.Arg("expected")
	var sols []solution
	for _, fh := range c.Files("solution") {
		data, ok, err := ReadUpload(fh, maxSolutionSize)
		if err != nil {
			c.Fail(err)
			return
		}
		if !ok {
			c.NotifyError(N("Invalid field(s)"), fmt.Sprintf("%s is too large", fh.Filename))
			c.Redirect(back)
			return
		}
		name := path.Base(fh.Filename)
		if expectedVerdict(name) == "" && slices.Contains(pkg.Verdicts, expected) {
			ext := path.Ext(name)
			name = strings.TrimSuffix(name, ext) + "." + expected + ext
		}
		sol, err := s.solutionFiles(t, name, data)
		if err != nil {
			c.NotifyError(N("Invalid field(s)"), fmt.Sprintf("%s: %v", fh.Filename, err))
			c.Redirect(back)
			return
		}
		sols = append(sols, sol)
	}
	if len(sols) == 0 {
		c.Notify(N("Invalid data"), N("Please choose the files of the solutions."), nil)
		c.Redirect(back)
		return
	}
	if _, err := s.createTestSubmissions(c, t, sols); !c.Commit(err) {
		c.Redirect(back)
		return
	}
	c.Redirect(back)
}

// solutionFiles fits a solution file into the submission format of the
// task: a source for a format with one element, or a zip with the files
// of the format (output-only tasks, several sources).
func (s *Server) solutionFiles(t *q.Task, name string, data []byte) (solution, error) {
	sol := solution{Name: name, Files: map[string][]byte{}}
	var given []submission.ReceivedFile
	if strings.EqualFold(path.Ext(name), ".zip") {
		files, err := submission.ExtractFilesFromArchive(data, maxSolutionSize, 1000)
		if err != nil {
			return sol, err
		}
		given = files
	} else {
		// A single source: the name of the solution is not that of the
		// format element; only its extension matters.
		if len(t.SubmissionFormat) != 1 {
			return sol, fmt.Errorf("the submission format has %d files: send them in a zip", len(t.SubmissionFormat))
		}
		element := t.SubmissionFormat[0]
		fname := element
		if strings.HasSuffix(element, ".%l") {
			lang := s.langs.FilenameToLanguage(name, nil)
			if lang == nil {
				return sol, fmt.Errorf("unknown language of %s", name)
			}
			fname = strings.TrimSuffix(element, ".%l") + path.Ext(name)
		}
		given = []submission.ReceivedFile{submission.NewFile("", fname, data)}
	}
	matched, lang, err := submission.MatchFilesAndLanguage(given, nil, t.SubmissionFormat, nil, true, s.langs)
	if err != nil {
		return sol, err
	}
	for codename, f := range matched {
		b, err := f.Content()
		if err != nil {
			return sol, err
		}
		sol.Files[codename] = b
	}
	if lang != nil {
		n := lang.Name
		sol.Language = &n
	}
	return sol, nil
}

// createTestSubmissions stores the files of the solutions and inserts
// them as unofficial submissions of the test participation, then tells
// the dispatcher.
func (s *Server) createTestSubmissions(c *Ctx, t *q.Task, sols []solution) ([]int32, error) {
	if t.ContestID == nil {
		return nil, errNoContest
	}
	ctx := c.Context()
	type stored struct {
		sol            solution
		names, digests []string
	}
	var all []stored
	for _, sol := range sols {
		st := stored{sol: sol}
		for codename, b := range sol.Files {
			d, err := s.blobs.PutBytes(ctx, b)
			if err != nil {
				return nil, err
			}
			st.names, st.digests = append(st.names, codename), append(st.digests, d)
		}
		all = append(all, st)
	}
	var ids []int32
	err := c.Write(func(tx *Tx) error {
		ids = nil
		part, err := s.prepParticipation(tx, *t.ContestID)
		if err != nil {
			return err
		}
		for _, st := range all {
			sub, err := store.CreateSubmission(tx.Ctx, tx.Q, q.CreateSubmissionParams{ParticipationID: part, TaskID: t.ID,
				Timestamp: c.Now, Language: st.sol.Language, Comment: testCommentPrefix + st.sol.Name, Official: false})
			if err != nil {
				return err
			}
			if _, err := tx.Q.CreateFiles(tx.Ctx, q.CreateFilesParams{SubmissionID: sub.ID, Filenames: st.names, Digests: st.digests}); err != nil {
				return err
			}
			ids = append(ids, sub.ID)
			tx.Publish(live.Submission{Contest: int64(*t.ContestID), Participation: int64(part), Task: int64(t.ID),
				ID: int64(sub.ID), Status: string(store.StatusOf(nil, nil, false)), New: true}.Event())
			if err := tx.Audit(AuditEntry{Action: "submission.test", TargetType: "submission", TargetID: int64(sub.ID),
				ContestID: *t.ContestID, Details: map[string]any{"task": t.Name, "solution": st.sol.Name}}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.notifySubmissions(ctx, *t.ContestID, ids)
	return ids, nil
}

func (s *Server) notifySubmissions(ctx context.Context, contestID int32, ids []int32) {
	if len(ids) == 0 {
		return
	}
	if err := judging.Notify(ctx, s.kv, judging.Event{Kind: judging.EventSubmission, ContestID: contestID, SubmissionIDs: ids}); err != nil {
		s.log.Warn("notifying the dispatcher of test submissions", "err", err)
	}
}
