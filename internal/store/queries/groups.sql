-- name: GetGroup :one
SELECT * FROM groups WHERE id = $1;

-- name: GetMainGroup :one
-- The default group of a contest (new participations go there).
SELECT g.* FROM groups g JOIN contests c ON c.main_group_id = g.id WHERE c.id = sqlc.arg(contest_id);

-- name: ListGroupsByContest :many
-- groups_contest_id_name_key serves the filter and the order.
SELECT * FROM groups WHERE contest_id = $1 ORDER BY name;

-- name: CreateGroup :one
INSERT INTO groups (contest_id, name, start, stop, analysis_enabled, analysis_start, analysis_stop, per_user_time)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: UpdateGroup :one
UPDATE groups
SET name = $2, start = $3, stop = $4, analysis_enabled = $5,
    analysis_start = $6, analysis_stop = $7, per_user_time = $8
WHERE id = $1
RETURNING *;

-- name: DeleteGroup :execrows
-- Cascades to the participations of the group.
DELETE FROM groups WHERE id = $1;
