#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
bin="$(mktemp -d)"
trap 'rm -rf "$bin"' EXIT

GOBIN="$bin" go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
GOBIN="$bin" go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2
GOBIN="$bin" go install github.com/bufbuild/buf/cmd/buf@v1.75.0

cat >"$bin/buf.gen.yaml" <<EOF
version: v2
plugins:
  - local: protoc-gen-go
    out: .
    opt: paths=source_relative
  - local: protoc-gen-go-grpc
    out: .
    opt: paths=source_relative
EOF

cd "$root"
PATH="$bin:$PATH" buf generate internal/kryptongrpc \
  --template "$bin/buf.gen.yaml" \
  --path internal/kryptongrpc/rootkeypb/rootkey.proto \
  -o internal/kryptongrpc
