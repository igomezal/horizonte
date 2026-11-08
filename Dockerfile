# syntax=docker/dockerfile:1
#
# Multi-platform image (linux/amd64, linux/arm64):
#
#   docker buildx build --platform linux/amd64,linux/arm64 -t horizonte:latest .
#
# The database lives in /data, a volume: mount a named volume or a host
# directory there so plans survive container restarts and upgrades.

# Build on the host's native platform and cross-compile for the target. The
# SQLite driver (modernc.org/sqlite) is pure Go, so no C toolchain or
# emulation is needed.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY cmd ./cmd
COPY internal ./internal
COPY ui ./ui

ARG TARGETOS TARGETARCH
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.release=horizonte@${VERSION}" \
      -o /out/horizonte ./cmd/server

# Empty data directory owned by the runtime user (distroless has no shell to
# create it). A new named volume copies this ownership on first use.
RUN mkdir -p /out/data

# Runtime: no shell or package manager, just CA certificates (for Sentry),
# time zone data and a non-root user (uid 65532).
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/horizonte /usr/local/bin/horizonte
COPY --from=build --chown=65532:65532 /out/data /data

USER 65532:65532
VOLUME /data
EXPOSE 8347

# Inside a container the server must listen on all interfaces; keep it
# private by publishing the port on localhost only (see compose.yaml).
ENTRYPOINT ["/usr/local/bin/horizonte", "-addr", "0.0.0.0:8347", "-db", "/data/horizonte.db"]

# The port opens after the simulation warm-up (60 s at most), plus a margin.
HEALTHCHECK --interval=30s --timeout=5s --start-period=90s --retries=3 \
  CMD ["/usr/local/bin/horizonte", "-healthcheck", "-addr", "0.0.0.0:8347"]

# On `docker stop` the server gets SIGTERM, finishes in-flight requests and
# closes the database cleanly (5 s, inside Docker's default 10 s grace).
STOPSIGNAL SIGTERM
