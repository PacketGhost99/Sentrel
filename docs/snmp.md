# SNMP devices

The server polls SNMP devices directly; nothing is installed on them. A device is a host of kind `snmp`: it gets liveness, interface statistics, LLDP neighbours and the metrics defined by a **profile** (CPU, memory, disks, temperatures, vendor-specific values). Threshold rules, checks, maintenance windows and notifications work the same as for agent hosts.

## Adding a device

**Add host, SNMP device** (or `POST /api/v1/hosts/snmp`). Fields:

| Field | Notes |
|---|---|
| name, address | the address is polled on UDP `port` (default 161) |
| version | `2c` (default) or `3` |
| community | v2c; defaults to `public` when empty |
| profile | `auto` (default) or a profile name |
| interval | seconds, minimum 15, default 60 |
| timeout, retries | per request; timeout defaults to 5 s |
| poll LLDP | on by default |
| extra metrics | additional OIDs for this device (same format as profile metrics) |
| group, tags, tenant | as for any host |

Use **Test** (`POST /api/v1/snmp/test`) before saving: it polls twice (so counter-based metrics show rates), and reports system info, the profile chosen, interface and neighbour counts, the metric values and per-metric errors.

### SNMPv3

Set the username and a security level:

| Level | Parameters |
|---|---|
| `noAuthNoPriv` | username |
| `authNoPriv` | + `auth_protocol` (`MD5`, `SHA` (default), `SHA224`, `SHA256`, `SHA384`, `SHA512`) and `auth_password` |
| `authPriv` | + `priv_protocol` (`DES`, `AES` (default), `AES192`, `AES256`, `AES192C`, `AES256C`) and `priv_password` |

An optional context name is supported. Communities and passphrases are stored in the database in clear text and never returned by the API ([security.md](security.md)).

### What is always collected

- System group: description, object id, name, location, contact, uptime (`hrSystemUptime` preferred).
- Interfaces (IF-MIB and IF-MIB extensions): name, alias, type, speed (`ifHighSpeed` preferred), MAC, admin/oper state, 64-bit counters when available. Metrics: `if_oper_up`, `if_in_bps`, `if_out_bps`, `if_util_percent`, `net_errors_per_sec`. `if_util` has default thresholds ([alerting.md](alerting.md)).
- LLDP neighbours (local port, remote chassis, port, system name and description, management address).
- `uptime_seconds` and `snmp_poll_ms`.

Profile metrics named `cpu_usage_percent`, `mem_used_percent`, `disk_used_percent` (label `mount`) and `temperature_celsius` (label `sensor`) feed the standard thresholds and the host summary. Any other name is stored as a time series (prefixed `hb_` in PromQL and `/metrics`).

## Profiles

Built in: `generic` (HOST-RESOURCES-MIB), `net-snmp`, `cisco`, `fortinet`, `hp`, `mikrotik`, `ubiquiti`. See them in `server/snmp/profiles/`.

**Selection** with `profile: auto`: the first profile (in alphabetical order) whose `match.sys_object_id` is a prefix of the device's `sysObjectID`; else the first whose `match.sys_descr` regex (case-insensitive) matches `sysDescr`; else `generic`.

**Custom profiles**: put `*.yaml` files in `<data dir>/snmp-profiles/` (they are included in backups). A custom file with the same `name` replaces a built-in. Apply changes with **Reload profiles** (`POST /api/v1/snmp/profiles/reload`, settings permission) or a restart. A custom file that fails validation (bad OID, expression that does not compile) stops loading at that file: the error is returned by the reload call and the profile list (`error`) and logged, and built-in profiles stay available.

### Format

```yaml
name: acme-switch                  # defaults to the file name
description: Acme X-series switches
include: [generic]                 # inherit metrics from other profiles (fallback)
match:
  sys_object_id: ["1.3.6.1.4.1.99999."]   # prefix match
  sys_descr: ["Acme X\\d+"]                # regex, case-insensitive
metrics:
  - name: cpu_usage_percent
    oid: 1.3.6.1.4.1.99999.1.1.0
```

Metric keys:

