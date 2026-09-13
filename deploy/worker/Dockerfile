# Build conductor-sandbox outside Docker, then pipe this small build context to
# Docker. No repository credentials or host package caches enter the image.
FROM node:22.19.0-bookworm-slim@sha256:4a4884e8a44826194dff92ba316264f392056cbe243dcc9fd3551e71cea02b90
RUN apt-get update -qq \
    && apt-get install --no-install-recommends -y git=1:2.39.5-0+deb12u3 ca-certificates=20250419~deb12u1 \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /opt/conductor-assistants
COPY package.json package-lock.json ./
RUN npm ci --ignore-scripts --no-audit --no-fund \
    && node node_modules/@anthropic-ai/claude-code/install.cjs \
    && ln -s /opt/conductor-assistants/node_modules/.bin/codex /usr/local/bin/codex \
    && ln -s /opt/conductor-assistants/node_modules/.bin/claude /usr/local/bin/claude \
    && codex --version \
    && claude --version \
    && npm cache clean --force
COPY conductor-sandbox /usr/local/bin/conductor-sandbox
RUN chmod 0555 /usr/local/bin/conductor-sandbox && mkdir /work
USER 10001:10001
WORKDIR /work
ENTRYPOINT ["/usr/local/bin/conductor-sandbox"]
