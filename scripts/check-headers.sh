#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
#
# Every Go file carries the AGPL SPDX identifier in its first lines. Files
# ported from CMS also keep the copyright lines of their original authors.
set -euo pipefail
cd "$(dirname "$0")/.."

files=$(git ls-files '*.go' 2>/dev/null || true)
[[ -n "$files" ]] || files=$(find . -name '*.go' -not -path './.infra/*' -not -path './.dev/*')
bad=0
for f in $files; do
    if ! head -n 4 "$f" | grep -q 'SPDX-License-Identifier: AGPL-3.0-or-later'; then
        echo "missing license header: $f"
        bad=1
    fi
done
exit $bad
