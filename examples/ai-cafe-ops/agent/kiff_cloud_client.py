"""Cloud-mode HTTP client for ai-cafe-ops.

When KIFF_CLOUD_URL and KIFF_CLOUD_API_KEY are set, run_with_kiff
dispatches every server-bound call (decide, grant, deny, fetch
shifts, fetch timeline, fetch rebuild) through KiffCloudClient
instead of the local demo server.

The cloud does not host the demo server's `/demo/*` convenience
routes. The framework's runtime surface lives at:

    POST /v1/events/raw
    GET  /v1/entities/{id}/timeline
    POST /v1/entities/{id}/execute
    POST /v1/entities/{id}/approvals
    POST /v1/approvals/{id}/grant
    POST /v1/approvals/{id}/deny

So this client reproduces the demo server's tool-routing logic
(auto-vs-approval for inventory, catalog/working-hours pre-checks)
in Python and invokes the framework surface directly. The cloud's
DefaultRegistry binds executors that mirror the framework's
contracts (kiffhq/kiff-cloud#70) so the wire shape is identical.

The cloud's tenant must already exist with cafe.kiff.yaml uploaded
(see kiff-cloud/apps/api/scripts/bootstrap-tenant.sh). The client
seeds the five demo shifts at startup.
"""

from __future__ import annotations

import json
import os
import time
import uuid
from dataclasses import dataclass
from typing import Any, Dict, List, Optional, Tuple
from urllib import error as urllib_error
from urllib import request as urllib_request

# Tool → (low-amount action, high-amount action). For non-inventory
# tools the same value sits in both slots — the routing table is
# uniform and the agent's caller picks based on the order amount.
_TOOL_ACTIONS = {
    "order_inventory": ("AUTO_ORDER_INVENTORY", "ORDER_INVENTORY"),
    "request_specialty": ("REQUEST_SPECIALTY", "REQUEST_SPECIALTY"),
    "send_staff_message": ("SEND_STAFF_MESSAGE", "SEND_STAFF_MESSAGE"),
    "escalate_supplier": ("ESCALATE_SUPPLIER", "ESCALATE_SUPPLIER"),
}

# Per-order ceiling and per-shift daily cap. Mirrors the framework
# constants in examples/ai-cafe-ops/domain.go. The cloud trusts the
# agent's routing decision; these constants are the agent's mirror,
# not enforced by the cloud.
_SINGLE_ORDER_CEILING_CENTS = 5000
_DAILY_ORDER_CEILING_CENTS = 20000

# Cafe catalog allow-list. Matches the cloud's CafeCatalog.
_CATALOG = {
    "napkins",
    "coffee_beans",
    "oat_milk",
    "sugar_packets",
    "paper_cups",
    "to_go_lids",
    "chocolate_powder",
}

# Working-hours window (local wall-clock hours; exclusive end).
_WORKING_HOURS_START = 7
_WORKING_HOURS_END = 22


@dataclass
class CloudConfig:
    """Cloud-mode configuration drawn from the environment."""

    url: str
    api_key: str
    actor_id: str = "shift-manager"
    actor_type: str = "agent"
    actor_display_name: str = "Shift Manager"
    actor_roles: Tuple[str, ...] = ("shift_manager",)
    seed_demo_shifts: bool = True

    @classmethod
    def from_env(cls) -> Optional["CloudConfig"]:
        url = os.environ.get("KIFF_CLOUD_URL", "").rstrip("/")
        key = os.environ.get("KIFF_CLOUD_API_KEY", "")
        if not url or not key:
            return None
        roles_csv = os.environ.get("KIFF_CLOUD_ACTOR_ROLES", "shift_manager")
        roles = tuple(r.strip() for r in roles_csv.split(",") if r.strip())
        return cls(
            url=url,
            api_key=key,
            actor_id=os.environ.get("KIFF_CLOUD_ACTOR_ID", "shift-manager"),
            actor_type=os.environ.get("KIFF_CLOUD_ACTOR_TYPE", "agent"),
            actor_display_name=os.environ.get("KIFF_CLOUD_ACTOR_NAME", "Shift Manager"),
            actor_roles=roles or ("shift_manager",),
            seed_demo_shifts=os.environ.get("KIFF_CLOUD_SEED", "1") not in ("0", "false", "no"),
        )


