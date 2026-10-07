-- name: GetUserTest :one
SELECT * FROM user_tests WHERE id = $1;

-- name: GetUserTestDetails :one
-- Admin user test page: the test with its participant, user and task.
SELECT sqlc.embed(ut), sqlc.embed(p), sqlc.embed(u), sqlc.embed(t)
FROM user_tests ut
JOIN participations p ON p.id = ut.participation_id
JOIN users u ON u.id = p.user_id
JOIN tasks t ON t.id = ut.task_id
WHERE ut.id = $1;

-- name: ListUserTestsByParticipationTask :many
-- Contestant testing page: the tests of the participant on the task,
-- oldest first, with their result on the active dataset (NULL columns
-- while there is none) (ix_user_tests_participation_id_task_id).
SELECT sqlc.embed(ut),
       utr.compilation_outcome, utr.compilation_text, utr.evaluation_outcome, utr.evaluation_text,
       utr.output, utr.execution_time, utr.execution_memory
FROM user_tests ut
JOIN tasks t ON t.id = ut.task_id
LEFT JOIN user_test_results utr ON utr.user_test_id = ut.id AND utr.dataset_id = t.active_dataset_id
WHERE ut.participation_id = $1 AND ut.task_id = $2
ORDER BY ut.timestamp, ut.id;

-- name: ListUserTestsByContest :many
-- Admin user tests page, newest first, one page at a time: rows strictly
-- before (before_timestamp, before_id) (NULL: from the newest). The page
-- is chosen on the tests and their tasks alone; the participant and the
-- result are joined to its rows only (a deferred join, as
-- store.ListSubmissions), then each test is read again by primary key.
SELECT sqlc.embed(ut), u.username, page.task_name,
       utr.compilation_outcome, utr.evaluation_outcome
FROM (
    SELECT ut.id, t.name AS task_name, t.active_dataset_id
    FROM user_tests ut
    JOIN tasks t ON t.id = ut.task_id
    WHERE t.contest_id = sqlc.arg(contest_id)::integer
      AND (ut.timestamp, ut.id) < (coalesce(sqlc.narg(before_timestamp)::timestamptz, 'infinity'),
                                   coalesce(sqlc.narg(before_id)::integer, 2147483647))
    ORDER BY ut.timestamp DESC, ut.id DESC
    LIMIT sqlc.arg(page_size)
) page
JOIN user_tests ut ON ut.id = page.id
JOIN participations p ON p.id = ut.participation_id
JOIN users u ON u.id = p.user_id
LEFT JOIN user_test_results utr ON utr.user_test_id = ut.id AND utr.dataset_id = page.active_dataset_id
ORDER BY ut.timestamp DESC, ut.id DESC;

-- name: GetUserTestStats :one
-- The test-time limits, as GetSubmissionStats for submissions.
SELECT n.task_count, n.contest_count, lt.timestamp AS task_last, lc.timestamp AS contest_last
FROM (
    SELECT count(*) FILTER (WHERE ut.task_id = sqlc.arg(task_id)) AS task_count, count(*) AS contest_count
    FROM user_tests ut
    JOIN tasks t ON t.id = ut.task_id
    WHERE ut.participation_id = sqlc.arg(participation_id) AND t.contest_id = sqlc.arg(contest_id)::integer
) n
LEFT JOIN user_tests lt ON lt.id = (
    SELECT ut.id FROM user_tests ut
    WHERE ut.participation_id = sqlc.arg(participation_id) AND ut.task_id = sqlc.arg(task_id)
    ORDER BY ut.timestamp DESC, ut.id DESC LIMIT 1)
LEFT JOIN user_tests lc ON lc.id = (
    SELECT ut.id FROM user_tests ut
    JOIN tasks t ON t.id = ut.task_id
    WHERE ut.participation_id = sqlc.arg(participation_id) AND t.contest_id = sqlc.arg(contest_id)::integer
    ORDER BY ut.timestamp DESC, ut.id DESC LIMIT 1);

-- name: CreateUserTest :one
INSERT INTO user_tests (participation_id, task_id, timestamp, language, input)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: DeleteUserTest :execrows
-- Cascades to its files, managers, results and executables.
DELETE FROM user_tests WHERE id = $1;
