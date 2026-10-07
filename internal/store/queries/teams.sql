-- name: GetTeam :one
SELECT * FROM teams WHERE id = $1;

-- name: GetTeamByCode :one
-- teams_code_key.
SELECT * FROM teams WHERE code = $1;

-- name: ListTeams :many
SELECT * FROM teams ORDER BY name, code;

-- name: CreateTeam :one
INSERT INTO teams (code, name) VALUES ($1, $2) RETURNING *;

-- name: UpdateTeam :one
UPDATE teams SET code = $2, name = $3 WHERE id = $1 RETURNING *;

-- name: DeleteTeam :execrows
-- Participations of the team keep existing without a team.
DELETE FROM teams WHERE id = $1;
