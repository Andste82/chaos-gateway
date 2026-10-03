# Open items, M0 to M6a

Date: 2026-10-03. Status checked against `main` at commit `2d3ad34c0ebca891ffeea71b9bc00cbfdc0fc195` (M6a merged).

This file lists what was left open or deferred from milestone M0 to M6a and is **not** scheduled in a later milestone of `docs/plan.md` §5 (including "After V1"). Items that a later milestone does cover are only listed in the last section.

How it was compiled:

1. Every finding, spec gap, deviation, known limit and "not done" remark was extracted from the independent review of each milestone (M1, M2, M3, M4, M4b, M4c, M5, M6a), from the merged PR descriptions, from `docs/development.md`, `docs/spikes/REPORT.md` and `docs/reviews/`. There was no separate review for M0 and M5b; both were compared against the plan directly.
2. Each item was checked against the code on `main`. It counts as fixed only where the fix or a test for it was found in the code; fixed items are not listed here.
3. Each remaining item was checked against the milestone list of `docs/plan.md`. If a later milestone names it, it moved to "Covered later".

Severity: high = wrong or unsafe behaviour likely in normal use; medium = real gap or risk with a workaround or a narrow trigger; low = hardening, test gap, documentation or nit. No open item is rated high.

Pure style nits and statements about what could not be run in review were left out. Some items were reported in two milestones and are listed once, under the earlier one.

## M0 (spikes, devcontainer, plan and docs)

| Title | Description | Where | Sev | Why not covered later |
|---|---|---|---|---|
| Nightly kernel matrix missing | Plan §4.4 promises nightly level 1b on Ubuntu 24.04 and 26.04, GA and HWE kernels, and an ARM64 level 1b run. `nightly.yml` has only `fuzz` and `appliance`; the devcontainer has GA kernels only; the arm64 CI job is an image build plus qemu-user unit tests, no arm64 VM. | `.github/workflows/nightly.yml`, `ci.yml`, `.devcontainer/Dockerfile`, plan §4.4 | medium | M10 tests "on the kernels of the distribution matrix" but no milestone owns building the matrix infrastructure. |
| Q1 evidence outdated | Plan §7.2 (Q1) says level 2 was "not yet tried". A nightly run on a hosted KVM runner has since passed the level 2 smoke test for 24.04 and 26.04. Q1 stays undecided in the text. | `docs/plan.md` §7.2, `docs/development.md` | low | Q1 is a decision, not a milestone task. |
| README status stale | README still says implementation starts with M1. | `README.md` | low | No milestone owns doc status. |
| Spike report section 5 stale | Items on Debian 12/13 (dropped by D1), DNS over TCP (now in plan) and the Kea check are still listed as open. | `docs/spikes/REPORT.md` section 5 | low | Doc hygiene. |
| Devcontainer rebuild never verified | `docker build .devcontainer` / `./start.sh rebuild` was unchecked in PR 1 and PR 3 (no Docker daemon in the dev container); no evidence it was built on the VPS. | `.devcontainer/` | low | Nothing schedules it. |
| ESP32 QEMU fork not evaluated | Plan §4.5 says the Espressif QEMU fork "has to be evaluated first" for firmware in levels 1 and 2. No spike or milestone owns it. | `docs/plan.md` §4.5 | low | No milestone. |

## M1 (repository, CI, testbed)

