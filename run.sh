#!/bin/sh
# LocalAI backend entry point for the beellama backend.
#
# LocalAI's core executes this script with `--addr <host:port>` and the working
# directory set to the backend dir (the directory containing this script). The
# shim binary lives alongside this script, so we exec it directly, forwarding
# all arguments.
#
# The shim then spawns the bundled llama-server (at app/llama-server, relative
# to this dir) and serves the LocalAI Backend gRPC interface on the given addr.

set -eu

CURDIR=$(cd "$(dirname "$0")" && pwd)

exec "$CURDIR"/beellama-shim "$@"
