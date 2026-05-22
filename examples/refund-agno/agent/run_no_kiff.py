"""Run the support agent against a mock in-memory DB. No KIFF.

This is the baseline a CTO recognizes from their own production: the
model picks a tool, the tool mutates the database, no governance layer
exists between the two. The point of running this is to make it visible
how easy it is for the agent to refund the wrong amount on the wrong
order with zero friction.

Usage::

    python -m run_no_kiff

The script prints the agent's decisions and the resulting DB state. It
exits non-zero if any decision crashed (the demo expects all decisions
to "succeed" in this unguarded run; that's the problem).
"""

from __future__ import annotations

import json
import sys
from dataclasses import dataclass, field
from typing import Dict

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
# Mock DB
#
# This stand-in is intentionally a few lines. Every CTO has the same shape
# in production, just with more columns. The point is that the tool call
# rewrites state directly — no policy, no approval, no audit.
# ---------------------------------------------------------------------------


@dataclass
class MockOrder:
    id: str
    state: str
    paid_cents: int
    refund_log: list = field(default_factory=list)


def fresh_db() -> Dict[str, MockOrder]:
    return {
        "order-1": MockOrder(id="order-1", state="PAID", paid_cents=4200),
        "order-2": MockOrder(id="order-2", state="PAID", paid_cents=99900),
        "order-3": MockOrder(id="order-3", state="PAID", paid_cents=25000),
    }


def refund_order_unguarded(db: Dict[str, MockOrder], call: ToolCall) -> str:
    """The unguarded tool implementation. This is what damages production."""
    order = db.get(call.order_id)
    if order is None:
        return f"order {call.order_id} not found"
    order.refund_log.append(
        {"amount_cents": call.amount_cents, "reason": call.reason}
    )
    order.state = "REFUNDED"
    return f"refunded {call.amount_cents} cents on {call.order_id}"


# ---------------------------------------------------------------------------
# Run loop
# ---------------------------------------------------------------------------


def run() -> int:
    config = AgentConfig()
    provider = make_provider(config)
    print(f"[run-no-kiff] {provider_banner(provider)}")
    print()
    print("=" * 72)
    print("  RUN A — agent without KIFF (the baseline most agents ship today)")
    print("=" * 72)

    db = fresh_db()
    tickets = load_tickets()

    for ticket in tickets:
        call = decide_for_ticket(provider, ticket)
        print()
        print(f"[ticket {ticket.id}] order={ticket.order_id} amount={call.amount_cents} cents")
        print(f"    proposal source: {call.source}")
        if call.reasoning:
            first_line = call.reasoning.splitlines()[0]
            print(f"    agent reasoning: {first_line}")
        print(f"    agent confidence: {call.confidence:.2f}")
        outcome = refund_order_unguarded(db, call)
        print(f"    [no-kiff] tool result: {outcome}")
        order = db[call.order_id]
        print(f"    [no-kiff] DB state  : {order.id} → {order.state}, refund_log={json.dumps(order.refund_log)}")

    print()
    print("[run-no-kiff] FINAL DB STATE")
    for order in db.values():
        marker = "⚠" if order.state == "REFUNDED" and any(
            r["amount_cents"] > 10000 for r in order.refund_log
        ) else " "
        print(
            f"  {marker} {order.id:<10} state={order.state:<10} "
            f"refunds={json.dumps(order.refund_log)}"
        )
    print()
    print("[run-no-kiff] note: nothing stopped the $999 refund. "
          "this is exactly the situation KIFF is designed to prevent.")
    print()
    return 0


if __name__ == "__main__":
    sys.exit(run())
