-- name: GetStatement :one
SELECT * FROM statements WHERE id = $1;

-- name: GetStatementByLanguage :one
-- Statement download (statements_task_id_language_key).
SELECT * FROM statements WHERE task_id = $1 AND language = $2;

-- name: ListStatementsByTask :many
SELECT * FROM statements WHERE task_id = $1 ORDER BY language;

-- name: ListStatementsByContest :many
-- The statements of every task of a contest in one query (task list
-- pages), in task order.
SELECT st.*
FROM statements st
JOIN tasks t ON t.id = st.task_id
WHERE t.contest_id = sqlc.arg(contest_id)::integer
ORDER BY t.num, t.id, st.language;

-- name: CreateStatement :one
INSERT INTO statements (task_id, language, digest) VALUES ($1, $2, $3) RETURNING *;

-- name: UpdateStatement :one
UPDATE statements SET language = $2, digest = $3 WHERE id = $1 RETURNING *;

-- name: DeleteStatement :execrows
DELETE FROM statements WHERE id = $1;
