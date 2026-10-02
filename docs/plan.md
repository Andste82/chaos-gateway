# Chaos Gateway — Product & Implementation Plan

> **A programmable network test gateway.**

Status: planning complete · October 2026

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

- A general-purpose home or enterprise router or firewall for production traffic. Routing, WireGuard and BIRD exist to connect test machines, test networks and test sites (§2.2.1, §2.2.2), not to run a site's internet access.
- A general VPN service for end users (road warriors, privacy VPN). WireGuard clients are test machines, test networks and administrators.
- Remote control of the connected test machines: Chaos Gateway configures tunnels and routes and exports client configurations, but does not install, start or configure anything on the remote side.
- Impairing traffic that does not cross the gateway, e.g. between two devices on the same test network (§2.2), or inside a remote site's own network.
- Link-layer (WiFi radio) impairment in V1 (see §8).
- IPv6 inside test and WireGuard networks in V1: IPv6 is blocked there until M32 (§2.2, D7).
- Multi-WAN and PPPoE. The routing model must not rule them out later.
- High-throughput WAN emulation beyond the hardware targets in §3.10.
- Decrypting TLS of devices that correctly refuse an untrusted CA. That refusal *is* the test result (see §2.8).

## 1.5 Platform

| | |
|---|---|
| Host operating system | **Ubuntu 24.04 LTS and 26.04 LTS** (D1). Other distributions are not supported in V1 |
| Minimum kernel | 6.8 (Ubuntu 24.04 GA kernel); HWE kernels of both releases are in the test matrix |
| Architectures | **x86-64 and ARM64** (e.g. Ubuntu on a Raspberry Pi 4/5) |
| Deployment | **Docker container** on the host, from one multi-arch image (§3.8, D2) |
| Later | BSD backend, behind the same domain and compiler boundaries |

The domain model is platform-independent; the execution layer is explicitly Linux. There are no portability abstractions beyond that boundary.

---

# 2. Functional Specification

## 2.1 Concepts

