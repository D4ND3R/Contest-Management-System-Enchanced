-- Queries of the submissions, user tests and judges pages of the
-- administration (internal/adminweb, work C). The submission lists
-- themselves are store.ListSubmissions (D24); these load what the rows
-- of one page show (bounded by the page) and the single-object pages.

-- name: AdminDatasetsScoring :many
-- What builds the score types of the datasets of a page (CMS
-- dataset.score_type_object) in one query: each dataset with its task's
-- score precision and feedback level and its testcases (codename,
-- public) in codename order.
SELECT d.id, d.task_id, d.description, d.autojudge, d.score_type, d.score_type_parameters,
       t.score_precision, t.feedback_level, coalesce(t.active_dataset_id = d.id, false)::boolean AS active,
       coalesce(array_agg(tc.codename ORDER BY tc.codename COLLATE "C") FILTER (WHERE tc.id IS NOT NULL), '{}')::text[] AS codenames,
       coalesce(array_agg(tc.public ORDER BY tc.codename COLLATE "C") FILTER (WHERE tc.id IS NOT NULL), '{}')::boolean[] AS publics
FROM datasets d
JOIN tasks t ON t.id = d.task_id
LEFT JOIN testcases tc ON tc.dataset_id = d.id
WHERE d.id = ANY (sqlc.arg(dataset_ids)::integer[])
GROUP BY d.id, t.id
ORDER BY d.id;

-- name: AdminSubmissionDatasets :many
-- The dataset selector of a task (CMS orders by description; Python's
-- order is the code point order).
SELECT d.id, d.description, d.autojudge, coalesce(t.active_dataset_id = d.id, false)::boolean AS active
FROM datasets d
JOIN tasks t ON t.id = d.task_id
WHERE d.task_id = $1
ORDER BY d.description COLLATE "C", d.id;

-- name: AdminDatasetTask :one
-- The dataset submissions page: the dataset and its task.
SELECT sqlc.embed(d), sqlc.embed(t)
FROM datasets d
JOIN tasks t ON t.id = d.task_id
WHERE d.id = $1;

-- name: AdminSubmissionRowTexts :many
-- The parts of the results of a page of submissions that the rows hide
-- in their toggles (compilation text, score details): pairs
-- (submission_ids[i], dataset_ids[i]), one probe of the primary key each.
SELECT sr.submission_id, sr.compilation_text, sr.score_details
FROM submission_results sr
WHERE (sr.submission_id, sr.dataset_id) IN (
    SELECT unnest(sqlc.arg(submission_ids)::integer[]), unnest(sqlc.arg(dataset_ids)::integer[]))
ORDER BY sr.submission_id;

-- name: AdminEvaluationsOf :many
-- The evaluations of some results (pairs as AdminSubmissionRowTexts) with
-- their testcase, in codename order: the rows of a page that are
-- "Scoring..." show them, and the submission page (one pair).
SELECT e.submission_id, e.id, e.testcase_id, tc.codename, tc.public, e.outcome, e.text, e.admin_text,
       e.execution_time, e.execution_wall_clock_time, e.execution_memory, e.evaluation_shard,
       e.evaluation_sandbox_paths, e.evaluation_sandbox_digests
FROM evaluations e
JOIN testcases tc ON tc.id = e.testcase_id
WHERE (e.submission_id, e.dataset_id) IN (
    SELECT unnest(sqlc.arg(submission_ids)::integer[]), unnest(sqlc.arg(dataset_ids)::integer[]))
ORDER BY e.submission_id, tc.codename COLLATE "C", e.id;

-- name: AdminParticipationByUsername :one
-- The user filter of the submission lists: the participation of a
-- username in a contest.
SELECT p.id
FROM participations p
JOIN users u ON u.id = p.user_id
WHERE p.contest_id = sqlc.arg(contest_id)::integer AND u.username = sqlc.arg(username);

-- name: AdminSubmissionFile :one
-- A file of a submission with the submission's language (its name
-- replaces %l).
SELECT f.id, f.filename, f.digest, s.language
FROM files f
JOIN submissions s ON s.id = f.submission_id
WHERE f.id = $1;

-- name: AdminUserTestFile :one
-- A file of a user test with the test's language.
SELECT f.id, f.filename, f.digest, ut.language
FROM user_test_files f
JOIN user_tests ut ON ut.id = f.user_test_id
WHERE f.id = $1;

