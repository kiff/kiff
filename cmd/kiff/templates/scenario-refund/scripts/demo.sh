#!/usr/bin/env bash
# Refund scenario demo — pure curl, no agent framework, no Python.
#
# It shows the enablement story and the boundary that makes it shippable:
#   1. Unguarded path double-refunds an order (the danger).
#   2. Guarded path issues the refund once a human approves,
#      then refuses the repeat because the state moved on.
#   3. Replay proves the final state from events alone.
set -euo pipefail

: "${SERVER_BIN:?SERVER_BIN must be set}"
: "${PORT_FILE:?PORT_FILE must be set}"
: "${SERVER_LOG:?SERVER_LOG must be set}"

cleanup() {
  if [[ -n "${SERVER_PID:-}" ]] && kill -0 "${SERVER_PID}" 2>/dev/null; then
    kill "${SERVER_PID}" 2>/dev/null || true
    wait "${SERVER_PID}" 2>/dev/null || true
  fi
  rm -f "${PORT_FILE}"
}
trap cleanup EXIT INT TERM
rm -f "${PORT_FILE}"

"${SERVER_BIN}" -addr :0 -port-file "${PORT_FILE}" >"${SERVER_LOG}" 2>&1 &
SERVER_PID=$!
for _ in $(seq 1 50); do [[ -s "${PORT_FILE}" ]] && break; sleep 0.2; done
if [[ ! -s "${PORT_FILE}" ]]; then echo "server did not start:"; cat "${SERVER_LOG}"; exit 1; fi
PORT=$(tr -d '[:space:]' < "${PORT_FILE}")
BASE="http://localhost:${PORT}"

post() { curl -s -X POST "${BASE}$1" -H 'content-type: application/json' -d "$2"; echo; }
get()  { curl -s "${BASE}$1"; echo; }

echo "== seeded orders (both PAID) =="
get /demo/orders

echo
echo "== 1) UNGUARDED refund of order-1, twice — nothing stops the repeat =="
post /demo/unguarded/refund '{"order_id":"order-1","amount_cents":4200,"reason":"first"}'
post /demo/unguarded/refund '{"order_id":"order-1","amount_cents":4200,"reason":"again (oops)"}'
echo "ledger now has TWO refunds for order-1 — the money went out twice:"
get /demo/ledger

echo
echo "== 2) GUARDED refund of order-2 THROUGH KIFF =="
echo "-- high-risk: KIFF holds it for approval --"
post /demo/agent/refund '{"order_id":"order-2","amount_cents":99900,"reason":"customer eligible","approval_id":"appr-demo-2"}'
echo "-- an operator grants the approval --"
post /approvals/appr-demo-2/grant '{"actor":{"id":"ops-operator","type":"human","roles":["ops_operator"]},"reason":"verified"}'
echo "-- same call now executes; the side effect runs --"
post /demo/agent/refund '{"order_id":"order-2","amount_cents":99900,"reason":"customer eligible","approval_id":"appr-demo-2"}'
echo "-- repeat is REFUSED: the order already moved to REFUNDED --"
post /demo/agent/refund '{"order_id":"order-2","amount_cents":99900,"reason":"double refund attempt"}'

echo
echo "== 3) replay proves order-2's state from events alone =="
get '/demo/rebuild?entity=order-2'

echo
echo "== final ledger: order-2 refunded exactly once via the guarded path =="
get /demo/ledger

echo
echo "demo complete."
