# Chaos Gateway — Product & Implementation Plan

> **A programmable network test gateway.**

Status: draft for review · September 2026

---

## Contents

1. Purpose and Scope
2. Functional Specification
3. Technical Architecture
4. Test Strategy
5. Milestones
6. Known Limitations and Technical Risks
7. Decisions
8. Proposed Additional Features

---

# 1. Purpose and Scope

## 1.1 Problem

IoT devices have to cope with bad networks: high latency, packet loss, outages, broken DNS, manipulated or failing TLS connections. Testing this reproducibly is hard. Client-side "slow network" tools don't work on embedded devices, and existing open-source tools each cover only part of the problem:

| Tool | Strength | Missing |
|---|---|---|
| Techkarma NetEm | web UI for tc/netem, transparent bridge | TLS, automation, gateway functions |
| FlowEmu | runtime-changeable impairment modules, MQTT control | TLS, gateway functions |
| mitmproxy | TLS interception, HTTP manipulation | L3/L4 impairment, gateway functions |
| plain tc/netem | complete L3/L4 impairment | UI, API, per-device handling, safety |

## 1.2 Product Definition

Chaos Gateway is a Linux machine that sits as the **gateway** between a test network with devices under test and an upstream network. Everything the devices send passes through it, so it can shape, break, intercept and record that traffic, per device and per traffic type, in both directions.

```
               Test automation (REST / CLI)      Browser (Web UI)
                            │                          │
                            └────────────┬─────────────┘
                                         ▼
 Devices under test ──► [ Chaos Gateway ] ──► Upstream / Internet / servers
    (IoT network)       routing · DHCP · DNS
                        firewall · faults
                        TLS proxy · capture
```

**Core principle:** the user describes what should happen to network traffic; Chaos Gateway determines how Linux implements it. The UI and API model **intent** (devices, networks, traffic, faults), not Linux objects (qdiscs, chains, routes).

**Positioning:** gateway functions (routing, NAT, DHCP, DNS, firewall) are the foundation, not the product. The product is **programmable network testing**: reproducible faults, profiles, scenarios and the evidence to go with them (counters, captures, events). Chaos Gateway must not grow into "another Linux router with a web UI".

## 1.3 Capabilities at a Glance

| Area | Capabilities |
|---|---|
| Connectivity | routed gateway, NAT, DHCP, DNS; WireGuard networks, clients and site links; static and dynamic routing (BIRD); later VLANs, IPv6 |
| Access control | allow, drop, reject, TCP reset, per device/network/traffic |
| Network faults | latency, jitter, loss, rate limit, reordering, duplication, corruption, blackout, flapping, MTU/PMTUD faults |
| Application faults | DNS faults, TLS certificate faults, TLS interception, HTTP manipulation |
| Infrastructure faults | DHCP behavior; later time (NTP) offset (M34) |
| Automation | profiles, scenarios, timed faults, REST API, CLI, event stream, test reports |
| Evidence | per-rule counters, live connections, packet capture, run artifacts |
| Diagnostics | connectivity checks, probe clients that experience the same faults as devices |

## 1.4 Non-Goals

- A general-purpose home or enterprise router or firewall.
- Link-layer (WiFi radio) impairment in V1 (see §8).
- Multi-WAN and PPPoE in V1. The routing model must not rule them out later. (Dynamic routing over WireGuard is planned, §2.2.2.)
- Acting as a general VPN concentrator for end users: WireGuard exists to connect test machines, test networks and administrators (§2.2.1).
- High-throughput WAN emulation beyond the hardware targets in §3.10.
- Decrypting TLS of devices that correctly refuse an untrusted CA. That refusal *is* the test result (see §2.8).

## 1.5 Platform

| | |
|---|---|
| Operating system | Linux. Primary: Ubuntu LTS (24.04 and newer). Secondary: Debian (12 and newer) |
| Minimum kernel | 6.1 (proposed, see §7) |
| Architectures | x86-64, ARM64 (Raspberry-Pi-class hardware) |
| Later | BSD backend, behind the same domain and compiler boundaries |

The domain model is platform-independent; the execution layer is explicitly Linux. There are no portability abstractions beyond that boundary.

---

# 2. Functional Specification

## 2.1 Concepts

