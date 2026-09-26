# Agent instructions

Read `docs/design.md` and `docs/plan.md` before changing the workflow. Keep experiments scoped to `experiments/<model-id>/<experiment-id>/`; do not modify another model's runtime variant as a side effect.

Do not start a run merely because an Issue or PR changed. A run requires an explicit request. Treat `config.yaml` as an execution recipe, not authority to manage cluster resources. Only operator-controlled target configuration may select GPU leases, inference pause/restore, Agent Sandbox pools, or GitOps manifest paths. There is no SSH executor.

Restoration is mandatory after every acquired hook, including failed runs. Never report an experiment complete while restoration is pending. The Issue is the canonical human A/B evaluation record; Discord may only notify and link to it.

The existing experiment and benchmark files are placeholders. Do not replace them with example data. Use `examples/` for local smoke tests.

# Go implementation notes

The Go module is rooted at the repository root. Packages are organised by what they provide; interfaces are declared at the point of use:

- `cmd/llmbench`: Cobra commands and the composition root (`wire.go`). Commands stay thin; wiring lives in one place.
- `internal/experiment`, `internal/operator`: pure configuration parsing and validation (experiment recipes vs. operator-owned, privileged settings).
- `internal/run`: the run state machine and `Engine`. Declares `Hook`/`HookSource`/`Executor`/`Finalizer`/`RunStore`/`LeaseStore` and the write-ahead state types. No SDK imports.
- `internal/runner`: benchmark execution strategies (local, sandbox) and the publication finalizer.
- `internal/hook`, `internal/gitops`, `internal/sandbox`, `internal/kube`, `internal/issues`, `internal/pages`, `internal/discord`, `internal/filestore`, `internal/provenance`: external effects as implementations of interfaces declared by their consumers. SDKs stay in these packages (and in the composition root).
- `internal/httpapi`: the thin chi HTTP layer (auth, decode, delegate).
- `internal/review`: the Issue-based A/B review policy; the Issue is the canonical vote history.

Workflow policy (`internal/run`, `internal/runner`, `internal/review`) must not import SDK packages: SDKs stay behind the interfaces those packages declare. Interface implementations (`internal/filestore`, `internal/kube`, `internal/hook`, `internal/sandbox`, `internal/gitops`) may import `internal/run` for its contract types and sentinel errors, and may own the reconciliation and ownership decisions that belong to their integration (for example the GitOps pause/restore decision table, or a command hook's exit-75 protocol). Run-wide phase/state-transition policy belongs in `internal/run` alone. Prefer a small interface at the point of use over a general plugin framework.

Every external effect follows the write-ahead rule: persist the intent (and the phase that follows it) before the call, make the effect idempotent, and release in strict reverse order. Restoration is complete only when the target lease is released; never report a run finished while restoration is pending.

Run `go test -race ./...` and `go vet ./...` from the repository root after changes. Do not put operator credentials or manifest-editing authority in experiment YAML. Command hooks must be safe to retry during recovery.
