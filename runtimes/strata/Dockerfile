# syntax=docker/dockerfile:1
# Strata's image built from upstream/. Upstream builds ggml's CPU backend for
# the build machine's CPU, which can use instructions the cluster's CPUs lack
# (the engine dies with SIGILL), so the engine is rebuilt for any AVX2 CPU, as
# Strata's own prebuilt releases are. The tree is made writable for the
# sandbox user (uid 1000): the server keeps its settings next to its code.
ARG BASE
FROM ${BASE}
LABEL org.opencontainers.image.source=https://github.com/nomanoma121/llm-bench
RUN .venv/bin/python - <<'PY'
import json, shutil
import setup
llama = setup.get_llama_cpp()
nvcc, _ = setup.find_nvcc()
archs = json.loads((setup.ROOT / "engine" / "BUILD.json").read_text())["archs"]
setup.cmake_build(setup.ROOT, setup.ROOT / "build", "strata",
    ["-DSTRATA_ENABLE_CUDA=ON", "-DSTRATA_BUILD_TESTS=OFF", "-DSTRATA_PORTABLE=ON",
     "-DCMAKE_CUDA_ARCHITECTURES=" + ";".join(map(str, archs)),
     f"-DCMAKE_CUDA_COMPILER={nvcc}", f"-DSTRATA_GGML_DIR={llama}"], None, "build-strata.bat")
shutil.copy2(setup.ROOT / "build" / setup.EXE, setup.ROOT / "engine" / setup.EXE)
PY
RUN rm -rf build && chown -R 1000:1000 /opt/strata
