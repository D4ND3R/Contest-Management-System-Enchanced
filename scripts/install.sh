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
#   ... | sudo bash -s -- --check            (preflight checks only)
#   ... | sudo bash -s -- --clean ...        (back up, wipe, install from zero)
#   ... | sudo bash -s -- --uninstall [--purge --yes]
#
# Run with --help for every option; docs/en/installation.md explains them.
#
# Re-running is safe: secrets are generated ONCE (/etc/cms/secrets), cms.yaml
# is written only if it does not exist, the first admin only if missing, and
# the cms binary is replaced only when --binary or --version is given (use
# `cmsctl upgrade` to upgrade). Units, the Caddy site, the firewall rules
# and /etc/cms/cms.env (listen addresses) are rewritten from the options.
#
# Everything printed also goes to /var/log/cms-install.log (0600). Secrets
# are never printed, except the generated admin password, once, and only to
# the terminal. Preflight checks run before anything is changed; any failure
# prints the step, the command, the last lines of output and a diagnosis
# with the fix.
set -Eeuo pipefail
shopt -s inherit_errexit

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
LOG=/var/log/cms-install.log
BACKUPS=/var/backups/cms
MIN_DISK_GIB=3

# Ops listeners (/healthz, /metrics) of the services (config/cms.sample.yaml).
declare -A OPS_PORTS=([contest-web]=9188 [admin-web]=9189 [ranking-web]=9190 [dispatcher]=9191 [worker]=9192 [monitor]=9193)

# --- options -----------------------------------------------------------------

DOMAIN="" ADMIN_DOMAIN="" RANKING_DOMAIN="" ACME_EMAIL=""
HTTP_PORTS="" CONTEST_PORT="" ADMIN_PORT="" RANKING_PORT=""
VERSION="" BINARY=""
WORKER_ONLY=0 DISPATCHER_URL="" BLOB_TOKEN_FILE="" VALKEY_URL_FILE=""
ALLOW_JUDGES="" ADMIN_ALLOW=""
CONCURRENCY=1
ADMIN_USER=admin
FIREWALL=auto COMPILERS=all
BACKUP_KEEP=14 BACKUP_CALENDAR="*-*-* 03:17:00"
UNINSTALL=0 PURGE=0 YES=0 FORCE_OS=0
CHECK_ONLY=0 CLEAN=0 NO_BACKUP=0

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
  --check                  only run the preflight checks (changes nothing)
  --uninstall              remove services and binary; keeps data unless --purge

Options:
  --clean                  clean install: back up, then delete every trace of
                           previous cms installations (services, /etc/cms,
                           data, the "cms" database and role, cms keys in
                           Valkey...) and install from zero. Asks first
                           (--yes to skip). Other software is never removed
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
  --firewall               enable ufw even if it is inactive (SSH is allowed
                           first). By default rules are added only when ufw
                           is already active; otherwise it is left off
  --no-firewall            do not touch ufw
  --backup-keep N          scheduled backups kept (default 14; 0 disables the timer)
  --backup-calendar SPEC   systemd OnCalendar of the backups (default "*-*-* 03:17:00")
  --dispatcher URL         (worker-only) blob server, e.g. http://10.0.0.1:8891
  --blob-token-file FILE   (worker-only) file with the output of `cmsctl blob-token`
  --valkey-url-file FILE   (worker-only) file with redis://:PASSWORD@HOST:6379/0
  --purge                  (uninstall) also delete the database, data and config
  --no-backup              (--clean, --purge) delete without backing up first
                           (default: database dump and /etc/cms in /var/backups/cms)
  --yes                    answer yes (needed by --clean and --purge when there
                           is no terminal)
  --force-os               try on an unsupported distribution

Everything is logged to /var/log/cms-install.log. Running the same command
again is safe.
EOF
}

# --- output, log and failure report ------------------------------------------

CURRENT_STEP="starting"
LOGGING=0 LOG_START=0 TEE_PID="" FAIL_NOTE=""

log() { CURRENT_STEP="$*"; echo "==> $*" >&2; }
warn() { echo "install.sh: WARNING: $*" >&2; }

# die stops the installer with a message. Once the log is open it prints the
# failure report; inside a command substitution it leaves the message to the
# shell that runs the installer.
die() {
    if [[ "$LOGGING" == 0 ]]; then
        echo "install.sh: error: $*" >&2
        exit 1
    fi
    if [[ "$BASHPID" != "$$" ]]; then
        note_failure 1 "" "" "$*"
        exit 1
    fi
    trap - ERR
    report_failure 1 "" "" "" "$*"
    exit 1
}

# note_failure records a failure inside a subshell (command substitution),
# so that the main shell reports the command that really failed.
note_failure() { # code line command message
    [[ -n "$FAIL_NOTE" ]] || return 0
    printf '%s\n%s\n%s\n%s\n' "$1" "$2" "${3%%$'\n'*}" "$4" >"$FAIL_NOTE" 2>/dev/null || true
}

on_err() { # code line command
    local code="$1" line="$2" cmd="${3%%$'\n'*}" func="${FUNCNAME[1]:-main}" msg=""
    if [[ "$BASHPID" != "$$" ]]; then
        # A nested substitution already noted the command that failed.
        if [[ "$cmd" != *'$('* || ! -s "$FAIL_NOTE" ]]; then
            note_failure "$code" "$line" "$cmd" ""
        fi
        exit "$code"
    fi
    trap - ERR
    if [[ "$cmd" == *'$('* && -s "$FAIL_NOTE" ]]; then
        { read -r code; read -r line; read -r cmd; read -r msg; } <"$FAIL_NOTE" || true
    fi
    report_failure "$code" "$cmd" "$line" "$func" "$msg"
    exit "$code"
}

on_signal() { # name exit-code
    trap - ERR INT TERM HUP
    report_failure "$2" "" "" "" "interrupted by SIG$1"
    exit "$2"
}

on_exit() {
    local rc=$?
    [[ -z "$FAIL_NOTE" ]] || rm -f "$FAIL_NOTE"
    if [[ "$LOGGING" == 1 ]]; then
        # Let tee write everything before the end mark.
        exec 1>&3 2>&4
        wait "$TEE_PID" 2>/dev/null || true
        printf -- '--- end of run: exit %s at %s\n\n' "$rc" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >>"$LOG"
    fi
}

# start_log sends everything printed from now on to the terminal and to
# $LOG. fd 3 keeps the terminal: what is printed there (the generated admin
# password) never reaches the log.
start_log() { # arguments of the run
    (umask 077; touch "$LOG")
    chmod 0600 "$LOG"
    {
        echo "======================================================================"
        echo "cms installer run: $(date -u +%Y-%m-%dT%H:%M:%SZ) on $(hostname 2>/dev/null || echo '?')"
        echo "release requested: ${VERSION:-none (keeps the installed binary, else the latest)}${BINARY:+ (binary $BINARY)}"
        echo "installed cms:     $(installed_version)"
        echo "arguments:         ${*:-(none)}"
        echo "======================================================================"
    } >>"$LOG"
    LOG_START="$(wc -l <"$LOG")"
    exec 3>&1 4>&2
    # tee ignores ^C so that the report of an interrupted run is still shown.
    exec > >(trap '' INT TERM HUP; exec tee -a "$LOG") 2>&1
    TEE_PID=$!
    LOGGING=1
    echo "==> full log: $LOG"
}

installed_version() {
    local v=""
    if [[ -x "$PREFIX/cms" ]]; then v="$("$PREFIX/cms" version 2>/dev/null || true)"; fi
    v="${v%%$'\n'*}"
    echo "${v:-none}"
}

# filter_secrets hides lines and URL parts that may carry a secret.
filter_secrets() {
    sed -E 's#(://[^:/@[:space:]]*:)[^@[:space:]]*@#\1***@#g' |
        grep -v -i -E 'requirepass|password|passwd|secret|token' || true
}

# tail_output prints the last N lines printed by this run.
tail_output() {
    [[ "$LOGGING" == 1 ]] || return 0
    tail -n +"$((LOG_START + 1))" "$LOG" 2>/dev/null | tail -n "$1" | filter_secrets || true
}

report_failure() { # code command line function message
    set +e
    local code="$1" cmd="$2" line="$3" func="$4" msg="$5" recent
    sleep 0.3 # tee
    recent="$(tail_output 200)"
    {
        echo
        echo "######################################################################"
        echo "  INSTALLATION FAILED"
        echo "######################################################################"
        echo "  Step:       $CURRENT_STEP"
        [[ -z "$msg" ]] || echo "  Error:      $msg"
        if [[ -n "$cmd" ]]; then
            echo "  Command:    $cmd"
            echo "  Exit code:  $code (line $line, in $func)"
        fi
        if [[ -n "$recent" ]]; then
            echo "  Last lines of output:"
            tail -n 20 <<<"$recent" | sed 's/^/    | /'
        fi
        echo
        echo "  DIAGNOSIS"
        diagnose "$code" "$cmd${msg:+$'\n'$msg}" "$recent" | sed 's/^/    /'
        echo
        [[ "$LOGGING" == 0 ]] || echo "  Full log:   $LOG"
        echo "  Running the same command again is safe once the cause is fixed."
        echo "  --clean backs up and removes every trace of cms, then installs from zero."
        echo "######################################################################"
    } >&2
}

