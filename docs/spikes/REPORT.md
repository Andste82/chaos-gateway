# Chaos Gateway — Phase 0 Spike Report

Status: all spikes executed · September 2026

This report records what the Phase 0 spikes (plan §5) proved, disproved or changed. Every number below comes from the result files in `spikes/results/`. The scripts are in [`spikes/`](../../spikes/), and each one can be re-run on its own (see *Reproducing*).

---

## 1. Summary

| Spike | Question | Verdict | Consequence for the plan |
|---|---|---|---|
| S1 Testbed | Namespace topology with NAT, usable for automated tests? | ✅ confirmed | keep; baseline noise documented |
| S10 Classification | Per-packet classification via conntrack original tuple, mark layout, live changes | ✅ confirmed | design fixed; conntrack-mark caching rejected with evidence |
| S2 Fault topology | HTB + netem per class, per direction, isolation, accuracy | ✅ confirmed, 3 corrections | compiler must always send complete netem parameters; jitter reorders; `replace` does not drop |
| S3 Connection behavior | What happens to established connections on rule changes? | ✅ behavior matrix | default semantics and "cut existing" mechanism fixed |
| S4 TLS | Certificate test cases, transparent interception | ⚠️ plan claim partly wrong | certificate checks can only be told apart with a trusted test CA |
| S5 DNS proxy | Per-client DNS faults, hostname selectors, throughput | ✅ confirmed, 2 gaps | proxy needs DNS over TCP; nft updates must be deduplicated |
| S6 DHCP | dnsmasq vs. Kea for runtime test actions | ✅ decided: Kea | dnsmasq cannot do leases below 120 s |
| S7 Deployment | Docker next to the gateway; gateway in a container | ✅ confirmed constraints | native package stays primary; preflight must handle Docker |
| S8 Performance | Cost of per-packet classification and of updates | ◐ partial (no Pi) | no measurable classification cost on x86; Pi numbers still open |
| S9 Capture | Capture exactly one selector's traffic | ✅ decided | AF_PACKET on the LAN side by default, NFLOG for rule-based capture |

---

## 2. Test Environment

| | |
|---|---|
| Host | Ubuntu 24.04.4, 2 vCPU Xeon 2.8 GHz, 8 GB, Firecracker kernel 6.18.44 |
| Host kernel limits | no `sch_netem`, no `cls_fw`, no `flower`, no WireGuard (minimal kernel) |
| Target kernel | Ubuntu 24.04 `linux-image-6.8.0-142-generic`, booted with QEMU (software emulation, no KVM) via virtme-ng; all modules available |
| Which spikes ran where | netem-dependent spikes (S1, S2, S10) on the **6.8 target kernel**; S3–S9 on the **native host kernel**; S5 and S9 repeated on 6.8 with the same outcome (S9 identical; in S5 the first two queries timed out while the proxy was still starting under emulation, all fault, selector and redirect results matched) |
| Tools | nftables 1.0.9, iproute2 6.1, conntrack-tools 1.4.8, Node.js 22, mitmproxy 11.0.2, dnsmasq 2.91, Kea 2.4.1, Docker 29.4.3 |

**Emulation noise.** The 6.8 kernel runs under software emulation. Baseline RTT through the gateway without faults is 2.3 ms median, and single outliers go up to ~30 ms. All latency tests therefore allow ±(3 ms + 5 %). On real hardware the baseline is ~0.3 ms (measured on the host kernel).

**Not covered:** Raspberry Pi hardware; Debian kernels; real physical NICs.

---

## 3. Results per Spike

### S1 — Testbed

Topology (see [`spikes/lib/testbed.sh`](../../spikes/lib/testbed.sh)):

```
cl1 10.10.0.11 ─┐
cl2 10.10.0.12 ─┼─ switch (bridge) ─ lan0 [gw] wan0 ─ srv 203.0.113.10/.20
                ┘                    10.10.0.1  203.0.113.1  (no route back → NAT required)
```

