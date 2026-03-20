#!/usr/bin/env bash
set -euo pipefail

REPO="${MARGIN_RELEASE_REPO:-Margin-Lab/evals}"
API_BASE_URL="${MARGIN_API_BASE_URL:-https://api.github.com}"
DOWNLOAD_BASE_URL="${MARGIN_DOWNLOAD_BASE_URL:-https://github.com}"
DEFAULT_INSTALL_DIR="${HOME}/.local/bin"
METADATA_PATH="${MARGIN_METADATA_PATH:-${HOME}/.margin/install.json}"

fail() {
  echo "error: $*" >&2
  exit 1
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "missing required command: $1"
}

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
  fail "need sha256sum or shasum"
}

json_escape() {
  printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

resolve_goos() {
  if [[ -n "${MARGIN_TARGET_OS:-}" ]]; then
    printf '%s' "${MARGIN_TARGET_OS}"
    return
  fi
  case "$(uname -s)" in
    Darwin) printf 'darwin' ;;
    Linux) printf 'linux' ;;
    *) fail "unsupported operating system: $(uname -s)" ;;
  esac
}

resolve_goarch() {
  if [[ -n "${MARGIN_TARGET_ARCH:-}" ]]; then
    printf '%s' "${MARGIN_TARGET_ARCH}"
    return
  fi
  case "$(uname -m)" in
    x86_64|amd64) printf 'amd64' ;;
    arm64|aarch64) printf 'arm64' ;;
    *) fail "unsupported architecture: $(uname -m)" ;;
  esac
}

resolve_latest_stable_tag() {
  local body
  body="$(curl -fsSL -H 'Accept: application/vnd.github+json' "${API_BASE_URL}/repos/${REPO}/releases/latest")" || fail "fetch latest stable release"
  local tag
  tag="$(printf '%s\n' "${body}" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"
  [[ -n "${tag}" ]] || fail "could not resolve latest stable release tag"
  printf '%s' "${tag}"
}

resolve_channel() {
  case "$1" in
    *-beta.*) printf 'beta' ;;
    *) printf 'stable' ;;
  esac
}

main() {
  need_cmd curl
  need_cmd tar

  local install_dir="${MARGIN_INSTALL_DIR:-${DEFAULT_INSTALL_DIR}}"
  [[ -n "${install_dir}" ]] || fail "MARGIN_INSTALL_DIR must not be empty"

  local tag="${MARGIN_VERSION:-}"
  if [[ -z "${tag}" ]]; then
    tag="$(resolve_latest_stable_tag)"
  fi

  local channel
  channel="$(resolve_channel "${tag}")"
  local goos
  goos="$(resolve_goos)"
  local goarch
  goarch="$(resolve_goarch)"

  local archive_name="margin_${tag}_${goos}_${goarch}.tar.gz"
  local checksum_name="margin_${tag}_SHA256SUMS.txt"
  local temp_dir
  temp_dir="$(mktemp -d)"
  trap 'rm -rf "${temp_dir:-}"' EXIT

  local archive_path="${temp_dir}/${archive_name}"
  local checksum_path="${temp_dir}/${checksum_name}"

  echo "Downloading Margin ${tag} for ${goos}/${goarch}..."
  curl -fsSL "${DOWNLOAD_BASE_URL}/${REPO}/releases/download/${tag}/${archive_name}" -o "${archive_path}" || fail "download ${archive_name}"
  curl -fsSL "${DOWNLOAD_BASE_URL}/${REPO}/releases/download/${tag}/${checksum_name}" -o "${checksum_path}" || fail "download ${checksum_name}"

  local expected
  expected="$(awk -v target="./${archive_name}" '$2 == target { print $1; exit }' "${checksum_path}")"
  [[ -n "${expected}" ]] || fail "checksum for ${archive_name} not found"
  local actual
  actual="$(sha256_file "${archive_path}")"
  [[ "${expected}" == "${actual}" ]] || fail "checksum mismatch for ${archive_name}"

  mkdir -p "${install_dir}" "$(dirname "${METADATA_PATH}")"
  tar -xzf "${archive_path}" -C "${temp_dir}" || fail "extract ${archive_name}"

  local binary_path="${install_dir}/margin"
  mv "${temp_dir}/margin" "${binary_path}" || fail "install margin to ${binary_path}"
  chmod 0755 "${binary_path}" || fail "chmod ${binary_path}"

  cat > "${METADATA_PATH}" <<EOF
{
  "schema_version": 1,
  "installed_via": "official-installer",
  "repo": "$(json_escape "${REPO}")",
  "channel": "$(json_escape "${channel}")",
  "binary_path": "$(json_escape "${binary_path}")",
  "installed_version": "$(json_escape "${tag}")"
}
EOF

  echo "Installed margin ${tag} to ${binary_path}"
  if [[ ":${PATH}:" != *":${install_dir}:"* ]]; then
    echo
    echo "Add ${install_dir} to your PATH before using margin."
  fi
  echo
  echo "Docker must be installed and running before you execute margin evals."
  echo "Run 'margin help' to verify the install and 'margin update' for manual updates."
}

main "$@"
