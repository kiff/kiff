"""Run the same agent through KIFF.

Same agent module, same tickets, same model. Tool now POSTs to the KIFF
demo HTTP server. The small refund executes (no approval shape on this
contract); the large one returns ``approval_required``. The runner
auto-grants the pending approval, retries, prints the timeline and
rebuild check.
"""

from __future__ import annotations

import json
import os
import sys
import time
from dataclasses import dataclass, field
from typing import Any, Dict, List, Optional, Tuple
from urllib import error as urllib_error
from urllib import request as urllib_request

from agent import (
    AgentConfig,
    Ticket,
    ToolCall,
    make_provider,
    provider_banner,
    seed_tickets,
)


def base_url() -> str:
    return os.environ.get("KIFF_BASE_URL", "http://localhost:8080").rstrip("/")


def http_json(method: str, path: str, body: Optional[Dict[str, Any]] = None,
              expect_status: Optional[Tuple[int, ...]] = None) -> Tuple[int, Dict[str, Any]]:
    url = base_url() + path
    data = None
    headers = {"Accept": "application/json"}
    if body is not None:
        data = json.dumps(body).encode("utf-8")
        headers["Content-Type"] = "application/json"
    req = urllib_request.Request(url, data=data, method=method, headers=headers)
    try:
        with urllib_request.urlopen(req, timeout=10) as resp:
            status = resp.status
            payload = resp.read().decode("utf-8")
    except urllib_error.HTTPError as exc:
        status = exc.code
        payload = exc.read().decode("utf-8")
    decoded: Dict[str, Any] = {}
    if payload:
        try:
            decoded = json.loads(payload)
        except json.JSONDecodeError:
            decoded = {"raw": payload}
    if expect_status and status not in expect_status:
        raise RuntimeError(f"unexpected status {status} from {method} {path}: {payload}")
    return status, decoded


@dataclass
class KiffOutcome:
    outcome: str
    action: str
    order_id: str
    approval_id: str
    state: str
    error: str = ""
    raw: Dict[str, Any] = field(default_factory=dict)


def call_refund(call: ToolCall, approval_id: Optional[str] = None) -> KiffOutcome:
    body = {
        "order_id": call.arguments.get("order_id"),
        "amount_cents": call.arguments.get("amount_cents"),
        "reason": call.arguments.get("reason"),
        "reasoning": call.reasoning,
        "confidence": call.confidence,
    }
    if approval_id:
        body["approval_id"] = approval_id
    _, payload = http_json("POST", "/demo/agent/refund", body=body)
    return KiffOutcome(
        outcome=str(payload.get("outcome", "blocked")),
        action=str(payload.get("action", "")),
        order_id=str(payload.get("order_id", "")),
        approval_id=str(payload.get("approval_id", "")),
        state=str(payload.get("state", "")),
        error=str(payload.get("error", "") or ""),
        raw=payload,
    )


def grant(approval_id: str) -> Dict[str, Any]:
    _, payload = http_json(
        "POST", f"/approvals/{approval_id}/grant",
        body={"actor": {"id": "ops-human", "type": "human"}, "reason": "approved"},
        expect_status=(200,),
    )
    return payload


def fetch_orders() -> List[Dict[str, Any]]:
    _, payload = http_json("GET", "/demo/orders", expect_status=(200,))
    return payload.get("orders", []) or []


def fetch_timeline(order_id: str) -> List[Dict[str, Any]]:
    _, payload = http_json("GET", f"/entities/{order_id}/timeline", expect_status=(200,))
    return payload.get("timeline", []) or []


def fetch_rebuild(order_id: str) -> Dict[str, Any]:
    _, payload = http_json("GET", f"/demo/rebuild?entity={order_id}", expect_status=(200,))
    return payload


def banner(title: str) -> None:
    bar = "=" * 72
    print(bar)
    print(f"  {title}")
    print(bar)


def wait_for_server(timeout_seconds: float = 10.0) -> None:
    deadline = time.time() + timeout_seconds
    while time.time() < deadline:
        try:
            http_json("GET", "/demo/orders", expect_status=(200,))
            return
        except Exception:
            time.sleep(0.2)
    raise RuntimeError(f"KIFF server at {base_url()} did not become ready")


def main() -> int:
    wait_for_server()
    provider = make_provider(AgentConfig())
    banner("RUN B — same agent, same tickets, through KIFF")
    print(f"[run-with-kiff] {provider_banner(provider)} url={base_url()}")
    results: List[Tuple[Ticket, ToolCall, KiffOutcome]] = []
    for ticket in seed_tickets():
        call = provider.decide(ticket)
        outcome = call_refund(call)
        print()
        print(f"[ticket {ticket.id}] order={ticket.order_id} amount={ticket.expected_amount_cents} cents")
        print(f"    proposal source : {call.source}")
        if call.reasoning:
            print(f"    agent reasoning : {call.reasoning.splitlines()[0]}")
        print(f"    KIFF outcome    : {outcome.outcome:<18} action={outcome.action}")
        if outcome.approval_id:
            print(f"    KIFF approval id: {outcome.approval_id}")
        print(f"    KIFF state      : {outcome.state}")
        results.append((ticket, call, outcome))

    pending = [r for r in results if r[2].outcome == "approval_required"]
    if pending:
        print()
        banner("Operator review")
        for _, _, outcome in pending:
            print(f"  - pending: {outcome.approval_id} on {outcome.order_id}")
        first_ticket, first_call, first_outcome = pending[0]
        grant(first_outcome.approval_id)
        print()
        print(f"[run-with-kiff] granted {first_outcome.approval_id} → retrying ticket {first_ticket.id}")
        retry = call_refund(first_call, approval_id=first_outcome.approval_id)
        print(f"    KIFF outcome    : {retry.outcome:<18} action={retry.action}")
        print(f"    KIFF state      : {retry.state}")

    print()
    banner("Audit timeline + rebuild check")
    for order in fetch_orders():
        order_id = order["id"]
        for record in fetch_timeline(order_id):
            kind = record.get("kind", "?")
            actor = record.get("actor_id", "")
            data = record.get("data") or {}
            tag = data.get("action") or data.get("event_type") or ""
            suffix = f" [{tag}]" if tag else ""
            print(f"    {kind:<22} actor={actor:<14} {record.get('message', '')}{suffix}")
        info = fetch_rebuild(order_id)
        marker = "✓" if info.get("matches") else "✗"
        print(f"  rebuild({order_id}): materialized={info.get('materialized')!r} replayed={info.get('replayed')!r} events={info.get('events_replayed')} {marker}")
        print()
    return 0


if __name__ == "__main__":
    sys.exit(main())
