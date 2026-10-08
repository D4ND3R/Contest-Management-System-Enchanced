-- name: JudgingEnsureSubmissionResults :exec
-- internal/judging BuildJobs: the empty ("compiling") result rows the
-- jobs are built for, so that every job carries the generation of an
-- existing row. Pairs whose submission or dataset is gone, or whose
-- dataset belongs to another task, are skipped; existing rows are kept.
INSERT INTO submission_results (submission_id, dataset_id)
SELECT x.submission_id, x.dataset_id
FROM (SELECT unnest(sqlc.arg(submission_ids)::integer[]) AS submission_id,
             unnest(sqlc.arg(dataset_ids)::integer[]) AS dataset_id) AS x
JOIN submissions s ON s.id = x.submission_id
JOIN datasets d ON d.id = x.dataset_id AND d.task_id = s.task_id
ORDER BY x.submission_id, x.dataset_id
ON CONFLICT (submission_id, dataset_id) DO NOTHING;

-- name: JudgingSubmissionJobData :many
-- What the jobs of the given results need besides files: the state of the
-- result (is the operation still needed? generation, tries), the
-- submission, the dataset and the languages of the contest.
SELECT sr.submission_id, sr.dataset_id, sr.generation,
       sr.compilation_outcome, sr.compilation_tries, sr.evaluation_outcome, sr.evaluation_tries,
       s.language, s.timestamp, s.task_id,
       d.task_type, d.task_type_parameters, d.time_limit, d.memory_limit,
       coalesce(c.languages, '{}')::text[] AS contest_languages
FROM (SELECT unnest(sqlc.arg(submission_ids)::integer[]) AS submission_id,
             unnest(sqlc.arg(dataset_ids)::integer[]) AS dataset_id) AS x
JOIN submission_results sr ON sr.submission_id = x.submission_id AND sr.dataset_id = x.dataset_id
JOIN submissions s ON s.id = sr.submission_id
JOIN datasets d ON d.id = sr.dataset_id
JOIN tasks t ON t.id = s.task_id
LEFT JOIN contests c ON c.id = t.contest_id
ORDER BY sr.submission_id, sr.dataset_id;

-- name: JudgingSubmissionFiles :many
-- The files of many submissions (files_submission_id_filename_key).
SELECT submission_id, filename, digest
FROM files
WHERE submission_id = ANY(sqlc.arg(submission_ids)::integer[])
ORDER BY submission_id, filename;

-- name: JudgingDatasetManagers :many
-- The managers of many datasets (managers_dataset_id_filename_key).
SELECT dataset_id, filename, digest
FROM managers
WHERE dataset_id = ANY(sqlc.arg(dataset_ids)::integer[])
ORDER BY dataset_id, filename;

-- name: JudgingTestcases :many
-- The named testcases (testcases_dataset_id_codename_key).
SELECT tc.id, tc.dataset_id, tc.codename, tc.input, tc.output
FROM (SELECT unnest(sqlc.arg(dataset_ids)::integer[]) AS dataset_id,
             unnest(sqlc.arg(codenames)::text[]) AS codename) AS x
JOIN testcases tc ON tc.dataset_id = x.dataset_id AND tc.codename = x.codename
ORDER BY tc.dataset_id, tc.codename;

-- name: JudgingSubmissionExecutables :many
-- The executables of many results (executables_submission_id_dataset_id_filename_key).
SELECT e.submission_id, e.dataset_id, e.filename, e.digest
FROM (SELECT unnest(sqlc.arg(submission_ids)::integer[]) AS submission_id,
             unnest(sqlc.arg(dataset_ids)::integer[]) AS dataset_id) AS x
JOIN executables e ON e.submission_id = x.submission_id AND e.dataset_id = x.dataset_id
ORDER BY e.submission_id, e.dataset_id, e.filename;

-- name: JudgingEnsureUserTestResults :exec
-- As JudgingEnsureSubmissionResults for user tests.
INSERT INTO user_test_results (user_test_id, dataset_id)
SELECT x.user_test_id, x.dataset_id
FROM (SELECT unnest(sqlc.arg(user_test_ids)::integer[]) AS user_test_id,
             unnest(sqlc.arg(dataset_ids)::integer[]) AS dataset_id) AS x
JOIN user_tests ut ON ut.id = x.user_test_id
JOIN datasets d ON d.id = x.dataset_id AND d.task_id = ut.task_id
ORDER BY x.user_test_id, x.dataset_id
ON CONFLICT (user_test_id, dataset_id) DO NOTHING;

-- name: JudgingUserTestJobData :many
-- As JudgingSubmissionJobData for user tests.
SELECT r.user_test_id, r.dataset_id, r.generation,
       r.compilation_outcome, r.compilation_tries, r.evaluation_outcome, r.evaluation_tries,
       ut.language, ut.timestamp, ut.task_id, ut.input,
       d.task_type, d.task_type_parameters, d.time_limit, d.memory_limit,
       coalesce(c.languages, '{}')::text[] AS contest_languages
FROM (SELECT unnest(sqlc.arg(user_test_ids)::integer[]) AS user_test_id,
             unnest(sqlc.arg(dataset_ids)::integer[]) AS dataset_id) AS x
JOIN user_test_results r ON r.user_test_id = x.user_test_id AND r.dataset_id = x.dataset_id
JOIN user_tests ut ON ut.id = r.user_test_id
JOIN datasets d ON d.id = r.dataset_id
JOIN tasks t ON t.id = ut.task_id
LEFT JOIN contests c ON c.id = t.contest_id
ORDER BY r.user_test_id, r.dataset_id;

-- name: JudgingUserTestFiles :many
-- The files of many user tests (user_test_files_user_test_id_filename_key).
SELECT user_test_id, filename, digest
FROM user_test_files
WHERE user_test_id = ANY(sqlc.arg(user_test_ids)::integer[])
ORDER BY user_test_id, filename;

-- name: JudgingUserTestManagers :many
-- The managers sent with many user tests (user_test_managers_user_test_id_filename_key).
SELECT user_test_id, filename, digest
FROM user_test_managers
WHERE user_test_id = ANY(sqlc.arg(user_test_ids)::integer[])
ORDER BY user_test_id, filename;

-- name: JudgingUserTestExecutables :many
-- The executables of many user test results (user_test_executables_user_test_id_dataset_id_filename_key).
SELECT e.user_test_id, e.dataset_id, e.filename, e.digest
FROM (SELECT unnest(sqlc.arg(user_test_ids)::integer[]) AS user_test_id,
             unnest(sqlc.arg(dataset_ids)::integer[]) AS dataset_id) AS x
JOIN user_test_executables e ON e.user_test_id = x.user_test_id AND e.dataset_id = x.dataset_id
ORDER BY e.user_test_id, e.dataset_id, e.filename;
