# Strata's image built from upstream/, made writable for the sandbox user
# (uid 1000): the server keeps its settings next to its code.
ARG BASE
FROM ${BASE}
LABEL org.opencontainers.image.source=https://github.com/nomanoma121/llm-bench
RUN chown -R 1000:1000 /opt/strata
