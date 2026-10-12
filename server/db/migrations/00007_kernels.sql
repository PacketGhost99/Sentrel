-- +goose Up
-- Optional automatic removal of old kernels (needs allow_upgrades), rate
-- limited to once a day per host.
ALTER TABLE hosts ADD COLUMN auto_remove_old_kernels INTEGER NOT NULL DEFAULT 0;
ALTER TABLE hosts ADD COLUMN last_auto_kernel_at INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE hosts DROP COLUMN last_auto_kernel_at;
ALTER TABLE hosts DROP COLUMN auto_remove_old_kernels;