| Title | Description | Where | Sev | Why not covered later |
|---|---|---|---|---|
| KVM mode of CI not asserted | The unprivileged KVM CI step is best effort (`|| true`); a runner silently falling back to emulation is visible only in the log. | `.github/workflows/ci.yml` | low | Q1 is only about where KVM tests run. |
| Netem test tolerances | `TestNetemDelayIsVisible` asserts unaffected paths are within reference + 20/25 ms; this can flake under TCG emulation. | `internal/testbed/topology_integration_test.go:184,191` | low | Nothing in the plan on test flakiness. |
| Runner does not return the tests' exit code | `testvm` in VM mode returns 0, 1 or 2; plan M1 says "returns the tests' exit code". Documented deviation. | `tools/testvm/main.go`, `docs/development.md:78` | low | No later milestone. |
| Leaked namespaces after crashed direct run | `Process.Wait` can hang on a grandchild; namespaces of a crashed or timed-out level 1 run are never swept. | `internal/testbed/testbed.go` | low | M27 is product recovery, not the testbed. |
| Skipped tests count as passes | `Summary.OK()` treats SKIP as pass; a skipped testbed test is not flagged (zero tests is flagged). | `internal/testbed/vmrun/results.go` | low | No later milestone. |
| No tests for runner flags and real VM path | No tests for `testvm` `-kernel`, `-keep`, `-vm-timeout`, `-v`; nothing cheap covers `HasKVM`, the `script` wrapper and the real exit-code path. | `tools/testvm/main_test.go` | low | No milestone owns it. |

## M2 (domain model, persistence)

| Title | Description | Where | Sev | Why not covered later |
|---|---|---|---|---|
| Retention and `Prune` not wired | `Store.Prune` (revisions and stale candidates) is never called outside tests; `settings.retention.revisions` is not connected. The PR said "wire up in M5"; it did not happen. Revisions and candidates grow without bound. | `internal/store/store.go:616`, `internal/engine`, `internal/api` | medium | Plan §3.6 names only the 200-revision default; no milestone schedules the wiring. |
| Matrix and timer rules undocumented | Rules not in the spec text (hold >= 3x keepalive, OSPF dead > hello, `matrix_self_entry`, `duplicate_matrix_entry`, BGP neighbor = link peer, lease/TTL >= 1 s, `duplicate_protocol`) are only listed in the PR text. | `internal/domain/validate_rules.go`, `validate_network.go` | low | M30 user guide is generic. |
| Config address beats lease: undocumented | "Explicit configured address beats a lease" is stronger than §2.3 and was never confirmed with the maintainer. | `internal/domain/observed.go:100-110` | low | Decision never recorded. |
| Reserved routing tables hard-coded | The range 100-110 is a hard-coded comment, not derived from the compiler constants. | `internal/domain/validate_rules.go:93-102` | low | Harmless; nothing scheduled. |
| `NewWorld` accepts un-normalized config | `Create` normalizes now, but `NewWorld` has only a comment, no guard. | `internal/domain/resolve.go:67` | low | No milestone. |
| `Store.get` lax decoding | Decodes with non-strict `json.Unmarshal`, no re-validation; `Configuration.schema_version` not checked on read (checksum guards the file). | `internal/store/store.go:287` | low | No milestone. |
| Missing edge tests | `<`, `&`, `>` pinned through the store checksum; /31 link and hub /30 with several clients; nested clients in the diff. | `internal/store`, `internal/domain` tests | low | Test gap. |

## M3 (executor)

| Title | Description | Where | Sev | Why not covered later |
|---|---|---|---|---|
| No `uidrange` selector | The executor `Rule` has no `uidrange`; the compiler (M4) emits no rule for the service user. `docs/development.md` says M7, but plan M7 does not list it, and plan §2.2.2 still mentions it. Possibly superseded by the service namespace (D29, M6b). | `internal/executor`, `internal/compiler`, plan §2.2.2, `docs/development.md:208` | low | No milestone names it; decide whether to drop it from the plan. |
| tc scope has no uplink rule | `AssignInterfaces` accepts any non-loopback, non-Docker name, including the management NIC; scope is as strong as the API client. No test for the uplink case. Documented as trust decision. | `internal/executor/validate.go:690`, `docs/development.md:140` | low | Accepted trade-off, nothing scheduled. |
| `FuzzFrame` scope | Covers only `Server.serve`, not the codec, the handshake or `maxLine`. | `internal/executor/fuzz_test.go:141` | low | M29 names long fuzz runs on operations only. |
| Parser test data abridged | `internal/linux/testdata` outputs are partly hand-trimmed (e.g. `tc_class_htb.json` has `dev` on the first entry only). | `internal/linux/testdata` | low | Test-data nit. |
| Executor generation not persisted | The generation counter starts at 0 after an executor restart. | `internal/executor/exec.go:57` | low | No milestone mentions it. |
| `Serve` goroutine leak | The context goroutine can leak on a non-cancel `Accept` error; an accept racing cancellation can hang `wg.Wait`. | `internal/executor/server.go:81-119` | low | Nit. |
| tc tokens allow `/` and `..` | The token regex allows path-like tokens (e.g. `netem distribution a/../b` reads `<tclib>/a/../b.dist`). Read-only, low risk. | `internal/executor/validate.go:41` | low | Low risk. |

