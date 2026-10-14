-- name: ImportTasksByName :many
-- The tasks an import creates or updates, by name (tasks_name_key), with
-- the name of their contest (NULL: none). FOR UPDATE: the import changes
-- them in the same transaction, so a concurrent import of the same task
-- waits for this one.
SELECT sqlc.embed(t), c.name AS contest_name
FROM tasks t
LEFT JOIN contests c ON c.id = t.contest_id
WHERE t.name = ANY (sqlc.arg(names)::text[])
ORDER BY t.name
FOR UPDATE OF t;

-- name: ImportLockContest :one
-- The contest an import creates or updates (contests_name_key), locked
-- until the import commits.
SELECT * FROM contests WHERE name = $1 FOR UPDATE;

-- name: ImportLockContestByID :one
-- The contest a task is imported into, locked: the position of the new
-- task is the next free one.
SELECT * FROM contests WHERE id = $1 FOR UPDATE;

-- name: ImportNextTaskNum :one
-- The position of a task added at the end of a contest
-- (tasks_contest_id_num_key).
SELECT coalesce(max(num) + 1, 0)::integer AS num FROM tasks WHERE contest_id = sqlc.arg(contest_id)::integer;

-- name: ImportDetachContestTasks :execrows
-- Detaches every task of a contest before an import attaches its task
-- list again in order (upstream ImportContest), so that the new positions
-- never collide with the old ones (tasks_contest_id_num_key is not
-- deferrable).
UPDATE tasks SET contest_id = NULL, num = NULL WHERE contest_id = sqlc.arg(contest_id)::integer;

-- name: ImportAttachTasks :execrows
-- Attaches tasks to a contest at the given positions, in one statement.
UPDATE tasks t SET contest_id = sqlc.arg(contest_id)::integer, num = a.num
FROM (SELECT unnest(sqlc.arg(ids)::integer[]) AS id, unnest(sqlc.arg(nums)::integer[]) AS num) a
WHERE t.id = a.id;

-- name: ImportDatasetsWithResults :many
-- The datasets, among these, that have results: an import that changes
-- their judging data in place warns that the results may be stale
-- (ix_submission_results_dataset_id).
SELECT d.id FROM datasets d
WHERE d.id = ANY (sqlc.arg(ids)::integer[])
  AND EXISTS (SELECT 1 FROM submission_results r WHERE r.dataset_id = d.id)
ORDER BY d.id;

-- name: ImportUpsertStatements :execrows
-- Statements of a task by language in one statement
-- (statements_task_id_language_key): new languages are added, the others
-- get the new file.
INSERT INTO statements (task_id, language, digest)
SELECT sqlc.arg(task_id)::integer, unnest(sqlc.arg(languages)::text[]), unnest(sqlc.arg(digests)::text[])
ON CONFLICT (task_id, language) DO UPDATE SET digest = EXCLUDED.digest;

-- name: ImportDeleteStatements :execrows
DELETE FROM statements WHERE id = ANY (sqlc.arg(ids)::integer[]);

-- name: ImportUpsertAttachments :execrows
-- Attachments of a task by file name (attachments_task_id_filename_key).
INSERT INTO attachments (task_id, filename, digest)
SELECT sqlc.arg(task_id)::integer, unnest(sqlc.arg(filenames)::text[]), unnest(sqlc.arg(digests)::text[])
ON CONFLICT (task_id, filename) DO UPDATE SET digest = EXCLUDED.digest;

-- name: ImportDeleteAttachments :execrows
DELETE FROM attachments WHERE id = ANY (sqlc.arg(ids)::integer[]);

-- name: ImportUpsertManagers :execrows
-- Managers of a dataset by file name (managers_dataset_id_filename_key).
INSERT INTO managers (dataset_id, filename, digest)
SELECT sqlc.arg(dataset_id)::integer, unnest(sqlc.arg(filenames)::text[]), unnest(sqlc.arg(digests)::text[])
ON CONFLICT (dataset_id, filename) DO UPDATE SET digest = EXCLUDED.digest;

-- name: ImportDeleteManagers :execrows
DELETE FROM managers WHERE id = ANY (sqlc.arg(ids)::integer[]);

-- name: ImportUpsertTestcases :execrows
-- Testcases of a dataset by codename (testcases_dataset_id_codename_key),
-- however many, in one statement. A testcase that changes keeps its id,
-- and so its evaluations, as upstream update_dataset does.
INSERT INTO testcases (dataset_id, codename, public, input, output)
SELECT sqlc.arg(dataset_id)::integer, unnest(sqlc.arg(codenames)::text[]), unnest(sqlc.arg(publics)::boolean[]),
       unnest(sqlc.arg(inputs)::text[]), unnest(sqlc.arg(outputs)::text[])
ON CONFLICT (dataset_id, codename) DO UPDATE
SET public = EXCLUDED.public, input = EXCLUDED.input, output = EXCLUDED.output;

-- name: ImportDeleteTestcases :execrows
-- Cascades to their evaluations (ix_evaluations_testcase_id).
DELETE FROM testcases WHERE id = ANY (sqlc.arg(ids)::integer[]);

