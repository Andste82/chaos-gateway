# Plan review — 2026-09-29

Independent review of `docs/plan.md` (state after commit "Switch implementation stack to Go backend and Vue 3 frontend") against `docs/spikes/REPORT.md`. Line numbers refer to that version of `plan.md`. Spot-checked: C1, G6, T1, T14, C4 confirmed in the text.

The most urgent issues are five design decisions that are missing or inconsistent, and they touch several milestones:

- **Where faults and rules live:** configuration and revisions versus overlays (G1, G2).
- **What rebuilding the nftables table destroys:** dynamic hostname sets and counters (T1).
- **The direction ambiguity of the mark** once there are several test networks (T2).
- **How probes attach to a test network:** macvlan cannot reach the parent interface's address (T4).
- **Default-route handling** between the OS-owned management interface and the uplink (T5).

## Contradictions / stale

| # | Sev | Location | Problem | Fix |
|---|---|---|---|---|
| C1 | high | §6 #2 (l.1096) "mark at the edge, restore via conntrack mark" | Describes the conntrack-mark caching that §3.3, S10 and the report explicitly rejected. | "classify every packet via the conntrack original tuple (§3.3)". |
| C2 | high | §2.16 "only a small executor process has network privileges" vs §3.4 DNS proxy writes sets via `google/nftables` | Writing nftables over netlink needs CAP_NET_ADMIN, which would put a privilege into the most exposed, device-facing parser. | The executor owns the persistent netlink session and exposes one narrow op ("add elements to set X"). |
| C3 | high | §3.6 `scenarios/*.yaml`, `profiles/*.yaml` beside `revisions/` vs §2.1 "configuration (persistent, revisioned): … profiles, scenarios" | Unclear whether profiles and scenarios are part of a revision; affects rollback, diff, export and locking. | Inside revisions; YAML only as import/export format. |
| C4 | high | §2.14 "overlays are not restored after a restart" vs §3.6 `state/ overlay state` | Overlay state is persisted but never restored; the behavior on an API-process restart (not reboot) with faults still in the kernel is undefined. | On any restart: recompile from the committed revision only, drop overlays, mark runs `aborted`, emit an event. |
| C5 | medium | §2.10 run states vs §2.17 run states | Two different enums; "failed" is ambiguous; "restored revision" implies that runs create revisions. | One enum: `queued, running, passed, failed (checks), error (engine), aborted`. A run removes its overlays; it never restores a revision. |
| C6 | medium | §2.17 rules list with "impair matching traffic" vs §2.4 specificity-ranked faults | The UI implies faults are part of ordered, revisioned rules; §2.4/§2.15 treat them as overlays. | Decide whether persistent faults exist and state their precedence relative to overlays. |
| C7 | medium | §3.2 "overlay changes … never routing or services" | DNS/TLS profiles, DNS faults and DHCP silence do touch services. | "never routing; services only through their runtime control channels". |
| C8 | medium | Phase 0 "all spikes except S8 are complete" | Open criteria: S1/S5 on Pi, S7 network-manager coexistence, S4 TLS 1.2/1.3 and connection reuse, S10 100 ms under emulation. | List open criteria per spike and move them into milestones. |
| C9 | medium | §2.15 TS client "for Jest" vs §3.7 Orval Vue Query hooks | Two generators, or a wrong claim. | Orval also generates a plain fetch client. |
| C10 | medium | §3.4 preflight module list | Missing `cls_flower`, `act_mirred`, `nfnetlink_log`, which §4.5 lists. | One module list, referenced from both places. |
| C11 | medium | §2.9 built-in "DNS broken", "TLS broken" vs M11 before M20/M21 | Two built-in profiles cannot work until Phase 5. | Ship them with M20/M21. |
| C12 | low | §7 D4, D5, D15 decided but listed under "Open Decisions" | Stale framing; D5 still mentions Node. | Split into "Decisions" and "Open decisions". |
| C13 | low | D5 "confirmed (S4): Go TLS responder" | S4 validated a Node responder. | "confirmed (S4, Node); implemented in Go per D15". |
| C14 | low | §2.15 "SSE (or WebSocket)" | SSE is decided. | "SSE". |
| C15 | low | §2.10 `POST /runs/{id}/abort` | No `/api/v1` prefix; abort, lease heartbeat, confirm and reset are missing from §2.15. | Complete the resource list. |
| C16 | low | §1.3 "time (NTP) offset" | Post-V1 (M34) but not marked "later". | Mark "later". |
| C17 | low | "71 % at 2 ms spacing" | The report gives 712/1000 without the spacing. | Cite "712/1000 packets, S2 F1". |
| C18 | low | M23 tests use `dhclient` | Not in the test image; `udhcpc` is. | Use `udhcpc`. |
| C19 | low | §2.5 "Timeout" | Identical to blackout and to a Drop rule. | One concept per mechanism. |
| C20 | low | §3.7 "known from sessile" | Undefined for other readers. | One-line reference. |

