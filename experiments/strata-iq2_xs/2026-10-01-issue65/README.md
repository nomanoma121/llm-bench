# 2026-10-01-issue65

- kind: benchmark
- model: strata-iq2_xs (`sha256:7ce577aeb726a7ff77bfa085d5fb3f2f38bb6e1b4bc55b506611ae60598045e7`)
- runtime: strata
- args: --gpu 0
- gpu: NVIDIA GeForce RTX 3060, NVIDIA GeForce RTX 3060 (driver 595.91.07)
- started: 2026-10-01T14:56:45Z (533s)
- measurement valid: true

| case | metric | value | min | max | n |
|---|---|---|---|---|---|
| visual | ttft_ms (ms) | 6644.42 | 6644.42 | 6644.42 | 1 |
| visual | latency_ms (ms) | 503709.38 | 503709.38 | 503709.38 | 1 |
| visual | decode_tok_per_s (tok/s) | 49.44 | 49.44 | 49.44 | 1 |
| visual | prefill_tok_per_s (tok/s) | 738.51 | 738.51 | 738.51 | 1 |
| visual | tokens_in (tokens) | 4907.00 | 4907.00 | 4907.00 | 1 |
| visual | tokens_out (tokens) | 24576.00 | 24576.00 | 24576.00 | 1 |
| visual | draft_acceptance (ratio) | 0.74 | 0.74 | 0.74 | 1 |
| visual | mtp_accept_len (tokens) | 2.20 | 2.20 | 2.20 | 1 |
| visual | expert_hit_rate (ratio) | 0.79 | 0.79 | 0.79 | 1 |
| visual | cached_tokens (tokens) | 0.00 | 0.00 | 0.00 | 1 |
| visual | itl_ms_p50 (ms) | 0.03 | 0.03 | 0.03 | 1 |
| visual | itl_ms_p95 (ms) | 61.03 | 61.03 | 61.03 | 1 |
| visual | itl_ms_p99 (ms) | 69.78 | 69.78 | 69.78 | 1 |
| visual | energy_j_per_token (J/token) | 3.75 | 3.75 | 3.75 | 1 |
|  | vram_used_mib (MiB) | 11583.00 | 1.00 | 11583.00 | 1006 |
|  | gpu_util_percent (%) | 49.67 | 0.00 | 100.00 | 1006 |
|  | power_w (W) | 91.81 | 22.00 | 169.34 | 1006 |
|  | temperature_c (°C) | 72.00 | 41.00 | 72.00 | 1006 |
|  | cpu_util_percent (%) | 50.59 | 4.64 | 54.95 | 502 |
|  | ram_used_mib (MiB) | 41895.39 | 41539.21 | 41895.39 | 503 |
