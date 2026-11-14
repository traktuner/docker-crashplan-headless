#!/usr/bin/env bash
# Real native watch registrations and watcher-worker traversal; no account or dataset.
set -euo pipefail
cd "$(dirname "$0")/.."
builder="$(sed -n 's/^ARG TEMURIN_IMAGE=//p' Dockerfile)"
[[ "$builder" == *@sha256:* ]] || { echo "Missing pinned Java builder" >&2; exit 1; }
image="${1:?usage: test-watchers.sh image}"
work="$(mktemp -d)"
# The disposable directory contains only the jar produced below.
trap 'rm -f "$work/probes.jar"; rmdir "$work"' EXIT
docker run --rm --network none -v "$PWD/agent:/agent:ro" -v "$work:/out" \
    "$builder" bash -euc '
    mkdir /tmp/classes
    javac --release 25 -Xlint:all -Werror -d /tmp/classes /agent/test/cph/WatcherProbe.java
    jar --create --file /out/probes.jar -C /tmp/classes .
    '
for mode in baseline watch-only scan-only; do
    case "$mode" in
        baseline) patches='' ;;
        watch-only) patches=no-file-watcher ;;
        scan-only) patches=no-file-watcher,no-watcher-walk ;;
    esac
    docker run --rm --network none -v "$work/probes.jar:/test/probes.jar:ro" \
        -e CPH_PATCHES="$patches" \
        -e LD_LIBRARY_PATH=/usr/local/crashplan/nlib \
        --entrypoint /usr/local/crashplan/jre/bin/java "$image" \
        --enable-native-access=ALL-UNNAMED -Djna.nosys=true \
        -Djava.library.path=/usr/local/crashplan/nlib \
        -javaagent:/usr/local/crashplan/cph/cph-agent.jar \
        -cp /test/probes.jar:/usr/local/crashplan/lib/cpg-agent-service.jar \
        cph.WatcherProbe "$mode"
done
