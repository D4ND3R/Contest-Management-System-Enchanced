-- name: AdminCoreContest :one
-- Every admin page of a contest (base.html): the contest with the times of
-- its main group (NULL columns if it has none) and the number of questions
-- neither answered nor ignored (the badge of the menu; CMS
-- render_params "unanswered").
SELECT sqlc.embed(c),
       g.name AS main_name, g.start AS main_start, g.stop AS main_stop,
       g.analysis_enabled AS main_analysis_enabled,
       g.analysis_start AS main_analysis_start, g.analysis_stop AS main_analysis_stop,
       g.per_user_time AS main_per_user_time,
       (SELECT count(*) FROM questions qn
        JOIN participations p ON p.id = qn.participation_id
        WHERE p.contest_id = c.id AND qn.reply_timestamp IS NULL AND NOT qn.ignored)::bigint AS unanswered
FROM contests c
LEFT JOIN groups g ON g.id = c.main_group_id
WHERE c.id = $1;

-- name: AdminCoreSidebar :one
-- Every global admin page (base.html): the first contests and tasks by
-- name (limit: the menu shows 5 and "show more" when there is a sixth) and
-- the numbers of users and teams, in one query (CMS loads all four tables
-- on every page).
SELECT
    (SELECT coalesce(jsonb_agg(jsonb_build_object('id', x.id, 'name', x.name, 'title', x.description)
                               ORDER BY x.name), '[]'::jsonb)
     FROM (SELECT id, name, description FROM contests ORDER BY name LIMIT sqlc.arg(lim)::integer) x)::jsonb AS contests,
    (SELECT coalesce(jsonb_agg(jsonb_build_object('id', x.id, 'name', x.name, 'title', x.title)
                               ORDER BY x.name), '[]'::jsonb)
     FROM (SELECT id, name, title FROM tasks ORDER BY name LIMIT sqlc.arg(lim)::integer) x)::jsonb AS tasks,
    (SELECT count(*) FROM users)::bigint AS users,
    (SELECT count(*) FROM teams)::bigint AS teams;

-- name: AdminCoreNewQuestions :many
-- The notifications of the admin pages (CMS NotificationsHandler): the
-- questions without an answer asked after a time, oldest first, with
-- their contest; bounded (a page polls every 15 s).
SELECT qn.id, qn.question_timestamp, qn.subject, qn.text, p.contest_id
FROM questions qn
JOIN participations p ON p.id = qn.participation_id
WHERE qn.reply_timestamp IS NULL AND qn.question_timestamp > sqlc.arg(after)::timestamptz
ORDER BY qn.question_timestamp, qn.id
LIMIT sqlc.arg(lim)::integer;

-- name: AdminCoreAdmins :many
-- The admins page (CMS AdminsHandler): enabled ones first, then by
-- username.
SELECT * FROM admins ORDER BY enabled DESC, username;

-- name: AdminCoreContestNames :many
-- The contest filter of the audit page.
SELECT id, name FROM contests ORDER BY name;

-- name: AdminCoreSubmissionsStatus :one
-- The "Submissions status" table of the overview of a contest (CMS
-- AdminWebServer.submissions_status): the submissions of the contest by
-- state of their result on the active dataset of their task; those
-- without a result count as compiling, as in CMS.
SELECT count(*)::bigint AS total,
       count(*) FILTER (WHERE sr.compilation_outcome IS NULL
           AND coalesce(sr.compilation_tries, 0) < sqlc.arg(max_compilation_tries)::integer)::bigint AS compiling,
       count(*) FILTER (WHERE sr.compilation_outcome IS NULL
           AND sr.compilation_tries >= sqlc.arg(max_compilation_tries)::integer)::bigint AS max_compilations,
       count(*) FILTER (WHERE sr.compilation_outcome = 'fail')::bigint AS compilation_fail,
       count(*) FILTER (WHERE sr.compilation_outcome = 'ok' AND sr.evaluation_outcome IS NULL
           AND sr.evaluation_tries < sqlc.arg(max_evaluation_tries)::integer)::bigint AS evaluating,
       count(*) FILTER (WHERE sr.compilation_outcome = 'ok' AND sr.evaluation_outcome IS NULL
           AND sr.evaluation_tries >= sqlc.arg(max_evaluation_tries)::integer)::bigint AS max_evaluations,
       count(*) FILTER (WHERE sr.compilation_outcome = 'ok' AND sr.evaluation_outcome IS NOT NULL
           AND NOT (sr.score IS NOT NULL AND sr.score_details IS NOT NULL AND sr.public_score IS NOT NULL
                    AND sr.public_score_details IS NOT NULL AND sr.ranking_score_details IS NOT NULL))::bigint AS scoring,
       count(*) FILTER (WHERE sr.compilation_outcome = 'ok' AND sr.evaluation_outcome IS NOT NULL
           AND sr.score IS NOT NULL AND sr.score_details IS NOT NULL AND sr.public_score IS NOT NULL
           AND sr.public_score_details IS NOT NULL AND sr.ranking_score_details IS NOT NULL)::bigint AS scored
FROM submissions s
JOIN tasks t ON t.id = s.task_id
LEFT JOIN submission_results sr ON sr.submission_id = s.id AND sr.dataset_id = t.active_dataset_id
WHERE t.contest_id = sqlc.arg(contest_id)::integer;

-- name: AdminCoreAuditLog :many
-- The audit page (R6.6), newest first, a page at a time: rows strictly
-- before (before_at, before_id) (NULL: from the newest), optionally of one
-- contest, one admin and one target type (and id). Each row carries the
-- current username of its admin (NULL if deleted) and the name of its
-- contest. The single-filter cases use their indexes (ix_audit_log_*);
-- combinations filter along them.
SELECT sqlc.embed(al), ad.username AS admin_username, ct.name AS contest_name
FROM audit_log al
LEFT JOIN admins ad ON ad.id = al.admin_id
LEFT JOIN contests ct ON ct.id = al.contest_id
WHERE (al.at, al.id) < (coalesce(sqlc.narg(before_at)::timestamptz, 'infinity'),
                        coalesce(sqlc.narg(before_id)::bigint, 9223372036854775807))
  AND (sqlc.narg(contest_id)::integer IS NULL OR al.contest_id = sqlc.narg(contest_id)::integer)
  AND (sqlc.narg(admin_id)::integer IS NULL OR al.admin_id = sqlc.narg(admin_id)::integer)
  AND (sqlc.narg(target_type)::text IS NULL OR al.target_type = sqlc.narg(target_type)::text)
  AND (sqlc.narg(target_id)::bigint IS NULL OR al.target_id = sqlc.narg(target_id)::bigint)
ORDER BY al.at DESC, al.id DESC
LIMIT sqlc.arg(page_size)::integer;
