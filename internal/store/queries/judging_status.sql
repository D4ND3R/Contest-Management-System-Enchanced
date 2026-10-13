-- name: JudgingSubmissionStatusCounts :many
-- cms ctl judging-status: the submissions of each task of a contest (0:
-- every contest) by state of their result on each dataset to judge
-- (upstream SubmissionResult.get_status; "gave up" are those that reached
-- the maximum number of tries and stay compiling or evaluating until an
-- admin reevaluates them). Reads every submission of the contest
-- (ix_submissions_task_id) with its results: an occasional admin command.
SELECT t.id AS task_id, t.name AS task_name, d.id AS dataset_id, d.description AS dataset_description,
       coalesce(d.id = t.active_dataset_id, false)::boolean AS active,
       count(*)::bigint AS submissions,
       count(*) FILTER (WHERE sr.compilation_outcome IS NULL
           AND coalesce(sr.compilation_tries, 0) < sqlc.arg(max_compilation_tries)::integer)::bigint AS compiling,
       count(*) FILTER (WHERE sr.compilation_outcome IS NULL
           AND sr.compilation_tries >= sqlc.arg(max_compilation_tries)::integer)::bigint AS compilation_gave_up,
       count(*) FILTER (WHERE sr.compilation_outcome = 'fail')::bigint AS compilation_failed,
       count(*) FILTER (WHERE sr.compilation_outcome = 'ok' AND sr.evaluation_outcome IS NULL
           AND sr.evaluation_tries < sqlc.arg(max_evaluation_tries)::integer)::bigint AS evaluating,
       count(*) FILTER (WHERE sr.compilation_outcome = 'ok' AND sr.evaluation_outcome IS NULL
           AND sr.evaluation_tries >= sqlc.arg(max_evaluation_tries)::integer)::bigint AS evaluation_gave_up,
       count(*) FILTER (WHERE sr.compilation_outcome = 'ok' AND sr.evaluation_outcome IS NOT NULL
           AND NOT (sr.score IS NOT NULL AND sr.score_details IS NOT NULL AND sr.public_score IS NOT NULL
                    AND sr.public_score_details IS NOT NULL AND sr.ranking_score_details IS NOT NULL))::bigint AS scoring,
       count(*) FILTER (WHERE sr.compilation_outcome = 'ok' AND sr.evaluation_outcome IS NOT NULL
           AND sr.score IS NOT NULL AND sr.score_details IS NOT NULL AND sr.public_score IS NOT NULL
           AND sr.public_score_details IS NOT NULL AND sr.ranking_score_details IS NOT NULL)::bigint AS scored
FROM tasks t
JOIN datasets d ON d.task_id = t.id AND (d.id = t.active_dataset_id OR d.autojudge)
JOIN submissions s ON s.task_id = t.id
LEFT JOIN submission_results sr ON sr.submission_id = s.id AND sr.dataset_id = d.id
WHERE (sqlc.arg(contest_id)::integer = 0 OR t.contest_id = sqlc.arg(contest_id)::integer)
GROUP BY t.id, t.name, d.id, d.description, t.active_dataset_id
ORDER BY t.contest_id, t.num, t.id, d.id;

-- name: JudgingUserTestStatusCounts :many
-- The same for user tests (upstream UserTestResult.get_status).
SELECT t.id AS task_id, t.name AS task_name, d.id AS dataset_id, d.description AS dataset_description,
       coalesce(d.id = t.active_dataset_id, false)::boolean AS active,
       count(*)::bigint AS user_tests,
       count(*) FILTER (WHERE r.compilation_outcome IS NULL
           AND coalesce(r.compilation_tries, 0) < sqlc.arg(max_compilation_tries)::integer)::bigint AS compiling,
       count(*) FILTER (WHERE r.compilation_outcome IS NULL
           AND r.compilation_tries >= sqlc.arg(max_compilation_tries)::integer)::bigint AS compilation_gave_up,
       count(*) FILTER (WHERE r.compilation_outcome = 'fail')::bigint AS compilation_failed,
       count(*) FILTER (WHERE r.compilation_outcome = 'ok' AND r.evaluation_outcome IS NULL
           AND r.evaluation_tries < sqlc.arg(max_evaluation_tries)::integer)::bigint AS evaluating,
       count(*) FILTER (WHERE r.compilation_outcome = 'ok' AND r.evaluation_outcome IS NULL
           AND r.evaluation_tries >= sqlc.arg(max_evaluation_tries)::integer)::bigint AS evaluation_gave_up,
       count(*) FILTER (WHERE r.compilation_outcome = 'ok' AND r.evaluation_outcome IS NOT NULL)::bigint AS evaluated
FROM tasks t
JOIN datasets d ON d.task_id = t.id AND (d.id = t.active_dataset_id OR d.autojudge)
JOIN user_tests ut ON ut.task_id = t.id
LEFT JOIN user_test_results r ON r.user_test_id = ut.id AND r.dataset_id = d.id
WHERE (sqlc.arg(contest_id)::integer = 0 OR t.contest_id = sqlc.arg(contest_id)::integer)
GROUP BY t.id, t.name, d.id, d.description, t.active_dataset_id
ORDER BY t.contest_id, t.num, t.id, d.id;

-- name: JudgingProgress :many
-- The progress counters of the admin pages, which the dispatcher publishes
-- at most once a second while judging: the unfinished submission results
-- on the datasets to judge, by contest (0: tasks without a contest). The
-- WHERE clause is the predicate of ix_submission_results_unfinished, so
-- only unfinished results are read, never the scored ones (almost every
-- row during a contest). Submissions received a moment ago, which have no
-- result row until their compilation job is built, are not counted.
SELECT coalesce(t.contest_id, 0)::integer AS contest_id,
       count(*) FILTER (WHERE sr.compilation_outcome IS NULL
           AND sr.compilation_tries < sqlc.arg(max_compilation_tries)::integer)::bigint AS compiling,
       count(*) FILTER (WHERE sr.compilation_outcome = 'ok' AND sr.evaluation_outcome IS NULL
           AND sr.evaluation_tries < sqlc.arg(max_evaluation_tries)::integer)::bigint AS evaluating,
       count(*) FILTER (WHERE sr.compilation_outcome = 'ok' AND sr.evaluation_outcome IS NOT NULL)::bigint AS scoring
FROM submission_results sr
JOIN submissions s ON s.id = sr.submission_id
JOIN tasks t ON t.id = s.task_id
JOIN datasets d ON d.id = sr.dataset_id AND (d.id = t.active_dataset_id OR d.autojudge)
WHERE sr.score IS NULL OR sr.score_details IS NULL OR sr.public_score IS NULL
   OR sr.public_score_details IS NULL OR sr.ranking_score_details IS NULL
GROUP BY coalesce(t.contest_id, 0)
ORDER BY 1;
