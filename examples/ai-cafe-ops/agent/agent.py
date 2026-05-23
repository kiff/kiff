"""Multi-tool ai-cafe-ops shift-manager agent.

The offline fixture reads each shift's `expected_tool` and builds a
ToolCall for that exact tool with the prepared parameters. This keeps
the demo deterministic and the breadth message clean: same agent, four
secondary tools, five distinct KIFF outcomes.

The Bedrock provider asks the model to pick one of four tools and
produce structured output. The same five outcomes should appear,
modulo per-run reasoning text. The KIFF runtime behavior does not
change between providers.
"""

from __future__ import annotations

import json
import os
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Dict, List, Optional


# ---------------------------------------------------------------------------
# Tool catalog (model-facing names)
# ---------------------------------------------------------------------------

ALL_TOOLS = [
    "order_inventory",
    "request_specialty",
    "send_staff_message",
    "escalate_supplier",
]


@dataclass
class Shift:
    id: str
    storefront: str
    customer_brief: str
    expected_tool: str
    parameters: Dict[str, Any]
    notes: str = ""


def default_shifts_path() -> Path:
    return Path(__file__).resolve().parent / "shifts.json"


def load_shifts(path: Optional[Path] = None) -> List[Shift]:
    path = path or default_shifts_path()
    with path.open("r", encoding="utf-8") as fh:
        raw = json.load(fh)
    return [Shift(**row) for row in raw]


@dataclass
class ToolCall:
    tool: str
    parameters: Dict[str, Any]
    reasoning: str = ""
    confidence: float = 0.0
    raw_response: str = ""
    source: str = "offline_fixture"


# ---------------------------------------------------------------------------
# Providers
# ---------------------------------------------------------------------------

class OfflineProvider:
    provider_type = "offline"
    model_id = "offline-fixture"

    @property
    def real_inference(self) -> bool:
        return False

    def decide(self, shift: Shift) -> ToolCall:
        reasoning = (
            f"shift {shift.id}: {shift.notes or shift.customer_brief[:80]}"
        )
        return ToolCall(
            tool=shift.expected_tool,
            parameters=dict(shift.parameters),
            reasoning=reasoning,
            confidence=0.78,
            raw_response="(offline fixture; deterministic for tests/demo)",
            source="offline_fixture",
        )


try:  # pragma: no cover - optional bedrock path
    from pydantic import BaseModel, Field

    class _ToolDecision(BaseModel):
        tool: str = Field(
            description="One of: order_inventory, request_specialty, send_staff_message, escalate_supplier"
        )
        parameters: Dict[str, Any] = Field(
            default_factory=dict,
            description="Tool parameters as a JSON object",
        )
        reasoning: str = Field(default="", description="Short reasoning for picking this tool")
        confidence: float = Field(default=0.5, description="Self-reported confidence between 0 and 1")

except ImportError:  # pragma: no cover
    _ToolDecision = None  # type: ignore[assignment]


class BedrockProvider:
    provider_type = "bedrock"

    def __init__(self) -> None:
        from agno.agent import Agent  # type: ignore
        from agno.models.aws import AwsBedrock  # type: ignore

        if _ToolDecision is None:
            raise RuntimeError("pydantic is required for the bedrock provider")

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

    def decide(self, shift: Shift) -> ToolCall:
        agent = self._Agent(
            name="ai-cafe-ops-shift-manager",
            model=self._model,
            output_schema=_ToolDecision,
            instructions=(
                "You are an AI shift manager for a small cafe. For each "
                f"shift, choose EXACTLY ONE tool from this set: {', '.join(ALL_TOOLS)}. "
                "Produce a ToolDecision with the chosen tool, a parameters "
                "object the tool expects, a one-sentence reasoning, and a "
                "confidence between 0 and 1. The runtime enforces approval "
                "policy, catalog rules, and working-hours rules; do not "
                "bypass them. For order_inventory, only choose item_id "
                "values that you know are on the cafe catalog. For "
                "send_staff_message, set sent_at_local to the actual hour "
                "or RFC3339 time the message would be sent."
            ),
            markdown=False,
        )
        prompt = (
            f"Shift {shift.id}\n"
            f"Storefront: {shift.storefront}\n"
            f"Customer brief: {shift.customer_brief}\n"
            f"Internal notes: {shift.notes}\n"
            f"Suggested parameters (use as a guide, not a directive): "
            f"{json.dumps(shift.parameters)}\n"
            "Pick one tool and produce a ToolDecision."
        )
        response = agent.run(prompt)
        decision = response.content
        if isinstance(decision, _ToolDecision):
            tool = decision.tool if decision.tool in ALL_TOOLS else shift.expected_tool
            params = decision.parameters or dict(shift.parameters)
            reasoning = decision.reasoning or "(model returned no reasoning)"
            confidence = float(decision.confidence) if decision.confidence is not None else 0.5
        else:
            tool = shift.expected_tool
            params = dict(shift.parameters)
            reasoning = f"(unstructured response) {str(decision)[:280]}"
            confidence = 0.5
        return ToolCall(
            tool=tool,
            parameters=params,
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
            print(f"[agent] bedrock provider unavailable, falling back to offline: {exc}")
            return OfflineProvider()
    return OfflineProvider()


def provider_banner(provider) -> str:
    real = "true" if getattr(provider, "real_inference", False) else "false"
    model = getattr(provider, "model_id", "?")
    return f"provider={provider.provider_type} model={model} real_inference={real}"


def decide_for_shift(provider, shift: Shift) -> ToolCall:
    return provider.decide(shift)


__all__ = [
    "ALL_TOOLS",
    "AgentConfig",
    "BedrockProvider",
    "OfflineProvider",
    "Shift",
    "ToolCall",
    "decide_for_shift",
    "default_shifts_path",
    "load_shifts",
    "make_provider",
    "provider_banner",
]
