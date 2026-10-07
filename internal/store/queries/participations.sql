-- name: GetParticipation :one
SELECT * FROM participations WHERE id = $1;

-- name: GetParticipationByContestUser :one
-- participations_contest_id_user_id_key.
SELECT * FROM participations WHERE contest_id = $1 AND user_id = $2;

-- name: GetContestant :one
-- What every contestant page needs about the logged-in participant, in
-- one query: the participation, its user and its group (contest times).
SELECT sqlc.embed(p), sqlc.embed(u), sqlc.embed(g)
FROM participations p
JOIN users u ON u.id = p.user_id
JOIN groups g ON g.id = p.group_id
WHERE p.id = $1;

-- name: GetContestantByUsername :one
-- Password login to a contest: users_username_key, then
-- participations_contest_id_user_id_key.
SELECT sqlc.embed(p), sqlc.embed(u), sqlc.embed(g)
FROM participations p
JOIN users u ON u.id = p.user_id
JOIN groups g ON g.id = p.group_id
WHERE p.contest_id = sqlc.arg(contest_id) AND u.username = sqlc.arg(username);

-- name: ListContestantsByIP :many
-- IP autologin: the participations of the contest whose subnets contain
-- the address (a NULL ip list never matches). Scans the contest's
-- participations, like CMS.
SELECT sqlc.embed(p), sqlc.embed(u), sqlc.embed(g)
FROM participations p
JOIN users u ON u.id = p.user_id
JOIN groups g ON g.id = p.group_id
WHERE p.contest_id = sqlc.arg(contest_id) AND sqlc.arg(ip)::inet <<= ANY (p.ip)
ORDER BY u.username;

-- name: ListParticipationsByContest :many
-- Admin contest users page and rankings: every participation with its
-- user, group and team (NULL columns when it has no team).
SELECT sqlc.embed(p), sqlc.embed(u), g.name AS group_name, tm.code AS team_code, tm.name AS team_name
FROM participations p
JOIN users u ON u.id = p.user_id
JOIN groups g ON g.id = p.group_id
LEFT JOIN teams tm ON tm.id = p.team_id
WHERE p.contest_id = $1
ORDER BY u.username;

-- name: ListParticipationsByUser :many
-- Admin user page and the contest list of a user (ix_participations_user_id).
SELECT sqlc.embed(p), c.name AS contest_name, c.description AS contest_description
FROM participations p
JOIN contests c ON c.id = p.contest_id
WHERE p.user_id = $1
ORDER BY c.name;

-- name: CreateParticipation :one
INSERT INTO participations (contest_id, user_id, group_id, team_id, ip, starting_time,
                            delay_time, extra_time, password, hidden, unrestricted)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: UpdateParticipation :one
UPDATE participations
SET group_id = $2, team_id = $3, ip = $4, starting_time = $5, delay_time = $6,
    extra_time = $7, password = $8, hidden = $9, unrestricted = $10
WHERE id = $1
RETURNING *;

-- name: StartParticipation :one
-- Starts the personal window (per_user_time) once: a second call keeps
-- the first time. Returns the effective starting time.
UPDATE participations SET starting_time = coalesce(starting_time, sqlc.arg(starting_time)::timestamptz)
WHERE id = sqlc.arg(id)
RETURNING starting_time;

-- name: DeleteParticipation :execrows
-- Cascades to its submissions, user tests, questions and messages.
DELETE FROM participations WHERE id = $1;
