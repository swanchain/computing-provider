#!/bin/bash
# Node.js and npm installation script for Ubuntu/Debian

set -e

echo "Installing Node.js and npm..."

# Update package index
sudo apt-get update

# Install prerequisites
sudo apt-get install -y \
    curl \
    ca-certificates \
    gnupg

# Add NodeSource repository for Node.js LTS
# This will install Node.js 20.x (LTS)
NODE_MAJOR=20
curl -fsSL https://deb.nodesource.com/setup_${NODE_MAJOR}.x | sudo -E bash -

# Install Node.js and npm
sudo apt-get install -y nodejs

echo "Node.js and npm installed successfully!"
echo ""
echo "Verify installation:"
echo "  node --version"
echo "  npm --version"


