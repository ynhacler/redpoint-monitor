#!/usr/bin/env bash
# One-time development setup on macOS. Safe to re-run.
set -euo pipefail
cd "$(dirname "$0")/.."

say() { printf "\n\033[1;34m==> %s\033[0m\n" "$*"; }
ask() { read -r -p "$1 [y/N] " a; [[ "$a" =~ ^[Yy]$ ]]; }

if ! command -v brew >/dev/null; then
  say "Homebrew not found. Install it first:"
  # shellcheck disable=SC2016  # 有意使用单引号：原样打印 Homebrew 官方安装命令，由用户自行复制执行
  echo '/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"'
  exit 1
fi

say "Go, Node, minisign"
brew install go node minisign

if ! command -v orb >/dev/null; then
  if ask "Install OrbStack (lightweight Linux VMs, used to test the real agent)?"; then
    brew install --cask orbstack
    echo "Open OrbStack once to finish setup, then run: make vm-create"
  fi
fi

if ! command -v flutter >/dev/null; then
  if ask "Install Flutter (needed for the iOS/Android app)?"; then
    brew install --cask flutter
  fi
fi

if ! xcode-select -p >/dev/null 2>&1; then
  say "Xcode command line tools"
  xcode-select --install || true
fi
if [ ! -d /Applications/Xcode.app ]; then
  echo "Note: full Xcode (App Store) is required for the iOS simulator. Android Studio for the Android emulator."
fi

say "Project dependencies"
go mod tidy
go mod download
(cd web && npm install)

say "Local dev database"
make dev-init

if [ ! -d .git ]; then git init -q && git add -A && git commit -qm "Initial scaffold"; fi

say "Done. Next:"
cat <<'EOF'
  make dev          # server + fake agent + Web → http://localhost:5173
  make test         # Go tests
  claude            # start vibe coding (reads CLAUDE.md)

  make vm-create && make vm-agent     # real Linux metrics in an OrbStack VM
  make app-setup && make app-run      # Flutter app (needs Xcode / Android Studio)
EOF
command -v flutter >/dev/null && echo && echo "Tip: run 'flutter doctor' to check iOS/Android toolchains."
