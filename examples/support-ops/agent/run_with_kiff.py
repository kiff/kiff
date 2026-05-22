"""Run the support-ops agent through KIFF on the 5-ticket batch.

The script picks one tool per ticket, posts to the demo HTTP server, and
prints a compact table at the end with `ticket | tool chosen | outcome
| reason`. Pending approvals are auto-resolved (first one granted, the
rest denied) so the table also shows the post-review outcome for every
ticket.

Modes:

    python -m run_with_kiff             # decide + auto-resolve approvals + audit
    python -m run_with_kiff --grant <id>
    python -m run_with_kiff --deny  <id>
"""

from __future__ import annotations

import argparse
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
    decide_for_ticket,
    load_tickets,
    make_provider,
    provider_banner,
)


# ---------------------------------------------------------------------------
# HTTP client
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


@dataclass
class KiffOutcome:
    outcome: str
    tool: str
    action: str
    ticket_id: str
    approval_id: str
    state: str
    reason: str
    error: str = ""
    raw: Dict[str, Any] = field(default_factory=dict)


def call_kiff(call: ToolCall, ticket: Ticket, approval_id: Optional[str] = None) -> KiffOutcome:
    body: Dict[str, Any] = {
        "ticket_id": ticket.id,
        "tool": call.tool,
        "parameters": call.parameters,
        "reasoning": call.reasoning,
        "confidence": call.confidence,
    }
    if approval_id:
        body["approval_id"] = approval_id
    _, payload = http_json("POST", "/demo/agent/decide", body=body)
    return KiffOutcome(
        outcome=str(payload.get("outcome", "blocked")),
        tool=str(payload.get("tool", call.tool)),
        action=str(payload.get("action", "")),
        ticket_id=str(payload.get("ticket_id", ticket.id)),
        approval_id=str(payload.get("approval_id", "")),
        state=str(payload.get("state", "")),
        reason=str(payload.get("reason", "") or ""),
        error=str(payload.get("error", "") or ""),
        raw=payload,
    )


def grant_approval(approval_id: str, reason: str = "approved") -> Dict[str, Any]:
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


def deny_approval(approval_id: str, reason: str = "denied") -> Dict[str, Any]:
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


def fetch_tickets() -> List[Dict[str, Any]]:
    _, payload = http_json("GET", "/demo/tickets", expect_status=(200,))
    return payload.get("tickets", []) or []


def fetch_timeline(ticket_id: str) -> List[Dict[str, Any]]:
    _, payload = http_json("GET", f"/entities/{ticket_id}/timeline", expect_status=(200,))
    return payload.get("timeline", []) or []


def fetch_rebuild(ticket_id: str) -> Dict[str, Any]:
    _, payload = http_json("GET", f"/demo/rebuild?entity={ticket_id}", expect_status=(200,))
    return payload


# ---------------------------------------------------------------------------
# Display
# ---------------------------------------------------------------------------


def banner(title: str) -> None:
    bar = "=" * 72
    print(bar)
    print(f"  {title}")
    print(bar)


def print_ticket(ticket: Ticket, call: ToolCall, outcome: KiffOutcome) -> None:
    print()
    print(f"[ticket {ticket.id}] tool={call.tool}")
    print(f"    proposal source : {call.source}")
    if call.reasoning:
        first = call.reasoning.splitlines()[0]
        print(f"    agent reasoning : {first}")
    print(f"    agent confidence: {call.confidence:.2f}")
    print(f"    KIFF outcome    : {outcome.outcome:<26} action={outcome.action}")
    if outcome.approval_id:
        print(f"    KIFF approval id: {outcome.approval_id}")
    print(f"    KIFF state      : {outcome.state}")
    if outcome.reason:
        print(f"    KIFF reason     : {outcome.reason}")


# ---------------------------------------------------------------------------
# Run
# ---------------------------------------------------------------------------


def wait_for_server(timeout_seconds: float = 10.0) -> None:
    deadline = time.time() + timeout_seconds
    while time.time() < deadline:
        try:
            http_json("GET", "/demo/tickets", expect_status=(200,))
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
        outcome = call_kiff(call, ticket)
        print_ticket(ticket, call, outcome)
        results.append((ticket, call, outcome))
    return results


