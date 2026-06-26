#!/usr/bin/env bash
set -euo pipefail
docker compose up --build -d
echo "Open: http://localhost:8080"
