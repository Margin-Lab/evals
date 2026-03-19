#!/usr/bin/env bash
set -euo pipefail

# Step 1: Define paths and fixed suite settings.
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SUITE_DIR="${ROOT_DIR}/suites/real-five"
AGENT_CONFIG_DIR="${ROOT_DIR}/configs/agent-configs/codex-default"
EVAL_PATH="${ROOT_DIR}/configs/evals/real-five-local.toml"
MARGIN_BIN="${ROOT_DIR}/bin/margin"
CASE_IMAGE="node:22-bullseye@sha256:30abf318b514315981eadbfe3af859b094e0ef6ff99cfefcc0d23f24ca43c84f"
CASE_TIMEOUT_SECONDS=120

# Step 2: Recreate clean suite/config directories.
mkdir -p "$(dirname "${SUITE_DIR}")"
ABS_SUITE_DIR="$(cd "$(dirname "${SUITE_DIR}")" && pwd)/$(basename "${SUITE_DIR}")"
rm -rf "${ABS_SUITE_DIR}"
mkdir -p "$(dirname "${EVAL_PATH}")"
rm -f "${EVAL_PATH}"

# Step 3: Initialize suite and cases using margin CLI commands.
"${MARGIN_BIN}" init suite --suite "${ABS_SUITE_DIR}" --name "real-five"
"${MARGIN_BIN}" init case --suite "${ABS_SUITE_DIR}" --case "env-basics"
"${MARGIN_BIN}" init case --suite "${ABS_SUITE_DIR}" --case "file-ops"
"${MARGIN_BIN}" init case --suite "${ABS_SUITE_DIR}" --case "text-pipeline"
"${MARGIN_BIN}" init case --suite "${ABS_SUITE_DIR}" --case "checksum-verify"
"${MARGIN_BIN}" init case --suite "${ABS_SUITE_DIR}" --case "jsonl-validation"

# Step 4: Initialize eval config using margin CLI commands.
"${MARGIN_BIN}" init eval-config \
  --eval "${EVAL_PATH}" \
  --name "real-five-local"

# Step 5: Define a helper that writes one case's real content.
write_case_files() {
  local case_name="$1"
  local description="$2"
  local prompt_body="$3"
  local test_body="$4"
  local image_mode="${5:-image}"
  local case_dir="${ABS_SUITE_DIR}/cases/${case_name}"

  if [[ "${image_mode}" == "dockerfile" ]]; then
    cat > "${case_dir}/case.toml" <<EOF
kind = "test_case"
name = "${case_name}"
description = "${description}"

test_cwd = "/suite/cases/${case_name}"
test_timeout_seconds = ${CASE_TIMEOUT_SECONDS}
EOF
    mkdir -p "${case_dir}/env"
    cat > "${case_dir}/env/Dockerfile" <<EOF
FROM ${CASE_IMAGE}
EOF
  else
    cat > "${case_dir}/case.toml" <<EOF
kind = "test_case"
name = "${case_name}"
description = "${description}"

image = "${CASE_IMAGE}"
test_cwd = "/suite/cases/${case_name}"
test_timeout_seconds = ${CASE_TIMEOUT_SECONDS}
EOF
  fi

  cat > "${case_dir}/prompt.md" <<EOF
${prompt_body}
EOF

  cat > "${case_dir}/tests/test.sh" <<EOF
#!/usr/bin/env bash
set -euo pipefail

${test_body}
EOF
  chmod +x "${case_dir}/tests/test.sh"
}

# Step 6: Populate each case with realistic prompts and test scripts.
write_case_files \
  "env-basics" \
  "Validate baseline shell/runtime environment values" \
  "Verify the runtime shell environment is healthy and report the key environment values you observed. Do not modify any files." \
  '[[ -n "${HOME:-}" ]]
[[ -n "${PWD:-}" ]]
[[ -n "${BASH_VERSION:-}" ]]
echo "home=${HOME}"
echo "pwd=${PWD}"
echo "bash=${BASH_VERSION}"'

write_case_files \
  "file-ops" \
  "Create and verify temporary files and directories" \
  "Confirm temporary filesystem operations work correctly. Keep all writes under /tmp." \
  'tmpdir="$(mktemp -d)"