matches() { grep -qiE -- "$1" <<<"$2"; }

# port_holder prints "NAME PID" of the process listening on a TCP port,
# nothing if the port is free.
port_holder() {
    local lines h
    lines="$(ss -Hltnp "sport = :$1" 2>/dev/null || true)"
    [[ -n "$lines" ]] || return 0
    h="$(sed -n 's/.*users:(("\([^"]*\)",pid=\([0-9]*\).*/\1 \2/p' <<<"$lines" | head -n1 || true)"
    echo "${h:-unknown ?}"
}

journal_tail() { # unit
    journalctl -u "$1" -n 20 --no-pager -o cat 2>&1 | filter_secrets | sed 's/^/  | /' || true
}

# curl_code prints the curl exit code of a failure, if curl failed.
curl_code() { # code command output
    local n
    n="$(grep -oE 'curl: \([0-9]+\)' <<<"$3" | tail -n1 | tr -dc 0-9 || true)"
    if [[ -z "$n" && "$2" == curl* ]]; then n="$1"; fi
    echo "$n"
}

diagnose_curl() { # curl-code output
    case "$1" in
    6)
        echo "This machine cannot resolve host names (curl error 6, DNS)."
        echo "Fix: check the DNS servers (resolvectl status; cat /etc/resolv.conf;"
        echo "     getent hosts github.com) and run the installer again."
        ;;
    7)
        echo "Could not connect (curl error 7): outgoing HTTPS is blocked by a firewall,"
        echo "there is no route, or a proxy is required."
        echo "Fix: try curl -I https://github.com; behind a proxy export https_proxy and run"
        echo "     the installer with sudo -E; then run it again."
        ;;
    28)
        echo "The download timed out (curl error 28): the network is slow or blocked."
        echo "Fix: check the connection (curl -I https://github.com) and run the installer again."
        ;;
    35 | 60)
        echo "The TLS connection failed (curl error $1): wrong clock, old certificates,"
        echo "or a proxy that intercepts HTTPS. The clock says: $(date -u)."
        echo "Fix: correct the time (timedatectl), apt install ca-certificates, or install"
        echo "     the proxy's CA in /usr/local/share/ca-certificates and run update-ca-certificates."
        ;;
    22)
        if matches 'error: 404' "$2"; then
            echo "The file does not exist on the server (HTTP 404)."
            echo "Fix: check --version (the tags are in https://github.com/$REPO/releases)"
            echo "     and that the release has cms_linux_${ARCH:-amd64}.tar.gz; or install a"
            echo "     local build with --binary PATH."
        elif matches 'error: 40[37]' "$2"; then
            echo "A proxy or the server refused the download (HTTP 403/407)."
            echo "Fix: allow github.com and objects.githubusercontent.com in the proxy (or give"
            echo "     its credentials in https_proxy) and run the installer again."
        else
            echo "The server answered with an HTTP error (see the curl line above)."
            echo "Fix: try again later, or install a local build with --binary PATH."
        fi
        ;;
    56)
        echo "The connection was cut during the download (curl error 56; often a proxy)."
        echo "Fix: run the installer again; behind a proxy check that it allows github.com."
        ;;
    *)
        echo "curl failed with error $1 (see EXIT CODES in man curl)."
        echo "Fix: check the network (curl -I https://github.com) and run the installer again."
        ;;
    esac
}

# diagnose prints the likely cause of a failure and its fix, from the failed
# command (and message) and the last lines of output.
diagnose() { # code command output
    local code="$1" cmd="$2" out="$3" all n unit svc h p
    all="$cmd"$'\n'"$out"
    if matches 'Could not get lock|dpkg frontend lock|Unable to lock the administration directory|is held by process' "$all"; then
        echo "Another package manager is running (often the automatic updates after boot):"
        ps -o pid,etime,cmd -C apt,apt-get,dpkg,unattended-upgr 2>/dev/null | sed 's/^/  /' || true
        echo "Fix: wait until it finishes and run the installer again (it waits up to 10"
        echo "     minutes for the lock by itself)."
    elif matches 'dpkg was interrupted' "$all"; then
        echo "A previous package installation was interrupted."
        echo "Fix: sudo dpkg --configure -a, then run the installer again."
    elif matches 'No space left on device' "$all"; then
        echo "The disk is full:"
        df -h / /var /tmp 2>&1 | sed 's/^/  /' || true
        echo "Fix: free space (sudo apt-get clean; sudo journalctl --vacuum-size=200M; unused"
        echo "     Docker images: docker system prune) and run the installer again; cms needs"
        echo "     at least $MIN_DISK_GIB GiB free on /var."
    elif matches 'column "checksum" does not exist|not made by this installer' "$all"; then
        echo "A PostgreSQL database named \"cms\" belongs to another program (or the original CMS)."
        echo "Fix: back it up and remove it:"
        echo "       sudo -u postgres pg_dump cms | gzip > cms-old.sql.gz"
        echo "       sudo -u postgres dropdb cms"
        echo "     or run the installer with --clean (it backs it up to $BACKUPS first)."
    elif matches 'password authentication failed' "$all"; then
        echo "PostgreSQL refused the password of database.url in $ETC/cms.yaml."
        echo "The installer sets the password of the role \"cms\" from $SECRETS/db_password."
        echo "Fix: put that password in database.url (sudo cat $SECRETS/db_password;"
        echo "     sudo nano $ETC/cms.yaml) and run the installer again, or start from zero"
        echo "     with --clean."
    elif matches 'role "cms" cannot be dropped' "$all"; then
        echo "The role \"cms\" still owns objects in other databases."
        echo "Fix: in each of them: sudo -u postgres psql -d DATABASE -c 'REASSIGN OWNED BY cms"
        echo "     TO postgres; DROP OWNED BY cms', then run the installer again."
    elif n="$(curl_code "$code" "$cmd" "$out")" && [[ -n "$n" ]]; then
        diagnose_curl "$n" "$all"
    elif matches 'Temporary failure resolving|Could not resolve' "$all"; then
        diagnose_curl 6 "$all"
    elif matches '407 Proxy|Proxy Authentication Required|403 +Forbidden' "$all"; then
        echo "A proxy refused the package download (HTTP 403/407)."
        echo "Fix: allow the package mirrors in the proxy (or set its credentials in"
        echo "     /etc/apt/apt.conf.d/) and run the installer again."
    elif matches 'Failed to fetch|Could not connect to|Unable to connect to|Network is unreachable|Connection timed out' "$all"; then
        echo "The package mirrors cannot be reached."
        echo "Fix: check the network and the apt sources (sudo apt-get update shows which"
        echo "     one fails) and run the installer again."
    elif matches 'does not match checksums.txt|checksum did NOT match|sha256sum' "$all"; then
        echo "The downloaded release does not match its checksums.txt (corrupted or cut"
        echo "download, a proxy that changes files, or an incomplete release)."
        echo "Fix: run the installer again; if it persists, download the release by hand and"
        echo "     use --binary PATH."
    elif matches 'unsupported system|unsupported architecture' "$all"; then
        echo "This distribution or architecture is not supported (Ubuntu 22.04/24.04/26.04"
        echo "or Debian 12, amd64 or arm64)."
        echo "Fix: use a supported system, or try with --force-os (untested)."
    elif matches '(valkey|redis)-server' "$all" && matches 'fail|error' "$all"; then
        svc=valkey-server
        if matches 'redis-server' "$all" && ! matches 'valkey-server' "$all"; then svc=redis-server; fi
        echo "$svc does not start. Last lines of its journal:"
        journal_tail "$svc"
        h="$(port_holder 6379)"
        if [[ -n "$h" && "${h%% *}" != "$svc" ]]; then
            echo "Port 6379 is held by ${h%% *} (pid ${h##* }): that is why."
            echo "Fix: if nothing else uses it: sudo systemctl disable --now ${h%% *}; then run"
            echo "     the installer again."
        else
            echo "Fix: correct what the journal says (sudo systemctl status $svc), then run the"
            echo "     installer again."
        fi
    elif unit="$(sed -n 's/.*Job for \([^ ]*\) failed.*/\1/p' <<<"$all" | head -n1)" && [[ -n "$unit" ]]; then
        echo "$unit does not start. Last lines of its journal:"
        journal_tail "$unit"
        echo "Fix: correct what the journal says (sudo systemctl status $unit), then run the"
        echo "     installer again."
    elif matches 'address already in use|EADDRINUSE' "$all"; then
        echo "A port is already used by another program:"
        for p in $(grep -iE 'address already in use|bind' <<<"$all" | grep -oE ':[0-9]{2,5}\b' | tr -d : | sort -u); do
            h="$(port_holder "$p")"
            echo "  port $p: ${h:-free now}"
        done
        echo "Fix: stop that program or choose other ports (--http-ports C,A,R), then run"
        echo "     the installer again."
    elif matches 'postgres|5432' "$all" && matches 'connection refused|could not connect to server|Is the server running|connection to server' "$all"; then
        echo "PostgreSQL does not answer. Last lines of its journal:"
        journal_tail postgresql
        echo "Fix: sudo systemctl status postgresql (and the cluster: pg_lsclusters), then"
        echo "     run the installer again."
    elif matches '(^| )migrate( |$)' "$cmd" || [[ "$CURRENT_STEP" == *migrations* ]]; then
        echo "A database migration failed (the error is in the last lines above). Each"
        echo "migration runs in a transaction: the database stays at the previous version."
        echo "Fix: if the database \"cms\" came from another program, use --clean; otherwise"
        echo "     report the error with $LOG."
    elif matches 'caddy' "$cmd$CURRENT_STEP"; then
        echo "Caddy rejected the configuration or did not start:"
        journal_tail caddy
        echo "Fix: caddy validate --config /etc/caddy/Caddyfile shows the error; correct"
        echo "     /etc/caddy/Caddyfile (the cms sites are in /etc/caddy/cms.caddy)."
    elif matches '^E: ' "$out"; then
        echo "apt failed (the lines starting with E: above say why)."
        echo "Fix: run sudo apt-get update and correct the sources it complains about, then"
        echo "     run the installer again."
    elif [[ "$code" == 130 || "$code" == 143 || "$code" == 129 ]]; then
        echo "The installer was interrupted."
        echo "Fix: run the same command again: it continues where it stopped."
    else
        echo "No known cause recognised: the last lines above and the full log show the"
        echo "error of the command."
        echo "Fix: correct it and run the installer again; if it is not clear, report it"
        echo "     with $LOG (it contains no secrets)."
    fi
}

