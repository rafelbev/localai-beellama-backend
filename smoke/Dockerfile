# Builds the gRPC smoke client (backend/smokeclient) that drives the
# beellama shim through the LocalAI backend gRPC interface. Not part of
# the backend image; used only by the CI smoke test.
#
# The pb/ package is generated at build time from backend.proto, exactly
# like the main Dockerfile (kept in sync with it).
FROM golang:1.24-alpine

RUN apk add --no-cache protoc
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.1 \
 && go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.4.0
ENV PATH=/root/go/bin:$PATH

WORKDIR /src
COPY backend/ ./
RUN mkdir -p /gen \
 && protoc --go_out=/gen --go-grpc_out=/gen backend.proto \
 && mkdir -p pb \
 && find /gen -name '*.go' -exec cp {} pb/ \; \
 && go mod tidy \
 && CGO_ENABLED=0 go build -o /smokeclient ./smokeclient

ENTRYPOINT ["/smokeclient"]
