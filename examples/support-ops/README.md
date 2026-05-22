# support-ops — KIFF scales governance per action, not per agent

This is the breadth demo for KIFF. One Agno-shaped support-ops agent
picks among five tools on a 5-ticket batch. KIFF gates each tool with a
different rule, and the run produces five distinct outcomes in a single
table.

The buyer's natural objection after the headline refund demo —
"yes, but I have N tools" — is the question this example answers.

## What the agent can do

| Tool                | KIFF action       | Governance shape                                                                |
| ------------------- | ----------------- | ------------------------------------------------------------------------------- |
| `issue_refund`      | `AUTO_REFUND` or  `ISSUE_REFUND` | Routed by the server: `AUTO_REFUND` when `amount_cents <= 5000` AND the ticket's running refund total stays under 20 000; otherwise `ISSUE_REFUND` (approval required). |
| `waive_fee`         | `WAIVE_FEE`       | Approval required, every call, every amount.                                    |
| `send_outreach`     | `SEND_OUTREACH`   | Custom validator hook on the contract. Rejects with `blocked_consent_missing` unless `parameters.consent_verified == true`. The check runs **before** any approval is opened, so consent failures never reach human review. Approval is still required when consent is OK. |
| `escalate_to_human` | `ESCALATE_TO_HUMAN` | No approval. Allowed in `NEW` and `TRIAGED`. Always available; escalation is its own answer. |
| `close_ticket`      | `CLOSE_TICKET`    | No approval, but only allowed in `RESOLVED`. The state machine refuses it elsewhere with `state_not_allowed`. |

Five rules, one runtime, one agent. KIFF's contract surface and the
`runtime.ApplyApproval` boundary are unchanged from the refund-agno
demo; the breadth all lives in the contracts and a tiny routing helper
in the demo server.

## Quickstart

```bash
cd examples/support-ops
make demo            # alias for make demo-offline (deterministic, no AWS)
```

`make demo` will:

1. compile `bin/support-ops-server`
2. start it on a free port
3. seed five tickets (one is pre-resolved so `close_ticket` is valid)
4. run the agent over all five tickets
5. auto-grant the first pending approval, deny the rest, retry
6. print the audit timeline, the rebuild check for each ticket, and a
   final summary table
7. shut the server down

To run with **real Agno + AwsBedrock inference**:

```bash
# Provide credentials in kiff-framework/.env (preferred — shared across
# examples) or examples/support-ops/agent/.env. Required keys:
#
#   AGNO_MODEL_PROVIDER=bedrock
#   AWS_REGION=<your-region>
#   BEDROCK_MODEL_ID=<a-bedrock-model-id>
#   AWS_ACCESS_KEY_ID=<...>
#   AWS_SECRET_ACCESS_KEY=<...>
#
# Then:
make demo-bedrock
```

## Sample output (offline)

The deterministic offline run produces exactly these five outcomes —
that is the breadth message in one screen.

```text
========================================================================
  RUN — same agent, five tickets, through KIFF
========================================================================
[run-with-kiff] provider=offline model=offline-fixture real_inference=false ...

[ticket ticket-1] tool=issue_refund      KIFF outcome: executed                action=AUTO_REFUND
[ticket ticket-2] tool=issue_refund      KIFF outcome: approval_required       action=ISSUE_REFUND
                                         reason: amount 9900 > single-refund ceiling 5000
[ticket ticket-3] tool=send_outreach     KIFF outcome: blocked_consent_missing action=SEND_OUTREACH
                                         reason: outreach blocked, consent_verified must be true
[ticket ticket-4] tool=escalate_to_human KIFF outcome: executed                action=ESCALATE_TO_HUMAN
[ticket ticket-5] tool=close_ticket      KIFF outcome: executed                action=CLOSE_TICKET

========================================================================
  Operator review
========================================================================
  - pending: approval-ticket-2-1 (ISSUE_REFUND on ticket-2)
[run-with-kiff] granted approval-ticket-2-1 → retrying ticket-2
[ticket ticket-2] tool=issue_refund      KIFF outcome: executed                action=ISSUE_REFUND

========================================================================
  Audit timeline + rebuild check
========================================================================
  rebuild(ticket-1): materialized='TRIAGED'        replayed='TRIAGED'        events=3 ✓
  rebuild(ticket-2): materialized='TRIAGED'        replayed='TRIAGED'        events=3 ✓
  rebuild(ticket-3): materialized='TRIAGED'        replayed='TRIAGED'        events=2 ✓
  rebuild(ticket-4): materialized='AWAITING_HUMAN' replayed='AWAITING_HUMAN' events=3 ✓
  rebuild(ticket-5): materialized='CLOSED'         replayed='CLOSED'         events=4 ✓

========================================================================
  Summary
========================================================================
ticket     tool                 first outcome              reason / final state
------------------------------------------------------------------------------
ticket-1   issue_refund         executed                   no approval needed (TRIAGED)
ticket-2   issue_refund         approval_required          granted→executed (TRIAGED)
ticket-3   send_outreach        blocked_consent_missing    consent_verified must be true (TRIAGED)
ticket-4   escalate_to_human    executed                   no approval needed (AWAITING_HUMAN)
ticket-5   close_ticket         executed                   no approval needed (CLOSED)
```

