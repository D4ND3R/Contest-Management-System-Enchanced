-- name: GetExecutable :one
SELECT * FROM executables WHERE id = $1;

-- name: ListExecutables :many
-- The executables of a result (executables_submission_id_dataset_id_filename_key).
SELECT * FROM executables WHERE submission_id = $1 AND dataset_id = $2 ORDER BY filename;

-- name: CreateExecutable :execrows
-- Idempotent, first result wins: a file already stored for the result is
-- kept (0 rows).
INSERT INTO executables (submission_id, dataset_id, filename, digest)
VALUES ($1, $2, $3, $4)
ON CONFLICT (submission_id, dataset_id, filename) DO NOTHING;

-- name: DeleteExecutables :execrows
DELETE FROM executables WHERE submission_id = $1 AND dataset_id = $2;
