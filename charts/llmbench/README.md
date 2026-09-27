# llmbench Helm Chart

The `mvp.*` values below are rendered into resources: a single-replica controller
Deployment, its ServiceAccount/Role/RoleBinding, and a ConfigMap holding the
operator configuration. The `harness.*` and `sandbox.*` blocks are the frozen
v1.6 scaffold and are documented further down; they are not rendered.

Install the upstream Agent Sandbox controllers and CRDs separately; this chart
does not install them.

## MVP values (`docs/mvp.md` §9)

- `mvp.image.repository` / `tag`: the controller image (required).
- `mvp.repository`, `mvp.defaultBranch`, `mvp.labels.*`: the repository the
  controller polls and the Issue labels it writes. The five labels must be
  distinct; the controller refuses to start otherwise.
- `mvp.lease.{namespace,name,durationSeconds}`: the global GPU lease. Its
  annotations are the durable phase of the in-flight job, so the controller
  needs no run store. Keep one replica.
- `mvp.gitops.*`: the manifest of the shared inference workload. Required: the
  controller pauses it before it takes the GPU and restores it afterwards.
- `mvp.sandbox.{namespace,warmPool,llmbench}`: the Agent Sandbox pool the
  measurement runs in, and the argv prefix that runs the CLI inside that image.
- `mvp.models[]`: `{id, path, digest}`. The digest pins the weights; without it
  a result cannot prove what it measured and the benchmark marks it invalid.
- `mvp.engines[]`, `mvp.images[]`, `mvp.outputRoots[]`, `mvp.maxRounds`: the
  allowlists a job spec may choose from.
- `mvp.githubApp.*`: the App id, installation id and the **existing Secret**
  that holds the private key. The key is mounted into the controller and never
  reaches a sandbox; the sandbox receives a short-lived installation token
  scoped to one repository with `contents: write`.
- `mvp.pollIntervalSeconds` / `mvp.recoveryIntervalSeconds`: how often the loop
  looks for a request and reconciles unfinished work.
- `mvp.agentServiceAccount.{namespace,name}`: the existing ServiceAccount of the
  Agent Pod (the DSH deployment). Binding it grants the Agent sandbox discovery
  and file transfer, and nothing else.
- `mvp.agent.{namespace,podSelector,container,exec,cwd}`: the long-lived harness
  Deployment that runs optimization jobs. The controller opens one ACP session
  per job on it over `pods/exec` (ACP is stdio-only, so there is no API to call)
  and never starts or steers it. `exec` is the argv that serves ACP, `cwd` the
  harness-side session directory. The Agent's model comes from the harness
  profile, not from this chart.

## RBAC

The chart creates one namespaced Role and RoleBinding per namespace the
controller actually touches, instead of a ClusterRole:

| Namespace | What it grants |
|---|---|
| `mvp.lease.namespace` | `coordination.k8s.io` leases (the GPU ownership and the durable phase) |
| `mvp.sandbox.namespace` | sandbox claims (create/get/list/delete), sandboxes (read), pods (read) and `pods/portforward` |
| `mvp.gitops.application.namespace` | `argoproj.io` applications (read), for the sync check |
| `mvp.gitops.workload.namespace` | deployments/statefulsets (read) and pods (read), for the pause/restore convergence |
| `mvp.agent.namespace` | pods (read) and `pods/exec`, to open the ACP session on the harness |

The Agent's separate Role is limited to the sandbox namespace and excludes the
lease and the manifest.

The Agent reaches the sandbox with `llmbench sandbox job exec|push|pull|ls
<job id> --operator-config <file>` and reports its outcome with `llmbench job
done`; both resolve the sandbox by the job id, so a replacement sandbox is
picked up automatically.

## Frozen v1.6 values (not rendered)

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
