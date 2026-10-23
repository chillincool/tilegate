# Configuration

tilegate is configured with environment variables. The apps and the theme live in the [apps file](apps-file.md). Any secret can be read from a file by setting `<NAME>_FILE` instead, e.g. `TILEGATE_SESSION_SECRET_FILE`.

- [Auth modes](#auth-modes)
- [How roles are matched](#how-roles-are-matched)
- [Databases](#databases)
- [All settings](#all-settings)
- [Endpoints](#endpoints)

## Auth modes

### `oidc`: built-in login

tilegate is a confidential OIDC client. It uses the authorization code flow with PKCE, and keeps the session in an encrypted cookie, so there's no session store and replicas only need to share the secret.

Register a client in your identity provider:

- **Redirect URI:** `https://apps.example.com/auth/callback`
- **Post-logout redirect URI:** `https://apps.example.com/*`
- **A mapper that puts groups or roles in the token.** In Keycloak, use a *Group Membership* mapper with *Full group path* off.

```sh
TILEGATE_AUTH_MODE=oidc
TILEGATE_OIDC_ISSUER=https://login.example.com/realms/home
TILEGATE_OIDC_CLIENT_ID=tilegate
TILEGATE_OIDC_CLIENT_SECRET=...
TILEGATE_OIDC_REDIRECT_URL=https://apps.example.com/auth/callback
TILEGATE_SESSION_SECRET=$(openssl rand -base64 48)
```

tilegate reads roles from both the ID token and the access token. Keycloak puts realm and client roles only in the access token by default, so this means they're picked up without extra mappers.

If tilegate reaches the identity provider at a different address than browsers do (an in-cluster Service, or Docker Compose), set `TILEGATE_OIDC_DISCOVERY_URL` to the internal address. The issuer stays the public URL.

### `proxy-jwt`: behind an auth proxy, verifying its token

The proxy signs users in and forwards their access token on every request. tilegate verifies the token's signature against the issuer's keys, so a request that bypasses the proxy can't fake an identity.

| Proxy | How to forward the token |
|---|---|
| Envoy Gateway | `SecurityPolicy` with `oidc.forwardAccessToken: true`; it arrives as `Authorization: Bearer …`, the default header. See the [Envoy Gateway + Flux guide](deploy/envoy-gateway-flux.md). |
| oauth2-proxy | `--pass-access-token` with `TILEGATE_PROXY_JWT_HEADER=X-Forwarded-Access-Token`, or `--pass-authorization-header` |
| Traefik + oauth2-proxy | `--set-authorization-header`, and add `Authorization` to the forward-auth middleware's `authResponseHeaders` |

```sh
TILEGATE_AUTH_MODE=proxy-jwt
TILEGATE_PROXY_JWT_ISSUER=https://login.example.com/realms/home
TILEGATE_LOGOUT_URL=/logout   # your proxy's sign-out path, if it has one
```

### `proxy-header`: behind an auth proxy, trusting plain headers

For proxies that can only pass headers like `X-Forwarded-User` and `X-Forwarded-Groups`. These headers aren't signed, so:

- tilegate refuses to start unless `TILEGATE_TRUSTED_PROXIES` lists your proxy's addresses;
- it ignores the headers on requests from anywhere else;
- the groups header stands in for every claim path, so per-app `claim:` settings don't apply.

Prefer `proxy-jwt` if your proxy can forward a token.

### `dev`

Every visitor is signed in as a test user with the groups in `TILEGATE_DEV_GROUPS`. It's for trying tilegate out and taking screenshots. **Never expose it.**

## How roles are matched

`TILEGATE_ROLES_CLAIM` lists one or more claim paths, separated by commas. Dots step into nested objects; write a literal dot as `\.`.

| Identity provider setup | `TILEGATE_ROLES_CLAIM` |
|---|---|
| Keycloak group mapper, Authentik, Authelia, Zitadel | `groups` |
| Keycloak realm roles | `realm_access.roles` |
| Keycloak client roles | `resource_access.tilegate.roles` |
| Several at once | `groups,realm_access.roles` |
| Namespaced claim (Auth0 style) | `https://example\.com/roles` |

A user sees an app if **any** value at **any** of those paths matches **any** of the app's `roles`. Claim values can be JSON arrays, or strings separated by spaces or commas.

In `oidc` mode, roles are captured at sign-in, so changes apply at the next sign-in or when the session expires (`TILEGATE_SESSION_TTL`, 8 hours by default). Roles are stored in the session cookie, which browsers limit to about 4 KB; tilegate logs a warning if a user's roles make it too large.

## Databases

A database is optional. It turns on [favourites](apps-file.md#favourites-and-app-ids), and makes each user's theme choice follow them across devices. tilegate creates and migrates its own tables.

| Database | Configure with | Replicas |
|---|---|---|
| SQLite | `TILEGATE_DATABASE_URL=sqlite:///data/tilegate.db` (chart: `persistence.enabled: true`) | 1 |
| PostgreSQL, as separate settings | `TILEGATE_DATABASE_HOST`, `_PORT`, `_NAME`, `_USER`, `_PASSWORD`, `_SSLMODE` (chart: `database.postgres`) | any |
| PostgreSQL, as a URL | `TILEGATE_DATABASE_URL=postgres://user:pass@host:5432/tilegate?sslmode=require` (chart: `database.existingSecret`) | any |
| CloudNativePG | chart: `database.cnpg.enabled: true` | any |

**Separate settings or a URL?** The separate settings match how most setups hand out credentials: a host, plus a password in a Secret. You don't have to build a URL, and special characters in passwords need no escaping.

**CloudNativePG.** With `database.cnpg.enabled: true`, the chart creates a `Cluster` named `<release>-db` and connects tilegate to it, so there's no URL to write. The CNPG operator must already be installed; with Flux, add `dependsOn` for it.

- **Credentials:** CNPG generates them by default. To use your own, put them in a `kubernetes.io/basic-auth` Secret and set `database.cnpg.existingSecret`.
- **Backups:** anything under `database.cnpg.backup` is copied into the cluster's `spec.backup`. `scheduledBackup.enabled` adds a `ScheduledBackup`; note CNPG's schedule has a leading seconds field, so `0 0 0 * * *` means daily at midnight.
- **Anything else:** `database.cnpg.extraSpec` is merged into the `Cluster` spec.

At startup tilegate keeps retrying the database for up to 2 minutes (`TILEGATE_DATABASE_WAIT`), because a new database often starts at the same moment.

## All settings

| Variable | Default | Description |
|---|---|---|
| `TILEGATE_AUTH_MODE` | `oidc` | `oidc`, `proxy-jwt`, `proxy-header` or `dev` |
| `TILEGATE_APPS_FILE` | `/config/apps.yaml` | Path to the apps file |
| `TILEGATE_ROLES_CLAIM` | `groups` | Comma-separated claim path(s) holding roles |
| `TILEGATE_LISTEN` | `:8080` | Listen address |
| `TILEGATE_ICONS_DIR` | | Folder served at `/icons/` |
| `TILEGATE_RELOAD_INTERVAL` | `10s` | How often to check the apps file; `0` turns reloading off |
| `TILEGATE_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| `TILEGATE_LOG_FORMAT` | `text` | `text` or `json` |
| **Database** | | |
| `TILEGATE_DATABASE_URL` | | `sqlite:///path` or `postgres://…` |
| `TILEGATE_DATABASE_HOST` | | PostgreSQL host; use instead of a URL |
| `TILEGATE_DATABASE_PORT` | `5432` | |
| `TILEGATE_DATABASE_NAME` | same as user | |
| `TILEGATE_DATABASE_USER` | `tilegate` | |
| `TILEGATE_DATABASE_PASSWORD` | | |
| `TILEGATE_DATABASE_SSLMODE` | driver default (`prefer`) | `disable`, `require`, `verify-full`, … |
| `TILEGATE_DATABASE_WAIT` | `2m` | How long to retry the database at startup |
| **oidc mode** | | |
| `TILEGATE_OIDC_ISSUER` | | Issuer URL (required) |
| `TILEGATE_OIDC_CLIENT_ID` | | Client ID (required) |
| `TILEGATE_OIDC_CLIENT_SECRET` | | Client secret |
| `TILEGATE_OIDC_REDIRECT_URL` | | `https://<host>/auth/callback` (required) |
| `TILEGATE_OIDC_SCOPES` | `openid profile email` | |
| `TILEGATE_OIDC_DISCOVERY_URL` | | Internal address for the identity provider's discovery |
| `TILEGATE_OIDC_POST_LOGOUT_REDIRECT_URL` | `https://<host>/` | Where users land after signing out |
| `TILEGATE_SESSION_SECRET` | | At least 32 characters; encrypts cookies (required) |
| `TILEGATE_SESSION_TTL` | `8h` | Session lifetime |
| **proxy modes** | | |
| `TILEGATE_PROXY_JWT_ISSUER` | | Expected token issuer (required for `proxy-jwt`) |
| `TILEGATE_PROXY_JWT_HEADER` | `Authorization` | Header carrying the token; a `Bearer ` prefix is removed |
| `TILEGATE_PROXY_JWT_JWKS_URL` | | Use this signing-key URL instead of discovery |
| `TILEGATE_PROXY_JWT_DISCOVERY_URL` | | Internal address for discovery |
| `TILEGATE_PROXY_JWT_AUDIENCE` | | If set, must be in the token's `aud` |
| `TILEGATE_PROXY_USER_HEADER` | `X-Forwarded-User` | `proxy-header` only |
| `TILEGATE_PROXY_EMAIL_HEADER` | `X-Forwarded-Email` | `proxy-header` only |
| `TILEGATE_PROXY_GROUPS_HEADER` | `X-Forwarded-Groups` | `proxy-header` only; comma-separated |
| `TILEGATE_TRUSTED_PROXIES` | | CIDRs allowed to send identity (required for `proxy-header`) |
| `TILEGATE_LOGOUT_URL` | | Sign-out link in the proxy modes |
| **dev mode** | | |
| `TILEGATE_DEV_GROUPS` | | Groups given to the test user |

## Endpoints

| Path | Purpose |
|---|---|
| `/` | The page |
| `/api/apps` | The user's apps as JSON |
| `/api/favorites` | The user's favourites (with a database) |
| `/healthz` | Liveness |
| `/readyz` | Readiness; also checks the identity provider and database are reachable |
| `/auth/login`, `/auth/callback`, `/logout` | Sign-in and sign-out (`oidc` mode) |
