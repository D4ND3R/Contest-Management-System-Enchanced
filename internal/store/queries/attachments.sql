-- name: GetAttachment :one
SELECT * FROM attachments WHERE id = $1;

-- name: GetAttachmentByFilename :one
-- Attachment download (attachments_task_id_filename_key).
SELECT * FROM attachments WHERE task_id = $1 AND filename = $2;

-- name: ListAttachmentsByTask :many
SELECT * FROM attachments WHERE task_id = $1 ORDER BY filename;

-- name: CreateAttachment :one
INSERT INTO attachments (task_id, filename, digest) VALUES ($1, $2, $3) RETURNING *;

-- name: UpdateAttachment :one
UPDATE attachments SET filename = $2, digest = $3 WHERE id = $1 RETURNING *;

-- name: DeleteAttachment :execrows
DELETE FROM attachments WHERE id = $1;
