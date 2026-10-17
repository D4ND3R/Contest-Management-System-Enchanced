-- The administration pages of contests, groups and the task list of a
-- contest (internal/adminweb contests*.go). Every page reads a bounded
-- number of rows with a fixed number of queries.

-- name: AdminContestsList :many
-- The contests page (CMS ContestListHandler: contest_list by name).
SELECT id, name, description FROM contests ORDER BY name;

-- name: AdminCreateContest :one
-- A new contest with the defaults of the schema (CMS AddContestHandler:
-- only the name, which is also the description).
INSERT INTO contests (name, description) VALUES (sqlc.arg(name), sqlc.arg(name)) RETURNING *;

-- name: AdminCreateDefaultGroup :one
-- The group "default" of a new contest, with the default times.
INSERT INTO groups (contest_id, name) VALUES (sqlc.arg(contest_id), 'default') RETURNING *;

-- name: AdminContestRemoveCounts :one
-- What removing a contest deletes (CMS RemoveContestHandler).
SELECT (SELECT count(*) FROM participations p WHERE p.contest_id = sqlc.arg(contest_id)::integer)::bigint AS participations,
       (SELECT count(*) FROM submissions s JOIN participations p ON p.id = s.participation_id
        WHERE p.contest_id = sqlc.arg(contest_id)::integer)::bigint AS submissions;

-- name: AdminContestGroups :many
-- The groups of a contest in the order CMS lists contest.groups (by id),
-- with their number of participations (deleting a group needs none).
SELECT sqlc.embed(g),
       (SELECT count(*) FROM participations p WHERE p.group_id = g.id)::bigint AS participations
FROM groups g
WHERE g.contest_id = sqlc.arg(contest_id)
ORDER BY g.id;

-- name: AdminGroupParticipationCount :one
-- A group can be deleted only without participations.
SELECT count(*)::bigint FROM participations WHERE group_id = sqlc.arg(group_id);

-- name: AdminGroupParticipants :many
-- The users of a group (CMS group.participations), bounded.
SELECT u.id, u.username, u.first_name, u.last_name
FROM participations p
JOIN users u ON u.id = p.user_id
WHERE p.group_id = sqlc.arg(group_id)
ORDER BY p.id
LIMIT sqlc.arg(lim)::integer;

-- name: AdminContestTasks :many
-- The tasks of a contest in their order (task list, ranking columns,
-- announcement subjects).
SELECT id, name, title, score_precision
FROM tasks
WHERE contest_id = sqlc.arg(contest_id)
ORDER BY num, id;

-- name: AdminContestTasksForUpdate :many
-- The tasks of a contest, locked, to move or remove one.
SELECT id, num
FROM tasks
WHERE contest_id = sqlc.arg(contest_id)
ORDER BY num, id
FOR UPDATE;

-- name: AdminUnassignedTasks :many
-- The tasks without a contest (CMS ContestTasksHandler unassigned_tasks).
SELECT id, name FROM tasks WHERE contest_id IS NULL ORDER BY id;

-- name: AdminClearTaskNums :exec
-- First step of renumbering the tasks of a contest:
-- tasks_contest_id_num_key is not deferrable, so the new positions are
-- written after every position is cleared.
UPDATE tasks SET num = NULL WHERE contest_id = sqlc.arg(contest_id);

-- name: AdminSetTaskNums :exec
-- Second step: tasks ids[i] get position nums[i] in the contest.
UPDATE tasks t SET contest_id = sqlc.arg(contest_id), num = v.num
FROM (SELECT unnest(sqlc.arg(ids)::integer[]) AS id, unnest(sqlc.arg(nums)::integer[]) AS num) v
WHERE t.id = v.id;
