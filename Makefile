# watch-aware-preloader - developer Makefile
# Run `make help` for a summary of targets.

BINARY      := preloadd
PKG         := ./cmd/preloadd
BIN_DIR     := bin
VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -X main.version=$(VERSION)
GOLANGCI    := golangci-lint
COMPOSER    := composer

.DEFAULT_GOAL := help

## ----- Go -----

.PHONY: build
build: ## Build the daemon into bin/preloadd
	@mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(PKG)

.PHONY: run
run: build ## Build and run locally
	./$(BIN_DIR)/$(BINARY)

.PHONY: test
test: ## Run tests
	go test ./...

.PHONY: test-race
test-race: ## Run tests with the race detector
	CGO_ENABLED=1 go test -race -count=1 ./...

.PHONY: cover
cover: ## Run tests with a coverage report
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

.PHONY: lint
lint: ## Run golangci-lint
	$(GOLANGCI) run

.PHONY: fmt
fmt: ## Format Go code and tidy modules
	gofmt -w .
	go mod tidy

.PHONY: vet
vet: ## Run go vet
	go vet ./...

## ----- PHP (Phase 2 settings page) -----

.PHONY: php-install
php-install: ## Install PHP dev tooling (PHPStan, PHP-CS-Fixer) via Composer
	$(COMPOSER) install

.PHONY: php-lint
php-lint: ## Static analysis (PHPStan) + style check (PHP-CS-Fixer, dry-run)
	@if find plugin -type f \( -name '*.php' -o -name '*.page' \) 2>/dev/null | grep -q .; then \
		vendor/bin/phpstan analyse --no-progress ; \
		vendor/bin/php-cs-fixer fix --dry-run --diff ; \
	else \
		echo "no PHP files under plugin/ yet - skipping PHP lint" ; \
	fi

.PHONY: php-fix
php-fix: ## Auto-fix PHP style (PHP-CS-Fixer)
	vendor/bin/php-cs-fixer fix

## ----- Meta -----

.PHONY: tools
tools: ## Install local dev tooling (golangci-lint + PHP dev deps)
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	$(COMPOSER) install

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BIN_DIR) coverage.out coverage.html

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'
