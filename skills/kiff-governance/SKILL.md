---
name: kiff-governance
description: Scan Go agent code with the KIFF CLI, explain unguarded consequential actions, and help route those actions through a real KIFF decision boundary. Use when auditing agent tools, adding KIFF governance, producing SARIF, or configuring a KIFF scan in CI. Do not use for generic Go linting or non-agent security review.
---

# KIFF Governance

Use the installed `kiff` CLI as the source of truth. Do not recreate its sink
taxonomy or infer a clean result from source inspection alone.

## Scan

1. Confirm the target path and inspect its language and existing agent/tool
   registration shape.
2. Choose the scanner for the repository language:
   - Python: use `kiff-scan`, not the native CLI. Check `kiff-scan -h` or
     `uvx kiff-scan -h`, then run `kiff-scan scan .` or `uvx kiff-scan scan .`.
   - Go: check that `kiff scan -h` is available, then run the initial scan
     without making the command fail on findings:

   ```bash
   kiff scan -format json -fail-on none .
   ```

   If the required scanner is missing, explain the installation requirement; do
   not install software without authorization.
3. Ground every explanation in the reported tool, consequential call, file,
   and line. Inspect the surrounding source before proposing a change.
4. State the scanner's limit: a clean result means no supported path was found,
   not that the application is safe or externally unreachable.

For Go, use `-tool FunctionName` when the project's framework registration
shape is not recognized. Adding `//kiff:tool` is another explicit entry-point
marker, not a security control.

## Govern

Only modify code when the user asks for a fix or integration.

- Put the KIFF decision before the consequential call. Prefer the project's
  existing runtime client or guard boundary over a new abstraction.
- Preserve fail-safe behavior: only an explicit allowed decision may proceed;
  blocked, approval-required, invalid, unknown, and transport-error outcomes
  must withhold the side effect.
- Do not silence a finding with naming, comments, exclusions, or a no-op guard.
- Do not claim an authorization check is sufficient when safety also depends on
  live operational state.
- Keep credentials in the executor/runtime boundary, not in agent-controlled
  code or generated policy.

After a change, run the relevant test suite and repeat the scan. Report which
finding disappeared and what decision now dominates the consequential call.

## CI

For GitHub code scanning, generate SARIF while preserving the report even when
findings exist:

```bash
kiff scan -format sarif -output kiff-scan.sarif -fail-on none .
kiff scan -fail-on high .
```

Upload the SARIF file before running the enforcing command. Exit code `1` means
the configured finding threshold was met; exit code `2` means the scan itself
could not be completed and must not be treated as clean.

The native CLI currently analyzes Go. For Python, use the separately published
`kiff-scan` package. Do not claim TypeScript source coverage until the CLI
reports it as supported.
