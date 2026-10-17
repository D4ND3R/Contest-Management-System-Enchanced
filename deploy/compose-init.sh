#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
#
# Prepares the production Compose stack (deploy/docker-compose.yml) ONCE:
# deploy/.env from env.example, and deploy/secrets/ with the secret_key, the
# PostgreSQL and Valkey passwords, the URLs that contain them and the token
# of the judge. Existing files are never overwritten, so it is safe to run
# again; nothing secret is printed.
#
#   deploy/compose-init.sh && docker compose -f deploy/docker-compose.yml up -d
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
S="$DIR/secrets"

[[ -f "$DIR/.env" ]] || { cp "$DIR/env.example" "$DIR/.env"; echo "wrote deploy/.env (edit it)"; }
# shellcheck disable=SC1091
set -a; . "$DIR/.env"; set +a

install -d -m 0700 "$S"
# The files are 0644 inside a 0700 directory: only the owner of the host
# directory reaches them, and the non-root users of the containers can
# read their bind-mounted copy.
gen() { # name, generated once
    [[ -s "$S/$1" ]] && return
    head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n' >"$S/$1"
    chmod 0644 "$S/$1"
    echo "generated deploy/secrets/$1"
}
gen secret_key
gen postgres_password
gen valkey_password
derive() { # name, content
    [[ -s "$S/$1" ]] && return
    printf '%s' "$2" >"$S/$1"
    chmod 0644 "$S/$1"
    echo "wrote deploy/secrets/$1"
}
derive database_url "postgres://cms:$(cat "$S/postgres_password")@postgres:5432/cms?sslmode=disable"
derive redis_url "redis://:$(cat "$S/valkey_password")@valkey:6379/0"
derive valkey.conf "requirepass $(cat "$S/valkey_password")
appendonly yes
appendfsync everysec
maxmemory-policy noeviction
"
# The judge gets the blob token (derived from secret_key), never the key.
if [[ ! -s "$S/blob_token" ]]; then
    docker run --rm -e CMS_SECRET_KEY_FILE=/s/secret_key -v "$S/secret_key:/s/secret_key:ro" \
        "${CMS_IMAGE:-ghcr.io/d4nd3r/cms}:${CMS_VERSION:-latest}" ctl blob-token >"$S/blob_token.tmp"
    mv "$S/blob_token.tmp" "$S/blob_token"
    chmod 0644 "$S/blob_token"
    echo "wrote deploy/secrets/blob_token"
fi

backups="${CMS_BACKUP_DIR:-./backups}"
[[ "$backups" = /* ]] || backups="$DIR/$backups"
if [[ ! -d "$backups" ]]; then
    install -d -m 0700 "$backups"
    # The containers run as uid 65532 (distroless nonroot).
    if [[ "$(id -u)" == 0 ]]; then chown 65532:65532 "$backups"; else
        chmod 0777 "$backups"
        echo "note: $backups is world-writable because this was not run as root (chown 65532 it and chmod 0700)"
    fi
fi
echo "ready: docker compose -f deploy/docker-compose.yml up -d"
