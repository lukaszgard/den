#!/bin/sh
# Stands in for an editor in config-reload.tape: each time den opens it, it
# makes the next of three edits to the file, shows the file for a moment, and
# returns, as an editor would once closed.
f="$1"
step="$(cat "$f.step" 2>/dev/null || echo 0)"
echo $((step + 1)) > "$f.step"
case "$step" in
0)
	what="a tunnel without its command, and a misspelt key"
	printf '  - name: jump\n    reconect: true\n' >> "$f" ;;
1)
	what="the fix"
	sed -i.bak 's/    reconect: true/    command: ssh -N jump.example.com/' "$f" ;;
2)
	what="runbooks added to menu_order"
	sed -i.bak 's/\[connect, tunnel, config/[connect, tunnel, runbooks, config/' "$f" ;;
esac
clear
printf '\033[1m  your editor — %s\033[0m\n\n' "$what"
sed -n '7,$p' "$f" | sed 's/^/  /'
sleep 2
