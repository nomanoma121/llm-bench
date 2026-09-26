# Go controller and CLI

The Go module lives at the repository root. See [design.md](design.md) for the full workflow.

The control plane validates an experiment, runs a command-backed benchmark locally or in an Agent Sandbox, exposes a chi HTTP API and a Cobra CLI, persists run state, and applies ordered acquire/release hooks. An optional visual output produces a fixed-viewport PNG preview and artifact hashes. The hooks are configured by the controller operator, not by experiment authors. An optional GitOps hook creates pause/restore GitHub PRs and checks the configured Argo CD Application and inference Deployment/Pods.

From the repository root:

```sh
go run ./cmd/llmbench validate examples/experiment.yaml
go run ./cmd/llmbench serve --root . --state .state --output runs --config examples/server.yaml
go run ./cmd/llmbench submit examples/experiment.yaml
go run ./cmd/llmbench status <run-id>
```

For Kubernetes-backed state and leader election, add `--coordination-namespace <namespace> --lease-name <name>` to `serve`. The ServiceAccount needs namespaced `get/create/update` on Leases and `get/list/create/update` on ConfigMaps. The Chart supplies these flags and RBAC. Do not start more than one replica yet: a Lease and versioned records do not alone fence an external GitHub operation or a command already running in a Sandbox after leadership loss.

The HTTP API is bound to `127.0.0.1:8080` by default. Set `LLMBENCH_API_TOKEN` for a shared deployment and put TLS in front of it. `POST /v1/runs` accepts a repository-relative experiment path; `GET /v1/runs/{id}` returns its status; `GET /v1/runs/{id}/preview` returns an authenticated PNG after successful restoration. `serve` requires an operator config with explicit target names; see `../examples/server.yaml`. An experiment can only select one of those targets. For Sandbox execution, a full `input_commit` is required and checked against the recipe and prompt. The local runner does not check out the optional commit.

A recipe may declare sampling conditions under `generation:` (for example `temperature`, `top_p`, `top_k`, `seed`, `max_tokens`). They are frozen into the run snapshot and participate in the BenchmarkFingerprint, so two runs that differ only in a generation parameter are not treated as A/B comparable.

Experiment commands receive `LLMBENCH_RUN_ID`, `LLMBENCH_PROMPT_PATH`, `LLMBENCH_OUTPUT_DIR`, `LLMBENCH_MODEL_ID`, `LLMBENCH_MODEL_PATH`, and `LLMBENCH_CONTEXT_SIZE`. The local runner invokes argument arrays without a shell; the Sandbox runner quotes each argument before constructing a sandboxd shell script. The operator configuration can inject ordered hooks per target, such as GPU lease acquisition and inference pause. Release runs in reverse order, including after a benchmark failure or controller restart; each release command must be safe to retry. The service admits at most one active run per target; different targets may run concurrently only when their operator-owned resources are independent. The local file store is for one controller process. The Chart uses a Kubernetes Lease and ConfigMap run records but still fixes one replica until external-effect fencing and failover validation are complete.

If a release cannot finish, the run remains `needs_restore` with `cleanup_error` and blocks new runs. The controller retries pending hooks every 30 seconds by default (`serve --retry-interval`); earlier hooks are not released until later hooks succeed. A benchmark that succeeded before a temporary cleanup failure returns to `succeeded` after cleanup completes.
Keep the same hook names and release implementation configured until every pending run has been restored; removing them would leave that run blocked for manual intervention.

An operator command hook can exit with status 75 during `acquire` to signal "not ready yet". The run stays `awaiting_acquire`, records `wait_reason`, and retries that hook at the same interval or after a controller restart. Previous hooks remain acquired; the benchmark does not begin. A non-75 acquire failure starts normal rollback. Exit 75 during `release` is a cleanup failure and leaves the run in `needs_restore`. Acquire commands must be idempotent because the same command can run again after a crash; release must also tolerate an acquire that was attempted but never completed.

For GitOps pause/restore, set `targets.<id>.gitops` in the operator config (see `../examples/server-gitops.yaml`) and provide `LLMBENCH_GITHUB_TOKEN` in the harness environment. The hook only changes the configured scalar at `yaml_path` in `file_path`; `active_value` and `paused_value` are compared before writing. It creates one pause PR and one restore PR per run, reuses them on retry, closes an unmerged pause PR during rollback, and waits for merge. It then requires the single-source Argo CD Application to report `Synced` at the manifest repository's current base-branch revision, and checks that the configured inference Deployment and Pods have stopped or reached the configured `active_replicas`. The Sandbox is released before the restore PR wait. The Application and workload may be in separate namespaces, and the harness ServiceAccount needs read access to each. This path still needs an end-to-end test on a non-production cluster and does not support multi-source Applications.

