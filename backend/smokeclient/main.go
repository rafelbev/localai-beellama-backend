// Command smokeclient is a minimal gRPC client that exercises the LocalAI
// Backend service implemented by beellama-shim. It is used for local smoke
// testing only and is NOT part of the backend image.
//
// Usage:
//
//	smokeclient -addr 127.0.0.1:50051 -model /models/qwen2.gguf \
//	  -prompt "What is the capital of France?"
//
// It performs, in order: Health, LoadModel, Predict (non-streaming), then
// prints each result. Exit code 0 on success, 1 on any failure.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/rafelbev/localai-beellama-backend/backend/pb"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:50051", "gRPC address of beellama-shim")
	model := flag.String("model", "", "model path to load (required)")
	prompt := flag.String("prompt", "What is the capital of France?", "user prompt")
	maxTokens := flag.Int("max-tokens", 64, "max tokens to generate")
	flag.Parse()

	if *model == "" {
		fmt.Fprintln(os.Stderr, "error: -model is required")
		os.Exit(1)
	}

	conn, err := grpc.NewClient(*addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()
	client := pb.NewBackendClient(conn)

	// 1. Health
	t0 := time.Now()
	hresp, err := client.Health(context.Background(), &pb.HealthMessage{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Health FAILED: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Health        : OK (%s)\n", string(hresp.Message))

	// 2. LoadModel
	t0 = time.Now()
	lresp, err := client.LoadModel(context.Background(), &pb.ModelOptions{
		Model:       *model,
		ContextSize: 2048,
		Threads:     4,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "LoadModel FAILED: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("LoadModel     : success=%v message=%q (%s)\n",
		lresp.Success, lresp.Message, time.Since(t0).Round(time.Millisecond))
	if !lresp.Success {
		fmt.Fprintln(os.Stderr, "LoadModel reported failure")
		os.Exit(1)
	}

	// 3. Predict (non-streaming)
	t0 = time.Now()
	presp, err := client.Predict(context.Background(), &pb.PredictOptions{
		Messages: []*pb.Message{{Role: "user", Content: *prompt}},
		Tokens:   int32(*maxTokens),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Predict FAILED: %v\n", err)
		os.Exit(1)
	}
	completion := string(presp.Message)
	fmt.Printf("Predict       : prompt_tokens=%d completion_tokens=%d (%s)\n",
		presp.PromptTokens, presp.Tokens, time.Since(t0).Round(time.Millisecond))
	fmt.Printf("Completion    : %q\n", completion)

	// 4. Status
	sresp, err := client.Status(context.Background(), &pb.HealthMessage{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Status FAILED: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Status        : state=%v\n", sresp.State)

	// 5. Free
	fresp, err := client.Free(context.Background(), &pb.HealthMessage{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Free FAILED: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Free          : success=%v message=%q\n", fresp.Success, fresp.Message)

	// Basic sanity: a non-empty completion.
	if completion == "" {
		fmt.Fprintln(os.Stderr, "SMOKE FAIL: empty completion")
		os.Exit(1)
	}
	fmt.Println("SMOKE PASS")
}
