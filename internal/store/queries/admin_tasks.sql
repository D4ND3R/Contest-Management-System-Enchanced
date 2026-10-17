-- The administration's task and dataset pages (internal/adminweb tasks.go,
-- datasets.go): CMS cms/server/admin/handlers/task.py and dataset.py.
-- Every page reads a constant number of queries whatever the number of
-- datasets, managers or testcases.

-- name: AdminTaskList :many
-- The tasks page (CMS task_list: every task by name).
SELECT id, name, title FROM tasks ORDER BY name;

-- name: AdminTaskSubmissionCount :one
-- The submissions of a task (task page, remove page; ix_submissions_task_id).
SELECT count(*)::bigint FROM submissions WHERE task_id = $1;

-- name: AdminTaskStatements :many
-- The statements of a task in the order CMS loads them.
SELECT * FROM statements WHERE task_id = $1 ORDER BY id;

-- name: AdminTaskAttachments :many
SELECT * FROM attachments WHERE task_id = $1 ORDER BY id;

-- name: AdminTaskDatasets :many
-- The datasets of a task by description (the order of the task page:
-- Python sorts by code point, hence COLLATE "C").
SELECT * FROM datasets WHERE task_id = $1 ORDER BY description COLLATE "C", id;

-- name: AdminTaskManagers :many
-- The managers of every dataset of a task.
SELECT m.*
FROM managers m
JOIN datasets d ON d.id = m.dataset_id
WHERE d.task_id = $1
ORDER BY m.dataset_id, m.id;

-- name: AdminTaskTestcases :many
-- The testcases of every dataset of a task, by codename (CMS dictsort).
SELECT tc.*
FROM testcases tc
JOIN datasets d ON d.id = tc.dataset_id
WHERE d.task_id = $1
ORDER BY tc.dataset_id, tc.codename COLLATE "C";

-- name: AdminSetTaskTestcasesPublic :execrows
-- The "public" checkboxes of the task page for every dataset of the task
-- in one statement: the listed testcases become public, the others
-- private.
UPDATE testcases
SET public = (id = ANY (coalesce(sqlc.arg(public_ids)::integer[], '{}')))
WHERE dataset_id IN (SELECT d.id FROM datasets d WHERE d.task_id = sqlc.arg(task_id))
  AND public IS DISTINCT FROM (id = ANY (coalesce(sqlc.arg(public_ids)::integer[], '{}')));

-- name: AdminLockTask :one
-- Locks a task for an update of its settings or datasets.
SELECT * FROM tasks WHERE id = $1 FOR UPDATE;

-- name: AdminCompactTaskNums :exec
-- After a task left a contest at position num, the following ones move up
-- by one (CMS keeps the positions 0...n-1). Two steps through negative
-- positions (AdminFinishCompactTaskNums), since tasks_contest_id_num_key
-- is checked row by row.
UPDATE tasks SET num = -num - 1
WHERE contest_id = sqlc.arg(contest_id)::integer AND num > sqlc.arg(num)::integer;

-- name: AdminFinishCompactTaskNums :exec
UPDATE tasks SET num = -num - 2
WHERE contest_id = sqlc.arg(contest_id)::integer AND num < 0;

-- name: AdminDatasetWithTask :one
-- A dataset with its task (the dataset pages).
SELECT sqlc.embed(d), sqlc.embed(t)
FROM datasets d
JOIN tasks t ON t.id = d.task_id
WHERE d.id = $1;

-- name: AdminDatasetDescriptionTaken :one
-- Whether another dataset of the task has the description (CMS checks it
-- before datasets_task_id_description_key does).
SELECT EXISTS (
    SELECT 1 FROM datasets
    WHERE task_id = sqlc.arg(task_id) AND description = sqlc.arg(description)
      AND id <> sqlc.arg(except_id)::integer
)::boolean;

-- name: AdminToggleAutojudge :one
UPDATE datasets SET autojudge = NOT autojudge WHERE id = $1 RETURNING *;

-- name: AdminCloneResults :execrows
-- Cloning a dataset with its results (CMS clone_from with clone_results):
-- the submission results of one dataset copied to another.
INSERT INTO submission_results (
    submission_id, dataset_id, compilation_outcome, compilation_text, compilation_tries,
    compilation_stdout, compilation_stderr, compilation_time, compilation_wall_clock_time,
    compilation_memory, compilation_shard, compilation_sandbox_paths, compilation_sandbox_digests,
    evaluation_outcome, evaluation_tries, score, score_details, scored_at, public_score,
    public_score_details, ranking_score_details, generation)
SELECT submission_id, sqlc.arg(to_dataset_id)::integer, compilation_outcome, compilation_text, compilation_tries,
       compilation_stdout, compilation_stderr, compilation_time, compilation_wall_clock_time,
       compilation_memory, compilation_shard, compilation_sandbox_paths, compilation_sandbox_digests,
       evaluation_outcome, evaluation_tries, score, score_details, scored_at, public_score,
       public_score_details, ranking_score_details, generation
FROM submission_results sr
WHERE sr.dataset_id = sqlc.arg(from_dataset_id)::integer;

