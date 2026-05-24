"""Run the ai-cafe-ops agent through KIFF on the 5-shift batch.

The script picks one tool per shift, posts to the demo HTTP server,
and prints a compact table at the end with `shift | tool chosen |
outcome | reason`. The single pending approval is auto-granted so the
table also shows the post-review outcome.

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
    Shift,
    ToolCall,
    decide_for_shift,
    load_shifts,
    make_provider,
    provider_banner,
)
from kiff_cloud_client import CloudConfig, KiffCloudClient


# Cloud client. When KIFF_CLOUD_URL + KIFF_CLOUD_API_KEY are set,
# every server-bound call is routed through this client instead of
# the local demo server's /demo/* surface. None means local mode.
_cloud: Optional[KiffCloudClient] = None


def _ensure_cloud() -> Optional[KiffCloudClient]:
    """Read env once and cache the client. Idempotent."""
    global _cloud
    if _cloud is not None:
        return _cloud
    config = CloudConfig.from_env()
    if config is None:
        return None
    _cloud = KiffCloudClient(config)
    return _cloud


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
    shift_id: str
    approval_id: str
    state: str
    reason: str
    error: str = ""
    raw: Dict[str, Any] = field(default_factory=dict)


def call_kiff(call: ToolCall, shift: Shift, approval_id: Optional[str] = None) -> KiffOutcome:
    cloud = _ensure_cloud()
    if cloud is not None:
        outcome = cloud.decide(
            tool=call.tool,
            shift_id=shift.id,
            parameters=call.parameters,
            approval_id=approval_id or "",
        )
        return KiffOutcome(
            outcome=outcome.outcome, tool=outcome.tool, action=outcome.action,
            shift_id=outcome.shift_id, approval_id=outcome.approval_id,
            state=outcome.state, reason=outcome.reason, error=outcome.error,
            raw=outcome.raw or {},
        )
    body: Dict[str, Any] = {
        "shift_id": shift.id,
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
        shift_id=str(payload.get("shift_id", shift.id)),
        approval_id=str(payload.get("approval_id", "")),
        state=str(payload.get("state", "")),
        reason=str(payload.get("reason", "") or ""),
        error=str(payload.get("error", "") or ""),
        raw=payload,
    )


def grant_approval(approval_id: str, reason: str = "approved") -> Dict[str, Any]:
    cloud = _ensure_cloud()
    if cloud is not None:
        return cloud.grant_approval(approval_id, reason)
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
    cloud = _ensure_cloud()
    if cloud is not None:
        return cloud.deny_approval(approval_id, reason)
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


def fetch_shifts() -> List[Dict[str, Any]]:
    cloud = _ensure_cloud()
    if cloud is not None:
        return cloud.fetch_shifts()
    _, payload = http_json("GET", "/demo/shifts", expect_status=(200,))
    return payload.get("shifts", []) or []


def fetch_timeline(shift_id: str) -> List[Dict[str, Any]]:
    cloud = _ensure_cloud()
    if cloud is not None:
        return cloud.fetch_timeline(shift_id)
    _, payload = http_json("GET", f"/entities/{shift_id}/timeline", expect_status=(200,))
    return payload.get("timeline", []) or []


def fetch_rebuild(shift_id: str) -> Dict[str, Any]:
    cloud = _ensure_cloud()
    if cloud is not None:
        return cloud.fetch_rebuild(shift_id)
    _, payload = http_json("GET", f"/demo/rebuild?entity={shift_id}", expect_status=(200,))
    return payload


# ---------------------------------------------------------------------------
# Display
# ---------------------------------------------------------------------------

def banner(title: str) -> None:
    bar = "=" * 72
    print(bar)
    print(f"  {title}")
    print(bar)


def print_shift(shift: Shift, call: ToolCall, outcome: KiffOutcome) -> None:
    print()
    print(f"[shift {shift.id}] tool={call.tool}")
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
    cloud = _ensure_cloud()
    if cloud is not None:
        # No local server in cloud mode; the binary is already up
        # at KIFF_CLOUD_URL. Run the seed phase here so the agent
        # picks up shifts in OPEN, matching local-mode.
        if cloud.config.seed_demo_shifts:
            cloud.seed_shifts()
        return
    deadline = time.time() + timeout_seconds
    while time.time() < deadline:
        try:
            http_json("GET", "/demo/shifts", expect_status=(200,))
            return
        except Exception:
            time.sleep(0.2)
    raise RuntimeError(f"KIFF server at {base_url()} did not become ready")


def run_shifts() -> List[Tuple[Shift, ToolCall, KiffOutcome]]:
    config = AgentConfig()
    provider = make_provider(config)
    cloud = _ensure_cloud()
    target_url = cloud.config.url if cloud is not None else base_url()
    mode = "cloud" if cloud is not None else "local"
    print(f"[run-with-kiff] {provider_banner(provider)} mode={mode} url={target_url}")
    shifts = load_shifts()
    results: List[Tuple[Shift, ToolCall, KiffOutcome]] = []
    for shift in shifts:
        call = decide_for_shift(provider, shift)
        outcome = call_kiff(call, shift)
        print_shift(shift, call, outcome)
        results.append((shift, call, outcome))
    return results


def auto_resolve(results: List[Tuple[Shift, ToolCall, KiffOutcome]]) -> List[Tuple[Shift, ToolCall, KiffOutcome]]:
    pending = [row for row in results if row[2].outcome == "approval_required"]
    if not pending:
        return []
    print()
    banner("Operator review")
    for _, _, outcome in pending:
        print(f"  - pending: {outcome.approval_id} ({outcome.action} on {outcome.shift_id})")

    retried: List[Tuple[Shift, ToolCall, KiffOutcome]] = []
    # Grant the first pending approval; deny the rest. The 5-shift
    # batch in the offline fixture only ever opens one approval, so
    # this loop typically executes the grant branch exactly once.
    for idx, (shift, call, outcome) in enumerate(pending):
        if idx == 0:
            grant_approval(outcome.approval_id, reason="reviewed; reasonable bulk order")
            print()
            print(f"[run-with-kiff] granted {outcome.approval_id} -> retrying {shift.id}")
        else:
            deny_approval(outcome.approval_id, reason="denied; insufficient evidence")
            print()
            print(f"[run-with-kiff] denied  {outcome.approval_id} -> retrying {shift.id}")
        retry_outcome = call_kiff(call, shift, approval_id=outcome.approval_id)
        print_shift(shift, call, retry_outcome)
        retried.append((shift, call, retry_outcome))
    return retried


def print_summary(results: List[Tuple[Shift, ToolCall, KiffOutcome]],
                  retried: List[Tuple[Shift, ToolCall, KiffOutcome]]) -> None:
    print()
    banner("Summary (one row per shift, first outcome)")
    print(f"{'shift':<10} {'tool':<22} {'first outcome':<28} {'reason / final state'}")
    print("-" * 96)
    retried_by_shift = {row[0].id: row[2] for row in retried}
    for shift, call, outcome in results:
        retry = retried_by_shift.get(shift.id)
        final_state = (retry.state if retry else outcome.state) or "?"
        if outcome.outcome == "approval_required":
            verdict = "granted->executed" if retry and retry.outcome == "executed" else "denied->still blocked"
            note = f"{verdict} ({final_state})"
        elif outcome.outcome in ("blocked_not_in_catalog", "blocked_after_hours"):
            note = f"{outcome.reason} ({final_state})"
        else:
            note = f"{outcome.reason or 'no approval needed'} ({final_state})"
        print(f"{shift.id:<10} {call.tool:<22} {outcome.outcome:<28} {note}")


def print_audit_section() -> None:
    print()
    banner("Audit timeline + rebuild check")
    for shift in fetch_shifts():
        shift_id = shift["id"]
        timeline = fetch_timeline(shift_id)
        print(f"  timeline({shift_id}):")
        for record in timeline:
            kind = record.get("kind", "?")
            actor = record.get("actor_id", "")
            data = record.get("data") or {}
            target = data.get("action") or data.get("event_type") or ""
            suffix = f" [{target}]" if target else ""
            print(f"    {kind:<22} actor={actor:<14} {record.get('message', '')}{suffix}")
        info = fetch_rebuild(shift_id)
        marker = "OK" if info.get("matches") else "FAIL"
        print(
            f"  rebuild({shift_id}): materialized={info.get('materialized')!r} "
            f"replayed={info.get('replayed')!r} events={info.get('events_replayed')} {marker}"
        )
        print()


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

def run_auto() -> int:
    wait_for_server()
    banner("RUN -- one agent, five shifts, through KIFF")
    results = run_shifts()
    retried = auto_resolve(results)
    print_audit_section()
    print_summary(results, retried)
    return 0


def main(argv: Optional[List[str]] = None) -> int:
    parser = argparse.ArgumentParser(description="Run ai-cafe-ops agent through KIFF.")
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
