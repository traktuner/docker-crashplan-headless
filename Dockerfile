#
# crashplan-headless: the CrashPlan Linux service without GUI, VNC or Electron.
#
# Build args come from versions.env (see scripts/build.sh and CI).

ARG CRASHPLAN_VERSION=
ARG CRASHPLAN_BUILD=
ARG CRASHPLAN_SHA256=
ARG SOURCE_REVISION=main
# Compile for the vendor's Java 25 runtime with --release 25.
# Image tests verify the builder's launcher with the bundled vendor runtime.
ARG TEMURIN_IMAGE=docker.io/library/eclipse-temurin:26-jdk-noble@sha256:9c0e8eddecccc0d5679b317b0c90fa7103ad25cae33df1c00280675eb681c7f8
ARG DEBIAN_IMAGE=docker.io/library/debian:trixie-slim@sha256:a72b4433de1cf7d6bae0c4cee74d0547f58601a643b3375f070c51600d63b6b2
ARG NODE_IMAGE=docker.io/library/node:26.13.1-bookworm-slim@sha256:5df79989ed685e09f0a98019db4cebfb22fb9df1a53c0161664a011f7ff6b0f1
ARG GO_IMAGE=docker.io/library/golang:1.27.4-bookworm@sha256:4fa7c968ba93f1635670522dbb01a2c78150443e1385746924566079f36570ea

