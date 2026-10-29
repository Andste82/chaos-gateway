# syntax=docker/dockerfile:1
#
# Multi-arch image (linux/amd64, linux/arm64) of Chaos Gateway (plan §3.7, §3.8): one image for every container of
# deploy/compose.yaml. It holds chaosgw and chaosctl, the tools the executor runs (iproute2, nftables, iptables, ethtool,
# conntrack, WireGuard, kmod for the modules it loads), the pinned Kea and BIRD, the interception proxy (mitmproxy in its own
# virtualenv with the addon of sidecars/tls-proxy, M22), tcpdump for the administrator's shell, and the files of the deployment
# the host setup copies out of it (/usr/local/share/chaosgw: compose.yaml, update.sh, host-setup.sh, env.example).
#
#   docker buildx build --platform linux/amd64,linux/arm64 -f deploy/Dockerfile .   (make image)

# the web app (plan §3.7): built once on the build platform, embedded into chaosgw below
FROM --platform=$BUILDPLATFORM node:24-bookworm-slim AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY api ../api
COPY web ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.27.1 AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY api ./api
COPY cmd ./cmd
COPY internal ./internal
COPY web/*.go ./web/
COPY --from=web /src/web/dist ./web/dist
# cross-compiled on the build platform: no emulation needed for the Go build
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -tags webui \
      -ldflags "-s -w -X github.com/Andste82/chaos-gateway/internal/version.Version=${VERSION} -X github.com/Andste82/chaos-gateway/internal/version.Commit=${COMMIT} -X github.com/Andste82/chaos-gateway/internal/version.Date=${DATE}" \
      -o /out/ ./cmd/...

FROM ubuntu:26.04@sha256:f144425ff09be612d6d9ad965196e9cdc23dae1f42110a8a11a3e9a8198759f7
# the executor runs these tools (plan §3.4; bird2 gives it `bird -p` and `birdc`, and the BIRD container
# runs the daemon from the same image; kmod gives it `modprobe` for the kernel modules the host setup did not load; tcpdump is for
# the administrator, who runs `docker compose exec exec tcpdump -i br-iot`, the gateway's own captures are AF_PACKET sockets, M17).
# Kea and BIRD are pinned (plan §3.7) so a package update cannot silently change DHCP or routing
# behaviour underneath the gateway; the version check below fails the build on a mismatch.
ARG KEA_VERSION=3.0.3-1
ARG BIRD_VERSION=2.18-1
# the interception proxy (plan §2.8, §3.7, M22): mitmproxy in a virtualenv of its own, pinned to the version the tests of the
# sidecar run against (.devcontainer/Dockerfile has the same pin); the Python of the core is none, it is the sidecar's alone
ARG MITMPROXY_VERSION=12.2.3
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates python3 python3-venv \
        iproute2 nftables iptables ethtool procps wireguard-tools "bird2=${BIRD_VERSION}" conntrack kmod tcpdump \
        "kea-dhcp4-server=${KEA_VERSION}" \
    && rm -rf /var/lib/apt/lists/* \
    && kea-dhcp4 -V | head -1 | grep -qF "${KEA_VERSION%%-*}" \
    && bird --version 2>&1 | grep -qF "${BIRD_VERSION%%-*}"
RUN python3 -m venv /opt/chaosgw/tls-proxy/venv \
    && /opt/chaosgw/tls-proxy/venv/bin/pip install --no-cache-dir "mitmproxy==${MITMPROXY_VERSION}" \
    && /opt/chaosgw/tls-proxy/venv/bin/mitmdump --version | head -1 | grep -qF "${MITMPROXY_VERSION}"
# the addon and its launcher (the container tls-proxy runs `python -m chaosgw_tlsproxy.launcher`, deploy/compose.tlsproxy.yaml)
COPY sidecars/tls-proxy/entry.py /opt/chaosgw/tls-proxy/entry.py
COPY sidecars/tls-proxy/chaosgw_tlsproxy /opt/chaosgw/tls-proxy/chaosgw_tlsproxy
COPY --from=build /out/chaosgw /out/chaosctl /usr/local/bin/
# the files of the deployment, for the host: `docker run --rm --entrypoint cat <image> /usr/local/share/chaosgw/host-setup.sh | sudo sh -s -- --image <image>`
# runs the host setup, which copies the others out of the image (deploy/host-setup.sh). compose.yaml is made from the compose files of
# deploy/ by tools/composegen and runs this image.
COPY deploy/host-setup.sh deploy/update.sh deploy/compose.yaml deploy/env.example /usr/local/share/chaosgw/
# Kea: the run_script hook (in the directory Kea allows scripts from) and the start script
COPY deploy/chaosgw-kea-hook /usr/share/kea/scripts/chaosgw-kea-hook
COPY deploy/kea-start.sh /usr/local/bin/chaosgw-kea-start
# The directories the API container (user 65532) writes, and the service namespace holder's PID file. The persistent ones are
# bind-mounted from the host in the compose file (plan §3.6, M28; the host setup makes them with these owners, and tools/ciconfig
# keeps its list equal to this one); the sockets' directories are named volumes, and a named volume that is still empty takes its owner
# and mode from the image's directory when a container first mounts it, so these must exist here with that owner (issue #57: otherwise
# the volumes are root's and the API cannot start). The secrets are the API's alone; the executor reads them as root. The
# directory of the Kea events socket is the API's too: the Kea container (group 65532) only sends to it. The
# captures (M17) are the API's volume as well: it makes one directory per capture, the executor writes into it. The
# CAs of the TLS responder (M21) are the API's, too: it makes them, the responder's container reads them.
RUN mkdir -p /var/lib/chaosgw/state /var/lib/chaosgw/secrets /var/lib/chaosgw/api /var/lib/chaosgw/service /var/lib/chaosgw/captures /var/lib/chaosgw/tlsca /run/chaosgw-svcns /run/chaosgw-kea-events \
    && chown 65532:65532 /var/lib/chaosgw/state /var/lib/chaosgw/secrets /var/lib/chaosgw/api /var/lib/chaosgw/service /var/lib/chaosgw/captures /var/lib/chaosgw/tlsca /run/chaosgw-svcns /run/chaosgw-kea-events \
    && chmod 0700 /var/lib/chaosgw/secrets \
    && chmod 0750 /run/chaosgw-kea-events
# The image's default user is the unprivileged one (M29, security review; the M1 item "no non-root user"): a container started from
# it without a `user` (a manual `docker run`, a service somebody adds to the compose file) is not root. Every service of
# deploy/compose.yaml names its user all the same, and the three that are root (the executor and the teardown with its privileges,
# BIRD and Kea for the capabilities they need) say so; internal/executor/hardening_review_test.go pins both. The commands of the
# documentation that need root (moving the old volumes) pass --user 0:0.
USER 65532:65532
LABEL org.opencontainers.image.source="https://github.com/Andste82/chaos-gateway" \
      org.opencontainers.image.documentation="https://github.com/Andste82/chaos-gateway/blob/main/docs/install.md"
ENTRYPOINT ["chaosgw"]
