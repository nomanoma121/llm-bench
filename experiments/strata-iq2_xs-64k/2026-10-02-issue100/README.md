# 2026-10-02-issue100

- kind: benchmark
- model: strata-iq2_xs-64k (`sha256:1ea678480e199a86809477636f8952377d5e250b6f7684937b36a6447888bde3`)
- runtime: strata 0.1.31
- args: --gpu 0,1
- gpu: NVIDIA GeForce RTX 3060, NVIDIA GeForce RTX 3060 (driver 595.91.07)
- started: 2026-10-02T01:31:35Z (37s)
- measurement valid: false
  - case visual repeat 1: harness opencode made no requests: harness exited with 1

| case | metric | value | min | max | n |
|---|---|---|---|---|---|
| visual | latency_ms (ms) | 12.98 | 12.98 | 12.98 | 1 |
| visual | tokens_in (tokens) | 0.00 | 0.00 | 0.00 | 1 |
| visual | tokens_out (tokens) | 0.00 | 0.00 | 0.00 | 1 |
| visual | requests (requests) | 0.00 | 0.00 | 0.00 | 1 |
| visual | completed (ratio) | 0.00 | 0.00 | 0.00 | 1 |
| visual | cached_tokens (tokens) | 0.00 | 0.00 | 0.00 | 1 |
|  | vram_used_mib (MiB) | 11483.00 | 11433.00 | 11483.00 | 18 |
|  | gpu_util_percent (%) | 0.00 | 0.00 | 0.00 | 18 |
|  | power_w (W) | 44.34 | 42.50 | 46.23 | 18 |
|  | temperature_c (°C) | 50.00 | 48.00 | 50.00 | 18 |
|  | cpu_util_percent (%) | 4.17 | 1.92 | 7.21 | 8 |
|  | ram_used_mib (MiB) | 43126.66 | 42271.03 | 43126.66 | 9 |
