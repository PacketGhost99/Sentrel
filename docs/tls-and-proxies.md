# TLS and reverse proxies

The server uses one listener for the UI, the REST API, the agent gRPC stream, installers and `/metrics`. HTTP/2 requests with `Content-Type: application/grpc` go to the gRPC server; everything else goes to the web router. Any proxy in front of Sentrel therefore has to pass HTTP/2 through to the origin.

TLS is configured under **Settings, TLS & access** (and in the setup wizard). Changing it rebinds the listeners without a process restart.

| Setting | Meaning |
|---|---|
| `mode` | `http`, `selfsigned` or `acme` |
| `listen` | address, default `0.0.0.0:8080` (the `--listen` flag or `SENTREL_LISTEN` overrides it) |
| `hostname`, `extra_names` | names for the certificate |
| `acme_email`, `acme_directory` | Let's Encrypt contact and an alternative ACME directory URL |
| `trust_proxy` | trust forwarding headers from a reverse proxy |
| `trusted_proxies` | CIDRs or IPs of the proxies whose forwarding headers are believed (default: loopback) |

Also set **General, server URL** (for example `https://monitor.example.com`): it is the address agents use, the base of links in notifications and the host in install commands.

## Mode: `http`

Plain HTTP, with HTTP/2 cleartext (h2c) so gRPC works. Use it on a trusted network, or behind a reverse proxy that terminates TLS. Agents use an `http://` server URL. Traffic between agent and server is not encrypted in this mode (jobs are still signed and agents still authenticate with a signed hello, see [security.md](security.md)), so do not expose it to the internet.

## Mode: `selfsigned`

The internal CA signs a server certificate (ECDSA P-256, 397 days, regenerated when the names change or less than 30 days remain). Names included: `hostname`, `extra_names`, the listen host if it is specific, `localhost`, `127.0.0.1` and the machine's non-loopback IP addresses.

- Agents do not trust this CA through the system store. The install commands on the **Agents** page therefore pin the server: the installer is fetched with `curl -k --pinnedpubkey "sha256//<spki>"` (a PowerShell certificate callback on Windows) and passed `--pin-sha256 <spki>`, so the binary download is pinned too, and `--ca-fingerprint sha256:...` pins the CA during registration, after which the agent trusts the CA it receives. `<spki>` is the base64 SHA-256 of the server certificate's public key (SubjectPublicKeyInfo); `GET /api/v1/agents/install-info` returns it as `spki_sha256`, and it changes when the server certificate is regenerated. Pinning needs `curl` (wget cannot pin a key); `--insecure-download` is deprecated and no longer generated. The CA fingerprint is also under **Settings** (`ca_fingerprint`).
- Browsers will warn unless you import `pki/ca.crt` as a trusted root.
- When the server terminates TLS itself, agents present their client certificate and are authenticated by mTLS.

## Mode: `acme` (Let's Encrypt)

Uses `golang.org/x/crypto/acme/autocert`. Requirements: a public DNS name (not an IP) in `hostname` pointing at the server, and ports 443 and 80 reachable from the internet. The server binds `:443` regardless of the port in `listen`, plus `:80` for HTTP-01 challenges and the HTTP to HTTPS redirect (if `:80` cannot be bound it relies on TLS-ALPN-01 on 443). Certificates and the account key are cached in `<data dir>/acme`. Set `acme_directory` to use a different ACME CA (for example a staging endpoint). The service needs `cap_net_bind_service`, which the installer grants.

## Behind a reverse proxy

Run Sentrel in `http` mode on a local port, terminate TLS in the proxy, and enable `trust_proxy`.

### Caddy

```
monitor.example.com {
    reverse_proxy h2c://127.0.0.1:8080
}
```

`h2c://` makes Caddy speak HTTP/2 cleartext to the upstream, which gRPC needs. Caddy obtains the certificate and proxies the long-lived server-sent-events stream (`/api/v1/events`) without buffering.

Then in Sentrel: TLS mode `http`, listen `127.0.0.1:8080`, server URL `https://monitor.example.com`, `trust_proxy` on. Agents register against `https://monitor.example.com` and verify the proxy's certificate with the system trust store.

For other proxies (nginx, HAProxy, Traefik) the requirements are the same: HTTP/2 to the upstream (gRPC proxying), no response buffering for streaming endpoints, long idle/read timeouts for the agent stream (the server sends a ping every 30 seconds), and forwarding of `X-Forwarded-For`, `X-Forwarded-Proto` and `X-Forwarded-Host`.

### `trust_proxy`

When enabled, forwarding headers are believed only if the TCP peer address is inside `trusted_proxies`, a list of IPs and CIDR ranges that defaults to loopback (`127.0.0.0/8`, `::1`) when empty. Requests from any other peer have their `X-Forwarded-*`, `CF-Connecting-IP` and `X-Real-IP` headers ignored.

- Client address: `X-Forwarded-For` is read from the right, skipping entries that are themselves trusted proxies; the first untrusted address is the client. Entries a client prepends on the left are therefore ignored, and a malformed chain falls back to the peer address. Without `X-Forwarded-For`, `CF-Connecting-IP` and then `X-Real-IP` are used.
- HTTPS: `X-Forwarded-Proto: https` (last value) marks the request as HTTPS, which sets the `Secure` flag on cookies.
- `X-Forwarded-Host` is used when deriving the server's own URL.

The client address feeds the audit log, login lockout and session list. If a CDN such as Cloudflare sits in front of your proxy, add the CDN's published ranges and your proxy's address to `trusted_proxies` so the walk reaches the real client.

Enable it only when the server is reachable exclusively through the proxy. If clients can reach the port directly they can spoof these headers and evade lockout and audit attribution. Leave it off otherwise.

### mTLS versus signed hello

A proxy that terminates TLS hides the agent's client certificate from the server. Agents then authenticate with the signed hello (their certificate plus an ECDSA signature over the agent id and a timestamp, with replay protection). Server-terminated TLS (`selfsigned`, `acme`) additionally uses mTLS.


## Recovery from a bad TLS setting

- A TLS change made in the UI is checked before the running listeners are touched: the certificate and key must load and a new address must be bindable. If the check fails, the current listeners keep running, the previous settings are restored, and the error is logged and recorded in the audit log (`settings.tls_reload_failed`).
- If the new listeners still fail to start, the last working configuration is restarted. If that fails as well, the server exits instead of serving plain HTTP; it never falls back to plain HTTP by itself. Plain HTTP only happens when you choose mode `http` or pass `--force-http`.
- If it cannot start at all (for example after a restart with a broken certificate setup), run it once with `--force-http`:

  ```sh
  sentrel-server serve --data-dir /var/lib/sentrel --force-http
  ```

  Then open the UI over HTTP and correct the TLS settings. For the systemd service, add `--force-http` to `ExecStart` with `systemctl edit sentrel`, fix the setting, and remove it again.
- Moving to a new hostname: agents keep the server URL from registration in `agent.yaml`. Keep the old name resolving, or edit `server_url` in each agent's config and restart it.
