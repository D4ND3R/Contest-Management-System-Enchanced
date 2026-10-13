-- name: JudgingEnsureTaskScores :exec
-- internal/judging: the rows of task_scores about to be recomputed, so
-- that JudgingLockTaskScores can lock them (concurrent recomputations of
-- the same participant and task then run one after the other). Pairs
-- whose participation or task is gone are skipped.
INSERT INTO task_scores (participation_id, task_id)
SELECT x.participation_id, x.task_id
FROM (SELECT unnest(sqlc.arg(participation_ids)::integer[]) AS participation_id,
             unnest(sqlc.arg(task_ids)::integer[]) AS task_id) AS x
JOIN participations p ON p.id = x.participation_id
JOIN tasks t ON t.id = x.task_id
ORDER BY x.participation_id, x.task_id
ON CONFLICT (participation_id, task_id) DO NOTHING;

-- name: JudgingLockTaskScores :many
-- Locks the rows of task_scores to recompute, in a fixed order, and
-- returns their current values (to report what changed).
SELECT ts.participation_id, ts.task_id, ts.dataset_id, ts.score, ts.public_score, ts.tokened_score, ts.partial
FROM task_scores ts
WHERE (ts.participation_id, ts.task_id) IN (
    SELECT x.participation_id, x.task_id
    FROM (SELECT unnest(sqlc.arg(participation_ids)::integer[]) AS participation_id,
                 unnest(sqlc.arg(task_ids)::integer[]) AS task_id) AS x)
ORDER BY ts.participation_id, ts.task_id
FOR UPDATE OF ts;

-- name: JudgingTaskScoreInputs :many
-- Everything upstream task_score reads for the given participants and
-- tasks: the score mode and precision of the task, and every submission
-- (ix_submissions_participation_id_task_id) with whether a token was
-- played and its result on the active dataset (and the contest of the
-- task, for the live events of the changes). Of the details, only the
-- subtask scores, and only for the max_subtask mode. A pair without
-- submissions gives one row with a NULL submission_id.
SELECT x.participation_id::integer AS participation_id, x.task_id::integer AS task_id,
       t.score_mode, t.score_precision, t.active_dataset_id, t.contest_id,
       s.id AS submission_id, s.timestamp, s.official,
       (tk.submission_id IS NOT NULL)::boolean AS tokened,
       (sr.score IS NOT NULL AND sr.score_details IS NOT NULL AND sr.public_score IS NOT NULL
        AND sr.public_score_details IS NOT NULL AND sr.ranking_score_details IS NOT NULL)::boolean AS scored,
       sr.score, sr.public_score,
       (CASE WHEN t.score_mode = 'max_subtask' THEN sr.subtask_scores END)::jsonb AS subtask_scores,
       (CASE WHEN t.score_mode = 'max_subtask' THEN sr.public_subtask_scores END)::jsonb AS public_subtask_scores
FROM (SELECT unnest(sqlc.arg(participation_ids)::integer[]) AS participation_id,
             unnest(sqlc.arg(task_ids)::integer[]) AS task_id) AS x
JOIN tasks t ON t.id = x.task_id
LEFT JOIN submissions s ON s.participation_id = x.participation_id AND s.task_id = x.task_id
LEFT JOIN submission_results sr ON sr.submission_id = s.id AND sr.dataset_id = t.active_dataset_id
LEFT JOIN tokens tk ON tk.submission_id = s.id
ORDER BY x.participation_id, x.task_id, s.timestamp, s.id;

-- name: JudgingUpdateTaskScores :exec
-- Writes recomputed task scores (rows locked by JudgingLockTaskScores);
-- dataset 0 stores NULL.
UPDATE task_scores ts SET
    dataset_id = nullif(x.dataset_id, 0),
    score = x.score,
    public_score = x.public_score,
    tokened_score = x.tokened_score,
    partial = x.partial,
    updated_at = now()
FROM (SELECT unnest(sqlc.arg(participation_ids)::integer[]) AS participation_id,
             unnest(sqlc.arg(task_ids)::integer[]) AS task_id,
             unnest(sqlc.arg(dataset_ids)::integer[]) AS dataset_id,
             unnest(sqlc.arg(scores)::double precision[]) AS score,
             unnest(sqlc.arg(public_scores)::double precision[]) AS public_score,
             unnest(sqlc.arg(tokened_scores)::double precision[]) AS tokened_score,
             unnest(sqlc.arg(partials)::boolean[]) AS partial) AS x
WHERE ts.participation_id = x.participation_id AND ts.task_id = x.task_id;

-- name: JudgingTaskParticipations :many
-- Every participant whose score on a task may be cached: those with a
-- submission (ix_submissions_task_id) or a row already
-- (ix_task_scores_task_id). Recomputing a whole task (active dataset,
-- score mode or precision changed).
SELECT s.participation_id FROM submissions s WHERE s.task_id = sqlc.arg(task_id)::integer
UNION
SELECT ts.participation_id FROM task_scores ts WHERE ts.task_id = sqlc.arg(task_id)::integer
ORDER BY 1;

-- name: JudgingSetActiveDataset :one
-- Makes a dataset of the task the active one; nothing (pgx.ErrNoRows) if
-- it is not a dataset of the task. Returns the previous active dataset.
UPDATE tasks SET active_dataset_id = d.id
FROM datasets d, tasks old
WHERE tasks.id = sqlc.arg(task_id)::integer AND d.id = sqlc.arg(dataset_id)::integer
  AND d.task_id = tasks.id AND old.id = tasks.id
RETURNING old.active_dataset_id AS previous_dataset_id;

-- name: ListTaskScoresByParticipation :many
-- Contestant pages: the cached scores of a participant on every task with
-- submissions (primary key). A task without a row scores 0, not partial.
SELECT * FROM task_scores WHERE participation_id = $1 ORDER BY task_id;

-- name: GetTaskScore :one
-- The cached score of a participant on a task (pgx.ErrNoRows: 0, not
-- partial).
SELECT * FROM task_scores WHERE participation_id = $1 AND task_id = $2;

-- name: ListTaskScoresByContest :many
-- Rankings: the cached scores of every participant on the tasks of a
-- contest (tasks_contest_id_num_key, then ix_task_scores_task_id).
SELECT ts.*
FROM tasks t
JOIN task_scores ts ON ts.task_id = t.id
WHERE t.contest_id = $1
ORDER BY ts.participation_id, ts.task_id;

-- name: JudgingSubmissionTaskKeys :many
-- The participants and tasks of some submissions (primary key), whose
-- cached scores a new submission or a token changes.
SELECT DISTINCT participation_id, task_id
FROM submissions
WHERE id = ANY(sqlc.arg(submission_ids)::integer[])
ORDER BY participation_id, task_id;
