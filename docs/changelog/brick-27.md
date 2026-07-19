# Brick 27 - Cloud-facing CLI: apply and the Tier-1 operator commands

Brick 27 adds the CLI surface that talks to a running KIFF cloud, rather than to a local project on disk. It has two halves: `kiff apply`, which pushes a domain contract to a tenant, and a read-only operator set (`kiff domains`, `kiff runtimes`, `kiff usage`, `kiff keys`) that inspects one. Together they give a `git`/`docker compose`-shaped loop: the domain lives as a versioned `kiff.yaml` in the developer's repo, `kiff apply` reconciles the running version to it, and the operator commands show what the cloud currently holds.

## What Was Added

- `cmd/kiff/apply.go` — `kiff apply`:
  - Reads a local `kiff.yaml`, extracts its top-level `domain:` name (a stdlib line scan — no YAML dependency is added to the module), and derives the URL-safe slug the same way the cloud does.
  - Lists the tenant's domains and chooses `PUT /v1/me/domains/{slug}` (update in place) when the domain exists, or `POST /v1/me/domains` (first apply) otherwise.
  - Renders the cloud's server-side validation: `422` issues are printed per field, `403` surfaces the authoring-role message, `401` hints at a bad token. `-dry-run` prints the plan without writing.

- `cmd/kiff/operate.go` — the Tier-1 read-only operator commands:
  - `kiff domains list` / `kiff domains show <name>` — the tenant's governed domains, and one domain's actions, lifecycle, observed agents, and evidence pointer.
  - `kiff runtimes` — the runtimes currently connected (adapter, mode, last seen).
  - `kiff usage [--domain <name>]` — governed-operation counters for the tenant, or one domain (counter rows are sorted for stable output).
  - `kiff keys list` — the tenant's active API keys (id, label, roles, timestamps — never the secret). `keys create`/`revoke` are deliberately refused here as a Tier-2 (mutating) follow-up.

- Shared, cloud-agnostic plumbing (both files):
  - Endpoint resolution: `-endpoint` flag -> `KIFF_CLOUD_URL` -> `~/.kiff/config` -> a build-time default that is **empty** in this source tree. A distributor sets it with `-ldflags "-X main.defaultCloudEndpoint=https://..."`, so the framework never names a specific hosted instance in source.
  - Token resolution: `-token` -> `KIFF_TOKEN` -> `~/.kiff/credentials`.
  - `-json` on every command for scripting; human-readable tables otherwise. Each command writes to an injected `io.Writer` (main passes `os.Stdout`), so tests capture a buffer instead of the process-global stream.

- `cmd/kiff/apply_test.go`, `cmd/kiff/operate_test.go` — slug parity with the cloud, `domain:` extraction, endpoint/token resolution order, create-vs-update selection, per-command path/bearer/rendering assertions over an `httptest` server, the Tier-2 refusal, and `401`/`403`/`422` rendering.

- `main.go` registers `apply`, `domains`, `runtimes`, `usage`, and `keys`.

## Why

A domain is a versioned artifact in the developer's repo, authored with their own tooling. The CLI is how that artifact reaches a governed runtime and how the developer sees what is running — without opening a dashboard. `kiff apply` is the write path (reconcile the running domain to the file); the operator commands are the read path (what domains, runtimes, usage, and keys does this tenant have?).

## Cloud-agnostic on Purpose

The framework is MIT and must not name a commercial instance. Every command resolves its endpoint from flag, environment, config, or a build-time default that ships empty here — so `kiff/kiff` stays publishable while a packaged build can point at a hosted KIFF. This mirrors how any open-source client targets a configurable server.

## Read vs Write

The operator commands are read-only by construction: each issues a `GET` and never mutates. Mutating operations (`apply`, and key mint/revoke) are separate and, on the cloud side, gated by a management role — a plain runtime key can read but cannot author a domain or manufacture keys. The CLI reflects that split: `keys list` is available, `keys create`/`revoke` are held back as a Tier-2 follow-up.

## Limitations

- `kiff apply` relies on the cloud's server-side validation for correctness; it does not re-run `kiff verify` on the file before pushing.
- The endpoint default is empty in source, so an open-source build requires `-endpoint`/`KIFF_CLOUD_URL` to be set.
- `kiff activity` (a combined evidence/proposals/approvals view) is not part of this brick; it is a planned follow-up.
