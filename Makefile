# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

SHELL := /bin/bash
.DEFAULT_GOAL := build

MODULE  := github.com/d4nd3r/contest-management-system-enchanced
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --verify -q HEAD 2>/dev/null)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X $(MODULE)/internal/buildinfo.Version=$(VERSION) \
	-X $(MODULE)/internal/buildinfo.Commit=$(COMMIT) \
	-X $(MODULE)/internal/buildinfo.Date=$(DATE)

GO      ?= go
# Directories holding Go code (gofmt never reads stdin with an explicit list).
GO_DIRS := cmd internal locale
GOFLAGS ?=
# Packages run under the race detector by `make test`.
TESTFLAGS ?= -race -count=1

.PHONY: build
build: ## build bin/cms and the bin/cmsctl alias
	CGO_ENABLED=0 $(GO) build $(GOFLAGS) -trimpath -ldflags '$(LDFLAGS)' -o bin/cms ./cmd/cms
	ln -sf cms bin/cmsctl

.PHONY: test
test: ## run every test against disposable PostgreSQL and Valkey
	@if [[ -z "$$CMS_TEST_DATABASE_URL" ]]; then scripts/infra.sh up; fi
	@if [[ -z "$$CMS_TEST_DATABASE_URL" ]]; then source .infra/env; fi; \
	CMS_TEST_REQUIRE_INFRA=1 $(GO) test $(GOFLAGS) $(TESTFLAGS) ./...

.PHONY: test-short
test-short: ## unit tests only (no PostgreSQL/Valkey: those tests are skipped)
	$(GO) test $(GOFLAGS) ./...

.PHONY: test-sandbox
test-sandbox: ## sandbox tests (root, isolate >= 2.0, cgroups v2)
	@if [[ "$$(id -u)" != "0" ]]; then echo "make test-sandbox must run as root (isolate)"; exit 1; fi
	$(GO) test $(GOFLAGS) -tags sandbox -count=1 -v ./internal/sandbox/... ./internal/worker/...

.PHONY: bench
bench: ## benchmarks of the hot paths
	@if [[ -z "$$CMS_TEST_DATABASE_URL" ]]; then scripts/infra.sh up >/dev/null; source .infra/env; fi; \
	$(GO) test $(GOFLAGS) -run '^$$' -bench . -benchmem ./...

.PHONY: lint
lint: ## gofmt + go vet + license headers
	@out="$$(gofmt -l $(GO_DIRS))"; \
	if [[ -n "$$out" ]]; then echo "gofmt needed on:"; echo "$$out"; exit 1; fi
	$(GO) vet ./...
	$(GO) vet -tags sandbox ./...
	scripts/check-headers.sh

.PHONY: generate
generate: ## regenerate code and translation templates (sqlc, .pot)
	$(GO) generate ./...

.PHONY: infra-up infra-down
infra-up: ## start disposable PostgreSQL and Valkey (scripts/infra.sh)
	scripts/infra.sh up
infra-down: ## stop them and delete their data
	scripts/infra.sh down

.PHONY: dev
dev: ## full stack with Docker Compose (deploy/docker-compose.dev.yml)
	scripts/dev-compose.sh up

.PHONY: dev-down
dev-down:
	scripts/dev-compose.sh down

.PHONY: dev-native
dev-native: build ## full stack with native binaries (scripts/dev.sh)
	scripts/dev.sh start

.PHONY: dev-stop
dev-stop:
	scripts/dev.sh stop

.PHONY: clean
clean:
	rm -rf bin

.PHONY: help
help:
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | sed 's/:.*## /\t/' | expand -t 16
