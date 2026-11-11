# syntax=docker/dockerfile:1

# beellama LocalAI backend image.
#
# Stage 1 builds the thin Go gRPC shim (a static binary) from the LocalAI
# backend.proto. Stage 2 drops the shim + run.sh + metadata.json into the
# published beellama server image, which already ships the llama-server
# binary and all CUDA/CPU runtime.
#
# The resulting image's root is what LocalAI extracts into the backend dir:
#   /run.sh          entry point (LocalAI execs it with --addr <host:port>)
#   /beellama-shim   the gRPC shim binary
#   /metadata.json   minimal install metadata (LocalAI rewrites at install)
#   /app/llama-server  the bundled server the shim spawns

# --- Stage 1: build the Go shim (static binary) ---
FROM golang:1.24-alpine AS builder

# protoc + the Go gRPC codegen plugins.
RUN apk add --no-cache protoc
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.1 \
 && go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.4.0
ENV PATH=/root/go/bin:$PATH

WORKDIR /src
COPY backend/ ./
# Generate the pb package, resolve deps, build a static binary.
# protoc-gen-go emits into a nested dir derived from go_package, so we
# generate into /gen and flatten the .go files into ./pb/ (package name is
# already `pb` from the go_package leaf).
RUN mkdir -p /gen \
 && protoc --go_out=/gen --go-grpc_out=/gen backend.proto \
 && mkdir -p pb \
 && find /gen -name '*.go' -exec cp {} pb/ \; \
 && go mod tidy \
 && CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /beellama-shim .

# --- Stage 2: drop the shim into the beellama server image ---
FROM ghcr.io/anbeeld/beellama.cpp:server-cuda13-v0.4.9

COPY --from=builder /beellama-shim /beellama-shim
COPY run.sh /run.sh
COPY metadata.json /metadata.json
RUN chmod +x /run.sh /beellama-shim

# Override the base image's default entrypoint (which runs llama-server in
# router mode) so LocalAI's run.sh contract is honored: the shim is the
# process LocalAI starts, and it spawns the bundled llama-server itself.
ENTRYPOINT ["/run.sh"]
