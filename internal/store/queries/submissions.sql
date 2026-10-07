-- name: GetSubmission :one
SELECT * FROM submissions WHERE id = $1;

-- name: GetSubmissionByOpaqueID :one
-- Contestant URLs name submissions by opaque id (participation_opaque_unique).
SELECT * FROM submissions
WHERE participation_id = $1 AND opaque_id = $2 AND task_id = $3;

-- name: GetSubmissionDetails :one
-- Admin submission page: the submission with its participant, user, task
-- and token (NULL if not played) in one query.
SELECT sqlc.embed(s), sqlc.embed(p), sqlc.embed(u), sqlc.embed(t), tk.timestamp AS token_timestamp
FROM submissions s
JOIN participations p ON p.id = s.participation_id
JOIN users u ON u.id = p.user_id
JOIN tasks t ON t.id = s.task_id
LEFT JOIN tokens tk ON tk.submission_id = s.id
WHERE s.id = $1;

-- name: ListSubmissionsByParticipationTask :many
-- Contestant task submissions page: every submission of the participant
-- to the task, oldest first, with its result on the active dataset (NULL
-- columns while there is no result row) and its token
-- (ix_submissions_participation_id_task_id).
SELECT sqlc.embed(s),
       sr.compilation_outcome, sr.evaluation_outcome,
       sr.score, sr.score_details, sr.public_score, sr.public_score_details,
       tk.timestamp AS token_timestamp
FROM submissions s
JOIN tasks t ON t.id = s.task_id
LEFT JOIN submission_results sr ON sr.submission_id = s.id AND sr.dataset_id = t.active_dataset_id
LEFT JOIN tokens tk ON tk.submission_id = s.id
WHERE s.participation_id = $1 AND s.task_id = $2
ORDER BY s.timestamp, s.id;

-- name: GetSubmissionStats :one
-- The submit-time limits of CMS in one query: how many submissions the
-- participant sent and when the last one was (NULL: none), on the task
-- and on every task of the contest. The last ones are LEFT JOINs on a
-- subquery (evaluated once) so that their timestamps may be NULL; the
-- task side reads ix_submissions_participation_id_task_id backwards.
SELECT n.task_count, n.contest_count, lt.timestamp AS task_last, lc.timestamp AS contest_last
FROM (
    SELECT count(*) FILTER (WHERE s.task_id = sqlc.arg(task_id)) AS task_count, count(*) AS contest_count
    FROM submissions s
    JOIN tasks t ON t.id = s.task_id
    WHERE s.participation_id = sqlc.arg(participation_id) AND t.contest_id = sqlc.arg(contest_id)::integer
) n
LEFT JOIN submissions lt ON lt.id = (
    SELECT s.id FROM submissions s
    WHERE s.participation_id = sqlc.arg(participation_id) AND s.task_id = sqlc.arg(task_id)
    ORDER BY s.timestamp DESC, s.id DESC LIMIT 1)
LEFT JOIN submissions lc ON lc.id = (
    SELECT s.id FROM submissions s
    JOIN tasks t ON t.id = s.task_id
    WHERE s.participation_id = sqlc.arg(participation_id) AND t.contest_id = sqlc.arg(contest_id)::integer
    ORDER BY s.timestamp DESC, s.id DESC LIMIT 1);

-- name: CreateSubmission :one
-- opaque_id is a random number in [0, 2^63) chosen by the caller
-- (store.CreateSubmission). A collision returns no row (pgx.ErrNoRows)
-- without aborting the transaction, so the caller can draw again.
INSERT INTO submissions (participation_id, task_id, opaque_id, timestamp, language, comment, official)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT ON CONSTRAINT participation_opaque_unique DO NOTHING
RETURNING *;

-- name: UpdateSubmission :one
-- Admin edits: the comment and whether the submission is official.
UPDATE submissions SET comment = $2, official = $3 WHERE id = $1 RETURNING *;

-- name: DeleteSubmission :execrows
-- Cascades to its files, token, results, executables and evaluations.
DELETE FROM submissions WHERE id = $1;
