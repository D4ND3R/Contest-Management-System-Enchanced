-- name: GetToken :one
SELECT * FROM tokens WHERE id = $1;

-- name: GetTokenBySubmission :one
-- tokens_submission_id_key.
SELECT * FROM tokens WHERE submission_id = $1;

-- name: ListTokensByParticipation :many
-- The tokens a participant played, oldest first, with the task of each
-- (token generation needs all of them).
SELECT sqlc.embed(tk), s.task_id
FROM tokens tk
JOIN submissions s ON s.id = tk.submission_id
WHERE s.participation_id = $1
ORDER BY tk.timestamp, tk.id;

-- name: CreateToken :one
-- A second token on the same submission fails on tokens_submission_id_key.
INSERT INTO tokens (submission_id, timestamp) VALUES ($1, $2) RETURNING *;

-- name: DeleteToken :execrows
DELETE FROM tokens WHERE id = $1;