| Concept | Meaning |
|---|---|
| **Gateway** | the Chaos Gateway machine itself, with its interfaces and services |
| **Uplink** | the upstream connection. Its address, gateway and DHCP are configured by the operating system (netplan); Chaos Gateway only selects the interface and uses it (D3) |
| **Network** | a logical test network: subnet, DHCP/DNS settings, access to other networks. Its gateway address lives on a Linux bridge owned by Chaos Gateway; **attachments** are the bridge's ports (V1: physical interfaces and probes; later: VLANs, tunnels) |
| **Device** | a device under test with a stable internal identity. It is recognized by one or more identifiers — MAC address, IPv4 address/range, later IPv6 address or WireGuard peer. Configured or discovered |
| **Group** | a named set of devices (e.g. "all sensors") |
| **WireGuard network** | a tunnel network on the gateway: a hub for many clients, or a point-to-point link to another site (§2.2.1) |
| **Peer / client** | a WireGuard endpoint with keys, a tunnel address and optionally **client networks** — subnets behind it (a remote test lab, another test machine's networks) |
| **Remote network** | a subnet reached through a peer or learned by dynamic routing; can be source and target of rules and faults like a local network |
| **Probe** | a virtual test client inside the gateway, attached to a network and treated like a device (see §2.12) |
| **Traffic selector** | a match expression: source, destination, protocol, ports, hostname, direction |
| **Flow** | an observed connection (from conntrack), with counters |
| **Access rule** | selector plus access action (allow/drop/reject/reset) |
| **Fault** | selector plus impairment parameters of one fault family (§2.4); time limits exist only for overlays (TTL) |
| **Profile** | a named, reusable set of fault parameters ("Bad LTE") |
| **Scenario** | a timeline of steps (faults, profiles, rules, actions) |
| **Run** | one execution of a scenario, with its artifacts |
| **Capture** | a packet recording |
| **Revision** | an immutable version of the persistent configuration |
| **Overlay** | a runtime change layered over the configuration — fault, profile activation, rule, DNS fault, TLS case or DHCP action — with an owner and optionally an expiry (§2.1.1) |

### 2.1.1 State model

Two levels of state are central to the design.

**Configuration** (persistent, revisioned): uplink selection, networks (incl. WireGuard networks and clients), routing, devices, groups, access rules, persistent faults (families impairment, MTU and tunnel; DNS faults, TLS cases, DHCP actions and profile activations exist only as overlays), profile definitions and scenario definitions. It changes rarely and deliberately. Profiles and scenarios are part of each revision; YAML files are only an import/export format.

**Overlays** (runtime, never revisioned): what tests switch on and off, often many times per minute.

| Overlay kind | Example |
|---|---|
| fault | ESP32-42 upload +200 ms for 5 min |
| profile activation | "Bad LTE" on network IoT |
| rule | drop TCP 8883 for ESP32-42 (a scenario step) |
| DNS fault | SERVFAIL for broker.example.com |
| TLS case | untrusted certificate on TCP 8883 |
| DHCP action | move reservation, silence DHCP |
| WireGuard action | link to site B down for 20 s, client key mismatch, endpoint blocked |

Every overlay has an **owner** (a user session, an API token or a run), an optional **expiry** (TTL) and optionally a **lease** that its owner must renew. When the expiry or lease runs out, the overlay is removed and an event is emitted — a crashed test job never leaves the network broken.

**Observed state** (neither revisioned nor overlay): what the gateway sees — DHCP leases, neighbor entries, WireGuard handshakes, flows, discovered devices. The addresses in it are a compiler input (§3.2): which IP currently belongs to which device.

**Precedence (D24): overlays always win over configuration.** For every fault family (§2.4), the compiler first resolves the matching overlays by specificity; only if no overlay of that family matches, the matching configuration faults are resolved the same way. A scenario that switches a whole network "offline" therefore takes every device of that network offline, even one with a more specific persistent fault. Among access rules the first match wins, overlay rules before configuration rules.

**Editing the configuration:** a change is submitted as a **candidate revision** (`POST /api/v1/revisions` with the full configuration or a JSON Merge Patch; the revision it is based on goes into `If-Match`). The server validates it, the preview shows the domain and Linux diff, and applying it by id makes it active after verification. If another revision became active in between, the request fails with `409 revision_conflict`; the client reloads and reapplies its change (a three-way merge follows after V1). The UI's "N changes · not applied yet" bar is a client-side draft that becomes a candidate on *Preview*.

**Overlays and concurrency:** overlays are not revisioned and not locked. An overlay is identified by the key *(owner, kind, target, selector)* — for faults the family is part of the selector, so faults of different families never replace each other (the per-kind selector is defined in `api/openapi.yaml`, `OverlayRequest`); writing an overlay with an existing key replaces it and keeps its id (`200` instead of `201`). Overlays reference targets by UUID, so renaming a device does not affect them; discovered devices that are not adopted yet have UUIDs too and can be targeted. `POST /api/v1/reset` removes overlays and stops runs — by default only those of the caller; `?owner=all` requires the full-access scope. Only one run may be active per target (device, group or network); runs on disjoint targets can run in parallel, others wait in `queued`.

**Applying a revision while overlays or runs are active:**
- A revision that deletes an object referenced by an active overlay or by a queued or running run is rejected with `validation_failed`, listing the references. With `?force=true` the orphaned overlays are removed (event `overlay_orphaned`) and the affected runs end as `aborted`.
- An active profile activation follows the new profile definition.
- A run takes a snapshot of its scenario when it starts and records the revision id; editing the scenario does not affect a running run.
- Only one revision can wait for confirmation (commit-confirm) at a time; a second apply gets `409 confirm_pending`. A revision that was never confirmed never becomes "last known good"; a reboot inside the confirmation window boots the previous revision.

**Restart:** whenever the service or the machine restarts, the kernel state is recompiled from the committed revision and the observed state only. Overlays are dropped, active runs end as `aborted`, and an event records both. Tests always restart from a clean baseline. When the service is **stopped**, the executor first removes all overlays (the gateway keeps routing with the configuration only, never with a leftover fault); `chaosgw teardown` removes everything Chaos Gateway created.

**Time:** TTLs, leases and scenario schedules run on the monotonic clock; wall-clock time is used only for display and the audit log. Raspberry-Pi-class machines have no real-time clock, so wall time can jump at the first NTP sync.

## 2.2 Networks and Connectivity

**V1**

- One uplink, configured by the operating system (netplan: static or DHCP). Chaos Gateway selects the interface, reads its address and gateway (the OS default route on that interface, any metric, or an explicitly configured gateway) and follows changes through netlink events. Masquerade adapts to address changes by itself.
- One or more **IPv4-only** test networks. Each network is a Linux bridge owned by Chaos Gateway; its physical interface and its probes are ports of that bridge (spike S12: a probe attached with macvlan to a physical port cannot reach the gateway address; on a bridge it can):
  - Static gateway address on the bridge.
  - Routing to the uplink with masquerade (NAT).
  - Access matrix between networks (e.g. IoT → Internet ✓, IoT → Management ✕), including WireGuard networks and their client networks.
- **Firewall layers** (all in `table inet chaosgw`, compiled from the configuration):
  1. **Gateway protection (input):** from test networks and *test*-role WireGuard networks the gateway itself answers only DHCP, DNS, ICMP echo and the ports of active redirects (TLS cases, DNS redirect); everything else, including UI/API and SSH, is dropped. The UI/API is reachable only from the management network and *management*-role WireGuard networks; SSH is left to the OS on the management interface. Active from M4 on, not only after setup (§2.16).
  2. **Access matrix** between networks (default policy per network pair).
  3. **Access rules** (§2.4): evaluated in the forward **and** input path on the conntrack original tuple, so they also apply to connections redirected to gateway services (e.g. "drop TCP 8883" beats a TLS case on 8883; "drop UDP 53" blocks the DNS proxy for that device); ordered, first match wins, per device, group, network or any source towards IP/CIDR, hostname, network or uplink, with protocol and ports; allow, drop, reject (ICMP), TCP reset; optionally cutting existing connections; counters per rule; usable as overlays in scenarios.
  4. **NAT:** masquerade towards the uplink per network (on by default for test networks, off between test and WireGuard networks). Port forwarding is M35.
- A management network or interface for the UI and API, separated from test networks (see §2.16).
- **Supported topologies:** three ports (uplink, test network, management) or two ports with management on the uplink side — the common case on a Raspberry Pi with one built-in port plus a USB adapter. In the two-port topology uplink and management are the same OS-owned interface; Chaos Gateway never changes its address. Both are covered by the level-2 tests (§4.5).
- **Policy routing:** traffic entering from test networks and WireGuard networks and the gateway's own service traffic (DNS proxy, TLS proxy upstream) use routing table 100, owned by Chaos Gateway. It contains the connected routes of all test and WireGuard networks, the client networks, static downstream routes, routes learned by BIRD, the uplink subnet and the default route via the uplink gateway. The PMTU mirror tables (§2.5) contain the same routes. The rules match on the test-network bridges (`iif`) and on the service user (`uidrange`). The OS-owned management interface keeps its own default route in the main table; other host processes (package updates, SSH) keep using it. Spike S12: without the policy route, forwarded test traffic left through the management interface; with it, forwarded traffic and sockets of the service user took the uplink and nothing leaked.
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
- IP versions: V1 is IPv4 only, inside the tunnels and for the underlay (the encrypted UDP); IPv6 for both comes with M32 (D22).
- **Who connects to whom:** either side of a link can be the reachable one. If the gateway has no publicly reachable IPv4 address (DS-Lite, carrier-grade NAT), it initiates the tunnel towards a remote site with a reachable endpoint and keeps it open with persistent keepalive; hub clients then need a reachable gateway (port forwarding on the upstream router).
- Addressing: between WireGuard and local test networks traffic is **routed without NAT** by default (remote test machines see real device addresses; the exported client configuration contains the needed routes). Towards the uplink it is masqueraded, like test networks.

**Keys and export**

- Default: the gateway generates the client's key pair (and optionally a preshared key), so it can export a complete configuration. Alternative: the client provides only its public key; the export then contains a placeholder for the private key.
- Client private keys are stored in the secrets directory (§2.16) and can be deleted after the first download ("export once").
- Export formats: `wg-quick` `.conf` file (download), **QR code** in the UI (for phones and tablets), QR as PNG/SVG via API, a zip with configurations of several clients, `chaosctl wg export <client>`. For links: the configuration of the remote side plus, if dynamic routing is used, a matching BIRD configuration snippet.
- Exports containing private keys are secret: shown with a warning, never included in configuration exports or logs.

**Faults and WireGuard**

- **Inner faults:** traffic of clients and remote networks is classified like any other traffic (§3.3: conntrack original tuple, direction bit); tc on the WireGuard interface's egress impairs the download towards the remote side, the other interfaces the upload.
- **Tunnel faults (underlay):** impair a client's or link's encrypted UDP traffic itself — latency, loss, blackout, flapping of the tunnel — to simulate a bad WAN between sites. Everything inside the tunnel is affected, including routing-protocol sessions. Tunnel faults are their own fault family and **stack** with inner faults (a device fault of 90 ms plus a tunnel fault of 70 ms gave 160 ms). Implementation (spike S15): towards the peer, the output hook classifies WireGuard's encrypted UDP by peer endpoint; from the peer, IFB with a flower filter on the outer source address and port. The encrypted packets do not inherit the inner connection's conntrack entry, so inner and tunnel classification do not interfere.
- Peer or link disable, key mismatch (rotated key not deployed) and endpoint blocking are available as **WireGuard-action overlays** and scenario steps (M10).

### 2.2.2 Routing

- **Static routes:** per test network (devices behind another router), per client (its client networks, automatic), per link.
- **Dynamic routing** with **BIRD 2**, managed by Chaos Gateway in its own instance (own configuration file and control socket, own container; an existing BIRD on the host is not touched):
  - Protocols: **BGP** (recommended for links; private ASNs), **OSPFv2** (point-to-point on links), **Babel** (suited for meshes of links), plus static.
  - Configured in the model: router id, ASN, neighbors per link, OSPF area, timers, and which prefixes are announced (selection of test networks, WireGuard networks, client networks, remote networks).
  - Chaos Gateway generates the BIRD configuration, validates it with `bird -p`, then applies it with `birdc configure` (graceful, sessions stay up). It is part of the revision and of preview/diff.
  - **Import safety:** learned routes go only into Chaos Gateway's routing tables (§2.2 policy routing, and the PMTU mirror tables), never into the main table. Import filters per neighbor: allowed prefix list, no default route unless explicitly allowed, never the management, uplink or gateway-own prefixes, maximum prefix count. A misbehaving remote site cannot hijack management traffic.
  - **Custom snippets:** per protocol, raw BIRD configuration can be added for cases the model does not cover; it is validated by `bird -p` and marked as "unmanaged" in the UI.
  - **Other routing daemons:** the routing layer is an adapter (generate configuration, reload, read status). BIRD is the only adapter in the plan; FRR is possible later. As a fallback, **external mode** imports routes that another daemon writes into a designated kernel table, with the same import filters.
- Spike S15: BGP over a WireGuard link learned the site's prefixes in 4.5 s; default route, management prefix and gateway-own prefixes were filtered, and nothing reached the main table. OSPF (point to point, multicast hellos) works over WireGuard too (7.8 s). A tunnel blackout withdrew the routes after 6 s (hold time 9 s); after restore they were back in 2.5 s.
- **Observability:** neighbor/session state, received and announced prefixes, route changes as events and in the activity log; the effective route for a destination is shown in the preview.
- **Routing faults:** tunnel faults, access rules on routing traffic (e.g. drop TCP 179) and link disable make convergence testable: "site B loses its link for 20 s — do devices reconnect after re-convergence?".

**V1 test networks are IPv4-only.** The gateway sends no router advertisements, offers no DHCPv6 and drops all forwarded IPv6 traffic. Devices only have link-local IPv6 addresses, so dual-stack devices fall back to IPv4. This is intended: faults and rules must never be bypassable, and a device reaching its server over an unimpaired IPv6 path would invalidate the test. On all interfaces it owns, Chaos Gateway disables router-advertisement acceptance, so its own services cannot leave over IPv6 either; the DNS proxy removes AAAA records from answers.

**Not impaired:** traffic between two devices on the *same* test network is switched directly at layer 2 and never passes the gateway's routing path. Faults apply to traffic that crosses the gateway.

## 2.3 Devices and Discovery

- Discovery sources: DHCP leases, the neighbor table (ARP/ND), conntrack (a flow observer on conntrack events, also used for the device view, hostname-set refresh and checks); for WireGuard networks the configured clients (identity: tunnel address and public key; online = recent handshake) and conntrack for hosts in client networks and behind links (identity: IP address).
- Configured devices have a name, MAC and optional fixed IP (DHCP reservation). Discovered devices appear automatically and can be adopted with one click.
- The device view shows: online state, IP, lease, current flows (destination, protocol, bytes, state), traffic rates, active rules/faults, captures.
- Rules and faults address the **device**, not an address. The compiler translates the device's current identifiers into match sets (see §3.3). The MAC address is the most stable identifier but is only visible for devices on the same L2 segment as the gateway. Devices behind another router, WireGuard peers and probes are identified by IP address or peer instead.
- **Identity changes during a test:** DHCP lease events, neighbor-table changes and WireGuard client changes update the observed state. The executor applies them as incremental map-element operations through the same serialized queue as full applies (no ruleset rebuild; target ≤ 1 s) and bumps the generation; a full apply always uses the latest observed state, so an apply can never restore an outdated address. Faults stay attached to the device when it gets a new IP, e.g. after the "force new IP" DHCP action. The old address stays mapped to the device while conntrack still has connections from it, unless it is leased to another device — then the new holder wins. DNS-derived hostname-set entries keyed on the old address are re-keyed.
- **Randomized MACs:** a device that changes its MAC appears as a new discovered device; two device entries can be merged manually.
- **Device attribute `trusts_test_ca`:** whether the firmware trusts the Chaos Gateway test CA. It decides the expected result of TLS checks (§2.8).

## 2.4 Access Rules and Faults: Semantics

Access rules and faults share one selector model but are separate objects with separate lists (D25): access rules are an ordered firewall list, faults are resolved by specificity.

**Selector**

```
source      device | group | network (incl. WireGuard and remote networks) | any
destination any | uplink | network | IP/CIDR | hostname
protocol    any | tcp | udp | icmp
port(s)     single, list, range
direction   upload (device → destination) | download | both
```

**Access rules** (allow, drop, reject, TCP reset)

- An ordered list; the **first match wins**, as in any firewall. Overlay rules come before configuration rules.
- The default policy per network comes from the access matrix.
- Evaluated in the forward and the input path on the conntrack original tuple, so redirected connections (TLS cases, DNS redirect) are judged by their original destination (§2.2).
- Changing a rule affects **new connections** by default (established traffic is accepted first).
- Optionally, *"also cut existing connections"* applies a time-limited `reject with tcp reset` to established packets of the selector. The device gets an immediate reset and can reconnect. The server side stays half-open, as in a real outage.
- Deleting conntrack entries alone does **not** cut a connection behind NAT; the flow is simply re-created (spike S3).

**Fault families**

A fault belongs to exactly one family. Precedence is resolved **per family**, so faults of different families combine: a device latency fault does not cancel a "DNS broken" profile on the network.

| Family | Parameters | Resolved into |
|---|---|---|
| Impairment | latency, jitter, loss, rate, queue limit, reorder, duplicate, corrupt, blackout, flapping | one netem configuration per direction |
| MTU | max. size, mode ICMP / black hole / MSS clamp | route, nftables or MSS rule |
| DNS | NXDOMAIN, SERVFAIL, timeout, delay, wrong answer, truncation, short TTL | DNS proxy behavior |
| TLS | certificate case, handshake reset/stall | redirect to the TLS responder |
| DHCP | short lease, forced new IP, option change, silence | Kea configuration |
| Tunnel | latency, loss, blackout, flapping of a WireGuard client's or link's encrypted traffic | netem on the underlay (§2.2.1) |

A **profile** is a bundle across families (e.g. "Bad LTE" = impairment only; a custom profile may add a DNS delay). When a profile is active on a scope, each of its parts competes in its own family like a fault on that scope. A fault and a profile part of the same family on the *same* scope **and in the same layer**: the fault wins; across layers the overlay wins (D24).

**Resolution within a family** — first overlays, then configuration (§2.1.1, D24); within each layer the most specific scope wins (first match):

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
  | 9 | any source + destination and/or port/protocol |
  | 10 | global |

  Within the same layer and level the **newer** entry wins; there are no explicit priorities (D26). A device in two groups is resolved the same way. Parameters are never merged: the winning fault's complete parameter set applies. The preview and the `explain` endpoint (§2.15) show the effective result and which faults were overridden.

**Worked examples** (normative; each is a golden test of the compiler)

| # | Active | Traffic | Effective |
|---|---|---|---|
| E1 | config: device A 50 ms · overlay (scenario): network IoT offline | A → broker | offline (overlay beats configuration) |
| E2 | config: network IoT 100 ms · config: device A 20 ms | A → broker | 20 ms (device is more specific) |
| E3 | overlay: group sensors 200 ms · overlay: device A + port 8883 loss 5 % | A → broker:8883 | loss 5 %, no delay (level 3 beats level 6; no merging) |
| E4 | same as E3 | A → ntp:123 | 200 ms (the port fault does not match) |
| E5 | overlay: device A 100 ms · overlay: network IoT "DNS broken" | A resolves and connects | DNS SERVFAIL **and** 100 ms (different families) |
| E6 | config: group G1 (A) 30 ms, created first · config: group G2 (A) 60 ms, created later | A → server | 60 ms (newer wins at the same level) |
| E7 | config: any → 203.0.113.0/24 loss 10 % · config: global 5 ms | A → 203.0.113.10 | loss 10 %, no delay (level 9 beats 10) |
| E8 | overlay: device A "Bad LTE" (2 Mbit/s) · overlay: device A latency 300 ms | A → server | 300 ms only (fault beats profile part on the same scope) |
| E9 | overlay: network IoT "Bad LTE" | A and B download in parallel | 2 Mbit/s each (per-device rate, D18) |
| E10 | config: device A 40 ms · overlay: tunnel of client rA 50 ms | A → host behind rA | 90 ms (impairment and tunnel families stack) |
| E11 | access overlay: drop A → tcp/8883 · overlay: TLS case on tcp/8883 for A | A → broker:8883 | dropped (access rules before redirects) |
| E12 | overlay: network IoT 100 ms | lab host behind WireGuard client → device A (lab host initiates) | not impaired by the IoT fault (initiator semantics: the lab host's scope decides) |

**Rate and queue limits are per device** (D18). "Bad LTE, 2 Mbit/s" on a group, network or globally gives **every** matched device its own 2 Mbit/s queue, as if each had its own LTE link. The same holds for an explicit queue limit and for "keep order" (netem `rate`). Impairments without these parameters (delay, jitter, loss, …) are per-packet effects and share one queue per winning fault. Addresses in a network that are not (yet) known as devices share one queue per scope until discovery (§2.3) adds them. The preview shows how many queues a scope creates, and the compiler refuses configurations above the capacity limit (`capacity_exceeded`, §3.3).

**What faults apply to**

- **Initiator semantics:** a device fault applies to connections the device *initiates*, in both directions (upload = packets in the connection's original direction, download = replies). Connections initiated towards the device by another device or by the server are matched by the initiator's scope, not by the responder's. Matching a device as responder is a later extension (§8).
- **Global** means: packets whose original source is in a test network, a WireGuard network or a remote network. The gateway's own traffic (management, package updates, BIRD sessions, WireGuard underlay unless a tunnel fault targets it) is never impaired, so a global "Offline" cannot lock the admin out.
- DHCP and ARP are never impaired by impairment faults; the DHCP family ("silence") exists for that.
- Connections that end on the gateway (DNS proxy, TLS responder, TLS proxy) **are** impaired like forwarded traffic, because the device experiences them as its path to the server (service namespace, §3.3).
- Faults act on **every packet**, including packets of connections that already existed when the fault was activated. Classification is therefore evaluated per packet, not cached per connection (see §3.3). Spike S10 confirmed this: with per-packet classification the first message after a change is affected, while conntrack-mark caching left the running connection on its old fault. Exception: redirect-based families (TLS, DNS redirect) change new connections only; activating a TLS case offers "also cut existing connections", and removing it resets the connections still held by the responder.
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
- **Wiring:** Kea hands out each network's gateway address as DNS server (option 6). The proxy runs in the service namespace (§3.3); queries to these addresses (UDP and TCP 53) are forwarded into it, so faults apply in both directions and it never conflicts with systemd-resolved on the host. Its upstream resolver is taken from the host's resolver configuration for the uplink or configured explicitly. In V1 it strips AAAA records (test networks are IPv4-only). DNS faults are overlays; the proxy keeps no state of its own: when it starts, it registers with the API and receives the current DNS overlays (internal API, long poll on `/api/v1/internal/dns/config`; hostname-set updates go through a synchronous call that returns after the executor has updated the set). It resolves upstream through the gateway like any other service traffic.
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

- **Per network:** every test network has its own DHCP scope with its own pool, reservations, lease time and options, and DHCP can be switched **on or off per network** (off: devices with static addresses, or another DHCP server on that segment). A network is a bridge (§2.2), so a network with several physical ports has **one** scope for all of them; a port that needs its own DHCP settings becomes its own network. One Kea instance serves all networks, one Kea subnet per network bound to the network's bridge. Test actions address a network or a single device.
- WireGuard networks have no DHCP; client addresses are assigned when the client is created and are part of its exported configuration (§2.2.1).
- Pools, reservations per MAC, lease time, options (router, DNS, domain, NTP, custom).
- Test actions:
  - short lease times
  - delete a lease (on renewal the client simply gets the same address again)
  - **force a new IP**: change the reservation → the renewing client gets a NAK and requests a new address
  - change gateway/DNS options
  - refuse leases (DHCP silent)
- Server: **Kea** (spike S6). dnsmasq also handles NAK and silence, but cannot hand out leases shorter than 120 s. Kea offers an API for lease operations; reservation and option changes are sent as `config-set` over the control socket, so the API never writes Kea's configuration file. The container image pins the Kea version (≥ 2.6); its socket and file paths follow Kea's path restrictions (`/run/kea`, `/var/lib/kea`).
- **Wiring:** the API service talks to Kea's control socket (owned by the `chaosgw` group). Lease events reach Chaos Gateway through Kea's `run_script` hook, which notifies the API; they drive device discovery and the identity updates of §2.3.
- **Uplink:** Chaos Gateway runs no DHCP client of its own; the OS configures the uplink (D3). A changed uplink address or gateway, observed through netlink, triggers a recompile and an event.
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
| Certificate from an untrusted CA (the never-distributed unknown CA) | ✓ must reject | ✓ must reject |
| Expired / not-yet-valid certificate | not distinguishable: fails as "unknown issuer" | ✓ must reject with the specific error |
| Hostname mismatch | not distinguishable: fails as "unknown issuer" | ✓ must reject |
| Self-signed certificate | ✓ must reject | ✓ must reject |
| Handshake reset / closed / stalled | ✓ retry with backoff, timeout handling | ✓ same |
| Device accepts a broken certificate | ✓ detected (handshake completes at the responder) | ✓ detected |

Chaos Gateway keeps **two CAs**: the *test CA*, which may be installed on development firmware, and an *unknown CA* that is never distributed. The "untrusted CA" case always uses the unknown CA, so it is a valid test for every firmware; expired, not-yet-valid and hostname-mismatch certificates are signed by the test CA.

A device without the test CA fails every certificate case at the first check ("unknown issuer"). Expiry and hostname validation can therefore only be tested individually on firmware that trusts the test CA. The most valuable test works with any firmware: *does the device accept a certificate it must reject?* If it does, the run records a failed security check. The expected result of each case comes from the device attribute `trusts_test_ca` (§2.3).

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
  - { id: lossy,   at: 20s, fault: { latency: 200ms, jitter: 50ms, loss: 10% } }   # replaces "slow": complete parameter set
  - { id: cut,     at: 30s, rule: { action: drop, protocol: tcp, ports: [8883], cut_existing: true } }
  - { id: restore, at: 45s, restore: true }
checks:
  - { reconnected: { protocol: tcp, ports: [8883] }, window: { from: restore, within: 30s } }
  - { dns_query_seen: { name: broker.example.com }, window: { from: restore, within: 30s } }
```

The scenario format is defined normatively as a JSON Schema inside the OpenAPI spec (`api/openapi.yaml`, schema `Scenario`; this example is part of `api/examples/configuration.yaml` and validated in CI). Check windows are relative to named steps.

- Scenarios are stored in the revisions (§2.1.1); YAML is the import/export format, so they can also be versioned in git.
- **Runs from a file:** `POST /api/v1/runs` also accepts an inline scenario plus parameters (e.g. `target.device`), stored with the run. CI jobs can run scenarios from their own branch without changing the gateway configuration (`chaosctl run -f mqtt-outage.yaml --set target.device=$DUT`).
- Step types: profile, fault, rule, DNS fault, TLS case, DHCP action, WireGuard action, capture start/stop, wait, remove, restore. A `wait` step (device online, connection established, DNS query seen) pauses the timeline until its condition holds; later steps shift by the waiting time, and a timeout ends the run as `error`.
- **Step semantics:** every step creates or replaces an overlay owned by the run. A step with the same key *(kind, target, selector)* as an earlier step replaces it — its parameters are complete, never merged (see `lossy` above). `remove: <step id>` removes one step's overlay; `restore` removes all overlays of the run. Steps inherit the scenario's target and may narrow it (a device of the target group or network) but never widen it, so the rule "one run per target" holds.
- **Preconditions:** before the first step, a run checks the baseline — the target device is online, uses the gateway's DNS when the scenario has DNS or hostname steps, and no overlays of other owners are active on the target. A failed precondition ends the run as `error` with the reason.
- **Zero-hit warning:** a fault or rule of the run that matched no packet is reported as a warning in the report (optionally as an error), because a "passed" run whose fault never hit proves nothing.
- **Checks** evaluate observations, e.g. "device reconnected to the broker within 30 s", "device did not accept the invalid certificate", "no traffic to unexpected destinations".
- A **run** stores the timeline as executed, events, counters, captures, check results and a report (JSON and JUnit XML for CI).
- Run lifecycle: `queued → running → passed | failed | error | aborted` — `failed` means a check failed, `error` means the engine could not execute a step.
- Scenario steps create overlays owned by the run; a run never creates revisions.
- A run keeps going when the client that started it disconnects. It ends when it reaches its last step or is stopped explicitly (`POST /api/v1/runs/{id}/abort`). Either way, its overlays are removed.
- Optional **lease** for unattended automation: the client must renew the lease periodically (heartbeat). If it expires, the server aborts the run and cleans up. Without a lease, the scenario's own end and the overlay TTLs are the safety net.
- Timing precision: a step counts as applied at the executor's commit timestamp. Target: within ±100 ms of the schedule on x86 with KVM or native; the tolerance for ARM64 hardware is set once hardware is measured (H1). The run timeline records the actual times and the generation (§2.15) of every step.

## 2.11 Capture

- Record by network, device or selector.
- Mechanism (spike S9, exact against a capture on the device itself): AF_PACKET (libpcap) on the network's interface with a BPF filter — full Ethernet frames with pre-NAT addresses on local networks, raw IP on WireGuard interfaces.
- Captures on the uplink cannot be attributed to devices after NAT and are offered only as "uplink capture".
- After V1: rule captures via NFLOG ("capture everything this fault affects", S9) and live streaming to Wireshark.
- PCAP/PCAPNG, ring buffer, size and time limits, disk quota, automatic cleanup.
- Download (also while the capture is running).
- TLS key log attached when the proxy was involved.
- Captures are attached to runs automatically when the scenario says so.

## 2.12 Diagnostics and Probes

**Diagnostics** return structured results:

- ping (with loss/RTT statistics)
- TCP/UDP port check
- DNS lookup
- HTTP(S) request
- TLS handshake details

Each can run from the gateway or from a probe (below). Traceroute, path MTU and throughput tests (iperf3) follow after V1.

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

- Per access rule and per fault: matched packets and bytes, from named nftables counters in per-rule and per-fault chains. *"Active"* is not enough; the counters show whether a rule actually matches. They are monotonic across re-applies (S11: named nftables counters survive) and reset only on reboot, which the API reports as a counter epoch.
- Per netem queue: packets dropped and delayed, reported with the faults that currently feed that queue; a queue that is re-created starts a new counter epoch.
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
  - the generation id (§2.15) of the applied revision, overlay set and observed state is stored as the comment of the single rule in the chain `generation`, which every apply flushes and rewrites (the comment of an existing table cannot be changed); maps and sets hold exactly the expected elements, except the DNS-derived address sets, whose elements the compiler does not own;
  - tc classes exist and their netem parameters match within the kernel's rounding;
  - routes and rules in the Chaos Gateway routing tables match;
  - the managed services (Kea, DNS proxy, TLS responder) report healthy.

  The normalizers for `nft -j` and `tc -j` output have their own golden tests.
- **Commit-confirm:** changes that could lock out the admin (management access, UI/API binding, *management*-role WireGuard networks, gateway protection) are rolled back automatically unless confirmed within 60 s (configurable, tests use seconds).
- **Anti-lockout:** access from the management network to the gateway's **control plane** (UI, API, SSH) is always allowed and cannot be removed by rules. The protection covers nothing else: traffic from the management network to devices or the Internet follows the normal rules.
- **Revisions:** diff, rollback, export/import (secrets excluded by default), clone.
- **Concurrency:** every write names the revision it is based on (`If-Match`). Conflicting writes are rejected with `409 revision_conflict` (optimistic locking), so UI and automation cannot silently overwrite each other; the client reloads and reapplies its change. A three-way merge is not part of V1.
- **Ownership:** Chaos Gateway only manages objects it owns: its own nftables table, qdiscs on its interfaces, its routing tables and rules, routes with its own protocol tag. Objects it doesn't own are ignored. V1 verifies them on every apply and at start; continuous drift detection with reconcile follows after V1.
- **Boot:** the last committed revision is applied at boot. Overlays are not restored after a restart; tests start from a clean baseline (§2.1.1).

## 2.15 API and CLI

- REST under `/api/v1`, described by OpenAPI (`api/openapi.yaml`, spec-first). UI and automation use the same API.
- **The spec is normative for the domain model and the API shape** — field names, types, enums, defaults, paths, status codes and error codes; this plan is normative for behavior. A contradiction between the two is a bug to fix in both. Examples that double as test fixtures are in `api/examples/` (validated by `api/examples/validate.py`). Model conventions (D30):
  - Collections in the configuration are maps keyed by UUID (so a JSON Merge Patch can add, change or delete a single object); the order of access rules is a separate id array.
  - The resource endpoints (`/networks`, `/devices`, `/rules`, …) are read-only views of the active revision joined with observed state; all configuration writes go through candidate revisions.
  - Durations, percentages and bit rates are strings with units (`200ms`, `10%`, `2Mbit`); ports are `ports: [8883]` plus `port_ranges`.
  - Overlays and scenario steps share their bodies; the source part of a selector is `target` there and `source` in the configuration. Flat impairment parameters apply to both directions; `upload`/`download` give each direction its own complete set.
  - Devices, WireGuard clients and probes share one UUID and name namespace.
  - Request bodies are decoded strictly (unknown fields are rejected).
  - The service containers use an internal part of the API (`/api/v1/internal/…`, scope `service`): DNS proxy and TLS responder fetch their configuration by long poll and report observations, Kea's hook reports lease events.
- SSE for events and live data.
- Main resources:
  - `uplink`, `networks` (incl. WireGuard networks, clients and `…/export` as `.conf`, QR PNG/SVG, zip), `routing` (static routes, BIRD protocols, `…/status`), `devices`, `groups`, `rules`, `profiles`, `scenarios` (read the active revision)
  - `revisions` (candidates, `…/preview`, `…/apply`, `…/confirm` for commit-confirm, `…/diff?base=`)
  - `explain` (`GET /api/v1/explain?device=…&dst=…&port=…`: access verdict, winning fault per family, overridden faults, kernel ids), `state` (current generation, last apply), `capabilities` (features available in this build)
  - `overlays` (all kinds of §2.1.1, with owner, TTL and lease; `…/renew`)
  - `runs` (`POST /api/v1/runs` with a scenario name or an inline scenario; `…/abort`, `…/renew`, `…/report.json`, `…/report.xml`), `captures`, `diagnostics`, `probes`
  - `reset`, `metrics`, `events`, `audit`, `dns/queries` (query log), `tls/ca` (test CA download), `auth` (sessions, tokens), `setup` (first start), `system` (`…/busy` for update scripts: are runs active?, `…/certificate`, preflight, interfaces, health)

**Conventions**

- Errors as RFC 9457 `application/problem+json` with stable codes, e.g. `validation_failed`, `revision_conflict`, `confirm_pending`, `verify_failed`, `lockout_protected`, `target_busy`, `capacity_exceeded`, `unsupported_feature`.
- **Generation:** every apply and every overlay or identity change produces a new generation id. Writes return it, `GET /api/v1/state` shows the current one, and the SSE event `applied` announces it, so tests can wait for a state deterministically instead of sleeping. Runs record the generation of every step.
- **Capabilities:** features not available in the running build (e.g. a step type whose milestone is not done) are rejected with `unsupported_feature`; `GET /api/v1/capabilities` lists what is available.
- Every object has a UUID and a unique, renameable name; the API accepts either in paths.
- Lists use cursor pagination and simple filters (`?network=`, `?owner=`).
- **Normative details:**

  | Topic | Rule |
  |---|---|
  | Partial updates | JSON Merge Patch (RFC 7396) on candidate revisions |
  | Base revision | only in `If-Match`; the active revision is the `ETag` |
  | Busy target | runs are queued by default; `?queue=false` returns `409 target_busy` instead |
  | Token scopes | *read-only*; *overlays* (overlays, runs, own reset); *full* (configuration, `reset?owner=all`, tokens) |
  | Overlay owner | the token, the run, or — for UI actions — the admin user (not the browser session) |
  | Durations | Go duration strings (`90s`, `5m`, `1h`) |
  | `Idempotency-Key` | kept for 24 h |
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
- **First start:** until setup is finished, the UI listens on all interfaces that are not yet assigned to a test network and requires a one-time setup token that the container prints to its log (`docker compose logs`). The setup wizard assigns interfaces, sets the admin password and then restricts the UI to the management network.
- Devices under test are untrusted: test networks cannot reach the management plane. WireGuard networks with role *test* are treated the same; only WireGuard networks with role *management* (§2.2.1) may reach the UI/API.
- V1: one admin account plus API tokens (scoped: read-only, overlays only, full).
- **Sessions:** the UI uses an HTTP-only session cookie with CSRF protection; SSE uses the same cookie. Automation uses bearer tokens; tokens are stored only as hashes and shown once at creation.
- **Privilege separation** (§3.8): every component runs in its own container from the same image. Only the executor container is privileged; the API container runs as an unprivileged user with all capabilities dropped; DNS proxy, TLS responder, Kea, BIRD and mitmproxy get only the capabilities they need (e.g. `NET_BIND_SERVICE`, `NET_RAW`). All containers have a read-only root file system and `no-new-privileges` where possible.
- The executor accepts typed, validated operations, never command strings (see §3.1). It also checks their scope: nftables operations may only touch `table inet chaosgw`, routing operations only Chaos Gateway's routing tables and rules, tc operations only interfaces assigned to Chaos Gateway. One narrowly defined exception: the operation that keeps an accept rule for Chaos Gateway's interfaces in Docker's `DOCKER-USER` chain (§3.8).
- The executor's Unix socket (on a volume shared between the containers) checks the caller with `SO_PEERCRED` and starts every connection with a protocol-version handshake; API and executor refuse to work with a mismatched version (e.g. a partially updated set of containers).
- Secrets live in a protected directory (0600, own volume): admin password hash, API tokens, test CA and unknown-CA keys, TLS keys, WireGuard keys. They are excluded from logs, events, API responses and exports by default.
- **Forgotten admin password:** reset from the host with `docker compose exec api chaosgw admin reset-password`; this also ends all sessions.
- The test CA key and decrypted traffic in captures are sensitive; captures with key logs are marked accordingly.

## 2.17 Web UI

**Reference prototype:** a clickable design of the main screens is kept in [`docs/ui/prototype/`](ui/prototype/) (one `.dc.html` file per screen, sample data). Where this section and the prototype disagree, this section wins — in particular, the prototype still shows access rules and faults in one list (now two screens, D25) and a three-way conflict dialog (now reload and reapply).

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
Overview · Devices · Networks · Access rules · Faults · Profiles · Scenarios · Captures · Diagnostics · Activity
Header per page: title + context line (revision, sync state) · health · active-faults counter · </> API · primary action
```

### Screens

| Screen | Content | Key interactions |
|---|---|---|
| **Overview** | network topology (test networks → gateway → uplink) with fault badge on the path; active faults with target, effect, affected packets, expiry; running scenario with progress; uplink traffic chart; recent events | every item links to its detail |
| **Devices** | table: name, IP, MAC, network, traffic, faults, status; search; filter chips (All / Online / With faults / Not adopted) | adopt discovered devices; row opens device detail |
| **Device detail** | header with identity; active faults with parameters, affected packets and remaining TTL; path view MQTT → broker with fault badge; flows; DHCP lease and DHCP test actions; recent DNS; captures; optional API panel | **Add fault** dialog (below); remove a fault; apply profile; TLS test; capture; diagnose |
| **Add fault** (dialog) | target, traffic, direction (Both / Upload / Download), start-from presets (LTE, Bad LTE, Satellite, Offline), latency, jitter, loss, duration; *Advanced*: rate, reorder, duplicate, corrupt, keep packet order; live preview sentence | Apply → fault appears with TTL; toast; API panel shows the equivalent `POST /api/v1/overlays` |
| **Access rules** | ordered rule list: position, name, selector, access chip, hit counter, enable switch; system rule "Control plane access" locked at the top; overlay rules shown above configuration rules | select a rule → editor: **IF** from / to / protocol / ports, **THEN** Allow · Drop · Reject · TCP reset + "also cut existing connections"; matches box with hits, bytes, last match and the effective result in words; hostname hint for DNS-derived matching |
| **Faults** (D25) | faults grouped by family and sorted by scope (device → group → network → any → global), overlays and configuration marked; per fault: selector, parameters, affected packets, TTL, and "overridden by X" where another fault wins | add/edit fault (dialog below); "Explain" for a device and destination shows the winning fault per family (`explain` endpoint) |
| **Preview & apply** (drawer) | "what changes" in domain terms (rule, field, old → new); expandable Linux changes (nft, tc, verify step) | Back to editing · Apply revision N → verified toast |
| **Profiles** | cards per profile with parameters and where it is active; scope selector (everything / network / group / device) and duration | activate / deactivate on the chosen scope (one profile per scope); "Measure with probe" shows the measured values against the profile |
| **Scenarios** | list with target, length, steps, last result; tabs Timeline / YAML / Runs; timeline of steps with type chips; checks; run artifacts | Run → live progress, current step marked, checks resolve to PASSED/FAILED at the end, artifacts appear (pcapng, JUnit, JSON, events); Abort → the run's overlays are removed |
| **Networks** | cards for test networks, management (owned by the OS) and uplink; settings of the selected network (subnet, gateway, attachment, DHCP, DNS, IPv6 blocked); access matrix between networks | toggling a matrix cell creates a change that goes through preview and apply like any other; commit-confirm only if it is lockout-relevant; Linux view shows the compiled forward chain |

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
| Concurrent change (optimistic locking) | dialog: "revision changed by <user/token> while you were editing" · show their change · reload and reapply mine · discard mine |
| Run states | queued · running (progress, current step) · passed · failed (a check failed) · error (a step or precondition could not be executed) · aborted (the run's overlays were removed) |
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
│ chaosgw exec  (Go, root in the only privileged container, §2.16)     │
│   applies plans: nft -j -f · tc -batch · ip -batch · sysctl ·        │
│   conntrack · wg · ethtool · capture processes · probe namespaces ·  │
│   netlink session for DNS-derived address sets and identity updates  │
│   reads state: ip -j · tc -j · nft -j · conntrack events             │
└───────────────┬──────────────────────────────────────────────────────┘
                │
    Linux kernel: nftables · conntrack · tc/netem · routing · netns

Further containers (same image, §3.8):
  in the service namespace (§3.3, S16), joined to the holder container svcns:
  chaosgw dns    DNS proxy (Go; part of the project)
  chaosgw tls    TLS responder for certificate test cases (Go; part of the project)
  tls-proxy      mitmproxy + Chaos Gateway addon (Python sidecar, interception only)
  in the host network:
  DHCP server    Kea (decided in spike S6)
  capture        libpcap processes started by the executor (no container of their own)
  BIRD 2         routing daemon, own instance, config generated by the compiler
```

- The core is **one Go binary, `chaosgw`**, with subcommands for the API server, the privileged executor, the DNS proxy and the TLS responder. Each runs in its own container with only the privileges it needs (§3.8); the web UI is compiled into the binary (`go:embed`). The spikes built the DNS proxy and TLS responder in Node.js; their findings are language-independent.
- Python runs only in the TLS interception sidecar (mitmproxy addon). It communicates with the core via a local API and can be left out entirely if TLS interception is not used; the certificate test cases work without it.
- Services register with the API when they start and receive their current state (DNS overlays, TLS cases); a restart of a single container therefore needs no coordination. All containers check the executor protocol version, so a half-updated set refuses to work instead of misbehaving.
- The executor accepts only a closed set of operation types. Each is validated and turned into command invocations with argument arrays: no shell, fixed binary paths. It checks the scope of every operation (§2.16) and is the **only writer** to the kernel's network configuration — except the routes BIRD installs into Chaos Gateway's routing tables (import-filtered, §2.2.2) — so all changes are serialized — including the DNS proxy's address-set updates, which reach it through a narrow "add elements to set" operation over the same socket.
- The executor protocol is versioned; both sides check the version and the peer credentials when a connection starts (§2.16).
- The Linux adapter uses the standard command-line tools in V1 (§3.4). It sits behind an interface, so parts can later be replaced by native netlink access (fewer process starts, better error details, events) without changing the compiler.
- Every executor operation takes an optional **network namespace**. This makes the entire stack testable in namespaces without touching the host (see §4).

## 3.2 Compiler

```
Configuration (revision) + Overlays + Observed state (addresses of devices)
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
    - nftables: one atomic transaction per apply that **re-creates the static structure but keeps dynamic data** (spike S11): `add table/set/map/counter/chain` (a no-op for objects that exist), `flush chain` and `flush map` for the compiled parts, then the new rules and map elements, then explicit `delete` for chains, maps, sets and counters of removed rules and faults. The DNS-derived address sets and the named counters of existing rules and faults are never flushed; they survive every apply and stay monotonic. S11 re-applied the ruleset 20 times while a device sent 1500 packets through a fault: not one packet lost its classification, and a set element and the counters survived.
    - `add set/map` fails if an object with the same name but a different type or flags exists, and would fail the whole transaction, including a rollback. Set and map names therefore carry a short hash of their definition; a changed definition creates a new object and deletes the old one (for a DNS-derived set this starts a new counter epoch and an event).
    - tc: existing qdiscs/classes are changed in place. Replacing a qdisc of the same kind keeps its queue too (S2); only deleting it or changing its kind drops queued packets.
    - **Make before break:** when traffic moves to a new fault id, the new tc classes are created on all interfaces first, then the nftables transaction switches the classification, and the old classes are deleted only after the largest configured delay plus 1 s, so packets still queued in them are delivered.
- **Complete parameter sets:** `tc qdisc change` keeps netem attributes that are not given; spike S2 showed a rate limit surviving a change. The compiler therefore always emits every netem attribute, with neutral values where unused (e.g. `rate 0bit`).
- **Overlapping selectors:** nftables interval maps cannot hold overlapping elements (e.g. `10.0.0.0/8` and `10.1.0.0/16` in one map, or overlapping port ranges). The compiler splits overlapping CIDRs and ranges of the same level into disjoint pieces, each carrying the fault that wins there; golden tests cover nested and partially overlapping cases.
- **nftables as JSON:** the compiler generates the ruleset as JSON, not text. Text syntax has pitfalls that the spikes hit repeatedly: reserved words used as chain names (`fwd`, `dnat`) and a missing `;` before `}` make the whole transaction fail.
- Overlay changes (the frequent case) touch faults, marks and classification maps; services only through their runtime control channels (DNS proxy, TLS responder, Kea control socket); never routing, except the PMTU mirror tables for MTU-family overlays.

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

- **Classification identity:** after precedence resolution, the compiler assigns an id to each **winning fault** (per family that uses tc: impairment and tunnel), and for faults with a rate, an explicit queue limit or "keep order" one id **per matched device** (D18, §2.4). Ids are stable while the winning fault stays the same: changing its parameters changes the existing netem qdiscs in place (complete parameter sets, §3.2) and keeps their queues.
  - Access rules need no marks; they are evaluated directly in nftables.
  - **Mark layout:**

    | Bits | Meaning |
    |---|---|
    | 4–15 | effective-fault id, 12 bits: up to 4095 ids at the same time (widened from 8 bits because of per-device queues, D18) |
    | 16 | direction: 0 = packet in the connection's original direction (upload of the initiator), 1 = reply (download), from `ct direction` |
    | 17–19 | PMTU table index: 0 = none, 1–7 select one of up to seven PMTU mirror tables (§2.5; S13 used a single bit, `0x00200000`) |
    | 20 | service selection: route into the service namespace (§3.3, S16) |
    | 21–23 | reserved for further routing marks |
    | 0–3, 24–31 | untouched, free for other software |

  - Every chain that writes the fault id must keep the direction bit (mask `0xffff000f`, not `0xfffe000f`); a golden test checks the compiled masks. In S15 a wrong mask gave both directions the upload parameters.
  - With the direction bit, every interface uses the **same** mapping (id, direction) → tc class. Without it, the meaning of an id depended on the interface, which breaks as soon as two test networks exist: the second network's interface carries both its own devices' downloads and the first network's uploads towards it. Spike S11 showed exactly that: a group fault (100 ms up, 20 ms down) on devices in two networks gave the correct 120 ms for traffic from network A to network B with the direction bit, and 43 ms without it.
  - **Lookup chain** in the order of §2.4 precedence, each a verdict map keyed on the conntrack original tuple, so it is identical for both directions and unaffected by NAT:
    1. `ct original ip saddr . ct original ip daddr . meta l4proto . ct original proto-dst` (device + destination + port)
    2. `ct original ip saddr . ct original ip daddr` (device + destination, including DNS-derived hostname sets)
    3. `ct original ip saddr . meta l4proto . ct original proto-dst` (device + port)
    4. `ct original ip saddr` (device)
    5. then the same for groups and networks, then any source + destination/port, then global

    The maps hold the **resolved** result, not raw faults: the compiler writes, for every key, the id of the fault that wins under §2.4 including D24. Where a less specific overlay overrides a more specific configuration fault (E1: device fault in the configuration, network "offline" as overlay), the device-level entry carries the overlay's id or is removed, so the first-match lookup never contradicts the precedence rules.

    Protocol-only selectors (e.g. "all UDP", ICMP) use a map keyed on `meta l4proto` at the level of their scope. Hostname selectors are ordered rules against the per-device DNS-derived sets, placed at their level.

    S11 verified levels 2 and 4 together: a device+destination fault (50 ms) won over the device's group fault (120 ms) for that destination only.
  - Capacity: 4095 ids; each active id needs up to two HTB classes (upload, download) per interface it leaves through. The compiler enforces a configurable class limit per interface (default 1000 on x86 and a conservative ARM64 default of 200, adjusted by H1 when it runs) and reports `capacity_exceeded` with the scope that caused it. Example: a rate-limited profile on a network with 250 devices needs 500 classes. S8 measured 50 classes; 500 and more are measured on the target hardware (H1).
- **Only test traffic is touched:** the classification chains first check that the packet's original source or destination belongs to a test, WireGuard or remote network (or is a tunnel of a WireGuard client or link); all other packets pass without their mark being read or written. Other software on the host that uses marks (Docker, VPN clients) is therefore not affected; the preflight lists known mark users (e.g. Tailscale `0x40000/0x80000`, kube-proxy `0x4000/0x8000`, wg-quick's fwmark) and reports overlaps with bits 4–23.
- **Per packet, not per connection:** the id is computed for every packet, using the conntrack original tuple for replies. Caching it in the conntrack mark would keep existing connections on the old fault after an overlay changes (S10 showed exactly that). Spike S8 found no measurable throughput cost with 1000 device and 1000 device+port entries on x86; Raspberry Pi still has to be measured.
- **tc topology** (confirmed in S2 and S11): an HTB root with a default class and one class per active (id, direction), each with a netem leaf, selected by a `fw` filter with mask (id 0x0a: `handle 0x000a0/0x1fff0` for upload, `0x100a0/0x1fff0` for download; the spikes used the earlier 8-bit layout `0x00a00/0x1ff00`, same mechanism).
  - A classful root is needed as soon as more than one fault is active on an interface; `prio` would be too small, since it is limited to 16 bands.
  - Rate limiting works both as netem `rate` and as HTB class rate. With 50 HTB classes, throughput dropped by about 10 % (S8).
- **Gateway services in a service namespace** (DNS proxy, TLS responder, TLS proxy; decided by spike S16, D29):
  - DNS faults are implemented inside the DNS proxy (delay, drop, wrong answer); TLS/HTTP faults inside the TLS components. L3/L4 faults of the device must still apply to these connections (§2.4).
  - The services do not run in the gateway's own network namespace but in a **service namespace** connected by a veth pair (`svc0`, gateway side `169.254.100.1/30`, service side `169.254.100.2/30`).
  - **Selection:** traffic for a service gets routing-mark bit 20 in a prerouting chain right after classification. Selectors are ordinary nftables expressions, including sets (DNS-derived hostname sets, device sets). Policy rule `fwmark 0x00100000/0x00100000 → table 102`; table 102 routes into `svc0` and has a `prohibit` fallback route, so selected traffic **fails closed** when the service namespace is missing instead of silently reaching the real server (S16 C4).
  - **Redirect inside:** the packets arrive unchanged (original destination); inside the service namespace nftables redirects them to the service's local port, and the service reads the original destination with `SO_ORIGINAL_DST` (S16: `203.0.113.10:8883` and a set-selected `203.0.113.20:8883`). Queries to the gateway's own DNS address are DNAT-ed to `169.254.100.2:53` instead, because packets to a local address cannot be policy-routed and there is no original destination to preserve.
  - **Faults:** upload leaves the gateway through `svc0`, download through the device's network interface — both are normal egress with fw marks, so the device's faults apply without IFB and without an output hook. S16: +80 ms for a 50/30 ms fault over TCP and UDP, set-based selection included, forwarded traffic of the same device not impaired twice. The alternative with IFB and flower filters missed the upload fault for set-based redirects (+30 instead of +80 ms), because flower cannot match sets.
  - The services' own upstream connections (TLS proxy → real server) leave through the gateway like other traffic (policy rule for `iif svc0`).
  - The output hook remains only for the gateway's own packets that faults can target: the encrypted WireGuard underlay (tunnel faults, S15).

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
- **NIC offloads:** GRO, GSO, TSO and LRO would let netem drop or delay whole aggregates of up to 64 KB instead of single packets. The executor switches them off (`ethtool -K`) on the physical interfaces and bridges of test networks and on the uplink; preflight and verify check it. The throughput cost is measured in H1.
- **Preflight check** at every start. It verifies:
  - kernel version
  - modules — one list, shared with the test-container preflight (§4.5): `sch_netem`, `sch_htb`, `cls_fw`, `cls_u32`, `cls_flower`, `act_mirred`, `ifb`, `nf_conntrack`, `nf_tables` with NAT/ct/dup/reject, `veth`, `bridge`, `wireguard` (M4b), later `8021q`
  - tool versions
  - IP forwarding
  - Docker (always present, §3.8): its FORWARD policy is DROP; an accept rule in Chaos Gateway's own table does not override it, only an accept in Docker's `DOCKER-USER` chain does (spike S7). The executor keeps that rule for Chaos Gateway's interfaces (a dedicated, narrowly scoped operation) and verify checks it.
  - other firewalls (ufw, firewalld) and known mark users (§3.3)
  - that the assigned test interfaces carry no OS configuration (netplan/NetworkManager), and that the uplink has an address and a gateway
  - offloads (above)

  Missing items are reported with the fix. On Ubuntu generic kernels all required modules are in the base `linux-modules` package; the host setup (§3.8) loads them at boot through `/etc/modules-load.d/`. Minimal kernels, such as some VM or cloud sandboxes, can lack netem entirely.
- **Interfaces** are identified by MAC address and name. If an assigned interface disappears (e.g. a USB adapter is unplugged), its network becomes *degraded* with an event; the rest keeps working and no safe mode is triggered. When it comes back, the network is re-applied.

## 3.5 Host Ownership

Chaos Gateway owns the interfaces **assigned to test networks**. The operating system keeps the management interface and the **uplink** (D3): netplan configures their addresses, gateways and DHCP as usual, and Chaos Gateway never changes them.

- Test interfaces must not be configured by netplan/NetworkManager (they simply have no entry there); the preflight checks this, and the setup wizard shows the netplan snippet to remove if one exists. Chaos Gateway runs in a container and does not edit host files.
- Its own nftables table (`inet chaosgw`), its own routing tables (100 and the PMTU mirrors) and `ip rule` entries (policy routing, §2.2), its own route protocol tag, one bridge per test network, WireGuard interfaces, qdiscs on its own interfaces and bridges and on the uplink (egress faults), router-advertisement acceptance off on its interfaces.
- In the two-port topology the management network shares the uplink: the control plane is then reachable from the uplink side, and the anti-lockout rule applies to the management addresses configured at setup.
- Other firewalls: detected by the preflight check. A drop verdict in another table still drops packets, so conflicts are reported, not silently ignored.

## 3.6 Persistence

Docker volumes (bind mounts on the host, backed up like any directory):

```
/etc/chaos-gateway/
    config.json            pointer to the active revision
    revisions/000042.json  immutable revisions (incl. profiles and scenarios)
/var/lib/chaos-gateway/
    secrets/               0600
    runs/<id>/             events.jsonl, report.json, report.xml, captures
    captures/
    state/                 last-known-good marker, record of runs aborted by a restart
/var/log/chaos-gateway/    audit.jsonl (service logs go to the container log)
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
| Logging | `log/slog`, JSON to stdout (container log) |
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

**Build and packaging:** Makefile as in sessile (`make dev`, `make test`, `make build`); the frontend build lands in `web/dist` and is embedded; one multi-arch container image (amd64/arm64, `docker buildx`) with `chaosgw`, pinned Kea and BIRD versions, tcpdump and the mitmproxy sidecar in its own Python virtualenv; a `compose.yaml` and a host-setup script (§3.8).

```
chaos-gateway/
  api/                openapi.yaml (source of truth), examples/ (fixtures + validate.py)
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
    wireguard/        WireGuard networks, clients, keys, export (.conf, QR)
    routing/          static routes, BIRD configuration and status
    observer/         observed state: leases, neighbors, handshakes, conntrack flows
    dnsproxy/  tlsresponder/  dhcp/  capture/  store/
    testbed/          namespace topology harness (from spike S1)
  web/                Vue app (src/), build output dist/ embedded via go:embed
  clients/            generated TypeScript and Python clients
  sidecars/tls-proxy/ mitmproxy addon
  profiles/  scenarios/  deploy/ (Dockerfile, compose.yaml, host setup)  docs/
```

## 3.8 Deployment

**Production runs in Docker** (D2): one multi-arch image, started with `docker compose` on an Ubuntu 24.04 or 26.04 host (x86-64 or ARM64). The executor, API, Kea and BIRD use the **host network**, because Chaos Gateway configures the host's interfaces, bridges, nftables, tc and routes. The gateway services (DNS proxy, TLS responder, TLS proxy) share the **service namespace** (§3.3) held by a small holder container.

| Container | Privileges | Why |
|---|---|---|
| `exec` | **privileged** (the only one), host network, `/run/netns` shared with the host (`rshared`), `/lib/modules` read-only | writes nftables, tc, routes, sysctls (per-interface sysctls of new bridges need a writable `/proc/sys`, which only a privileged container has, S7), network namespaces for probes, loads missing modules |
| `api` | unprivileged user, all capabilities dropped | REST/SSE/UI; reaches the executor, Kea and BIRD through Unix sockets on a shared volume |
| `svcns` | none, `--network none` | holder of the service namespace: only `sleep`; the executor attaches the `svc0` veth pair to it by PID and installs the redirect rules inside |
| `dns`, `tls` | `NET_BIND_SERVICE`, joined to `svcns` (`network_mode: service:svcns`), unprivileged user | bind port 53 and the redirect ports inside the service namespace |
| `kea` | `NET_RAW`, `NET_BIND_SERVICE` | DHCP on raw sockets |
| `bird` | `NET_ADMIN`, `NET_RAW`, `NET_BIND_SERVICE` | writes routes into Chaos Gateway's tables; OSPF needs raw sockets |
| `tls-proxy` (optional) | `NET_BIND_SERVICE`, joined to `svcns` | mitmproxy, only when interception is used |

- **Host setup** (a script shipped with the image, run once): installs Docker Engine if missing, loads the kernel modules at boot (`/etc/modules-load.d/chaos-gateway.conf`), sets `net.ipv4.ip_forward=1`, and prints the netplan changes for the test interfaces. It never touches the uplink or management configuration.
- Docker itself is always present: its FORWARD DROP policy is handled through `DOCKER-USER` (§3.4). Chaos Gateway's networks must not overlap Docker's own address pools.
- **Start order:** `exec` first (health check: executor socket ready and initial apply verified), then `svcns` (the executor attaches the veth pair), then the services.
- **Service namespace lifecycle** (S16 part C): a restarting service container rejoins the holder's namespace and works immediately. If the holder itself restarts, it gets a new namespace; services still running keep the old one alive. The executor therefore watches the holder's namespace (inode) and re-attaches `svc0` when it changes; each service checks that its namespace carries the `svc0` peer address and exits when not, so its restart policy moves it into the current namespace. While no namespace is attached, selected traffic fails closed (`prohibit` route). Restart policy `unless-stopped`; containers restart after a host reboot with Docker.
- **Stop:** on `SIGTERM` the executor removes all overlays before it exits (§2.1.1); the kernel state with the configuration stays, so devices keep their normal connectivity while the gateway is stopped. `docker compose run exec chaosgw teardown` removes everything Chaos Gateway created.
- **Development** uses a devcontainer with the same toolchain (unprivileged, §4.5); level 1 in CI uses a privileged one. A native package (.deb) is not planned for V1 but possible later from the same binary.
- **Dedicated appliance image** (Raspberry Pi / x86) as a later option.

## 3.9 Updates and Recovery

- Updates by pulling a new image (`docker compose pull && docker compose up -d`). Configuration migrations run at start, with a backup of the previous revision.
- **Last known good:** if applying the configuration at boot fails, the gateway applies the last revision that worked. If that fails too, it starts in **safe mode**: management access only, no forwarding, UI shows the error.
- An interrupted apply (power loss) is recovered at boot by recompiling from the committed revision. The kernel state is never the source of truth.
- **Upgrade during a run:** an update would end active runs as `aborted`. The shipped update script therefore waits until `GET /api/v1/system/busy` reports no active run (up to a timeout) before it restarts the containers; all containers are updated together, and the executor protocol version check (§3.1) rejects a mixed set.
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
| Linux integration | real kernel behavior in namespaces: routing, NAT, faults, rules, DNS, DHCP, TLS proxy | `go test` + testbed in a VM (§4.5 level 1b, the standard on the development VPS) or in a privileged test container (level 1, CI only) | every commit |
| Measurement | statistical accuracy of faults, timing of scenarios | testbed on a machine with KVM or native (§4.4, open question Q1) | nightly |
| API contract | OpenAPI conformance, error cases, concurrency | `go test` against the spec; generated clients compile | every commit |
| UI | components against a mocked API; a few end-to-end flows against the real stack in the testbed | Playwright | every commit / nightly |
| Distribution | host setup, container start, preflight, smoke tests on clean Ubuntu 24.04 and 26.04 hosts (x86-64 appliance VMs with KVM; the arm64 image functionally in emulated level 1b) | appliance VMs (§4.5 level 2, harness built in M5b) | nightly / before release |

## 4.2 Testbed

A library that builds topologies from network namespaces and virtual interfaces (see §4.5 for the building blocks). Default topology, as used in the spikes; the default for product tests has **two** test networks, because some errors only appear there (spike S11: fault direction between two networks), and attaches each network through a gateway-side bridge as in production (§2.2):

```
 ns: client-a ─┐                                                 ┌─ ns: server
 ns: client-b ─┼─ ns: switch ─ lan0 [ ns: gateway ] wan0 ────────┤  (HTTP, TLS, MQTT broker,
 ns: probe    ─┘   (bridge)      Chaos Gateway stack runs         │   DNS upstream, iperf3, NTP;
                                 against this namespace           │   no route back → NAT required)
```

- A second test network (`lan1` ─ switch ─ client-c) and a management interface with its own default route (as in spike S12) are part of the default topology.
- WireGuard is part of the default topology from M4b on (as in spike S15): an "internet" router namespace behind the uplink, a hub client with a client network behind it, and a link site. **Attachment matrix:** every integration test of forwarded traffic (classification, faults, rules, DNS, TLS, capture, scenarios) runs for a local test network **and** a WireGuard client network unless the feature does not apply (DHCP and MAC identity exist only on local networks).
- Tests start the real API and executor, pointed at the gateway namespace. Nothing touches the host network.
- The server namespace provides reference services, with valid and invalid certificates for TLS tests.
- Clients use standard tools: `ping`, `curl`, `openssl s_client`, `dig`, `udhcpc`, `iperf3`, `mosquitto_sub`, `wg`, BIRD, plus a small measurement tool.
- **Test clock:** the scheduler, TTLs, leases and the commit-confirm timeout take their time from an injectable monotonic clock, and all timeouts are configurable. Logic tests (TTL expiry, lease expiry, confirm timeout, step order) run with a fake clock in milliseconds; only the measurement tests use real time.

## 4.3 Measuring Faults

Faults are random processes; tests use statistics, not exact values:

- **Latency:** send N ≥ 200 probes, then compare the median with the configured delay (tolerance ±2 ms + 5 %) and the spread with the configured jitter.
- **Loss:** send N ≥ 2000 packets. The measured loss must lie within the **99.9 %** binomial confidence interval of the configured rate.
- **Flakiness policy:** a statistical assertion that fails is repeated once; only a second failure fails the test. With dozens of assertions per night, a 99 % interval alone would fail a run every few nights by chance. Measured distributions are stored per night, so drift shows up as a trend before it fails a test.
- **Rate:** iperf3 throughput within ±10 % of the limit.
- **Isolation:** every fault test also measures one unaffected device or traffic class. It must show no change.
- **Timing:** scenario steps are timestamped by the event stream and checked against the schedule, with a tolerance.
- **Where:** accuracy and timing assertions run only with native execution or KVM. Under software emulation (level 1b without KVM) the same tests run with functional assertions only (effect present, direction and isolation correct), because emulation adds tens of milliseconds of noise.

## 4.4 CI

**Available infrastructure (V1):** one VPS that is itself a QEMU/KVM guest **without nested virtualization**, so there is no `/dev/kvm` on it, and development runs in an **unprivileged** devcontainer there (D9). No Raspberry Pi, no ARM64 machine, and no KVM-capable machine yet (Q1, §7.2). The plan works with that:

- **Every commit:** levels 0 and 1b on the development VPS: the namespace testbed runs in a QEMU VM with a stock Ubuntu kernel, in software emulation (functional assertions only). One VM boots per test run, not per test (§4.5). In addition level 1 in the hosted CI of the repository where its runners allow a privileged container.
- **Nightly:** level 1b for the kernel matrix (Ubuntu 24.04 and 26.04, GA and HWE kernels; functional, runs anywhere). **Measurement tests (§4.3) and level 2 appliance VMs need KVM** and run on a KVM-capable machine as soon as one exists (Q1); until then they do not run, and the release notes say that accuracy and timing are unvalidated.
- **ARM64:** the image is built for arm64 on every commit (cross-compiled Go, `docker buildx`). Unit tests run under `qemu-user`; level 1b runs nightly with an Ubuntu ARM64 kernel in QEMU **software emulation** — functional assertions only, no timing or throughput. ARM64 measurements need hardware (H1).
- **Level 3** (hardware lab) is not available; H1 runs when hardware exists. Until then the Raspberry Pi targets in §3.10 are unvalidated, and the release notes say so.

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
| IFB | ingress shaping | tunnel faults on encrypted UDP from a WireGuard peer (M10, S15) |
| WireGuard interface | tunnel endpoint | WireGuard networks, clients and site links (M4b, M4c); remote sites are namespaces with their own WireGuard interface and BIRD |
| tap | a VM's NIC | connecting appliance VMs (level 2) |

Link events are simulated by setting one end of a veth pair down; the other end loses its carrier. Devices are namespaces with their own MAC, a DHCP client (`udhcpc`) and test tools, so "a device behind another router" or "several LANs" are just different topologies.

### Levels

| Level | Environment | What it can test | When |
|---|---|---|---|
| **0** | plain process, no root | domain, validation, compiler golden files, API contract, UI against mocked API | every commit |
| **1** | namespace testbed in a **privileged container** (CI only: the development VPS does not allow privileged containers, D9) | routing, NAT, access rules, faults (functional and measurements), DNS proxy and faults, DHCP and test actions, TLS responder and interception, capture, probes, API and UI end-to-end, scenarios | every commit in CI where privileged containers are allowed |
| **1b** | namespace testbed inside a VM with a **stock distribution kernel** (QEMU + virtme-ng); the **standard testbed on the development VPS** (unprivileged container, no KVM) | the same functional tests as level 1, with any kernel of the matrix; without KVM only functional assertions (§4.3); the decision for modules uses the same list as the product preflight (§3.4) | every commit on the development VPS; nightly for the kernel matrix |
| **2** | **appliance VMs** (QEMU/KVM) from Ubuntu 24.04 and 26.04 cloud images, gateway VM with three virtio NICs (uplink, test LAN, management) connected via tap and bridges to client/server namespaces or VMs | host setup, Docker and the compose deployment, preflight, interface assignment, coexistence with netplan, setup wizard, reboot, last-known-good, safe mode, image updates and migrations, `DOCKER-USER` handling, two- and three-port topologies | nightly on a KVM-capable machine (Q1), from M5b on |
| **3** | **hardware lab**: Raspberry Pi 4/5, x86 mini PC, real NICs, managed switch, real ESP32 devices (later a WiFi AP) | performance targets (§3.10), timing precision, NIC drivers and offloads, real firmware behavior, long-running tests | when hardware is available (not in V1 infrastructure) |

A candidate for levels 1–2 is Espressif's QEMU fork, which can run ESP32 firmware with an emulated Ethernet interface. That would allow testing real ESP-IDF firmware against the gateway without hardware; it has to be evaluated first.

### Feature → minimum level

| Feature | Level |
|---|---|
| Compiler output, precedence resolution, validation | 0 |
| Routing, NAT, access rules, connection behavior | 1 |
| Faults: function (effect, isolation, direction, live changes) | 1 |
| Faults: accuracy measurements, scenario timing | 1 or 1b on a machine with KVM (x86-64); 3 for ARM64 hardware |
| DNS proxy, DNS faults, hostname selectors | 1 |
| DHCP (Kea) and DHCP test actions | 1 |
| TLS responder, mitmproxy interception | 1 |
| Capture | 1 |
| Probes and calibration | 1 |
| VLANs, WireGuard, IPv6 | 1 (kernel modules `8021q`, `wireguard` required) |
| API, UI, scenarios end-to-end | 1 |
| Host setup, container deployment, preflight, interface ownership, netplan | 2 |
| Boot, recovery, safe mode, upgrades | 2 |
| `DOCKER-USER` handling, Docker address pools | 2 |
| Performance, timing precision on target hardware | 3 |
| Real device firmware, WiFi | 3 |

### The development container

Development runs in a Docker container on the development VPS (`.devcontainer/`). Verified in this environment:

| Requirement | Why |
|---|---|
| **Unprivileged** (default capabilities and seccomp) | Privileged containers are not allowed on the VPS: network tests could affect other containers and interfaces on the host. The namespace testbed therefore runs in QEMU (level 1b). A user namespace inside the container does not help: Docker's default seccomp profile blocks `unshare`. Each test VM has its own kernel, so nothing can reach the host's interfaces, nftables rules or other containers. |
| Stock kernels in the image (level 1b) | Ubuntu 24.04 GA `6.8.0-142-generic` and 26.04 GA `7.0.0-38-generic`, extracted from the packages without the metapackages (no firmware, microcode or initramfs). The guest boots from the container's root file system and needs `zstd` (compressed modules), `udev` (the `/dev/virtio-ports` links of `vng --exec`) and `systemd-sysv` (`poweroff`). Smoke test on both kernels: namespaces with veth, netem 50 ms (measured 51–143 ms under emulation), nftables, sysctl, WireGuard, BIRD and Kea. |
| A pseudo-terminal for `vng` | `vng` refuses to start without a valid PTY; scripts and CI wrap it in `script -qec "vng …" /dev/null` (`spikes/vm.sh` does). |
| One VM per test run | Under software emulation a VM boots in 3–9 minutes (udev settling dominates). The testbed harness (M1) boots one VM per run and executes all tests in it, with results copied out through a read-write share. |
| `--privileged` (level 1, CI only) | With only `NET_ADMIN`, `NET_RAW` and `SYS_ADMIN`, the testbed runs, but sysctls in the test namespaces cannot be set (`/proc/sys` is read-only). Forwarding then only worked because new namespaces inherit the host's IPv4 settings — on a host with `ip_forward=0` the tests would fail. |
| Host kernel with the required modules (level 1 only) | The container uses the host kernel and cannot bring its own modules. The list is the one of the product preflight (§3.4); a test preflight checks it, and if modules are missing, the affected tests run in level 1b. |
| `/dev/kvm` passed through (optional) | Makes level 1b fast and allows level 2. Without KVM, QEMU falls back to software emulation: functional tests still work, but timing measurements do not (baseline 2.4 ms instead of 0.3 ms, `nft` commands 0.5 s instead of 6 ms). |
| Unique namespace prefix per test run | Parallel runs with the same names destroy each other's topology. |
| Test tools in the image | iproute2, nftables, conntrack-tools, tcpdump, tshark, iperf3, dnsutils, busybox (`udhcpc`), ethtool, socat, Kea, BIRD 2, wireguard-tools, QEMU + virtme-ng (level 1b, incl. `qemu-system-aarch64`), Go toolchain, Node.js (frontend build and Playwright), Python 3, mitmproxy (sidecar tests). |

### Reference development setup

An Ubuntu VPS (itself a QEMU/KVM guest, kernel 6.8, no nested virtualization) with the unprivileged devcontainer on it (`.devcontainer/start.sh`). The VPS provides the target kernel, but the container may not use it for namespace tests (level 1b instead).

| Setting | Why |
|---|---|
| 4+ vCPUs, 8+ GB RAM, 40+ GB disk | the emulated test VMs, Kea, mitmproxy, Go and Node toolchains and captures in parallel |
| No `/dev/kvm` | nested virtualization is not offered; level 1b runs in software emulation |

**Limits of this setup:** measurement tests (§4.3), level 2 and timing precision need a KVM-capable machine (Q1, §7.2); real devices (e.g. an ESP32 behind the gateway) cannot be attached to the VPS (level 3).

Further notes:

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
| S6 DHCP | Kea vs. dnsmasq for runtime test actions | ✅ Kea | Kea version pinned in the container image → M6a |
| S7 Deployment | Docker coexistence, gateway in a container | ✅ (now the production deployment, §3.8) | netplan coexistence, compose deployment → M5b, M28 |
| S8 Hardware | cost of classification and updates; throughput | ◐ x86 only | Raspberry Pi, 500 HTB classes → H1 |
| S9 Capture | exact per-selector capture despite NAT | ✅ AF_PACKET + NFLOG | — |
| S10 Classification | mark layout, per-packet vs. per-connection classification | ✅ per packet | — |
| S11 Direction | direction bit with two test networks; device+destination lookup; re-apply that keeps dynamic sets and counters | ✅ (without the direction bit: 43 ms instead of 120 ms) | — |
| S12 Attachment | probe on bridge vs. macvlan; tc on the bridge; policy routing vs. management default route | ✅ bridge; macvlan probe cannot reach the gateway | — |
| S13 PMTUD | path-MTU faults with ICMP and as black hole | ✅ (side effect on shared NAT address) | — |
| S14 Local replies | download faults for connections that end on the gateway | ✅ with output hook (superseded for services by S16) | — |
| S15 WireGuard & routing | (1) inner faults: classification of client/remote-network traffic and netem on a WireGuard interface's egress; (2) tunnel faults: marking WireGuard's own encrypted UDP in the output hook per peer endpoint, netem on the uplink; (3) BIRD (BGP and OSPF) over a WireGuard link between two sites, exporting only into the Chaos Gateway table, import filter rejects default and management prefixes; (4) re-convergence time when a tunnel fault blacks out the link; (5) hub interface with client networks via `AllowedIPs` | ✅ all 16 checks (12-bit id layout included) | — |
| S16 Service namespace | upload faults for connections redirected to gateway services: flower filters repeating the redirect selectors vs. a service namespace behind a veth pair (`SO_ORIGINAL_DST` inside, both directions through normal egress); works with the container deployment? | ✅ service namespace (flower misses set-based redirects; Docker holder-container pattern works; fail-closed fallback needed) | — |

## Milestone overview and MVP

Sizes: **S** ≈ up to 1 week, **M** ≈ 1–2 weeks, **L** ≈ 2–4 weeks for one experienced developer.

| Phase | Milestones | Result |
|---|---|---|
| 1 Foundation | M1 (M), M2 (M), M3 (M), M4 (L), M4b (L), M4c (L), M5 (L), M5b (M), M6a (M), M6b (M) | API-configurable routed gateway with DHCP, DNS, discovery, WireGuard networks and dynamic routing |
| 2 Faults | M7 (M), M8a (M), M8b (M), M9 (M), M10 (M), M11 (S); H1 (S, optional, when hardware exists) | faults, rules, profiles via API, measured on x86 |
| 3 Web UI | M12 (L), M13 (L), M14 (M) | interactive use |
| 4 Automation | M15 (L), M16 (M), M17 (M), M18 (S), M19 (M) | scenarios, checks, capture, CLI |
| 5 Application layer | M20 (M), M21 (M), M22 (L), M23 (S) | DNS, TLS, DHCP test actions |
| 6 Diagnostics | M24 (M), M25 (S), M26 (S) | diagnostics, probes, metrics |
| 7 Production | M27 (M), M28 (M), M29 (M), M30 (S) | recovery, container deployment, hardening, release |

**MVP (automation first):** Phases 1 and 2 (without H1) plus M15, M16 and M18 — faults, rules and profiles, scenarios with checks and JUnit reports, driven by the CLI and the API. This already serves the original use case (automated IoT tests in CI). The web UI (Phase 3) follows; V1 is defined at the end of this section.

## Phase 1 — Foundation: Routed Gateway

**M1 — Repository, CI and testbed library** (M)
- Scope: Go module and Vue app skeleton, code generation from the existing `api/openapi.yaml` (oapi-codegen for types and Gin server interfaces, Orval for Vue Query hooks and Zod schemas, openapi-python-client) with a CI check that the spec lints, the examples validate and the generated code compiles, Makefile, golangci-lint, `go test`, Vitest, Playwright skeleton, CI (levels 0 and 1b on the development VPS; a level 1 job in hosted CI where privileged containers are allowed), `internal/testbed` from spike S1 (with two test networks and a bridge-based attachment as default topology) and its **level 1b runner**: one QEMU VM per test run, all tests of the run execute in it, results come back through a read-write share, works without a terminal (`script`), the devcontainer image with all test tools and the stock kernels (`.devcontainer/`, exists), the injectable test clock, the arm64 image build, the decision where KVM-dependent tests run (Q1), and the shared kernel-module preflight (level 1; inside the VM on level 1b).
- Tests: CI runs a testbed test in a level 1b VM (client pings server through a plain forwarding namespace; a netem delay is visible); the runner returns the tests' exit code and results; the same test runs in a privileged container where CI allows it.
- Depends on: S1.

**M2 — Domain model and persistence** (M)
- Scope: the domain model from the generated types of `api/openapi.yaml` (uplink selection, networks incl. WireGuard, device incl. `trusts_test_ca`, group, access rule, fault with family, profile, scenario, overlay kinds with owner, key, TTL and lease, §2.1.1); strict decoding; the validation rules the schema cannot express ("exactly one of", references exist, names unique in their namespace, subnets do not overlap, jitter ≤ latency, step ids unique, …); observed-state model; validation incl. jitter ≤ delay; precedence resolution per family with overlays before configuration (§2.4); atomic file persistence; revisions with diff; schema version.
- Tests: unit tests for validation; golden tests for every precedence row and for the worked examples of §2.4 that need no compiler output yet (E1–E8, E12 as domain resolution); revision round-trip; corrupted-file handling; every file in `api/examples/` decodes and validates, and a set of invalid documents is rejected with the expected JSON pointer and error code.
- Depends on: M1.

**M3 — Executor and state reader** (M)
- Scope: privileged executor with typed operations, argument-array command invocation, namespace targeting, parsers for `ip -j`, `nft -j`, `tc -j`; Unix-socket protocol with version handshake and `SO_PEERCRED` check; scope validation (nftables only `inet chaosgw`, routing only own tables and rules, tc only assigned interfaces and the uplink qdisc, the single `DOCKER-USER` operation); serialized operation queue (full applies and incremental identity updates); container hardening profile.
- Tests: unit tests for command building and parsing (recorded outputs); integration test reads interfaces/routes of the gateway namespace; operations outside the allowed scope are rejected; fuzz tests on the operation decoder (5 min per CI run, longer nightly); version mismatch is refused.
- Depends on: M1.

**M4 — Compiler v1, preview, safe apply: routed gateway** (L)
- Scope: uplink selection (OS-configured; address and gateway read and followed via netlink), test networks as bridges with physical ports, policy routing table 100 with its full contents (§2.2), forwarding, masquerade per network, gateway protection (input policy, §2.2), access matrix default, IPv6 blocked and RA acceptance off, offloads off (§3.4); the `DOCKER-USER` accept rule for Chaos Gateway's interfaces (§3.4); nftables layout that keeps dynamic sets and counters, deletes removed objects and hashes set definitions (§3.2); generation chain and verify (§2.14); observed state as compiler input; target state, diff, preview, apply, rollback on failure; commit-confirm (configurable timeout) and anti-lockout for the management network; interface assignment by MAC and name; `chaosgw apply --file` to apply a configuration without the API (bootstrap for tests and M5b).
- Tests: golden tests; integration — client reaches server through the gateway; a test-network client reaches ICMP and test listeners on UDP 67 and 53 of the gateway, but not a listener on the UI/API port or SSH; a management default route in the main table does not attract test traffic; preview matches applied state; verify detects a manipulated element; an injected executor failure leaves the previous state active; an unconfirmed lockout-relevant change rolls back after the timeout (fake clock); an apply that changes a set's definition succeeds (hashed names) and a removed rule's chain is gone; a changed uplink address keeps NAT working and emits an event.
- Depends on: M2, M3.

**M4b — WireGuard networks and clients** (L)
- Why here: WireGuard networks are a first-class network type from the start, so every later feature (faults, rules, DNS, TLS, capture, scenarios, UI) is built and tested against WireGuard interfaces as well as local test networks.
- Scope: hub and link networks (§2.2.1) as network type `wireguard` in the domain model, compiler and executor (`wgctrl`); clients with client networks and reachable-network selection; static routes for client networks, links and downstream routers (§2.2.2); policy-routing rules for WireGuard interfaces; routed without NAT towards test networks, masqueraded towards the uplink; key generation, optional preshared keys, "export once"; export as `.conf`, QR (PNG/SVG), zip; client status (handshake, endpoint, bytes) and online/offline events; WireGuard clients as configured devices, hosts in client networks as IP-identified devices (§2.3); role *management* for admin remote access; MSS clamp and MTU 1420 on WireGuard interfaces; the testbed default topology gains a hub client with a client network and a link site (§4.2).
- Tests (level 1): a device in a local test network reaches a host in a client network without NAT, and the reverse only if the access matrix allows it; a link with static routes carries traffic between the gateway's test network and the remote site; the exported `.conf` brings up a working tunnel in a fresh namespace (as in S15), and the decoded QR equals the file; disabling a client stops its handshake and emits the event; a *test* role client cannot reach a test listener on the UI/API port, a *management* role client can; private keys never appear in configuration exports or logs; re-applying the configuration does not interrupt an established tunnel.
- Depends on: M4, S15. (API resources follow in M5, UI in M14.)

**M4c — Dynamic routing (BIRD)** (L)
- Why here: WireGuard links between sites depend on dynamic routing; like WireGuard itself it is part of the foundation, so later features are tested with learned routes too.
- Scope: BIRD 2 instance managed by Chaos Gateway (§2.2.2): BGP, OSPFv2, Babel, static; router id, ASN, neighbors per link, areas and timers in the model; announced prefixes from the model; import filters (prefix lists, no default unless allowed, protected prefixes, max prefixes); export only into Chaos Gateway's routing tables; `bird -p` validation and `birdc configure` apply as part of the revision; custom snippets; external mode for another daemon's kernel table; neighbor and route status and events; remote-side BIRD snippet in link exports. (API resources in M5, routing view in the UI with M14.)
- Tests (level 1, as in S15): three sites (gateway plus two remote namespaces with BIRD) over WireGuard links, with BGP and with OSPF; routes are learned and withdrawn; learned routes appear only in Chaos Gateway's tables, never in main; a neighbor announcing a default route, the management prefix or a gateway-own prefix is filtered; max-prefix triggers; taking a link down withdraws its routes within the hold time and they return after it comes back; a configuration change is applied without resetting established sessions; an invalid custom snippet is rejected in preview with BIRD's error message.
- Depends on: M4b, S15.

**M5 — REST API v1** (L)
- Scope: API conventions of §2.15 incl. the normative details table (problem+json, UUID + name, pagination, ETag/If-Match, JSON Merge Patch, idempotency keys, SSE with ids and replay); generation (`state`, SSE `applied`) and `capabilities`; candidate-revision model with preview, apply, confirm, `409 revision_conflict` and `409 confirm_pending` (§2.1.1); sessions with CSRF, hashed API tokens with scopes, first-start setup token, admin password reset (§2.16); UI/API bound to the management network after setup; audit log.
- Tests: API contract tests against the spec; generated clients compile; E2E through the testbed (configure via API → traffic flows, including creating a WireGuard client and downloading its configuration); a conflicting candidate is rejected with `revision_conflict`; a second apply during a confirmation window gets `confirm_pending`; SSE reconnect with `Last-Event-ID` gets the missed events.
- Depends on: M4, M4b, M4c.

**M5b — Appliance VM harness (test level 2)** (M)
- Scope: download Ubuntu 24.04 and 26.04 cloud images; boot a gateway VM with three virtio NICs (uplink, test network, management) connected via tap and bridges to client and server namespaces; run a first version of the host-setup script (modules, `ip_forward`) and a minimal compose deployment (executor container plus `chaosgw apply --file`); collect logs. Needs KVM: runs nightly on a KVM-capable machine (Q1), not on the development VPS.
- Tests: a smoke test boots Ubuntu 24.04 and 26.04, starts the current image and passes traffic from a client namespace through the VM; the same in the two-port topology.
- Depends on: M4.

**M6a — DHCP and device discovery** (M)
- Scope: Kea container (pinned version) with one subnet per network, pools and reservations via `config-set`, DHCP on/off per network; lease events via Kea's `run_script` hook; flow observer on conntrack events and flows API; device discovery from leases, neighbor table, conntrack and WireGuard clients; identity events into the observed state (§2.3); manual device merge; devices API.
- Tests: client namespace gets a lease; device appears with MAC/IP; reservation honored; DHCP off on one network leaves it silent; discovered vs. configured devices; an address change emits an identity event within 1 s; flows of a device are listed.
- Depends on: M5.

**M6b — DNS proxy** (M)
- Scope: service namespace (holder container `svcns`, `svc0` veth pair, table 102 with `prohibit` fallback, re-attach on holder change, §3.3); DNS proxy container in it, answering queries to each test network's and WireGuard network's gateway address (DNAT into the service namespace), UDP and TCP, forwarding, caching, query log (`/dns/queries`), AAAA removal; registration with the API at start (internal API, `/internal/dns/config`); coexistence with systemd-resolved; upstream resolver from the host configuration.
- Tests: clients in a local test network and a WireGuard client (DNS = gateway tunnel address) resolve names over UDP and TCP; the proxy does not bind 127.0.0.53 and resolved keeps working; restarting only the DNS container restores its state from the API; restarting the holder is healed by re-attach and service restart; without a service namespace, selected traffic is refused (fail closed).
- Depends on: M5.

*After Phase 1: a working, API-configurable test gateway with local and WireGuard networks and dynamic routing, without faults.*

## Phase 2 — Faults (core value)

**M7 — Classification layer** (M)
- Scope: the lookup chain of §3.3 (device + destination + port … any + destination … global, protocol-only maps, splitting of overlapping selectors) on prerouting, only for test traffic, direction bit, identity updates as incremental map operations from the observed state. No IFB and no output-hook classification here: gateway services are reached through `svc0` egress (D29, S16); the output hook and IFB are only used for tunnel faults (M10).
- Tests: with a test tc class per (id, direction), per-class counters increase only for matching traffic, in both directions, behind NAT, across two test networks, for a host in a WireGuard client network (as initiator and as destination), and over a WireGuard link (connections redirected to gateway services are tested with the first redirect in M20/M21); a map change moves an established connection to its new class (observed via the class counters); after a forced address change the device's map entry follows within 1 s and a concurrent full apply does not restore the old address; non-test traffic keeps its mark untouched; golden test of the id masks (direction bit kept).
- Depends on: M6a, M6b, S2, S10, S11, S16.

**M8a — Overlays** (M)
- Scope: overlay store with owner, key, TTL, lease and renew; overlay kinds whose milestone is not done yet (rule before M9, DNS before M20, TLS before M21, DHCP before M23) are rejected with `unsupported_feature`; `POST /api/v1/reset` (own vs. all); per-family precedence into winning faults (§2.4); stable ids; compiler output for tc (per id and direction, complete parameter sets, computed queue limits); named per-fault counters; `explain` endpoint.
- Tests: golden tests for precedence (E1–E8, E12; E9 and E10 follow in M10, E11 in M21) and tc output; TTL and lease expiry remove overlays and emit events (fake clock); writing an overlay with an existing key replaces it and keeps the id; `reset` only touches the caller's overlays; a restart drops overlays; `explain` returns the expected winner per family.
- Depends on: M7.

**M8b — Fault engine: latency, jitter, loss** (M)
- Scope: apply the tc tree per interface with in-place parameter changes and make-before-break for id changes (§3.2); overlay writes return after verify with the new generation; netem queue statistics with counter epochs.
- Tests: measurement tests (§4.3) for device, group and network scope, for traffic between two test networks, between a test network and a WireGuard client network, and over a route learned via BGP; isolation test; updating one fault does not disturb others; changing the parameters of a 600 ms fault under load loses no queued packet.
- Depends on: M8a.

**M9 — Access rules** (M)
- Scope: ordered allow/drop/reject/TCP reset rules in configuration and as overlays, evaluated in forward and input on the conntrack original tuple (§2.2); "also cut existing connections"; precedence rules vs. faults; named per-rule counters; preview and `explain` of the effective result.
- Tests: behavior matrix from S3 as automated tests; rule order; overlay rules before configuration rules; anti-lockout rule cannot be overridden; a drop rule on UDP 53 blocks the device's queries to the DNS proxy.
- Depends on: M8a, S3.

**M10 — Extended faults** (M)
- Scope: **tunnel faults** on WireGuard clients and links (§2.2.1: latency, loss, blackout, flapping of the encrypted UDP; output hook towards the peer, IFB with flower on the outer UDP from the peer); **WireGuard-action overlays** (peer or link disable, key mismatch, endpoint blocking); rate and queue limit per device (D18), reorder, duplicate, corrupt, burst loss (Gilbert-Elliott), blackout (netem loss 100 %), flapping, MTU/PMTUD with the three modes of §2.5.
- Tests: one measurement test per fault type on the kernels of the distribution matrix; flapping timing within tolerance; PMTUD: a 300 KB TCP transfer completes with ICMP mode and stalls in black-hole mode, the control device is unaffected (as in S13); MSS clamp limits segment size of the selected device only; a tunnel fault affects everything inside that tunnel and nothing else, and stacks with inner faults (as in S15); a tunnel blackout on a BGP link withdraws the learned routes and the re-convergence time is reported; PMTU faults through a tunnel; golden tests E9 and E10; per-device rate (D18): a 2 Mbit/s fault on a network gives two devices transferring at the same time 2 Mbit/s each (±10 %); exceeding the class limit returns `capacity_exceeded` in preview.
- Depends on: M8b, M9.

**M11 — Profiles** (S)
- Scope: built-in (except DNS/TLS profiles, which arrive with M20/M21) and custom profiles, activation on scopes via overlays, precedence with individual faults.
- Tests: activating/switching profiles yields the configured parameters (compiler) and measured values (integration); a device fault overrides the network profile's impairment part (same layer); a fault on the same scope replaces only the profile part of its family, other families stay active; an overlay profile beats a configuration fault.
- Depends on: M10.

**H1 — Hardware validation** (S, **when hardware is available**; not required for the V1 release)
- Scope: run the measurement suite and the S8 performance scripts on a Raspberry Pi 4/5 and an x86 mini PC with real NICs: throughput with 50 and 250 faults (500 HTB classes), cost of disabled offloads, loss accuracy on a real NIC, DNS proxy queries per second, fault accuracy; after M15 also scenario step timing on ARM64.
- Tests: results recorded; §3.10 targets and the ARM64 timing tolerance confirmed or adjusted. Until H1 has run, the Raspberry Pi targets are published as unvalidated.
- Depends on: M11 (step timing: M15); hardware.

*After Phase 2: the core product via API — faults, rules, profiles — measured on x86 (accuracy measurements need a KVM-capable machine, Q1).*

## Phase 3 — Web UI

**M12 — UI shell, overview, devices** (L)
- Scope: visual system and shared components (§2.17), login and first-start setup wizard (select uplink and management, assign test interfaces, admin password, netplan hints from the preflight), layout, overview, device list and device detail with the data available after Phase 2 (identity, lease, active faults, counters); live updates via SSE; empty, offline and safe-mode states. Panels whose backend arrives later (captures, diagnostics, DHCP actions, TLS tests) appear with those milestones.
- Tests: Playwright against a mocked API for every state (empty, many devices, offline, safe mode); an E2E flow in the testbed: first start with setup token → assign interfaces → see a discovered device.
- Depends on: M11.

**M13 — UI for faults, rules and profiles** (L)
- Scope: add/edit fault dialog with preview, access-rules screen (ordered list, rule editor IF/THEN, counters) and faults screen (by family and scope, "overridden by", explain), unapplied-changes bar, preview-and-apply drawer, concurrent-change dialog (reload and reapply), profile cards, TTL display, `</> API` panel.
- Tests: Playwright — create a fault in the UI, then verify the measured effect in the testbed; validation errors are shown; a conflicting change made through the API triggers the conflict dialog; the faults screen marks an overridden fault.
- Depends on: M12.

**M14 — Networks view and technical view** (M)
- Scope: network cards and detail, DHCP pool/leases, WireGuard networks with client list, client status, add-client dialog, QR dialog and config download, routing view (BIRD sessions, received and announced prefixes), access matrix editing with commit-confirm, compiled-state view (nftables, tc, routes, WireGuard).
- Tests: Playwright; commit-confirm — an unconfirmed change is rolled back after the timeout and the UI shows it; create a WireGuard client in the UI, scan its QR in the test (decoded content equals the download) and bring the tunnel up in the testbed.
- Depends on: M13.

*After Phase 3: usable interactively. First release candidate for internal use.*

## Phase 4 — Automation

**M15 — Scenario engine and runs** (L)
- Scope: scenarios as defined in §2.10 (step semantics, remove/restore, narrowing targets), inline scenarios with parameters in `POST /api/v1/runs`, preconditions, scheduler on the injectable monotonic clock, step types profile, fault, rule, WireGuard action, wait, remove, restore (further step types arrive with M17, M20–M23); runs with lifecycle, owner, one run per target, queue, explicit abort, optional lease, scenario snapshot and generation per step; JSON/JUnit report.
- Tests: step order and semantics with the fake clock; step timing within ±100 ms on a machine with KVM; abort removes the run's overlays; a disconnecting client does not stop a run, an expired lease does; a restart ends a running run as `aborted`; a second run on the same target waits in `queued`; a failed precondition ends the run as `error`; an inline scenario runs without changing the active revision; the report contains all steps.
- Depends on: M11.

**M16 — Checks** (M)
- Scope: observation-based checks with windows relative to named steps (connection established within t, no connection accepted, traffic to destination seen/not seen, DNS query seen); zero-hit warnings for faults and rules of the run (optionally errors).
- Tests: checks pass and fail correctly with scripted client behavior in the testbed; a scenario whose fault never matches reports the zero-hit warning.
- Depends on: M15.

**M17 — Capture** (M)
- Scope: capture by network (incl. WireGuard interfaces, raw IP link type), device or selector (AF_PACKET per S9); ring buffer, quotas, retention, disk-low behavior; download (also while running); capture step type; attachment to runs.
- Tests: the capture contains exactly the selected traffic; quota enforcement; a run with capture attaches it to the report; low disk stops captures but not the run.
- Depends on: M7, M15, S9.

**M18 — CLI and clients** (S)
- Scope: `chaosctl` in Go for CI: apply profile, set fault with TTL, `run -f <file> --set …`, `--wait`, `--junit <file>`, `--artifacts <dir>`, exit codes 0 passed / 1 failed / 2 error or aborted, automatic lease renewal; `chaosctl wait device <name> --online [--connected tcp/8883] --timeout …`; `chaosctl with --profile … --ttl … -- <command>` (applies, runs the command, always cleans up); generated TypeScript and Python clients; examples for pytest and Jest.
- Tests: CLI tests against the testbed incl. exit codes and cleanup of `chaosctl with` when the command fails; the example test suites run in CI.
- Depends on: M15.

**M19 — Scenario UI** (M)
- Scope: scenario list, timeline editor, YAML view, run view with live progress, results and artifacts.
- Tests: Playwright — build and run a scenario, then check result and artifact display.
- Depends on: M13, M16, M17.

*After Phase 4: L3/L4 testing and automation complete; the V1 scope is listed at the end of this section.*

## Phase 5 — Application Layer

**M20 — DNS faults and hostname selectors** (M)
- Scope: NXDOMAIN, SERVFAIL, timeout, delay, wrong answer, truncation (with TCP fallback), short TTL, per device/group/pattern; DNS-derived address sets with the lifetime rule of §2.6; redirect of hardcoded DNS; DoT blocking; hostname selectors for rules and faults; "DNS broken" profile; DNS scenario step type.
- Tests: `dig` from clients shows each fault; a hostname-selector fault affects only traffic to the resolved IPs; a long-lived connection keeps its hostname fault past a 1 s TTL; the set survives 10 overlay changes; hardcoded DNS is redirected; download and upload latency apply to queries of a device (service namespace, S16).
- Depends on: M8b, M9, M15, S5, S16.

**M21 — TLS responder: certificate cases** (M)
- Scope: TLS responder in the core, test CA (with download, `GET /tls/ca`) and never-distributed unknown CA, transparent redirect of selected traffic (new connections; "cut existing" on activation, reset on removal), the TLS cases of §2.8 as confirmed by S4, no-SNI fallback, expected results from `trusts_test_ca`, events per handshake, check type "TLS rejected/accepted", "TLS broken" profile, TLS scenario step type.
- Tests: `openssl s_client`/`curl` with TLS 1.2 and 1.3 — untrusted/expired/wrong-host/self-signed are rejected by a correct client; a deliberately insecure client is flagged by the check; golden test E11; a client trusting the test CA passes the "untrusted CA" case; upload and download latency apply to connections to the responder (service namespace); the responder reads the original destination; the same cases work for a WireGuard client.
- Depends on: M9, M16, S4, S16.

**M22 — TLS interception and HTTP faults** (L)
- Scope: mitmproxy sidecar management and control from the core, test CA management (generate, download for dev firmware), HTTP(S)/WebSocket inspection, URL blocking, error codes, delay/throttle, modification, connection abort, key log for captures; block-UDP-443 option; interception scenario step type.
- Tests: a client trusting the CA sees the modified responses; timing of delayed responses; QUIC fallback; connection reuse across requests; the key log decrypts the run's capture.
- Depends on: M17, M21.

**M23 — DHCP test actions** (S)
- Scope: short leases, lease deletion, forced new IP via reservation change (NAK), option changes, silence; DHCP scenario step type.
- Tests: `udhcpc` in the testbed observes each behavior; a device fault stays attached after the forced new IP.
- Depends on: M6a, M15.

## Phase 6 — Diagnostics and Observability

**M24 — Diagnostics** (M)
- Scope: ping, TCP/UDP check, DNS, HTTP(S), TLS details from the gateway; structured results; API and UI panel. (Traceroute, path MTU and iperf3 after V1.)
- Tests: each diagnostic against known testbed targets, including failure cases.
- Depends on: M6b, M12.

**M25 — Probes and calibration** (S)
- Scope: virtual clients as bridge ports of test networks (S12); DHCP address; targeting by rules/faults; diagnostics of M24 run from a probe; self-test "measure this profile".
- Tests: a probe reaches the gateway address, devices and the server; its measurement matches the configured profile within the §4.3 tolerances.
- Depends on: M8b, M24.

**M26 — Metrics and flow view** (S)
- Scope: Prometheus metrics endpoint (counters from M7–M9, interface and queue statistics, system health); flow view in the UI (flow data from M6a).
- Tests: metric values against known traffic; Playwright for the flow view.
- Depends on: M9, M13.

## Phase 7 — Production Readiness

**M27 — Recovery** (M)
- Scope: last-known-good at start, safe mode, recompile after interrupted apply, verify at start (§2.14), overlay removal on stop, `chaosgw teardown`, degraded networks on missing interfaces.
- Tests: a broken revision at start leads to last-known-good (level 2); killing the executor mid-apply is recovered; stopping the containers leaves no fault active; unplugging a test interface (link removal in the testbed) marks its network degraded without safe mode.
- Depends on: M5b, M8b.

**M28 — Container deployment** (M)
- Scope: multi-arch image (amd64/arm64), `compose.yaml` with the containers and privileges of §3.8, health checks and start order, final host-setup script (modules, `ip_forward`, netplan hints), preflight in the executor, setup token in the container log, update script that waits for `system/busy`, refusal of newer schemas, teardown.
- Tests: level-2 smoke tests on Ubuntu 24.04 and 26.04 hosts (two- and three-port topologies): host setup, `compose up`, setup wizard, reboot, image update during an idle and during an active run; the arm64 image starts and passes the functional smoke test in emulated level 1b; the non-exec containers run without privileges (checked from the container runtime).
- Depends on: M5b, M14, M27, S7.

**M29 — Security hardening** (M)
- Scope: HTTPS with replaceable certificate (`/system/certificate`), token scopes, secret storage, secret-free exports, rate limiting on login, container hardening review (capabilities, read-only root file systems), external review of the executor interface.
- Tests: test networks and *test*-role WireGuard clients cannot reach the UI/API (regression of M4); tokens with insufficient scope are rejected; exports contain no secrets; long fuzz runs on executor operations nightly.
- Depends on: M5, M28.

**M30 — Documentation and V1 release** (S)
- Scope: user guide, API guide with examples, scenario cookbook, troubleshooting (preflight messages).
- Tests: documentation examples run in CI.
- Depends on: all milestones included in V1.

## After V1

| Milestone | Content |
|---|---|
| M31 VLANs | multiple networks on a trunk (VLAN ports on the network bridges), access matrix between them |
| M32 IPv6 | RA/SLAAC, DHCPv6, IPv6 rules and faults (lifts the V1 IPv6 block); IPv6 inside WireGuard tunnels and as underlay; IPv6 in BIRD |
| M34 NTP time faults | see §8 |
| M35 Port forwarding | DNAT for inbound test traffic; matching a device as responder |
| M36 Further proposals | features from §8 by priority |
| M38 Deferred from V1 | three-way merge of concurrent revisions; rule captures via NFLOG and live streaming to Wireshark; traceroute, path MTU and iperf3 diagnostics; continuous drift detection with reconcile; native .deb package |
| M39 Persistent faults for all families | persistent (configuration) DNS faults, TLS cases and DHCP states, and persistent profile activations (`profile_activations`); "permanent" as the default in the add-fault dialog; a run precondition that warns about persistent faults on its target (D31) |

## V1 Scope Summary

V1 = Phases 1–4 (incl. M4b WireGuard, M4c dynamic routing and M5b), plus M20, M21, M24, M25, M27, M28, M29, M30 — deployed as containers on Ubuntu 24.04/26.04, x86-64 and ARM64.

M22 (interception), M23 (DHCP actions) and M26 (metrics and flow view) are optional for V1. H1 runs when hardware is available; V1 can be released without it, with the ARM64 performance targets marked as unvalidated. No further features from §8 are in V1 (D27).

---

# 6. Known Limitations and Technical Risks

| # | Topic | Consequence | Handling |
|---|---|---|---|
| 1 | tc acts on egress only | upload and download faults must be applied on different interfaces; traffic terminating at the gateway has no egress in upload direction | classification by marks (§3.3); gateway services in a service namespace, so they are reached through an egress (S16); IFB only for tunnel faults from a WireGuard peer (S15) |
| 2 | Uplink egress is after NAT | device IP is invisible there | classification per packet by the conntrack original tuple (`ct original ip saddr`), which is unchanged by NAT (S10, S11) |
| 3 | netem loss on locally generated traffic may be reported to the local TCP stack | loss on gateway-originated traffic (e.g. proxy → server) could be unrealistic | not observed on Ubuntu 6.8 with netem under HTB (S2); kept in the test matrix |
| 4 | netem jitter reorders packets by default | 712 of 1000 packets reordered (S2 F1) | default documented; "keep order" option via netem rate, which shifts the delay distribution |
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
| 16 | Docker on the same host (always, since Chaos Gateway runs in it) | FORWARD policy DROP, read-only sysctls in unprivileged containers, Docker's own address pools and rules | executor keeps the `DOCKER-USER` accept rule; privileged executor container; preflight checks address-pool overlaps (§3.8) |
| 17 | Kernel module availability | netem missing on minimal kernels | preflight check with instructions; on Ubuntu generic all modules are in `linux-modules` |
| 18 | Timing precision on loaded small hardware | scenario tolerance may be exceeded | x86 measurements with KVM; ARM64 tolerance set by H1 when hardware exists (risk 33); tolerance documented per platform |
| 19 | mitmproxy throughput and resource use on small hardware | TLS proxy limits throughput | only selected traffic is proxied; targets in §3.10 |
| 20 | Management lockout through rules | admin loses access | anti-lockout rule for the control plane only, commit-confirm, management interface and uplink addressing stay with the OS; global faults never touch the gateway's own traffic (§2.4) |
| 21 | Per-packet classification cost | many selectors evaluated per packet could cost throughput on small hardware | nftables maps/sets; measured in S10/S8; fallback: conntrack-mark caching plus explicit re-classification when faults change |
| 22 | Kernel differences in netem | behavior and allowed combinations of netem (e.g. duplication in nested trees) differ between kernel versions | test matrix over the supported distributions (§4.4) |
| 23 | PMTU faults leak through the shared NAT address | with ICMP mode the server caches the reduced path MTU for the gateway's uplink address, so other devices talking to the same server are affected for up to 10 min (S13) | UI warning; MSS clamp mode as isolated alternative for TCP; documented cache flush on test servers |
| 24 | Hostname-derived sets age | addresses learned from DNS expire with the DNS TTL; a device that caches longer than the TTL or uses a hardcoded IP escapes the rule | set element timeout = max(TTL, configurable minimum); "unmatched hostname" events; documented as best effort |
| 25 | Distribution matrix grows | each supported release adds kernel and netem differences and a nightly job | only Ubuntu 24.04 and 26.04 hosts (D1); Kea, BIRD and tools are pinned in the image, so only the host kernel varies |
| 26 | Connections that end at gateway services | at tc ingress a redirected packet still has the original destination; replies of local services never pass prerouting | services run in a service namespace, so both directions pass a normal egress (S16); the output hook remains only for the WireGuard underlay |
| 27 | WireGuard cryptokey routing vs. dynamic routes | on a shared hub interface every prefix is bound to one peer; learned routes cannot be expressed there | dynamic routing only on point-to-point links (§2.2.1); hub clients use declared client networks |
| 28 | Route injection by remote sites | a remote BIRD could announce the default route or management prefixes and divert traffic | learned routes only in Chaos Gateway's tables; import filters, protected prefixes, max prefixes (§2.2.2) |
| 29 | Tunnel MTU | WireGuard overhead (60–80 bytes) plus PMTU faults can black-hole traffic inside tunnels | MTU 1420 default, MSS clamp on WireGuard interfaces, PMTU tests through tunnels in M10 |
| 30 | Per-device queues multiply classes | a rate-limited profile on a network of N devices creates N classes per direction (D18); many devices on small hardware | id space and class count limits enforced by the compiler (`capacity_exceeded`); measured in H1 |
| 31 | Gateway behind an IPv4-less uplink (DS-Lite, CGNAT) | hub clients cannot reach the gateway's WireGuard endpoint over IPv4 in V1 | gateway initiates links towards reachable sites; port forwarding upstream; IPv6 underlay with M32 (D22) |
| 32 | Privileged executor container | a compromised executor has root on the host network stack | only the executor is privileged; it accepts typed operations over a Unix socket with peer checks and scope validation; the network-facing API runs without any capability (§2.16) |
| 33 | No hardware and no ARM64 machine in V1 | ARM64 timing and throughput, real NIC behavior and offload costs are not measured | functional ARM64 tests under emulation; x86 measurements with KVM; H1 when hardware exists; release notes mark unvalidated targets |
| 34 | NIC offloads | with GRO/GSO/TSO, netem acts on 64 KB aggregates, so loss and duplication are far off | offloads switched off on owned interfaces and the uplink (§3.4); cost measured in H1 |
| 35 | Service namespace missing | redirected traffic could silently reach the real server (fail open), which would pass a TLS test that should fail | `prohibit` fallback in table 102 (S16 C4); executor re-attaches on holder restart; services exit when their namespace is stale |

---

# 7. Decisions

## 7.1 Decided

| # | Decision | Result | Basis |
|---|---|---|---|
| D1 | Supported platforms | **Ubuntu 24.04 LTS and 26.04 LTS hosts, x86-64 and ARM64**; minimum kernel 6.8; no Debian, no Raspberry Pi OS in V1 | maintainer |
| D2 | Deployment | **Docker containers** (compose, host network, only the executor privileged, §3.8); native package after V1 | maintainer |
| D3 | Host ownership | Chaos Gateway owns the test interfaces; **the OS keeps the uplink and management addressing** (netplan); Chaos Gateway selects and uses the uplink | maintainer |
| D4 | DHCP server | **Kea** — short leases, lease API, `run_script` hook; dnsmasq's minimum lease is 120 s | S6 |
| D5 | TLS components | certificate and handshake cases in a TLS responder in the core; interception via a mitmproxy sidecar; the "no Python" rule applies to the core only. Confirmed with a Node prototype in S4; implemented in Go per D15 | S4 |
| D6 | Uplink types | whatever the OS configures (static or DHCP tested; others such as PPPoE untested) | follows from D3 |
| D7 | IP versions in V1 | IPv4-only test networks (§2.2); IPv6 blocked on test networks until dual-stack in M32 | review |
| D8 | L2 transparent mode (gateway as a bridge **between** device and upstream router, not routing) | **later** (§8); only needed when the gateway cannot be the device's default router. Unrelated to the bridge that joins the ports of one test network (D16) | maintainer |
| D9 | Test infrastructure and hardware | development on one VPS (QEMU/KVM guest without nested virtualization) in an **unprivileged** devcontainer, because privileged containers could affect other containers and interfaces on the host; the namespace testbed runs in QEMU (level 1b, software emulation, functional only); KVM-dependent tests (measurements, level 2) need a KVM-capable machine (Q1); ARM64 emulated; hardware validation (H1) when hardware exists (updated 2026-10-02; replaces the VirtualBox development VM) | maintainer |
| D10 | Users | **one admin account plus scoped API tokens** in V1; no roles | maintainer |
| D11 | Existing connections when an access rule changes | **new connections only** by default; "also cut existing connections" is an explicit option (§2.4) | maintainer |
| D12 | Rule and fault precedence | as §2.4, with D24–D26 | maintainer |
| D13 | License | **MIT** (`LICENSE`) | maintainer |
| D14 | Interface naming | **logical names** (UPLINK, IOT, MGMT) in the UI; Linux names only in the technical views | maintainer |
| D15 | Implementation stack | **Go backend** (single binary with embedded UI, low memory on Raspberry Pi, mature netlink/nftables/DNS libraries, experience from sessile) **+ Vue 3 frontend**; shared types come from the OpenAPI spec instead of shared code (§3.7) | maintainer |
| D16 | Test-network attachment | every test network is a gateway-owned bridge; physical port and probes are bridge ports (§2.2). Not to be confused with D8: the gateway still routes, the bridge only joins ports of one network | S12 |
| D17 | Classification | per packet via the conntrack original tuple, mark with 12-bit fault id and direction bit, identical tc mapping on every interface (§3.3) | S10, S11, S15 |
| D18 | Rate and queue limits on group/network/global scope | **per device**: every device matched by a rate-limited fault or profile gets its own queue with the full rate; the UI shows how many queues a scope creates (§2.4) | maintainer |
| D19 | WireGuard | hub networks (clients with client networks) and point-to-point links; keys generated on the gateway by default; export as `.conf` and QR (§2.2.1) | maintainer |
| D20 | Routing daemon | **BIRD 2** in its own managed instance (BGP, OSPFv2, Babel, static); adapter interface for FRR later; external mode for other daemons (§2.2.2) | maintainer |
| D21 | When WireGuard comes | **Phase 1 (M4b)**, so every later feature works with WireGuard interfaces from the start | maintainer |
| D22 | IPv6 for WireGuard (underlay and inside) | **later, with M32**; V1 is IPv4 only; behind IPv4-less uplinks the gateway initiates links (§2.2.1) | maintainer |
| D23 | When dynamic routing comes | **Phase 1 (M4c)**, directly after WireGuard | maintainer |
| D24 | Overlays vs. configuration | **overlays always win** over configuration; specificity decides within each layer (§2.1.1) | maintainer |
| D25 | Access rules and faults in the UI | **two separate objects and screens**: ordered access rules; faults by family and scope (§2.4, §2.17) | maintainer |
| D26 | Explicit fault priorities | **none**; within a level the newer entry wins | maintainer |
| D27 | Additional features in V1 | **none** of the proposals of §8 (review 2, part B); they stay proposals | maintainer |
| D28 | Scope reductions for V1 | three-way merge, NFLOG rule captures, live capture streaming, traceroute/path MTU/iperf3, continuous drift detection moved after V1 (M38) | proposed by review 2; reversible |
| D29 | Gateway services and faults | DNS proxy, TLS responder and TLS proxy run in a **service namespace** reached by policy routing; no IFB/flower for services (§3.3) | S16 |
| D30 | API and domain model | `api/openapi.yaml` (OpenAPI 3.0.3 for oapi-codegen/kin-openapi) is normative for the model and API shape; model conventions in §2.15 (maps keyed by UUID, read-only resource views, unit strings, shared overlay/step bodies, one device namespace, strict decoding, internal service API); configured faults only for impairment/MTU/tunnel, everything else as overlays | spec draft 1, independent review 2026-10-02 |
| D31 | Persistent faults | **V1: only the impairment, MTU and tunnel families are persistent** (configuration); DNS faults, TLS cases, DHCP actions and profile activations exist only as overlays. **After V1 (M39):** every family and profile activations can be persistent. Reason: persistent faults serve a permanent test environment, not the CI use case, and they weaken the clean baseline (`reset` does not remove them; D27 keeps V1 small) | maintainer |

## 7.2 Open

| # | Question | Recommendation |
|---|---|---|
| Q1 | Where do the KVM-dependent tests run — measurement tests (§4.3) and level 2 appliance VMs (from M5b)? The development VPS has no `/dev/kvm`. | A KVM-capable CI runner: a hosted runner that offers `/dev/kvm`, or a dedicated or bare-metal machine as self-hosted runner. Decide before M5b (level 2) and before M8b (first accuracy measurements). Until then these tests do not run, and the release notes say that accuracy and timing are unvalidated. |

New questions are added here with a recommendation.

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
| **Event-triggered scenario steps** | "cut 2 s after the MQTT session is up" instead of fixed times — boot times of 3–9 s make fixed-time cuts flaky | medium | after V1 (review 2, high value) |
| **NAT idle timeout and rebinding** | carrier NAT drops idle TCP after ~5 min while the MQTT keepalive is 20 min; DTLS sessions break on a new source port | medium | after V1 (review 2, high value) |
| **Faults after N bytes** | reset, stall or throttle a flow after N bytes — OTA resume and read timeouts, works without the test CA | small | after V1 (review 2) |
| **Fail the first N attempts + backoff checks** | detects reconnect storms without backoff (battery, rate limits, thundering herd) | medium | after V1 (review 2) |
| **HIL integration** | webhook steps (power cycle via relay/PDU), device log upload onto the run timeline, external check results in the JUnit report | small–medium | after V1 (review 2) |
| **Resource budget checks** | bytes, connections, DNS queries, full vs. resumed TLS handshakes per run — cellular data plans | small | after V1 (review 2) |
| **Security and compliance checks** | no plaintext, TLS ClientHello audit, DNS leaks, MUD allowlist — evidence for RED/EN 18031 and CRA | medium | after V1 (review 2) |
| **Cellular/LPWAN profiles, radio wake-up delay** | NB-IoT, LTE-M, 2G profiles; extra latency after idle | small / medium | after V1 (review 2) |
| **pytest plugin, CI templates, Robot Framework library** | fixtures with guaranteed cleanup, pytest-embedded integration | small–medium | after V1 (review 2) |
| **HTML run report and run comparison** | one file for a bug ticket; firmware A vs. B with measured values | medium | after V1 (review 2) |
| **Seeds and repeated runs** | reproducible random faults (netem seed on newer kernels), pass rate over N runs | small | after V1 (review 2) |
| **Time faults beyond the NTP offset** | redirect hardcoded NTP, kiss-of-death, stratum 16, NTP unreachable at boot, 2038 preset | small | with M34 |
| **Small fault additions** | drop IP fragments, "first DNS address dead", ICMP type for reject, server FIN instead of RST | small | after V1 (review 2) |
| **Trace replay** | replay recorded latency/loss/rate time series (handover, tunnels) | medium | after V1 (review 2) |
| **Faults for local and multicast traffic** | mDNS, Matter commissioning; needs one port per device or VLANs | large | after M31 |
| **Flight recorder** | rolling per-device buffer of pcap, DNS, flows and events; snapshot after the bug happened | medium | after V1 (review 2) |
| **Save a session as a scenario** | turns interactive poking in the UI into a reproducible scenario | medium | after V1 (review 2) |
