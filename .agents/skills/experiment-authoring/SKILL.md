---
name: experiment-authoring
description: Prepare model-specific llm-bench experiment recipes and hypotheses before running a benchmark.
---

# Author an experiment

Read `AGENTS.md` and `docs/design.md`. Place a new candidate under `experiments/<model-id>/<experiment-id>/`; do not edit another model's runtime variant as a side effect. Keep `config.yaml` limited to the model, benchmark prompt, allowlisted target ID, runtime settings, and invocation. Target privileges, GitOps paths, GPU claims, and credentials belong to operator configuration, not the experiment.

Write the README hypothesis and expected change before execution. Reuse `benchmarks/visual/prompt.md` unless the task explicitly changes the benchmark. Run `go run ./cmd/llmbench validate <config.yaml>` to check the recipe. Opening an Issue or PR does not authorize a run.
