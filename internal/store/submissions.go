// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package store

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// SubmissionStatus is the state of a submission result, as
// SubmissionResult.get_status in CMS.
type SubmissionStatus string

// The statuses, in the order a result goes through them.
const (
	// StatusCompiling: no result yet, or no compilation outcome.
	StatusCompiling SubmissionStatus = "compiling"
	// StatusCompilationFailed: the submission does not compile; it is
	// scored (zero) but never evaluated.
	StatusCompilationFailed SubmissionStatus = "compilation_failed"
	// StatusEvaluating: compiled, the testcases are running.
	StatusEvaluating SubmissionStatus = "evaluating"
	// StatusScoring: evaluated, the score is not stored yet.
	StatusScoring SubmissionStatus = "scoring"
	// StatusScored: evaluated and scored.
	StatusScored SubmissionStatus = "scored"
)

// Valid reports whether s is one of the statuses.
func (s SubmissionStatus) Valid() bool {
	switch s {
	case StatusCompiling, StatusCompilationFailed, StatusEvaluating, StatusScoring, StatusScored:
		return true
	}
	return false
}

// StatusOf computes the status from the outcomes of a result (nil when
// unset) and whether it is scored.
func StatusOf(compilationOutcome, evaluationOutcome *string, scored bool) SubmissionStatus {
	switch {
	case compilationOutcome == nil:
		return StatusCompiling
	case *compilationOutcome != "ok":
		return StatusCompilationFailed
	case evaluationOutcome == nil:
		return StatusEvaluating
	case !scored:
		return StatusScoring
	default:
		return StatusScored
	}
}

// ResultStatus is the status of a stored result. CMS counts it as scored
// when all five score columns are set.
func ResultStatus(r q.SubmissionResult) SubmissionStatus {
	scored := r.Score != nil && r.ScoreDetails != nil && r.PublicScore != nil &&
		r.PublicScoreDetails != nil && r.RankingScoreDetails != nil
	return StatusOf(r.CompilationOutcome, r.EvaluationOutcome, scored)
}

// statusSQL is the WHERE condition of each status on the result sr (a
// LEFT JOIN: a missing row has NULL columns and is compiling).
var statusSQL = map[SubmissionStatus]string{
	StatusCompiling:         "sr.compilation_outcome IS NULL",
	StatusCompilationFailed: "sr.compilation_outcome = 'fail'",
	StatusEvaluating:        "sr.compilation_outcome = 'ok' AND sr.evaluation_outcome IS NULL",
	StatusScoring:           "sr.compilation_outcome = 'ok' AND sr.evaluation_outcome IS NOT NULL AND NOT (" + scoredSQL + ")",
	StatusScored:            "sr.compilation_outcome = 'ok' AND sr.evaluation_outcome IS NOT NULL AND (" + scoredSQL + ")",
}

const scoredSQL = "sr.score IS NOT NULL AND sr.score_details IS NOT NULL AND sr.public_score IS NOT NULL" +
	" AND sr.public_score_details IS NOT NULL AND sr.ranking_score_details IS NOT NULL"

// Page sizes of ListSubmissions.
const (
	DefaultPageSize = 50 // the admin pages of CMS
	MaxPageSize     = 1000
)

// SubmissionFilter selects the submissions of ListSubmissions and
// CountSubmissions. Zero fields do not filter.
type SubmissionFilter struct {
	// ContestID keeps the submissions to the tasks of a contest (as the
	// admin contest submissions page of CMS).
	ContestID int32
	TaskID    int32
	// ParticipationID keeps the submissions of one participant.
	ParticipationID int32
	// DatasetID shows the results on this dataset instead of the active
	// dataset of each task, and keeps the submissions to its task.
	DatasetID int32
	// Status keeps the submissions whose result (on the dataset shown) is
	// in this state.
	Status SubmissionStatus
	// Before, when set, returns the page after the row it names: rows
	// strictly older in the order of the list (timestamp, then id,
	// newest first). Use SubmissionRow.Cursor of the last row of a page.
	Before *SubmissionCursor
	// Limit is the page size: DefaultPageSize when 0, at most MaxPageSize.
	Limit int
}

// SubmissionCursor is a position in a list of submissions.
type SubmissionCursor struct {
	Timestamp time.Time
	ID        int32
}

// SubmissionRow is one line of the submission lists of the admin: the
// submission, who sent it, the task and the result on the dataset shown.
type SubmissionRow struct {
	Submission q.Submission
	// ContestID is the contest of the task (nil: the task left its contest).
	ContestID *int32
	UserID    int32
	Username  string
	FirstName string
	LastName  string
	TaskName  string
	TaskTitle string
	// DatasetID is the dataset whose result is shown: the filter's, or the
	// active dataset of the task (nil if the task has none).
	DatasetID *int32
	// Result is nil while there is no result row (compiling).
	Result *ResultSummary
	// TokenTimestamp is when a token was played on it (nil: none).
	TokenTimestamp *time.Time
}

