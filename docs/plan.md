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
7. Open Decisions
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
| Connectivity | routed gateway, NAT, DHCP, DNS, later VLANs, IPv6, WireGuard remote access |
| Access control | allow, drop, reject, TCP reset, per device/network/traffic |
| Network faults | latency, jitter, loss, rate limit, reordering, duplication, corruption, blackout, flapping, MTU/PMTUD faults |
| Application faults | DNS faults, TLS certificate faults, TLS interception, HTTP manipulation |
| Infrastructure faults | DHCP behavior, time (NTP) offset |
| Automation | profiles, scenarios, timed faults, REST API, CLI, event stream, test reports |
| Evidence | per-rule counters, live connections, packet capture, run artifacts |
| Diagnostics | connectivity checks, probe clients that experience the same faults as devices |

## 1.4 Non-Goals

- A general-purpose home or enterprise router or firewall.
- Link-layer (WiFi radio) impairment in V1 (see §8).
- Dynamic routing protocols, multi-WAN, PPPoE in V1. The routing model must not rule them out later.
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
| **Network** | a logical test network: subnet, DHCP/DNS settings, access to other networks. Connected to the gateway through an **attachment** (V1: physical interface; later: VLAN, bridge, tunnel) |
| **Device** | a device under test with a stable internal identity. It is recognized by one or more identifiers — MAC address, IPv4 address/range, later IPv6 address or WireGuard peer. Configured or discovered |
| **Group** | a named set of devices (e.g. "all sensors") |
| **Probe** | a virtual test client inside the gateway, attached to a network and treated like a device (see §2.12) |
| **Traffic selector** | a match expression: source, destination, protocol, ports, hostname, direction |
| **Flow** | an observed connection (from conntrack), with counters |
| **Rule** | selector plus access action (allow/drop/reject/reset) |
| **Fault** | selector plus impairment parameters, optionally time-limited |
| **Profile** | a named, reusable set of fault parameters ("Bad LTE") |
| **Scenario** | a timeline of steps (faults, profiles, rules, actions) |
| **Run** | one execution of a scenario, with its artifacts |
| **Capture** | a packet recording |
| **Revision** | an immutable version of the persistent configuration |
| **Overlay** | runtime state layered over the configuration (active faults, active profiles, running scenarios), optionally with an expiry |

Two levels of state are central to the design:

- **Configuration** (persistent, revisioned): uplink, networks, devices, groups, rules, profiles, scenarios. This changes rarely and deliberately.
- **Overlays** (runtime, not revisioned): the faults and profiles a test run switches on and off, often many times per minute. Overlays can carry a **TTL**, so a test job that crashes does not leave the network broken; the fault expires on its own.

## 2.2 Networks and Connectivity

**V1**

- One uplink: static IPv4 or DHCP client.
- One or more **IPv4-only** test networks, each attached to its own physical interface (the domain model allows other attachment types later):
  - Static gateway address.
  - Routing to the uplink with masquerade (NAT).
  - Access matrix between networks (e.g. IoT → Internet ✓, IoT → Management ✕).
- A management network or interface for the UI and API, separated from test networks (see §2.16).

**Later**

- VLANs (multiple networks on one trunk port).
- IPv6: router advertisements, SLAAC, DHCPv6, IPv6 firewalling and faults.
- WireGuard remote access, with each peer granted access to specific networks.
- Port forwarding (DNAT).

**V1 test networks are IPv4-only.** The gateway sends no router advertisements, offers no DHCPv6 and drops all forwarded IPv6 traffic. Devices only have link-local IPv6 addresses, so dual-stack devices fall back to IPv4. This is intended: faults and rules must never be bypassable, and a device reaching its server over an unimpaired IPv6 path would invalidate the test.

## 2.3 Devices and Discovery

- Discovery sources: DHCP leases, the neighbor table (ARP/ND), conntrack.
- Configured devices have a name, MAC and optional fixed IP (DHCP reservation). Discovered devices appear automatically and can be adopted with one click.
- The device view shows: online state, IP, lease, current flows (destination, protocol, bytes, state), traffic rates, active rules/faults, captures.
- Rules and faults address the **device**, not an address. The compiler translates the device's current identifiers into match sets (see §3.3). The MAC address is the most stable identifier but is only visible for devices on the same L2 segment as the gateway. Devices behind another router, WireGuard peers and probes are identified by IP address or peer instead.

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

- When several faults match the same traffic, Chaos Gateway resolves them into **one effective fault configuration per direction**. This is a deliberate product decision, not a Linux limitation. Linux could chain impairments, but stacked faults are hard to predict and to explain. The effective configuration maps onto one netem instance.
- Resolution: the more specific fault wins. Precedence: `device + traffic > device > group > network > global`. An explicit priority value can override this order. The preview shows the effective result and which faults were overridden.
- A profile activated on a scope acts like a fault on that scope.
- Faults act on **every packet**, including packets of connections that already existed when the fault was activated. Classification is therefore evaluated per packet, not cached per connection (see §3.3). Spike S10 confirmed this: with per-packet classification the first message after a change is affected, while conntrack-mark caching left the running connection on its old fault.
- **Direction "both"** means separate parameters per direction. The UI shows them as a pair, and they can be set asymmetrically (e.g. upload 2 % loss, download 0 %).

**Precedence between access rules and faults:** access rules are evaluated first. A dropped packet does not reach any fault. The preview (§2.14) shows the effective result for each selector.

