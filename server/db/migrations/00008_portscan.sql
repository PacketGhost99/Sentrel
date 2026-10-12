-- +goose Up
-- Port-scan detection: scanners are stored in security_events next to failed
-- logins (kind distinguishes them); the agent also reports whether the host
-- supports kernel-side detection.
ALTER TABLE security_events ADD COLUMN kind TEXT NOT NULL DEFAULT 'login';

CREATE TABLE portscan_status (
    host_id    INTEGER PRIMARY KEY REFERENCES hosts(id) ON DELETE CASCADE,
    tenant_id  INTEGER NOT NULL DEFAULT 0,
    supported  INTEGER NOT NULL DEFAULT 0,
    active     INTEGER NOT NULL DEFAULT 0,
    reason     TEXT NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE portscan_status;
ALTER TABLE security_events DROP COLUMN kind;
