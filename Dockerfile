# Stage 1: static web bundle
FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web ./
RUN npm run generate && mkdir -p /src/internal/web/dist && cp -r .output/public/. /src/internal/web/dist/

# Stage 2: Go binary with the bundle embedded
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/internal/web/dist ./internal/web/dist
ARG VERSION=docker
ARG COMMIT=unknown
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-X github.com/phenixrizen/conductor/internal/version.Version=${VERSION} -X github.com/phenixrizen/conductor/internal/version.Commit=${COMMIT}" \
    -o /out/conductor ./cmd/conductor

# Stage 3: runtime. Agent CLIs (claude, codex, agy) are NOT included; install
# them into a derived image, or run them on developer machines with
# `conductor host` and use this image for the server only.
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates bash git curl \
    && rm -rf /var/lib/apt/lists/* \
    && useradd -m -u 10001 conductor \
    && install -d -o conductor -g conductor -m 0700 /var/lib/conductor
COPY --from=build /out/conductor /usr/local/bin/conductor
COPY LICENSE NOTICE THIRD_PARTY_NOTICES /usr/share/doc/conductor/
USER conductor
WORKDIR /home/conductor
# Sessions run under /home/conductor. The data directory (catalog.json with the
# env secrets of agents added in the UI) stays outside it, on a volume so it
# survives the container.
ENV CONDUCTOR_LISTEN=:8080 CONDUCTOR_ALLOWED_ROOTS=/home/conductor CONDUCTOR_DEFAULT_CWD=/home/conductor \
    CONDUCTOR_DATA_DIR=/var/lib/conductor
VOLUME /var/lib/conductor
EXPOSE 8080
ENTRYPOINT ["conductor", "serve"]
