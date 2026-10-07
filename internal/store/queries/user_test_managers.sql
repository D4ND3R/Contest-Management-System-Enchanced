-- name: GetUserTestManager :one
SELECT * FROM user_test_managers WHERE id = $1;

-- name: ListUserTestManagers :many
-- user_test_managers_user_test_id_filename_key serves the filter and the order.
SELECT * FROM user_test_managers WHERE user_test_id = $1 ORDER BY filename;

-- name: CreateUserTestManagers :execrows
-- Every manager of a new user test in one statement, as CreateFiles.
INSERT INTO user_test_managers (user_test_id, filename, digest)
SELECT sqlc.arg(user_test_id)::integer, unnest(sqlc.arg(filenames)::text[]), unnest(sqlc.arg(digests)::text[]);

-- name: DeleteUserTestManager :execrows
DELETE FROM user_test_managers WHERE id = $1;
