#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
#
# `make dev`: the full stack with Docker Compose.
#
#   scripts/dev-compose.sh up      build bin/cms and the image, start everything
#   scripts/dev-compose.sh down    stop and delete the containers and volumes
#   scripts/dev-compose.sh ps|logs
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENVFILE="$ROOT/.dev/compose.env"
COMPOSE=(docker compose -f "$ROOT/deploy/docker-compose.dev.yml" --env-file "$ENVFILE")

ensure_env() {
    mkdir -p "$ROOT/.dev"
    if [[ ! -f "$ENVFILE" ]]; then
        # The secret is generated once and reused by every container.
        (umask 077; echo "CMS_SECRET_KEY=$("$ROOT/bin/cms" ctl gen-secret)" >"$ENVFILE")
        echo "dev-compose: wrote $ENVFILE" >&2
    fi
}

case "${1:-up}" in
up)
    make -C "$ROOT" build >/dev/null
    if [[ ! -f /sys/fs/cgroup/cgroup.controllers ]]; then
        # Without cgroups v2 isolate limits memory per process only.
        export CMS_SANDBOX_CGROUPS="${CMS_SANDBOX_CGROUPS:-false}"
        echo "dev-compose: no cgroups v2 on this host, worker runs with CMS_SANDBOX_CGROUPS=false" >&2
    fi
    ensure_env
    "${COMPOSE[@]}" up -d --build --wait
    "${COMPOSE[@]}" ps
    cat >&2 <<MSG

  contestant site  http://127.0.0.1:8888
  administration   http://127.0.0.1:8889
  scoreboard       http://127.0.0.1:8890
  monitor          http://127.0.0.1:9193/healthz
MSG
    ;;
down)
    ensure_env
    "${COMPOSE[@]}" down -v
    ;;
ps) ensure_env; "${COMPOSE[@]}" ps ;;
logs) ensure_env; shift; "${COMPOSE[@]}" logs -f "$@" ;;
*) echo "usage: $0 up|down|ps|logs" >&2; exit 2 ;;
esac
