-- name: GetTask :one
SELECT * FROM tasks WHERE id = $1;

-- name: GetTaskByName :one
-- Task names are unique across contests (tasks_name_key).
SELECT * FROM tasks WHERE name = $1;

-- name: GetTaskByContestName :one
-- Task URLs of the contestant site (tasks_contest_id_name_key).
SELECT * FROM tasks WHERE contest_id = sqlc.arg(contest_id)::integer AND name = sqlc.arg(name);

-- name: ListTasksByContest :many
-- The tasks of a contest in their order (tasks_contest_id_num_key).
SELECT * FROM tasks WHERE contest_id = sqlc.arg(contest_id)::integer ORDER BY num, id;

-- name: ListTasks :many
-- Admin task list, with the contest of each task (NULL: none).
SELECT sqlc.embed(t), c.name AS contest_name
FROM tasks t
LEFT JOIN contests c ON c.id = t.contest_id
ORDER BY t.name;

-- name: ListTasksWithoutContest :many
-- Tasks that can be added to a contest. Scans tasks (a small table).
SELECT * FROM tasks WHERE contest_id IS NULL ORDER BY name;

-- name: CreateTask :one
-- The active dataset is set with SetTaskActiveDataset once it exists.
INSERT INTO tasks (
    contest_id, num, name, title, submission_format, primary_statements, allowed_languages,
    token_mode, token_max_number, token_min_interval, token_gen_initial, token_gen_number,
    token_gen_interval, token_gen_max, max_submission_number, max_user_test_number,
    min_submission_interval, min_user_test_interval, feedback_level, score_precision, score_mode
) VALUES (
    sqlc.narg(contest_id), sqlc.narg(num), sqlc.arg(name), sqlc.arg(title),
    coalesce(sqlc.arg(submission_format)::text[], '{}'), coalesce(sqlc.arg(primary_statements)::text[], '{}'),
    sqlc.narg(allowed_languages),
    sqlc.arg(token_mode), sqlc.narg(token_max_number), sqlc.arg(token_min_interval), sqlc.arg(token_gen_initial),
    sqlc.arg(token_gen_number), sqlc.arg(token_gen_interval), sqlc.narg(token_gen_max),
    sqlc.narg(max_submission_number), sqlc.narg(max_user_test_number),
    sqlc.narg(min_submission_interval), sqlc.narg(min_user_test_interval),
    sqlc.arg(feedback_level), sqlc.arg(score_precision), sqlc.arg(score_mode)
)
RETURNING *;

-- name: UpdateTask :one
-- Everything but the contest, the position and the active dataset, which
-- have their own queries.
UPDATE tasks SET
    name = sqlc.arg(name),
    title = sqlc.arg(title),
    submission_format = coalesce(sqlc.arg(submission_format)::text[], '{}'),
    primary_statements = coalesce(sqlc.arg(primary_statements)::text[], '{}'),
    allowed_languages = sqlc.narg(allowed_languages),
    token_mode = sqlc.arg(token_mode),
    token_max_number = sqlc.narg(token_max_number),
    token_min_interval = sqlc.arg(token_min_interval),
    token_gen_initial = sqlc.arg(token_gen_initial),
    token_gen_number = sqlc.arg(token_gen_number),
    token_gen_interval = sqlc.arg(token_gen_interval),
    token_gen_max = sqlc.narg(token_gen_max),
    max_submission_number = sqlc.narg(max_submission_number),
    max_user_test_number = sqlc.narg(max_user_test_number),
    min_submission_interval = sqlc.narg(min_submission_interval),
    min_user_test_interval = sqlc.narg(min_user_test_interval),
    feedback_level = sqlc.arg(feedback_level),
    score_precision = sqlc.arg(score_precision),
    score_mode = sqlc.arg(score_mode)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: SetTaskPosition :one
-- Moves a task into a contest at position num, or out of any contest
-- (both NULL). tasks_contest_id_num_key is not deferrable: swapping two
-- tasks goes through a NULL num, as in CMS.
UPDATE tasks SET contest_id = $2, num = $3 WHERE id = $1 RETURNING *;

-- name: SetTaskActiveDataset :one
-- fk_active_dataset_id checks that the dataset belongs to the task.
UPDATE tasks SET active_dataset_id = $2 WHERE id = $1 RETURNING *;

-- name: DeleteTask :execrows
-- Cascades to its statements, attachments, datasets and submissions.
DELETE FROM tasks WHERE id = $1;
