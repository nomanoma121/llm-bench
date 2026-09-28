ARG LLAMA_CPP_VERSION
ARG LLMBENCH_VERSION=latest

FROM ghcr.io/nomanoma121/llmbench:${LLMBENCH_VERSION} AS llmbench

FROM ghcr.io/ggml-org/llama.cpp:server-cuda13-${LLAMA_CPP_VERSION}
RUN apt-get update \
 && apt-get install -y --no-install-recommends git ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY --from=llmbench /ko-app/llmbench /usr/local/bin/llmbench
ENV PATH=/app:$PATH LD_LIBRARY_PATH=/app
ENTRYPOINT []
USER 1000
WORKDIR /workspace
