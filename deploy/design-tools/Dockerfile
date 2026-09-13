# The operator selects an already reviewed immutable Conductor worker image.
# Runtime inherits its sandbox entry point and non-root isolation controls.
ARG WORKER_IMAGE=conductor-design-base:required
FROM ${WORKER_IMAGE}
ARG WORKER_DIGEST
LABEL io.conductor.worker-base-digest="${WORKER_DIGEST}"
USER 0:0
RUN apt-get update -qq \
    && apt-get install --no-install-recommends -y python3=3.11.2-1+b1 python3-venv=3.11.2-1+b1 \
    && rm -rf /var/lib/apt/lists/* \
    && python3 -m venv /opt/conductor-design/venv
WORKDIR /opt/conductor-design
COPY package.json package-lock.json requirements.lock specify_cli-1.0.6-py3-none-any.whl ./
RUN npm ci --ignore-scripts --no-audit --no-fund \
    && /opt/conductor-design/venv/bin/pip install --no-cache-dir --require-hashes -r requirements.lock \
    && /opt/conductor-design/venv/bin/pip install --no-cache-dir --no-deps ./specify_cli-1.0.6-py3-none-any.whl \
    && /opt/conductor-design/venv/bin/specify --version \
    && node_modules/.bin/adr --version \
    && /opt/conductor-design/venv/bin/specify init /opt/conductor-design/scaffold --integration generic --integration-options='--commands-dir .conductor/spec-kit' --ignore-agent-tools --non-interactive \
    && rm specify_cli-1.0.6-py3-none-any.whl \
    && npm cache clean --force
COPY conductor-design-tools /usr/local/bin/conductor-design-tools
RUN chmod 0555 /usr/local/bin/conductor-design-tools
USER 10001:10001
WORKDIR /work
