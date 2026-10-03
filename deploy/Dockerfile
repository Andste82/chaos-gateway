# syntax=docker/dockerfile:1
#
# Multi-arch image (linux/amd64, linux/arm64) of the Chaos Gateway core binaries (plan §3.7,
# §3.8). M1 builds what exists so far, chaosgw and chaosctl; the full image with Kea, BIRD,
# tcpdump and the mitmproxy sidecar follows with M28.
#
#   docker buildx build --platform linux/amd64,linux/arm64 -f deploy/Dockerfile .   (make image)

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
# cross-compiled on the build platform: no emulation needed for the Go build
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath \
      -ldflags "-s -w -X github.com/Andste82/chaos-gateway/internal/version.Version=${VERSION} -X github.com/Andste82/chaos-gateway/internal/version.Commit=${COMMIT} -X github.com/Andste82/chaos-gateway/internal/version.Date=${DATE}" \
      -o /out/ ./cmd/...

FROM ubuntu:26.04
# the executor runs these tools (plan §3.4; bird2 gives it `bird -p` and `birdc`, and the BIRD container
# runs the daemon from the same image); the full image with Kea and tcpdump follows with M28
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
        iproute2 nftables iptables ethtool procps wireguard-tools bird2 conntrack kea-dhcp4-server \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/chaosgw /out/chaosctl /usr/local/bin/
# run on the host, not in the container: `docker run --rm --entrypoint cat <image> /usr/local/share/chaosgw/host-setup.sh | sudo sh`
COPY deploy/host-setup.sh /usr/local/share/chaosgw/host-setup.sh
# Kea: the run_script hook (in the directory Kea allows scripts from) and the start script
COPY deploy/chaosgw-kea-hook /usr/share/kea/scripts/chaosgw-kea-hook
COPY deploy/kea-start.sh /usr/local/bin/chaosgw-kea-start
ENTRYPOINT ["chaosgw"]
