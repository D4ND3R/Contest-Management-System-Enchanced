#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
#
# Installer of CMS (Go rewrite) for Ubuntu 22.04/24.04/26.04 and Debian 12 (R9.1).
#
#   curl -fsSL https://raw.githubusercontent.com/D4ND3R/Contest-Management-System-Enchanced/main/scripts/install.sh \
#     | sudo bash -s -- --domain example.org
#   ... | sudo bash -s -- --http-ports 8000,8001,8002
#   ... | sudo bash -s -- --worker-only --dispatcher http://10.0.0.1:8891 \
#           --blob-token-file /root/blob-token --valkey-url-file /root/valkey-url
#   ... | sudo bash -s -- --uninstall [--purge --yes]
#
# Run with --help for every option; docs/en/installation.md explains them.
#
# Re-running is safe: secrets are generated ONCE (/etc/cms/secrets), cms.yaml
# is written only if it does not exist, the first admin only if missing, and
# the cms binary is replaced only when --binary or --version is given (use
# `cmsctl upgrade` to upgrade). Units, the Caddy site, the firewall rules
# and /etc/cms/cms.env (listen addresses) are rewritten from the options.
# Secrets are never printed, except the generated admin password, once.
set -euo pipefail

REPO="${CMS_INSTALL_REPO:-D4ND3R/Contest-Management-System-Enchanced}"
ISOLATE_SRC_TAG="v2.7"

PREFIX=/usr/local/bin
ETC=/etc/cms
SECRETS=$ETC/secrets
DATA=/var/lib/cms
CACHE=/var/cache/cms
UNITDIR=/etc/systemd/system
SERVICES=(contest-web admin-web ranking-web dispatcher worker monitor)
BLOB_PORT=8891

# --- options -----------------------------------------------------------------

DOMAIN="" ADMIN_DOMAIN="" RANKING_DOMAIN="" ACME_EMAIL=""
HTTP_PORTS=""
VERSION="" BINARY=""
WORKER_ONLY=0 DISPATCHER_URL="" BLOB_TOKEN_FILE="" VALKEY_URL_FILE=""
ALLOW_JUDGES="" ADMIN_ALLOW=""
CONCURRENCY=1
ADMIN_USER=admin
FIREWALL=1 COMPILERS=all
BACKUP_KEEP=14 BACKUP_CALENDAR="*-*-* 03:17:00"
UNINSTALL=0 PURGE=0 YES=0 FORCE_OS=0

usage() {
    cat <<'EOF'
CMS installer (Ubuntu 22.04/24.04/26.04, Debian 12). Run as root.

Modes (one of):
  --domain NAME            HTTPS with Caddy: contest site at NAME, admin at
                           admin.NAME, scoreboard at ranking.NAME (DNS records
                           for the three names must point here)
  --http-ports C,A,R       plain HTTP on ports (contest, admin, ranking);
                           default 8000,8001,8002 when no --domain is given
  --worker-only            an extra judging machine (needs --dispatcher,
                           --blob-token-file and --valkey-url-file)
  --uninstall              remove services and binary; keeps data unless --purge

Options:
  --admin-domain NAME      admin site name (default admin.DOMAIN)
  --ranking-domain NAME    scoreboard name (default ranking.DOMAIN)
  --email ADDRESS          ACME account email for the certificates
  --version TAG            release to install (default: latest; an existing
                           binary is kept unless --version or --binary is given)
  --binary PATH            install this cms binary instead of downloading one
  --judge-concurrency N    parallel jobs of the local judge (default 1; at most
                           one per physical core reserved for judging)
  --allow-judges CIDR      judges on other machines (that network) may reach
                           Valkey (6379) and the blob server (8891)
  --admin-allow CIDR       only this network may open the admin site
  --admin-user NAME        username of the first admin (default admin)
  --compilers all|basic|none   languages of the judge (default all;
                           basic = C, C++, Python 3, Java)
  --no-firewall            do not touch ufw
  --backup-keep N          scheduled backups kept (default 14; 0 disables the timer)
  --backup-calendar SPEC   systemd OnCalendar of the backups (default "*-*-* 03:17:00")
  --dispatcher URL         (worker-only) blob server, e.g. http://10.0.0.1:8891
  --blob-token-file FILE   (worker-only) file with the output of `cmsctl blob-token`
  --valkey-url-file FILE   (worker-only) file with redis://:PASSWORD@HOST:6379/0
  --purge                  (uninstall) also delete the database, data and config
  --yes                    answer yes (needed by --purge when there is no terminal)
  --force-os               try on an unsupported distribution
EOF
}

die() { echo "install.sh: error: $*" >&2; exit 1; }
log() { echo "==> $*" >&2; }
warn() { echo "install.sh: WARNING: $*" >&2; }

while [[ $# -gt 0 ]]; do
    case "$1" in
    --domain) DOMAIN="${2:?}"; shift ;;
    --admin-domain) ADMIN_DOMAIN="${2:?}"; shift ;;
    --ranking-domain) RANKING_DOMAIN="${2:?}"; shift ;;
    --email) ACME_EMAIL="${2:?}"; shift ;;
    --http-ports) HTTP_PORTS="${2:?}"; shift ;;
    --version) VERSION="${2:?}"; shift ;;
    --binary) BINARY="${2:?}"; shift ;;
    --worker-only) WORKER_ONLY=1 ;;
    --dispatcher) DISPATCHER_URL="${2:?}"; shift ;;
    --blob-token-file) BLOB_TOKEN_FILE="${2:?}"; shift ;;
    --valkey-url-file) VALKEY_URL_FILE="${2:?}"; shift ;;
    --allow-judges) ALLOW_JUDGES="${2:?}"; shift ;;
    --admin-allow) ADMIN_ALLOW="${2:?}"; shift ;;
    --judge-concurrency) CONCURRENCY="${2:?}"; shift ;;
    --admin-user) ADMIN_USER="${2:?}"; shift ;;
    --compilers) COMPILERS="${2:?}"; shift ;;
    --no-firewall) FIREWALL=0 ;;
    --backup-keep) BACKUP_KEEP="${2:?}"; shift ;;
    --backup-calendar) BACKUP_CALENDAR="${2:?}"; shift ;;
    --uninstall) UNINSTALL=1 ;;
    --purge) PURGE=1 ;;
    --yes | -y) YES=1 ;;
    --force-os) FORCE_OS=1 ;;
    -h | --help) usage; exit 0 ;;
    *) usage >&2; die "unknown option $1" ;;
    esac
    shift
