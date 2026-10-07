-- name: GetMessage :one
SELECT * FROM messages WHERE id = $1;

-- name: ListMessagesByParticipation :many
-- Communication pages, newest first (ix_messages_participation_id).
SELECT * FROM messages WHERE participation_id = $1 ORDER BY timestamp DESC, id DESC;

-- name: ListMessagesSince :many
-- Contestant notifications: messages sent in (after, until]; a NULL
-- after means from the beginning.
SELECT * FROM messages
WHERE participation_id = sqlc.arg(participation_id)
  AND timestamp <= sqlc.arg(until)
  AND (sqlc.narg(after)::timestamptz IS NULL OR timestamp > sqlc.narg(after)::timestamptz)
ORDER BY timestamp, id;

-- name: CreateMessage :one
INSERT INTO messages (participation_id, admin_id, timestamp, subject, text)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: UpdateMessage :one
UPDATE messages SET subject = $2, text = $3, admin_id = $4 WHERE id = $1 RETURNING *;

-- name: DeleteMessage :execrows
DELETE FROM messages WHERE id = $1;
