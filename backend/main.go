// beellama-shim: a thin LocalAI backend that wraps the bundled llama-server.
//
// LocalAI's core dials this process over gRPC (see backend/backend.proto) and
// drives it with the Backend service. This shim translates the gRPC surface
// to the llama-server OpenAI-compatible HTTP API and manages exactly one
// llama-server child process per model load.
package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"

	pb "github.com/rafelbev/localai-beellama-backend/backend/pb"
)

func main() {
	addr := flag.String("addr", "0.0.0.0:50051", "gRPC listen address (host:port)")
	flag.Parse()

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "beellama-shim: listen on %s: %v\n", *addr, err)
		os.Exit(1)
	}

	srv := grpc.NewServer()
	b := newShimBackend()
	pb.RegisterBackendServer(srv, b)

	// Shut the child down (and stop serving) on SIGTERM/SIGINT.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-stop
		fmt.Fprintln(os.Stderr, "beellama-shim: shutting down")
		b.Shutdown()
		srv.GracefulStop()
	}()

	fmt.Fprintf(os.Stderr, "beellama-shim: serving gRPC on %s\n", *addr)
	if err := srv.Serve(ln); err != nil {
		fmt.Fprintf(os.Stderr, "beellama-shim: serve: %v\n", err)
		os.Exit(1)
	}
}
