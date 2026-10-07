# syntax=docker/dockerfile:1
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
#
# Image of the cms binary for every service except judging (the worker
# image with isolate and the compilers arrives in phase F2).
#
# The binary is built outside (`make build`, or GoReleaser for releases)
# and copied in: builds are reproducible and need no network.
#
#   make build && docker build -f deploy/docker/Dockerfile -t cms .
FROM gcr.io/distroless/static-debian12:nonroot
COPY bin/cms /usr/local/bin/cms
COPY LICENSE.txt AUTHORS.txt /usr/share/doc/cms/
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/cms"]
CMD ["--help"]