## 2.5 Network Faults (L3/L4)

| Fault | Parameters | Mechanism | Notes |
|---|---|---|---|
| Latency | delay, jitter, distribution, keep order | netem | jitter reorders packets by default (S2: 71 % at 2 ms spacing); "keep order" uses netem rate, which shifts the delay distribution |
| Loss | % random, burst models (Gilbert-Elliott) | netem | S2: accuracy within the 99 % confidence interval |
| Rate limit | bit/s | netem rate or HTB | large delay × rate needs larger queue limits |
| Reordering | % | netem | requires a delay |
| Duplication | % | netem | |
| Corruption | % | netem | corrupted packets are usually dropped by checksums at the receiver |
| Queue limit | packets | netem limit | small queues cause bufferbloat-free tail drop |
| Blackout | on/off | nftables drop | "offline". Counters show dropped packets |
| Flapping | up/down durations | timed blackout | "intermittent connectivity" |
| TCP reset | on connect / on existing | nftables reject with tcp reset | a firewall action, not traffic control |
| Timeout | silently drop | nftables drop | the device waits for its own timeouts |
| MTU / PMTUD | max. packet size, ICMP "fragmentation needed" on/off | nftables size match plus ICMP handling | simulates PMTUD black holes. The device's own interface MTU cannot be changed |

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
- **Hardcoded resolvers:** DNS traffic to other resolvers (UDP/TCP 53) can be redirected to the gateway, and DNS-over-TLS (853) can be blocked. DNS over HTTPS cannot be distinguished reliably from normal HTTPS (see §6).
- **Hostname selectors (best effort):** rules and faults can target hostnames. The DNS proxy records which IPs it returned for which name and fills them into address sets. It follows CNAMEs, tracks answers per requesting device and honors their TTL.
  - The set is updated **before** the answer is sent, so the device's first packet already matches.
  - Updates are deduplicated and batched. In spike S5, one `nft` process per answer limited the proxy to ~280 queries/s; with deduplication it reached ~6,700.

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
- **Limitation:** the server cannot force a client to renew immediately. The FORCERENEW mechanism is rarely supported by clients. Renewal-related tests work through short lease times or through a link interruption.
- The UI shows DHCP inside the network and device views, not as a separate menu.

## 2.8 TLS and Application Layer

Selected traffic (by device, port or hostname) is redirected transparently to one of two components. No proxy configuration is needed on the device.

- **TLS responder** (part of the core, Node.js) for certificate test cases. It terminates the connection itself with a deliberately broken certificate generated for the requested SNI and never forwards traffic. A correct device aborts anyway.
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

A device without the test CA fails every certificate case at the first check ("unknown issuer"). Expiry and hostname validation can therefore only be tested individually on firmware that trusts the test CA. The most valuable test works with any firmware: *does the device accept a certificate it must reject?* If it does, the run records a failed security check.

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
| Congested WiFi | 30 ms ± 40 ms, 2 % loss, bursts |
| Offline | blackout |
| Intermittent | flapping 20 s up / 10 s down |
| DNS broken | DNS SERVFAIL |
| TLS broken | handshake reset on TLS ports |

Profiles apply to a device, group, network or globally, and can be activated with a TTL. The built-in values are starting points to be calibrated.

## 2.10 Scenarios and Runs

A scenario is a timeline:

```yaml
name: mqtt-outage
target: { device: esp32-42 }
capture: true
steps:
  - at: 0s    profile: normal
  - at: 10s   fault: { latency: 200ms, jitter: 50ms }
  - at: 20s   fault: { loss: 10% }
  - at: 30s   rule:  { action: drop, protocol: tcp, port: 8883 }
  - at: 45s   restore
checks:
  - after: 60s  device_reconnected: { port: 8883, within: 30s }
```

- Scenarios are stored as YAML and can be versioned in git and imported/exported.
- Step types: profile, fault, rule, DNS fault, TLS case, DHCP action, capture start/stop, wait, restore.
- **Checks** evaluate observations, e.g. "device reconnected to the broker within 30 s", "device did not accept the invalid certificate", "no traffic to unexpected destinations".
- A **run** stores the timeline as executed, events, counters, captures, check results and a report (JSON and JUnit XML for CI).
- Run lifecycle: `queued → running → completed | failed | aborted`.
- A run keeps going when the client that started it disconnects. It ends when it reaches its last step or is stopped explicitly (`POST /runs/{id}/abort`). Either way, the state from before the run is restored.
- Optional **lease** for unattended automation: the client must renew the lease periodically (heartbeat). If it expires, the server aborts the run and cleans up. Without a lease, the scenario's own end and the overlay TTLs are the safety net.
- Timing precision: steps are applied within ±100 ms of their scheduled time (to be confirmed by measurement).

## 2.11 Capture

- Record by network, device, selector or rule ("capture everything this fault affects").
- Mechanism (spike S9, all exact against a capture on the device itself):
  - **Device and network captures:** AF_PACKET (libpcap) on the LAN-side interface with a BPF filter. Full Ethernet frames, pre-NAT addresses.
  - **Rule captures:** NFLOG from the rule's own nftables selector. Exact regardless of NAT, L3 packets only (link type NFLOG, readable by Wireshark).
  - Captures on the uplink cannot be attributed to devices after NAT and are offered only as "uplink capture".
- PCAP/PCAPNG, ring buffer, size and time limits, disk quota, automatic cleanup.
- Download, or stream live to Wireshark.
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

