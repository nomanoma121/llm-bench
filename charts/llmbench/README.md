# llmbench chart

Installs the controller: a single-replica Deployment, its ConfigMap and the namespaced Roles it needs
(sandbox claims and port-forward, Argo CD application read, inference deployment/pod read, harness pod exec).

- `config`: the controller configuration, same shape as `examples/controller.yaml`.
- `githubAppSecret`: existing Secret with the GitHub App key as `private-key.pem`.
- `agentServiceAccount`: optional; grants the harness pod's ServiceAccount sandbox exec/port-forward so the agent can run `llmbench sandbox`.

Agent Sandbox CRDs, the warm pool and the harness are installed separately.
