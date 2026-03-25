# ─────────────────────────────────────────────────────────────
# Application configuration
# ─────────────────────────────────────────────────────────────

APP_NAME   := rentloop                 # application name
BINARY     := bin/$(APP_NAME)          # output binary path
MAIN       := ./cmd/server             # main package entrypoint

# ─────────────────────────────────────────────────────────────
# Deployment configuration (DigitalOcean droplet)
# ─────────────────────────────────────────────────────────────

DO_HOST    ?= YOUR_DROPLET_IP          # remote server IP
DO_USER    ?= rentloop                 # SSH user
REMOTE_DIR := /opt/rentloop            # server install directory

# ─────────────────────────────────────────────────────────────
# Terminal colors for help output
# ─────────────────────────────────────────────────────────────

GREEN  := \033[0;32m
YELLOW := \033[0;33m
RESET  := \033[0m

# Declare non-file commands
.PHONY: help run dev build build-linux test test-race test-cover \
        lint vet migrate deploy clean

# Show available commands
help:
	@echo ""
	@echo "  $(GREEN)RentLoop$(RESET)"
	@echo ""
	@echo "  $(YELLOW)Dev$(RESET)"
	@echo "    make run          start server"
	@echo "    make dev          live reload (requires air)"
	@echo "    make build        compile → ./bin/rentloop"
	@echo ""
	@echo "  $(YELLOW)Database$(RESET)"
	@echo "    make migrate      run all pending SQL migrations"
	@echo ""
	@echo "  $(YELLOW)Quality$(RESET)"
	@echo "    make test         run all tests"
	@echo "    make test-race    run tests with race detector"
	@echo "    make test-cover   coverage report → coverage.html"
	@echo "    make lint         golangci-lint"
	@echo "    make vet          go vet"
	@echo ""
	@echo "  $(YELLOW)Deploy$(RESET)"
	@echo "    make deploy       test + build linux binary + scp + restart"
	@echo ""

# Run the server with environment variables from .env
run:
	@export $$(grep -v '^#' .env | grep -v '^$$' | xargs) && go run $(MAIN)

# Development mode with live reload (requires air)
dev:
	@which air > /dev/null 2>&1 || \
		(echo "install air: go install github.com/air-verse/air@latest" && exit 1)
	@export $$(grep -v '^#' .env | grep -v '^$$' | xargs) && air

# Build local binary
build:
	@mkdir -p bin
	go build -ldflags="-s -w" -o $(BINARY) $(MAIN)

# Build Linux binary for production deployment
build-linux:
	@mkdir -p bin
	GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o $(BINARY)-linux $(MAIN)

# Run database migrations
migrate:
	@export $$(grep -v '^#' .env | grep -v '^$$' | xargs) && \
		go run ./cmd/migrate

# Run unit tests
test:
	go test ./... -count=1

# Run tests with race detector
test-race:
	CGO_ENABLED=1 go test ./... -race -count=1

# Generate coverage report
test-cover:
	go test ./... -race -count=1 -coverprofile=coverage.out
	go tool cover -html=coverage.out -o coverage.html
	@open coverage.html 2>/dev/null || xdg-open coverage.html 2>/dev/null || true

# Run static analysis via golangci-lint
lint:
	@which golangci-lint > /dev/null 2>&1 || \
		(echo "install: https://golangci-lint.run/usage/install/" && exit 1)
	golangci-lint run ./...

# Run Go static analyzer
vet:
	go vet ./...

# Deploy binary to server and restart service
deploy: test-race build-linux
	scp $(BINARY)-linux $(DO_USER)@$(DO_HOST):$(REMOTE_DIR)/$(APP_NAME)
	ssh $(DO_USER)@$(DO_HOST) "sudo systemctl restart $(APP_NAME)"
	@sleep 3
	@curl -sf https://$(DO_HOST)/health && \
		echo "$(GREEN)✓ deployed$(RESET)" || \
		echo "$(YELLOW)⚠ health check failed$(RESET)"

# Remove build artifacts and coverage files
clean:
	@rm -rf bin/ coverage.out coverage.html