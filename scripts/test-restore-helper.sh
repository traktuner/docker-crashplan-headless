#!/usr/bin/env bash
# Exercise packaged restore IPC and symlink output without an account or engine.
set -euo pipefail
cd "$(dirname "$0")/.."
builder="$(sed -n 's/^ARG TEMURIN_IMAGE=//p' Dockerfile)"
[[ "$builder" == *@sha256:* ]] || { echo "Missing pinned Java builder" >&2; exit 1; }
image="${1:?usage: test-restore-helper.sh image}"
work="$(mktemp -d)"
trap 'rm -f "$work/probes.jar"; rmdir "$work"' EXIT
docker run --rm --network none -v "$PWD/agent/test:/test:ro" -v "$work:/out" \
    "$builder" bash -euc '
    mkdir /tmp/classes
    javac --release 25 -Xlint:all -Werror -d /tmp/classes /test/cph/RestoreHelperTest.java
    jar --create --file /out/probes.jar -C /tmp/classes .
    '
docker run --rm --network none -v "$work/probes.jar:/test/probes.jar:ro" \
    -e CPH_PATCHES=none --entrypoint timeout "$image" 30 \
    /usr/local/crashplan/jre/bin/java --enable-native-access=ALL-UNNAMED \
    -javaagent:/usr/local/crashplan/cph/cph-agent.jar \
    -cp '/test/probes.jar:/usr/local/crashplan/lib/*' cph.RestoreHelperTest
