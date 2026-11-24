# syntax=docker/dockerfile:1

# The simulation's own programs, built from the local checkout so they use
# the current client. compose.yaml builds it with the repository root as
# context; Dockerfile.dockerignore keeps that context small.

# Keep GO_VERSION in step with .mise.toml.
ARG GO_VERSION=1.27.4

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-trixie AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY client ./client
COPY simulation ./simulation

ARG TARGETOS
ARG TARGETARCH

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -o /out/ ./simulation/app

FROM gcr.io/distroless/static-debian13:nonroot

COPY --from=build /out/ /usr/local/bin/

USER nonroot:nonroot
