-- name: CWSTaskFiles :many
-- The task description page (CMS task_description.html): the statements
-- (name = language) and the attachments (name = file name) of a task in
-- one query (statements_task_id_language_key,
-- attachments_task_id_filename_key). The page sorts them as Jinja does.
SELECT f.kind, f.name, f.digest
FROM (
    SELECT 'statement'::text AS kind, st.language AS name, st.digest
    FROM statements st
    WHERE st.task_id = sqlc.arg(task_id)
    UNION ALL
    SELECT 'attachment'::text, a.filename, a.digest
    FROM attachments a
    WHERE a.task_id = sqlc.arg(task_id)
) f
ORDER BY f.kind, f.name COLLATE "C";

-- name: CWSDatasetScoring :one
-- What builds the score type of a dataset (CMS
-- dataset.score_type_object): its score type and parameters and its
-- testcases (codename, public) in codename order, in one query
-- (testcases_dataset_id_codename_key).
SELECT d.score_type, d.score_type_parameters,
       coalesce(array_agg(tc.codename ORDER BY tc.codename COLLATE "C") FILTER (WHERE tc.id IS NOT NULL), '{}')::text[] AS codenames,
       coalesce(array_agg(tc.public ORDER BY tc.codename COLLATE "C") FILTER (WHERE tc.id IS NOT NULL), '{}')::boolean[] AS publics
FROM datasets d
LEFT JOIN testcases tc ON tc.dataset_id = d.id
WHERE d.id = $1
GROUP BY d.id;

-- name: CWSTaskSubmissions :many
-- The submissions page of a task (CMS task_submissions.html): the latest
-- submissions of the participant on the task, newest first, at most
-- page_size of them, with their result on the dataset (the active one;
-- NULL columns while there is no result), whether a token was played and
-- the names of their files. The scores, not the details, are read
-- (ix_submissions_participation_id_task_id backwards, then the primary
-- keys of submission_results and tokens, files_submission_id_filename_key).
SELECT s.id, s.opaque_id, s.timestamp, s.language, s.official,
       sr.compilation_outcome, sr.evaluation_outcome, sr.score, sr.public_score,
       (sr.score IS NOT NULL AND sr.score_details IS NOT NULL AND sr.public_score IS NOT NULL
        AND sr.public_score_details IS NOT NULL AND sr.ranking_score_details IS NOT NULL)::boolean AS scored,
       (tk.id IS NOT NULL)::boolean AS tokened,
       coalesce((SELECT array_agg(f.filename ORDER BY f.filename COLLATE "C") FROM files f WHERE f.submission_id = s.id),
                '{}')::text[] AS files
FROM submissions s
LEFT JOIN submission_results sr ON sr.submission_id = s.id AND sr.dataset_id = sqlc.narg(dataset_id)::integer
LEFT JOIN tokens tk ON tk.submission_id = s.id
WHERE s.participation_id = sqlc.arg(participation_id) AND s.task_id = sqlc.arg(task_id)
ORDER BY s.timestamp DESC, s.id DESC
LIMIT sqlc.arg(page_size);

-- name: CWSSubmission :one
-- A submission of the participant on a task by its opaque id
-- (participation_opaque_unique), with its result on the dataset (NULL
-- columns while there is none) and whether a token was played: the
-- status JSON, the details and the token of CMS (get_submission).
SELECT sqlc.embed(s), (tk.id IS NOT NULL)::boolean AS tokened,
       (sr.submission_id IS NOT NULL)::boolean AS has_result,
       sr.compilation_outcome, sr.compilation_text, sr.compilation_stdout, sr.compilation_stderr,
       sr.compilation_time, sr.compilation_memory, sr.evaluation_outcome,
       sr.score, sr.score_details, sr.public_score, sr.public_score_details,
       (sr.ranking_score_details IS NOT NULL)::boolean AS has_ranking_details
FROM submissions s
LEFT JOIN submission_results sr ON sr.submission_id = s.id AND sr.dataset_id = sqlc.narg(dataset_id)::integer
LEFT JOIN tokens tk ON tk.submission_id = s.id
WHERE s.participation_id = sqlc.arg(participation_id) AND s.task_id = sqlc.arg(task_id)
  AND s.opaque_id = sqlc.arg(opaque_id);

-- name: CWSSubmissionFiles :many
-- The files of a submission of the participant by its opaque id, for the
-- downloads: one row per file (a submission without files has one row
-- with NULL file columns; no row: no such submission).
SELECT s.language, f.filename, f.digest
FROM submissions s
LEFT JOIN files f ON f.submission_id = s.id
WHERE s.participation_id = sqlc.arg(participation_id) AND s.task_id = sqlc.arg(task_id)
  AND s.opaque_id = sqlc.arg(opaque_id)
ORDER BY f.filename COLLATE "C";

-- name: CWSLatestSubmissionFiles :many
-- The files of the latest submission of the participant on a task (CMS
-- fetch_file_digests_from_previous_submission), for the task types that
-- reuse them: one row per file, or one with NULL file columns.
SELECT s.id, s.language, f.filename, f.digest
FROM (
    SELECT s.id, s.language FROM submissions s
    WHERE s.participation_id = sqlc.arg(participation_id) AND s.task_id = sqlc.arg(task_id)
    ORDER BY s.timestamp DESC, s.id DESC
    LIMIT 1
) s
LEFT JOIN files f ON f.submission_id = s.id
ORDER BY f.filename COLLATE "C";

-- name: CWSLockParticipation :one
-- Serializes the submissions, user tests and tokens of a participant: the
-- limits (number, interval, tokens available) are checked again under
-- this lock in the transaction that inserts.
SELECT id FROM participations WHERE id = $1 FOR UPDATE;

