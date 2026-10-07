-- name: GetSubmissionResult :one
SELECT * FROM submission_results WHERE submission_id = $1 AND dataset_id = $2;

-- name: ListSubmissionResultsBySubmission :many
-- Admin submission page: the results on every dataset, with the dataset
-- and whether it is the active one.
SELECT sqlc.embed(sr), d.description AS dataset_description, d.autojudge AS dataset_autojudge,
       coalesce(t.active_dataset_id = d.id, false)::boolean AS active
FROM submission_results sr
JOIN datasets d ON d.id = sr.dataset_id
JOIN tasks t ON t.id = d.task_id
WHERE sr.submission_id = $1
ORDER BY d.id;

-- name: ListSubmissionResultsByDataset :many
-- Every result on a dataset (rescoring, dataset comparison) through
-- ix_submission_results_dataset_id.
SELECT * FROM submission_results WHERE dataset_id = $1 ORDER BY submission_id;

-- name: ListScoredResultsByContest :many
-- Rankings: every scored submission of the contest's tasks on their
-- active dataset (all five score columns set, as ResultStatus and CMS
-- filter_scored), with whether a token was played, grouped by
-- participant and task, oldest first (tasks_contest_id_num_key, then
-- ix_submissions_task_id and the primary key of submission_results).
SELECT s.id AS submission_id, s.participation_id, s.task_id, s.timestamp, s.official,
       (tk.id IS NOT NULL)::boolean AS tokened,
       sr.score, sr.score_details, sr.public_score, sr.public_score_details, sr.ranking_score_details
FROM tasks t
JOIN submissions s ON s.task_id = t.id
JOIN submission_results sr ON sr.submission_id = s.id AND sr.dataset_id = t.active_dataset_id
LEFT JOIN tokens tk ON tk.submission_id = s.id
WHERE t.contest_id = sqlc.arg(contest_id)::integer
  AND sr.score IS NOT NULL AND sr.score_details IS NOT NULL AND sr.public_score IS NOT NULL
  AND sr.public_score_details IS NOT NULL AND sr.ranking_score_details IS NOT NULL
ORDER BY s.participation_id, s.task_id, s.timestamp, s.id;

-- name: CreateSubmissionResult :execrows
-- The empty ("compiling") result of a submission on a dataset; does
-- nothing if it exists.
INSERT INTO submission_results (submission_id, dataset_id) VALUES ($1, $2)
ON CONFLICT (submission_id, dataset_id) DO NOTHING;

-- name: SetSubmissionCompilation :execrows
-- Stores a compilation result, creating the row if needed. attempt is
-- the job's attempt number (1 for the first job; the result's
-- compilation_tries + 1 when the job was queued) and compilation_tries
-- becomes the highest attempt seen. A NULL outcome records a failed
-- attempt (the worker could not compile). Idempotent, first result
-- wins: once an outcome is stored, a repeated result (a redelivered
-- job) changes nothing and affects 0 rows, and so does a failed attempt
-- already counted; store the executables (and queue a retry) only when
-- it affected 1.
INSERT INTO submission_results AS sr (
    submission_id, dataset_id, compilation_outcome, compilation_text, compilation_tries,
    compilation_stdout, compilation_stderr, compilation_time, compilation_wall_clock_time,
    compilation_memory, compilation_shard, compilation_sandbox_paths, compilation_sandbox_digests
) VALUES (
    sqlc.arg(submission_id), sqlc.arg(dataset_id), sqlc.narg(compilation_outcome),
    coalesce(sqlc.arg(compilation_text)::text[], '{}'), sqlc.arg(attempt)::integer,
    sqlc.narg(compilation_stdout), sqlc.narg(compilation_stderr), sqlc.narg(compilation_time),
    sqlc.narg(compilation_wall_clock_time), sqlc.narg(compilation_memory), sqlc.narg(compilation_shard),
    sqlc.narg(compilation_sandbox_paths), sqlc.narg(compilation_sandbox_digests)
)
ON CONFLICT (submission_id, dataset_id) DO UPDATE SET
    compilation_outcome = excluded.compilation_outcome,
    compilation_text = excluded.compilation_text,
    compilation_tries = greatest(sr.compilation_tries, excluded.compilation_tries),
    compilation_stdout = excluded.compilation_stdout,
    compilation_stderr = excluded.compilation_stderr,
    compilation_time = excluded.compilation_time,
    compilation_wall_clock_time = excluded.compilation_wall_clock_time,
    compilation_memory = excluded.compilation_memory,
    compilation_shard = excluded.compilation_shard,
    compilation_sandbox_paths = excluded.compilation_sandbox_paths,
    compilation_sandbox_digests = excluded.compilation_sandbox_digests
WHERE sr.compilation_outcome IS NULL
  AND (excluded.compilation_outcome IS NOT NULL OR sr.compilation_tries < excluded.compilation_tries);