| | host 6.18 | target 6.8 (emulated) |
|---|---|---|
| Reachability through gateway | ✅ | ✅ |
| Server sees NAT address | ✅ 203.0.113.1 | ✅ 203.0.113.1 |
| UDP RTT median / p90 | 0.30 / 0.44 ms | 2.43 / 3.35 ms |

Lessons for the testbed library:

- Namespace names must be unique per run. Two runs using the same names (host and guest share `/run/netns`) destroyed each other's topology.
- Processes started in namespaces must be killed before the namespace is deleted.
- HTTP proxy variables from the environment must be cleared, otherwise curl bypasses the testbed.

### S10 + S2 — Classification Identity and Fault Topology

**Design under test** (as in plan §3.3):

- **nftables:** a prerouting (mangle) chain classifies **every packet** by the conntrack original tuple: `ct original ip saddr . meta l4proto . ct original proto-dst`.
  - The key is the same for both directions and is not affected by NAT.
  - Verdict maps jump to one chain per class, which writes the class id into mark bits 8–15 (`0x0000ff00`).
- **tc:** each egress interface has an HTB root with a default class and one class per id, each with a netem leaf. A `fw` filter with a mask (`handle 0x0a00/0xff00`) maps the mark to its class.
  - `wan0` carries the upload direction.
  - `lan0` carries the download direction.

| Test | Expected | Measured | |
|---|---|---|---|
| T1 cl1 bidirectional (100 ms up, 50 ms down), behind NAT | 152.4 ms | 154.0 ms | ✅ |
| T1 cl2 unaffected | 2.4 ms | 2.1 ms | ✅ |
| T1 ICMP (device rule, no port) | 152.4 ms | 152.6 ms | ✅ |
| T2 upload only / download only | 102.4 / 52.4 ms | 103.1 / 53.6 ms | ✅ |
| T3 device+port rule beats device rule (TCP 7000) | 42.4 ms | 44.8 ms | ✅ |
| T3 same device, UDP (not in device+port map) → device rule | 152.4 ms | 153.8 ms | ✅ |
| T5 loss 10 % up (3000 pkts) | 99 % CI 8.6–11.4 % | 10.13 % | ✅ |
| T5 loss 10 % up + 5 % down | CI 12.8–16.2 % | 14.53 % | ✅ |
| T6 rate 2 Mbit/s: netem rate / HTB rate | ~2 | 1.90 / 1.90 Mbit/s | ✅ |
| T8 other class while own class is changed 20× | 0 loss | 0 loss | ✅ |
| T9 traffic to the gateway itself via IFB + flower (L3 match) | +100 ms for cl1 only | 102.0 ms / cl2 0.8 ms | ✅ |

**Live change on an existing connection (T4).** A TCP connection sends one message every 50 ms; a fault (+150 ms) is added after 4 s and removed after 8 s.

| Variant | During fault | Effect on the running connection |
|---|---|---|
| per-packet classification | median 154.2 ms | the **first message sent after the change** is affected; removal likewise |
| id cached in conntrack mark | median 2.8 ms | **no effect**; the connection keeps its old class until it ends |

→ The plan's per-packet design is confirmed. Conntrack-mark caching contradicts "faults act on every packet" and is rejected.

Note on the raw result file: `check.py` marks the per-packet variant as failed. Its threshold required the first affected message to be *sent* within 100 ms after the change; under emulation with a 50 ms message interval it was sent after 102.5 ms. The substantive criterion — no message sent after the change was unaffected — held in both directions.

The `nft` command itself took ~500 ms under emulation (process start). Natively it takes 6 ms (S8).

**Follow-up tests** (`s02b`, fixed-rate sender, 1000–1500 packets):

| Test | Result |
|---|---|
| F1 jitter `delay 50ms 20ms` | **712 of 1000 packets reordered**, delay p10–p90 35.7–69.0 ms |
| F1 same + `rate 1gbit` | 0 reordered, but the distribution shifts: median 63.6 ms, p10–p90 55.8–70.0 ms |
| F2 20× `tc qdisc change` with ~40 packets queued | 0 loss |
| F2 20× `tc qdisc replace` (same kind) with ~40 packets queued | 0 loss |
| F3 set `rate 2mbit`, then `change` without rate | still 1.90 Mbit/s — **the rate sticks** |
| F3 `change … rate 0bit` | rate cleared (115 Mbit/s) |
| T10 5 % loss on forwarded vs. gateway-originated TCP | 14.9 Mbit/s, 399 retransmits vs. 12.7 Mbit/s, 408 retransmits |

