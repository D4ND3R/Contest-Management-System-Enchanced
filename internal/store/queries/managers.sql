-- name: GetManager :one
SELECT * FROM managers WHERE id = $1;

-- name: ListManagersByDataset :many
-- managers_dataset_id_filename_key serves the filter and the order.
SELECT * FROM managers WHERE dataset_id = $1 ORDER BY filename;

-- name: CreateManager :one
INSERT INTO managers (dataset_id, filename, digest) VALUES ($1, $2, $3) RETURNING *;

-- name: UpdateManager :one
UPDATE managers SET filename = $2, digest = $3 WHERE id = $1 RETURNING *;

-- name: DeleteManager :execrows
DELETE FROM managers WHERE id = $1;
