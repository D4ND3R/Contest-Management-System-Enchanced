# syntax=docker/dockerfile:1
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
#
# Image of the cms binary for every service except judging (the worker
# image, Dockerfile.worker, adds isolate and the compilers).
#
# The binary is built outside and copied in (D15): builds are reproducible
# and need no network.
#
#   make build && docker build -f deploy/docker/Dockerfile -t cms .
#
# Multi-arch release images (.github/workflows/images.yml) take one binary
# per architecture from dist/linux_<arch>/cms:
#
#   docker buildx build --platform linux/amd64,linux/arm64 \
#     --build-arg BINARY_SOURCE=release -f deploy/docker/Dockerfile .
ARG BINARY_SOURCE=local

FROM scratch AS bin-local
COPY bin/cms /cms

FROM scratch AS bin-release
ARG TARGETARCH
COPY dist/linux_${TARGETARCH}/cms /cms

FROM bin-${BINARY_SOURCE} AS bin

# Directories owned by nonroot: new named volumes mounted there (Compose)
# inherit the ownership. distroless has no shell to create them.
FROM busybox:1.36 AS dirs
RUN mkdir -p /out/var/lib/cms/blobs /out/var/lib/cms/ranking /out/var/lib/cms/backups /out/var/cache/cms /out/backups

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=dirs --chown=65532:65532 /out/ /
COPY --from=bin /cms /usr/local/bin/cms
COPY LICENSE.txt AUTHORS.txt /usr/share/doc/cms/
COPY config/cms.sample.yaml /usr/share/doc/cms/
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/cms"]
CMD ["--help"]
