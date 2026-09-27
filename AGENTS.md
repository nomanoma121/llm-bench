# Agent instructions

- Keep it small. One package per external system or concept; no adapter layers, no speculative abstractions, no compatibility shims.
- Comments only where the code cannot say it. Do not reference docs/ from code.
- There is exactly one GPU set. The controller runs one job at a time and needs no locking. Its state is the issue labels plus names derived from the job id; do not add a state store.
- Sandbox deletion and inference restore must always run after a job, including failed ones.
- Measurements come only from `llmbench benchmark`. `compare` reports facts, never a verdict.
- The PR is the result. The issue gets the request, the harness session id and one "Completed: PR #n" comment.
- Do not replace the placeholder files under `experiments/`; use a temporary directory for local runs.
- Run `go vet ./...` and `go test -race ./...` before committing.
