-- name: JudgingSubmissionStates :many
-- internal/judging Pending (upstream submission_get_operations): the state
-- of the given submissions on each dataset to judge (the active dataset of
-- their task and the autojudge ones, upstream get_datasets_to_judge),
-- whether or not the result row exists (a missing row is "compiling").
-- With all_datasets, also the results ready to score and not scored on the
-- other datasets of the task (a rescore: upstream
-- ScoringService.invalidate_submission scores what it invalidated on any
-- dataset).
SELECT s.id AS submission_id, s.participation_id, s.task_id, s.timestamp,
       d.id AS dataset_id, coalesce(d.id = t.active_dataset_id, false)::boolean AS active,
       coalesce(d.id = t.active_dataset_id OR d.autojudge, false)::boolean AS to_judge,
       (sr.submission_id IS NOT NULL)::boolean AS has_result,
       sr.compilation_outcome, coalesce(sr.compilation_tries, 0)::integer AS compilation_tries,
       sr.evaluation_outcome, coalesce(sr.evaluation_tries, 0)::integer AS evaluation_tries,
       coalesce(sr.generation, 0)::bigint AS generation,
       (sr.score IS NOT NULL AND sr.score_details IS NOT NULL AND sr.public_score IS NOT NULL
        AND sr.public_score_details IS NOT NULL AND sr.ranking_score_details IS NOT NULL)::boolean AS scored
FROM submissions s
JOIN tasks t ON t.id = s.task_id
JOIN datasets d ON d.task_id = s.task_id
LEFT JOIN submission_results sr ON sr.submission_id = s.id AND sr.dataset_id = d.id
WHERE s.id = ANY(sqlc.arg(submission_ids)::integer[])
  AND (d.id = t.active_dataset_id OR d.autojudge
       OR sqlc.arg(all_datasets)::boolean
          AND (sr.compilation_outcome = 'fail' OR sr.evaluation_outcome IS NOT NULL)
          AND (sr.score IS NULL OR sr.score_details IS NULL OR sr.public_score IS NULL
               OR sr.public_score_details IS NULL OR sr.ranking_score_details IS NULL))
ORDER BY s.id, d.id;

-- name: JudgingMissingEvaluations :many
-- The testcases of the dataset of each given result that have no
-- evaluation yet (through evaluations_submission_id_dataset_id_testcase_id_key):
-- one evaluation operation each. A result with no row here has all its
-- evaluations and only needs its evaluation outcome.
SELECT x.submission_id::integer AS submission_id, x.dataset_id::integer AS dataset_id, tc.codename
FROM (SELECT unnest(sqlc.arg(submission_ids)::integer[]) AS submission_id,
             unnest(sqlc.arg(dataset_ids)::integer[]) AS dataset_id) AS x
JOIN testcases tc ON tc.dataset_id = x.dataset_id
WHERE NOT EXISTS (
    SELECT 1 FROM evaluations e
    WHERE e.submission_id = x.submission_id AND e.dataset_id = x.dataset_id AND e.testcase_id = tc.id)
ORDER BY x.submission_id, x.dataset_id, tc.codename;

-- name: JudgingSweepMissingTaskScores :many
-- A sweep step (internal/judging Sweep) also looks for the participants
-- and tasks of the submissions with an id in (after, upto] of a contest
-- (0: of every contest) that have no row in the task score cache: a
-- database migrated from before it (0004), or submissions and results
-- written without RecomputeTask (an import, a restore). Every
-- recomputation creates the row, so once repaired nothing is found. No
-- new index: the submissions of the window are read through their primary
-- key (the first arm of JudgingSweepSubmissions reads them too), and the
-- cache is hashed while small and probed through its primary key once
-- much larger than the window (TestSweepAndScorePlans).
SELECT DISTINCT s.participation_id, s.task_id
FROM submissions s
JOIN tasks t ON t.id = s.task_id
WHERE s.id > sqlc.arg(after)::integer AND s.id <= sqlc.arg(upto)::integer
  AND (sqlc.arg(contest_id)::integer = 0 OR t.contest_id = sqlc.arg(contest_id)::integer)
  AND NOT EXISTS (
      SELECT 1 FROM task_scores ts
      WHERE ts.participation_id = s.participation_id AND ts.task_id = s.task_id)
