"""Agent package for the support-ops demo.

One agent, five tools, five outcomes:

  - TRIAGE_TICKET happens at seed time so the agent always picks one
    secondary tool per ticket. The five secondary tools are:
      issue_refund, waive_fee, send_outreach, escalate_to_human, close_ticket
  - The offline fixture is engineered so the 5-ticket batch produces
    exactly: auto_executed, approval_required, blocked_consent_missing,
    escalated, closed.
  - Bedrock provider is identical to refund-agno's: lazy import, AWS
    creds via env, structured output via pydantic. KIFF behavior is the
    same regardless of provider.
"""
