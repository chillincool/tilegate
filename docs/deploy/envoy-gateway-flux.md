# Deploying with Flux, Envoy Gateway and Keycloak

This guide runs tilegate in `proxy-jwt` mode behind an Envoy Gateway `SecurityPolicy`:

1. Envoy handles the OIDC login with Keycloak and forwards the user's access token to tilegate.
2. tilegate verifies that token against Keycloak's keys and reads the roles from it.

The apps behind the tiles keep their own `SecurityPolicy`. tilegate only decides which tiles each user sees.

## 1. Decide where roles come from

The tilegate client's access token has to contain every role that tilegate matches on. There are two common layouts.

**Realm groups.** Add a *Group Membership* mapper (claim `groups`, *Full group path* off) to a client scope that is assigned to the `tilegate` client. Then set `rolesClaim: groups`, and give each app something like `roles: [media]`.

**One client role per app.** Use this if each app already has its own Keycloak client, with an `access` role and a client scope named after the app that adds an `<app>_roles` claim. Add each app's client scope to the `tilegate` client as a **default** client scope. The tilegate token then carries `radarr_roles`, `grafana_roles` and so on, and each tile points at its own claim:

```yaml
- name: Radarr
  url: https://radarr.example.com
  icon: di:radarr
  claim: radarr_roles
  roles: [access]
```

This way, the role that grants access to an app also controls whether its tile appears, so there's only one place to manage it.

To check what tilegate will see, open Keycloak → Clients → `tilegate` → Client scopes → Evaluate, pick a user, and look at the generated access token.

## 2. Flux manifests

```yaml
# ocirepository.yaml
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata:
  name: tilegate
spec:
  interval: 15m
  layerSelector:
    mediaType: application/vnd.cncf.helm.chart.content.v1.tar+gzip
    operation: copy
  ref:
    tag: 0.1.0
  url: oci://ghcr.io/chillincool/charts/tilegate
```

```yaml
# helmrelease.yaml
apiVersion: helm.toolkit.fluxcd.io/v2
kind: HelmRelease
metadata:
  name: tilegate
spec:
  chartRef:
    kind: OCIRepository
    name: tilegate
  interval: 1h
  values:
    auth:
      mode: proxy-jwt
      rolesClaim: groups
      proxyJWT:
        issuer: https://login.example.com/realms/home
      # Matches logoutPath in the SecurityPolicy below.
      logoutURL: /logout
    route:
      enabled: true
      hostnames: [apps.example.com]
      parentRefs:
        - name: envoy-internal
          namespace: network
    config:
      title: My Apps
      apps:
        - name: Grafana
          url: https://grafana.example.com
          icon: di:grafana
          claim: grafana_roles
          roles: [admin, viewer]
        - name: Jellyfin
          url: https://jellyfin.example.com
          icon: di:jellyfin
          roles: [media]
```

The chart names the HTTPRoute after the release (`tilegate`), so a `SecurityPolicy` can target it by name:

```yaml
# securitypolicy.yaml
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: SecurityPolicy
metadata:
  name: tilegate-auth
spec:
  targetRefs:
    - group: gateway.networking.k8s.io
      kind: HTTPRoute
      name: tilegate
  oidc:
    provider:
      issuer: https://login.example.com/realms/home
    clientID: tilegate
    clientSecret:
      name: tilegate-oidc-secret     # key: client-secret
    redirectURL: https://apps.example.com/oauth2/callback
    logoutPath: /logout
    forwardAccessToken: true         # tilegate reads roles from this token
```

In Keycloak, set the `tilegate` client's redirect URI to `https://apps.example.com/oauth2/callback`. That callback belongs to Envoy, not to tilegate.

### Optional: favourites in CloudNativePG

Add this to the HelmRelease values and the chart creates the database cluster too:

```yaml
    database:
      cnpg:
        enabled: true
        storage:
          storageClass: longhorn
        # Optional: your own basic-auth Secret for the owner; otherwise CNPG generates one.
        existingSecret: tilegate-db-secret
        backup: {}                 # your usual spec.backup (barmanObjectStore etc.)
        scheduledBackup:
          enabled: false
```

The `Cluster` is a CNPG custom resource, so the Flux Kustomization that applies tilegate must wait for the operator:

```yaml
spec:
  dependsOn:
    - name: cloudnative-pg
      namespace: cloudnative-pg
```

## 3. Things to check

- **The page says "No signed-in user was passed to tilegate."** The `Authorization` header isn't arriving. Check `forwardAccessToken: true`, and look for `rejected forwarded token` in the logs (`logLevel: debug` shows more).
- **Some tiles are missing.** The role isn't in the access token. Use Keycloak's *Evaluate* tab (step 1). Also make sure the claim path matches exactly; client roles, for example, are `resource_access.<client>.roles`.
- **The token is too large.** Every client scope you add makes the token bigger. Envoy stores tokens in cookies, so once you pass roughly 30 apps, a single `groups` claim is easier to manage.
- **Built-in login instead.** If you'd rather not use a `SecurityPolicy`, set `auth.mode: oidc` and point the client's redirect URI at `https://apps.example.com/auth/callback`. Then tilegate runs the login itself.
