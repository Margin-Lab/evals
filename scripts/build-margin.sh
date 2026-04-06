#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="${MARGIN_VERSION:-dev}"
BUILD_DATETIME="${BUILD_DATETIME:-$(date -u +"%Y-%m-%dT%H:%M:%SZ")}"
OUTPUT_PATH=""
export GOTOOLCHAIN="${GOTOOLCHAIN:-go1.25.4}"

fail() {
  echo "error: $*" >&2
  exit 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --output)
      [[ $# -ge 2 ]] || fail "--output requires a value"
      OUTPUT_PATH="$2"
      shift 2
      ;;
    *)
      fail "unknown argument: $1"
      ;;
  esac
done

[[ -n "${OUTPUT_PATH}" ]] || fail "--output is required"

mkdir -p "$(dirname "${OUTPUT_PATH}")"

(
  cd "${ROOT_DIR}/cli"
  go build \
    -ldflags "-X github.com/marginlab/margin-eval/cli/internal/buildinfo.Version=${VERSION} -X github.com/marginlab/margin-eval/cli/internal/buildinfo.BuildTime=${BUILD_DATETIME}" \
    -o "${OUTPUT_PATH}" \
    ./cmd/margin
)
