-- name: GetAuditEntry :one
SELECT * FROM audit_log WHERE id = $1;

-- name: CreateAuditEntry :one
-- details defaults to {} when nil. Put the admin's username in details:
-- admin_id becomes NULL when the admin is deleted.
INSERT INTO audit_log (at, admin_id, action, target_type, target_id, contest_id, details, ip)
VALUES (
    sqlc.arg(at), sqlc.narg(admin_id), sqlc.arg(action), sqlc.arg(target_type), sqlc.narg(target_id),
    sqlc.narg(contest_id), coalesce(sqlc.narg(details)::jsonb, '{}'), sqlc.narg(ip)
)
RETURNING *;

-- name: ListAuditLog :many
-- The whole log newest first, a page at a time: rows strictly before
-- (before_at, before_id), NULL meaning from the newest (ix_audit_log_at).
-- Each row carries the current username of its admin (NULL if deleted).
SELECT sqlc.embed(al), ad.username AS admin_username
FROM audit_log al
LEFT JOIN admins ad ON ad.id = al.admin_id
WHERE (al.at, al.id) < (coalesce(sqlc.narg(before_at)::timestamptz, 'infinity'),
                        coalesce(sqlc.narg(before_id)::bigint, 9223372036854775807))
ORDER BY al.at DESC, al.id DESC
LIMIT sqlc.arg(page_size);

-- name: ListAuditLogByContest :many
-- As ListAuditLog for one contest (ix_audit_log_contest_id).
SELECT sqlc.embed(al), ad.username AS admin_username
FROM audit_log al
LEFT JOIN admins ad ON ad.id = al.admin_id
WHERE al.contest_id = sqlc.arg(contest_id)::integer
  AND (al.at, al.id) < (coalesce(sqlc.narg(before_at)::timestamptz, 'infinity'),
                        coalesce(sqlc.narg(before_id)::bigint, 9223372036854775807))
ORDER BY al.at DESC, al.id DESC
LIMIT sqlc.arg(page_size);

-- name: ListAuditLogByTarget :many
-- The history of one object, newest first (ix_audit_log_target).
SELECT sqlc.embed(al), ad.username AS admin_username
FROM audit_log al
LEFT JOIN admins ad ON ad.id = al.admin_id
WHERE al.target_type = sqlc.arg(target_type) AND al.target_id = sqlc.arg(target_id)::bigint
  AND (al.at, al.id) < (coalesce(sqlc.narg(before_at)::timestamptz, 'infinity'),
                        coalesce(sqlc.narg(before_id)::bigint, 9223372036854775807))
ORDER BY al.at DESC, al.id DESC
LIMIT sqlc.arg(page_size);

-- name: ListAuditLogByAdmin :many
-- The actions of one admin, newest first (ix_audit_log_admin_id).
SELECT sqlc.embed(al), ad.username AS admin_username
FROM audit_log al
LEFT JOIN admins ad ON ad.id = al.admin_id
WHERE al.admin_id = sqlc.arg(admin_id)::integer
  AND (al.at, al.id) < (coalesce(sqlc.narg(before_at)::timestamptz, 'infinity'),
                        coalesce(sqlc.narg(before_id)::bigint, 9223372036854775807))
ORDER BY al.at DESC, al.id DESC
LIMIT sqlc.arg(page_size);
