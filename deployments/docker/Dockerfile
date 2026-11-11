# syntax=docker/dockerfile:1

# Build:  docker build -f deployments/docker/Dockerfile --build-arg VERSION=v0.1.0 -t beacon .
# Run:    docker run --rm -p 8080:8080 beacon

# Keep GO_VERSION in step with .mise.toml.
ARG GO_VERSION=1.27.4

# The build stage runs on the build machine's own platform and cross-compiles,
# so multi-platform images need no emulation.
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-trixie AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY cmd ./cmd
COPY internal ./internal

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/beacon ./cmd/beacon

# Distroless static: no shell or package manager, and the nonroot user.
FROM gcr.io/distroless/static-debian13:nonroot

COPY --from=build /out/beacon /usr/local/bin/beacon
COPY configs/beacon.example.toml /etc/beacon/beacon.toml

USER nonroot:nonroot
EXPOSE 8080

# The image has no curl, so the binary checks itself. It reads the same
# config and BEACON_* variables as the server, so it finds the right port.
HEALTHCHECK --interval=10s --timeout=5s --start-period=5s --retries=3 \
    CMD ["/usr/local/bin/beacon", "--config", "/etc/beacon/beacon.toml", "--health-check"]

ENTRYPOINT ["/usr/local/bin/beacon"]
CMD ["--config", "/etc/beacon/beacon.toml"]
