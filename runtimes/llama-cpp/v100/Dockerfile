# syntax=docker/dockerfile:1
# llama.cpp for Qwen3.8-Flash-Next (arch qwen4exp) on gpu-node-02's mixed GPUs:
# one Tesla V100 32GB (sm_70) and two RTX 3060 12GB (sm_86).
#
# Pinned to llama.cpp v0.5.0, not the runtimes/llama-cpp submodule's master: the
# patches below apply to that tag only. The base image is CUDA 12.9 because CUDA 13
# dropped sm_70, so a Volta build needs the last 12.x release.
#
# patches/01 is pentacoxian-dev's v100-v3 patch, unmodified, from
# https://huggingface.co/pentacoxian-dev/Qwen3.8-Flash-Next-IQ3E-Q8D-MTP-GGUF.
# It adds qwen4exp, MTP speculative decoding, the FreeToken MoE port (Apache-2.0,
# see patches/LICENSE-freetoken) and the sm_70 kernels. patches/02 is this repo's
# own work on top; README.md says what each piece measured.
FROM nvidia/cuda:12.9.1-devel-ubuntu22.04 AS build
ARG CUDA_ARCHITECTURES=70;86
ARG LLAMA_CPP_COMMIT=7fe450e19305b828c199d602c23a8337aaa1f03b
RUN apt-get update && apt-get install -y --no-install-recommends \
      build-essential ca-certificates cmake git libcurl4-openssl-dev \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
RUN git init -q . \
    && git remote add origin https://github.com/ggml-org/llama.cpp.git \
    && git fetch -q --depth 1 origin refs/tags/v0.5.0 \
    && git checkout -q FETCH_HEAD \
    && test "$(git rev-parse HEAD)" = "${LLAMA_CPP_COMMIT}"
COPY patches/ /patches/
# --verbose so a patch that stopped applying names the file instead of building
# something that merely compiles.
RUN git apply --verbose /patches/01-pentacoxian-v100-v3.patch \
    && git apply --verbose /patches/02-llm-bench-local.patch
# GGML_NATIVE is off: the build runner's CPU is not the cluster's, and the CPU half
# of the MoE needs nothing beyond AVX2, which both have.
RUN cmake -B build -DCMAKE_BUILD_TYPE=Release \
      -DGGML_CUDA=ON -DGGML_NATIVE=OFF \
      -DCMAKE_CUDA_ARCHITECTURES="${CUDA_ARCHITECTURES}" \
      -DCMAKE_CUDA_FLAGS=-Wno-deprecated-gpu-targets \
      -DLLAMA_BUILD_TESTS=OFF -DLLAMA_BUILD_EXAMPLES=OFF \
    && cmake --build build --target llama-server llama-quantize -j"$(nproc)"

FROM nvidia/cuda:12.9.1-runtime-ubuntu22.04
LABEL org.opencontainers.image.source=https://github.com/nomanoma121/llm-bench
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates libcurl4 libgomp1 python3 \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /opt/llama-cpp-v100
# The binaries and their shared libraries are all in build/bin.
COPY --from=build /src/build/bin/ bin/
COPY tools/ tools/
COPY serve.sh .
ENV LD_LIBRARY_PATH=/opt/llama-cpp-v100/bin
ENV PATH=/opt/llama-cpp-v100/bin:$PATH
# The sandbox runs as uid 1000.
RUN chown -R 1000:1000 /opt/llama-cpp-v100
ENTRYPOINT ["/opt/llama-cpp-v100/serve.sh"]
