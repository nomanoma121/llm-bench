# Usage

## Local

```sh
llmbench job init benchmark > job.yaml
llmbench job validate job.yaml
llmbench benchmark --job job.yaml --model-path /models/qwen38-27b --model-digest sha256:... --job-id baseline
llmbench benchmark --job job.yaml --model-path /models/qwen38-27b --model-digest sha256:... --job-id candidate --bin ./build/bin/llama-server
llmbench compare experiments/qwen38-27b/baseline experiments/qwen38-27b/candidate
llmbench site --root experiments --out _site
```

`benchmark` writes `experiments/<model>/<job-id>/{jobspec.yaml,result.json,series.jsonl,README.md,raw/}`.
With `--push` it commits that directory and pushes it to `llmbench/<job-id>`.
Exit codes: 0 measured (check `measurement_valid`), 2 invalid job spec, 10 no result.

## Controller

```sh
helm upgrade --install llmbench charts/llmbench -f values.yaml
```

`values.yaml` sets `image.repository`, `githubAppSecret` (a Secret with `private-key.pem`) and `config`, which has the shape of `examples/controller.yaml`.

- Requests are open issues labelled `llmbench:benchmark` or `llmbench:optimize` with a ```yaml job spec (the issue forms fill it in).
- The controller runs one job at a time: pause the inference deployment through a PR in the manifests repository, create the GPU sandbox, run the job, open the result PR, delete the sandbox, restore the deployment. Sandbox deletion and restore always run, also after a failure.
- The job id is `<issue created date>-issue<number>`. The sandbox, the pause/restore branches and the result branch `llmbench/<job-id>` are named after it, so a restarted controller finds everything again: an issue still labelled `llmbench:running` is published if its result branch exists, otherwise it is marked failed, and cleaned up either way.
- For optimize jobs the controller opens an ACP session on the harness pod, posts the session id on the issue and waits for the turn to end. The result is the branch the agent pushed. To continue a failed session, talk to the harness directly and open a new issue.

## Agent commands

```sh
llmbench sandbox exec <job-id> -- nvidia-smi
llmbench sandbox put <job-id> ./patch.diff /workspace/patch.diff
llmbench sandbox get <job-id> /workspace/llm-bench/experiments/<model>/<dir>/result.json
```

## CI runner

Publication CI (`.github/workflows/pages.yml`) runs on **self-hosted runners** so it does not consume hosted Actions minutes, and pull-request code never runs on the host that holds the deployment credentials.

| Job | Label | Where it runs |
|-----|-------|---------------|
| `verify` (pull requests, `main` pushes) | `llm-bench-verify` | a **disposable Linux container** (`--ephemeral`, one container per job) |
| `deploy` (`main` only) | `llm-bench-deploy` | the host, only for merged code |

### Verify runner (container)

`~/.local/bin/llm-bench-verify-runner.sh` loops: it mints a registration token and starts one ephemeral `ghcr.io/actions/actions-runner` container per job, mounting only its own work directory. It is kept alive by the LaunchAgent `~/Library/LaunchAgents/dev.llmbench.verify-runner.plist` and logs to `~/Library/Logs/llm-bench-verify-runner.log`.

- The loop needs a GitHub token with `administration: write` (to request runner registration tokens) in `~/.config/llm-bench/github-token`, mode `600`. Refresh it with `gh auth token > ~/.config/llm-bench/github-token`.
- Restart the agent after editing the script: `launchctl unload ~/Library/LaunchAgents/dev.llmbench.verify-runner.plist && launchctl load ~/Library/LaunchAgents/dev.llmbench.verify-runner.plist`.
- Pull-request code therefore sees a fresh container each time and cannot write to the host's runner directories or the deploy runner's environment.

### Deploy runner (host)

Register it once on the host that will publish:

```sh
TOKEN=$(gh api -X POST repos/<owner>/<repo>/actions/runners/registration-token --jq .token)
mkdir -p ~/actions-runner-llm-bench-deploy && cd ~/actions-runner-llm-bench-deploy
curl -sLO https://github.com/actions/runner/releases/download/v<version>/actions-runner-osx-arm64-<version>.tar.gz
tar xzf actions-runner-osx-arm64-<version>.tar.gz && rm actions-runner-osx-arm64-<version>.tar.gz
./config.sh --url https://github.com/<owner>/<repo> --token "$TOKEN" --labels llm-bench-deploy --unattended --replace
./svc.sh install && ./svc.sh start
```

Deploy-host requirements:

- `upload-pages-artifact` shells out to **GNU tar** (`gtar --hard-dereference`), which macOS does not ship. Install it once on the deploy host with `brew install gnu-tar`; the runner's `PATH` must include `/opt/homebrew/bin`. Without it the deploy job fails with `gtar: command not found`.

Operational notes:

- Pull-request jobs get `contents: read` only; `pages: write` / `id-token: write` exist solely on the `main`-only deploy job.
- Fork pull requests do not run workflows at all: `run_workflows_from_fork_pull_requests` is disabled for this private repository (`gh api -X PUT repos/<owner>/<repo>/actions/permissions/fork-pr-workflows-private-repos -F run_workflows_from_fork_pull_requests=false`). Because no `verify` check is produced, branch protection blocks such a pull request from merging. The explicit reject step in the workflow is a second line of defense: a skipped job would count as a successful required check, and `pull_request` runs the workflow definition from the pull request, so neither the skip nor the step alone is a gate.
- `allow_forking` itself cannot be changed on a user-owned private repository (the API returns 422); the setting above is the supported control.
- `actions/setup-go` installs the toolchain named by `go.mod` and caching is disabled on self-hosted runners (the module cache is local; saving it to the Actions cache hangs the post step).
- `verify` is a **required** status check on `main`, so pull requests cannot merge while the verify runner is down. Keep the container loop running, or stop requiring the check when the runner is decommissioned.
- After changing a runner's labels, restart its service: a running listener only re-reads labels when it opens a new session, otherwise jobs stay queued.
- Decommission: `./svc.sh stop && ./svc.sh uninstall` for the deploy runner, `launchctl unload ~/Library/LaunchAgents/dev.llmbench.verify-runner.plist` for the container loop, then remove both under Settings → Actions → Runners.
