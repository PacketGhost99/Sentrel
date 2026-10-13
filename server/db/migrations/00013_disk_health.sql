-- +goose Up
-- Latest SMART / RAID / ZFS snapshot per host (Linux agents).
CREATE TABLE disk_health (
    tenant_id   INTEGER NOT NULL DEFAULT 0,
    host_id     INTEGER PRIMARY KEY REFERENCES hosts(id) ON DELETE CASCADE,
    data        TEXT NOT NULL DEFAULT '{}',
    updated_at  INTEGER NOT NULL
);

-- +goose Down
DROP TABLE disk_health;
