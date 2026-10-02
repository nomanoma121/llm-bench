# 2026-10-02-issue110

- kind: benchmark
- model: strata-iq2_xs-64k (`sha256:1ea678480e199a86809477636f8952377d5e250b6f7684937b36a6447888bde3`)
- runtime: strata
- args: --gpu 0,1
- gpu: NVIDIA GeForce RTX 3060, NVIDIA GeForce RTX 3060 (driver 595.91.07)
- started: 2026-10-02T02:38:43Z (238s)
- measurement valid: true

| case | metric | value | min | max | n |
|---|---|---|---|---|---|
| visual | ttft_ms (ms) | 9946.84 | 9946.84 | 9946.84 | 1 |
| visual | latency_ms (ms) | 205843.45 | 205843.45 | 205843.45 | 1 |
| visual | decode_tok_per_s (tok/s) | 58.66 | 58.66 | 58.66 | 1 |
| visual | prefill_tok_per_s (tok/s) | 452.56 | 452.56 | 452.56 | 1 |
| visual | tokens_in (tokens) | 9003.00 | 9003.00 | 9003.00 | 1 |
| visual | tokens_out (tokens) | 11137.00 | 11137.00 | 11137.00 | 1 |
| visual | draft_acceptance (ratio) | 0.77 | 0.77 | 0.77 | 1 |
| visual | mtp_accept_len (tokens) | 2.78 | 2.78 | 2.78 | 1 |
| visual | requests (requests) | 2.00 | 2.00 | 2.00 | 1 |
| visual | completed (ratio) | 1.00 | 1.00 | 1.00 | 1 |
| visual | cached_tokens (tokens) | 0.00 | 0.00 | 0.00 | 1 |
| visual | itl_ms_p50 (ms) | 0.03 | 0.03 | 0.03 | 1 |
| visual | itl_ms_p95 (ms) | 54.83 | 54.83 | 54.83 | 1 |
| visual | itl_ms_p99 (ms) | 59.27 | 59.27 | 59.27 | 1 |
| visual | energy_j_per_token (J/token) | 4.14 | 4.14 | 4.14 | 1 |
|  | vram_used_mib (MiB) | 11589.00 | 11433.00 | 11589.00 | 422 |
|  | gpu_util_percent (%) | 48.41 | 0.00 | 100.00 | 422 |
|  | power_w (W) | 109.41 | 42.36 | 176.69 | 422 |
|  | temperature_c (°C) | 68.00 | 46.00 | 68.00 | 422 |
|  | cpu_util_percent (%) | 47.95 | 2.06 | 55.27 | 210 |
|  | ram_used_mib (MiB) | 43549.29 | 42350.48 | 43549.29 | 211 |
