-- +goose Up
-- A device the controller is forgetting but has not finished with: its adapter
-- will reset it the next time it makes contact, and the controller drops the
-- device once the adapter reports it released. Until then it stays adopted,
-- so its keys stay and the reset can be delivered.
ALTER TABLE devices ADD COLUMN forgetting BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE devices DROP COLUMN forgetting;
