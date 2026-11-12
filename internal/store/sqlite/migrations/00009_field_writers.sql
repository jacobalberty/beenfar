-- +goose Up
-- Who last set each field of a configurable resource, so clients can tell
-- which fields another client (say, Terraform) keeps writing. This is a
-- record of writes, not of ownership: a row exists once a field has been
-- named by a create or by the update mask of an update.
CREATE TABLE field_writers (
    resource    TEXT    NOT NULL CHECK (resource IN ('wifi_network', 'network', 'radius_server', 'switch_port')),
    resource_id TEXT    NOT NULL,
    -- The API name of the field, e.g. 'ap_groups'.
    field       TEXT    NOT NULL,
    -- The user name or, for an API token, the token's name.
    writer      TEXT    NOT NULL,
    kind        TEXT    NOT NULL CHECK (kind IN ('user', 'api_token')),
    write_time  INTEGER NOT NULL,
    PRIMARY KEY (resource, resource_id, field)
);

-- The resource ids live in four tables, so there is no foreign key; these
-- triggers drop the rows with their resource.
-- +goose StatementBegin
CREATE TRIGGER wifi_networks_field_writers_delete AFTER DELETE ON wifi_networks
BEGIN
    DELETE FROM field_writers WHERE resource = 'wifi_network' AND resource_id = OLD.id;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER networks_field_writers_delete AFTER DELETE ON networks
BEGIN
    DELETE FROM field_writers WHERE resource = 'network' AND resource_id = OLD.id;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER radius_servers_field_writers_delete AFTER DELETE ON radius_servers
BEGIN
    DELETE FROM field_writers WHERE resource = 'radius_server' AND resource_id = OLD.id;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER switch_ports_field_writers_delete AFTER DELETE ON switch_ports
BEGIN
    DELETE FROM field_writers WHERE resource = 'switch_port' AND resource_id = OLD.id;
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER switch_ports_field_writers_delete;
DROP TRIGGER radius_servers_field_writers_delete;
DROP TRIGGER networks_field_writers_delete;
DROP TRIGGER wifi_networks_field_writers_delete;
DROP TABLE field_writers;
