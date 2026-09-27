# Implementation plan

> **現在の作業計画は §4「MVP」であり、仕様は `docs/mvp.md` が正である。**§1〜§3c と §4b は v1.6/v1.7 の実装履歴である。MVP は旧仕様との互換性を保たない(visual 経路は freeze し、後で削除する)。

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
- [x] Gate GitOps hook completion on the configured single-source Argo CD Application being Synced at the same manifest revision the decision read (snapshot-pinned), and on the inference Deployment/Pods being fully stopped or ready. Verify with a real cluster before production use.
- [x] Add a single-run Agent Sandbox executor using an operator-owned SandboxTemplate and GPU ResourceClaim; send a Git commit snapshot, invoke build/runtime commands in the Sandbox, and collect HTML/logs.
- [x] Add the official Go SDK access layer for named SandboxClaims, port-forwarded sandboxd commands, file transfer, and explicit release; expose manual operations in the CLI.
- [ ] Keep the Agent session outside the Sandbox; reattach to its Claim after backing Pod replacement and avoid replaying non-idempotent commands.
- [ ] Support optimization rounds as **collections of independent runs** (one measurement run per attempt), starting with a MeasurementWindow that holds the pause across rounds; release the GPU before human review and keep the SandboxWarmPool at zero idle replicas. Never re-run the same run.
- [x] Keep GPU claims and inference pause/restore as operator-injected hooks; the target lease is engine-managed (acquired write-ahead, released as the final step of `releasing`). Do not add SSH execution.
- [ ] Persist cluster run/session state durably and enforce one active run per target across harness replicas. Kubernetes ConfigMap run records, atomic target leases and leader election (with worker cancellation on lease loss) are implemented; durable Agent conversation and proven external-effect fencing are not.
- [ ] Add failover tests and spread harness replicas across nodes with Pod anti-affinity. Lease election (including re-election after loss) has fake-client tests; multi-replica stays disabled because external-effect fencing is not proven safe end-to-end, and the chart has no templates yet.
- [ ] Package the harness, RBAC, configuration, and SandboxTemplate/WarmPool integration as a Helm Chart.
- [x] Add an experimental Helm Chart values scaffold for those resources (values-only scaffold: no `templates/` yet, so it installs nothing). The HTTP path can select a Sandbox target; multi-replica deployment remains disabled until coordination and recovery are implemented.
- [ ] Isolate generated HTML from the credentialed harness across a network boundary. Publication now writes run output to a separate static site (dedicated origin, CSP required); a hostile-HTML review workload should still not share the harness network.
- [ ] Verify restoration after process, Pod, node, and GitHub failures.
- [ ] Exercise GitHub PR, Argo CD, Deployment, and SandboxClaim transitions end-to-end on a non-production cluster with the target manifest repository.

## 3b. Publication split (designed in `docs/architecture.md` v1.6, implemented)

The controller will stop owning permanent publication: it serves an authenticated preview of a run's artifacts, and a human merges accepted artifacts into `experiments/<model-id>/<experiment-id>/output/` for CI to publish.

- [x] Serve candidate artifacts from a separate preview listener (upstream cluster authentication, `output/` only, sandbox CSP, root-confined paths, sealed artifacts only) and switch `review` from the published URL to a preview URL derived from `preview.base_url`.
- [x] Add `llmbench adopt` (digest-checked, atomic, idempotent, dry-run by default) and `manifest.json` with a complete payload inventory plus one canonical artifact digest shared by preview, review and adopt.
- [x] Add `llmbench adopted verify` and `llmbench site build`, and a GitHub Actions workflow that verifies on pull requests and deploys only on merges to `main`.
- [x] Remove the controller publication lifecycle (`internal/pages`, `PublishFinalizer`, the `finalizing` phase and the operator `site:` block), migrating legacy `finalizing` records to succeeded.

The split is implemented: the controller seals artifacts, serves authenticated previews and records the review in the Issue; `adopt` materializes the accepted artifact (with the authorizing vote) into `experiments/<model-id>/<experiment-id>/output/`, and CI publishes on merge. Digests and path checks live in Go, so the site toolchain never re-implements hashing. Publishing to GitHub Pages has not yet been exercised by a real merge.

## 3c. Measurement and optimization (superseded by `docs/mvp.md`)

この節は v1.7 設計の履歴である。**MVP では C〜H のうち `compare` 以外を実装しない**(MeasurementWindow、PromotionPolicy による自動採否、OptimizationSession 台帳、公開サイトへの metrics 反映は凍結)。B(sealed evidence)の `internal/measurement` は MVP の `result.json` の基盤として流用する。

