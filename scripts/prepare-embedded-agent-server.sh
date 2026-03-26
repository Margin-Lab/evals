#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STAGING_DIR="${ROOT_DIR}/cli/internal/agentserverembed/generated"
export GOTOOLCHAIN="${GOTOOLCHAIN:-go1.25.4}"

mkdir -p "${STAGING_DIR}"

sha256_file() {
  local path="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "${path}" | awk '{print $1}'
    return
  fi
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "${path}" | awk '{print $1}'
    return
  fi
  echo "error: need sha256sum or shasum" >&2
  exit 1
}

echo "Building embedded agent-server payloads..."
(
  cd "${ROOT_DIR}/agent-server"
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o "${STAGING_DIR}/agent-server-linux-amd64" ./cmd/agent-server
  CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o "${STAGING_DIR}/agent-server-linux-arm64" ./cmd/agent-server
)

amd64_sha="$(sha256_file "${STAGING_DIR}/agent-server-linux-amd64")"
arm64_sha="$(sha256_file "${STAGING_DIR}/agent-server-linux-arm64")"

cat > "${STAGING_DIR}/manifest.json" <<EOF
{
  "schema_version": "v1",
  "binaries": [
    {
      "platform": "linux/amd64",
      "filename": "agent-server-linux-amd64",
      "sha256": "${amd64_sha}"
    },
    {
      "platform": "linux/arm64",
      "filename": "agent-server-linux-arm64",
      "sha256": "${arm64_sha}"
    }
  ]
}
EOF

echo "Embedded payload staging complete:"
echo "  ${STAGING_DIR}/agent-server-linux-amd64"
echo "  ${STAGING_DIR}/agent-server-linux-arm64"
echo "  ${STAGING_DIR}/manifest.json"
