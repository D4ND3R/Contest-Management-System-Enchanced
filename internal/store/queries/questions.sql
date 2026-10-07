-- name: GetQuestion :one
SELECT * FROM questions WHERE id = $1;

-- name: ListQuestionsByParticipation :many
-- Contestant communication page, newest first (ix_questions_participation_id).
SELECT * FROM questions WHERE participation_id = $1 ORDER BY question_timestamp DESC, id DESC;

-- name: ListQuestionsByContest :many
-- Admin questions page, newest first, with the participation, its user
-- and the admin who last handled the question (NULL if none).
SELECT sqlc.embed(qn), sqlc.embed(p), sqlc.embed(u), ad.username AS admin_username
FROM questions qn
JOIN participations p ON p.id = qn.participation_id
JOIN users u ON u.id = p.user_id
LEFT JOIN admins ad ON ad.id = qn.admin_id
WHERE p.contest_id = $1
ORDER BY qn.question_timestamp DESC, qn.id DESC;

-- name: ListRepliesSince :many
-- Contestant notifications: questions answered in (after, until]; a NULL
-- after means from the beginning.
SELECT * FROM questions
WHERE participation_id = sqlc.arg(participation_id)
  AND reply_timestamp IS NOT NULL
  AND reply_timestamp <= sqlc.arg(until)::timestamptz
  AND (sqlc.narg(after)::timestamptz IS NULL OR reply_timestamp > sqlc.narg(after)::timestamptz)
ORDER BY reply_timestamp, id;

-- name: CountUnansweredQuestions :one
-- The counter in the admin menu.
SELECT count(*)
FROM questions qn
JOIN participations p ON p.id = qn.participation_id
WHERE p.contest_id = $1 AND qn.reply_timestamp IS NULL AND NOT qn.ignored;

-- name: CreateQuestion :one
INSERT INTO questions (participation_id, question_timestamp, subject, text)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ReplyQuestion :one
-- Answers (or edits the answer of) a question; answering un-ignores it.
UPDATE questions
SET reply_subject = sqlc.arg(reply_subject)::text, reply_text = sqlc.arg(reply_text)::text,
    reply_timestamp = sqlc.arg(reply_timestamp)::timestamptz, admin_id = sqlc.narg(admin_id), ignored = false
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: SetQuestionIgnored :one
UPDATE questions SET ignored = $2, admin_id = $3 WHERE id = $1 RETURNING *;

-- name: DeleteQuestion :execrows
DELETE FROM questions WHERE id = $1;
