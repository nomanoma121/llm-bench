# 2026-10-01-issue57

- kind: benchmark
- model: Qwen3.8-27B-Q4_0 (`sha256:a2d596d8334dddbca700a03da8c65f1180f26c45c6fc8a8767e262b9ba510428`)
- runtime: llamacpp
- args: --ctx-size 8192
- gpu: NVIDIA GeForce RTX 3060, NVIDIA GeForce RTX 3060 (driver 595.91.07)
- started: 2026-10-01T07:11:36Z (21s)
- measurement valid: true

| case | metric | value | min | max | n |
|---|---|---|---|---|---|
| short | ttft_ms (ms) | 0.00 | 0.00 | 0.00 | 3 |
| short | latency_ms (ms) | 0.00 | 0.00 | 0.00 | 3 |
| short | tokens_in (tokens) | 4855.00 | 4855.00 | 4855.00 | 3 |
| short | tokens_out (tokens) | 1.00 | 1.00 | 1.00 | 3 |
|  | vram_used_mib (MiB) | 8347.00 | 7841.00 | 8347.00 | 34 |
|  | gpu_util_percent (%) | 79.38 | 0.00 | 100.00 | 34 |