-- name: ImportUpsertGroups :many
-- Groups of a contest by name (groups_contest_id_name_key): new ones are
-- added, the others get the new times. per_user_time_us is in
-- microseconds (NULL: none). Returns the ids of the groups written.
WITH written AS (
    INSERT INTO groups (contest_id, name, start, stop, analysis_enabled, analysis_start, analysis_stop, per_user_time)
    SELECT sqlc.arg(contest_id)::integer, g.name, g.start, g.stop, g.analysis_enabled, g.analysis_start, g.analysis_stop,
           g.per_user_time_us * interval '1 microsecond'
    FROM jsonb_to_recordset(sqlc.arg(groups)::jsonb) AS g(name text, start timestamptz, stop timestamptz,
         analysis_enabled boolean, analysis_start timestamptz, analysis_stop timestamptz, per_user_time_us bigint)
    ON CONFLICT (contest_id, name) DO UPDATE
    SET start = EXCLUDED.start, stop = EXCLUDED.stop, analysis_enabled = EXCLUDED.analysis_enabled,
        analysis_start = EXCLUDED.analysis_start, analysis_stop = EXCLUDED.analysis_stop,
        per_user_time = EXCLUDED.per_user_time
    RETURNING id, name
)
SELECT id, name FROM written ORDER BY name;

-- name: ImportUsersByUsername :many
-- The users an import links to participations or updates, by username
-- (users_username_key).
SELECT * FROM users WHERE username = ANY (sqlc.arg(usernames)::text[]) ORDER BY username FOR UPDATE;

-- name: ImportCreateUsers :many
-- New users in one statement; returns their ids.
WITH created AS (
    INSERT INTO users (first_name, last_name, username, password, email, timezone, preferred_languages)
    SELECT u.first_name, u.last_name, u.username, u.password, u.email, u.timezone,
           coalesce(u.preferred_languages, '{}')
    FROM jsonb_to_recordset(sqlc.arg(users)::jsonb) AS u(first_name text, last_name text, username text,
         password text, email text, timezone text, preferred_languages text[])
    RETURNING id, username
)
SELECT id, username FROM created ORDER BY username;

-- name: ImportUpdateUsers :execrows
-- Users changed by an import (--update), in one statement.
UPDATE users u
SET first_name = x.first_name, last_name = x.last_name, password = x.password, email = x.email,
    timezone = x.timezone, preferred_languages = coalesce(x.preferred_languages, '{}')
FROM jsonb_to_recordset(sqlc.arg(users)::jsonb) AS x(id integer, first_name text, last_name text,
     password text, email text, timezone text, preferred_languages text[])
WHERE u.id = x.id;

-- name: ImportTeamsByCode :many
-- The teams an import links to participations or updates, by code
-- (teams_code_key).
SELECT * FROM teams WHERE code = ANY (sqlc.arg(codes)::text[]) ORDER BY code FOR UPDATE;

-- name: ImportCreateTeams :many
-- New teams in one statement; returns their ids.
WITH created AS (
    INSERT INTO teams (code, name)
    SELECT unnest(sqlc.arg(codes)::text[]), unnest(sqlc.arg(names)::text[])
    RETURNING id, code
)
SELECT id, code FROM created ORDER BY code;

-- name: ImportUpdateTeams :execrows
-- Teams renamed by an import (--update), in one statement.
UPDATE teams t SET name = x.name
FROM (SELECT unnest(sqlc.arg(ids)::integer[]) AS id, unnest(sqlc.arg(names)::text[]) AS name) x
WHERE t.id = x.id;

-- name: ImportCreateParticipations :execrows
-- New participations of a contest in one statement. delay_us and
-- extra_us are in microseconds.
INSERT INTO participations (contest_id, user_id, group_id, team_id, password, ip, hidden, unrestricted,
                            delay_time, extra_time)
SELECT sqlc.arg(contest_id)::integer, x.user_id, x.group_id, x.team_id, x.password, x.ip, x.hidden, x.unrestricted,
       x.delay_us * interval '1 microsecond', x.extra_us * interval '1 microsecond'
FROM jsonb_to_recordset(sqlc.arg(participations)::jsonb) AS x(user_id integer, group_id integer, team_id integer,
     password text, ip cidr[], hidden boolean, unrestricted boolean, delay_us bigint, extra_us bigint);

-- name: ImportUpdateParticipations :execrows
-- Participations changed by an import, in one statement.
UPDATE participations p
SET group_id = x.group_id, team_id = x.team_id, password = x.password, ip = x.ip, hidden = x.hidden,
    unrestricted = x.unrestricted, delay_time = x.delay_us * interval '1 microsecond',
    extra_time = x.extra_us * interval '1 microsecond'
FROM jsonb_to_recordset(sqlc.arg(participations)::jsonb) AS x(id integer, group_id integer, team_id integer,
     password text, ip cidr[], hidden boolean, unrestricted boolean, delay_us bigint, extra_us bigint)
WHERE p.id = x.id;

-- name: ImportDeleteParticipations :execrows
-- Stale participations (--delete-stale-participations); cascades to their
-- submissions, user tests, questions and messages.
DELETE FROM participations WHERE id = ANY (sqlc.arg(ids)::integer[]);
