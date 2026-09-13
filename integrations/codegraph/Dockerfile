FROM debian:bookworm-slim@sha256:88200866dfff7ea7f5cbcb6ec7c8a701889efe6fe859fe64d6990e4b07ea4171
COPY codegraph-linux-x64 /opt/codegraph
COPY extract.cjs /opt/conductor/extract.cjs
COPY launcher /opt/conductor/launcher
USER 10001:10001
ENV HOME=/work/home CODEGRAPH_TELEMETRY=0 DO_NOT_TRACK=1 CODEGRAPH_NO_DAEMON=1 CODEGRAPH_NO_WATCH=1 NO_COLOR=1 NODE_NO_WARNINGS=1
WORKDIR /work
ENTRYPOINT ["/opt/conductor/launcher"]
