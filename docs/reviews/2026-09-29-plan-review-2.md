# Plan review 2 — 2026-09-29 (after commit 461ad16)

Second, independent pass over `docs/plan.md` after the first review and spikes S11–S14 were incorporated. Two reviewers: (A) consistency and implementability, (B) missing features from an IoT QA perspective. Line numbers refer to plan.md at 461ad16. Findings marked **[verified]** were reproduced with nft 1.0.9 in a throwaway namespace.

> **Status:** B2 decided (per device, D18) and incorporated; WireGuard and routing added as §2.2.1/§2.2.2, M33, M37 (not a review finding). All other findings still open.

## A. Unclear, inconsistent or wrong

### Blockers

| # | Finding | Fix proposal |
|---|---|---|
| B1 | **Observed identity is not a compiler input.** §2.1.1 says kernel state = revision + overlays, but IP-keyed maps depend on leases/neighbors. Identity updates write map elements directly while every apply `flush map`s and re-adds → an apply racing a lease event restores the old IP; verify fails; "recompile from committed revision" cannot build the maps. Undefined: old IP after re-lease to another device; overlays on not-yet-adopted devices. | Compiler input = revision + overlays + observed-identity snapshot. Identity events go through the executor queue and bump the generation; full applies use the latest snapshot. Old IP stays mapped until its conntrack entries end or it is leased to someone else. Hostname-set keys are re-keyed on IP change. |
| B2 | **Rate / queue limit on group, network or global scope is shared.** One id per effective configuration → one netem leaf → "Bad LTE 2 Mbit/s" on a network gives all devices one shared 2 Mbit/s. Id capacity inconsistent (L667 "255 configurations" vs L681 "255 per direction"). Queue limit "delay × rate" undefined without rate. | Decide: per-device (one id per device×config, widen id field) or aggregate (UI says "shared by N devices"). Add `capacity_exceeded`; queue limit from min(rate, link speed, cap) with a memory budget; test with two devices under one rate-limited profile. |

### Important

| # | Finding | Fix proposal |
|---|---|---|
| I1 | **nft apply layout [verified]:** table comment of an existing table is not updated (generation id frozen); `add set/map` with changed type/flags fails the whole transaction ("File exists") → every apply incl. rollback fails; removed objects are never deleted. | Generation in a flushed chain's rule comment or a one-element set; compiler emits explicit `delete` for objects not in target state; versioned names for dynamic sets whose definition changes; verify ignores DNS-derived elements. |
| I2 | **Overlapping CIDRs/port ranges can't coexist in one interval map [verified]**; "explicit priority" and "lift above level" undefined; protocol-only selectors have no map level. (earlier G5) | Compiler flattens overlaps into disjoint pieces; priority semantics defined; protocol-only = ports 0–65535; hostname selectors as ordered rules. |
| I3 | **Precedence contradictions:** rule order decides access, specificity decides fault → UI list suggests otherwise. §2.1.1 "overlays evaluated before configuration" vs §2.4 "within the same level" → a persistent device fault beats a scenario's network "Offline" overlay. Also: **no level for "any source + destination"** (e.g. all devices → broker). | List position matters for access only; UI shows "fault overridden by X". Reword overlay precedence; worked example. Add levels "global + destination/port" before "global". |
| I4 | "One effective fault per direction" lumps together netem, MTU, DNS, TLS, DHCP: a device latency fault would cancel a network "DNS broken" profile. | Resolve precedence per family (netem incl. blackout/flapping, MTU, DNS, TLS, DHCP); profiles are bundles across families. |
| I5 | Fault coverage undefined: global fault also hits gateway's own traffic (apt, DNS upstream, UI in 2-port topology) → admin lockout; DHCP bypasses output hook. | Global = packets whose original source is in a test network; never classify management/own sockets; DHCP/ARP never impaired. |
| I6 | Two-port topology contradicts ownership: one interface is OS-owned management and Chaos-owned uplink with own DHCP client. | In 2-port, OS keeps interface, address and DHCP; Chaos uses it as uplink without ownership. Record under D3. |
| I7 | Policy-routing table contents unspecified (only default route → inter-network traffic would leave via uplink); one PMTU bit = one MTU value; static routes in no milestone. | List table contents (connected routes of all test networks, downstream routes, default via uplink); PMTU tables mirror it; bits as table index (≤ 7 MTU values); static routes in M4. |
| I8 | Access rules vs. redirects: redirected packets go to input, not forward → "drop tcp 8883" + TLS case on 8883 undefined. Redirects affect new connections only. | Evaluate access rules on the original tuple before dstnat; access beats redirect; document new-connections-only, offer cut-existing. |
| I9 | Upload faults for gateway-terminated redirected traffic: IFB flower on dst = gateway misses redirected packets (still carry original dst at ingress); S14 tested download only; IFB on bridge untested. | Flower filters repeating the redirect selectors, or a service namespace (see improvement 1). Spike before M20/M21. |
| I10 | Overlay identity: "same scope, kind, owner → last write wins" — scope undefined; in the §2.10 example step `lossy` replaces `slow` (latency back to 0). `restore` semantics undefined. | Overlay key = (owner, kind, target, selector); steps replace same key; `restore` removes all run overlays; steps may only address the run target. |
| I11 | Applying a revision vs. overlays/runs: deleted objects referenced by overlays, profile edited while active, scenario edited during run, second apply during confirm window, reboot in window. | Orphan handling with event or reject; runs snapshot scenario; one pending confirm (409 `confirm_pending`); reboot in window boots previous revision. |
| I12 | Fault id lifecycle: parameter change → new id → old class deleted with queue; nft and tc not atomic → packets fall into default class; per-fault drop counters not attributable/monotonic. | Stable id per winning fault, change in place; make-before-break (tc classes → nft → delete old after max delay); per-fault named nft counters. |
| I13 | NIC offloads (GRO/GSO/TSO) not handled: loss acts on 64 KB aggregates on real NICs. | Executor disables offloads on owned interfaces; preflight/verify check; H1 measures loss on a real NIC. |
| I14 | Hidden dependencies / untestable tests: M6a tests map updates (maps in M7); M8a tests run abort (runs in M15); M7 counters before rules/faults; H1 scenario timing before M15; M27 needs systemd units from M28; M5b traffic test without API; M6a Debian test needs M5b; M22 needs M17. | Move tests; reject unimplemented overlay kinds with `unsupported_feature`; split H1 into H1a/H1b; minimal units in M5b; `chaosgw apply --file` in M4; declare dependencies. |
| I15 | Control plane reachable from test networks until M29; before setup the UI listens on all interfaces. (earlier M-3) | M4 compiles an input policy (test networks → only DHCP, DNS, ICMP, redirected ports); M5 binds after setup. |

