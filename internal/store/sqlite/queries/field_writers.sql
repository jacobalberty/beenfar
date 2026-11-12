-- name: UpsertFieldWriter :exec
INSERT INTO field_writers (resource, resource_id, field, writer, kind, write_time)
VALUES (sqlc.arg(resource), sqlc.arg(resource_id), sqlc.arg(field), sqlc.arg(writer), sqlc.arg(kind), sqlc.arg(write_time))
ON CONFLICT (resource, resource_id, field) DO UPDATE SET
    writer     = excluded.writer,
    kind       = excluded.kind,
    write_time = excluded.write_time;

-- name: ListFieldWritersOf :many
SELECT * FROM field_writers WHERE resource = ? AND resource_id = ? ORDER BY field;

-- name: ListFieldWriters :many
SELECT * FROM field_writers WHERE resource = ? ORDER BY resource_id, field;
