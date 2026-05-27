"""Agno-shaped support agent for the refund-agno demo.

This module contains the agent definition only. It does not act on the
world. The two runners (``run_no_kiff`` and ``run_with_kiff``) supply the
tool implementation that does.

Two providers are supported:

- ``offline``: a deterministic stub that returns canned tool calls so the
  demo and tests are reproducible without any API keys. This is the
  default and what ``make demo`` uses.
- ``bedrock``: the real Agno + AwsBedrock integration that powers
  ``the-line``'s agent runtime. Activated by setting
  ``AGNO_MODEL_PROVIDER=bedrock`` and the standard AWS env vars. The same
  ``Agent`` object, the same tool spec; only the provider changes.

Why the dual provider matters: the buyer should be able to clone, run
``make demo``, and see KIFF block a refund in 60 seconds with no AWS
account. ``make demo-bedrock`` is what you show on the call.
"""

from __future__ import annotations

import json
import os
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Callable, Dict, List, Optional


# ---------------------------------------------------------------------------
# Tool surface (model-facing schema)
#
# An Agno agent registers Python callables as tools. Internally Agno (and
# every major LLM SDK) ends up calling the tool with kwargs derived from the
# model's JSON arguments. We keep the same shape for the offline provider so
# that swapping providers does not change anything about how the runners use
# the agent.
# ---------------------------------------------------------------------------

REFUND_TOOL_NAME = "refund_order"
REFUND_TOOL_DESCRIPTION = (
    "Issue a refund for a paid order. Use when the support ticket asks for a "
    "refund. Provide order_id, amount_cents (integer, USD cents), and a short "
    "reason. The system enforces approval policy; do not bypass it."
)

REFUND_TOOL_PARAMETERS = {
    "type": "object",
    "properties": {
        "order_id": {"type": "string"},
        "amount_cents": {"type": "integer", "minimum": 1},
        "reason": {"type": "string"},
    },
    "required": ["order_id", "amount_cents", "reason"],
}


# ---------------------------------------------------------------------------
# Ticket loading
# ---------------------------------------------------------------------------


@dataclass
class Ticket:
    """A support ticket the agent must act on."""

    id: str
    order_id: str
    customer_message: str
    expected_amount_cents: int
    notes: str = ""


def default_tickets_path() -> Path:
    return Path(__file__).resolve().parent / "tickets.json"


def load_tickets(path: Optional[Path] = None) -> List[Ticket]:
    path = path or default_tickets_path()
    with path.open("r", encoding="utf-8") as fh:
        raw = json.load(fh)
    return [Ticket(**row) for row in raw]


# ---------------------------------------------------------------------------
# Agent decision model
# ---------------------------------------------------------------------------


@dataclass
class ToolCall:
    """The shape every provider produces, regardless of model."""

    tool: str
    arguments: Dict[str, Any]
    reasoning: str = ""
    confidence: float = 0.0
    raw_response: str = ""
    # Where the call came from. "offline_fixture" for the deterministic
    # stub, "bedrock:<model_id>" for real LLM inference. Surfaced in the
    # runner output so a buyer can verify real_inference=true.
    source: str = "offline_fixture"

    @property
    def order_id(self) -> str:
        return str(self.arguments.get("order_id", ""))

    @property
    def amount_cents(self) -> int:
        try:
            return int(self.arguments.get("amount_cents", 0))
        except (TypeError, ValueError):
            return 0

    @property
    def reason(self) -> str:
        return str(self.arguments.get("reason", ""))


# ---------------------------------------------------------------------------
# Providers
# ---------------------------------------------------------------------------


