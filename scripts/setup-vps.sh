#!/bin/bash
# VPS Setup Script for ImmoBot
# Run as root on Ubuntu 24.04

set -e

echo "=== ImmoBot VPS Setup ==="

# Update system
echo "Updating system..."
apt update && apt upgrade -y

# Install Go
echo "Installing Go..."
wget -q https://go.dev/dl/go1.24.0.linux-amd64.tar.gz
rm -rf /usr/local/go && tar -C /usr/local -xzf go1.24.0.linux-amd64.tar.gz
rm go1.24.0.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin' >> /etc/profile
export PATH=$PATH:/usr/local/go/bin

# Install Chrome dependencies for chromedp
echo "Installing Chrome..."
apt install -y wget gnupg2
wget -q -O - https://dl.google.com/linux/linux_signing_key.pub | gpg --dearmor -o /usr/share/keyrings/google-chrome.gpg
echo "deb [arch=amd64 signed-by=/usr/share/keyrings/google-chrome.gpg] http://dl.google.com/linux/chrome/deb/ stable main" > /etc/apt/sources.list.d/google-chrome.list
apt update
apt install -y google-chrome-stable

# Create immobot user
echo "Creating immobot user..."
useradd -m -s /bin/bash immobot || true

# Create app directory
mkdir -p /opt/immobot
chown immobot:immobot /opt/immobot

echo "=== Setup complete ==="
echo "Now copy the project to /opt/immobot and run as immobot user"
