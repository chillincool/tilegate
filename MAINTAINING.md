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

## Making changes

`main` is protected, so every change, including your own, goes through a pull request:

```sh
git switch -c fix/something
# ...edit, then: mise run ci   (and mise run e2e for anything user-facing)
git commit -am "fix: something"
git push -u origin fix/something
gh pr create --fill
gh pr merge --squash --auto     # merges by itself once the checks pass
```

Dependabot opens PRs for Go modules, GitHub Actions and the Docker base image every week. CI tests each one; merge the green ones. For a major version bump, skim the upstream release notes first.

## Repository settings

These are already applied. They're listed here so you know what's enforced and where to change it.

| Setting | What it does | Where |
|---|---|---|
| `main` ruleset | Changes need a PR; the `checks`, `e2e (ubuntu-latest)` and `e2e (ubuntu-24.04-arm)` checks must pass; CodeQL must report no high or critical alerts; no force-pushes, deletion or merge commits. No bypass, not even for admins. | Settings → Rules → Rulesets |
| `release tags` ruleset | `v*` tags can't be moved, overwritten or deleted. | Settings → Rules → Rulesets |
| Immutable releases | A published release's tag and assets can't be changed. | Settings → General → Releases |
| Merging | Squash only, with the PR title as the commit message. Auto-merge on; branches deleted after merge. | Settings → General → Pull Requests |
| Private vulnerability reporting | The "Report a vulnerability" button that [SECURITY.md](SECURITY.md) links to. | Settings → Security |
| Secret scanning + push protection | Blocks pushes containing known credential formats, and alerts on any already committed. | Settings → Security |
| Dependabot alerts + security updates | Alerts on vulnerable dependencies and opens fix PRs automatically. | Settings → Security |
| CodeQL (default setup, extended queries) | Scans the Go code and the workflow files on every PR, on every push to `main`, and weekly. | Security → Code scanning |
| Actions: read-only token by default | Workflows must ask for write access explicitly; `release.yml` asks for `contents` and `packages`. | Settings → Actions → General |
| Actions: allowed actions | Only GitHub's own actions, verified creators, `jdx/mise-action`, `azure/setup-helm` and `docker/*`. Adding a new action means adding it here. | Settings → Actions → General |
| Actions: outside contributors | Workflows from outside contributors' PRs wait for your approval before running. | Settings → Actions → General |
| Actions pinned to commit SHAs | Workflows use full commit SHAs, not movable tags; Dependabot updates them. | `.github/workflows/` |

Not enabled, because they need a paid GitHub licence: secret scanning for generic (non-vendor) patterns, and secret validity checks.

### Still to do after the first release

- **Make the packages public.** Packages that GitHub Actions publishes to GHCR start out private. After tagging `v0.1.0`, open the organisation's *Packages* page, and for both `tilegate` and `charts/tilegate` go to *Package settings* → *Change visibility* → Public. Also check each package is linked to this repository.

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
| **Rulesets with no bypass, and SHA-pinned actions** | A solo maintainer is the easiest account to compromise and the likeliest to cut corners at 1am. Requiring PRs and green checks even for the owner means nothing reaches `main` or a release untested. Pinning actions to commits means an upstream tag being moved can't change what runs in CI. |
| **CI also runs `govulncheck`** | An auth-adjacent project should hear about vulnerable dependencies before its users do. |
| **No `CHANGELOG.md`** | Generated release notes come from PR titles. Write clear PR titles and the changelog writes itself. |
