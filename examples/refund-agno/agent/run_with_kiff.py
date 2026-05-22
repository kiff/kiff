"""Run the same support agent through KIFF.

Same agent module (``agent.py``), same tickets, same model. Only the tool
implementation changes: instead of mutating a mock DB, the tool POSTs to
the KIFF demo HTTP server, which routes the call through the runtime.

The runner has three modes::

    python -m run_with_kiff                  # run all tickets, stop after first block
    python -m run_with_kiff --grant <id>     # grant a pending approval, then retry
    python -m run_with_kiff --deny  <id>     # deny a pending approval, then retry
    python -m run_with_kiff --auto           # full demo: tickets, then auto-grant
                                             # one approval, auto-deny another,
                                             # then print timeline + rebuild

The ``--auto`` mode is what ``make demo`` invokes. It produces the final
buyer-facing artifact: a side-by-side run that ends with the audit
timeline and a rebuild check for every order.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import time
from dataclasses import dataclass
from typing import Any, Dict, List, Optional, Tuple
from urllib import error as urllib_error
from urllib import request as urllib_request

from agent import (
    AgentConfig,
    Ticket,
    ToolCall,
    decide_for_ticket,
    load_tickets,
    make_provider,
    provider_banner,
)


# ---------------------------------------------------------------------------
# Tiny HTTP client
#
# stdlib-only on purpose. The agent's "tool" is just a POST to the KIFF
# server. The agent never sees the runtime; it only sees an outcome
# string. This is the contract any LLM-bridge layer should preserve.
# ---------------------------------------------------------------------------


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
        raise RuntimeError(
            f"unexpected status {status} from {method} {path}: {payload}"
        )
    return status, decoded


# ---------------------------------------------------------------------------
# Tool implementation that calls KIFF
# ---------------------------------------------------------------------------


@dataclass
class KiffOutcome:
    outcome: str
    action: str
    order_id: str
    approval_id: str
    state: str
    error: str = ""
    raw: Dict[str, Any] = None  # type: ignore[assignment]

    def is_blocked(self) -> bool:
        return self.outcome != "executed"


def call_refund_through_kiff(call: ToolCall, approval_id: Optional[str] = None) -> KiffOutcome:
    body = {
        "order_id": call.order_id,
        "amount_cents": call.amount_cents,
        "reason": call.reason,
        "reasoning": call.reasoning,
        "confidence": call.confidence,
    }
    if approval_id:
        body["approval_id"] = approval_id
    status, payload = http_json("POST", "/demo/agent/refund", body=body)
    return KiffOutcome(
        outcome=str(payload.get("outcome", "blocked")),
        action=str(payload.get("action", "")),
        order_id=str(payload.get("order_id", call.order_id)),
        approval_id=str(payload.get("approval_id", "")),
        state=str(payload.get("state", "")),
        error=str(payload.get("error", "") or ""),
        raw=payload,
    )


def grant_approval(approval_id: str, reason: str = "approved by ops on call") -> Dict[str, Any]:
    _, payload = http_json(
        "POST",
        f"/approvals/{approval_id}/grant",
        body={
            "actor": {"id": "ops-human", "type": "human"},
            "reason": reason,
        },
        expect_status=(200,),
    )
    return payload


def deny_approval(approval_id: str, reason: str = "denied: evidence missing") -> Dict[str, Any]:
    _, payload = http_json(
        "POST",
        f"/approvals/{approval_id}/deny",
        body={
            "actor": {"id": "ops-human", "type": "human"},
            "reason": reason,
        },
        expect_status=(200,),
    )
    return payload


def fetch_timeline(order_id: str) -> List[Dict[str, Any]]:
    _, payload = http_json("GET", f"/entities/{order_id}/timeline", expect_status=(200,))
    return payload.get("timeline", []) or []


def fetch_orders() -> List[Dict[str, str]]:
    _, payload = http_json("GET", "/demo/orders", expect_status=(200,))
    return payload.get("orders", []) or []


# ---------------------------------------------------------------------------
# Display helpers
# ---------------------------------------------------------------------------


def banner(title: str) -> None:
    bar = "=" * 72
    print(bar)
    print(f"  {title}")
    print(bar)


def print_ticket(ticket: Ticket, call: ToolCall, outcome: KiffOutcome) -> None:
    print()
    print(f"[ticket {ticket.id}] order={ticket.order_id} amount={call.amount_cents} cents")
    print(f"    proposal source : {call.source}")
    if call.reasoning:
        first_line = call.reasoning.splitlines()[0]
        print(f"    agent reasoning : {first_line}")
    print(f"    agent confidence: {call.confidence:.2f}")
    print(f"    KIFF outcome    : {outcome.outcome:<18} action={outcome.action}")
    if outcome.approval_id:
        print(f"    KIFF approval id: {outcome.approval_id}")
    print(f"    KIFF state      : {outcome.state}")
    if outcome.error:
        print(f"    KIFF error      : {outcome.error}")


def print_timeline(order_id: str) -> None:
    timeline = fetch_timeline(order_id)
    print(f"  timeline({order_id}):")
    for record in timeline:
        kind = record.get("kind", "?")
        actor = record.get("actor_id", "")
        message = record.get("message", "")
        data = record.get("data") or {}
        action_name = data.get("action") or data.get("event_type") or ""
        suffix = f" [{action_name}]" if action_name else ""
        print(f"    {kind:<22} actor={actor:<14} {message}{suffix}")


def print_rebuild_check(order_id: str) -> None:
    """Compare materialized state with replayed state via the KIFF API."""
    try:
        _, payload = http_json("GET", f"/demo/rebuild?entity={order_id}", expect_status=(200,))
    except Exception as exc:
        print(f"  rebuild({order_id}): unavailable ({exc})")
        return
    materialized = payload.get("materialized", "")
    replayed = payload.get("replayed", "")
    matches = bool(payload.get("matches"))
    events = payload.get("events_replayed", 0)
    marker = "✓" if matches else "✗"
    print(
        f"  rebuild({order_id}): materialized={materialized!r} "
        f"replayed={replayed!r} events={events} {marker}"
    )


# ---------------------------------------------------------------------------
# Run modes
# ---------------------------------------------------------------------------


def wait_for_server(timeout_seconds: float = 10.0) -> None:
    deadline = time.time() + timeout_seconds
    while time.time() < deadline:
        try:
            http_json("GET", "/demo/orders", expect_status=(200,))
            return
        except Exception:
            time.sleep(0.2)
    raise RuntimeError(f"KIFF server at {base_url()} did not become ready")


def run_tickets() -> List[Tuple[Ticket, ToolCall, KiffOutcome]]:
    config = AgentConfig()
    provider = make_provider(config)
    print(f"[run-with-kiff] {provider_banner(provider)} url={base_url()}")
    tickets = load_tickets()
    results: List[Tuple[Ticket, ToolCall, KiffOutcome]] = []
    for ticket in tickets:
        call = decide_for_ticket(provider, ticket)
        outcome = call_refund_through_kiff(call)
        print_ticket(ticket, call, outcome)
        results.append((ticket, call, outcome))
    return results


def run_with_grant_or_deny(approval_id: str, *, grant: bool) -> int:
    if grant:
        review = grant_approval(approval_id)
        print(f"[run-with-kiff] granted {approval_id}: {review.get('approval', {}).get('status')}")
    else:
        review = deny_approval(approval_id)
        print(f"[run-with-kiff] denied {approval_id}: {review.get('approval', {}).get('status')}")
    # Caller is responsible for retrying the action. Print a hint.
    print("[run-with-kiff] re-run the matching ticket with the same approval id "
          "(or use --auto for the full demo).")
    return 0


def run_auto() -> int:
    """Full demo: tickets, then grant one pending approval, deny another,
    then dump timeline + rebuild for each order. This is what
    ``make demo`` invokes."""
    wait_for_server()

    banner("RUN B — same agent, same tickets, through KIFF")
    results = run_tickets()

    pending: List[Tuple[Ticket, ToolCall, KiffOutcome]] = [
        row for row in results if row[2].outcome == "approval_required"
    ]
    if not pending:
        print()
        print("[run-with-kiff] (no approvals were requested; nothing to grant or deny)")
        _print_summary(results)
        return 0

    print()
    banner("Operator review")
    print(f"[run-with-kiff] {len(pending)} approval(s) opened by KIFF:")
    for _, _, outcome in pending:
        print(f"  - {outcome.approval_id} on {outcome.order_id}")

    # First pending approval: grant. Retry the ticket through KIFF to show
    # execution after grant.
    grant_target = pending[0]
    grant_approval(grant_target[2].approval_id, reason="reviewed; refund is reasonable")
    print()
    print(f"[run-with-kiff] granted {grant_target[2].approval_id} → retrying ticket {grant_target[0].id}")
    retry_call = grant_target[1]
    retry_outcome = call_refund_through_kiff(retry_call, approval_id=grant_target[2].approval_id)
    print_ticket(grant_target[0], retry_call, retry_outcome)

    # Second pending approval (if any): deny. Retry to show KIFF still
    # blocks. If only one approval was opened, skip cleanly.
    if len(pending) >= 2:
        deny_target = pending[1]
        deny_approval(deny_target[2].approval_id, reason="evidence missing")
        print()
        print(f"[run-with-kiff] denied {deny_target[2].approval_id} → retrying ticket {deny_target[0].id}")
        retry_call_2 = deny_target[1]
        retry_outcome_2 = call_refund_through_kiff(
            retry_call_2, approval_id=deny_target[2].approval_id
        )
        print_ticket(deny_target[0], retry_call_2, retry_outcome_2)

    # Final story: timeline + rebuild for every order.
    print()
    banner("Audit timeline + rebuild check")
    for order in fetch_orders():
        order_id = order["id"]
        print_timeline(order_id)
        print_rebuild_check(order_id)
        print()

    _print_summary(results)
    return 0


def _print_summary(results: List[Tuple[Ticket, ToolCall, KiffOutcome]]) -> None:
    print()
    banner("Summary")
    print(f"{'ticket':<10} {'order':<10} {'amount':<10} {'first outcome':<20} {'final state':<12}")
    print("-" * 72)
    for ticket, call, outcome in results:
        # The state on `outcome` is the state right after the first call,
        # which is what the buyer wants to see (was anything mutated?).
        print(
            f"{ticket.id:<10} {ticket.order_id:<10} {call.amount_cents:<10} "
            f"{outcome.outcome:<20} {outcome.state:<12}"
        )


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------


def main(argv: Optional[List[str]] = None) -> int:
    parser = argparse.ArgumentParser(description="Run support agent through KIFF.")
    parser.add_argument("--grant", metavar="APPROVAL_ID", help="grant a pending approval")
    parser.add_argument("--deny", metavar="APPROVAL_ID", help="deny a pending approval")
    parser.add_argument("--auto", action="store_true", help="full demo (tickets + grant + deny + audit)")
    args = parser.parse_args(argv)

    if args.grant and args.deny:
        print("[run-with-kiff] choose --grant OR --deny, not both")
        return 2
    if args.grant:
        return run_with_grant_or_deny(args.grant, grant=True)
    if args.deny:
        return run_with_grant_or_deny(args.deny, grant=False)
    if args.auto:
        return run_auto()

    # Default: run tickets, leave approvals open. Useful for poking by
    # hand. Identical to --auto but stops before grant/deny + audit.
    wait_for_server()
    banner("RUN B — same agent, same tickets, through KIFF")
    run_tickets()
    return 0


if __name__ == "__main__":
    sys.exit(main())
