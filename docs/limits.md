# Limits: what an actor may do in total

Every other check in KIFF answers a question about **one** action. Is the
entity in a state that allows it? Does this actor hold the permission? Are
the parameters valid? Does a human need to sign?

Each is right every time it runs. None of them can answer the question this
page is about: **what has this actor already done today, and is that enough?**

## Why per-call checking cannot get there

This is structural, not an oversight. A per-call check sees one call. Ten
correct refunds is ten correct decisions and one bad day.

```text
a limit: refund-agent may refund €1,100 per calendar day

  AUTO_REFUND  €42     ✓ ALLOWED   €1,058 left
  AUTO_REFUND  €990    ✓ ALLOWED   €68 left
  AUTO_REFUND  €88     ✗ REFUSED   limit_reached
```

The third refund is in the right state, with the right permission, valid
parameters, under the approval threshold, and **smaller than the two that just
went through**. Nothing is wrong with it. The day is spent.

No amount of checking that one call produces that answer, because the answer
is about the other two.

## What a limit is not

- **Not a rate limit.** A rate limit protects your infrastructure from volume.
  A limit bounds consequence, in the unit the action is measured in.
- **Not a second opinion on one action.** It runs last, after everything else
  has already said yes, and it refuses actions that are correct in every other
  way.
- **Not an approval.** At the ceiling the action is refused, not routed to a
  person. A cap that produces a request someone clicks through at the end of a
  long day is not a cap. More room is a raised limit, which is a deliberate act
  that leaves a record.

## Wiring one

```go
import "github.com/kiff/kiff/pkg/kiff/limit"

rt, err := runtime.New(runtime.Config{
    Domain: &definition,
    Limits: runtime.StaticLimits{{
        ID:      "refund-agent-daily",
        Subject: "refund-agent",          // must match actor.Actor.ID
        Actions: []string{"AUTO_REFUND"}, // empty covers every action
        Aggregates: []limit.Aggregate{{
            Quantity: limit.Quantity{Parameter: "amount"},
            Max:      110000,
            Window:   limit.WindowCalendarDay,
        }},
    }},
    LimitLedger: limit.NewMemoryLedger(),
})
```

`Limits` and `LimitLedger` must be set together. A limits source with no ledger
cannot tell what has been drawn and would allow every action while appearing to
bound them, so `runtime.New` refuses that combination rather than half-enabling
the check.

A runtime with neither behaves exactly as it did before. Limits are something a
domain opts into.

### Quantities

Two, and only two:

| Quantity | Counts |
| --- | --- |
| `limit.Quantity{}` | how many times the actions run |
| `limit.Quantity{Parameter: "amount"}` | the sum of that parameter across them |

Counting needs no parameter at all, which is how you bound something with no
currency — three rollbacks an hour, ten account closures a day.

A quantity that needed arithmetic to describe would be one nobody can predict
the behaviour of, and a limit nobody can predict is not a control.

### Windows

`WindowCalendarDay` (midnight in the limit's `Location`), `WindowRolling24h`,
`WindowRolling1h`. The calendar day is the one to reach for: it is what a person
means by "a day" when they say what an agent may spend in one.

## Five properties worth knowing before you rely on it

**A limit constrains; it never confers.** An actor with no limit is unaffected.
This is the opposite of a capability system, and it is why you can adopt limits
one actor at a time without the first one breaking something already running.

**Several limits can cover one action, and the tightest binds.** Each is
checked; the first refusal decides. There is no precedence to reason about and
no way to gain authority by adding a limit.

**A revoked limit refuses.** It does not vanish and leave the actor unbounded.
The obvious implementation filters to the limits currently in force and reads
the empty set as "nothing applies, so allow" — which makes revoking a limit the
act that removes the bound. `Limits.ForSubject` must return revoked and expired
limits too, and the interface says so.

**An unreadable ledger refuses.** Allowing on a balance nobody could read is
exactly what a limit exists to prevent. `limit.ErrUsageUnavailable` travels out
so a caller can refuse deliberately rather than by accident.

**The draw is recorded at authorization, not at execution.** The boundary is
told whether an action may run and is never told whether it did. An
authorization your code chose not to execute still counts. A retry carrying the
same request id does not draw twice.

## One process, or more than one

`limit.MemoryLedger` is correct for a single process **and only that**. Two
replicas each hold their own ledger, each enforces the full ceiling, and the
real total is the sum: an agent in three replicas can spend three times the
limit.

That is not a bug to be fixed in the ledger. A bound on a sequence has to live
where the whole sequence is visible, and for more than one process that means
shared storage.

Implement `limit.Ledger` against your database when you scale out. Two things
your implementation must get right, both of which `MemoryLedger` gets from its
mutex:

1. **Serialize the read-modify-write.** `Used` then `Record` is a critical
   section per (limit, subject). Without it, two concurrent authorizations both
   read the same remaining balance and both proceed.
2. **Make `Record` idempotent per request id**, so a retry replaces its earlier
   draw instead of adding to it.

[KIFF Cloud](https://kiff.dev/docs/limits) is that shared ledger, with the
statement surface and a management credential separating who may act from who
sets how much. The interface here is the same shape, so moving is a wiring
change.

## Reading what was drawn

```go
statement, err := limit.StatementFor(ctx, theLimit, ledger, time.Now().UTC())
// [] {Quantity: "sum(amount)", Max: 110000, Used: 103200, Remaining: 6800, ...}
```

`StatementFor` returns an error rather than zero usage when the ledger cannot be
read. An unused limit and an unreadable one are very different facts, and
showing the second as the first is the failure the whole package is shaped to
avoid.
