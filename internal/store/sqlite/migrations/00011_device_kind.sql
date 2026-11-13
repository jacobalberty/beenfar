-- +goose Up
-- What kind of device an adapter says it is (''  unknown, 'access_point',
-- 'switch', 'gateway'), how many ports beyond its uplink can be configured
-- (most access points have none), and the AP group an operator has pinned
-- an access point to. An empty ap_group is the default: the access point
-- broadcasts every wifi network that is not limited to named groups.
ALTER TABLE devices ADD COLUMN kind TEXT NOT NULL DEFAULT ''
    CHECK (kind IN ('', 'access_point', 'switch', 'gateway'));
ALTER TABLE devices ADD COLUMN port_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE devices ADD COLUMN ap_group TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE devices DROP COLUMN ap_group;
ALTER TABLE devices DROP COLUMN port_count;
ALTER TABLE devices DROP COLUMN kind;
