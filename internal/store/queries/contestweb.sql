-- name: CWSContest :one
-- Every contestant page: the contest of the URL (contests_name_key) with
-- the times of its main group (NULL columns if it has none).
SELECT sqlc.embed(c),
       g.start AS main_start, g.stop AS main_stop,
       g.analysis_enabled AS main_analysis_enabled,
       g.analysis_start AS main_analysis_start, g.analysis_stop AS main_analysis_stop,
       g.per_user_time AS main_per_user_time
FROM contests c
LEFT JOIN groups g ON g.id = c.main_group_id
WHERE c.name = $1;

-- name: CWSContestList :many
-- The contest list of the contestant site (CMS ContestListHandler), by
-- name, with the end of each main group (finished contests are grey).
SELECT c.name, c.description, g.stop AS main_stop
FROM contests c
LEFT JOIN groups g ON g.id = c.main_group_id
ORDER BY c.name;

-- name: CWSTasks :many
-- The tasks of a contest in order (CMS contest.tasks) with the limits and
-- the task type of their active dataset (NULL columns without one): the
-- menu of every contestant page, the overview and the task pages, in one
-- query whatever the number of tasks (tasks_contest_id_num_key).
SELECT sqlc.embed(t),
       d.time_limit, d.memory_limit, d.task_type, d.task_type_parameters
FROM tasks t
LEFT JOIN datasets d ON d.id = t.active_dataset_id
WHERE t.contest_id = $1
ORDER BY t.num, t.id;

-- name: CWSCommunications :many
-- Contestant notifications (CMS get_communications): the announcements of
-- the contest, the messages to the participation and the answers to its
-- questions in (after, until]; a NULL after means from the beginning. An
-- answer without subject shows its text as the subject, as CMS does. One
-- query for the three kinds (ix_announcements_contest_id,
-- ix_messages_participation_id, ix_questions_participation_id).
SELECT 'announcement'::text AS kind, a.id, a.timestamp, a.subject, a.text
FROM announcements a
WHERE a.contest_id = sqlc.arg(contest_id)
  AND a.timestamp <= sqlc.arg(until)::timestamptz
  AND (sqlc.narg(after)::timestamptz IS NULL OR a.timestamp > sqlc.narg(after)::timestamptz)
UNION ALL
SELECT 'message'::text, m.id, m.timestamp, m.subject, m.text
FROM messages m
WHERE m.participation_id = sqlc.arg(participation_id)
  AND m.timestamp <= sqlc.arg(until)::timestamptz
  AND (sqlc.narg(after)::timestamptz IS NULL OR m.timestamp > sqlc.narg(after)::timestamptz)
UNION ALL
SELECT 'question'::text, qn.id, qn.reply_timestamp,
       coalesce(qn.reply_subject, qn.reply_text, ''),
       CASE WHEN qn.reply_subject IS NULL THEN '' ELSE coalesce(qn.reply_text, '') END
FROM questions qn
WHERE qn.participation_id = sqlc.arg(participation_id)
  AND qn.reply_timestamp IS NOT NULL
  AND qn.reply_timestamp <= sqlc.arg(until)::timestamptz
  AND (sqlc.narg(after)::timestamptz IS NULL OR qn.reply_timestamp > sqlc.narg(after)::timestamptz)
ORDER BY 1, 3, 2;

-- name: CWSRegisterParticipation :one
-- Registration (CMS RegistrationHandler): the participation of an
-- existing or new user in the main group of the contest; nothing when
-- the user already takes part (participations_contest_id_user_id_key).
INSERT INTO participations (contest_id, user_id, group_id, team_id)
SELECT c.id, sqlc.arg(user_id), c.main_group_id, sqlc.narg(team_id)
FROM contests c
WHERE c.id = sqlc.arg(contest_id) AND c.main_group_id IS NOT NULL
ON CONFLICT (contest_id, user_id) DO NOTHING
RETURNING id;

-- name: CWSTaskScores :many
-- The scores of a participant on the tasks of their contest from the
-- task_scores cache (D34), for the task pages and the API: one row per
-- task with official submissions (none: 0, not partial). Primary key of
-- task_scores.
SELECT task_id, score, public_score, tokened_score, partial
FROM task_scores
WHERE participation_id = $1
ORDER BY task_id;
