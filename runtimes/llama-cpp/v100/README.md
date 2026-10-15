# llama-cpp/v100

Qwen3.8-Flash-Next (`qwen4exp`) on gpu-node-02: one Tesla V100 32GB (sm_70) and two
RTX 3060 12GB (sm_86), a 12-core Ryzen 9 5900X and dual-channel DDR4 measured at
43.5 GB/s.

A sibling of `upstream/` rather than a build of it: the patches below apply to the
v0.5.0 tag only, so this image pins that commit itself and is not moved by the
daily `runtimes` submodule bump. It also needs CUDA 12.9, the last release that can
target sm_70 at all.

## Measured, 262K context, KV f16

What `serve.yaml` runs, and the only row verified on a long prompt:

| stack | prompt | prefill | decode |
|---|---|---|---|
| `QSA_DENSE=1 NGRAM=1` | 20k tokens | 151 tok/s | 30.5 tok/s |
| `QSA_DENSE=1 NGRAM=1` | short | 146-149 tok/s | 32 tok/s, 46.5 repeated |

8K context reaches 34.4 tok/s decode, because 29 expert layers fit in VRAM instead
of 24. The quantization is never changed: all expert tensors are Q4 or above.

`PIPE=1` and `OFFL=3` measured 169 tok/s prefill on 4K prompts, but they are off in
`serve.yaml`: together they leave under 1 GiB free on each 3060, and a 20k-token
prompt then aborts in the pipeline's staging pool with `cuMemCreate: out of memory`
on CUDA1. Check the headroom with `nvidia-smi` against your own longest prompt
before turning them on.

## Why the model is 24 layers on the GPUs and 24 in host RAM

The expert bank is 71.7 GiB at this quantization and VRAM is 56 GiB, so 24 of the 48
layers stay in host RAM and the CPU computes them. That half is the critical path:
about 60% of a decode step. Only the 12 QSA layers hold attention KV (f16 is
24 KiB/token plus 5.6 KiB/token of indexer), which is what sets the 15/5/4 split at
262K; the other 36 layers are GDN, whose recurrent state does not grow with context.

## Two settings that are not optional

`--no-op-offload`, which `serve.sh` always passes. With llama.cpp's default
`op_offload`, every ubatch streams the experts it routes to over PCIe. A prompt
touches all 512 of them, so that is 36 GiB per ubatch and prefill falls to 81 tok/s
— and it looks like 90% GPU utilization, because the copy is on the GPU's stream.

`-t` at or below the cores the pod actually gets. 14 threads against 12 cores with a
strict mask collapses decode from 34 to 3.6 tok/s.

## Output reproducibility

The weights and the KV precision are identical to upstream's, but three of the
options change the order of floating-point work, so greedy output is not
bit-identical to a plain run — a few near-ties flip, e.g. "not too terse" becomes
"not too short":

- speculation (`draft-mtp`, on by default, and `ngram-mod`): the verify batch is a
  batched GEMM where a single token is a GEMV
- `OFFL=3`: four expert tensors of the host-resident layers move to spare VRAM, so
  their MoE becomes a GPU kernel

`NOSPEC=1` (and leaving `OFFL` unset) gives the unspeculated path. `QSA_DENSE=1` is
bit-identical to the gather path and safe to leave on.

## Use

```sh
# once: fetch the shards and merge the MTP head into the trunk
llmbench models download Qwen3.8-Flash-Next-UD-Q4_K_XL
kubectl apply -f runtimes/llama-cpp/v100/prepare-mtp.yaml

# then
kubectl apply -f runtimes/llama-cpp/v100/serve.yaml
```

The image entrypoints `llama-server`, so a deployment serving another model just
passes its own flags. `serve.sh` is this node's measured Flash-Next configuration
and has to be asked for (`command: [/opt/llama-cpp-v100/serve.sh]`); it takes `CTX`,
`NGRAM`, `NOSPEC`, `PIPE`, `QSA_DENSE`, `OFFL`, `THINK`, `THREADS`, `CPUMASK`, `UB`,
`BATCH`, `MODEL`, `HOST`, `PORT`, and passes anything else straight through.

## Patches

`patches/01-pentacoxian-v100-v3.patch` is pentacoxian-dev's v100-v3 patch, taken
unmodified from
[the model repo](https://huggingface.co/pentacoxian-dev/Qwen3.8-Flash-Next-IQ3E-Q8D-MTP-GGUF).
It adds the `qwen4exp` architecture, MTP speculative decoding, a port of FreeToken's
MoE kernels (Apache-2.0, `patches/LICENSE-freetoken`) and the sm_70 kernels.

`patches/02-llm-bench-local.patch` is this repo's work on top of it:

| change | effect |
|---|---|
| `GGML_Q4_PIPE` two-stage pipeline (`PIPE=1`) | overlaps the GPU head of one ubatch with the CPU expert tail of the previous one: prefill +6.5%, decode unchanged, output bit-identical |
| `GGML_SPLIT_PROFILE=1` per-split timing | how `op_offload` was found: CUDA0 3.2 s of a 3.5 s ubatch, with the CPU at 0.1% |
| MoE cache slots allocated on the device that computes the layer | 328 graph splits down to 100, hit rate 66% to 92% — but the cache still loses to pinning layers, so it stays off |
| `GGML_CPU_ASYNC=1` asynchronous CPU `graph_compute` | no effect with pinned layers (GPU attention → CPU MoE → GPU residual is serial); kept for the cache path |
| `GGML_MMID_EXPERT_PAR` expert-major queue | default off, measured 9% slower on prefill |
| `tests/bench_mmid.c` | microbenchmark behind the decision not to rewrite the CPU `mul_mat_id` as a GEMM (`vec_dot_q4_K` is 0.07 µs/row; dequant + f32 dot is 5-7x slower) |

To rebuild the local patch after changing llama.cpp, apply patch 01 to v0.5.0, commit
that, and `git diff` it against the working tree.
