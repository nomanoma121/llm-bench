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

## CI

All workflows run on `k8s-runner-llm-bench`, the Actions Runner Controller scale set in the cluster (defined in the manifests repository).