## M4 (compiler, apply, engine)

| Title | Description | Where | Sev | Why not covered later |
|---|---|---|---|---|
| Management endpoint vs spec | With explicit `allowed_sources` the interface subnet is not included, while the API description says "allowed_sources and the interface subnet". | `api/openapi.yaml:2560`, `internal/compiler/compile.go:301-321` | low | Spec/code mismatch nobody tracks. |
| `FirstV4()` may flap | Host resolution takes the first address; with several addresses on the uplink it can change between reads. | `internal/compiler/host.go:88` | low | Nit. |
| Direct apply without rollback | `chaosgw apply --file` without `--state-dir` leaves the kernel as a failed apply left it. Documented. | `cmd/chaosgw/apply.go`, `docs/development.md:210` | low | Documented limit; M27 covers the daemon only. |
| Preview-equals-apply test is indirect | Tested by hash and empty plan, not by comparing the previewed plan with the executed plan. | `internal/engine/engine_test.go:712` | low | Test gap. |
| Lockout rollback test is loose | Accepts either `"22,8443"` or `"8443"` as proof that the new port is in the kernel. | `internal/engine/engine_test.go:318` | low | Test gap. |
| No rename and failed-restore tests | No test for a network rename or for a restore that itself fails. | `internal/engine`, `internal/apply` tests | low | Test gap. |

## M4b (WireGuard)

| Title | Description | Where | Sev | Why not covered later |
|---|---|---|---|---|
| No real-kernel test of NAT via uplink | Masquerade of client and link hosts towards the uplink and the return path are compile-tested only; the testbed server is on-link. | `internal/engine/integration_wg_test.go` | medium | The M4b/M5 test lists are closed and do not name it. |
| Plan §4.2 describes old topology | Plan says WireGuard is in the default topology with an "internet" router; code uses the opt-in `WithRemotes`, client network is a /32 on `lo`. | `docs/plan.md:1111`, `docs/development.md:258,303` | low | No milestone amends §4.2. |
| PSK changes verified by presence | A regenerated preshared key without key rotation goes unnoticed by verify. Documented. | `internal/apply/plan.go` (`wgMismatch`) | low | Documented limit. |
| Peer-online events on restart | The first poll after an engine restart announces every online peer (intentional per comment). | `internal/engine/wireguard.go` | low | Arguably by design. |

## M4c (BIRD)

