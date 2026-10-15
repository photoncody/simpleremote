# syntax=docker/dockerfile:1
#
# Build from the repository root:
#   docker build -f deploy/Dockerfile -t simpleremote .
#
# Agent binaries are copied from dist/agents/ (CI fills it from the agent
# build jobs). You can also mount your own builds at /app/agents.

FROM --platform=$BUILDPLATFORM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS server
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN --mount=type=cache,target=/root/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/simpleremote-server ./server/cmd/simpleremote-server
RUN mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=server /out/simpleremote-server /app/simpleremote-server
COPY --from=server --chown=65532:65532 /out/data /data
COPY dist/agents/ /app/agents/
ENV LISTEN_ADDR=:8080 DATA_DIR=/data AGENT_DIR=/app/agents
EXPOSE 8080
VOLUME ["/data"]
USER nonroot
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["/app/simpleremote-server", "healthcheck"]
ENTRYPOINT ["/app/simpleremote-server"]
