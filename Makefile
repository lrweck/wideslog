# wideslog — development, quality, and release tasks.
#
# Targets are grouped: quality, testing, build, ci, release, and utilities.
# Run `make help` (the default) for the full list.

.DEFAULT_GOAL := help

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------

GO            ?= go
BIN_DIR        := bin
EXAMPLE_BIN    := $(BIN_DIR)/wideslog-example

GIT_VERSION    := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BRANCH         := $(shell git branch --show-current 2>/dev/null)
LATEST_TAG     := $(shell git tag -l 'v*' --sort=-v:refname | head -1)
LATEST_TAG    ?= v0.0.0

STATICCHECK    := $(shell command -v staticcheck 2>/dev/null)

PROFILES       := cpu.prof mem.prof block.prof
TEST_BIN       := wideslog.test

# ---------------------------------------------------------------------------
# Colors (only when the terminal supports them)
# ---------------------------------------------------------------------------

NO_COLOR := \033[0m
BOLD     := \033[1m
RED      := \033[31m
YELLOW   := \033[33m
GREEN    := \033[32m
CYAN     := \033[36m

ifeq ($(NO_COLOR), true)
NO_COLOR :=
BOLD     :=
RED      :=
YELLOW   :=
GREEN    :=
CYAN     :=
endif

define info
	@printf "$(BOLD)$(CYAN)▶$(NO_COLOR) %s\n" "$(1)"
endef

define ok
	@printf "$(GREEN)✔$(NO_COLOR) %s\n" "$(1)"
endef

define warn
	@printf "$(BOLD)$(YELLOW)⚠$(NO_COLOR) %s\n" "$(1)"
endef

define err
	@printf "$(BOLD)$(RED)✘$(NO_COLOR) %s\n" "$(1)"
endef

# ---------------------------------------------------------------------------
# Testing
# ---------------------------------------------------------------------------

.PHONY: test
test: ## Run all tests
	$(call info, running tests)
	$(GO) test ./...
	$(call ok, tests passed)

.PHONY: test-race
test-race: ## Run all tests with the race detector
	$(call info, running tests with race detector)
	$(GO) test -race ./...
	$(call ok, race tests passed)

.PHONY: test-verbose
test-verbose: ## Run all tests with verbose output
	$(GO) test -v ./...

.PHONY: bench
bench: ## Run all benchmarks with allocation counts
	$(GO) test -bench=. -benchmem ./...

.PHONY: bench-cpu
bench-cpu: cpu.prof ## Run benchmarks and write a CPU profile
	$(call info, wrote cpu.prof — inspect with: go tool pprof cpu.prof)

cpu.prof:
	$(GO) test -bench=. -cpuprofile=$@ .

.PHONY: bench-mem
bench-mem: mem.prof ## Run benchmarks and write a memory profile
	$(call info, wrote mem.prof — inspect with: go tool pprof mem.prof)

mem.prof:
	$(GO) test -bench=. -memprofile=$@ .

# ---------------------------------------------------------------------------
# Quality
# ---------------------------------------------------------------------------

.PHONY: vet
vet: ## Run go vet
	$(call info, running go vet)
	$(GO) vet ./...
	$(call ok, vet clean)

.PHONY: staticcheck
staticcheck: ## Run staticcheck (install: go run honnef.co/go/tools/cmd/staticcheck@latest)
	@if [ -z "$(STATICCHECK)" ]; then \
		printf "$(BOLD)$(YELLOW)⚠$(NO_COLOR) staticcheck not installed; skipping\n"; \
		printf "$(BOLD)$(YELLOW)⚠$(NO_COLOR) install with: go install honnef.co/go/tools/cmd/staticcheck@latest\n"; \
	else \
		printf "$(BOLD)$(CYAN)▶$(NO_COLOR) running staticcheck\n"; \
		$(STATICCHECK) ./... ; \
		printf "$(GREEN)✔$(NO_COLOR) staticcheck clean\n" ; \
	fi

.PHONY: lint
lint: vet staticcheck ## Run vet and staticcheck

.PHONY: fmt
fmt: ## Format all Go sources in place
	$(call info, formatting)
	gofmt -l -w .
	$(call ok, formatted)

.PHONY: fmt-check
fmt-check: ## Check formatting without modifying (fails on unformatted files)
	@files="$$(gofmt -l .)"; \
	if [ -n "$$files" ]; then \
		printf "$(BOLD)$(RED)✘$(NO_COLOR) unformatted files:\n"; \
		printf '%s\n' "$$files" ; \
		exit 1 ; \
	else \
		printf "$(GREEN)✔$(NO_COLOR) formatting clean\n" ; \
	fi

# ---------------------------------------------------------------------------
# Build
# ---------------------------------------------------------------------------

.PHONY: build
build: ## Build all packages
	$(call info, building)
	$(GO) build ./...
	$(call ok, build ok)

.PHONY: build-example
build-example: ## Build the example binary into ./bin
	$(call info, building example)
	$(GO) build -o $(EXAMPLE_BIN) ./example
	$(call ok, built $(EXAMPLE_BIN))

# ---------------------------------------------------------------------------
# CI
# ---------------------------------------------------------------------------

.PHONY: ci
ci: tidy fmt-check vet staticcheck test-race build ## Full validation pipeline (local CI)
	$(call ok, CI green — ready to ship)

.PHONY: ci-fast
ci-fast: fmt-check vet test build ## Faster validation for day-to-day work
	$(call ok, fast CI green)

# ---------------------------------------------------------------------------
# Dependencies
# ---------------------------------------------------------------------------

.PHONY: tidy
tidy: ## Tidy go.mod and go.sum
	$(call info, tidying modules)
	$(GO) mod tidy
	$(call ok, modules tidy)

.PHONY: deps
deps: ## Download and verify dependencies
	$(GO) mod download
	$(GO) mod verify

# ---------------------------------------------------------------------------
# Utilities
# ---------------------------------------------------------------------------

.PHONY: version
version: ## Print the current git-derived version
	@printf '%s\n' "$(GIT_VERSION)"

.PHONY: clean
clean: ## Remove build artifacts and profiles
	rm -rf $(BIN_DIR)
	rm -f $(PROFILES)
	rm -f $(TEST_BIN)
	$(call ok, cleaned)

# ---------------------------------------------------------------------------
# Release
# ---------------------------------------------------------------------------

# Guard: releases only from main.
.PHONY: check-branch
check-branch:
	@if [ "$(BRANCH)" != "main" ]; then \
		printf "$(BOLD)$(RED)✘$(NO_COLOR) releases are only allowed from main (current: $(BRANCH))\n"; \
		exit 1; \
	fi

# Compute the next version from the latest tag and a bump operator, using the
# shell at recipe time only (fails safely if the tag does not parse).
define next_version
echo "$(LATEST_TAG)" | awk -F'[v.]' -v op="$1" '{ \
	maj=$$2; min=$$3; pat=$$4; \
	if (op=="major") { maj=maj+1; min=0; pat=0 } \
	else if (op=="minor") { min=min+1; pat=0 } \
	else { pat=pat+1 } \
	printf "v%d.%d.%d", maj, min, pat }'
endef

# Print the changelog since LATEST_TAG.
.PHONY: changelog
changelog: ## Print the changelog since the latest tag
	@git log "$(LATEST_TAG)..HEAD" --reverse \
		--pretty=format:"- %s (%h)" \
		--no-merges

# Release targets. They share one implementation (do-release) driven by BUMP;
# the tag is created and, after asking, pushed only from main.
.PHONY: release-patch
release-patch: check-branch ci ## Bump patch (vX.Y.Z -> vX.Y.Z+1), tag, ask to push
	@$(MAKE) --no-print-directory do-release BUMP=patch

.PHONY: release-minor
release-minor: check-branch ci ## Bump minor (vX.Y.Z -> vX.Y+1.0), tag, ask to push
	@$(MAKE) --no-print-directory do-release BUMP=minor

.PHONY: release-major
release-major: check-branch ci ## Bump major (vX.Y.Z -> vX+1.0.0), tag, ask to push
	@$(MAKE) --no-print-directory do-release BUMP=major

.PHONY: do-release
do-release:
	@version="$$($(call next_version,$(BUMP)))"; \
	printf "$(BOLD)$(CYAN)▶$(NO_COLOR) current: $(LATEST_TAG) -> next: $$version\n"; \
	git tag -a "$$version" -m "$$version"; \
	printf "$(GREEN)✔$(NO_COLOR) created tag $$version\n"; \
	read -p "$(BOLD)Push tag $$version to origin? [y/N] $(NO_COLOR)" ans; \
	if [ "$$ans" = "y" ] || [ "$$ans" = "Y" ]; then \
		git push origin "$$version"; \
		printf "$(GREEN)✔$(NO_COLOR) pushed $$version to origin\n"; \
	else \
		printf "$(BOLD)$(YELLOW)⚠$(NO_COLOR) tag $$version created locally; push it with: git push origin $$version\n"; \
	fi

# tag: create an arbitrary version, validated against main + CI.
# Usage: make tag VERSION=v1.2.3
.PHONY: tag
tag: check-branch ci ## Tag an explicit version (make tag VERSION=v1.2.3); ask to push
	@if [ -z "$(VERSION)" ]; then \
		printf "$(BOLD)$(RED)✘$(NO_COLOR) usage: make tag VERSION=v1.2.3\n"; \
		exit 1; \
	fi; \
	git tag -a "$(VERSION)" -m "$(VERSION)"; \
	printf "$(GREEN)✔$(NO_COLOR) created tag $(VERSION)\n"; \
	read -p "$(BOLD)Push tag $(VERSION) to origin? [y/N] $(NO_COLOR)" ans; \
	if [ "$$ans" = "y" ] || [ "$$ans" = "Y" ]; then \
		git push origin "$(VERSION)"; \
		printf "$(GREEN)✔$(NO_COLOR) pushed $(VERSION) to origin\n"; \
	else \
		printf "$(BOLD)$(YELLOW)⚠$(NO_COLOR) tag $(VERSION) created locally; push it with: git push origin $(VERSION)\n"; \
	fi

# ---------------------------------------------------------------------------
# Help
# ---------------------------------------------------------------------------

.PHONY: help
help: ## Show this help
	@printf "$(BOLD)wideslog — $(GIT_VERSION)$(NO_COLOR)\n\n"
	@printf "$(BOLD)Usage:$(NO_COLOR) make <target>\n\n"
	@awk 'BEGIN {FS = ":.*?## "} \
		/^[a-zA-Z0-9._-]+:.*?## / { \
			printf "  $(GREEN)%-15s$(NO_COLOR) %s\n", $$1, $$2 \
		}' $(MAKEFILE_LIST)
	@printf "\n$(BOLD)Release flow:$(NO_COLOR) make release-patch|release-minor|release-major (main branch only)\n"
	@printf "$(BOLD)Profile flows:$(NO_COLOR) make bench-cpu / bench-mem, then: go tool pprof cpu.prof\n"
