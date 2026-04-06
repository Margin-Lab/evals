#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DIR="${ROOT_DIR}/bin"
export GOTOOLCHAIN="${GOTOOLCHAIN:-go1.25.4}"

mkdir -p "${BIN_DIR}"

echo "Preparing embedded agent-server payloads..."
"${ROOT_DIR}/scripts/prepare-embedded-agent-server.sh"

echo "Building CLI binary..."
"${ROOT_DIR}/scripts/build-margin.sh" --output "${BIN_DIR}/margin"

echo "Build complete:"
echo "  ${BIN_DIR}/margin"
