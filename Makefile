BINARY_SERVER  := rementor
BINARY_CTL     := rementorctl
CMD_SERVER     := ./cmd/server
CMD_CTL        := ./cmd/rementorctl
FRONTEND_DIR   := web/frontend
FRONTEND_DIST  := cmd/server/dist
DIST_DIR       := dist

GO_BUILD_FLAGS := -trimpath -ldflags="-s -w"

.PHONY: all build build-server build-ctl frontend frontend-json generate setup dev run demo demo-down demo-test vet test test-race audit check clean rebuild install install-json help

all: frontend build

## Build

build: build-server build-ctl

build-server: frontend
	@mkdir -p $(DIST_DIR)
	go build $(GO_BUILD_FLAGS) -o $(DIST_DIR)/$(BINARY_SERVER) $(CMD_SERVER)

build-ctl:
	@mkdir -p $(DIST_DIR)
	go build $(GO_BUILD_FLAGS) -o $(DIST_DIR)/$(BINARY_CTL) $(CMD_CTL)

frontend:
	rm -rf $(FRONTEND_DIST)
	cd $(FRONTEND_DIR) && npm run build

frontend-json:
	rm -rf $(FRONTEND_DIST)
	cd $(FRONTEND_DIR) && npm run build:json

generate:
	buf generate

## Dev

dev:
	@echo "→ Go backend:      http://127.0.0.1:9300"
	@echo "→ Vite dev server: http://localhost:5173"
	@trap 'kill 0' SIGINT; \
	  go run $(CMD_SERVER) & backend_pid=$$!; \
	  cd $(FRONTEND_DIR) && npm run dev -- --clearScreen false & frontend_pid=$$!; \
	  wait $$backend_pid; status=$$?; \
	  kill $$frontend_pid 2>/dev/null || true; \
	  wait $$frontend_pid 2>/dev/null || true; \
	  exit $$status

run: build
	$(DIST_DIR)/$(BINARY_SERVER)

demo:
	docker compose up --build

demo-down:
	docker compose down

demo-test:
	./scripts/test-demo.sh

## Quality

vet:
	go vet ./...

test:
	go test ./...

test-race:
	go test -race ./...

audit:
	cd $(FRONTEND_DIR) && npm audit

check:
	buf lint
	go vet ./...
	go test -race ./...
	cd $(FRONTEND_DIR) && npm run typecheck && npm run build && npm audit

fmt:
	go fmt ./...

## Install

install: frontend
	@mkdir -p $(DIST_DIR)
	go build $(GO_BUILD_FLAGS) -o $(DIST_DIR)/$(BINARY_SERVER) $(CMD_SERVER)
	go build $(GO_BUILD_FLAGS) -o $(DIST_DIR)/$(BINARY_CTL) $(CMD_CTL)
	./scripts/install-system.sh $(DIST_DIR)/$(BINARY_SERVER) $(DIST_DIR)/$(BINARY_CTL)

install-json: frontend-json
	@mkdir -p $(DIST_DIR)
	go build $(GO_BUILD_FLAGS) -o $(DIST_DIR)/$(BINARY_SERVER) $(CMD_SERVER)
	go build $(GO_BUILD_FLAGS) -o $(DIST_DIR)/$(BINARY_CTL) $(CMD_CTL)
	./scripts/install-system.sh $(DIST_DIR)/$(BINARY_SERVER) $(DIST_DIR)/$(BINARY_CTL)

## Setup

setup:
	cd $(FRONTEND_DIR) && npm install
	go mod download

## Maintenance

clean:
	rm -rf $(DIST_DIR) $(FRONTEND_DIST)

rebuild: clean all

## Help

help:
	@echo "Usage: make <target>"
	@echo ""
	@echo "Build:"
	@echo "  all          Build frontend + both binaries (default)"
	@echo "  build        Build both Go binaries to $(DIST_DIR)/"
	@echo "  build-server Build rementor server binary"
	@echo "  build-ctl    Build rementorctl CLI binary"
	@echo "  frontend     Build SolidJS frontend with binary protobuf Connect transport"
	@echo "  frontend-json Build frontend with JSON Connect transport"
	@echo "  generate     Generate Go and TypeScript RPC code from proto"
	@echo ""
	@echo "Dev:"
	@echo "  dev          Start Go backend + Vite dev server (hot reload)"
	@echo "  run          Build then run the server"
	@echo "  demo         Run the isolated Docker demo on port 8080"
	@echo "  demo-down    Stop and remove the Docker demo"
	@echo "  demo-test    Verify remote/local routing against the Docker demo"
	@echo ""
	@echo "Quality:"
	@echo "  vet          Run go vet"
	@echo "  test         Run go test"
	@echo "  test-race    Run Go tests with the race detector"
	@echo "  audit        Audit frontend dependencies"
	@echo "  check        Run the complete local quality gate"
	@echo "  fmt          Format Go code"
	@echo ""
	@echo "Install:"
	@echo "  install      Build and install the user service and binaries"
	@echo "  install-json Build/install with JSON frontend transport"
	@echo ""
	@echo "Setup:"
	@echo "  setup        Install npm deps + go mod download"
	@echo ""
	@echo "Maintenance:"
	@echo "  clean        Remove $(DIST_DIR)/ and $(FRONTEND_DIST)/"
	@echo "  rebuild      Clean then build everything"
