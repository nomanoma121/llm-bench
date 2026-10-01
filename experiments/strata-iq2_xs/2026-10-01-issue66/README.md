# 2026-10-01-issue66

- kind: benchmark
- model: strata-iq2_xs (`sha256:7ce577aeb726a7ff77bfa085d5fb3f2f38bb6e1b4bc55b506611ae60598045e7`)
- runtime: strata
- args: --gpu 0,1
- gpu: NVIDIA GeForce RTX 3060, NVIDIA GeForce RTX 3060 (driver 595.91.07)
- started: 2026-10-01T16:26:59Z (404s)
- measurement valid: true

| case | metric | value | min | max | n |
|---|---|---|---|---|---|
| visual | ttft_ms (ms) | 6543.37 | 6543.37 | 6543.37 | 1 |
| visual | latency_ms (ms) | 373989.72 | 373989.72 | 373989.72 | 1 |
| visual | decode_tok_per_s (tok/s) | 66.88 | 66.88 | 66.88 | 1 |
| visual | prefill_tok_per_s (tok/s) | 749.92 | 749.92 | 749.92 | 1 |
| visual | tokens_in (tokens) | 4907.00 | 4907.00 | 4907.00 | 1 |
| visual | tokens_out (tokens) | 24576.00 | 24576.00 | 24576.00 | 1 |
| visual | draft_acceptance (ratio) | 0.95 | 0.95 | 0.95 | 1 |
| visual | mtp_accept_len (tokens) | 3.40 | 3.40 | 3.40 | 1 |
| visual | expert_hit_rate (ratio) | 0.92 | 0.92 | 0.92 | 1 |
| visual | cached_tokens (tokens) | 0.00 | 0.00 | 0.00 | 1 |
| visual | itl_ms_p50 (ms) | 0.03 | 0.03 | 0.03 | 1 |
| visual | itl_ms_p95 (ms) | 56.75 | 56.75 | 56.75 | 1 |
| visual | itl_ms_p99 (ms) | 59.87 | 59.87 | 59.87 | 1 |
| visual | energy_j_per_token (J/token) | 3.44 | 3.44 | 3.44 | 1 |
|  | vram_used_mib (MiB) | 11587.00 | 11469.00 | 11587.00 | 746 |
|  | gpu_util_percent (%) | 50.79 | 0.00 | 100.00 | 746 |
|  | power_w (W) | 113.45 | 45.35 | 173.19 | 746 |
|  | temperature_c (°C) | 68.00 | 45.00 | 68.00 | 746 |
|  | cpu_util_percent (%) | 50.18 | 4.56 | 51.55 | 372 |
|  | ram_used_mib (MiB) | 43466.03 | 43267.17 | 43466.03 | 373 |
