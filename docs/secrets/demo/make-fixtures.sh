#!/usr/bin/env bash
# Regenerates the throwaway secrets that docs/secrets/secrets.tape records:
#
#   demo-age.key    an age key made for this demo; it decrypts the fake values below
#   demo.enc.yaml   fake values, encrypted to that key with sops
#   team.kdbx       a KeePassXC database with fake entries; the master password is "demo"
#
# Nothing here is a real secret: the key, the values and the password exist so that
# the recording needs no account and no setup. Run it from the repository root and
# commit the result:
#
#   docs/secrets/demo/make-fixtures.sh
#
# Needs sops and keepassxc-cli (the app's copy on macOS is found); age-keygen comes
# from PATH, else from `go run`.
set -euo pipefail

dir=docs/secrets/demo
password=demo

cli=$(command -v keepassxc-cli || true)
cli=${cli:-/Applications/KeePassXC.app/Contents/MacOS/keepassxc-cli}
[ -x "$cli" ] || { echo "keepassxc-cli not found" >&2; exit 1; }
command -v sops >/dev/null || { echo "sops not found" >&2; exit 1; }

keygen() {
	if command -v age-keygen >/dev/null; then age-keygen "$@"; else go run filippo.io/age/cmd/age-keygen@v1.2.1 "$@"; fi
}

# ── SOPS ──────────────────────────────────────────────────────────────────────
rm -f "$dir/demo-age.key" "$dir/demo.enc.yaml"
keygen -o "$dir/demo-age.key" 2>/dev/null
recipient=$(keygen -y "$dir/demo-age.key")

plain=$(mktemp)
trap 'rm -f "$plain"' EXIT
# A dotted key (client.orders) beside real nesting: the tape shows why the tree is the default.
cat >"$plain" <<'EOF'
db:
  user: app_rw
  password: correct-horse
api:
  token: demo-123
client.orders:
  basicauth.clientId: orders-app
EOF
sops --encrypt --age "$recipient" --input-type yaml --output-type yaml "$plain" >"$dir/demo.enc.yaml"

# ── KeePassXC ─────────────────────────────────────────────────────────────────
rm -f "$dir/team.kdbx"
# -t 200: unlock in a fraction of a second, so the recording does not wait on the key derivation.
printf '%s\n%s\n' "$password" "$password" | "$cli" db-create -q -p -t 200 "$dir/team.kdbx" >/dev/null 2>&1

for group in CI Databases Monitoring; do
	printf '%s\n' "$password" | "$cli" mkdir -q "$dir/team.kdbx" "$group" >/dev/null 2>&1
done
add() { # entry username url entry-password
	printf '%s\n%s\n' "$password" "$4" | "$cli" add -q -u "$2" --url "$3" -p "$dir/team.kdbx" "$1" >/dev/null 2>&1
}
add "CI/Jenkins" "ci-bot" "https://jenkins.example.com" "jenkins-demo-pw"
add "Databases/orders-e2e" "orders_rw" "postgres://db.example.com:5432/orders" "orders-demo-pw"
add "Monitoring/Grafana" "admin" "https://grafana.example.com" "grafana-demo-pw"

echo "wrote $dir/demo-age.key, $dir/demo.enc.yaml and $dir/team.kdbx"