**Findings**

1. The classification design works as intended, including NAT, both directions, precedence by rule order (most specific first), ICMP and gateway-terminated traffic (IFB).
2. **Sticky netem attributes:** `tc qdisc change` keeps attributes that are not given (proven for `rate`). The compiler must always send the **complete** parameter set, including neutral values (`rate 0bit`).
3. **Jitter reorders heavily.** "Jitter without reordering" is possible with `rate`, but it changes the delay distribution. It becomes an explicit option ("keep packet order") with a documented effect.
4. **`replace` with the same qdisc kind does not drop queued packets**. The plan's risk "replacing qdiscs drops packets" only applies to delete+add or to a change of qdisc kind.
5. **Loss on gateway-originated traffic** behaved like loss on forwarded traffic in this setup (6.8, netem under HTB). The documented netem caveat did not show up. It stays in the test matrix but is no longer a design constraint.
6. **Mark layout decision:** bits 8–15 = effective-fault id (up to 255 fault classes), mask `0x0000ff00`. All other bits stay free for routing, capture and other software.

### S3 — Connection Behavior (established TCP behind NAT)

| Case | Established connection | New connection | Server side afterwards |
|---|---|---|---|
| C0 no change | continues | ok | closed normally |
| C1 drop all matching packets | **stalls** (timeouts) | timeout | still established (half-open) |
| C2 `ct state established accept`, then drop | **continues** | timeout | normal |
| C3 `reject with tcp reset` for all | reset after 38 ms | refused | still established |
| C4 C2 + delete conntrack entry | stalls (mid-stream packet counts as new → dropped) | timeout | still established |
| C5 delete conntrack entry only (NAT flow) | **continues** (NAT re-created transparently) | ok | normal |
| C6 one-shot cut: reset for established packets for 0.5 s, then remove | reset after 37 ms | **ok** | still established |

(`nf_conntrack_tcp_loose = 1`, the default.)

**Decisions**

- The default for access rules is **new connections only** (C2 pattern: established traffic is accepted first).
- *"Also cut existing connections"* is implemented as C6: a time-limited `reject with tcp reset` for established packets of the selector. The device gets an immediate reset and can reconnect.
- The server side stays half-open in C1, C3, C4 and C6, because the gateway only resets toward the device. This is the realistic behavior of a network outage and is documented, not "fixed".
- Deleting conntrack entries alone is harmless for NAT flows (C5). It is not a way to cut connections.

### S4 — TLS

Three client profiles against a Node.js **TLS responder** on the gateway (transparent nftables `redirect`):

| Case (responder) | prod (trusts public CA only) | devfw (trusts test CA too) | insecure (no verification) |
|---|---|---|---|
| untrusted CA | ✗ `unable to get local issuer` | ✓ accepted (test CA trusted) | ✓ accepted → **flagged** |
| expired | ✗ `unable to get local issuer` | ✗ `certificate has expired` | ✓ accepted → flagged |
| not yet valid | ✗ `unable to get local issuer` | ✗ `certificate is not yet valid` | ✓ accepted → flagged |
| wrong hostname | ✗ `unable to get local issuer` | ✗ `hostname mismatch` | ✓ accepted → flagged |
| self-signed | ✗ `self-signed certificate` | ✗ `self-signed certificate` | ✓ accepted → flagged |
| reset after ClientHello | ✗ connection reset | same | same |
| FIN after ClientHello | ✗ unexpected EOF | same | same |
| stall | ✗ client timeout | same | same |

- The SNI is read correctly from the raw ClientHello (`broker.example.com`).
- A completed handshake is logged by the responder. That is how an insecure device is detected.

**mitmproxy (transparent mode, `redirect` to port 8080)**

