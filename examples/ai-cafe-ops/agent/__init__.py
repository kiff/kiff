"""Agent package for the ai-cafe-ops demo.

One AI shift manager, four secondary tools, five shifts, five outcomes:

  - START_SHIFT happens at seed time so the agent always picks one
    secondary tool per shift. The four secondary tools are:
      order_inventory, request_specialty, send_staff_message,
      escalate_supplier.
  - The offline fixture is engineered so the 5-shift batch produces
    exactly: auto_executed, approval_required (granted -> executed),
    blocked_not_in_catalog, blocked_after_hours, escalated.
  - Bedrock provider is identical to refund-agno's: lazy import, AWS
    creds via env, structured output via pydantic. KIFF behavior is the
    same regardless of provider.

The example was inspired by the Andon Cafe experiment: an AI manager
that confidently issued operational actions which did not make sense.
The runtime boundary that decides which actions execute, wait, block
or escalate is the layer the experiment was missing. That layer is KIFF.
"""
