# VPS Monitor — development commands. Run `make` for help.
# Works with the GNU make 3.81 that ships with macOS.

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
DEV     := .dev
DATA    := $(DEV)/data
LISTEN  ?= 127.0.0.1:8080
VM      ?= vpsmon-dev
# Arch of the OrbStack VM: arm64 on Apple silicon, amd64 on Intel Macs
VM_ARCH ?= $(shell uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')

.PHONY: help setup deps dev dev-init dev-server dev-agent dev-web test lint web build build-linux \
        vm-create vm-agent app-setup app-run deploy deploy-agent remote-add-server clean

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-18s\033[0m %s\n",$$1,$$2}'

setup: ## Install toolchain on macOS (Homebrew, Go, Node, OrbStack, Flutter optional)
	@scripts/setup-mac.sh

deps: ## Download Go and npm dependencies
	go mod tidy
	cd web && npm install

# ---------- local development ----------

dev: ## Run server + fake agent + Web (Vite) together; Ctrl-C stops all
	@scripts/dev.sh

dev-init: ## Create local DB, admin token and two demo servers (.dev/*.token)
	@mkdir -p $(DEV)
	@if [ -f $(DEV)/admin.token ]; then echo "already initialised ($(DEV)); run 'make clean' to reset"; exit 0; fi; \
	go run ./cmd/server init --data $(DATA) > $(DEV)/admin.token && \
	go run ./cmd/server add-server --data $(DATA) --name Mac-Fake --limit-gb 1000 --reset-day 1 > $(DEV)/agent.token && \
	go run ./cmd/server add-server --data $(DATA) --name Orb-VM --limit-gb 500 --reset-day 15 > $(DEV)/agent-vm.token && \
	echo "admin token: $$(cat $(DEV)/admin.token)  (paste into the Web/App)"

dev-server: ## Run the server on $(LISTEN)
	go run ./cmd/server run --data $(DATA) --listen $(LISTEN)

dev-agent: ## Run an agent with fake metrics against the local server
	go run ./cmd/agent --server http://127.0.0.1:8080 --token-file $(DEV)/agent.token --fake --interval 3s

dev-web: ## Run Vite dev server on :5173 (proxies /api to :8080)
	cd web && npm run dev

test: ## Run Go tests
	go test ./...

lint: ## go vet + Web type-check
	go vet ./...
	cd web && npx vue-tsc --noEmit

# ---------- builds ----------

web: ## Build Web into web/dist (embedded by the server)
	cd web && npm run build

build: web ## Build server + agent for this machine into bin/
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/vpsmon-server ./cmd/server
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/vpsmon-agent ./cmd/agent

build-linux: web ## Cross-compile linux/amd64 + linux/arm64 into dist/
	@for arch in amd64 arm64; do \
	  echo "→ linux/$$arch"; \
	  CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -ldflags "$(LDFLAGS)" -o dist/vpsmon-server-linux-$$arch ./cmd/server || exit 1; \
	  CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -ldflags "$(LDFLAGS)" -o dist/vpsmon-agent-linux-$$arch ./cmd/agent || exit 1; \
	done
	@cd dist && shasum -a 256 vpsmon-* > SHA256SUMS && echo "dist/SHA256SUMS written"

# ---------- Linux VM on your Mac (OrbStack) for real agent metrics ----------

vm-create: ## Create an Ubuntu VM in OrbStack for testing the real Linux collector
	orb create ubuntu $(VM)

vm-agent: build-linux ## Run the real agent inside the VM against the server on your Mac
	@echo "If this cannot connect, restart the server with: make dev-server LISTEN=0.0.0.0:8080"
	orb -m $(VM) ./dist/vpsmon-agent-linux-$(VM_ARCH) --server http://host.orb.internal:8080 \
	  --token-file $(DEV)/agent-vm.token --allow-http --interval 3s

# ---------- App ----------

app-setup: ## Generate iOS/Android projects for the Flutter app (first time)
	cd app && flutter create --org dev.vpsmon --project-name vpsmon_app --platforms ios,android . && flutter pub get

app-run: ## Run the app (pick a simulator/emulator when prompted)
	cd app && flutter run

# ---------- remote VPS ----------

deploy: build-linux ## Deploy server to VPS: make deploy VPS=root@1.2.3.4
	@test -n "$(VPS)" || (echo "usage: make deploy VPS=user@host" && exit 1)
	scripts/deploy.sh server $(VPS)

deploy-agent: build-linux ## Deploy agent: make deploy-agent VPS=root@host SERVER=https://... TOKEN_FILE=path
	@test -n "$(VPS)" -a -n "$(SERVER)" -a -n "$(TOKEN_FILE)" || (echo "usage: make deploy-agent VPS=user@host SERVER=https://monitor.example.com TOKEN_FILE=./hk1.token" && exit 1)
	scripts/deploy.sh agent $(VPS) $(SERVER) $(TOKEN_FILE)

remote-add-server: ## Add a server on the VPS: make remote-add-server VPS=root@host NAME=HK-1 (token → ./NAME.token)
	@test -n "$(VPS)" -a -n "$(NAME)" || (echo "usage: make remote-add-server VPS=user@host NAME=HK-1" && exit 1)
	ssh $(VPS) "sudo -u vpsmon /usr/local/bin/vpsmon-server add-server --data /var/lib/vpsmon --name '$(NAME)'" > $(NAME).token
	@chmod 600 $(NAME).token && echo "agent token saved to ./$(NAME).token (do not commit it)"

clean: ## Remove local dev data and build output
	rm -rf $(DEV) bin dist
