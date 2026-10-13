-- +goose Up
-- Recurring update schedules (auto-patching): a scope, a time rule and
-- whether to install security updates only.
CREATE TABLE update_schedules (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id     INTEGER NOT NULL DEFAULT 0 REFERENCES tenants(id) ON DELETE CASCADE,
    name          TEXT NOT NULL DEFAULT '',
    scope         TEXT NOT NULL,     -- all, tenant, group, host
    scope_id      INTEGER NOT NULL DEFAULT 0,
    frequency     TEXT NOT NULL,     -- daily, weekly, monthly, maintenance
    weekday       INTEGER NOT NULL DEFAULT 0,
    month_day     INTEGER NOT NULL DEFAULT 1,
    time_of_day   TEXT NOT NULL DEFAULT '03:00',
    security_only INTEGER NOT NULL DEFAULT 1,
    enabled       INTEGER NOT NULL DEFAULT 1,
    last_run_at   INTEGER NOT NULL DEFAULT 0,
    last_result   TEXT NOT NULL DEFAULT '{}',
    created_at    INTEGER NOT NULL
);
CREATE INDEX update_schedules_tenant ON update_schedules(tenant_id);

-- +goose Down
DROP TABLE update_schedules;
