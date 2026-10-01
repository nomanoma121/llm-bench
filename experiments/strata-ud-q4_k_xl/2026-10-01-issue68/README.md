# 2026-10-01-issue68

- kind: benchmark
- model: strata-ud-q4_k_xl (`sha256:53ae850f9d17d1353b77401dcb7c1697463509c7d4f0ea20e24e809037c7f1dd`)
- runtime: strata
- args: --gpu 0
- gpu: NVIDIA GeForce RTX 3060, NVIDIA GeForce RTX 3060 (driver 595.91.07)
- started: 2026-10-01T16:39:58Z (1283s)
- measurement valid: true

| case | metric | value | min | max | n |
|---|---|---|---|---|---|
| visual | ttft_ms (ms) | 14347.92 | 14347.92 | 14347.92 | 1 |
| visual | latency_ms (ms) | 1186939.12 | 1186939.12 | 1186939.12 | 1 |
| visual | decode_tok_per_s (tok/s) | 20.96 | 20.96 | 20.96 | 1 |
| visual | prefill_tok_per_s (tok/s) | 342.00 | 342.00 | 342.00 | 1 |
| visual | tokens_in (tokens) | 4907.00 | 4907.00 | 4907.00 | 1 |
| visual | tokens_out (tokens) | 24576.00 | 24576.00 | 24576.00 | 1 |
| visual | draft_acceptance (ratio) | 0.60 | 0.60 | 0.60 | 1 |
| visual | mtp_accept_len (tokens) | 2.21 | 2.21 | 2.21 | 1 |
| visual | expert_hit_rate (ratio) | 0.49 | 0.49 | 0.49 | 1 |
| visual | cached_tokens (tokens) | 0.00 | 0.00 | 0.00 | 1 |
| visual | itl_ms_p50 (ms) | 0.02 | 0.02 | 0.02 | 1 |
| visual | itl_ms_p95 (ms) | 146.57 | 146.57 | 146.57 | 1 |
| visual | itl_ms_p99 (ms) | 165.90 | 165.90 | 165.90 | 1 |
| visual | energy_j_per_token (J/token) | 6.28 | 6.28 | 6.28 | 1 |
|  | vram_used_mib (MiB) | 11757.00 | 1.00 | 11757.00 | 2372 |
|  | gpu_util_percent (%) | 46.62 | 0.00 | 100.00 | 2372 |
|  | power_w (W) | 65.12 | 21.98 | 129.70 | 2372 |
|  | temperature_c (°C) | 66.00 | 41.00 | 66.00 | 2372 |
|  | cpu_util_percent (%) | 47.50 | 4.55 | 51.11 | 1185 |
|  | ram_used_mib (MiB) | 78928.00 | 78686.51 | 78928.00 | 1186 |