# --- helpers -----------------------------------------------------------------

cms() { "$PREFIX/cms" "$@"; }
# as_cms runs a cms command as the cms user (it owns the database and data).
as_cms() { runuser -u cms -- "$PREFIX/cms" "$@"; }

# apt-get waits for the lock of another package manager instead of failing.
apt_get() { apt-get -o DPkg::Lock::Timeout=600 "$@"; }

apt_install() {
    apt_get install -y --no-install-recommends "$@"
}

have_candidate() {
    local c
    c="$(apt-cache policy "$1" 2>/dev/null | awk '/Candidate:/ {print $2}' || true)"
    [[ -n "$c" && "$c" != "(none)" ]]
}

pkg_installed() { [[ "$(dpkg-query -W -f '${Status}' "$1" 2>/dev/null || true)" == *" installed" ]]; }

# kv_package prints the Valkey/Redis package the installer uses here.
kv_package() {
    if pkg_installed valkey-server || have_candidate valkey-server; then echo valkey-server; else echo redis-server; fi
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

# yaml_value prints the value of a key of cms.yaml (first occurrence).
yaml_value() {
    local v
    v="$(sed -n "s/^[[:space:]]*$1:[[:space:]]*\"\{0,1\}\([^\"#]*\)\"\{0,1\}.*/\1/p" "$ETC/cms.yaml" 2>/dev/null | head -n1 || true)"
    echo "${v%"${v##*[![:space:]]}"}"
}

read_os() {
    # Read os-release in a subshell: it defines VERSION, NAME, ID... and
    # must not overwrite the installer's own variables (--version).
    local os
    # shellcheck disable=SC1091
    os="$(. /etc/os-release && printf '%s\n%s\n%s\n' "${ID:-}" "${VERSION_ID:-}" "${VERSION_CODENAME:-}" || true)"
    { read -r OS_ID; read -r OS_VERSION; read -r CODENAME; } <<<"$os" || true
    ARCH="$(dpkg --print-architecture 2>/dev/null || uname -m)"
}

os_supported() {
    case "${OS_ID:-}:${OS_VERSION:-}" in
    ubuntu:22.04 | ubuntu:24.04 | ubuntu:26.04 | debian:12) return 0 ;;
    *) return 1 ;;
    esac
}

have_tty() { (exec </dev/tty) 2>/dev/null; }

confirm() {
    [[ "$YES" == 1 ]] && return 0
    have_tty || die "$1: there is no terminal to ask; add --yes to confirm"
    local ans
    read -r -p "$1 [type yes] " ans </dev/tty
    echo "answer: $ans"
    [[ "$ans" == yes ]]
}

# pg_query runs a query as postgres and prints the result (nothing if
# PostgreSQL is not installed or not running).
pg_query() {
    command -v psql >/dev/null || return 0
    runuser -u postgres -- psql -tAq "$@" 2>/dev/null || true
}

cms_db_exists() { [[ "$(pg_query -c "SELECT 1 FROM pg_database WHERE datname = 'cms'")" == 1 ]]; }

# foreign_cms_db succeeds when a database "cms" exists that this installer
# did not make (another application, the original CMS): it has tables but
# not our schema_migrations.checksum.
foreign_cms_db() {
    cms_db_exists &&
        [[ "$(pg_query -d cms -c "SELECT 1 FROM pg_tables WHERE schemaname = 'public' LIMIT 1")" == 1 ]] &&
        [[ "$(pg_query -d cms -c "SELECT 1 FROM information_schema.columns WHERE table_name = 'schema_migrations' AND column_name = 'checksum'")" != 1 ]]
}

# host_ip prints the address of the interface of the default route.
host_ip() {
    ip -4 route get 1.1.1.1 2>/dev/null | awk '{for (i=1;i<NF;i++) if ($i=="src") {print $(i+1); exit}}' || true
}

sshd_port() {
    local out p
    # sshd -T fails when sshd is missing or socket-activated without
    # /run/sshd: 22 is then the right guess.
    out="$(sshd -T 2>/dev/null || true)"
    p="$(awk '$1 == "port" {print $2; exit}' <<<"$out")"
    echo "${p:-22}"
}

# --- options and mode --------------------------------------------------------

parse_args() {
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
        --firewall) FIREWALL=on ;;
        --no-firewall) FIREWALL=off ;;
        --backup-keep) BACKUP_KEEP="${2:?}"; shift ;;
        --backup-calendar) BACKUP_CALENDAR="${2:?}"; shift ;;
        --check) CHECK_ONLY=1 ;;
        --clean) CLEAN=1 ;;
        --no-backup) NO_BACKUP=1 ;;
        --uninstall) UNINSTALL=1 ;;
        --purge) PURGE=1 ;;
        --yes | -y) YES=1 ;;
        --force-os) FORCE_OS=1 ;;
        -h | --help) usage; exit 0 ;;
        *) usage >&2; die "unknown option $1" ;;
        esac
        shift
    done
    if [[ "$UNINSTALL" == 1 && "$CLEAN$CHECK_ONLY" != 00 ]]; then die "--uninstall does not take --clean or --check"; fi
    if [[ "$CHECK_ONLY" == 1 && "$CLEAN" == 1 ]]; then die "use either --check or --clean"; fi
    if [[ "$PURGE" == 1 && "$UNINSTALL" == 0 ]]; then die "--purge goes with --uninstall (or use --clean)"; fi
    return 0
}

resolve_mode() {
    # Options of the previous run, reused when this one gives no mode.
    if [[ -r "$ETC/install.env" && -z "$DOMAIN" && -z "$HTTP_PORTS" && "$WORKER_ONLY" == 0 ]]; then
        # shellcheck disable=SC1091
        . "$ETC/install.env"
        DOMAIN="${INSTALL_DOMAIN:-}" ADMIN_DOMAIN="${INSTALL_ADMIN_DOMAIN:-}" RANKING_DOMAIN="${INSTALL_RANKING_DOMAIN:-}"
        HTTP_PORTS="${INSTALL_HTTP_PORTS:-}" WORKER_ONLY="${INSTALL_WORKER_ONLY:-0}"
        ALLOW_JUDGES="${ALLOW_JUDGES:-${INSTALL_ALLOW_JUDGES:-}}" ADMIN_ALLOW="${ADMIN_ALLOW:-${INSTALL_ADMIN_ALLOW:-}}"
        echo "==> reusing the mode of the previous installation ($ETC/install.env)" >&2
    fi
    if [[ "$WORKER_ONLY" == 1 ]]; then
        [[ -z "$DOMAIN$HTTP_PORTS" ]] || die "--worker-only does not take --domain or --http-ports"
        if [[ ! -f "$ETC/cms.yaml" || "$CLEAN" == 1 ]]; then
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
        local p
        for p in "$CONTEST_PORT" "$ADMIN_PORT" "$RANKING_PORT"; do
            [[ "$p" =~ ^[0-9]+$ && "$p" -gt 0 && "$p" -lt 65536 ]] || die "--http-ports wants three ports: CONTEST,ADMIN,RANKING"
        done
    fi
    [[ "$CONCURRENCY" =~ ^[1-9][0-9]*$ ]] || die "--judge-concurrency must be a positive number"
    [[ "$BACKUP_KEEP" =~ ^[0-9]+$ ]] || die "--backup-keep must be a number"
    [[ "$ADMIN_USER" =~ ^[A-Za-z0-9_-]+$ ]] || die "--admin-user may only have letters, digits, _ and -"
    case "$COMPILERS" in all | basic | none) ;; *) die "--compilers is all, basic or none" ;; esac
}

