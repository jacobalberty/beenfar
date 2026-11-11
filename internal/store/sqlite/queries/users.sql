-- name: CreateUser :one
INSERT INTO users (id, username, password_hash, admin, revision, create_time, update_time)
VALUES (sqlc.arg(id), sqlc.arg(username), sqlc.arg(password_hash), sqlc.arg(admin), 1, sqlc.arg(now), sqlc.arg(now))
RETURNING *;

-- name: GetUser :one
SELECT * FROM users WHERE id = ?;

-- name: GetUserByUsername :one
SELECT * FROM users WHERE username = ?;

-- name: ListUsers :many
SELECT * FROM users ORDER BY username;

-- name: CountUsers :one
SELECT COUNT(*) FROM users;

-- name: CountAdmins :one
SELECT COUNT(*) FROM users WHERE admin;

-- name: UpdateUser :one
UPDATE users SET
    username    = sqlc.arg(username),
    admin       = sqlc.arg(admin),
    revision    = revision + 1,
    update_time = sqlc.arg(now)
WHERE id = sqlc.arg(id)
  AND (CAST(sqlc.arg(expected_revision) AS INTEGER) = 0 OR revision = sqlc.arg(expected_revision))
RETURNING *;

-- name: SetUserPassword :execrows
UPDATE users SET password_hash = sqlc.arg(password_hash), update_time = sqlc.arg(now)
WHERE id = sqlc.arg(id);

-- name: DeleteUser :execrows
DELETE FROM users
WHERE id = sqlc.arg(id)
  AND (CAST(sqlc.arg(expected_revision) AS INTEGER) = 0 OR revision = sqlc.arg(expected_revision));
