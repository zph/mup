#!/bin/bash
# Build the SSH test node Docker image

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "Building mup-ssh-node Docker image..."
docker build -t mup-ssh-node:latest "$SCRIPT_DIR"

echo "✓ Image built successfully: mup-ssh-node:latest"
echo ""
echo "To test the image manually:"
echo "  docker run -d -p 2222:22 --name mup-test mup-ssh-node:latest"
echo "  ssh -p 2222 testuser@localhost  # password: testpass"
echo "  docker stop mup-test && docker rm mup-test"