| Key | Meaning |
|---|---|
| `name` | metric name (required) |
| `oid` | one OID; shorthand for `expr: v`. A trailing `.0` makes it a scalar `GET`; any other OID is walked as a table |
| `expr`, `vars` | an expression over several OIDs. `vars` maps variable names to OIDs. The expression uses the [expr](https://expr-lang.org) language, receives the variables as numbers, and must return a number |
| `filter` | keep only table rows where the column `filter.oid` at the same index `equals` a value or matches `regex` |
| `aggregate` | how rows collapse into one value: `avg` (default), `sum`, `min`, `max`, `first`. Ignored with `label_oid` |
| `scale` | multiply the result (for example `0.1` for tenths of a degree) |
| `label_oid` | emit one series per row, labelled with the text in this column |
| `label_name` | the label's name (default `index`), for example `mount` or `sensor` |
| `label_exclude` | regex; rows whose label matches are dropped |
| `counters` | variables are monotonic counters; the expression sees per-second rates between consecutive polls (the first poll yields nothing; counter wraps are handled for 32-bit counters) |

Rules for evaluation:

- Scalar variables (OIDs ending in `.0`) are broadcast to every table index; table variables are joined on index, and only indices present in all of them are used.
- OIDs must be numeric dotted OIDs (a leading dot is allowed). Names and expressions are validated when the profile loads.
- `include` pulls in another profile's metrics. For each metric name the first definition that returns data wins, so a profile's own definition takes precedence and an included one acts as a fallback. This lets `net-snmp` provide an old `ssCpuIdle` formula and a newer raw-counter formula under the same name.
- A metric that returns no data is skipped; errors show in the test result and the host's SNMP info.

### Examples

A scalar with a scale, and a table with one series per sensor:

```yaml
metrics:
  - name: temperature_celsius          # chassis temperature in 0.1 C
    oid: 1.3.6.1.4.1.14988.1.1.3.10.0
    scale: 0.1
  - name: temperature_celsius          # one series per sensor
    oid: 1.3.6.1.4.1.9.9.13.1.3.1.3
    label_oid: 1.3.6.1.4.1.9.9.13.1.3.1.2
    label_name: sensor
```

An expression over a scalar pair, guarding against division by zero:

```yaml
  - name: disk_used_percent
    expr: "cap > 0 ? used * 100 / cap : 0"
    vars:
      used: 1.3.6.1.4.1.12356.101.4.1.6.0
      cap:  1.3.6.1.4.1.12356.101.4.1.7.0
```

A filtered table, aggregated with `max` (memory pools whose name contains "processor"):

```yaml
  - name: mem_used_percent
    expr: "used * 100 / (used + free)"
    vars:
      used: 1.3.6.1.4.1.9.9.48.1.1.1.5
      free: 1.3.6.1.4.1.9.9.48.1.1.1.6
    filter:
      oid: 1.3.6.1.4.1.9.9.48.1.1.1.2
      regex: "(?i)processor"
    aggregate: max
```

Per-mount disk usage from HOST-RESOURCES, skipping pseudo-filesystems:

```yaml
  - name: disk_used_percent
    expr: "used * 100 / size"
    vars:
      used: 1.3.6.1.2.1.25.2.3.1.6
      size: 1.3.6.1.2.1.25.2.3.1.5
    filter:
      oid: 1.3.6.1.2.1.25.2.3.1.2
      equals: 1.3.6.1.2.1.25.2.1.4
    label_oid: 1.3.6.1.2.1.25.2.3.1.3
    label_name: mount
    label_exclude: "^/(run|dev|sys|proc)(/|$)"
```

A rate from counters (retransmitted TCP segments per second as a percentage of segments sent):

```yaml
  - name: tcp_retransmit_percent
    counters: true
    expr: "retrans * 100 / (sent + 1)"
    vars:
      retrans: 1.3.6.1.2.1.6.12.0
      sent:    1.3.6.1.2.1.6.11.0
```

## Troubleshooting

- "no response to system group": wrong community or v3 credentials, a firewall on UDP 161, or the device restricts SNMP sources to specific addresses.
- A host shows as down about three poll intervals (at least 180 s) after the last successful poll.
- Run the **Test** to see which profile was chosen and which metrics failed. Use `snmpwalk` against the device to find the right OIDs, and test an `extra_metrics` entry on one device before putting it in a profile.
- A live test of the poller against a real device is part of the test suite ([development.md](development.md)).
