"""Agent package for the refund-agno demo.

Two runners share one agent definition (``agent.py``):

- ``run_no_kiff``: tool calls a mock in-memory DB. Damage happens.
- ``run_with_kiff``: tool calls the KIFF HTTP server. Damage is governed.

The deterministic offline provider in ``agent.py`` produces the same tool
calls every run so ``make demo`` and ``make test`` are reproducible.
Set ``AGNO_MODEL_PROVIDER=bedrock`` (and AWS creds) to swap in real
Claude Haiku 4.5.
"""
