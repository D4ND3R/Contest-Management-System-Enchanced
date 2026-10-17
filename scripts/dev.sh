#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
#
# Full stack with native binaries, for development and field reviews.
#
#   scripts/dev.sh start    infra + migrations + every service in background
#   scripts/dev.sh stop     stop the services (the infra keeps running)
#   scripts/dev.sh status
#   scripts/dev.sh logs [service]
#
# State lives in .dev/: cms.yaml (written once, with a generated
# secret_key), logs/ and pids/. Services are stopped by their recorded PIDs,
# never with `pkill -f` (a pattern that also matches the calling shell
# kills it).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEV="$ROOT/.dev"
BIN="$ROOT/bin/cms"
SERVICES=(contest-web admin-web ranking-web dispatcher worker monitor)

log() { echo "dev: $*" >&2; }

write_config() {
    mkdir -p "$DEV"
    [[ -f "$DEV/cms.yaml" ]] && return
    local secret
    secret="$("$BIN" ctl gen-secret)"
    source "$ROOT/.infra/env"
    local kvurl="$CMS_TEST_REDIS_URL"
    local pgbase
    # postgres://user@host:port/postgres?sslmode=disable -> postgres://user@host:port
    pgbase="$(sed -E 's#/[^/?]*(\?.*)?$##' <<<"$CMS_TEST_DATABASE_URL")"
    umask 077
    cat >"$DEV/cms.yaml" <<YAML
# Development configuration written by scripts/dev.sh (not for production).
secret_key: "$secret"
data_dir: $DEV/data
timezone: UTC
log:
  level: debug
  format: text
database:
  url: $pgbase/cms_dev?sslmode=disable
redis:
  url: $kvurl
  key_prefix: "cmsdev:"
storage:
  local:
    path: $DEV/data/blobs
  cache_dir: $DEV/cache
ranking_web:
  data_dir: $DEV/data/ranking
dispatcher:
  # The local scoreboard (credentials derived from secret_key).
  rankings:
    - url: http://127.0.0.1:8890/
worker:
  work_dir: $DEV/worker
YAML
    log "wrote $DEV/cms.yaml"
}

create_db() {
    source "$ROOT/.infra/env"
    local admin="$CMS_TEST_DATABASE_URL"
    if command -v psql >/dev/null 2>&1; then
        psql "$admin" -tAc "SELECT 1 FROM pg_database WHERE datname='cms_dev'" | grep -q 1 ||
            psql "$admin" -qc "CREATE DATABASE cms_dev"
    else
        docker exec cms-infra-pg psql -U postgres -tAc "SELECT 1 FROM pg_database WHERE datname='cms_dev'" | grep -q 1 ||
            docker exec cms-infra-pg psql -U postgres -qc "CREATE DATABASE cms_dev"
    fi
}

is_running() {
    local pidfile="$DEV/pids/$1.pid"
    [[ -f "$pidfile" ]] && kill -0 "$(cat "$pidfile")" 2>/dev/null
}

start() {
    # Always rebuild: a stale bin/cms rejects settings added since (go
    # build is incremental and takes a second when nothing changed).
    log "building bin/cms"
    (cd "$ROOT" && make -s build) || { log "build failed"; exit 1; }
    "$ROOT/scripts/infra.sh" up
    write_config
    create_db
    export CMS_CONFIG="$DEV/cms.yaml"
    "$BIN" migrate
    mkdir -p "$DEV/logs" "$DEV/pids"
    for s in "${SERVICES[@]}"; do
        if is_running "$s"; then
            log "$s already running"
            continue
        fi
        nohup "$BIN" "$s" >"$DEV/logs/$s.log" 2>&1 &
        echo $! >"$DEV/pids/$s.pid"
        log "started $s (pid $!)"
    done
    sleep 1
    status
    cat >&2 <<MSG

  contestant site  http://127.0.0.1:8888
  administration   http://127.0.0.1:8889
  scoreboard       http://127.0.0.1:8890
  logs             scripts/dev.sh logs [service]
MSG
}

stop() {
    local s pid
    for s in "${SERVICES[@]}"; do
        if is_running "$s"; then
            pid="$(cat "$DEV/pids/$s.pid")"
            kill "$pid"
            for _ in $(seq 1 50); do kill -0 "$pid" 2>/dev/null || break; sleep 0.2; done
            kill -0 "$pid" 2>/dev/null && kill -9 "$pid"
            log "stopped $s"
        fi
        rm -f "$DEV/pids/$s.pid"
    done
}

status() {
    local s
    for s in "${SERVICES[@]}"; do
        if is_running "$s"; then
            printf '  %-12s running (pid %s)\n' "$s" "$(cat "$DEV/pids/$s.pid")"
        else
            printf '  %-12s stopped\n' "$s"
        fi
    done
}

case "${1:-start}" in
start) start ;;
stop) stop ;;
restart) stop; start ;;
status) status ;;
logs)
    if [[ -n "${2:-}" ]]; then tail -n 100 -f "$DEV/logs/$2.log"; else tail -n 20 -f "$DEV"/logs/*.log; fi
    ;;
*) echo "usage: $0 start|stop|restart|status|logs [service]" >&2; exit 2 ;;
esac