# --- preflight checks --------------------------------------------------------

PF_FAIL=0 PF_WARN=0

pf() { # PASS|WARN|FAIL name detail [fix]
    printf '  %-4s  %-24s %s\n' "$1" "$2" "$3"
    if [[ -n "${4:-}" ]]; then printf '        %-24s fix: %s\n' "" "$4"; fi
    case "$1" in
    FAIL) PF_FAIL=$((PF_FAIL + 1)) ;;
    WARN) PF_WARN=$((PF_WARN + 1)) ;;
    esac
}

needs_download() {
    [[ -z "$BINARY" ]] && [[ -n "$VERSION" || ! -x "$PREFIX/cms" || "$CLEAN" == 1 ]]
}

pf_system() {
    if [[ "$(id -u)" == 0 ]]; then pf PASS root "running as root"; else pf FAIL root "not running as root" "curl ... | sudo bash -s -- OPTIONS"; fi
    if os_supported; then
        pf PASS "operating system" "$OS_ID $OS_VERSION"
    elif [[ "$FORCE_OS" == 1 ]]; then
        pf WARN "operating system" "$OS_ID $OS_VERSION is not supported: trying because of --force-os"
    else
        pf FAIL "operating system" "${OS_ID:-unknown} ${OS_VERSION:-} is not supported" "use Ubuntu 22.04/24.04/26.04 or Debian 12 (or --force-os to try)"
    fi
    case "$ARCH" in
    amd64 | arm64) pf PASS architecture "$ARCH" ;;
    *) pf FAIL architecture "$ARCH is not supported" "use an amd64 or arm64 machine" ;;
    esac
    if [[ -d /run/systemd/system ]]; then
        pf PASS systemd "PID 1 is systemd"
    else
        pf FAIL systemd "PID 1 is not systemd (a container?)" "use a machine or VM with systemd, or the Docker images (docs/en/docker.md)"
    fi
}

pf_resources() {
    local kb gib
    kb="$(df -Pk /var 2>/dev/null | awk 'NR == 2 {print $4}' || true)"
    gib=$(((${kb:-0}) / 1048576))
    if [[ -z "$kb" ]]; then
        pf WARN "disk (/var)" "could not measure the free space"
    elif ((gib < MIN_DISK_GIB)); then
        pf FAIL "disk (/var)" "${gib} GiB free, at least $MIN_DISK_GIB GiB needed" "free space (sudo apt-get clean; sudo journalctl --vacuum-size=200M; docker system prune)"
    elif ((gib < 10)); then
        pf WARN "disk (/var)" "${gib} GiB free: enough to install, little for test data and submissions"
    else
        pf PASS "disk (/var)" "${gib} GiB free"
    fi
    kb="$(awk '/^MemTotal:/ {print $2}' /proc/meminfo 2>/dev/null || true)"
    if ((${kb:-0} < 1900000)); then
        pf WARN memory "$(((${kb:-0}) / 1024)) MiB: less than 2 GiB (4 GiB recommended for a contest)"
    else
        pf PASS memory "$(((${kb:-0}) / 1024)) MiB"
    fi
    if [[ -f /sys/fs/cgroup/cgroup.controllers ]]; then
        pf PASS "cgroups v2" "available (the judge limits memory with it)"
    else
        pf WARN "cgroups v2" "missing: the judge cannot limit memory reliably" "boot with systemd.unified_cgroup_hierarchy=1 (verify-host explains)"
    fi
}

pf_network() {
    local url="https://github.com/$REPO/releases" rc=0 out
    if ! needs_download; then
        pf PASS network "nothing to download from GitHub (apt still needs its mirrors)"
        return 0
    fi
    if ! getent hosts github.com >/dev/null 2>&1; then
        pf FAIL network "github.com does not resolve (DNS)" "check resolvectl status / /etc/resolv.conf, or install a local build with --binary PATH"
        return 0
    fi
    if ! command -v curl >/dev/null; then
        pf WARN network "curl is not installed yet: the download was not tested"
        return 0
    fi
    out="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 15 "$url" 2>&1)" || rc=$?
    if [[ "$rc" != 0 ]]; then
        pf FAIL network "cannot reach $url (curl error $rc)" "$(diagnose_curl "$rc" "$out" | sed -n 's/^Fix: *//p' | head -n1)"
    elif [[ "$out" =~ ^[23] ]]; then
        pf PASS network "GitHub reachable (the release is downloaded from it)"
    else
        pf FAIL network "GitHub answered HTTP $out (a proxy?)" "allow github.com and objects.githubusercontent.com, or use --binary PATH"
    fi
}

pf_apt() {
    if compgen -G '/var/lib/dpkg/updates/*' >/dev/null; then
        pf FAIL "packages (dpkg)" "a previous package installation was interrupted" "sudo dpkg --configure -a"
        return 0
    fi
    local holders=""
    if command -v lslocks >/dev/null; then
        holders="$(lslocks -n -o COMMAND,PID,PATH 2>/dev/null | awk '$3 ~ /^\/var\/lib\/(dpkg\/lock|apt\/lists\/lock)/ {print $1 " (pid " $2 ")"}' | sort -u | tr '\n' ' ' || true)"
    fi
    if [[ -n "$holders" ]]; then
        pf WARN "packages (apt)" "the apt/dpkg lock is held by $holders: the installer will wait for it (up to 10 minutes)"
    else
        pf PASS "packages (apt)" "no other package manager running"
    fi
}

# pf_port checks that a port is free or held by an allowed process.
pf_port() { # port label fix allowed-process...
    local port="$1" label="$2" fix="$3" h name
    shift 3
    h="$(port_holder "$port")"
    [[ -n "$h" ]] || return 0
    name="${h%% *}"
    local a
    for a in cms "$@"; do [[ "$name" == "$a" ]] && return 0; done
    pf FAIL "port $port ($label)" "used by $name (pid ${h##* })" "$fix"
}

pf_ports() {
    local before="$PF_FAIL" list
    if [[ "$WORKER_ONLY" == 1 ]]; then
        pf_port "${OPS_PORTS[worker]}" "judge health" "stop the program that uses it (cms needs it on 127.0.0.1)"
        list="${OPS_PORTS[worker]}"
    else
        if [[ -n "$DOMAIN" ]]; then
            local fix="the domain mode needs Caddy on ports 80 and 443: stop that program, or use --http-ports behind your own proxy"
            pf_port 80 "HTTP" "$fix" caddy
            pf_port 443 "HTTPS" "$fix" caddy
            pf_port 8888 "contest site" "stop the program that uses it (cms listens there on 127.0.0.1)"
            pf_port 8889 "admin site" "stop the program that uses it (cms listens there on 127.0.0.1)"
            pf_port 8890 "scoreboard" "stop the program that uses it (cms listens there on 127.0.0.1)"
            list="80, 443, 8888-8890"
        else
            local fix="choose other ports with --http-ports CONTEST,ADMIN,RANKING, or stop that program"
            pf_port "$CONTEST_PORT" "contest site" "$fix"
            pf_port "$ADMIN_PORT" "admin site" "$fix"
            pf_port "$RANKING_PORT" "scoreboard" "$fix"
            list="$CONTEST_PORT, $ADMIN_PORT, $RANKING_PORT"
        fi
        pf_port "$BLOB_PORT" "blob server" "stop the program that uses it (the dispatcher serves files there)"
        local svc
        for svc in contest-web admin-web ranking-web dispatcher worker monitor; do
            pf_port "${OPS_PORTS[$svc]}" "$svc health" "stop the program that uses it (cms needs it on 127.0.0.1)"
        done
        list="$list, $BLOB_PORT, 9188-9193"
    fi
    if [[ "$PF_FAIL" == "$before" ]]; then pf PASS ports "$list free or used by cms"; fi
}

