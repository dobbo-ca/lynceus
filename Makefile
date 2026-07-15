.PHONY: build test proto templ gen dev-up dev-down dev-logs clean

BINARIES := collector ingestion api

build:
	@for b in $(BINARIES); do \
		echo ">> building $$b"; \
		go build -o bin/$$b ./cmd/$$b || exit 1; \
	done

test:
	go test ./...

GO_BIN := $(shell go env GOPATH)/bin

# Pinned generator versions. These must match the version stamps baked into
# the committed generated files, or CI's "generated code in sync" check fails.
# protoc-gen-go tracks the google.golang.org/protobuf version in go.mod; protoc
# itself is pinned via .tool-versions and CI's setup-protoc (libprotoc 35.0 ->
# stamp protoc v7.35.0). Bumping any of these means regenerating in the same
# commit. We install the pinned versions every run because `go install` won't
# downgrade an already-present binary on its own.
PROTOC_GEN_GO_VERSION := v1.36.11
TEMPL_VERSION         := v0.3.1020

# Regenerate Go from proto/. Output paths are derived from each .proto's
# go_package option (module= strips the module prefix). Requires protoc 35.0
# on PATH (see .tool-versions); installs the pinned protoc-gen-go.
proto:
	@command -v protoc >/dev/null 2>&1 || (echo "ERROR: protoc not on PATH (need libprotoc 35.0 — see .tool-versions, or 'brew install protobuf')" && exit 1)
	go install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)
	PATH="$(GO_BIN):$$PATH" protoc \
	  --go_out=. \
	  --go_opt=module=github.com/dobbo-ca/lynceus \
	  proto/lynceus/v1/*.proto

# Regenerate Go from web/*.templ. Installs the pinned templ CLI.
templ:
	go install github.com/a-h/templ/cmd/templ@$(TEMPL_VERSION)
	PATH="$(GO_BIN):$$PATH" templ generate

# Run all code generators.
gen: proto templ

dev-up:
	@grep -q '^CLICKHOUSE_PASSWORD=' .env 2>/dev/null || echo "CLICKHOUSE_PASSWORD=$$(openssl rand -hex 24)" >> .env
	docker compose -f docker-compose.dev.yml up -d
	@echo ">> config DB on localhost:5432  /  monitored target on localhost:5433  /  ClickHouse stats on localhost:8123"

dev-down:
	docker compose -f docker-compose.dev.yml down

dev-logs:
	docker compose -f docker-compose.dev.yml logs -f

clean:
	rm -rf bin/
