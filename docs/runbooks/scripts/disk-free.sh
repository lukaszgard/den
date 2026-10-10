#!/usr/bin/env bash
# den:name        Disk free
# den:description Free space where the script sits
set -euo pipefail
df -h .
