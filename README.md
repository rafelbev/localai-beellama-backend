# localai-beellama-backend

A [LocalAI](https://localai.io) backend wrapping the
[beellama.cpp](https://github.com/Anbeeld/beellama.cpp) server — a fork of
llama.cpp with KVarN KV-cache quantization, KV cache precision tail, and
low-bit quants (longer context in the same VRAM).

## What

A LocalAI backend (see
[LocalAI's backend docs](https://localai.io/docs/backends/index.html#creating-a-backend))
built FROM the published beellama server image
`ghcr.io/anbeeld/beellama.cpp:server-cuda13-v0.4.9`. The backend image adds a
thin **gRPC shim** that implements the LocalAI backend interface
(`service Backend` in `backend/backend.proto`) on top of the base image's
bundled `llama-server` binary (OpenAI-compatible HTTP):

- `LoadModel` → spawns `llama-server` with the model's flags
- `Predict` / `PredictStream` → `/v1/chat/completions` (stream chunks are
  forwarded as separate gRPC `Reply`s)
- `Embedding` → `/v1/embeddings`
- `Free` → kills the child process group
- `Health` → the server's `/health`

The backend is named **`beellama`** (alias `beellama`) and is distributed as
the OCI image `ghcr.io/rafelbev/localai-beellama-backend` (semver tags +
`latest`), installable from a backend gallery — see `gallery.yaml` (added in
a follow-up) and the LocalAI
[gallery docs](https://localai.io/docs/backends/index.html#backends-gallery).

## Why

LocalAI's built-in llama-cpp backend is a hard C++ build of llama.cpp.
beellama.cpp's value (KVarN + KV precision tail) lives in the engine, not in
the serving layer, so this backend takes the thin-shim approach: no C++
rebuild of the fork, fast iteration, and updating beellama is just a base
image tag bump. The gRPC surface is a hard LocalAI contract
(verified against LocalAI source), so the shim is the whole of the backend.

## How to build

The backend is built by the multi-stage `Dockerfile`:

```
Dockerfile            # Stage 1 builds the Go shim; Stage 2 drops it into the
                      #   base image with run.sh + metadata.json
run.sh                # LocalAI entry point: exec "$CURDIR"/beellama-shim "$@"
metadata.json         # minimal install metadata (LocalAI rewrites at install)
backend/backend.proto # LocalAI backend gRPC contract (verbatim, + go_package)
backend/*.go          # the gRPC shim (main, server, shim, options)
backend/go.mod        # Go module
gallery.yaml          # backend gallery entry (served via a `github:` URI)
```

Stage 1 (`golang:alpine`) runs `protoc` over `backend.proto` to generate the
`pb` package, then builds a static `beellama-shim` binary. Stage 2 copies
`beellama-shim`, `run.sh`, and `metadata.json` into the image root of
`ghcr.io/anbeeld/beellama.cpp:server-cuda13-v0.4.9`.

`run.sh` receives `--addr 0.0.0.0:<port>` from the LocalAI core (which dials
`127.0.0.1:<port>` over gRPC and polls `Health`), so the shim always parses
`--addr` and binds there. On `LoadModel` the shim spawns the bundled
`app/llama-server` (relative to the backend dir), waits for its `/health`
endpoint to report ready, then serves `Predict`/`PredictStream`/`Embedding`
by proxying to the server's OpenAI-compatible HTTP API on a loopback port it
picks itself.

## Docs

- LocalAI backends: <https://localai.io/docs/backends/index.html>
- LocalAI private galleries/credentials:
  <https://localai.io/docs/advanced/private-sources>
- beellama.cpp: <https://github.com/Anbeeld/beellama.cpp>
- Reference backend implementations: LocalAI's
  [cpp/llama-cpp](https://github.com/mudler/LocalAI/tree/master/backend/cpp/llama-cpp)
  and
  [python template](https://github.com/mudler/LocalAI/tree/master/backend/python/common/template)

## CI

GitHub Actions on a self-hosted scale set (see the `garage-ops` repo,
`kubernetes/apps/github-runners/`): `.github/workflows/ci.yaml` runs on the
`localai-beellama-backend` runner scale set (dind mode) and, on every push to
`main`, builds the image, publishes it to
`ghcr.io/rafelbev/localai-beellama-backend:latest`, pulls the published image
back (verifying registry read), and runs a CPU-only smoke test
(`smoke/test.sh`): it downloads Qwen2-0.5B q4_k_m (~380 MB), runs the backend
container, and drives the gRPC shim with `smokeclient`
(Health, LoadModel, Predict, Status, Free). Tagged releases (`vX.Y.Z`) are
published under their tag name. CPU-only is deliberate — T3 verified the
small model runs without a GPU, and the runner pod has no GPU.

## License

MIT — matching upstream beellama.cpp. See [LICENSE](LICENSE).
