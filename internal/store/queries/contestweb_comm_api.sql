-- name: CWSAPIStatementLanguages :many
-- The JSON API's task list (CMS ApiTaskListHandler): the languages of the
-- statements of every task of a contest, in the order they were added
-- (CMS iterates task.statements, unordered: the order of the rows). One
-- query whatever the number of tasks (statements_task_id_language_key).
SELECT s.task_id, array_agg(s.language ORDER BY s.id)::text[] AS languages
FROM statements s
JOIN tasks t ON t.id = s.task_id
WHERE t.contest_id = $1
GROUP BY s.task_id
ORDER BY s.task_id;

-- name: CWSAPISubmissionList :many
-- The JSON API's submission list (CMS ApiSubmissionListHandler): the
-- opaque ids of the submissions of a participant on a task, oldest first
-- (ix_submissions_participation_id_task_id).
SELECT s.opaque_id
FROM submissions s
WHERE s.participation_id = $1 AND s.task_id = $2
ORDER BY s.timestamp, s.id;
