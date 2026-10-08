-- name: JudgingLockSubmissionResults :many
-- internal/judging Apply and Score: locks the result rows a batch is about
-- to change, in a fixed order (two dispatchers never deadlock, the second
-- waits and then sees what the first committed), and returns their state
-- with what scoring and the notifications need. The tasks are share-locked
-- so that their active dataset cannot change before the transaction ends:
-- otherwise a result scored on the dataset being activated could miss
-- both the recomputation of ActivateDataset and its own (the task score
-- cache would stay partial).
SELECT sr.submission_id, sr.dataset_id, sr.generation,
       sr.compilation_outcome, sr.compilation_tries, sr.evaluation_outcome, sr.evaluation_tries,
       (sr.score IS NOT NULL AND sr.score_details IS NOT NULL AND sr.public_score IS NOT NULL
        AND sr.public_score_details IS NOT NULL AND sr.ranking_score_details IS NOT NULL)::boolean AS scored,
       s.participation_id, s.task_id, s.timestamp, s.official, t.contest_id,
       coalesce(sr.dataset_id = t.active_dataset_id, false)::boolean AS active,
       coalesce(sr.dataset_id = t.active_dataset_id OR d.autojudge, false)::boolean AS to_judge,
       d.score_type, d.score_type_parameters, d.time_limit, t.score_precision
FROM submission_results sr
JOIN submissions s ON s.id = sr.submission_id
JOIN tasks t ON t.id = s.task_id
JOIN datasets d ON d.id = sr.dataset_id
WHERE (sr.submission_id, sr.dataset_id) IN (
    SELECT x.submission_id, x.dataset_id
    FROM (SELECT unnest(sqlc.arg(submission_ids)::integer[]) AS submission_id,
                 unnest(sqlc.arg(dataset_ids)::integer[]) AS dataset_id) AS x)
ORDER BY sr.submission_id, sr.dataset_id
FOR UPDATE OF sr FOR SHARE OF t;

-- name: JudgingSetSubmissionCompilation :batchone
-- Stores the outcome of a compilation built for this generation. Nothing
-- (pgx.ErrNoRows) for a duplicate or a stale result. compilation_tries
-- becomes the highest attempt seen (D23).
UPDATE submission_results SET
    compilation_outcome = sqlc.arg(compilation_outcome)::text,
    compilation_text = coalesce(sqlc.arg(compilation_text)::text[], '{}'),
    compilation_tries = greatest(compilation_tries, sqlc.arg(attempt)::integer),
    compilation_stdout = sqlc.narg(compilation_stdout),
    compilation_stderr = sqlc.narg(compilation_stderr),
    compilation_time = sqlc.narg(compilation_time),
    compilation_wall_clock_time = sqlc.narg(compilation_wall_clock_time),
    compilation_memory = sqlc.narg(compilation_memory),
    compilation_sandbox_paths = sqlc.narg(compilation_sandbox_paths),
    compilation_sandbox_digests = sqlc.narg(compilation_sandbox_digests)
WHERE submission_id = sqlc.arg(submission_id) AND dataset_id = sqlc.arg(dataset_id)
  AND generation = sqlc.arg(generation) AND compilation_outcome IS NULL
RETURNING compilation_tries;

-- name: JudgingFailSubmissionCompilation :batchone
-- Counts a failed compilation attempt of this generation: compilation_tries
-- becomes the attempt, once (D23). Nothing for a stale, repeated or
-- already superseded failure.
UPDATE submission_results SET compilation_tries = sqlc.arg(attempt)::integer
WHERE submission_id = sqlc.arg(submission_id) AND dataset_id = sqlc.arg(dataset_id)
  AND generation = sqlc.arg(generation) AND compilation_outcome IS NULL
  AND compilation_tries < sqlc.arg(attempt)::integer
RETURNING compilation_tries;