done

[[ "$(id -u)" == 0 ]] || die "run as root (curl ... | sudo bash -s -- OPTIONS)"
export DEBIAN_FRONTEND=noninteractive
umask 022

# --- helpers -----------------------------------------------------------------

cms() { "$PREFIX/cms" "$@"; }
# as_cms runs a cms command as the cms user (it owns the database and data).
as_cms() { runuser -u cms -- "$PREFIX/cms" "$@"; }

apt_install() {
    apt-get install -y --no-install-recommends "$@"
}

have_candidate() {
    local c
    c="$(apt-cache policy "$1" 2>/dev/null | awk '/Candidate:/ {print $2}')"
    [[ -n "$c" && "$c" != "(none)" ]]
}

# secret NAME prints the secret stored in $SECRETS/NAME, generating it once.
secret() {
    local f="$SECRETS/$1"
    if [[ ! -s "$f" ]]; then
        (umask 077; head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n' >"$f")
    fi
    cat "$f"
}

write_if_changed() { # path mode content-on-stdin
    local path="$1" mode="$2" tmp
    tmp="$(mktemp)"
    cat >"$tmp"
    if [[ -f "$path" ]] && cmp -s "$tmp" "$path"; then
        rm -f "$tmp"
        return 1
    fi
    install -m "$mode" "$tmp" "$path"
    rm -f "$tmp"
    return 0
}

read_os() {
    # Read os-release in a subshell: it defines VERSION, NAME, ID... and
    # must not overwrite the installer's own variables (--version).
    local os
    # shellcheck disable=SC1091
    os="$(. /etc/os-release && printf '%s\n%s\n%s\n' "${ID:-}" "${VERSION_ID:-}" "${VERSION_CODENAME:-}")"
    { read -r OS_ID; read -r OS_VERSION; read -r CODENAME; } <<<"$os"
    ARCH="$(dpkg --print-architecture)"
    case "$OS_ID:$OS_VERSION" in
    ubuntu:22.04 | ubuntu:24.04 | ubuntu:26.04 | debian:12) ;;
    *)
        [[ "$FORCE_OS" == 1 ]] || die "unsupported system $OS_ID $OS_VERSION (Ubuntu 22.04/24.04/26.04 or Debian 12; --force-os to try)"
        warn "unsupported system $OS_ID $OS_VERSION: continuing because of --force-os"
        ;;
    esac
    case "$ARCH" in
    amd64 | arm64) ;;
    *) die "unsupported architecture $ARCH (amd64 or arm64)" ;;
    esac
}

# --- uninstall ---------------------------------------------------------------

confirm() {
    [[ "$YES" == 1 ]] && return 0
    if [[ -r /dev/tty ]]; then
        local ans
        read -r -p "$1 [type yes] " ans </dev/tty
        [[ "$ans" == yes ]]
    else
        die "$1: add --yes (no terminal to ask)"
    fi
}

uninstall() {
    log "stopping and removing the cms services"
    local units=(cms.target cms-backup.timer cms-backup.service)
    for s in "${SERVICES[@]}"; do units+=("cms-$s.service"); done
    systemctl disable --now "${units[@]}" 2>/dev/null || true
    for u in "${units[@]}"; do rm -f "$UNITDIR/$u"; done
    systemctl daemon-reload
    rm -f "$PREFIX/cms" "$PREFIX/cms.prev" "$PREFIX/cmsctl"
    if [[ -f /etc/caddy/cms.caddy ]]; then
        rm -f /etc/caddy/cms.caddy
        sed -i '\#^import /etc/caddy/cms.caddy$#d' /etc/caddy/Caddyfile 2>/dev/null || true
        systemctl reload caddy 2>/dev/null || true
    fi
    if [[ "$PURGE" == 1 ]]; then
        confirm "Delete the cms database, $DATA, $CACHE and $ETC (backups included)?" || die "not confirmed"
        if command -v psql >/dev/null; then
            runuser -u postgres -- psql -q -v ON_ERROR_STOP=1 <<'SQL' || warn "could not drop the database"
DROP DATABASE IF EXISTS cms;
DROP ROLE IF EXISTS cms;
SQL
        fi
        for conf in /etc/valkey/valkey.conf /etc/redis/redis.conf; do
            [[ -f "$conf" ]] && sed -i '\#^include /etc/.*/cms.conf$#d' "$conf"
        done
        rm -f /etc/valkey/cms.conf /etc/redis/cms.conf
        rm -rf "$DATA" "$CACHE" "$ETC"
        userdel cms 2>/dev/null || true
        log "purged. PostgreSQL, Valkey/Redis, Caddy, isolate and the compilers are still installed (apt remove them if unused)"
    else
        log "removed the services and the binary. Kept: $ETC (configuration and secrets), $DATA (blobs, backups), the database"
        log "run again with --uninstall --purge to delete them"
    fi
}

if [[ "$UNINSTALL" == 1 ]]; then
    uninstall
    exit 0