## Gaps

| # | Sev | Location | Problem | Fix |
|---|---|---|---|---|
| G1 | high | §2.10 scenario `rule:` step; §2.1 overlay covers faults/profiles only | Rule steps need **rule overlays**, which are never defined. Otherwise every scenario step creates a revision. | Define overlay kinds: fault, profile, rule, DNS fault, TLS case, DHCP action. Each has a TTL, an owner (run/token/lease) and a place in precedence. |
| G2 | high | §2.14 locking, §2.17 "not applied yet", §2.15 preview/apply | No resource model for editing: server-side draft or a revision per `PUT`? What does "reapply mine on top" need? | Candidate model: `POST /revisions` with body + `base`; preview/apply by revision id; ETag/If-Match = revision; overlays last-writer-wins per (scope, owner). |
| G3 | high | §2.3, §2.7 "force a new IP", §6 #13 | Classification is keyed on IP; after a new IP or a new randomized MAC, faults silently stop applying. | Lease and neighbor events trigger an incremental map update; test "fault persists across forced re-addressing". |
| G4 | high | §2.15 | No error model, id rules, pagination, versioning policy, idempotency keys, SSE replay. | API conventions in M5: RFC 9457 problem+json with stable codes, UUID + unique name, cursor pagination, SSE event ids with a replay buffer. |
| G5 | high | §2.4 precedence | Rank of destination/hostname selectors, tie-break between groups, what "explicit priority" compiles to, and how profile + fault on the same scope combine are unspecified. | Full precedence table with worked examples and golden tests; state whether fields merge or the whole configuration is replaced. |
| G6 | high | §2.10 YAML example | **Invalid YAML** (`- at: 0s    profile: normal`); check window "within 30 s" of what? | Normative scenario schema in the OpenAPI spec; check windows relative to named steps. |
| G7 | medium | §2.10 runs, `POST /reset` | Concurrent runs, `queued`, and `reset` wiping another job's overlays are unspecified. | Owner on overlays and runs, `reset?owner=`, a documented concurrency rule. |
| G8 | medium | §2.14 verify | Comparison undefined (`nft -j` normalizes, `tc -j` rounds). | Generation hash in the table comment, set equality, tc parameters within tolerance, service health. |
| G9 | medium | §2.16 auth | Session, CSRF, SSE auth (EventSource has no Authorization header), token storage. | Cookie session + CSRF for the UI, bearer tokens for automation, hashed tokens. |
| G10 | medium | setup wizard vs "listens only on the management network" | First-boot bootstrap undefined. | One-time setup token printed at install; restrict after setup. |
| G11 | medium | §2.2 management "network or interface" | A Pi has one Ethernet port: can management share the uplink? | Define supported topologies (2-NIC with management on the uplink, 3-NIC) and test both at level 2. |
| G12 | medium | §2.6/§2.7 DNS ↔ Kea | Option 6 per network, proxy bind addresses vs systemd-resolved, upstream resolver source, AAAA handling, proxy restart. | DNS/DHCP wiring subsection; M6 test for resolved coexistence. |
| G13 | medium | §2.3 discovery, Kea control socket | How lease events reach chaosgw and who talks to Kea's socket. | Specify mechanism and owner in M6. |
| G14 | medium | M6 uplink DHCP client | Client not chosen; uplink address changes not handled. | Name the client; uplink-changed event triggers a recompile. |
| G15 | medium | §2.2 IPv6 | Uplink RA policy unstated; device-to-device traffic on the same segment bypasses all faults. | `accept_ra=0` on owned interfaces; document intra-network traffic as unimpaired. |
| G16 | medium | §2.3 "devices behind another router" | No static routes in V1, so they are unreachable. | Static routes per network in V1, or move to "later". |
| G17 | medium | §2.8 "device accepts broken certificate → failed" | Firmware trusting the test CA legitimately accepts the "untrusted CA" case; no-SNI fallback unstated. | Device attribute `trusts_test_ca`; fall back to the DNS-proxy name for the IP. |
| G18 | medium | time / clock | A Pi has no RTC; wall time jumps at the first NTP sync. | Monotonic time for TTLs and runs. |
| G19 | medium | §3.1 executor protocol | No version handshake; socket authentication unspecified. | Version handshake + `SO_PEERCRED` check in M3. |
| G20 | medium | §5 | No sizes or target date. | T-shirt sizes and an MVP cut line. |
| G21 | low | §2.11 live Wireshark stream | Mechanism unspecified. | Chunked pcapng over HTTPS. |
| G22 | low | §3.6 retention | No defaults; disk-full behavior during a run. | Defaults + "disk low → stop captures, not the run". |
| G23 | low | §3.9 upgrades | Upgrade during a run; downgrade with newer schema. | Defer restart while a run is active; refuse a newer schema. |
| G24 | low | §2.6 hostname pattern | Syntax undefined. | Exact name or `*.suffix`. |