ORDER BY s.participation_id, s.task_id;

-- name: JudgingUserTestStates :many
-- As JudgingSubmissionStates for user tests (upstream
-- user_test_get_operations).
SELECT ut.id AS user_test_id, ut.participation_id, ut.task_id, ut.timestamp,
       d.id AS dataset_id, coalesce(d.id = t.active_dataset_id, false)::boolean AS active,
       (r.user_test_id IS NOT NULL)::boolean AS has_result,
       r.compilation_outcome, coalesce(r.compilation_tries, 0)::integer AS compilation_tries,
       r.evaluation_outcome, coalesce(r.evaluation_tries, 0)::integer AS evaluation_tries,
       coalesce(r.generation, 0)::bigint AS generation
FROM user_tests ut
JOIN tasks t ON t.id = ut.task_id
JOIN datasets d ON d.task_id = ut.task_id AND (d.id = t.active_dataset_id OR d.autojudge)
LEFT JOIN user_test_results r ON r.user_test_id = ut.id AND r.dataset_id = d.id
WHERE ut.id = ANY(sqlc.arg(user_test_ids)::integer[])
ORDER BY ut.id, d.id;

-- name: JudgingSweepSubmissions :many
-- One step of the sweep (internal/judging Sweep, upstream
-- _missing_operations of EvaluationService and ScoringService): the
-- submissions with an id in (after, upto] of a contest (0: of every
-- contest) that still have work on a dataset to judge, the max_rows
-- smallest (each arm returns distinct ids, so that a submission with
-- work on several datasets counts once). Work is a dataset to judge without a result row (found
-- through the primary key of submission_results), or a result not scored
-- yet that can still progress (ix_submission_results_unfinished: the
-- scored results are not read).
SELECT c.id::integer AS submission_id
FROM (
    (SELECT DISTINCT s.id
     FROM submissions s
     JOIN tasks t ON t.id = s.task_id
     JOIN datasets d ON d.task_id = s.task_id AND (d.id = t.active_dataset_id OR d.autojudge)
     WHERE s.id > sqlc.arg(after)::integer AND s.id <= sqlc.arg(upto)::integer
       AND (sqlc.arg(contest_id)::integer = 0 OR t.contest_id = sqlc.arg(contest_id)::integer)
       AND NOT EXISTS (
           SELECT 1 FROM submission_results sr
           WHERE sr.submission_id = s.id AND sr.dataset_id = d.id
             -- The window again: the planner does not carry it over from
             -- s.id, and without it may hash every result of the table
             -- for each step (seen with 120 000 results: 35 ms, now 8 ms).
             AND sr.submission_id > sqlc.arg(after)::integer AND sr.submission_id <= sqlc.arg(upto)::integer)
     ORDER BY s.id
     LIMIT sqlc.arg(max_rows)::integer)
    UNION
    (SELECT DISTINCT sr.submission_id
     FROM submission_results sr
     JOIN submissions s ON s.id = sr.submission_id
     JOIN tasks t ON t.id = s.task_id
     JOIN datasets d ON d.id = sr.dataset_id AND (d.id = t.active_dataset_id OR d.autojudge)
     WHERE sr.submission_id > sqlc.arg(after)::integer AND sr.submission_id <= sqlc.arg(upto)::integer
       AND (sr.score IS NULL OR sr.score_details IS NULL OR sr.public_score IS NULL
            OR sr.public_score_details IS NULL OR sr.ranking_score_details IS NULL)
       AND (sqlc.arg(contest_id)::integer = 0 OR t.contest_id = sqlc.arg(contest_id)::integer)
       AND ((sr.compilation_outcome IS NULL AND sr.compilation_tries < sqlc.arg(max_compilation_tries)::integer)
            OR (sr.compilation_outcome = 'ok' AND sr.evaluation_outcome IS NULL
                AND sr.evaluation_tries < sqlc.arg(max_evaluation_tries)::integer)
            OR sr.compilation_outcome = 'fail' OR sr.evaluation_outcome IS NOT NULL)
     ORDER BY sr.submission_id
     LIMIT sqlc.arg(max_rows)::integer)
) AS c
ORDER BY c.id
LIMIT sqlc.arg(max_rows)::integer;

