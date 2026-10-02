# 2026-10-02-issue111

- kind: benchmark
- model: strata-iq2_xs-64k (`sha256:1ea678480e199a86809477636f8952377d5e250b6f7684937b36a6447888bde3`)
- runtime: strata
- args: --gpu 0,1
- gpu: NVIDIA GeForce RTX 3060, NVIDIA GeForce RTX 3060 (driver 595.91.07)
- started: 2026-10-02T02:48:57Z (329s)
- measurement valid: true

| case | metric | value | min | max | n |
|---|---|---|---|---|---|
| visual | ttft_ms (ms) | 4337.31 | 4337.31 | 4337.31 | 1 |
| visual | latency_ms (ms) | 296794.56 | 296794.56 | 296794.56 | 1 |
| visual | decode_tok_per_s (tok/s) | 56.08 | 56.08 | 56.08 | 1 |
| visual | prefill_tok_per_s (tok/s) | 571.55 | 571.55 | 571.55 | 1 |
| visual | tokens_in (tokens) | 2479.00 | 2479.00 | 2479.00 | 1 |
| visual | tokens_out (tokens) | 16384.00 | 16384.00 | 16384.00 | 1 |
| visual | draft_acceptance (ratio) | 0.73 | 0.73 | 0.73 | 1 |
| visual | mtp_accept_len (tokens) | 2.55 | 2.55 | 2.55 | 1 |
| visual | requests (requests) | 1.00 | 1.00 | 1.00 | 1 |
| visual | completed (ratio) | 1.00 | 1.00 | 1.00 | 1 |
| visual | cached_tokens (tokens) | 0.00 | 0.00 | 0.00 | 1 |
| visual | itl_ms_p50 (ms) | 0.03 | 0.03 | 0.03 | 1 |
| visual | itl_ms_p95 (ms) | 54.74 | 54.74 | 54.74 | 1 |
| visual | itl_ms_p99 (ms) | 59.72 | 59.72 | 59.72 | 1 |
| visual | energy_j_per_token (J/token) | 4.12 | 4.12 | 4.12 | 1 |
|  | vram_used_mib (MiB) | 11587.00 | 11433.00 | 11587.00 | 604 |
|  | gpu_util_percent (%) | 49.44 | 0.00 | 100.00 | 604 |
|  | power_w (W) | 111.96 | 42.31 | 173.40 | 604 |
|  | temperature_c (°C) | 69.00 | 47.00 | 69.00 | 604 |
|  | cpu_util_percent (%) | 49.56 | 2.14 | 52.30 | 301 |
|  | ram_used_mib (MiB) | 42863.77 | 42405.47 | 42863.77 | 302 |
