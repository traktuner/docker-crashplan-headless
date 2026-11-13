#!/usr/bin/env bash
# Use the pinned build image; vendor jars remain outside the checkout.
# Usage: scripts/test-agent.sh /absolute/path/to/vendor/lib
set -euo pipefail
cd "$(dirname "$0")/.."
builder="$(sed -n 's/^ARG TEMURIN_IMAGE=//p' Dockerfile)"
[[ "$builder" == *@sha256:* ]] || { echo "Missing pinned Java builder" >&2; exit 1; }
vendor="$(realpath "${1:?usage: test-agent.sh /path/to/vendor/lib}")"
docker run --rm --network none \
    -v "$PWD/agent:/agent:ro" -v "$vendor:/vendor:ro" \
    "$builder" bash -euc '
    mkdir /tmp/classes
    javac --release 25 -Xlint:all,-path -Werror -cp "/vendor/*" -d /tmp/classes $(find /agent/src /agent/test -name "*.java")
    printf "Premain-Class: cph.Agent\n" >/tmp/manifest
    jar cfm /tmp/agent.jar /tmp/manifest -C /tmp/classes cph
    java -javaagent:/tmp/agent.jar -cp "/tmp/classes:/vendor/*" cph.RestoreBoundaryTest
    java -javaagent:/tmp/agent.jar -cp "/tmp/classes:/vendor/*" cph.DirectRestoreBoundaryTest
    java -cp "/tmp/classes:/vendor/*" cph.SelfTest
    java -cp "/tmp/classes:/vendor/*" cph.NativeContractsTest
    java --add-opens=java.base/java.io=ALL-UNNAMED --add-opens=java.base/java.util=ALL-UNNAMED --add-opens=java.base/java.lang=ALL-UNNAMED --add-opens=java.base/java.security=ALL-UNNAMED -cp "/tmp/classes:/vendor/*" cph.NativeShapesTest
    failed=0
    for test in cph.RestoreStrategiesTest cph.RestoreRootAvailabilityTest cph.RestoreLinkSerializationTest cph.NativeQueryEchoTest cph.NativeSearchContextTest cph.archive.ArchiveRequestContextTest cph.archive.ArchiveResponseContextTest cph.archive.ArchiveRegistryBoundsTest cph.archive.ArchiveVersionPagesTest cph.archive.ArchiveNativeCallerTest cph.archive.ArchiveChildrenPagesTest cph.archive.ArchiveOptionalFieldsTest; do
      java --add-opens=java.base/java.io=ALL-UNNAMED --add-opens=java.base/java.util=ALL-UNNAMED -Xverify:all -javaagent:/tmp/agent.jar -cp "/tmp/classes:/vendor/*" "$test" || failed=1
    done
    test "$failed" -eq 0
    '