**Probes:** diagnostics from the gateway's own address do **not** pass through device-specific faults. Probes are virtual clients (a network namespace connected to a test network). They get an address via DHCP like a real device and can be targeted by rules, faults and profiles. They allow:

- verifying that a profile really produces the configured latency and loss (**calibration / self-test**)
- testing a scenario before connecting real hardware
- comparing: "with this fault, the probe reaches the broker in 1.8 s instead of 0.2 s"

## 2.13 Observability and Events

- Per rule and fault: matched packets and bytes, packets dropped/delayed by the fault. *"Active"* is not enough; the counters show whether a rule actually matches.
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
- **Verify:** reads back the kernel state and compares it with the target.
- **Commit-confirm:** changes that could lock out the admin (management access, uplink, firewall defaults) are rolled back automatically unless confirmed within 60 s.
- **Anti-lockout:** access from the management network to the gateway's **control plane** (UI, API, SSH) is always allowed and cannot be removed by rules. The protection covers nothing else: traffic from the management network to devices or the Internet follows the normal rules.
- **Revisions:** diff, rollback, export/import (secrets excluded by default), clone.
- **Concurrency:** every write names the revision it is based on. Conflicting writes are rejected (optimistic locking), so UI and automation cannot silently overwrite each other.
- **Drift detection:** Chaos Gateway only manages objects it owns: its own nftables table, qdiscs on its interfaces, routes with its own protocol tag. Changes to those are detected and can be reconciled. Objects it doesn't own are ignored.
- **Boot:** the last committed revision is applied at boot. Overlays are not restored after a restart; tests start from a clean baseline.

## 2.15 API and CLI

- REST under `/api/v1`, described by OpenAPI. UI and automation use the same API.
- SSE (or WebSocket) for events and live data.
- Main resources:
  - `uplink`, `networks`, `devices`, `groups`, `rules`, `profiles`, `scenarios`
  - `overlays` (active faults/profiles, with TTL)
  - `runs`, `captures`, `diagnostics`, `probes`
  - `revisions`, `preview`, `apply`, `metrics`, `events`
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
- **CLI and SDK:** a TypeScript client library and a CLI (`chaosctl`) for CI pipelines and pytest/Jest test suites.
- Optional later: MQTT control interface.

## 2.16 Security

- The UI/API listens only on the management network, over HTTPS (self-signed certificate by default, replaceable).
- Devices under test are untrusted: test networks cannot reach the management plane.
- V1: one admin account plus API tokens (scoped: read-only, overlays only, full).
- Privilege separation: the API server runs unprivileged. Only a small executor process has network privileges and accepts typed, validated operations, never command strings (see §3.1).
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
│ chaosgw-api   (Node.js/TypeScript, unprivileged user)                │
│   REST/SSE · auth · domain model · validation · compiler · scheduler │
│   (overlays/TTL, scenarios) · observers · persistence · static UI    │
└───────────────┬──────────────────────────────────────────────────────┘
                │ Unix socket, typed operations (JSON schema)
┌───────────────▼──────────────────────────────────────────────────────┐
│ chaosgw-exec  (Node.js, root / CAP_NET_ADMIN, CAP_NET_RAW)           │
│   applies plans: nft -j -f · tc -batch · ip -batch · sysctl ·        │
│   conntrack · wg · capture processes · probe namespaces              │
│   reads state: ip -j · tc -j · nft -j · conntrack events             │
└───────────────┬──────────────────────────────────────────────────────┘
                │
    Linux kernel: nftables · conntrack · tc/netem · routing · netns

Services managed by chaosgw:
  chaosgw-dns    DNS proxy (Node.js; part of the project)
  chaosgw-tls    TLS responder for certificate test cases (Node.js; part of the project)
  DHCP server    Kea (decided in spike S6)
  tls-proxy      mitmproxy + Chaos Gateway addon (Python sidecar, interception only)
  tcpdump        capture
