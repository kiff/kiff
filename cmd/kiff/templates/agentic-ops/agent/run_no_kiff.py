"""Run the agent against a mock in-memory DB. No KIFF.

This is the baseline: the model picks a refund, the tool mutates state
directly, no governance layer between them. Run this to see what most
agentic backends ship today.
"""

from __future__ import annotations

import json
import sys

from agent import (
    AgentConfig,
    Ticket,
    ToolCall,
    make_provider,
    provider_banner,
    seed_tickets,
)


def fresh_db():
    return {
        "order-1": {"id": "order-1", "state": "PAID", "refunds": []},
        "order-2": {"id": "order-2", "state": "PAID", "refunds": []},
    }


def refund_unguarded(db, call: ToolCall) -> str:
    order = db.get(call.arguments.get("order_id"))
    if order is None:
        return f"order {call.arguments.get('order_id')} not found"
    order["refunds"].append({
        "amount_cents": call.arguments.get("amount_cents"),
        "reason": call.arguments.get("reason"),
    })
    order["state"] = "REFUNDED"
    return f"refunded {call.arguments.get('amount_cents')} cents on {order['id']}"


def main() -> int:
    provider = make_provider(AgentConfig())
    print(f"[run-no-kiff] {provider_banner(provider)}")
    print()
    print("=" * 72)
    print("  RUN A — agent without KIFF")
    print("=" * 72)
    db = fresh_db()
    for ticket in seed_tickets():
        call = provider.decide(ticket)
        print()
        print(f"[ticket {ticket.id}] order={ticket.order_id} amount={ticket.expected_amount_cents} cents")
        print(f"    proposal source: {call.source}")
        if call.reasoning:
            print(f"    agent reasoning: {call.reasoning.splitlines()[0]}")
        outcome = refund_unguarded(db, call)
        print(f"    [no-kiff] tool result: {outcome}")
    print()
    print("[run-no-kiff] FINAL DB STATE")
    for order in db.values():
        marker = "⚠" if order["refunds"] and order["refunds"][0].get("amount_cents", 0) > 10000 else " "
        print(f"  {marker} {order['id']:<10} state={order['state']:<10} refunds={json.dumps(order['refunds'])}")
    print()
    print("[run-no-kiff] note: nothing stopped the large refund. KIFF is the missing layer.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
