# Developer tasks. `make check` is what CI runs; run it before committing.

GO            ?= go
GOLANGCI_LINT ?= golangci-lint
# v1.8.0 at least: older ones bundle an x/tools that panics on Go 1.27 syntax.
GOVULNCHECK   ?= $(GO) run golang.org/x/vuln/cmd/govulncheck@v1.8.0
PREFIX        ?= $(HOME)/.local
HUGO          ?= hugo

.PHONY: help build install vet test test-short race lint vuln cover check check-fast generate schema snapshot demo tape \
	site site-build script-test release-next release-draft release-prep release-tag

help: ## List the targets
	@grep -hE '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-14s %s\n", $$1, $$2}'

build: ## Compile every package
	$(GO) build ./...

install: ## Build den into $(PREFIX)/bin
	$(GO) build -o $(PREFIX)/bin/den ./cmd/den

vet: ## go vet
	$(GO) vet ./...

test: ## Run the tests
	$(GO) test ./...

test-short: ## Run the tests, skipping the slow lifecycle ones
	$(GO) test -short ./...

race: ## Run the tests under the race detector
	$(GO) test -race ./...

lint: ## golangci-lint (config in .golangci.yml)
	$(GOLANGCI_LINT) run ./...

vuln: ## Report known vulnerabilities reachable from den's code
	$(GOVULNCHECK) ./...

cover: ## Tests with coverage, failing below the per-package floors
	$(GO) test -race -coverprofile=coverage.out ./...
	./scripts/coverfloor.sh coverage.out

check: build vet lint race vuln script-test ## Everything CI checks

check-fast: build vet test-short ## Quick pre-commit check

script-test: ## Tests of the shell scripts (release.sh, install.sh, install.ps1, site-lastmod.sh)
	sh scripts/release_test.sh
	sh scripts/install_test.sh
	sh scripts/site_lastmod_test.sh
	@if command -v pwsh >/dev/null 2>&1; then pwsh -NoProfile -File scripts/install_test.ps1; else echo 'skip install.ps1 tests (no pwsh)'; fi

generate: ## Regenerate files made from code: the den.yaml schema, docs/config, docs/cli and docs/doctor/install.md
	$(GO) generate . ./internal/prereq
	$(GO) test ./cmd/den -run TestCLIReference -update -count=1

schema: generate ## Same as generate

snapshot: ## Build release archives locally without publishing
	goreleaser release --snapshot --clean --skip=publish

demo: ## Render every VHS tape (drives the real app — needs AWS access)
	@for tape in main.tape main-hd.tape docs/*/*.tape docs/*/*/*.tape docs/*/*/*/*.tape; do echo "vhs $$tape"; vhs "$$tape" || exit 1; done

tape: ## Render one VHS tape from the repo root, where its Source and Output paths resolve: make tape TAPE=secrets (or a path)
	@t="$(TAPE)"; [ -n "$$t" ] || { echo "usage: make tape TAPE=secrets (or TAPE=docs/secrets/secrets.tape)"; exit 1; }; \
	[ -f "$$t" ] || t=$$(find main*.tape docs -name "$$t.tape" | head -1); \
	[ -f "$$t" ] || { echo "no tape called $(TAPE)"; exit 1; }; \
	echo "vhs $$t"; vhs "$$t"

site: ## Preview the documentation site (site/, Hugo + Hextra) at http://localhost:1313
	./scripts/site-lastmod.sh
	cd site && $(HUGO) server

site-build: ## Build the documentation site into site/public [BASE_URL=https://…/]
	./scripts/site-lastmod.sh
	cd site && $(HUGO) --gc --minify $(if $(BASE_URL),--baseURL "$(BASE_URL)")

# A release is these three with a person in between: write the CHANGELOG.md
# entry after the draft, merge the release PR before tagging. See
# .claude/skills/release.
release-next: ## Next version from this branch's commits [BASE=origin/main]
	./scripts/release.sh next $(BASE)

release-draft: ## Draft the CHANGELOG entry: VERSION=X.Y.Z [REF=feature-branch]
	./scripts/release.sh draft "$(VERSION)" $(REF)

release-prep: ## Check this branch can ship as VERSION=X.Y.Z (make check, dry run)
	./scripts/release.sh prepare "$(VERSION)"

release-tag: ## On main after the merge: tag VERSION=X.Y.Z and push it (asks first)
	./scripts/release.sh tag "$(VERSION)"
