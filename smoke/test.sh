#!/usr/bin/env bash
# CI smoke test: runs the published backend image with a small CPU model
# and drives it through the LocalAI backend gRPC interface via
# smokeclient (Health, LoadModel, Predict, Status, Free).
#
# CPU-only by design: T3 verified that Qwen2-0.5B q4_k_m runs fine without
# a GPU, and the runner pod has no GPU — no nvidia.com/gpu / runtimeClassName
# needed for CI.
#
# Requirements: a docker daemon (the scale-set runs in dind mode) and
# egress to huggingface.co.
set -euo pipefail

: "${IMAGE:=ghcr.io/rafelbev/localai-beellama-backend:latest}"

MODEL_FILE="qwen2-0_5b-instruct-q4_k_m.gguf"
MODEL_URL="https://huggingface.co/Qwen/Qwen2-0.5B-Instruct-GGUF/resolve/main/${MODEL_FILE}"

echo ">> using image ${IMAGE}"

# 1. Fetch the small CPU model (~380 MB).
MODEL_DIR="$(mktemp -d)"
curl -fSL --progress-bar -o "${MODEL_DIR}/${MODEL_FILE}" "$MODEL_URL"

# 2. Stage the model into a docker volume visible to the backend container.
#    docker cp can only target containers, not volumes — use a temp container.
docker volume create beellama-models
STAGING=$(docker create -v beellama-models:/models alpine true)
docker cp "${MODEL_DIR}/${MODEL_FILE}" "${STAGING}:/models/${MODEL_FILE}"
docker rm "${STAGING}" >/dev/null

# 3. Build the smoke client from the repo source.
docker build -f smoke/Dockerfile -t beellama-smokeclient .

# 4. Start the backend container on a private docker network.
#    The image ENTRYPOINT is /run.sh; args are passed straight to the shim.
docker network create smoke-net
docker run -d --name beellama --network smoke-net \
  -v beellama-models:/models \
  "$IMAGE" --addr 0.0.0.0:50051

cleanup() {
  docker rm -f beellama >/dev/null 2>&1 || true
  docker network rm smoke-net >/dev/null 2>&1 || true
  docker volume rm beellama-models >/dev/null 2>&1 || true
  rm -rf "${MODEL_DIR}"
}
trap cleanup EXIT

# 5. Wait for the gRPC shim to come up, then run the smoke client.
#    smokeclient exits 0 on SMOKE PASS, 1 on any failure.
deadline=$(( $(date +%s) + 300 ))
until docker run --rm --network smoke-net \
    beellama-smokeclient \
    -addr beellama:50051 \
    -model "/models/${MODEL_FILE}" \
    -max-tokens 64; do
  if [ "$(date +%s)" -ge "$deadline" ]; then
    echo ">> smoke client failed; backend container log:" >&2
    docker logs beellama >&2 || true
    exit 1
  fi
  sleep 5
done

echo ">> SMOKE PASS: ${IMAGE}"