| Title | Description | Where | Sev | Why not covered later |
|---|---|---|---|---|
| BIRD outage fails every apply | While the daemon is down, `planBird` re-plans and `birdc configure` fails, so unrelated applies and rollbacks fail too. Documented. | `internal/apply/bird.go`, `docs/development.md:299` | medium | M27 covers executor kill and safe mode, not BIRD down. |
| No route-change events | Only per-protocol counts and `routing_session_changed` exist; no per-route change events (§2.2.2). | `internal/engine/routing.go`, `internal/bird/status.go` | medium | Only plan §2.2.2 mentions it. The received-prefix list is covered by M14. |
| Max-prefix disable never recovers | After `import limit ... action disable` the protocol stays disabled until the config changes; no op, flag or distinct event. | `internal/engine/routing.go`, `docs/development.md:299` | medium | Documented limit, no milestone. |
| Preview lacks effective route | §2.2.2 says preview shows the effective route for a destination; not implemented. | `internal/engine/api.go` (`Preview`) | low | No milestone. |
| External mode unverified | Export via `source ~ [RTS_PIPE]` is untested with a real BIRD; if wrong, external mode exports nothing. | `internal/bird/render.go` | low | Documented limit. |
| Babel not tested end to end | No session test; nothing keeps IPv6 link-local on WG interfaces (Babel uses ff02::1:6); input rule is `udp dport 6696` in the inet table. | `internal/compiler/rules.go`, `docs/development.md:301` | low | Documented limit. |
| `AllowDefault` with allowed list | The final `reject` still drops 0.0.0.0/0 unless it is in the allowed list; semantics undocumented. | `internal/bird/render.go` | low | No milestone. |
| Routing input rules lack source match | BGP (`tcp 179`) and OSPF (`ip protocol 89`) accepts have no neighbor address restriction. | `internal/compiler/rules.go` | low | M29 hardening review does not name it. |
| Learned routes not in PMTU mirror tables | Only table 100 is fed; tables 101-110 are not. | `internal/bird/render.go` | low | M10 scope does not mention it. |
| No OSPF filter test | The no-allowed-list variant exists for BGP only. | `internal/engine/integration_bird_test.go` | low | Test gap. |
| No routing rollback / weak config-change test | No rollback or confirm-timeout case for routing; the "no session reset" test only changes the announce set. | `internal/engine/integration_bird_test.go:303-330`, `routing_test.go` | low | Test gap. |
| Small BIRD leftovers | `Preview` hard error and `"bird: "` substring match; `check` op creates a temp file but `Mutates()` is false; `EnsureBirdConfig` overwrites on any `Stat` error; `routingStatus` republishes every poll; idle text uses `OwnTableFirst` (executor) vs `PolicyTable` (apply), both 100; verify checks nothing when routing is off. | `internal/engine/api.go:136`, `internal/executor/exec.go:638-641`, `internal/apply/bird.go:18` | low | Nits. |

## M5 (REST API)

| Title | Description | Where | Sev | Why not covered later |
|---|---|---|---|---|
| SSE has no resync signal | Event ids are per boot; a `Last-Event-ID` older than the buffer or from another boot replays silently, the client is not told it missed events. | `internal/engine/events.go:61`, `internal/api/events.go` | medium | M12 only says "live updates via SSE". |
| SSE events lack actor/subject; discard emits nothing | `publicEvent` has neither field; only `revision_created` and `revision_applied` come from the API. | `internal/api/events.go` | low | No milestone. |
| Setup without commit-confirm | Spec says setup applies with commit-confirm; code applies normally and a `pending_confirm` result would leave setup complete. | `api/openapi.yaml:145`, `internal/api/system.go` | low | M12 covers the wizard UI only. |
| Loopback always bound | On host networking any local process reaches the API (used by the health check). | `cmd/chaosgw/api.go` (`listenAddrs`) | low | Deliberate; M29 names no such item. |
| Audit retention | Trimmed only at process start (100000 entries); no time-based retention (plan §3.6: 1 year); every append fsyncs. | `internal/audit/audit.go` | low | No milestone. |
| `auth.refresh` mtime-only | Detects changes by mtime and size; no lock between CLI and API; a stat per authenticated request under the global mutex. | `internal/auth/auth.go:172` | low | No milestone. |
| Body limit gives 400 | An oversized body is 400 `bad_request` with a Go error string, not 413. | `internal/api/helpers.go:106`, `middleware.go:64` | low | No milestone. |
| No server read timeout | The binder sets only `ReadHeaderTimeout` and `IdleTimeout`. | `internal/api/binder.go:75` | low | M29 names login rate limiting only. |
| Wrong method gives 400 | `NoMethod` answers 400 instead of 405. | `internal/api/server.go:96` | low | No milestone. |
| Unauthenticated health does executor round-trip | Each call runs an executor read (3 s timeout), unlimited. | `internal/api/system.go:160` | low | No milestone. |
| Binder log spam | A warning every 2 s per unbindable address. | `internal/api/binder.go:72` | low | Nit. |
| `bus.publish` copies the log | Copies the slice on each publish while trimming. | `internal/engine/events.go:94` | low | Efficiency nit. |
| WG export audited late | Audit entry is written after the key was consumed and the body sent. | `internal/api/exports.go:116-122` | low | No milestone. |
| `reset-password` not audited | No `--data-dir`, so no audit entry. | `cmd/chaosgw/admin.go` | low | No milestone. |
| `reset-password` has no prompt | Needs `--password-stdin` or `--password-file`; plan §2.16 shows the bare command. | `cmd/chaosgw/admin.go` | low | No milestone. |
| Cookies not `__Host-` prefixed | Session and setup cookies are plain names. | `internal/api/middleware.go:19` | low | M29 does not name it. |
| Contract harness validates responses only | Requests and SSE bodies not validated; `additionalProperties` open; YAML and SVG skip the check. | `internal/api/harness_test.go` | low | Test gap. |
| Missing API tests | No tests for 413, ignored `X-Forwarded-For`, CORS/Origin posture. | `internal/api` | low | Test gap. |

