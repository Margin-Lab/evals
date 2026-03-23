#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT_REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
ROOT_DIR="${MARGINLAB_PUBLISH_ROOT:-${SCRIPT_REPO_ROOT}}"
HELPER_PY="${SCRIPT_DIR}/publish_case_images_lib.py"
PYTHON_BIN="${PYTHON_BIN:-python3}"
DOCKER_BIN="${DOCKER_BIN:-docker}"
REGISTRY_PREFIX="ghcr.io/margin-lab/evals"
PLATFORM="linux/amd64"
STABLE_TAG="manual"
MANIFEST_PATH=""
RESUME_PATH=""
REMOTE_TIMEOUT_SEC="${REMOTE_TIMEOUT_SEC:-30}"
SOURCE_URL="${PUBLISH_SOURCE_URL:-https://github.com/Margin-Lab/evals}"
DRY_RUN=0
FORCE=0
NO_APPLY=0
SELECT_ALL=0
WORKERS="${PUBLISH_WORKERS:-4}"
CLEANUP_EVERY="${CLEANUP_EVERY_BUILDS:-0}"

declare -a SELECTED_SUITES=()
declare -a SELECTED_CASES=()

usage() {
  cat <<'EOF'
Usage: scripts/publish_case_images/publish-case-images.sh [selector] [options]

Selectors:
  --all
  --suite <suite-name>          Repeatable
  --case <suite-name>/<case-id> Repeatable

Options:
  --registry-prefix <prefix>    Default: ghcr.io/margin-lab/evals
  --platform <platform>         Default: linux/amd64
  --stable-tag <tag>            Default: manual
  --manifest-out <path>         Default: .tmp/publish-case-images/<timestamp>.json
  --resume-from <path>          Resume from an existing manifest
  --workers <n>                 Default: 4
  --cleanup-every <n>           After every N completed builds, run docker buildx prune
  --dry-run                     Discover and write manifest only
  --force                       Rebuild and repush even if manifest/registry already has the image
  --no-apply                    Skip case.toml rewrites after publishing
  --help
EOF
}

log() {
  printf '%s\n' "$*" >&2
}

warn() {
  printf 'warning: %s\n' "$*" >&2
}

die() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

helper() {
  "${PYTHON_BIN}" "${HELPER_PY}" "$@"
}

manifest_upsert() {
  local suite=$1
  local case_id=$2
  shift 2
  helper manifest-upsert --path "${MANIFEST_PATH}" --suite "${suite}" --case-id "${case_id}" "$@"
}

timestamp_utc() {
  date -u +"%Y-%m-%dT%H:%M:%SZ"
}

sanitize_segment() {
  helper sanitize-repo-component --value "$1"
}

emit_prefixed_output() {
  local prefix=$1
  local output=$2
  local line
  [[ -n "${output}" ]] || return 0
  while IFS= read -r line; do
    printf '[%s] %s\n' "${prefix}" "${line}" >&2
  done <<< "${output}"
}

run_cleanup_batch() {
  local label=$1
  local prune_output=""

  log "[${label}] Pruning buildx cache"
  if ! prune_output="$("${DOCKER_BIN}" buildx prune -f 2>&1)"; then
    emit_prefixed_output "${label}" "${prune_output}"
    warn "docker buildx prune failed during cleanup; continuing"
  else
    emit_prefixed_output "${label}" "${prune_output}"
  fi
}

drain_cleanup_batch() {
  local label=$1
  (( CLEANUP_EVERY > 0 )) || return 0
  run_cleanup_batch "${label}"
}

maybe_prune_build_cache() {
  local label=$1
  local triggered=0
  (( CLEANUP_EVERY > 0 )) || return 0

  triggered="$(helper cleanup-register-build-trigger --path "${CLEANUP_STATE_PATH}" --threshold "${CLEANUP_EVERY}")"
  [[ "${triggered}" == "1" ]] || return 0
  run_cleanup_batch "${label}"
}

