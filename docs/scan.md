# Check the agent boundary

Use `kiff scan .` inside a Go agent repository to find explicit agent tools
that can reach consequential operations without a recognized decision earlier
in the function. Mark a handler with `//kiff:tool`, pass it to a common tool
registration call, or identify it with `-tool FunctionName`. The initial Go
scanner emits terminal, JSON, and SARIF reports and can enforce a CI threshold:

```bash
kiff scan .
kiff scan -format sarif -output kiff-scan.sarif -fail-on high .
```

This is static analysis: a clean result means no supported path was found, not
that the application is safe or that every framework-specific registration
shape is understood.

For Python agent repositories, use the separate
[kiff-scan](https://github.com/kiff/kiff-scan) tool instead:

```bash
uvx kiff-scan scan .
```

It also ships a GitHub Action: `uses: kiff/kiff-scan@v0.1.1`.