### Minor

- m1 Step timing ±100 ms vs apply ≤ 200 ms on Pi — define "applied" = executor commit timestamp; Pi tolerance from H1.
- m2 Leftovers: run state "aborted (with restored revision)" (C5), "Scenarios are stored as YAML" (C3), §6 #4 numbers (C17), `dhclient` not in image, "After Phase 4: V1 complete" vs V1 definition, "fault optionally time-limited", matrix toggle skips preview; `no_unexpected_destinations` undefined and `capture: true` needs M17.
- m3 Flows only in optional M26, but device view, hostname refresh and M16 checks need conntrack observation → observer in M6a/M7.
- m4 Coexistence: `DOCKER-USER` outside executor scope; classification rewrites marks of every packet (Tailscale 0x40000/0x80000 inside reserved 17–23, wg-quick 0xCA6C) → only test-network traffic, preflight detects mark users.
- m5 Kea version spread and path restrictions (≥ 2.6.3); prefer `config-set`; uplink DHCP client unnamed (G14).
- m6 Service restarts lose pushed overlays; startup ordering; upgrade "defer restart"; USB interface disappearing → degraded, not safe mode.
- m7 API: patch format, base revision in body vs `If-Match`, `target_busy` vs queueing, token scope for runs/reset, session-owned overlays, TTL format, idempotency retention.
- m8 Tests: ARM64 nightly needs ARM64 KVM host; commit-confirm test needs configurable timeout; 99 % CIs make nightlies flaky.

### Improvement ideas

1. **Service namespace** for DNS proxy, TLS responder, mitmproxy: marked packets are policy-routed into it and redirected there → normal egress with fw marks, removes IFB/flower and the output-hook special case (I9).
2. **Normative semantics appendix**: ~20 worked examples = documentation + golden tests (I2–I4, I10).
3. **`explain` endpoint**: `GET /api/v1/explain?device=&dst=&port=` → access verdict, winning fault per family, overridden entries, kernel ids.
4. **Observable generation** (`GET /api/v1/state`, SSE `applied`) so tests wait deterministically.
5. **Capabilities endpoint / feature gating** with `unsupported_feature`.
6. **Two TLS CAs**: distributable test CA and a never-distributed "unknown CA".
7. **Injectable clock** and configurable timeouts for fast, deterministic timing tests.
8. **Statistical test policy**: 99.9 % CIs, retry-once rule, nightly trend storage.

