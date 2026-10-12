#!/usr/bin/env bash
# Starts (or stops) the throwaway Vault server that docs/secrets/secrets.tape reads:
#
#   docs/secrets/demo/vault-demo.sh start    # dev server on 127.0.0.1:18200, seeded
#   docs/secrets/demo/vault-demo.sh stop
#
# A dev server keeps everything in memory and starts with the KV v2 engine at
# secret/, so there is nothing to commit and nothing left behind: `stop` ends it.
# The root token is "demo" (the tape exports it as VAULT_TOKEN, as a person's
# `vault login` would leave one) and the values are fake. The port is not 8200, so a
# Vault of your own on the default one is left alone.
#
# Needs the vault CLI (brew install hashicorp/tap/vault).
set -euo pipefail

addr=http://127.0.0.1:18200
state=${DEN_SECRETS_DEMO_DIR:-/tmp/den-secrets-demo}

stop() {
	if [ -f "$state/vault.pid" ]; then
		kill "$(cat "$state/vault.pid")" 2>/dev/null || true
		rm -f "$state/vault.pid"
	fi
}

start() {
	command -v vault >/dev/null || { echo "vault not found" >&2; exit 1; }
	mkdir -p "$state"
	stop
	vault server -dev -dev-root-token-id=demo -dev-listen-address=127.0.0.1:18200 \
		>"$state/vault.log" 2>&1 &
	echo $! >"$state/vault.pid"

	export VAULT_ADDR=$addr VAULT_TOKEN=demo
	for _ in $(seq 1 50); do
		vault status >/dev/null 2>&1 && break
		sleep 0.2
	done
	vault status >/dev/null 2>&1 || { echo "vault dev server did not start, see $state/vault.log" >&2; exit 1; }

	# One secret to open, and a folder with subfolders to pick from.
	vault kv put -mount=secret grafana/admin user=admin password=grafana-demo-pw url=https://grafana.example.com >/dev/null
	vault kv put -mount=secret apps/ams/kafka/e2e bootstrap=kafka.example.com:9094 username=ams_e2e password=kafka-demo-pw >/dev/null
	vault kv put -mount=secret apps/ams/kafka/prod bootstrap=kafka.example.com:9094 username=ams_prod password=kafka-prod-demo-pw >/dev/null
	vault kv put -mount=secret apps/ams/orders/db user=orders_rw password=orders-demo-pw >/dev/null
	echo "vault dev server on $addr, token demo"
}

case "${1:-}" in
start) start ;;
stop) stop ;;
*) echo "usage: $0 start|stop" >&2; exit 2 ;;
esac
