#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
FRONTEND_DIR="${PROJECT_ROOT}/frontend"
DEFAULT_CONFIG_PATH="${PROJECT_ROOT}/configs/config.local.yaml"
CONFIG_PATH="${LITEDNS_CONFIG_PATH:-${DEFAULT_CONFIG_PATH}}"

# Dev-only fallback key, generated on 2026-03-11.
DEFAULT_MASTER_KEY="1m9viZptNE3nGhPfQEYL9FC2fqnot4pWiKmBgD/tO+w="
MASTER_KEY="${LITEDNS_MASTER_KEY:-}"

usage() {
  cat <<EOF
Usage: $0 [-k|--master-key <base64_32byte_key>] [-- <go_run_args...>]

Options:
  -k, --master-key   Set LITEDNS_MASTER_KEY (Base64-encoded 32-byte key)
  -h, --help         Show help

If no key is provided, the script falls back to:
1) existing LITEDNS_MASTER_KEY environment variable
2) built-in dev default key
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    -k|--master-key)
      if [[ $# -lt 2 ]]; then
        echo "error: missing value for $1" >&2
        usage
        exit 1
      fi
      MASTER_KEY="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    --)
      shift
      break
      ;;
    *)
      echo "error: unknown argument: $1" >&2
      usage
      exit 1
      ;;
  esac
done

if [[ -z "${MASTER_KEY}" ]]; then
  MASTER_KEY="${DEFAULT_MASTER_KEY}"
fi

decoded_len="$(printf '%s' "${MASTER_KEY}" | base64 --decode 2>/dev/null | wc -c | tr -d ' ')"
if [[ "${decoded_len}" != "32" ]]; then
  echo "error: LITEDNS_MASTER_KEY must be valid Base64 and decode to 32 bytes" >&2
  exit 1
fi

export LITEDNS_MASTER_KEY="${MASTER_KEY}"
export LITEDNS_CONFIG_PATH="${CONFIG_PATH}"

if [[ ! -f "${LITEDNS_CONFIG_PATH}" ]]; then
  example_config="${PROJECT_ROOT}/configs/config.example.yaml"
  if [[ -f "${example_config}" ]]; then
    cp "${example_config}" "${LITEDNS_CONFIG_PATH}"
    echo "created local config: ${LITEDNS_CONFIG_PATH}"
  else
    echo "error: config not found at ${LITEDNS_CONFIG_PATH}" >&2
    exit 1
  fi
fi

if ! command -v npm >/dev/null 2>&1; then
  echo "error: npm command not found; please install Node.js/npm first" >&2
  exit 1
fi

if [[ ! -f "${FRONTEND_DIR}/package.json" ]]; then
  echo "error: frontend package.json not found at ${FRONTEND_DIR}" >&2
  exit 1
fi

backend_pid=0
frontend_pid=0

cleanup() {
  local exit_code=$?
  trap - EXIT INT TERM

  if [[ "${backend_pid}" -gt 0 ]] && kill -0 "${backend_pid}" 2>/dev/null; then
    kill "${backend_pid}" 2>/dev/null || true
  fi
  if [[ "${frontend_pid}" -gt 0 ]] && kill -0 "${frontend_pid}" 2>/dev/null; then
    kill "${frontend_pid}" 2>/dev/null || true
  fi

  wait "${backend_pid}" 2>/dev/null || true
  wait "${frontend_pid}" 2>/dev/null || true
  exit "${exit_code}"
}

trap cleanup EXIT INT TERM

(
  cd "${PROJECT_ROOT}"
  go run ./cmd/litedns "$@"
) &
backend_pid=$!

(
  cd "${FRONTEND_DIR}"
  npm run dev
) &
frontend_pid=$!

echo "LiteDNS backend started (pid=${backend_pid})"
echo "LiteDNS frontend started (pid=${frontend_pid})"

wait -n "${backend_pid}" "${frontend_pid}"
