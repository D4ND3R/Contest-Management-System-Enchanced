-- name: GetAnnouncement :one
SELECT * FROM announcements WHERE id = $1;

-- name: ListAnnouncementsByContest :many
-- Communication pages, newest first, with the author (NULL once the
-- admin is deleted). ix_announcements_contest_id.
SELECT sqlc.embed(a), ad.username AS admin_username
FROM announcements a
LEFT JOIN admins ad ON ad.id = a.admin_id
WHERE a.contest_id = $1
ORDER BY a.timestamp DESC, a.id DESC;

-- name: ListAnnouncementsSince :many
-- Contestant notifications: announcements published in (after, until];
-- a NULL after means from the beginning.
SELECT * FROM announcements
WHERE contest_id = sqlc.arg(contest_id)
  AND timestamp <= sqlc.arg(until)
  AND (sqlc.narg(after)::timestamptz IS NULL OR timestamp > sqlc.narg(after)::timestamptz)
ORDER BY timestamp, id;

-- name: CreateAnnouncement :one
INSERT INTO announcements (contest_id, admin_id, timestamp, subject, text)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: UpdateAnnouncement :one
UPDATE announcements SET subject = $2, text = $3, admin_id = $4 WHERE id = $1 RETURNING *;

-- name: DeleteAnnouncement :execrows
DELETE FROM announcements WHERE id = $1;
