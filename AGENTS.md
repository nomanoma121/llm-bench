# Agent instructions

The current workflow authority is `docs/mvp.md` (MVP architecture, job spec, CLI contract) together with `docs/plan.md` §4 (implementation order). `docs/architecture.md` (v1.6/v1.7) and `docs/optimization.md` describe a superseded design: keep them as history and do not implement the v1.7 phases C–H (MeasurementWindow, promotion policy, optimization sessions, published metrics) unless the operator asks. The MVP does not preserve compatibility with the old specification — optimise the code for the two MVP paths instead.

The controller runs as a single-replica Deployment. The durable job phase lives on the GPU Lease annotation (`llmbench.io/phase`); Issue labels are the human-facing mirror. Do not add a ConfigMap run store, leader election, or an Agent-facing HTTP API. Do not create an Agent Pod: an existing DSH deployment owns conversation and session state, and the controller only binds and rebinds the GPU Sandbox to it.

Frozen for the MVP (do not extend, delete only after the MVP runs end to end): `llmbench adopt` and the artifact materialization flow below, `internal/httpapi`/`serve`, `internal/preview`, `internal/adopt`, `internal/sitebuild`, `internal/review`, `internal/discord`, and `.github/workflows/pages.yml`. When they are removed, this paragraph goes with them. Until then: only human-accepted artifacts belong in `experiments/<model-id>/<experiment-id>/output/`, materialized with `llmbench adopt`, never by editing HTML by hand.

An explicit run request is an open Issue carrying `llmbench:benchmark` or `llmbench:optimize`. Never start a run because a PR changed, or because an unlabeled Issue was edited. Treat the JobSpec in the Issue body as an execution recipe, not as authority to manage cluster resources. Only operator-controlled configuration may select GPU leases, inference pause/restore, Agent Sandbox pools, runtime/model allowlists, or GitOps manifest paths. There is no SSH executor; the Sandbox is reached through `llmbench sandbox exec|cp|shell` over the Agent Sandbox port-forward transport.

Restoration is mandatory after every acquired hook, including failed runs. Never report a job complete while restoration is pending. In the MVP the PR is the result and the Issue holds the request plus one link comment; do not treat the Issue as a vote history.

The existing experiment and benchmark files are placeholders. Do not replace them with example data. Use `examples/` for local smoke tests.

# Go implementation notes

The Go module is rooted at the repository root. Packages are organised by what they provide; interfaces are declared at the point of use:

- `cmd/llmbench`: Cobra commands and the composition root (`wire.go`). Commands stay thin; wiring lives in one place.
- `internal/acp`: the Agent Client Protocol client the controller uses to create one session per optimization job on the harness; `internal/agent` binds a job to that harness (task, permission policy, session working directory); `internal/lease` is the GPU ownership and durable-phase contract implemented by `internal/kube`.
- `internal/job`: the JobSpec parsed from an Issue body, validated by one implementation shared by the CLI and the controller. `internal/runtime`: runtime adapters (llama.cpp, FreeToken) used by the in-Sandbox `llmbench benchmark`; an adapter owns the flags that identify the target (model path, listen address, metrics/log output) and the job spec may only set tuning flags (`Constraints.ReservedArgs`). `internal/githubapp`: GitHub App authentication and installation tokens. `internal/measurement` and `internal/provenance` own the result schema, digests and canonicalization.
- `internal/experiment`, `internal/operator`: pure configuration parsing and validation (`internal/experiment` is the frozen visual recipe; the MVP job spec lives in `internal/job`).
- `internal/run`: the run state machine and `Engine`. Declares `Hook`/`HookSource`/`Executor`/`Finalizer`/`RunStore`/`LeaseStore` and the write-ahead state types. No SDK imports.
- `internal/runner`: benchmark execution strategies (local, sandbox) and the publication finalizer.
- `internal/hook`, `internal/gitops`, `internal/sandbox`, `internal/kube`, `internal/issues`, `internal/pages`, `internal/discord`, `internal/filestore`, `internal/provenance`: external effects as implementations of interfaces declared by their consumers. SDKs stay in these packages (and in the composition root).
- `internal/httpapi`: the thin chi HTTP layer (auth, decode, delegate).
- `internal/review`: the Issue-based A/B review policy; the Issue is the canonical vote history.

Workflow policy (`internal/run`, `internal/runner`, `internal/review`) must not import SDK packages: SDKs stay behind the interfaces those packages declare. Interface implementations (`internal/filestore`, `internal/kube`, `internal/hook`, `internal/sandbox`, `internal/gitops`) may import `internal/run` for its contract types and sentinel errors, and may own the reconciliation and ownership decisions that belong to their integration (for example the GitOps pause/restore decision table, or a command hook's exit-75 protocol). Run-wide phase/state-transition policy belongs in `internal/run` alone. Prefer a small interface at the point of use over a general plugin framework.

Every external effect follows the write-ahead rule: persist the intent (and the phase that follows it) before the call, make the effect idempotent, and release in strict reverse order. Restoration is complete only when the target lease is released; never report a run finished while restoration is pending.

Run `go test -race ./...` and `go vet ./...` from the repository root after changes. Do not put operator credentials or manifest-editing authority in experiment YAML. Command hooks must be safe to retry during recovery.