-- name: CWSParticipationTokens :many
-- The tokens played by a participant with the task of each (CMS
-- tokens_available), oldest first. The tokens are looked up from the
-- participant's submissions (tokens_submission_id_key): joined the
-- usual way the planner hashes every token of the installation.
SELECT tk.timestamp, s.task_id
FROM tokens tk
JOIN submissions s ON s.id = tk.submission_id
WHERE tk.submission_id = ANY (ARRAY(SELECT x.id FROM submissions x WHERE x.participation_id = @participation_id::integer))
  AND s.participation_id = @participation_id::integer
ORDER BY tk.timestamp, tk.id;

-- name: CWSUserTests :many
-- The testing page (CMS test_interface.html): the latest user tests of
-- the participant on every task of the contest, at most page_size per
-- task, with their number (num: 1 for the first one of the task, the
-- number in the URLs), how many the task has (total), their result on
-- the active dataset of the task (NULL columns while there is none) and
-- the names of their files and managers
-- (ix_user_tests_participation_id_task_id).
SELECT ut.id, ut.task_id, ut.timestamp, ut.language, x.num, x.total,
       (utr.user_test_id IS NOT NULL)::boolean AS has_result,
       utr.compilation_outcome, utr.evaluation_outcome, (utr.output IS NOT NULL)::boolean AS has_output,
       utr.execution_time, utr.execution_memory,
       coalesce((SELECT array_agg(f.filename ORDER BY f.filename COLLATE "C") FROM user_test_files f WHERE f.user_test_id = ut.id),
                '{}')::text[] AS files,
       coalesce((SELECT array_agg(m.filename ORDER BY m.filename COLLATE "C") FROM user_test_managers m WHERE m.user_test_id = ut.id),
                '{}')::text[] AS managers
FROM (
    SELECT ut.id,
           row_number() OVER (PARTITION BY ut.task_id ORDER BY ut.timestamp, ut.id) AS num,
           count(*) OVER (PARTITION BY ut.task_id) AS total
    FROM user_tests ut
    JOIN tasks t ON t.id = ut.task_id
    WHERE ut.participation_id = sqlc.arg(participation_id) AND t.contest_id = sqlc.arg(contest_id)::integer
) x
JOIN user_tests ut ON ut.id = x.id
JOIN tasks t ON t.id = ut.task_id
LEFT JOIN user_test_results utr ON utr.user_test_id = ut.id AND utr.dataset_id = t.active_dataset_id
WHERE x.num > x.total - sqlc.arg(page_size)::bigint
ORDER BY ut.task_id, ut.timestamp DESC, ut.id DESC;

-- name: CWSUserTest :one
-- The num-th user test (from 1, oldest first) of the participant on a
-- task (CMS get_user_test), with its result on the dataset (NULL columns
-- while there is none) and its files and managers (name and digest).
SELECT sqlc.embed(ut), (utr.user_test_id IS NOT NULL)::boolean AS has_result,
       utr.output, utr.compilation_outcome, utr.compilation_text, utr.compilation_stdout,
       utr.compilation_stderr, utr.compilation_time, utr.compilation_memory,
       utr.evaluation_outcome, utr.evaluation_text, utr.execution_time, utr.execution_memory,
       coalesce((SELECT array_agg(f.filename ORDER BY f.filename COLLATE "C") FROM user_test_files f WHERE f.user_test_id = ut.id),
                '{}')::text[] AS file_names,
       coalesce((SELECT array_agg(f.digest ORDER BY f.filename COLLATE "C") FROM user_test_files f WHERE f.user_test_id = ut.id),
                '{}')::text[] AS file_digests,
       coalesce((SELECT array_agg(m.filename ORDER BY m.filename COLLATE "C") FROM user_test_managers m WHERE m.user_test_id = ut.id),
                '{}')::text[] AS manager_names,
       coalesce((SELECT array_agg(m.digest ORDER BY m.filename COLLATE "C") FROM user_test_managers m WHERE m.user_test_id = ut.id),
                '{}')::text[] AS manager_digests
FROM user_tests ut
LEFT JOIN user_test_results utr ON utr.user_test_id = ut.id AND utr.dataset_id = sqlc.narg(dataset_id)::integer
WHERE ut.participation_id = sqlc.arg(participation_id) AND ut.task_id = sqlc.arg(task_id)
ORDER BY ut.timestamp, ut.id
OFFSET sqlc.arg(num)::bigint - 1
LIMIT 1;

-- name: CWSLatestUserTest :one
-- The latest user test of the participant on a task with its files and
-- managers (CMS fetch_file_digests_from_previous_submission with
-- cls=UserTest).
SELECT ut.id, ut.language, ut.input,
       coalesce((SELECT array_agg(f.filename ORDER BY f.filename COLLATE "C") FROM user_test_files f WHERE f.user_test_id = ut.id),
                '{}')::text[] AS file_names,
       coalesce((SELECT array_agg(f.digest ORDER BY f.filename COLLATE "C") FROM user_test_files f WHERE f.user_test_id = ut.id),
                '{}')::text[] AS file_digests,
       coalesce((SELECT array_agg(m.filename ORDER BY m.filename COLLATE "C") FROM user_test_managers m WHERE m.user_test_id = ut.id),
                '{}')::text[] AS manager_names,
       coalesce((SELECT array_agg(m.digest ORDER BY m.filename COLLATE "C") FROM user_test_managers m WHERE m.user_test_id = ut.id),
                '{}')::text[] AS manager_digests
FROM user_tests ut
WHERE ut.participation_id = sqlc.arg(participation_id) AND ut.task_id = sqlc.arg(task_id)
ORDER BY ut.timestamp DESC, ut.id DESC
LIMIT 1;
