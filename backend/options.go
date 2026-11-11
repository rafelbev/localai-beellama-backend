package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/rafelbev/localai-beellama-backend/backend/pb"
)

// osEnviron returns the current process environment as a slice of KEY=VALUE
// strings (os.Environ).
func osEnviron() []string {
	return os.Environ()
}

// buildLlamaArgs builds the full command line (binary + flags) for the
// bundled llama-server from a ModelOptions message. The mapping is
// intentionally conservative: we only set the flags we are confident
// llama.cpp accepts, and we leave the rest to llama-server defaults.
//
// Field name mapping (LocalAI backend.proto ModelOptions -> llama-server flag):
//
//	Model            -> --model <path>
//	ContextSize      -> --ctx-size <n>
//	Seed             -> --seed <n>
//	NBatch           -> --batch <n>
//	MLock            -> --mlock
//	MMap             -> --mmap
//	NUMA             -> --numa
//	NGPULayers       -> --n-gpu-layers <n>
//	Threads          -> --threads <n>
//	FlashAttention   -> --flash-attn on (string field in proto)
//	EnvVars          -> environment variables (handled separately)
//
// Generation-time parameters (temperature, top_p, max_tokens, ...) are NOT
// set here; they are passed per-request in the OpenAI HTTP body by Predict.
func buildLlamaArgs(opts *pb.ModelOptions, bin string, port int) []string {
	args := []string{bin}

	// Model path is required.
	args = append(args, "--model", opts.Model)

	// Always bind to loopback; the shim proxies to it.
	args = append(args, "--host", "127.0.0.1")
	args = append(args, "--port", strconv.Itoa(port))

	// Context size.
	if opts.ContextSize > 0 {
		args = append(args, "--ctx-size", strconv.FormatInt(int64(opts.ContextSize), 10))
	}
	// Seed.
	if opts.Seed > 0 {
		args = append(args, "--seed", strconv.FormatInt(int64(opts.Seed), 10))
	}
	// Batch size.
	if opts.NBatch > 0 {
		args = append(args, "--batch", strconv.FormatInt(int64(opts.NBatch), 10))
	}
	// Memory options.
	if opts.MLock {
		args = append(args, "--mlock")
	}
	if opts.MMap {
		args = append(args, "--mmap")
	}
	// NUMA.
	if opts.NUMA {
		args = append(args, "--numa")
	}
	// GPU layers.
	if opts.NGPULayers > 0 {
		args = append(args, "--n-gpu-layers", strconv.FormatInt(int64(opts.NGPULayers), 10))
	}
	// Threads.
	if opts.Threads > 0 {
		args = append(args, "--threads", strconv.FormatInt(int64(opts.Threads), 10))
	}
	// Flash attention: proto field is a string ("on"/"off"/""), not a bool.
	if opts.FlashAttention == "on" || opts.FlashAttention == "true" {
		args = append(args, "--flash-attn", "on")
	}
	// Overrides: repeated string of extra CLI flags passed through verbatim.
	// Each element is one argument (e.g. "--flash-attn", "off").
	for _, o := range opts.Overrides {
		args = append(args, o)
	}

	return args
}

// buildLlamaEnv builds the environment for the llama-server child process.
// It inherits the current environment and adds any EnvVars from the options.
func buildLlamaEnv(opts *pb.ModelOptions) []string {
	return append(osEnviron(), envVarsToPairs(opts)...)
}

// envVarsToPairs converts a map of env vars to KEY=VALUE strings.
func envVarsToPairs(opts *pb.ModelOptions) []string {
	var out []string
	if opts == nil {
		return out
	}
	for k, v := range opts.EnvVars {
		out = append(out, k+"="+v)
	}
	return out
}

var _ = fmt.Sprintf // keep fmt imported for future use
