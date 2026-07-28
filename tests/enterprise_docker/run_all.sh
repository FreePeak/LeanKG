#!/bin/bash
# Run every test in this directory. Used by CI / local pre-merge check.
#
# Usage:  bash tests/enterprise_docker/run_all.sh

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

PASS=0
FAIL=0

for t in "${SCRIPT_DIR}"/test_*.sh; do
    echo
    echo "==== $(basename "$t") ===="
    if bash "$t"; then
        PASS=$((PASS + 1))
    else
        FAIL=$((FAIL + 1))
    fi
done

echo
echo "================================="
echo "Test files: PASS=$PASS  FAIL=$FAIL"
echo "================================="

if [ "$FAIL" -gt 0 ]; then
    exit 1
fi