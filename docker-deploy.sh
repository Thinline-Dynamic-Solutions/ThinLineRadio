#!/usr/bin/env bash
# Thin wrapper so clone + `./docker-deploy.sh` from repo root still works (see docker/docker-deploy.sh).
set -e
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec "$ROOT/docker/docker-deploy.sh" "$@"
