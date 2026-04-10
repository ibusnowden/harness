# Ascaris Philosophy

Ascaris is a practical coding harness, not a research artifact.

The product goal is straightforward:

- keep the runtime local and inspectable
- prefer typed state over inferred terminal prose
- make prompt execution, tools, sessions, recovery, and operator controls scriptable
- ship one active implementation language and one active product name

The repo reflects that bias. The active surface is the Go CLI.

What matters here is operational clarity:

- a prompt run should leave durable session state
- worker and recovery state should be visible without guesswork
- tools should be permissioned and inspectable
- configuration should live under one canonical namespace: `.ascaris`

Ascaris should feel like a dependable local control plane for coding work, not a pile of half-retired runtimes.
