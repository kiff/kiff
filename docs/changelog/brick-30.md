# Brick 30 - Bound what an actor may do in total

Brick 30 adds `pkg/kiff/limit`: a ceiling on what one actor may do across
actions, as distinct from whether any one action is allowed.

Every check before this one answers a question about a single action — the
state allows it, the actor holds the permission, the parameters are valid, a
human signs for the risky one. Each is right every time it runs, and none can
answer what the actor has already done today.

The gap is structural rather than an oversight. A per-call check sees one call,
so ten correct refunds is ten correct decisions and one bad day. A bound on a
sequence has to live where the sequence is visible, which is why this is a
ledger and not a predicate.

## What Was Added

- `limit.Limit` — a subject, the actions it covers, and one or more ceilings,
  each counting either the number of actions or the sum of one numeric
  parameter, over a calendar day or a rolling hour or day.
- `limit.Ledger` — the storage seam, with `MemoryLedger` for a single process.
- `runtime.Config.Limits` / `LimitLedger` — off by default; a runtime with
  neither behaves exactly as before.
- `outcome.ReasonLimitReached` and `ReasonLimitExpired`, so a working control
  never reads as a generic error.
- `AUTO_REFUND` and a daily ceiling in the refund scenario, so
  `kiff new -scenario refund` demonstrates the refusal a per-call check cannot
  make.

## The Two Rules That Matter

**A limit constrains; it never confers.** An actor with no limit is unaffected,
so a first limit cannot break a running system and limits can be adopted one
actor at a time.

**An expired or revoked limit refuses.** The obvious implementation filters to
the limits in force and reads the empty set as "nothing applies, so allow" —
which makes revoking a limit the act that removes the bound. `Limits.ForSubject`
is documented to return revoked limits, `Evaluate` reports `OutcomeExpired`, and
a test fails if either changes.

Also enforced: a retry carrying the same request id does not draw twice; an
unreadable ledger refuses rather than allowing on an unknown balance; the draw
is recorded at authorization, because the boundary is told whether an action may
run and never told whether it did.

## Scope

`MemoryLedger` is correct for one process and only that. Two replicas each hold
their own ledger and each enforces the full ceiling, so the real total is the
sum. Implement `limit.Ledger` against shared storage before scaling out, and
serialize the read-modify-write the way the in-memory mutex does — otherwise two
concurrent authorizations read the same remaining balance and both proceed.

Wiring `Limits` without `LimitLedger` is refused at construction: a limits
source with no ledger cannot tell what has been drawn, and would allow every
action while appearing to bound them.
