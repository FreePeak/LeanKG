#!/bin/bash
set -e

echo "=== LeanKG Plugin Hooks Verification ==="

# Test 1: Claude Code plugin.json exists and is valid JSON
echo "Test 1: Checking .claude-plugin/plugin.json..."
if [ -f ".claude-plugin/plugin.json" ]; then
    python3 -c "import json; json.load(open('.claude-plugin/plugin.json'))" 2>/dev/null
    echo "  PASS: Valid JSON"
else
    echo "  FAIL: File not found"
    exit 1
fi

# Test 2: Claude Code marketplace.json exists and is valid JSON
echo "Test 2: Checking .claude-plugin/marketplace.json..."
if [ -f ".claude-plugin/marketplace.json" ]; then
    python3 -c "import json; json.load(open('.claude-plugin/marketplace.json'))" 2>/dev/null
    echo "  PASS: Valid JSON"
else
    echo "  FAIL: File not found"
    exit 1
fi

# Test 3: Claude Code hooks.json exists and is valid JSON
echo "Test 3: Checking .claude-plugin/hooks/hooks.json..."
if [ -f ".claude-plugin/hooks/hooks.json" ]; then
    python3 -c "import json; json.load(open('.claude-plugin/hooks/hooks.json'))" 2>/dev/null
    echo "  PASS: Valid JSON"
else
    echo "  FAIL: File not found"
    exit 1
fi

# Test 4: Cursor plugin.json exists and is valid JSON
echo "Test 4: Checking .cursor-plugin/plugin.json..."
if [ -f ".cursor-plugin/plugin.json" ]; then
    python3 -c "import json; json.load(open('.cursor-plugin/plugin.json'))" 2>/dev/null
    echo "  PASS: Valid JSON"
else
    echo "  FAIL: File not found"
    exit 1
fi

# Test 5: Cursor hooks.json exists and is valid JSON
echo "Test 5: Checking .cursor-plugin/hooks/hooks.json..."
if [ -f ".cursor-plugin/hooks/hooks.json" ]; then
    python3 -c "import json; json.load(open('.cursor-plugin/hooks/hooks.json'))" 2>/dev/null
    echo "  PASS: Valid JSON"
else
    echo "  FAIL: File not found"
    exit 1
fi

# Test 6: Hook scripts are executable
echo "Test 6: Checking hook scripts are executable..."
for f in .claude-plugin/hooks/run-hook.cmd .claude-plugin/hooks/session-start .cursor-plugin/hooks/session-start; do
    if [ -x "$f" ]; then
        echo "  PASS: $f is executable"
    else
        echo "  FAIL: $f is not executable"
        exit 1
    fi
done

# Test 7: Install script syntax is valid
echo "Test 7: Checking install.sh syntax..."
if bash -n scripts/install.sh 2>/dev/null; then
    echo "  PASS: Syntax OK"
else
    echo "  FAIL: Syntax error"
    exit 1
fi

# Test 8: leankg-bootstrap.md exists
echo "Test 8: Checking leankg-bootstrap.md exists..."
if [ -f "leankg-bootstrap.md" ]; then
    echo "  PASS: File exists"
else
    echo "  FAIL: File not found"
    exit 1
fi

echo ""
echo "=== All verification tests passed ==="