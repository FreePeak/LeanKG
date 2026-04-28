#!/bin/bash
set -e

echo "Installing LeanKG..."
curl -fsSL https://raw.githubusercontent.com/FreePeak/LeanKG/main/scripts/install.sh | bash

echo "Initializing LeanKG..."
leankg init

echo ""
echo "=========================================="
echo "LeanKG is ready!"
echo ""
echo "To index and explore your code:"
echo "  1. leankg index ./src"
echo "  2. leankg web"
echo ""
echo "The Web UI will open automatically on port 8080"
echo "=========================================="
