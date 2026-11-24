#!/usr/bin/env bash
# Regenerates the Go code for the protobuf API in api/ with pinned
# protoc-gen-go and protoc-gen-go-grpc. protoc itself comes from mise.
# With --check, fails instead if the committed code is out of date.
set -euo pipefail

cd "$(dirname "$0")/.."

bin="$(mktemp -d)"
trap 'rm -rf "$bin"' EXIT

GOBIN="$bin" go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
GOBIN="$bin" go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2

PATH="$bin:$PATH" protoc -I api \
	--go_out=. --go_opt=module=github.com/nm1996/beacon \
	--go-grpc_out=. --go-grpc_opt=module=github.com/nm1996/beacon \
	api/beacon/v1/*.proto

if [[ "${1:-}" == "--check" ]] && ! git diff --quiet -- api/; then
	git diff --stat -- api/
	echo "proto: the generated code is out of date; run mise run proto and commit the result" >&2
	exit 1
fi