discover_suite_cases() {
  local suite=$1
  local suite_dir="${ROOT_DIR}/suites/${suite}"
  [[ -d "${suite_dir}" ]] || die "suite not found: ${suite}"
  while IFS= read -r dockerfile; do
    local case_dir env_dir case_id case_toml
    env_dir="$(dirname "${dockerfile}")"
    case_dir="$(dirname "${env_dir}")"
    case_id="$(basename "${case_dir}")"
    case_toml="${case_dir}/case.toml"
    [[ -f "${case_toml}" ]] || die "case.toml not found for ${suite}/${case_id}"
    printf '%s\t%s\t%s\t%s\t%s\t%s\n' \
      "${suite}" \
      "${case_id}" \
      "${case_dir}" \
      "${case_toml}" \
      "${env_dir}" \
      "${dockerfile}"
  done < <(find "${suite_dir}/cases" -path '*/env/Dockerfile' | LC_ALL=C sort)
}

discover_explicit_case() {
  local suite_case=$1
  [[ "${suite_case}" == */* ]] || die "--case must use <suite>/<case-id>, got ${suite_case}"
  local suite="${suite_case%%/*}"
  local case_id="${suite_case#*/}"
  local case_dir="${ROOT_DIR}/suites/${suite}/cases/${case_id}"
  local case_toml="${case_dir}/case.toml"
  local env_dir="${case_dir}/env"
  local dockerfile="${env_dir}/Dockerfile"
  [[ -f "${dockerfile}" ]] || die "Dockerfile-backed case not found: ${suite_case}"
  [[ -f "${case_toml}" ]] || die "case.toml not found for ${suite_case}"
  printf '%s\t%s\t%s\t%s\t%s\t%s\n' \
    "${suite}" \
    "${case_id}" \
    "${case_dir}" \
    "${case_toml}" \
    "${env_dir}" \
    "${dockerfile}"
}

require_prereqs() {
  command -v "${PYTHON_BIN}" >/dev/null 2>&1 || die "python executable not found: ${PYTHON_BIN}"
  [[ -f "${HELPER_PY}" ]] || die "helper script not found: ${HELPER_PY}"
  if (( DRY_RUN == 0 )); then
    command -v "${DOCKER_BIN}" >/dev/null 2>&1 || die "docker executable not found: ${DOCKER_BIN}"
    "${DOCKER_BIN}" buildx version >/dev/null 2>&1 || die "docker buildx is required for publishing"
  fi
}

process_case_record() {
  local record=$1
  local suite case_id case_dir case_toml env_dir dockerfile
  local key context_hash suite_component case_component current_repository
  local content_tag content_ref stable_ref now resume_info resumed_status
  local resumed_digest resumed_publish_status remote_digest_ref build_output
  local published_digest_ref
  local -a build_cmd=()

  IFS=$'\t' read -r suite case_id case_dir case_toml env_dir dockerfile <<< "${record}"
  key="${suite}/${case_id}"

  context_hash="$(helper hash-context --path "${env_dir}")"
  suite_component="$(sanitize_segment "${suite}")"
  case_component="$(sanitize_segment "${case_id}")"
  current_repository="${REGISTRY_PREFIX}/${suite_component}/${case_component}"
  content_tag="sha-${context_hash:0:16}"
  content_ref="${current_repository}:${content_tag}"
  stable_ref="${current_repository}:${STABLE_TAG}"
  now="$(timestamp_utc)"

  if (( FORCE == 0 )); then
    if resume_info="$(helper manifest-resume --path "${MANIFEST_PATH}" --suite "${suite}" --case-id "${case_id}" --context-hash "${context_hash}" 2>/dev/null)"; then
      IFS=$'\t' read -r resumed_status resumed_digest resumed_publish_status <<< "${resume_info}"
      log "Reusing manifest digest for ${key}: ${resumed_digest}"
      return 0
    fi
  fi

  manifest_upsert "${suite}" "${case_id}" \
    --set "case_dir=${case_dir}" \
    --set "case_toml_path=${case_toml}" \
    --set "context_dir=${env_dir}" \
    --set "dockerfile_path=${dockerfile}" \
    --set "context_hash=${context_hash}" \
    --set "repository=${current_repository}" \
    --set "stable_tag=${STABLE_TAG}" \
    --set "content_tag=${content_tag}" \
    --set "stable_ref=${stable_ref}" \
    --set "content_ref=${content_ref}" \
    --set "status=planned" \
    --set "publish_status=" \
    --set "digest_ref=" \
    --set "error=" \
    --set "published_at="

  if (( DRY_RUN == 1 )); then
    return 0
  fi

  if (( FORCE == 0 )); then
    if remote_digest_ref="$(helper remote-digest --docker-bin "${DOCKER_BIN}" --reference "${content_ref}" --platform "${PLATFORM}" --timeout "${REMOTE_TIMEOUT_SEC}" 2>/dev/null)"; then
      manifest_upsert "${suite}" "${case_id}" \
        --set "status=skipped_existing" \
        --set "publish_status=skipped_existing" \
        --set "digest_ref=${remote_digest_ref}" \
        --set "error=" \
        --set "published_at=${now}"
      log "Skipping remote-existing image for ${key}: ${remote_digest_ref}"
      return 0
    fi
  fi

  log "Building ${key} -> ${content_ref}"
  build_cmd=(
    "${DOCKER_BIN}" buildx build
    --platform "${PLATFORM}"
    --push
    --progress=plain
    -t "${content_ref}"
    -t "${stable_ref}"
    -f "${dockerfile}"
    --label "org.opencontainers.image.source=${SOURCE_URL}"
    --label "io.marginlab.eval.suite=${suite}"
    --label "io.marginlab.eval.case_id=${case_id}"
  )
  if [[ -n "${git_revision}" ]]; then
    build_cmd+=(--label "org.opencontainers.image.revision=${git_revision}")
  fi
  build_cmd+=("${env_dir}")

  if ! build_output="$("${build_cmd[@]}" 2>&1)"; then
    emit_prefixed_output "${key}" "${build_output}"
    manifest_upsert "${suite}" "${case_id}" \
      --set "status=failed" \
      --set "error=build failed" \
      --set "publish_status=" \
      --set "digest_ref="
    maybe_prune_build_cache "${key}"
    return 0
  fi
  emit_prefixed_output "${key}" "${build_output}"

  published_digest_ref=""
  if published_digest_ref="$(helper remote-digest --docker-bin "${DOCKER_BIN}" --reference "${content_ref}" --platform "${PLATFORM}" --timeout "${REMOTE_TIMEOUT_SEC}" 2>/dev/null)"; then
    :
  else
    manifest_upsert "${suite}" "${case_id}" \
      --set "status=failed" \
      --set "error=unable to resolve pushed digest for ${content_ref}" \
      --set "publish_status=" \
      --set "digest_ref="
    maybe_prune_build_cache "${key}"
    return 0
  fi

  manifest_upsert "${suite}" "${case_id}" \
    --set "status=published" \
    --set "publish_status=published" \
    --set "digest_ref=${published_digest_ref}" \
    --set "error=" \
    --set "published_at=${now}"
  maybe_prune_build_cache "${key}"
  return 0
}

run_worker() {
  local worker_index=$1
  local records_path=$2
  local record
  local line_no=0

  while IFS= read -r record; do
    ((line_no += 1))
    if (( (line_no - 1) % WORKERS != worker_index )); then
      continue
    fi
    process_case_record "${record}"
  done < "${records_path}"
}

while (($# > 0)); do
  case "$1" in
    --all)
      SELECT_ALL=1
      shift
      ;;
    --suite)
      (($# >= 2)) || die "--suite requires a value"
      SELECTED_SUITES+=("$2")
      shift 2
      ;;
    --case)
      (($# >= 2)) || die "--case requires a value"
      SELECTED_CASES+=("$2")
      shift 2
      ;;
    --registry-prefix)
      (($# >= 2)) || die "--registry-prefix requires a value"
      REGISTRY_PREFIX="$2"
      shift 2
      ;;
    --platform)
      (($# >= 2)) || die "--platform requires a value"
      PLATFORM="$2"
      shift 2
      ;;
    --stable-tag)
      (($# >= 2)) || die "--stable-tag requires a value"
      STABLE_TAG="$2"
      shift 2
      ;;
    --manifest-out)
      (($# >= 2)) || die "--manifest-out requires a value"
      MANIFEST_PATH="$2"
      shift 2
      ;;
    --resume-from)
      (($# >= 2)) || die "--resume-from requires a value"
      RESUME_PATH="$2"
      shift 2
      ;;
    --workers)
      (($# >= 2)) || die "--workers requires a value"
      WORKERS="$2"
      shift 2
      ;;
    --cleanup-every)
      (($# >= 2)) || die "--cleanup-every requires a value"
      CLEANUP_EVERY="$2"
      shift 2
      ;;
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    --force)
      FORCE=1
      shift
      ;;
    --no-apply)
      NO_APPLY=1
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      die "unknown argument: $1"
      ;;
  esac
done

if (( SELECT_ALL == 1 )) && (( ${#SELECTED_SUITES[@]} > 0 || ${#SELECTED_CASES[@]} > 0 )); then
  die "--all cannot be combined with --suite or --case"
fi
if (( SELECT_ALL == 0 )) && (( ${#SELECTED_SUITES[@]} == 0 )) && (( ${#SELECTED_CASES[@]} == 0 )); then
  die "one selector is required: --all, --suite, or --case"
fi
if [[ -n "${RESUME_PATH}" ]] && [[ -n "${MANIFEST_PATH}" ]] && [[ "${RESUME_PATH}" != "${MANIFEST_PATH}" ]]; then
  die "--resume-from and --manifest-out must match when both are provided"
fi

REGISTRY_PREFIX="${REGISTRY_PREFIX%/}"
[[ -n "${REGISTRY_PREFIX}" ]] || die "--registry-prefix must not be empty"
[[ -n "${PLATFORM}" ]] || die "--platform must not be empty"
[[ -n "${STABLE_TAG}" ]] || die "--stable-tag must not be empty"
[[ "${WORKERS}" =~ ^[1-9][0-9]*$ ]] || die "--workers must be a positive integer"
[[ "${CLEANUP_EVERY}" =~ ^[0-9]+$ ]] || die "--cleanup-every must be a non-negative integer"

if [[ -n "${RESUME_PATH}" ]]; then
  [[ -f "${RESUME_PATH}" ]] || die "resume manifest not found: ${RESUME_PATH}"
  MANIFEST_PATH="${RESUME_PATH}"
fi
if [[ -z "${MANIFEST_PATH}" ]]; then
  MANIFEST_PATH="${ROOT_DIR}/.tmp/publish-case-images/$(date -u +"%Y%m%dT%H%M%SZ").json"
fi

require_prereqs
mkdir -p "$(dirname "${MANIFEST_PATH}")"
helper manifest-init --path "${MANIFEST_PATH}"

selection_file="$(mktemp)"
sorted_file="$(mktemp)"
success_file="$(mktemp)"
cleanup_state_file="$(mktemp)"
rm -f "${cleanup_state_file}"
cleanup() {
  rm -f "${selection_file}" "${sorted_file}" "${success_file}" "${cleanup_state_file}"
}
trap cleanup EXIT

if (( SELECT_ALL == 1 )); then
  while IFS= read -r suite_dir; do
    suite_name="$(basename "${suite_dir}")"
    discover_suite_cases "${suite_name}" >> "${selection_file}"
  done < <(find "${ROOT_DIR}/suites" -mindepth 1 -maxdepth 1 -type d | LC_ALL=C sort)
fi

if (( ${#SELECTED_SUITES[@]} > 0 )); then
  for suite in "${SELECTED_SUITES[@]}"; do
    discover_suite_cases "${suite}" >> "${selection_file}"
  done
fi

if (( ${#SELECTED_CASES[@]} > 0 )); then
  for suite_case in "${SELECTED_CASES[@]}"; do
    discover_explicit_case "${suite_case}" >> "${selection_file}"
  done
fi

LC_ALL=C sort -u "${selection_file}" > "${sorted_file}"
if [[ ! -s "${sorted_file}" ]]; then
  die "selector resolved to zero Dockerfile-backed cases"
fi

git_revision=""
if git_revision="$(git -C "${SCRIPT_REPO_ROOT}" rev-parse HEAD 2>/dev/null)"; then
  :
else
  git_revision=""
fi

CLEANUP_STATE_PATH="${cleanup_state_file}"
helper cleanup-init --path "${CLEANUP_STATE_PATH}"

if (( DRY_RUN == 1 )); then
  while IFS= read -r record; do
    process_case_record "${record}"
  done < "${sorted_file}"
  PLANNED_COUNT="$(helper manifest-count --path "${MANIFEST_PATH}" --status planned)"
  log "Planned ${PLANNED_COUNT} cases. Manifest written to ${MANIFEST_PATH}"
  exit 0
fi

declare -a worker_pids=()
worker_errors=0
worker_index=0
while (( worker_index < WORKERS )); do
  run_worker "${worker_index}" "${sorted_file}" &
  worker_pids+=("$!")
  ((worker_index += 1))
done

for worker_pid in "${worker_pids[@]}"; do
  if ! wait "${worker_pid}"; then
    ((worker_errors += 1))
  fi
done

drain_cleanup_batch "final"

if (( worker_errors > 0 )); then
  die "${worker_errors} worker(s) exited unexpectedly; inspect ${MANIFEST_PATH}"
fi

FAILED_COUNT="$(helper manifest-count --path "${MANIFEST_PATH}" --status failed)"
PLANNED_COUNT="$(helper manifest-count --path "${MANIFEST_PATH}" --status planned)"
PUBLISHED_COUNT="$(helper manifest-count --path "${MANIFEST_PATH}" --publish-status published)"
SKIPPED_COUNT="$(helper manifest-count --path "${MANIFEST_PATH}" --publish-status skipped_existing)"
PATCHED_COUNT="$(helper manifest-count --path "${MANIFEST_PATH}" --status patched)"
CLEANUP_RUNS="$(helper cleanup-run-count --path "${CLEANUP_STATE_PATH}")"

if (( FAILED_COUNT > 0 )); then
  log "Skipping case.toml rewrites because ${FAILED_COUNT} case(s) failed."
  if (( PLANNED_COUNT > 0 )); then
    log "Manifest still has ${PLANNED_COUNT} planned case(s)."
  fi
  log "Manifest: ${MANIFEST_PATH}"
  exit 1
fi

if (( NO_APPLY == 1 )); then
  log "Published ${PUBLISHED_COUNT} and skipped ${SKIPPED_COUNT} existing images with ${WORKERS} worker(s). Cleanup runs: ${CLEANUP_RUNS}. Manifest: ${MANIFEST_PATH}"
  exit 0
fi

helper manifest-success-records --path "${MANIFEST_PATH}" > "${success_file}"
while IFS=$'\t' read -r suite case_id case_toml digest_ref publish_status; do
  key="${suite}/${case_id}"
  [[ -n "${digest_ref}" ]] || die "missing digest ref for ${key} after successful publish pass"
  patch_mode="$(helper patch-case --path "${case_toml}" --digest-ref "${digest_ref}" --write)"
  manifest_upsert "${suite}" "${case_id}" \
    --set "status=patched" \
    --set "publish_status=${publish_status}" \
    --set "digest_ref=${digest_ref}" \
    --set "error="
  log "Patched ${key} (${patch_mode})"
done < "${success_file}"

PATCHED_COUNT="$(helper manifest-count --path "${MANIFEST_PATH}" --status patched)"
log "Published ${PUBLISHED_COUNT}, skipped ${SKIPPED_COUNT}, patched ${PATCHED_COUNT} with ${WORKERS} worker(s). Cleanup runs: ${CLEANUP_RUNS}. Manifest: ${MANIFEST_PATH}"