-- name: JudgingInsertExecutables :exec
-- The executables of the compilations just stored; first result wins.
INSERT INTO executables (submission_id, dataset_id, filename, digest)
SELECT x.submission_id, x.dataset_id, x.filename, x.digest
FROM (SELECT unnest(sqlc.arg(submission_ids)::integer[]) AS submission_id,
             unnest(sqlc.arg(dataset_ids)::integer[]) AS dataset_id,
             unnest(sqlc.arg(filenames)::text[]) AS filename,
             unnest(sqlc.arg(digests)::text[]) AS digest) AS x
ORDER BY x.submission_id, x.dataset_id, x.filename
ON CONFLICT (submission_id, dataset_id, filename) DO NOTHING;

-- name: JudgingInsertEvaluation :batchone
-- Stores the evaluation of a testcase, if the result is still being
-- evaluated in this generation; first result wins. Nothing for a stale or
-- repeated result, or an unknown testcase.
INSERT INTO evaluations (
    submission_id, dataset_id, testcase_id, outcome, text, admin_text,
    execution_time, execution_wall_clock_time, execution_memory,
    evaluation_sandbox_paths, evaluation_sandbox_digests
)
SELECT sr.submission_id, sr.dataset_id, tc.id, sqlc.narg(outcome)::text,
       coalesce(sqlc.arg(text)::text[], '{}'), sqlc.narg(admin_text)::text,
       sqlc.narg(execution_time)::double precision, sqlc.narg(execution_wall_clock_time)::double precision,
       sqlc.narg(execution_memory)::bigint,
       sqlc.narg(evaluation_sandbox_paths)::text[], sqlc.narg(evaluation_sandbox_digests)::text[]
FROM submission_results sr
JOIN testcases tc ON tc.dataset_id = sr.dataset_id AND tc.codename = sqlc.arg(codename)::text
WHERE sr.submission_id = sqlc.arg(submission_id)::integer AND sr.dataset_id = sqlc.arg(dataset_id)::integer
  AND sr.generation = sqlc.arg(generation)::bigint
  AND sr.compilation_outcome = 'ok' AND sr.evaluation_outcome IS NULL
ON CONFLICT (submission_id, dataset_id, testcase_id) DO NOTHING
RETURNING id;

-- name: JudgingFailSubmissionEvaluation :batchone
-- Counts a failed evaluation attempt of this generation, as
-- JudgingFailSubmissionCompilation: evaluation_tries is the highest
-- attempt in which some testcase failed.
UPDATE submission_results SET evaluation_tries = sqlc.arg(attempt)::integer
WHERE submission_id = sqlc.arg(submission_id) AND dataset_id = sqlc.arg(dataset_id)
  AND generation = sqlc.arg(generation) AND compilation_outcome = 'ok' AND evaluation_outcome IS NULL
  AND evaluation_tries < sqlc.arg(attempt)::integer
RETURNING evaluation_tries;

-- name: JudgingFinishEvaluations :many
-- Ends the evaluation of the given results (of the given generations)
-- whose every testcase has an evaluation (upstream set_evaluation_outcome
-- once the evaluations are as many as the testcases).
WITH done AS (
    UPDATE submission_results sr SET evaluation_outcome = 'ok'
    FROM (SELECT unnest(sqlc.arg(submission_ids)::integer[]) AS submission_id,
                 unnest(sqlc.arg(dataset_ids)::integer[]) AS dataset_id,
                 unnest(sqlc.arg(generations)::bigint[]) AS generation) AS x
    WHERE sr.submission_id = x.submission_id AND sr.dataset_id = x.dataset_id
      AND sr.generation = x.generation
      AND sr.compilation_outcome = 'ok' AND sr.evaluation_outcome IS NULL
      AND NOT EXISTS (
          SELECT 1 FROM testcases tc
          WHERE tc.dataset_id = sr.dataset_id
            AND NOT EXISTS (
                SELECT 1 FROM evaluations e
                WHERE e.submission_id = sr.submission_id AND e.dataset_id = sr.dataset_id
                  AND e.testcase_id = tc.id))
    RETURNING sr.submission_id, sr.dataset_id
)
SELECT submission_id, dataset_id FROM done ORDER BY submission_id, dataset_id;