## M5b (appliance VM harness)

| Title | Description | Where | Sev | Why not covered later |
|---|---|---|---|---|
| Anti-lockout assertion is a no-op | The smoke test comment says management access survived the apply, but the check is `must("true")`. | `internal/appliance/appliance_test.go` (end of smoke) | low | Test gap. |
| Cloud image trust | Images are verified via SHA256SUMS fetched over HTTPS; the SUMS signature is not verified. | `internal/appliance/images.go` | low | No milestone. |

## M6a (DHCP, discovery)

| Title | Description | Where | Sev | Why not covered later |
|---|---|---|---|---|
| Conntrack is polled, not event-driven | Plan §2.3 and M6a scope call for a flow observer on conntrack events; the engine does a full `conntrack -L` every second and on every `GET /flows`. | `internal/engine/observe.go`, `internal/executor/exec.go` | medium | M6a scope item that was not delivered; no later milestone names it (M26 only consumes flow data). |
| `nf_conntrack_acct` never enabled | Flow packet/byte counters are always 0 and omitted. | repo-wide (no sysctl set), `internal/engine/observe.go` | medium | No milestone enables it; M26 flow view depends on it. |
| Flow and device fields missing | Flows lack `started_at`; devices lack `upload_bps`, `download_bps`, `flows_active`. | `internal/engine/observe.go`, `internal/api/devices.go` | medium | M26 uses "flow data from M6a"; no milestone adds the fields. |
| ICMP flow ids collide | Flow id is `proto/src:sport>dst:dport`; the ICMP `id=` is not parsed, so concurrent ICMP flows share an id and break cursor paging. | `internal/engine/observe.go` (`flowID`), `internal/linux/conntrack.go` | low | No milestone. |
| Conntrack read unbounded | Executor output cap is 16 MiB (about 80k entries); a failed read is only logged at debug and treated as "no connections" (last active set is kept, but no signal). | `internal/executor/runner.go:44`, `internal/engine/observe.go` | medium | No milestone. |
| Discovery `sources` values limited | `config`, `wireguard`, `probe` are never produced; configured devices have no sources. | `internal/engine/devices.go` | low | No milestone. |
| Identity update runs full read and verify | After the element ops every identity update does `ReadState` plus `Verify` of the whole target; drift anywhere falls back to a full apply; each update bumps the generation and emits `applied`. Event noise on busy networks. | `internal/engine/applyloop.go:121-125`, `owner.go` | medium | M7 moves identity to maps but does not mention verify cost or event volume. |
| Kernel generation marker not updated | Identity updates leave the `generation` chain at the last full apply, so API generation and kernel marker diverge (plan §2.14 expects the marker to hold the generation). | `internal/engine/applyloop.go:120` | low | M7 test only requires map follow-up within 1 s. |
| New device triggers full apply | `identityOps` returns not-incremental when the set count differs, so each new device means a full ruleset rebuild; a LAN host spoofing MACs can force one per poll (registry bound 1024, kept 24 h). | `internal/engine/applyloop.go:48,134`, `devices.go` (`maxDiscovered`) | medium | M7 (maps) may replace the per-device sets but does not say so. Re-evaluate when M7 is designed. |
| Devices known only from conntrack | Hosts seen only as conntrack sources become address-only entries without MAC; they stay as address-less registry entries when later seen by MAC. | `internal/engine/devices.go` | low | No milestone. |
| One bad DHCP option breaks all networks | Custom options are validated only for duplicate codes; a code colliding with a named option or bad data makes Kea reject the single `config-set` for every network. The apply succeeds with only `DHCPError` set. | `internal/compiler/dhcp.go`, `internal/domain/validate_network.go` | medium | M23 covers option changes as test actions, not validation. |
| Only the first MAC is reserved | A device with several MACs gets a Kea reservation for the first MAC only, silently. | `internal/compiler/dhcp.go:84-85` | low | No milestone. |
| `lease4-get-all` unpaged | All leases are fetched every second; large pools mean large payloads. | `internal/kea/client.go:170` | low | No milestone. |
| Identity resolved against committed config only | `owner.observed` resolves with the committed configuration while desired state compiles the current or pending one; new devices of a pending or just-committed revision get empty sets until the next poll after commit; nothing triggers an observation on commit or confirm. | `internal/engine/owner.go:654` | medium | No milestone. |
| Online ignores leases | A device holding a lease but sending nothing counts as offline. | `internal/engine/devices.go` (`online`) | low | No milestone. |
| No dedicated merge, limited WG discovery | Manual merge exists only as adding a MAC to a configured device (no merge API, no overlay migration); hosts behind tunnels are found only from conntrack within peer routes shorter than /32; WG peer online changes arrive via the 5 s poll, not within 1 s. | `internal/engine/devices.go`, `internal/engine/wireguard.go` | low | No milestone. |
| No test for `observer.WatchNeighbors` | The netlink neighbor watcher has no test (only `Watch`). | `internal/observer/netlink_test.go` | low | Test gap. |
| Burst test does not assert one update | The testbed burst creates 40 devices (the full-apply path) and asserts at most 3 generations, not one incremental update. | `internal/engine/integration_dhcp_test.go:317-370` | low | Test gap. |
| Executor priority only unit-tested | The engine issues identity ops from one goroutine, synchronously, so the priority class is exercised only by the executor unit test. | `internal/executor/exec_test.go:666`, `internal/engine/applyloop.go` | low | M7 may add concurrent callers; no test scheduled. |
| Kea hook: no rate limit | Kea forks one `chaosgw` process with a TLS handshake per lease event; a DHCP flood with random MACs causes a fork storm (`sync: false`, unbounded). | `cmd/chaosgw/keahook.go`, `deploy/chaosgw-kea-hook` | medium | M29 hardening names login rate limiting, not this. |
| Service token file mode 0644 | `EnsureServiceToken` writes the token 0644 in a 0755 directory; it is never rotated or expired. Scope enforcement itself is correct. | `internal/auth/auth.go:411-420` | medium | M29 "secret storage" is generic and does not name it. |
| Hook API address default | The hook defaults to `https://127.0.0.1:8443`; `CHAOSGW_API` overrides it (set in `compose.kea.yaml`). Failures are visible only in Kea's log and are masked by the 1 s poll; `InsecureSkipVerify` with a bearer token to loopback lets a local listener that binds the port first read the token. | `cmd/chaosgw/keahook.go:34-35` | low | No milestone. |
| Kea version not pinned | The Dockerfile installs the distro `kea-dhcp4-server` unpinned; plan M6a, S6 and risk 25 promise a pinned version (spikes ran 2.4.1, devcontainer has 3.0.3). | `deploy/Dockerfile:31`, `.devcontainer/Dockerfile:39` | medium | M6a promised it; M28 does not name pinning. |
| Kea container path untested | Directory permissions of `/var/lib/kea` and `/run/kea` in the real image, `kea-start.sh` and the real hook binary with a real token are never exercised (the testbed does not use the compose file); `DAC_OVERRIDE` was added but the effect is unverified. | `deploy/compose.kea.yaml`, `deploy/kea-start.sh` | medium | M28 level-2 smoke may cover it but does not name Kea. |

