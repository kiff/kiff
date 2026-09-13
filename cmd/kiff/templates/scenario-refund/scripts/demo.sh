#!/usr/bin/env bash
# Refund scenario demo — pure curl, no agent framework, no Python.
#
# It shows the enablement story and the boundary that makes it shippable:
#   1. Unguarded path double-refunds an order.
#   2. Guarded path issues the refund once a human approves,
#      then refuses the repeat because the state moved on.
#   3. The aggregate: a refund correct in every way, refused because the
#      day's ceiling is spent. This is the one a per-call check cannot
#      make, and the reason the rest is worth wiring.
#   4. Replay proves the final state from events alone.
#
# Step 2's repeat refusal is table stakes — a unique constraint does it,
# and so does most tool code. It is here to show the boundary reading
# state, not as a reason to adopt one. Step 3 is the reason.
set -euo pipefail

: "${SERVER_BIN:?SERVER_BIN must be set}"
: "${PORT_FILE:?PORT_FILE must be set}"
: "${SERVER_LOG:?SERVER_LOG must be set}"
: "${STORE:=file}"
: "${PROJECT_DIR:=.}"

cleanup() {
  if [[ -n "${SERVER_PID:-}" ]] && kill -0 "${SERVER_PID}" 2>/dev/null; then
    kill "${SERVER_PID}" 2>/dev/null || true
    wait "${SERVER_PID}" 2>/dev/null || true
  fi
  rm -f "${PORT_FILE}"
}
trap cleanup EXIT INT TERM
rm -f "${PORT_FILE}"

server_args=(-addr :0 -port-file "${PORT_FILE}" -store "${STORE}")
if [[ "${STORE}" == "file" ]]; then
  server_args+=(-data-dir "${PROJECT_DIR}/data")
fi

"${SERVER_BIN}" "${server_args[@]}" >"${SERVER_LOG}" 2>&1 &
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
echo "== 2) GUARDED refund of order-2 through the app API (POST /api/tools/refund_order) =="
echo "-- high-risk: KIFF holds it for approval --"
post /api/tools/refund_order '{"entity_id":"order-2","approval_id":"appr-demo-2","parameters":{"amount_cents":99900,"reason":"customer eligible"}}'
echo "-- an operator grants the approval --"
post /api/approvals/appr-demo-2/grant '{}'
echo "-- same call now executes; the side effect runs --"
post /api/tools/refund_order '{"entity_id":"order-2","approval_id":"appr-demo-2","parameters":{"amount_cents":99900,"reason":"customer eligible"}}'
echo "-- repeat is REFUSED: the order already moved to REFUNDED --"
post /api/tools/refund_order '{"entity_id":"order-2","parameters":{"amount_cents":99900,"reason":"double refund attempt"}}'

echo
echo "== 3) THE AGGREGATE: every check passes, and the refund is still refused =="
echo "-- the agent auto-refunds order-1, no human involved --"
post /api/tools/auto_refund '{"entity_id":"order-1","parameters":{"amount_cents":4200,"reason":"damaged"}}'
echo "-- order-2 already took 99900 through the approved path --"
echo "-- so order-3 is REFUSED, and nothing is wrong with it --"
post /api/tools/auto_refund '{"entity_id":"order-3","parameters":{"amount_cents":8800,"reason":"late delivery"}}'
echo "   right state, right permission, valid parameters, no approval needed,"
echo "   and smaller than the two that just went through. The day is spent."
echo "   4200 + 99900 + 8800 > 110000, the daily ceiling in domain.DailyLimits()."
echo "   No per-call check can produce that refusal: none of them sees the other two."

echo
echo "== 4) the tool manifest, generated from the domain =="
get /api/tools/manifest.json

echo
echo "== 5) replay proves the state of order-2 from events alone =="
get '/demo/rebuild?entity=order-2'

echo
echo "== final ledger: order-2 refunded exactly once via the guarded path =="
get /demo/ledger

echo
echo "demo complete."
