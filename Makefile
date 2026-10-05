# VPS Monitor — development commands. Run `make` for help.
# Works with the GNU make 3.81 that ships with macOS.

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
# -trimpath：不嵌入编译机上的绝对路径（不泄露用户名），同一提交、同一 Go 版本在任何机器上构建出逐字节相同的二进制，
# 用户可自行构建并与官方签名发布的文件比对（设计 29.7）
GOBUILD := go build -trimpath
DEV     := .dev
DATA    := $(DEV)/data
LISTEN  ?= 127.0.0.1:8080
VM      ?= vpsmon-dev
# Arch of the OrbStack VM: arm64 on Apple silicon, amd64 on Intel Macs
VM_ARCH ?= $(shell uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')

.PHONY: help setup deps dev dev-init dev-server dev-agent dev-web test lint check-design api-types web build build-linux \
        vm-create vm-agent app-setup app-run deploy deploy-agent install-server install-agent remote-add-server clean

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

dev-init: ## Create local DB, admin login (.dev/admin.password) and two demo servers (.dev/*.token)
	@mkdir -p $(DEV)
	@if [ -f $(DEV)/admin.password ]; then echo "already initialised ($(DEV)); run 'make clean' to reset"; exit 0; fi; \
	pw=$$(LC_ALL=C tr -dc 'a-zA-Z0-9' </dev/urandom | head -c 16); \
	if [ -f $(DATA)/monitor.db ]; then \
	  VPSMON_INIT_PASSWORD=$$pw go run ./cmd/server admin reset-password --data $(DATA) >/dev/null || exit 1; \
	else \
	  VPSMON_INIT_PASSWORD=$$pw go run ./cmd/server init --data $(DATA) >/dev/null && \
	  go run ./cmd/server add-server --data $(DATA) --name Mac-Fake --limit-gb 1000 --reset-day 1 > $(DEV)/agent.token && \
	  go run ./cmd/server add-server --data $(DATA) --name Orb-VM --limit-gb 500 --reset-day 15 > $(DEV)/agent-vm.token || exit 1; \
	fi; \
	echo "$$pw" > $(DEV)/admin.password && chmod 600 $(DEV)/admin.password && \
	echo "web login: admin / $$pw"

dev-server: ## Run the server on $(LISTEN)
	go run ./cmd/server run --data $(DATA) --listen $(LISTEN)

dev-agent: ## Run an agent with fake metrics against the local server
	go run ./cmd/agent --server http://127.0.0.1:8080 --token-file $(DEV)/agent.token --fake --interval 3s

dev-web: ## Run Vite dev server on :5173 (proxies /api to :8080)
	cd web && npm run dev

test: ## Run Go tests
	go test ./...

lint: web/node_modules/.package-lock.json ## gofmt + go vet + Web type-check
	@# 未经 gofmt 格式化的 Go 文件直接失败，列出文件
	@unformatted="$$(gofmt -l cmd internal)"; \
	if [ -n "$$unformatted" ]; then \
		echo "以下 Go 文件未格式化，请运行 gofmt -w cmd internal："; \
		echo "$$unformatted"; exit 1; \
	fi
	go vet ./...
	cd web && npx vue-tsc --noEmit

check-design: ## Check design doc numbering and section references (design 40.9.3)
	python3 scripts/check_design_refs.py

api-types: ## Regenerate web/src/api.gen.ts from api/openapi.yaml (design 19.0.1)
	ruby scripts/check-openapi.rb api/openapi.yaml
	ruby scripts/gen-api-types.rb

# ---------- builds ----------

# 依赖变化（package-lock.json 比已安装的新）时自动 npm ci：拉取新代码后直接 make build 即可，
# 不会因为缺少新增的依赖而构建失败。npm ci 会写入 node_modules/.package-lock.json，作为“已安装”的标记。
web/node_modules/.package-lock.json: web/package-lock.json
	cd web && npm ci

web: web/node_modules/.package-lock.json ## Build Web into web/dist (embedded by the server)
	cd web && npm run build

build: web ## Build server + agent for this machine into bin/
	CGO_ENABLED=0 $(GOBUILD) -ldflags "$(LDFLAGS)" -o bin/vpsmon-server ./cmd/server
	CGO_ENABLED=0 $(GOBUILD) -ldflags "$(LDFLAGS)" -o bin/vpsmon-agent ./cmd/agent

# Agent 覆盖安装脚本支持的全部架构，构建名与设计 27.5.4 一致（armv7 / armv6 即 GOARCH=arm + GOARM；
# mips / mipsle 为软浮点，路由器等多数 MIPS 设备没有 FPU；386 用 GO386=softfloat，不依赖 SSE2，
# Pentium III、AMD Geode、i486 / i586 等较老的 32 位 x86 也能运行）。
# 面板只构建 amd64 / arm64：面板机器通常是常见 VPS，其他架构按需再加。
# 全部静态编译（CGO_ENABLED=0），不区分 glibc / musl。
AGENT_ARCHS  := amd64 arm64 armv7 armv6 386 riscv64 mips mipsle
SERVER_ARCHS := amd64 arm64

build-linux: web ## Cross-compile agent (8 arches) + server (amd64/arm64) into dist/
	@mkdir -p dist && rm -f dist/vpsmon-* dist/SHA256SUMS
	@for arch in $(AGENT_ARCHS); do \
	  goarch=$$arch; goarm=; gomips=; go386=; \
	  case $$arch in armv7) goarch=arm; goarm=7;; armv6) goarch=arm; goarm=6;; mips|mipsle) gomips=softfloat;; 386) go386=softfloat;; esac; \
	  echo "→ agent  linux/$$arch"; \
	  CGO_ENABLED=0 GOOS=linux GOARCH=$$goarch GOARM=$$goarm GOMIPS=$$gomips GO386=$$go386 $(GOBUILD) -ldflags "$(LDFLAGS)" -o dist/vpsmon-agent-linux-$$arch ./cmd/agent || exit 1; \
	done
	@for arch in $(SERVER_ARCHS); do \
	  echo "→ server linux/$$arch"; \
	  CGO_ENABLED=0 GOOS=linux GOARCH=$$arch $(GOBUILD) -ldflags "$(LDFLAGS)" -o dist/vpsmon-server-linux-$$arch ./cmd/server || exit 1; \
	done
	@cd dist && shasum -a 256 vpsmon-* > SHA256SUMS && echo "dist/SHA256SUMS written"

