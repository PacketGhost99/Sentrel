# Disk usage and cleanup

## Disk usage analysis

Open a host, choose a mount and run an analysis (or call the API). The agent walks that one filesystem the way `du -x` does: it does not cross into other mounts and skips `/proc`, `/sys` and `/dev`. Unreadable entries are counted and skipped, and the walk stops at its time limit and returns what it found. On Linux the walk runs through the privileged helper (`heartbeat-agent priv disk-usage`) so it can read every directory.

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

Cleanup is off for every host until `allow_cleanup` is enabled in the host settings. A cleanup job needs `confirm`; a dry run (`dry_run: true`) only estimates what would be freed. The task list is fixed (`GET /hosts/{id}/disk-cleanup/tasks`); anything else is rejected by both the server and the agent helper, and no task passes user input to a shell.

| Linux task | What it does |
|---|---|
| `pkg_cache` | `apt-get clean`, `dnf clean all` and equivalents |
| `journal` | `journalctl --vacuum-size=200M --vacuum-time=14d` |
| `old_kernels` | `apt-get autoremove --purge`, `dnf autoremove` |
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
