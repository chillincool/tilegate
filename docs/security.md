# Security

How tilegate protects sessions, tokens and itself. To report a vulnerability, see [SECURITY.md](../SECURITY.md).

**What tilegate does and doesn't protect.** tilegate decides which tiles each user sees, and the filtering happens on the server: apps a user can't see are never sent to their browser. It doesn't control access to the apps themselves. Each app needs its own login, or a proxy in front of it.

## Sign-in and sessions

- **OIDC login checks.** The built-in login uses the authorization code flow with PKCE (S256), and checks both `state` and `nonce`.
- **Tokens are verified before use.** ID tokens and access tokens are signature-checked before tilegate reads any claims. In `proxy-jwt` mode, every forwarded token is verified the same way.
- **Encrypted cookies.** Sessions are stored in AES-GCM-encrypted cookies, each bound to its own name. Users can't read or change their own roles.
- **Cookie flags.** Cookies are `HttpOnly` and `SameSite=Lax`, and `Secure` whenever tilegate is served over https.
- **No cross-site requests.** Writes (favourites, theme) are only accepted from tilegate's own pages, which blocks CSRF.

## The page

- **Strict Content-Security-Policy:** no inline scripts or styles, no external stylesheets or fonts, and the page can't be framed. Images may load from any `https:` origin, which is what icon URLs need.
- **The apps file is validated:** only http(s) app URLs are accepted, unknown keys are rejected, and theme colours can't escape the stylesheet.

## The container

- **Minimal image.** Distroless, with no shell or package manager.
- **Locked down at runtime.** Runs as a non-root user on a read-only root filesystem, with all Linux capabilities dropped.
- **Default user** is UID/GID 65532, but any UID works:
  - Helm: set `podSecurityContext.runAsUser`, `runAsGroup` and `fsGroup`.
  - Compose: `user: "1000:1000"`. With a custom UID in Docker, mount `/data` from a folder owned by that user rather than a named volume.
- **Releases** come with an SBOM and build provenance.

## Verifying releases

Every image and chart is signed with [cosign](https://docs.sigstore.dev/) using "keyless" signing. The signature is tied to the GitHub Actions identity of this repository's release workflow, and recorded in Sigstore's public transparency log. There is no long-lived signing key that could leak. To check that something you're about to run was built by tilegate's own release workflow from a release tag:

```sh
VERSION=0.1.2
cosign verify ghcr.io/chillincool/tilegate:$VERSION \
  --certificate-identity-regexp '^https://github.com/chillincool/tilegate/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

Use `ghcr.io/chillincool/charts/tilegate:$VERSION` to verify the Helm chart the same way. The chart also ships a values schema, so Helm rejects misspelt or invalid settings at install time.
