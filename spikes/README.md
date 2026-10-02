# Chaos Gateway — Phase 0 Spikes

Throwaway experiments that validate the technical assumptions of the plan (`docs/plan.md`, §5 Phase 0). The results and decisions are in [`docs/spikes/REPORT.md`](../docs/spikes/REPORT.md).

```
lib/testbed.sh           namespace topology: cl1, cl2 ─ switch ─ gw (NAT) ─ srv
tools/echo.py            measurement helper (UDP/TCP echo, RTT, loss, reordering, connection events)
tools/check.py           evaluates measurements against expectations (tolerances, 99 % loss intervals)
vm.sh                    runs a spike inside QEMU with a stock Ubuntu 24.04 kernel (virtme-ng)
s01-testbed/             S1  testbed and baseline
s02-s10-classification/  S10 classification identity + S2 fault topology (run.sh, followup.sh)
s03-connections/         S3  established connections vs. rule changes
s04-tls/                 S4  Node TLS responder, certificate cases, mitmproxy transparent mode
s05-dns/                 S5  Node DNS proxy: faults, hostname selectors, throughput
s06-dhcp/                S6  dnsmasq vs. Kea runtime test actions
s07-deployment/          S7  Docker FORWARD policy, gateway operations inside a container
s08-perf/                S8  classification cost, nftables update latency (x86 only so far)
s09-capture/             S9  capture mechanisms compared against ground truth
s11-direction/           S11 direction bit with two test networks, destination selectors, re-apply
s12-attachment/          S12 probe on a bridge vs. macvlan, tc on the bridge, policy routing
s13-pmtud/               S13 path-MTU faults (ICMP, black hole)
s14-local-replies/       S14 download faults for connections that end on the gateway
s15-wireguard/           S15 WireGuard networks, tunnel faults, BIRD over WireGuard
s16-service-ns/          S16 gateway services in a service namespace (flower vs. namespace, Docker)
followups.sh             S11, S12 and S14 in one VM session
results/                 result files (*.json / *.jsonl) of the runs in the report
```

## Prerequisites

The devcontainer (`.devcontainer/`, Ubuntu 26.04) contains everything below, including QEMU, virtme-ng and the stock kernels. On a plain Ubuntu 24.04 host:

```bash
sudo apt install iproute2 nftables conntrack iperf3 tcpdump tshark ethtool socat \
  dnsmasq-base dnsmasq-utils kea-dhcp4-server dnsutils iputils-ping busybox-static \
  wireguard-tools bird2 zstd udev   # wireguard-tools and bird2 for S15
# netem spikes when the running kernel lacks sch_netem (e.g. minimal VM kernels):
sudo apt install qemu-system-x86 linux-image-6.8.0-142-generic
pip install virtme-ng
# S4 interception part
python3 -m venv /opt/mitm-venv && /opt/mitm-venv/bin/pip install mitmproxy
# S5
(cd s05-dns && npm install)
# S7: minimal busybox image, host tools are mounted read-only
mkdir -p /tmp/bb/bin && cp /bin/busybox /tmp/bb/bin/ && ln -sf busybox /tmp/bb/bin/sh
tar -C /tmp/bb -c . | docker import - cg-bb:latest
```

Node.js 22 and Python 3 are required. All scripts need root (network namespaces).

## Running

```bash
./vm.sh s01-testbed/run.sh                     # netem spikes in the 6.8 kernel
./vm.sh s02-s10-classification/run.sh
./vm.sh s02-s10-classification/followup.sh
TB_PREFIX=h bash s03-connections/run.sh        # everything else on any kernel with nftables
TB_PREFIX=h bash s04-tls/run.sh
TB_PREFIX=h bash s05-dns/run.sh
TB_PREFIX=h bash s06-dhcp/run.sh
bash s07-deployment/run.sh
TB_PREFIX=h bash s08-perf/run.sh
TB_PREFIX=h bash s09-capture/run.sh
./vm.sh followups.sh                       # S11, S12, S14
./vm.sh s15-wireguard/run.sh                # needs wireguard-tools and bird2
./vm.sh s16-service-ns/run.sh
TB_PREFIX=h bash s13-pmtud/run.sh
TB_PREFIX=h bash s16-service-ns/docker.sh   # needs a Docker daemon and the cg-bb image
```

- Do not run a `vm.sh` spike and a host spike at the same time: `/run/netns` is shared with the guest, and identical namespace names destroy each other's topology.
- `vm.sh` provides the pseudo-terminal that `vng` needs (via `script`), so it also works from scripts and CI. Without `/dev/kvm` a VM boots in 3–9 minutes.
- Under emulation, `nft`/`ip` commands take ~0.5 s each and the RTT baseline is ~2.4 ms. The latency tolerances in `tools/check.py` account for that.
- Temporary files (captures, certificates, logs) go to `results/tmp/`, which is not committed.
