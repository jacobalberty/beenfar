-- +goose Up
-- A locked device is one an adapter has seen but cannot talk to because it
-- holds no credentials for it (e.g. a UniFi device still managed by another
-- controller). It clears when the adapter next reports the device unlocked.
ALTER TABLE devices ADD COLUMN locked BOOLEAN NOT NULL DEFAULT FALSE;

-- Secrets an adapter keeps per device, named by the adapter (the UniFi
-- adapter stores its AES keys as 'current', 'pending' and 'imported').
-- They are never returned by the public API.
CREATE TABLE device_secrets (
    device_id   TEXT    NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    name        TEXT    NOT NULL,
    secret      BLOB    NOT NULL,
    update_time INTEGER NOT NULL,
    PRIMARY KEY (device_id, name)
);

-- +goose Down
DROP TABLE device_secrets;
ALTER TABLE devices DROP COLUMN locked;
