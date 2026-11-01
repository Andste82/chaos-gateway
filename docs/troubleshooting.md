# Troubleshooting

What to look at when the gateway does not do what you expect: first the **preflight checks**, which find most problems of the host
before they show as strange behavior, then the symptoms in the order they usually appear. When the gateway cannot apply its
configuration at all, go to the [recovery instructions](recovery.md).

## Contents

- [Where to look](#where-to-look)
- [The preflight checks](#the-preflight-checks)
- [Symptoms](#symptoms)

## Where to look

| What | How |
|---|---|
| Is the gateway healthy? | `GET /api/v1/system/health` (a status per component: `api`, `executor`, `svcns`, `dns`, `tls`, `tls_proxy`, `kea`, `bird`) |
| Is it in safe mode? | `GET /api/v1/state`: `safe_mode`, `safe_mode_reason`; the red banner of the web app |
| What is wrong with the host? | `GET /api/v1/system/preflight` or `docker compose logs exec \| grep preflight` |
| Why did a change fail? | the response of the apply (`apply_failed`, `verify_failed`, `validation_failed` with pointers), the event `apply_failed`, `docker compose logs api` |
| What happens to this traffic? | `GET /api/v1/explain` / `chaosctl explain` / the **Explain** buttons of the web app |
| Is my fault hit by any packet? | the counters of the overlay (`GET /overlays/{id}`), and the `zero_hit` warning of a run |
| What happened? | the event stream (`GET /events`), the audit log (`GET /audit`: who changed what, via UI or API) |

```sh run
curl -sS -H "Authorization: Bearer $CHAOSGW_TOKEN" "$CHAOSGW_URL/api/v1/system/preflight" | jq -c '.checks[] | {id, status}'
```

```text output
{"id":"preflight","status":"warn"}
{"id":"interface.lan0.os_config","status":"ok"}
```

(The simulated gateway of the documentation's tests has no host to inspect, so its report holds only the checks of its interfaces and
the warning `preflight` (no host check ran); a real gateway also has the kernel, module and host checks listed below.)

## The preflight checks

At every start the API checks what it can see of the host (the kernel and its modules) and the executor checks the host it runs on. The
executor writes its result into its log (`docker compose logs exec | grep preflight`); the API keeps its own only in the report.
`GET /api/v1/system/preflight` and the setup wizard show the same lines, with one more check per host interface that may
become or is a test interface. Each line has an `id`, a status (`ok`, `warn`, `fail`), a message and, when something is wrong, a `fix`.
A failed check does not stop the gateway: it is a thing to repair, and the parts that do not depend on it go on working. The
status of the report is the worst status of the checks of the host and of the interfaces that are assigned to a test network.

| Id | Status when wrong | What it looks at | What to do |
|---|---|---|---|
| `kernel.version` | fail | the host's kernel is at least 6.8 | boot a kernel of 6.8 or newer (Ubuntu 24.04 and 26.04 ship one) |
| `module.<name>` | fail (warn for the VLAN module, needed only after V1) | one line per kernel module the gateway needs (`sch_netem`, `sch_htb`, `cls_fw`, `nf_tables`, `nft_nat`, `wireguard`, ...), with the feature that needs it | `linux-modules-extra-$(uname -r)` for a kernel without them (cloud kernels); the host setup loads them now and at boot |
| `host.tools` | fail | `ip`, `tc`, `nft`, `ethtool`, `conntrack`, `iptables`, `wg`, `bird` are in the executor's image | pull the image again: it holds every tool the executor runs |
| `host.iptables` | warn | `iptables` of the image uses the nf_tables backend, so Docker's `DOCKER-USER` chain is the one the gateway edits | on the host: `update-alternatives --set iptables /usr/sbin/iptables-nft`, restart Docker |
| `host.modules` | fail | the modules are loaded; those that exist and are not loaded yet are loaded by the executor | the extra kernel modules for the host's kernel, or a standard Ubuntu kernel; the executor mounts the host's `/lib/modules` |
| `host.kernel` | warn | listed only when the executor cannot read the host's kernel release (`uname`), so the kernel modules could not be looked up for it; the line is absent when the release is read | restart the executor and read `docker compose logs exec` |
| `host.ip_forward` | warn | `net.ipv4.ip_forward` is on (the first apply switches it on) | the host setup makes it permanent (`/etc/sysctl.d/90-chaos-gateway.conf`); a reboot switches it off again until the first apply |
| `host.ping_group_range` | warn | the API container may open an unprivileged ICMP socket (the ping of the diagnostics) | the host setup writes the sysctl; see [the ping](install.md#diagnostics-the-ping-needs-a-sysctl-of-the-host) |
| `host.proc_sys` | fail | `/proc/sys` is writable in the executor's container | the executor container must be privileged, as in `compose.yaml` (`deploy/compose.executor.yaml`) |
| `host.netns` | fail, warn | a network namespace can be created, and `/run/netns` is shared with the host (`rshared`) | the executor's mounts in `compose.yaml`: the host's `/run/netns` with propagation `rshared` |
| `host.firewall` | warn | no other firewall (`ufw`, `firewalld`, an `nft` table of your own) whose drop verdicts would hit test traffic | allow the traffic of the test networks there, or stop it (`ufw disable`, `systemctl disable --now firewalld`) |
| `host.docker_pools` | warn | no address range of Docker's bridges or `default-address-pools` overlaps an address of the uplink, the management interface or a test network | give Docker other ranges (`default-address-pools` and `bip` in `/etc/docker/daemon.json`, restart Docker) or other addresses to the gateway's networks |
| `host.data.<dir>` | fail | the directories below `/var/lib/chaosgw` exist and belong to the API's user (65532); the secrets are mode 0700 | `sudo sh /opt/chaosgw/host-setup.sh --data-only` |
| `interfaces.os_config` | warn | the executor reads the host's netplan, NetworkManager and systemd-networkd configuration (mounted read-only below `/host`) | give the executor the host's `/etc` and `/usr/lib/systemd/network` read-only (`compose.yaml` does) |
| `interface.<name>.os_config` | fail for an interface assigned to a test network, warn for a candidate | the operating system configures this interface (netplan, NetworkManager, systemd-networkd), which a test interface must not have: it would put an address or a DHCP client on a port of a test network | the `fix` of the line says what to remove (the netplan entry, the NetworkManager connection, the networkd file) and the host setup writes the rule that keeps networkd off the interface (`--test-interface`); then `sudo netplan apply` |
| `host` | warn | the executor's checks could not be read, or the host cannot be inspected from the API container | start the executor; check that the API reaches its socket (`docker compose logs exec`) |
| `preflight` | warn | no check has run in this process | restart the API container |

The setup wizard asks the same checks before the first apply, and the unauthenticated `GET /api/v1/setup` carries only their status and
fix, not the messages (those hold tool versions and the names of other firewalls' tables); the executor's log has the full text.

## Symptoms

**The web app does not open after the first start.** The wizard listens on every address of the host that is not on a test network,
port 8443 (`CHAOSGW_UI_PORT` in `.env` changes it). `docker compose ps` must show every container healthy; `docker compose logs api` shows
the listener and the setup token. The browser warns about the self-signed certificate until you
[install your own](install.md#https-certificate). After the setup the UI and API listen on the management network only: use the
address of the management interface.

**The setup was applied and the browser lost the gateway.** The first revision waits for confirmation for 60 seconds and rolls back by
itself when nobody confirms; the setup opens again with a new token in the log. Check that you connect from the management network.

**`GET /system/health` says `degraded`, the DNS proxy has not polled.** Normal until the first configuration is applied, because the
DNS proxy fetches its configuration from the API and the link is made by the first apply. If it stays after the setup, look at the logs of
`api` and `dns`.

**A device gets no address.** [DHCP does not answer](install.md#troubleshooting-dhcp-does-not-answer): the port is `unmanaged` by
networkd, it has a link and is in the network's bridge, Kea listens on the bridge. A DHCP `silence` or `short-lease` action in force on
the device or network (`GET /overlays`) is the other cause.

**A device is not in the device list.** A device is listed when the gateway sees it (a DHCP lease or an ARP entry in a test network). Check
that it is on a port of a network (the Networks screen), and `GET /devices?origin=discovered`. A device behind another router can only be
told by its IP address, not by MAC.

**My fault has no effect.** Work through it in this order:

1. `chaosctl explain --device <name> --dst <address> --protocol tcp --port <port>`: the fault must be listed as the winner of its
   family for exactly that traffic. A more specific fault (a device before a group before a network) or an overlay of another owner may
   win, and parameters are never merged. A fault matches the connections the device **initiates**; traffic towards a device (a
   port forward, a lab host) is judged by who started it.
2. The counters of the overlay: packets 0 means nothing matched. The destination may be a hostname the device does not resolve through
   the gateway's DNS (hardcoded DNS, DNS over HTTPS) or an address that changed.
3. `GET /system/preflight`: a failed `host.modules` check (no `sch_netem`) means no fault can be applied; the apply would have failed
   with `apply_failed`.
4. The traffic is not the device's: the gateway's own traffic (management, updates, the WireGuard underlay) is never impaired, and
   diagnostics run from the gateway's own address do not meet device faults (use a probe).

**A write is refused with `capacity_exceeded`.** The faults would need more queues than the gateway can have on one interface (per-device
rate and queue limits create one class per matched device). Narrow the scope, or remove faults. `GET /capabilities` shows the limits.

**A run is `queued` for a long time, or a call answers `target_busy`.** Only one run may be active per target (device, group or
network; groups overlap, so a run on a device blocks a run on its group). `GET /runs?status=running` shows which; abort it, or start with
`?queue=false` to get the error at once.

**A run ends as `error` with `precondition target_online failed`.** The device was not online when the run started: check the cable,
DHCP, the device list. The other preconditions are `uses_gateway_dns` (the device does not query the gateway's DNS, needed when the
scenario has DNS or hostname steps) and `no_foreign_overlays` (an overlay of another owner is active on the target); a scenario can
skip a precondition with `preconditions.skip`.

**A run passes, but with `zero_hit` warnings.** A fault or rule of the run matched no packet, so the run proves nothing about it. Is the
device talking to that destination and port during the step? Use `zero_hit: error` in the scenario to make this a failure.

**Everything is `503 unavailable`.** The gateway is in [safe mode](recovery.md#safe-mode).

**A ping in the diagnostics answers `cannot open an ICMP socket`.** The host's `net.ipv4.ping_group_range`
([install.md](install.md#diagnostics-the-ping-needs-a-sysctl-of-the-host)).

**A capture is refused or stops.** The volume has less than 10 % free space, or the quota of the captures is used up
(`GET /captures/storage`: `quota_bytes`, `used_bytes`, `free_percent`, `disk_low`), or eight captures already run. Delete old captures; the
oldest stopped ones are removed first when the quota is reached.

**Interception answers 502 or 503.** The interception proxy is an optional container (`compose.tlsproxy.yaml`); its health is the
component `tls_proxy` of the system health. A case with a CA file for the upstream servers that cannot be read is refused
([install.md](install.md#3-start-the-containers)). The proxy is not validated on ARM64 hardware
([release notes](release-notes/v1.0.0.md#not-validated-on-arm64-until-the-hardware-lab-has-run)).

**A WireGuard client does not connect.** `GET /networks/{id}/clients`: the last handshake of the peer. No handshake means the packets do
not arrive: the endpoint in the client's configuration is the one the client can reach (a port forward from the router to
the gateway's `listen_port`, UDP), and the key pair matches (`key_mismatch` and `disable` overlays of a test are visible in
`GET /overlays`). `block_endpoint` drops the peer's UDP on purpose.

**Locked out by a rule or a fault.** A global fault never touches the gateway's own traffic, and the anti-lockout rule keeps the
control plane (UI, API, SSH) open from the management network, so a test cannot lock you out. A change of the management
configuration that does is rolled back by itself after 60 seconds ([recovery](recovery.md#a-change-locked-me-out)).

**Too many failed logins (`429 rate_limited`).** The wait doubles with each failure up to 15 minutes, per client address. Wait, or
restart the API container.

If none of this explains it, the audit log and the event stream tell what the gateway did and when; `docker compose logs api exec` has
the details of every apply.
