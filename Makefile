# Build, verify, and test the Go linter and its AGENTS.md index.

.DEFAULT_GOAL := verify

GOLANGCI_LINT_VERSION=v2.13.2

.PHONY: build custom-gcl format-check lint test tools verify

tools:
	@command -v golangci-lint > /dev/null 2>&1 && golangci-lint --version 2>/dev/null | grep -Fqw "$(GOLANGCI_LINT_VERSION:v%=%)" || go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

build:
	@go build ./...

verify: lint format-check
	@go mod tidy -diff

test:
	@go test -timeout=600s -race ./...

# methodfilecheck (see .golangci.yml) is a golangci-lint module plugin, so the
# Go linting must run through the custom binary built from this checkout.
custom-gcl: tools .custom-gcl.yml
	@golangci-lint custom --version $(GOLANGCI_LINT_VERSION)

lint: custom-gcl
	@./custom-gcl run --show-stats=false ./...
	@python3 scripts/update_agents_file_index.py --check

format-check: tools
	@out=$$(golangci-lint fmt --diff); [ -z "$$out" ] || { echo 'Go files need formatting (gofmt, goimports):' >&2; echo "$$out" >&2; exit 1; }
