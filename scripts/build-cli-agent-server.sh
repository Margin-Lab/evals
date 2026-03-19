#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DIR="${ROOT_DIR}/bin"

mkdir -p "${BIN_DIR}"

echo "Preparing embedded agent-server payloads..."
"${ROOT_DIR}/scripts/prepare-embedded-agent-server.sh"

echo "Building CLI binary..."
(
  cd "${ROOT_DIR}/cli"
  go build -o "${BIN_DIR}/margin" ./cmd/margin
)

echo "Build complete:"
echo "  ${BIN_DIR}/margin"
