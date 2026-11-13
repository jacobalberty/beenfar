-- +goose Up
-- Settings an operator can change on a device, whose names and meaning belong
-- to the device's adapter (for a UniFi access point: radio channel, width,
-- power). original is the baseline the adapter built when the device was
-- adopted (its profile's defaults); current is what the device should run,
-- the baseline with the operator's changes. Resetting copies original to
-- current. Both are JSON objects of string values.
CREATE TABLE device_settings (
    device_id   TEXT    NOT NULL PRIMARY KEY REFERENCES devices (id) ON DELETE CASCADE,
    original_json TEXT    NOT NULL CHECK (json_valid(original_json) AND json_type(original_json) = 'object'),
    current_json  TEXT    NOT NULL CHECK (json_valid(current_json) AND json_type(current_json) = 'object'),
    create_time INTEGER NOT NULL,
    update_time INTEGER NOT NULL
);

-- A settings document each adapter keeps for itself, e.g. the UniFi
-- adapter's site settings (ntp, ssh, controller ids and the secrets it
-- generated at first start). It can hold secrets: the API never returns it
-- and it must never be logged.
CREATE TABLE adapter_settings (
    adapter     TEXT    NOT NULL PRIMARY KEY,
    settings    TEXT    NOT NULL CHECK (json_valid(settings) AND json_type(settings) = 'object'),
    create_time INTEGER NOT NULL,
    update_time INTEGER NOT NULL
);

-- +goose Down
DROP TABLE adapter_settings;
DROP TABLE device_settings;
