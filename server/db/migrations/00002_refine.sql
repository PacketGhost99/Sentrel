-- +goose Up
-- Per-rule notification muting. A muted rule still raises alerts; they are
-- shown in the UI but never sent to notification channels.

ALTER TABLE threshold_rules ADD COLUMN notify INTEGER NOT NULL DEFAULT 1;
ALTER TABLE alerts ADD COLUMN muted INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE alerts DROP COLUMN muted;
ALTER TABLE threshold_rules DROP COLUMN notify;