// ResultSummary is the part of a submission result that lists show.
type ResultSummary struct {
	CompilationOutcome *string
	CompilationTries   int32
	EvaluationOutcome  *string
	EvaluationTries    int32
	Score              *float64
	PublicScore        *float64
	ScoredAt           *time.Time
	// Scored: every score column is set (CMS SubmissionResult.scored).
	Scored bool
}

// Status returns the status of the result shown.
func (r *SubmissionRow) Status() SubmissionStatus {
	if r.Result == nil {
		return StatusCompiling
	}
	return StatusOf(r.Result.CompilationOutcome, r.Result.EvaluationOutcome, r.Result.Scored)
}

// Cursor returns the position of the row, for SubmissionFilter.Before.
func (r *SubmissionRow) Cursor() SubmissionCursor {
	return SubmissionCursor{Timestamp: r.Submission.Timestamp, ID: r.Submission.ID}
}

// sqlArgs collects the arguments of a query built by hand.
type sqlArgs []any

// add appends v and returns its placeholder.
func (a *sqlArgs) add(v any) string {
	*a = append(*a, v)
	return "$" + strconv.Itoa(len(*a))
}

// submissionSQL is a SubmissionFilter turned into SQL on the tables s
// (submissions), t (tasks) and sr (submission_results).
type submissionSQL struct {
	// dataset is the expression of the dataset shown.
	dataset string
	// resultJoin joins the result shown to s; empty when no condition
	// reads it (only the status filter does).
	resultJoin string
	// taskConds are the conditions on t, subConds those on s and sr.
	taskConds, subConds []string
}

// where returns the conditions as a WHERE clause (empty without them).
func where(conds ...string) string {
	if len(conds) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(conds, " AND ")
}

func submissionFilterSQL(f SubmissionFilter, args *sqlArgs) (submissionSQL, error) {
	if f.Status != "" && !f.Status.Valid() {
		return submissionSQL{}, fmt.Errorf("store: unknown submission status %q", f.Status)
	}
	x := submissionSQL{dataset: "t.active_dataset_id"}
	if f.DatasetID != 0 {
		x.dataset = args.add(f.DatasetID) + "::integer"
		x.taskConds = append(x.taskConds, "t.id = (SELECT d.task_id FROM datasets d WHERE d.id = "+x.dataset+")")
	}
	if f.ContestID != 0 {
		x.taskConds = append(x.taskConds, "t.contest_id = "+args.add(f.ContestID))
	}
	if f.TaskID != 0 {
		x.taskConds = append(x.taskConds, "t.id = "+args.add(f.TaskID))
	}
	if f.ParticipationID != 0 {
		x.subConds = append(x.subConds, "s.participation_id = "+args.add(f.ParticipationID))
	}
	if f.Status != "" {
		x.resultJoin = " LEFT JOIN submission_results sr ON sr.submission_id = s.id AND sr.dataset_id = " + x.dataset
		x.subConds = append(x.subConds, "("+statusSQL[f.Status]+")")
	}
	if f.Before != nil {
		x.subConds = append(x.subConds, "(s.timestamp, s.id) < ("+args.add(f.Before.Timestamp)+", "+args.add(f.Before.ID)+")")
	}
	return x, nil
}