## Technical risks

| # | Sev | Location | Problem | Fix |
|---|---|---|---|---|
| T1 | high | §3.2 whole-table rebuild vs DNS-filled sets and counters | A rebuild wipes the hostname set elements written by the DNS proxy, races with its updates, and resets every rule counter on each overlay change. Sets cannot be referenced across tables. | Static structure applied atomically; dynamic sets never rebuilt (or re-seeded from the proxy's state); one serialized writer; named counters with offsets. Test: hostname fault survives 10 overlay changes; counters monotonic. |
| T2 | high | §3.3 one id per effective fault, per-interface class | With two test networks, LAN_B egress carries both B's download and A→B upload; one id cannot select asymmetric parameters. | Direction bit in the mark (from `ct direction`) or ids per (configuration, direction); test A→B asymmetric faults. |
| T3 | high | §3.3 classification key | Cannot express destination/CIDR or hostname selectors; "device" means connection initiator. | Full lookup chain (device+dst+port → device+dst → device+port → device → group → network); document initiator semantics. |
| T4 | high | §2.12 probes on a physical-interface attachment | A macvlan child cannot reach the parent's address (the gateway IP). | Test-network address on a Linux bridge (physical port + probe veths); decide in M4; spike first. |
| T5 | high | §3.5 OS-owned management interface + uplink | Two default routes; forwarded or proxy traffic may leave via management, un-NATed and un-impaired. | Policy routing: test-network ingress and proxy traffic use a chaosgw table with the uplink default; in M4. |
| T6 | high | §2.6 honoring DNS TTL for set elements | A long MQTT connection stops matching its hostname fault when the element expires (after 1 s with the TTL-1 fault). | Expiry max(TTL, grace), refreshed while conntrack shows flows. |
| T7 | high | §2.5 MTU/PMTUD via nftables | nft cannot emit "fragmentation needed" with a next-hop MTU. Not spiked. | Policy route with `mtu lock N` for "ICMP on"; `meta length > N drop` for "ICMP off"; spike before M10. |
| T8 | medium | §3.3 gateway-terminated download path | Replies from DNS proxy / TLS responder / mitmproxy are locally generated and unmarked; only the IFB upload path was tested. | Output-hook classification; test download latency through the TLS responder. |
| T9 | medium | §3.3 IFB with flower | A second classification path outside nftables. | Compile flower filters from the same effective policy; include in verify and drift detection. |
| T10 | medium | §2.4 established-accept + blackout as nftables drop | If the fault drop sits after the established-accept rule, blackout does not affect running connections. | Fault drops in the mark chain before access rules, or netem `loss 100%`. |
| T11 | medium | §3.1 executor capabilities | `setns`/netns creation need CAP_SYS_ADMIN (S7); Kea/mitmproxy management unspecified. | List the real capability set or run as root with systemd hardening. |
| T12 | medium | §3.1 "typed operations" | "Apply this nft JSON" is effectively an arbitrary firewall program. | Executor validates that nft JSON touches only `inet chaosgw` and tc only assigned interfaces; fuzz tests in M3. |
| T13 | medium | §1.5 kernel 6.1 / Debian 12 | All spikes ran on 6.8/6.18; Debian's Kea is older; Ubuntu 26.04 LTS is missing from the matrix. | Re-run S2/S10/S6 on Debian 12/13 and Ubuntu 26.04 before M8. Check whether newer Kea packages include the `host_cmds` hook (not verified). |
| T14 | medium | §2.9 "Congested WiFi 30 ms ± 40 ms" | Jitter > delay is clamped at 0 by netem; the median test fails. One-way vs RTT unstated. | Jitter ≤ delay; state "one-way, per direction". |
| T15 | medium | §2.5 queue limits | netem's default limit of 1000 packets causes tail drop at high delay × rate (Satellite). | Compiler computes `limit` from delay × rate. |
| T16 | medium | §3.10 250 faults vs 255-id field | Id space nearly exhausted; doubles with per-direction ids; 250 HTB classes unmeasured. | Wider field or documented cap; measure at 250. |
| T17 | low | M15 ±100 ms timing | Fails under emulation (level 1b). | Timing assertions on KVM/native only. |
| T18 | low | §2.5 Gilbert-Elliott, duplication, corruption, reordering | Not spiked. | Include in the first M10 measurement run across kernels. |

## Milestones

| # | Sev | Location | Problem | Fix |
|---|---|---|---|---|
| M-1 | high | M12 scope vs dependency M11 | Device detail needs flows, captures, DHCP actions, TLS test, diagnose, safe mode and an interface-assignment API from later milestones. | Limit M12 to data available after M11; add panels with their backends; move interface assignment into M4/M5. |
| M-2 | high | M27 tests boot/recovery at level 2 | The level-2 harness arrives in M28, after M27. | Build the level-2 harness earlier. |
| M-3 | medium | commit-confirm, management network | No backend milestone for commit-confirm; management binding only in M29. | Add both to M4/M5. |
| M-4 | medium | M20 hostname selectors for rules | Needs M9. | Add the dependency. |
| M-5 | medium | M21 check type | Needs M16. | Add the dependency. |
| M-6 | medium | scenario step types | DNS, TLS and interception step types have no milestone. | Add them to M20, M21, M22. |
| M-7 | medium | M13 "reapply mine on top" | Needs server-side diff/rebase. | Add a diff endpoint to M5, or reduce the dialog to reload/overwrite. |
| M-8 | medium | M6 too big | Kea, DNS proxy, uplink DHCP, discovery, devices API. | Split into M6a (DHCP + discovery) and M6b (DNS proxy + uplink DHCP). |
| M-9 | medium | M8 too big | tc tree, overlays, TTL, reset, precedence, measurements. | Split into overlays/TTL/reset and netem engine + measurements. |
| M-10 | medium | counters in optional M26 | Counters are core (§2.13). | Per-rule/per-fault counters and flow list in M7–M9; M26 only metrics. |
| M-11 | medium | probes late (M25) but T4 affects M4 | Rework of M4, M7, M17. | Attachment decision in M4; probe spike in Phase 0. |
| M-12 | medium | vague tests in M7, M12, M15, M28, M29 | No concrete pass criteria. | Name them (e.g. M7 via per-class counters; M15 on KVM only; M29 fuzz N minutes nightly). |
| M-13 | low | M19 | Artifacts need M17. | Add the dependency. |
| M-14 | low | M24 UI | Needs M12. | Add the dependency. |
| M-15 | low | Pi hardware follow-up | No milestone; §3.10 and D9 stay open until M28. | Small hardware-validation milestone before the end of Phase 3. |
| M-16 | low | "manual device merge" | No milestone. | Assign to M6 or M12, or drop it. |

## Improvements (prioritized)

1. A **state model** section settling C3, C4, C6, G1, G2, G7: what is revisioned, overlay kinds (incl. rule overlays), owner and lease, restart semantics, precedence.
2. An **nftables layout for dynamic data** (T1): static structure applied atomically, dynamic sets and counters preserved, one serialized writer (also fixes C2).
3. A **direction bit in the mark and an extended classification key** (T2, T3); re-run S10 with two LANs and a destination selector.
4. **Attachment and routing model in M4**: test networks on a bridge (probes, later VLANs) and policy routing for uplink vs. management (T4, T5).
5. **Event-driven classification** on lease and neighbor changes (G3).
6. **API conventions** in M5 (G4, G9).
7. **Three small follow-up spikes** before Phase 2: PMTUD via route MTU (T7), output-hook classification for proxy replies (T8), bridge-attached probe reachability (T4).
8. **Level-2 VM tests and a Pi hardware check earlier** (M-2, M-15).
9. **Verify** as hash plus normalized comparison (G8).
10. **Sizes and an MVP cut** (G20): M1–M11 + M15 + M18 first, UI after.