# --- 1. download and unpack CrashPlan, drop GUI and updater -----------------
FROM ${DEBIAN_IMAGE} AS crashplan
ARG CRASHPLAN_VERSION
ARG CRASHPLAN_BUILD
ARG CRASHPLAN_SHA256
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl \
 && rm -rf /var/lib/apt/lists/*
WORKDIR /work
RUN set -eux; \
    test -n "$CRASHPLAN_VERSION" && test -n "$CRASHPLAN_BUILD" && test -n "$CRASHPLAN_SHA256"; \
    url="https://download.crashplan.com/installs/agent/cloud/${CRASHPLAN_VERSION}/${CRASHPLAN_BUILD}/install/CrashPlan_${CRASHPLAN_VERSION}_${CRASHPLAN_BUILD}_Linux.tgz"; \
    curl -fsSL -o crashplan.tgz "$url"; \
    echo "${CRASHPLAN_SHA256}  crashplan.tgz" | sha256sum -c -; \
    tar -xzf crashplan.tgz; \
    grep -qx "cp_version_name=${CRASHPLAN_VERSION}" crashplan-install/cp-version; \
    mkdir app; \
    tar -xzf crashplan-install/CrashPlan.tgz -C app; \
    rm -rf app/electron app/app.asar app/upgrade \
           app/bin/desktop.sh app/bin/crashplan.desktop app/bin/icon_app4.png app/log; \
    test -f app/lib/cpg-agent-service.jar; \
    test -f app/jre/lib/server/libjvm.so

# --- 2. LD_PRELOAD shim (update block, kernel version) ------------------------
FROM ${DEBIAN_IMAGE} AS libwrapper
RUN apt-get update \
 && apt-get install -y --no-install-recommends gcc libc6-dev \
 && rm -rf /var/lib/apt/lists/*
COPY src/libwrapper/libwrapper.c /src/libwrapper.c
RUN gcc -O2 -Wall -Werror -fPIC -shared -o /libwrapper.so /src/libwrapper.c -ldl && strip /libwrapper.so

# --- 3. Java agent, self-tested against the CrashPlan jars ---------------------
FROM ${TEMURIN_IMAGE} AS agent
COPY agent /agent
COPY --from=crashplan /work/app/lib /crashplan/lib
RUN set -eux; \
    javac --release 25 -Xlint:all,-path -Werror -cp "/crashplan/lib/*" -d /agent/out $(find /agent/src -name '*.java'); \
    java -cp "/agent/out:/crashplan/lib/*" cph.SelfTest; \
    javac --release 25 -Xlint:all -Werror -d /agent/test-out /agent/test/cph/ProductionAgentTest.java; \
    javac --release 25 -Xlint:all -Werror -d /agent/test-out /agent/test/cph/NativeContracts.java /agent/test/cph/NativeContractsTest.java; \
    java -cp '/agent/test-out:/crashplan/lib/*' cph.NativeContractsTest; \
    javac --release 25 -Xlint:all,-path -Werror -cp "/agent/out:/crashplan/lib/*" -d /agent/test-out $(find /agent/test -name '*.java'); \
    mkdir -p /agent/release/cph; \
    cp /agent/out/cph/Agent*.class /agent/out/cph/Patches*.class /agent/out/cph/RestoreBoundary*.class /agent/release/cph/; \
    cp -a /agent/out/cph/archive /agent/release/cph/; \
    jar --create --file /cph-agent.jar --manifest /agent/MANIFEST.MF -C /agent/release .; \
    java --add-opens=java.base/java.io=ALL-UNNAMED --add-opens=java.base/java.util=ALL-UNNAMED --add-opens=java.base/java.lang=ALL-UNNAMED --add-opens=java.base/java.security=ALL-UNNAMED -cp "/agent/test-out:/crashplan/lib/*" cph.NativeShapesTest; \
    CPH_PATCHES=none java -javaagent:/cph-agent.jar -cp "/agent/test-out:/crashplan/lib/*" cph.RestoreBoundaryTest; \
    CPH_PATCHES=none java -javaagent:/cph-agent.jar -cp "/agent/test-out:/crashplan/lib/*" cph.DirectRestoreBoundaryTest; \
    CPH_PATCHES=none java -javaagent:/cph-agent.jar -cp "/agent/test-out:/crashplan/lib/*" cph.RestoreStrategiesTest; \
    CPH_PATCHES=none java -javaagent:/cph-agent.jar -cp "/agent/test-out:/crashplan/lib/*" cph.RestoreRootAvailabilityTest; \
    CPH_PATCHES=none java -javaagent:/cph-agent.jar -cp "/agent/test-out:/crashplan/lib/*" cph.RestoreLinkSerializationTest; \
    CPH_PATCHES=none java -javaagent:/cph-agent.jar -cp "/agent/test-out:/crashplan/lib/*" cph.NativeQueryEchoTest; \
    CPH_PATCHES=none java --add-opens=java.base/java.io=ALL-UNNAMED --add-opens=java.base/java.util=ALL-UNNAMED -Xverify:all -javaagent:/cph-agent.jar -cp "/agent/test-out:/crashplan/lib/*" cph.NativeSearchContextTest; \
    CPH_PATCHES=none java -Xverify:all -javaagent:/cph-agent.jar -cp "/agent/test-out:/crashplan/lib/*" cph.archive.ArchiveRequestContextTest; \
    CPH_PATCHES=none java -Xverify:all -javaagent:/cph-agent.jar -cp "/agent/test-out:/crashplan/lib/*" cph.archive.ArchiveResponseContextTest; \
    CPH_PATCHES=none java -Xverify:all -javaagent:/cph-agent.jar -cp "/agent/test-out:/crashplan/lib/*" cph.archive.ArchiveRegistryBoundsTest; \
    CPH_PATCHES=none java -Xverify:all -javaagent:/cph-agent.jar -cp "/agent/test-out:/crashplan/lib/*" cph.archive.ArchiveVersionPagesTest; \
    CPH_PATCHES=none java -Xverify:all -javaagent:/cph-agent.jar -cp "/agent/test-out:/crashplan/lib/*" cph.archive.ArchiveNativeCallerTest; \
    CPH_PATCHES=none java -Xverify:all -javaagent:/cph-agent.jar -cp "/agent/test-out:/crashplan/lib/*" cph.archive.ArchiveChildrenPagesTest; \
    CPH_PATCHES=none java -Xverify:all -javaagent:/cph-agent.jar -cp "/agent/test-out:/crashplan/lib/*" cph.archive.ArchiveOptionalFieldsTest; \
    CPH_CONTROL=1 CPH_DIAG_INTERVAL_SECONDS=1 CPH_PATCHES=no-file-watcher,no-watcher-walk \
    java -javaagent:/cph-agent.jar -cp /agent/test-out cph.ProductionAgentTest

# Build the original product SPA. Node is absent from the runtime.
FROM ${NODE_IMAGE} AS web
ARG SOURCE_REVISION
ENV VITE_SOURCE_REVISION=${SOURCE_REVISION}
WORKDIR /work/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm test && npm run build

FROM ${GO_IMAGE} AS control
ENV CGO_ENABLED=0 GOTOOLCHAIN=local
WORKDIR /work
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY --from=web /work/internal/ui/dist/ ./internal/ui/dist/
RUN go test ./... && go vet ./... \
 && go build -trimpath -ldflags="-s -w -buildid=" -o /out/cphd ./cmd/cphd \
 && go build -trimpath -ldflags="-s -w -buildid=" -o /out/cphctl ./cmd/cphctl

FROM control AS control-test
RUN CGO_ENABLED=1 go test -race ./... -count=1

# --- 4. runtime ------------------------------------------------------------------
FROM ${DEBIAN_IMAGE}
ARG CRASHPLAN_VERSION
ARG CRASHPLAN_BUILD

RUN apt-get update \
 && apt-get install -y --no-install-recommends tini ca-certificates procps \
 && rm -rf /var/lib/apt/lists/*

ENV CP_DIR=/usr/local/crashplan
COPY --from=crashplan /work/app ${CP_DIR}
# The bundled JRE has no bin/ (the stock launcher embeds libjvm). Borrow the
# plain java launcher and verify it against the bundled runtime below.
COPY --from=agent /opt/java/openjdk/bin/java ${CP_DIR}/jre/bin/java
COPY --from=libwrapper /libwrapper.so ${CP_DIR}/nlib/libwrapper.so
COPY --from=agent /cph-agent.jar ${CP_DIR}/cph/cph-agent.jar
COPY rootfs/usr/local/bin/cph-launch /usr/local/bin/cph-launch
COPY --from=control /out/cphd /out/cphctl /usr/local/bin/
RUN printf '#!/bin/sh\nexec /usr/local/bin/cphctl engine restart\n' > /usr/local/crashplan/bin/restartLinux.sh
COPY LICENSES /usr/share/doc/crashplan-headless/
COPY LICENSE /usr/share/doc/crashplan-headless/LICENSE

RUN set -eux; \
    chmod 0755 /usr/local/bin/cph-* ${CP_DIR}/bin/restartLinux.sh ${CP_DIR}/jre/bin/java; \
    # The launcher must run with the bundled runtime, not with its own.
    ${CP_DIR}/jre/bin/java -XshowSettings:properties -version 2>&1 | grep -q "java.home = ${CP_DIR}/jre"; \
    # Every native library must resolve inside the image.
    missing="$(for f in ${CP_DIR}/nlib/*.so ${CP_DIR}/jre/lib/*.so ${CP_DIR}/jre/lib/server/libjvm.so ${CP_DIR}/jre/bin/java; do \
        LD_LIBRARY_PATH=${CP_DIR}/nlib:${CP_DIR}/jre/lib/server:${CP_DIR}/jre/lib ldd "$f" | grep 'not found' | sed "s|^|$f: |"; done || true)"; \
    if [ -n "$missing" ]; then echo "$missing"; exit 1; fi; \
    # Same /config layout as jlesage/crashplan-pro, so its volume can be reused.
    mkdir -p /defaults; \
    mv ${CP_DIR}/conf /defaults/conf; \
    echo "${CRASHPLAN_VERSION}" > /defaults/cp_version; \
    echo "${CRASHPLAN_BUILD}" > /defaults/cp_build; \
    sha256sum ${CP_DIR}/bin/restore-tool | cut -d' ' -f1 > /defaults/restore-tool.sha256; \
    ln -s /config/conf ${CP_DIR}/conf; \
    ln -s /config/cache ${CP_DIR}/cache; \
    ln -s /config/log ${CP_DIR}/log; \
    ln -s /config/repository/metadata ${CP_DIR}/metadata; \
    ln -s /config/var /var/lib/crashplan; \
    # Keep the updater from ever installing anything.
    touch ${CP_DIR}/upgrade; chmod 0400 ${CP_DIR}/upgrade; \
    rm -f /etc/machine-id; ln -s /config/machine-id /etc/machine-id; \
    ln -s /config/.crashplan /root/.crashplan; \
    # A container started directly as another user creates its private
    # runtime directory here when /run is not a tmpfs.
    chmod 1777 /run

# File names with umlauts need a UTF-8 locale (sun.jnu.encoding follows it).
ENV LANG=C.UTF-8 \
    LC_ALL=C.UTF-8 \
    CPH_PATCHES=no-file-watcher,no-watcher-walk

VOLUME ["/config"]
HEALTHCHECK --interval=60s --timeout=5s --start-period=120s --retries=3 CMD ["/usr/local/bin/cphctl", "health"]
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/cphd"]

LABEL org.opencontainers.image.title="crashplan-headless" \
      org.opencontainers.image.description="CrashPlan Linux service without GUI, with optional web interface and scan-based local backup" \
      org.opencontainers.image.source="https://github.com/traktuner/docker-crashplan-headless" \
      dev.cph.crashplan.version="${CRASHPLAN_VERSION}" \
      dev.cph.crashplan.build="${CRASHPLAN_BUILD}"
