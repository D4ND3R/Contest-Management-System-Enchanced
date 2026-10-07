-- name: GetUserTestExecutable :one
SELECT * FROM user_test_executables WHERE id = $1;

-- name: ListUserTestExecutables :many
-- user_test_executables_user_test_id_dataset_id_filename_key.
SELECT * FROM user_test_executables WHERE user_test_id = $1 AND dataset_id = $2 ORDER BY filename;

-- name: CreateUserTestExecutable :execrows
-- Idempotent, first result wins (0 rows if the file is already stored).
INSERT INTO user_test_executables (user_test_id, dataset_id, filename, digest)
VALUES ($1, $2, $3, $4)
ON CONFLICT (user_test_id, dataset_id, filename) DO NOTHING;

-- name: DeleteUserTestExecutables :execrows
DELETE FROM user_test_executables WHERE user_test_id = $1 AND dataset_id = $2;