// ListSubmissions returns a page of submissions, newest first, in one
// query whatever the filters. The page is chosen in two steps that only
// read submissions (and their result, to filter by status): the newest
// rows of each task that passes the filter, walking
// ix_submissions_task_id backwards (or
// ix_submissions_participation_id_task_id for one participant) and
// stopping at the page size; then the newest of those. So a page reads
// at most (tasks × page size) submissions whatever the size of the
// contest. The participant, the result shown and the token are joined
// afterwards to the rows of the page only (a deferred join).
func ListSubmissions(ctx context.Context, db q.DBTX, f SubmissionFilter) ([]SubmissionRow, error) {
	limit := f.Limit
	switch {
	case limit == 0:
		limit = DefaultPageSize
	case limit < 0 || limit > MaxPageSize:
		return nil, fmt.Errorf("store: page size %d out of range 1..%d", limit, MaxPageSize)
	}
	var args sqlArgs
	x, err := submissionFilterSQL(f, &args)
	if err != nil {
		return nil, err
	}
	n := args.add(limit)
	sql := `SELECT s.id, s.opaque_id, s.participation_id, s.task_id, s.timestamp, s.language, s.comment, s.official,
       s.contest_id, p.user_id, u.username, u.first_name, u.last_name, s.task_name, s.task_title, s.dataset_id,
       sr.submission_id IS NOT NULL, sr.compilation_outcome, coalesce(sr.compilation_tries, 0),
       sr.evaluation_outcome, coalesce(sr.evaluation_tries, 0), sr.score, sr.public_score, sr.scored_at,
       ` + scoredSQL + `,
       tk.timestamp
FROM (
    SELECT s.*, t.contest_id, t.name AS task_name, t.title AS task_title, ` + x.dataset + ` AS dataset_id
    FROM tasks t
    CROSS JOIN LATERAL (
        SELECT s.id, s.opaque_id, s.participation_id, s.task_id, s.timestamp, s.language, s.comment, s.official
        FROM submissions s` + x.resultJoin + where(append([]string{"s.task_id = t.id"}, x.subConds...)...) + `
        ORDER BY s.timestamp DESC, s.id DESC
        LIMIT ` + n + `
    ) s` + where(x.taskConds...) + `
    ORDER BY s.timestamp DESC, s.id DESC
    LIMIT ` + n + `
) s
JOIN participations p ON p.id = s.participation_id
JOIN users u ON u.id = p.user_id
LEFT JOIN submission_results sr ON sr.submission_id = s.id AND sr.dataset_id = s.dataset_id
LEFT JOIN tokens tk ON tk.submission_id = s.id
ORDER BY s.timestamp DESC, s.id DESC`

	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanSubmissionRow)
}

func scanSubmissionRow(row pgx.CollectableRow) (SubmissionRow, error) {
	var (
		r         SubmissionRow
		hasResult bool
		res       ResultSummary
	)
	err := row.Scan(
		&r.Submission.ID, &r.Submission.OpaqueID, &r.Submission.ParticipationID, &r.Submission.TaskID,
		&r.Submission.Timestamp, &r.Submission.Language, &r.Submission.Comment, &r.Submission.Official,
		&r.ContestID, &r.UserID, &r.Username, &r.FirstName, &r.LastName, &r.TaskName, &r.TaskTitle,
		&r.DatasetID,
		&hasResult, &res.CompilationOutcome, &res.CompilationTries,
		&res.EvaluationOutcome, &res.EvaluationTries, &res.Score, &res.PublicScore, &res.ScoredAt,
		&res.Scored,
		&r.TokenTimestamp,
	)
	if hasResult {
		r.Result = &res
	}
	return r, err
}

// CountSubmissions returns how many submissions match the filter
// (Before and Limit are ignored), for "page X of Y".
func CountSubmissions(ctx context.Context, db q.DBTX, f SubmissionFilter) (int64, error) {
	f.Before = nil
	var args sqlArgs
	x, err := submissionFilterSQL(f, &args)
	if err != nil {
		return 0, err
	}
	var n int64
	err = db.QueryRow(ctx, `SELECT count(*) FROM submissions s JOIN tasks t ON t.id = s.task_id`+x.resultJoin+
		where(append(x.taskConds, x.subConds...)...), args...).Scan(&n)
	return n, err
}

// ListSubmissions is the package function on the store's pool.
func (s *Store) ListSubmissions(ctx context.Context, f SubmissionFilter) ([]SubmissionRow, error) {
	return ListSubmissions(ctx, s.Pool, f)
}

// CountSubmissions is the package function on the store's pool.
func (s *Store) CountSubmissions(ctx context.Context, f SubmissionFilter) (int64, error) {
	return CountSubmissions(ctx, s.Pool, f)
}

// NewOpaqueID returns a random opaque submission id in [0, 2^63), as CMS.
func NewOpaqueID() int64 { return rand.Int64() }

// drawOpaqueID is NewOpaqueID; tests replace it to force collisions.
var drawOpaqueID = NewOpaqueID

// opaqueIDDraws bounds the draws of CreateSubmission (a collision has a
// probability of about n/2^63 for a participant with n submissions).
const opaqueIDDraws = 5

// CreateSubmission inserts a submission with a new random opaque id
// (arg.OpaqueID is ignored), drawing again on a collision. Inside a
// transaction a collision does not abort it.
func CreateSubmission(ctx context.Context, qs *q.Queries, arg q.CreateSubmissionParams) (q.Submission, error) {
	for range opaqueIDDraws {
		arg.OpaqueID = drawOpaqueID()
		sub, err := qs.CreateSubmission(ctx, arg)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		return sub, err
	}
	return q.Submission{}, fmt.Errorf("store: no free opaque id for participation %d after %d draws", arg.ParticipationID, opaqueIDDraws)
}
