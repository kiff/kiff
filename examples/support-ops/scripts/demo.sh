#!/usr/bin/env bash
# Run the support-ops 5-ticket breadth demo.
set -euo pipefail

: "${AGNO_MODEL_PROVIDER:=offline}"
: "${SERVER_BIN:?SERVER_BIN must be set}"
: "${PORT_FILE:?PORT_FILE must be set}"
: "${SERVER_LOG:?SERVER_LOG must be set}"
: "${EXAMPLE_DIR:?EXAMPLE_DIR must be set}"
: "${PYTHON:=python3}"

cleanup() {
  if [[ -n "${SERVER_PID:-}" ]] && kill -0 "${SERVER_PID}" 2>/dev/null; then
    kill "${SERVER_PID}" 2>/dev/null || true
    wait "${SERVER_PID}" 2>/dev/null || true
  fi
  rm -f "${PORT_FILE}"
}
trap cleanup EXIT INT TERM

rm -f "${PORT_FILE}"

echo
echo "========================================================================"
echo "  support-ops demo: provider=${AGNO_MODEL_PROVIDER}"
echo "========================================================================"

"${SERVER_BIN}" -addr :0 -port-file "${PORT_FILE}" >"${SERVER_LOG}" 2>&1 &
SERVER_PID=$!

for i in $(seq 1 50); do
  if [[ -s "${PORT_FILE}" ]]; then break; fi
  sleep 0.2
done
if [[ ! -s "${PORT_FILE}" ]]; then
  echo "demo: server did not start; log:"; cat "${SERVER_LOG}" || true; exit 1
fi
PORT=$(tr -d '[:space:]' < "${PORT_FILE}")
export KIFF_BASE_URL="http://localhost:${PORT}"
export AGNO_MODEL_PROVIDER

echo "  server : ${KIFF_BASE_URL}"
echo "  log    : ${SERVER_LOG}"
echo

( cd "${EXAMPLE_DIR}/agent" && "${PYTHON}" -m run_with_kiff )

echo
echo "demo complete. server log: ${SERVER_LOG}"
