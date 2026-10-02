# 2026-10-02-issue113

- kind: benchmark
- model: strata-iq2_xs (`sha256:0a6f1b25385e42eb853a3da069c0fb3e0fa93e834297183311ff8c8055bc64ad`)
- runtime: strata
- args: --gpu 0,1
- gpu: NVIDIA GeForce RTX 3060, NVIDIA GeForce RTX 3060 (driver 595.91.07)
- started: 2026-10-02T02:59:18Z (139s)
- measurement valid: true

| case | metric | value | min | max | n |
|---|---|---|---|---|---|
| visual | ttft_ms (ms) | 2025.03 | 2025.03 | 2025.03 | 1 |
| visual | latency_ms (ms) | 112416.43 | 112416.43 | 112416.43 | 1 |
| visual | decode_tok_per_s (tok/s) | 63.14 | 63.14 | 63.14 | 1 |
| visual | prefill_tok_per_s (tok/s) | 280.00 | 280.00 | 280.00 | 1 |
| visual | tokens_in (tokens) | 567.00 | 567.00 | 567.00 | 1 |
| visual | tokens_out (tokens) | 6971.00 | 6971.00 | 6971.00 | 1 |
| visual | draft_acceptance (ratio) | 0.82 | 0.82 | 0.82 | 1 |
| visual | mtp_accept_len (tokens) | 2.98 | 2.98 | 2.98 | 1 |
| visual | requests (requests) | 1.00 | 1.00 | 1.00 | 1 |
| visual | expert_hit_rate (ratio) | 0.94 | 0.94 | 0.94 | 1 |
| visual | cached_tokens (tokens) | 0.00 | 0.00 | 0.00 | 1 |
| visual | itl_ms_p50 (ms) | 0.03 | 0.03 | 0.03 | 1 |
| visual | itl_ms_p95 (ms) | 53.45 | 53.45 | 53.45 | 1 |
| visual | itl_ms_p99 (ms) | 58.44 | 58.44 | 58.44 | 1 |
| visual | energy_j_per_token (J/token) | 3.59 | 3.59 | 3.59 | 1 |
|  | vram_used_mib (MiB) | 11591.00 | 11473.00 | 11591.00 | 224 |
|  | gpu_util_percent (%) | 51.12 | 0.00 | 100.00 | 224 |
|  | power_w (W) | 112.35 | 47.75 | 130.16 | 224 |
|  | temperature_c (°C) | 67.00 | 46.00 | 67.00 | 224 |
|  | cpu_util_percent (%) | 50.47 | 10.74 | 51.72 | 111 |
|  | ram_used_mib (MiB) | 42815.78 | 42620.44 | 42815.78 | 112 |
