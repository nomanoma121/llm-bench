# 2026-10-02-issue119

- kind: benchmark
- model: strata-iq2_xs-64k (`sha256:1ea678480e199a86809477636f8952377d5e250b6f7684937b36a6447888bde3`)
- runtime: strata
- args: --gpu 0,1
- gpu: NVIDIA GeForce RTX 3060, NVIDIA GeForce RTX 3060 (driver 595.91.07)
- started: 2026-10-02T03:18:59Z (395s)
- measurement valid: true

| case | metric | value | min | max | n |
|---|---|---|---|---|---|
| visual | ttft_ms (ms) | 4337.11 | 4337.11 | 4337.11 | 1 |
| visual | latency_ms (ms) | 362597.58 | 362597.58 | 362597.58 | 1 |
| visual | decode_tok_per_s (tok/s) | 58.43 | 58.43 | 58.43 | 1 |
| visual | prefill_tok_per_s (tok/s) | 571.58 | 571.58 | 571.58 | 1 |
| visual | tokens_in (tokens) | 2479.00 | 2479.00 | 2479.00 | 1 |
| visual | tokens_out (tokens) | 20916.00 | 20916.00 | 20916.00 | 1 |
| visual | draft_acceptance (ratio) | 0.77 | 0.77 | 0.77 | 1 |
| visual | mtp_accept_len (tokens) | 2.77 | 2.77 | 2.77 | 1 |
| visual | requests (requests) | 1.00 | 1.00 | 1.00 | 1 |
| visual | completed (ratio) | 1.00 | 1.00 | 1.00 | 1 |
| visual | cached_tokens (tokens) | 0.00 | 0.00 | 0.00 | 1 |
| visual | itl_ms_p50 (ms) | 0.03 | 0.03 | 0.03 | 1 |
| visual | itl_ms_p95 (ms) | 55.38 | 55.38 | 55.38 | 1 |
| visual | itl_ms_p99 (ms) | 59.60 | 59.60 | 59.60 | 1 |
| visual | energy_j_per_token (J/token) | 3.95 | 3.95 | 3.95 | 1 |
|  | vram_used_mib (MiB) | 11587.00 | 11433.00 | 11587.00 | 734 |
|  | gpu_util_percent (%) | 50.16 | 0.00 | 100.00 | 734 |
|  | power_w (W) | 112.62 | 42.63 | 171.89 | 734 |
|  | temperature_c (°C) | 69.00 | 48.00 | 69.00 | 734 |
|  | cpu_util_percent (%) | 49.88 | 2.09 | 52.05 | 367 |
|  | ram_used_mib (MiB) | 42901.27 | 42442.07 | 42901.27 | 368 |
