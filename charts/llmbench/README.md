# llmbench Helm Chart (values scaffold)

**This chart is a values-only scaffold: there is no `templates/` directory, so installing it creates no resources.** It records the intended shape of the harness deployment and the Agent Sandbox GPU workspace so the templates can be added later against a reviewed specification. Install the upstream Agent Sandbox controllers and CRDs separately; this chart will not install them.

## Intended values

- `harness.image.*`, `harness.repositoryPVC`, `harness.dataPVC`, `harness.apiTokenSecret` (a Secret with key `token`), `harness.port`, `harness.retryInterval`: the single-replica harness Pod, its repository/data volumes and the API token. Installing with more than one replica requires leader-election fencing that is still unproven end-to-end, so templates should keep `replicas: 1`.
- `harness.githubTokenSecret` (key `token`): needed when an operator target configures `gitops`, `site` publication, or `operatorConfig.review`. The token belongs to the harness only; it must never reach the Sandbox or any browser/rendering container.
- `operatorConfig.targets.*`: the operator configuration that is mounted into the harness. Local executor targets must be rejected in a cluster deployment so untrusted experiment commands never run inside the credentialed harness Pod.
- `sandbox.*`: the Agent Sandbox development image, sandboxd image, GPU ResourceClaimTemplate, workspace storage class and optional model/cache PVCs. Keep the WarmPool at zero idle replicas; a claim is acquired only for an explicit run.

## Operational requirements for future templates

- The harness ServiceAccount needs namespaced read access to the configured Argo CD Application and inference Deployment/Pods, and — when coordination is enabled — `get/create/update` on Leases plus `get/list/create/update/delete` on ConfigMaps.
- Reviewed output is served from the operator's static site (dedicated origin), not from the harness Pod. Keep generated HTML away from harness credentials and enforce a CSP with a fixed script allowlist and no outbound connections (see `docs/architecture.md` section 4.9).
- Pod-to-Pod access to sandboxd goes through Kubernetes port-forward; do not expose sandboxd through a Service.
- A Git archive omits submodule contents (the controller rejects commits with gitlinks) and does not materialize Git LFS objects.
- The GitOps and cluster checks (pause PR merge, Argo CD synced revision, Deployment/Pods stopped or restored) and the SandboxClaim lifecycle still need a real-cluster end-to-end test.
- A Discord webhook Secret with key `webhook` is optional via `harness.discordWebhookSecret`.