-- name: SetSubmissionEvaluation :execrows
-- Ends the evaluation of a compiled result: outcome 'ok' once every
-- evaluation is stored, NULL to record a failed attempt.
-- evaluation_tries becomes the highest attempt seen, as
-- compilation_tries in SetSubmissionCompilation; idempotent like it (a
-- failed attempt already counted affects 0 rows).
UPDATE submission_results
SET evaluation_outcome = sqlc.narg(evaluation_outcome),
    evaluation_tries = greatest(evaluation_tries, sqlc.arg(attempt)::integer)
WHERE submission_id = sqlc.arg(submission_id) AND dataset_id = sqlc.arg(dataset_id)
  AND compilation_outcome = 'ok' AND evaluation_outcome IS NULL
  AND (sqlc.narg(evaluation_outcome)::text IS NOT NULL OR evaluation_tries < sqlc.arg(attempt)::integer);

-- name: SetSubmissionScore :execrows
-- Stores the score of a result whose compilation failed or that was
-- evaluated. Writing a score makes the result scored: CMS counts it as
-- scored only when all five score columns are set, so nil details (a
-- score type without subtasks or ranking details) are stored empty,
-- never NULL. Idempotent: an already scored result is left alone (0
-- rows). scored_at keeps the first scoring time, as in CMS.
UPDATE submission_results SET
    score = sqlc.arg(score)::double precision,
    score_details = coalesce(sqlc.arg(score_details)::jsonb, '[]'),
    public_score = sqlc.arg(public_score)::double precision,
    public_score_details = coalesce(sqlc.arg(public_score_details)::jsonb, '[]'),
    ranking_score_details = coalesce(sqlc.arg(ranking_score_details)::text[], '{}'),
    scored_at = coalesce(scored_at, sqlc.arg(scored_at)::timestamptz)
WHERE submission_id = sqlc.arg(submission_id) AND dataset_id = sqlc.arg(dataset_id)
  AND (score IS NULL OR score_details IS NULL OR public_score IS NULL
       OR public_score_details IS NULL OR ranking_score_details IS NULL)
  AND (compilation_outcome = 'fail' OR evaluation_outcome IS NOT NULL);

-- name: InvalidateSubmissionCompilation :execrows
-- Reevaluation from scratch (CMS invalidate_compilation): blanks the
-- compilation, the evaluation and the score and deletes the executables
-- and evaluations, in one statement. The writes above cannot tell the
-- result of a job queued before this from a new one: the caller must
-- make the dispatcher drop the results of the jobs in flight (CMS
-- WorkerPool.ignore_operation), or a late one wins over the new job.
WITH gone_executables AS (
    DELETE FROM executables e
    WHERE e.submission_id = sqlc.arg(submission_id) AND e.dataset_id = sqlc.arg(dataset_id)
), gone_evaluations AS (
    DELETE FROM evaluations ev
    WHERE ev.submission_id = sqlc.arg(submission_id) AND ev.dataset_id = sqlc.arg(dataset_id)
)
UPDATE submission_results sr SET
    compilation_outcome = NULL, compilation_text = '{}', compilation_tries = 0,
    compilation_stdout = NULL, compilation_stderr = NULL, compilation_time = NULL,
    compilation_wall_clock_time = NULL, compilation_memory = NULL, compilation_shard = NULL,
    compilation_sandbox_paths = NULL, compilation_sandbox_digests = NULL,
    evaluation_outcome = NULL, evaluation_tries = 0,
    score = NULL, score_details = NULL, public_score = NULL, public_score_details = NULL,
    ranking_score_details = NULL
WHERE sr.submission_id = sqlc.arg(submission_id) AND sr.dataset_id = sqlc.arg(dataset_id);

-- name: InvalidateSubmissionEvaluation :execrows
-- Re-run the testcases (CMS invalidate_evaluation): deletes the
-- evaluations and blanks the evaluation outcome and the score. As for
-- InvalidateSubmissionCompilation, results of evaluation jobs already in
-- flight must be dropped by the dispatcher.
WITH gone_evaluations AS (
    DELETE FROM evaluations ev
    WHERE ev.submission_id = sqlc.arg(submission_id) AND ev.dataset_id = sqlc.arg(dataset_id)
)
UPDATE submission_results sr SET
    evaluation_outcome = NULL, evaluation_tries = 0,
    score = NULL, score_details = NULL, public_score = NULL, public_score_details = NULL,
    ranking_score_details = NULL
WHERE sr.submission_id = sqlc.arg(submission_id) AND sr.dataset_id = sqlc.arg(dataset_id);

-- name: InvalidateSubmissionScore :execrows
-- Rescore (CMS invalidate_score).
UPDATE submission_results SET
    score = NULL, score_details = NULL, public_score = NULL, public_score_details = NULL,
    ranking_score_details = NULL
WHERE submission_id = $1 AND dataset_id = $2;

-- name: InvalidateDatasetScores :execrows
-- Rescore every result of a dataset (score type changed) through
-- ix_submission_results_dataset_id.
UPDATE submission_results SET
    score = NULL, score_details = NULL, public_score = NULL, public_score_details = NULL,
    ranking_score_details = NULL
WHERE dataset_id = $1 AND score IS NOT NULL;

-- name: DeleteSubmissionResult :execrows
-- Cascades to its executables and evaluations.
DELETE FROM submission_results WHERE submission_id = $1 AND dataset_id = $2;
