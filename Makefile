.DEFAULT_GOAL := help

# ==================================================================================== #
# VARIABLES
# ==================================================================================== #

# Quality gates (ratchets: raise over time, never lower to green a build)
COVER_MIN  ?= 80
LINE_LIMIT ?= 500

# Pinned tool versions: must match the CI workflow and _shared/references/versions.md
GOLANGCI_VERSION    ?= v2.14.0
GOVULNCHECK_VERSION ?= v1.7.0

# ==================================================================================== #
# PHONY DECLARATIONS (in alphabetical order)
# ==================================================================================== #
.PHONY: audit checklen ci cover fulltest help release test test-s3 tidy tools

# ==================================================================================== #
# STANDARD TARGETS (in alphabetical order)
# ==================================================================================== #

## audit: run quality control checks (mod verify, gofmt, file length, lint, vuln scan, coverage gate)
audit: cover checklen
	@which golangci-lint > /dev/null || $(MAKE) tools
	@which govulncheck > /dev/null || $(MAKE) tools
	go mod verify
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "FAIL: gofmt found unformatted files:"; echo "$$unformatted"; exit 1; \
	fi
	golangci-lint run ./...
	govulncheck ./...

## checklen: fail if any non-generated .go file exceeds LINE_LIMIT lines
checklen:
	@fail=0; \
	for f in $$(git ls-files '*.go' | grep -Ev '_test\.go$$|\.gen\.go$$|\.pb\.go$$'); do \
		n=$$(wc -l < "$$f"); \
		if [ "$$n" -gt "$(LINE_LIMIT)" ]; then echo "$$f: $$n lines > $(LINE_LIMIT)"; fail=1; fi; \
	done; \
	exit $$fail

## ci: run the full local CI pipeline (tidy, audit, fulltest)
ci: tidy audit fulltest

## cover: run tests with coverage and fail below COVER_MIN
# A module with no statements yet has nothing to measure; the gate applies from the first one.
cover:
	go test -covermode=atomic -coverpkg=./... -coverprofile=coverage.out ./...
	@if [ "$$(grep -vc '^mode:' coverage.out)" -eq 0 ]; then echo "coverage: no statements"; exit 0; fi; \
	go tool cover -func=coverage.out | awk '/^total:/ {print "coverage: " $$3}'; \
	total=$$(go tool cover -func=coverage.out | awk '/^total:/ {print $$3}' | tr -d '%'); \
	awk -v t="$$total" -v min="$(COVER_MIN)" 'BEGIN { if (t+0 < min+0) { printf "FAIL: coverage %.1f%% < %d%%\n", t, min; exit 1 } }'

## fulltest: phase-closing gate: race detector and coverage over every package
fulltest:
	go test -race -cover ./...

## help: display this help message
help:
	@echo 'Usage:'
	@sed -n 's/^##//p' ${MAKEFILE_LIST} | column -t -s ':' | sed -e 's/^/ /'

## release: cut and publish a release (derive version, changelog, tag, push)
release:
	@./scripts/release.sh

## test: short units for red/green; race and coverage run in fulltest
test:
	go test -short ./...

## test-s3: run the S3 backend suite against a disposable MinIO from docker-compose.test.yml
# Teardown output goes through a command substitution, not the caller's pipe, so a reader exiting early cannot kill it.
test-s3:
	@cleanup() { status=$$?; trap - EXIT; \
		out=$$(docker compose -p bizkit-test -f docker-compose.test.yml down -v 2>&1) || { echo "$$out" >&2; [ $$status -ne 0 ] || status=1; }; \
		exit $$status; }; \
	trap cleanup EXIT; trap 'exit 129' HUP; trap 'exit 130' INT; trap 'exit 143' TERM; trap '' PIPE; \
	docker compose -p bizkit-test -f docker-compose.test.yml up -d --wait && \
	go test -tags s3 -race -count=1 -v ./blob/s3

## tidy: format Go code and tidy the module file
tidy:
	go fmt ./...
	go mod tidy -v

## tools: install pinned Go development tools
tools:
	@echo "Installing Go tools..."
	@go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)
	@go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	@echo "Tools installed in $(shell go env GOBIN || go env GOPATH)/bin"
