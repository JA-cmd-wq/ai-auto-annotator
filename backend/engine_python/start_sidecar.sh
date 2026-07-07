#!/usr/bin/env bash
# Start the LocateAnything-3B Python sidecar (model stays resident on the GPU).
# Loads shared env from backend/.env if present (e.g. LA_MODEL_PATH overrides).
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
BACKEND_DIR="$(cd "$HERE/.." && pwd)"

if [ -f "$BACKEND_DIR/.env" ]; then
  set -a
  . "$BACKEND_DIR/.env"
  set +a
fi

: "${LA_MODEL_PATH:=/mnt/LocateAnything-3B}"
: "${LA_HOST:=127.0.0.1}"
: "${LA_PORT:=8001}"

cd "$HERE"
exec python3 server.py
