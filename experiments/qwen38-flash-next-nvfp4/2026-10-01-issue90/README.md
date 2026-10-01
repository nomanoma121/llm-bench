# 2026-10-01-issue90

- kind: benchmark
- model: qwen38-flash-next-nvfp4 (`sha256:3cc792d27642e808c8cd903da7f395f266be934616c3c21f36d255e941a1e9a2`)
- runtime: freetoken-kai
- args: --pp-size 2 --gpu 1,0 --pp-layers 30 --moe-strategy offload --text-model-only --dense-quant none --kv-cache-dtype auto --kv-reserve-tokens 32768 --memory-ratio 0.95 --max-running-requests 1 --prefill-mixer-pieces 8 --moe-prefill-hit-d2d --prefill-chunk-budget 0.7
- gpu: NVIDIA GeForce RTX 3060, NVIDIA GeForce RTX 3060 (driver 595.91.07)
- started: 2026-10-01T21:25:58Z (1650s)
- measurement valid: true

| case | metric | value | min | max | n |
|---|---|---|---|---|---|
| visual | ttft_ms (ms) | 68770.58 | 68770.58 | 68770.58 | 1 |
| visual | latency_ms (ms) | 1415093.40 | 1415093.40 | 1415093.40 | 1 |
| visual | decode_tok_per_s (tok/s) | 18.25 | 18.25 | 18.25 | 1 |
| visual | prefill_tok_per_s (tok/s) | 71.35 | 71.35 | 71.35 | 1 |
| visual | tokens_in (tokens) | 4907.00 | 4907.00 | 4907.00 | 1 |
| visual | tokens_out (tokens) | 24575.00 | 24575.00 | 24575.00 | 1 |
| visual | cached_tokens (tokens) | 0.00 | 0.00 | 0.00 | 1 |
| visual | itl_ms_p50 (ms) | 53.44 | 53.44 | 53.44 | 1 |
| visual | itl_ms_p95 (ms) | 70.97 | 70.97 | 70.97 | 1 |
| visual | itl_ms_p99 (ms) | 85.16 | 85.16 | 85.16 | 1 |
| visual | energy_j_per_token (J/token) | 10.73 | 10.73 | 10.73 | 1 |
|  | vram_used_mib (MiB) | 11837.00 | 11801.00 | 11837.00 | 2830 |
|  | gpu_util_percent (%) | 47.04 | 0.00 | 100.00 | 2830 |
|  | power_w (W) | 93.23 | 18.59 | 176.54 | 2830 |
|  | temperature_c (°C) | 68.00 | 36.00 | 68.00 | 2830 |
|  | cpu_util_percent (%) | 6.28 | 4.49 | 13.43 | 1414 |
|  | ram_used_mib (MiB) | 77892.79 | 75234.83 | 77892.79 | 1415 |
