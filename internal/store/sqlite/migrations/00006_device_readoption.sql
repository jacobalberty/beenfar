-- +goose Up
-- Readoption tracks an adopted device that came back without its key (it was
-- factory reset). It stays adopted, so the UI can tell it from a new device:
--   ''            nothing to do
--   'awaiting'    reset; nobody has accepted the re-adoption yet
--   'in_progress' accepted; the device is being given a new key
ALTER TABLE devices ADD COLUMN readoption TEXT NOT NULL DEFAULT ''
    CHECK (readoption IN ('', 'awaiting', 'in_progress'));

-- +goose Down
ALTER TABLE devices DROP COLUMN readoption;