fi

# --- mode --------------------------------------------------------------------

read_os
mkdir -p "$ETC"
# Options of the previous run, reused when this one gives no mode.
if [[ -f "$ETC/install.env" && -z "$DOMAIN" && -z "$HTTP_PORTS" && "$WORKER_ONLY" == 0 ]]; then
    # shellcheck disable=SC1091
    . "$ETC/install.env"
    DOMAIN="${INSTALL_DOMAIN:-}" ADMIN_DOMAIN="${INSTALL_ADMIN_DOMAIN:-}" RANKING_DOMAIN="${INSTALL_RANKING_DOMAIN:-}"
    HTTP_PORTS="${INSTALL_HTTP_PORTS:-}" WORKER_ONLY="${INSTALL_WORKER_ONLY:-0}"
    ALLOW_JUDGES="${ALLOW_JUDGES:-${INSTALL_ALLOW_JUDGES:-}}" ADMIN_ALLOW="${ADMIN_ALLOW:-${INSTALL_ADMIN_ALLOW:-}}"
    log "reusing the mode of the previous installation ($ETC/install.env)"
fi
if [[ "$WORKER_ONLY" == 1 ]]; then
    [[ -z "$DOMAIN$HTTP_PORTS" ]] || die "--worker-only does not take --domain or --http-ports"
    if [[ ! -f "$ETC/cms.yaml" ]]; then
        [[ -n "$DISPATCHER_URL" && -n "$BLOB_TOKEN_FILE" && -n "$VALKEY_URL_FILE" ]] ||
            die "--worker-only needs --dispatcher URL, --blob-token-file FILE and --valkey-url-file FILE"
        [[ -s "$BLOB_TOKEN_FILE" ]] || die "$BLOB_TOKEN_FILE is empty or missing"
        [[ -s "$VALKEY_URL_FILE" ]] || die "$VALKEY_URL_FILE is empty or missing"
    fi
elif [[ -n "$DOMAIN" ]]; then
    [[ -z "$HTTP_PORTS" ]] || die "use either --domain or --http-ports"
    ADMIN_DOMAIN="${ADMIN_DOMAIN:-admin.$DOMAIN}"
    RANKING_DOMAIN="${RANKING_DOMAIN:-ranking.$DOMAIN}"
else
    HTTP_PORTS="${HTTP_PORTS:-8000,8001,8002}"
fi
if [[ -n "$HTTP_PORTS" ]]; then
    IFS=, read -r CONTEST_PORT ADMIN_PORT RANKING_PORT <<<"$HTTP_PORTS"
    for p in "$CONTEST_PORT" "$ADMIN_PORT" "$RANKING_PORT"; do
        [[ "$p" =~ ^[0-9]+$ && "$p" -gt 0 && "$p" -lt 65536 ]] || die "--http-ports wants three ports: CONTEST,ADMIN,RANKING"
    done
fi
[[ "$CONCURRENCY" =~ ^[1-9][0-9]*$ ]] || die "--judge-concurrency must be a positive number"
[[ "$BACKUP_KEEP" =~ ^[0-9]+$ ]] || die "--backup-keep must be a number"
[[ "$ADMIN_USER" =~ ^[A-Za-z0-9_-]+$ ]] || die "--admin-user may only have letters, digits, _ and -"
case "$COMPILERS" in all | basic | none) ;; *) die "--compilers is all, basic or none" ;; esac

# --- packages ----------------------------------------------------------------

install_base() {
    log "installing base packages"
    apt-get update -q
    apt_install ca-certificates curl gnupg tar gzip coreutils procps iproute2
}

install_isolate() {
    if command -v isolate >/dev/null; then
        log "isolate already installed"
    elif [[ "$ARCH" == amd64 && "$CODENAME" =~ ^(jammy|noble|bookworm)$ ]]; then
        log "installing isolate from its authors' repository"
        local suite="$CODENAME-isolate"
        install -d -m 0755 /etc/apt/keyrings
        curl -fsSL https://www.ucw.cz/isolate/debian/signing-key.asc -o /etc/apt/keyrings/isolate.asc
        echo "deb [arch=amd64 signed-by=/etc/apt/keyrings/isolate.asc] http://www.ucw.cz/isolate/debian/ $suite main" \
            >/etc/apt/sources.list.d/isolate.list
        apt-get update -q
        apt_install isolate
    else
        # The repository only has amd64 packages for jammy, noble and
        # bookworm: elsewhere (arm64, Ubuntu 26.04...) build the same release.
        log "building isolate $ISOLATE_SRC_TAG from source ($ARCH)"
        apt_install build-essential git pkg-config libcap-dev libseccomp-dev libsystemd-dev
        local src
        src="$(mktemp -d)"
        git -c advice.detachedHead=false clone -q --depth 1 --branch "$ISOLATE_SRC_TAG" https://github.com/ioi/isolate "$src/isolate"
        make -C "$src/isolate" -s PREFIX=/usr VARPREFIX=/var CONFIGDIR=/etc isolate isolate-cg-keeper isolate-check-environment
        make -C "$src/isolate" -s PREFIX=/usr VARPREFIX=/var CONFIGDIR=/etc install
        rm -rf "$src"
        systemctl daemon-reload
    fi
    if [[ ! -f /sys/fs/cgroup/cgroup.controllers ]]; then
        warn "this machine has no cgroups v2: the judge cannot limit memory reliably (verify-host will FAIL)"
    fi
    systemctl enable --now isolate.service || warn "isolate.service did not start (see journalctl -u isolate)"
}

