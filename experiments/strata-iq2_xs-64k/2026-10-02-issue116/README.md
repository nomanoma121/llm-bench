# 2026-10-02-issue116

- kind: benchmark
- model: strata-iq2_xs-64k (`sha256:1ea678480e199a86809477636f8952377d5e250b6f7684937b36a6447888bde3`)
- runtime: strata
- args: --gpu 0,1
- gpu: NVIDIA GeForce RTX 3060, NVIDIA GeForce RTX 3060 (driver 595.91.07)
- started: 2026-10-02T03:07:05Z (243s)
- measurement valid: true

| case | metric | value | min | max | n |
|---|---|---|---|---|---|
| visual | ttft_ms (ms) | 9893.58 | 9893.58 | 9893.58 | 1 |
| visual | latency_ms (ms) | 211067.54 | 211067.54 | 211067.54 | 1 |
| visual | decode_tok_per_s (tok/s) | 58.20 | 58.20 | 58.20 | 1 |
| visual | prefill_tok_per_s (tok/s) | 454.99 | 454.99 | 454.99 | 1 |
| visual | tokens_in (tokens) | 9003.00 | 9003.00 | 9003.00 | 1 |
| visual | tokens_out (tokens) | 11353.00 | 11353.00 | 11353.00 | 1 |
| visual | draft_acceptance (ratio) | 0.76 | 0.76 | 0.76 | 1 |
| visual | mtp_accept_len (tokens) | 2.73 | 2.73 | 2.73 | 1 |
| visual | requests (requests) | 2.00 | 2.00 | 2.00 | 1 |
| visual | completed (ratio) | 1.00 | 1.00 | 1.00 | 1 |
| visual | cached_tokens (tokens) | 0.00 | 0.00 | 0.00 | 1 |
| visual | itl_ms_p50 (ms) | 0.03 | 0.03 | 0.03 | 1 |
| visual | itl_ms_p95 (ms) | 54.66 | 54.66 | 54.66 | 1 |
| visual | itl_ms_p99 (ms) | 59.80 | 59.80 | 59.80 | 1 |
| visual | energy_j_per_token (J/token) | 4.15 | 4.15 | 4.15 | 1 |
|  | vram_used_mib (MiB) | 11589.00 | 11433.00 | 11589.00 | 432 |
|  | gpu_util_percent (%) | 48.29 | 0.00 | 100.00 | 432 |
|  | power_w (W) | 109.33 | 42.38 | 176.62 | 432 |
|  | temperature_c (°C) | 68.00 | 45.00 | 68.00 | 432 |
|  | cpu_util_percent (%) | 48.15 | 2.14 | 54.19 | 215 |
|  | ram_used_mib (MiB) | 43779.73 | 42477.39 | 43779.73 | 216 |
