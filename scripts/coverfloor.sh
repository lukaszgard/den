#!/bin/sh
# coverfloor.sh <coverage.out> — print per-package statement coverage and fail
# when a package listed in FLOORS drops below its floor. The floors cover the
# packages whose regressions are invisible from the TUI (lifecycle, config,
# process supervision, self-update); raise them as tests are added, never lower
# them to make a change pass.
set -eu

profile=${1:?usage: coverfloor.sh coverage.out}

# package=floor%, space separated
FLOORS="\
github.com/lukaszgard/den/internal/agent=89 \
github.com/lukaszgard/den/internal/config=87 \
github.com/lukaszgard/den/internal/doctor=83 \
github.com/lukaszgard/den/internal/platform=73 \
github.com/lukaszgard/den/internal/prereq=90 \
github.com/lukaszgard/den/internal/process=83 \
github.com/lukaszgard/den/internal/runbook=84 \
github.com/lukaszgard/den/internal/runbook/source=75 \
github.com/lukaszgard/den/internal/secrets=85 \
github.com/lukaszgard/den/internal/secrets/sops=85 \
github.com/lukaszgard/den/internal/service=68 \
github.com/lukaszgard/den/internal/service/ssmfwd=70 \
github.com/lukaszgard/den/internal/termsession=85 \
github.com/lukaszgard/den/internal/tui=82 \
github.com/lukaszgard/den/internal/tui/applog=88 \
github.com/lukaszgard/den/internal/tui/components=75 \
github.com/lukaszgard/den/internal/tui/screens/applogs=90 \
github.com/lukaszgard/den/internal/tui/screens/configview=80 \
github.com/lukaszgard/den/internal/tui/screens/detail=85 \
github.com/lukaszgard/den/internal/tui/screens/help=95 \
github.com/lukaszgard/den/internal/tui/screens/secrets=79 \
github.com/lukaszgard/den/internal/tui/screens/updates=54 \
github.com/lukaszgard/den/internal/update=74"

awk -v floors="$FLOORS" '
BEGIN {
	n = split(floors, entries, " ")
	for (i = 1; i <= n; i++) {
		if (split(entries[i], f, "=") == 2) floor[f[1]] = f[2]
	}
}
NR == 1 { next } # mode: line
{
	split($1, loc, ":")
	pkg = loc[1]; sub(/\/[^\/]*$/, "", pkg)
	stmts[pkg] += $2
	if ($3 > 0) covered[pkg] += $2
}
END {
	status = 0
	for (p in stmts) {
		pct = stmts[p] ? 100 * covered[p] / stmts[p] : 0
		mark = ""
		if (p in floor) {
			mark = sprintf("(floor %d%%)", floor[p])
			if (pct < floor[p]) { mark = mark " BELOW FLOOR"; status = 1 }
		}
		printf "%-45s %5.1f%% %s\n", p, pct, mark | "sort"
	}
	for (p in floor) if (!(p in stmts)) { printf "%-45s  no coverage data (floor %d%%)\n", p, floor[p]; status = 1 }
	close("sort")
	exit status
}' "$profile"