## B. Missing features (IoT QA perspective)

| # | Feature | Real bug it catches | Effort | Placement |
|---|---|---|---|---|
| 1 | **Event-triggered steps** (`after: connection_established{port:8883}`, dns_query, tls_handshake, dhcp_lease, external signal) | ESP32 boot-to-CONNECT takes 3–9 s; a cut at fixed 10 s is random → flaky tests | M | V1 |
| 2 | **NAT / firewall idle timeout and rebinding** (ct timeout per selector; drop/RST/new source port) | MQTT keepalive 20 min vs carrier NAT 5 min → device silently deaf; DTLS session breaks on port change | M | V1 |
| 3 | **Byte-triggered faults** (reset/stall/throttle after N bytes on a flow; `ct bytes`) | OTA without HTTP Range resume, no read timeout → watchdog reboot mid-flash; works without test CA | S | V1 |
| 4 | **Fail first N attempts + retry/backoff checks** | reconnect every 100 ms without backoff → battery drain, rate-limit bans, thundering herd | M | V1 |
| 5 | **HIL integration**: webhook steps (power-cycle relay), device log upload onto run timeline, external verdicts into JUnit | "no recovery after power loss during TLS handshake" | S–M | V1 core |
| 6 | **Resource-budget checks** (bytes, new connections, DNS queries, full vs resumed TLS handshakes) | 40 MB/day on a 5 MB/month SIM after flaky link | S | V1 |
| 7 | **Security/compliance checks**: no plaintext, TLS ClientHello audit, DNS leak, MUD allowlist | evidence for RED/EN 18031 and CRA | M | V1 core |
| 8 | **Cellular/LPWAN profiles** (NB-IoT, LTE-M, 2G) and radio wake-up delay | 2 s CONNACK timeout always hit after sleep | S / M | profiles V1 |
| 9 | **pytest plugin** (fixture, context manager with guaranteed cleanup, pytest-embedded), CI templates, later Robot Framework | fault leaks into following tests | S–M | pytest V1 |
| 10 | **HTML run report** on one time axis; run comparison firmware A vs B with measured values | — | M | HTML V1 |
| 11 | **Seeds and repeated runs** (`--repeat 20`, pass rate) | 1-in-15 reconnect bug at 5 % loss | S | V1 |
| 12 | **Time faults beyond offset**: redirect hardcoded NTP, KoD / stratum 16, NTP unreachable at boot, 2038 preset | cert "not yet valid" at 1970 boot, 32-bit `time_t` | S | move M34 into V1 |
| 13 | Small gaps: **drop IP fragments**, DNS "dead first address", ICMP unreachable type for reject, server FIN vs RST | DTLS with 3 KB chain over fragment-dropping carrier | S | V1 |
| 14 | **Trace replay** (latency/loss/rate time series, periodic patterns) | handover spikes, customer-reported conditions | M | V1.1 |
| 15 | Faults for local/multicast traffic (mDNS, Matter) | local control timeouts | L | later |

Placement changes suggested: M23 DHCP actions mandatory in V1; local mock services V1.1; link-down on own test port in V1.

### Cut or defer candidates for V1

3-way diff / rebase (single admin in V1); priority lifting across levels; 4-release distribution matrix (Raspberry Pi OS missing instead); own uplink DHCP client; gateway-origin traceroute/iperf3; continuous drift detection; UI setup wizard (CLI setup instead), UI matrix editing, rendered Linux view, live Wireshark streaming, NFLOG rule captures; downstream static routes; 250-device performance targets.

### Workflow improvements

1. **Run a scenario from a file** without changing the configuration: `POST /runs` with inline scenario and parameters (`chaosctl run -f x.yaml --set target.device=$DUT`) — the most important CI fix.
2. **CI-ready CLI**: `--wait --junit --artifacts`, exit codes 0/1/2; `chaosctl wait device … --online --connected tcp/8883`; `chaosctl with --profile … -- pytest …`.
3. **Preconditions and false-pass warnings**: baseline checks before the first fault (online, uses gateway DNS, session exists, no foreign overlays); fault/rule with 0 matched packets → report warning (optionally error).
4. **Flight recorder**: rolling per-device buffer (pcap, DNS, flows, events), `chaosctl snapshot esp32-42 --last 10m`.
5. **Save an interactive session as a scenario**, plus symptom templates.
