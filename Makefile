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

# Packages with sandbox-tagged tests (those that exist yet). The ones of
# internal/dispatcher (end to end and chaos) also need PostgreSQL and Valkey
# and run after the others: the chaos test keeps several judges busy, and
# next to the language suite it made slow compilers (GHC) time out.
SANDBOX_PKGS := $(foreach d,sandbox grading worker dispatcher,$(if $(wildcard internal/$(d)),./internal/$(d)/...))
SANDBOX_LAST := $(filter ./internal/dispatcher/...,$(SANDBOX_PKGS))

.PHONY: test-sandbox
test-sandbox: ## as root: isolate, malicious battery, example solutions, dispatcher end to end and chaos (CMS_SANDBOX_CGROUPS=0 without cgroups v2)
	@if [[ "$$(id -u)" != "0" ]]; then echo "make test-sandbox must run as root (isolate)"; exit 1; fi
	@if [[ -z "$$CMS_TEST_DATABASE_URL" ]]; then scripts/infra.sh up >/dev/null; fi
	@echo "CMS_SANDBOX_CGROUPS=$${CMS_SANDBOX_CGROUPS:-1}"
	@if [[ -z "$$CMS_TEST_DATABASE_URL" ]]; then source .infra/env; fi; \
	export CMS_SANDBOX_CGROUPS="$${CMS_SANDBOX_CGROUPS:-1}" CMS_TEST_REQUIRE_INFRA=1; \
	$(GO) test $(GOFLAGS) -tags sandbox -count=1 -timeout 30m -v $(filter-out $(SANDBOX_LAST),$(SANDBOX_PKGS)) && \
	$(if $(SANDBOX_LAST),$(GO) test $(GOFLAGS) -tags sandbox -count=1 -timeout 30m -v $(SANDBOX_LAST),true)

.PHONY: chaos
chaos: ## as root: the chaos test alone (kill -9 of the dispatcher and the workers; CMS_TEST_CHAOS_SEED, CMS_TEST_CHAOS_SUBMISSIONS)
	@if [[ "$$(id -u)" != "0" ]]; then echo "make chaos must run as root (isolate)"; exit 1; fi
	@if [[ -z "$$CMS_TEST_DATABASE_URL" ]]; then scripts/infra.sh up >/dev/null; fi
	@if [[ -z "$$CMS_TEST_DATABASE_URL" ]]; then source .infra/env; fi; \
	CMS_SANDBOX_CGROUPS="$${CMS_SANDBOX_CGROUPS:-1}" CMS_TEST_REQUIRE_INFRA=1 \
		$(GO) test $(GOFLAGS) -tags sandbox -count=1 -timeout 30m -v -run 'TestChaos$$' ./internal/dispatcher/

.PHONY: load
load: ## load test of the dispatcher (N1): 2 submissions/s, then 20/s for 60s, with simulated workers
	@if [[ -z "$$CMS_TEST_DATABASE_URL" ]]; then scripts/infra.sh up >/dev/null; fi
	@if [[ -z "$$CMS_TEST_DATABASE_URL" ]]; then source .infra/env; fi; \
	CMS_TEST_LOAD=1 CMS_TEST_REQUIRE_INFRA=1 \
		$(GO) test $(GOFLAGS) -count=1 -timeout 30m -v -run 'TestLoad$$' ./internal/dispatcher/

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

# Code generators, pinned and installed in bin/tools (not committed).
TOOLS_DIR    := $(CURDIR)/bin/tools
SQLC_VERSION := v1.31.1
SQLC         := $(TOOLS_DIR)/sqlc
# Header of the generated Go files (scripts/check-headers.sh).
GO_HEADER    := // SPDX-License-Identifier: AGPL-3.0-or-later\n// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors\n\n

.PHONY: tools
tools: ## install the pinned code generators in bin/tools (sqlc)
	@if [[ ! -x "$(SQLC)" || "$$("$(SQLC)" version 2>/dev/null)" != "$(SQLC_VERSION)" ]]; then \
		echo "installing sqlc $(SQLC_VERSION) in bin/tools"; \
		GOBIN="$(TOOLS_DIR)" $(GO) install github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION); \
	fi

.PHONY: generate
generate: tools ## regenerate code and translation templates (sqlc, .pot)
	rm -f internal/store/q/*.go
	"$(SQLC)" generate
	"$(SQLC)" vet
	@for f in internal/store/q/*.go; do \
		{ printf '$(GO_HEADER)'; cat "$$f"; } > "$$f.tmp" && mv "$$f.tmp" "$$f"; \
	done
	$(GO) generate ./...

# CLDR tables of internal/l10n and their golden outputs, extracted from
# Babel 2.12.1 (the version CMS pins). Committed; not part of `generate`.
L10N_PYTHON ?= python3

.PHONY: l10n
l10n: ## regenerate the CLDR tables of internal/l10n (L10N_PYTHON: a python with babel==2.12.1)
	$(L10N_PYTHON) tools/l10n/extract.py
	$(GO) test $(GOFLAGS) -count=1 -run 'TestGolden' ./internal/l10n/

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
	@grep -E '^[a-z0-9-]+:.*## ' $(MAKEFILE_LIST) | sed 's/:.*## /\t/' | expand -t 16
