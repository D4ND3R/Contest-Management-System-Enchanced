#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
#
# Entry point of the worker image. With cgroups v2 it does inside the
# container what isolate.service does on a systemd host: it runs
# isolate-cg-keeper in a cgroup of its own, which delegates the cpuset and
# memory controllers to isolate and writes /run/isolate/cgroup. The
# processes of the container first leave its root cgroup, because a cgroup
# that holds processes cannot delegate controllers to its children.
set -eu

CG=/sys/fs/cgroup

cgroups_wanted() {
    # The values strconv.ParseBool reads as false.
    case "${CMS_SANDBOX_CGROUPS:-true}" in
    0 | f | F | false | FALSE | False) return 1 ;;
    esac
    return 0
}

start_cg_keeper() {
    if [ ! -f "$CG/cgroup.controllers" ]; then
        echo "cms-worker-entrypoint: no cgroups v2 at $CG (development hosts: CMS_SANDBOX_CGROUPS=false)" >&2
        return 1
    fi
    # /run survives container restarts; the cgroup tree does not.
    rm -f /run/isolate/cgroup
    mkdir -p "$CG/init" "$CG/isolate"
    for pid in $(cat "$CG/cgroup.procs"); do
        echo "$pid" >"$CG/init/cgroup.procs" 2>/dev/null || true
    done
    echo "+cpuset +memory" >"$CG/cgroup.subtree_control"
    sh -c "echo \$\$ >$CG/isolate/cgroup.procs && exec /usr/sbin/isolate-cg-keeper" &
    i=0
    while [ ! -s /run/isolate/cgroup ]; do
        i=$((i + 1))
        if [ "$i" -gt 50 ] || ! kill -0 $! 2>/dev/null; then
            echo "cms-worker-entrypoint: isolate-cg-keeper did not start" >&2
            return 1
        fi
        sleep 0.1
    done
}

if cgroups_wanted; then
    # On failure the worker's sandbox self-test reports the problem.
    start_cg_keeper || true
fi
exec /usr/local/bin/cms "$@"