pf_valkey() {
    local pkg h name configured=0
    pkg="$(kv_package)"
    if grep -qsE '^include /etc/(valkey|redis)/cms\.conf$' /etc/valkey/valkey.conf /etc/redis/redis.conf; then configured=1; fi
    h="$(port_holder 6379)"
    name="${h%% *}"
    if [[ -z "$h" ]]; then
        pf PASS "port 6379 (Valkey)" "free: $pkg will be installed"
    elif [[ "$name" == "$pkg" && "$configured" == 1 ]]; then
        pf PASS "port 6379 (Valkey)" "$name, configured for cms"
    elif [[ "$name" == "$pkg" ]]; then
        pf WARN "port 6379 (Valkey)" "$name runs without the cms settings: cms gives it a password, so other programs that use it without one would stop working"
    elif [[ "$name" == valkey-server || "$name" == redis-server ]]; then
        pf FAIL "port 6379 (Valkey)" "$name (pid ${h##* }) already listens; cms installs $pkg, which needs the port" \
            "if nothing else uses it: sudo systemctl disable --now $name (--clean does not remove other software)"
    else
        pf FAIL "port 6379 (Valkey)" "used by $name (pid ${h##* })" "stop it or move it to another port: cms needs a local Valkey on 6379"
    fi
}

pf_postgres() {
    local h name
    h="$(port_holder 5432)"
    name="${h%% *}"
    if [[ -n "$h" && "$name" != postgres ]]; then
        pf FAIL "port 5432 (PostgreSQL)" "used by $name (pid ${h##* }), not by the system PostgreSQL" \
            "stop it or publish it on another port (Docker: -p 5433:5432): cms uses the system PostgreSQL on 5432"
    elif [[ -z "$h" ]]; then
        pf PASS "PostgreSQL" "not running yet: it will be installed"
    elif foreign_cms_db; then
        if [[ "$CLEAN" == 1 ]]; then
            pf WARN "database \"cms\"" "made by another program: --clean backs it up to $BACKUPS and drops it"
        else
            pf FAIL "database \"cms\"" "exists and was not made by this installer (another program or the original CMS)" \
                "back it up and remove it (sudo -u postgres pg_dump cms | gzip > cms-old.sql.gz; sudo -u postgres dropdb cms), or use --clean (it backs it up first)"
        fi
    elif cms_db_exists; then
        if [[ "$CLEAN" == 1 ]]; then
            pf PASS "database \"cms\"" "of a previous installation: --clean backs it up and recreates it"
        else
            pf PASS "database \"cms\"" "of this installer: kept"
        fi
    else
        pf PASS "PostgreSQL" "running"
    fi
}

pf_dns() {
    local name ips locals
    locals=" $(ip -o addr show 2>/dev/null | awk '{sub(/\/.*/, "", $4); print $4}' | tr '\n' ' ' || true)"
    for name in "$DOMAIN" "$ADMIN_DOMAIN" "$RANKING_DOMAIN"; do
        ips="$(getent ahosts "$name" 2>/dev/null | awk '{print $1}' | sort -u | tr '\n' ' ' || true)"
        ips="${ips% }"
        if [[ -z "$ips" ]]; then
            pf WARN "DNS $name" "does not resolve" "create an A/AAAA record pointing to this machine (Caddy needs it for the certificate)"
            continue
        fi
        local ip here=0
        for ip in $ips; do [[ "$locals" == *" $ip "* ]] && here=1; done
        if [[ "$here" == 1 ]]; then
            pf PASS "DNS $name" "$ips (this machine)"
        else
            pf WARN "DNS $name" "$ips: not an address of this machine (fine behind NAT or port forwarding)"
        fi
    done
}

# preflight checks the machine before anything is changed. It returns 1
# when a check failed.
preflight() {
    log "preflight checks"
    PF_FAIL=0 PF_WARN=0
    read_os
    echo "  ----  ----------------------- ------"
    pf_system
    pf_resources
    pf_network
    pf_apt
    pf_ports
    if [[ "$WORKER_ONLY" == 0 ]]; then
        pf_valkey
        pf_postgres
    fi
    if [[ -n "$DOMAIN" ]]; then pf_dns; fi
    echo "  ----  ----------------------- ------"
    if [[ "$PF_FAIL" -gt 0 ]]; then
        echo "  $PF_FAIL check(s) FAILED, $PF_WARN warning(s). Nothing was changed: fix what the"
        echo "  'fix:' lines say and run the same command again."
        return 1
    fi
    echo "  All checks passed ($PF_WARN warning(s))."
}

# --- removal: --uninstall, --uninstall --purge and --clean -------------------

# cms_units prints the cms unit files installed on this machine.
cms_units() {
    local f
    for f in "$UNITDIR"/cms.target "$UNITDIR"/cms-*.service "$UNITDIR"/cms-*.timer; do
        if [[ -f "$f" ]]; then echo "${f##*/}"; fi
    done
}

# ufw_cms_rules turns `ufw show added` (stdin) into the cms rules to delete:
# those whose comment starts with "cms".
ufw_cms_rules() {
    sed -n "s/^ufw \(.*\) comment 'cms[^']*'\$/\1/p"
}

isolate_box_root() {
    local r
    r="$(sed -n 's/^[[:space:]]*box_root[[:space:]]*=[[:space:]]*\([^[:space:]]*\).*/\1/p' /etc/isolate /usr/local/etc/isolate 2>/dev/null | head -n1 || true)"
    echo "${r:-/var/local/lib/isolate}"
}

# cms_boxes prints the isolate boxes left in the range of the cms judge.
cms_boxes() {
    local root base count d id
    command -v isolate >/dev/null || return 0
    root="$(isolate_box_root)"
    base="$(yaml_value box_id_base)" count="$(yaml_value box_count)"
    [[ "$base" =~ ^[0-9]+$ ]] || base=0
    [[ "$count" =~ ^[0-9]+$ ]] || count=100
    for d in "$root"/*; do
        id="${d##*/}"
        if [[ -d "$d" && "$id" =~ ^[0-9]+$ ]] && ((id >= base && id < base + count)); then echo "$id"; fi
    done
}

# kv_conf_dirs prints the Valkey/Redis directories with the cms settings.
kv_conf_dirs() {
    local d
    for d in /etc/valkey /etc/redis; do
        if [[ -f "$d/cms.conf" ]]; then echo "$d"; fi
    done
}

