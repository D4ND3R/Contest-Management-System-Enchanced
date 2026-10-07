-- name: GetTestcase :one
SELECT * FROM testcases WHERE id = $1;

-- name: GetTestcaseByCodename :one
-- testcases_dataset_id_codename_key.
SELECT * FROM testcases WHERE dataset_id = $1 AND codename = $2;

-- name: ListTestcasesByDataset :many
-- testcases_dataset_id_codename_key serves the filter and the order.
SELECT * FROM testcases WHERE dataset_id = $1 ORDER BY codename;

-- name: CreateTestcase :one
INSERT INTO testcases (dataset_id, codename, public, input, output)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: CreateTestcases :copyfrom
-- Bulk import of the testcases of a dataset (COPY: one round trip).
INSERT INTO testcases (dataset_id, codename, public, input, output)
VALUES ($1, $2, $3, $4, $5);

-- name: UpdateTestcase :one
UPDATE testcases SET codename = $2, public = $3, input = $4, output = $5 WHERE id = $1 RETURNING *;

-- name: SetTestcasesPublic :execrows
-- The "public" checkboxes of the dataset page in one statement: the
-- listed testcases become public, the others private. No ids (a nil
-- slice, NULL) makes them all private: id = ANY (NULL) would be NULL.
UPDATE testcases
SET public = (id = ANY (coalesce(sqlc.arg(public_ids)::integer[], '{}')))
WHERE dataset_id = sqlc.arg(dataset_id);

-- name: DeleteTestcase :execrows
DELETE FROM testcases WHERE id = $1;