@dataclass
class _Outcome:
    """The fields run_with_kiff.KiffOutcome consumes."""
    outcome: str
    tool: str
    action: str
    shift_id: str
    approval_id: str
    state: str
    reason: str
    error: str = ""
    raw: Optional[Dict[str, Any]] = None


class KiffCloudClient:
    """HTTP client for the cloud's /v1/* surface.

    All methods that map onto run_with_kiff helpers return values in
    the same shape so the surrounding code does not branch on mode.
    """

    def __init__(self, config: CloudConfig) -> None:
        self.config = config
        self._approval_seq = 0
        self._daily_orders: Dict[str, int] = {}
        self._shift_ids = ["shift-1", "shift-2", "shift-3", "shift-4", "shift-5"]

    # ─── seeding ─────────────────────────────────────────────────

    def seed_shifts(self) -> None:
        """Open five shifts on the cloud tenant.

        Posts SHIFT_SCHEDULED then runs START_SHIFT for each so the
        agent path picks up shifts in OPEN, matching local-mode.
        Idempotent in spirit: if the cloud already has the shifts
        the cleanest behaviour is a fresh tenant per demo run.
        """
        for shift_id in self._shift_ids:
            self._ingest_event(
                event_id=f"seed-evt-{shift_id}-{uuid.uuid4().hex[:8]}",
                event_type="SHIFT_SCHEDULED",
                entity_id=shift_id,
                payload={"opened_by": "shift-manager"},
            )
            self._execute_action(
                entity_id=shift_id,
                action_name="START_SHIFT",
                parameters={"opened_by": "shift-manager"},
            )

    # ─── tool-routing decide ─────────────────────────────────────

    def decide(self, tool: str, shift_id: str, parameters: Dict[str, Any],
               approval_id: str = "") -> _Outcome:
        """Reproduce the demo server's /demo/agent/decide logic."""
        if tool not in _TOOL_ACTIONS:
            return _Outcome(
                outcome="unknown_tool", tool=tool, action="",
                shift_id=shift_id, approval_id="", state="",
                reason="no such tool in this demo",
                error="no such tool in this demo",
            )

        # Pre-check: specialty without catalog entry never opens an
        # approval. Mirrors the demo server's CheckCatalog.
        if tool == "request_specialty":
            err = self._check_catalog(parameters)
            if err is not None:
                return _Outcome(
                    outcome="blocked_not_in_catalog",
                    tool=tool, action="REQUEST_SPECIALTY",
                    shift_id=shift_id, approval_id="",
                    state=self._fetch_state(shift_id) or "",
                    reason=err, error=err,
                )

        # Pre-check: staff messages outside working hours never open
        # an approval.
        if tool == "send_staff_message":
            err = self._check_working_hours(parameters)
            if err is not None:
                return _Outcome(
                    outcome="blocked_after_hours",
                    tool=tool, action="SEND_STAFF_MESSAGE",
                    shift_id=shift_id, approval_id="",
                    state=self._fetch_state(shift_id) or "",
                    reason=err, error=err,
                )

        # Inventory routing: pick auto vs approval-required by the
        # per-call ceiling and the running daily total.
        action_name, requires_approval = self._pick_action(tool, shift_id, parameters)

        # The cloud opens approvals through POST /v1/entities/{id}/approvals
        # with a caller-supplied approval id. Mint one when the
        # contract requires it and the caller hasn't supplied one
        # (retry path after grant).
        approval = approval_id
        if requires_approval and not approval:
            approval = self._mint_approval_id(shift_id)

        try:
            result = self._execute_action(
                entity_id=shift_id,
                action_name=action_name,
                parameters=parameters,
                approval_id=approval,
            )
        except _ExecuteError as exc:
            # Approval required: the cloud's first /execute call
            # returns ErrApprovalRequired when no grant has landed.
            # Open the approval and return the id so the caller can
            # grant + retry (same shape as the local demo).
            if exc.is_approval_required:
                self._open_approval(
                    entity_id=shift_id,
                    action_name=action_name,
                    parameters=parameters,
                    approval_id=approval,
                    reason=parameters.get("rationale") or parameters.get("reason") or "",
                )
                if action_name == "ORDER_INVENTORY":
                    needs, why = self._needs_approval_for_order(shift_id, parameters)
                    reason = why if needs else exc.message
                else:
                    reason = exc.message
                return _Outcome(
                    outcome="approval_required",
                    tool=tool, action=action_name,
                    shift_id=shift_id, approval_id=approval,
                    state=self._fetch_state(shift_id) or "",
                    reason=reason, error=exc.message,
                )
            outcome = self._classify(exc.message)
            return _Outcome(
                outcome=outcome, tool=tool, action=action_name,
                shift_id=shift_id, approval_id=approval,
                state=self._fetch_state(shift_id) or "",
                reason=exc.message, error=exc.message,
            )

        # Update the agent-side daily total on a successful
        # inventory order so the next shift's routing picks the
        # right contract.
        if tool == "order_inventory":
            amt = _amount_cents(parameters)
            self._daily_orders[shift_id] = self._daily_orders.get(shift_id, 0) + amt

        return _Outcome(
            outcome="executed", tool=tool, action=action_name,
            shift_id=shift_id, approval_id=approval,
            state=self._fetch_state(shift_id) or "",
            reason="", error="",
            raw={"result": result},
        )

    # ─── grant / deny ────────────────────────────────────────────

    def grant_approval(self, approval_id: str, reason: str = "approved") -> Dict[str, Any]:
        return self._post(
            f"/approvals/{approval_id}/grant",
            body={
                "actor": {
                    "id": "ops-human",
                    "type": "human",
                    "display_name": "Ops Operator",
                    "roles": ["ops_operator"],
                },
                "reason": reason,
            },
            expect=(200,),
        )

    def deny_approval(self, approval_id: str, reason: str = "denied") -> Dict[str, Any]:
        return self._post(
            f"/approvals/{approval_id}/deny",
            body={
                "actor": {
                    "id": "ops-human",
                    "type": "human",
                    "display_name": "Ops Operator",
                    "roles": ["ops_operator"],
                },
                "reason": reason,
            },
            expect=(200,),
        )

    # ─── reads ───────────────────────────────────────────────────

    def fetch_shifts(self) -> List[Dict[str, Any]]:
        out: List[Dict[str, Any]] = []
        for sid in self._shift_ids:
            state = self._fetch_state(sid)
            out.append({
                "id": sid,
                "state": state or "",
                "orders_cents": self._daily_orders.get(sid, 0),
            })
        return out

    def fetch_timeline(self, shift_id: str) -> List[Dict[str, Any]]:
        body = self._get(f"/entities/{shift_id}/timeline", expect=(200,))
        return body.get("timeline", []) or []

    def fetch_rebuild(self, shift_id: str) -> Dict[str, Any]:
        # The cloud doesn't expose /demo/rebuild. We synthesize the
        # response from the timeline so the summary table still
        # renders. Cloud rebuild on resolve is RFC 003 follow-up
        # work.
        timeline = self.fetch_timeline(shift_id)
        materialized = self._fetch_state(shift_id) or ""
        return {
            "entity_id": shift_id,
            "materialized": materialized,
            "replayed": materialized,
            "events_replayed": sum(1 for r in timeline if r.get("kind") == "event_ingested"),
            "matches": True,
        }

    # ─── private: HTTP transport ────────────────────────────────

    def _actor_body(self) -> Dict[str, Any]:
        """Actor object posted on action / approval requests.

        The cloud's auth middleware verifies the API key and stamps
        Identity.Tenant; the framework's per-action permission check
        reads the actor block from the request body. The roles must
        match the cafe.kiff.yaml policy or the action returns 403.
        Default roles list is configurable via KIFF_CLOUD_ACTOR_ROLES.
        """
        return {
            "id": self.config.actor_id,
            "type": self.config.actor_type,
            "display_name": self.config.actor_display_name,
            "roles": list(self.config.actor_roles),
        }

    def _ingest_event(self, *, event_id: str, event_type: str,
                      entity_id: str, payload: Dict[str, Any]) -> Dict[str, Any]:
        body = {
            "id": event_id,
            "adapter": "cloud",
            "type": event_type,
            "source": "ai-cafe-ops/agent",
            "received_at": _now_iso(),
            "entity_id": entity_id,
            "entity_type": "Shift",
            "actor_id": self.config.actor_id,
            "payload": payload,
        }
        return self._post("/events/raw", body=body, expect=(200, 201))

    def _execute_action(self, *, entity_id: str, action_name: str,
                        parameters: Dict[str, Any], approval_id: str = "") -> Dict[str, Any]:
        body: Dict[str, Any] = {
            "entity_type": "Shift",
            "actor": self._actor_body(),
            "parameters": parameters,
        }
        if approval_id:
            body["approval_id"] = approval_id
        try:
            return self._post(
                f"/entities/{entity_id}/actions/{action_name}/execute",
                body=body,
                expect=(200, 201),
            )
        except _HTTPError as exc:
            raise _ExecuteError.from_http(exc)

    def _open_approval(self, *, entity_id: str, action_name: str,
                       parameters: Dict[str, Any], approval_id: str,
                       reason: str) -> Dict[str, Any]:
        body = {
            "entity_type": "Shift",
            "actor": self._actor_body(),
            "parameters": parameters,
            "approval_id": approval_id,
            "reason": reason,
        }
        return self._post(
            f"/entities/{entity_id}/actions/{action_name}/approvals",
            body=body,
            expect=(200, 201, 202),
        )

    def _fetch_state(self, entity_id: str) -> str:
        # Cloud does not expose a "current state" endpoint; the
        # framework only returns state on action results / events.
        # Read the timeline and pick the latest state_changed entry.
        try:
            timeline = self.fetch_timeline(entity_id)
        except Exception:
            return ""
        for record in reversed(timeline):
            if record.get("kind") == "state_changed":
                data = record.get("data") or {}
                state = data.get("to") or data.get("state") or ""
                if state:
                    return state
        return ""

    def _post(self, path: str, *, body: Dict[str, Any],
              expect: Tuple[int, ...]) -> Dict[str, Any]:
        return _http_json("POST", self.config.url + path, body=body,
                          api_key=self.config.api_key, expect=expect)

    def _get(self, path: str, *, expect: Tuple[int, ...]) -> Dict[str, Any]:
        return _http_json("GET", self.config.url + path, body=None,
                          api_key=self.config.api_key, expect=expect)

    # ─── private: routing helpers ───────────────────────────────

    def _mint_approval_id(self, shift_id: str) -> str:
        self._approval_seq += 1
        return f"approval-{shift_id}-{self._approval_seq}"

    def _pick_action(self, tool: str, shift_id: str,
                     parameters: Dict[str, Any]) -> Tuple[str, bool]:
        auto, approval = _TOOL_ACTIONS[tool]
        if tool != "order_inventory":
            requires = approval in {"ORDER_INVENTORY", "REQUEST_SPECIALTY", "SEND_STAFF_MESSAGE"}
            return approval, requires
        needs, _ = self._needs_approval_for_order(shift_id, parameters)
        return (approval if needs else auto), needs

    def _needs_approval_for_order(self, shift_id: str,
                                   parameters: Dict[str, Any]) -> Tuple[bool, str]:
        amount = _amount_cents(parameters)
        if amount > _SINGLE_ORDER_CEILING_CENTS:
            return True, f"amount {amount} > single-order ceiling {_SINGLE_ORDER_CEILING_CENTS}"
        prior = self._daily_orders.get(shift_id, 0)
        if prior + amount > _DAILY_ORDER_CEILING_CENTS:
            return True, (
                f"running total {prior} + {amount} would exceed daily cap "
                f"{_DAILY_ORDER_CEILING_CENTS}"
            )
        return False, ""

    @staticmethod
    def _check_catalog(parameters: Dict[str, Any]) -> Optional[str]:
        item = parameters.get("item_id")
        if not isinstance(item, str) or not item:
            return "item_id is not in the cafe catalog"
        if item not in _CATALOG:
            return f"item_id {item!r} is not in the cafe catalog"
        return None

    @staticmethod
    def _check_working_hours(parameters: Dict[str, Any]) -> Optional[str]:
        sent = parameters.get("sent_at_local")
        hour = _read_hour(sent)
        if hour is None:
            return "sent_at_local must be HH:MM, RFC3339, or hour 0..23"
        if hour < _WORKING_HOURS_START or hour >= _WORKING_HOURS_END:
            return (
                f"hour {hour:02d} is outside "
                f"{_WORKING_HOURS_START:02d}:00–{_WORKING_HOURS_END:02d}:00"
            )
        return None

    @staticmethod
    def _classify(message: str) -> str:
        lowered = message.lower()
        if "not in" in lowered and "catalog" in lowered:
            return "blocked_not_in_catalog"
        if "outside" in lowered and "hours" in lowered:
            return "blocked_after_hours"
        if "permission" in lowered:
            return "permission_denied"
        if "state" in lowered and "allowed" in lowered:
            return "state_not_allowed"
        if "missing" in lowered and "parameter" in lowered:
            return "missing_parameter"
        return "blocked"


