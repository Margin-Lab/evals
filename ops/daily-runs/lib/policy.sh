#!/usr/bin/env bash

# Load and validate the daily publication policy into the caller's shell.
#
# Infrastructure failures are excluded from the accuracy denominator. By
# default, one missing test outcome is tolerated for the 50-instance suite;
# MARGINLAB_MINIMUM_VALID_INSTANCES remains the explicit safety bound.
marginlab_load_daily_run_policy() {
  local expected_instances="${MARGINLAB_EXPECTED_INSTANCES:-50}"
  local minimum_valid_instances
  local non_test_failure_policy="${MARGINLAB_NON_TEST_FAILURE_POLICY:-exclude}"

  if ! [[ "${expected_instances}" =~ ^[1-9][0-9]*$ ]]; then
    echo "MARGINLAB_EXPECTED_INSTANCES must be a positive integer" >&2
    return 2
  fi

  if [[ -n "${MARGINLAB_MINIMUM_VALID_INSTANCES:-}" ]]; then
    minimum_valid_instances="${MARGINLAB_MINIMUM_VALID_INSTANCES}"
  elif ((expected_instances > 1)); then
    minimum_valid_instances="$((expected_instances - 1))"
  else
    minimum_valid_instances="${expected_instances}"
  fi

  if ! [[ "${minimum_valid_instances}" =~ ^[1-9][0-9]*$ ]] \
    || ((minimum_valid_instances > expected_instances)); then
    echo "MARGINLAB_MINIMUM_VALID_INSTANCES must be positive and no greater than MARGINLAB_EXPECTED_INSTANCES" >&2
    return 2
  fi

  case "${non_test_failure_policy}" in
    reject|count-as-failure|exclude)
      ;;
    *)
      echo "MARGINLAB_NON_TEST_FAILURE_POLICY must be reject, count-as-failure, or exclude" >&2
      return 2
      ;;
  esac

  EXPECTED_INSTANCES="${expected_instances}"
  MINIMUM_VALID_INSTANCES="${minimum_valid_instances}"
  NON_TEST_FAILURE_POLICY="${non_test_failure_policy}"
}
