-- name: GetAdmin :one
SELECT * FROM admins WHERE id = $1;

-- name: GetAdminByUsername :one
-- Login of the administration (admins_username_key).
SELECT * FROM admins WHERE username = $1;

-- name: ListAdmins :many
SELECT * FROM admins ORDER BY username;

-- name: CountSuperAdmins :one
-- Enabled admins with every permission: the last one cannot be disabled,
-- demoted or deleted.
SELECT count(*) FROM admins WHERE enabled AND permission_all;

-- name: CreateAdmin :one
INSERT INTO admins (name, username, authentication, enabled, permission_all, permission_messaging, permission_tasks)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: UpdateAdmin :one
UPDATE admins
SET name = $2, username = $3, authentication = $4, enabled = $5,
    permission_all = $6, permission_messaging = $7, permission_tasks = $8
WHERE id = $1
RETURNING *;

-- name: DeleteAdmin :execrows
DELETE FROM admins WHERE id = $1;
