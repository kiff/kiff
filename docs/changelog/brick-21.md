# Brick 21 - Breadth Demo: support-ops + kiff timeline

Brick 21 adds the second end-to-end agent demo and a small operator CLI. Where Brick 20 shows depth (one tool, two runs, governed vs unguarded), Brick 21 shows breadth (one agent, five tools, five distinct outcomes in a single run) and gives operators a way to inspect any KIFF server's audit trail without writing curl scripts.

## What Was Added

- `examples/support-ops/` package containing:
  - `domain.go` / `domain_test.go` — six action contracts and a state machine covering an L1 support ticket lifecycle. Includes `Domain.NeedsApprovalForRefund`, which gates `ISSUE_REFUND` based on amount plus a cumulative cap. Tests cover one outcome per action plus the cumulative-cap case.
  - `server/main.go` / `proposal.go` / `server_test.go` — `net/http` host with one `/demo/agent/decide` route. The server routes tools to contracts, pre-checks consent on `send_outreach` *before* opening any approval, and seeds five tickets (ticket-5 pre-resolved so `close` is a valid action). Five integration tests cover the five distinct outcomes.
  - `agent/agent.py` — same offline + bedrock pattern as `refund-agno`.
  - `agent/run_with_kiff.py` — five-ticket runner: decide, auto-grant the first pending approval, deny the rest, then print the audit summary table.
  - `tickets.json` — five deterministic tickets engineered to hit all five outcomes.
  - `scripts/demo.sh` and `Makefile` matching `refund-agno`.
  - `README.md` — canonical offline 5-row outcome table plus Bedrock notes.

- `cmd/kiff/timeline.go` (+ test) — a new subcommand:

  ```bash
  kiff timeline -base http://localhost:8080 -entity ticket-1
  ```

  Hits `/entities/{id}/timeline` and `/demo/rebuild` (when present) on any KIFF server and renders a compact table with the full audit trail and a rebuild-check footer:

  ```text
  time         kind                   actor       summary
  ----------------------------------------------------------------
  01:48:16.782 event_ingested         system      event ingested [TICKET_OPENED]
  ...
  rebuild: materialized="TRIAGED" replayed="TRIAGED" events=2 ✓
  ```

  Tests cover round-trip parsing for both endpoints, the not-found path, the summary fallback, and the truncate helper.

## Five Outcomes in One Run

`support-ops` is engineered so a single agent run produces five distinct outcomes back-to-back:

| Ticket | Tool | Outcome | Reason |
|---|---|---|---|
| 1 | `issue_refund` | executed | small amount, no approval needed |
| 2 | `issue_refund` | approval_required → granted → executed | over the cap, granted on review |
| 3 | `send_outreach` | blocked_consent_missing | structural rejection *before* any approval is opened |
| 4 | `escalate_to_human` | executed | escalation never needs approval |
| 5 | `close_ticket` | executed | only legal in `RESOLVED`; ticket-5 was pre-resolved to make this case visible |

The five-row summary at the end of `make demo` is the entire breadth pitch in one screen.

## Why

Brick 20 proved one tool flowing through KIFF with two visible paths. Brick 21 proves the same governance applies cleanly across a heterogeneous tool surface, including a custom validator hook (`SEND_OUTREACH` rejecting on missing consent before opening any approval) and a state-gated action (`CLOSE_TICKET` only legal from `RESOLVED`).

`kiff timeline` exists because every other demo, every starter project, and every future hosted runtime instance needs the same operational view. Hand-rolling curl scripts to inspect audit trails is friction. One subcommand removes it.

## Pattern

Brick 21 establishes the second pattern every future agent example should consider:

1. **One run, many outcomes.** Engineer the fixture so a single demo invocation hits every governance path. The five-row outcome table is the artifact you put in the README.
2. **Pre-checks before approvals.** When a structural condition (consent, eligibility, identity) makes an action invalid regardless of authority, reject it before opening an approval. Approvals are for *authority decisions*, not *eligibility checks*.
3. **One operator CLI for every server.** Anything that runs the `httpapi` handler should be inspectable with `kiff timeline`. If it isn't, the server is missing the route, not the CLI.

## Limitations

- The cumulative refund cap is per-process (in-memory). It resets when the server restarts. Production deployments would back this with the same persistence layer the audit store uses.
- The agent is still single-turn. Multi-turn workflows that span tickets (e.g., "the customer said yes, now run X") are out of scope.
- `kiff timeline` is read-only. It does not grant or deny approvals. Approving from a CLI is a separate concern with its own credential and confirmation requirements.
