# Implementation plan

## 1. Local vertical slice

- [x] Define and validate a minimal `config.yaml` without changing existing placeholder experiments.
- [x] Provide a Go CLI and chi API for validation, submission, and status.
- [x] Persist run records and retry incomplete cleanup after controller restart.
- [x] Retry pending cleanup periodically while the controller stays up; preserve strict reverse hook order and separate benchmark errors from cleanup errors.
- [x] Persist a pending hook acquisition, then resume it on retry or restart without treating a human approval wait as a benchmark failure. Command hooks use exit code 75 to indicate pending.
- [x] Add operator-injected acquire/release hooks and a local command benchmark runner.
- [x] Test stage ordering, rollback, recovery, configuration validation, and API behavior.

## 2. Deterministic visual benchmark without an Agent

- [x] Validate visual output, publish it to the configured static site and record artifact hashes (no screenshots).
- [x] Provide local and Sandbox benchmark-only request paths that require no Issue, PR, or Agent.
- [x] Pin source/model/prompt identity for each execution and retain artifacts outside the worker Pod. Sandbox runs verify source and prompt against a Git commit, hash materialized model files before and after execution, optionally enforce an operator-pinned model digest, and copy HTML/logs to harness storage.

## 3. Cluster and GitOps integration

- [x] Resolve target IDs through operator configuration; permit only named targets.
- [x] Create and reconcile GitHub manifest pause/restore PRs through an operator-configured YAML scalar path; wait for merge. Cluster convergence still needs verification.
- [x] Gate GitOps hook completion on the configured single-source Argo CD Application's synced revision and the inference Deployment/Pods stopped or ready state. Verify with a real cluster before production use.
- [x] Add a single-run Agent Sandbox executor using an operator-owned SandboxTemplate and GPU ResourceClaim; send a Git commit snapshot, invoke build/runtime commands in the Sandbox, and collect HTML/logs.
- [x] Add the official Go SDK access layer for named SandboxClaims, port-forwarded sandboxd commands, file transfer, and explicit release; expose manual operations in the CLI.
- [ ] Keep the Agent session outside the Sandbox; reattach to its Claim after backing Pod replacement and avoid replaying non-idempotent commands.
- [ ] Support multiple attempts within an optimization round; release GPU before human review. Keep the SandboxWarmPool at zero idle replicas.
- [ ] Keep coordination leases as injected acquire/release hooks. Do not add SSH execution.
- [ ] Persist cluster run/session state durably and enforce one active run per target across harness replicas. Kubernetes ConfigMap run records and atomic target claims are implemented for the benchmark workflow; durable Agent conversation and external-effect fencing are not.
- [ ] Add leader election and failover tests; spread harness replicas across nodes with Pod anti-affinity. Lease election and fake-client tests exist; the Chart still fixes one replica because failover is not proven safe end-to-end.
- [ ] Package the harness, RBAC, configuration, and SandboxTemplate/WarmPool integration as a Helm Chart.
- [x] Add an experimental single-replica Helm Chart scaffold for those resources. The HTTP path can now select a Sandbox target; multi-replica deployment remains disabled until coordination and recovery are implemented.
- [ ] Isolate generated HTML from the credentialed harness across a network boundary. Publication now writes run output to a separate static site (dedicated origin, CSP required); a hostile-HTML review workload should still not share the harness network.
- [ ] Verify restoration after process, Pod, node, and GitHub failures.
- [ ] Exercise GitHub PR, Argo CD, Deployment, and SandboxClaim transitions end-to-end on a non-production cluster with the target manifest repository.

## 4. Human evaluation and Agent loop

- [x] Post A/B requests and run links to the originating Issue; accept A/B/tie/invalid feedback through the authenticated API and record it in the Issue. Free-form Issue replies are not parsed as votes.
- [x] Optionally send Discord notifications linking to that Issue.
- [x] Add Agent-facing skills under `.agents/skills/` for experiment authoring, running, and result analysis.
- [x] Serve the A/B comparison from the published site and record votes through the CLI; the Issue is the canonical history.
- [ ] Write final experiment notes and PR summaries from recorded results.

The local slice can run without a manifest repository, model downloads, or a browser. Do not treat it as capable of pausing production inference workloads until stage 3 is implemented and verified. Real cluster identifiers are supplied through operator configuration, flags, or environment variables; no production values belong in experiment YAML. The Go module is at the repository root and requires Go 1.26 for the Agent Sandbox SDK.
