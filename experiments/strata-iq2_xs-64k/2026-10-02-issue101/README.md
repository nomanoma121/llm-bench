# 2026-10-02-issue101

- kind: benchmark
- model: strata-iq2_xs-64k (`sha256:1ea678480e199a86809477636f8952377d5e250b6f7684937b36a6447888bde3`)
- runtime: strata
- args: --gpu 0,1
- gpu: NVIDIA GeForce RTX 3060, NVIDIA GeForce RTX 3060 (driver 595.91.07)
- started: 2026-10-02T01:39:04Z (329s)
- measurement valid: true

| case | metric | value | min | max | n |
|---|---|---|---|---|---|
| visual | ttft_ms (ms) | 4345.91 | 4345.91 | 4345.91 | 1 |
| visual | latency_ms (ms) | 297461.03 | 297461.03 | 297461.03 | 1 |
| visual | decode_tok_per_s (tok/s) | 55.95 | 55.95 | 55.95 | 1 |
| visual | prefill_tok_per_s (tok/s) | 573.18 | 573.18 | 573.18 | 1 |
| visual | tokens_in (tokens) | 2491.00 | 2491.00 | 2491.00 | 1 |
| visual | tokens_out (tokens) | 16384.00 | 16384.00 | 16384.00 | 1 |
| visual | draft_acceptance (ratio) | 0.73 | 0.73 | 0.73 | 1 |
| visual | mtp_accept_len (tokens) | 2.59 | 2.59 | 2.59 | 1 |
| visual | requests (requests) | 1.00 | 1.00 | 1.00 | 1 |
| visual | completed (ratio) | 1.00 | 1.00 | 1.00 | 1 |
| visual | cached_tokens (tokens) | 0.00 | 0.00 | 0.00 | 1 |
| visual | itl_ms_p50 (ms) | 0.03 | 0.03 | 0.03 | 1 |
| visual | itl_ms_p95 (ms) | 55.79 | 55.79 | 55.79 | 1 |
| visual | itl_ms_p99 (ms) | 60.10 | 60.10 | 60.10 | 1 |
| visual | energy_j_per_token (J/token) | 4.13 | 4.13 | 4.13 | 1 |
|  | vram_used_mib (MiB) | 11587.00 | 11433.00 | 11587.00 | 604 |
|  | gpu_util_percent (%) | 49.66 | 0.00 | 100.00 | 604 |
|  | power_w (W) | 112.32 | 43.78 | 170.47 | 604 |
|  | temperature_c (°C) | 69.00 | 48.00 | 69.00 | 604 |
|  | cpu_util_percent (%) | 49.64 | 2.01 | 55.72 | 301 |
|  | ram_used_mib (MiB) | 42827.63 | 42408.90 | 42827.63 | 302 |
