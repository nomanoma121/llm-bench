# 2026-10-01-issue74

- kind: benchmark
- model: Qwen3.8-Flash-Next-UD-Q4_K_XL (`sha256:5f771c0aa60d78b1ae9fb1484cd5cf0f95421c237ed86b1ef77c30f2110ea038`)
- runtime: llamacpp
- args: --ctx-size 32768 -ngl 99 -fa on -ot ffn_(gate|up|down)_exps\.weight=CPU,per_layer_token_embd\.weight=CPU
- gpu: NVIDIA GeForce RTX 3060, NVIDIA GeForce RTX 3060 (driver 595.91.07)
- started: 2026-10-01T17:58:44Z (1793s)
- measurement valid: true

| case | metric | value | min | max | n |
|---|---|---|---|---|---|
| visual | ttft_ms (ms) | 82842.06 | 82842.06 | 82842.06 | 1 |
| visual | latency_ms (ms) | 1786531.04 | 1786531.04 | 1786531.04 | 1 |
| visual | decode_tok_per_s (tok/s) | 14.42 | 14.42 | 14.42 | 1 |
| visual | prefill_tok_per_s (tok/s) | 59.23 | 59.23 | 59.23 | 1 |
| visual | tokens_in (tokens) | 4907.00 | 4907.00 | 4907.00 | 1 |
| visual | tokens_out (tokens) | 24576.00 | 24576.00 | 24576.00 | 1 |
| visual | cached_tokens (tokens) | 0.00 | 0.00 | 0.00 | 1 |
| visual | itl_ms_p50 (ms) | 69.13 | 69.13 | 69.13 | 1 |
| visual | itl_ms_p95 (ms) | 74.08 | 74.08 | 74.08 | 1 |
| visual | itl_ms_p99 (ms) | 78.77 | 78.77 | 78.77 | 1 |
| visual | energy_j_per_token (J/token) | 8.68 | 8.68 | 8.68 | 1 |
|  | vram_used_mib (MiB) | 4777.00 | 3637.00 | 4777.00 | 3572 |
|  | gpu_util_percent (%) | 21.81 | 0.00 | 100.00 | 3572 |
|  | power_w (W) | 59.72 | 45.12 | 74.00 | 3572 |
|  | temperature_c (°C) | 64.00 | 44.00 | 64.00 | 3572 |
|  | cpu_util_percent (%) | 49.01 | 4.55 | 52.38 | 1785 |
|  | ram_used_mib (MiB) | 6181.81 | 5674.41 | 6181.81 | 1786 |