def auto_resolve(results: List[Tuple[Ticket, ToolCall, KiffOutcome]]) -> List[Tuple[Ticket, ToolCall, KiffOutcome]]:
    pending = [row for row in results if row[2].outcome == "approval_required"]
    if not pending:
        return []
    print()
    banner("Operator review")
    for _, _, outcome in pending:
        print(f"  - pending: {outcome.approval_id} ({outcome.action} on {outcome.ticket_id})")

    retried: List[Tuple[Ticket, ToolCall, KiffOutcome]] = []
    # Grant the first pending approval; deny the rest. This way the
    # 5-ticket batch always shows the full story: at least one grant
    # turning into an executed action.
    for idx, (ticket, call, outcome) in enumerate(pending):
        if idx == 0:
            grant_approval(outcome.approval_id, reason="reviewed; reasonable")
            print()
            print(f"[run-with-kiff] granted {outcome.approval_id} → retrying {ticket.id}")
        else:
            deny_approval(outcome.approval_id, reason="denied; insufficient evidence")
            print()
            print(f"[run-with-kiff] denied  {outcome.approval_id} → retrying {ticket.id}")
        retry_outcome = call_kiff(call, ticket, approval_id=outcome.approval_id)
        print_ticket(ticket, call, retry_outcome)
        retried.append((ticket, call, retry_outcome))
    return retried


def print_summary(results: List[Tuple[Ticket, ToolCall, KiffOutcome]],
                  retried: List[Tuple[Ticket, ToolCall, KiffOutcome]]) -> None:
    print()
    banner("Summary (one row per ticket, first outcome)")
    print(f"{'ticket':<10} {'tool':<20} {'first outcome':<26} {'reason / final state'}")
    print("-" * 90)
    retried_by_ticket = {row[0].id: row[2] for row in retried}
    for ticket, call, outcome in results:
        retry = retried_by_ticket.get(ticket.id)
        final_state = (retry.state if retry else outcome.state) or "?"
        if outcome.outcome == "approval_required":
            verdict = "granted→executed" if retry and retry.outcome == "executed" else "denied→still blocked"
            note = f"{verdict} ({final_state})"
        elif outcome.outcome == "blocked_consent_missing":
            note = f"{outcome.reason} ({final_state})"
        else:
            note = f"{outcome.reason or 'no approval needed'} ({final_state})"
        print(f"{ticket.id:<10} {call.tool:<20} {outcome.outcome:<26} {note}")


def print_audit_section() -> None:
    print()
    banner("Audit timeline + rebuild check")
    for ticket in fetch_tickets():
        ticket_id = ticket["id"]
        timeline = fetch_timeline(ticket_id)
        print(f"  timeline({ticket_id}):")
        for record in timeline:
            kind = record.get("kind", "?")
            actor = record.get("actor_id", "")
            data = record.get("data") or {}
            target = data.get("action") or data.get("event_type") or ""
            suffix = f" [{target}]" if target else ""
            print(f"    {kind:<22} actor={actor:<14} {record.get('message', '')}{suffix}")
        info = fetch_rebuild(ticket_id)
        marker = "✓" if info.get("matches") else "✗"
        print(
            f"  rebuild({ticket_id}): materialized={info.get('materialized')!r} "
            f"replayed={info.get('replayed')!r} events={info.get('events_replayed')} {marker}"
        )
        print()


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------


def run_auto() -> int:
    wait_for_server()
    banner("RUN — same agent, five tickets, through KIFF")
    results = run_tickets()
    retried = auto_resolve(results)
    print_audit_section()
    print_summary(results, retried)
    return 0


def main(argv: Optional[List[str]] = None) -> int:
    parser = argparse.ArgumentParser(description="Run support-ops agent through KIFF.")
    parser.add_argument("--grant", metavar="APPROVAL_ID", help="grant a pending approval")
    parser.add_argument("--deny", metavar="APPROVAL_ID", help="deny a pending approval")
    args = parser.parse_args(argv)

    if args.grant and args.deny:
        print("[run-with-kiff] choose --grant OR --deny, not both")
        return 2
    if args.grant:
        grant_approval(args.grant, reason="manual grant")
        print(f"[run-with-kiff] granted {args.grant}")
        return 0
    if args.deny:
        deny_approval(args.deny, reason="manual deny")
        print(f"[run-with-kiff] denied {args.deny}")
        return 0
    return run_auto()


if __name__ == "__main__":
    sys.exit(main())