# list_traces fills TRACES with what a purge deletes on this machine.
list_traces() {
    TRACES=()
    local units f d n
    units="$(cms_units | tr '\n' ' ')"
    [[ -z "$units" ]] || TRACES+=("systemd units (stopped and disabled): $units")
    for f in "$ETC" "$DATA" "$CACHE" /var/log/cms; do
        if [[ -e "$f" ]]; then TRACES+=("$f ($(du -sh "$f" 2>/dev/null | cut -f1 || true))"); fi
    done
    for f in "$PREFIX/cms" "$PREFIX/cms.prev" "$PREFIX/cms.new" "$PREFIX/cmsctl"; do
        if [[ -e "$f" || -L "$f" ]]; then TRACES+=("$f"); fi
    done
    if cms_db_exists; then
        if foreign_cms_db; then
            TRACES+=("the PostgreSQL database \"cms\" (made by ANOTHER program) and the role \"cms\"")
        else
            TRACES+=("the PostgreSQL database \"cms\" and the role \"cms\"")
        fi
    elif [[ "$(pg_query -c "SELECT 1 FROM pg_roles WHERE rolname = 'cms'")" == 1 ]]; then
        TRACES+=("the PostgreSQL role \"cms\"")
    fi
    for d in $(kv_conf_dirs); do
        TRACES+=("the cms settings of Valkey/Redis ($d/cms.conf and its include) and the keys \"$(kv_prefix)*\" stored in it")
    done
    n="$(cms_boxes | wc -l)"
    [[ "$n" == 0 ]] || TRACES+=("$n leftover isolate box(es) of the judge")
    [[ ! -f /etc/caddy/cms.caddy ]] || TRACES+=("the Caddy site /etc/caddy/cms.caddy")
    if command -v ufw >/dev/null; then
        n="$(LC_ALL=C ufw show added 2>/dev/null | ufw_cms_rules | wc -l || true)"
        [[ "$n" == 0 ]] || TRACES+=("$n ufw rule(s) commented \"cms\"")
    fi
    [[ ! -f /etc/sysctl.d/60-cms-valkey.conf ]] || TRACES+=("/etc/sysctl.d/60-cms-valkey.conf")
    for f in /etc/postgresql/*/main/conf.d/cms.conf; do
        if [[ -f "$f" ]]; then TRACES+=("$f (PostgreSQL tuning)"); fi
    done
    if id cms >/dev/null 2>&1; then TRACES+=("the system user and group \"cms\""); fi
}

kv_prefix() {
    local p
    p="$(yaml_value key_prefix)"
    echo "${p:-cms:}"
}

# backup_before_delete keeps the database (even a foreign one) and /etc/cms
# in $BACKUPS before a purge.
backup_before_delete() { # tag
    local ts base
    ts="$(date +%Y%m%d-%H%M%S)"
    base="$BACKUPS/pre-$1-$ts"
    install -d -m 0700 "$BACKUPS"
    BACKUP_FILES=()
    if cms_db_exists; then
        log "backing up the database \"cms\" to $base.sql.gz"
        (umask 077; runuser -u postgres -- pg_dump cms | gzip >"$base.sql.gz")
        BACKUP_FILES+=("$base.sql.gz")
    fi
    if [[ -d "$ETC" ]]; then
        log "backing up $ETC to $base-etc-cms.tar.gz"
        (umask 077; tar -czf "$base-etc-cms.tar.gz" -C / "${ETC#/}")
        BACKUP_FILES+=("$base-etc-cms.tar.gz")
    fi
    if ((${#BACKUP_FILES[@]})); then
        echo "  backup: ${BACKUP_FILES[*]}"
    fi
}

remove_units() {
    local units=()
    mapfile -t units < <(cms_units)
    if ((${#units[@]})); then
        log "stopping and removing the cms services"
        systemctl disable --now "${units[@]}"
        local u
        for u in "${units[@]}"; do rm -f "$UNITDIR/$u"; done
    fi
    rm -rf "$UNITDIR"/cms.target.d "$UNITDIR"/cms-*.service.d "$UNITDIR"/cms-*.timer.d
    systemctl daemon-reload
    systemctl reset-failed 'cms*' 2>/dev/null || true
}

caddy_forget_cms() {
    if [[ -f /etc/caddy/cms.caddy ]] || grep -qsx 'import /etc/caddy/cms.caddy' /etc/caddy/Caddyfile; then
        log "removing the cms site of Caddy"
        rm -f /etc/caddy/cms.caddy
        if grep -qs '^# Written by the cms installer' /etc/caddy/Caddyfile && [[ -f /etc/caddy/Caddyfile.orig ]]; then
            mv -f /etc/caddy/Caddyfile.orig /etc/caddy/Caddyfile
        elif [[ -f /etc/caddy/Caddyfile ]]; then
            sed -i '\#^import /etc/caddy/cms.caddy$#d' /etc/caddy/Caddyfile
        fi
        if systemctl -q is-active caddy; then systemctl reload caddy || warn "Caddy did not reload: sudo systemctl status caddy"; fi
    fi
    return 0
}

drop_cms_database() {
    command -v psql >/dev/null || return 0
    if [[ "$(pg_query -c "SELECT 1")" != 1 ]]; then
        warn "PostgreSQL is not running: the database and role \"cms\" (if any) were not dropped"
        return 0
    fi
    log "dropping the PostgreSQL database and role \"cms\""
    runuser -u postgres -- psql -q -v ON_ERROR_STOP=1 <<'SQL'
DROP DATABASE IF EXISTS cms WITH (FORCE);
DROP ROLE IF EXISTS cms;
SQL
}

# delete_kv_keys deletes the keys under the cms prefix only: Valkey may hold
# data of other programs, so FLUSHALL is never used.
delete_kv_keys() { # cli password prefix
    local cli="$1" pattern envs=()
    if [[ -z "$3" ]]; then
        warn "no key prefix in $ETC/cms.yaml: the keys in Valkey were not deleted"
        return 0
    fi
    pattern="$(sed 's/[][*?\\]/\\&/g' <<<"$3")*"
    if [[ -n "$2" ]]; then envs=(REDISCLI_AUTH="$2"); fi
    env "${envs[@]}" "$cli" --no-auth-warning --scan --pattern "$pattern" |
        env "${envs[@]}" xargs -r -d '\n' -n 500 "$cli" --no-auth-warning UNLINK >/dev/null
    # Valkey loads the RDB file once appendonly is off: save the rest first.
    env "${envs[@]}" "$cli" --no-auth-warning SAVE >/dev/null || warn "Valkey could not save its data (SAVE)"
}

valkey_forget_cms() {
    local dir conf svc cli pw
    for dir in $(kv_conf_dirs); do
        if [[ "$dir" == /etc/valkey ]]; then
            conf=$dir/valkey.conf svc=valkey-server cli=valkey-cli
        else
            conf=$dir/redis.conf svc=redis-server cli=redis-cli
        fi
        pw="$(sed -n 's/^requirepass //p' "$dir/cms.conf" | head -n1 || true)"
        if command -v "$cli" >/dev/null && systemctl -q is-active "$svc"; then
            log "deleting the cms keys ($(kv_prefix)*) from $svc"
            delete_kv_keys "$cli" "$pw" "$(kv_prefix)"
        else
            warn "$svc is not running: the cms keys stored in it were not deleted"
        fi
        log "removing the cms settings of $svc"
        if [[ -f "$conf" ]]; then sed -i "\\#^include $dir/cms.conf\$#d" "$conf"; fi
        rm -f "$dir/cms.conf"
        if systemctl -q is-active "$svc"; then systemctl restart "$svc"; fi
    done
}

clean_isolate_boxes() {
    local id
    for id in $(cms_boxes); do
        isolate --box-id="$id" --cleanup >/dev/null 2>&1 ||
            isolate --cg --box-id="$id" --cleanup || warn "could not clean the isolate box $id"
    done
}

ufw_forget_cms() {
    command -v ufw >/dev/null || return 0
    local rules=() rule args
    mapfile -t rules < <(LC_ALL=C ufw show added 2>/dev/null | ufw_cms_rules || true)
    if ((${#rules[@]})); then log "removing the ufw rules of cms"; fi
    for rule in "${rules[@]}"; do
        read -ra args <<<"$rule"
        ufw --force delete "${args[@]}"
    done
}

# remove_cms removes the services, the binary and the Caddy site; with
# "purge", every other trace of cms too. Other software and apt packages
# (PostgreSQL, Valkey, Caddy, isolate, compilers) are never removed.
remove_cms() { # [purge]
    remove_units
    rm -f "$PREFIX/cms" "$PREFIX/cms.prev" "$PREFIX/cms.new" "$PREFIX/cmsctl"
    caddy_forget_cms
    [[ "${1:-}" == purge ]] || return 0
    # cms.yaml is read here (key prefix, box ids): before deleting $ETC.
    drop_cms_database
    valkey_forget_cms
    clean_isolate_boxes
    ufw_forget_cms
    rm -f /etc/sysctl.d/60-cms-valkey.conf /etc/postgresql/*/main/conf.d/cms.conf
    log "deleting $ETC, $DATA, $CACHE"
    rm -rf "$ETC" "$DATA" "$CACHE" /var/log/cms
    if id cms >/dev/null 2>&1; then userdel cms; fi
    if getent group cms >/dev/null; then groupdel cms; fi
}

