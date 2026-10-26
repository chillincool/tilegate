#!/usr/bin/env bash
# End-to-end tests in a throwaway kind cluster. Used by `mise run e2e*` and CI.
#
#   run.sh up     create the cluster and install CNPG, Envoy Gateway and Keycloak
#   run.sh test   build tilegate, (re)install both releases and run the tests
#   run.sh down   delete the cluster
#   run.sh all    up + test + down (down also runs if something fails;
#                 set E2E_KEEP=1 to keep the cluster for debugging)
set -euo pipefail

CLUSTER=tilegate-e2e
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
HERE=$ROOT/test/e2e
STATE=$ROOT/.e2e

# renovate: datasource=docker depName=docker.io/envoyproxy/gateway-helm
ENVOY_GATEWAY_VERSION=1.9.3
# renovate: datasource=helm depName=cloudnative-pg registryUrl=https://cloudnative-pg.github.io/charts
CNPG_CHART_VERSION=0.29.1
# renovate: datasource=docker depName=curlimages/curl
CURL_IMAGE=curlimages/curl:8.23.0

# Always use our own kubeconfig, whatever the caller's environment says, so
# nothing here can reach a real cluster.
export KUBECONFIG=$STATE/kubeconfig
mkdir -p "$STATE"

log() { printf '\n\033[1m==> %s\033[0m\n' "$*"; }

require_e2e_context() {
	local ctx
	ctx=$(kubectl config current-context 2>/dev/null || true)
	if [[ $ctx != "kind-$CLUSTER" ]]; then
		echo "refusing to continue: kubectl context is '$ctx', expected 'kind-$CLUSTER'" >&2
		exit 1
	fi
}

# A throwaway CA and a Keycloak certificate signed by it. Kept in .e2e so
# reruns of `up` reuse them.
make_keycloak_certs() {
	[[ -f $STATE/keycloak.crt ]] && return
	local host=keycloak.keycloak.svc.cluster.local
	openssl req -x509 -newkey rsa:2048 -nodes -days 30 -subj "/CN=tilegate e2e CA" \
		-keyout "$STATE/ca.key" -out "$STATE/ca.crt" 2> /dev/null
	openssl req -newkey rsa:2048 -nodes -subj "/CN=$host" \
		-keyout "$STATE/keycloak.key" -out "$STATE/keycloak.csr" 2> /dev/null
	printf 'subjectAltName=DNS:%s,DNS:keycloak.keycloak.svc,DNS:keycloak\n' "$host" > "$STATE/keycloak.ext"
	openssl x509 -req -in "$STATE/keycloak.csr" -CA "$STATE/ca.crt" -CAkey "$STATE/ca.key" -CAcreateserial \
		-days 30 -extfile "$STATE/keycloak.ext" -out "$STATE/keycloak.crt" 2> /dev/null
}

up() {
	if kind get clusters 2>/dev/null | grep -qx "$CLUSTER"; then
		log "Cluster $CLUSTER already exists"
		kind export kubeconfig --name "$CLUSTER" --kubeconfig "$KUBECONFIG" > /dev/null
	else
		log "Creating kind cluster $CLUSTER"
		kind create cluster --name "$CLUSTER" --kubeconfig "$KUBECONFIG" --wait 120s
	fi
	require_e2e_context

	log "Installing CloudNativePG operator"
	helm upgrade --install cnpg cloudnative-pg --repo https://cloudnative-pg.github.io/charts \
		--version "$CNPG_CHART_VERSION" -n cnpg-system --create-namespace --wait --timeout 5m

	log "Installing Envoy Gateway"
	helm upgrade --install eg oci://docker.io/envoyproxy/gateway-helm \
		--version "$ENVOY_GATEWAY_VERSION" -n envoy-gateway-system --create-namespace --wait --timeout 5m

	log "Installing Keycloak"
	make_keycloak_certs
	kubectl apply -f "$HERE/manifests/keycloak.yaml" > /dev/null
	kubectl -n keycloak create secret tls keycloak-tls --cert "$STATE/keycloak.crt" --key "$STATE/keycloak.key" \
		--dry-run=client -o yaml | kubectl apply -f - > /dev/null
	kubectl -n keycloak create configmap keycloak-ca --from-file=ca.crt="$STATE/ca.crt" \
		--dry-run=client -o yaml | kubectl apply -f - > /dev/null
	kubectl -n keycloak create configmap keycloak-realm --from-file=realm.json="$HERE/realm.json" \
		--dry-run=client -o yaml | kubectl apply -f - > /dev/null
	kubectl -n keycloak rollout status deploy/keycloak --timeout 5m
}

