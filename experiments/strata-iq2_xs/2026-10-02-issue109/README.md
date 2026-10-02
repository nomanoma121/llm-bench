# 2026-10-02-issue109

- kind: benchmark
- model: strata-iq2_xs (`sha256:0a6f1b25385e42eb853a3da069c0fb3e0fa93e834297183311ff8c8055bc64ad`)
- runtime: strata
- args: --gpu 0,1
- gpu: NVIDIA GeForce RTX 3060, NVIDIA GeForce RTX 3060 (driver 595.91.07)
- started: 2026-10-02T02:23:29Z (540s)
- measurement valid: true

| case | metric | value | min | max | n |
|---|---|---|---|---|---|
| visual | ttft_ms (ms) | 2053.93 | 2053.93 | 2053.93 | 1 |
| visual | latency_ms (ms) | 513773.32 | 513773.32 | 513773.32 | 1 |
| visual | decode_tok_per_s (tok/s) | 56.03 | 56.03 | 56.03 | 1 |
| visual | prefill_tok_per_s (tok/s) | 289.69 | 289.69 | 289.69 | 1 |
| visual | tokens_in (tokens) | 595.00 | 595.00 | 595.00 | 1 |
| visual | tokens_out (tokens) | 28672.00 | 28672.00 | 28672.00 | 1 |
| visual | draft_acceptance (ratio) | 0.77 | 0.77 | 0.77 | 1 |
| visual | mtp_accept_len (tokens) | 2.47 | 2.47 | 2.47 | 1 |
| visual | requests (requests) | 1.00 | 1.00 | 1.00 | 1 |
| visual | expert_hit_rate (ratio) | 0.93 | 0.93 | 0.93 | 1 |
| visual | cached_tokens (tokens) | 0.00 | 0.00 | 0.00 | 1 |
| visual | itl_ms_p50 (ms) | 0.03 | 0.03 | 0.03 | 1 |
| visual | itl_ms_p95 (ms) | 55.62 | 55.62 | 55.62 | 1 |
| visual | itl_ms_p99 (ms) | 60.08 | 60.08 | 60.08 | 1 |
| visual | energy_j_per_token (J/token) | 4.05 | 4.05 | 4.05 | 1 |
|  | vram_used_mib (MiB) | 11589.00 | 11473.00 | 11589.00 | 1026 |
|  | gpu_util_percent (%) | 50.63 | 0.00 | 100.00 | 1026 |
|  | power_w (W) | 113.42 | 46.12 | 131.73 | 1026 |
|  | temperature_c (°C) | 69.00 | 46.00 | 69.00 | 1026 |
|  | cpu_util_percent (%) | 50.75 | 8.62 | 51.49 | 512 |
|  | ram_used_mib (MiB) | 42747.87 | 42577.52 | 42747.87 | 513 |