class OfflineProvider:
    """Deterministic tool-call producer keyed off the ticket id.

    Mirrors the shape of ``the-line``'s offline provider so a buyer who
    has both repos open sees the same pattern. The decision table below is
    intentionally simple: it produces one tool call per ticket so the demo
    is reproducible and so the side-by-side run vs. KIFF is fair.
    """

    provider_type = "offline"
    model_id = "offline-fixture"

    @property
    def real_inference(self) -> bool:
        return False

    def decide(self, ticket: Ticket) -> ToolCall:
        # The agent always picks ``refund_order`` for these tickets. The
        # amount comes from the ticket's expected_amount_cents so each
        # ticket exercises a distinct path through KIFF.
        reasoning_lines = [
            f"ticket {ticket.id}: customer message excerpt",
            f"  > {ticket.customer_message[:120]}",
            f"decision: issue refund of {ticket.expected_amount_cents} cents on {ticket.order_id}.",
        ]
        return ToolCall(
            tool=REFUND_TOOL_NAME,
            arguments={
                "order_id": ticket.order_id,
                "amount_cents": ticket.expected_amount_cents,
                "reason": ticket.notes or "customer requested refund",
            },
            reasoning="\n".join(reasoning_lines),
            confidence=0.78,
            raw_response="(offline fixture; deterministic for tests/demo)",
            source="offline_fixture",
        )


# ---------------------------------------------------------------------------
# Bedrock provider
#
# Uses Agno's structured-output pattern: ``Agent(output_schema=PydanticModel)``.
# The model returns structured JSON that maps cleanly to a refund tool call.
# This keeps the demo's contract stable across providers: in both modes the
# runner gets a ToolCall with arguments, reasoning, confidence.
# ---------------------------------------------------------------------------


try:  # pragma: no cover - optional import path exercised in demo-bedrock
    from pydantic import BaseModel, Field

    class _RefundDecision(BaseModel):
        """Structured refund decision the model is asked to produce."""

        order_id: str = Field(description="The order id provided in the prompt")
        amount_cents: int = Field(description="Refund amount in USD cents")
        reason: str = Field(description="Short justification for the refund")
        confidence: float = Field(
            default=0.5,
            description="Self-reported confidence between 0 and 1",
        )
        reasoning: str = Field(
            default="",
            description="One-paragraph reasoning for the decision",
        )

except ImportError:  # pragma: no cover - pydantic is required for bedrock
    _RefundDecision = None  # type: ignore[assignment]