## Covered later

| Item | Milestone |
|---|---|
| M1: container image has no non-root user (`deploy/Dockerfile`) | M29 |
| M2: overlay `expires_at` / `lease_expires_at` not set by `NewOverlay` | M8a |
| M3: DNS set updates spawn `nft` per call, no persistent netlink connection | M20 (plan §3.4) |
| M4: verify matches rules by comment hash only (expressions not compared) | M38 drift detection (partial) |
| M4: Docker started after the last apply is not noticed (`DOCKER-USER`) | M38 drift detection (partial) |
| M4c: received-prefix list in routing view | M14 |
| M5: certificate loaded once at start, SANs fixed (`/system/certificate`) | M29 |
| M5: `lockout_protected` never produced | M9 |
| M5: `capacity_exceeded` never produced | M8a, M10 |
| M5: `target_busy` never produced | M15 |
| M5: `?force=` ignored, `references[]` undefined | M8a |
| M5: IPv6 management addresses never bound | M32 |
| M5: `counter_epoch` always 0 | M8b |
| M5: capabilities lists (`overlay_kinds`, `fault_families`, `step_types`) empty | M8a, M15 |
| M5b: netplan hints are static text | M28 |
| M5b: only the executor is deployed, x86-64 only | M28 |
| M0: S8 on real hardware (HTB with 500 classes, 50/250 faults, offloads, loss) | H1 |
| M0: S1 testbed on Raspberry Pi | H1 |
| M0: DNS proxy at least 1000 queries/s on Pi | H1 |
| M0: scenario step timing on real hardware | H1, M15 |
| M0: S7 coexistence with netplan, NetworkManager, systemd-networkd | M28 (no explicit test named; consider adding one) |
| M0: S4 TLS 1.2 vs 1.3, connection reuse, control from the core | M21, M22 |
| M0: IPv6 never spiked | M32 |
| M0: DNS over TCP and persistent nft session (now in plan, code pending) | M6b, M20 |
| M0: `image.yml` has never run (ghcr push, release tags) | M28 |
| M0: persistent faults for DNS, TLS, DHCP and profile activations (D31) | M39 |
| M0: three-way merge, NFLOG capture, extra diagnostics, drift detection, .deb (D28) | M38 |
| Stubs: `chaosgw dns` / `chaosgw tls`, `chaosctl` beyond `version` | M6b, M21, M18 |
| Image holds only `chaosgw`/`chaosctl` and executor tools; full image with Kea, tcpdump, mitmproxy | M28 |
| WireGuard endpoints in matrix, `nft_add_elements` per-call spawn, identity maps and faults not compiled yet | M7, M6b, M8a |
| `?force` / `references[]`, `lockout_protected`, `capacity_exceeded`, setup without confirm window (M5 deviations) | M8a, M10 |
| `expires_at` of overlays and secrets store (M2 "not done") | M8a, M4b (done) |
| Supervisor helper, overlay removal on stop, reader pool and time stamps (M3 deferrals) | M8a |