-- name: JudgingSweepUserTests :many
-- As JudgingSweepSubmissions for user tests (ix_user_test_results_unfinished).
SELECT c.id::integer AS user_test_id
FROM (
    (SELECT DISTINCT ut.id
     FROM user_tests ut
     JOIN tasks t ON t.id = ut.task_id
     JOIN datasets d ON d.task_id = ut.task_id AND (d.id = t.active_dataset_id OR d.autojudge)
     WHERE ut.id > sqlc.arg(after)::integer AND ut.id <= sqlc.arg(upto)::integer
       AND (sqlc.arg(contest_id)::integer = 0 OR t.contest_id = sqlc.arg(contest_id)::integer)
       AND NOT EXISTS (
           SELECT 1 FROM user_test_results r
           WHERE r.user_test_id = ut.id AND r.dataset_id = d.id
             AND r.user_test_id > sqlc.arg(after)::integer AND r.user_test_id <= sqlc.arg(upto)::integer)
     ORDER BY ut.id
     LIMIT sqlc.arg(max_rows)::integer)
    UNION
    (SELECT DISTINCT r.user_test_id
     FROM user_test_results r
     JOIN user_tests ut ON ut.id = r.user_test_id
     JOIN tasks t ON t.id = ut.task_id
     JOIN datasets d ON d.id = r.dataset_id AND (d.id = t.active_dataset_id OR d.autojudge)
     WHERE r.user_test_id > sqlc.arg(after)::integer AND r.user_test_id <= sqlc.arg(upto)::integer
       AND (r.compilation_outcome IS NULL OR (r.compilation_outcome = 'ok' AND r.evaluation_outcome IS NULL))
       AND (sqlc.arg(contest_id)::integer = 0 OR t.contest_id = sqlc.arg(contest_id)::integer)
       AND ((r.compilation_outcome IS NULL AND r.compilation_tries < sqlc.arg(max_compilation_tries)::integer)
            OR (r.compilation_outcome = 'ok' AND r.evaluation_outcome IS NULL
                AND r.evaluation_tries < sqlc.arg(max_evaluation_tries)::integer))
     ORDER BY r.user_test_id
     LIMIT sqlc.arg(max_rows)::integer)
) AS c
ORDER BY c.id
LIMIT sqlc.arg(max_rows)::integer;

-- name: JudgingMaxIDs :one
-- Where the sweep ends (primary keys).
SELECT coalesce((SELECT max(id) FROM submissions), 0)::integer AS max_submission_id,
       coalesce((SELECT max(id) FROM user_tests), 0)::integer AS max_user_test_id;

-- name: JudgingStaleTaskScores :many
-- Cached task scores computed on a dataset that is not the active one any
-- more (the active dataset was changed without internal/judging), to
-- recompute. Reads all of task_scores: one small row per participant and
-- task.
SELECT ts.participation_id, ts.task_id
FROM task_scores ts
JOIN tasks t ON t.id = ts.task_id
WHERE ts.dataset_id IS DISTINCT FROM t.active_dataset_id
  AND (sqlc.arg(contest_id)::integer = 0 OR t.contest_id = sqlc.arg(contest_id)::integer)
ORDER BY ts.participation_id, ts.task_id
LIMIT sqlc.arg(max_rows)::integer;
