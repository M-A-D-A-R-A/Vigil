APP_NAME := vigil
GO_PACKAGES := ./cmd/... ./internal/... ./web/...
GO_ENV := GOCACHE=$(CURDIR)/.gocache GOMODCACHE=$(CURDIR)/.gomodcache

.PHONY: run build test ui-install ui-dev ui-build size size-release build-linux-arm64 seed-sample bench

run:
	@env $(GO_ENV) go run ./cmd/vigil

build:
	@mkdir -p bin
	@env $(GO_ENV) go build -o bin/$(APP_NAME) ./cmd/vigil

test:
	@env $(GO_ENV) go test $(GO_PACKAGES)

ui-install:
	cd ui && bun install

ui-dev:
	cd ui && bun run dev

ui-build:
	cd ui && bun run build

size: ui-build
	@mkdir -p bin
	@env $(GO_ENV) go build -o bin/$(APP_NAME) ./cmd/vigil
	@GO_BYTES=$$(stat -f%z bin/$(APP_NAME)); \
	UI_BYTES=$$(find web/dist -type f ! -name '.gitkeep' -exec stat -f%z {} \; | awk '{sum += $$1} END {print sum + 0}'); \
	TOTAL_BYTES=$$((GO_BYTES + UI_BYTES)); \
	GO_HUMAN=$$(ls -lh bin/$(APP_NAME) | awk '{print $$5}'); \
	UI_HUMAN=$$(du -sh web/dist | awk '{print $$1}'); \
	TOTAL_HUMAN=$$(awk 'BEGIN { printf "%.2f MB", '"$$TOTAL_BYTES"' / 1024 / 1024 }'); \
	printf "Go binary: %s (%s bytes)\n" "$$GO_HUMAN" "$$GO_BYTES"; \
	printf "UI bundle: %s (%s bytes)\n" "$$UI_HUMAN" "$$UI_BYTES"; \
	printf "Combined:  %s (%s bytes)\n" "$$TOTAL_HUMAN" "$$TOTAL_BYTES"

size-release: ui-build
	@mkdir -p bin
	@env $(GO_ENV) go build -ldflags="-s -w" -o bin/$(APP_NAME)-release ./cmd/vigil
	@GO_BYTES=$$(stat -f%z bin/$(APP_NAME)-release); \
	UI_BYTES=$$(find web/dist -type f ! -name '.gitkeep' -exec stat -f%z {} \; | awk '{sum += $$1} END {print sum + 0}'); \
	TOTAL_BYTES=$$((GO_BYTES + UI_BYTES)); \
	GO_HUMAN=$$(ls -lh bin/$(APP_NAME)-release | awk '{print $$5}'); \
	UI_HUMAN=$$(du -sh web/dist | awk '{print $$1}'); \
	TOTAL_HUMAN=$$(awk 'BEGIN { printf "%.2f MB", '"$$TOTAL_BYTES"' / 1024 / 1024 }'); \
	printf "Go release binary: %s (%s bytes)\n" "$$GO_HUMAN" "$$GO_BYTES"; \
	printf "UI bundle:         %s (%s bytes)\n" "$$UI_HUMAN" "$$UI_BYTES"; \
	printf "Combined release:  %s (%s bytes)\n" "$$TOTAL_HUMAN" "$$TOTAL_BYTES"

build-linux-arm64:
	@mkdir -p bin
	@env $(GO_ENV) GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o bin/$(APP_NAME)-linux-arm64 ./cmd/vigil
	@ls -lh bin/$(APP_NAME)-linux-arm64

seed-sample:
	@env $(GO_ENV) go run ./cmd/vigil-seed $(ARGS) $(if $(ADDR),-addr $(ADDR),) $(if $(PROJECT_ID),-project-id $(PROJECT_ID),) $(if $(PROJECT_NAME),-project-name $(PROJECT_NAME),) $(if $(INGEST_KEY),-ingest-key $(INGEST_KEY),)

bench:
	@env $(GO_ENV) go run ./cmd/vigil-bench $(ARGS)
