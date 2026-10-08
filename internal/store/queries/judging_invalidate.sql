-- name: JudgingInvalidateCompilations :many
-- internal/judging Invalidate at the compilation level (upstream
-- invalidate_compilation) of results already locked: deletes their
-- executables and evaluations, blanks compilation, evaluation and score,
-- and bumps the generation, so that the results of the jobs in flight are
-- discarded (D23).
WITH keys AS (
    SELECT x.submission_id, x.dataset_id
    FROM (SELECT unnest(sqlc.arg(submission_ids)::integer[]) AS submission_id,
                 unnest(sqlc.arg(dataset_ids)::integer[]) AS dataset_id) AS x
), gone_executables AS (
    DELETE FROM executables e USING keys k
    WHERE e.submission_id = k.submission_id AND e.dataset_id = k.dataset_id
), gone_evaluations AS (
    DELETE FROM evaluations e USING keys k
    WHERE e.submission_id = k.submission_id AND e.dataset_id = k.dataset_id
), done AS (
    UPDATE submission_results sr SET
        compilation_outcome = NULL, compilation_text = '{}', compilation_tries = 0,
        compilation_stdout = NULL, compilation_stderr = NULL, compilation_time = NULL,
        compilation_wall_clock_time = NULL, compilation_memory = NULL, compilation_shard = NULL,
        compilation_sandbox_paths = NULL, compilation_sandbox_digests = NULL,
        evaluation_outcome = NULL, evaluation_tries = 0,
        score = NULL, score_details = NULL, public_score = NULL, public_score_details = NULL,
        ranking_score_details = NULL,
        generation = sr.generation + 1
    FROM keys k
    WHERE sr.submission_id = k.submission_id AND sr.dataset_id = k.dataset_id
    RETURNING sr.submission_id, sr.dataset_id, sr.generation
)
SELECT submission_id, dataset_id, generation FROM done ORDER BY submission_id, dataset_id;

-- name: JudgingInvalidateEvaluations :many
-- Invalidate at the evaluation level (upstream invalidate_evaluation):
-- deletes the evaluations (only those of one testcase when testcase_id is
-- not 0), blanks the evaluation outcome, its tries and the score, keeps
-- the compilation, bumps the generation.
WITH keys AS (
    SELECT x.submission_id, x.dataset_id
    FROM (SELECT unnest(sqlc.arg(submission_ids)::integer[]) AS submission_id,
                 unnest(sqlc.arg(dataset_ids)::integer[]) AS dataset_id) AS x
), gone_evaluations AS (
    DELETE FROM evaluations e USING keys k
    WHERE e.submission_id = k.submission_id AND e.dataset_id = k.dataset_id
      AND (sqlc.arg(testcase_id)::integer = 0 OR e.testcase_id = sqlc.arg(testcase_id)::integer)
), done AS (
    UPDATE submission_results sr SET
        evaluation_outcome = NULL, evaluation_tries = 0,
        score = NULL, score_details = NULL, public_score = NULL, public_score_details = NULL,
        ranking_score_details = NULL,
        generation = sr.generation + 1
    FROM keys k
    WHERE sr.submission_id = k.submission_id AND sr.dataset_id = k.dataset_id
    RETURNING sr.submission_id, sr.dataset_id, sr.generation
)
SELECT submission_id, dataset_id, generation FROM done ORDER BY submission_id, dataset_id;

