#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
#
# Disposable PostgreSQL and Valkey for tests and development.
#
#   scripts/infra.sh up      start them (idempotent) and write .infra/env
#   scripts/infra.sh down    stop them and delete their data
#   scripts/infra.sh env     print the export lines (source it: eval "$(scripts/infra.sh env)")
#   scripts/infra.sh status
#
# CMS_INFRA=native|docker|auto (default auto: native binaries if installed,
# Docker otherwise). Ports: CMS_INFRA_PG_PORT (55432), CMS_INFRA_KV_PORT (56379).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIR="${CMS_INFRA_DIR:-$ROOT/.infra}"
MODE="${CMS_INFRA:-auto}"
PG_PORT="${CMS_INFRA_PG_PORT:-55432}"
KV_PORT="${CMS_INFRA_KV_PORT:-56379}"
PG_IMAGE="${CMS_INFRA_PG_IMAGE:-postgres:16}"
KV_IMAGE="${CMS_INFRA_KV_IMAGE:-valkey/valkey:8}"
DOCKER_PREFIX="cms-infra"

log() { echo "infra: $*" >&2; }
die() { echo "infra: error: $*" >&2; exit 1; }

find_pg_bin() {
    if command -v pg_ctl >/dev/null 2>&1 && command -v initdb >/dev/null 2>&1; then
        dirname "$(command -v pg_ctl)"
        return
    fi
    local d
    for d in $(ls -d /usr/lib/postgresql/*/bin 2>/dev/null | sort -V -r); do
        if [[ -x "$d/pg_ctl" && -x "$d/initdb" ]]; then
            echo "$d"
            return
        fi
    done
}

find_kv_bin() {
    command -v valkey-server 2>/dev/null || command -v redis-server 2>/dev/null || true
}

resolve_mode() {
    if [[ "$MODE" == "auto" ]]; then
        if [[ -n "$(find_pg_bin)" && -n "$(find_kv_bin)" ]]; then
            MODE=native
        elif command -v docker >/dev/null 2>&1; then
            MODE=docker
        else
            die "neither PostgreSQL+Valkey binaries nor Docker are installed"
        fi
    fi
}

# PostgreSQL refuses to run as root: use an unprivileged account then.
as_pg_user() {
    if [[ "$(id -u)" == "0" ]]; then
        runuser -u nobody -- "$@"
    else
        "$@"
    fi
}

write_env() {
    mkdir -p "$DIR"
    cat >"$DIR/env" <<EOF
export CMS_TEST_DATABASE_URL="postgres://postgres@127.0.0.1:${PG_PORT}/postgres?sslmode=disable"
export CMS_TEST_REDIS_URL="redis://127.0.0.1:${KV_PORT}/0"
EOF
}

wait_pg() {
    local i
    for i in $(seq 1 60); do
        if (exec 3<>"/dev/tcp/127.0.0.1/$PG_PORT") 2>/dev/null; then
            if [[ "$MODE" == "docker" ]]; then
                docker exec "$DOCKER_PREFIX-pg" pg_isready -U postgres >/dev/null 2>&1 && return 0
            else
                "$(find_pg_bin)/pg_isready" -h 127.0.0.1 -p "$PG_PORT" >/dev/null 2>&1 && return 0
            fi
        fi
        sleep 0.5
    done
    die "PostgreSQL did not start on port $PG_PORT"
}

wait_kv() {
    local i
    for i in $(seq 1 40); do
        if (exec 3<>"/dev/tcp/127.0.0.1/$KV_PORT") 2>/dev/null; then return 0; fi
        sleep 0.25
    done
    die "Valkey did not start on port $KV_PORT"
}

native_up() {
    local pgbin kvbin
    pgbin="$(find_pg_bin)"
    kvbin="$(find_kv_bin)"
    [[ -n "$pgbin" ]] || die "PostgreSQL binaries (initdb, pg_ctl) not found"
    [[ -n "$kvbin" ]] || die "valkey-server or redis-server not found"
    mkdir -p "$DIR"
    chmod 755 "$DIR"

    if ! as_pg_user "$pgbin/pg_ctl" -D "$DIR/pg" status >/dev/null 2>&1; then
        if [[ ! -f "$DIR/pg/PG_VERSION" ]]; then
            log "initialising PostgreSQL in $DIR/pg"
            mkdir -p "$DIR/pg" "$DIR/pgrun"
            [[ "$(id -u)" == "0" ]] && chown nobody "$DIR/pg" "$DIR/pgrun"
            chmod 700 "$DIR/pg"
            # The linguistic collation of real installations (and of CI, en_US.utf8)
            # when the locale exists, so tests catch ORDER BY that assume byte
            # order; otherwise the C locale.
            local loc=C.UTF-8
            if locale -a 2>/dev/null | grep -qi '^en_US\.utf-\?8$'; then loc=en_US.UTF-8; fi
            as_pg_user "$pgbin/initdb" -D "$DIR/pg" -U postgres -A trust -E UTF8 --locale="$loc" >/dev/null
        fi
        log "starting PostgreSQL on 127.0.0.1:$PG_PORT"
        # Durability is useless for disposable test data; speed matters.
        as_pg_user "$pgbin/pg_ctl" -D "$DIR/pg" -l "$DIR/pgrun/pg.log" -w -o \
            "-p $PG_PORT -k $DIR/pgrun -c listen_addresses=127.0.0.1 -c fsync=off -c synchronous_commit=off -c full_page_writes=off -c max_connections=300" \
            start >/dev/null
    fi
    wait_pg

    if [[ ! -f "$DIR/kv.pid" ]] || ! kill -0 "$(cat "$DIR/kv.pid")" 2>/dev/null; then
        log "starting $(basename "$kvbin") on 127.0.0.1:$KV_PORT"
        mkdir -p "$DIR/kv"
        "$kvbin" --port "$KV_PORT" --bind 127.0.0.1 --save '' --appendonly no \
            --dir "$DIR/kv" --daemonize yes --pidfile "$DIR/kv.pid" --logfile "$DIR/kv.log"
    fi
    wait_kv
}

native_down() {
    local pgbin
    pgbin="$(find_pg_bin)"
    if [[ -n "$pgbin" && -d "$DIR/pg" ]]; then
        as_pg_user "$pgbin/pg_ctl" -D "$DIR/pg" -m immediate stop >/dev/null 2>&1 || true
    fi
    if [[ -f "$DIR/kv.pid" ]]; then
        kill "$(cat "$DIR/kv.pid")" 2>/dev/null || true
    fi
}

docker_up() {
    if [[ -z "$(docker ps -q -f "name=^$DOCKER_PREFIX-pg$")" ]]; then
        docker rm -f "$DOCKER_PREFIX-pg" >/dev/null 2>&1 || true
        log "starting $PG_IMAGE on 127.0.0.1:$PG_PORT"
        docker run -d --name "$DOCKER_PREFIX-pg" -p "127.0.0.1:$PG_PORT:5432" \
            -e POSTGRES_HOST_AUTH_METHOD=trust --tmpfs /var/lib/postgresql/data \
            "$PG_IMAGE" -c fsync=off -c synchronous_commit=off -c full_page_writes=off -c max_connections=300 >/dev/null
    fi
    if [[ -z "$(docker ps -q -f "name=^$DOCKER_PREFIX-kv$")" ]]; then
        docker rm -f "$DOCKER_PREFIX-kv" >/dev/null 2>&1 || true
        log "starting $KV_IMAGE on 127.0.0.1:$KV_PORT"
        docker run -d --name "$DOCKER_PREFIX-kv" -p "127.0.0.1:$KV_PORT:6379" "$KV_IMAGE" \
            --save '' --appendonly no >/dev/null
    fi
    wait_pg
    wait_kv
}

docker_down() {
    docker rm -f "$DOCKER_PREFIX-pg" "$DOCKER_PREFIX-kv" >/dev/null 2>&1 || true
}

cmd="${1:-up}"
case "$cmd" in
up)
    resolve_mode
    echo "$MODE" >"$( mkdir -p "$DIR"; echo "$DIR/mode")"
    "${MODE}_up"
    write_env
    log "ready ($MODE): source $DIR/env"
    ;;
down)
    if [[ -f "$DIR/mode" ]]; then MODE="$(cat "$DIR/mode")"; else resolve_mode; fi
    "${MODE}_down"
    rm -rf "$DIR"
    log "stopped"
    ;;
env)
    [[ -f "$DIR/env" ]] || die "not running: use 'scripts/infra.sh up'"
    cat "$DIR/env"
    ;;
status)
    if [[ -f "$DIR/mode" ]]; then
        echo "mode: $(cat "$DIR/mode")"
        (exec 3<>"/dev/tcp/127.0.0.1/$PG_PORT") 2>/dev/null && echo "postgres: up ($PG_PORT)" || echo "postgres: down"
        (exec 3<>"/dev/tcp/127.0.0.1/$KV_PORT") 2>/dev/null && echo "valkey: up ($KV_PORT)" || echo "valkey: down"
    else
        echo "not running"
    fi
    ;;
*)
    die "usage: $0 up|down|env|status"
    ;;
esac