trap "rm -rf ${tmpdir}" EXIT
printf "alpha\nbeta\ngamma\n" > "${tmpdir}/items.txt"
test -s "${tmpdir}/items.txt"
line_count="$(wc -l < "${tmpdir}/items.txt" | tr -d " ")"
[[ "${line_count}" == "3" ]]
grep -q "^beta$" "${tmpdir}/items.txt"
echo "line_count=${line_count}"'

write_case_files \
  "text-pipeline" \
  "Exercise common shell text pipelines" \
  "Run shell pipelines that normalize, transform, and aggregate simple text input." \
  'tmpdir="$(mktemp -d)"
trap "rm -rf ${tmpdir}" EXIT
printf "orange\napple\nbanana\napple\n" > "${tmpdir}/fruits.txt"
sort "${tmpdir}/fruits.txt" | uniq -c > "${tmpdir}/counts.txt"
grep -q "  *2 apple" "${tmpdir}/counts.txt"
grep -q "  *1 banana" "${tmpdir}/counts.txt"
grep -q "  *1 orange" "${tmpdir}/counts.txt"
echo "pipeline=ok"'

write_case_files \
  "checksum-verify" \
  "Compute and compare deterministic checksums" \
  "Verify deterministic checksum behavior for identical and different payloads." \
  'tmpdir="$(mktemp -d)"
trap "rm -rf ${tmpdir}" EXIT
printf "stable-payload\n" > "${tmpdir}/a.txt"
printf "stable-payload\n" > "${tmpdir}/b.txt"
printf "different-payload\n" > "${tmpdir}/c.txt"
sum_a="$(cksum "${tmpdir}/a.txt" | awk "{print \$1\":\"\$2}")"
sum_b="$(cksum "${tmpdir}/b.txt" | awk "{print \$1\":\"\$2}")"
sum_c="$(cksum "${tmpdir}/c.txt" | awk "{print \$1\":\"\$2}")"
[[ "${sum_a}" == "${sum_b}" ]]
[[ "${sum_a}" != "${sum_c}" ]]
echo "checksum=ok"'

write_case_files \
  "jsonl-validation" \
  "Validate JSONL formatting and required fields" \
  "Ensure JSONL records follow a strict one-object-per-line shape and include mandatory keys." \
  'tmpdir="$(mktemp -d)"
trap "rm -rf ${tmpdir}" EXIT
cat > "${tmpdir}/events.jsonl" <<'"'"'JSONL'"'"'
{"id":"evt-1","status":"ok","duration_ms":12}
{"id":"evt-2","status":"ok","duration_ms":7}
{"id":"evt-3","status":"ok","duration_ms":1}
JSONL

line_count="$(wc -l < "${tmpdir}/events.jsonl" | tr -d " ")"
[[ "${line_count}" == "3" ]]
grep -q '"'"'"id":"evt-1"'"'"' "${tmpdir}/events.jsonl"
grep -q '"'"'"status":"ok"'"'"' "${tmpdir}/events.jsonl"
grep -q '"'"'"duration_ms":'"'"' "${tmpdir}/events.jsonl"
echo "jsonl=ok"' \
  "dockerfile"

# Step 7: Print what was created and how to run it.
cat <<EOF
Created suite at:
  ${ABS_SUITE_DIR}
Using agent config at:
  ${AGENT_CONFIG_DIR}
Created eval config at:
  ${EVAL_PATH}

Cases:
  env-basics
  file-ops
  text-pipeline
  checksum-verify
  jsonl-validation

Run note:
  These cases set test_cwd to /suite/cases/<case>.
  The jsonl-validation case is Dockerfile-backed (cases/jsonl-validation/env/Dockerfile).
  Mount the suite directory into the container when running the CLI:

  margin run \\
    --suite ${ABS_SUITE_DIR} \\
    --agent-config ${AGENT_CONFIG_DIR} \\
    --eval ${EVAL_PATH} \\
    --agent-server-binary <path-to-agent-server-binary> \\
    --agent-bind ${ABS_SUITE_DIR}=/suite
EOF