The offline demo produces these five outcomes deterministically. The
Bedrock demo produces the same five outcomes, with the model's own
reasoning text and confidence values, because the gating rules are
deterministic and depend only on amount, state, and consent — not on
the model's words.

## Bedrock demo (illustrative)

```bash
make demo-bedrock
```

Reasoning text and confidence vary per run. KIFF's runtime behavior is
stable. Look for:

- Header reads `provider=bedrock model=<your-model-id> real_inference=true`.
- Each ticket prints `proposal source : bedrock:<your-model-id>`.
- The five outcomes table still shows: `executed`, `approval_required`
  (then `executed` after grant), `blocked_consent_missing`, `executed`
  (escalation), `executed` (close).
- Every rebuild check passes.

## Inspect the audit trail with `kiff timeline`

The framework CLI ships a small `kiff timeline` subcommand that calls
the running server's `/entities/{id}/timeline` (and `/demo/rebuild`,
when present) and renders a compact table. Useful during demos and
when smoke-testing your own domains.

```bash
go install github.com/kiffhq/kiff/cmd/kiff
make demo-offline           # leave the server running, or use make demo-bedrock
kiff timeline -base http://localhost:<port> -entity ticket-2
```

```
timeline for ticket-2 (NN records)
  time         kind                   actor            summary
  --------------------------------------------------------------------------------
  HH:MM:SS.mmm event_ingested         system           event ingested [TICKET_OPENED]
  HH:MM:SS.mmm action_executed        system           action executed [TRIAGE_TICKET]
  HH:MM:SS.mmm decision_proposed      support-agent    decision proposed
  HH:MM:SS.mmm approval_required      support-agent    approval required [ISSUE_REFUND]
  HH:MM:SS.mmm approval_granted       ops-human        approval granted [ISSUE_REFUND]
  HH:MM:SS.mmm action_executed        support-agent   action executed [ISSUE_REFUND]
  HH:MM:SS.mmm event_ingested         support-agent    event ingested [REFUND_ISSUED]

  rebuild: materialized="TRIAGED" replayed="TRIAGED" events=3 ✓
```

(See `cmd/kiff/timeline.go` for the implementation. The subcommand
lives in the framework, not in any single example, so any KIFF-based
project that runs `httpapi.NewHandler` gets it.)

## Layout

```
examples/support-ops/
├── domain.go              # five contracts, state machine, routing helper
├── domain_test.go         # one test per action (auto, approval, denied, consent, escalate, etc.)
├── server/
│   ├── main.go            # net/http host + the demo-only routes
│   ├── proposal.go        # ToolCall → KIFF action proposal
│   └── server_test.go     # five integration tests covering the five outcomes
├── agent/
│   ├── agent.py           # Agno-shaped agent + offline & bedrock providers
│   ├── run_with_kiff.py   # 5-ticket runner, auto grant + deny + audit
│   ├── tickets.json       # five deterministic tickets
│   ├── requirements.txt
│   └── .env.example
├── scripts/
│   └── demo.sh            # spawn server, run agent, shut down
├── Makefile               # demo / demo-offline / demo-bedrock / test / build / clean
└── README.md
```

## Why this is the right second demo

- **One agent, five governance shapes.** The breadth question is
  answered visually in a single 5-row table.
- **The consent example proves custom validators compose with
  approvals.** A buyer who has been worried about consent or PII flows
  in their own product sees the structural answer.
- **The state-machine example proves KIFF's no-bypass guarantee.** The
  agent's `close_ticket` only works when the ticket is `RESOLVED`; the
  state machine refuses the rest with a stable `state_not_allowed`
  outcome the agent's calling layer can react to.
- **The framework core is unchanged.** `pkg/kiff/*` is identical to
  what shipped with the refund demo; the breadth lives in contracts
  and a tiny routing function in the demo server.

## See also

- [`examples/refund-agno/`](../refund-agno/) — the headline (depth) demo.
- [`docs/principles/02-actions-are-contracts.md`](../../docs/principles/02-actions-are-contracts.md) —
  why actions are first-class contracts and not free-form tool calls.
- [`docs/principles/04-approval-is-runtime-controlled.md`](../../docs/principles/04-approval-is-runtime-controlled.md) —
  the trust boundary every example exercises.
- [`docs/changelog/brick-21.md`](../../docs/changelog/brick-21.md) — the
  brick entry that landed this example and the `kiff timeline` CLI.
