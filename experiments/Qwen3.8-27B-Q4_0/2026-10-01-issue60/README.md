# 2026-10-01-issue60

- kind: benchmark
- model: Qwen3.8-27B-Q4_0 (`sha256:a2d596d8334dddbca700a03da8c65f1180f26c45c6fc8a8767e262b9ba510428`)
- runtime: llamacpp
- args: --ctx-size 8192
- gpu: NVIDIA GeForce RTX 3060, NVIDIA GeForce RTX 3060 (driver 595.91.07)
- started: 2026-10-01T07:24:30Z (49s)
- measurement valid: true

| case | metric | value | min | max | n |
|---|---|---|---|---|---|
| short | ttft_ms (ms) | 194.15 | 193.75 | 5882.83 | 3 |
| short | latency_ms (ms) | 13079.25 | 13077.95 | 18769.70 | 3 |
| short | decode_tok_per_s (tok/s) | 19.79 | 19.79 | 19.79 | 3 |
| short | prefill_tok_per_s (tok/s) | 25274.72 | 834.12 | 25326.77 | 3 |
| short | tokens_in (tokens) | 4907.00 | 4907.00 | 4907.00 | 3 |
| short | tokens_out (tokens) | 256.00 | 256.00 | 256.00 | 3 |
|  | vram_used_mib (MiB) | 8347.00 | 7841.00 | 8347.00 | 88 |
|  | gpu_util_percent (%) | 53.38 | 0.00 | 100.00 | 88 |
