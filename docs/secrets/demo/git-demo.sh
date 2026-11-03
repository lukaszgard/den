#!/usr/bin/env bash
# Makes the git working copies that docs/secrets/secrets-git.tape records:
#
#   docs/secrets/demo/git-demo.sh
#
# In /tmp/den-secrets-git: remote.git, a bare repository standing for a team's infra
# repository; infra, your clone of it, which den follows; and teammate, a clone in
# which a teammate rotates the database password of the secret and pushes. Your
# clone has not fetched since, so den first shows the file up to date with what it
# knows, and f finds the newer version. The secret is docs/secrets/demo/demo.enc.yaml
# (fake values, encrypted to the demo age key beside it). git runs here without
# your global config, so your signing and hooks stay out of it. Run it from the
# repository root; it starts afresh every time.
#
# Needs git and sops.
set -euo pipefail

src=$PWD/docs/secrets/demo
dir=${DEN_SECRETS_GIT_DIR:-/tmp/den-secrets-git}
export SOPS_AGE_KEY_FILE=$src/demo-age.key GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
command -v sops >/dev/null || { echo "sops not found" >&2; exit 1; }
g() { git -c user.name=Anna -c user.email=anna@example.com "$@"; }

rm -rf "$dir"
mkdir -p "$dir"
g init -q --bare -b main "$dir/remote.git"
g clone -q "$dir/remote.git" "$dir/teammate" 2>/dev/null
mkdir -p "$dir/teammate/secrets"
cp "$src/demo.enc.yaml" "$dir/teammate/secrets/payments-dev.enc.yaml"
echo "# infra" >"$dir/teammate/README.md"
g -C "$dir/teammate" add -A
g -C "$dir/teammate" commit -q -m "add the payments dev secrets"
g -C "$dir/teammate" push -q origin main
g clone -q "$dir/remote.git" "$dir/infra"

# The teammate rotates the password and pushes; your clone has not fetched since.
sops set "$dir/teammate/secrets/payments-dev.enc.yaml" '["db"]["password"]' '"battery-staple"'
g -C "$dir/teammate" commit -q -am "rotate the payments db password"
g -C "$dir/teammate" push -q origin main
echo "your clone is $dir/infra, one commit behind origin/main"