-- name: AdminCloneExecutables :execrows
INSERT INTO executables (submission_id, dataset_id, filename, digest)
SELECT ex.submission_id, sqlc.arg(to_dataset_id)::integer, ex.filename, ex.digest
FROM executables ex
WHERE ex.dataset_id = sqlc.arg(from_dataset_id)::integer;

-- name: AdminCloneEvaluations :execrows
-- The evaluations follow the testcases by codename (the clone's testcases
-- must exist already).
INSERT INTO evaluations (submission_id, dataset_id, testcase_id, outcome, text, admin_text,
                         execution_time, execution_wall_clock_time, execution_memory,
                         evaluation_shard, evaluation_sandbox_paths, evaluation_sandbox_digests)
SELECT e.submission_id, sqlc.arg(to_dataset_id)::integer, nt.id, e.outcome, e.text, e.admin_text,
       e.execution_time, e.execution_wall_clock_time, e.execution_memory,
       e.evaluation_shard, e.evaluation_sandbox_paths, e.evaluation_sandbox_digests
FROM evaluations e
JOIN testcases ot ON ot.id = e.testcase_id
JOIN testcases nt ON nt.dataset_id = sqlc.arg(to_dataset_id)::integer AND nt.codename = ot.codename
WHERE e.dataset_id = sqlc.arg(from_dataset_id)::integer;

-- name: AdminDatasetChanges :many
-- The preview of a dataset activation (CMS compute_changes_for_dataset):
-- the submissions of the task whose score, public score or ranking
-- details differ between the active dataset and the new one, with their
-- participant and whether they were tokened, by participation and
-- submission; at most max_rows rows.
SELECT s.id AS submission_id, s.participation_id, u.id AS user_id, u.username,
       (tk.id IS NOT NULL)::boolean AS tokened,
       o.score AS old_score, n.score AS new_score,
       o.public_score AS old_public_score, n.public_score AS new_public_score,
       o.ranking_score_details AS old_ranking_score_details,
       n.ranking_score_details AS new_ranking_score_details
FROM submissions s
JOIN participations p ON p.id = s.participation_id
JOIN users u ON u.id = p.user_id
LEFT JOIN tokens tk ON tk.submission_id = s.id
LEFT JOIN submission_results o ON o.submission_id = s.id AND o.dataset_id = sqlc.narg(old_dataset_id)::integer
LEFT JOIN submission_results n ON n.submission_id = s.id AND n.dataset_id = sqlc.arg(new_dataset_id)::integer
WHERE s.task_id = sqlc.arg(task_id)
  AND (o.score IS DISTINCT FROM n.score
       OR o.public_score IS DISTINCT FROM n.public_score
       OR o.ranking_score_details IS DISTINCT FROM n.ranking_score_details)
ORDER BY s.participation_id, s.id
LIMIT sqlc.arg(max_rows)::integer;

-- name: AdminSendMessages :many
-- The messages of a dataset activation, one per participation of the
-- contest given (others are ignored), in one statement.
INSERT INTO messages (participation_id, admin_id, timestamp, subject, text)
SELECT p.id, sqlc.narg(admin_id)::integer, sqlc.arg(timestamp), sqlc.arg(subject), sqlc.arg(text)
FROM participations p
WHERE p.id = ANY(sqlc.arg(participation_ids)::integer[])
  AND p.contest_id = sqlc.arg(contest_id)::integer
ORDER BY p.id
RETURNING id, participation_id;

-- name: AdminDeleteTestcasesByCodename :execrows
-- Overwriting testcases from a zip (CMS deletes and adds them again).
DELETE FROM testcases
WHERE dataset_id = sqlc.arg(dataset_id) AND codename = ANY(sqlc.arg(codenames)::text[]);

-- name: AdminDatasetTestcases :many
-- The testcases of a dataset (download, zip import).
SELECT * FROM testcases WHERE dataset_id = $1 ORDER BY codename COLLATE "C";

-- name: AdminDatasetManagers :many
SELECT * FROM managers WHERE dataset_id = $1 ORDER BY filename COLLATE "C";

-- name: AdminUpsertManager :one
-- A manager added or replaced by name (problem preparation).
INSERT INTO managers (dataset_id, filename, digest)
VALUES ($1, $2, $3)
ON CONFLICT (dataset_id, filename) DO UPDATE SET digest = EXCLUDED.digest
RETURNING *;

-- name: AdminUpsertStatement :one
INSERT INTO statements (task_id, language, digest)
VALUES ($1, $2, $3)
ON CONFLICT (task_id, language) DO UPDATE SET digest = EXCLUDED.digest
RETURNING *;

-- name: AdminUpsertAttachment :one
INSERT INTO attachments (task_id, filename, digest)
VALUES ($1, $2, $3)
ON CONFLICT (task_id, filename) DO UPDATE SET digest = EXCLUDED.digest
RETURNING *;

-- name: AdminUpsertTestcase :one
-- A testcase added or changed by codename (problem preparation): a new
-- one takes public; an existing one keeps its id (and its public flag),
-- with the input and output given.
INSERT INTO testcases (dataset_id, codename, public, input, output)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (dataset_id, codename) DO UPDATE SET input = EXCLUDED.input, output = EXCLUDED.output
RETURNING *;
