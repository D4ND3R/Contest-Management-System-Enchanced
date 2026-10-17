-- Problem preparation from the browser (internal/adminweb prepare*.go,
-- R6.2; new): the test participation of the problem preparers, their test
-- submissions with a verdict per testcase, and the user tests that
-- generate outputs and inputs.

-- name: AdminPrepParticipation :one
-- The hidden, unrestricted participation of the preparation user in a
-- contest (created on first use), in the main group of the contest or
-- else its first group.
INSERT INTO participations (contest_id, user_id, group_id, hidden, unrestricted)
SELECT c.id, sqlc.arg(user_id)::integer,
       coalesce(c.main_group_id, (SELECT min(g.id) FROM groups g WHERE g.contest_id = c.id)),
       true, true
FROM contests c
WHERE c.id = sqlc.arg(contest_id)::integer
ON CONFLICT (contest_id, user_id) DO UPDATE SET hidden = true, unrestricted = true
RETURNING id;

-- name: AdminPrepSubmissions :many
-- The latest test submissions of a task (those of the preparation user),
-- with their result on a dataset; ix_participations_user_id and
-- ix_submissions_participation_id_task_id.
SELECT s.id, s.timestamp, s.language, s.comment, s.participation_id,
       (r.submission_id IS NOT NULL)::boolean AS has_result,
       r.compilation_outcome, r.compilation_tries, r.compilation_text,
       r.evaluation_outcome, r.evaluation_tries, r.score, r.score_details
FROM submissions s
LEFT JOIN submission_results r ON r.submission_id = s.id AND r.dataset_id = sqlc.arg(dataset_id)::integer
WHERE s.task_id = sqlc.arg(task_id)::integer
  AND s.participation_id IN (SELECT p.id FROM participations p WHERE p.user_id = sqlc.arg(user_id)::integer)
ORDER BY s.timestamp DESC, s.id DESC
LIMIT sqlc.arg(max_rows)::integer;

-- name: AdminPrepEvaluations :many
-- The evaluations of some submissions on a dataset.
SELECT e.submission_id, e.testcase_id, e.outcome, e.text, e.execution_time, e.execution_memory
FROM evaluations e
WHERE e.dataset_id = sqlc.arg(dataset_id)::integer
  AND e.submission_id = ANY(sqlc.arg(submission_ids)::integer[])
ORDER BY e.submission_id, e.testcase_id;

-- name: AdminPrepUserTestResults :many
-- The state of the user tests that generate outputs or inputs, on a
-- dataset (no row yet: still compiling).
SELECT ut.id, ut.input, ut.task_id,
       (r.user_test_id IS NOT NULL)::boolean AS has_result,
       r.compilation_outcome, r.compilation_tries, r.compilation_text, r.compilation_stderr,
       r.evaluation_outcome, r.evaluation_tries, r.evaluation_text, r.output
FROM user_tests ut
LEFT JOIN user_test_results r ON r.user_test_id = ut.id AND r.dataset_id = sqlc.arg(dataset_id)::integer
WHERE ut.id = ANY(sqlc.arg(user_test_ids)::integer[])
ORDER BY ut.id;

-- name: AdminPrepSetTestcaseOutput :one
UPDATE testcases SET output = sqlc.arg(output) WHERE id = sqlc.arg(id) RETURNING *;
