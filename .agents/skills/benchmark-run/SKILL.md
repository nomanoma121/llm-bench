---
name: benchmark-run
description: Explicitly submit and monitor a llm-bench run, including pending pause and restoration states.
---

# Run a benchmark

Read `AGENTS.md` and `docs/usage.md`. Submit only after an explicit run request. For Sandbox targets, use the full Git commit containing the recipe and prompt; ensure the operator-approved target is selected. Use `llmbench submit <config.yaml> --commit <sha>` and `llmbench status <run-id>` with the configured controller URL and API token.

`awaiting_acquire` can mean the manifest pause PR still needs merging or the cluster has not converged. `needs_restore` means the GPU/workload cleanup is incomplete. Neither is a completed experiment. Do not submit another run or claim success until restoration finishes. Preserve the run ID and artifact hashes in the experiment notes. Do not run arbitrary Kubernetes or GitHub mutations outside the configured controller hooks.