-- name: JudgingTestcaseVisibility :many
-- The testcases of the datasets to score with, by codename, and whether
-- they are public (what a score type is built from).
SELECT dataset_id, codename, public
FROM testcases
WHERE dataset_id = ANY(sqlc.arg(dataset_ids)::integer[])
ORDER BY dataset_id, codename;

-- name: JudgingEvaluationsToScore :many
-- The evaluations of the results to score
-- (evaluations_submission_id_dataset_id_testcase_id_key).
SELECT e.submission_id, e.dataset_id, tc.codename, e.outcome, e.text, e.execution_time, e.execution_memory
FROM (SELECT unnest(sqlc.arg(submission_ids)::integer[]) AS submission_id,
             unnest(sqlc.arg(dataset_ids)::integer[]) AS dataset_id) AS x
JOIN evaluations e ON e.submission_id = x.submission_id AND e.dataset_id = x.dataset_id
JOIN testcases tc ON tc.id = e.testcase_id
ORDER BY e.submission_id, e.dataset_id, tc.codename;

-- name: JudgingSetScore :batchone
-- Stores the score of a result of this generation that is ready for it
-- (upstream needs_scoring); nothing if it is already scored or not ready.
-- scored_at keeps the first scoring time, as in CMS.
UPDATE submission_results SET
    score = sqlc.arg(score)::double precision,
    score_details = sqlc.arg(score_details)::jsonb,
    public_score = sqlc.arg(public_score)::double precision,
    public_score_details = sqlc.arg(public_score_details)::jsonb,
    ranking_score_details = coalesce(sqlc.arg(ranking_score_details)::text[], '{}'),
    scored_at = coalesce(scored_at, sqlc.arg(scored_at)::timestamptz)
WHERE submission_id = sqlc.arg(submission_id) AND dataset_id = sqlc.arg(dataset_id)
  AND generation = sqlc.arg(generation)
  AND (score IS NULL OR score_details IS NULL OR public_score IS NULL
       OR public_score_details IS NULL OR ranking_score_details IS NULL)
  AND (compilation_outcome = 'fail' OR evaluation_outcome IS NOT NULL)
RETURNING submission_id;

-- name: JudgingLockUserTestResults :many
-- As JudgingLockSubmissionResults for user test results.
SELECT r.user_test_id, r.dataset_id, r.generation,
       r.compilation_outcome, r.compilation_tries, r.evaluation_outcome, r.evaluation_tries,
       ut.participation_id, ut.task_id, ut.timestamp, t.contest_id,
       coalesce(r.dataset_id = t.active_dataset_id, false)::boolean AS active,
       coalesce(r.dataset_id = t.active_dataset_id OR d.autojudge, false)::boolean AS to_judge
FROM user_test_results r
JOIN user_tests ut ON ut.id = r.user_test_id
JOIN tasks t ON t.id = ut.task_id
JOIN datasets d ON d.id = r.dataset_id
WHERE (r.user_test_id, r.dataset_id) IN (
    SELECT x.user_test_id, x.dataset_id
    FROM (SELECT unnest(sqlc.arg(user_test_ids)::integer[]) AS user_test_id,
                 unnest(sqlc.arg(dataset_ids)::integer[]) AS dataset_id) AS x)
ORDER BY r.user_test_id, r.dataset_id
FOR UPDATE OF r;

