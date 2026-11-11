package main

import (
	"context"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/rafelbev/localai-beellama-backend/backend/pb"
)

// shimBackend implements the LocalAI Backend service. It owns at most one
// llama-server child process (spawned on LoadModel, reaped on Free/shutdown).
type shimBackend struct {
	pb.UnimplementedBackendServer

	mu   sync.Mutex
	proc *llamaProcess
}

func newShimBackend() *shimBackend { return &shimBackend{} }

// Shutdown reaps any running child. Called on SIGTERM/SIGINT.
func (b *shimBackend) Shutdown() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.proc != nil {
		b.proc.kill()
		b.proc = nil
	}
}

// Health always reports OK. The core polls this right after starting the
// process, before any model is loaded, so it must not depend on load state
// (this matches the reference C++ backend, which unconditionally returns "OK").
func (b *shimBackend) Health(context.Context, *pb.HealthMessage) (*pb.Reply, error) {
	return &pb.Reply{Message: []byte("OK")}, nil
}

// LoadModel spawns llama-server with flags derived from ModelOptions and
// blocks until the child's /health endpoint reports ready.
func (b *shimBackend) LoadModel(_ context.Context, req *pb.ModelOptions) (*pb.Result, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.proc != nil {
		return &pb.Result{Message: "model already loaded", Success: true}, nil
	}
	proc, err := startLlamaServer(req)
	if err != nil {
		return &pb.Result{Message: err.Error(), Success: false}, nil
	}
	b.proc = proc
	return &pb.Result{Message: "OK", Success: true}, nil
}

// Free reaps the child process (process group + SIGTERM).
func (b *shimBackend) Free(context.Context, *pb.HealthMessage) (*pb.Result, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.proc == nil {
		return &pb.Result{Message: "no model loaded", Success: true}, nil
	}
	b.proc.kill()
	b.proc = nil
	return &pb.Result{Message: "OK", Success: true}, nil
}

// Predict runs a non-streaming completion.
func (b *shimBackend) Predict(ctx context.Context, req *pb.PredictOptions) (*pb.Reply, error) {
	p, err := b.loaded()
	if err != nil {
		return nil, err
	}
	return p.predict(ctx, req)
}

// PredictStream streams completion tokens as individual Reply messages.
func (b *shimBackend) PredictStream(req *pb.PredictOptions, stream pb.Backend_PredictStreamServer) error {
	p, err := b.loaded()
	if err != nil {
		return err
	}
	return p.predictStream(req, stream)
}

// Embedding proxies /v1/embeddings.
func (b *shimBackend) Embedding(ctx context.Context, req *pb.PredictOptions) (*pb.EmbeddingResult, error) {
	p, err := b.loaded()
	if err != nil {
		return nil, err
	}
	return p.embedding(ctx, req)
}

// Status reports whether a model is loaded.
func (b *shimBackend) Status(context.Context, *pb.HealthMessage) (*pb.StatusResponse, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.proc == nil {
		return &pb.StatusResponse{State: pb.StatusResponse_UNINITIALIZED}, nil
	}
	return &pb.StatusResponse{State: pb.StatusResponse_READY}, nil
}

func (b *shimBackend) loaded() (*llamaProcess, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.proc == nil {
		return nil, status.Error(codes.FailedPrecondition, "no model loaded; call LoadModel first")
	}
	return b.proc, nil
}

func unimpl(name string) error {
	return status.Errorf(codes.Unimplemented, "beellama-shim: %s is not implemented", name)
}

var _ = pb.StatusResponse_READY
