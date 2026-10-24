# tilegate

A self-hosted "My Apps" launcher. Each signed-in user sees only the apps their OIDC roles or groups allow, and the filtering happens on the server.

Full documentation lives in the [project repository](https://github.com/chillincool/tilegate).

![tilegate](https://raw.githubusercontent.com/chillincool/tilegate/main/docs/screenshots/admin-light.png)

## Install

```sh
helm install tilegate oci://ghcr.io/chillincool/charts/tilegate -f values.yaml
```

## Examples

**Built-in OIDC login** (Keycloak, Authentik, Zitadel, …), with favourites stored in SQLite:

```yaml
auth:
  mode: oidc
  rolesClaim: groups
  oidc:
    issuer: https://login.example.com/realms/home
    redirectURL: https://apps.example.com/auth/callback
  existingSecret: tilegate-oidc   # keys: client-secret, session-secret
persistence:
  enabled: true
route:
  enabled: true
  hostnames: [apps.example.com]
  parentRefs: [{ name: envoy-external, namespace: network }]
config:
  title: My Apps
  apps:
    - name: Grafana
      url: https://grafana.example.com
      icon: di:grafana
      roles: [admins]
```

**Behind an auth proxy** that forwards the user's token (Envoy Gateway, oauth2-proxy, …):

```yaml
auth:
  mode: proxy-jwt
  proxyJWT:
    issuer: https://login.example.com/realms/home
  logoutURL: /logout
```

**Favourites in a CloudNativePG cluster** the chart creates (needs the CNPG operator). PostgreSQL also allows more than one replica:

```yaml
replicaCount: 2
database:
  cnpg:
    enabled: true
    storage:
      storageClass: longhorn
```

## Key values

| Value | Default | Description |
|---|---|---|
| `auth.mode` | `oidc` | `oidc`, `proxy-jwt`, `proxy-header` or `dev` |
| `auth.rolesClaim` | `groups` | Comma-separated claim path(s) holding roles, e.g. `groups,realm_access.roles` |
| `auth.oidc.issuer`, `auth.oidc.redirectURL` | | Required in `oidc` mode |
| `auth.existingSecret` | | Secret with `client-secret` and `session-secret`. If empty, the chart creates one and generates the session secret. |
| `auth.proxyJWT.issuer` | | Required in `proxy-jwt` mode |
| `config` | | The apps file: apps, roles, theme. See the [apps file reference](https://github.com/chillincool/tilegate/blob/main/docs/apps-file.md). |
| `existingConfigMap` | | Use your own ConfigMap (key `apps.yaml`) instead of `config` |
| `persistence.enabled` | `false` | SQLite on a PVC, which turns on favourites (single replica) |
| `database.cnpg.enabled` | `false` | Create a CloudNativePG cluster for favourites |
| `database.postgres.host` | | Use an existing PostgreSQL server |
| `route.enabled` / `ingress.enabled` | `false` | Gateway API HTTPRoute, or Ingress |
| `podSecurityContext.runAsUser` | `65532` | Any UID works |

Every option is documented in [`values.yaml`](https://github.com/chillincool/tilegate/blob/main/charts/tilegate/values.yaml). The chart includes a values schema, so a misspelt key or an invalid value fails at install time rather than being silently ignored.

## Verifying the chart and image

Releases are signed with [cosign](https://docs.sigstore.dev/) using GitHub Actions' identity, with no long-lived keys. To check that a chart or image was built by this repository's release workflow:

```sh
VERSION=0.1.2   # the release you're checking
cosign verify ghcr.io/chillincool/charts/tilegate:$VERSION \
  --certificate-identity-regexp '^https://github.com/chillincool/tilegate/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com

cosign verify ghcr.io/chillincool/tilegate:$VERSION \
  --certificate-identity-regexp '^https://github.com/chillincool/tilegate/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```
