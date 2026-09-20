# Build, test, and lint the repository.

GOLANGCI_LINT_VERSION=v2.13.2

.PHONY: tools test lint custom-gcl format-check

tools:
	@command -v golangci-lint > /dev/null 2>&1 && golangci-lint --version 2>/dev/null | grep -Fqw "$(GOLANGCI_LINT_VERSION:v%=%)" || go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

test:
	@go test -race ./...

# methodfilecheck (see .golangci.yml) is a golangci-lint module plugin, so the
# Go linting must run through the custom binary built from this checkout.
custom-gcl: .custom-gcl.yml
	@golangci-lint custom --version $(GOLANGCI_LINT_VERSION)

lint: tools custom-gcl
	@./custom-gcl run --show-stats=false ./...

format-check: tools
	@out=$$(golangci-lint fmt --diff); [ -z "$$out" ] || { echo 'Go files need formatting (gofmt, goimports):' >&2; echo "$$out" >&2; exit 1; }