| Check | Result |
|---|---|
| Production client (trusts public CA only) | rejected (curl exit 60) ✅ |
| Client trusting the mitmproxy CA | traffic passes, original response ✅ |
| Response modification by addon | `MODIFIED BY CHAOS GATEWAY` ✅ |
| Injected status | `503` ✅ |
| TLS key log for Wireshark | written (80 lines) ✅ |

**Findings**

1. **The plan claimed the certificate cases need no device cooperation. That is only half true.** A device that does not trust the test CA reports "unknown issuer" in every case, because that check fails first. Without device trust, the tests can distinguish:
   - untrusted chain
   - self-signed certificate
   - handshake reset, close or stall
   - whether the device accepts a certificate it should reject (the security-relevant check)

   Expiry, not-yet-valid and hostname checks can only be tested **individually** on firmware that trusts the test CA.
2. The Node TLS responder is sufficient for all certificate and handshake cases. Python/mitmproxy is only needed for interception.
3. Transparent `redirect` works for both components; TPROXY is not needed. (Node cannot set `IP_TRANSPARENT` without a native addon. This is not tested; it is simply no longer needed.)

### S5 — DNS Proxy (Node.js)

| Fault for cl1 on `broker.example.com` | cl1 | other name, cl1 | cl2 |
|---|---|---|---|
| NXDOMAIN | NXDOMAIN ✅ | normal | normal |
| SERVFAIL | SERVFAIL ✅ | normal | normal |
| timeout | no answer ✅ | normal | normal |
| delay 800 ms | 803 ms ✅ | 4 ms | 0 ms |
| wrong answer 203.0.113.99 | 203.0.113.99 ✅ | normal | normal |
| TTL 1 | TTL 1 ✅ | 60 | 60 |
| truncated | ✗ client retries over TCP, **proxy has no TCP** | | |

**Hostname selector** (per device; the set key is `device IP . address`):

| Check | Packets counted by the selector |
|---|---|
| cl1 resolves `broker.example.com` (CNAME → CDN edge .20), then connects | 6 ✅ (first SYN already matched) |
| cl2 connects to the same IP without resolving | 0 ✅ |
| cl1 connects to *another* site on the same IP | 6 ⚠️ shared-IP limitation, as expected |

Further results:

- Set update per answer: 8.4 ms, done **before** the answer is sent.
- **Hardcoded resolver** (`dig @8.8.8.8`): redirected and answered by the proxy ✅.
- **DoT** (TCP 853): rejected immediately ✅.

**Throughput** (3000 queries, 50 in flight, native host):

| Setup | Queries/s |
|---|---|
| Upstream directly | 3,424 |
| Proxy | 7,042 |
| Proxy + selector, deduplicated set updates | 6,721 |
| Proxy + selector, one `nft` process per answer | **282** |

(The upstream dnsmasq was the slower component in this run; an earlier run measured ~10,000 for it and ~4,700 for the proxy, so treat these as order-of-magnitude figures.)

**Findings**

1. The proxy must support **DNS over TCP**. Without it the truncation fault is useless and large answers fail.
2. Selector updates must be deduplicated; one `nft` process per answer limits the proxy to ~280 queries/s. For higher rates: one batched or persistent nft session (later netlink).
3. Per-device selector sets work and avoid affecting devices that never resolved the name. Shared IPs remain a documented limitation.

### S6 — DHCP: dnsmasq vs. Kea (no restarts)

| Test action | dnsmasq 2.91 | Kea 2.4.1 |
|---|---|---|
| Lease with router/DNS/NTP options | ✅ | ✅ |
| Short lease (requested 10 s) | ✗ **120 s minimum** | ✅ 10 s / 30 s as configured |
| Delete lease at runtime | ✅ `dhcp_release` | ✅ `lease4-del` (control socket, `lease_cmds` hook) |
| Client renews after deletion | ACK, same IP (lease re-created) | ACK, same IP |
| Change reservation → client renews | **NAK** → DISCOVER → new IP ✅ | **NAK** → DISCOVER → new IP ✅ |
| Mechanism for the change | hosts file + SIGHUP | `config-reload` (the `host_cmds` hook is not shipped in the Ubuntu package) |
| Silence one client | ✅ `ignore` in hosts file + SIGHUP | ✅ `DROP` client class + `config-reload` |

