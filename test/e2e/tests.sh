#!/bin/sh
# End-to-end tests. Runs inside the kind cluster (in a curl container) so it
# reaches Keycloak, Envoy and tilegate at the same addresses they use for each
# other. Signs in through real login forms, like a browser would.
set -u

OIDC=http://tilegate-oidc.tilegate.svc.cluster.local:8080
PROXY_SVC=http://tilegate-proxy.tilegate.svc.cluster.local:8080
APPS=https://apps.e2e.test
# Send apps.e2e.test to the Envoy service.
ENVOY="--connect-to apps.e2e.test:443:${GATEWAY_ADDR}:443"

failures=0
pass() { echo "ok   - $1"; }
fail() { echo "FAIL - $1"; failures=$((failures + 1)); }
check() {
	desc=$1
	shift
	if "$@"; then pass "$desc"; else fail "$desc"; fi
}
eq() {
	[ "$1" = "$2" ] && return 0
	echo "       got:  $1" >&2
	echo "       want: $2" >&2
	return 1
}
contains() { printf '%s' "$1" | grep -q -- "$2"; }

# -k: Keycloak and the gateway use certificates from a throwaway test CA.
c() { curl -sS -k --max-time 30 "$@"; }
code() { c -o /dev/null -w '%{http_code}' "$@"; }

# login JAR START_URL USER [curl args...]: follows redirects to the Keycloak
# form, submits it (password = username) and follows the redirects back.
login() {
	jar=$1 start=$2 user=$3
	shift 3
	rm -f "$jar"
	page=$(c "$@" -c "$jar" -b "$jar" -L "$start") || return 1
	action=$(printf '%s' "$page" | grep -o 'action="[^"]*"' | head -1 | sed 's/^action="//; s/"$//; s/&amp;/\&/g')
	if [ -z "$action" ]; then
		echo "       no login form reached from $start" >&2
		return 1
	fi
	c "$@" -c "$jar" -b "$jar" -L -o /dev/null --data-urlencode "username=$user" --data-urlencode "password=$user" "$action"
}

# App ids from /api/apps JSON, sorted and space-separated.
ids() { grep -o '"id":"[^"]*"' | sed 's/"id":"//; s/"$//' | sort | tr '\n' ' ' | sed 's/ $//'; }
# Ids from /api/favorites, which is a JSON array of strings, in order.
favs() { tr -d '[]" \n' | tr ',' ' '; }

expected() {
	case $1 in
	alice) echo "grafana jellyfin wiki" ;;
	bob) echo "jellyfin photos wiki" ;;
	carol) echo "wiki" ;;
	esac
}

echo "# Built-in OIDC login (2 replicas, favourites in CloudNativePG)"

check "anonymous page redirects to login" eq "$(code "$OIDC/")" 302
check "anonymous API gets 401" eq "$(code "$OIDC/api/apps")" 401

for u in alice bob carol; do
	if login /tmp/$u "$OIDC/" $u; then
		check "$u sees only their apps" eq "$(c -b /tmp/$u "$OIDC/api/apps" | ids)" "$(expected $u)"
	else
		fail "$u can sign in"
	fi
done

page=$(c -b /tmp/alice "$OIDC/")
check "page is rendered with the configured title" contains "$page" "E2E Apps"
check "loginURL is used as the tile link" contains "$page" 'href="https://grafana.e2e.test/login/generic_oauth"'
check "hidden apps are not in bob's page at all" eq "$(c -b /tmp/bob "$OIDC/" | grep -c grafana.e2e.test)" 0

check "bob can favourite an app he can see" eq "$(code -b /tmp/bob -X PUT "$OIDC/api/favorites/jellyfin")" 204
check "bob cannot favourite an app he can't see" eq "$(code -b /tmp/bob -X PUT "$OIDC/api/favorites/grafana")" 404
check "cross-site favourite request is refused" eq "$(code -b /tmp/bob -X PUT -H 'Sec-Fetch-Site: cross-site' "$OIDC/api/favorites/photos")" 403
consistent=true
for _ in 1 2 3 4 5 6; do
	# Several requests, so both replicas answer at least once.
	[ "$(c -b /tmp/bob "$OIDC/api/favorites" | favs)" = "jellyfin" ] || consistent=false
done
check "favourites are the same on every replica" $consistent

check "bob can choose dark mode" eq "$(code -b /tmp/bob -X PUT "$OIDC/api/theme/dark")" 204
login /tmp/bob2 "$OIDC/" bob
check "theme choice follows bob to a new session (from the database)" contains "$(c -b /tmp/bob2 "$OIDC/")" 'data-theme="dark"'
check "favourites follow bob to a new session" eq "$(c -b /tmp/bob2 "$OIDC/api/favorites" | favs)" "jellyfin"

loc=$(c -b /tmp/alice -c /tmp/alice -o /dev/null -w '%{redirect_url}' "$OIDC/logout")
check "sign-out goes to Keycloak's logout" contains "$loc" "/protocol/openid-connect/logout"
check "after sign-out alice must log in again" eq "$(code -b /tmp/alice "$OIDC/api/apps")" 401

echo
echo "# Behind Envoy Gateway (proxy-jwt, favourites in SQLite)"

check "bypassing Envoy without a token gets 401" eq "$(code "$PROXY_SVC/api/apps")" 401
check "a forged token is rejected" eq "$(code -H 'Authorization: Bearer eyJhbGciOiJub25lIn0.eyJzdWIiOiJ4IiwiZ3JvdXBzIjpbImFkbWlucyJdfQ.' "$PROXY_SVC/api/apps")" 401

for u in alice bob carol; do
	# shellcheck disable=SC2086
	if login /tmp/p-$u "$APPS/" $u $ENVOY; then
		# shellcheck disable=SC2086
		check "$u sees only their apps through Envoy" eq "$(c $ENVOY -b /tmp/p-$u "$APPS/api/apps" | ids)" "$(expected $u)"
	else
		fail "$u can sign in through Envoy"
	fi
done
# shellcheck disable=SC2086
check "alice can favourite through Envoy" eq "$(code $ENVOY -b /tmp/p-alice -X PUT "$APPS/api/favorites/grafana")" 204
# shellcheck disable=SC2086
check "alice's favourite is saved" eq "$(c $ENVOY -b /tmp/p-alice "$APPS/api/favorites" | favs)" "grafana"
# shellcheck disable=SC2086
check "the sign-out link points at Envoy's logout" contains "$(c $ENVOY -b /tmp/p-alice "$APPS/")" 'href="/logout"'

echo
if [ "$failures" -gt 0 ]; then
	echo "$failures check(s) failed"
	exit 1
fi
echo "all checks passed"