-- name: AdminDiffSubmissions :many
-- The two submissions of a diff: their task, language, the task's
-- submission format and their files.
SELECT s.id, s.task_id, s.language, t.submission_format,
       coalesce(array_agg(f.filename ORDER BY f.filename COLLATE "C") FILTER (WHERE f.id IS NOT NULL), '{}')::text[] AS filenames,
       coalesce(array_agg(f.digest ORDER BY f.filename COLLATE "C") FILTER (WHERE f.id IS NOT NULL), '{}')::text[] AS digests
FROM submissions s
JOIN tasks t ON t.id = s.task_id
LEFT JOIN files f ON f.submission_id = s.id
WHERE s.id = ANY (sqlc.arg(ids)::integer[])
GROUP BY s.id, t.id
ORDER BY s.id;

-- name: AdminSetSubmissionComment :one
UPDATE submissions SET comment = $2 WHERE id = $1 RETURNING *;

-- name: AdminSetSubmissionOfficial :one
UPDATE submissions SET official = $2 WHERE id = $1 RETURNING *;

-- name: AdminSubmissionContest :one
-- The contest (and participation) of a submission, for the audit log and
-- the live events of a change.
SELECT s.id, s.participation_id, s.task_id, p.contest_id
FROM submissions s
JOIN participations p ON p.id = s.participation_id
WHERE s.id = $1;

-- name: AdminCountUserTests :one
-- The number of user tests of a contest (optionally of a task or a
-- participation), for the pages of the list.
SELECT count(*)
FROM user_tests ut
JOIN tasks t ON t.id = ut.task_id
WHERE t.contest_id = sqlc.arg(contest_id)::integer
  AND (sqlc.narg(task_id)::integer IS NULL OR ut.task_id = sqlc.narg(task_id)::integer)
  AND (sqlc.narg(participation_id)::integer IS NULL OR ut.participation_id = sqlc.narg(participation_id)::integer);

-- name: AdminUserTests :many
-- A page of the user tests of a contest, newest first (CMS
-- render_params_for_user_tests): the page is chosen on the tests and
-- their tasks alone, then the participant, the result on the active
-- dataset and the files are joined to its rows (a deferred join, D24).
SELECT sqlc.embed(ut), p.contest_id, u.id AS user_id, u.username, page.task_name, page.active_dataset_id,
       (utr.user_test_id IS NOT NULL)::boolean AS has_result, utr.compilation_outcome, utr.compilation_text,
       utr.evaluation_outcome,
       coalesce((SELECT array_agg(f.id ORDER BY f.filename COLLATE "C") FROM user_test_files f WHERE f.user_test_id = ut.id), '{}')::integer[] AS file_ids,
       coalesce((SELECT array_agg(f.filename ORDER BY f.filename COLLATE "C") FROM user_test_files f WHERE f.user_test_id = ut.id), '{}')::text[] AS file_names
FROM (
    SELECT ut.id, t.name AS task_name, t.active_dataset_id
    FROM user_tests ut
    JOIN tasks t ON t.id = ut.task_id
    WHERE t.contest_id = sqlc.arg(contest_id)::integer
      AND (sqlc.narg(task_id)::integer IS NULL OR ut.task_id = sqlc.narg(task_id)::integer)
      AND (sqlc.narg(participation_id)::integer IS NULL OR ut.participation_id = sqlc.narg(participation_id)::integer)
    ORDER BY ut.timestamp DESC, ut.id DESC
    LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(page_offset)
) page
JOIN user_tests ut ON ut.id = page.id
JOIN participations p ON p.id = ut.participation_id
JOIN users u ON u.id = p.user_id
LEFT JOIN user_test_results utr ON utr.user_test_id = ut.id AND utr.dataset_id = page.active_dataset_id
ORDER BY ut.timestamp DESC, ut.id DESC;

-- name: AdminUserTestFiles :many
-- The files of a user test.
SELECT * FROM user_test_files WHERE user_test_id = $1 ORDER BY filename COLLATE "C";

-- name: AdminScopeContest :one
-- The contest an invalidation belongs to (for the audit log): of the
-- submission, else the participation, else the dataset's task, else the
-- task, else contest_id (0: none).
SELECT coalesce(
    (SELECT p.contest_id FROM submissions s JOIN participations p ON p.id = s.participation_id WHERE s.id = sqlc.narg(submission_id)::integer),
    (SELECT p.contest_id FROM participations p WHERE p.id = sqlc.narg(participation_id)::integer),
    (SELECT t.contest_id FROM datasets d JOIN tasks t ON t.id = d.task_id WHERE d.id = sqlc.narg(dataset_id)::integer),
    (SELECT t.contest_id FROM tasks t WHERE t.id = sqlc.narg(task_id)::integer),
    sqlc.narg(contest_id)::integer,
    0
)::integer AS contest_id;
