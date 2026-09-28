# Build, verify, and test the Go linter and its AGENTS.md index.

.DEFAULT_GOAL := verify

.PHONY: build custom-gcl test verify

build:
	@go build ./...

verify: custom-gcl
	@go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12
	@./custom-gcl run --show-stats=false ./...
	@python3 scripts/update_agents_file_index.py --check
	@out=$$(go tool golangci-lint fmt --diff); [ -z "$$out" ] || { echo 'Go files need formatting (gofmt, goimports):' >&2; echo "$$out" >&2; exit 1; }
	@go mod tidy -diff

test:
	@go test -timeout=600s -race ./...

# methodfilecheck (see .golangci.yml) is a golangci-lint module plugin, so the
# Go linting must run through the custom binary built from this checkout.
custom-gcl: .custom-gcl.yml
	@version=$$(go list -m -f '{{.Version}}' github.com/golangci/golangci-lint/v2) && go tool golangci-lint custom --version "$$version"
