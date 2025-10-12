#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_DIR="${ROOT_DIR}/app"
LOG_DIR="${ROOT_DIR}/logs"
LOG_FILE="${APP_LOG_FILE:-${LOG_DIR}/minishop.log}"

mkdir -p "${LOG_DIR}"

export APP_LOG_FILE="${LOG_FILE}"

cd "${APP_DIR}"

exec go run . 2>&1 | tee -a "${LOG_FILE}"
