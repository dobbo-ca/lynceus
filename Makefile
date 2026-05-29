.PHONY: build test proto dev-up dev-down dev-logs clean

BINARIES := collector ingestion api

build:
	@for b in $(BINARIES); do \
		echo ">> building $$b"; \
		go build -o bin/$$b ./cmd/$$b || exit 1; \
	done

test:
	go test ./...

GO_BIN        := $(shell go env GOPATH)/bin
PROTOC_GEN_GO := $(GO_BIN)/protoc-gen-go

# Regenerate Go from proto/. Output paths are derived from each .proto's
# go_package option (module= strips the module prefix). Requires `protoc`
# on PATH; auto-installs protoc-gen-go if missing.
proto:
	@command -v protoc >/dev/null 2>&1 || (echo "ERROR: protoc not on PATH (brew install protobuf)" && exit 1)
	@test -x $(PROTOC_GEN_GO) || go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	PATH="$(GO_BIN):$$PATH" protoc \
	  --go_out=. \
	  --go_opt=module=github.com/dobbo-ca/lynceus \
	  proto/lynceus/v1/*.proto

dev-up:
	docker compose -f docker-compose.dev.yml up -d
	@echo ">> config DB on localhost:5432  /  stats DB on localhost:5433"

dev-down:
	docker compose -f docker-compose.dev.yml down

dev-logs:
	docker compose -f docker-compose.dev.yml logs -f

clean:
	rm -rf bin/