# purge asks for confirmation listing what will be deleted, backs it up and
# deletes it. It returns 1 when there was nothing to delete.
purge() { # tag question
    list_traces
    if ((${#TRACES[@]} == 0)); then
        echo "==> no trace of a cms installation on this machine"
        return 1
    fi
    echo "This DELETES:"
    printf '  - %s\n' "${TRACES[@]}"
    if [[ "$NO_BACKUP" == 0 ]]; then
        echo "A backup of the database \"cms\" and of $ETC goes first to $BACKUPS."
    else
        echo "No backup is made (--no-backup)."
    fi
    echo "Kept: PostgreSQL, Valkey/Redis, Caddy, isolate and the compilers (apt packages), $BACKUPS, $LOG."
    confirm "$2" || die "not confirmed: nothing was deleted"
    if [[ "$NO_BACKUP" == 0 ]]; then backup_before_delete "$1"; fi
    remove_cms purge
}

uninstall() {
    if [[ "$PURGE" == 1 ]]; then
        log "uninstalling cms and deleting its data (--purge)"
        if purge purge "Delete all of the above?"; then
            log "purged. PostgreSQL, Valkey/Redis, Caddy, isolate and the compilers are still installed (apt remove them if unused)"
        fi
    else
        log "uninstalling cms (services, binary, Caddy site)"
        remove_cms
        log "removed the services and the binary. Kept: $ETC (configuration and secrets), $DATA (blobs, backups), the database"
        log "run again with --uninstall --purge to delete them"
    fi
}

clean_install() {
    log "clean install: removing previous cms installations"
    if purge clean "Delete all of the above and install cms from zero?"; then
        log "previous installation removed: installing from zero"
    fi
    return 0
}

# --- packages ----------------------------------------------------------------

install_base() {
    log "installing base packages"
    apt_get update -q
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
        echo "deb [arch=amd64 signed-by=/etc/apt/keyrings/isolate.asc] https://www.ucw.cz/isolate/debian/ $suite main" \
            >/etc/apt/sources.list.d/isolate.list
        apt_get update -q
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
    [[ "$COMPILERS" != none ]] || return 0
    log "installing compilers ($COMPILERS)"
    local pkgs=(g++ gcc python3 default-jdk-headless)
    if [[ "$COMPILERS" == all ]]; then
        pkgs+=(pypy3 fp-compiler ghc mono-mcs mono-runtime rustc golang-go php-cli kotlin)
    fi
    local avail=() p
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
    ver="$(find /etc/postgresql -mindepth 1 -maxdepth 1 -printf '%f\n' 2>/dev/null | sort -V | tail -n1 || true)"
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
    # A database "cms" made by something else (the original CMS, another
    # application) is never reused or touched, nor is its role.
    if foreign_cms_db; then
        die "a PostgreSQL database named \"cms\" already exists and was not made by this installer (another application or the original CMS). Back it up and remove it (sudo -u postgres pg_dump cms | gzip > cms-old.sql.gz; sudo -u postgres dropdb cms), or run the installer with --clean"
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
    if ! cms_db_exists; then
        runuser -u postgres -- psql -q -v ON_ERROR_STOP=1 -c \
            "CREATE DATABASE cms OWNER cms TEMPLATE template0 ENCODING 'UTF8' LC_COLLATE '$collate' LC_CTYPE '$collate'"
    fi
}

install_valkey() {
    local pkg conf dir user svc
    if have_candidate valkey-server || pkg_installed valkey-server; then
        pkg=valkey-server conf=/etc/valkey/valkey.conf dir=/etc/valkey user=valkey svc=valkey-server
    else
        # Redis >= 7 works too (Ubuntu 24.04, Debian 12); Ubuntu 22.04 only
        # has 6.0, so the Redis repository is used there.
        if [[ "$CODENAME" == jammy ]] && ! pkg_installed redis-server; then
            log "adding the Redis repository (Ubuntu 22.04 ships Redis 6, cms needs >= 7)"
            curl -fsSL https://packages.redis.io/gpg | gpg --dearmor --yes -o /usr/share/keyrings/redis-archive-keyring.gpg
            echo "deb [signed-by=/usr/share/keyrings/redis-archive-keyring.gpg] https://packages.redis.io/deb $CODENAME main" \
                >/etc/apt/sources.list.d/redis.list
            apt_get update -q
        fi
        pkg=redis-server conf=/etc/redis/redis.conf dir=/etc/redis user=redis svc=redis-server
    fi
    # Another Redis/Valkey on the port makes the package fail to start.
    local holder
    holder="$(port_holder 6379)"
    if [[ -n "$holder" && "${holder%% *}" != "$pkg" ]]; then
        die "port 6379 is already used by \"${holder%% *}\" (pid ${holder##* }; another Redis or Valkey). Stop it (for redis-server: sudo systemctl disable --now redis-server) and run the installer again"
    fi
    log "installing $pkg"
    apt_install "$pkg"
    # Valkey/Redis warn and may fail background saves without it.
    if [[ "$(sysctl -n vm.overcommit_memory 2>/dev/null || true)" != 1 ]]; then
        echo 'vm.overcommit_memory = 1' >/etc/sysctl.d/60-cms-valkey.conf
        sysctl -q -p /etc/sysctl.d/60-cms-valkey.conf || warn "could not set vm.overcommit_memory=1"
    fi
    local pw bind="127.0.0.1 -::1"
    pw="$(secret valkey_password)"
    if [[ -n "$ALLOW_JUDGES" ]]; then bind="* -::*"; fi
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
    chown "root:$user" "$dir/cms.conf"
    grep -qx "include $dir/cms.conf" "$conf" || { echo "include $dir/cms.conf" >>"$conf"; KV_CHANGED=1; }
    systemctl enable "$svc"
    if [[ "${KV_CHANGED:-0}" == 1 ]]; then systemctl restart "$svc"; else systemctl start "$svc"; fi
}

install_caddy() {
    if ! command -v caddy >/dev/null; then
        log "installing Caddy"
        if ! have_candidate caddy; then
            curl -fsSL https://dl.cloudsmith.io/public/caddy/stable/gpg.key | gpg --dearmor --yes -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
            curl -fsSL https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt >/etc/apt/sources.list.d/caddy-stable.list
            apt_get update -q
        fi
        apt_install caddy
    fi
    log "configuring Caddy"
    local admin_guard=""
    if [[ -n "$ADMIN_ALLOW" ]]; then
        admin_guard="	@outside not remote_ip $ADMIN_ALLOW
	respond @outside 403"
    fi
    local email=""
    if [[ -n "$ACME_EMAIL" ]]; then
        email="{
	email $ACME_EMAIL
}
"
    fi
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
        if [[ -f /etc/caddy/Caddyfile ]]; then cp -n /etc/caddy/Caddyfile /etc/caddy/Caddyfile.orig; fi
        printf '%s# Written by the cms installer; the cms sites are in cms.caddy.\nimport /etc/caddy/cms.caddy\n' "$email" >/etc/caddy/Caddyfile
    elif ! grep -qx 'import /etc/caddy/cms.caddy' /etc/caddy/Caddyfile; then
        echo 'import /etc/caddy/cms.caddy' >>/etc/caddy/Caddyfile
        if [[ -n "$email" ]]; then warn "your Caddyfile is custom: set the ACME email in its global options yourself"; fi
    fi
    local out
    if ! out="$(caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile 2>&1)"; then
        tail -n 20 <<<"$out" >&2
        die "the Caddy configuration is invalid (caddy validate --config /etc/caddy/Caddyfile)"
    fi
    systemctl enable caddy
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
        log "keeping the installed $(installed_version) (cmsctl upgrade upgrades it)"
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
    log "creating the cms user and directories"
    getent group cms >/dev/null || groupadd --system cms
    id cms >/dev/null 2>&1 || useradd --system --gid cms --home-dir "$DATA" --no-create-home --shell /usr/sbin/nologin cms
    install -d -m 0750 -o root -g cms "$ETC"
    install -d -m 0750 -o root -g cms "$SECRETS"
    install -d -m 0750 -o cms -g cms "$DATA" "$DATA/blobs" "$DATA/ranking" "$CACHE" "$CACHE/blobs"
    install -d -m 0700 -o cms -g cms "$DATA/backups"
    install -d -m 0755 -o root -g root "$DATA/worker"
    install -d -m 0700 -o root -g root "$CACHE/worker"
}

server_timezone() {
    local tz
    tz="$(timedatectl show -p Timezone --value 2>/dev/null || true)"
    if [[ -z "$tz" && -f /etc/timezone ]]; then tz="$(cat /etc/timezone)"; fi
    [[ -n "$tz" && -f "/usr/share/zoneinfo/$tz" ]] || tz=UTC
    echo "$tz"
}

write_config() {
    local ranking_port=8890
    if [[ -n "$HTTP_PORTS" ]]; then ranking_port="$RANKING_PORT"; fi
    if [[ -f "$ETC/cms.yaml" ]]; then
        log "keeping $ETC/cms.yaml"
        if [[ -f "$ETC/install.env" ]] && ! grep -q "^INSTALL_RANKING_PORT=$ranking_port\$" "$ETC/install.env"; then
            warn "the scoreboard port changed: update dispatcher.rankings[0].url in $ETC/cms.yaml (http://127.0.0.1:$ranking_port/)"
        fi
        return 0
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
        return 0
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
    log "writing $ETC/cms.env and the token of the local judge"
    local contest admin ranking proxies=0 blob=127.0.0.1:$BLOB_PORT
    if [[ -n "$DOMAIN" ]]; then
        contest=127.0.0.1:8888 admin=127.0.0.1:8889 ranking=127.0.0.1:8890 proxies=1
    else
        contest=0.0.0.0:$CONTEST_PORT admin=0.0.0.0:$ADMIN_PORT ranking=0.0.0.0:$RANKING_PORT
    fi
    if [[ -n "$ALLOW_JUDGES" ]]; then blob=0.0.0.0:$BLOB_PORT; fi
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
    if [[ "$WORKER_ONLY" == 0 ]]; then worker_env="EnvironmentFile=$ETC/worker.env"; fi
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
    log "enabling and starting the services"
    local units=(cms-worker.service)
    if [[ "$WORKER_ONLY" == 0 ]]; then
        units+=(cms-contest-web.service cms-admin-web.service cms-ranking-web.service cms-dispatcher.service cms-monitor.service)
    fi
    systemctl enable cms.target "${units[@]}"
    if [[ "$WORKER_ONLY" == 0 && "$BACKUP_KEEP" -gt 0 ]]; then
        systemctl enable --now cms-backup.timer
    elif [[ -f "$UNITDIR/cms-backup.timer" ]]; then
        systemctl disable --now cms-backup.timer
    fi
    systemctl restart "${units[@]}"
}

# --- firewall ----------------------------------------------------------------

# firewall_rules prints the ufw rules of this installation, one per line:
# "ufw arguments|comment".
firewall_rules() {
    echo "allow $(sshd_port)/tcp|ssh"
    [[ "$WORKER_ONLY" == 0 ]] || return 0
    if [[ -n "$DOMAIN" ]]; then
        echo "allow 80/tcp|cms (caddy)"
        echo "allow 443/tcp|cms (caddy)"
        echo "allow 443/udp|cms (caddy, HTTP/3)"
    else
        echo "allow $CONTEST_PORT/tcp|cms contest site"
        if [[ -n "$ADMIN_ALLOW" ]]; then
            echo "allow from $ADMIN_ALLOW to any port $ADMIN_PORT proto tcp|cms admin"
        else
            echo "allow $ADMIN_PORT/tcp|cms admin"
        fi
        echo "allow $RANKING_PORT/tcp|cms scoreboard"
    fi
    if [[ -n "$ALLOW_JUDGES" ]]; then
        echo "allow from $ALLOW_JUDGES to any port 6379 proto tcp|cms judges: valkey"
        echo "allow from $ALLOW_JUDGES to any port $BLOB_PORT proto tcp|cms judges: blobs"
    fi
}

firewall_note() {
    echo " ufw is not active, so the installer left it off (enabling it could block"
    echo " your other services). If you enable it later, allow these ports first:"
    local spec comment
    while IFS='|' read -r spec comment; do
        echo "   sudo ufw $spec comment '$comment'"
    done < <(firewall_rules)
    echo "   sudo ufw enable"
    echo " (or run the installer again with --firewall)"
}

FIREWALL_NOTE=""
setup_firewall() {
    [[ "$FIREWALL" != off ]] || return 0
    local status="" active=0
    if command -v ufw >/dev/null; then status="$(LC_ALL=C ufw status 2>&1 || true)"; fi
    if [[ "$status" == *"Status: active"* ]]; then active=1; fi
    if [[ "$FIREWALL" == auto && "$active" == 0 ]]; then
        log "ufw is not active: left off (the summary says how to open the ports)"
        FIREWALL_NOTE="$(firewall_note)"
        return 0
    fi
    log "configuring the firewall (ufw)"
    if ! command -v ufw >/dev/null; then apt_install ufw; fi
    # Never lock out the administrator: SSH first.
    local spec comment args
    while IFS='|' read -r spec comment; do
        read -ra args <<<"$spec"
        ufw "${args[@]}" comment "$comment" >/dev/null
    done < <(firewall_rules)
    if [[ "$active" == 0 ]]; then ufw --force enable; fi
}

# --- database, first admin, checks -------------------------------------------

ADMIN_PASSWORD=""
setup_database() {
    log "applying the database migrations"
    as_cms --config "$ETC/cms.yaml" migrate
    log "creating the first administrator if missing"
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

HEALTHY=1
wait_healthy() {
    log "waiting for the services to answer /healthz"
    local list=(worker) pending still svc port deadline=$((SECONDS + 90))
    if [[ "$WORKER_ONLY" == 0 ]]; then list=(contest-web admin-web ranking-web dispatcher worker monitor); fi
    pending=("${list[@]}")
    while ((${#pending[@]})) && ((SECONDS < deadline)); do
        still=()
        for svc in "${pending[@]}"; do
            curl -fsS -o /dev/null --max-time 3 --noproxy '*' "http://127.0.0.1:${OPS_PORTS[$svc]}/healthz" 2>/dev/null || still+=("$svc")
        done
        pending=("${still[@]}")
        if ((${#pending[@]})); then sleep 2; fi
    done
    HEALTHY=1
    for svc in "${pending[@]}"; do
        HEALTHY=0
        port="${OPS_PORTS[$svc]}"
        local journal
        journal="$(journalctl -u "cms-$svc" -n 15 --no-pager -o cat 2>&1 | filter_secrets || true)"
        warn "cms-$svc is not healthy ($(systemctl is-active "cms-$svc" 2>&1 || true); http://127.0.0.1:$port/healthz does not answer)"
        echo "  last lines of its journal:"
        sed 's/^/  | /' <<<"$journal"
        diagnose 0 "" "$journal" | sed -n '/^Fix:/,$p' | sed 's/^/  /'
        echo "  after fixing it: sudo systemctl restart cms-$svc (or run the installer again)"
    done
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

DOCTOR="not run" DOCTOR_FAILED=0
run_doctor() {
    if ! "$PREFIX/cms" ctl doctor --help >/dev/null 2>&1; then
        DOCTOR="not available in this cms version"
        return 0
    fi
    log "checking the installation (cmsctl doctor)"
    local rc=0
    timeout 120 "$PREFIX/cms" --config "$ETC/cms.yaml" ctl doctor || rc=$?
    case "$rc" in
    0) DOCTOR="passed (see above for warnings)" ;;
    1) DOCTOR="FAILED: see the FAIL lines of cmsctl doctor above" DOCTOR_FAILED=1 ;;
    *) DOCTOR="could not run (exit code $rc, see above)" DOCTOR_FAILED=1 ;;
    esac
}

summary() {
    local version state=installed
    version="$(installed_version)"
    if [[ "$DOCTOR_FAILED" == 1 || "$HEALTHY" == 0 ]]; then state="installed with problems"; fi
    echo
    echo "======================================================================"
    echo " $version $state"
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
            echo " First administrator: $ADMIN_USER (its password is printed at the very end,"
            echo " on the terminal only)"
        else
            echo " Administrator '$ADMIN_USER' already existed: its password was not changed."
            echo " To set a new one: sudo cmsctl admin-password $ADMIN_USER"
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
    if [[ -n "$FIREWALL_NOTE" ]]; then
        echo
        echo "$FIREWALL_NOTE"
    fi
    echo
    echo " Judge check:    $VERIFY_HOST"
    echo " Doctor:         $DOCTOR"
    if [[ "$HEALTHY" == 0 ]]; then echo " Some services are not healthy: see the warnings above (journal and fix)."; fi
    echo " Install log:    $LOG"
    echo "======================================================================"
}

# print_admin_password shows the generated password on the terminal only
# (fd 3): it never reaches the log.
print_admin_password() {
    [[ -n "$ADMIN_PASSWORD" ]] || return 0
    sleep 0.3 # let tee print the summary first
    {
        echo
        echo " First administrator: $ADMIN_USER"
        echo " Password:            $ADMIN_PASSWORD"
        echo " It is shown only now and stored nowhere in clear (not in the log): write it down."
        echo
    } >&3
}

# --- main --------------------------------------------------------------------

# english_messages makes apt, psql, systemctl... speak English, so that the
# diagnosis recognises their messages; the rest of the locale is kept.
english_messages() {
    if [[ -n "${LC_ALL:-}" ]]; then
        export LANG="$LC_ALL"
        unset LC_ALL
    fi
    export LC_MESSAGES=C
    unset LANGUAGE
}

main() {
    parse_args "$@"
    cd /
    if [[ "$(id -u)" != 0 ]]; then
        if [[ "$CHECK_ONLY" == 1 ]]; then
            resolve_mode
            preflight || exit 1
            exit 0
        fi
        die "run as root (curl ... | sudo bash -s -- OPTIONS)"
    fi
    export DEBIAN_FRONTEND=noninteractive
    english_messages
    umask 022
    FAIL_NOTE="$(mktemp)"
    trap on_exit EXIT
    start_log "$@"
    trap 'on_err $? $LINENO "$BASH_COMMAND"' ERR
    trap 'on_signal INT 130' INT
    trap 'on_signal TERM 143' TERM
    trap 'on_signal HUP 129' HUP

    if [[ "$UNINSTALL" == 1 ]]; then
        uninstall
        exit 0
    fi
    resolve_mode
    if ! preflight; then
        trap - ERR
        exit 1
    fi
    if [[ "$CHECK_ONLY" == 1 ]]; then exit 0; fi
    if [[ "$CLEAN" == 1 ]]; then clean_install; fi

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
        if [[ -n "$DOMAIN" ]]; then install_caddy; fi
    fi
    write_install_env
    write_units
    setup_firewall
    enable_units
    wait_healthy
    verify_host
    run_doctor
    summary
    print_admin_password
}

# CMS_INSTALL_SOURCE_ONLY=1 only defines the functions (tests source it).
if [[ "${CMS_INSTALL_SOURCE_ONLY:-0}" != 1 ]]; then
    main "$@"
fi
