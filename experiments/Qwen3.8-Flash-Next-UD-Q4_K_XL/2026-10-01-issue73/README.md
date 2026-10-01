# 2026-10-01-issue73

- kind: benchmark
- model: Qwen3.8-Flash-Next-UD-Q4_K_XL (`sha256:5f771c0aa60d78b1ae9fb1484cd5cf0f95421c237ed86b1ef77c30f2110ea038`)
- runtime: llamacpp
- args: --device CUDA0 --ctx-size 32768 -ngl 99 -fa on -ot ffn_(gate|up|down)_exps\.weight=CPU,per_layer_token_embd\.weight=CPU
- gpu: NVIDIA GeForce RTX 3060, NVIDIA GeForce RTX 3060 (driver 595.91.07)
- started: 2026-10-01T17:12:33Z (1853s)
- measurement valid: true

| case | metric | value | min | max | n |
|---|---|---|---|---|---|
| visual | ttft_ms (ms) | 95499.90 | 95499.90 | 95499.90 | 1 |
| visual | latency_ms (ms) | 1827662.23 | 1827662.23 | 1827662.23 | 1 |
| visual | decode_tok_per_s (tok/s) | 14.19 | 14.19 | 14.19 | 1 |
| visual | prefill_tok_per_s (tok/s) | 51.38 | 51.38 | 51.38 | 1 |
| visual | tokens_in (tokens) | 4907.00 | 4907.00 | 4907.00 | 1 |
| visual | tokens_out (tokens) | 24576.00 | 24576.00 | 24576.00 | 1 |
| visual | cached_tokens (tokens) | 0.00 | 0.00 | 0.00 | 1 |
| visual | itl_ms_p50 (ms) | 70.15 | 70.15 | 70.15 | 1 |
| visual | itl_ms_p95 (ms) | 74.67 | 74.67 | 74.67 | 1 |
| visual | itl_ms_p99 (ms) | 79.59 | 79.59 | 79.59 | 1 |
| visual | energy_j_per_token (J/token) | 8.13 | 8.13 | 8.13 | 1 |
|  | vram_used_mib (MiB) | 7985.00 | 113.00 | 7985.00 | 3654 |
|  | gpu_util_percent (%) | 21.42 | 0.00 | 100.00 | 3654 |
|  | power_w (W) | 54.70 | 22.03 | 97.78 | 3654 |
|  | temperature_c (°C) | 63.00 | 43.00 | 63.00 | 3654 |
|  | cpu_util_percent (%) | 48.64 | 1.97 | 52.54 | 1826 |
|  | ram_used_mib (MiB) | 6092.96 | 5356.50 | 6092.96 | 1827 |
