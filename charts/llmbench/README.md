# llmbench chart

Installs the controller: a single-replica Deployment, its ConfigMap and the namespaced Roles it needs
(sandbox claims and port-forward, Argo CD application read, inference deployment/pod read, harness pod exec).

- `image`: defaults to `ghcr.io/nomanoma121/llmbench:latest`, published by `.github/workflows/image.yml` on every push to main (also tagged with the commit SHA). The package is private while the repository is, so set `imagePullSecrets`.
- `config`: the controller configuration, same shape as `examples/controller.yaml`.
- `githubAppSecret`: existing Secret with the GitHub App key as `private-key.pem`.
- `agentServiceAccount`: optional; grants the harness pod's ServiceAccount sandbox exec/port-forward so the agent can run `llmbench sandbox`.

Agent Sandbox CRDs, the warm pool and the harness are installed separately.