install_compilers() {
    [[ "$COMPILERS" == none ]] && return
    log "installing compilers ($COMPILERS)"
    local pkgs=(g++ gcc python3 default-jdk-headless)
    if [[ "$COMPILERS" == all ]]; then
        pkgs+=(pypy3 fp-compiler ghc mono-mcs mono-runtime rustc golang-go php-cli kotlin)
    fi
    local avail=()
    for p in "${pkgs[@]}"; do
        if have_candidate "$p"; then avail+=("$p"); else warn "package $p is not available here: skipped"; fi
    done
    apt_install "${avail[@]}"
    if [[ -x /usr/bin/go ]]; then
        # Prebuilt standard library (config/languages/go.yaml, D30).
        install -d -m 0755 /var/lib/cms
        CGO_ENABLED=0 GODEBUG=installgoroot=all GOCACHE="$(mktemp -d)" /usr/bin/go install -pkgdir=/var/lib/cms/go-std std ||
            warn "could not prebuild the Go standard library"
    fi
}

install_postgres() {
    log "installing PostgreSQL"
    apt_install postgresql
    systemctl enable --now postgresql
    local ver conf
    ver="$(ls /etc/postgresql 2>/dev/null | sort -V | tail -n1)"
    conf="/etc/postgresql/$ver/main/conf.d/cms.conf"
    if [[ -n "$ver" && -d "/etc/postgresql/$ver/main/conf.d" && ! -f "$conf" ]]; then
        # Sized for the web side of SPEC N1 (2 vCPU, 4 GB, shared with Valkey and the services).
        cat >"$conf" <<'EOF'
# Written once by the cms installer; edit freely.
max_connections = 200
shared_buffers = 512MB
effective_cache_size = 1536MB
work_mem = 8MB
maintenance_work_mem = 128MB
wal_compression = on
checkpoint_completion_target = 0.9
random_page_cost = 1.1
EOF
        systemctl restart postgresql
    fi
    local pw collate=C.UTF-8
    pw="$(secret db_password)"
    if locale -a 2>/dev/null | grep -qix 'en_US.utf8'; then collate=en_US.UTF-8; fi
    # The password goes through stdin, never through the command line.
    runuser -u postgres -- psql -q -v ON_ERROR_STOP=1 >/dev/null <<SQL
\\set pw '$pw'
SELECT format('CREATE ROLE cms LOGIN PASSWORD %L', :'pw') WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'cms') \\gexec
SELECT format('ALTER ROLE cms LOGIN PASSWORD %L', :'pw') \\gexec
SQL
    if ! runuser -u postgres -- psql -tAq -c "SELECT 1 FROM pg_database WHERE datname = 'cms'" | grep -q 1; then
        runuser -u postgres -- psql -q -v ON_ERROR_STOP=1 -c \
            "CREATE DATABASE cms OWNER cms TEMPLATE template0 ENCODING 'UTF8' LC_COLLATE '$collate' LC_CTYPE '$collate'"
    fi
}

install_valkey() {
    local pkg conf dir user svc
    if have_candidate valkey-server; then
        pkg=valkey-server conf=/etc/valkey/valkey.conf dir=/etc/valkey user=valkey svc=valkey-server
    else
        # Redis >= 7 works too (Ubuntu 24.04, Debian 12); Ubuntu 22.04 only
        # has 6.0, so the Redis repository is used there.
        if [[ "$CODENAME" == jammy ]] && ! dpkg -s redis-server >/dev/null 2>&1; then
            log "adding the Redis repository (Ubuntu 22.04 ships Redis 6, cms needs >= 7)"
            curl -fsSL https://packages.redis.io/gpg | gpg --dearmor --yes -o /usr/share/keyrings/redis-archive-keyring.gpg
            echo "deb [signed-by=/usr/share/keyrings/redis-archive-keyring.gpg] https://packages.redis.io/deb $CODENAME main" \
                >/etc/apt/sources.list.d/redis.list
            apt-get update -q
        fi
        pkg=redis-server conf=/etc/redis/redis.conf dir=/etc/redis user=redis svc=redis-server
    fi
    log "installing $pkg"
    apt_install "$pkg"
    local pw bind="127.0.0.1 -::1"
    pw="$(secret valkey_password)"
    [[ -n "$ALLOW_JUDGES" ]] && bind="* -::*"
    # Queues must never be evicted; appendonly keeps them across restarts.
    write_if_changed "$dir/cms.conf" 0640 <<EOF && KV_CHANGED=1 || true
# Written by the cms installer (rewritten on every run).
bind $bind
protected-mode yes
requirepass $pw
appendonly yes
appendfsync everysec
maxmemory-policy noeviction
EOF
    chown "root:$user" "$dir/cms.conf" 2>/dev/null || true
    grep -qx "include $dir/cms.conf" "$conf" || { echo "include $dir/cms.conf" >>"$conf"; KV_CHANGED=1; }
    systemctl enable "$svc" >/dev/null 2>&1 || true
    if [[ "${KV_CHANGED:-0}" == 1 ]]; then systemctl restart "$svc"; else systemctl start "$svc"; fi
}

install_caddy() {
    if ! command -v caddy >/dev/null; then
        log "installing Caddy"
        if ! have_candidate caddy; then
            curl -fsSL https://dl.cloudsmith.io/public/caddy/stable/gpg.key | gpg --dearmor --yes -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
            curl -fsSL https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt >/etc/apt/sources.list.d/caddy-stable.list
            apt-get update -q
        fi
        apt_install caddy
    fi
    local admin_guard=""
    if [[ -n "$ADMIN_ALLOW" ]]; then
        admin_guard="	@outside not remote_ip $ADMIN_ALLOW
	respond @outside 403"
    fi
    local email=""
    [[ -n "$ACME_EMAIL" ]] && email="{
	email $ACME_EMAIL
}
"
    # Server-sent events (*/events) must not be compressed (contestant-site.md).
    write_if_changed /etc/caddy/cms.caddy 0644 <<EOF || true
