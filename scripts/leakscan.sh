#!/usr/bin/env bash
# Scan files for strings that must not ship: the denylist names them. The
# denylist lives outside the repository, because a list of the strings to hunt
# would publish them itself.
#
#   scripts/leakscan.sh [-a ALLOWLIST] DENYLIST PATH...
#
# DENYLIST holds one extended regular expression per line, matched without
# regard to case. PATH is a directory (text files are searched, binary files by
# their printable strings, image and video metadata with exiftool when it is
# installed) or a single file such as a built binary. ALLOWLIST holds regular
# expressions matched against "path:line:text"; a hit that matches one has been
# reviewed and is not reported. Exit status 1 when a hit is left.
set -euo pipefail

allow=""
if [[ ${1:-} == -a ]]; then
  allow=${2:?-a needs a file}
  shift 2
fi
[[ $# -ge 2 ]] || { sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//' >&2; exit 2; }
deny=$1
shift
[[ -s $deny ]] || { echo "leakscan: $deny is missing or empty" >&2; exit 2; }

pattern=$(grep -v '^[[:space:]]*\(#\|$\)' "$deny" | paste -sd'|' -)
tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT

# Hits are printed as path:line:text with the path relative to the directory
# scanned, which is what an allowlist line is written against.
scan_strings() { # file
  strings -a "$1" 2>/dev/null | grep -iE -n -- "$pattern" | sed "s|^|$1:|" || true
}

scan_dir() { # dir
  (
    cd "$1"
    # Text files, with line numbers.
    grep -rIiEn --exclude-dir=.git -- "$pattern" . | sed 's|^\./||' || true
    # Binary files (images, videos, executables): their printable strings.
    grep -rIL --exclude-dir=.git -- '' . 2>/dev/null | sed 's|^\./||' | while IFS= read -r f; do
      scan_strings "$f"
    done
    # Metadata of recordings and images, where the tool is there.
    if command -v exiftool >/dev/null; then
      find . -type f \( -name '*.gif' -o -name '*.png' -o -name '*.jpg' -o -name '*.jpeg' -o -name '*.mp4' -o -name '*.avif' \) |
        sed 's|^\./||' | while IFS= read -r f; do
        exiftool -a -G1 "$f" 2>/dev/null | grep -iE -- "$pattern" | sed "s|^|$f:meta:|" || true
      done
    fi
  )
}

for target in "$@"; do
  if [[ -f $target ]]; then
    scan_strings "$target" >>"$tmp"
  else
    scan_dir "$target" >>"$tmp"
  fi
done

if [[ -n $allow && -s $allow ]]; then
  grep -v -E -f <(grep -v '^[[:space:]]*\(#\|$\)' "$allow") "$tmp" >"$tmp.left" || true
  mv "$tmp.left" "$tmp"
fi

if [[ -s $tmp ]]; then
  cat "$tmp"
  echo "leakscan: $(wc -l <"$tmp" | tr -d ' ') hit(s) left" >&2
  exit 1
fi
echo "leakscan: clean" >&2
