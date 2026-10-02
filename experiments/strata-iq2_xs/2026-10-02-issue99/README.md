# 2026-10-02-issue99

- kind: benchmark
- model: strata-iq2_xs (`sha256:0a6f1b25385e42eb853a3da069c0fb3e0fa93e834297183311ff8c8055bc64ad`)
- runtime: strata
- args: --gpu 0,1
- gpu: NVIDIA GeForce RTX 3060, NVIDIA GeForce RTX 3060 (driver 595.91.07)
- started: 2026-10-02T01:15:24Z (562s)
- measurement valid: true

| case | metric | value | min | max | n |
|---|---|---|---|---|---|
| visual | ttft_ms (ms) | 2120.94 | 2120.94 | 2120.94 | 1 |
| visual | latency_ms (ms) | 529111.39 | 529111.39 | 529111.39 | 1 |
| visual | decode_tok_per_s (tok/s) | 54.41 | 54.41 | 54.41 | 1 |
| visual | prefill_tok_per_s (tok/s) | 286.19 | 286.19 | 286.19 | 1 |
| visual | tokens_in (tokens) | 607.00 | 607.00 | 607.00 | 1 |
| visual | tokens_out (tokens) | 28672.00 | 28672.00 | 28672.00 | 1 |
| visual | draft_acceptance (ratio) | 0.74 | 0.74 | 0.74 | 1 |
| visual | mtp_accept_len (tokens) | 2.34 | 2.34 | 2.34 | 1 |
| visual | requests (requests) | 1.00 | 1.00 | 1.00 | 1 |
| visual | expert_hit_rate (ratio) | 0.94 | 0.94 | 0.94 | 1 |
| visual | cached_tokens (tokens) | 0.00 | 0.00 | 0.00 | 1 |
| visual | itl_ms_p50 (ms) | 0.03 | 0.03 | 0.03 | 1 |
| visual | itl_ms_p95 (ms) | 55.24 | 55.24 | 55.24 | 1 |
| visual | itl_ms_p99 (ms) | 58.83 | 58.83 | 58.83 | 1 |
| visual | energy_j_per_token (J/token) | 4.19 | 4.19 | 4.19 | 1 |
|  | vram_used_mib (MiB) | 11589.00 | 11473.00 | 11589.00 | 1058 |
|  | gpu_util_percent (%) | 50.48 | 0.00 | 100.00 | 1058 |
|  | power_w (W) | 113.77 | 45.53 | 133.45 | 1058 |
|  | temperature_c (°C) | 69.00 | 45.00 | 69.00 | 1058 |
|  | cpu_util_percent (%) | 50.78 | 6.11 | 55.34 | 528 |
|  | ram_used_mib (MiB) | 42696.43 | 42372.49 | 42696.43 | 529 |
