#!/bin/sh
# PostToolUse: gofmt the Go file Claude just edited or wrote.
f=$(jq -r '.tool_response.filePath // .tool_input.file_path // empty')
case "$f" in
*.go) [ -f "$f" ] && gofmt -w "$f" ;;
esac
exit 0