installer: build-linux ## Release files for VERSION=x.y.z: agent-x.y.z.sh, manifest.json, SHA256SUMS into dist/ (no signing)
	@test -n "$(VERSION)" || (echo "usage: make installer VERSION=0.2.0" && exit 1)
	go run ./cmd/vpsmon-release prepare --version $(VERSION) --dist dist

# ---------- Linux VM on your Mac (OrbStack) for real agent metrics ----------

loadtest: ## Panel capacity: temp panel + 100 simulated agents, steady load then reconnect burst (design 3.2)
	@scripts/loadtest.sh 100 10s 60s 180

agent-footprint: build-linux ## On Linux: run the real agent 60s and report RSS / CPU against design 4.2
	@scripts/agent-footprint.sh

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

install-server: ## On the VPS, after `make build`: install/upgrade the server as a systemd service
	sudo scripts/install.sh server

install-agent: ## On the VPS, after `make build`: make install-agent SERVER=https://... TOKEN_FILE=path
	@test -n "$(SERVER)" -a -n "$(TOKEN_FILE)" || (echo "usage: make install-agent SERVER=https://monitor.example.com TOKEN_FILE=./node.token" && exit 1)
	sudo scripts/install.sh agent $(SERVER) $(TOKEN_FILE)

remote-add-server: ## Add a server on the VPS: make remote-add-server VPS=root@host NAME=HK-1 (token → ./NAME.token)
	@test -n "$(VPS)" -a -n "$(NAME)" || (echo "usage: make remote-add-server VPS=user@host NAME=HK-1" && exit 1)
	ssh $(VPS) "sudo -u vpsmon /usr/local/bin/vpsmon-server add-server --data /var/lib/vpsmon --name '$(NAME)'" > $(NAME).token
	@chmod 600 $(NAME).token && echo "agent token saved to ./$(NAME).token (do not commit it)"

clean: ## Remove local dev data and build output
	rm -rf $(DEV) bin dist
