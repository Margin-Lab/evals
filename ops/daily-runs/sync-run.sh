#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "Usage: $0 --source-run <run-dir> --destination-root <tracker-data-dir>" >&2
}

SOURCE_RUN=""
DESTINATION_ROOT=""
while (($# > 0)); do
  case "$1" in
    --source-run)
      SOURCE_RUN="${2:-}"
      shift 2
      ;;
    --destination-root)
      DESTINATION_ROOT="${2:-}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      usage
      exit 2
      ;;
  esac
done

if [ -z "${SOURCE_RUN}" ] || [ -z "${DESTINATION_ROOT}" ]; then
  usage
  exit 2
fi

run_name="${SOURCE_RUN##*/}"
if ! [[ "${run_name}" =~ ^[0-9]{8}-[0-9]{6}$ ]]; then
  echo "Source run directory must end in YYYYMMDD-HHMMSS: ${SOURCE_RUN}" >&2
  exit 2
fi
for filename in results.json statistics.json; do
  if [ ! -f "${SOURCE_RUN}/${filename}" ]; then
    echo "Completed source run is missing ${filename}: ${SOURCE_RUN}" >&2
    exit 1
  fi
done

destination_run="${DESTINATION_ROOT}/${run_name//-/_}"
mkdir -p "${destination_run}"
rsync -a \
  --include='results.json' \
  --include='statistics.json' \
  --exclude='*' \
  "${SOURCE_RUN}/" "${destination_run}/"

for filename in results.json statistics.json; do
  cmp -s "${SOURCE_RUN}/${filename}" "${destination_run}/${filename}" || {
    echo "Synced ${filename} does not match its source: ${destination_run}" >&2
    exit 1
  }
done
