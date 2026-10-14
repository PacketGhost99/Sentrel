# Roadmap

This is the current direction for Sentrel. Items move between sections as work starts and priorities change, and there are no fixed dates. Ideas and votes are welcome in [GitHub Discussions](https://github.com/PacketGhost99/Sentrel/discussions); bugs go in [Issues](https://github.com/PacketGhost99/Sentrel/issues). Shipped work is listed in the [changelog](CHANGELOG.md).

## Now

- **Fleet-scale performance.** Paginate the host list API and UI. Smooth the reconnect burst when thousands of agents connect at once. The current limits are in [Capacity](docs/operations.md#capacity).
- **Staged agent updates.** Roll agent updates out in batches (a canary group first, then the rest), confirm each host is running the new version, and pause automatically when updates fail.
- **Staged patch rollouts.** Extend update schedules with batches and pause-on-failure, so a bad package update stops after the first hosts instead of reaching the whole fleet.

## Next

- **Monitoring rules from discovery.** Agents already report every service, listening port and container. Rules will turn that into monitoring automatically, for example "hosts tagged `role:web` get an HTTP check and an nginx service check", applied by group, tag or label.
- **Escalation and on-call.** Notify the next person or channel when an alert stays unacknowledged, with simple on-call rotations and handover, on top of the existing repeat notifications and severity escalation.
- **Local check plugins.** Let the agent run check scripts that an administrator places on the host, unprivileged and compatible with Nagios-style plugins. Scripts stay host-local by design: the server never pushes code to agents.
- **Remote probes.** Let an agent run ping, TCP, HTTP, DNS, certificate and SNMP checks for its own network. Sites the server cannot reach directly then need no separate collector.
- **Log pattern alerts.** Match journald and Windows Event Log entries, such as OOM kills, disk I/O errors or custom patterns, and alert on them.
- **Windows parity.** Bring disk health and port-scan detection, which are Linux-only today, to Windows agents.

## Later

- **Reports and SLAs.** Scheduled availability reports per host, group or tenant, with SLA targets and exportable summaries.
- **Configuration as code.** Export and import checks, thresholds, notification rules and schedules as YAML, for backups, review and automation.
- **Import from Uptime Kuma.** Bring monitors and notification settings across from an Uptime Kuma backup.
- **Application checks.** Database query checks (PostgreSQL, MySQL) and a wider set of built-in service checks.
- **Status page incidents.** Post incident updates and planned-maintenance notices on status pages.
- **Mobile.** An installable web app with push notifications.
- **Optional reboots in update schedules.** Reboot hosts that need it inside a maintenance window, one batch at a time.

## Not planned

- **Kubernetes monitoring.** Sentrel focuses on servers, sites and network devices. Tools built for Kubernetes do that job better.
- **High availability or clustering.** Sentrel is a single server with a single data directory by design. Backup and restore are the recovery path ([operations](docs/operations.md)).
- **Scripts pushed from the server.** Running server-supplied code on agents would let a compromised server control every host. Custom checks will come through local plugins instead ([threat model](docs/threat-model.md)).
