#!/usr/bin/env bash
# Local build with the pinned versions from versions.env.
# Usage: scripts/build.sh [image-tag]   (uses docker; podman works via its docker shim)
set -euo pipefail
cd "$(dirname "$0")/.."
set -a; . ./versions.env; set +a
tag="${1:-crashplan-headless:${CRASHPLAN_VERSION}-${CRASHPLAN_BUILD}}"
# Podman's docker shim otherwise emits OCI images and silently drops HEALTHCHECK.
# Docker ignores this Podman/Buildah environment variable.
export BUILDAH_FORMAT=docker
docker build \
    --build-arg CRASHPLAN_VERSION="$CRASHPLAN_VERSION" \
    --build-arg CRASHPLAN_BUILD="$CRASHPLAN_BUILD" \
    --build-arg CRASHPLAN_SHA256="$CRASHPLAN_SHA256" \
    -t "$tag" .
echo "$tag"
