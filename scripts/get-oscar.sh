#!/bin/sh
# Fetch and build open-oscar-server at the pinned commit into .cache/.
set -eu
PIN=0de81537ed9cd953b0181d1d059f214de9fad25b
DIR="$(cd "$(dirname "$0")/.." && pwd)/.cache/open-oscar-server"
if [ ! -d "$DIR/.git" ]; then
  git clone https://github.com/mk6i/open-oscar-server "$DIR"
fi
git -C "$DIR" fetch -q origin "$PIN" 2>/dev/null || true
git -C "$DIR" checkout -q "$PIN"
CGO_ENABLED=0 go -C "$DIR" build -o "$DIR/../oscar-server" ./cmd/server
echo "built: $DIR/../oscar-server @ $PIN"
