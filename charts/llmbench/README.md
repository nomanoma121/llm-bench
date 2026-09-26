# llmbench Helm Chart (values scaffold)

**This chart is a values-only scaffold: there is no `templates/` directory, so installing it creates no resources.** It records the intended shape of the harness deployment and the Agent Sandbox GPU workspace so the templates can be added later against a reviewed specification. Install the upstream Agent Sandbox controllers and CRDs separately; this chart will not install them.

## Intended values

- `harness.image.*`, `harness.repositoryPVC`, `harness.dataPVC`, `harness.apiTokenSecret` (a Secret with key `token`), `harness.port`, `harness.retryInterval`: the single-replica harness Pod, its repository/data volumes and the API token. Multi-replica operation requires external-effect fencing that is still unproven end-to-end, so future templates must keep one replica.
- `harness.githubTokenSecret` (key `token`): needed when an operator target configures `gitops` or `operatorConfig.review`. It is **not** needed for publication: the controller never writes to a publication host, and CI publishes with its own `GITHUB_TOKEN`. The token belongs to the harness only; it must never reach the Sandbox or any browser/rendering container.
- `operatorConfig.targets.*`: the operator configuration that is mounted into the harness. Local executor targets must be rejected in a cluster deployment so untrusted experiment commands never run inside the credentialed harness Pod.
- `sandbox.*`: the Agent Sandbox development image, sandboxd image, GPU ResourceClaimTemplate, workspace storage class and optional model/cache PVCs. Keep the WarmPool at zero idle replicas; a claim is acquired only for an explicit run.

## Operational requirements for future templates

- The harness ServiceAccount needs namespaced read access to the configured Argo CD Application and inference Deployment/Pods, and — when coordination is enabled — `get/create/update` on Leases plus `get/list/create/update/delete` on ConfigMaps.
- Candidate output is previewed from the harness Pod on a separate listener that has no bearer auth of its own, so an Ingress terminating cluster authentication must sit in front of it. Generated HTML keeps a sandbox CSP and, once adopted, is embedded in a sandboxed iframe on the published site (see `docs/architecture.md` sections 4.9 and 4.12).
- Pod-to-Pod access to sandboxd goes through Kubernetes port-forward; do not expose sandboxd through a Service.
- A Git archive omits submodule contents (the controller rejects commits with gitlinks) and does not materialize Git LFS objects.
- The GitOps and cluster checks (pause PR merge, Argo CD synced revision, Deployment/Pods stopped or restored) and the SandboxClaim lifecycle still need a real-cluster end-to-end test.
- A Discord webhook Secret with key `webhook` is optional via `harness.discordWebhookSecret`.
