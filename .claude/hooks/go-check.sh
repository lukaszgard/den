#!/bin/sh
# Stop: compile and vet before Claude finishes. A failure blocks the stop and
# hands the compiler output back to Claude; stop_hook_active prevents a loop
# when the fix attempt itself ends with a failing build.
input=$(cat)
[ "$(printf '%s' "$input" | jq -r '.stop_hook_active // false')" = "true" ] && exit 0
cd "${CLAUDE_PROJECT_DIR:-.}" || exit 0
out=$( { go build ./... && go vet ./...; } 2>&1 ) && exit 0
jq -n --arg out "$out" '{decision: "block", reason: ("go build / go vet failed — fix this before finishing:\n" + $out)}'