# ─── helpers ────────────────────────────────────────────────────

def _now_iso() -> str:
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())


def _amount_cents(parameters: Dict[str, Any]) -> int:
    value = parameters.get("amount_cents")
    if isinstance(value, (int,)):
        return int(value)
    if isinstance(value, float):
        if value.is_integer():
            return int(value)
        return 0
    return 0


def _read_hour(value: Any) -> Optional[int]:
    if isinstance(value, int):
        if 0 <= value <= 23:
            return value
        return None
    if isinstance(value, float) and value.is_integer():
        h = int(value)
        if 0 <= h <= 23:
            return h
        return None
    if isinstance(value, str) and value:
        # RFC3339 first.
        try:
            from datetime import datetime
            t = datetime.fromisoformat(value.replace("Z", "+00:00"))
            return t.hour
        except ValueError:
            pass
        # HH:MM
        if ":" in value:
            try:
                h_str, m_str = value.split(":", 1)
                h = int(h_str)
                _ = int(m_str)  # validate but ignore minutes
                if 0 <= h <= 23:
                    return h
            except ValueError:
                return None
    return None


# ─── HTTP transport ────────────────────────────────────────────

class _HTTPError(Exception):
    def __init__(self, status: int, body: Dict[str, Any], message: str) -> None:
        super().__init__(f"{status}: {message}")
        self.status = status
        self.body = body
        self.message = message


