-- name: GetEvaluation :one
SELECT * FROM evaluations WHERE id = $1;

-- name: ListEvaluations :many
-- Submission details: the evaluations of a result with their testcase,
-- by codename (evaluations_submission_id_dataset_id_testcase_id_key).
SELECT sqlc.embed(e), tc.codename, tc.public
FROM evaluations e
JOIN testcases tc ON tc.id = e.testcase_id
WHERE e.submission_id = $1 AND e.dataset_id = $2
ORDER BY tc.codename;

-- name: ListUnevaluatedTestcases :many
-- The testcases of the dataset that a result has no evaluation for yet.
SELECT tc.*
FROM testcases tc
WHERE tc.dataset_id = sqlc.arg(dataset_id)
  AND NOT EXISTS (
      SELECT 1 FROM evaluations e
      WHERE e.submission_id = sqlc.arg(submission_id) AND e.dataset_id = tc.dataset_id AND e.testcase_id = tc.id)
ORDER BY tc.codename;

-- name: CreateEvaluation :execrows
-- Idempotent, first result wins: a repeated result for the same testcase
-- (a redelivered job) is ignored and affects 0 rows.
INSERT INTO evaluations (
    submission_id, dataset_id, testcase_id, outcome, text, admin_text,
    execution_time, execution_wall_clock_time, execution_memory,
    evaluation_shard, evaluation_sandbox_paths, evaluation_sandbox_digests
) VALUES (
    sqlc.arg(submission_id), sqlc.arg(dataset_id), sqlc.arg(testcase_id), sqlc.narg(outcome),
    coalesce(sqlc.arg(text)::text[], '{}'), sqlc.narg(admin_text),
    sqlc.narg(execution_time), sqlc.narg(execution_wall_clock_time), sqlc.narg(execution_memory),
    sqlc.narg(evaluation_shard), sqlc.narg(evaluation_sandbox_paths), sqlc.narg(evaluation_sandbox_digests)
)
ON CONFLICT (submission_id, dataset_id, testcase_id) DO NOTHING;

-- name: CreateEvaluationsBatch :batchexec
-- CreateEvaluation for many testcases in one round trip.
INSERT INTO evaluations (
    submission_id, dataset_id, testcase_id, outcome, text, admin_text,
    execution_time, execution_wall_clock_time, execution_memory,
    evaluation_shard, evaluation_sandbox_paths, evaluation_sandbox_digests
) VALUES (
    sqlc.arg(submission_id), sqlc.arg(dataset_id), sqlc.arg(testcase_id), sqlc.narg(outcome),
    coalesce(sqlc.arg(text)::text[], '{}'), sqlc.narg(admin_text),
    sqlc.narg(execution_time), sqlc.narg(execution_wall_clock_time), sqlc.narg(execution_memory),
    sqlc.narg(evaluation_shard), sqlc.narg(evaluation_sandbox_paths), sqlc.narg(evaluation_sandbox_digests)
)
ON CONFLICT (submission_id, dataset_id, testcase_id) DO NOTHING;

-- name: DeleteEvaluations :execrows
DELETE FROM evaluations WHERE submission_id = $1 AND dataset_id = $2;
