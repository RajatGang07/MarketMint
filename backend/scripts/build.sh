#!/usr/bin/env bash
# Build the single self-contained binary: React UI embedded in the Go server.
# Usage: backend/scripts/build.sh   (from anywhere)
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

echo "==> building frontend"
(cd "$ROOT/frontend" && npm install --no-audit --no-fund && npm run build)

echo "==> embedding UI into the Go binary"
rm -rf "$ROOT/backend/internal/web/dist"
cp -R "$ROOT/frontend/dist" "$ROOT/backend/internal/web/dist"

echo "==> building server"
(cd "$ROOT/backend" && go build -o bin/server ./cmd/server)

echo "==> done: backend/bin/server (serves API + dashboard on \$PORT)"