| Concept | Meaning |
|---|---|
| **Gateway** | the Chaos Gateway machine itself, with its interfaces and services |
| **Uplink** | the upstream connection (static IPv4 or DHCP client) |
| **Network** | a logical test network: subnet, DHCP/DNS settings, access to other networks. Its gateway address lives on a Linux bridge owned by Chaos Gateway; **attachments** are the bridge's ports (V1: physical interfaces and probes; later: VLANs, tunnels) |
| **Device** | a device under test with a stable internal identity. It is recognized by one or more identifiers — MAC address, IPv4 address/range, later IPv6 address or WireGuard peer. Configured or discovered |
| **Group** | a named set of devices (e.g. "all sensors") |
| **WireGuard network** | a tunnel network on the gateway: a hub for many clients, or a point-to-point link to another site (§2.2.1) |
| **Peer / client** | a WireGuard endpoint with keys, a tunnel address and optionally **client networks** — subnets behind it (a remote test lab, another test machine's networks) |
| **Remote network** | a subnet reached through a peer or learned by dynamic routing; can be source and target of rules and faults like a local network |
| **Probe** | a virtual test client inside the gateway, attached to a network and treated like a device (see §2.12) |
| **Traffic selector** | a match expression: source, destination, protocol, ports, hostname, direction |
| **Flow** | an observed connection (from conntrack), with counters |
| **Rule** | selector plus access action (allow/drop/reject/reset), optionally with fault parameters for the matched traffic |
| **Fault** | selector plus impairment parameters, optionally time-limited |
| **Profile** | a named, reusable set of fault parameters ("Bad LTE") |
| **Scenario** | a timeline of steps (faults, profiles, rules, actions) |
| **Run** | one execution of a scenario, with its artifacts |
| **Capture** | a packet recording |
| **Revision** | an immutable version of the persistent configuration |
| **Overlay** | a runtime change layered over the configuration — fault, profile activation, rule, DNS fault, TLS case or DHCP action — with an owner and optionally an expiry (§2.1.1) |

### 2.1.1 State model

Two levels of state are central to the design.

**Configuration** (persistent, revisioned): uplink, networks, devices, groups, rules (access rules and persistent faults), profile definitions and scenario definitions. It changes rarely and deliberately. Profiles and scenarios are part of each revision; YAML files are only an import/export format.

**Overlays** (runtime, never revisioned): what tests switch on and off, often many times per minute.

| Overlay kind | Example |
|---|---|
| fault | ESP32-42 upload +200 ms for 5 min |
| profile activation | "Bad LTE" on network IoT |
| rule | drop TCP 8883 for ESP32-42 (a scenario step) |
| DNS fault | SERVFAIL for broker.example.com |
| TLS case | untrusted certificate on TCP 8883 |
| DHCP action | move reservation, silence DHCP |

Every overlay has an **owner** (a user session, an API token or a run), an optional **expiry** (TTL) and optionally a **lease** that its owner must renew. When the expiry or lease runs out, the overlay is removed and an event is emitted — a crashed test job never leaves the network broken.

**Precedence:** overlays are evaluated before configuration of the same kind. Among faults the most specific one wins (§2.4); among access rules the first match wins, overlay rules first.

**Editing the configuration:** a change is submitted as a **candidate revision** (`POST /api/v1/revisions` with the full configuration or a patch, plus the id of the revision it is based on). The server validates it, the preview shows the domain and Linux diff, and applying it by id makes it active after verification. If another revision became active in between, the request fails with a conflict; the server offers a three-way diff so the client can rebase its change. The UI's "N changes · not applied yet" bar is a client-side draft that becomes a candidate on *Preview*.

**Overlays and concurrency:** overlays are not revisioned and not locked. For the same scope, kind and owner the last write wins. `POST /api/v1/reset` removes overlays and stops runs — by default only those of the caller; `?owner=all` requires the full-access scope. Only one run may be active per target (device, group or network); runs on disjoint targets can run in parallel, others wait in `queued`.

**Restart:** whenever the API service or the machine restarts, the kernel state is recompiled from the committed revision only. Overlays are dropped, active runs end as `aborted`, and an event records both. Tests always restart from a clean baseline.

**Time:** TTLs, leases and scenario schedules run on the monotonic clock; wall-clock time is used only for display and the audit log. Raspberry-Pi-class machines have no real-time clock, so wall time can jump at the first NTP sync.

## 2.2 Networks and Connectivity

**V1**

- One uplink: static IPv4 or DHCP client.
- One or more **IPv4-only** test networks. Each network is a Linux bridge owned by Chaos Gateway; its physical interface and its probes are ports of that bridge (spike S12: a probe attached with macvlan to a physical port cannot reach the gateway address; on a bridge it can):
  - Static gateway address on the bridge.
  - Routing to the uplink with masquerade (NAT).
  - Access matrix between networks (e.g. IoT → Internet ✓, IoT → Management ✕).
- A management network or interface for the UI and API, separated from test networks (see §2.16).
- **Supported topologies:** three ports (uplink, test network, management) or two ports with management on the uplink side — the common case on a Raspberry Pi with one built-in port plus a USB adapter. Both are covered by the level-2 tests (§4.5).
- **Policy routing:** traffic entering from test networks and the gateway's own service traffic (DNS proxy, TLS proxy upstream) use a routing table owned by Chaos Gateway whose default route is the uplink. The rules match on the test-network bridges (`iif`) and on the service user (`uidrange`). The OS-owned management interface keeps its own default route in the main table; other host processes (package updates, SSH) keep using it. Spike S12: without the policy route, forwarded test traffic left through the management interface; with it, forwarded traffic and sockets of the service user took the uplink and nothing leaked.
- **Downstream routes:** static routes per test network, for devices behind another router.

**Later**

- VLANs (multiple networks on one trunk port).
- IPv6: router advertisements, SLAAC, DHCPv6, IPv6 firewalling and faults.
- Port forwarding (DNAT).

### 2.2.1 WireGuard Networks

**Purpose:** connect further test machines and test networks to the gateway, so that Chaos Gateway is the central point that routes, controls and impairs traffic between all of them, and give administrators remote access. Chaos Gateway manages the tunnels, keys, client configurations and routes.

**Network kinds**

| Kind | Topology | Use | Routing |
|---|---|---|---|
| **Hub** | one WireGuard interface, many clients (star) | test machines, laptops, remote test computers, a remote lab router | static: each client's tunnel address and its declared client networks become the client's `AllowedIPs` and routes via the interface |
| **Link** | one WireGuard interface **per remote site** (point to point, /31 transfer net) | connecting another gateway, router or lab network | static routes or dynamic routing (§2.2.2); `AllowedIPs 0.0.0.0/0` on the link, routes decide |

Why two kinds: WireGuard's cryptokey routing binds every prefix to exactly one peer of an interface. Dynamically learned prefixes therefore cannot be expressed on a shared hub interface without rewriting `AllowedIPs` on every route change; on a point-to-point link the routing table alone decides, which is the standard way to run BGP/OSPF over WireGuard.

**Managing networks and clients**

- Create, edit and delete WireGuard networks: name, kind, tunnel subnet, gateway tunnel address, listen port, MTU (default 1420), public endpoint (hostname or IP and port that clients use; defaults to the uplink address), role (**test** — untrusted, like a test network; **management** — admin access to UI/API; see §2.16).
- Create clients (peers) in a hub network: name, tunnel address (next free address proposed), **client networks** (0..n subnets behind the client), networks the client may reach (selection of test networks, other WireGuard networks, other clients' networks, internet via uplink = full tunnel), DNS server for the client (optional: the gateway's DNS proxy, so DNS faults and hostname selectors apply), persistent keepalive (default 25 s).
- Enable/disable a client (disabled = removed from the interface; usable as a fault: "peer offline"), rotate keys, delete.
- Status per client and link: last handshake, current endpoint, bytes in/out, online state (handshake younger than 3 min), learned routes. Events on state changes.
- Clients and their client networks appear as **devices and remote networks** in the model: they can be grouped, targeted by rules, faults, profiles and scenarios, and appear in the access matrix. Identity is the tunnel address or the client network (no MAC, no DHCP).
- Addressing: between WireGuard and local test networks traffic is **routed without NAT** by default (remote test machines see real device addresses; the exported client configuration contains the needed routes). Towards the uplink it is masqueraded, like test networks.

**Keys and export**

- Default: the gateway generates the client's key pair (and optionally a preshared key), so it can export a complete configuration. Alternative: the client provides only its public key; the export then contains a placeholder for the private key.
- Client private keys are stored in the secrets directory (§2.16) and can be deleted after the first download ("export once").
- Export formats: `wg-quick` `.conf` file (download), **QR code** in the UI (for phones and tablets), QR as PNG/SVG via API, a zip with configurations of several clients, `chaosctl wg export <client>`. For links: the configuration of the remote side plus, if dynamic routing is used, a matching BIRD configuration snippet.
- Exports containing private keys are secret: shown with a warning, never included in configuration exports or logs.

**Faults and WireGuard**

- **Inner faults:** traffic of clients and remote networks is classified like any other traffic (§3.3: conntrack original tuple, direction bit); tc on the WireGuard interface's egress impairs the download towards the remote side, the other interfaces the upload.
- **Tunnel faults (underlay):** impair a client's or link's encrypted UDP traffic itself — latency, loss, blackout, flapping of the tunnel — to simulate a bad WAN between sites. Everything inside the tunnel is affected, including routing-protocol sessions. Implementation: the output hook classifies WireGuard's UDP packets by peer endpoint (spike S15).
- Peer disable, key mismatch (rotated key not deployed) and endpoint blocking are available as scenario actions.

### 2.2.2 Routing

- **Static routes:** per test network (devices behind another router), per client (its client networks, automatic), per link.
- **Dynamic routing** with **BIRD 2**, managed by Chaos Gateway in its own instance (own configuration file and control socket, own systemd unit; an existing BIRD on the host is not touched):
  - Protocols: **BGP** (recommended for links; private ASNs), **OSPFv2** (point-to-point on links), **Babel** (suited for meshes of links), plus static.
  - Configured in the model: router id, ASN, neighbors per link, OSPF area, timers, and which prefixes are announced (selection of test networks, WireGuard networks, client networks, remote networks).
  - Chaos Gateway generates the BIRD configuration, validates it with `bird -p`, then applies it with `birdc configure` (graceful, sessions stay up). It is part of the revision and of preview/diff.
  - **Import safety:** learned routes go only into Chaos Gateway's routing tables (§2.2 policy routing, and the PMTU mirror tables), never into the main table. Import filters per neighbor: allowed prefix list, no default route unless explicitly allowed, never the management, uplink or gateway-own prefixes, maximum prefix count. A misbehaving remote site cannot hijack management traffic.
  - **Custom snippets:** per protocol, raw BIRD configuration can be added for cases the model does not cover; it is validated by `bird -p` and marked as "unmanaged" in the UI.
  - **Other routing daemons:** the routing layer is an adapter (generate configuration, reload, read status). BIRD is the only adapter in the plan; FRR is possible later. As a fallback, **external mode** imports routes that another daemon writes into a designated kernel table, with the same import filters.
- **Observability:** neighbor/session state, received and announced prefixes, route changes as events and in the activity log; the effective route for a destination is shown in the preview.
- **Routing faults:** tunnel faults, access rules on routing traffic (e.g. drop TCP 179) and link disable make convergence testable: "site B loses its link for 20 s — do devices reconnect after re-convergence?".

**V1 test networks are IPv4-only.** The gateway sends no router advertisements, offers no DHCPv6 and drops all forwarded IPv6 traffic. Devices only have link-local IPv6 addresses, so dual-stack devices fall back to IPv4. This is intended: faults and rules must never be bypassable, and a device reaching its server over an unimpaired IPv6 path would invalidate the test. On all interfaces it owns, Chaos Gateway disables router-advertisement acceptance, so its own services cannot leave over IPv6 either; the DNS proxy removes AAAA records from answers.

**Not impaired:** traffic between two devices on the *same* test network is switched directly at layer 2 and never passes the gateway's routing path. Faults apply to traffic that crosses the gateway.

## 2.3 Devices and Discovery

- Discovery sources: DHCP leases, the neighbor table (ARP/ND), conntrack.
- Configured devices have a name, MAC and optional fixed IP (DHCP reservation). Discovered devices appear automatically and can be adopted with one click.
- The device view shows: online state, IP, lease, current flows (destination, protocol, bytes, state), traffic rates, active rules/faults, captures.
- Rules and faults address the **device**, not an address. The compiler translates the device's current identifiers into match sets (see §3.3). The MAC address is the most stable identifier but is only visible for devices on the same L2 segment as the gateway. Devices behind another router, WireGuard peers and probes are identified by IP address or peer instead.
- **Identity changes during a test:** DHCP lease events and neighbor-table changes update the device's address in the classification maps immediately (map elements only, no ruleset rebuild; target ≤ 1 s). Faults stay attached to the device when it gets a new IP, e.g. after the "force new IP" DHCP action. Connections from the old address keep their old conntrack entries until they end.
- **Randomized MACs:** a device that changes its MAC appears as a new discovered device; two device entries can be merged manually.
- **Device attribute `trusts_test_ca`:** whether the firmware trusts the Chaos Gateway test CA. It decides the expected result of TLS checks (§2.8).

## 2.4 Rules and Faults: Semantics

Access rules and faults share one selector model and one UI list, but differ in how they combine.

**Selector**

```
source      device | group | network | any
destination any | uplink | network | IP/CIDR | hostname
protocol    any | tcp | udp | icmp
port(s)     single, list, range
direction   upload (device → destination) | download | both
```

**Access rules** (allow, drop, reject, TCP reset)

- An ordered list; the **first match wins**, as in any firewall.
- The default policy per network comes from the access matrix.
- Changing a rule affects **new connections** by default (established traffic is accepted first).
- Optionally, *"also cut existing connections"* applies a time-limited `reject with tcp reset` to established packets of the selector. The device gets an immediate reset and can reconnect. The server side stays half-open, as in a real outage.
- Deleting conntrack entries alone does **not** cut a connection behind NAT; the flow is simply re-created (spike S3).

**Faults** (latency, loss, etc.)

- When several faults match the same traffic, Chaos Gateway resolves them into **one effective fault configuration per direction**. This is a deliberate product decision, not a Linux limitation. Linux could chain impairments, but stacked faults are hard to predict and to explain. The effective configuration maps onto one netem instance. Parameters are never merged: the winning fault's complete parameter set applies.
- Resolution: the most specific fault wins, in this order (first match):

  | # | Scope of the fault's selector |
  |---|---|
  | 1 | device + destination (IP/CIDR or hostname) + port/protocol |
  | 2 | device + destination |
  | 3 | device + port/protocol |
  | 4 | device |
  | 5 | group + destination and/or port/protocol |
  | 6 | group |
  | 7 | network + destination and/or port/protocol |
  | 8 | network |
  | 9 | global |

  Within the same level: an explicit priority value decides, then overlays win over configuration, then the newer entry wins (a device in two groups is resolved the same way). An explicit priority can also lift a fault above its level. The preview shows the effective result and which faults were overridden; golden tests cover every row.
- A profile activated on a scope acts like a fault on that scope. A fault and a profile on the *same* scope: the fault wins as a whole.
- **Rate and queue limits are per device** (decision D18). "Bad LTE, 2 Mbit/s" on a group, network or globally gives **every** matched device its own 2 Mbit/s queue, as if each had its own LTE link. The same holds for an explicit queue limit and for "keep order" (netem `rate`). Faults without these parameters (delay, jitter, loss, …) are per-packet effects and share one queue per configuration. Addresses in a network that are not (yet) known as devices share one queue per scope until discovery (§2.3) adds them. The preview shows how many queues a scope creates, and the compiler refuses configurations above the capacity limit (`capacity_exceeded`, §3.3).
- **Initiator semantics:** a device fault applies to connections the device *initiates*, in both directions (upload = packets in the connection's original direction, download = replies). Connections initiated towards the device by another device or by the server are matched by the initiator's scope, not by the responder's. Matching a device as responder is a later extension (§8).
- Faults act on **every packet**, including packets of connections that already existed when the fault was activated. Classification is therefore evaluated per packet, not cached per connection (see §3.3). Spike S10 confirmed this: with per-packet classification the first message after a change is affected, while conntrack-mark caching left the running connection on its old fault.
- **Direction "both"** means separate parameters per direction. The UI shows them as a pair, and they can be set asymmetrically (e.g. upload 2 % loss, download 0 %).

**Precedence between access rules and faults:** access rules are evaluated first. A dropped packet does not reach any fault. The preview (§2.14) shows the effective result for each selector.

## 2.5 Network Faults (L3/L4)

| Fault | Parameters | Mechanism | Notes |
|---|---|---|---|
| Latency | delay, jitter, distribution, keep order | netem | values are one-way per direction (RTT = upload + download delay); jitter ≤ delay (larger jitter is clamped at 0 by netem and skews the distribution); jitter reorders packets by default (S2 F1: 712 of 1000 packets); "keep order" uses netem rate, which shifts the delay distribution |
| Loss | % random, burst models (Gilbert-Elliott) | netem | S2: accuracy within the 99 % confidence interval |
| Rate limit | bit/s | netem rate or HTB | |
| Reordering | % | netem | requires a delay |
| Duplication | % | netem | |
| Corruption | % | netem | corrupted packets are usually dropped by checksums at the receiver |
| Queue limit | packets | netem limit | per device (D18). By default the compiler computes the limit from delay × rate, where rate is the fault's rate or, without one, the egress interface's link speed capped at 1 Gbit/s, and caps the result by a memory budget per interface (netem's default of 1000 packets causes tail drop at high delay × rate, e.g. satellite); an explicit value models small buffers |
| Blackout | on/off | netem loss 100 % | "offline"; part of the effective fault configuration, so it follows the same precedence and hits running connections too; fault counters show the dropped packets |
| Flapping | up/down durations | timed blackout | "intermittent connectivity" |
| MTU / PMTUD | max. packet size; mode ICMP / black hole / MSS clamp | policy route with `mtu lock` (ICMP), nftables length drop (black hole), TCP MSS rewrite (clamp) | spike S13: with ICMP the kernel itself answers "fragmentation needed, mtu N" in both directions; black hole stalls TCP transfers. **Side effect:** the server caches the reduced path MTU for the shared NAT address, so other devices talking to the same server are affected until the cache expires (Linux: 10 min). MSS clamp affects only TCP of the selected device and has no such side effect. The device's own interface MTU cannot be changed |

TCP reset and silent drop of matching traffic are access actions (§2.4), not faults.

## 2.6 DNS

The gateway provides DNS to test networks through its own **DNS proxy** in front of the upstream resolver. It answers over UDP **and TCP**; TCP is required for truncation faults and large answers (spike S5).

- Normal resolution, with caching, static entries and logging per device.
- Faults per device, group or hostname pattern:
  - NXDOMAIN
  - SERVFAIL
  - timeout (no answer)
  - delayed answer
  - wrong or redirected answer (e.g. the MQTT broker hostname points to a local mock server)
  - truncated answer (forces TCP fallback)
  - short TTLs
- **Wiring:** Kea hands out each network's gateway address as DNS server (option 6). The proxy listens only on those addresses (UDP and TCP 53), so it coexists with systemd-resolved on 127.0.0.53. Its upstream resolver comes from the uplink (DHCP) or is configured. In V1 it strips AAAA records (test networks are IPv4-only). DNS faults are overlays; the proxy receives them from the API and keeps no state of its own across restarts.
- **Hardcoded resolvers:** DNS traffic to other resolvers (UDP/TCP 53) can be redirected to the gateway, and DNS-over-TLS (853) can be blocked. DNS over HTTPS cannot be distinguished reliably from normal HTTPS (see §6).
- **Hostname selectors (best effort):** rules and faults can target hostnames (exact name or `*.suffix`). The DNS proxy records which IPs it returned for which name and fills them into address sets, keyed per requesting device. It follows CNAMEs.
  - The set is updated **before** the answer is sent, so the device's first packet already matches.
  - Element lifetime is max(DNS TTL, 10 min) and is refreshed while conntrack still shows flows to the address. A long-lived connection therefore keeps its fault even when the answer's TTL is short (or was shortened by a "short TTL" fault).
  - Updates are deduplicated and batched, and written by the executor, the only process with network privileges. In spike S5, one `nft` process per answer limited the proxy to ~280 queries/s; with deduplication it reached ~6,700.

  The kernel still matches IP addresses, which has two consequences:
  - On CDNs or shared hosting, other services on the same IP are affected too.
  - Devices that do not use the gateway's DNS are not covered.

  The UI labels hostname selectors as DNS-derived.

## 2.7 DHCP

DHCP is both infrastructure and a test instrument.

- Pools, reservations per MAC, lease time, options (router, DNS, domain, NTP, custom).
- Test actions:
  - short lease times
  - delete a lease (on renewal the client simply gets the same address again)
  - **force a new IP**: change the reservation → the renewing client gets a NAK and requests a new address
  - change gateway/DNS options
  - refuse leases (DHCP silent)
- Server: **Kea** (spike S6). dnsmasq also handles NAK and silence, but cannot hand out leases shorter than 120 s. Kea offers an API for lease operations; reservation changes go through a configuration reload.
- **Wiring:** the API service talks to Kea's control socket (owned by the `chaosgw` group). Lease events reach Chaos Gateway through Kea's `run_script` hook, which notifies the API; they drive device discovery and the identity updates of §2.3.
- **Uplink DHCP:** the uplink is not managed by the OS network stack (§3.5), so the executor runs its own DHCP client for it. A changed uplink address triggers a recompile and an event.
- **Limitation:** the server cannot force a client to renew immediately. The FORCERENEW mechanism is rarely supported by clients. Renewal-related tests work through short lease times or through a link interruption.
- The UI shows DHCP inside the network and device views, not as a separate menu.

## 2.8 TLS and Application Layer

Selected traffic (by device, port or hostname) is redirected transparently to one of two components. No proxy configuration is needed on the device.

- **TLS responder** (part of the core, Go) for certificate test cases. It terminates the connection itself with a deliberately broken certificate generated for the requested SNI and never forwards traffic. A correct device aborts anyway.
- **TLS interception proxy** based on mitmproxy (sidecar, see §3.1) for inspection and manipulation.

Spike S4 confirmed the cases below on real TLS clients.

**What can be tested depends on the firmware:**

| Case | Production firmware (trusts only public CAs) | Development firmware (trusts the test CA) |
|---|---|---|
| Certificate from an untrusted CA | ✓ must reject | — (the test CA is trusted) |
| Expired / not-yet-valid certificate | not distinguishable: fails as "unknown issuer" | ✓ must reject with the specific error |
| Hostname mismatch | not distinguishable: fails as "unknown issuer" | ✓ must reject |
| Self-signed certificate | ✓ must reject | ✓ must reject |
| Handshake reset / closed / stalled | ✓ retry with backoff, timeout handling | ✓ same |
| Device accepts a broken certificate | ✓ detected (handshake completes at the responder) | ✓ detected |

A device without the test CA fails every certificate case at the first check ("unknown issuer"). Expiry and hostname validation can therefore only be tested individually on firmware that trusts the test CA. The most valuable test works with any firmware: *does the device accept a certificate it must reject?* If it does, the run records a failed security check. The expected result of each case comes from the device attribute `trusts_test_ca` (§2.3); for a device that trusts the test CA, accepting the "untrusted CA" case is correct.

Certificates are generated for the SNI in the ClientHello. Without SNI, the responder uses the name the DNS proxy last returned for the destination address, else a certificate for the IP address.

**Interception with inspection** (interception proxy; requires the device to trust the test CA, e.g. development firmware)

- Inspect and log HTTP/1.1, HTTP/2 and WebSocket traffic.
- Block specific URLs; return error codes (500, 503, 429); delay or throttle responses; modify requests and responses; abort connections.
- For other protocols over TLS (e.g. MQTT over TLS): raw byte stream view and connection-level faults. Protocol-aware MQTT manipulation is a later extension.
- The proxy writes a TLS key log, so captures can be decrypted in Wireshark.

**Limits:** QUIC/HTTP-3 is not intercepted. The option "block UDP 443" forces most clients back to TCP. Certificate pinning makes interception impossible by design; it then shows up as the expected rejection.

## 2.9 Profiles

Named fault sets, built-in and user-defined:

| Profile | Example parameters (up/down) |
|---|---|
| Normal | no impairment |
| LTE | 50 ms ± 10 ms, 0.1 % loss |
| Bad LTE | 150 ms ± 50 ms, 3 % loss, 2 Mbit/s |
| Satellite | 600 ms ± 30 ms, 1 % loss |
| Congested WiFi | 30 ms ± 20 ms, 2 % loss, bursts |
| Offline | blackout |
| Intermittent | flapping 20 s up / 10 s down |
| DNS broken | DNS SERVFAIL |
| TLS broken | handshake reset on TLS ports |

Values are one-way per direction. Profiles apply to a device, group, network or globally, and can be activated with a TTL. The built-in values are starting points to be calibrated. "DNS broken" and "TLS broken" become available with the DNS and TLS milestones (M20, M21).

## 2.10 Scenarios and Runs

A scenario is a timeline:

```yaml
name: mqtt-outage
target: { device: esp32-42 }
capture: true
steps:
  - { id: normal,  at: 0s,  profile: normal }
  - { id: slow,    at: 10s, fault: { latency: 200ms, jitter: 50ms } }
  - { id: lossy,   at: 20s, fault: { loss: 10% } }
  - { id: cut,     at: 30s, rule: { action: drop, protocol: tcp, port: 8883, cut_existing: true } }
  - { id: restore, at: 45s, restore: true }
checks:
  - { reconnected: { port: 8883 }, window: { from: restore, within: 30s } }
  - { no_unexpected_destinations: true }
```

The scenario format is defined normatively as a JSON Schema inside the OpenAPI spec. Check windows are relative to named steps.

- Scenarios are stored as YAML and can be versioned in git and imported/exported.
- Step types: profile, fault, rule, DNS fault, TLS case, DHCP action, capture start/stop, wait, restore.
- **Checks** evaluate observations, e.g. "device reconnected to the broker within 30 s", "device did not accept the invalid certificate", "no traffic to unexpected destinations".
- A **run** stores the timeline as executed, events, counters, captures, check results and a report (JSON and JUnit XML for CI).
- Run lifecycle: `queued → running → passed | failed | error | aborted` — `failed` means a check failed, `error` means the engine could not execute a step.
- Scenario steps create overlays owned by the run; a run never creates revisions.
- A run keeps going when the client that started it disconnects. It ends when it reaches its last step or is stopped explicitly (`POST /api/v1/runs/{id}/abort`). Either way, its overlays are removed.
- Optional **lease** for unattended automation: the client must renew the lease periodically (heartbeat). If it expires, the server aborts the run and cleans up. Without a lease, the scenario's own end and the overlay TTLs are the safety net.
- Timing precision: steps are applied within ±100 ms of their scheduled time (to be confirmed by measurement on native hardware or KVM; not under software emulation).

## 2.11 Capture

- Record by network, device, selector or rule ("capture everything this fault affects").
- Mechanism (spike S9, all exact against a capture on the device itself):
  - **Device and network captures:** AF_PACKET (libpcap) on the LAN-side interface with a BPF filter. Full Ethernet frames, pre-NAT addresses.
  - **Rule captures:** NFLOG from the rule's own nftables selector. Exact regardless of NAT, L3 packets only (link type NFLOG, readable by Wireshark).
  - Captures on the uplink cannot be attributed to devices after NAT and are offered only as "uplink capture".
- PCAP/PCAPNG, ring buffer, size and time limits, disk quota, automatic cleanup.
- Download, or stream live to Wireshark (chunked PCAPNG over HTTPS, e.g. `curl … | wireshark -k -i -`).
- TLS key log attached when the proxy was involved.
- Captures are attached to runs automatically when the scenario says so.

## 2.12 Diagnostics and Probes

**Diagnostics** run from the gateway and return structured results:

- ping (with loss/RTT statistics)
- TCP/UDP port check
- DNS lookup
- HTTP(S) request
- TLS handshake details
- traceroute
- path MTU
- throughput (iperf3 against a target)

Example:

```
ESP32-42 → broker.example.com:8883
DNS ✓   Route ✓   TCP ✓   TLS ✗ (certificate expired)
```

**Probes:** diagnostics from the gateway's own address do **not** pass through device-specific faults. Probes are virtual clients: a network namespace whose veth interface is a port of the test network's bridge (spike S12). They get an address via DHCP like a real device and can be targeted by rules, faults and profiles. They allow:

- verifying that a profile really produces the configured latency and loss (**calibration / self-test**)
- testing a scenario before connecting real hardware
- comparing: "with this fault, the probe reaches the broker in 1.8 s instead of 0.2 s"

## 2.13 Observability and Events

- Per rule and fault: matched packets and bytes, packets dropped/delayed by the fault. *"Active"* is not enough; the counters show whether a rule actually matches. Counters are monotonic across re-applies (S11: named nftables counters survive); they reset only on reboot, which the API reports as a counter epoch.
- Per device: traffic rates, current flows, DNS queries.
- System: interface counters, errors, queue drops, CPU load, disk usage.
- **Event stream** (SSE): configuration changes, overlays activated/expired, run steps, device online/offline, DHCP events, DNS faults triggered, TLS handshake outcomes.
- Metrics endpoint in Prometheus format.
- Audit log of all changes: who, when, via UI or API.

## 2.14 Configuration Lifecycle

```
edit → validate → preview → apply → verify → commit
                                  ↘ failure → roll back to previous revision
```

- **Preview (dry run):** shows the effective change in domain terms ("ESP32-42 upload: +200 ms") and, expandable, the Linux changes (nftables, tc, routes).
- **Apply** is idempotent. The compiler generates the complete target state; the executor applies the difference.
- **Consistency model** (central architecture principle): nftables, tc, routes, DHCP, DNS and the proxies cannot be changed in one shared transaction.
  - The committed revision is the only source of truth.
  - Apply runs in a fixed order and is verified afterwards.
  - On failure, the committed revision is recompiled and re-applied.
  - Short intermediate states during apply are possible; they never persist.
- **Verify:** reads back the kernel state and compares it with the target:
  - the `inet chaosgw` table carries the generation id of the applied revision and overlay set (in its comment), and its maps and sets hold exactly the expected elements;
  - tc classes exist and their netem parameters match within the kernel's rounding;
  - routes and rules in the Chaos Gateway routing tables match;
  - the managed services (Kea, DNS proxy, TLS responder) report healthy.

  The normalizers for `nft -j` and `tc -j` output have their own golden tests.
- **Commit-confirm:** changes that could lock out the admin (management access, uplink, firewall defaults) are rolled back automatically unless confirmed within 60 s.
- **Anti-lockout:** access from the management network to the gateway's **control plane** (UI, API, SSH) is always allowed and cannot be removed by rules. The protection covers nothing else: traffic from the management network to devices or the Internet follows the normal rules.
- **Revisions:** diff, rollback, export/import (secrets excluded by default), clone.
- **Concurrency:** every write names the revision it is based on. Conflicting writes are rejected (optimistic locking), so UI and automation cannot silently overwrite each other.
- **Drift detection:** Chaos Gateway only manages objects it owns: its own nftables table, qdiscs on its interfaces, routes with its own protocol tag. Changes to those are detected and can be reconciled. Objects it doesn't own are ignored.
- **Boot:** the last committed revision is applied at boot. Overlays are not restored after a restart; tests start from a clean baseline (§2.1.1).

## 2.15 API and CLI

- REST under `/api/v1`, described by OpenAPI (`api/openapi.yaml`, spec-first). UI and automation use the same API.
- SSE for events and live data.
- Main resources:
  - `uplink`, `networks`, `devices`, `groups`, `rules`, `profiles`, `scenarios` (read the active revision)
  - `revisions` (candidates, `…/preview`, `…/apply`, `…/confirm` for commit-confirm, `…/diff?base=`)
  - `overlays` (all kinds of §2.1.1, with owner, TTL and lease; `…/renew`)
  - `runs` (`…/abort`, `…/renew`, `…/report.json`, `…/report.xml`), `captures`, `diagnostics`, `probes`
  - `reset`, `metrics`, `events`, `auth` (sessions, tokens), `setup` (first start)

**Conventions**

- Errors as RFC 9457 `application/problem+json` with stable codes, e.g. `validation_failed`, `revision_conflict`, `verify_failed`, `lockout_protected`, `target_busy`.
- Every object has a UUID and a unique, renameable name; the API accepts either in paths.
- Lists use cursor pagination and simple filters (`?network=`, `?owner=`).
- Writes to configuration carry `If-Match` with the base revision; the active revision is the `ETag`.
- Automation can send an `Idempotency-Key` header, so a retried request does not create a second overlay or run.
- SSE events have ids; a client reconnecting with `Last-Event-ID` gets the missed events from a replay buffer (at least 10 min).
- The version stays `/api/v1` for additive changes; a breaking change gets `/api/v2`, and v1 is kept for at least one release.
- Typical automation calls:

```http
POST /api/v1/overlays
{ "target": {"device": "esp32-42"}, "profile": "bad-lte", "ttl": "5m" }

DELETE /api/v1/overlays/{id}

POST /api/v1/scenarios/mqtt-outage/runs     → run id
GET  /api/v1/runs/{id}                        → status, checks, artifacts
GET  /api/v1/runs/{id}/report.xml             → JUnit
POST /api/v1/reset                            → remove all overlays, stop runs
```

- Writes return only after verification, so a test step can rely on the fault being active.
- **CLI and clients:** a CLI (`chaosctl`, a single Go binary) for CI pipelines, and API clients generated from the OpenAPI spec: TypeScript via Orval (Vue Query hooks for the UI plus a plain fetch client for Jest suites) and Python (for pytest suites).
- Optional later: MQTT control interface.

## 2.16 Security

- The UI/API listens only on the management network, over HTTPS (self-signed certificate by default, replaceable).
- **First start:** until setup is finished, the UI listens on all interfaces and requires a one-time setup token that the package installation prints and writes to the journal. The setup wizard assigns interfaces, sets the admin password and then restricts the UI to the management network.
- Devices under test are untrusted: test networks cannot reach the management plane. WireGuard networks with role *test* are treated the same; only WireGuard networks with role *management* (§2.2.1) may reach the UI/API.
- V1: one admin account plus API tokens (scoped: read-only, overlays only, full).
- **Sessions:** the UI uses an HTTP-only session cookie with CSRF protection; SSE uses the same cookie. Automation uses bearer tokens; tokens are stored only as hashes and shown once at creation.
- Privilege separation: the API server runs unprivileged. Only the executor has network privileges; it runs as root under systemd hardening (restricted file system, capability bounding set `CAP_NET_ADMIN CAP_NET_RAW CAP_SYS_ADMIN` for namespaces, no new privileges).
- The executor accepts typed, validated operations, never command strings (see §3.1). It also checks their scope: nftables operations may only touch `table inet chaosgw`, routing operations only Chaos Gateway's routing tables and rules, tc operations only interfaces assigned to Chaos Gateway.
- The executor's Unix socket checks the caller with `SO_PEERCRED` and starts every connection with a protocol-version handshake; API and executor refuse to work with a mismatched version (e.g. halfway through a package upgrade).
- Secrets live in a protected directory (0600): admin password hash, API tokens, test CA key, TLS keys, WireGuard keys. They are excluded from logs, events, API responses and exports by default.
- The test CA key and decrypted traffic in captures are sensitive; captures with key logs are marked accordingly.

## 2.17 Web UI

**Reference prototype:** a clickable design of the main screens is kept in [`docs/ui/prototype/`](ui/prototype/) (one `.dc.html` file per screen, sample data). Where this section and the prototype disagree, this section wins.

### Design direction

A modern developer tool / observability dashboard — dark, compact, live, with the data path visualized. The pattern to follow is "Grafana meets firewall rule editor meets network lab", not a router configuration interface. Linux terms appear only in the technical views (§ "Technical view" below).

**Principles**

- Devices are the primary entry point. Actions start where the object is ("Add fault" on the device).
- Progressive disclosure: the common case needs three fields; everything else is under *Advanced*.
- Everything is connected: a device shows its network, flows, rules, faults and captures, and each links onward.
- No separate "modes" (router/firewall/chaos).
- Nothing changes the network without the user seeing what will change (preview) and whether it worked (verified, counters).
- Every UI action has an API equivalent, and the UI can show it (`</> API`).

### Visual system

| Token | Value | Use |
|---|---|---|
| `bg` | `#0E1116` | page background |
| `sidebar` | `#0B0D11` | navigation |
| `surface` / `surface-2` | `#151A21` / `#1B212A` | cards / raised items, selected rows |
| `border` / `border-strong` | `#232A34` / `#2F3844` | card borders / inputs, secondary buttons |
| `text` / `text-2` / `text-3` | `#E7EAEE` / `#A3ADBA` / `#7D8796` | primary / secondary / meta text (all ≥ 4.5:1 on surface) |
| `accent` | `#6CB2FF` (strong `#3D6A99`, tint `#1F3550`, text `#8CC4FF`) | normal state, primary actions, selection, links |
| `fault` | `#FFA24C` (border `#6B4420`, tint `#2A1C10`, text `#FFB870`) | anything that impairs traffic: fault chips, active profiles, countdowns |
| `block` | border `#6B2E2E`, tint `#2B1717`, text `#FFB3B3` | drop/reject, failed checks, abort |

- Blue means "as configured / normal", orange means "traffic is being impaired", red means "blocked or failed". Status is never shown by color alone: every chip and dot has a text label.
- Type: **IBM Plex Sans** for UI text, **IBM Plex Mono** for addresses, ports, counters, times, parameters and code. Sizes 12 / 13 / 14 / 16 / 20–26 px.
- Spacing on a 4 px grid; cards 10 px radius, controls 6 px; controls 34–38 px high (dense desktop tool).
- Icons: 16 px inline stroke icons, 1.5 px stroke.
- Recurring components: fault chip (mono, orange), access chip (Allow blue / Drop·Reject red), status dot + label, segmented control, switch, card, table row, toast, sticky action bar, side drawer, modal dialog, countdown banner.

### Navigation

```
Overview · Devices · Networks · Rules & Faults · Profiles · Scenarios · Captures · Diagnostics · Activity
Header per page: title + context line (revision, sync state) · health · active-faults counter · </> API · primary action
```

### Screens

| Screen | Content | Key interactions |
|---|---|---|
| **Overview** | network topology (test networks → gateway → uplink) with fault badge on the path; active faults with target, effect, affected packets, expiry; running scenario with progress; uplink traffic chart; recent events | every item links to its detail |
| **Devices** | table: name, IP, MAC, network, traffic, faults, status; search; filter chips (All / Online / With faults / Not adopted) | adopt discovered devices; row opens device detail |
| **Device detail** | header with identity; active faults with parameters, affected packets and remaining TTL; path view MQTT → broker with fault badge; flows; DHCP lease and DHCP test actions; recent DNS; captures; optional API panel | **Add fault** dialog (below); remove a fault; apply profile; TLS test; capture; diagnose |
| **Add fault** (dialog) | target, traffic, direction (Both / Upload / Download), start-from presets (LTE, Bad LTE, Satellite, Offline), latency, jitter, loss, duration; *Advanced*: rate, reorder, duplicate, corrupt, keep packet order; live preview sentence | Apply → fault appears with TTL; toast; API panel shows the equivalent `POST /api/v1/overlays` |
| **Rules & Faults** | ordered rule list: position, name, selector, access chip, fault chips, hit counter, enable switch; system rule "Control plane access" locked at the top | select a rule → editor: **IF** from / to / protocol / ports, **THEN** access (Allow · Drop · Reject · TCP reset) + "also cut existing connections", "impair matching traffic" with parameters; matches box with hits, bytes, last match and the effective result in words; hostname hint for DNS-derived matching |
| **Preview & apply** (drawer) | "what changes" in domain terms (rule, field, old → new); expandable Linux changes (nft, tc, verify step) | Back to editing · Apply revision N → verified toast |
| **Profiles** | cards per profile with parameters and where it is active; scope selector (everything / network / group / device) and duration | activate / deactivate on the chosen scope (one profile per scope); "Measure with probe" shows the measured values against the profile |
| **Scenarios** | list with target, length, steps, last result; tabs Timeline / YAML / Runs; timeline of steps with type chips; checks; run artifacts | Run → live progress, current step marked, checks resolve to PASSED/FAILED at the end, artifacts appear (pcapng, JUnit, JSON, events); Abort → state restored |
| **Networks** | cards for test networks, management (owned by the OS) and uplink; settings of the selected network (subnet, gateway, attachment, DHCP, DNS, IPv6 blocked); access matrix between networks | toggling a matrix cell applies it with **commit-confirm**; Linux view shows the compiled forward chain |

Not yet designed: Captures, Diagnostics (incl. probes), Activity, setup wizard, login, API tokens, settings.

### States every screen must handle

| State | Pattern |
|---|---|
| Empty (no devices, no faults, no runs) | dashed box with one sentence and the primary action |
| Unapplied changes | sticky bar at the bottom: "N changes in M rules · not applied yet" · Discard · Preview & apply; changed rows marked CHANGED |
| Preview | side drawer, marked "dry run · nothing changed yet" |
| Applying / applied | button busy state; toast "Revision N applied and verified in X ms" |
| Apply failed | inline error in the drawer with the failing step; "revision N-1 restored" |
| Commit-confirm | orange banner with live countdown, Confirm / Roll back now; controls that would change more are disabled until resolved; on timeout a notice "rolled back to revision N-1" |
| Expiring overlays | remaining time next to each fault/profile; toast when one expires |
| Concurrent change (optimistic locking) | dialog: "revision changed by <user/token> while you were editing" · show their change · reapply mine on top · discard mine |
| Run states | pending · running (progress, current step) · passed · failed · aborted (with restored revision) |
| Validation | message under the field, apply disabled until valid |
| Offline / backend unreachable | banner at the top, live data greyed out with "last updated" time |
| Safe mode | full-width red banner: forwarding disabled, reason, link to recovery |

### Accessibility

Real buttons, links and labelled inputs; visible focus ring (`accent`, 2 px); `aria-pressed` on toggles, `role="switch"` on switches, `aria-live` status for toasts and countdowns; text contrast ≥ 4.5:1; status never by color alone; keyboard access for every action in dialogs and drawers.

### Technical view

Per object, the compiled configuration next to the domain view: nftables rules, tc tree, routes, service configs and their live counters. Reached from "Show Linux view" / "Show Linux changes" links, never needed for normal use.

---

# 3. Technical Architecture

## 3.1 Components and Processes

```
┌──────────────────────────────────────────────────────────────────────┐
│ chaosgw api   (Go, unprivileged user)                                │
│   REST/SSE · auth · domain model · validation · compiler · scheduler │
│   (overlays/TTL, scenarios) · observers · persistence · embedded UI  │
└───────────────┬──────────────────────────────────────────────────────┘
                │ Unix socket, typed operations (JSON schema)
┌───────────────▼──────────────────────────────────────────────────────┐
│ chaosgw exec  (Go, root with systemd hardening, see §2.16)           │
│   applies plans: nft -j -f · tc -batch · ip -batch · sysctl ·        │
│   conntrack · wg · capture processes · probe namespaces ·            │
│   uplink DHCP client · netlink session for DNS-derived address sets  │
│   reads state: ip -j · tc -j · nft -j · conntrack events             │
└───────────────┬──────────────────────────────────────────────────────┘
                │
    Linux kernel: nftables · conntrack · tc/netem · routing · netns

Services managed by chaosgw:
  chaosgw dns    DNS proxy (Go; part of the project)
  chaosgw tls    TLS responder for certificate test cases (Go; part of the project)
  DHCP server    Kea (decided in spike S6)
  tls-proxy      mitmproxy + Chaos Gateway addon (Python sidecar, interception only)
  tcpdump        capture
  BIRD 2         routing daemon, own instance (chaosgw-bird), config generated by the compiler
```

- The core is **one Go binary, `chaosgw`**, with subcommands for the API server, the privileged executor, the DNS proxy and the TLS responder. Each runs as its own systemd unit with only the privileges it needs; the web UI is compiled into the binary (`go:embed`). The spikes built the DNS proxy and TLS responder in Node.js; their findings are language-independent.
- Python runs only in the TLS interception sidecar (mitmproxy addon). It communicates with the core via a local API and can be left out entirely if TLS interception is not used; the certificate test cases work without it.
- The executor accepts only a closed set of operation types. Each is validated and turned into command invocations with argument arrays: no shell, fixed binary paths. It checks the scope of every operation (§2.16) and is the **only writer** to the kernel's network configuration, so all changes are serialized — including the DNS proxy's address-set updates, which reach it through a narrow "add elements to set" operation over the same socket.
- The executor protocol is versioned; both sides check the version and the peer credentials when a connection starts (§2.16).
- The Linux adapter uses the standard command-line tools in V1 (§3.4). It sits behind an interface, so parts can later be replaced by native netlink access (fewer process starts, better error details, events) without changing the compiler.
- Every executor operation takes an optional **network namespace**. This makes the entire stack testable in namespaces without touching the host (see §4).

## 3.2 Compiler

```
Configuration (revision) + Overlays
          │
          ▼
Effective policy  (resolve devices→MAC/IP, groups, hostnames→IP sets,
          │        precedence, fault per scope and direction)
          ▼
Target state      (nftables ruleset JSON, tc tree per interface,
          │        routes, sysctls, service configs)
          ▼
Diff against current state → execution plan → executor
```

- The compiler is a pure function (input → target state). It can be fully tested with golden files.
- Three levels are kept apart:
  - **Domain diff:** what changed, in the user's terms. This is what the preview shows.
  - **Execution plan:** which Linux operations are needed, per subsystem, in a fixed order.
  - **Linux operations:** how each subsystem is changed.
    - nftables: one atomic transaction per apply that **re-creates the static structure but keeps dynamic data** (spike S11): `add table/set/map/counter/chain` (a no-op for objects that exist), `flush chain` and `flush map` for the compiled parts, then the new rules and map elements. Never deleted: the DNS-derived address sets (their elements are written by the executor between applies) and the named rule and fault counters, which stay monotonic. S11 re-applied the ruleset 20 times while a device sent 1500 packets through a fault: not one packet lost its classification, and a set element and the counters survived.
    - tc: existing qdiscs/classes are changed in place. Replacing a qdisc of the same kind keeps its queue too (S2); only deleting it or changing its kind drops queued packets.
- **Complete parameter sets:** `tc qdisc change` keeps netem attributes that are not given; spike S2 showed a rate limit surviving a change. The compiler therefore always emits every netem attribute, with neutral values where unused (e.g. `rate 0bit`).
- **nftables as JSON:** the compiler generates the ruleset as JSON, not text. Text syntax has pitfalls that the spikes hit repeatedly: reserved words used as chain names (`fwd`, `dnat`) and a missing `;` before `}` make the whole transaction fail.
- Overlay changes (the frequent case) touch faults, marks and classification maps; services only through their runtime control channels (DNS proxy, TLS responder, Kea control socket); never routing.

## 3.3 Packet Path and Classification

Linux traffic control acts on the **egress** (outgoing) side of an interface. Egress on the uplink comes **after** NAT, where the device's IP is already replaced by the gateway's. Classification therefore happens in nftables, where the original addresses are still known, and the result is handed to tc as a packet mark. Spikes S10 and S2 confirmed this design on the Ubuntu 24.04 kernel:

```
             LAN interface                                   Uplink interface
Device ──► prerouting (nftables)                                   │
             • identify device (identifier sets)                   │
             • resolve effective fault → classification id         │
             • write id into reserved mark bits                    │
           forward: access rules (drop / reject / reset)           │
           postrouting: masquerade ───────────────────────► egress: tc
                                                            fw filter: mark → class
                                                            class → netem  (UPLOAD fault)

Server reply ──► prerouting on uplink:
                   classify again via conntrack original tuple
                   (device address before NAT) → same id
                 forward ───────────────────────────────► egress on LAN: tc
                                                          fw filter: mark → class
                                                          class → netem  (DOWNLOAD fault)
```

- **Classification identity:** after precedence resolution, the compiler assigns an id to each *effective fault configuration*, not to each rule. Configurations with a rate, an explicit queue limit or "keep order" get one id **per matched device** (D18, §2.4); all others one id per configuration. Ids stay stable while the winning fault stays the same; parameter changes are applied to the existing classes in place.
  - Access rules need no marks; they are evaluated directly in nftables.
  - **Mark layout:**

    | Bits | Meaning |
    |---|---|
    | 4–15 | effective-fault id, 12 bits: up to 4095 ids at the same time (widened from 8 bits because of per-device queues, D18) |
    | 16 | direction: 0 = packet in the connection's original direction (upload of the initiator), 1 = reply (download), from `ct direction` |
    | 17–23 | reserved for routing marks matched by `ip rule fwmark`; e.g. bit 21 (`0x00200000`) selects the PMTU policy route of §2.5 (S13) |
    | 0–3, 24–31 | untouched, free for other software |

  - With the direction bit, every interface uses the **same** mapping (id, direction) → tc class. Without it, the meaning of an id depended on the interface, which breaks as soon as two test networks exist: the second network's interface carries both its own devices' downloads and the first network's uploads towards it. Spike S11 showed exactly that: a group fault (100 ms up, 20 ms down) on devices in two networks gave the correct 120 ms for traffic from network A to network B with the direction bit, and 43 ms without it.
  - **Lookup chain** in the order of §2.4 precedence, each a verdict map keyed on the conntrack original tuple, so it is identical for both directions and unaffected by NAT:
    1. `ct original ip saddr . ct original ip daddr . meta l4proto . ct original proto-dst` (device + destination + port)
    2. `ct original ip saddr . ct original ip daddr` (device + destination, including DNS-derived hostname sets)
    3. `ct original ip saddr . meta l4proto . ct original proto-dst` (device + port)
    4. `ct original ip saddr` (device)
    5. then the same for groups and networks, then global

    S11 verified levels 2 and 4 together: a device+destination fault (50 ms) won over the device's group fault (120 ms) for that destination only.
  - Capacity: 4095 ids; each active id needs up to two HTB classes (upload, download) per interface it leaves through. The compiler enforces a configurable class limit per interface (default 1000 on x86, set for Raspberry Pi from H1) and reports `capacity_exceeded` with the scope that caused it. Example: a rate-limited profile on a network with 250 devices needs 500 classes. S8 measured 50 classes; 500 and more are measured on the target hardware (H1).
- **Per packet, not per connection:** the id is computed for every packet, using the conntrack original tuple for replies. Caching it in the conntrack mark would keep existing connections on the old fault after an overlay changes (S10 showed exactly that). Spike S8 found no measurable throughput cost with 1000 device and 1000 device+port entries on x86; Raspberry Pi still has to be measured.
- **tc topology** (confirmed in S2 and S11): an HTB root with a default class and one class per active (id, direction), each with a netem leaf, selected by a `fw` filter with mask (id 0x0a: `handle 0x000a0/0x1fff0` for upload, `0x100a0/0x1fff0` for download; the spikes used the earlier 8-bit layout `0x00a00/0x1ff00`, same mechanism).
  - A classful root is needed as soon as more than one fault is active on an interface; `prio` would be too small, since it is limited to 16 bands.
  - Rate limiting works both as netem `rate` and as HTB class rate. With 50 HTB classes, throughput dropped by about 10 % (S8).
- **Traffic terminating at the gateway** (DNS proxy, TLS responder, TLS proxy):
  - DNS faults are implemented inside the DNS proxy (delay, drop, wrong answer); TLS/HTTP faults inside the TLS components.
  - **Download** (replies generated on the gateway) never passes prerouting. The same classification chain is therefore also attached to the **output** hook; the key is identical because conntrack keeps the original tuple of the redirected connection (spike S14).
  - **Upload** (packets towards the gateway) has no egress; it uses IFB ingress redirection with `flower` L3 filters (marks are not yet set at tc ingress; S2 confirmed +100 ms for one device, the other unaffected). The compiler generates these flower filters from the same effective policy as the nftables maps, updates them on address changes, and includes them in verify and drift detection.

## 3.4 Linux Interface Layer

- **CLI first:** in V1 the executor uses the standard tools with JSON output and batch input. Replacing parts with native netlink later is possible (§3.1); Go has mature libraries for it (`vishvananda/netlink` for links, addresses, routes and tc including netem/HTB; `google/nftables` for nftables).
- **Exception from the start:** DNS-derived address-set updates go through a persistent netlink connection (`google/nftables`) held by the executor, instead of one `nft` process per answer, which limited the spike proxy to ~280 queries/s (S5).

| Tool | Read | Write |
|---|---|---|
| nftables | `nft -j list` | `nft -j -f` (atomic transaction) |
| iproute2 | `ip -j` | `ip -batch` |
| tc | `tc -j` | `tc -batch` |
| conntrack-tools | `conntrack -L` | `conntrack -D` |
| WireGuard | `wgctrl` (netlink) / `wg show` | `wgctrl` (netlink), interfaces via `ip -batch` |
| BIRD 2 | `birdc show protocols/route` (control socket) | generated config file, `bird -p`, `birdc configure` |

- Event sources, for live data without polling: `ip monitor`, `nft monitor`, `conntrack -E`.
- Counters are polled once per second from nftables and tc (configurable).
- **Preflight check** at start and install. It verifies:
  - kernel version
  - modules — one list, shared with the test-container preflight (§4.5): `sch_netem`, `sch_htb`, `cls_fw`, `cls_u32`, `cls_flower`, `act_mirred`, `ifb`, `nf_conntrack`, `nf_tables` with NAT/ct/log/dup/reject, `nfnetlink_log`, `veth`, `bridge`, `wireguard` (M33), later `8021q`
  - tool versions
  - IP forwarding
  - conflicting firewalls (ufw, firewalld, Docker). With Docker installed, the FORWARD policy is DROP; an accept rule in Chaos Gateway's own table does not override it, only an accept in Docker's `DOCKER-USER` chain does (spike S7). The preflight offers to add that rule.
  - other network managers owning the assigned interfaces

  Missing items are reported with the fix. On Ubuntu 24.04 generic kernels all required modules are in the base `linux-modules` package. Minimal kernels, such as some VM or cloud sandboxes, can lack netem entirely.

## 3.5 Host Ownership

Chaos Gateway owns the interfaces **assigned to it** (uplink, test networks) and leaves the management interface to the operating system. That interface is configured by netplan or NetworkManager as usual.

- At setup, assigned interfaces are marked as unmanaged for netplan/NetworkManager/systemd-networkd, so no two tools fight over them.
- Its own nftables table (`inet chaosgw`), its own routing table and `ip rule` entries (policy routing, §2.2), its own route protocol tag, one bridge per test network, qdiscs only on its own interfaces and bridges, router-advertisement acceptance off on its interfaces.
- In the two-port topology the management network shares the uplink: the control plane is then reachable from the uplink side, and the anti-lockout rule applies to the management addresses configured at setup.
- Other firewalls: detected by the preflight check. A drop verdict in another table still drops packets, so conflicts are reported, not silently ignored.

## 3.6 Persistence

```
/etc/chaos-gateway/
    config.json            pointer to the active revision
    revisions/000042.json  immutable revisions (incl. profiles and scenarios)
/var/lib/chaos-gateway/
    secrets/               0600
    runs/<id>/             events.jsonl, report.json, report.xml, captures
    captures/
    state/                 last-known-good marker, record of runs aborted by a restart
/var/log/chaos-gateway/    audit.jsonl, service logs
```

- Writes are atomic: write a temp file, fsync, rename.
- Schema version in every file, with migrations on upgrade.
- No database in V1. Run and event history are JSONL files with retention limits.
- **Retention defaults** (configurable): 200 revisions; runs 30 days; captures 5 GB quota, oldest first; events 7 days; audit log 1 year.
- **Disk low** (< 10 % free): new captures are refused and running captures stop; runs themselves continue and report the missing capture.

## 3.7 Technology Stack

*sessile* is the maintainer's earlier project with the same stack shape (Go + Gin backend, Vue 3 + Vite frontend, one Makefile); it is referenced below where a choice is carried over from it.

**Backend (Go)**

| Area | Choice |
|---|---|
| Language | Go (current stable), one module, one binary `chaosgw` plus `chaosctl` |
| HTTP / SSE | Gin (known from sessile); SSE for live data |
| API contract | **spec-first**: `api/openapi.yaml` is the source of truth; `oapi-codegen` generates the Go server interfaces and request/response types; request validation from the spec |
| Linux | `nft -j -f` / `tc -batch` / `ip -batch` via the executor (§3.4); `vishvananda/netns` for namespaces; `google/nftables` for DNS selector sets |
| DNS proxy | `miekg/dns` (UDP and TCP) |
| TLS responder | Go standard library `crypto/tls`, `crypto/x509` |
| DHCP | Kea, driven through its JSON control socket |
| Config / scenarios | JSON (revisions), YAML (`gopkg.in/yaml.v3`) for scenarios and profiles |
| Logging | `log/slog`, JSON to journald |
| CLI | `chaosctl` with Cobra |
| WireGuard | `golang.zx2c4.com/wireguard/wgctrl` (netlink) for peers and status; keys via `wgtypes`; QR codes generated in the backend (`skip2/go-qrcode`, PNG/SVG) |
| Routing | BIRD 2 (`bird2` package), own instance; configuration generated from `text/template`, status via the BIRD control socket |
| Tests | `go test` (unit, compiler golden files, Linux integration against the testbed) |

**Frontend (Vue)**

| Area | Choice | Why |
|---|---|---|
| Framework | Vue 3 + TypeScript + Vite | known from sessile; single-page app embedded into the Go binary |
| Server state | TanStack Query for Vue, invalidated by SSE events | caching, live updates, conflict handling (§2.17 states) |
| API client | generated from `openapi.yaml` (Orval: typed Vue Query hooks + Zod schemas) | UI and backend cannot drift apart; client-side validation from the same spec |
| UI primitives | Reka UI (accessible, unstyled dialogs, selects, switches, tabs, toasts, tooltips) | complete keyboard and ARIA support; more components than Headless UI |
| Styling | Tailwind CSS 4 with the §2.17 tokens as theme variables | tokens in one place, dense layouts |
| UI state | Pinia (only for UI state; server data stays in the query cache) | |
| Charts | uPlot | small and fast for live time series |
| Tests | Vitest (components), Playwright (end-to-end against the testbed) | |

**Build and packaging:** Makefile as in sessile (`make dev`, `make test`, `make build`); the frontend build lands in `web/dist` and is embedded; `.deb` for amd64/arm64 with systemd units; container image for development and demos; mitmproxy sidecar in its own Python virtualenv.

```
chaos-gateway/
  api/                openapi.yaml (source of truth)
  cmd/
    chaosgw/          subcommands: api, exec, dns, tls
    chaosctl/         CLI
  internal/
    domain/           concepts, validation, precedence resolution
    compiler/         effective policy, target state, diff
    linux/            command builders, parsers (nft/tc/ip JSON), netlink
    executor/         privileged operations, socket protocol
    apiserver/        handlers (generated interfaces), auth, SSE
    scheduler/        overlays/TTL, scenarios, checks
    dnsproxy/  tlsresponder/  dhcp/  capture/  store/
    testbed/          namespace topology harness (from spike S1)
  web/                Vue app (src/), build output dist/ embedded via go:embed
  clients/            generated TypeScript and Python clients
  sidecars/tls-proxy/ mitmproxy addon
  profiles/  scenarios/  packaging/  docs/
```

## 3.8 Deployment

- **Primary: native installation** as a .deb package on Ubuntu/Debian, with a setup wizard in the UI: select uplink, select test interfaces, set admin password.
- **Container:** for development and demos. Running the gateway itself in Docker on a host is possible, with limits:
  - It requires host networking plus `NET_ADMIN`/`NET_RAW`.
  - IP forwarding and other sysctls cannot be set from a non-privileged container; they must be set on the host (S7).
  - Probes need network namespaces, which requires `SYS_ADMIN` in the container (S7).
  - Docker's own rules set the FORWARD policy to DROP. Routed test traffic only passes with an accept rule in `DOCKER-USER` (S7).
  - The kernel modules must be present on the host.

  This path is documented but not the recommended appliance setup.
- **Dedicated appliance image** (Raspberry Pi / x86) as a later option.

## 3.9 Updates and Recovery

- Package updates through apt. Configuration migrations run at service start, with a backup of the previous revision.
- **Last known good:** if applying the configuration at boot fails, the gateway applies the last revision that worked. If that fails too, it starts in **safe mode**: management access only, no forwarding, UI shows the error.
- An interrupted apply (power loss) is recovered at boot by recompiling from the committed revision. The kernel state is never the source of truth.
- **Upgrade during a run:** the package's maintainer scripts defer the service restart while a run is active (up to the run's scheduled end, then the restart proceeds and the run ends as `aborted`).
- **Downgrade:** a binary refuses to start on configuration with a newer schema version and says which version is needed.

## 3.10 Performance Targets (to be validated)

| Target | Raspberry Pi 4/5 class | x86-64 (e.g. NUC) |
|---|---|---|
| Forwarded throughput with faults active | ≥ 100 Mbit/s | ≥ 1 Gbit/s |
| Throughput through TLS proxy | ≥ 20 Mbit/s | ≥ 200 Mbit/s |
| Devices | 50 | 250 |
| Simultaneous faults | 50 | 250 |
| Overlay apply latency (API call → active) | ≤ 200 ms | ≤ 100 ms |
| Capture without loss | 50 Mbit/s | 500 Mbit/s |

- The packet plane stays in the kernel; only proxied traffic goes through user space.
- No flow offloading, because it would bypass rules, faults, counters and capture.

---

# 4. Test Strategy

## 4.1 Test Levels

| Level | What | Tools | Where |
|---|---|---|---|
| Unit | domain, validation, precedence resolution, scheduler, parsers | `go test`; Vitest for UI components | every commit |
| Compiler golden | configuration → nftables/tc/route output, compared with reviewed golden files | `go test` | every commit |
| Linux integration | real kernel behavior in namespaces: routing, NAT, faults, rules, DNS, DHCP, TLS proxy | `go test` + testbed in the privileged test container (§4.5 level 1) | every commit |
| Measurement | statistical accuracy of faults, timing of scenarios | testbed | nightly, dedicated runner |
| API contract | OpenAPI conformance, error cases, concurrency | `go test` against the spec; generated clients compile | every commit |
| UI | components against a mocked API; a few end-to-end flows against the real stack in the testbed | Playwright | every commit / nightly |
| Distribution | install package, preflight, smoke tests on clean Ubuntu 24.04 and 26.04, Debian 12/13, ARM64 | appliance VMs (§4.5 level 2, harness built in M5b) | nightly / before release |

## 4.2 Testbed

A library that builds topologies from network namespaces and virtual interfaces (see §4.5 for the building blocks). Default topology, as used in the spikes; the default for product tests has **two** test networks, because some errors only appear there (spike S11: fault direction between two networks), and attaches each network through a gateway-side bridge as in production (§2.2):

```
 ns: client-a ─┐                                                 ┌─ ns: server
 ns: client-b ─┼─ ns: switch ─ lan0 [ ns: gateway ] wan0 ────────┤  (HTTP, TLS, MQTT broker,
 ns: probe    ─┘   (bridge)      Chaos Gateway stack runs         │   DNS upstream, iperf3, NTP;
                                 against this namespace           │   no route back → NAT required)
```

- A second test network (`lan1` ─ switch ─ client-c) and a management interface with its own default route (as in spike S12) are part of the default topology.
- Tests start the real API and executor, pointed at the gateway namespace. Nothing touches the host network.
- The server namespace provides reference services, with valid and invalid certificates for TLS tests.
- Clients use standard tools: `ping`, `curl`, `openssl s_client`, `dig`, `dhclient`/`udhcpc`, `iperf3`, `mosquitto_sub`, plus a small measurement tool.

## 4.3 Measuring Faults

Faults are random processes; tests use statistics, not exact values:

- **Latency:** send N ≥ 200 probes, then compare the median with the configured delay (tolerance ±2 ms + 5 %) and the spread with the configured jitter.
- **Loss:** send N ≥ 2000 packets. The measured loss must lie within the 99 % binomial confidence interval of the configured rate.
- **Rate:** iperf3 throughput within ±10 % of the limit.
- **Isolation:** every fault test also measures one unaffected device or traffic class. It must show no change.
- **Timing:** scenario steps are timestamped by the event stream and checked against the schedule, with a tolerance.
- **Where:** accuracy and timing assertions run only with native execution or KVM. Under software emulation (level 1b without KVM) the same tests run with functional assertions only (effect present, direction and isolation correct), because emulation adds tens of milliseconds of noise.

## 4.4 CI

- CI jobs run the same privileged test container as development (§4.5). Level 1 runs on every commit; if the runner's kernel lacks required modules, the affected tests run in level 1b instead of being skipped silently.
- Measurement tests run nightly on a runner with native execution or KVM (stable timing), not under software emulation.
- Level 2 (appliance VMs, distribution matrix) runs nightly on a runner with KVM; level 3 before releases.

## 4.5 Test Environments

**Principle:** almost every feature is tested with virtual network interfaces inside network namespaces. Namespaces are fully isolated from the host network and from each other, and a topology is built in milliseconds. What namespaces cannot change is the **kernel**: all namespaces and containers on a machine share its kernel and its modules. The kernel therefore decides which features can be tested where.

### Virtual building blocks

| Building block | Represents | Used for |
|---|---|---|
| network namespace | a device, the gateway, a server | every topology |
| veth pair | a cable | links between namespaces |
| bridge (optionally VLAN-filtering) | a switch | LAN segment with several devices, trunk ports |
| VLAN subinterface | tagged network on a trunk | VLAN networks (M31) |
| macvlan | several devices behind one port | many devices without many veth pairs |
| dummy | a local address or sink | services, routing tests |
| IFB | ingress shaping | faults on traffic to the gateway itself |
| WireGuard interface | tunnel endpoint | WireGuard networks, clients and site links (M33, M37); remote sites are namespaces with their own WireGuard interface and BIRD |
| tap | a VM's NIC | connecting appliance VMs (level 2) |

Link events are simulated by setting one end of a veth pair down; the other end loses its carrier. Devices are namespaces with their own MAC, a DHCP client (`udhcpc`) and test tools, so "a device behind another router" or "several LANs" are just different topologies.

### Levels

| Level | Environment | What it can test | When |
|---|---|---|---|
| **0** | plain process, no root | domain, validation, compiler golden files, API contract, UI against mocked API | every commit |
| **1** | namespace testbed in the **privileged development/CI container** | routing, NAT, access rules, faults (functional), DNS proxy and faults, DHCP and test actions, TLS responder and interception, capture, probes, API and UI end-to-end, scenarios | every commit |
| **1b** | namespace testbed inside a VM with a **stock distribution kernel** (QEMU + virtme-ng) | same as level 1 when the host kernel lacks modules (e.g. no netem), or to check a specific distribution kernel; the decision uses the same module list as the product preflight (§3.4) | when needed; nightly for the kernel matrix |
| **2** | **appliance VMs** (QEMU/KVM) from distribution images, gateway VM with three virtio NICs (uplink, test LAN, management) connected via tap and bridges to client/server namespaces or VMs | .deb installation, preflight, interface assignment, coexistence with netplan/NetworkManager/systemd-networkd, setup wizard, reboot, last-known-good, safe mode, upgrades and migrations, coexistence with Docker (`DOCKER-USER`) | nightly, from M5b on |
| **3** | **hardware lab**: Raspberry Pi 4/5, x86 mini PC, real NICs, managed switch, real ESP32 devices (later a WiFi AP) | performance targets (§3.10), timing precision, NIC drivers and offloads, real firmware behavior, long-running tests | before releases |

A candidate for levels 1–2 is Espressif's QEMU fork, which can run ESP32 firmware with an emulated Ethernet interface. That would allow testing real ESP-IDF firmware against the gateway without hardware; it has to be evaluated first.

### Feature → minimum level

| Feature | Level |
|---|---|
| Compiler output, precedence resolution, validation | 0 |
| Routing, NAT, access rules, connection behavior | 1 |
| Faults: function (effect, isolation, direction, live changes) | 1 |
| Faults: accuracy measurements, scenario timing | 1 on native execution or KVM; 3 for Raspberry Pi |
| DNS proxy, DNS faults, hostname selectors | 1 |
| DHCP (Kea) and DHCP test actions | 1 |
| TLS responder, mitmproxy interception | 1 |
| Capture | 1 |
| Probes and calibration | 1 |
| VLANs, WireGuard, IPv6 | 1 (kernel modules `8021q`, `wireguard` required) |
| API, UI, scenarios end-to-end | 1 |
| Installation, preflight, interface ownership, network managers | 2 |
| Boot, recovery, safe mode, upgrades | 2 |
| Coexistence with Docker on the gateway host | 2 |
| Performance, timing precision on target hardware | 3 |
| Real device firmware, WiFi | 3 |

### The development container

Development and levels 0–1 run in a Docker container. Verified in this environment:

| Requirement | Why |
|---|---|
| `--privileged` | With only `NET_ADMIN`, `NET_RAW` and `SYS_ADMIN`, the testbed runs, but sysctls in the test namespaces cannot be set (`/proc/sys` is read-only). Forwarding then only worked because new namespaces inherit the host's IPv4 settings — on a host with `ip_forward=0` the tests would fail. |
| Host kernel with the required modules | The container uses the host kernel and cannot bring its own modules. The list is the one of the product preflight (§3.4); a test preflight checks it, and if modules are missing, the affected tests run in level 1b. |
| `/dev/kvm` passed through (optional) | Level 1b and level 2 inside the container. Without KVM, QEMU falls back to software emulation: functional tests still work, but timing measurements do not (baseline 2.4 ms instead of 0.3 ms, `nft` commands 0.5 s instead of 6 ms). |
| Unique namespace prefix per test run | Parallel runs with the same names destroy each other's topology. |
| Test tools in the image | iproute2, nftables, conntrack-tools, tcpdump, tshark, iperf3, dnsutils, busybox (`udhcpc`), ethtool, socat, Kea, Go toolchain, Node.js (frontend build and Playwright), Python 3, mitmproxy (sidecar tests). |

### Reference development setup

Ubuntu Server 24.04 in a VirtualBox VM, development inside the privileged devcontainer on that VM (VS Code Remote-SSH + Dev Containers). The VM provides the exact target kernel, so level 1 runs completely.

| Setting | Why |
|---|---|
| 4+ vCPUs, 8+ GB RAM, 40+ GB disk | testbed, Kea, mitmproxy, Go and Node toolchains and captures in parallel |
| NIC 1: NAT (or bridged) | internet access and SSH from the host |
| Optional NIC 2: bridged to a dedicated (USB) Ethernet adapter, promiscuous mode "Allow All" | real test devices (e.g. an ESP32) behind the gateway; "Allow All" is needed as soon as the guest bridges this NIC or uses macvlan |
| Nested VT-x/AMD-V enabled (`VBoxManage modifyvm <vm> --nested-hw-virt on`) | `/dev/kvm` in the guest for levels 1b and 2 |
| Snapshot after base setup | quick reset when an experiment breaks the VM's own networking |

If Hyper-V or Windows virtualization-based security is active on the Windows host (often the case with WSL2 or Memory Integrity), VirtualBox runs on the Hyper-V backend: noticeably slower, and nested virtualization is not available, so the guest has no `/dev/kvm`. Level 1 is unaffected; levels 1b and 2 then fall back to software emulation (functional only) or move to CI.

Further notes:

- **Unprivileged container:** if the development container cannot run privileged, level 1 runs as level 1b *inside* the container. QEMU is an ordinary process and needs no extra capabilities; inside the VM the tests are root with their own kernel. Verified with Docker's default capabilities only (no `NET_ADMIN`, no `SYS_ADMIN`): the guest kernel loaded netem, created namespaces and veth pairs, set sysctls, loaded nftables rules, and a 50 ms netem delay was measurable. The image then needs QEMU, virtme-ng, `busybox-static` and a distribution kernel with modules. Without `/dev/kvm` the VM is emulated in software, which is slow and noisy (50 ms delay measured as 51–115 ms): functional tests only, no measurement tests. Passing `--device /dev/kvm` (not the same as privileged) makes it fast.
- Docker's `FORWARD DROP` policy on the host does not affect the testbed: the test namespaces are separate network namespaces with their own rules.
- **Docker Desktop (macOS/Windows)** runs containers in a Linux VM whose kernel decides which modules exist. The test preflight shows whether level 1 is complete there; level 2 needs a Linux host with KVM.
- **What the container cannot cover:** everything that needs a whole machine (installation, boot, network managers — level 2) and real hardware (level 3).

---

# 5. Milestones

**Rules for every milestone**

- It delivers a capability that can be demonstrated on its own.
- It has automated tests at the levels named under *Tests*.
- After it, the main branch is releasable (everything before still works).
- Dependencies are listed explicitly; nothing depends on a later milestone.

## Phase 0 — Technical Spikes

Short, throwaway experiments in the testbed. Each answers a specific question with pass/fail. The results are written down as short architecture decision records.

**Status:** executed; results and decisions in [`docs/spikes/REPORT.md`](spikes/REPORT.md), already incorporated into this plan. S11–S14 were added after the plan review of 2026-09-29. Criteria that could not be checked in the sandbox are listed under *Open* and closed by the milestone named there.

| Spike | Question | Result | Open → closed by |
|---|---|---|---|
| S1 Testbed | client/gateway/server namespaces in CI and on target machines | ✅ | on Raspberry Pi → H1 |
| S2 Fault topology | tc topology for many independent per-direction faults; rate limiting; in-place changes; IFB for gateway-terminated traffic | ✅ with corrections (sticky attributes, jitter reorders) | — |
| S3 Connection behavior | effect of drop/reject/reset and conntrack deletion on existing TCP connections | ✅ behavior matrix | — |
| S4 TLS | transparent redirect, SNI, broken certificates, mitmproxy transparent mode, key log | ✅ with correction (certificate checks need a trusted test CA) | TLS 1.2 vs 1.3, connection reuse, control from the core → M21, M22 |
| S5 DNS proxy | per-client faults, hostname sets, throughput | ✅ with gaps (TCP, batching) | ≥ 1000 queries/s on Pi → H1 |
| S6 DHCP | Kea vs. dnsmasq for runtime test actions | ✅ Kea | Kea on Debian 12/13 → M6a |
| S7 Deployment | Docker coexistence, gateway in a container | ✅ | netplan/NetworkManager coexistence → M28 |
| S8 Hardware | cost of classification and updates; throughput | ◐ x86 only | Raspberry Pi, 500 HTB classes → H1 |
| S9 Capture | exact per-selector capture despite NAT | ✅ AF_PACKET + NFLOG | — |
| S10 Classification | mark layout, per-packet vs. per-connection classification | ✅ per packet | — |
| S11 Direction | direction bit with two test networks; device+destination lookup; re-apply that keeps dynamic sets and counters | ✅ (without the direction bit: 43 ms instead of 120 ms) | — |
| S12 Attachment | probe on bridge vs. macvlan; tc on the bridge; policy routing vs. management default route | ✅ bridge; macvlan probe cannot reach the gateway | — |
| S13 PMTUD | path-MTU faults with ICMP and as black hole | ✅ (side effect on shared NAT address) | — |
| S14 Local replies | download faults for connections that end on the gateway | ✅ with output hook | — |
| S15 WireGuard & routing | (1) inner faults: classification of client/remote-network traffic and netem on a WireGuard interface's egress; (2) tunnel faults: marking WireGuard's own encrypted UDP in the output hook per peer endpoint, netem on the uplink; (3) BIRD (BGP and OSPF) over a WireGuard link between two sites, exporting only into the Chaos Gateway table, import filter rejects default and management prefixes; (4) re-convergence time when a tunnel fault blacks out the link; (5) hub interface with client networks via `AllowedIPs` | planned | before M33/M37 |

## Milestone overview and MVP

Sizes: **S** ≈ up to 1 week, **M** ≈ 1–2 weeks, **L** ≈ 2–4 weeks for one experienced developer.

| Phase | Milestones | Result |
|---|---|---|
| 1 Foundation | M1 (M), M2 (M), M3 (M), M4 (L), M5 (L), M5b (M), M6a (M), M6b (M) | API-configurable routed gateway with DHCP, DNS, discovery |
| 2 Faults | M7 (M), M8a (M), M8b (M), M9 (M), M10 (M), M11 (S), H1 (S) | faults, rules, profiles via API, validated on target hardware |
| 3 Web UI | M12 (L), M13 (L), M14 (M) | interactive use |
| 4 Automation | M15 (L), M16 (M), M17 (M), M18 (S), M19 (M) | scenarios, checks, capture, CLI |
| 5 Application layer | M20 (M), M21 (M), M22 (L), M23 (S) | DNS, TLS, DHCP test actions |
| 6 Diagnostics | M24 (M), M25 (S), M26 (S) | diagnostics, probes, metrics |
| 7 Production | M27 (M), M28 (M), M29 (M), M30 (S) | recovery, packages, hardening, release |
| 8 Sites | M33 (L), M37 (L) | WireGuard networks, clients, site links, static and dynamic routing |

**MVP (automation first):** Phases 1 and 2 plus M15, M16 and M18 — faults, rules and profiles, scenarios with checks and JUnit reports, driven by the CLI and the API. This already serves the original use case (automated IoT tests in CI). The web UI (Phase 3) follows; V1 is defined at the end of this section.

## Phase 1 — Foundation: Routed Gateway

**M1 — Repository, CI and testbed library** (M)
- Scope: Go module and Vue app skeleton, `api/openapi.yaml` with code generation (oapi-codegen, Orval), Makefile, golangci-lint, `go test`, Vitest, Playwright skeleton, CI with unprivileged and privileged jobs, `internal/testbed` from spike S1 (with two test networks and a bridge-based attachment as default topology), the privileged test container image with all test tools, and the shared kernel-module preflight that sends tests to level 1b when modules are missing (§4.5).
- Tests: CI runs a testbed test (client pings server through a plain forwarding namespace).
- Depends on: S1.

**M2 — Domain model and persistence** (M)
- Scope: schemas for uplink, network, device (incl. `trusts_test_ca`), group, rule, fault, profile, scenario (JSON Schema, §2.10), overlay kinds with owner, TTL and lease (§2.1.1); validation incl. jitter ≤ delay; precedence resolution (§2.4); atomic file persistence; revisions with diff; schema version.
- Tests: unit tests for validation; golden tests for every precedence row; revision round-trip; corrupted-file handling; the scenario example of §2.10 validates.
- Depends on: M1.

**M3 — Executor and state reader** (M)
- Scope: privileged executor with typed operations, argument-array command invocation, namespace targeting, parsers for `ip -j`, `nft -j`, `tc -j`; Unix-socket protocol with version handshake and `SO_PEERCRED` check; scope validation (nftables only `inet chaosgw`, routing only own tables and rules, tc only assigned interfaces); systemd hardening profile.
- Tests: unit tests for command building and parsing (recorded outputs); integration test reads interfaces/routes of the gateway namespace; operations outside the allowed scope are rejected; fuzz tests on the operation decoder (5 min per CI run, longer nightly); version mismatch is refused.
- Depends on: M1.

**M4 — Compiler v1, preview, safe apply: routed gateway** (L)
- Scope: uplink (static IPv4), test networks as bridges with physical ports, policy routing table and rules (§2.2), forwarding, masquerade, access matrix default, IPv6 blocked and RA acceptance off; nftables layout that keeps dynamic sets and counters (§3.2); generation id and verify (§2.14); target state, diff, preview, apply, rollback on failure; commit-confirm and anti-lockout for the management network; interface assignment (which port is uplink, test network, management).
- Tests: golden tests; integration — client reaches server through the gateway; a management default route in the main table does not attract test traffic; preview matches applied state; verify detects a manipulated element; an injected executor failure leaves the previous state active; an unconfirmed lockout-relevant change rolls back after 60 s.
- Depends on: M2, M3.

**M5 — REST API v1** (L)
- Scope: API conventions of §2.15 (problem+json, UUID + name, pagination, ETag/If-Match, idempotency keys, SSE with ids and replay); candidate-revision model with preview, apply, confirm and three-way diff (§2.1.1); sessions with CSRF, hashed API tokens with scopes, first-start setup token (§2.16); audit log.
- Tests: API contract tests against the spec; generated clients compile; E2E through the testbed (configure via API → traffic flows); conflicting candidate is rejected and the diff endpoint returns the other change; SSE reconnect with `Last-Event-ID` gets the missed events.
- Depends on: M4.

**M5b — Appliance VM harness (test level 2)** (M)
- Scope: build or download distribution images; boot a gateway VM with three virtio NICs (uplink, test network, management) connected via tap and bridges to client and server namespaces; run a command set inside; collect logs. Runs nightly on a KVM runner.
- Tests: a smoke test boots Ubuntu 24.04 and Debian 12, installs the current build (tarball until M28 provides the package) and passes traffic from a client namespace through the VM.
- Depends on: M4.

**M6a — DHCP and device discovery** (M)
- Scope: Kea with pools and reservations; lease events via Kea's `run_script` hook; device discovery from leases, neighbor table and conntrack; identity updates in the classification maps (§2.3); manual device merge; devices API.
- Tests: client namespace gets a lease; device appears with MAC/IP; reservation honored; discovered vs. configured devices; after a forced address change the device's entry in the maps follows within 1 s; Kea package works on Debian 12/13.
- Depends on: M5.

**M6b — DNS proxy and uplink DHCP** (M)
- Scope: DNS proxy on each test network's gateway address, UDP and TCP, forwarding, caching, query log, AAAA removal; coexistence with systemd-resolved; uplink DHCP client in the executor; uplink-changed event and recompile.
- Tests: clients resolve names over UDP and TCP; the proxy does not bind 127.0.0.53 and resolved keeps working; changing the uplink address keeps NAT working and emits the event.
- Depends on: M5.

*After Phase 1: a working, API-configurable test gateway without faults.*

## Phase 2 — Faults (core value)

**M7 — Classification layer** (M)
- Scope: the lookup chain of §3.3 (device + destination + port … global) on prerouting **and output**, direction bit, per-rule and per-fault named counters, flower filters for IFB generated from the same policy.
- Tests: with a test tc class per (id, direction), per-class counters increase only for matching traffic, in both directions, behind NAT, across two test networks and for a connection redirected to a local service; a map change moves an established connection to its new class (observed via the class counters); counters survive 20 re-applies.
- Depends on: M6a, M6b, S2, S10, S11, S14.

**M8a — Overlays** (M)
- Scope: overlay store with all kinds of §2.1.1, owner, TTL, lease and renew; `POST /api/v1/reset` (own vs. all); precedence into effective fault configurations; compiler output for tc (per id and direction, complete parameter sets, computed queue limits).
- Tests: golden tests for precedence and tc output; TTL and lease expiry remove overlays and emit events; `reset` only touches the caller's overlays; a restart drops overlays and aborts runs (§2.1.1).
- Depends on: M7.

**M8b — Fault engine: latency, jitter, loss** (M)
- Scope: apply the tc tree per interface with in-place changes; overlay writes return after verify.
- Tests: measurement tests (§4.3) for device, group and network scope and for traffic between two test networks; isolation test; updating one fault does not disturb others.
- Depends on: M8a.

**M9 — Access rules** (M)
- Scope: ordered allow/drop/reject/TCP reset rules in configuration and as overlays; "also cut existing connections"; precedence rules vs. faults; hit counters; preview of the effective result.
- Tests: behavior matrix from S3 as automated tests; rule order; overlay rules before configuration rules; anti-lockout rule cannot be overridden.
- Depends on: M8a, S3.

**M10 — Extended faults** (M)
- Scope: rate and queue limit per device (D18), reorder, duplicate, corrupt, burst loss (Gilbert-Elliott), blackout (netem loss 100 %), flapping, MTU/PMTUD with the three modes of §2.5.
- Tests: one measurement test per fault type on the kernels of the distribution matrix; flapping timing within tolerance; PMTUD: a 300 KB TCP transfer completes with ICMP mode and stalls in black-hole mode, the control device is unaffected (as in S13); MSS clamp limits segment size of the selected device only; per-device rate (D18): a 2 Mbit/s fault on a network gives two devices transferring at the same time 2 Mbit/s each (±10 %); exceeding the class limit returns `capacity_exceeded` in preview.
- Depends on: M8b, M9.

**M11 — Profiles** (S)
- Scope: built-in (except DNS/TLS profiles, which arrive with M20/M21) and custom profiles, activation on scopes via overlays, precedence with individual faults.
- Tests: activating/switching profiles yields the configured parameters (compiler) and measured values (integration); a device fault overrides a network profile; a fault on the same scope replaces the profile as a whole.
- Depends on: M10.

**H1 — Hardware validation** (S)
- Scope: run the measurement suite and the S8 performance scripts on a Raspberry Pi 4/5 and an x86 mini PC with real NICs: throughput with 50 and 250 faults (500 HTB classes), DNS proxy queries per second, fault accuracy, scenario step timing, testbed on the Pi.
- Tests: results recorded; §3.10 targets and decision D9 confirmed or adjusted.
- Depends on: M11.

*After Phase 2: the core product via API — faults, rules, profiles.*

## Phase 3 — Web UI

**M12 — UI shell, overview, devices** (L)
- Scope: visual system and shared components (§2.17), login and first-start setup wizard (interface assignment, admin password), layout, overview, device list and device detail with the data available after Phase 2 (identity, lease, active faults, counters); live updates via SSE; empty, offline and safe-mode states. Panels whose backend arrives later (captures, diagnostics, DHCP actions, TLS tests) appear with those milestones.
- Tests: Playwright against a mocked API for every state (empty, many devices, offline, safe mode); an E2E flow in the testbed: first start with setup token → assign interfaces → see a discovered device.
- Depends on: M11.

**M13 — UI for faults, rules and profiles** (L)
- Scope: add/edit fault dialog with preview, rules & faults list with counters, rule editor (IF/THEN), unapplied-changes bar, preview-and-apply drawer, concurrent-change dialog (using M5's three-way diff), profile cards, TTL display, `</> API` panel.
- Tests: Playwright — create a fault in the UI, then verify the measured effect in the testbed; validation errors are shown; a conflicting change made through the API triggers the conflict dialog and can be rebased.
- Depends on: M12.

**M14 — Networks view and technical view** (M)
- Scope: network cards and detail, DHCP pool/leases, access matrix editing with commit-confirm, compiled-state view (nftables, tc, routes).
- Tests: Playwright; commit-confirm — an unconfirmed change is rolled back after the timeout and the UI shows it.
- Depends on: M13.

*After Phase 3: usable interactively. First release candidate for internal use.*

## Phase 4 — Automation

**M15 — Scenario engine and runs** (L)
- Scope: scenarios as defined in §2.10, scheduler on the monotonic clock, step types profile, fault, rule, wait, restore (further step types arrive with M17, M20–M23); runs with lifecycle, owner, one run per target, queue, explicit abort, optional lease; JSON/JUnit report.
- Tests: step timing within ±100 ms (native or KVM runner only); abort removes the run's overlays; a disconnecting client does not stop a run, an expired lease does; a second run on the same target waits in `queued`; the report contains all steps.
- Depends on: M11.

**M16 — Checks** (M)
- Scope: observation-based checks with windows relative to named steps (connection established within t, no connection accepted, traffic to destination seen/not seen, DNS query seen).
- Tests: checks pass and fail correctly with scripted client behavior in the testbed.
- Depends on: M15.

**M17 — Capture** (M)
- Scope: capture by network, device, selector or rule (AF_PACKET / NFLOG per S9); ring buffer, quotas, retention, disk-low behavior; download and live stream; capture step type; attachment to runs.
- Tests: the capture contains exactly the selected traffic; quota enforcement; a run with capture attaches it to the report; low disk stops captures but not the run.
- Depends on: M7, M15, S9.

**M18 — CLI and clients** (S)
- Scope: `chaosctl` in Go (apply profile, set fault with TTL, run scenario, wait for result, fetch report); generated TypeScript and Python clients; examples for pytest and Jest.
- Tests: CLI tests against the testbed; the example test suites run in CI.
- Depends on: M15.

**M19 — Scenario UI** (M)
- Scope: scenario list, timeline editor, YAML view, run view with live progress, results and artifacts.
- Tests: Playwright — build and run a scenario, then check result and artifact display.
- Depends on: M13, M16, M17.

*After Phase 4: V1 feature complete for L3/L4 testing and automation.*

## Phase 5 — Application Layer

**M20 — DNS faults and hostname selectors** (M)
- Scope: NXDOMAIN, SERVFAIL, timeout, delay, wrong answer, truncation (with TCP fallback), short TTL, per device/group/pattern; DNS-derived address sets with the lifetime rule of §2.6; redirect of hardcoded DNS; DoT blocking; hostname selectors for rules and faults; "DNS broken" profile; DNS scenario step type.
- Tests: `dig` from clients shows each fault; a hostname-selector fault affects only traffic to the resolved IPs; a long-lived connection keeps its hostname fault past a 1 s TTL; the set survives 10 overlay changes; hardcoded DNS is redirected.
- Depends on: M8b, M9, M15, S5.

**M21 — TLS responder: certificate cases** (M)
- Scope: TLS responder in the core, transparent redirect of selected traffic, the TLS cases of §2.8 as confirmed by S4, no-SNI fallback, expected results from `trusts_test_ca`, events per handshake, check type "TLS rejected/accepted", "TLS broken" profile, TLS scenario step type.
- Tests: `openssl s_client`/`curl` with TLS 1.2 and 1.3 — untrusted/expired/wrong-host/self-signed are rejected by a correct client; a deliberately insecure client is flagged by the check; a client trusting the test CA passes the "untrusted CA" case; download latency applies to the responder's replies (output hook).
- Depends on: M9, M16, S4, S14.

**M22 — TLS interception and HTTP faults** (L)
- Scope: mitmproxy sidecar management and control from the core, test CA management (generate, download for dev firmware), HTTP(S)/WebSocket inspection, URL blocking, error codes, delay/throttle, modification, connection abort, key log for captures; block-UDP-443 option; interception scenario step type.
- Tests: a client trusting the CA sees the modified responses; timing of delayed responses; QUIC fallback; connection reuse across requests.
- Depends on: M21.

**M23 — DHCP test actions** (S)
- Scope: short leases, lease deletion, forced new IP via reservation change (NAK), option changes, silence; DHCP scenario step type.
- Tests: `udhcpc` in the testbed observes each behavior; a device fault stays attached after the forced new IP.
- Depends on: M6a, M15.

## Phase 6 — Diagnostics and Observability

**M24 — Diagnostics** (M)
- Scope: ping, TCP/UDP check, DNS, HTTP(S), TLS details, traceroute, path MTU, iperf3; structured results; API and UI panel.
- Tests: each diagnostic against known testbed targets, including failure cases.
- Depends on: M6b, M12.

**M25 — Probes and calibration** (S)
- Scope: virtual clients as bridge ports of test networks (S12); DHCP address; targeting by rules/faults; self-test "measure this profile".
- Tests: a probe reaches the gateway address, devices and the server; its measurement matches the configured profile within the §4.3 tolerances.
- Depends on: M8b, M24.

**M26 — Metrics and flow view** (S)
- Scope: Prometheus metrics endpoint (counters from M7–M9, interface and queue statistics, system health); flow list and flow view in the UI.
- Tests: metric values against known traffic; Playwright for the flow view.
- Depends on: M9, M13.

## Phase 7 — Production Readiness

**M27 — Recovery and drift** (M)
- Scope: last-known-good at boot, safe mode, recompile after interrupted apply, drift detection and reconcile for owned objects (using the generation id and verify of §2.14).
- Tests: external deletion of a rule/qdisc is detected and reconciled; a broken revision at boot leads to last-known-good (level 2); killing the executor mid-apply is recovered.
- Depends on: M5b, M8b.

**M28 — Packaging and installation** (M)
- Scope: .deb for amd64/arm64, systemd units with hardening, preflight check, network-manager coexistence, setup token at installation, deferred restart during runs, refusal of newer schemas, uninstall restoring interfaces.
- Tests: install and smoke tests in the level-2 harness (Ubuntu 24.04 and 26.04, Debian 12/13, two- and three-port topologies) and on a Raspberry Pi; netplan and NetworkManager leave assigned interfaces alone.
- Depends on: M5b, M14, M27, S7.

**M29 — Security hardening** (M)
- Scope: management-plane binding after setup, HTTPS, token scopes, secret storage, secret-free exports, rate limiting on login, external review of the executor interface.
- Tests: test networks cannot reach the UI/API; tokens with insufficient scope are rejected; exports contain no secrets; long fuzz runs on executor operations nightly.
- Depends on: M5, M28.

**M30 — Documentation and V1 release** (S)
- Scope: user guide, API guide with examples, scenario cookbook, troubleshooting (preflight messages).
- Tests: documentation examples run in CI.
- Depends on: all milestones included in V1.

## Phase 8 — Sites: WireGuard and Routing

**M33 — WireGuard networks and clients** (L)
- Scope: hub and link networks (§2.2.1); clients with client networks and reachable-network selection; key generation, optional preshared keys, "export once"; export as `.conf`, QR (UI, PNG/SVG), zip, `chaosctl wg export`; client status (handshake, endpoint, bytes) and events; clients and remote networks as devices/networks in rules, faults, access matrix and scenarios; inner faults on WireGuard traffic; tunnel faults on a peer's encrypted UDP; peer disable and key mismatch as scenario actions; role *management* for admin remote access. UI: WireGuard section in Networks, client list with QR dialog.
- Tests (level 1): gateway plus two remote namespaces as clients, one with a client network behind it; a device in a local test network reaches a machine in the client network without NAT, and vice versa per access matrix; the exported `.conf` brings up a working tunnel in a fresh namespace, and the decoded QR equals the file; a device fault on a client network is measurable; a tunnel fault (latency, blackout) affects everything inside the tunnel and nothing else; a *test* role client cannot reach the UI, a *management* role client can; private keys are absent from configuration exports.
- Depends on: M4, M7, M9, S15; UI parts on M13.

**M37 — Routing: static and dynamic (BIRD)** (L)
- Scope: static routes per network, client and link (§2.2.2); BIRD 2 instance managed by Chaos Gateway: BGP, OSPFv2, Babel, static; announced prefixes from the model; import filters (prefix lists, no default unless allowed, protected prefixes, max prefixes); `bird -p` validation and `birdc configure` apply as part of the revision; custom snippets; external mode for another daemon's kernel table; neighbor and route status, events; remote-side BIRD snippet in link exports. UI: routing view with sessions and prefixes.
- Tests (level 1): three sites (gateway plus two remote namespaces with BIRD) over WireGuard links; routes are learned and withdrawn; learned routes appear only in Chaos Gateway's tables, never in main; a neighbor announcing a default route or the management prefix is filtered; max-prefix triggers; re-convergence after a 20 s tunnel blackout is measured and reported; an invalid custom snippet is rejected in preview with BIRD's error message.
- Depends on: M33.

## After V1

| Milestone | Content |
|---|---|
| M31 VLANs | multiple networks on a trunk (VLAN ports on the network bridges), access matrix between them |
| M32 IPv6 | RA/SLAAC, DHCPv6, IPv6 rules and faults (lifts the V1 IPv6 block) |
| M34 NTP time faults | see §8 |
| M35 Port forwarding | DNAT for inbound test traffic; matching a device as responder |
| M36 Further proposals | features from §8 by priority |

## V1 Scope Summary

V1 = Phases 1–4 (incl. M5b and H1), plus M20, M21, M24, M25, M27, M28, M29, M30.

M22 (interception), M23 (DHCP actions) and M26 (metrics and flow view) are optional for V1.

---

# 6. Known Limitations and Technical Risks

| # | Topic | Consequence | Handling |
|---|---|---|---|
| 1 | tc acts on egress only | upload and download faults must be applied on different interfaces; traffic terminating at the gateway has no egress in upload direction | classification by marks (§3.3); faults for DNS/TLS inside the services; IFB where needed (spike S2) |
| 2 | Uplink egress is after NAT | device IP is invisible there | classification per packet by the conntrack original tuple (`ct original ip saddr`), which is unchanged by NAT (S10, S11) |
| 3 | netem loss on locally generated traffic may be reported to the local TCP stack | loss on gateway-originated traffic (e.g. proxy → server) could be unrealistic | not observed on Ubuntu 6.8 with netem under HTB (S2); kept in the test matrix |
| 4 | netem jitter reorders packets by default | 71 % of packets reordered at 2 ms spacing (S2) | default documented; "keep order" option via netem rate, which shifts the delay distribution |
| 5 | Fault composition | stacking several impairments on one packet is technically possible but hard to predict | product rule: one effective fault configuration per direction (§2.4) |
| 6 | Sticky netem attributes | `tc qdisc change` keeps attributes that are not given, e.g. a rate limit (S2) | compiler always emits complete parameter sets; same-kind change/replace keeps the queue (S2) |
| 7 | Rollback across nftables, tc, routes and services is not transactional | partial states are possible for milliseconds | nftables atomic per table; ordered apply; recompile from the committed revision on failure |
| 8 | Hostnames cannot be matched in the kernel | hostname rules depend on DNS observation; on shared IPs (CDNs) other services are affected too | best-effort DNS-derived address sets, labeled as such; not covered: DoH, hardcoded IPs |
| 9 | TLS decryption requires device trust | production firmware cannot be inspected | certificate test cases work without trust; interception for dev firmware |
| 10 | QUIC/HTTP-3 | not intercepted | block UDP 443 option |
| 11 | DHCP cannot force an immediate renew | renew tests depend on client behavior | short leases, link interruption, documentation |
| 12 | MAC matching needs L2 adjacency | devices behind another router can only be targeted by IP | documented; IP-based device definition |
| 13 | MAC randomization, IPv6 privacy addresses | device identity changes | manual device merge; IPv4-only test networks in V1 |
| 14 | IPv6 bypass | faults for IPv4 only would be bypassed | IPv6 forwarding blocked until M32 |
| 15 | Diagnostics from gateway addresses bypass device faults | misleading results | probes (§2.12) |
| 16 | Docker on the same host | FORWARD policy DROP, read-only sysctls, iptables interference | native package recommended; preflight detects conflicts |
| 17 | Kernel module availability | netem missing on minimal kernels | preflight check with instructions; on Ubuntu generic all modules are in `linux-modules` |
| 18 | Timing precision on loaded small hardware | scenario tolerance may be exceeded | measurement tests on target hardware (S8); tolerance documented per platform |
| 19 | mitmproxy throughput and resource use on small hardware | TLS proxy limits throughput | only selected traffic is proxied; targets in §3.10 |
| 20 | Management lockout through rules | admin loses access | anti-lockout rule for the control plane only, commit-confirm, management interface outside Chaos Gateway control |
| 21 | Per-packet classification cost | many selectors evaluated per packet could cost throughput on small hardware | nftables maps/sets; measured in S10/S8; fallback: conntrack-mark caching plus explicit re-classification when faults change |
| 22 | Kernel differences in netem | behavior and allowed combinations of netem (e.g. duplication in nested trees) differ between kernel versions | test matrix over the supported distributions (§4.4) |
| 23 | PMTU faults leak through the shared NAT address | with ICMP mode the server caches the reduced path MTU for the gateway's uplink address, so other devices talking to the same server are affected for up to 10 min (S13) | UI warning; MSS clamp mode as isolated alternative for TCP; documented cache flush on test servers |
| 24 | Hostname-derived sets age | addresses learned from DNS expire with the DNS TTL; a device that caches longer than the TTL or uses a hardcoded IP escapes the rule | set element timeout = max(TTL, configurable minimum); "unmatched hostname" events; documented as best effort |
| 25 | Distribution matrix grows | each supported release adds kernel and netem differences and a nightly job | support only what is in the level-2 matrix (Ubuntu 24.04/26.04, Debian 12/13); new releases are added when the matrix passes |
| 26 | Traffic generated on the gateway | replies of the DNS proxy, TLS responder and mitmproxy never pass prerouting | the classification chain is attached to the output hook too (S14); without it, download faults were missing |
| 27 | WireGuard cryptokey routing vs. dynamic routes | on a shared hub interface every prefix is bound to one peer; learned routes cannot be expressed there | dynamic routing only on point-to-point links (§2.2.1); hub clients use declared client networks |
| 28 | Route injection by remote sites | a remote BIRD could announce the default route or management prefixes and divert traffic | learned routes only in Chaos Gateway's tables; import filters, protected prefixes, max prefixes (§2.2.2) |
| 29 | Tunnel MTU | WireGuard overhead (60–80 bytes) plus PMTU faults can black-hole traffic inside tunnels | MTU 1420 default, MSS clamp on WireGuard interfaces, PMTU tests through tunnels in M33 |
| 30 | Per-device queues multiply classes | a rate-limited profile on a network of N devices creates N classes per direction (D18); many devices on small hardware | id space and class count limits enforced by the compiler (`capacity_exceeded`); measured in H1 |

---

# 7. Decisions

## 7.1 Decided

| # | Decision | Result | Basis |
|---|---|---|---|
| D4 | DHCP server | **Kea** — short leases, lease API, `run_script` hook; dnsmasq's minimum lease is 120 s | S6 |
| D5 | TLS components | certificate and handshake cases in a TLS responder in the core; interception via a mitmproxy sidecar; the "no Python" rule applies to the core only. Confirmed with a Node prototype in S4; implemented in Go per D15 | S4 |
| D7 | IP versions in V1 | IPv4-only test networks (§2.2); IPv6 blocked on test networks until dual-stack in M32 | review |
| D15 | Implementation stack | **Go backend** (single binary with embedded UI, low memory on Raspberry Pi, mature netlink/nftables/DNS libraries, experience from sessile) **+ Vue 3 frontend**; shared types come from the OpenAPI spec instead of shared code (§3.7) | maintainer |
| D16 | Test-network attachment | every test network is a gateway-owned bridge; physical port and probes are bridge ports (§2.2). Not to be confused with D8: the gateway still routes, the bridge only joins ports of one network | S12 |
| D17 | Classification | per packet via the conntrack original tuple, mark with 12-bit fault id and direction bit, identical tc mapping on every interface, output hook for local replies (§3.3) | S10, S11, S14 |
| D18 | Rate and queue limits on group/network/global scope | **per device**: every device matched by a rate-limited fault or profile gets its own queue with the full rate; the UI shows how many queues a scope creates (§2.4) | maintainer |
| D19 | WireGuard | hub networks (clients with client networks) and point-to-point links; keys generated on the gateway by default; export as `.conf` and QR (§2.2.1) | maintainer |
| D20 | Routing daemon | **BIRD 2** in its own managed instance (BGP, OSPFv2, Babel, static); adapter interface for FRR later; external mode for other daemons (§2.2.2) | maintainer |

## 7.2 Open

Each open decision has a recommendation; confirming it is enough to proceed.

| # | Decision | Options | Recommendation |
|---|---|---|---|
| D1 | Supported releases and minimum kernel | Ubuntu 22.04/24.04/26.04; Debian 12/13 | Ubuntu 24.04 LTS and newer (26.04 in the matrix), Debian 12 and newer, kernel ≥ 6.1 |
| D2 | Deployment | native package, container, appliance image | native .deb primary; container for development/demo; appliance image later |
| D3 | Host ownership | own everything; own assigned interfaces only | own assigned interfaces; management interface stays with the OS |
| D6 | Uplink types in V1 | static, DHCP, PPPoE | static and DHCP |
| D8 | L2 transparent mode (gateway as a bridge **between** device and upstream router, not routing) | V1, later, never | later. Only needed when the gateway cannot be the device's default router. Unrelated to the bridge that joins the ports of one test network (D16) |
| D9 | Hardware targets | as §3.10 | confirm after H1 (S8 covered x86 only) |
| D10 | Users | single admin + tokens, multi-user with roles | single admin + scoped tokens in V1 |
| D11 | Existing connections default for access rules | affect new only, cut existing | affect new only; cutting existing is an explicit option |
| D12 | Rule and fault precedence | as §2.4 | confirm §2.4 |
| D13 | License | open source (which license), closed | decide before first public release |
| D21 | Placement of Phase 8 (WireGuard, routing) | V1, V1.1 | M33 in V1 (connects the existing test machines and networks); M37 in V1 if the lab depends on dynamic routing from the start, otherwise V1.1 |
| D14 | Interface naming | Linux names, logical names | logical names (UPLINK, IOT, MGMT) in the UI; Linux names in technical views |

---

# 8. Proposed Additional Features

| Feature | Benefit for IoT testing | Effort | Suggested placement |
|---|---|---|---|
| **Overlays with TTL and reset** | a crashed test job cannot leave the network broken | small | V1 (included, M8a) |
| **Probes and calibration** | trust in the fault values; testing without hardware | medium | V1 (included, M25) |
| **Scenario checks and JUnit reports** | turns the gateway into a test tool, not just a fault tool | medium | V1 (included, M16) |
| **CLI and client library** | integration into existing test suites | small | V1 (included, M18) |
| **Device groups** | apply faults to a whole fleet at once | small | V1 (included, M2) |
| **NTP server with time offset** | tests certificate validity handling, token expiry, time-based logic — a frequent IoT failure | small | M34 |
| **Local mock services** | redirect a broker/API hostname to a local MQTT broker or HTTP mock (e.g. with a failing backend) via the DNS proxy | medium | after V1 |
| **Captive portal simulation** | tests onboarding and connectivity checks of devices | small | after V1 |
| **Protocol-aware MQTT faults** | drop specific topics, delay PUBACK, disconnect on subscribe | medium | after V1 |
| **WiFi test access point** (hostapd) | the majority of IoT devices use WiFi: AP disappearing, deauthentication, reconnect storms, channel/password changes | large | after V1, high value |
| **Physical link control** | real link loss via managed switch/PoE control (SNMP) or directly attached ports | medium | after V1 |
| **IPv6-only network with NAT64/DNS64** | tests devices in IPv6-only environments | medium | after M32 |
| **Upstream latency map** | different faults per destination region/service | small | after V1 |
| **Traffic baseline and anomaly report** | "what does this device normally talk to?"; flags unexpected destinations after firmware updates | medium | after V1 |
| **Bandwidth history per device** | long-term observation of device behavior | small | after V1 |
| **MQTT control interface** | control from environments that already use MQTT | small | on demand |
| **L2 bridge mode** | insert into existing networks without re-addressing devices | large | on demand |
| **Multi-WAN / failover simulation** | tests device behavior on uplink switch (IP change of NAT address) | large | on demand |
