-- The administration pages of users, teams and participations
-- (internal/adminweb users*.go). Lists are paginated (D24) and every page
-- reads with a fixed number of queries.

-- name: AdminUsersCount :one
SELECT count(*)::bigint FROM users;

-- name: AdminUsersPage :many
-- The users page (CMS UserListHandler, by username), one page.
SELECT id, username, first_name, last_name
FROM users
ORDER BY username
LIMIT sqlc.arg(lim)::integer OFFSET sqlc.arg(off)::integer;

-- name: AdminTeamsCount :one
SELECT count(*)::bigint FROM teams;

-- name: AdminTeamsPage :many
-- The teams page (CMS TeamListHandler sorts by code), one page.
SELECT id, code, name
FROM teams
ORDER BY code
LIMIT sqlc.arg(lim)::integer OFFSET sqlc.arg(off)::integer;

-- name: AdminTeamCodes :many
-- The team codes offered by the participation form (bounded).
SELECT code FROM teams ORDER BY id LIMIT sqlc.arg(lim)::integer;

-- name: AdminUserRemoveCounts :one
-- What removing a user deletes (CMS RemoveUserHandler).
SELECT (SELECT count(*) FROM participations p WHERE p.user_id = sqlc.arg(user_id)::integer)::bigint AS participations,
       (SELECT count(*) FROM submissions s JOIN participations p ON p.id = s.participation_id
        WHERE p.user_id = sqlc.arg(user_id)::integer)::bigint AS submissions;

-- name: AdminTeamParticipationCount :one
-- The participations that lose their team when it is removed.
SELECT count(*)::bigint FROM participations WHERE team_id = sqlc.arg(team_id);

-- name: AdminDetachTeam :execrows
-- CMS RemoveTeamHandler clears the team of its participations first.
UPDATE participations SET team_id = NULL WHERE team_id = sqlc.arg(team_id);

-- name: AdminUserParticipations :many
-- The participations of a user (CMS UserHandler), with their contest and
-- group.
SELECT p.id, p.hidden, p.unrestricted, p.contest_id, c.name AS contest_name,
       c.description AS contest_description, g.name AS group_name
FROM participations p
JOIN contests c ON c.id = p.contest_id
JOIN groups g ON g.id = p.group_id
WHERE p.user_id = sqlc.arg(user_id)
ORDER BY p.id;

-- name: AdminContestsWithoutUser :many
-- The contests a user does not take part in (CMS unassigned_contests).
SELECT c.id, c.name
FROM contests c
WHERE NOT EXISTS (SELECT 1 FROM participations p WHERE p.contest_id = c.id AND p.user_id = sqlc.arg(user_id))
ORDER BY c.id;

-- name: AdminContestParticipationsCount :one
SELECT count(*)::bigint FROM participations WHERE contest_id = sqlc.arg(contest_id);

-- name: AdminContestParticipationsPage :many
-- The users of a contest (CMS ContestUsersHandler, by username), one
-- page.
SELECT u.id AS user_id, u.username, u.first_name, u.last_name, g.name AS group_name
FROM participations p
JOIN users u ON u.id = p.user_id
JOIN groups g ON g.id = p.group_id
WHERE p.contest_id = sqlc.arg(contest_id)
ORDER BY u.username
LIMIT sqlc.arg(lim)::integer OFFSET sqlc.arg(off)::integer;

-- name: AdminUsersNotInContest :many
-- The users that can be added to a contest (bounded).
SELECT u.id, u.username
FROM users u
WHERE NOT EXISTS (SELECT 1 FROM participations p WHERE p.user_id = u.id AND p.contest_id = sqlc.arg(contest_id))
ORDER BY u.id
LIMIT sqlc.arg(lim)::integer;

-- name: AdminParticipation :one
-- The participation page (CMS ParticipationHandler): the participation of
-- a user in a contest, with the user, the code of its team and its number
-- of submissions.
SELECT sqlc.embed(p), sqlc.embed(u), tm.code AS team_code,
       (SELECT count(*) FROM submissions s WHERE s.participation_id = p.id)::bigint AS submissions
FROM participations p
JOIN users u ON u.id = p.user_id
LEFT JOIN teams tm ON tm.id = p.team_id
WHERE p.contest_id = sqlc.arg(contest_id) AND p.user_id = sqlc.arg(user_id);

-- name: AdminImportTeams :many
-- Creates or renames the teams of a CSV import: (codes[i], names[i]);
-- an existing code keeps its id and gets the new name. Returns the teams
-- with whether each was created.
WITH up AS (
    INSERT INTO teams (code, name)
    SELECT unnest(sqlc.arg(codes)::text[]), unnest(sqlc.arg(names)::text[])
    ON CONFLICT (code) DO UPDATE SET name = excluded.name
    RETURNING id, code, name, (xmax = 0)::boolean AS created
)
SELECT id, code, name, created FROM up ORDER BY code;