class BedrockProvider:
    """Real Agno + AwsBedrock provider.

    Imported lazily so the offline path has no AWS or agno dependency.
    Activated by ``AGNO_MODEL_PROVIDER=bedrock`` plus the standard AWS env
    vars. Uses ``Agent(output_schema=...)`` exactly like
    ``the-line/apps/agent-runtime/src/agent_runtime/agents/challenge_evaluation.py``.
    """

    provider_type = "bedrock"

    def __init__(self) -> None:
        # Lazy imports so offline machines without agno/boto3 still work.
        from agno.agent import Agent  # type: ignore
        from agno.models.aws import AwsBedrock  # type: ignore

        if _RefundDecision is None:
            raise RuntimeError("pydantic is required for the bedrock provider")

        self.model_id = os.environ.get(
            "BEDROCK_MODEL_ID", "anthropic.claude-haiku-4-5-20251001-v1:0"
        )
        self.region = os.environ.get(
            "AWS_REGION", os.environ.get("AWS_DEFAULT_REGION", "us-east-1")
        )

        # Mirror the-line's POC: pass region + explicit keys when present;
        # otherwise rely on the default boto3 credential chain. We do NOT
        # log the keys; never echo them back.
        kwargs: Dict[str, Any] = {"id": self.model_id, "aws_region": self.region}
        if os.environ.get("AWS_ACCESS_KEY_ID"):
            kwargs["aws_access_key_id"] = os.environ["AWS_ACCESS_KEY_ID"]
        if os.environ.get("AWS_SECRET_ACCESS_KEY"):
            kwargs["aws_secret_access_key"] = os.environ["AWS_SECRET_ACCESS_KEY"]

        self._model = AwsBedrock(**kwargs)
        self._Agent = Agent

    @property
    def real_inference(self) -> bool:
        return True

    def decide(self, ticket: Ticket) -> ToolCall:
        agent = self._Agent(
            name="refund-agno-support-agent",
            model=self._model,
            output_schema=_RefundDecision,
            instructions=(
                "You are a support ops agent. For each ticket, decide whether "
                "to issue a refund. ALWAYS produce a structured RefundDecision "
                "with order_id, amount_cents (integer USD cents), reason, "
                "confidence between 0 and 1, and a one-paragraph reasoning. "
                "Use the order id and amount provided in the ticket; do not "
                "invent ids or amounts. The system enforces approval policy; "
                "do not bypass it."
            ),
            markdown=False,
        )

        prompt = (
            f"Ticket {ticket.id}\n"
            f"Order: {ticket.order_id}\n"
            f"Customer says: {ticket.customer_message}\n"
            f"Notes: {ticket.notes}\n"
            f"Refund amount to issue (cents): {ticket.expected_amount_cents}\n"
            "Produce a RefundDecision."
        )

        response = agent.run(prompt)
        decision = response.content

        if isinstance(decision, _RefundDecision):
            order_id = decision.order_id or ticket.order_id
            amount = int(decision.amount_cents) if decision.amount_cents else int(ticket.expected_amount_cents)
            reason = decision.reason or (ticket.notes or "customer requested refund")
            reasoning = decision.reasoning or "(model returned no reasoning)"
            confidence = float(decision.confidence) if decision.confidence is not None else 0.5
        else:
            # The model returned unstructured text. Fall back to the ticket
            # data and capture the raw content as reasoning so the audit
            # trail still has provenance.
            order_id = ticket.order_id
            amount = ticket.expected_amount_cents
            reason = ticket.notes or "customer requested refund"
            reasoning = f"(unstructured response) {str(decision)[:280]}"
            confidence = 0.5

        return ToolCall(
            tool=REFUND_TOOL_NAME,
            arguments={
                "order_id": order_id,
                "amount_cents": amount,
                "reason": reason,
            },
            reasoning=reasoning,
            confidence=confidence,
            raw_response=str(getattr(response, "content", ""))[:500],
            source=f"bedrock:{self.model_id}",
        )


# ---------------------------------------------------------------------------
# Provider selector
# ---------------------------------------------------------------------------


@dataclass
class AgentConfig:
    provider_name: str = field(
        default_factory=lambda: os.environ.get("AGNO_MODEL_PROVIDER", "offline")
    )

    def is_bedrock(self) -> bool:
        return self.provider_name.lower() == "bedrock"


def make_provider(config: Optional[AgentConfig] = None):
    config = config or AgentConfig()
    if config.is_bedrock():
        try:
            return BedrockProvider()
        except Exception as exc:  # pragma: no cover - exercised in demo-bedrock
            print(f"[agent] bedrock provider unavailable, falling back to offline: {exc}")
            return OfflineProvider()
    return OfflineProvider()


def provider_banner(provider) -> str:
    """One-line provider summary suitable for stdout. No secrets."""
    real = "true" if getattr(provider, "real_inference", False) else "false"
    model = getattr(provider, "model_id", "?")
    return (
        f"provider={provider.provider_type} model={model} real_inference={real}"
    )


# ---------------------------------------------------------------------------
# Public API used by the runners
# ---------------------------------------------------------------------------


def decide_for_ticket(provider, ticket: Ticket) -> ToolCall:
    """Single entry point: ask the provider for one tool call per ticket."""
    return provider.decide(ticket)


__all__ = [
    "AgentConfig",
    "BedrockProvider",
    "OfflineProvider",
    "REFUND_TOOL_DESCRIPTION",
    "REFUND_TOOL_NAME",
    "REFUND_TOOL_PARAMETERS",
    "Ticket",
    "ToolCall",
    "decide_for_ticket",
    "default_tickets_path",
    "load_tickets",
    "make_provider",
    "provider_banner",
]
