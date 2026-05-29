.PHONY: build test proto dev-up dev-down dev-logs clean

BINARIES := collector ingestion api

build:
	@for b in $(BINARIES); do \
		echo ">> building $$b"; \
		go build -o bin/$$b ./cmd/$$b || exit 1; \
	done

test:
	go test ./...

# Regenerate Go from proto/. Wired in Task 2 of the MVP plan; placeholder
# target here so the surface is stable from day one.
proto:
	@echo "(proto generation is wired in Task 2 of the MVP plan)"

dev-up:
	docker compose -f docker-compose.dev.yml up -d
	@echo ">> config DB on localhost:5432  /  stats DB on localhost:5433"

dev-down:
	docker compose -f docker-compose.dev.yml down

dev-logs:
	docker compose -f docker-compose.dev.yml logs -f

clean:
	rm -rf bin/