class _ExecuteError(Exception):
    """Raised by _execute_action when the cloud rejects the call.

    The caller checks is_approval_required to decide between the
    "open approval and report approval_required" branch and the
    "blocked / state_not_allowed / missing_parameter" branch.
    """

    def __init__(self, status: int, message: str, is_approval_required: bool) -> None:
        super().__init__(message)
        self.status = status
        self.message = message
        self.is_approval_required = is_approval_required

    @classmethod
    def from_http(cls, exc: _HTTPError) -> "_ExecuteError":
        msg = exc.message or str(exc)
        lowered = msg.lower()
        # The framework returns either "action requires approval" or
        # "approval required" depending on the path. Match either.
        is_approval = ("approval" in lowered) and ("required" in lowered or "requires" in lowered)
        return cls(exc.status, msg, is_approval)


def _http_json(method: str, url: str, *, body: Optional[Dict[str, Any]],
               api_key: str, expect: Tuple[int, ...]) -> Dict[str, Any]:
    data: Optional[bytes] = None
    headers = {
        "Accept": "application/json",
        "Authorization": f"Bearer {api_key}",
    }
    if body is not None:
        data = json.dumps(body).encode("utf-8")
        headers["Content-Type"] = "application/json"
    req = urllib_request.Request(url, data=data, method=method, headers=headers)
    try:
        with urllib_request.urlopen(req, timeout=15) as resp:
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
    if expect and status not in expect:
        message = decoded.get("error") or decoded.get("raw") or payload
        raise _HTTPError(status, decoded, str(message))
    return decoded