-- name: JudgingSetUserTestCompilation :batchone
-- As JudgingSetSubmissionCompilation for user tests.
UPDATE user_test_results SET
    compilation_outcome = sqlc.arg(compilation_outcome)::text,
    compilation_text = coalesce(sqlc.arg(compilation_text)::text[], '{}'),
    compilation_tries = greatest(compilation_tries, sqlc.arg(attempt)::integer),
    compilation_stdout = sqlc.narg(compilation_stdout),
    compilation_stderr = sqlc.narg(compilation_stderr),
    compilation_time = sqlc.narg(compilation_time),
    compilation_wall_clock_time = sqlc.narg(compilation_wall_clock_time),
    compilation_memory = sqlc.narg(compilation_memory),
    compilation_sandbox_paths = sqlc.narg(compilation_sandbox_paths),
    compilation_sandbox_digests = sqlc.narg(compilation_sandbox_digests)
WHERE user_test_id = sqlc.arg(user_test_id) AND dataset_id = sqlc.arg(dataset_id)
  AND generation = sqlc.arg(generation) AND compilation_outcome IS NULL
RETURNING compilation_tries;

-- name: JudgingFailUserTestCompilation :batchone
-- As JudgingFailSubmissionCompilation for user tests.
UPDATE user_test_results SET compilation_tries = sqlc.arg(attempt)::integer
WHERE user_test_id = sqlc.arg(user_test_id) AND dataset_id = sqlc.arg(dataset_id)
  AND generation = sqlc.arg(generation) AND compilation_outcome IS NULL
  AND compilation_tries < sqlc.arg(attempt)::integer
RETURNING compilation_tries;

-- name: JudgingInsertUserTestExecutables :exec
-- As JudgingInsertExecutables for user tests.
INSERT INTO user_test_executables (user_test_id, dataset_id, filename, digest)
SELECT x.user_test_id, x.dataset_id, x.filename, x.digest
FROM (SELECT unnest(sqlc.arg(user_test_ids)::integer[]) AS user_test_id,
             unnest(sqlc.arg(dataset_ids)::integer[]) AS dataset_id,
             unnest(sqlc.arg(filenames)::text[]) AS filename,
             unnest(sqlc.arg(digests)::text[]) AS digest) AS x
ORDER BY x.user_test_id, x.dataset_id, x.filename
ON CONFLICT (user_test_id, dataset_id, filename) DO NOTHING;

-- name: JudgingSetUserTestEvaluation :batchone
-- Stores the execution of a user test compiled in this generation
-- (upstream EvaluationJob.to_user_test: the outcome is always "ok").
-- Nothing for a stale or repeated result.
UPDATE user_test_results SET
    evaluation_outcome = 'ok',
    evaluation_text = coalesce(sqlc.arg(evaluation_text)::text[], '{}'),
    evaluation_tries = greatest(evaluation_tries, sqlc.arg(attempt)::integer),
    output = sqlc.narg(output),
    execution_time = sqlc.narg(execution_time),
    execution_wall_clock_time = sqlc.narg(execution_wall_clock_time),
    execution_memory = sqlc.narg(execution_memory),
    evaluation_sandbox_paths = sqlc.narg(evaluation_sandbox_paths),
    evaluation_sandbox_digests = sqlc.narg(evaluation_sandbox_digests)
WHERE user_test_id = sqlc.arg(user_test_id) AND dataset_id = sqlc.arg(dataset_id)
  AND generation = sqlc.arg(generation) AND compilation_outcome = 'ok' AND evaluation_outcome IS NULL
RETURNING evaluation_tries;

-- name: JudgingFailUserTestEvaluation :batchone
-- As JudgingFailSubmissionEvaluation for user tests.
UPDATE user_test_results SET evaluation_tries = sqlc.arg(attempt)::integer
WHERE user_test_id = sqlc.arg(user_test_id) AND dataset_id = sqlc.arg(dataset_id)
  AND generation = sqlc.arg(generation) AND compilation_outcome = 'ok' AND evaluation_outcome IS NULL
  AND evaluation_tries < sqlc.arg(attempt)::integer
RETURNING evaluation_tries;
