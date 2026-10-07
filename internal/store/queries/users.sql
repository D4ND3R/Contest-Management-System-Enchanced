-- name: GetUser :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByUsername :one
-- users_username_key.
SELECT * FROM users WHERE username = $1;

-- name: ListUsers :many
SELECT * FROM users ORDER BY username;

-- name: ListUsersNotInContest :many
-- Candidates for a new participation (admin contest users page).
SELECT * FROM users u
WHERE NOT EXISTS (SELECT 1 FROM participations p WHERE p.user_id = u.id AND p.contest_id = $1)
ORDER BY u.username;

-- name: CreateUser :one
INSERT INTO users (first_name, last_name, username, password, email, timezone, preferred_languages)
VALUES (sqlc.arg(first_name), sqlc.arg(last_name), sqlc.arg(username), sqlc.arg(password),
        sqlc.narg(email), sqlc.narg(timezone), coalesce(sqlc.arg(preferred_languages)::text[], '{}'))
RETURNING *;

-- name: UpdateUser :one
UPDATE users SET
    first_name = sqlc.arg(first_name),
    last_name = sqlc.arg(last_name),
    username = sqlc.arg(username),
    password = sqlc.arg(password),
    email = sqlc.narg(email),
    timezone = sqlc.narg(timezone),
    preferred_languages = coalesce(sqlc.arg(preferred_languages)::text[], '{}')
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: DeleteUser :execrows
-- Cascades to the participations of the user.
DELETE FROM users WHERE id = $1;
