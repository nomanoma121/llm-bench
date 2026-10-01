# syntax=docker/dockerfile:1
# FreeToken from upstream/, with its CUDA kernels compiled into the image for
# the RTX 3060 (sm_86): without the kernel cache they are compiled on first
# use, which would land in the measured requests.
FROM nvidia/cuda:13.0.0-devel-ubuntu24.04
LABEL org.opencontainers.image.source=https://github.com/nomanoma121/llm-bench
COPY --from=ghcr.io/astral-sh/uv:0.9 /uv /usr/local/bin/uv
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl git \
    && rm -rf /var/lib/apt/lists/*
ENV UV_PYTHON_INSTALL_DIR=/opt/python UV_LINK_MODE=copy
WORKDIR /opt/freetoken
COPY upstream .
RUN uv venv --python 3.12 .venv && uv pip install --python .venv/bin/python ".[accel]"
RUN FREETOKEN_KERNEL_CACHE_ARCHES=8.6 FREETOKEN_KERNEL_CACHE_BUILD_DIR=/tmp/kc \
      uv build --wheel --no-build-isolation --python .venv/bin/python --out-dir /tmp/dist freetoken-kernel-cache \
    && uv pip install --python .venv/bin/python /tmp/dist/*.whl \
    && rm -rf /tmp/dist /tmp/kc
ENV PATH=/opt/freetoken/.venv/bin:$PATH
# The sandbox runs as uid 1000.
RUN chown -R 1000:1000 /opt/freetoken
