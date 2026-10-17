-- The communication pages of a contest in the administration (R6.3:
-- announcements, questions, private messages; internal/adminweb
-- communication.go) and the ranking (ranking.go).

-- name: AdminContestAnnouncements :many
-- The announcements of a contest, newest first (CMS shows
-- contest.announcements reversed), with the name of the author; bounded.
SELECT sqlc.embed(a), ad.name AS admin_name
FROM announcements a
LEFT JOIN admins ad ON ad.id = a.admin_id
WHERE a.contest_id = sqlc.arg(contest_id)
ORDER BY a.timestamp DESC, a.id DESC
LIMIT sqlc.arg(lim)::integer;

-- name: AdminContestQuestions :many
-- The questions page (CMS QuestionsHandler): unanswered and not ignored
-- first, then newest first; with the user and the name of the admin who
-- last handled each; bounded (the unanswered ones come first, so they are
-- always shown).
SELECT sqlc.embed(qn), u.id AS user_id, u.username, u.first_name, u.last_name, ad.name AS admin_name
FROM questions qn
JOIN participations p ON p.id = qn.participation_id
JOIN users u ON u.id = p.user_id
LEFT JOIN admins ad ON ad.id = qn.admin_id
WHERE p.contest_id = sqlc.arg(contest_id)
ORDER BY (qn.reply_timestamp IS NOT NULL OR qn.ignored), qn.question_timestamp DESC, qn.id
LIMIT sqlc.arg(lim)::integer;

-- name: AdminParticipationQuestions :many
-- The questions of one participation (CMS participation.questions order),
-- with the name of the admin who last handled each; bounded.
SELECT sqlc.embed(qn), ad.name AS admin_name
FROM questions qn
LEFT JOIN admins ad ON ad.id = qn.admin_id
WHERE qn.participation_id = sqlc.arg(participation_id)
ORDER BY qn.question_timestamp, qn.reply_timestamp, qn.id
LIMIT sqlc.arg(lim)::integer;

-- name: AdminParticipationMessages :many
-- The private messages of one participation, newest first, with the name
-- of the author; bounded.
SELECT sqlc.embed(m), ad.name AS admin_name
FROM messages m
LEFT JOIN admins ad ON ad.id = m.admin_id
WHERE m.participation_id = sqlc.arg(participation_id)
ORDER BY m.timestamp DESC, m.id DESC
LIMIT sqlc.arg(lim)::integer;

-- name: AdminQuestionForUpdate :one
-- A question with its contest and participation, locked for an action.
SELECT sqlc.embed(qn), p.contest_id, p.user_id
FROM questions qn
JOIN participations p ON p.id = qn.participation_id
WHERE qn.id = sqlc.arg(id)
FOR UPDATE OF qn;

-- name: AdminReplyQuestion :one
-- Answers (or edits the answer of) a question (CMS QuestionReplyHandler:
-- the ignored flag is left as it is).
UPDATE questions
SET reply_subject = sqlc.arg(reply_subject)::text, reply_text = sqlc.arg(reply_text)::text,
    reply_timestamp = sqlc.arg(reply_timestamp)::timestamptz, admin_id = sqlc.narg(admin_id)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: AdminSetQuestionAdmin :one
-- Claims (an admin) or releases (NULL) a question.
UPDATE questions SET admin_id = sqlc.narg(admin_id) WHERE id = sqlc.arg(id) RETURNING *;

-- name: AdminRanking :many
-- The ranking of a contest (CMS RankingHandler) from the task_scores
-- cache (D34): every participation with its user and team, and its score
-- and partial flag on each task of the contest in task order (no row: 0,
-- not partial).
SELECT p.id, p.hidden, p.team_id, u.id AS user_id, u.username, u.first_name, u.last_name,
       tm.name AS team_name,
       coalesce((SELECT array_agg(coalesce(ts.score, 0) ORDER BY t.num, t.id)
                 FROM tasks t LEFT JOIN task_scores ts ON ts.task_id = t.id AND ts.participation_id = p.id
                 WHERE t.contest_id = p.contest_id), '{}')::double precision[] AS scores,
       coalesce((SELECT array_agg(coalesce(ts.partial, false) ORDER BY t.num, t.id)
                 FROM tasks t LEFT JOIN task_scores ts ON ts.task_id = t.id AND ts.participation_id = p.id
                 WHERE t.contest_id = p.contest_id), '{}')::boolean[] AS partials
FROM participations p
JOIN users u ON u.id = p.user_id
LEFT JOIN teams tm ON tm.id = p.team_id
WHERE p.contest_id = sqlc.arg(contest_id)
ORDER BY u.username;
