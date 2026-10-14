#!/usr/bin/env bash
# Quick installer for OmniAuth Pro Plugin for CPAMC
set -euo pipefail

REPO="WiloxDev/omniauth-pro-plugin"
PLUGIN_DIR="${CPAMC_PLUGIN_DIR:-$HOME/services/cliproxy/plugins}"
BINARY_NAME="omni-auth-pro-linux-amd64.so"

echo "=========================================="
echo "  OmniAuth Pro Plugin Installer for CPAMC "
echo "=========================================="

ARCH=$(uname -m)
OS=$(uname -s | tr '[:upper:]' '[:lower:]')

mkdir -p "$PLUGIN_DIR"

if command -v go >/dev/null 2>&1 && command -v gcc >/dev/null 2>&1; then
    echo "Go and GCC detected. Building OmniAuth Pro from source..."
    CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -buildmode=c-shared -o "${PLUGIN_DIR}/${BINARY_NAME}" .
    echo "OmniAuth Pro Plugin successfully installed to: ${PLUGIN_DIR}/${BINARY_NAME}"
    exit 0
else
    echo "Error: Go and GCC are required to compile OmniAuth Pro."
    exit 1
fi
