"""Agno-shaped support-ops starter agent.

The agent has one tool, ``refund_order``. Two providers are supported:

  - ``offline``: deterministic stub. ``make demo`` works without AWS.
  - ``bedrock``: real Agno + AwsBedrock via ``output_schema``.

Configure with ``AGNO_MODEL_PROVIDER`` and the standard AWS env vars.
The Makefile loads ``.env`` automatically; ``.env.example`` lists the
required keys.
"""

from __future__ import annotations

import os
from dataclasses import dataclass, field
from typing import Any, Dict, List, Optional


REFUND_TOOL_NAME = "refund_order"


@dataclass
class Ticket:
    id: str
    order_id: str
    summary: str
    expected_amount_cents: int


@dataclass
class ToolCall:
    tool: str
    arguments: Dict[str, Any]
    reasoning: str = ""
    confidence: float = 0.0
    raw_response: str = ""
    source: str = "offline_fixture"


# ---------------------------------------------------------------------------
# Offline provider
# ---------------------------------------------------------------------------


class OfflineProvider:
    provider_type = "offline"
    model_id = "offline-fixture"

    @property
    def real_inference(self) -> bool:
        return False

    def decide(self, ticket: Ticket) -> ToolCall:
        return ToolCall(
            tool=REFUND_TOOL_NAME,
            arguments={
                "order_id": ticket.order_id,
                "amount_cents": ticket.expected_amount_cents,
                "reason": ticket.summary,
            },
            reasoning=f"ticket {ticket.id}: {ticket.summary[:100]}",
            confidence=0.78,
            raw_response="(offline fixture; deterministic for tests/demo)",
            source="offline_fixture",
        )


# ---------------------------------------------------------------------------
# Bedrock provider
# ---------------------------------------------------------------------------


try:  # pragma: no cover - optional bedrock path
    from pydantic import BaseModel, Field

    class _RefundDecision(BaseModel):
        order_id: str = Field(description="The order id from the prompt")
        amount_cents: int = Field(description="Refund amount in USD cents")
        reason: str = Field(description="Short justification")
        confidence: float = Field(default=0.5, description="Self-reported confidence")
        reasoning: str = Field(default="", description="One-paragraph reasoning")

except ImportError:  # pragma: no cover
    _RefundDecision = None  # type: ignore[assignment]


class BedrockProvider:
    provider_type = "bedrock"

    def __init__(self) -> None:
        from agno.agent import Agent  # type: ignore
        from agno.models.aws import AwsBedrock  # type: ignore

        if _RefundDecision is None:
            raise RuntimeError("pydantic is required for bedrock")

        self.model_id = os.environ.get(
            "BEDROCK_MODEL_ID", "anthropic.claude-haiku-4-5-20251001-v1:0"
        )
        self.region = os.environ.get(
            "AWS_REGION", os.environ.get("AWS_DEFAULT_REGION", "us-east-1")
        )

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
            name="agentic-ops-starter-agent",
            model=self._model,
            output_schema=_RefundDecision,
            instructions=(
                "You are a support agent. Produce a RefundDecision for the "
                "ticket. Use the order id and amount provided; do not invent. "
                "The system enforces approval policy; do not bypass it."
            ),
            markdown=False,
        )
        prompt = (
            f"Ticket {ticket.id}\n"
            f"Order: {ticket.order_id}\n"
            f"Summary: {ticket.summary}\n"
            f"Refund amount (cents): {ticket.expected_amount_cents}\n"
            "Produce a RefundDecision."
        )
        response = agent.run(prompt)
        decision = response.content
        if isinstance(decision, _RefundDecision):
            order_id = decision.order_id or ticket.order_id
            amount = int(decision.amount_cents) if decision.amount_cents else int(ticket.expected_amount_cents)
            reason = decision.reason or ticket.summary
            reasoning = decision.reasoning or "(model returned no reasoning)"
            confidence = float(decision.confidence) if decision.confidence is not None else 0.5
        else:
            order_id = ticket.order_id
            amount = ticket.expected_amount_cents
            reason = ticket.summary
            reasoning = f"(unstructured response) {str(decision)[:200]}"
            confidence = 0.5
        return ToolCall(
            tool=REFUND_TOOL_NAME,
            arguments={"order_id": order_id, "amount_cents": amount, "reason": reason},
            reasoning=reasoning,
            confidence=confidence,
            raw_response=str(getattr(response, "content", ""))[:500],
            source=f"bedrock:{self.model_id}",
        )


# ---------------------------------------------------------------------------
# Selector
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
        except Exception as exc:  # pragma: no cover
            print(f"[agent] bedrock unavailable, falling back to offline: {exc}")
            return OfflineProvider()
    return OfflineProvider()


def provider_banner(provider) -> str:
    real = "true" if getattr(provider, "real_inference", False) else "false"
    model = getattr(provider, "model_id", "?")
    return f"provider={provider.provider_type} model={model} real_inference={real}"


# Two seed tickets matched to the seeded orders in the Go server.
def seed_tickets() -> List[Ticket]:
    return [
        Ticket(id="ticket-1", order_id="order-1", summary="duplicate charge, small amount", expected_amount_cents=4200),
        Ticket(id="ticket-2", order_id="order-2", summary="broken product, full refund", expected_amount_cents=99900),
    ]


__all__ = [
    "AgentConfig",
    "BedrockProvider",
    "OfflineProvider",
    "REFUND_TOOL_NAME",
    "Ticket",
    "ToolCall",
    "make_provider",
    "provider_banner",
    "seed_tickets",
]
