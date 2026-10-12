# Vulnerability scanning

Heartbeat matches each Linux host's package inventory against the [Grype](https://github.com/anchore/grype) vulnerability database, embedded in the server as a library. No scanner runs on the monitored host; agents only send package lists.

## How matching works

1. The agent reports installed packages (name, version, architecture, package type `deb`/`rpm`/`apk`, source package) with the host's `os-release` `ID` and `VERSION_ID`.
2. The server converts them to Grype package records with the host's distribution and runs Grype's default matchers, including source-package ("upstream") matching, and normalises results by CVE.
3. Each finding stores the CVE/advisory id, package, installed and fixed version, fix state, severity (Critical, High, Medium, Low, Negligible, Unknown), CVSS score and vector, EPSS, whether it is in CISA's Known Exploited Vulnerabilities catalogue, description and reference URLs.
4. Findings that were present before but are no longer matched become `fixed`. Findings you ignored stay ignored. Per-host counts are stored as the `vulnerabilities_open{severity}` series.

Supported distributions (`ID` in `/etc/os-release`): `ubuntu`, `debian`, `raspbian` (matched as Debian), `rhel`, `centos`, `rocky`, `almalinux`, `ol`, `amzn`, `fedora`, `alpine`, `sles`, `opensuse-leap`, `mariner`, `azurelinux`, `wolfi`, `chainguard`, `photon`. Other Linux distributions (for example Arch) are recorded as "skipped" with the reason.

**Windows is not supported for package CVE matching.** Windows hosts get inventory and pending Windows Updates (and the `security_updates` threshold) but no CVE findings; scans for them are marked skipped. SNMP devices and agentless targets have no inventory and are skipped as well.

## The database

- Location: `<data dir>/vulndb/`. Plan for roughly **3 GB** of disk space on the data volume (schema v6).
- First run: if scanning is enabled and no local database exists, the server downloads it in the background; scans fail with "vulnerability database not downloaded yet" until it finishes.
- Updates: checked daily at `sync_hour` (server local time, default 2), or immediately if the database is older than 36 hours. **Update now** (`POST /api/v1/vulnerabilities/db/sync`, settings permission) starts a manual update. A failed update raises the system alert "Vulnerability database update failed" and clears when the next one succeeds.
- Status (build time, schema, size, last check, last error) is shown on the Vulnerabilities page and at `GET /api/v1/vulnerabilities/db`.
- Scans never need Internet access once the database is on disk; the server opens the existing database at start without network access.

## When scans run

| Trigger | Notes |
|---|---|
| After inventory | when a host's package inventory changed and `scan_after_inventory` is on (default) |
| Manual | host page, or **Scan all** (queues all visible hosts) |
| Schedules | **Vuln schedules**: scope `all`, `tenant`, `group` or `host`; `daily` or `weekly` (weekday 0 to 6, Sunday is 0); time of day in the timezone set under Settings, General |
| After a server restart | scans that were running are marked failed and re-queued |

Scans run one at a time from a queue with a 10 minute limit each. The scan history (packages, findings by severity, database build used) is kept per host.

## Alerts

After each scan the server evaluates one alert per host. It counts distinct open CVEs at or above `alert_severity` (`Critical` default, `High`, `Medium`, or `none` to disable) and, when `only_fixable` is on (default), only those that have a fixed version. The alert is critical when any Critical finding is counted, otherwise warning, and clears when the count reaches zero. Notification channels, maintenance windows and acknowledgement behave as for any alert ([alerting.md](alerting.md)).

## Ignoring findings

Mark a finding `ignored` (UI, or `PUT /api/v1/vulnerabilities/{id}` with `{"status":"ignored"}`; `monitoring` permission) to drop it from counts and alerts. Reopen with `{"status":"open"}`. Ignored findings stay ignored across rescans.

## Settings

**Settings, Vulnerabilities** (`vuln` section): `enabled` (default on), `db_url`, `sync_hour` (2), `scan_after_inventory` (on), `alert_severity` (Critical), `only_fixable` (on).

## Air-gapped and mirrored databases

Set `db_url` to your own mirror. It follows Grype's distribution layout:

- either the base URL (the server appends `/v6/latest.json`), for example `https://mirror.internal/grype/databases`, or a direct URL ending in `.json`;
- `latest.json` is a small document describing the newest database; its `path` field is the archive file name, **relative to the location of `latest.json`**, and it carries the archive checksum.

To operate a mirror: on a machine with Internet access fetch Anchore's `https://grype.anchore.io/databases/v6/latest.json` and the archive named in its `path` (at the same relative location), transfer both into the isolated network, serve them over HTTP(S) at the URL you configured, and repeat on your own schedule. The server's daily update check then picks up each new archive. Updates are skipped when the mirrored `latest.json` is not newer than what is installed. Keep the file layout and checksum intact, since the server verifies the checksum. Because validity-age checks are disabled in Heartbeat, an old mirror still works; check the build date in the status panel to see how stale it is.

Anchore is not contacted when `db_url` points to your mirror. The server also needs no other Internet access for vulnerability scanning.