# Written by the cms installer (rewritten on every run).
(cms_site) {
	@noevents not path */events
	encode @noevents zstd gzip
	header -Server
}
$DOMAIN {
	import cms_site
	reverse_proxy 127.0.0.1:8888
}
$ADMIN_DOMAIN {
	import cms_site
$admin_guard
	reverse_proxy 127.0.0.1:8889
}
$RANKING_DOMAIN {
	import cms_site
	reverse_proxy 127.0.0.1:8890
}
EOF
    if [[ ! -f /etc/caddy/Caddyfile ]] || grep -q '/usr/share/caddy' /etc/caddy/Caddyfile; then
        # The stock Caddyfile (a welcome page on :80) is replaced.
        [[ -f /etc/caddy/Caddyfile ]] && cp -n /etc/caddy/Caddyfile /etc/caddy/Caddyfile.orig
        printf '%s# Written by the cms installer; the cms sites are in cms.caddy.\nimport /etc/caddy/cms.caddy\n' "$email" >/etc/caddy/Caddyfile
    elif ! grep -qx 'import /etc/caddy/cms.caddy' /etc/caddy/Caddyfile; then
        echo 'import /etc/caddy/cms.caddy' >>/etc/caddy/Caddyfile
        [[ -n "$email" ]] && warn "your Caddyfile is custom: set the ACME email in its global options yourself"
    fi
    caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile >/dev/null 2>&1 || die "the Caddy configuration is invalid (caddy validate --config /etc/caddy/Caddyfile)"
    systemctl enable caddy >/dev/null 2>&1 || true
    systemctl reload-or-restart caddy
}

# --- cms binary --------------------------------------------------------------

install_binary() {
    if [[ -n "$BINARY" ]]; then
        log "installing $BINARY"
        "$BINARY" version >/dev/null || die "$BINARY does not run"
        install -m 0755 "$BINARY" "$PREFIX/cms.new"
        mv -f "$PREFIX/cms.new" "$PREFIX/cms"
    elif [[ -x "$PREFIX/cms" && -z "$VERSION" ]]; then
        log "keeping the installed $(cms version | head -n1) (cmsctl upgrade upgrades it)"
    else
        local base asset tmp
        if [[ -z "$VERSION" || "$VERSION" == latest ]]; then
            base="https://github.com/$REPO/releases/latest/download"
        else
            base="https://github.com/$REPO/releases/download/$VERSION"
        fi
        asset="cms_linux_$ARCH.tar.gz"
        log "downloading $base/$asset"
        tmp="$(mktemp -d)"
        curl -fsSL -o "$tmp/$asset" "$base/$asset"
        curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt"
        (cd "$tmp" && grep " $asset\$" checksums.txt | sha256sum -c --quiet -) || die "$asset does not match checksums.txt"
        tar -xzf "$tmp/$asset" -C "$tmp" cms
        "$tmp/cms" version >/dev/null || die "the downloaded binary does not run"
        install -m 0755 "$tmp/cms" "$PREFIX/cms.new"
        mv -f "$PREFIX/cms.new" "$PREFIX/cms"
        rm -rf "$tmp"
    fi
    ln -sfn cms "$PREFIX/cmsctl"
}

# --- users, directories, configuration --------------------------------------

setup_dirs() {
    getent group cms >/dev/null || groupadd --system cms
    id cms >/dev/null 2>&1 || useradd --system --gid cms --home-dir "$DATA" --no-create-home --shell /usr/sbin/nologin cms
    install -d -m 0750 -o root -g cms "$ETC"
    install -d -m 0750 -o root -g cms "$SECRETS"
    install -d -m 0750 -o cms -g cms "$DATA" "$DATA/blobs" "$DATA/ranking" "$CACHE" "$CACHE/blobs"
    install -d -m 0700 -o cms -g cms "$DATA/backups"
    install -d -m 0755 -o root -g root "$DATA/worker"
    install -d -m 0700 -o root -g root "$CACHE/worker"
}

# host_ip prints the address of the interface of the default route.
host_ip() {
    ip -4 route get 1.1.1.1 2>/dev/null | awk '{for (i=1;i<NF;i++) if ($i=="src") {print $(i+1); exit}}'
}

server_timezone() {
    local tz
    tz="$(timedatectl show -p Timezone --value 2>/dev/null || true)"
    [[ -z "$tz" && -f /etc/timezone ]] && tz="$(cat /etc/timezone)"
    [[ -n "$tz" && -f "/usr/share/zoneinfo/$tz" ]] || tz=UTC
    echo "$tz"
}

write_config() {
    local ranking_port=8890
    [[ -n "$HTTP_PORTS" ]] && ranking_port="$RANKING_PORT"
    if [[ -f "$ETC/cms.yaml" ]]; then
        log "keeping $ETC/cms.yaml"
        if [[ -f "$ETC/install.env" ]] && ! grep -q "^INSTALL_RANKING_PORT=$ranking_port\$" "$ETC/install.env"; then
            warn "the scoreboard port changed: update dispatcher.rankings[0].url in $ETC/cms.yaml (http://127.0.0.1:$ranking_port/)"
        fi
        return
    fi
    log "writing $ETC/cms.yaml (secrets generated once)"
    local key dbpw kvpw
    key="$(cms ctl gen-secret)"
    dbpw="$(secret db_password)"
    kvpw="$(secret valkey_password)"
    (umask 027; cat >"$ETC/cms.yaml" <<EOF
# Written ONCE by the cms installer on $(date -u +%Y-%m-%dT%H:%M:%SZ); it is never overwritten.
# Every option: /usr/share/doc/cms/cms.sample.yaml or "cmsctl config show".
# Listen addresses and proxies come from $ETC/cms.env (rewritten by the
# installer); the judge's storage settings from $ETC/worker.env.
secret_key: "$key"
data_dir: $DATA
timezone: $(server_timezone)

database:
  url: postgres://cms:$dbpw@127.0.0.1:5432/cms?sslmode=disable

redis:
  url: redis://:$kvpw@127.0.0.1:6379/0

storage:
  backend: local
  local:
    path: $DATA/blobs
  cache_dir: $CACHE/blobs

ranking_web:
  data_dir: $DATA/ranking

dispatcher:
  # The local scoreboard; its credentials are derived from secret_key.
  rankings:
    - url: http://127.0.0.1:$ranking_port/

worker:
  concurrency: $CONCURRENCY
  work_dir: $DATA/worker
EOF
    )
    chown root:cms "$ETC/cms.yaml"
    chmod 0640 "$ETC/cms.yaml"
}

