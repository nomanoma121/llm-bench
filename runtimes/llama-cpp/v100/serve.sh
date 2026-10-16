#!/bin/sh
# Serves Qwen3.8-Flash-Next from the merged MTP GGUF that prepare-mtp.yaml writes.
# Every number in the comments is measured on gpu-node-02 (V100 32GB + 2x RTX 3060
# 12GB, Ryzen 9 5900X, dual-channel DDR4 at 43.5 GB/s).
#
#   MODEL     merged GGUF (default: the path prepare-mtp.yaml writes)
#   CTX       262144 (default) / 131072 / 8192
#   PROFILE   maxprefill  -> 131k only, 16/4/4 + ub 512
#   NGRAM=1   add ngram-mod next to MTP: repeated prompts decode at ~46 tok/s
#   NOSPEC=1  turn speculation off, for bit-reproducible output
#   PIPE=1 QSA_DENSE=1 OFFL=3   the measured-fastest 262k stack (see below)
#   THINK=0   end thinking immediately
#   CPUMASK   affinity mask, default fff (the node's 12 physical cores); empty to
#             let the scheduler place the threads
set -e
MODEL=${MODEL:-/models/flashnext-mtp/Flash-Next-UD-Q4_K_XL-MTP.gguf}
CTX=${CTX:-262144}
CPUMASK=${CPUMASK-fff}
test -f "$MODEL" || { echo "model not found: $MODEL (run prepare-mtp.yaml first)" >&2; exit 1; }

# Expert layers pinned to VRAM, one range per device. CUDA0 is the V100: CUDA
# orders devices fastest-first, so it is index 0 even though nvidia-smi lists it
# second. Everything not listed stays in host RAM, including the 26.8 GiB
# per-layer embedding table, which is only ever gathered a few rows at a time.
#
# The ceiling is the attention KV, and only the 12 QSA layers hold any: f16 is
# 24 KiB/token plus 5.6 KiB/token of indexer. At 262k that is +7.2 GiB and caps
# the devices at 15/5/4 expert layers; 8k has room for 17/6/6.
ot() { # ot <V100 layers> <3060a> <3060b>
  v=$(seq 0 $(($1 - 1)) | paste -sd'|')
  a=$(seq $1 $(($1 + $2 - 1)) | paste -sd'|')
  b=$(seq $(($1 + $2)) $(($1 + $2 + $3 - 1)) | paste -sd'|')
  printf 'per_layer_token_embd\\.weight=CPU'
  printf ',blk\\.(%s)\\.ffn_(down|gate|up)_exps\\.weight=CUDA0' "$v"
  printf ',blk\\.(%s)\\.ffn_(down|gate|up)_exps\\.weight=CUDA1' "$a"
  printf ',blk\\.(%s)\\.ffn_(down|gate|up)_exps\\.weight=CUDA2' "$b"
  printf ',ffn_(down|gate|up)_exps\\.weight=CPU'
}
UB_SET=${UB:-}
case "$CTX" in
  262144) OT=$(ot 15 5 4); UB=${UB:-256} ;;   # decode 32.4, prefill 156-161
  131072) OT=$(ot 15 5 4); UB=${UB:-384} ;;   # decode 32.4, prefill 162-167
  *)      OT=$(ot 17 6 6); UB=${UB:-256} ;;   # 8k: decode 34.4
esac
# ub 512 needs 4 or fewer layers on each 3060, or their compute buffer will not fit.
if [ "${PROFILE:-}" = maxprefill ]; then
  test "$CTX" = 131072 || { echo "PROFILE=maxprefill is 131072-only" >&2; exit 1; }
  OT=$(ot 16 4 4); UB=${UB_SET:-512}
fi

# Speculation changes the numerical path, so its output is not bit-identical to
# NOSPEC=1 (a few near-ties flip). The weights and the KV precision are untouched.
if [ "${NOSPEC:-0}" = 1 ]; then
  SPEC="--spec-type none"