test_() {
	require_e2e_context
	local tag
	tag=e2e-$(date +%s)

	log "Building tilegate:$tag and loading it into kind"
	docker build -q -t "tilegate:$tag" "$ROOT" > /dev/null
	kind load docker-image "tilegate:$tag" --name "$CLUSTER" > /dev/null

	log "Setting up the gateway"
	kubectl create namespace tilegate --dry-run=client -o yaml | kubectl apply -f - > /dev/null
	if ! kubectl -n tilegate get secret apps-e2e-tls > /dev/null 2>&1; then
		openssl req -x509 -newkey rsa:2048 -nodes -days 30 -subj "/CN=apps.e2e.test" \
			-addext "subjectAltName=DNS:apps.e2e.test" \
			-keyout "$STATE/tls.key" -out "$STATE/tls.crt" 2> /dev/null
		kubectl -n tilegate create secret tls apps-e2e-tls --cert "$STATE/tls.crt" --key "$STATE/tls.key" > /dev/null
	fi

	log "Installing tilegate (oidc mode, 2 replicas, CloudNativePG)"
	helm upgrade --install tilegate-oidc "$ROOT/charts/tilegate" -n tilegate \
		-f "$HERE/values-common.yaml" -f "$HERE/values-oidc.yaml" --set image.tag="$tag" \
		--wait --timeout 6m

	log "Installing tilegate (proxy-jwt mode behind Envoy, SQLite)"
	helm upgrade --install tilegate-proxy "$ROOT/charts/tilegate" -n tilegate \
		-f "$HERE/values-common.yaml" -f "$HERE/values-proxy.yaml" --set image.tag="$tag" \
		--wait --timeout 4m
	kubectl apply -f "$HERE/manifests/gateway.yaml" > /dev/null

	log "Waiting for the database cluster and the gateway"
	kubectl -n tilegate wait cluster.postgresql.cnpg.io/tilegate-oidc-db --for=condition=Ready --timeout 5m
	kubectl -n tilegate wait gateway/e2e --for=condition=Programmed --timeout 3m
	kubectl -n tilegate wait securitypolicy/tilegate-proxy-auth --for=jsonpath='{.status.ancestors[0].conditions[?(@.type=="Accepted")].status}'=True --timeout 2m
	local envoy
	envoy=$(kubectl -n envoy-gateway-system get svc \
		-l gateway.envoyproxy.io/owning-gateway-name=e2e,gateway.envoyproxy.io/owning-gateway-namespace=tilegate \
		-o jsonpath='{.items[0].metadata.name}')

	log "Running tests"
	kubectl -n tilegate delete pod e2e-tests --ignore-not-found > /dev/null
	kubectl -n tilegate create configmap e2e-tests --from-file=tests.sh="$HERE/tests.sh" \
		--dry-run=client -o yaml | kubectl apply -f - > /dev/null
	kubectl -n tilegate run e2e-tests --restart=Never --image "$CURL_IMAGE" \
		--overrides '{"spec":{"containers":[{"name":"e2e-tests","image":"'"$CURL_IMAGE"'","command":["sh","/tests/tests.sh"],"env":[{"name":"GATEWAY_ADDR","value":"'"$envoy"'.envoy-gateway-system.svc.cluster.local"}],"volumeMounts":[{"name":"tests","mountPath":"/tests"}]}],"volumes":[{"name":"tests","configMap":{"name":"e2e-tests"}}]}}' \
		> /dev/null
	kubectl -n tilegate wait pod/e2e-tests --for=jsonpath='{.status.phase}'=Succeeded --timeout 5m > /dev/null 2>&1 || true
	kubectl -n tilegate logs e2e-tests
	local phase
	phase=$(kubectl -n tilegate get pod e2e-tests -o jsonpath='{.status.phase}')
	if [[ $phase != Succeeded ]]; then
		log "Tests failed (pod phase: $phase). tilegate logs:"
		kubectl -n tilegate logs -l app.kubernetes.io/instance=tilegate-oidc --tail 30 --prefix || true
		kubectl -n tilegate logs -l app.kubernetes.io/instance=tilegate-proxy --tail 30 --prefix || true
		return 1
	fi
}

down() {
	log "Deleting kind cluster $CLUSTER"
	kind delete cluster --name "$CLUSTER" --kubeconfig "$KUBECONFIG"
}

case ${1:-} in
up) up ;;
test) test_ ;;
down) down ;;
all)
	[[ ${E2E_KEEP:-} == 1 ]] || trap down EXIT
	up
	test_
	;;
*)
	echo "usage: $0 up|test|down|all" >&2
	exit 2
	;;
esac
