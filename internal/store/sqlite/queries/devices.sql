-- name: GetDevice :one
SELECT * FROM devices WHERE id = ?;

-- name: GetDeviceByMAC :one
SELECT * FROM devices WHERE mac = ?;

-- name: ListDevices :many
SELECT * FROM devices
WHERE (CAST(sqlc.arg(state) AS TEXT) = '' OR state = sqlc.arg(state))
  AND (CAST(sqlc.arg(adapter) AS TEXT) = '' OR adapter = sqlc.arg(adapter))
ORDER BY mac;

-- name: ReportDevice :one
INSERT INTO devices (id, mac, adapter, state, model, ip, firmware, locked, last_seen, create_time, update_time)
VALUES (sqlc.arg(id), sqlc.arg(mac), sqlc.arg(adapter), 'pending', sqlc.arg(model), sqlc.arg(ip), sqlc.arg(firmware),
        sqlc.arg(locked), sqlc.arg(now), sqlc.arg(now), sqlc.arg(now))
-- Blank details in a report keep the values already known.
ON CONFLICT (mac) DO UPDATE SET
    adapter     = excluded.adapter,
    model       = CASE WHEN excluded.model = '' THEN devices.model ELSE excluded.model END,
    ip          = CASE WHEN excluded.ip = '' THEN devices.ip ELSE excluded.ip END,
    firmware    = CASE WHEN excluded.firmware = '' THEN devices.firmware ELSE excluded.firmware END,
    locked      = excluded.locked,
    -- A locked report is not authenticated (no key opened the packet), so
    -- it must not count as hearing from the device.
    last_seen   = CASE WHEN excluded.locked THEN devices.last_seen ELSE excluded.last_seen END,
    update_time = excluded.update_time
RETURNING *;

-- name: RecordDeviceState :one
-- What an adapter learned from the device itself. Blank details keep the
-- values already known; the device is evidently reachable, so it is unlocked.
UPDATE devices SET
    model           = CASE WHEN sqlc.arg(model) = '' THEN model ELSE sqlc.arg(model) END,
    ip              = CASE WHEN sqlc.arg(ip) = '' THEN ip ELSE sqlc.arg(ip) END,
    firmware        = CASE WHEN sqlc.arg(firmware) = '' THEN firmware ELSE sqlc.arg(firmware) END,
    config_revision = sqlc.arg(config_revision),
    locked          = FALSE,
    readoption      = sqlc.arg(readoption),
    last_seen       = sqlc.arg(now),
    update_time     = sqlc.arg(now)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: AdoptDevice :one
-- Adopts a pending device, or accepts the re-adoption of an adopted device that
-- is awaiting it (which then moves on to being re-keyed).
UPDATE devices SET
    state      = 'adopted',
    readoption = CASE WHEN readoption = 'awaiting' THEN 'in_progress' ELSE readoption END,
    update_time = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND (state = 'pending' OR readoption = 'awaiting')
RETURNING *;

-- name: MarkDeviceForgetting :one
-- Marks an adopted device as being forgotten (its adapter will reset it on its
-- next contact). Only adopted devices have anything to reset.
UPDATE devices SET
    forgetting  = TRUE,
    update_time = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND state = 'adopted'
RETURNING *;

-- name: DeleteDevice :execrows
DELETE FROM devices WHERE id = ?;

-- name: GetDeviceSecret :one
SELECT secret FROM device_secrets WHERE device_id = ? AND name = ?;

-- name: SetDeviceSecret :exec
INSERT INTO device_secrets (device_id, name, secret, update_time)
VALUES (sqlc.arg(device_id), sqlc.arg(name), sqlc.arg(secret), sqlc.arg(now))
ON CONFLICT (device_id, name) DO UPDATE SET secret = excluded.secret, update_time = excluded.update_time;

-- name: DeleteDeviceSecret :exec
DELETE FROM device_secrets WHERE device_id = ? AND name = ?;