**Decision: Kea** (plan D4). The reasons:

- Short leases are essential for renewal tests.
- Kea has an API for lease operations.

Consequences:

- Reservation changes go through a full `config-reload`. Check whether newer Kea versions provide `host_cmds` in the distribution packages.
- **Deleting a lease does not force a new address**; the client simply renews it. "Force new IP" = change the reservation → NAK → new address (works in both servers).

### S7 — Deployment Constraints

**A. Docker installed on the same host, gateway routing in the host namespace**

| | Routed test traffic |
|---|---|
| Docker's `FORWARD` policy | `DROP` |
| Plain | blocked ✗ |
| Own nftables table with `accept` (priority before Docker) | **still blocked ✗** — an accept in one table does not override a drop in another |
| Accept rule in Docker's `DOCKER-USER` chain | passes ✅ |

**B. Gateway operations from a container with `--network host`**

| Operation | NET_ADMIN + NET_RAW | + SYS_ADMIN | privileged |
|---|---|---|---|
| nftables | ✅ | ✅ | ✅ |
| tc | ✅ | ✅ | ✅ |
| create links, set addresses | ✅ | ✅ | ✅ |
| raw sockets, bind UDP 67 | ✅ | ✅ | ✅ |
| write `ip_forward`, `rp_filter` sysctls | ✗ | ✗ | ✅ |
| create network namespaces (needed for probes) | ✗ | ✅ | ✅ |

**Findings**

1. On hosts with Docker, the gateway must add its own accept rules to `DOCKER-USER`. The preflight check has to detect this and offer the fix.
2. In a container, sysctls must be set on the host (or the container runs privileged), and probes need `SYS_ADMIN`.

The native package stays the primary deployment; the container remains a development and demo option.

### S8 — Performance (native host, x86, partial)

| Configuration | TCP | UDP 64-byte packets/s* |
|---|---|---|
| NAT only | 2,865 Mbit/s | 90,395 |
| + per-packet classification, 1000 device + 1000 device/port entries, no match | 3,099 Mbit/s | 103,262 |
| + same, device matches | 2,863 Mbit/s | 94,898 |
| + HTB with 50 classes on both interfaces | 2,586 Mbit/s | 88,392 |

\* limited by the single-threaded load generator, not the gateway.

| nftables update | Time |
|---|---|
| One element per `nft` process | 6.2 ms |
| 100 elements in one batch | 9.3 ms |
| Rebuild the complete table with 2000 elements | 23.4 ms |

**Findings**

- Per-packet classification has no measurable cost at these rates; HTB with many classes costs ~10 %.
- The plan target "overlay apply ≤ 100 ms" is easily met natively, even with full table rebuilds.
- **Open:** the same measurements on Raspberry Pi 4/5 (plan §3.10) and with netem active on real hardware.

### S9 — Capture

Selector: *device cl1, TCP 8883*. Distractors: cl1 UDP traffic and cl2 on the same port. Ground truth is a capture on cl1's own interface: 161 packets (97 up, 64 down), 40,720 payload bytes.

| Method | Packets | Up/Down | Payload | Foreign packets | Link type | Device identifiable |
|---|---|---|---|---|---|---|
| M1 AF_PACKET on `lan0`, BPF MAC + port | 161 | 97/64 | 40,720 | 0 | Ethernet | ✅ |
| M2 AF_PACKET on `wan0`, BPF port | **258** | 161/97 | 41,440 | includes cl2 | Ethernet | ✗ (after NAT) |
| M3 NFLOG, selector = conntrack original tuple | 161 | 97/64 | 40,720 | 0 | NFLOG (L3) | ✅ |
| M4 nftables netdev `dup` (ingress + egress hooks) | 161 | 97/64 | 40,720 | 0 | Ethernet | ✅ |
| M5 tc `mirred` (u32) | 161 | 97/64 | 40,720 | 0 | Ethernet | ✅ |

