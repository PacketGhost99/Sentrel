-- +goose Up
-- Heartbeat initial schema.
-- Every tenant-scoped row carries tenant_id. Tenant 0 is the provider/admin
-- tenant; its users can see every tenant.

CREATE TABLE tenants (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    name          TEXT NOT NULL,
    slug          TEXT NOT NULL UNIQUE,
    hostname      TEXT NOT NULL DEFAULT '',  -- optional custom portal hostname
    disabled      INTEGER NOT NULL DEFAULT 0,
    notes         TEXT NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL
);
INSERT INTO tenants (id, name, slug, created_at) VALUES (0, 'Provider', 'provider', strftime('%s','now'));

-- Key/value settings. tenant_id 0 holds global settings; other tenants may
-- override a subset (branding, status page defaults).
CREATE TABLE settings (
    tenant_id   INTEGER NOT NULL DEFAULT 0 REFERENCES tenants(id) ON DELETE CASCADE,
    key         TEXT NOT NULL,
    value       TEXT NOT NULL,
    updated_at  INTEGER NOT NULL,
    PRIMARY KEY (tenant_id, key)
);

CREATE TABLE users (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id       INTEGER NOT NULL DEFAULT 0 REFERENCES tenants(id) ON DELETE CASCADE,
    username        TEXT NOT NULL COLLATE NOCASE UNIQUE,
    email           TEXT NOT NULL DEFAULT '',
    display_name    TEXT NOT NULL DEFAULT '',
    password_hash   TEXT NOT NULL DEFAULT '',
    role            TEXT NOT NULL,          -- admin, operator, viewer, tenant_admin, tenant_viewer
    auth_source     TEXT NOT NULL DEFAULT 'local', -- local, ldap, radius
    totp_secret     TEXT NOT NULL DEFAULT '',
    totp_enabled    INTEGER NOT NULL DEFAULT 0,
    recovery_codes  TEXT NOT NULL DEFAULT '[]', -- JSON array of hashes
    webauthn_id     BLOB,
    disabled        INTEGER NOT NULL DEFAULT 0,
    must_change_pw  INTEGER NOT NULL DEFAULT 0,
    theme           TEXT NOT NULL DEFAULT 'system',
    created_at      INTEGER NOT NULL,
    last_login_at   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX users_tenant ON users(tenant_id);

CREATE TABLE webauthn_credentials (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id       INTEGER NOT NULL DEFAULT 0,
    user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    credential_id   BLOB NOT NULL UNIQUE,
    credential_json TEXT NOT NULL,
    created_at      INTEGER NOT NULL,
    last_used_at    INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE sessions (
    id_hash       TEXT PRIMARY KEY,
    tenant_id     INTEGER NOT NULL DEFAULT 0,
    user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    mfa_pending   INTEGER NOT NULL DEFAULT 0,
    csrf_token    TEXT NOT NULL,
    ip            TEXT NOT NULL DEFAULT '',
    user_agent    TEXT NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL,
    expires_at    INTEGER NOT NULL,
    last_seen_at  INTEGER NOT NULL
);
CREATE INDEX sessions_user ON sessions(user_id);

CREATE TABLE api_keys (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id     INTEGER NOT NULL DEFAULT 0 REFERENCES tenants(id) ON DELETE CASCADE,
    user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    prefix        TEXT NOT NULL UNIQUE,
    key_hash      TEXT NOT NULL,
    scopes        TEXT NOT NULL DEFAULT '["read"]',
    expires_at    INTEGER NOT NULL DEFAULT 0,
    last_used_at  INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL
);

CREATE TABLE host_groups (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id     INTEGER NOT NULL DEFAULT 0 REFERENCES tenants(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    description   TEXT NOT NULL DEFAULT '',
    color         TEXT NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL,
    UNIQUE (tenant_id, name)
);

-- A host is anything monitored: an agent host, an SNMP device, or a plain
-- target that only has server-side checks.
CREATE TABLE hosts (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id           INTEGER NOT NULL DEFAULT 0 REFERENCES tenants(id) ON DELETE CASCADE,
    kind                TEXT NOT NULL,      -- agent, snmp, target
    name                TEXT NOT NULL,
    address             TEXT NOT NULL DEFAULT '',
    group_id            INTEGER REFERENCES host_groups(id) ON DELETE SET NULL,
    tags                TEXT NOT NULL DEFAULT '[]',
    notes               TEXT NOT NULL DEFAULT '',
    agent_id            TEXT UNIQUE,
    cert_serial         TEXT NOT NULL DEFAULT '',
    cert_expires_at     INTEGER NOT NULL DEFAULT 0,
    revoked             INTEGER NOT NULL DEFAULT 0,
    agent_version       TEXT NOT NULL DEFAULT '',
    host_info           TEXT NOT NULL DEFAULT '{}',  -- JSON HostInfo
    state               TEXT NOT NULL DEFAULT '{}',  -- JSON HostState (latest)
    status              TEXT NOT NULL DEFAULT 'pending', -- pending, up, down, unknown
    last_seen_at        INTEGER NOT NULL DEFAULT 0,
    connected           INTEGER NOT NULL DEFAULT 0,
    reboot_required     INTEGER NOT NULL DEFAULT 0,
    -- agent settings (NULL = inherit global default)
    interval_seconds    INTEGER,
    inventory_interval  INTEGER,
    retention_days      INTEGER,
    agent_options       TEXT NOT NULL DEFAULT '{}',  -- JSON: top_processes, docker, bruteforce...
    -- opt-in remote actions (tenants opt in per host)
    allow_upgrades      INTEGER NOT NULL DEFAULT 0,
    allow_ip_block      INTEGER NOT NULL DEFAULT 0,
    allow_scripts       INTEGER NOT NULL DEFAULT 0,
    auto_block          INTEGER NOT NULL DEFAULT 0,
    -- SNMP
    snmp_config         TEXT NOT NULL DEFAULT '{}',  -- JSON: version, community, v3 auth, port, profile
    snmp_info           TEXT NOT NULL DEFAULT '{}',  -- sysDescr, sysName, interfaces...
    storage_bytes       INTEGER NOT NULL DEFAULT 0,
    storage_computed_at INTEGER NOT NULL DEFAULT 0,
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL
);
CREATE INDEX hosts_tenant ON hosts(tenant_id);
CREATE INDEX hosts_group ON hosts(group_id);

CREATE TABLE enrollment_tokens (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id     INTEGER NOT NULL DEFAULT 0 REFERENCES tenants(id) ON DELETE CASCADE,
    token_hash    TEXT NOT NULL UNIQUE,
    group_id      INTEGER REFERENCES host_groups(id) ON DELETE SET NULL,
    tags          TEXT NOT NULL DEFAULT '[]',
    host_name     TEXT NOT NULL DEFAULT '',
    created_by    INTEGER,
    expires_at    INTEGER NOT NULL,
    used_at       INTEGER NOT NULL DEFAULT 0,
    used_by_host  INTEGER,
    created_at    INTEGER NOT NULL
);

-- Inventory: installed and upgradable packages per host.
CREATE TABLE packages (
    tenant_id     INTEGER NOT NULL DEFAULT 0,
    host_id       INTEGER NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    version       TEXT NOT NULL,
    arch          TEXT NOT NULL DEFAULT '',
    type          TEXT NOT NULL DEFAULT '',
    source        TEXT NOT NULL DEFAULT '',
    new_version   TEXT NOT NULL DEFAULT '',
    security      INTEGER NOT NULL DEFAULT 0,
    kb            TEXT NOT NULL DEFAULT '',
    publisher     TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (host_id, name, arch, kb)
);
CREATE INDEX packages_upgradable ON packages(host_id) WHERE new_version <> '';

CREATE TABLE inventory_status (
    tenant_id       INTEGER NOT NULL DEFAULT 0,
    host_id         INTEGER PRIMARY KEY REFERENCES hosts(id) ON DELETE CASCADE,
    package_manager TEXT NOT NULL DEFAULT '',
    installed_count INTEGER NOT NULL DEFAULT 0,
    upgradable_count INTEGER NOT NULL DEFAULT 0,
    security_count  INTEGER NOT NULL DEFAULT 0,
    error           TEXT NOT NULL DEFAULT '',
    updated_at      INTEGER NOT NULL DEFAULT 0
);

-- Server-side and agent-evaluated checks.
CREATE TABLE checks (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id        INTEGER NOT NULL DEFAULT 0 REFERENCES tenants(id) ON DELETE CASCADE,
    host_id          INTEGER REFERENCES hosts(id) ON DELETE CASCADE,
    name             TEXT NOT NULL,
    type             TEXT NOT NULL,   -- ping, tcp, http, cert, dns, service, process, port, container
    target           TEXT NOT NULL DEFAULT '',
    config           TEXT NOT NULL DEFAULT '{}',
    interval_seconds INTEGER NOT NULL DEFAULT 60,
    timeout_seconds  INTEGER NOT NULL DEFAULT 10,
    failure_threshold INTEGER NOT NULL DEFAULT 3, -- consecutive failures before alerting
    severity         TEXT NOT NULL DEFAULT 'critical',
    enabled          INTEGER NOT NULL DEFAULT 1,
    status           TEXT NOT NULL DEFAULT 'pending', -- pending, up, down, warning
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    last_message     TEXT NOT NULL DEFAULT '',
    last_latency_ms  REAL NOT NULL DEFAULT 0,
    last_checked_at  INTEGER NOT NULL DEFAULT 0,
    last_change_at   INTEGER NOT NULL DEFAULT 0,
    created_at       INTEGER NOT NULL
);
CREATE INDEX checks_tenant ON checks(tenant_id);
CREATE INDEX checks_host ON checks(host_id);

-- Threshold rules with inheritance: global -> group -> host.
CREATE TABLE threshold_rules (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id     INTEGER NOT NULL DEFAULT 0 REFERENCES tenants(id) ON DELETE CASCADE,
    scope         TEXT NOT NULL,          -- global, group, host
    scope_id      INTEGER NOT NULL DEFAULT 0,
    metric        TEXT NOT NULL,          -- cpu, memory, swap, disk, load, disk_fill_days, ...
    label_match   TEXT NOT NULL DEFAULT '', -- e.g. "/var" for disk mount, "eth0" for iface
    warn          REAL,
    crit          REAL,
    for_count     INTEGER NOT NULL DEFAULT 2, -- consecutive evaluations before firing
    enabled       INTEGER NOT NULL DEFAULT 1,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL,
    UNIQUE (tenant_id, scope, scope_id, metric, label_match)
);

-- Current alert state, one row per (host, source key).
CREATE TABLE alerts (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id       INTEGER NOT NULL DEFAULT 0 REFERENCES tenants(id) ON DELETE CASCADE,
    host_id         INTEGER REFERENCES hosts(id) ON DELETE CASCADE,
    check_id        INTEGER REFERENCES checks(id) ON DELETE CASCADE,
    key             TEXT NOT NULL UNIQUE,  -- stable identity of the condition
    source          TEXT NOT NULL,         -- threshold, check, host, bruteforce, vuln, storage, cert, system
    metric          TEXT NOT NULL DEFAULT '',
    labels          TEXT NOT NULL DEFAULT '',
    severity        TEXT NOT NULL,         -- warning, critical
    status          TEXT NOT NULL,         -- pending, firing, resolved
    title           TEXT NOT NULL,
    message         TEXT NOT NULL DEFAULT '',
    value           REAL,
    threshold       REAL,
    pending_count   INTEGER NOT NULL DEFAULT 0,
    started_at      INTEGER NOT NULL,
    fired_at        INTEGER NOT NULL DEFAULT 0,
    resolved_at     INTEGER NOT NULL DEFAULT 0,
    last_eval_at    INTEGER NOT NULL,
    acked_by        TEXT NOT NULL DEFAULT '',
    acked_at        INTEGER NOT NULL DEFAULT 0,
    ack_comment     TEXT NOT NULL DEFAULT '',
    flapping        INTEGER NOT NULL DEFAULT 0,
    transitions     TEXT NOT NULL DEFAULT '[]',   -- recent transition timestamps
    last_notified_at INTEGER NOT NULL DEFAULT 0,
    notify_count    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX alerts_tenant_status ON alerts(tenant_id, status);
CREATE INDEX alerts_host ON alerts(host_id);

CREATE TABLE alert_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id   INTEGER NOT NULL DEFAULT 0,
    alert_id    INTEGER NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
    host_id     INTEGER,
    event       TEXT NOT NULL,   -- fired, resolved, acked, unacked, escalated, flapping, notified, silenced
    severity    TEXT NOT NULL DEFAULT '',
    message     TEXT NOT NULL DEFAULT '',
    actor       TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL
);
CREATE INDEX alert_events_alert ON alert_events(alert_id);
CREATE INDEX alert_events_tenant_time ON alert_events(tenant_id, created_at);

CREATE TABLE maintenance_windows (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id     INTEGER NOT NULL DEFAULT 0 REFERENCES tenants(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    scope         TEXT NOT NULL,          -- all, tenant, group, host
    scope_ids     TEXT NOT NULL DEFAULT '[]',
    starts_at     INTEGER NOT NULL,       -- one-off start or first occurrence
    ends_at       INTEGER NOT NULL,       -- one-off end (0 for recurring)
    recurrence    TEXT NOT NULL DEFAULT 'none', -- none, daily, weekly, monthly
    weekdays      TEXT NOT NULL DEFAULT '[]',   -- 0=Sun..6=Sat for weekly
    start_time    TEXT NOT NULL DEFAULT '',     -- HH:MM for recurring
    duration_minutes INTEGER NOT NULL DEFAULT 60,
    timezone      TEXT NOT NULL DEFAULT 'UTC',
    suppress_alerts INTEGER NOT NULL DEFAULT 1,
    allow_jobs    INTEGER NOT NULL DEFAULT 1, -- scheduled upgrades may run inside
    enabled       INTEGER NOT NULL DEFAULT 1,
    created_by    TEXT NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL
);

CREATE TABLE notification_channels (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id     INTEGER NOT NULL DEFAULT 0 REFERENCES tenants(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    type          TEXT NOT NULL,  -- webhook, discord, slack, teams, telegram, ntfy, gotify, pushover, email
    config        TEXT NOT NULL DEFAULT '{}',
    min_severity  TEXT NOT NULL DEFAULT 'warning',
    send_resolved INTEGER NOT NULL DEFAULT 1,
    repeat_minutes INTEGER NOT NULL DEFAULT 240,
    filter_groups TEXT NOT NULL DEFAULT '[]',
    filter_tags   TEXT NOT NULL DEFAULT '[]',
    filter_hosts  TEXT NOT NULL DEFAULT '[]',
    enabled       INTEGER NOT NULL DEFAULT 1,
    last_error    TEXT NOT NULL DEFAULT '',
    last_sent_at  INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL
);

CREATE TABLE notification_log (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id   INTEGER NOT NULL DEFAULT 0,
    channel_id  INTEGER REFERENCES notification_channels(id) ON DELETE CASCADE,
    alert_ids   TEXT NOT NULL DEFAULT '[]',
    status      TEXT NOT NULL,  -- sent, failed
    error       TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL
);

-- Remote actions. Every job is signed when dispatched and fully logged.
CREATE TABLE jobs (
    id              TEXT PRIMARY KEY,
    tenant_id       INTEGER NOT NULL DEFAULT 0 REFERENCES tenants(id) ON DELETE CASCADE,
    host_id         INTEGER NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    type            TEXT NOT NULL,
    params          TEXT NOT NULL DEFAULT '{}',
    dry_run         INTEGER NOT NULL DEFAULT 0,
    status          TEXT NOT NULL,   -- scheduled, queued, sent, running, succeeded, failed, cancelled, expired, rejected
    schedule        TEXT NOT NULL DEFAULT 'now', -- now, at, maintenance
    run_after       INTEGER NOT NULL DEFAULT 0,
    expires_at      INTEGER NOT NULL DEFAULT 0,
    timeout_seconds INTEGER NOT NULL DEFAULT 1800,
    requested_by    TEXT NOT NULL DEFAULT '',
    confirmed_by    TEXT NOT NULL DEFAULT '',
    created_at      INTEGER NOT NULL,
    sent_at         INTEGER NOT NULL DEFAULT 0,
    started_at      INTEGER NOT NULL DEFAULT 0,
    finished_at     INTEGER NOT NULL DEFAULT 0,
    exit_code       INTEGER,
    error           TEXT NOT NULL DEFAULT '',
    output          TEXT NOT NULL DEFAULT '',
    result_json     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX jobs_host ON jobs(host_id, created_at);
CREATE INDEX jobs_status ON jobs(status, run_after);
CREATE INDEX jobs_tenant ON jobs(tenant_id, created_at);

-- Brute-force detection: failed logins aggregated per host/IP.
CREATE TABLE security_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id   INTEGER NOT NULL DEFAULT 0,
    host_id     INTEGER NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    ip          TEXT NOT NULL,
    count       INTEGER NOT NULL,
    usernames   TEXT NOT NULL DEFAULT '[]',
    window_start INTEGER NOT NULL,
    window_end  INTEGER NOT NULL,
    source      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX security_events_host_time ON security_events(host_id, window_end);

CREATE TABLE ip_blocks (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id   INTEGER NOT NULL DEFAULT 0,
    host_id     INTEGER NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    ip          TEXT NOT NULL,
    reason      TEXT NOT NULL DEFAULT '',
    expires_at  INTEGER NOT NULL,
    job_id      TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'pending', -- pending, active, removed, failed
    created_by  TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL
);
CREATE INDEX ip_blocks_host ON ip_blocks(host_id, status);

CREATE TABLE fail2ban_status (
    tenant_id   INTEGER NOT NULL DEFAULT 0,
    host_id     INTEGER PRIMARY KEY REFERENCES hosts(id) ON DELETE CASCADE,
    data        TEXT NOT NULL DEFAULT '{}',
    updated_at  INTEGER NOT NULL
);

-- Vulnerability scanning.
CREATE TABLE vuln_scans (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id     INTEGER NOT NULL DEFAULT 0,
    host_id       INTEGER NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    status        TEXT NOT NULL,   -- running, completed, failed, skipped
    triggered_by  TEXT NOT NULL DEFAULT 'manual', -- manual, schedule, inventory
    packages      INTEGER NOT NULL DEFAULT 0,
    findings      INTEGER NOT NULL DEFAULT 0,
    critical      INTEGER NOT NULL DEFAULT 0,
    high          INTEGER NOT NULL DEFAULT 0,
    medium        INTEGER NOT NULL DEFAULT 0,
    low           INTEGER NOT NULL DEFAULT 0,
    db_built      TEXT NOT NULL DEFAULT '',
    error         TEXT NOT NULL DEFAULT '',
    started_at    INTEGER NOT NULL,
    finished_at   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX vuln_scans_host ON vuln_scans(host_id, started_at);

CREATE TABLE vuln_findings (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id       INTEGER NOT NULL DEFAULT 0,
    host_id         INTEGER NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    vuln_id         TEXT NOT NULL,
    package         TEXT NOT NULL,
    version         TEXT NOT NULL,
    fixed_version   TEXT NOT NULL DEFAULT '',
    fix_state       TEXT NOT NULL DEFAULT '',
    severity        TEXT NOT NULL DEFAULT 'Unknown',
    cvss            REAL NOT NULL DEFAULT 0,
    cvss_vector     TEXT NOT NULL DEFAULT '',
    epss            REAL NOT NULL DEFAULT 0,
    kev             INTEGER NOT NULL DEFAULT 0,
    description     TEXT NOT NULL DEFAULT '',
    urls            TEXT NOT NULL DEFAULT '[]',
    namespace       TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'open',  -- open, fixed, ignored
    first_seen_at   INTEGER NOT NULL,
    last_seen_at    INTEGER NOT NULL,
    fixed_at        INTEGER NOT NULL DEFAULT 0,
    UNIQUE (host_id, vuln_id, package)
);
CREATE INDEX vuln_findings_tenant ON vuln_findings(tenant_id, status, severity);

CREATE TABLE vuln_schedules (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id     INTEGER NOT NULL DEFAULT 0 REFERENCES tenants(id) ON DELETE CASCADE,
    scope         TEXT NOT NULL,     -- all, tenant, group, host
    scope_id      INTEGER NOT NULL DEFAULT 0,
    frequency     TEXT NOT NULL,     -- daily, weekly
    weekday       INTEGER NOT NULL DEFAULT 0,
    time_of_day   TEXT NOT NULL DEFAULT '03:00',
    enabled       INTEGER NOT NULL DEFAULT 1,
    last_run_at   INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL
);

-- SNMP: LLDP neighbours and interface table snapshot.
CREATE TABLE lldp_neighbors (
    tenant_id       INTEGER NOT NULL DEFAULT 0,
    host_id         INTEGER NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    local_port      TEXT NOT NULL,
    remote_chassis  TEXT NOT NULL DEFAULT '',
    remote_port     TEXT NOT NULL DEFAULT '',
    remote_port_desc TEXT NOT NULL DEFAULT '',
    remote_sysname  TEXT NOT NULL DEFAULT '',
    remote_sysdesc  TEXT NOT NULL DEFAULT '',
    remote_mgmt_addr TEXT NOT NULL DEFAULT '',
    updated_at      INTEGER NOT NULL,
    PRIMARY KEY (host_id, local_port, remote_chassis, remote_port)
);

CREATE TABLE status_pages (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id     INTEGER NOT NULL DEFAULT 0 REFERENCES tenants(id) ON DELETE CASCADE,
    slug          TEXT NOT NULL UNIQUE,
    title         TEXT NOT NULL,
    description   TEXT NOT NULL DEFAULT '',
    public        INTEGER NOT NULL DEFAULT 1,
    access_token  TEXT NOT NULL DEFAULT '',
    items         TEXT NOT NULL DEFAULT '[]',  -- [{type: host|check, id, label}]
    show_history  INTEGER NOT NULL DEFAULT 1,
    announcement  TEXT NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);

-- Uptime history for status pages and SLA: one row per item per day.
CREATE TABLE uptime_daily (
    tenant_id   INTEGER NOT NULL DEFAULT 0,
    item_type   TEXT NOT NULL,  -- host, check
    item_id     INTEGER NOT NULL,
    day         TEXT NOT NULL,  -- YYYY-MM-DD UTC
    up_seconds  INTEGER NOT NULL DEFAULT 0,
    down_seconds INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (item_type, item_id, day)
);

CREATE TABLE audit_log (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id   INTEGER NOT NULL DEFAULT 0,
    user_id     INTEGER,
    actor       TEXT NOT NULL,
    action      TEXT NOT NULL,
    target_type TEXT NOT NULL DEFAULT '',
    target_id   TEXT NOT NULL DEFAULT '',
    details     TEXT NOT NULL DEFAULT '{}',
    ip          TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL
);
CREATE INDEX audit_tenant_time ON audit_log(tenant_id, created_at);

-- Push installs (SSH / WinRM) and their logs.
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

-- Revoked agent certificates (serials).
CREATE TABLE revoked_certs (
    serial      TEXT PRIMARY KEY,
    tenant_id   INTEGER NOT NULL DEFAULT 0,
    host_id     INTEGER,
    revoked_at  INTEGER NOT NULL
);

-- +goose Down
DROP TABLE revoked_certs;
DROP TABLE push_installs;
DROP TABLE audit_log;
DROP TABLE uptime_daily;
DROP TABLE status_pages;
DROP TABLE lldp_neighbors;
DROP TABLE vuln_schedules;
DROP TABLE vuln_findings;
DROP TABLE vuln_scans;
DROP TABLE fail2ban_status;
DROP TABLE ip_blocks;
DROP TABLE security_events;
DROP TABLE jobs;
DROP TABLE notification_log;
DROP TABLE notification_channels;
DROP TABLE maintenance_windows;
DROP TABLE alert_events;
DROP TABLE alerts;
DROP TABLE threshold_rules;
DROP TABLE checks;
DROP TABLE inventory_status;
DROP TABLE packages;
DROP TABLE enrollment_tokens;
DROP TABLE hosts;
DROP TABLE host_groups;
DROP TABLE api_keys;
DROP TABLE sessions;
DROP TABLE webauthn_credentials;
DROP TABLE users;
DROP TABLE settings;
DROP TABLE tenants;
