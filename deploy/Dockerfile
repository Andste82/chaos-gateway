# syntax=docker/dockerfile:1
#
# Multi-arch image (linux/amd64, linux/arm64) of the Chaos Gateway core binaries (plan §3.7,
# §3.8). M1 builds what exists so far, chaosgw and chaosctl; the full image with Kea, BIRD,
# tcpdump and the mitmproxy sidecar follows with M28.
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
# runs the daemon from the same image); the full image with tcpdump follows with M28.
# Kea and BIRD are pinned (plan §3.7) so a package update cannot silently change DHCP or routing
# behaviour underneath the gateway; the version check below fails the build on a mismatch.
ARG KEA_VERSION=3.0.3-1
ARG BIRD_VERSION=2.18-1
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
        iproute2 nftables iptables ethtool procps wireguard-tools "bird2=${BIRD_VERSION}" conntrack \
        "kea-dhcp4-server=${KEA_VERSION}" \
    && rm -rf /var/lib/apt/lists/* \
    && kea-dhcp4 -V | head -1 | grep -qF "${KEA_VERSION%%-*}" \
    && bird --version 2>&1 | grep -qF "${BIRD_VERSION%%-*}"
COPY --from=build /out/chaosgw /out/chaosctl /usr/local/bin/
# run on the host, not in the container: `docker run --rm --entrypoint cat <image> /usr/local/share/chaosgw/host-setup.sh | sudo sh`
COPY deploy/host-setup.sh /usr/local/share/chaosgw/host-setup.sh
# Kea: the run_script hook (in the directory Kea allows scripts from) and the start script
COPY deploy/chaosgw-kea-hook /usr/share/kea/scripts/chaosgw-kea-hook
COPY deploy/kea-start.sh /usr/local/bin/chaosgw-kea-start
# The directories the API container (user 65532) writes, and the service namespace holder's PID file: a
# named volume that is still empty takes its owner and mode from the image's directory when a container
# first mounts it, so these must exist here with that owner (issue #57: otherwise the volumes are root's
# and the API cannot start). The secrets are the API's alone; the executor reads them as root. The
# directory of the Kea events socket is the API's too: the Kea container (group 65532) only sends to it. The
# captures (M17) are the API's volume as well: it makes one directory per capture, the executor writes into it. The
# CAs of the TLS responder (M21) are the API's, too: it makes them, the responder's container reads them.
RUN mkdir -p /var/lib/chaosgw/state /var/lib/chaosgw/secrets /var/lib/chaosgw/api /var/lib/chaosgw/service /var/lib/chaosgw/captures /var/lib/chaosgw/tlsca /run/chaosgw-svcns /run/chaosgw-kea-events \
    && chown 65532:65532 /var/lib/chaosgw/state /var/lib/chaosgw/secrets /var/lib/chaosgw/api /var/lib/chaosgw/service /var/lib/chaosgw/captures /var/lib/chaosgw/tlsca /run/chaosgw-svcns /run/chaosgw-kea-events \
    && chmod 0700 /var/lib/chaosgw/secrets \
    && chmod 0750 /run/chaosgw-kea-events
ENTRYPOINT ["chaosgw"]
