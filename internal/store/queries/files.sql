-- name: GetFile :one
SELECT * FROM files WHERE id = $1;

-- name: ListFilesBySubmission :many
-- files_submission_id_filename_key serves the filter and the order.
SELECT * FROM files WHERE submission_id = $1 ORDER BY filename;

-- name: ListFilesBySubmissions :many
-- The files of a page of submissions in one query (no N+1).
SELECT * FROM files
WHERE submission_id = ANY (sqlc.arg(submission_ids)::integer[])
ORDER BY submission_id, filename;

-- name: CreateFile :one
INSERT INTO files (submission_id, filename, digest) VALUES ($1, $2, $3) RETURNING *;

-- name: CreateFiles :execrows
-- Every file of a new submission in one statement; filenames[i] has
-- digest digests[i] (arrays of different lengths fail on NOT NULL).
INSERT INTO files (submission_id, filename, digest)
SELECT sqlc.arg(submission_id)::integer, unnest(sqlc.arg(filenames)::text[]), unnest(sqlc.arg(digests)::text[]);

-- name: DeleteFile :execrows
DELETE FROM files WHERE id = $1;
