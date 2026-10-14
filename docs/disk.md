# Disk usage and cleanup

## Disk usage analysis

Open a host, choose a mount and run an analysis (or call the API). The agent walks that one filesystem the way `du -x` does: it does not cross into other mounts and skips `/proc`, `/sys` and `/dev`. Unreadable entries are counted and skipped, and the walk stops at its time limit and returns what it found. On Linux the walk runs through the privileged helper (`sentrel-agent priv disk-usage`) so it can read every directory.

The report lists the biggest directories (sizes aggregated up to a depth, default 3) and the 20 largest files. The newest five reports per host and mount are kept.

When a `disk`, `inodes` or `disk_fill_hours` alert fires for an agent host, an analysis of that mount is queued automatically, at most once every 6 hours per host and mount.

```bash
# Queue an analysis, then read the latest report
curl -X POST -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
     -d '{"mount":"/"}' https://monitor.example.com/api/v1/hosts/7/disk-usage
curl -H "Authorization: Bearer $KEY" 'https://monitor.example.com/api/v1/hosts/7/disk-usage?mount=/'
```

It can also be run as a plain job: `{"type":"disk_usage","host_id":7,"params":{"path":"/var","depth":"3","top":"30"}}`.

## Disk cleanup

Cleanup is controlled by `allow_cleanup`, which is on by default (Settings, Agents, Host defaults) and can be overridden per host. On a host it is the **Allow disk cleanup** switch in the storage breakdown on the Overview tab, next to the Clean up button; it shows whether the value is inherited from the global default or overridden. A cleanup job needs `confirm`; a dry run (`dry_run: true`) only estimates what would be freed. The task list is fixed (`GET /hosts/{id}/disk-cleanup/tasks`); anything else is rejected by both the server and the agent helper, and no task passes user input to a shell.

| Linux task | What it does |
|---|---|
| `pkg_cache` | `apt-get clean`, `dnf clean all` and equivalents |
| `journal` | `journalctl --vacuum-size=200M --vacuum-time=14d` |
| `old_kernels` | removes every installed kernel except the running one and the newest (`apt-get purge` of the exact packages of each old kernel, `dnf remove`); a dry run reports the packages and sizes. See [vulnerabilities.md](vulnerabilities.md#kernels) |
| `tmp` | files in `/tmp` and `/var/tmp` not modified for 10 days |
| `docker` | `docker system prune -f` (dangling images, stopped containers, build cache; never volumes) |
| `logs` | rotated or compressed logs in `/var/log` (`*.gz`, `*.1`, `*.old`) older than 7 days |

| Windows task | What it does |
|---|---|
| `tmp` | files older than 7 days in the service account's temp folder and `C:\Windows\Temp` |
| `windows_update` | `C:\Windows\SoftwareDistribution\Download` |
| `recycle` | the recycle bin on fixed drives |
| `component_store` | `DISM /Online /Cleanup-Image /StartComponentCleanup` |

The job output has one line per task and ends with `freed_bytes=<n>`.

```bash
curl -X POST -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
     -d '{"host_id":7,"type":"disk_cleanup","dry_run":true,"params":{"tasks":"journal,tmp,logs"}}' \
     https://monitor.example.com/api/v1/jobs
```

## Disk health (Linux)

Linux agents report SMART, software RAID and ZFS health about every 30 minutes (first report a couple of minutes after the agent starts). It appears as the **Disk health** card on a host's System tab; `GET /api/v1/hosts/{id}/disk-health` returns the latest snapshot. **Windows hosts are not covered yet.**

| Source | What runs | Root? |
|---|---|---|
| SMART | `sentrel-agent priv smart`, which runs `smartctl --scan-open -j` and then `smartctl -a -j [-d TYPE] DEVICE` for each device found | Yes: smartctl needs raw access to the disks |
| Software RAID | reads `/proc/mdstat` | No |
| ZFS | `zpool list -H -o name,health,size,alloc` and `zpool status -x` | No: both are read-only and work for an unprivileged user |

The `smart` verb is covered by the existing `priv *` sudo rule, so no sudoers change is needed. It runs no shell, uses fixed smartctl locations (`/usr/sbin`, `/usr/bin`, `/sbin`, `/usr/local/...`) rather than `PATH`, caps each run at 25 seconds and 2 MB of output, and handles at most 32 devices. Device paths must match `/dev/sd[a-z]`, `/dev/hd[a-z]`, `/dev/vd[a-z]` (up to three letters) or `/dev/nvme<N>n<M>`; anything else the scan reports is ignored. The optional `-d` type is limited to `ata`, `sat`, `scsi` and `nvme`.

If smartctl is not installed, the card shows "Install smartmontools to see SMART data" (smartmontools 7.0 or newer is required for JSON output). Nothing is installed automatically. Serial numbers are reduced to their last four characters before leaving the host.

Per device the card shows model, health (the SMART overall check), temperature, power-on hours, reallocated, pending and uncorrectable sectors, and for NVMe the percentage of rated endurance used and media errors. RAID rows show members working, failed members and resync or recovery progress; ZFS rows show pool health and usage.

Alerts raised from each report (they clear when the condition goes away or the device disappears):

| Condition | Severity |
|---|---|
| SMART health check failed | critical |
| Reallocated, pending or uncorrectable sectors rising above the count first seen for that drive (stays raised until a week passes with no further rise, then the new count is the baseline; a replaced drive starts fresh) | warning |
| NVMe percentage used at or above 90 | warning |
| md array degraded or failed | critical |
| ZFS pool not `ONLINE` | critical |

Numeric values are also stored as metrics: `smart_temperature_celsius`, `smart_reallocated_sectors`, `smart_pending_sectors`, `smart_uncorrectable_sectors`, `smart_nvme_percentage_used` and `smart_power_on_hours`, each labelled with `device`.
