# 2026-10-02-issue95

- kind: benchmark
- model: strata-iq2_xs-64k (`sha256:1ea678480e199a86809477636f8952377d5e250b6f7684937b36a6447888bde3`)
- runtime: strata 0.1.31
- args: --gpu 0,1
- gpu: NVIDIA GeForce RTX 3060, NVIDIA GeForce RTX 3060 (driver 595.91.07)
- started: 2026-10-02T01:03:18Z (60s)
- measurement valid: false
  - case visual repeat 1: mise install: exit status 1
.1  waiting for node@22
mise node@22.23.3 v22.23.3
mise node@22.23.3 10.9.9
mise ✓ node@22.23.3                              4.5s  node-v22.23.3-linux-x64.tar.gz
mise ████████░░░░░░░░ 2/4 · 6.0s
  npm:opencode-ai@1.18.34                   checking package  1.1s
  npm:@mariozechner/pi-coding-agent@0.73.1  checking package  1.1s
mise WARN  deprecated @mariozechner/pi-coding-agent@0.73.1: please use @earendil-works/pi-coding-agent instead going forward
mise WARN  4 transitive packages have deprecation warnings.
mise ✓ npm:@mariozechner/pi-coding-agent@0.73.1  3.7s
mise ████████████░░░░ 3/4 · 9.0s
  npm:opencode-ai@1.18.34                   resolving  4.1s  1/1 pkgs · 3.0 KiB
mise ████████████░░░░ 3/4 · 12.0s
  npm:opencode-ai@1.18.34                   resolving  7.1s  1/1 pkgs · 3.0 KiB
mise ████████████░░░░ 3/4 · 15.0s
  npm:opencode-ai@1.18.34                   resolving  10.1s  1/1 pkgs · 3.0 KiB
mise ████████████░░░░ 3/4 · 18.0s
  npm:opencode-ai@1.18.34                   resolving  13.1s  1/1 pkgs · 3.0 KiB
mise ███████████████░ 3/4 · 21.0s
  npm:opencode-ai@1.18.34                   fetching  16.1s  2/13 pkgs · 57.5 MiB
mise ✓ npm:opencode-ai@1.18.34                   17.0s
mise ████████████████ 4/4 · installed 3 tools · 1 failed in 21.9s
mise ERROR Failed to install pipx:huggingface_hub@latest: pipx is required to install pipx:huggingface_hub but was not found.

To use pipx packages with mise, you need to install pipx first:
  mise use pipx@latest

Alternatively, you can use uv/uvx by installing uv:
  mise use uv@latest

If pipx is already installed, verify it with `mise which pipx` and `pipx --version`.
mise ERROR Version: 2026.9.18 linux-x64 (2026-09-30)
mise ERROR Run with --verbose or MISE_VERBOSE=1 for more information

| case | metric | value | min | max | n |
|---|---|---|---|---|---|
|  | vram_used_mib (MiB) | 11483.00 | 11433.00 | 11483.00 | 42 |
|  | gpu_util_percent (%) | 0.00 | 0.00 | 0.00 | 42 |
|  | power_w (W) | 40.28 | 19.07 | 46.98 | 42 |
|  | temperature_c (°C) | 52.00 | 44.00 | 52.00 | 42 |
|  | cpu_util_percent (%) | 2.31 | 0.42 | 9.32 | 21 |
|  | ram_used_mib (MiB) | 43399.72 | 42330.18 | 43399.72 | 22 |
