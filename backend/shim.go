package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	pb "github.com/rafelbev/localai-beellama-backend/backend/pb"
)

// llamaProcess wraps a running llama-server child and the HTTP client used to
// talk to its OpenAI-compatible API.
type llamaProcess struct {
	cmd    *exec.Cmd
	port   int
	client *http.Client
}

// findLlamaServer locates the bundled llama-server binary.
//
// The image root is extracted by LocalAI directly into the backend dir, so
// the shim binary and the base image's /app/llama-server are siblings under
// that dir (i.e. <backendDir>/app/llama-server). We resolve it relative to the
// shim's own executable path, with a couple of fallbacks for testing.
func findLlamaServer() (string, error) {
	if override := os.Getenv("LLAMA_SERVER_BIN"); override != "" {
		return override, nil
	}
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "app", "llama-server")
		if st, err := os.Stat(candidate); err == nil && st.Mode().IsRegular() {
			return candidate, nil
		}
	}
	if candidate, err := exec.LookPath("llama-server"); err == nil {
		return candidate, nil
	}
	return "", fmt.Errorf("could not locate llama-server (set LLAMA_SERVER_BIN to override)")
}

// findFreePort asks the OS for an ephemeral port on loopback.
func findFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port, nil
}

// startLlamaServer spawns the child and blocks until its /health endpoint is
// ready (returns HTTP 200).
func startLlamaServer(opts *pb.ModelOptions) (*llamaProcess, error) {
	if opts == nil || opts.Model == "" {
		return nil, fmt.Errorf("LoadModel: no model path provided")
	}
	bin, err := findLlamaServer()
	if err != nil {
		return nil, err
	}
	port, err := findFreePort()
	if err != nil {
		return nil, fmt.Errorf("findFreePort: %w", err)
	}

	args := buildLlamaArgs(opts, bin, port)

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = os.TempDir()
	cmd.Env = buildLlamaEnv(opts)
	// Inherit stdout/stderr so llama-server's logs surface in the container
	// log (LocalAI collects the backend process's stdout/stderr).
	cmd.Stdin = nil
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Put the child in its own process group so we can reap the whole tree.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start llama-server: %w", err)
	}

	p := &llamaProcess{
		cmd:    cmd,
		port:   port,
		client: &http.Client{Timeout: 600 * time.Second},
	}

	// Wait for readiness.
	deadline := time.Now().Add(300 * time.Second)
	client := &http.Client{Timeout: 5 * time.Second}
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	for time.Now().Before(deadline) {
		if p.childDead() {
			_ = cmd.Wait()
			return nil, fmt.Errorf("llama-server exited before becoming ready")
		}
		resp, err := client.Get(base + "/health")
		if err == nil {
			if resp.StatusCode == http.StatusOK {
				_ = resp.Body.Close()
				return p, nil
			}
			_ = resp.Body.Close()
		}
		time.Sleep(500 * time.Millisecond)
	}
	p.kill()
	return nil, fmt.Errorf("llama-server did not become ready within 300s")
}

func (p *llamaProcess) childDead() bool {
	// Signal 0 checks existence without actually signaling the process.
	if p.cmd == nil || p.cmd.Process == nil {
		return true
	}
	return syscall.Kill(p.cmd.Process.Pid, 0) == syscall.ESRCH
}

// kill terminates the child process group (SIGTERM, then SIGKILL if needed).
func (p *llamaProcess) kill() {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return
	}
	pid := p.cmd.Process.Pid
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		_, _ = p.cmd.Process.Wait()
		close(done)
	}()
	select {
	case <-done:
		return
	case <-time.After(5 * time.Second):
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
}

// buildChatBody builds an OpenAI chat-completions request body from
// PredictOptions.
func buildChatBody(req *pb.PredictOptions) (map[string]any, error) {
	messages := make([]map[string]any, 0, len(req.Messages))
	for _, m := range req.Messages {
		messages = append(messages, map[string]any{
			"role":    m.Role,
			"content": m.Content,
		})
	}
	body := map[string]any{
		"messages":    messages,
		"max_tokens":  int(req.Tokens),
		"temperature": req.Temperature,
		"top_p":       req.TopP,
		"stream":      false,
	}
	if req.TopK > 0 {
		body["top_k"] = int(req.TopK)
	}
	if req.FrequencyPenalty != 0 {
		body["frequency_penalty"] = req.FrequencyPenalty
	}
	if req.PresencePenalty != 0 {
		body["presence_penalty"] = req.PresencePenalty
	}
	if len(req.StopPrompts) > 0 {
		body["stop"] = req.StopPrompts
	}
	return body, nil
}

// predict runs a non-streaming chat completion.
func (p *llamaProcess) predict(ctx context.Context, req *pb.PredictOptions) (*pb.Reply, error) {
	body, err := buildChatBody(req)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(body)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("http://127.0.0.1:%d/v1/chat/completions", p.port), &buf)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("llama-server returned %d: %s", resp.StatusCode, truncate(string(data), 512))
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("parse completion response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return nil, fmt.Errorf("no choices in completion response")
	}
	return &pb.Reply{
		Message:       []byte(parsed.Choices[0].Message.Content),
		Tokens:        int32(parsed.Usage.CompletionTokens),
		PromptTokens:  int32(parsed.Usage.PromptTokens),
	}, nil
}

// predictStream streams chat-completion tokens as individual Reply messages.
func (p *llamaProcess) predictStream(req *pb.PredictOptions, stream pb.Backend_PredictStreamServer) error {
	body, err := buildChatBody(req)
	if err != nil {
		return err
	}
	body["stream"] = true
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(body)
	ctx := stream.Context()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("http://127.0.0.1:%d/v1/chat/completions", p.port), &buf)
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("llama-server returned %d: %s", resp.StatusCode, truncate(string(data), 512))
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
			if err := stream.Send(&pb.Reply{Message: []byte(chunk.Choices[0].Delta.Content)}); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}

// embedding proxies /v1/embeddings for a single prompt.
func (p *llamaProcess) embedding(ctx context.Context, req *pb.PredictOptions) (*pb.EmbeddingResult, error) {
	prompt := ""
	if len(req.Messages) > 0 {
		prompt = req.Messages[len(req.Messages)-1].Content
	} else {
		prompt = req.Prompt
	}
	body := map[string]any{"input": []string{prompt}}
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(body)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("http://127.0.0.1:%d/v1/embeddings", p.port), &buf)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("llama-server returned %d: %s", resp.StatusCode, truncate(string(data), 512))
	}
	var parsed struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("parse embedding response: %w", err)
	}
	if len(parsed.Data) == 0 {
		return nil, fmt.Errorf("no embedding data in response")
	}
	return &pb.EmbeddingResult{Embeddings: parsed.Data[0].Embedding}, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
