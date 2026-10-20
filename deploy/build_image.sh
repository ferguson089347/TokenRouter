#!/usr/bin/env bash
# build_image.sh builds the repository's application image with the shared
# Dockerfile and tags it for kiroo92's GHCR package. Local builds use the same
# image name as the deployment templates and the release workflow.

set -euo pipefail

# SCRIPT_DIR and REPO_ROOT locate the shared Dockerfile and build context when
# this script is invoked from a directory outside the repository.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

docker build -t ghcr.io/kiroo92/tokenrouter:latest \
    --build-arg GOPROXY=https://goproxy.cn,direct \
    --build-arg GOSUMDB=sum.golang.google.cn \
    -f "${REPO_ROOT}/Dockerfile" \
    "${REPO_ROOT}"
