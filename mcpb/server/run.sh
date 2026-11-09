#!/bin/sh
# Выбор бинарника под платформу (macOS / Linux). На Windows manifest запускает .exe напрямую.
DIR="$(cd "$(dirname "$0")" && pwd)"
case "$(uname -s)-$(uname -m)" in
  Darwin-arm64)  BIN="$DIR/atl-mcp-darwin-arm64" ;;
  Darwin-*)      BIN="$DIR/atl-mcp-darwin-amd64" ;;
  Linux-x86_64)  BIN="$DIR/atl-mcp-linux-amd64" ;;
  *) echo "atl-mcp: платформа $(uname -s)-$(uname -m) не поддерживается" >&2; exit 1 ;;
esac
chmod +x "$BIN" 2>/dev/null
if [ "$(uname -s)" = "Darwin" ]; then
  xattr -d com.apple.quarantine "$BIN" 2>/dev/null
fi
exec "$BIN" "$@"
