#!/bin/bash
# GitHub CLI installation script for Ubuntu/Debian

set -e

echo "Installing GitHub CLI (gh)..."

# Update package index
sudo apt-get update

# Install prerequisites
sudo apt-get install -y \
    curl \
    gnupg \
    ca-certificates

# Add GitHub CLI repository key
curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg | sudo dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg
sudo chmod go+r /usr/share/keyrings/githubcli-archive-keyring.gpg

# Add GitHub CLI repository
echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" | sudo tee /etc/apt/sources.list.d/github-cli.list > /dev/null

# Update package index
sudo apt-get update

# Install GitHub CLI
sudo apt-get install -y gh

echo "GitHub CLI installed successfully!"
echo ""
echo "Verify installation with: gh --version"
echo ""
echo "To authenticate, run: gh auth login"


