# Brick 28 - Sign in to a KIFF cloud: kiff auth

Brick 28 adds `kiff auth login | status | logout` — the device-authorization client that signs the CLI in to a KIFF cloud and fills the credential the cloud-facing commands (`kiff apply`, brick 27's operator set) already read. It completes the loop brick 27 started: brick 27 gave the commands that talk to a cloud; brick 28 is how you authenticate to one without pasting a token by hand.

## What Was Added

- `cmd/kiff/auth.go`:
  - **`kiff auth login`** runs the OAuth 2.0 Device Authorization Grant (RFC 8628): `POST /v1/auth/device/start` returns a short `user_code` and a verification URL; the CLI prints the code, opens the URL in the browser (best-effort — the link is always printed too), and polls `POST /v1/auth/device/token`, honoring `authorization_pending` and `slow_down`, until the human approves in the browser. On success it stores the minted developer session in `~/.kiff/credentials` (0600) and the endpoint in `~/.kiff/config`, so a later `kiff apply` / `kiff domains list` authenticates with no extra flags.
  - **`kiff auth status`** reads the stored session and calls `GET /v1/me`, printing the signed-in subject + tenant, or a "not signed in" hint.
  - **`kiff auth logout`** revokes this device's session server-side (`POST /v1/auth/device/logout`) and forgets the local credential. Best-effort revoke: the local credential is removed regardless so it never lingers.

- `main.go` registers the `auth` subcommand; `version.go` bumps `CLIVersion` to `0.8.0` for the completed cloud-facing CLI surface (apply + operator + auth).

- `cmd/kiff/auth_test.go` covers the full login flow (pending → success, credential + endpoint persisted at 0600), each poll case (pending / slow_down / expired / denied / success), status (signed-in and not), logout (revokes + forgets), and config-key preservation.

## Why

The cloud-facing commands read a bearer from `-token` / `KIFF_TOKEN` / `~/.kiff/credentials`. Before brick 28 the only way to fill that slot was to open the dashboard, mint a key, and paste it. `kiff auth login` makes it one command, `gh auth login`-style, without the CLI ever handling the human's password: the human authenticates and approves in the browser, and the CLI receives a KIFF-issued developer session bound to their subject + tenant.

## Cloud-agnostic, on Purpose

Like the rest of the cloud-facing CLI, `auth` never names a hosted instance in source (RFC 034 Decision 1): the endpoint resolves `-endpoint` → `KIFF_CLOUD_URL` → `~/.kiff/config` → a build-time default that ships empty here. A packaged build sets the default via `-ldflags`; the open-source build requires the endpoint. This keeps `kiff/kiff` publishable while letting a distribution point at its own cloud.

## Credential Storage

The developer session is written to `~/.kiff/credentials` at mode 0600 — the same file `kiff apply` and the operator commands read. A real OS keychain (Keychain / libsecret / Credential Manager) is a follow-up; the 0600 file is the dependency-free interim, and the framework module stays stdlib-only.

## Pairs With Apply and the Operator Commands

- `kiff auth login` → obtain a developer session for a cloud.
- `kiff apply` → push a `kiff.yaml` to that cloud (uses the stored session).
- `kiff domains` / `runtimes` / `usage` / `keys` → inspect what's running (uses the stored session).
- `kiff auth logout` → revoke the session for this device.

## Limitations

- Developer sessions are owner-plane: they can author domains and mint keys (behind the cloud's server-side gates). Treat the `~/.kiff/credentials` file accordingly.
- `logout` revokes only this device's session; a "log out everywhere" is a follow-up.
- Browser auto-open is best-effort; headless environments use the printed link.
