#!/usr/bin/env bash
# Start the ai-auto-annotator Go backend, loading env from backend/.env.
# Assumes the LocateAnything-3B Python sidecar is already running on LA_PYTHON_URL
# (start it with: engine_python/start_sidecar.sh or `python3 engine_python/server.py`).
set -euo pipefail
cd "$(dirname "$0")"

# Load .env (gitignored) and export every var into the environment.
if [ -f .env ]; then
  set -a
  . ./.env
  set +a
fi

# Prefer a project-local binary if present, else the one built earlier in /tmp.
BIN="${ANNOTATOR_BIN:-./bin/annotator-server}"
if [ ! -x "$BIN" ]; then
  BIN="/tmp/annotator-server"
fi

exec "$BIN"
