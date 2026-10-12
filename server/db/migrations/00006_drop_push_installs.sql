-- +goose Up
-- Push install (SSH / WinRM) was removed; agents install with a one-liner or a package.
DROP TABLE IF EXISTS push_installs;

-- +goose Down
CREATE TABLE push_installs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id   INTEGER NOT NULL DEFAULT 0,
    method      TEXT NOT NULL,  -- ssh, winrm
    target      TEXT NOT NULL,
    username    TEXT NOT NULL,
    status      TEXT NOT NULL,  -- running, succeeded, failed
    log         TEXT NOT NULL DEFAULT '',
    host_id     INTEGER,
    created_by  TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    finished_at INTEGER NOT NULL DEFAULT 0
);
