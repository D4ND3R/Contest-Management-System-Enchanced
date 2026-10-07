-- name: GetUserTestResult :one
SELECT * FROM user_test_results WHERE user_test_id = $1 AND dataset_id = $2;

-- name: ListUserTestResults :many
-- Admin user test page: the results on every dataset.
SELECT sqlc.embed(utr), d.description AS dataset_description
FROM user_test_results utr
JOIN datasets d ON d.id = utr.dataset_id
WHERE utr.user_test_id = $1
ORDER BY d.id;

-- name: CreateUserTestResult :execrows
-- The empty ("compiling") result of a user test on a dataset; does
-- nothing if it exists.
INSERT INTO user_test_results (user_test_id, dataset_id) VALUES ($1, $2)
ON CONFLICT (user_test_id, dataset_id) DO NOTHING;

-- name: SetUserTestCompilation :execrows
-- As SetSubmissionCompilation: creates the row if needed, a NULL outcome
-- records a failed attempt, compilation_tries is the highest attempt
-- seen, first result wins (0 rows afterwards, and for a failed attempt
-- already counted).
INSERT INTO user_test_results AS utr (
    user_test_id, dataset_id, compilation_outcome, compilation_text, compilation_tries,
    compilation_stdout, compilation_stderr, compilation_time, compilation_wall_clock_time,
    compilation_memory, compilation_shard, compilation_sandbox_paths, compilation_sandbox_digests
) VALUES (
    sqlc.arg(user_test_id), sqlc.arg(dataset_id), sqlc.narg(compilation_outcome),
    coalesce(sqlc.arg(compilation_text)::text[], '{}'), sqlc.arg(attempt)::integer,
    sqlc.narg(compilation_stdout), sqlc.narg(compilation_stderr), sqlc.narg(compilation_time),
    sqlc.narg(compilation_wall_clock_time), sqlc.narg(compilation_memory), sqlc.narg(compilation_shard),
    sqlc.narg(compilation_sandbox_paths), sqlc.narg(compilation_sandbox_digests)
)
ON CONFLICT (user_test_id, dataset_id) DO UPDATE SET
    compilation_outcome = excluded.compilation_outcome,
    compilation_text = excluded.compilation_text,
    compilation_tries = greatest(utr.compilation_tries, excluded.compilation_tries),
    compilation_stdout = excluded.compilation_stdout,
    compilation_stderr = excluded.compilation_stderr,
    compilation_time = excluded.compilation_time,
    compilation_wall_clock_time = excluded.compilation_wall_clock_time,
    compilation_memory = excluded.compilation_memory,
    compilation_shard = excluded.compilation_shard,
    compilation_sandbox_paths = excluded.compilation_sandbox_paths,
    compilation_sandbox_digests = excluded.compilation_sandbox_digests
WHERE utr.compilation_outcome IS NULL
  AND (excluded.compilation_outcome IS NOT NULL OR utr.compilation_tries < excluded.compilation_tries);

-- name: SetUserTestEvaluation :execrows
-- Stores the execution of a compiled user test; a NULL outcome records a
-- failed attempt. evaluation_tries is the highest attempt seen, as in
-- SetSubmissionEvaluation. First result wins (0 rows afterwards, and for
-- a failed attempt already counted).
UPDATE user_test_results SET
    evaluation_outcome = sqlc.narg(evaluation_outcome),
    evaluation_text = coalesce(sqlc.arg(evaluation_text)::text[], '{}'),
    evaluation_tries = greatest(evaluation_tries, sqlc.arg(attempt)::integer),
    output = sqlc.narg(output),
    execution_time = sqlc.narg(execution_time),
    execution_wall_clock_time = sqlc.narg(execution_wall_clock_time),
    execution_memory = sqlc.narg(execution_memory),
    evaluation_shard = sqlc.narg(evaluation_shard),
    evaluation_sandbox_paths = sqlc.narg(evaluation_sandbox_paths),
    evaluation_sandbox_digests = sqlc.narg(evaluation_sandbox_digests)
WHERE user_test_id = sqlc.arg(user_test_id) AND dataset_id = sqlc.arg(dataset_id)
  AND compilation_outcome = 'ok' AND evaluation_outcome IS NULL
  AND (sqlc.narg(evaluation_outcome)::text IS NOT NULL OR evaluation_tries < sqlc.arg(attempt)::integer);

-- name: DeleteUserTestResult :execrows
-- Cascades to its executables.
DELETE FROM user_test_results WHERE user_test_id = $1 AND dataset_id = $2;
