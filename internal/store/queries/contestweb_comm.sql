-- name: CWSCommunicationPage :many
-- The communication page of a contestant (CMS communication.html): the
-- announcements of the contest, the questions of the participation with
-- their answers and the messages to it, newest first within each kind
-- (CMS shows contest.announcements, participation.questions and
-- participation.messages reversed). One query for the three lists
-- (ix_announcements_contest_id, ix_questions_participation_id,
-- ix_messages_participation_id). For an announcement or a message,
-- timestamp is its time and the reply columns are NULL; for a question,
-- timestamp is question_timestamp.
SELECT 'announcement'::text AS kind, a.id, a.timestamp, a.subject, a.text,
       NULL::timestamptz AS reply_timestamp, NULL::text AS reply_subject, NULL::text AS reply_text
FROM announcements a
WHERE a.contest_id = sqlc.arg(contest_id)
UNION ALL
SELECT 'message'::text, m.id, m.timestamp, m.subject, m.text, NULL, NULL, NULL
FROM messages m
WHERE m.participation_id = sqlc.arg(participation_id)
UNION ALL
SELECT 'question'::text, qn.id, qn.question_timestamp, qn.subject, qn.text,
       qn.reply_timestamp, qn.reply_subject, qn.reply_text
FROM questions qn
WHERE qn.participation_id = sqlc.arg(participation_id)
ORDER BY 1, 3 DESC, 6 DESC NULLS FIRST, 2 DESC;