Manual Sandbox operations use the official Agent Sandbox Go SDK and require its controllers/CRDs and a configured `SandboxWarmPool`:

```sh
go run ./cmd/llmbench sandbox --namespace bench acquire 0123456789abcdef0123456789abcdef my-pool
go run ./cmd/llmbench sandbox --namespace bench run llmbench-0123456789abcdef0123456789abcdef 'cd /workspace && git status --short'
go run ./cmd/llmbench sandbox --namespace bench pull llmbench-0123456789abcdef0123456789abcdef /workspace/output/index.html ./index.html
go run ./cmd/llmbench sandbox --namespace bench release 0123456789abcdef0123456789abcdef
```

The `--kubeconfig` flag is available for out-of-cluster access; in a Pod, the SDK uses the ServiceAccount. `run` executes the supplied string via `/bin/sh -c`; it does not replay a command on transport failure. Claim creation is idempotent by run ID. `release` deletes the Claim, so save needed artifacts first.

For an automatic Sandbox run, configure an operator target with `sandbox.namespace` and `sandbox.warm_pool` (see `../examples/server-sandbox.yaml`), start `serve` with that config, and submit an experiment targeting it with `--commit <full-SHA>`. The experiment config and prompt must match the given Git commit. The controller uploads that commit's `git archive`, executes `runtime.start` and `invoke` inside the Sandbox, downloads HTML and logs, and releases the Claim. `runtime.start.ready_timeout_seconds` defaults to 300 and readiness uses `curl` inside the development image; that image also needs `sh`, `tar`, `date`, `python3`, the runtime build tools, and access to the materialized model PVC. The runner hashes every regular file in `/models/<model-id>` before and after execution, records `output/model-identity.json` outside the Sandbox, and fails if the model changes. Symlinks and missing model files are rejected. The model files under ignored `models/` are **not** included in `git archive`; mount them separately. This path supports a single benchmark without an Agent; it does not retain one Claim over several optimization attempts.

`LLMBENCH_MODEL_PATH` points to `<root>/models/<model-id>` locally and `/models/<model-id>` in a Sandbox. To require an already-known model revision, add an operator-owned `targets.<id>.sandbox.model_sha256.<model-id>` value matching a previously recorded `model-identity.json` tree digest; a mismatch blocks invocation. Establish that digest from a trusted initial run before making it policy. The digest hashes file paths and contents, so it is not the same as a single weight file's SHA-256. Git submodules are rejected for Sandbox runs because `git archive` does not include their content; Git LFS materialization is also not implemented.

## Issue A/B review

An optional operator-owned `review` block enables the Issue record. It needs `LLMBENCH_GITHUB_TOKEN` with Issue-comment permission and a public HTTPS base URL routed to this controller. `public_base_url` makes only hash-addressed PNGs and minimal run metadata public; it does not expose the raw HTML, commands, or full run record. Do not enable it for private benchmark artifacts. The base URL must actually be reachable by GitHub reviewers; a private cluster DNS name will not work. Configure TLS and ingress separately.

```yaml
review:
  owner: example
  repository: llm-bench
  public_base_url: https://bench.example.org
  bot_login: bench-app[bot]
  discord_webhook_env: LLMBENCH_DISCORD_WEBHOOK
```

`bot_login` is optional for a personal token and required when the GitHub credential cannot call `GET /user` (for example, an installation token). Only comments from that login are interpreted as controller review/vote records, preventing another Issue participant from forging the machine-readable markers. The Discord setting is optional. If set, provide that environment variable in the controller (the Chart can read it from `harness.discordWebhookSecret`, key `webhook`). Discord receives only the Issue link. Once both visual runs have succeeded and restoration is complete, request and record a review:

```sh
go run ./cmd/llmbench review request <baseline-run-id> <candidate-run-id> --issue 42
go run ./cmd/llmbench review vote <review-id> --choice B --notes 'The geometry is cleaner'
go run ./cmd/llmbench review status <review-id> --config examples/server-gitops.yaml
```

Both runs must belong to the same Issue and use the same prompt and render settings. For the same model, recorded weight digests must match. The Issue is the vote history; a manual free-form reply is not parsed as a vote by the controller. The controller API token authorizes programmatic votes, so restrict it to trusted evaluators. Final experiment notes and PR summary are still written by an Agent, not generated by these commands.
