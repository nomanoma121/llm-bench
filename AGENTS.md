# Agent instructions

Read `docs/design.md` and `docs/plan.md` before changing the workflow. Keep experiments scoped to `experiments/<model-id>/<experiment-id>/`; do not modify another model's runtime variant as a side effect.

Do not start a run merely because an Issue or PR changed. A run requires an explicit request. Treat `config.yaml` as an execution recipe, not authority to manage cluster resources. Only operator-controlled target configuration may select GPU leases, inference pause/restore, Agent Sandbox pools, or GitOps manifest paths. There is no SSH executor.

Restoration is mandatory after every acquired hook, including failed runs. Never report an experiment complete while restoration is pending. The Issue is the canonical human A/B evaluation record; Discord may only notify and link to it.

The existing experiment and benchmark files are placeholders. Do not replace them with example data. Use `examples/` for local smoke tests.

# Go implementation notes

The Go module is rooted at the repository root. Keep Cobra commands in `cmd/llmbench`, the HTTP layer thin (`internal/httpapi`), workflow policy in `internal/workflow`, experiment parsing in `internal/experiment`, and external effects behind injected adapters (`internal/adapter`). Prefer a small interface at the point of use over a general plugin framework.

Run `go test -race ./...` and `go vet ./...` from the repository root after changes. Do not put operator credentials or manifest-editing authority in experiment YAML. Command hooks must be safe to retry during recovery.
