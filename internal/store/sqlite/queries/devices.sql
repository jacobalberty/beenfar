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
INSERT INTO devices (id, mac, adapter, state, model, ip, firmware, last_seen, create_time, update_time)
VALUES (sqlc.arg(id), sqlc.arg(mac), sqlc.arg(adapter), 'pending', sqlc.arg(model), sqlc.arg(ip), sqlc.arg(firmware),
        sqlc.arg(now), sqlc.arg(now), sqlc.arg(now))
-- Blank details in a report keep the values already known.
ON CONFLICT (mac) DO UPDATE SET
    adapter     = excluded.adapter,
    model       = CASE WHEN excluded.model = '' THEN devices.model ELSE excluded.model END,
    ip          = CASE WHEN excluded.ip = '' THEN devices.ip ELSE excluded.ip END,
    firmware    = CASE WHEN excluded.firmware = '' THEN devices.firmware ELSE excluded.firmware END,
    last_seen   = excluded.last_seen,
    update_time = excluded.update_time
RETURNING *;

-- name: AdoptDevice :one
UPDATE devices SET state = 'adopted', update_time = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND state = 'pending'
RETURNING *;

-- name: DeleteDevice :execrows
DELETE FROM devices WHERE id = ?;
