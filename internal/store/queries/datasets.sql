-- name: GetDataset :one
SELECT * FROM datasets WHERE id = $1;

-- name: ListDatasetsByTask :many
-- datasets_task_id_description_key serves the filter.
SELECT * FROM datasets WHERE task_id = $1 ORDER BY id;

-- name: GetActiveDataset :one
SELECT d.* FROM datasets d JOIN tasks t ON t.active_dataset_id = d.id WHERE t.id = sqlc.arg(task_id);

-- name: ListActiveDatasetsByContest :many
-- The active dataset of every task of a contest (limits on the task
-- pages, judging), in task order.
SELECT d.*
FROM datasets d
JOIN tasks t ON t.active_dataset_id = d.id
WHERE t.contest_id = sqlc.arg(contest_id)::integer
ORDER BY t.num, t.id;

-- name: ListJudgedDatasetsByTask :many
-- The datasets a new submission of the task is judged on: the active one
-- and the autojudge ones.
SELECT d.*
FROM datasets d
JOIN tasks t ON t.id = d.task_id
WHERE d.task_id = $1 AND (d.autojudge OR d.id = t.active_dataset_id)
ORDER BY d.id;

-- name: CreateDataset :one
INSERT INTO datasets (task_id, description, autojudge, time_limit, memory_limit,
                      task_type, task_type_parameters, score_type, score_type_parameters)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: UpdateDataset :one
UPDATE datasets
SET description = $2, autojudge = $3, time_limit = $4, memory_limit = $5,
    task_type = $6, task_type_parameters = $7, score_type = $8, score_type_parameters = $9
WHERE id = $1
RETURNING *;

-- name: CopyDatasetManagers :execrows
-- Cloning a dataset: copies the managers of one dataset into another.
INSERT INTO managers (dataset_id, filename, digest)
SELECT sqlc.arg(to_dataset_id)::integer, m.filename, m.digest
FROM managers m
WHERE m.dataset_id = sqlc.arg(from_dataset_id);

-- name: CopyDatasetTestcases :execrows
-- Cloning a dataset: copies the testcases of one dataset into another.
INSERT INTO testcases (dataset_id, codename, public, input, output)
SELECT sqlc.arg(to_dataset_id)::integer, tc.codename, tc.public, tc.input, tc.output
FROM testcases tc
WHERE tc.dataset_id = sqlc.arg(from_dataset_id);

-- name: DeleteDataset :execrows
-- Cascades to its managers, testcases and results. The active dataset of a
-- task cannot be deleted (fk_active_dataset_id, SQLSTATE 23503): make
-- another one live first.
DELETE FROM datasets WHERE id = $1;
