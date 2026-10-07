-- name: GetUserTestFile :one
SELECT * FROM user_test_files WHERE id = $1;

-- name: ListUserTestFiles :many
-- user_test_files_user_test_id_filename_key serves the filter and the order.
SELECT * FROM user_test_files WHERE user_test_id = $1 ORDER BY filename;

-- name: CreateUserTestFiles :execrows
-- Every file of a new user test in one statement, as CreateFiles.
INSERT INTO user_test_files (user_test_id, filename, digest)
SELECT sqlc.arg(user_test_id)::integer, unnest(sqlc.arg(filenames)::text[]), unnest(sqlc.arg(digests)::text[]);

-- name: DeleteUserTestFile :execrows
DELETE FROM user_test_files WHERE id = $1;
