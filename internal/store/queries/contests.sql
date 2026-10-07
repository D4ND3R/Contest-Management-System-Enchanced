-- name: GetContest :one
SELECT * FROM contests WHERE id = $1;

-- name: GetContestByName :one
-- Contest URLs /<name>/… (contests_name_key).
SELECT * FROM contests WHERE name = $1;

-- name: ListContests :many
SELECT * FROM contests ORDER BY name;

-- name: CreateContest :one
-- main_group_id is set afterwards with SetContestMainGroup, once the
-- group exists.
INSERT INTO contests (
    name, description, allowed_localizations, languages,
    submissions_download_allowed, allow_questions, allow_user_tests,
    allow_unofficial_submission_before_analysis_mode, block_hidden_participations,
    allow_password_authentication, allow_registration, ip_restriction, ip_autologin,
    token_mode, token_max_number, token_min_interval, token_gen_initial,
    token_gen_number, token_gen_interval, token_gen_max, timezone,
    max_submission_number, max_user_test_number, min_submission_interval,
    min_submission_interval_grace_period, min_user_test_interval, score_precision
) VALUES (
    sqlc.arg(name), sqlc.arg(description),
    coalesce(sqlc.arg(allowed_localizations)::text[], '{}'), coalesce(sqlc.arg(languages)::text[], '{}'),
    sqlc.arg(submissions_download_allowed), sqlc.arg(allow_questions), sqlc.arg(allow_user_tests),
    sqlc.arg(allow_unofficial_submission_before_analysis_mode), sqlc.arg(block_hidden_participations),
    sqlc.arg(allow_password_authentication), sqlc.arg(allow_registration), sqlc.arg(ip_restriction), sqlc.arg(ip_autologin),
    sqlc.arg(token_mode), sqlc.narg(token_max_number), sqlc.arg(token_min_interval), sqlc.arg(token_gen_initial),
    sqlc.arg(token_gen_number), sqlc.arg(token_gen_interval), sqlc.narg(token_gen_max), sqlc.narg(timezone),
    sqlc.narg(max_submission_number), sqlc.narg(max_user_test_number), sqlc.narg(min_submission_interval),
    sqlc.narg(min_submission_interval_grace_period), sqlc.narg(min_user_test_interval), sqlc.arg(score_precision)
)
RETURNING *;

-- name: UpdateContest :one
UPDATE contests SET
    name = sqlc.arg(name),
    description = sqlc.arg(description),
    allowed_localizations = coalesce(sqlc.arg(allowed_localizations)::text[], '{}'),
    languages = coalesce(sqlc.arg(languages)::text[], '{}'),
    submissions_download_allowed = sqlc.arg(submissions_download_allowed),
    allow_questions = sqlc.arg(allow_questions),
    allow_user_tests = sqlc.arg(allow_user_tests),
    allow_unofficial_submission_before_analysis_mode = sqlc.arg(allow_unofficial_submission_before_analysis_mode),
    block_hidden_participations = sqlc.arg(block_hidden_participations),
    allow_password_authentication = sqlc.arg(allow_password_authentication),
    allow_registration = sqlc.arg(allow_registration),
    ip_restriction = sqlc.arg(ip_restriction),
    ip_autologin = sqlc.arg(ip_autologin),
    token_mode = sqlc.arg(token_mode),
    token_max_number = sqlc.narg(token_max_number),
    token_min_interval = sqlc.arg(token_min_interval),
    token_gen_initial = sqlc.arg(token_gen_initial),
    token_gen_number = sqlc.arg(token_gen_number),
    token_gen_interval = sqlc.arg(token_gen_interval),
    token_gen_max = sqlc.narg(token_gen_max),
    timezone = sqlc.narg(timezone),
    max_submission_number = sqlc.narg(max_submission_number),
    max_user_test_number = sqlc.narg(max_user_test_number),
    min_submission_interval = sqlc.narg(min_submission_interval),
    min_submission_interval_grace_period = sqlc.narg(min_submission_interval_grace_period),
    min_user_test_interval = sqlc.narg(min_user_test_interval),
    score_precision = sqlc.arg(score_precision),
    main_group_id = sqlc.narg(main_group_id)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: SetContestMainGroup :execrows
UPDATE contests SET main_group_id = $2 WHERE id = $1;

-- name: DeleteContest :execrows
-- Cascades to its groups, participations, announcements and tasks.
DELETE FROM contests WHERE id = $1;