All files open with tshark/Wireshark.

**Decision**

- **Device and network captures:** AF_PACKET (tcpdump/libpcap) on the LAN-side interface. Full frames, simplest, exact.
- **"Capture what this rule matches":** NFLOG from the rule's own nftables selector. It is exact regardless of NAT, but L3 only.
- Captures on the uplink cannot be attributed to devices and are offered only as "uplink capture".

---

## 4. Plan Changes

These changes are applied to `docs/plan.md`:

| Plan section | Change |
|---|---|
| §2.4 Faults | per-packet classification confirmed; conntrack-mark caching rejected (S10 T4) |
| §2.5 Fault table | jitter reorders by default; "keep order" option via `rate` changes the distribution |
| §2.6 DNS | DNS over TCP required; selector updates deduplicated/batched; per-device selector keys |
| §2.7 DHCP | Kea; "force new IP" via reservation change + NAK; lease deletion alone does not change the IP |
| §2.8 TLS | certificate checks individually testable only with trusted test CA; without it: untrusted chain, self-signed, handshake faults, insecure-acceptance detection |
| §2.11 Capture | AF_PACKET on LAN side as default, NFLOG for rule-based capture, uplink capture not attributable |
| §3.2 Compiler | always emit complete netem parameter sets (sticky attributes); generate nftables as JSON (text syntax pitfalls: reserved words like `fwd`/`dnat` as chain names, missing `;`) |
| §3.3 Classification | design confirmed; mark layout fixed (bits 8–15); IFB + flower for gateway-terminated traffic |
| §3.4 Preflight | minimal kernels can lack netem entirely; on Ubuntu generic 6.8 all needed modules are in `linux-modules` (not `-extra`); detect Docker `FORWARD DROP` and offer `DOCKER-USER` rule |
| §3.8 Deployment | container needs host-side sysctls and `SYS_ADMIN` for probes |
| §5 Phase 0 | S1–S7, S9, S10 done; S8 open for Raspberry Pi |
| §6 Risks | #3 downgraded (not observed); #4 confirmed with numbers; #6 corrected (same-kind replace keeps queue) |
| §7 Decisions | D4 → Kea; D5 → confirmed (Node TLS responder + mitmproxy sidecar) |

---

## 5. Open Follow-Ups

1. **S8 on hardware:** Raspberry Pi 4/5 and an x86 mini PC with real NICs, with netem active (throughput, CPU, timing precision).
2. **Distribution matrix:** Debian 12/13 kernels and nftables versions.
3. **Kea version:** check `host_cmds` availability and control-socket path restrictions in newer Kea releases.
4. **DNS proxy over TCP** and a persistent nftables session for selector updates.
5. **IPv6:** all spikes were IPv4-only, consistent with V1.
6. **Timing precision** of scenario steps on real hardware (plan target ±100 ms); emulation is not representative.
7. **S7 remainder:** coexistence with netplan/NetworkManager/systemd-networkd on assigned interfaces was not tested (the sandbox has no network manager).

---

## 6. Reproducing

```bash
cd spikes
# spikes that need netem: run in the Ubuntu 6.8 kernel (QEMU via virtme-ng)
./vm.sh s01-testbed/run.sh
./vm.sh s02-s10-classification/run.sh
./vm.sh s02-s10-classification/followup.sh
# spikes that run on any kernel with nftables/conntrack (use a unique TB_PREFIX)
TB_PREFIX=h bash s03-connections/run.sh
TB_PREFIX=h bash s04-tls/run.sh          # mitmproxy part needs /opt/mitm-venv
TB_PREFIX=h bash s05-dns/run.sh          # needs: cd s05-dns && npm install
TB_PREFIX=h bash s06-dhcp/run.sh
bash s07-deployment/run.sh                # needs Docker and the cg-bb image (see spikes/README.md)
TB_PREFIX=h bash s08-perf/run.sh
TB_PREFIX=h bash s09-capture/run.sh
```

Never run a VM spike and a host spike at the same time: `/run/netns` is shared with the guest.