write_worker_config() {
    if [[ -f "$ETC/cms.yaml" ]]; then
        log "keeping $ETC/cms.yaml"
        return
    fi
    log "writing $ETC/cms.yaml for a judging machine"
    local token kvurl
    token="$(tr -d ' \r\n' <"$BLOB_TOKEN_FILE")"
    kvurl="$(tr -d ' \r\n' <"$VALKEY_URL_FILE")"
    (umask 077; cat >"$ETC/cms.yaml" <<EOF
# Written ONCE by the cms installer (--worker-only) on $(date -u +%Y-%m-%dT%H:%M:%SZ).
# A judge never connects to PostgreSQL and needs no secret_key (D12, D25).
data_dir: $DATA
redis:
  url: $kvurl
storage:
  backend: remote
  remote:
    url: $DISPATCHER_URL
    token: $token
  cache_dir: $CACHE/worker
worker:
  concurrency: $CONCURRENCY
  work_dir: $DATA/worker
EOF
    )
    chmod 0600 "$ETC/cms.yaml"
}

write_env() {
    local contest admin ranking proxies=0 blob=127.0.0.1:$BLOB_PORT
    if [[ -n "$DOMAIN" ]]; then
        contest=127.0.0.1:8888 admin=127.0.0.1:8889 ranking=127.0.0.1:8890 proxies=1
    else
        contest=0.0.0.0:$CONTEST_PORT admin=0.0.0.0:$ADMIN_PORT ranking=0.0.0.0:$RANKING_PORT
    fi
    [[ -n "$ALLOW_JUDGES" ]] && blob=0.0.0.0:$BLOB_PORT
    write_if_changed "$ETC/cms.env" 0644 <<EOF || true
# Written by the cms installer on every run (no secrets): mode-dependent settings.
CMS_CONTEST_WEB_LISTEN=$contest
CMS_ADMIN_WEB_LISTEN=$admin
CMS_RANKING_WEB_LISTEN=$ranking
CMS_CONTEST_WEB_NUM_PROXIES=$proxies
CMS_ADMIN_WEB_NUM_PROXIES=$proxies
CMS_RANKING_WEB_NUM_PROXIES=$proxies
CMS_DISPATCHER_BLOB_LISTEN=$blob
EOF
    # The local judge reaches the files through the blob server of the
    # dispatcher, like a judge on another machine: it never writes in the
    # blob directory of the cms user.
    cms --config "$ETC/cms.yaml" ctl blob-token >"$SECRETS/blob_token.new"
    mv -f "$SECRETS/blob_token.new" "$SECRETS/blob_token"
    chmod 0600 "$SECRETS/blob_token"
    write_if_changed "$ETC/worker.env" 0644 <<EOF || true
# Written by the cms installer on every run: storage of the local judge.
# The blob server is the dispatcher's (cms.env); the judge only reads it.
CMS_DISPATCHER_BLOB_LISTEN=
CMS_STORAGE_BACKEND=remote
CMS_STORAGE_REMOTE_URL=http://127.0.0.1:$BLOB_PORT
CMS_STORAGE_REMOTE_TOKEN_FILE=$SECRETS/blob_token
CMS_STORAGE_CACHE_DIR=$CACHE/worker
EOF
    # What a judge on another machine needs (copy these two files to it).
    if [[ -n "$ALLOW_JUDGES" ]]; then
        install -d -m 0700 "$ETC/judges"
        cp "$SECRETS/blob_token" "$ETC/judges/blob-token"
        local ip
        ip="$(host_ip)"
        (umask 077; echo "redis://:$(secret valkey_password)@${ip:-SERVER-IP}:6379/0" >"$ETC/judges/valkey-url")
        JUDGE_IP="${ip:-SERVER-IP}"
    fi
}

write_install_env() {
    cat >"$ETC/install.env" <<EOF
# Options of the last run of the installer (no secrets).
INSTALL_DOMAIN=$DOMAIN
INSTALL_ADMIN_DOMAIN=$ADMIN_DOMAIN
INSTALL_RANKING_DOMAIN=$RANKING_DOMAIN
INSTALL_HTTP_PORTS=$HTTP_PORTS
INSTALL_RANKING_PORT=${RANKING_PORT:-8890}
INSTALL_WORKER_ONLY=$WORKER_ONLY
INSTALL_ALLOW_JUDGES=$ALLOW_JUDGES
INSTALL_ADMIN_ALLOW=$ADMIN_ALLOW
EOF
}

# --- systemd -----------------------------------------------------------------

# Hardening of the services that run as the cms user.
HARDENING='NoNewPrivileges=yes
ProtectSystem=strict
ReadWritePaths=/var/lib/cms /var/cache/cms
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectKernelLogs=yes
ProtectControlGroups=yes
ProtectClock=yes
ProtectHostname=yes
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK
RestrictNamespaces=yes
RestrictRealtime=yes
RestrictSUIDSGID=yes
LockPersonality=yes
SystemCallArchitectures=native
SystemCallFilter=@system-service
CapabilityBoundingSet=
AmbientCapabilities=
UMask=0027'

