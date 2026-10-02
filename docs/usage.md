# Usage

## Local

```sh
llmbench job init benchmark > job.yaml
llmbench job validate job.yaml
llmbench benchmark --job job.yaml --job-id baseline
llmbench benchmark --job job.yaml --job-id candidate --bin ./build/bin/llama-server --source ./runtimes/llama-cpp/upstream
llmbench compare experiments/Qwen3.8-27B-Q4_0/baseline experiments/Qwen3.8-27B-Q4_0/candidate
llmbench site --root experiments --out _site
```

The job spec's `model` is a path under `--models-dir` (default `/models`); the benchmark hashes it and records the digest, so models need no registration anywhere.
`benchmark` writes `experiments/<model>/<job-id>/{jobspec.yaml,result.json,series.jsonl,README.md,raw/}`.
With `--push` it commits that directory and pushes it to `llmbench/<job-id>`.
Exit codes: 0 measured (check `measurement_valid`), 2 invalid job spec, 10 no result.

## Models

`models.yaml` lists the weights, keyed by the directory they go into:

```yaml
Qwen3.8-27B:
  repo: <hugging face repo>
  revision: main
  include: ["*Q4_0.gguf", "mmproj-*"]
```

`llmbench models download [name...] --dir /var/lib/llama-cpp/models` fetches them with the `hf` CLI (`mise install` provides it; set `HF_TOKEN` for gated repos). A job spec then names a file or directory under that directory, for example `model: Qwen3.8-27B/Qwen3.8-27B-Q4_0.gguf`.

## Controller

```sh
helm upgrade --install llmbench charts/llmbench -f values.yaml
```

`values.yaml` sets `image.repository`, `githubAppSecret` (a Secret with `private-key.pem`) and `config`, which has the shape of `examples/controller.yaml`.

- Requests are open issues labelled `llmbench:benchmark` or `llmbench:optimize`, filed through the issue forms. The controller builds the job spec from the form's fields: a preset from `internal/job/preset.go` for the model and runtime, the benchmark, the harness and so on, with the optional `Spec override` YAML laid on top. The forms are generated: after changing presets, harnesses or `benchmarks/`, run `llmbench job form benchmark > .github/ISSUE_TEMPLATE/benchmark.yml` (and `optimize`); a test fails while they are stale.
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

All workflows run on GitHub-hosted runners.
