# Philosophy

KIFF has a point of view. This page captures it in one place so the framework, the docs, and anything built on top of it stay coherent.

## The one-line version

> KIFF is trust infrastructure for AI-operated systems.

Most AI backends start at the prompt. They collapse in production because nothing controls what the agent actually does to your state. KIFF starts earlier, at the layer where humans, agents, and software coordinate around shared operational truth.

## The promise

You can turn messy human or agent requests into controlled, traceable action without rebuilding your whole system.

## What KIFF chooses to be

**Simple.** You should not need to understand the entire framework before getting a result. The core loop is six words long: event, state, decision, action, approval, audit.

**Opinionated.** There is a normal way to do things. Domain vocabulary lives in your code; coordination mechanics live in `pkg/kiff`. Actions are contracts, not free-form calls. Audit is part of the protocol, not a logging afterthought. These are decisions you do not have to relitigate.

**Fast.** Fast in performance because Go. Fast in time-to-first-result because the demo runs in seconds and the loop is visible from the first line of output.

**Complete.** More than a single tool: a whole way to build a runtime control plane, with events in, state forward, decisions explainable, actions validated, approvals enforced, executions audited, and replay possible.

**Coherent.** Every package is shaped by the same philosophy. The same trust boundary that protects approvals also protects executors and audit. You will not find one corner of the framework that contradicts another.

**Readable.** A developer landing on a KIFF codebase should be able to read a domain file top to bottom and understand the intent. Plain Go. No reflection magic. No DSL. No surprise.

**Productive.** The point is to get from idea to a working governed backend in an afternoon, not a quarter.

**Elegant.** Plumbing is invisible. The interesting code is the domain. KIFF carries the boring parts so the developer feels creative, not buried.

**Default-driven.** The starter, the conventions, the file-backed stores, the HTTP API: KIFF gives you "the normal way" so you do not have to invent one before writing your first action.

**Demoable.** KIFF is valuable when it can stop an action as clearly as it can execute one. The demos are designed to make that visible in under two minutes.

**Narrative-friendly.** KIFF has a story: governed agentic backends, built quickly, with less pain. That story is the same in the README, in talks, in code comments, and in commit messages.

**Empowering.** A small team should feel capable of shipping a serious operational system on KIFF. The framework is sized for that, not for an enterprise platform team.

**Anti-bloat.** KIFF arrives as a rebellion against heavy, slow, over-configured systems. Its enemy is complexity. When in doubt, the smaller idiomatic Go design wins.

## What KIFF chooses not to be

KIFF stays out of several roles by design. It holds no chatbot layer, no generic web framework, no LLM wrapper, and no ambition to replace a workflow engine. It avoids becoming a universal business ontology, and the MIT license keeps it from becoming a vendor lock-in.

If your application only needs CRUD, a router, or direct LLM tool calls with no governed state, KIFF is too much structure. Use something smaller and ship.

## How adoption should feel

The feeling we want a developer to have on first read:

> Finally, someone organized the chaos for me.

Not dread at learning another framework or memorizing 200 abstractions, but relief that the obvious-in-hindsight skeleton already exists, written in idiomatic Go, with tests, a runnable demo, and a clear path from "first event" to "first audited execution."

## The principles, distilled

These are restated from the vision, kept here because they belong on the same page as the adjectives above.

1. Normalize mechanics, not semantics.
2. Domains define their own business vocabulary.
3. KIFF provides reusable coordination primitives.
4. State comes before action.
5. Actions are explicit contracts, not free-form tool calls.
6. Agents may propose actions, but KIFF validates them before execution.
7. High-risk actions require human authority.
8. Every important fact is auditable. Trust comes from reconstruction.
9. Prefer boring, idiomatic Go over clever abstractions.
10. Keep the framework small and composable.
11. Do not add external dependencies without a strong reason.
12. Always include focused tests for new behavior.

## Adjectives that describe a system built on KIFF

Controlled. Traceable. Adoptable. Low-friction. Incremental. Auditable. Human-approved. System-friendly. Non-invasive. Replayable. Practical. Composable. Safe. Operational.

If something we ship makes one of those words less true, that is a signal we got the design wrong.
