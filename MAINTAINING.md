# Maintaining tilegate

Notes for whoever cuts releases and looks after the repository.

## Releasing

Versions follow [semver](https://semver.org). While the version is `0.x`, a minor bump (0.1 → 0.2) may include breaking changes; call them out in the release notes.

```sh
git switch main && git pull
git tag v0.1.0
git push origin v0.1.0
```

The tag starts [`release.yml`](.github/workflows/release.yml), which:

1. Builds `linux/amd64` and `linux/arm64` images and pushes `ghcr.io/chillincool/tilegate:0.1.0` and `:0.1`. From 1.0 onwards it also pushes `:1`. Every image comes with an SBOM and build provenance.
2. Packages the Helm chart with `version` and `appVersion` both set to `0.1.0`, and pushes it to `oci://ghcr.io/chillincool/charts/tilegate`.
3. Creates a GitHub release with notes generated from the merged PRs.

Every push to `main` also publishes `:main` and `:sha-<short>` images, so you can test a change in your cluster before tagging it.

There's no separate chart version to keep in sync: the chart is always released together with the app, at the same version.

## One-time setup after the first push

- **Make the packages public.** Packages that GitHub Actions publishes to GHCR start out private. After the first release, open the org's *Packages* page, and for both `tilegate` and `charts/tilegate` go to *Package settings* → *Change visibility* → Public. Also confirm the package is linked to this repository.
- **Turn on private vulnerability reporting** in *Settings → Security*. [SECURITY.md](SECURITY.md) links to it.
- **Protect `main`.** In *Settings → Rules → Rulesets*, add a ruleset for `main` that requires a pull request and the `checks`, `e2e (ubuntu-latest)` and `e2e (ubuntu-24.04-arm)` CI checks, and blocks force pushes.
- **Merge settings.** Allow squash merging only, use the PR title as the commit message, and turn on *Automatically delete head branches*.
- **About box.** Add a description and the topics `self-hosted`, `oidc`, `keycloak`, `kubernetes`, `homelab`, `dashboard`.
- **Org Actions permissions.** If the release workflow fails with `denied: permission_denied` while pushing to GHCR, go to *Org settings → Actions → General → Workflow permissions* and allow read and write. The workflow already requests only the permissions it needs.

## Why the repository is set up this way

| Choice | Reason |
|---|---|
| **Go, standard library first** | One static binary, so the image needs no OS (`distroless/static`, about 12 MB, no shell). There are only three runtime dependencies (plus `go-jose` for the test IdP), and the OIDC one (`go-oidc`) is the de facto standard. Fewer dependencies means less to patch. |
| **`cmd/` and `internal/` layout** | The usual Go convention. Code under `internal/` can't be imported by other projects, so it can be refactored freely without breaking anyone. |
| **Configuration from environment variables, apps from YAML** | Environment variables suit containers and Kubernetes Secrets (`*_FILE` variants work with mounted secrets). The apps list is data that changes often, so it lives in a file that hot-reloads from a ConfigMap. |
| **Cross-compiling in the Dockerfile** | `--platform=$BUILDPLATFORM` plus `GOARCH` builds arm64 without QEMU emulation, which is much faster in CI. |
| **GHCR for both the image and the chart** | It's free for public repos, authenticates with the built-in `GITHUB_TOKEN` (no secrets to manage), and Flux can consume OCI Helm charts directly through `OCIRepository`. |
| **Chart in the same repo, versioned with the app** | One version number for users to think about, and chart changes are reviewed alongside the code they deploy. |
| **Tag-driven releases** | Pushing a tag is the whole release process. Nothing is published by hand, so a release can be repeated and checked later. |
| **Renovate for updates, GitHub for security alerts** | Renovate tracks every pinned version: Go modules, Actions (kept pinned to SHAs), the Dockerfile, `mise.toml` tools, and the image and chart versions in the e2e scripts. Dependabot can't see most of those. Updates arrive grouped, once a week; security fixes come straight away. GitHub's own Dependabot *alerts* stay on as a second pair of eyes. Config: [`.github/renovate.json5`](.github/renovate.json5). |
| **MIT licence** | Short and permissive, and the most common choice for small self-hosted tools. Anyone can use or fork tilegate, as long as the copyright notice stays. |
| **SECURITY.md, issue forms, PR template** | GitHub shows these in the right places (Security tab, *New issue*). The bug form asks for version, auth mode and IdP up front, which are what auth bugs almost always depend on. |
| **CI runs `mise` tasks** | Each check is defined once, in `mise.toml`. CI calls the same tasks you run locally, with the same tool versions, so "passes on my machine" and "passes in CI" mean the same thing. |
| **End-to-end tests in kind, on amd64 and arm64** | tilegate's job is login and access control, so the tests that matter run against a real Keycloak, Envoy Gateway and CloudNativePG. Free on public repos, and the arm64 job tests the arm64 image for real rather than only building it. |
| **CI also runs `govulncheck`** | An auth-adjacent project should hear about vulnerable dependencies before its users do. |
| **No `CHANGELOG.md`** | Generated release notes come from PR titles. Write clear PR titles and the changelog writes itself. |
