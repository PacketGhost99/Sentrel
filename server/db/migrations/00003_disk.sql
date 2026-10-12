-- +goose Up
-- Disk usage analysis and remote disk cleanup.

ALTER TABLE hosts ADD COLUMN allow_cleanup INTEGER NOT NULL DEFAULT 0;

CREATE TABLE disk_reports (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    host_id     INTEGER NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    mount       TEXT NOT NULL,
    job_id      TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'running',   -- running, done, failed
    error       TEXT NOT NULL DEFAULT '',
    data        TEXT NOT NULL DEFAULT '{}',         -- agent result JSON
    created_at  INTEGER NOT NULL
);
CREATE INDEX idx_disk_reports_host ON disk_reports(host_id, mount, created_at);
CREATE INDEX idx_disk_reports_job ON disk_reports(job_id);

-- +goose Down
DROP TABLE disk_reports;
ALTER TABLE hosts DROP COLUMN allow_cleanup;
