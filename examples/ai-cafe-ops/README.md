# ai-cafe-ops — KIFF puts a runtime boundary between AI proposals and operational actions

This is the operational-authority breadth demo for KIFF. One AI shift
manager picks among four tools on a 5-shift batch in a small café.
KIFF gates each tool with a different rule, and the run produces five
distinct outcomes in a single table.

The example was inspired by the
[Andon Café experiment](https://www.lesswrong.com/posts/4Z2D2cPtwTqRn8j8B/anthropic-s-ai-cafe-experiment-in-stockholm-the-results-are):
an AI-managed café whose AI manager confidently issued operational
actions that did not make sense — oversized supply orders, after-hours
staff messages, off-catalog requests. The failure mode was not "model
quality." It was "no runtime boundary between proposed and executed."
That boundary is exactly what KIFF is.

`refund-agno` answers a buyer's first objection ("yes, but I don't
just refund"). `support-ops` answers the second ("yes, but I have N
tools"). This example answers the third: **"yes, but my AI is going
to be doing things in the real world."**

## What the agent can do

| Tool                | KIFF action                              | Governance shape                                                                                                                                                                                                          |
| ------------------- | ---------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `order_inventory`   | `AUTO_ORDER_INVENTORY` or `ORDER_INVENTORY` | Routed by the server. Auto when `amount_cents <= 5000` AND the shift's running daily total stays under 20 000. Otherwise approval required.                                                                                |
| `request_specialty` | `REQUEST_SPECIALTY`                      | Custom validator on the contract. Rejects with `blocked_not_in_catalog` unless `parameters.item_id` is in the café's allow-list. The check runs **before** any approval is opened.                                          |
| `send_staff_message`| `SEND_STAFF_MESSAGE`                     | Custom validator on the contract. Rejects with `blocked_after_hours` unless `parameters.sent_at_local` is inside the working-hours window (07:00 – 22:00 by default). Approval is still required when the time is OK.       |
| `escalate_supplier` | `ESCALATE_SUPPLIER`                      | No approval. Always allowed in `OPEN`. The shift moves to `AWAITING_HUMAN` so an operator can pick it up.                                                                                                                  |

Four tools, one runtime, one agent. KIFF's contract surface and the
`runtime.ApplyApproval` boundary are unchanged from the refund-agno
demo; the breadth lives in the contracts and a tiny routing helper
in the demo server.

## Quickstart

```bash
cd examples/ai-cafe-ops
make demo            # alias for make demo-offline (deterministic, no AWS)
```

`make demo` will:

1. compile `bin/ai-cafe-ops-server`
2. start it on a free port
3. seed five shifts (all in `OPEN` after `START_SHIFT` runs at seed time)
4. run the agent over all five shifts
5. auto-grant the single pending approval and retry it
6. print the audit timeline, the rebuild check for each shift, and a
   final summary table
7. shut the server down

To run with **real Agno + AwsBedrock inference**:

```bash
# Provide credentials in kiff-framework/.env (preferred — shared across
# examples) or examples/ai-cafe-ops/agent/.env. Required keys:
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
================================================================
  ai-cafe-ops demo: provider=offline
================================================================
[run-with-kiff] provider=offline model=offline-fixture real_inference=false ...

[shift shift-1] tool=order_inventory      KIFF outcome: executed                action=AUTO_ORDER_INVENTORY
[shift shift-2] tool=order_inventory      KIFF outcome: approval_required       action=ORDER_INVENTORY
                                          reason: amount 9900 > single-order ceiling 5000
[shift shift-3] tool=request_specialty    KIFF outcome: blocked_not_in_catalog  action=REQUEST_SPECIALTY
                                          reason: item is not in the café catalog: "yuzu_concentrate"
[shift shift-4] tool=send_staff_message   KIFF outcome: blocked_after_hours     action=SEND_STAFF_MESSAGE
                                          reason: hour 02 is outside 07:00–22:00
[shift shift-5] tool=escalate_supplier    KIFF outcome: executed                action=ESCALATE_SUPPLIER

================================================================
  Operator review
================================================================
  - pending: approval-shift-2-1 (ORDER_INVENTORY on shift-2)
[run-with-kiff] granted approval-shift-2-1 -> retrying shift-2
[shift shift-2] tool=order_inventory      KIFF outcome: executed                action=ORDER_INVENTORY

================================================================
  Audit timeline + rebuild check
================================================================
  rebuild(shift-1): materialized='OPEN'           replayed='OPEN'           events=3 OK
  rebuild(shift-2): materialized='OPEN'           replayed='OPEN'           events=3 OK
  rebuild(shift-3): materialized='OPEN'           replayed='OPEN'           events=2 OK
  rebuild(shift-4): materialized='OPEN'           replayed='OPEN'           events=2 OK
  rebuild(shift-5): materialized='AWAITING_HUMAN' replayed='AWAITING_HUMAN' events=3 OK

================================================================
  Summary (one row per shift, first outcome)
================================================================
shift      tool                   first outcome                 reason / final state
------------------------------------------------------------------------------------------------
shift-1    order_inventory        executed                      no approval needed (OPEN)
shift-2    order_inventory        approval_required             granted->executed (OPEN)
shift-3    request_specialty      blocked_not_in_catalog        catalog rejection (OPEN)
shift-4    send_staff_message     blocked_after_hours           outside working hours (OPEN)
shift-5    escalate_supplier      executed                      no approval needed (AWAITING_HUMAN)
```

The offline demo produces these five outcomes deterministically. The
Bedrock demo produces the same five outcomes, with the model's own
reasoning text and confidence values, because the gating rules are
deterministic and depend only on amount, state, catalog membership,
and working-hours window — not on the model's words.

## Bedrock demo

```bash
make demo-bedrock
```

Reasoning text and confidence vary per run, by definition. KIFF's
runtime behavior is stable. A representative excerpt with
`BEDROCK_MODEL_ID=moonshotai.kimi-k2.5`:

```text
[run-with-kiff] provider=bedrock model=moonshotai.kimi-k2.5 real_inference=true ...

[shift shift-2] tool=order_inventory
    proposal source : bedrock:moonshotai.kimi-k2.5
    agent reasoning : The request is for a bulk inventory order of napkins to
                      prepare for tomorrow's festival surge. The order_inventory
                      tool is appropriate for standard catalog items, and the
                      approval requirement noted in internal notes will be
                      handled by the runtime's approval policy enforcement
                      rather than tool selection.
    agent confidence: 0.85
    KIFF outcome    : approval_required           action=ORDER_INVENTORY

[shift shift-4] tool=send_staff_message
    proposal source : bedrock:moonshotai.kimi-k2.5
    agent reasoning : The customer brief indicates this is a handoff note for
                      the barista team about preparation for an early rush,
                      which is a legitimate staff communication need despite
                      the working hours note; the runtime approval policy will
                      handle the outside-hours validation.
    agent confidence: 0.85
    KIFF outcome    : blocked_after_hours         action=SEND_STAFF_MESSAGE
    KIFF reason     : hour 02 is outside 07:00–22:00
```

Proof points to look for in your own Bedrock run:

- Header reads `provider=bedrock model=<your-model-id> real_inference=true`.
- Each shift prints `proposal source : bedrock:<your-model-id>`.
- Reasoning text is multi-sentence and confidence is not always `0.78`
  (the offline fixture's constant).
- The model **may try to reason around guards** — e.g. shift-4 above,
  where the model argued that "the runtime approval policy will handle
  the outside-hours validation." KIFF blocked the action anyway,
  because validation is structural, not negotiated.
- The five outcomes table still shows: `executed`, `approval_required`
  (then `executed` after grant), `blocked_not_in_catalog`,
  `blocked_after_hours`, `executed` (escalation parking the shift in
  `AWAITING_HUMAN`).
- Every rebuild check passes.

## What KIFF blocked, and why

| Shift | Tool                   | KIFF action chosen           | Reason it was gated                                                  | Final state          |
| ----- | ---------------------- | ---------------------------- | -------------------------------------------------------------------- | -------------------- |
| 1     | `order_inventory`      | `AUTO_ORDER_INVENTORY`       | under the per-call ceiling and the daily cap                         | `OPEN`               |
| 2     | `order_inventory`      | `ORDER_INVENTORY`            | high-risk action; approval required                                  | `OPEN` (granted)     |
| 3     | `request_specialty`    | `REQUEST_SPECIALTY`          | item not in the café catalog; rejected before approval was opened    | `OPEN`               |
| 4     | `send_staff_message`   | `SEND_STAFF_MESSAGE`         | proposal at 02:14 is outside the working-hours window                | `OPEN`               |
| 5     | `escalate_supplier`    | `ESCALATE_SUPPLIER`          | always allowed; parks the shift awaiting a human ops review          | `AWAITING_HUMAN`     |

The thresholds and the catalog live in the demo server (the routing
function `NeedsApprovalForOrder`, the `CheckCatalog` helper, the
`CheckWorkingHours` helper). KIFF's `ApprovalRequirement` is binary;
the contract declares authority shape, the routing layer and the
custom validators decide which contract a particular agent intent
lands on. This separation is the right production pattern even if you
eventually push policy into the framework.

## Inspect the audit trail with `kiff timeline`

The framework CLI ships a small `kiff timeline` subcommand that calls
the running server's `/entities/{id}/timeline` (and `/demo/rebuild`,
when present) and renders a compact table.

```bash
go install github.com/kiffhq/kiff/cmd/kiff
make demo-offline           # leave the server running, or use make demo-bedrock
kiff timeline -base http://localhost:<port> -entity shift-2
```

(See `cmd/kiff/timeline.go` for the implementation. The subcommand
lives in the framework, not in any single example, so any KIFF-based
project that runs `httpapi.NewHandler` gets it.)

## Layout

```
examples/ai-cafe-ops/
├── domain.go              # six contracts (incl. START_SHIFT), state machine, routing helper
├── domain_test.go         # one test per action (auto, approval, denied, catalog, after-hours, escalate, routing)
├── server/
│   ├── main.go            # net/http host + the demo-only routes
│   ├── proposal.go        # ToolCall -> KIFF action proposal
│   └── server_test.go     # integration tests for each outcome
├── agent/
│   ├── agent.py           # Agno-shaped agent + offline & bedrock providers
│   ├── run_with_kiff.py   # 5-shift runner, auto-grant + audit
│   ├── shifts.json        # five deterministic shifts
│   ├── requirements.txt
│   └── .env.example
├── scripts/
│   └── demo.sh            # spawn server, run agent, shut down
├── Makefile               # demo / demo-offline / demo-bedrock / test / build / clean
└── README.md
```

## Why this is the right operational-authority demo

- **The failure modes are recognizable.** A 6,000-napkin order, an
  after-hours staff message, an off-catalog ingredient request — these
  are exactly the kinds of confident proposals an AI manager has been
  observed to make. The KIFF runtime sits between the proposal and the
  consequence.
- **The catalog and working-hours examples prove custom validators
  compose with approvals.** A buyer who has been worried about
  governance over AI-driven actions in their own product sees the
  structural answer.
- **The state-machine example proves KIFF's no-bypass guarantee.** The
  agent's `escalate_supplier` only works in `OPEN`; the state machine
  refuses the rest with a stable `state_not_allowed` outcome the
  agent's calling layer can react to.
- **The framework core is unchanged.** `pkg/kiff/*` is identical to
  what shipped with the refund and support demos; the breadth lives in
  contracts and a tiny routing function in the demo server.

## See also

- [`examples/refund-agno/`](../refund-agno/) — the depth demo (one
  tool, two runs).
- [`examples/support-ops/`](../support-ops/) — the breadth demo for
  customer support (one agent, five tools, five outcomes).
- [`docs/principles/02-actions-are-contracts.md`](../../docs/principles/02-actions-are-contracts.md) —
  why actions are first-class contracts and not free-form tool calls.
- [`docs/principles/04-approval-is-runtime-controlled.md`](../../docs/principles/04-approval-is-runtime-controlled.md) —
  the trust boundary every example exercises.
