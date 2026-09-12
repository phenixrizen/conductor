#!/usr/bin/env bash
# Run as your normal user; sudo prompts stay in your terminal.
set -euo pipefail

export PATH="$HOME/.local/bin:$PATH"

if [[ "$EUID" -eq 0 ]]; then
  echo "Run this script as your normal user, without sudo." >&2
  exit 1
fi

command -v snap >/dev/null
command -v docker >/dev/null
command -v gh >/dev/null
snap list docker >/dev/null

if ! docker info >/dev/null 2>&1; then
  echo "Enabling Docker access for $(id -un). This restarts the Docker snap."
  sudo -v
  if ! getent group docker >/dev/null; then
    sudo addgroup --system docker
  fi
  sudo adduser "$(id -un)" docker
  sudo snap disable docker
  sudo snap enable docker

  # Use the new group immediately, without requiring a new login session.
  ready=false
  for ((attempt = 0; attempt < 30; attempt++)); do
    if sg docker -c 'docker info' >/dev/null 2>&1; then
      ready=true
      break
    fi
    sleep 1
  done
  if [[ "$ready" != true ]]; then
    echo "Docker did not become ready. Check: sudo snap logs docker.dockerd" >&2
    exit 1
  fi
  sg docker -c 'docker version && docker compose version'
else
  docker version
  docker compose version
fi

if ! gh auth status --hostname github.com >/dev/null 2>&1; then
  gh auth login --hostname github.com --web --git-protocol https
fi
gh auth setup-git --hostname github.com
gh auth status --hostname github.com

echo "Docker and GitHub access are ready."
echo "Log out and back in so all terminals and apps inherit Docker group access."
