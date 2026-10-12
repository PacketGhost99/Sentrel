# Single sign-on

Sentrel supports two kinds of single sign-on, both configured under **Settings → Single sign-on**:

- **OpenID Connect (OIDC)**: a "Sign in with ..." button on the login page. Works with Authentik, Authelia, Keycloak, Google, Microsoft Entra and other providers.
- **Forward-auth headers**: a reverse proxy (Authelia, an Authentik proxy outpost, oauth2-proxy) authenticates the user and passes the identity in headers.

Users created by SSO have `auth_source` of `oidc` or `proxy`. They cannot sign in with a password. Local, LDAP and RADIUS accounts keep working, and an SSO login can never take over an existing account that was created another way.

## OpenID Connect

### How it works

1. The login page shows the button when OIDC is enabled. It links to `/api/v1/auth/oidc/start`.
2. Sentrel sets a short-lived signed cookie holding the state, nonce and PKCE verifier, then redirects to the provider (authorization code flow with PKCE S256).
3. The provider redirects back to `/api/v1/auth/oidc/callback`. Sentrel checks the state, exchanges the code with the PKCE verifier, verifies the ID token signature, issuer, audience and nonce, and reads the claims. Groups missing from the ID token are read from the userinfo endpoint.
4. The user is created or updated, the role is mapped from their groups, and a normal session is issued.

### Redirect URI

Register this exact URL with the provider:

```
<server URL>/api/v1/auth/oidc/callback
```

The server URL is the **Public URL** in Settings → General (for example `https://monitor.example.com`). The settings page shows the full redirect URI with a copy button. If no server URL is set, the address of the current request is used, which is fragile behind a proxy, so set it.

### Settings

| Setting | Default | Meaning |
|---|---|---|
| `enabled` | off | Show the button and accept logins |
| `issuer_url` | | Provider issuer; must serve `/.well-known/openid-configuration` |
| `client_id`, `client_secret` | | From the provider. The secret is never returned by the API |
| `scopes` | `openid profile email groups` | Space separated. `openid` is always included |
| `button_label` | `SSO` | Shown as "Sign in with <label>" |
| `username_claim` | `preferred_username` | Falls back to email, then the subject |
| `groups_claim` | `groups` | A list or a comma-separated string. Dotted paths such as `realm_access.roles` work |
| `group_role_map` | none | List of `{group, role, tenant_id}`; the most privileged match wins (same rules as LDAP) |
| `default_role`, `default_tenant` | empty | Role for users matching no mapping. Empty refuses them |
| `allow_provision` | off | Create accounts on first sign-in. Otherwise only existing OIDC users can sign in |
| `require_verified_email` | off | Refuse users whose `email_verified` claim is not true |
| `trust_idp_mfa` | on | An `amr` claim containing `mfa`, `otp` or `hwk` counts as the second factor |

When `trust_idp_mfa` is off, or the provider does not assert MFA, Sentrel applies its normal two-factor policy (Settings → Security). **Test discovery** checks the issuer and shows warnings; save first.

Roles and groups are refreshed at every login, so removing someone from a group takes effect at their next sign-in.

### Authentik

1. Create an OAuth2/OpenID provider: client type **Confidential**, redirect URI `https://monitor.example.com/api/v1/auth/oidc/callback` (strict match).
2. Scopes: `openid`, `profile`, `email`. Authentik includes `groups` through its default profile mapping.
3. Create an application using the provider. Issuer URL: `https://auth.example.com/application/o/<application-slug>/` (keep the trailing slash: it must match the issuer exactly).
4. In Sentrel map your Authentik group names, e.g. `sentrel-admins` to Administrator.

### Authelia

```yaml
identity_providers:
  oidc:
    clients:
      - client_id: sentrel
        client_name: Sentrel
        client_secret: '$pbkdf2-sha512$...'   # hash of the secret you enter in Sentrel
        public: false
        authorization_policy: two_factor
        redirect_uris:
          - https://monitor.example.com/api/v1/auth/oidc/callback
        scopes: [openid, profile, email, groups]
        token_endpoint_auth_method: client_secret_basic
```

Issuer URL: `https://auth.example.com`.

### Keycloak

1. Create a client: **OpenID Connect**, **Client authentication on**, standard flow enabled. Valid redirect URI `https://monitor.example.com/api/v1/auth/oidc/callback`.
2. Add a client scope with a **Group Membership** mapper (token claim name `groups`, full group path off, add to ID token on) and assign it to the client; or point `groups_claim` at `realm_access.roles`.
3. Issuer URL: `https://keycloak.example.com/realms/<realm>`.
4. Scopes: `openid profile email groups`.

### Google and Entra

Both work with the default settings except that neither sends groups: use `default_role` (and `allow_provision`) rather than a group map. For Google use issuer `https://accounts.google.com` and set `username_claim` to `email`. For Entra use `https://login.microsoftonline.com/<tenant-id>/v2.0`.

## Forward-auth headers

For setups where the proxy in front of Sentrel already enforces login.

**Warning: the proxy must strip these headers from client requests and set them itself.** If a client can send `Remote-User` straight through, it can sign in as anyone. Sentrel only believes the headers when the request's direct TCP peer is in the trusted proxy list (Settings → TLS & access: **Trust reverse proxy headers** on, and **Trusted proxies**; empty means loopback only), but it cannot tell whether the proxy sanitised them.

Behaviour:

- A GET or HEAD request with no session and the user header present from a trusted peer creates a session (`auth_source` `proxy`) and sets the normal session cookie. Writes are never authenticated by headers alone; the UI loads the session first.
- A request with a valid session or API key ignores the headers.
- Headers from other peers are ignored entirely.
- Proxy sign-in is treated as fully authenticated: Sentrel does not ask for a second factor, because the proxy is responsible for it.

Settings (`proxy_auth`): `enabled`, `user_header` (default `Remote-User`), `email_header`, `name_header`, `groups_header` (comma separated), `group_role_map`, `default_role`, `default_tenant`, `allow_provision`.

Example for Caddy with Authelia, which forwards the identity headers and removes any the client sent:

```
monitor.example.com {
  forward_auth authelia:9091 {
    uri /api/authz/forward-auth
    copy_headers Remote-User Remote-Groups Remote-Email Remote-Name
  }
  reverse_proxy h2c://127.0.0.1:8080
}
```

Do not put agent gRPC traffic behind forward-auth; agents authenticate by themselves, so route their address separately or use an auth bypass for it.