write_unit() { # service description after unit-extra service-extra
    local svc="$1" desc="$2" after="$3" unit_extra="$4" extra="$5"
    write_if_changed "$UNITDIR/cms-$svc.service" 0644 <<EOF && UNITS_CHANGED=1 || true
# Written by the cms installer.
[Unit]
Description=CMS $desc
Documentation=https://github.com/$REPO
After=network-online.target $after
Wants=network-online.target
PartOf=cms.target
$unit_extra

[Service]
Type=simple
ExecStart=$PREFIX/cms --config $ETC/cms.yaml $svc
EnvironmentFile=-$ETC/cms.env
Restart=always
RestartSec=2
LimitNOFILE=65536
$extra

[Install]
WantedBy=cms.target
EOF
}

write_units() {
    log "writing the systemd units"
    UNITS_CHANGED=0
    write_if_changed "$UNITDIR/cms.target" 0644 <<EOF && UNITS_CHANGED=1 || true
# Written by the cms installer: every cms service of this machine.
[Unit]
Description=CMS (all services)
Documentation=https://github.com/$REPO

[Install]
WantedBy=multi-user.target
EOF
    local db="postgresql.service valkey-server.service redis-server.service"
    local as_cms="User=cms
Group=cms
$HARDENING"
    if [[ "$WORKER_ONLY" == 0 ]]; then
        write_unit contest-web "contestant site" "$db" "" "$as_cms"
        write_unit admin-web "administration" "$db" "" "$as_cms"
        write_unit ranking-web "public scoreboard" "" "" "$as_cms"
        write_unit dispatcher "dispatcher (queues, scoring, scoreboard push)" "$db" "" "$as_cms"
        write_unit monitor "monitor (health, host resources)" "$db" "" "$as_cms"
    fi
    # The judge runs as root: isolate needs it (it drops to its own box
    # users). Only hardening that neither isolate nor the compilers notice
    # (no device or namespace restrictions).
    local worker_env=""
    [[ "$WORKER_ONLY" == 0 ]] && worker_env="EnvironmentFile=$ETC/worker.env"
    write_unit worker "judge (isolate)" "isolate.service cms-dispatcher.service" "Wants=isolate.service" "$worker_env
KillMode=mixed
TimeoutStopSec=30
ProtectHome=yes
ProtectKernelModules=yes"

    if [[ "$WORKER_ONLY" == 0 && "$BACKUP_KEEP" -gt 0 ]]; then
        write_if_changed "$UNITDIR/cms-backup.service" 0644 <<EOF && UNITS_CHANGED=1 || true
# Written by the cms installer: scheduled backup (cms-backup.timer).
[Unit]
Description=CMS backup (database, files, configuration)
After=postgresql.service

[Service]
Type=oneshot
User=cms
Group=cms
ExecStart=$PREFIX/cms --config $ETC/cms.yaml ctl backup --keep $BACKUP_KEEP
Nice=10
IOSchedulingClass=idle
$HARDENING
EOF
        write_if_changed "$UNITDIR/cms-backup.timer" 0644 <<EOF && UNITS_CHANGED=1 || true
# Written by the cms installer.
[Unit]
Description=Scheduled CMS backup

[Timer]
OnCalendar=$BACKUP_CALENDAR
RandomizedDelaySec=10min
Persistent=true

[Install]
WantedBy=timers.target
EOF
    fi
    systemctl daemon-reload
}

enable_units() {
    local units=(cms-worker.service)
    [[ "$WORKER_ONLY" == 0 ]] && units+=(cms-contest-web.service cms-admin-web.service cms-ranking-web.service cms-dispatcher.service cms-monitor.service)
    systemctl enable cms.target "${units[@]}" >/dev/null 2>&1
    if [[ "$WORKER_ONLY" == 0 && "$BACKUP_KEEP" -gt 0 ]]; then
        systemctl enable --now cms-backup.timer >/dev/null 2>&1
    elif [[ -f "$UNITDIR/cms-backup.timer" ]]; then
        systemctl disable --now cms-backup.timer >/dev/null 2>&1 || true
    fi
    log "starting the services"
    systemctl restart "${units[@]}"
}

# --- firewall ----------------------------------------------------------------

setup_firewall() {
    [[ "$FIREWALL" == 1 ]] || return 0
    log "configuring the firewall (ufw)"
    apt_install ufw
    # Never lock out the administrator: SSH first.
    local ssh_port
    ssh_port="$(sshd -T 2>/dev/null | awk '$1 == "port" {print $2; exit}')"
    ufw allow "${ssh_port:-22}/tcp" comment 'ssh' >/dev/null
    if [[ "$WORKER_ONLY" == 0 ]]; then
        if [[ -n "$DOMAIN" ]]; then
            ufw allow 80/tcp comment 'cms (caddy)' >/dev/null
            ufw allow 443/tcp comment 'cms (caddy)' >/dev/null
            ufw allow 443/udp comment 'cms (caddy, HTTP/3)' >/dev/null
        else
            ufw allow "$CONTEST_PORT/tcp" comment 'cms contest site' >/dev/null
            if [[ -n "$ADMIN_ALLOW" ]]; then
                ufw allow from "$ADMIN_ALLOW" to any port "$ADMIN_PORT" proto tcp comment 'cms admin' >/dev/null
            else
                ufw allow "$ADMIN_PORT/tcp" comment 'cms admin' >/dev/null
            fi
            ufw allow "$RANKING_PORT/tcp" comment 'cms scoreboard' >/dev/null
        fi
        if [[ -n "$ALLOW_JUDGES" ]]; then
            ufw allow from "$ALLOW_JUDGES" to any port 6379 proto tcp comment 'cms judges: valkey' >/dev/null
            ufw allow from "$ALLOW_JUDGES" to any port "$BLOB_PORT" proto tcp comment 'cms judges: blobs' >/dev/null
        fi
    fi
    ufw --force enable >/dev/null
}

