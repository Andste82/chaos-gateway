FROM ubuntu:26.04

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl wget unzip zip git jq time pkg-config \
    ripgrep fd-find shellcheck shfmt xxd make gcc libc6-dev \
    && rm -rf /var/lib/apt/lists/*

# --- Go (official toolchain) --------------------------------------------------
ENV PATH=/usr/local/go/bin:$PATH
RUN GO_VERSION=1.27.1 && ARCH="$(dpkg --print-architecture)" \
    && curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${ARCH}.tar.gz" -o /tmp/go.tgz \
    && tar -C /usr/local -xzf /tmp/go.tgz \
    && rm /tmp/go.tgz \
    && go version

# --- GitHub CLI ---------------------------------------------------------------
RUN mkdir -p -m 755 /etc/apt/keyrings \
    && curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
        | tee /etc/apt/keyrings/githubcli-archive-keyring.gpg > /dev/null \
    && chmod go+r /etc/apt/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
        > /etc/apt/sources.list.d/github-cli.list \
    && apt-get update && apt-get install -y --no-install-recommends gh \
    && rm -rf /var/lib/apt/lists/*

# --- Network and testbed tools (plan §4.5) ------------------------------------
# Gateway stack: nftables, tc/iproute2, conntrack, ethtool, WireGuard, BIRD 2,
# Kea. Test clients: ping, dig, curl, openssl, iperf3, mosquitto, udhcpc
# (busybox), socat, netcat. Capture: tcpdump, tshark. dnsmasq is the upstream
# resolver/DHCP comparison in the spikes.
# Kea and BIRD are pinned to the same versions as deploy/Dockerfile (M6a-01, M4c-17), so
# devcontainer-based tests exercise the same behaviour the deployed containers run.
ARG KEA_VERSION=3.0.3-1
ARG BIRD_VERSION=2.18-1
RUN apt-get update && apt-get install -y --no-install-recommends \
    iproute2 nftables iptables conntrack ethtool kmod procps psmisc \
    iputils-ping iputils-tracepath arping bind9-dnsutils netcat-openbsd socat \
    iperf3 mosquitto-clients openssl tcpdump tshark \
    busybox-static udhcpc \
    wireguard-tools "bird2=${BIRD_VERSION}" \
    "kea-dhcp4-server=${KEA_VERSION}" kea-admin \
    dnsmasq-base dnsmasq-utils \
    python3 python3-venv python3-pip python3-cryptography \
    python3-yaml python3-jsonschema python3-openapi-spec-validator \
    && rm -rf /var/lib/apt/lists/* \
    && kea-dhcp4 -V | head -1 | grep -qF "${KEA_VERSION%%-*}" \
    && bird --version 2>&1 | grep -qF "${BIRD_VERSION%%-*}"

# --- Test level 1b: namespace testbed inside a VM (plan §4.5) ------------------
# This container runs unprivileged, so the testbed runs in QEMU via virtme-ng
# against stock Ubuntu kernels. Pass --device /dev/kvm for real timing; without
# it QEMU emulates (functional tests only). qemu-system-arm and qemu-user-binfmt
# are for the arm64 build and level 1b on arm64. The guest boots from this
# container's root file system: it needs zstd (compressed modules), udev
# (/dev/virtio-ports links for --exec I/O) and poweroff (systemd-sysv).
RUN apt-get update && apt-get install -y --no-install-recommends \
    qemu-system-x86 qemu-system-arm qemu-user-binfmt virtme-ng zstd udev systemd-sysv \
    && rm -rf /var/lib/apt/lists/*

# Stock kernels for level 1b, extracted without the metapackages (those pull in
# firmware, microcode and an initramfs that virtme-ng does not need):
#   noble    — Ubuntu 24.04 GA kernel (minimum supported kernel, D1; used by spikes/vm.sh)
#   resolute — Ubuntu 26.04 GA kernel
# The nightly kernel matrix (plan §4.4, M1-02) runs level 1b once per kernel listed here, so keep
# .github/workflows/nightly.yml's testbed-matrix job in sync with this list. 26.04 has no HWE
# kernel of its own yet; 24.04's HWE kernel (linux-image-generic-hwe-24.04) is, as of this
# writing, version-identical to 26.04's GA kernel (verified against the live archive: both
# 7.0.0-38-generic), so it is not listed separately here — add it once the versions diverge.
ARG KERNELS="noble:6.8.0-142-generic resolute:7.0.0-38-generic"
RUN set -eux; \
    MIRROR=http://archive.ubuntu.com/ubuntu; \
    [ "$(dpkg --print-architecture)" = amd64 ] || MIRROR=http://ports.ubuntu.com/ubuntu-ports; \
    APTDIR=/tmp/apt-kernels; mkdir -p "$APTDIR/lists/partial" "$APTDIR/cache/archives/partial" "$APTDIR/src"; \
    for k in $KERNELS; do \
      suite="${k%%:*}"; kver="${k#*:}"; \
      printf 'Types: deb\nURIs: %s\nSuites: %s %s-updates %s-security\nComponents: main\nSigned-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg\n' \
        "$MIRROR" "$suite" "$suite" "$suite" > "$APTDIR/src/$suite.sources"; \
    done; \
    O="-o Dir::Etc::SourceList=/dev/null -o Dir::Etc::SourceParts=$APTDIR/src -o Dir::State::Lists=$APTDIR/lists -o Dir::Cache=$APTDIR/cache -o APT::Sandbox::User=root"; \
    apt-get $O update; \
    cd /tmp; \
    for k in $KERNELS; do \
      kver="${k#*:}"; \
      apt-get $O download "linux-image-$kver" "linux-modules-$kver"; \
      for deb in linux-image-"$kver"_*.deb linux-modules-"$kver"_*.deb; do dpkg-deb -x "$deb" /tmp/kroot; done; \
      install -D -m 0644 /tmp/kroot/boot/vmlinuz-"$kver" /boot/vmlinuz-"$kver"; \
      cp /tmp/kroot/boot/config-"$kver" /tmp/kroot/boot/System.map-"$kver" /boot/; \
      mkdir -p /usr/lib/modules; \
      if [ -d /tmp/kroot/lib/modules/"$kver" ]; then cp -a /tmp/kroot/lib/modules/"$kver" /usr/lib/modules/; \
      else cp -a /tmp/kroot/usr/lib/modules/"$kver" /usr/lib/modules/; fi; \
      depmod -a "$kver"; \
      rm -rf /tmp/kroot /tmp/*.deb; \
    done; \
    rm -rf "$APTDIR"; \
    ls -l /boot/vmlinuz-*

# --- mitmproxy (TLS interception sidecar, spike S4) ---------------------------
ARG MITMPROXY_VERSION=12.2.3
RUN python3 -m venv /opt/mitm-venv \
    && /opt/mitm-venv/bin/pip install --no-cache-dir "mitmproxy==${MITMPROXY_VERSION}" \
    && /opt/mitm-venv/bin/mitmdump --version

# --- Go tools -----------------------------------------------------------------
# Project-specific generators (oapi-codegen) are pinned in go.mod as `go tool`
# dependencies, not here.
ARG GOLANGCI_LINT_VERSION=v2.14.0
RUN curl -fsSL https://raw.githubusercontent.com/golangci/golangci-lint/${GOLANGCI_LINT_VERSION}/install.sh \
        | sh -s -- -b /usr/local/bin "${GOLANGCI_LINT_VERSION}" \
    && golangci-lint version

# --- Node.js LTS + Playwright system dependencies -----------------------------
# Browser binaries match the project's @playwright/test version and are installed
# by `npx playwright install chromium`; only their OS libraries are baked in here.
ARG PLAYWRIGHT_VERSION=1.63.0
RUN curl -fsSL https://deb.nodesource.com/setup_lts.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && npx -y "playwright@${PLAYWRIGHT_VERSION}" install-deps chromium \
    && rm -rf /var/lib/apt/lists/* /root/.npm

# --- Claude Code (last: updates often, keeps the layers above cached) ----------
RUN npm install -g @anthropic-ai/claude-code

WORKDIR /workspace

CMD ["/bin/bash"]
