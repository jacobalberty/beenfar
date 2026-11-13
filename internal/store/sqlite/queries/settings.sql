-- name: GetDeviceSettings :one
SELECT * FROM device_settings WHERE device_id = ?;

-- name: SetDeviceSettings :exec
INSERT INTO device_settings (device_id, original_json, current_json, create_time, update_time)
VALUES (sqlc.arg(device_id), sqlc.arg(original_json), sqlc.arg(current_json), sqlc.arg(now), sqlc.arg(now))
ON CONFLICT (device_id) DO UPDATE SET
    original_json = excluded.original_json,
    current_json  = excluded.current_json,
    update_time = excluded.update_time;

-- name: GetAdapterSettings :one
SELECT settings FROM adapter_settings WHERE adapter = ?;

-- name: SetAdapterSettings :exec
INSERT INTO adapter_settings (adapter, settings, create_time, update_time)
VALUES (sqlc.arg(adapter), sqlc.arg(settings), sqlc.arg(now), sqlc.arg(now))
ON CONFLICT (adapter) DO UPDATE SET settings = excluded.settings, update_time = excluded.update_time;

-- name: InitAdapterSettings :execrows
-- Stores the settings only if the adapter has none yet, and reports whether it did.
INSERT INTO adapter_settings (adapter, settings, create_time, update_time)
VALUES (sqlc.arg(adapter), sqlc.arg(settings), sqlc.arg(now), sqlc.arg(now))
ON CONFLICT (adapter) DO NOTHING;