```

- The core is TypeScript. Python runs only in the TLS interception sidecar (mitmproxy addon). It communicates with the core via a local API and can be left out entirely if TLS interception is not used; the certificate test cases work without it.
- The executor accepts only a closed set of operation types. Each is validated and turned into command invocations with argument arrays: no shell, fixed binary paths.
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
    - nftables: the whole `inet chaosgw` table is rebuilt in one atomic transaction, even for a small domain change. That is simpler and safer than rule-level edits.
    - tc: existing qdiscs/classes are changed in place. Replacing a qdisc of the same kind keeps its queue too (S2); only deleting it or changing its kind drops queued packets.
- **Complete parameter sets:** `tc qdisc change` keeps netem attributes that are not given; spike S2 showed a rate limit surviving a change. The compiler therefore always emits every netem attribute, with neutral values where unused (e.g. `rate 0bit`).
- **nftables as JSON:** the compiler generates the ruleset as JSON, not text. Text syntax has pitfalls that the spikes hit repeatedly: reserved words used as chain names (`fwd`, `dnat`) and a missing `;` before `}` make the whole transaction fail.
- Overlay changes (the frequent case) touch only faults and marks, never routing or services.

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

- **Classification identity:** after precedence resolution, the compiler assigns an id to each *effective fault configuration*, not to each rule.
  - Access rules need no marks; they are evaluated directly in nftables.
  - The id goes into a reserved bit field of the packet mark: **bits 8–15, mask `0x0000ff00`** (up to 255 fault classes). Other bit fields stay free for later uses (routing, capture) and for other software.
  - The classification key is `ct original ip saddr . meta l4proto . ct original proto-dst` in verdict maps. It is identical for both directions and unaffected by NAT; precedence is the order of the lookups (device+port before device before network).
- **Per packet, not per connection:** the id is computed for every packet, using the conntrack original tuple for replies. Caching it in the conntrack mark would keep existing connections on the old fault after an overlay changes (S10 showed exactly that). Spike S8 found no measurable throughput cost with 1000 device and 1000 device+port entries on x86; Raspberry Pi still has to be measured.
- Each interface maps the id to its own tc class, so upload and download parameters are independent.
- **tc topology** (confirmed in S2): an HTB root with a default class and one class per active id, each with a netem leaf, selected by a `fw` filter with mask (`handle 0x0a00/0xff00`).
  - A classful root is needed as soon as more than one fault is active on an interface; `prio` would be too small, since it is limited to 16 bands.
  - Rate limiting works both as netem `rate` and as HTB class rate. With 50 HTB classes, throughput dropped by about 10 % (S8).
- **Traffic terminating at the gateway** (DNS proxy, TLS proxy) does not leave through LAN egress in the upload direction. For it:
  - DNS faults are implemented inside the DNS proxy (delay, drop, wrong answer).
  - TLS/HTTP faults are implemented inside the TLS proxy.
  - Latency/loss combined with the TLS proxy: download is applied on LAN egress; upload uses IFB ingress redirection. Spike S2 confirmed IFB with a `flower` L3 match (marks are not yet set at tc ingress): +100 ms applied to one device's traffic to the gateway, the other device unaffected.

## 3.4 Linux Interface Layer

- **CLI first:** in V1 the executor uses the standard tools with JSON output and batch input. Replacing parts with native netlink later is possible (§3.1).

| Tool | Read | Write |
|---|---|---|
| nftables | `nft -j list` | `nft -j -f` (atomic transaction) |
| iproute2 | `ip -j` | `ip -batch` |
| tc | `tc -j` | `tc -batch` |
| conntrack-tools | `conntrack -L` | `conntrack -D` |
| WireGuard | `wg show` | `wg syncconf` |

- Event sources, for live data without polling: `ip monitor`, `nft monitor`, `conntrack -E`.
- Counters are polled once per second from nftables and tc (configurable).
- **Preflight check** at start and install. It verifies:
  - kernel version
  - modules: `sch_netem`, `sch_htb`, `cls_fw`, `ifb`, `nf_conntrack`, `nft_*`
  - tool versions
  - IP forwarding
  - conflicting firewalls (ufw, firewalld, Docker). With Docker installed, the FORWARD policy is DROP; an accept rule in Chaos Gateway's own table does not override it, only an accept in Docker's `DOCKER-USER` chain does (spike S7). The preflight offers to add that rule.
  - other network managers owning the assigned interfaces

  Missing items are reported with the fix. On Ubuntu 24.04 generic kernels all required modules (`sch_netem`, `cls_fw`, `ifb`, `wireguard`) are in the base `linux-modules` package. Minimal kernels, such as some VM or cloud sandboxes, can lack netem entirely.

## 3.5 Host Ownership

Chaos Gateway owns the interfaces **assigned to it** (uplink, test networks) and leaves the management interface to the operating system. That interface is configured by netplan or NetworkManager as usual.

- At setup, assigned interfaces are marked as unmanaged for netplan/NetworkManager/systemd-networkd, so no two tools fight over them.
- Its own nftables table (`inet chaosgw`), its own route protocol tag, qdiscs only on its own interfaces.
- Other firewalls: detected by the preflight check. A drop verdict in another table still drops packets, so conflicts are reported, not silently ignored.

## 3.6 Persistence

```
/etc/chaos-gateway/
    config.json            current revision (pointer)
    revisions/000042.json  immutable revisions
    scenarios/*.yaml
    profiles/*.yaml
/var/lib/chaos-gateway/
    secrets/               0600
    runs/<id>/             events.jsonl, report.json, report.xml, captures
    captures/
    state/                 overlay state, last-known-good marker
/var/log/chaos-gateway/    audit.jsonl, service logs
```

- Writes are atomic: write a temp file, fsync, rename.
- Schema version in every file, with migrations on upgrade.
- No database in V1. Run and event history are JSONL files with retention limits.

## 3.7 Technology Stack

| Area | Choice |
|---|---|
| Language | TypeScript (strict), Node.js LTS |
| API server | Fastify; schemas shared with the frontend (Zod); OpenAPI generated from schemas |
| Frontend | React + Vite, TanStack Query, SSE for live data |
| Tests | Vitest (unit, integration), Playwright (UI) |
| Build | pnpm workspaces |
| Packaging | .deb with systemd units; container image for development and demos |
| TLS proxy | mitmproxy in a Python virtualenv, managed as a systemd unit |

```
chaos-gateway/
  apps/
    api/            REST, auth, scheduler, observers
    exec/           privileged executor
    web/            React UI
    dns/            DNS proxy
    cli/            chaosctl
  packages/
    domain/         concepts, schemas, validation
    compiler/       policy resolution, target state, diff
    linux/          command builders, parsers (nft/tc/ip JSON)
    client/         TypeScript API client (used by UI, CLI, tests)
    testbed/        namespace topology harness
  sidecars/
    tls-proxy/      mitmproxy addon
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
| Unit | domain, validation, precedence resolution, scheduler, parsers | Vitest | every commit |
| Compiler golden | configuration → nftables/tc/route output, compared with reviewed golden files | Vitest | every commit |
| Linux integration | real kernel behavior in namespaces: routing, NAT, faults, rules, DNS, DHCP, TLS proxy | Vitest + testbed in the privileged test container (§4.5 level 1) | every commit |
| Measurement | statistical accuracy of faults, timing of scenarios | testbed | nightly, dedicated runner |
| API contract | OpenAPI conformance, error cases, concurrency | Vitest | every commit |
| UI | components against a mocked API; a few end-to-end flows against the real stack in the testbed | Playwright | every commit / nightly |
| Distribution | install package, preflight, smoke tests on clean Ubuntu 24.04, Debian 12/13, ARM64 | appliance VMs (§4.5 level 2) | nightly / before release |

## 4.2 Testbed

A library that builds topologies from network namespaces and virtual interfaces (see §4.5 for the building blocks). Default topology, as used in the spikes:

```
 ns: client-a ─┐                                                 ┌─ ns: server
 ns: client-b ─┼─ ns: switch ─ lan0 [ ns: gateway ] wan0 ────────┤  (HTTP, TLS, MQTT broker,
 ns: probe    ─┘   (bridge)      Chaos Gateway stack runs         │   DNS upstream, iperf3, NTP;
                                 against this namespace           │   no route back → NAT required)
```

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
| WireGuard interface | tunnel endpoint | remote access (M33) |
| tap | a VM's NIC | connecting appliance VMs (level 2) |

Link events are simulated by setting one end of a veth pair down; the other end loses its carrier. Devices are namespaces with their own MAC, a DHCP client (`udhcpc`) and test tools, so "a device behind another router" or "several LANs" are just different topologies.

### Levels

| Level | Environment | What it can test | When |
|---|---|---|---|
| **0** | plain process, no root | domain, validation, compiler golden files, API contract, UI against mocked API | every commit |
| **1** | namespace testbed in the **privileged development/CI container** | routing, NAT, access rules, faults (functional), DNS proxy and faults, DHCP and test actions, TLS responder and interception, capture, probes, API and UI end-to-end, scenarios | every commit |
| **1b** | namespace testbed inside a VM with a **stock distribution kernel** (QEMU + virtme-ng) | same as level 1 when the host kernel lacks modules (e.g. no netem), or to check a specific distribution kernel | when needed; nightly for the kernel matrix |
| **2** | **appliance VMs** (QEMU/KVM) from distribution images, gateway VM with three virtio NICs (uplink, test LAN, management) connected via tap and bridges to client/server namespaces or VMs | .deb installation, preflight, interface assignment, coexistence with netplan/NetworkManager/systemd-networkd, setup wizard, reboot, last-known-good, safe mode, upgrades and migrations, coexistence with Docker (`DOCKER-USER`) | nightly |
| **3** | **hardware lab**: Raspberry Pi 4/5, x86 mini PC, real NICs, managed switch, real ESP32 devices (later a WiFi AP) | performance targets (§3.10), timing precision, NIC drivers and offloads, real firmware behavior, long-running tests | before releases |

A candidate for levels 1–2 is Espressif's QEMU fork, which can run ESP32 firmware with an emulated Ethernet interface. That would allow testing real ESP-IDF firmware against the gateway without hardware; it has to be evaluated first.

### Feature → minimum level

| Feature | Level |
|---|---|
| Compiler output, precedence resolution, validation | 0 |
| Routing, NAT, access rules, connection behavior | 1 |
| Faults: function (effect, isolation, direction, live changes) | 1 |
| Faults: accuracy measurements | 1 on native execution or KVM; 3 for Raspberry Pi |
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
| Host kernel with the required modules | The container uses the host kernel and cannot bring its own modules: `sch_netem`, `sch_htb`, `cls_fw`, `cls_u32`, `cls_flower`, `act_mirred`, `ifb`, `nf_tables` with NAT/conntrack/log/dup, `8021q`, `wireguard`, `veth`, `bridge`. A test preflight checks them; if some are missing, the affected tests run in level 1b. |
| `/dev/kvm` passed through (optional) | Level 1b and level 2 inside the container. Without KVM, QEMU falls back to software emulation: functional tests still work, but timing measurements do not (baseline 2.4 ms instead of 0.3 ms, `nft` commands 0.5 s instead of 6 ms). |
| Unique namespace prefix per test run | Parallel runs with the same names destroy each other's topology. |
| Test tools in the image | iproute2, nftables, conntrack-tools, tcpdump, tshark, iperf3, dnsutils, busybox (`udhcpc`), ethtool, socat, Kea, Node.js, Python 3, mitmproxy (sidecar tests). |

### Reference development setup

Ubuntu Server 24.04 in a VirtualBox VM, development inside the privileged devcontainer on that VM (VS Code Remote-SSH + Dev Containers). The VM provides the exact target kernel, so level 1 runs completely.

| Setting | Why |
|---|---|
| 4+ vCPUs, 8+ GB RAM, 40+ GB disk | testbed, Kea, mitmproxy, Node toolchain and captures in parallel |
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

**Status:** executed. All spikes except S8 are complete; S8 still has to run on Raspberry Pi hardware. Results and decisions are in [`docs/spikes/REPORT.md`](spikes/REPORT.md); the findings are already incorporated into this plan.

| Spike | Question | Pass criterion |
|---|---|---|
| S1 Testbed | Can we build client/gateway/server namespaces in CI and on target machines? | ping across the topology in CI and on a Raspberry Pi |
| S2 Fault topology | Which tc topology carries many independent per-direction faults (HTB/DRR/other + netem), including rate limiting and in-place changes? Which approach for traffic terminating at the gateway (IFB)? | device A gets 200 ms/5 % in both directions, device B unaffected; changing A's fault does not disturb B; measured values within tolerance |
| S3 Connection behavior | How do drop/reject/reset and conntrack deletion affect existing TCP connections? | documented behavior matrix, reproducible tests |
| S4 TLS | Transparent redirect (REDIRECT vs. TPROXY) by device/port/hostname; SNI handling; certificates generated with broken properties (Node TLS responder); TLS 1.2/1.3; connection reuse; mitmproxy transparent mode, key log, control from Node | each TLS case of §2.8 observable from a client; list of cases that are not feasible |
| S5 DNS proxy | Node DNS proxy: per-client faults, updating nftables sets for hostname selectors, performance | selectors match after resolution; ≥ 1000 queries/s on Pi |
| S6 DHCP server | Kea vs. dnsmasq: reservations, lease deletion, option changes, NAK/silence at runtime via API | decision with feature table |
| S7 Deployment | Native package vs. container: sysctls, Docker FORWARD policy, NetworkManager/netplan coexistence | documented supported setups |
| S8 Hardware | Throughput and CPU on Raspberry Pi and x86 with 50 active faults | targets of §3.10 confirmed or adjusted |
| S9 Capture | Which mechanism captures exactly the traffic of a selector — device identifiable despite NAT, both directions, full payload, valid PCAP/PCAPNG, live to Wireshark — at acceptable cost? | a capture of one device's MQTT traffic contains all and only its packets and opens in Wireshark |
| S10 Classification identity | Mark layout (bits, fields); per-packet classification via conntrack original tuple vs. ct mark; cost of per-packet evaluation; coexistence with routing marks and other software; one shared id for nftables and tc | an overlay change affects existing connections within 100 ms; layout documented; throughput cost measured |

Order: S1 first, then S10 and S2 together. Classification is the foundation for faults, NAT handling, capture and later routing. The other spikes can run in parallel.

## Phase 1 — Foundation: Routed Gateway

**M1 — Repository, CI and testbed library**
- Scope: monorepo, lint/format, Vitest, Playwright skeleton, CI with unprivileged and privileged jobs, `packages/testbed` from spike S1, the privileged test container image with all test tools, and a kernel-module preflight that sends tests to level 1b when modules are missing (§4.5).
- Tests: CI runs a testbed test (client pings server through a plain forwarding namespace).
- Depends on: S1.

**M2 — Domain model and persistence**
- Scope: schemas for uplink, network, device, group, rule, fault, profile, scenario, overlay; validation; atomic file persistence; revisions with diff; schema version.
- Tests: unit tests for validation and precedence resolution; revision round-trip; corrupted-file handling.
- Depends on: M1.

**M3 — Executor and state reader**
- Scope: privileged executor with typed operations, argument-array command invocation, namespace targeting, parsers for `ip -j`, `nft -j`, `tc -j`; Unix-socket protocol with the API.
- Tests: unit tests for command building and parsing (recorded outputs); integration test reads interfaces/routes of the gateway namespace; rejection of invalid operations.
- Depends on: M1.

**M4 — Compiler v1, preview and safe apply: routed gateway with NAT**
- Scope: uplink (static IPv4), one test network (static address), forwarding, masquerade, access matrix default; target state, diff, preview, apply, verify, rollback on failure.
- Tests: golden tests; integration — client reaches server through the gateway; preview matches applied state; an injected executor failure leaves the previous state active.
- Depends on: M2, M3.

**M5 — REST API v1**
- Scope: API server, admin login, API tokens, OpenAPI, configuration and revision endpoints, preview/apply, optimistic concurrency, SSE event stream skeleton, audit log.
- Tests: API contract tests; E2E through the testbed (configure via API → traffic flows); concurrent-write conflict test.
- Depends on: M4.

**M6 — DHCP, DNS and device discovery**
- Scope: DHCP server (Kea, per S6) with pools and reservations; DNS proxy (UDP and TCP) with forwarding, caching and query log; uplink via DHCP client; device discovery from leases, neighbor table and conntrack; devices API.
- Tests: client namespace gets a lease and resolves names; device appears with MAC/IP; reservation honored; discovered vs. configured devices.
- Depends on: M5, S5, S6.

*After Phase 1: a working, API-configurable test gateway without faults.*

## Phase 2 — Faults (core value)

**M7 — Classification layer**
- Scope: classification as defined by S10 — device identifiers, group, network and selector (protocol, ports, destination IP/CIDR); per-packet evaluation for both directions; per-rule counters.
- Tests: integration — counters increase only for matching traffic, for both directions, including behind NAT; changing the classification affects an already established connection.
- Depends on: M6, S2, S10.

**M8 — Fault engine: latency, jitter, loss; overlays with TTL**
- Scope: tc tree per interface, one class per fault, netem leaves, per-direction parameters, precedence resolution, overlays API with TTL, `POST /reset`, in-place updates.
- Tests: measurement tests (§4.3) for device, group and network scope; isolation test; TTL expiry removes the fault; updating a fault does not reset other faults.
- Depends on: M7.

**M9 — Access rules**
- Scope: ordered allow/drop/reject/TCP reset rules; "also cut existing connections"; precedence rules vs. faults; hit counters; preview of the effective result.
- Tests: behavior matrix from S3 as automated tests; rule order; anti-lockout rule cannot be overridden.
- Depends on: M7, S3.

**M10 — Extended faults**
- Scope: rate, reorder, duplicate, corrupt, burst loss, queue limit, blackout, flapping, MTU/PMTUD faults, timeout.
- Tests: one measurement test per fault type; flapping timing within tolerance; PMTUD black hole reproduced with a large HTTP download.
- Depends on: M8, M9.

**M11 — Profiles**
- Scope: built-in and custom profiles (YAML), activation on scopes via overlays, precedence with individual faults.
- Tests: activating/switching profiles yields the configured parameters (compiler) and measured values (integration); a device fault overrides a network profile.
- Depends on: M10.

*After Phase 2: the core product via API — faults, rules, profiles.*

## Phase 3 — Web UI

**M12 — UI shell, overview, devices (read and live)**
- Scope: visual system and shared components (§2.17), login, layout, overview, device list and detail with flows, rates and active faults; live updates via SSE; setup wizard for interface assignment; empty, offline and safe-mode states.
- Tests: Playwright against a mocked API (all states: empty, many devices, offline); one E2E flow in the testbed.
- Depends on: M11 (API complete for display).

**M13 — UI for faults, rules and profiles**
- Scope: add/edit fault dialog with preview, rules & faults list with counters, rule editor (IF/THEN), unapplied-changes bar, preview-and-apply drawer, concurrent-change dialog, profile cards, TTL display, `</> API` panel.
- Tests: Playwright — create a fault in the UI, then verify the measured effect in the testbed; validation errors are shown; concurrent-change conflict dialog.
- Depends on: M12.

**M14 — Networks view and technical diagnostics view**
- Scope: network cards and detail, DHCP pool/leases, access matrix editing with commit-confirm; compiled-state view.
- Tests: Playwright; commit-confirm — unconfirmed change is rolled back after the timeout.
- Depends on: M13.

*After Phase 3: usable interactively. First release candidate for internal use.*

## Phase 4 — Automation

**M15 — Scenario engine and runs**
- Scope: YAML scenarios, scheduler, step types (profile, fault, rule, wait, restore), runs with lifecycle, timeline and events, explicit abort, optional lease with server-side cleanup, JSON/JUnit report.
- Tests: timing within ±100 ms; abort restores the previous state; a disconnecting client does not stop a run, an expired lease does; the report contains all steps.
- Depends on: M11.

**M16 — Checks**
- Scope: observation-based checks (connection established within t, no connection accepted, traffic to destination seen/not seen, DNS query seen), evaluated in runs.
- Tests: checks pass and fail correctly with scripted client behavior in the testbed.
- Depends on: M15.

**M17 — Capture**
- Scope: capture by network, device, selector or rule (mechanism per S9); ring buffer, quotas, retention; download and live stream; attachment to runs.
- Tests: the capture contains exactly the selected traffic; quota enforcement; capture in a run is attached to the report.
- Depends on: M7, M15, S9.

**M18 — CLI and client library**
- Scope: `chaosctl` (apply profile, set fault with TTL, run scenario, wait for result, fetch report); published TypeScript client; examples for pytest and Jest.
- Tests: CLI tests against the testbed; the example test suite runs in CI.
- Depends on: M15.

**M19 — Scenario UI**
- Scope: scenario list, timeline editor, YAML view, run view with live progress and results.
- Tests: Playwright — build and run a scenario, then check the result display.
- Depends on: M16, M13.

*After Phase 4: V1 feature complete for L3/L4 testing and automation.*

## Phase 5 — Application Layer

**M20 — DNS faults and hostname selectors**
- Scope: NXDOMAIN, SERVFAIL, timeout, delay, wrong answer, truncation (with TCP fallback), short TTL, per device/group/pattern; deduplicated selector set updates; redirect of hardcoded DNS; DoT blocking; hostname selectors for rules and faults.
- Tests: `dig` from clients shows each fault; a hostname-selector fault affects only traffic to the resolved IPs; hardcoded DNS is redirected.
- Depends on: M8, S5.

**M21 — TLS responder: certificate cases**
- Scope: TLS responder in the core, transparent redirect of selected traffic, the TLS cases of §2.8 as confirmed by S4, events per handshake, check type "TLS rejected/accepted". No Python sidecar needed.
- Tests: `openssl s_client`/`curl` from the client — untrusted/expired/wrong-host/self-signed are rejected by a correct client; a deliberately insecure client is flagged by the check.
- Depends on: M9, S4.

**M22 — TLS interception and HTTP faults**
- Scope: mitmproxy sidecar management, test CA management (generate, download for dev firmware), HTTP(S)/WebSocket inspection, URL blocking, error codes, delay/throttle, modification, connection abort, key log for captures; block-UDP-443 option.
- Tests: a client trusting the CA sees the modified responses; timing of delayed responses; QUIC fallback.
- Depends on: M21.

**M23 — DHCP test actions**
- Scope: short leases, lease deletion, forced new IP via reservation change (NAK), option changes, silence; scenario step types for DHCP.
- Tests: `dhclient` in the testbed observes each behavior.
- Depends on: M6, M15.

## Phase 6 — Diagnostics and Observability

**M24 — Diagnostics**
- Scope: ping, TCP/UDP check, DNS, HTTP(S), TLS details, traceroute, path MTU, iperf3; structured results; API and UI.
- Tests: each diagnostic against known testbed targets, including failure cases.
- Depends on: M6.

**M25 — Probes and calibration**
- Scope: virtual clients in namespaces attached to test networks; targeting by rules/faults; self-test "measure this profile".
- Tests: probe measurement matches the configured profile within the §4.3 tolerances.
- Depends on: M8, M24.

**M26 — Observability**
- Scope: metrics endpoint, flow list with counters, fault-affected-packet counters, interface and queue statistics, system health; flow view in the UI.
- Tests: metric values against known traffic; Playwright for the flow view.
- Depends on: M9, M13.

## Phase 7 — Production Readiness

**M27 — Recovery and drift**
- Scope: last-known-good at boot, safe mode, recompile after interrupted apply, drift detection and reconcile for owned objects.
- Tests: external deletion of a rule/qdisc is detected and reconciled; a broken revision at boot leads to last-known-good; killing the executor mid-apply is recovered.
- Depends on: M5, M8.

**M28 — Packaging and installation**
- Scope: .deb for amd64/arm64, systemd units, preflight check, network-manager coexistence, uninstall restoring interfaces; level 2 test harness (appliance VM with three virtio NICs, §4.5).
- Tests: install and smoke tests in clean VMs (Ubuntu 24.04, Debian 12/13) and on a Raspberry Pi.
- Depends on: M14, M27, S7.

**M29 — Security hardening**
- Scope: management-plane binding, HTTPS, token scopes, secret storage, secret-free exports, rate limiting on login, security review of the executor interface.
- Tests: test networks cannot reach the UI/API; tokens with insufficient scope are rejected; exports contain no secrets; fuzz tests on executor operations.
- Depends on: M5, M28.

**M30 — Documentation and V1 release**
- Scope: user guide, API guide with examples, scenario cookbook, troubleshooting (preflight messages).
- Tests: documentation examples run in CI.
- Depends on: M1–M29 as included in V1.

## After V1

| Milestone | Content |
|---|---|
| M31 VLANs | multiple networks on a trunk, access matrix between them |
| M32 IPv6 | RA/SLAAC, DHCPv6, IPv6 rules and faults (lifts the V1 IPv6 block) |
| M33 WireGuard remote access | peers, network access per peer, QR config |
| M34 NTP time faults | see §8 |
| M35 Port forwarding | DNAT for inbound test traffic |
| M36 Further proposals | features from §8 by priority |

## V1 Scope Summary

V1 = Phases 1–4, plus M20, M21, M24, M25, M27, M28, M29, M30.

M22 (interception), M23 (DHCP actions) and M26 (observability) are optional for V1.

---

# 6. Known Limitations and Technical Risks

| # | Topic | Consequence | Handling |
|---|---|---|---|
| 1 | tc acts on egress only | upload and download faults must be applied on different interfaces; traffic terminating at the gateway has no egress in upload direction | classification by marks (§3.3); faults for DNS/TLS inside the services; IFB where needed (spike S2) |
| 2 | Uplink egress is after NAT | device IP is invisible there | mark at the edge, restore via conntrack mark |
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

---

# 7. Open Decisions

Each decision has a recommendation; confirming it is enough to proceed.

| # | Decision | Options | Recommendation |
|---|---|---|---|
| D1 | Supported releases and minimum kernel | Ubuntu 22.04/24.04/newer; Debian 12/13 | Ubuntu 24.04 LTS and newer, Debian 12 and newer, kernel ≥ 6.1 |
| D2 | Deployment | native package, container, appliance image | native .deb primary; container for development/demo; appliance image later |
| D3 | Host ownership | own everything; own assigned interfaces only | own assigned interfaces; management interface stays with the OS |
| D4 | DHCP server | Kea, dnsmasq, own implementation | **decided (S6): Kea** — short leases and a lease API; dnsmasq's minimum lease is 120 s |
| D5 | TLS components | mitmproxy for everything, own Node implementation for everything, split | **confirmed (S4):** certificate and handshake cases in a Node TLS responder (core); interception via mitmproxy sidecar; the "no Python" rule applies to the core only |
| D6 | Uplink types in V1 | static, DHCP, PPPoE | static and DHCP |
| D7 | IP versions in V1 | dual-stack, IPv4-only | IPv4-only test networks in V1 (§2.2); dual-stack in M32 |
| D8 | L2 transparent (bridge) mode | V1, later, never | later. Only needed when the gateway cannot be the device's default router |
| D9 | Hardware targets | as §3.10 | confirm after spike S8 |
| D10 | Users | single admin + tokens, multi-user with roles | single admin + scoped tokens in V1 |
| D11 | Existing connections default for access rules | affect new only, cut existing | affect new only; cutting existing is an explicit option |
| D12 | Rule and fault precedence | as §2.4 | confirm §2.4 |
| D13 | License | open source (which license), closed | decide before first public release |
| D14 | Interface naming | Linux names, logical names | logical names (UPLINK, IOT, MGMT) in the UI; Linux names in technical views |

---

# 8. Proposed Additional Features

| Feature | Benefit for IoT testing | Effort | Suggested placement |
|---|---|---|---|
| **Overlays with TTL and reset** | a crashed test job cannot leave the network broken | small | V1 (included, M8) |
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
