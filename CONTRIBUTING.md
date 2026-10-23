# Contributing

Thanks for your interest! Bug reports, docs fixes and small focused PRs are all welcome.
For anything larger, please open an issue first so we can agree on the approach.

## Development

Install [mise](https://mise.jdx.dev); it installs the exact Go, Helm, kind and kubectl versions CI uses. You also need Docker.

```sh
mise install                 # once
mise run test                # unit tests
mise run ci                  # everything CI checks, except e2e
mise run test:postgres       # unit tests including PostgreSQL (starts a temporary container)
mise run e2e                 # full end-to-end tests in a throwaway kind cluster (~5 min)
```

The end-to-end tests create a kind cluster with the CloudNativePG operator, Envoy Gateway and Keycloak, then install tilegate twice:
- with its built-in OIDC login, 2 replicas, and a CNPG database;
- in `proxy-jwt` mode behind an Envoy `SecurityPolicy`, with SQLite.

Then they sign in as three users through the real login forms and check each one's tiles, favourites and theme. CI runs the same tests on both amd64 and arm64.

To iterate on a change, keep the cluster up instead of rebuilding it every time:

```sh
mise run e2e:up      # create the cluster once
mise run e2e:test    # rebuild tilegate and rerun the tests (repeat as needed)
mise run e2e:down    # delete it
```

Inside this repo, mise points `KUBECONFIG` at `.e2e/kubeconfig`, and the scripts refuse to run against anything but the `kind-tilegate-e2e` context. So these commands can't touch any other cluster you have access to.

To try the app by hand:

```sh
TILEGATE_AUTH_MODE=dev TILEGATE_DEV_GROUPS=admins \
  TILEGATE_APPS_FILE=examples/apps.yaml go run ./cmd/tilegate
```

For a real login flow, use `examples/docker-compose`, which runs Keycloak.

Layout:

| Path | What's there |
|---|---|
| `cmd/tilegate` | Entry point and environment configuration |
| `internal/apps` | Apps file parsing, validation, hot reload, visibility filter |
| `internal/claims` | Claim path parsing and lookup |
| `internal/auth` | The auth modes and encrypted cookie sessions |
| `internal/server` | HTTP handlers, HTML template, CSS/JS (embedded in the binary) |
| `internal/store` | SQLite/PostgreSQL storage for favourites and preferences |
| `charts/tilegate` | Helm chart |
| `test/e2e` | End-to-end tests (kind, Keycloak, Envoy Gateway, CloudNativePG) |
| `scripts` | Helpers used by mise tasks and CI |

## Pull requests

- Run `mise run ci` before pushing. If you touched auth, the chart, or anything a user sees, also run `mise run e2e`.
- If you change behaviour or configuration, update the docs: the README for anything users meet first, `docs/` for the details.
- Keep dependencies to a minimum; this project deliberately has very few.