- [x] A: measurement identity — record `MetricsDigest`, the frozen `MeasurementProtocol` (snapshot + digest), `RuntimeSpecDigest`, `RuntimeBuildDigest` and `EnvironmentDigest` on the run, without changing the existing `BenchmarkFingerprint`. `RunKind` (empty decodes as legacy visual) and the executor's `ExecutionOutputs` are in place, and the engine enforces each kind's required output instead of trusting a nil error.
- [x] B: sealed evidence — `evidence/metrics.json` with schema validation, metric source/trust, measurement validity, size bounds, atomic seal; `GET /v1/runs/{id}/metrics` on the authenticated control API; `llmbench metrics --json`. The harness seals raw driver output and adds harness timing; the sandbox transfers raw measurements with the bounded pull.
- [ ] C: comparability and the remote Agent CLI — `compare --kind model|runtime`, `submit --remote --request-id`, `status --remote`, `wait`/`list`/`logs`, preflight (local and server-side) and the exit-code contract.
- [ ] D: promotion policy — operator-owned `PromotionPolicy` (snapshot + digest) and a pure `optimize decide` that returns a verdict with reasons.
- [ ] W: MeasurementWindow — generalize resource ownership (`OwnerRef`), keep the pause/restore PR held across measurement runs, one `ActiveRunID` via CAS, timeouts and recovery.
- [ ] E: optimization session — round ledger, operator budgets, Issue intent (`kind: benchmark|optimize`, never a trigger), failure classification and retry as new runs.
- [ ] F/G: runtime spec and image release — `runtimes/<engine>/<variant>` with spec/build digests and a build cache, plus a main-only trusted builder publishing OCI images pinned by digest.
- [ ] H: published metrics — adopt evidence into git with manifest v2, render deterministic static SVG in the site build and include metric deltas in review comments.

Explicitly not doing: attempts inside a run, sharing a SandboxClaim across a window, re-running the same run, Agent-controlled promotion, runtime-reported metrics as the primary objective, a single weighted score, always-on profiling.

## 4. MVP (spec: `docs/mvp.md`)

2 本の経路(benchmark / optimization)を end-to-end で動かすことが完了条件。PR 単位で進める。

- [x] `internal/job`: JobSpec の parse / validate(CLI と Controller が同一コードを共有、unknown field は拒否)+ `llmbench job validate|init` + GitHub Issue Form(`.github/ISSUE_TEMPLATE/`)。
- [x] `internal/runtime` + `llmbench benchmark`: runtime adapter(llamacpp / freetoken)、runtime 起動と readiness、case ごとの計測、collector(harness / runtime / nvidia)、`experiments/<model-id>/<job-id>/` への `jobspec.yaml` / `result.json` / `series.jsonl` / `README.md` / `raw/` 出力、`--push` での commit / push。
- [ ] `llmbench compare`: baseline と candidate の中央値・delta・`valid`・`comparable`(+理由)・prefill 回帰・VRAM 差分を**事実としてのみ**返す(採否は返さない)。
- [ ] GitHub App 認証(`internal/githubapp`): private key から JWT → installation token(キャッシュ + 期限前更新)+ Issue polling + push 済みブランチからの PR 作成 + リンクコメント。
- [ ] Controller 薄版(`llmbench controller`, Deployment): Issue poll → JobSpec 検証 → claim ラベル → Lease → GitOps pause → SandboxClaim → Sandbox 内 `llmbench benchmark --push` → PR → claim 削除 → restore → release → 完了ラベル。startup と定期の recovery、write-ahead、mandatory restore、同一 job を再実行しない規律。ConfigMap store と leader election は作らない。
- [ ] Helm chart: controller Deployment / RBAC / GitHub App Secret / gpuLease / gitops / sandbox / models / runtimeImages / dsh option。
- [ ] Agent / DSH 連携: 既存 DSH deployment を参照し、Sandbox の bind / rebind と `llmbench sandbox exec|cp|shell`(port-forward transport)を提供する。conversation も session も llm-bench は持たない。
- [ ] 実データで `result.json` / `compare` の形式を調整し、凍結した visual 経路(httpapi / serve / preview / adopt / sitebuild / review / discord / pages workflow)を削除する。

## 4b. Human evaluation and Agent loop (v1.6/v1.7, frozen for MVP)

- [x] Post A/B requests and run links to the originating Issue; record A/B/tie/invalid votes through the CLI (which posts marker comments) and rebuild the vote history from the Issue. There is no vote endpoint on the HTTP API; free-form Issue replies are not parsed as votes.
- [x] Optionally send Discord notifications linking to that Issue.
- [x] Add Agent-facing skills under `.agents/skills/` for experiment authoring, running, and result analysis.
- [x] Serve the A/B comparison from the published site and record votes through the CLI; the Issue is the canonical history.
- [ ] Write final experiment notes and PR summaries from recorded results.

The local slice can run without a manifest repository, model downloads, or a browser. Do not treat it as capable of pausing production inference workloads until stage 3 is implemented and verified. Real cluster identifiers are supplied through operator configuration, flags, or environment variables; no production values belong in experiment YAML. The Go module is at the repository root and requires Go 1.26 for the Agent Sandbox SDK.
