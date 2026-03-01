#!/usr/bin/env bash
set -euo pipefail

# Installs codemap (Homebrew) if missing, then runs the recommended setup flow.
# Usage:
#   ./scripts/onboard.sh                # current directory as project root
#   ./scripts/onboard.sh /path/to/repo  # explicit project root

PROJECT_ROOT="${1:-$PWD}"

if [[ ! -d "$PROJECT_ROOT" ]]; then
  echo "Error: project root not found: $PROJECT_ROOT" >&2
  exit 1
fi

if ! command -v codemap >/dev/null 2>&1; then
  if command -v brew >/dev/null 2>&1; then
    echo "Installing codemap via Homebrew..."
    brew tap JordanCoin/tap
    brew install codemap
  else
    echo "Error: codemap is not installed and Homebrew is unavailable." >&2
    echo "Install codemap first: https://github.com/JordanCoin/codemap#install" >&2
    exit 1
  fi
fi

echo "Running codemap setup for: $PROJECT_ROOT"
codemap setup "$PROJECT_ROOT"