elif [ "${NGRAM:-0}" = 1 ]; then
  SPEC="--spec-type draft-mtp,ngram-mod --spec-draft-n-max ${NMAX:-2} --spec-ngram-mod-n-min 16 --spec-ngram-mod-n-max 32"
else
  SPEC="--spec-type draft-mtp --spec-draft-n-max ${NMAX:-2}"
fi
# n-max 2 is the optimum: the verify batch is n+1 tokens and every extra token
# re-reads its own experts from host RAM. 2 -> 34.9, 3 -> 32.8, 4 -> 28.1 tok/s.

# QSA_DENSE replaces the gather fast path with a dense masked one for prompt
# batches: prefill +4.4% and 1.4 GiB less VRAM per device, output bit-identical.
# PIPE overlaps the GPU head of one ubatch with the CPU expert tail of the
# previous one; it needs QSA_DENSE because the gather pool and the pipeline's
# second pool do not fit together.
[ "${QSA_DENSE:-0}" = 1 ] && QSA=LLAMA_QSA_DENSE=1 || QSA=
[ "${PIPE:-0}" = 1 ] && PIPE_ENV=GGML_Q4_PIPE=auto || PIPE_ENV=
# OFFL=3 moves four expert tensors of the host-resident layers into the spare
# VRAM: prefill 169, decode 32.2. It makes the MoE a GPU kernel for those
# tensors, so the output is no longer bit-identical.
if [ "${OFFL:-0}" = 3 ]; then
  test "$CTX" = 262144 && test "${PIPE:-0}" = 1 && test "${QSA_DENSE:-0}" = 1 \
    || { echo "OFFL=3 needs CTX=262144 PIPE=1 QSA_DENSE=1" >&2; exit 1; }
  EXTRA='blk\.(25|26)\.ffn_down_exps\.weight=CUDA1,blk\.29\.ffn_down_exps\.weight=CUDA2,blk\.31\.ffn_gate_exps\.weight=CUDA2'
  OT="${OT%,*},$EXTRA,${OT##*,}"
fi
if [ -z "$UB_SET" ] && [ "$CTX" = 262144 ] && [ -n "$PIPE_ENV" ] && [ -n "$QSA" ]; then
  UB=512
fi
[ "${THINK:-1}" = 0 ] && THINKFLAG="--reasoning-budget 0" || THINKFLAG=

# --no-op-offload is not optional. With op_offload on (the default) every ubatch
# streams the experts it routes to over PCIe; a prompt touches all 512 of them, so
# that is 36 GiB per ubatch and prefill drops to 81 tok/s.
# -t 12 is the core count. 14 threads against the same 12 cores collapses to 3.6,
# so THREADS must stay at or below the cores the pod actually gets. The mask
# assumes the pod has the node's 12 physical cores (cpu 0-11); CPUMASK= drops it.
exec env $QSA $PIPE_ENV \
  GGML_CUDA_GRAPH_OPT=1 GGML_CUDA_DISABLE_GRAPHS=1 \
  GGML_FT_GRAPH_CACHE=1 GGML_FT_GRAPH_CACHE_MIN_NODES=32 \
  LLAMA_QWEN4EXP_HC_FUSION=1 LLAMA_HC_FUSED=2 LLAMA_MTP_PAD=5 \
  LLAMA_MTP_N_UBATCH=32 LLAMA_GPU_ARGMAX=1 \
  llama-server -m "$MODEL" \
  -ngl 99 -fit off -fa on -t "${THREADS:-12}" -c "$CTX" \
  ${CPUMASK:+-C "$CPUMASK" --cpu-strict 1} \
  -ub "$UB" -b "${BATCH:-4096}" --parallel 1 \
  -ot "$OT" --moe-expert-cache 0 --no-op-offload \
  $SPEC $THINKFLAG \
  --host "${HOST:-0.0.0.0}" --port "${PORT:-8080}" "$@"
