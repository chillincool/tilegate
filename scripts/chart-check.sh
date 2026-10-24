#!/usr/bin/env bash
# Lints the chart and renders it with every auth mode and database option,
# including combinations that must be rejected.
set -euo pipefail
chart=charts/tilegate
oidc=(--set auth.oidc.issuer=https://idp --set auth.oidc.redirectURL=https://apps/auth/callback)

render() { helm template t "$chart" "$@" > /dev/null; }
must_fail() {
  if helm template t "$chart" "$@" > /dev/null 2>&1; then
    echo "expected the chart to refuse: $*" >&2
    exit 1
  fi
}

scripts/values-schema.py --check
helm lint "$chart" "${oidc[@]}"
render "${oidc[@]}"
render --set auth.mode=proxy-jwt --set auth.proxyJWT.issuer=https://idp
render --set auth.mode=proxy-header --set auth.trustedProxies=10.0.0.0/8
render --set auth.mode=dev --set persistence.enabled=true
render --set auth.mode=dev --set database.existingSecret=pg --set replicaCount=2
render --set auth.mode=dev --set database.cnpg.enabled=true --set database.cnpg.scheduledBackup.enabled=true --set replicaCount=2
render --set auth.mode=dev --set database.postgres.host=pg --set database.postgres.passwordSecret.name=pg
render --set auth.mode=dev --set route.enabled=true --set 'route.parentRefs[0].name=gw'
must_fail --set auth.mode=dev --set persistence.enabled=true --set replicaCount=2
must_fail --set auth.mode=dev --set database.postgres.host=pg
must_fail --set auth.oidc.redirectURL=https://apps/auth/callback   # oidc mode without an issuer
# values.schema.json catches typos and bad values at install time
must_fail "${oidc[@]}" --set auht.mode=oidc
must_fail "${oidc[@]}" --set auth.mode=sso
must_fail "${oidc[@]}" --set 'config.apps[0].name=A' --set 'config.apps[0].url=https://a' --set 'config.apps[0].role=admins'
must_fail "${oidc[@]}" --set database.postgres.port=99999
# the e2e values files must also pass the schema
render -f test/e2e/values-common.yaml -f test/e2e/values-oidc.yaml
render -f test/e2e/values-common.yaml -f test/e2e/values-proxy.yaml
echo "chart OK"