# --- database, first admin, checks -------------------------------------------

ADMIN_PASSWORD=""
setup_database() {
    log "applying the database migrations"
    as_cms --config "$ETC/cms.yaml" migrate
    local out
    out="$(as_cms --config "$ETC/cms.yaml" ctl add-admin --if-not-exists "$ADMIN_USER")"
    ADMIN_PASSWORD="$(sed -n 's/^password: //p' <<<"$out")"
    if [[ -n "$DOMAIN" ]]; then
        as_cms --config "$ETC/cms.yaml" ctl public-urls --if-unset \
            --contest "https://$DOMAIN/" --ranking "https://$RANKING_DOMAIN/" >/dev/null
    else
        as_cms --config "$ETC/cms.yaml" ctl public-urls --if-unset \
            --contest ":$CONTEST_PORT" --ranking ":$RANKING_PORT" >/dev/null
    fi
}

wait_healthy() {
    local svc port ok=1
    declare -A ports=([contest-web]=9188 [admin-web]=9189 [ranking-web]=9190 [dispatcher]=9191 [worker]=9192 [monitor]=9193)
    local list=(worker)
    [[ "$WORKER_ONLY" == 0 ]] && list=(contest-web admin-web ranking-web dispatcher worker monitor)
    for svc in "${list[@]}"; do
        port="${ports[$svc]}"
        local i
        for ((i = 0; i < 30; i++)); do
            curl -fsS -o /dev/null --noproxy '*' "http://127.0.0.1:$port/healthz" && break
            sleep 2
        done
        if ((i == 30)); then
            warn "cms-$svc is not healthy yet: journalctl -u cms-$svc; curl http://127.0.0.1:$port/healthz"
            ok=0
        fi
    done
    HEALTHY=$ok
}

VERIFY_HOST="not run"
verify_host() {
    log "checking the judging host (cmsctl verify-host)"
    local envs=()
    if [[ "$WORKER_ONLY" == 0 ]]; then
        mapfile -t envs < <(grep -E '^CMS_' "$ETC/worker.env")
    fi
    if env "${envs[@]}" "$PREFIX/cms" --config "$ETC/cms.yaml" ctl verify-host; then
        VERIFY_HOST="passed (see above for warnings)"
    else
        VERIFY_HOST="FAILED: the judge is not reliable on this machine (see the FAIL lines above)"
    fi
}

summary() {
    local version
    version="$(cms version | head -n1)"
    echo
    echo "======================================================================"
    echo " $version installed"
    echo "======================================================================"
    if [[ "$WORKER_ONLY" == 1 ]]; then
        echo " Judging machine: cms-worker.service ($CONCURRENCY parallel jobs)"
        echo " It appears on the Judges page of the administration once connected."
    else
        if [[ -n "$DOMAIN" ]]; then
            echo " Contestant site:  https://$DOMAIN/"
            echo " Administration:   https://$ADMIN_DOMAIN/"
            echo " Scoreboard:       https://$RANKING_DOMAIN/"
            echo " (certificates are issued on the first visit; DNS must point here)"
        else
            local h
            h="$(host_ip)"
            h="${h:-SERVER}"
            echo " Contestant site:  http://$h:$CONTEST_PORT/"
            echo " Administration:   http://$h:$ADMIN_PORT/"
            echo " Scoreboard:       http://$h:$RANKING_PORT/"
        fi
        echo
        if [[ -n "$ADMIN_PASSWORD" ]]; then
            echo " First administrator: $ADMIN_USER"
            echo " Password:            $ADMIN_PASSWORD"
            echo " It is shown only now and stored nowhere in clear: write it down."
        else
            echo " Administrator '$ADMIN_USER' already existed: its password was not changed."
        fi
        echo
        echo " Configuration:  $ETC/cms.yaml (kept on re-runs), $ETC/cms.env"
        if [[ "$BACKUP_KEEP" -gt 0 ]]; then
            echo " Backups:        $DATA/backups ($BACKUP_CALENDAR, newest $BACKUP_KEEP kept)"
        fi
        echo " Upgrade:        sudo cmsctl upgrade   (backs up and rolls back on failure)"
        echo " Services:       systemctl status cms.target 'cms-*'"
        if [[ -n "$ALLOW_JUDGES" ]]; then
            echo
            echo " Extra judges ($ALLOW_JUDGES): copy $ETC/judges/blob-token and"
            echo " $ETC/judges/valkey-url to the machine (they are secrets) and run there:"
            echo "   install.sh --worker-only --dispatcher http://$JUDGE_IP:$BLOB_PORT \\"
            echo "     --blob-token-file blob-token --valkey-url-file valkey-url"
        fi
    fi
    echo
    echo " Judge check:    $VERIFY_HOST"
    [[ "${HEALTHY:-1}" == 1 ]] || echo " Some services are not healthy yet: see the warnings above."
    echo "======================================================================"
}

# --- main --------------------------------------------------------------------

install_base
install_binary
setup_dirs
install_isolate
install_compilers
if [[ "$WORKER_ONLY" == 1 ]]; then
    write_worker_config
else
    install_postgres
    install_valkey
    write_config
    write_env
    setup_database
    [[ -n "$DOMAIN" ]] && install_caddy
fi
write_install_env
write_units
setup_firewall
enable_units
wait_healthy
verify_host
summary