-- name: JudgingInvalidateScores :many
-- Invalidate at the score level (upstream ScoringService
-- invalidate_submission): blanks the score of the scored results, which
-- are scored again; no worker is involved, so the generation stays.
WITH done AS (
    UPDATE submission_results sr SET
        score = NULL, score_details = NULL, public_score = NULL, public_score_details = NULL,
        ranking_score_details = NULL
    FROM (SELECT unnest(sqlc.arg(submission_ids)::integer[]) AS submission_id,
                 unnest(sqlc.arg(dataset_ids)::integer[]) AS dataset_id) AS x
    WHERE sr.submission_id = x.submission_id AND sr.dataset_id = x.dataset_id
      AND (sr.score IS NOT NULL OR sr.score_details IS NOT NULL OR sr.public_score IS NOT NULL
           OR sr.public_score_details IS NOT NULL OR sr.ranking_score_details IS NOT NULL)
    RETURNING sr.submission_id, sr.dataset_id, sr.generation
)
SELECT submission_id, dataset_id, generation FROM done ORDER BY submission_id, dataset_id;

-- name: JudgingLockUserTestResultsByID :many
-- The result rows of the given user tests on every dataset, locked in a
-- fixed order (user test invalidation).
SELECT r.user_test_id, r.dataset_id
FROM user_test_results r
WHERE r.user_test_id = ANY(sqlc.arg(user_test_ids)::integer[])
  AND (sqlc.arg(dataset_id)::integer = 0 OR r.dataset_id = sqlc.arg(dataset_id)::integer)
ORDER BY r.user_test_id, r.dataset_id
FOR UPDATE OF r;

-- name: JudgingInvalidateUserTestCompilations :many
-- As JudgingInvalidateCompilations for user tests (upstream
-- UserTestResult.invalidate_compilation): also drops the executables and
-- the output.
WITH keys AS (
    SELECT x.user_test_id, x.dataset_id
    FROM (SELECT unnest(sqlc.arg(user_test_ids)::integer[]) AS user_test_id,
                 unnest(sqlc.arg(dataset_ids)::integer[]) AS dataset_id) AS x
), gone_executables AS (
    DELETE FROM user_test_executables e USING keys k
    WHERE e.user_test_id = k.user_test_id AND e.dataset_id = k.dataset_id
), done AS (
    UPDATE user_test_results r SET
        compilation_outcome = NULL, compilation_text = '{}', compilation_tries = 0,
        compilation_stdout = NULL, compilation_stderr = NULL, compilation_time = NULL,
        compilation_wall_clock_time = NULL, compilation_memory = NULL, compilation_shard = NULL,
        compilation_sandbox_paths = NULL, compilation_sandbox_digests = NULL,
        evaluation_outcome = NULL, evaluation_text = '{}', evaluation_tries = 0, output = NULL,
        execution_time = NULL, execution_wall_clock_time = NULL, execution_memory = NULL,
        evaluation_shard = NULL, evaluation_sandbox_paths = NULL, evaluation_sandbox_digests = NULL,
        generation = r.generation + 1
    FROM keys k
    WHERE r.user_test_id = k.user_test_id AND r.dataset_id = k.dataset_id
    RETURNING r.user_test_id, r.dataset_id, r.generation
)
SELECT user_test_id, dataset_id, generation FROM done ORDER BY user_test_id, dataset_id;

-- name: JudgingInvalidateUserTestEvaluations :many
-- As JudgingInvalidateEvaluations for user tests (upstream
-- UserTestResult.invalidate_evaluation): the execution and its output.
WITH done AS (
    UPDATE user_test_results r SET
        evaluation_outcome = NULL, evaluation_text = '{}', evaluation_tries = 0, output = NULL,
        execution_time = NULL, execution_wall_clock_time = NULL, execution_memory = NULL,
        evaluation_shard = NULL, evaluation_sandbox_paths = NULL, evaluation_sandbox_digests = NULL,
        generation = r.generation + 1
    FROM (SELECT unnest(sqlc.arg(user_test_ids)::integer[]) AS user_test_id,
                 unnest(sqlc.arg(dataset_ids)::integer[]) AS dataset_id) AS x
    WHERE r.user_test_id = x.user_test_id AND r.dataset_id = x.dataset_id
    RETURNING r.user_test_id, r.dataset_id, r.generation
)
SELECT user_test_id, dataset_id, generation FROM done ORDER BY user_test_id, dataset_id;
