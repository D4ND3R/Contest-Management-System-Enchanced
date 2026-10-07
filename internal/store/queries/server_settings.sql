-- name: GetServerSetting :one
SELECT * FROM server_settings WHERE key = $1;

-- name: ListServerSettings :many
SELECT * FROM server_settings ORDER BY key;

-- name: SetServerSetting :one
INSERT INTO server_settings (key, value, updated_at) VALUES ($1, $2, now())
ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
RETURNING *;

-- name: DeleteServerSetting :execrows
DELETE FROM server_settings WHERE key = $1;
