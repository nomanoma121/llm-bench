---
name: result-analysis
description: Compare completed visual benchmark results and document a human A/B verdict in an experiment and PR.
---

# Analyze a result

Read `AGENTS.md` and `docs/design.md`. Compare only completed runs with the same prompt hash and rendering conditions. Use the originating GitHub Issue as the canonical A/B record; Discord is only a link notification. Present baseline and candidate with their run IDs, source commits, runtime settings, and artifact hashes. Do not infer image quality from a smoke test or invent a human vote.

After a recorded A/B/tie/invalid verdict, write the observed result, limitations, and interpretation to the relevant experiment README. Summarize the chosen candidate and run IDs in the PR. If either run still needs restoration, wait and report that state rather than finalizing the conclusion.
