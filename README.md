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

The build system (Dockerfile, `run.sh`, the Go shim) lands in a follow-up
change. Until then the repo is skeleton + config only. Expected layout:

```
Dockerfile            # FROM ghcr.io/anbeeld/beellama.cpp:server-cuda13-v0.4.9
run.sh                # LocalAI entry point: exec "$CURDIR"/$BINARY "$@"
metadata.json         # generated at build (name/alias only)
backend/backend.proto # LocalAI backend gRPC contract
main.go, go.mod, ...   # the gRPC shim
gallery.yaml          # backend gallery entry (served via a `github:` URI)
```

`run.sh` receives `--addr 0.0.0.0:<port>` from the LocalAI core (which dials
`127.0.0.1:<port>` over gRPC and polls `Health`), so the shim always parses
`--addr` and binds there.

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
`kubernetes/apps/github-runners/`): builds the image, smoke-tests it, and
pushes to GHCR.

## License

MIT — matching upstream beellama.cpp. See [LICENSE](LICENSE).
