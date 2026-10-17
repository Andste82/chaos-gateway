# Audits and open items

This file holds the audits of the phases of `docs/plan.md` §5, what they left open, and the open items of each phase:

- **Phase 1** (M1–M6b): audited on 2026-10-04, closed on 2026-10-05. The result, the decisions and the
  per-milestone verdicts follow directly below.
- **Phase 2** (M7–M11): audited on 2026-10-12, its items closed on 2026-10-12. The result is in
  [Phase 2 audit result](#phase-2-audit-result), right before the Phase 2 open items; the items the audit
  found, P2-AUDIT-01 to P2-AUDIT-16, and P2-AUDIT-17, which the work on them found, are closed (table
  "Closed after the audit (2026-10-12)" under [Phase 2 open items](#phase-2-open-items)). One Phase 2 item
  stays open: P2-M9-06, a hardware measurement for H1.
- **Phase 3** (M12–M14): audited on 2026-10-14, its items closed on 2026-10-15, merged to `main` on 2026-10-15 (PR #56,
  with the follow-up PR #58; CI run 38744189903 on `16824f8` green). Phase 3 is complete. The result is
  in [Phase 3 audit result](#phase-3-audit-result), right before the Phase 3 open items; its items P3-AUDIT-01 to
  P3-AUDIT-36 are fixed (CI run 38646886557 on `aeeeed3` green), P3-AUDIT-37 (the device detail's path view, → M26)
  and P3-AUDIT-38 (a management hub in the overview topology, fixed) were decided by the maintainer on 2026-10-15, and
  the four choices the fixes made, listed there, were confirmed on 2026-10-15. What the implementation of a milestone could not settle is
  under [Phase 3 open items](#phase-3-open-items), at the end of the
  file; the maintainer decided those items on 2026-10-14 and every one is closed except P3-M12-05 (the recovery
  instructions the safe-mode banner links to), which waits for M27/M30. The choices the audit's fixes made follow the
  items ("Phase 3 audit: choices made in …").
- **Phase 3.1** (M35a): implemented on the branch `phase-3-1-port-forwarding`, PR pending, not yet audited. What the
  implementation could not settle is under [Phase 3.1 open items](#phase-31-open-items), at the end of the file:
  P31-M35a-01 to P31-M35a-10, each open for the maintainer's decision with the option that was implemented.

## Phase 1 audit result

Date: 2026-10-04. Audited against `main` at `c4d51d3` (M6b merged) and `docs/plan.md` §5 Phase 1 (M1–M6b).
Closed: 2026-10-05, work package 12.

**Phase 1 is complete.** Every milestone delivers its scope, every test the plan lists exists and
passes, and every item this audit found — all 11 "Suggested work packages" plus the 13 small items
decided on 2026-10-04 but never bundled into one (work package 12: one-line doc/spec edits, CI action
version bumps, a scheduled-nightly confirmation, and three small real gaps — `internal/linkexport`'s
unit tests, the anti-lockout check's SSH connection, and the cloud-image signature check) — is
closed. CI on `main` is green.

| Milestone | Verdict |
|---|---|
| M0 (spikes, docs) | done |
| M1 Repository, CI, testbed | done |
| M2 Domain model, persistence | done |
| M3 Executor | done |
| M4 Compiler, preview, safe apply | done |
| M4b WireGuard | done |
| M4c Dynamic routing | done |
| M5 REST API | done |
| M5b Appliance harness | done |
| M6a DHCP, devices | done |
| M6b DNS, service namespace | done |
| Cross-cutting | done |

### Why items stayed open

Every item carries a reason:

| Reason | Meaning |
|---|---|
| `forgotten` | Simply not done, although the plan or the spec asks for it. |
| `partial` | Started but incomplete. |
| `test-gap` | The code exists, but the test the plan asks for is missing or weak. |
| `plan-error` | The plan is wrong, contradictory, outdated or underspecified. The task says what the plan should say. |
| `deferred` | Consciously moved; the item says where it was documented, or that the target milestone does not mention it yet. |
| `env-limit` | Cannot be done in the development environment (no Docker daemon, no KVM, no privileges on the VPS). It needs CI, the nightly appliance job, or real hardware. |
| `needs-decision` | The maintainer has to choose. The options and a recommendation are given. |

The most common causes are:

- **Forgotten follow-ups that the milestone PR announced.** Examples: "wire up retention in M5", "pinned Kea", "conntrack events".
- **Plan text that the implementation overtook.** Examples: Q1 still open although hosted KVM runners work, `uidrange` replaced by the service namespace, the testbed topology, the `wg` tool.
- **Behaviour that is only observable on a real kernel or in containers,** which the dev environment cannot run: BIRD external mode, Babel, the Kea/BIRD/DNS containers.

## How to work on this file (instructions for the agent)

1. **Branching and commits.**
   - Branch from `main` per work package below, e.g. `phase1-fix-routing`.
   - Commit messages and all written text in English.
   - No AI attribution of any kind in commits, PRs or issues (no `Co-Authored-By`, no "Generated with").
   - Commit as the repository's git user.
2. **Decisions.** All open questions of Phase 1 were decided on 2026-10-04 (table below, and in each item's "Needs maintainer" line). Implement the decided option. Where it differs from the recommendation, the task was rewritten to match the decision. The Phase 2 items (M7 to M11) were decided on 2026-10-11: the tables under "Phase 2 open items" list them and the items closed with them; one item stays open, a hardware measurement for H1. The Phase 2 audit (2026-10-12) added the items P2-AUDIT-01 to P2-AUDIT-16; the two that needed a decision, P2-AUDIT-01 and P2-AUDIT-07, were decided by the maintainer on 2026-10-12 (option (a) each, table "Decisions on the audit items (made 2026-10-12)"), and all of them were closed on 2026-10-12 together with P2-AUDIT-17 (table "Closed after the audit (2026-10-12)").
3. **Test levels.**
   - **Local:** unit tests (`make test`, `go test ./internal/...`), lint (`make lint`), `make check-generated`.
   - **CI only:** tests with build tag `testbed` (levels 1 and 1b; they need network namespaces, which the dev container lacks) and `appliance` (nightly job, needs KVM).
   - For those, push the branch, open a PR, and watch `gh pr checks <n> --watch`.
   - Read failures with `gh run view <run> --log-failed`.
   - When a testbed test fails without a clear reason, add diagnostics to the test (nft ruleset, routes, sockets) and push again; that is how M6b was debugged.
4. **Toolchain.**
   - Generated code: after editing `api/openapi.yaml`, run `make generate` and commit the generated files; CI runs `make check-spec check-generated check-clients`.
   - Formatting: run `gofmt` (CI lint fails on it) and keep `make lint` at 0 issues.
5. **Updating this file.** When an item is done, delete its block here in the same PR and mention the item id in the commit message. Keep the tables in "Phase 1 audit result" and "Phase 2 audit result" up to date.
6. **Plan changes.** Edit `docs/plan.md` only where an item says so (`plan-error` tasks).

## Decisions (made 2026-10-04)

| Item | Question | Recommendation | Decision (2026-10-04) |
|---|---|---|---|
| M1-01 | Where do KVM-dependent tests run (Q1)? | Hosted GitHub runners with `/dev/kvm` now, a self-hosted runner when H1 hardware exists | hosted runners only |
| M1-02 | Arm64 level 1b now or with M28? | With M28, amend §4.4 | with M28 |
| M2-03 | Configured address vs. DHCP lease: who wins? | Configuration wins (current) | configuration wins |
| M2-08 | Persistence layout: named volumes or §3.6 bind mounts? | Bind mounts in M28; amend §3.6 now for the status files | bind mounts in M28 |
| M3-01 | Restrict ops on OS-owned interfaces (uplink)? | Yes, an `os_owned` class | `os_owned` |
| M3-04 | Persist the executor generation? | No, document it as per-process | document |
| M4-02 | Management matrix endpoint: spec or code? | The spec follows the code | spec follows code |
| M4-07 | Warn when `apply --file` runs without `--state-dir`? | Yes | warn |
| M4b-04 | PSK verify by presence only: accept? | Accept | accept |
| M4b-05 | First poll announces online peers: intended? | Yes | intended |
| M4c-02 | Should a BIRD outage fail applies? | No: write the file, warn, do not fail | best effort |
| M4c-04 | Max-prefix action? | `restart` | `action block` |
| M4c-05 | Make Babel work or drop it from V1? | Make it work | make it work |
| M4c-10 | Effective route for a destination: where? | In M8a `/explain` | M8a explain |
| M4c-12 | Source match on routing input rules? | Accept as is | accept |
| M5-02 | How is a client told it missed events? | Synthetic `events_lost` event | `events_lost` |
| M5-03 | Setup with commit-confirm? | Yes, implement it | implement |
| M5-07 | Loopback binding of the API? | Document it | document |
| M5-10 | 413 for oversized bodies? | Add `payload_too_large` | 413 |
| M6a-04 | Conntrack events or polling? | Polling, amend the plan | events now |
| M6a-09 | Kea hook per lease (fork + TLS)? | A datagram socket to the API | datagram socket |
| M6a-12 | Generation marker and identity updates? | Amend §2.14 | amend plan |
| M6b-02 | Dead holder blocks applies? | Degrade and report | degrade |
| M6b-12 | `ui_port` default 443 vs the API's port? | The spec follows the code | spec follows code |
| M0-03 | ESP32 QEMU evaluation? | After V1 | remove from §4.5 |

## Suggested work packages

Ordered by value. Each package is one branch and one PR, and stays green in CI.

1. **Routing fixes** (done, `phase1-routing-fixes`): M4c-01 (high), M4b-01, M4b-02, M4c-03, M4c-06, M4c-08, M4c-09, M4c-13, M4c-14, M4c-15.
2. **Deployment pinning and health** (done, `phase1-deploy-pin-health`):
   - M6a-01 and M4c-17 (pin Kea and BIRD);
   - M6b-05 and M4-01 (health of the managed services and the supervisor);
   - M6a-25, M4c-07, M5b-04, M5b-01 (run the nightly job on main).
3. **API correctness** (done, `phase1-api-correctness`): M5-01, M5-04, M5-05, M5-06, M5-09, M5-11 to M5-22, M5-24.
4. **Devices and flows** (done, `phase1-devices-flows`; M6a-03 was only narrowed here and closed outright by package 11, M6a-07's doc part closed by package 9; M6a-22 closed outright once M6b-08 added certificate pinning): M6a-02, M6a-03, M6a-05, M6a-06, M6a-07 (limiter part), M6a-08, M6a-11, M6a-13 to M6a-22.
5. **Service namespace hardening** (done, `phase1-svcns-hardening`): M6b-01, M6b-03, M6b-04, M6b-06, M6b-07, M6b-08, M6b-10.
6. **Retention and domain** (done, `phase1-retention-domain`): M2-01, M2-02, M2-04 to M2-07, M2-09.
7. **Executor and engine robustness** (done, `phase1-executor-engine-robustness`): M3-02, M3-03, M3-05, M4-03 to M4-06, M4-10.
8. **Test infrastructure** (done, `phase1-test-infrastructure`): M1-02 (x86 matrix), M1-03 to M1-09, CC-04.
9. **Plan and docs sync** (done, one commit directly on `main`; M6a-04 was listed here originally but excluded, since its decision was to implement conntrack events in code, see package 11):
   - all `plan-error` items once their decisions are made: M1-01, M1-06, M1-10, M3-06, M4-09, M4b-03, M4b-07, M4b-08, M4c-11, M4c-16, M5-07, M5-23, M6a-07 (doc part), M6a-10, M6a-12, M6a-23, M6a-24, M6b-04 (M7 test), M6b-11;
   - doc items M0-01, M0-02, M3-07, M4-08, M4b-06, M6b-09, CC-01, CC-02.
10. **Decided changes** (done, `phase1-decided-changes`): M3-01, M4c-02, M4c-04, M4c-05, M5-02, M5-03, M5-10, M6a-09, M6b-02; closed M4b-04, M4b-05 and M4c-12 with a doc sentence.
11. **Conntrack events** (done, `phase1-conntrack-events`): M6a-04, then the rates of M6a-03 on top of it.
12. **Final cleanup** (done, `phase1-final-cleanup`): the 13 items decided on 2026-10-04 but never bundled above — CC-03, CC-05, CC-06, M0-03, M2-03, M2-08, M3-04, M4-02, M4-07, M4c-10, M5b-02, M5b-03, M6b-12.

## Cross-cutting

Removed from the old list: CC-01, CC-02 (closed in work package 9). CC-03 (`internal/linkexport` unit
tests) — closed with `internal/linkexport/linkexport_test.go`, covering a link with static routes
(the remote `.conf`: peer, endpoint, AllowedIPs; the BIRD snippet against a real `bird -p`) and the
no-routing error case. CC-04 (closed in work package 8). CC-05 (Node 20 deprecation warnings) —
every action in `.github/workflows/*.yml` bumped to its current major version (checkout v7, setup-go
v7, setup-node v7, setup-python v7, upload-artifact v7, cache v6, the Docker actions v4-v7); checked
each for breaking input changes against what this repo actually passes, none apply. CC-06 (the
scheduled nightly on `main`) — a `schedule`-triggered run exists and is green (run 37189011437,
2026-10-04T08:28:05Z); the workaround note in M5b-01 is now historical. Also fixed in passing: the
doc comment of `vmrun.GuestOptions` mentioned a `Verbose` field that does not exist
(`internal/testbed/vmrun/guest.go`).

## M0 (spikes, plan, docs)

Removed from the old list: "Devcontainer rebuild never verified" — wrong: CI builds `.devcontainer/Dockerfile` on every run (jobs `testbed-vm`, `testbed-privileged`); only the `start.sh rebuild` wrapper is untested. "Q1 evidence outdated" moved to M1-01, "Nightly kernel matrix" to M1-02. M0-03 (ESP32 QEMU fork) — the candidate sentence removed from plan.md §4.5; firmware tests keep using real devices.

## M1 (repository, CI, testbed)

Verdict: done. The scope is delivered, the M1 tests pass in CI, and the plan/doc drift (Q1/D33, the runner's exit code wording, the repository layout) is fixed.

| Plan item | Status | Evidence |
|---|---|---|
| Go module, Vue skeleton, oapi-codegen, Orval, openapi-python-client | done | `go.mod`, `web/`, `api/oapi-codegen-*.yaml`, `web/orval.config.ts`, Makefile `generate-python` |
| CI check: spec validates, examples validate, generated code compiles | done (validation, no style linter) | `make check-spec`, `check-generated`, `check-clients`; ci.yml level0 |
| Makefile, golangci-lint, go test -race, Vitest, Playwright skeleton | done | Makefile, `.golangci.yml`, `web/src/App.test.ts`, `web/e2e/smoke.spec.ts` |
| CI levels 0, 1b and 1 in hosted CI; `make test-vm` on the VPS before a merge | done | ci.yml; nightly kernel matrix (`testbed-matrix`) and weekly emulated check (`weekly.yml`) |
| Level 1 job in hosted CI | done | ci.yml `testbed-privileged` |
| `internal/testbed` (two networks, bridges, management default route) | done | `internal/testbed/topology.go`; `TestDefaultTopology` |
| Level 1b runner (one VM, rw share, no terminal) | done | `internal/testbed/vmrun` |
| Devcontainer with tools and stock kernels | done (GA kernels; nightly matrix runs each) | `.devcontainer/Dockerfile`; `nightly.yml` `testbed-matrix` |
| Injectable clock | done | `internal/clock` |
| arm64 image build | done | ci.yml `arm64` |
| Decision where KVM tests run (Q1) | done | D33 (plan §7.1) |
| Shared module preflight | done | `internal/preflight`; `TestHostSetupLoadsExactlyTheModulesOfThePreflight` |
| T: ping through a forwarding namespace in level 1b | done (CI) | `TestClientPingsServerThroughPlainForwardingGateway` |
| T: netem delay visible | done (CI) | `TestNetemDelayIsVisible` |
| T: runner returns the tests' exit code | done | `TestRunVMExitCodes` |
| T: same test in a privileged container | done (CI) | ci.yml `testbed-privileged` |

## M2 (domain model, persistence)

Verdict: done. Every scope and test item is implemented and tested.

| Plan item | Status | Evidence |
|---|---|---|
| Domain model from generated types | done | `internal/model/model.gen.go`, `domain.OverlayKey`, `NewOverlay` |
| Strict decoding | done | `domain/decode.go`; `TestUnknownFieldsInAConfigurationAreRejected`, `TestDuplicateKeysInAJSONDocumentAreRejected` |
| Rules the schema cannot express | done | `validate*.go` |
| Observed-state model | done | `domain/observed.go` |
| Precedence per family, overlays first | done | `domain/resolve.go`; `TestOverlaysWinWhateverTheLevel` |
| Atomic persistence, revisions with diff, schema versions | done | `store/files.go`, `store.Diff`, `store/migrate.go` |
| T: validation, precedence rows, E1–E8/E12, round-trip, corrupted files, examples, pointers and codes | done | `TestEveryPrecedenceLevel…`, `TestE1…`–`TestE12…`, corrupt_test.go, crash_test.go, `TestEveryExampleFileIsCheckedByTheDomain` |
| Retention 200 revisions (§3.6) | done | `internal/engine/owner.go` `pruneRevisions`; `TestCommittedRevisionsArePrunedToTheRetention` |

Removed from the old list: M2-03 ("configured address beats a lease") — documented as intended in
plan.md §2.3 and docs/development.md ("DHCP and devices"); the code's existing, stronger behaviour is
unchanged. M2-08 (persistence layout vs. plan §3.6) — plan.md §3.6 rewritten to the real current
paths (`/var/lib/chaosgw/{state,secrets,api,service}`, revision status files, `audit.jsonl`); the
switch to bind mounts at these paths is now in the M28 scope.

## M3 (executor and state reader)

Verdict: done. All scope and test items exist.

| Plan item | Status | Evidence |
|---|---|---|
| Typed closed operations, argument arrays, fixed paths, namespace targeting | done | `internal/executor/op.go`, `runner.go`; `TestRunnerUsesOnlyFixedAbsolutePaths`, `TestNamespaceIsPassedToEveryCommand` |
| Parsers `ip -j`, `nft -j`, `tc -j` | done | `internal/linux`; `parse_test.go` |
| Unix socket, version handshake, `SO_PEERCRED` | done | `proto.go`, `server.go`; `TestPeerCredentialsAreChecked`, version-mismatch tests |
| Scope: nft only `inet chaosgw`; routing only own tables/rules | done | `validate.go`; testbed `TestNftablesApplyIsAtomicAndScoped`, `TestExecutorCannotDeleteForeignRulesOrRoutes` |
| Scope: tc only assigned interfaces **and the uplink qdisc** | done | `os_owned` (M3-01); `TestOSOwnedInterfacesTakeOnlyTrafficControlRoutesAndOffloads` |
| Single `DOCKER-USER` operation | done | `DockerUser`; `TestDockerUserAcceptRulesAreMaintained` |
| Serialized queue with identity priority | done | `exec.go`; `TestIdentityUpdatesGoBeforeQueuedPlansAndNeverRunConcurrently` |
| Container hardening profile | done | `deploy/compose.executor.yaml`; `TestExecutorContainerHardeningProfile` |
| T: integration reads the gateway namespace; out-of-scope rejected; fuzz 5 min/nightly; version mismatch | done | testbed `TestExecutorReadsTheGatewayNamespace`; `TestDecodeRejects`; `FuzzDecode`/`FuzzFrame`, Makefile, nightly.yml |

Removed from the old list: M3-04 (executor generation restarts at 0) — documented as per-process,
not persistent, on `Outcome.Generation` itself (`internal/executor/exec.go`); the persisted
generation lives in the engine (M5-01).

## M4 (compiler, preview, safe apply)

Verdict: done. All scope and test items exist and pass.

| Plan item | Status | Evidence |
|---|---|---|
| Uplink selection, followed via netlink | done | `compile.go`, `internal/observer`; testbed `TestTheEngineFollowsTheUplinkThroughNetlinkEvents` |
| Bridges, table 100, forwarding, masquerade, gateway protection, matrix default, IPv6 block, offloads, DOCKER-USER | done | `compile.go`, `rules.go`; unit tests and testbed `TestGatewayProtection`, `TestIPv6IsBlockedOnTestNetworks`, `TestDockerUserAcceptLetsTestTrafficThroughDockersForwardPolicy` |
| nft layout (dynamic sets survive, removed objects deleted, hashed names), generation and verify | done | `compiler/nft.go`, `apply/verify.go`; `TestDynamicSetsAndCountersSurviveEveryApply`, `TestAGenerationThatDoesNotMatchIsReported` |
| Preview, apply, rollback, commit-confirm, anti-lockout, assignment by MAC, `chaosgw apply --file` | done | `engine/api.go`, `owner.go`, `lockout.go`, `compiler/host.go`, `cmd/chaosgw/apply.go` |
| State owner, snapshots, apply loop (§3.11) | done | owner.go, applyloop.go |
| Supervisor helper | done | used by the engine; `Engine.Health()` read by `GetHealth` |
| goleak in goroutine packages | done (gap: `internal/appliance`, see M5b-05) | TestMain in executor, engine, observer, supervisor, cmd/chaosgw, api, clock, dnsproxy, testbed |
| T: golden, client reaches server, protection, management route, verify manipulation, injected failure, rollback, set change, uplink change, immutable snapshots, observer during apply | done | see the test names in `internal/{compiler,apply,engine}` |
| T: preview matches applied state | done | `TestPreviewShowsTheChangeAndChangesNothing` compares the previewed plan and diff with what `apply.BuildPlan`/`apply.Diff` build from the same pre-apply state |

Removed from the old list: "tc tokens allow `/` and `..`" — wrong: `..` is rejected (`internal/executor/validate.go:423`, test "tc path traversal token"). "Supervisor helper deferred to M8a" (Covered later) — wrong: the supervisor exists and is used; only the reader pool and time stamps go to M8a, overlay removal on stop to M27. M4-02 (management matrix endpoint vs. spec) — the spec's description now follows the code
(`Management.allowed_sources`, or the interface subnet when none are given). M4-07 (`apply --file`
without `--state-dir` has no rollback) — now warns on stderr, asserted in `TestApplyFileFailures`.

## M4b (WireGuard networks and clients)

Verdict: done. All plan tests exist and passed in CI (run 37147106957); two formerly open items (PSK verify by presence only, the first poll announcing online peers) are accepted as documented behaviour (docs/development.md).

| Plan item | Status | Evidence |
|---|---|---|
| Hub and link as network type, `wg` tool, clients with client networks, reachable lists, static routes, policy rules | done | `internal/compiler/wireguard.go`; `TestWireGuardRoutesAndRules`; testbed `TestLocalDeviceReachesAClientNetworkWithoutNATAndTheReverseNeedsTheMatrix`, `TestALinkWithStaticRoutesCarriesTrafficToTheRemoteSite` |
| Routed without NAT to test networks | done | testbed `integration_wg_test.go:251,275,304` |
| Masqueraded towards the uplink | done | `TestMasqueradeTowardsTheUplinkOnlyAndForTheNetworksBehindClients` (a link matches by interface, covering learned routes too); testbed `TestAClientNetworkIsMasqueradedTowardsTheUplink` |
| Keys, preshared keys, export once, `.conf`/QR/zip | done | `internal/wireguard/*_test.go`; `TestQRCodesAndNetworkExports` |
| Client status and events, clients as devices, role management, MSS clamp, MTU | done | `engine/wireguard.go`; testbed `TestDisablingAClientStopsItsHandshakeAndEmitsTheEvent`, `TestRolesDecideWhoReachesTheControlPlane`, `TestTheMSSIsClampedOnTheTunnel` |
| T: export works in a fresh namespace, QR decodes, keys never in logs, re-apply keeps the tunnel | done | `TestPrivateKeysStayOutOfStoreSnapshotAndLogsAndAReapplyKeepsTheTunnel` |

## M4c (dynamic routing, BIRD)

Verdict: done. The high bug (external mode) is fixed; every scope and test item exists and passes.

| Plan item | Status | Evidence |
|---|---|---|
| BIRD instance, own config/socket/container | done | `deploy/compose.bird.yaml`, `executor/exec.go:662-758` |
| BGP, OSPFv2 | done | testbed `TestThreeSitesWithBGPAndOSPFLearnRoutesOnlyIntoTheOwnTable` (now also exercises the OSPF import filter) |
| Babel | done | the link-local address Babel needs is assigned to the WireGuard interface (M4c-05); testbed `TestBabelOverAWireGuardLink` |
| Static, router id/ASN/neighbors/areas/timers, announcements | done | `compiler/routing.go`; `TestTheBirdConfigurationFollowsTheModel` |
| Import filters | done | `bird/render.go`; the management subnet is protected with explicit `allowed_sources` too, and `allow_default` is honored with an allowed list |
| Export only into own tables | done for table 100 and, since M10, the PMTU mirror tables | `TestLearnedRoutesAreExportedIntoThePMTUMirrorTablesToo` |
| `bird -p` + `birdc configure` part of the revision | done | `executor/exec.go:705-758`, `apply/bird.go` |
| …and of preview/diff | done | `Preview.linux.bird` and `.wireguard` are filled |
| Custom snippets | done | `bird/lexical.go` |
| External mode | done | fixed (RTS_INHERIT, not RTS_PIPE); `TestExternalModeImportsAnotherDaemonsTable` |
| Neighbor and route status and events | done | `routing_session_changed` and `routing_routes_changed`; `TestRouteCountChangesAreEvents` |
| Remote-side snippet in link exports | done | `bird/remote.go`, `internal/linkexport`, API `format=bird` |
| T: three sites, learned only into own tables, filters (BGP and OSPF), max-prefix, link down, withdrawal, config change, invalid snippet, failed apply, confirm-timeout rollback | done | see the M4c test names in `internal/engine`, `internal/apply` |

Removed from the old list: M4c-10 (the preview does not show the effective route for a destination)
— moved to M8a's `/explain` scope; plan.md §2.2.2 now says "shown by explain" instead of promising
it in the preview.

## M5 (REST API v1)

Verdict: done. Every scope and test item exists and all 41 `x-milestone: M5` operations have handlers.

| Plan item | Status | Evidence |
|---|---|---|
| problem+json, UUID or name in paths, cursor pagination, ETag/If-Match/428, merge-patch candidates, idempotency keys | done | `internal/api/{problem,helpers,middleware,revisions,idempotency}.go`; `TestProblemsAreProblemJSON`, `TestIdempotencyKeys` |
| SSE with ids, replay, keepalive | done | `events.go`, `engine/events.go`; a restart or an expired replay window sends a synthetic `events_lost` event (M5-02) |
| Generation (state, header, SSE `applied`) | done | persisted across restarts (`engine.Config.GenerationFile`); `TestGenerationContinuesAfterRestart`, `TestGenerationSurvivesAnAPIRestart` |
| Capabilities, candidate model, sessions + CSRF, hashed tokens with scopes, setup token | done | `system.go`, `revisions.go`, `auth/auth.go`; `TestLoginSessionCSRFAndLogout`, `TestTokenScopesAndTheirLifecycle` |
| Setup "applies with commit-confirm" (spec `POST /setup`) | done | `engine.ApplyOptions.ForceConfirm` (M5-03); `TestTheSetupWaitsForConfirmation`, `TestAnUnconfirmedSetupIsRolledBackAndReopened` |
| Admin password reset | done | `cmd/chaosgw/admin.go`: audited, interactive prompt |
| UI/API bound to the management network after setup | done | `cmd/chaosgw/api.go`: also excludes test networks before setup; the loopback binding for the container health check is documented (plan §2.16) |
| Audit log | done | `internal/audit`: retention, failure surfaced as unhealthy, system-originated rollbacks audited |
| T: contract tests, clients compile, E2E testbed, conflicts, confirm_pending, SSE reconnect, slow subscriber, concurrent applies | done | `harness_test.go` `checkContract` (now validates requests and SSE events too), `make check-clients`, `e2e_test.go`, `revisions_test.go`, `events_test.go` |

## M5b (appliance VM harness, level 2)

Verdict: done. Scope done; the level-2 smoke is green with the current deploy config (24.04 and 26.04, three and two ports, run 37171706227, on branch `phase1-deploy-pin-health`, which also added BIRD to the deployment).

Removed from the old list: M5b-02 (anti-lockout check reused the old SSH connection) — `VM.DialFresh`
opens a brand new connection and runs `true` over it; the client namespace's inability to reach SSH
on the gateway's LAN address is asserted alongside it (real verification needs the nightly appliance
job). M5b-03 (cloud image checksums not signature-verified) — `Fetch` now checks `SHA256SUMS.gpg`
against Ubuntu's own signing key with `gpgv` before trusting `SHA256SUMS` at all.

(Q1 and the level-2 wording of the plan: see M1-01.)

## M6a (DHCP and device discovery)

Verdict: done. Every plan test exists (testbed tests passed in CI); conntrack is followed through events with a polling fallback, and `started_at` is parsed and exposed.

| Plan item | Status | Evidence |
|---|---|---|
| Kea container, pinned version | done | `deploy/Dockerfile`, `.devcontainer/Dockerfile` (`ARG KEA_VERSION`) |
| One subnet per network, pools, reservations via `config-set`, DHCP on/off | done | `internal/compiler/dhcp.go`, `internal/kea`; `TestTheClientDrivesARealKea`; testbed `TestDhcpOffOnOneNetworkLeavesItSilent` |
| Lease events via `run_script` | done | `cmd/chaosgw/keahook.go`, `api/devices.go:312`; `TestACommittedHookCarriesEveryLease`; a Unix datagram socket to the API, HTTP only as a fallback, not a fork per lease (M6a-09) |
| Flow observer on conntrack events | done | `Engine.FollowConntrack` watches `conntrack -E` through the executor's `Watch`/`Event` frames, debounced into `TriggerObserve`; the 1 s poll is a fallback after 10 s without a watch (M6a-04) |
| Flows API | done | `started_at` parsed from `conntrack -o ktimestamp`'s `start=` field (`nf_conntrack_timestamp`), exposed as `Flow.StartedAt`/`started_at` (M6a-03); the real kernel field format still needs confirming once this runs in CI (see "Deviations" in the merged PR) |
| Discovery from leases, neighbors, conntrack, WG clients | done | discovery by address also covers a LAN network's own downstream routes |
| Identity events, incremental updates | done | `owner.go`, `applyloop.go`; `device_identity_changed` carries its generation |
| Manual device merge | done (as a revision per the spec) | `domain/observed.go:278`; moving overlays to the configured device waits for M8a (plan.md updated) |
| Devices API | done | `upload_bps`, `download_bps`, `flows_active` |
| §3.11 identity before plans | done | `TestIdentityUpdatesGoBeforeQueuedPlansAndNeverRunConcurrently` |
| T: lease, MAC/IP, reservation, DHCP off, discovered vs configured, identity within 1 s, flows | done | `internal/engine/integration_dhcp_test.go` |
| T: burst debounced into one identity update | done | `TestABurstOfNeighborChangesIsOneIdentityUpdate` |

Removed from the old list: M6a-03 (`started_at` needs a verified conntrack timestamp format) and M6a-04 (conntrack polled, not followed through events) — both closed by `phase1-conntrack-events` (work package 11), which added `Engine.FollowConntrack` watching `conntrack -E` through a new executor `Watch`/`Event` frame pair, debounced into `TriggerObserve` with the 1 s poll kept as a 10 s fallback, and parses `Flow.StartedAt`/API `started_at` from `conntrack -o ktimestamp`'s `start=` field. "Only the first MAC is reserved" — the spec defines `fixed_ip` as "DHCP reservation for the device's first MAC" (openapi.yaml:2752). "Executor priority only unit-tested" — plan §3.11 prescribes exactly that (fake-executor test). "Online ignores leases" → M6a-24.

## M6b (DNS proxy and service namespace)

Verdict: done. Scope and tests exist and the DNS testbed test passed in CI.

| Plan item | Status | Evidence |
|---|---|---|
| Holder `svcns`, `svc0` pair, table 102 with prohibit fallback | done | `cmd/chaosgw/svcns.go`, `compiler/service.go`, `apply/service_test.go`; the holder's PID is checked against the netns inode it reported, so a reused PID is refused |
| Re-attach on holder change | done | `engine/service.go` (WatchService), `executor/exec.go`; `TestAHolderThatRestartsIsNoticedAndTheNamespaceReplaced` (simulated kernel), `TestAHolderRestartIsHealedWithoutHelp` (real holder process) |
| A dead holder degrades instead of failing every apply | done | `ServiceHealth.HolderExists` distinguishes dead from merely unattached (M6b-02); `engine.Snapshot.ServiceError`; `TestADeadHolderDoesNotBlockARevisionApply` |
| Services leave a stale namespace (§3.8, risk 35) | done | `dnsproxy.WatchNamespace`; `chaosgw dns --namespace-check` |
| DNAT for LAN and WG gateway addresses, UDP+TCP | done | `compiler/service.go`; testbed `TestDNSThroughTheServiceNamespace` |
| Forwarding, caching, AAAA removal, query log, registration | done | `internal/dnsproxy`, `api/dns.go`; an AAAA query of a missing name is NXDOMAIN, SVCB/HTTPS `ipv6hint` is stripped too |
| Coexistence with systemd-resolved | done | tested |
| Upstream from the host | done, limits documented | per-link resolvers are not distinguished (docs/development.md) |
| Managed services report health (§2.14, Health enum `kea`/`svcns`/`dns`) | done | `internal/api/system.go` GetHealth |
| Proxy and Kea hook verify the API's certificate | done | `internal/api.PublishCertificate`, `--api-cert-file`/`CHAOSGW_API_CERT` |
| T: LAN and WG client over UDP and TCP; restart of the proxy | done | `e2e_dns_test.go` |
| T: no bind on 127.0.0.53, resolved keeps working | done | |
| T: holder restart healed | done | |
| T: fail closed | done | named in the plan's M7 test list |

Removed from the old list: "Query log in memory only" — the plan asks only for `/dns/queries` with a device filter (§2.13); persistence is not required. M6b-12 (`ui_port` spec default vs. the code) —
replaced the stale `default: 443` with a description naming the real default (the API's own port,
8443 in the shipped compose files).

## Covered later

These are open but scheduled in a later milestone of docs/plan.md §5; they are not Phase 1 work. Items whose target milestone does not mention them yet are in the lists above (with a task to add them to that milestone).

| Item | Milestone |
|---|---|
| M1: container image has no non-root user (`deploy/Dockerfile`) | M29 |
| ~~M2: overlay `expires_at` / `lease_expires_at` not set by `NewOverlay`~~ — set since M8a (`internal/overlay/store.go`, P2-AUDIT-16) | M8a |
| M3: DNS set updates spawn `nft` per call, no persistent netlink connection | M20 (plan §3.4) |
| M4: verify matches rules by comment hash only (expressions not compared) | M38 drift detection (partial) |
| M4: Docker started after the last apply is not noticed (`DOCKER-USER`) | M38 drift detection (partial) |
| M4c: received-prefix list in routing view | M14 |
| M5: certificate loaded once at start, SANs fixed (`/system/certificate`) | M29 |
| ~~M5: `lockout_protected` never produced~~ — decided 2026-10-11 (P2-M9-08): the anti-lockout rule cannot be overridden and lockout-relevant configuration changes take commit-confirm, which is enough; the code stays reserved | decided |
| ~~M5: `capacity_exceeded` never produced~~ — produced for classes, fault ids, map cells and rules since M8a; the preview refuses with it (M10 test) | M8a, M10 |
| M5: `target_busy` never produced | M15 |
| M5: IPv6 management addresses never bound | M32 |
| ~~M5: `counter_epoch` always 0~~ — done in M8b (P2-M8b-05 for what it means) | M8b |
| M5: capabilities list `step_types` empty (`overlay_kinds` and `fault_families` are filled since M8a to M10, P2-AUDIT-16) | M15 |
| M5b: netplan hints are static text | M28 |
| M5b: only the executor is deployed, x86-64 only | M28 |
| M0: S8 on real hardware (HTB with 500 classes, 50/250 faults, offloads, loss) | H1 |
| M0: S1 testbed on Raspberry Pi | H1 |
| M0: DNS proxy at least 1000 queries/s on Pi | H1 |
| M0: scenario step timing on real hardware | H1, M15 |
| M0: S7 coexistence with netplan, NetworkManager, systemd-networkd | M28 (no explicit test named: add one to the M28 tests) |
| M0: S4 TLS 1.2 vs 1.3, connection reuse, control from the core | M21, M22 |
| M0: IPv6 never spiked | M32 |
| M0: persistent nft session for DNS-derived sets (DNS over TCP is done in M6b) | M20 |
| M0: `image.yml` has never run (ghcr push, release tags) | M28 |
| M0: persistent faults for DNS, TLS, DHCP and profile activations (D31) | M39 |
| M0: three-way merge, NFLOG capture, extra diagnostics, drift detection, .deb (D28) | M38 |
| Stubs: `chaosgw tls`, `chaosctl` beyond `version` | M21, M18 |
| Image holds only `chaosgw`/`chaosctl` and executor tools; full image with Kea, tcpdump, mitmproxy | M28 |
| ~~Identity maps (address → device), classification and faults not compiled yet~~ — done in M7, M8a, M8b | M7, M8a, M8b |
| ~~Reader pool and operation time stamps (M3 deferral)~~ — done in M8a | M8a |
| Overlay removal on stop | M27 |
| M6b: DNS faults, hostname selectors, redirect of hardcoded resolvers, DoT blocking, `/internal/dns/resolutions` | M20 |
| M6b: classification sets the service mark (bit 20) that table 102 routes on | M20/M21 (confirmed 2026-10-11, P2-M7-01) |
| M6b: TLS responder joins the service namespace | M21 |

## Phase 2 audit result

Date: 2026-10-12. Audited against `main` at `c97b9ba` and `docs/plan.md` §5 Phase 2 (M7, M8a, M8b, M9, M10,
M11; H1 is dropped from scope, since there is no target hardware). Every milestone was checked against its
Scope, Tests and Acceptance lines, the behavior sections of the plan they refer to (§2.2–§2.5, §2.13, §3.2,
§3.3, §3.11, §4.3), its section in `docs/development.md`, `api/openapi.yaml` (normative for the API shape)
and the code. Every test the plan lists was looked up by name and its result read from CI.
Closed: 2026-10-12, PR "Close the Phase 2 audit items" (the six work packages below and P2-AUDIT-17).

**Phase 2 is complete.** Every milestone delivers its scope, every test the plan lists exists and passes in
CI on a native kernel and under KVM with the §4.3 accuracy assertions on, and every item the audit found is
closed (table "Closed after the audit (2026-10-12)" under "Phase 2 open items"). The maintainer decided the
two items that needed it on 2026-10-12: P2-AUDIT-01 option (a), stable device numbers, and P2-AUDIT-07
option (a), the measurement record as a nightly artifact. P2-M9-06 (the hardware measurements of H1) stays
open by scope.

What the audit found: two findings of medium severity, P2-AUDIT-01 (a new device reached the kernel through
a throttled full apply instead of an element update, as the M7 scope says) and P2-AUDIT-02 (the revision
preview did not return the tc diff, the per-interface class capacity and the per-scope queue count that the
plan, §2.4, §2.14 and D18, and the spec promise), and 14 of low severity: documentation and spec text that
the code overtook, four test gaps, the per-night record of measurements that §4.3 asks for, and an edge
case of commit-confirm with overlays. One of them, P2-AUDIT-06 (a sentence in `docs/development.md` that
still called orphaning and the device merge the next steps of M8a), was fixed in the audit's own PR,
together with a second stale M8a sentence of P2-AUDIT-16. The work on P2-AUDIT-15 found one more low item,
P2-AUDIT-17 (the configuration's `class_limit_per_interface` was never read), closed with the others.

| Milestone | Verdict | Audit items (all closed on 2026-10-12) |
|---|---|---|
| M7 Classification layer | done | P2-AUDIT-01 (medium), P2-AUDIT-03, P2-AUDIT-04 |
| M8a Overlays | done | P2-AUDIT-05, P2-AUDIT-17 (P2-AUDIT-06 fixed in the audit's PR) |
| M8b Fault engine: latency, jitter, loss | done | P2-AUDIT-07 |
| M9 Access rules | done | P2-AUDIT-08, P2-AUDIT-09, P2-AUDIT-10 (P2-M9-06 stays open for H1) |
| M10 Extended faults | done | P2-AUDIT-07, P2-AUDIT-11, P2-AUDIT-12, P2-AUDIT-13 |
| M11 Profiles | done | — |
| Cross-cutting | done | P2-AUDIT-02 (medium), P2-AUDIT-07, P2-AUDIT-14, P2-AUDIT-15, P2-AUDIT-16 |

### Evidence

- **CI on `main`:** run 38219524690 on `c97b9ba`, conclusion success; all five jobs green (level0 with
  `go test -race ./...`, e2e, arm64 under qemu, testbed level 1 in a native privileged container on
  6.17.0-1022-azure, testbed level 1b in a VM with KVM on 6.8.0-142). Both testbed jobs logged
  `accurate=true` and 1245 `--- PASS` lines with 0 `--- SKIP` and 0 `--- FAIL`; the engine package
  reported 285 passed and 0 skipped.
- **Nightly:** 38126430957 (2026-10-11, `d435aa9`) success: the kernel matrix 6.8.0-142 and 7.0.0-38
  booted with `kvm=true` and passed every Phase 2 measurement test with 0 skipped, and the fuzz targets ran
  60 minutes each. 38228641768 on `c97b9ba`: the kernel matrix (6.8, 7.0) and the appliance jobs (Ubuntu
  24.04, 26.04) succeeded; its fuzz job was still running when the audit ended.
- **Weekly (emulated, no KVM):** 38212356584 (`3e854ce`, the tree of `c97b9ba`) success, without
  `internal/engine` (documented in `docs/development.md`); the run before it, 38158196621, failed in the
  engine package (P2-AUDIT-14).
- **Local:** `go test ./internal/...`, `make check-spec` and `make check-generated` pass.

### Per milestone

As the audit found them; every gap named below was closed on 2026-10-12.

- **M7** — the lookup chain (four levels with groups, networks and global folded into the per-source
  tables, P2-M8a-01), the guard for test traffic, the direction bit, overlap splitting and the identity map
  are delivered; all M7 tests of the plan pass on level 1 and 1b, among them
  `TestClassificationMarksOnlyMatchingTrafficBehindNAT`,
  `TestClassificationForAWireGuardClientNetworkAsInitiatorAndAsDestination`,
  `TestAMapChangeMovesAnEstablishedConnectionToItsNewClass` and
  `TestTheIdentityMapEntryFollowsAForcedAddressChangeWithinASecond` (3 s bound, P2-M7-03). Gaps: a new
  device is a full apply (P2-AUDIT-01), plan §3.3 still describes the old levels (P2-AUDIT-03), and the
  concurrent-full-apply test covers only the sequential case (P2-AUDIT-04).
- **M8a** — overlay store, TTL and lease, `reset`, precedence goldens E1–E8 and E12, stable ids, the tc
  output, named counters, `explain` with the effective route, coalescing (200 concurrent writes, on the
  real kernel too), the reader pool, orphaning with `?force` and the move of overlays on a merge are
  delivered and tested; the nine M8a operations of the spec match the code. Gaps: a commit-confirm rollback
  leaves the overlay changes of the rolled-back revision in place (P2-AUDIT-05), and a stale sentence in
  `docs/development.md` (P2-AUDIT-06, fixed in the audit's PR).
- **M8b** — in-place tc changes, make-before-break, overlay writes that return after verify with the new
  generation, and netem queue statistics with epochs are delivered; every measurement test of the plan
  (device, group, network, two test networks, WireGuard client network, a BGP route, isolation, updating
  one fault, the 600 ms fault under load, the `tc -j` normalizer goldens) passes on level 1, level 1b and
  the nightly kernels. Gap: the measured distributions are not stored per night (P2-AUDIT-07).
- **M9** — ordered rules in configuration and as overlays on the conntrack original tuple, cutting
  existing connections, precedence before faults, named counters, preview and `explain` are delivered; the
  S3 behavior matrix (C0–C6), rule order, overlays first, the anti-lockout rule and the UDP 53 test against
  the gateway address and 169.254.100.2 pass in CI and on both nightly kernels. Gaps: one spec sentence on
  `system_rules` (P2-AUDIT-08), one validation code described wrongly (P2-AUDIT-09), one assertion that
  checks nothing (P2-AUDIT-10).
- **M10** — tunnel faults, the WireGuard actions, per-device rate and queue limit (D18), reorder,
  duplicate, corrupt, burst loss, blackout, flapping, the three PMTU modes and BIRD's export into the
  mirror tables are delivered; one measurement test per fault type passes on level 1, level 1b and the
  nightly kernels 6.8 and 7.0, as do E9 and E10 and the `capacity_exceeded` previews. Gaps: P2-AUDIT-07,
  user-visible decisions missing from the spec and plan §2.4 (P2-AUDIT-11), a BGP re-convergence under KVM
  that contradicts the documented expectation (P2-AUDIT-12), and no real-kernel test that a duplicate meets
  the class of the original (P2-AUDIT-13).
- **M11** — built-in and custom profiles, activation on device, group and network through overlays, and
  precedence with individual faults are delivered; every test of the plan passes with measured values
  (e.g. `TestActivatingAndSwitchingProfilesYieldsTheMeasuredValues`,
  `TestADeviceFaultOverridesTheNetworkProfileForThatDeviceOnly`,
  `TestAnOverlayProfileBeatsAConfigurationFaultOnTheWire`), on level 1, level 1b and both nightly kernels.
  P2-M11-01 to P2-M11-05 hold in the code. No gap.
- **Cross-cutting** — "measured on x86" holds (accuracy assertions run natively and under KVM, never
  skipped); every Phase 2 operation of the spec is implemented and none is answered as unsupported; the
  Phase 2 counts in this file and the plan agree; every Phase 2 package passes on arm64. Gaps: the preview
  fields (P2-AUDIT-02), the emulated branch of the engine tests (P2-AUDIT-14), `Capabilities.limits`
  (P2-AUDIT-15), stale rows in the documents (P2-AUDIT-16).

### Checked and not recorded as items

- Latency `distribution` and `keep_order` are never measured on a real kernel: not a plan gap. M8b and
  §4.3 ask for the median and the spread against the jitter, which the tests check; `keep_order` is a latency
  parameter, not a fault type, and its effect is bounded through the rate profiles (P2-M11-04).
- The SSE event type `counter_epoch` is in the spec's enum but never emitted: not a Phase 2 gap. §2.13 asks
  for the reset to be reported as a counter epoch, which the state's `counter_epoch` and the epoch on every
  counter and queue reading do (P2-M8b-05); the enum also lists events of later milestones.
- `lockout_protected` kept in the spec but never produced: decided (P2-M9-08). The rule limits answering
  `capacity_exceeded`: documented in the spec at the rule limits. The `system_rules` sentence of the spec was
  P2-AUDIT-08 (closed).

### Suggested work packages

All six were done on 2026-10-12, each on its own branch, and brought into one PR ("Close the Phase 2 audit
items", branch `phase2-audit-close`).

1. **Preview and capabilities** (`phase2-audit-preview`): P2-AUDIT-02 (medium), P2-AUDIT-15 — done; the
   integration added P2-AUDIT-17, found by P2-AUDIT-15.
2. **A new device as an element update** (`phase2-audit-identity`), after the decision of P2-AUDIT-01:
   P2-AUDIT-01 (medium), with the concurrent-apply test P2-AUDIT-04 — done.
3. **Overlays across a rollback** (`phase2-audit-rollback`): P2-AUDIT-05 — done.
4. **Test gaps** (`phase2-audit-tests`): P2-AUDIT-10, P2-AUDIT-13, P2-AUDIT-14 — done.
5. **Measurement record and BGP re-convergence** (`phase2-audit-measurements`), after the decision of
   P2-AUDIT-07: P2-AUDIT-07, P2-AUDIT-12 — done.
6. **Spec and docs sync** (`phase2-audit-docs`): P2-AUDIT-03, P2-AUDIT-08, P2-AUDIT-09, P2-AUDIT-11,
   P2-AUDIT-16 — done. (P2-AUDIT-06 was done in the audit's PR.)

## Phase 2 open items

Phase 2 (M7 to M11) produced 42 items: 39 from the first review, and three that the review of M9 and M11 and the first CI runs
added (P2-M9-11, P2-M10-09, P2-M11-05). On 2026-10-11 the maintainer decided 36 of them (first table below): 31 in the first round, then
the three confirmations P2-M7-02, P2-M8b-07 and P2-M11-02 and the two items transferred to the acceptance criteria of M20 and M21 in
`docs/plan.md` (P2-M9-02, P2-M11-01). The decisions are recorded where the behavior is documented: the milestone's section in
`docs/development.md`, the description in `api/openapi.yaml` where the behavior is user-visible, and `docs/plan.md` where the plan said
otherwise. Seven decisions changed behavior and were implemented with them (P2-M9-01, P2-M9-03, P2-M9-04, P2-M9-11, P2-M10-08, P2-M11-03,
P2-M11-05); the others confirmed the interpretation the item had chosen (for P2-M9-11 the second question, the meaning of `uplink`, was
confirmed and documented). P2-M8a-07 was resolved in M9. Five items needed no decision and were closed by work on 2026-10-11 and
2026-10-12 (second table: P2-M8a-06, P2-M8b-02, P2-M8b-04, P2-M8b-08, P2-M10-09). The blocks of all 41 are gone from this file, like
the closed items of Phase 1; the item ids stay as names, since the code and the documents refer to them. One of the 42 stays open (below):
P2-M9-06, a hardware measurement for H1, which also carries the arm64 apply-cost measurement left from P2-M8b-04.

The Phase 2 audit of 2026-10-12 ("Phase 2 audit result" above) added 16 items, P2-AUDIT-01 to P2-AUDIT-16 (two of medium
severity, fourteen low; the per-night record of measurements was found under M8b, M10 and the cross-cutting checks and is
one item, P2-AUDIT-07). P2-AUDIT-06 was fixed in the audit's own PR. The maintainer decided P2-AUDIT-01 and P2-AUDIT-07 on
2026-10-12 (third table). The 15 items left after P2-AUDIT-06, these two included, were closed on 2026-10-12 together
with P2-AUDIT-17, a low item the work on P2-AUDIT-15 found (fourth table, 16 rows); their blocks are gone. So one Phase 2 item is open: P2-M9-06, under "Items that stay open".

### Decisions (made 2026-10-11)

| Item | Question | Decision |
|---|---|---|
| P2-M7-01 | Should M7 write the service-selection mark (bit 20)? | Confirmed: no, it stays deferred to M20/M21, where the real redirect-interaction test is |
| P2-M7-02 | Classification maps keyed by address, not by the identity map's device number | Confirmed: keyed by address (a device that gets a new address changes 6 elements in all maps, `TestAnAddressChangeChangesAHandfulOfElements`); recorded in `docs/development.md` (M8a) |
| P2-M7-03 | Convergence bound of the identity map on a shared CI runner | Confirmed: 3 s on CI, the plan's 1 s is the target on dedicated hardware (H1) |
| P2-M8a-01 | Group and network scopes folded into per-source tables | Confirmed: folded |
| P2-M8a-02 | Packet size, memory budget and link speed of the computed queue limit | Confirmed: 1500 bytes, 256 MiB per interface, never below netem's 1000 packets, 1 Gbit/s as the cap; the link speed from the host stays a later refinement |
| P2-M8a-03 | Unknown addresses of a network share one queue per fault | Confirmed: one per winning fault |
| P2-M8a-04 | The class limit counts the default class and applies to every interface alike | Confirmed |
| P2-M8a-07 | `explain` by rules | Resolved in M9 (`World.AccessDecision`) |
| P2-M8b-01 | A duplicating netem cannot share an interface's tree | Resolved by P2-M10-01 |
| P2-M8b-03 | Make-before-break holds old and new classes at once; its time limits | Confirmed: the three limits as constants, the class limit does not count the waiting classes |
| P2-M8b-05 | Counter epochs are generations and a restart starts a new one | Confirmed |
| P2-M8b-06 | Tolerance for the spread of a jitter and for "no change" | Confirmed: 0.5 to 1.5 times the uniform spread plus 2 ms; median within ±2 ms + 5 % of before and no loss |
| P2-M8b-07 | A burst through a delay fault loses replies in the echo server's own output path on a slow runner | Confirmed: the burst test accounts for the echo server's own output errors (`Icmp.OutErrors`) and the host's receive drops and fails on any packet nothing counts; the burst is not paced |
| P2-M9-01 | An allow rule in the input path | **Changed:** it accepts, so it also opens the gateway's closed ports for its selector; the UI/API port rule for other sources and the anti-lockout rule stand in front of the rules |
| P2-M9-02 | A rule that names a hostname is not compiled before M20 | **Transferred to the plan:** M20's acceptance criteria (`docs/plan.md`) include a drop rule with a hostname destination that blocks the addresses the device resolved; until then the rule stays out of the chain with the warning `hostname_unresolved` |
| P2-M9-03 | The type of a `reject` | **Changed:** chosen per rule with `reject_with` (`port_unreachable` by default, `host_unreachable`, `admin_prohibited`, `no_route`) |
| P2-M9-04 | The network scope and the networks behind a test network's static routes | **Changed:** "network X" includes them, for rules and faults alike |
| P2-M9-05 | Fault counters count packets a rule drops afterwards | Confirmed: left as it is |
| P2-M9-07 | A rule's counter counts new connections only | Confirmed; documented in the spec |
| P2-M9-08 | `lockout_protected` is never produced | Confirmed: commit-confirm is enough, the code stays reserved |
| P2-M9-09 | Access rules select IPv4 only | Confirmed: IPv6 selectors come with M32 |
| P2-M9-10 | What "also cut existing connections" does at its edges | Confirmed |
| P2-M9-11 | Does the two-port guard stand in front of the access rules, and what does the destination `uplink` mean? | **Changed (1):** the guard stands in front of the jump to the access rules (chain `mgmt_guard`), so no rule, an `allow` rule included, opens the management network behind the uplink; only an explicit matrix entry does, and `explain` agrees with the kernel. Confirmed (2): `uplink` keeps its meaning (in no configured network and not in the management prefixes), so learned prefixes (BGP, OSPF) and the service namespace's 169.254.100.0/30 count as uplink; documented in the spec and in `docs/development.md` |
| P2-M10-01 | Duplication as a draw plus a copy on the netdev egress hook | Confirmed |
| P2-M10-02 | When a flapping starts, and a boundary during an apply | Confirmed |
| P2-M10-03 | A rate below 8 bit/s is refused; the phase of a flapping in the API | Confirmed |
| P2-M10-04 | Black hole before the access rules, the route lock, the clamp's headers | Confirmed |
| P2-M10-05 | Directions and peer address of a tunnel fault | Confirmed |
| P2-M10-06 | The WireGuard actions | Confirmed |
| P2-M10-07 | One IFB with a fixed name; the host's ingress qdisc is shared | Confirmed |
| P2-M10-08 | The per-packet delay check of the update test on a loaded runner | **Changed:** the measured stream is at least 200 packets (sent 5 ms apart); the bounds and the rule that none is more than 10 ms outside stay |
| P2-M11-01 | A profile with a DNS or TLS part is stored but cannot be activated before M20 and M21 | **Transferred to the plan:** M20 (`dns`) and M21 (`tls`) lift `domain.unavailableFamilies` family by family, and their acceptance criteria include that such a profile activates and its part competes in its family; until then the activation is refused with `unsupported_feature` |
| P2-M11-02 | Custom profiles only through candidate revisions, no endpoints of their own | Confirmed: no `POST /profiles`; the configuration's `profiles` are created, changed and deleted with a revision |
| P2-M11-03 | The rate of the built-in Bad LTE end to end | **Changed:** a UDP measurement above the rate was added (`TestTheRateOfBadLTEShapesUDPSentAboveItEndToEnd`) |
| P2-M11-04 | The accuracy check of a profile with a rate and a jitter | Confirmed: a bound is enough for the ordered delay |
| P2-M11-05 | A revision that adds a DNS or TLS part to a custom profile that an overlay activates | **Changed:** the revision is refused with `unsupported_feature` naming the profile and the milestone (M20 for `dns`, M21 for `tls`) while the activation exists, as the write of the activation is; without the activation it is accepted |

### Closed by work (2026-10-11 and 2026-10-12)

| Item | Question | Outcome |
|---|---|---|
| P2-M8a-06 | An overlay or fault that wins only for a part of its selector is shown as effective | Done on 2026-10-11. The compiler now counts, per fault, the table cells it matches and wins over every source's table (`domain.Table.Coverage`, `compiler.Target.Coverage`), so the API reports a fault, overlay or profile activation that wins only some of its traffic as `partially_overridden`, one that wins all of it as `effective`, and one that wins none as `overridden`. A test checks the coverage cell by cell against `Resolve` on random worlds (`TestTheCoverageOfATableIsWhatResolveSaysCellByCell`), and an API test shows a network fault as `partially_overridden` next to an effective device fault (`TestAFaultThatWinsOnlyAPartOfItsTrafficIsPartiallyOverridden`). A fault that wins no traffic is no longer in `Target.Winners` or the preview's effective list. |
| P2-M8b-02 | Verification cannot see the HTB quantum or the netem distribution table | Done on 2026-10-12, through the first way of its acceptance (no maintainer decision needed): after an engine start a leaf the apply has not written counts as possibly holding a distribution table; an unchanged leaf that is to be uniform is left alone (its queue stays), the first change of that leaf makes it again, and a named table is written in place at the first apply, so a table left from before the start is gone at the first change of the leaf (`TestAfterARestartTheFirstChangeOfALeafDropsTheTableThePreviousRunLeft` on the real kernel, `TestAChangeToAUniformJitterMakesTheLeafAgainOnlyWhereATableMayBe`). The table memory keeps the leaves of the classes that wait for their deletion, so a class wanted again keeps its queue (`TestAnIdThatReturnsBeforeTheDeletionKeepsItsClass`). The HTB quantum turned out to be reported by the kernel (tc prints it with `-d` on 6.8 and 6.17), so the executor now reads classes with `-d` and verify compares the quantum; a quantum changed from outside is found and repaired in place (`TestTheRealKernelShowsAChangedQuantumAndTheApplyRepairsIt`). Not covered (documented in `docs/development.md`): a table written into a leaf while the engine runs, without changing what the listing shows, is replaced only at the next change of that leaf. |
| P2-M8b-04 | The tc state of every assigned interface is read at every apply and every verify | Done on 2026-10-11, except the arm64 measurement, which moved to P2-M9-06 (H1). A state read lists the qdiscs of all interfaces in one tc run and reads the full tc state only of the interfaces holding a tree: an apply with ten assigned interfaces and the tree on three runs tc 20 times to read instead of 38 (`TestAnApplyWithTenAssignedInterfacesListsTheQdiscsOnce`). `ReadHost` (the compiler's input) no longer reads the tc state at all. |
| P2-M8b-08 | The first packets of the first flow of a lab wait about a second on a fast kernel | Done on 2026-10-11. Cause: in the product-test topology Switch1's port q1 has the same interface index as its peer `lan1`, so the kernel's link-watch treats q1's carrier event as non-urgent and leaves the port disabled for up to a second after the topology is built; device C's first ARP request was lost and C's first packets waited for the 1 s retry. The testbed now returns the topology only when every veth end is up and every switch port forwards (`Bed.WaitLinks`, called by `NewDefault`; `TestTheFirstPacketOfEveryDeviceGoesThroughRightAfterTheTopologyIsBuilt`). The warm-up stays as `meetDevices` plus `settleIdentity`, documented: it exists only so the engine has discovered every device before a test compares its compile with the kernel, not for timing. |
| P2-M10-09 | One probe of a stream is not classified while other faults are changed | Done on 2026-10-11. Cause: every apply flushed and refilled the static nft sets and maps in one transaction, and the kernel's interval sets (pipapo, rbtree) can miss lookups during that commit; the new test `TestAFlowKeepsItsClassificationWhileOtherFaultsAreWritten` measured 23 to 122 misses per stream of about 150k on the native runner (6.17) and 7 to 136 on 6.8.0-142. The apply now changes set and map elements by their difference with the kernel listing, which missed none in every native, level-1b and emulated run, and `TestUpdatingOneFaultDoesNotDisturbTheOthers` passed in all six native level-1 runs and both level-1b runs. Known limit (documented): an element whose value changes (traffic that moves to another fault) is still deleted and added in one commit. |

### Decisions on the audit items (made 2026-10-12)

| Item | Question | Decision |
|---|---|---|
| P2-AUDIT-01 | A new device: (a) stable device numbers, so that it is an element update of the identity map, or (b) record that it takes the throttled full apply | **(a)**: device numbers stay stable across compiles; implemented (fourth table) |
| P2-AUDIT-07 | The per-night record of measurements (§4.3): (a) a JSON line per accuracy assertion, uploaded by every nightly matrix job, or (b) the job logs are the record | **(a)**: the measurement record as a nightly artifact; implemented (fourth table) |

### Closed after the audit (2026-10-12)

| Item | Finding | Outcome |
|---|---|---|
| P2-AUDIT-01 | A new device was still a full apply, not an element update as the M7 scope says (medium) | Done with option (a). Device numbers stay stable across compiles (`compiler.Input.DeviceNums`/`DeviceNumHigh`, fed back from the last applied target as `Input.FaultIDs` is); a new device takes the number above the highest one handed out, so a number is never reused while the process runs, and the numbering is not persisted, since nothing outside the identity map holds a number. The identity map always exists, and a device that comes or goes is an element update of the identity and classification maps; the 2 s window (M6a-07) now holds back only a full apply that devices need (a device with a queue of its own, D18), never an address change. `TestANewDeviceReachesTheIdentityMapWithoutAFullApply` shows on the real kernel that a new device arrives with element updates only, the generation marker unchanged and the known device keeping its number: 133 ms on level 1, 669 ms and 536 ms on level 1b with KVM (run 38263396695, draft PR #52), 445 ms and 679 ms on the nightly kernels 6.8 and 7.0 (38303832065); over kernelsim `TestANewDeviceIsAnElementUpdateNotARebuild` and `TestNewDevicesThatNeedAFullApplyConvergeOncePerWindow` (mutation-checked). |
| P2-AUDIT-02 | The revision preview omitted the tc diff, the class capacity per interface and the queues per scope (medium) | Done. The preview returns `linux.tc` (`apply.LinuxDiff.TC`, built from the same interface set and normalized trees that the apply's plan and verify compare, without seeds or counters), `capacity[]` (the classes of every interface with a tree, the IFB included, and the limit) and `queues[]` (one entry per winning fault or profile part with its queues, D18). `TestThePreviewOfARateOnANetworkShowsItsQueuesTheClassesAndTheTCChange` previews a 2 Mbit/s network fault over three known devices: 4 queues, 9 classes per interface, the tc change, responses validated against the spec. On the real kernel `TestThePreviewsTCDiffIsEmptyForTheKernelsTreeAndAChangeIsItsLeaves` passed on level 1 and level 1b with KVM (run 38261785647, draft PR #49) and in the emulated VM. |
| P2-AUDIT-03 | Plan §3.3 still described group and network levels and `meta l4proto` maps | Done. Plan §3.3 and the M7 scope line describe the four lookup levels the compiler builds: group, network and global scopes folded into the entries of each source (P2-M8a-01), a protocol-only selector as a level-3 element (level 1 with a destination) over the full port range of its protocol (tcp/udp 1-65535, icmp 0-65535). `docs/development.md` (M7, "The lookup chain") says the same and names `domain.portSels` and `compiler.clsKey`. |
| P2-AUDIT-04 | The concurrent-full-apply test covered only an observation after the apply returned | Done. `TestAnObservationDuringAFullApplyIsAppliedAfterIt` delivers the observation while `blockingNftExec` holds the full apply, then releases it: the identity map holds the new address without a further observation, moved by an element update after the full transaction. A loop that takes the update as already applied fails the test (mutation-checked). |
| P2-AUDIT-05 | A commit-confirm rollback ignored the overlays | Done. A rollback (confirmation timeout, an expired confirm, `Engine.Rollback`) settles the overlays for the restored revision: overlays that the rolled-back merge moved go back to the discovered device (`overlay.Store.Move`, `overlay_updated` with reason `moved`), and overlays of objects only the pending revision had are removed with `overlay_orphaned` (reason `rollback`), also when the restore's apply fails (`overlay.Store.Amend`); an activation of a profile that the restored revision gives a dns or tls part is removed too (P2-M11-05). Engine tests in `overlays_revision_test.go` and `profiles_test.go` and the API test `TestAnUnconfirmedRevisionTakesItsOverlayChangesBackWithIt` cover both cases (mutation-checked); all five CI jobs passed (run 38263860294, draft PR #51). |
| P2-AUDIT-07 | Measured distributions were not stored per night (§4.3) | Done with option (a). Every check of an accuracy assertion writes one JSON line (`testbed.Measure`, `testbed.Statistically`: test, assertion, flow, direction, quantity, kernel, accurate, N, configured and measured value, bounds, median/p5/p95, loss, attempt, verdict) into `$CHAOS_MEASUREMENTS`; the VM runner copies the guest's file out (`-measurements`). Each nightly matrix job uploads it as `measurements-<kernel>` for 90 days, `ci.yml` as `measurements-level1` and `-level1b`, and `tools/measurements` compares the nights. Verified on the dispatched nightly 38277262112: 447 lines per kernel (6.8.0-142, 7.0.0-38), 38 tests; on the integration branch (nightly 38303832065) 452 lines per kernel with the copies of P2-AUDIT-13, none failed. |
| P2-AUDIT-08 | The spec said `system_rules` lists the closed UI/API port | Done by changing the spec: `AccessRuleBody` and `AccessRulePage.system_rules` say that `system_rules` holds only the anti-lockout rule with its counter; the closure of the UI/API port is described in words, is not listed and has no counter of its own, as `GET /rules` and `docs/development.md` say. |
| P2-AUDIT-09 | `docs/development.md` described `cut_existing_requires_tcp` as "protocol is not tcp" | Done. The row reads "a rule whose protocol is neither tcp nor any", the rule `validate_rules.go` applies. |
| P2-AUDIT-10 | A rule-counter test compared the counter epoch with itself | Done. `TestReplacingARuleOverlayKeepsItsIdItsPlaceInTheKeyAndItsCounter` records the epoch before the replacement and asserts that it is non-zero, that the replacement belongs to a later generation and that the epoch is unchanged after it; a `trackRules` that gives a replaced rule a new epoch fails it (mutation-checked). |
| P2-AUDIT-11 | User-visible M10 decisions were missing from the spec and plan §2.4 | Done. `WireGuardAction` says `key_mismatch` uses a key derived from the overlay id and the real key, and `block_endpoint` needs an IPv4 endpoint, observed or configured as address:port (host names are not resolved), and is disabled otherwise (P2-M10-06); `NetemParams.rate` states the 8 bit/s minimum and `invalid_rate` (P2-M10-03); the `explain` description and the precedence paragraph of plan §2.4 name the MTU black hole as the one exception to "access rules first" (P2-M10-04). |
| P2-AUDIT-12 | BGP re-convergence after a tunnel blackout took 39 to 56 s under KVM | Done. Cause: the exported remote BIRD configuration (`bird.RenderRemote`, `chaosgw wg export --bird`) had BIRD's default error wait (60, 300 s), so after its hold timer ran out the remote side refused the gateway for a minute. The export now carries the gateway's `connect retry time 5; error wait time 1, 30;` (`bird.reconnectTimers`, both sides), and the test waits until both sides have dropped the session, then bounds re-convergence to 15 s, which follows from the timers. Measured 4.0 s native and 1.1 s under KVM (draft PR #54), 0.05 and 0.07 s on the nightly kernels 6.8 and 7.0; the cause and the figures are in `docs/development.md`. |
| P2-AUDIT-13 | No real-kernel test showed that a duplicated copy meets the class of the original | Done. `TestADuplicatingFaultDuplicatesAsConfiguredNextToOtherFaults` also runs `{latency: 50ms, duplicate: 10%}` and `{loss: 100%, duplicate: 50%}`: every copy arrives with the delay, nothing passes the loss, and the netem leaf of the class sends or drops the originals and the copies (`ProbeResult.UpCopies`/`DownCopies`); copy share and medians are checked under `testbed.Accurate()` and go into the measurement record. Passed natively (2000 + 199 sent by the leaf, copies at 50.07 ms), under KVM (2000 + 186) and emulated (600 + 66), and on the nightly kernels 6.8.0-142 and 7.0.0-38 with KVM (nightly 38303832065, dispatched on the integration branch: 183 and 220 copies of 2000, their median delay 50.09 and 50.21 ms). |
| P2-AUDIT-14 | The emulated branch of a Phase 2 engine test failed, undocumented, with no CI run | Done. `weekly.yml` runs `internal/engine` emulated in a job of its own (`-no-kvm -test-timeout 200m -vm-timeout 4h30m`). The emulated branch of both flapping tests counts its run in committed toggles (`emulatedFlapRun`) and accepts whole cycles skipped while an apply of a later generation ran. The dispatched runs 38271140920, 38283373614 and 38294081018 are recorded in `docs/development.md`; in the last the flapping tests passed, and every other failure of the three is a known emulated failure listed there. |
| P2-AUDIT-15 | `Capabilities.limits` was never filled | Done. `GET /capabilities` reports `limits.fault_ids` (4095, `compiler.MarkIDMax`) and `limits.class_limit_per_interface` (the effective class limit, `Engine.ClassLimit`); `TestSystemEndpoints` asserts both, also with a configured limit. |
| P2-AUDIT-16 | Stale rows in the documents | Done. Plan §8 no longer lists "ICMP type for reject" (M9 delivered `reject_with`); "Covered later" strikes the `NewOverlay` `expires_at` row and narrows the capabilities row to `step_types` → M15; the comment on `api.Features` names the milestones up to M11. |
| P2-AUDIT-17 | The configuration's `settings.class_limit_per_interface` was never read: the class limit came only from the engine's own configuration, which has no production flag, so the limit plan §3.3 calls configurable was not (found by the work on P2-AUDIT-15) | Done. Every compile takes the setting of the configuration it compiles (`Engine.classLimitOf`; without it the engine's limit, else the default of the architecture), so faults beyond it are refused with `capacity_exceeded`, a preview counts a candidate's classes against the candidate's own limit, and the capabilities report the applied configuration's limit. `TestTheClassLimitOfTheConfigurationIsTheOneEnforced` (a limit of 10 lets four faults in and refuses the fifth, a candidate raising it to 20 previews and applies with 20) fails when the compile ignores the setting (mutation-checked); the spec's `Settings.class_limit_per_interface` says so. |

### Items that stay open

P2-M9-06 (from the Phase 2 review); every item of the Phase 2 audit is closed. The reasons are the ones of "Why items
stayed open".

### P2-M9-06 The limits of the access rules and the cost of an apply are not measured on small hardware

- Status: new (extended on 2026-10-11 with the arm64 measurement left from P2-M8b-04; both measurements are in the Scope and Tests of plan H1)
- Severity: low
- Reason: env-limit (H1). The compiler refuses more than 1000 rules (overlays included) and more than
  65536 source addresses in all rule sets with `capacity_exceeded`, on every architecture. Rules are
  evaluated for the first packet of each connection only (established traffic is accepted first), so
  the chain is a linear scan per new connection; nothing in the repository measures what 1000 rules
  cost a Raspberry Pi at the connection rates of a test lab. Likewise the cost of an apply was only
  counted in tool runs, not timed on arm64: P2-M8b-04 cut the tc reads of an apply with ten assigned
  interfaces and the tree on three from 38 tool runs to 20 (one qdisc listing of all interfaces,
  `TestAnApplyWithTenAssignedInterfacesListsTheQdiscsOnce`).
- Evidence: `internal/compiler/access.go` (`DefaultRuleLimit`, `MaxRuleElements`); `internal/apply/state.go`
  (`readTC`, `devsWithTree`).
- Task: chosen interpretation — one limit for all architectures (the class limit has two because tc
  classes cost memory and CPU per packet; rules cost per connection). H1 measures new connections per
  second with 1000 rules on the target hardware and sets an ARM64 default if needed, and times an apply
  with ten assigned interfaces (the tree on three) on the arm64 runner against the overlay apply latency of plan §3.10 (≤ 200 ms on a Raspberry Pi 4/5).
- Acceptance: the H1 measurements exist; the rule limit is confirmed or lowered for ARM64, and the apply
  with ten assigned interfaces stays within the overlay apply latency of plan §3.10 (or the reads are reduced further,
  for example by keeping which interfaces carried a tree at the last apply).
- Needs maintainer: no
- Effort: S

## Phase 3 audit result

Date: 2026-10-14. Audited on the branch `phase-3-web-ui` (PR #56) at `3a120ad`, after the Phase 3 open items were
decided and implemented, against `docs/plan.md` §5 Phase 3 (M12, M13, M14), the behavior sections the milestones refer to
(§2.1–§2.17, especially §2.16 and §2.17, and §3.x), `api/openapi.yaml`, the web UI's sections and acceptance maps in
`docs/development.md`, the reference design `docs/ui/prototype/*.dc.html` (the plan wins where they disagree) and the code.
The audit read the branch through several lenses: backend and security, the shell with the overview, the devices, the
states of §2.17 and the visual system (M12), the faults, rules and profiles screens (M13), the networks and technical views
(M14), the interplay of the draft with other revisions, and the tests and CI. A re-review of the fixes on 2026-10-14 added
P3-AUDIT-35 and P3-AUDIT-36. Fixed: 2026-10-14 (P3-AUDIT-01 to P3-AUDIT-36), on the same branch, in the commits named below (the "close-phase-3" round).

**Phase 3 is complete** (merged to `main` on 2026-10-15, PR #56; the commit ids in this section and in the Phase 3
open items are those of the branch: PR #56 was merged by rebase, so on `main` the same commits have other ids and the
same subjects). Every milestone delivers its scope, every test the plan lists exists and passes
(level 0 locally; the Playwright suite against the mocked API; the three testbed browser flows in CI on `3a120ad`), and
every item this audit found is closed, each with a test or the corrected document. Two needed a maintainer decision,
made on 2026-10-15: P3-AUDIT-37 (the device detail's path view) belongs to M26, and P3-AUDIT-38 (a management hub drawn
among the test networks) was fixed with a row of its own. The fixes' new assertions in the testbed browser flow of M13
(`web/e2e-testbed/faults.spec.ts`, in `0b95142`: the device row shows "↑↓ 30 ms", the "With faults" filter, the active
fault on the device page), which depend on the gateway's new `DeviceView.effective`, passed in the `testbed-ui` job of
CI run 38646886557 on `aeeeed3`. Of the Phase 3 open items
only P3-M12-05 stays open; it waits for M27/M30.

No fix was blocked on a maintainer decision; where a fix had to choose, it took the option closest to the plan, and each
choice is recorded below under "Phase 3 audit: choices made in …". The maintainer confirmed these four on 2026-10-15:

1. **The setup's OS-configuration details without a token.** The unauthenticated `GET /setup` still names the files it
   cannot read and the netplan entries and NetworkManager connections (ids included) that configure an interface, as
   plan §3.4 wants the wizard to show them. Sending them only with a valid `X-Setup-Token` would need a change of the
   spec (today only `POST /setup` checks the token).
2. **The export-once WireGuard configuration across a logout.** A logout clears the query cache and the draft but keeps
   a configuration no dialog has shown yet (`ClientConfigDialog`'s `unseen`): it is the only copy of the client's private
   key, and dropping it would force a key rotation.
3. **The conflict dialog for an edit dialog on a moved base.** An edit dialog open while another revision was applied
   saves against the revision it opened on and meets the newer one in the concurrent-change dialog, rather than being
   reapplied silently onto it (where both changed the same field the user would not see theirs overwritten).
4. **Editors stay open while the own revision waits for confirmation.** Only Preview & apply is disabled; edits made
   meanwhile move onto the revision once it is confirmed. Plan §2.17's "controls that would change more are disabled
   until resolved" was not extended to every editor.

Each item is in exactly one row: an item that touches several milestones is in the row most of it concerns
(P3-AUDIT-10, the states, under M12; P3-AUDIT-17, mostly the rules and profiles, under M13), and the interplay of the
draft with other revisions (P3-AUDIT-26 to P3-AUDIT-29) is cross-cutting.

| Milestone | Verdict | Audit items (fixed on 2026-10-14 unless marked open) |
|---|---|---|
| M12 UI shell, overview, devices | done with gaps, fixed | P3-AUDIT-01, P3-AUDIT-03, P3-AUDIT-05 to P3-AUDIT-16, P3-AUDIT-36; decided 2026-10-15: P3-AUDIT-37 (→ M26), P3-AUDIT-38 (fixed) |
| M13 UI for faults, rules and profiles | done with gaps, fixed | P3-AUDIT-17 to P3-AUDIT-20 |
| M14 Networks view and technical view | done with gaps, fixed | P3-AUDIT-21 to P3-AUDIT-25 |
| Cross-cutting (interplay, API, tests and CI, documents) | done with gaps, fixed | P3-AUDIT-02, P3-AUDIT-04, P3-AUDIT-26 to P3-AUDIT-35 |

### Evidence

- **CI on the audited commit:** run 38571841356 on `3a120ad`, conclusion success (level 0, e2e, arm64, the testbed levels
  and the `testbed-ui` job with the browser flows of M12, M13 and M14).
- **Local, after the fixes:** `make test`, `make lint`, `make check-spec`, `make check-generated`, `make check-clients`,
  `npm run build` and `make test-e2e` pass.
- **CI after the fixes:** CI run 38646886557 on `aeeeed3` (branch head after the audit's fixes), conclusion success: level0, e2e, arm64,
  testbed level 1, testbed level 1b (VM) and the `testbed-ui` job with the three browser flows, among them the changed
  `web/e2e-testbed/faults.spec.ts` (`0b95142`).

### Items

Each item names the finding as the audit made it and the commit that fixed it (its message says what changed; the
development guide's acceptance maps name the tests).

| Item | Lens | Finding | Fixed in |
|---|---|---|---|
| P3-AUDIT-01 | backend | The preflight's netplan check did not take only the file that shadows the others (`/run` before `/etc` before `/lib`), dropped a file over a renderer per device type or a device it could not read, and missed a match by driver or by a key it cannot evaluate | `2ad96f4` |
| P3-AUDIT-02 | backend | The executor's comment claimed that only parts of the host's `/etc` are mounted | `2ad96f4` |
| P3-AUDIT-03 | security | The unauthenticated `GET /setup` read the host's network configuration through the executor on every call and quoted the executor's error | `48139ba` |
| P3-AUDIT-04 | backend | A requested rollback was audited twice, as the caller's and as a system rollback; the spec did not say that rolling back the setup's first revision reopens the setup | `9f71f91` |
| P3-AUDIT-05 | security | A logout kept the query cache and the draft for the next session in the same browser | `7c13fe1` |
| P3-AUDIT-06 | M12 | A locked-out login did not say how long to wait and offered new attempts the gateway refused | `068771b` |
| P3-AUDIT-07 | API | `DeviceView.effective` and `overlays`, declared by the spec, were never sent | `cd304e6` |
| P3-AUDIT-08 | M12 | The device list and detail computed a device's faults in the browser, not as the overview's chips from the gateway's winning faults | `0b95142` |
| P3-AUDIT-09 | M12 | Not every overview item linked to its detail (fault targets, events, the topology's boxes) | `0b95142` |
| P3-AUDIT-10 | states | Loading, failed and stale data read as an answer: the topology, recent events, routing sessions and uplink said "none" or "up", a failed overlay read left the rules list short, the Linux views did not say their counters are not live | `0b95142` |
| P3-AUDIT-11 | M12 | The device list did not mark an adoption that is not applied yet nor offer its Undo; its empty state had no primary action; Adopt shared a column | `0b95142` |
| P3-AUDIT-12 | M12 | The device detail did not show the router and DNS servers its network hands out | `0b95142` |
| P3-AUDIT-13 | M12 | Group names did not follow applied revisions, the uplink chart's time axis did not start at now in 24-hour time, the offline page stayed `aria-busy` | `0b95142` |
| P3-AUDIT-14 | accessibility | The focus did not return to the opener after a dialog or drawer, nor move to the replacement of a Remove/Delete/Undo swap | `2c88f12` |
| P3-AUDIT-15 | visual | Tall dialogs pushed their action row out of view | `2c88f12` |
| P3-AUDIT-16 | visual | Sizes outside the plan's type scale, `text-3` below 4.5:1 on `surface-2`, light native controls | `2c88f12` |
| P3-AUDIT-17 | accessibility | The selected rule was marked only by its colour, profile buttons did not name their profile, a UDP rule did not say why existing connections cannot be cut, the profile editor showed errors before any input; the conflict dialog's wording, the setup's interface selects, the access matrix's overflow, the verify step's wrapping and a hub's default endpoint | `2c88f12` |
| P3-AUDIT-18 | M13 | A TCP reset rule kept another protocol, which the gateway refuses | `e73807c` |
| P3-AUDIT-19 | M13 | The preview drawer could be closed while the apply ran, so a failed apply was not shown | `e73807c` |
| P3-AUDIT-20 | M13 | Explain did not name the tunnel of a tunnel family's fault | `e73807c` |
| P3-AUDIT-21 | M14 | WireGuard traffic arrows showed the gateway's side, not the remote side as plan §2.2.1 sets the directions | `a512ca9` |
| P3-AUDIT-22 | M14 | A link could not export its remote configuration and BIRD snippet in the UI | `a512ca9` |
| P3-AUDIT-23 | M14 | A client could not be given other clients' networks to reach | `a512ca9` |
| P3-AUDIT-24 | M14 | A new client of the draft could not be edited or undone on its own | `a512ca9` |
| P3-AUDIT-25 | M14 | A refused action on a client was not marked in its row | `a512ca9` |
| P3-AUDIT-26 | interplay | An edit dialog open while another revision was applied saved onto the newer revision, overwriting its fields unseen | `a7624a2` |
| P3-AUDIT-27 | interplay | A client edit whose hub the other revision deleted was kept on reapply | `a7624a2` |
| P3-AUDIT-28 | interplay | Edits made while the own revision waited for confirmation stayed on the old base once it was confirmed | `a7624a2` |
| P3-AUDIT-29 | interplay | An overlay that expired while its edit dialog was open was written again for 1 s ("Keep: 0:00 left", `remainingTtl` clamps to one second); Keep now says it expired and a new duration is required (`keepError`) | `a7624a2` |
| P3-AUDIT-30 | tests | A direct `testvm` run in which no test ran passed, and the `testbed-ui` job could stay green with a renamed browser flow | `0b067e6` |
| P3-AUDIT-31 | tests | Flaky e2e tests: the adopt-conflict test, the networks helpers' first render, assertion timeouts under CI load | `822cf08` |
| P3-AUDIT-32 | docs | The development guide and the open items did not record the fixes and the choices they made | `249b355`, `5baf400`, `b187f94` |
| P3-AUDIT-33 | docs | Plan §5 M14 Scope, the §2.17 Networks and Profiles rows and M30's Scope did not name what the open items added | `b187f94` |
| P3-AUDIT-34 | docs | The M12 to M14 Status lines, the header of this file and the README did not say the open items were closed; a Decision line marked open work | `dd99280` |
| P3-AUDIT-35 | docs | (re-review) No audit result for Phase 3 in this file and no phase-level Status line in the plan | this section and the Phase 3 Status line of `docs/plan.md`, in `263defe` |
| P3-AUDIT-36 | accessibility | (re-review) The lockout countdown re-rendered every second inside the login's `role="alert"`, so screen readers announced it again and again; the alert now names the time of day once, and the countdown beside it is no live region (`login.spec.ts` "a locked-out login says how long to wait") | `263defe` |
| P3-AUDIT-37 | M12 | The device detail's "path view MQTT → broker with fault badge" (plan §2.17) is neither built nor assigned to a milestone | decided 2026-10-15: option (a), M26 (below) |
| P3-AUDIT-38 | M12 | The overview topology lists a management WireGuard hub among the test networks, on the path with the fault badge | decided 2026-10-15: option (b), fixed (below) |

### Audit items decided by the maintainer

Two findings needed a maintainer decision; the maintainer took the recommendation for both on 2026-10-15.

#### P3-AUDIT-37 The device detail's path view has no milestone

- Status: closed (2026-10-15): decided — option (a). M26's Scope and Tests in `docs/plan.md` name the path view
  (device → gateway → uplink → a destination of its flows, with the fault badge), the §2.17 Device detail row marks it
  "(with M26)", and the development guide's list of left-out panels names it.
- Severity: minor (a planning gap; no wrong behavior).
- Reason: plan §2.17 lists "path view MQTT → broker with fault badge" for the device detail, and the reference design draws
  it (`docs/ui/prototype/DeviceDetail.dc.html`, "PATH · MQTT TO BROKER"). M12's Scope gives the device detail identity,
  lease, active faults and counters; the panels left out (running scenario, captures, diagnostics, DHCP and TLS test
  actions, flows and recent DNS, P3-M12-02 → M26) do not include the path view, and no milestone's Scope names it.
- Evidence: `docs/plan.md` §2.17 "Device detail" row; `docs/development.md` "Web UI (M12)", the list of left-out panels;
  `web/src/views/DeviceDetailView.vue`; M26's Scope names flows and DNS only.
- Options: (a) assign it to M26 (metrics and flow view), which brings the flows the path is drawn from: name it in M26's
  Scope and Tests and in the development guide's list of left-out panels; (b) build a static version now from the
  device's network, the uplink and the faults that win for it (`DeviceView.effective`), without per-flow data; (c) drop
  it from §2.17.
- Recommendation: (a). The path is only meaningful with the destinations the device talks to, which M26's flow view
  provides; M26 is optional for V1, so the path view becomes optional with it.
- Needs maintainer: no (decided 2026-10-15).

#### P3-AUDIT-38 The overview topology shows a management hub as a test network

- Status: closed (2026-10-15): decided — option (b). `TopologyCard.vue` keeps networks of role `management` off the
  test-network column and draws them in a row of their own below the gateway ("Management access · no faults apply"),
  without a device count or the fault badge; the gateway counts "N test networks · M management" (`overview.spec.ts`
  "a management network is drawn apart from the test networks, off the faulted path (P3-AUDIT-38)").
- Severity: minor (misleading picture, no wrong action).
- Reason: plan §2.17 draws the overview topology as "test networks → gateway → uplink" with the fault badge on the path.
  `TopologyCard.vue` puts every network in the test-network column, including a WireGuard hub of role `management`
  (tagged "· management"), and the gateway box counts it. A management network is not a test network (§2.2.1, §2.16) and
  faults never apply to it, so drawing it on the faulted path suggests otherwise. The Networks screen already shows
  management apart.
- Evidence: `web/src/components/overview/TopologyCard.vue` (`boxes` maps all networks); the audit's screenshot of the
  overview with "admin 10.98.0.0/24 · wg-admin · WireGuard · management" in the left column and "5 networks" on the gateway.
- Options: (a) leave networks of role `management` out of the topology and the gateway's count; (b) draw them in a
  separate row below the gateway, apart from the faulted path and without the fault badge; (c) keep it as is.
- Recommendation: (b): an admin sees the management access exists (useful when a fault seems to cut it), without it
  reading as a test network. (a) is the smaller change if the overview should show test traffic only.
- Needs maintainer: no (decided 2026-10-15).

### Checked and not recorded as items

Six further findings of the lenses were checked and rejected (the behavior matched the plan or the spec, or the finding
was a duplicate of an item above); they are not recorded as items.

## Phase 3 open items

Phase 3 (M12 to M14) is complete and merged to `main` (PR #56, 2026-10-15). The items below were found while implementing M12, M13
and M14; each records the option that was implemented so the work was not blocked, and what the maintainer may want to
decide. The reasons are the ones of "Why items stayed open". The maintainer decided every item that needed a decision on
2026-10-14; a "Decision (2026-10-14)" line records what was decided, and the item's Status line says whether it is closed
and how the decision was implemented. As of 2026-10-14 every Phase 3 item is closed except P3-M12-05, which waits for
M27/M30.

### P3-M12-01 The commit-confirm banner has no "Roll back now"

- Status: closed (2026-10-13, M14): option (a) — `POST /revisions/{id}/rollback` (only for the revision that is
  `pending_confirm`, the rollback the deadline runs, event `revision_rolled_back` with `reason` `requested` and, for every
  rollback, `restored`), and the banner's Roll back now (`TestARevisionWaitingForConfirmationIsRolledBackOnRequest`,
  `networks.spec.ts` "commit-confirm: Roll back now ends the waiting at once")
- Severity: low
- Reason: needs-decision. Plan §2.17 ("Commit-confirm") asks for an orange banner with a live countdown and the actions
  Confirm and Roll back now. The API has `POST /revisions/{id}/confirm`, but nothing that ends a `pending_confirm` revision
  at once; it is rolled back only at its deadline (or at a restart).
- Evidence: `api/openapi.yaml` (`/revisions/{revisionId}/confirm`, no rollback operation); `web/src/components/layout/AppShell.vue`.
- Task: implemented — the banner offers Confirm only and announces the rollback at the deadline. Options: (a) add
  `POST /revisions/{id}/rollback` (only for `pending_confirm`, the same rollback the timeout runs, event `revision_rolled_back`
  with reason `manual`) and the button with it, in M14, where the access matrix is the first lockout-relevant edit of the UI;
  (b) leave it to the timeout. Recommendation: (a).
- Acceptance: with (a), the endpoint exists with a test, and the banner's Roll back now restores the previous revision in a Playwright test.
- Needs maintainer: no
- Effort: S

### P3-M12-02 The device detail's flows and recent DNS are not shown yet

- Status: closed (2026-10-14): decided — the device's flows and its recent DNS queries stay with M26, as the plan
  says; plan §5 M26 names them in its Scope (a device's flows and its recent DNS queries on the device detail) and its
  Tests (Playwright for the device detail's flows and recent DNS queries); `docs/development.md` (M12) says so
- Severity: low
- Reason: deferred. Plan §2.17 lists flows, recent DNS, captures, the DHCP lease and test actions, TLS test and diagnose on
  the device detail. M12 scopes the detail to "the data available after Phase 2 (identity, lease, active faults,
  counters)"; M26 has the "flow view in the UI (flow data from M6a)". `GET /flows?device=` and `GET /dns/queries?device=`
  exist since M6a/M6b.
- Evidence: plan §5 M12 Scope, M26 Scope; `web/src/views/DeviceDetailView.vue`.
- Task: implemented — the detail shows identity, lease, active faults (removable overlays) and the counters (rates, active
  connections, last seen). Decide whether the device's flows and its recent DNS queries belong to M13 (with the device's
  faults) or stay with M26.
- Acceptance: the milestone that takes them lists them in its Scope and Tests.
- Needs maintainer: no (decided 2026-10-14)
- Effort: S

### P3-M12-03 Adopting a device applies directly instead of through the preview-and-apply drawer

- Status: closed (2026-10-13, M13): the adopt dialog puts the device into the configuration draft and opens the
  preview-and-apply drawer; a conflict opens the concurrent-change dialog (`devices.spec.ts` "adopting a discovered device:
  the name, what changes, then the preview drawer and the apply", "adopting while the draft is based on an older revision
  opens the conflict dialog: reload and reapply")
- Severity: low
- Reason: deferred. Plan §2.17 lists "adopt discovered devices" on the Devices screen and the principle "nothing changes the
  network without the user seeing what will change (preview)"; the preview-and-apply drawer and the concurrent-change dialog
  are M13's.
- Evidence: `web/src/components/devices/AdoptDialog.vue`.
- Task: implemented — the dialog says in words what changes (the device, its MAC or address, its network, the base
  revision, "traffic is not changed"), then creates the merge-patch candidate with `If-Match`, applies it and shows the
  verified toast; a `revision_conflict` is reported in the dialog and the candidate discarded. M13 moves adopt onto the drawer and
  the conflict dialog.
- Acceptance: M13's Playwright tests cover adopt through the drawer and a conflict on it.
- Needs maintainer: no
- Effort: S

### P3-M12-04 The setup wizard makes one test network per test interface

- Status: closed (2026-10-14): decided — the wizard stays as it is (one LAN network per test interface), and the network
  form of P3-M14-01 adds and edits a LAN network with several ports: its ports are chosen from the host's interfaces
  (`GET /system/interfaces`; the uplink, the management interface and the ports of other networks cannot be chosen) and
  go into `interfaces` by name and MAC (`web/src/components/networks/NetworkDialog.vue`, `web/src/lib/networkForm.ts`);
  tests `network-forms.spec.ts` "network forms: add a test network with several ports through the drawer; after the
  apply it is listed with both ports (P3-M12-04)", "network forms: edit a test network — a second port, a pool and DNS;
  the patch has only what changed, Undo takes it back (P3-M12-04)", `src/lib/networkForm.test.ts` "a test network with
  several ports, DHCP and DNS: no problem, and the network the spec accepts", `src/stores/draft.networks.test.ts` "a new
  test network with several ports is a change in words and the whole network in the patch; taken back it is none"
- Decision (2026-10-14): keep the wizard as is (one LAN network per test interface); several ports per LAN network are edited in
  the network form of P3-M14-01; the item closes when that form can do it
- Severity: low
- Reason: plan-error (underspecified). M12 says "assign test interfaces"; a LAN network can have several ports (plan §2.2).
- Evidence: `web/src/lib/setup.ts` (`buildSetupRequest`).
- Task: implemented — each chosen test interface becomes a LAN network of its own with a proposed name, gateway address
  and DHCP switch; more ports, WireGuard networks and the access matrix are edited afterwards (M14's networks view).
  Confirm, or ask for grouping several ports into one network in the wizard.
- Acceptance: the decision is recorded here; a change gets a Playwright test.
- Needs maintainer: no (decided 2026-10-14)
- Effort: S

### P3-M12-05 The safe-mode banner links to the plan, not to recovery instructions

- Status: new
- Severity: low
- Reason: deferred. Safe mode itself comes with M27 (the API reports `safe_mode` and `safe_mode_reason`, which the banner
  shows); there is no recovery document yet. The banner's link points to plan §3.9 "Updates and Recovery".
- Evidence: `web/src/components/layout/AppShell.vue`.
- Task: when M27/M30 write the recovery instructions, point the link at them (served with the app or in the docs).
  M30's Scope in docs/plan.md §5 names the recovery instructions and the banner link (added by the Phase 3 audit).
- Acceptance: the link leads to the recovery instructions.
- Needs maintainer: no
- Effort: S

### P3-M12-06 The image build with the web app was not run locally

- Status: closed (2026-10-13): CI run 38369081090 on PR #56 built the image for `linux/amd64` and `linux/arm64`
  with the web stage (job `arm64`), and the job `testbed UI flow` ran the first-start flow in the browser against the
  app served by `chaosgw` (`TestFirstStartInTheBrowserUntilADiscoveredDevice` passed)
- Severity: low
- Reason: env-limit. The devcontainer has no Docker daemon. `deploy/Dockerfile` builds the app in a Node stage and embeds
  it (`-tags webui`); `.dockerignore` lets the web sources in.
- Evidence: `deploy/Dockerfile`, `.dockerignore`.
- Task: the `arm64` CI job builds the image for both architectures; check its first run on this branch.
- Acceptance: the image builds in CI and `chaosgw api` in it serves the app.
- Needs maintainer: no
- Effort: S

### P3-M12-07 The netplan hints of the wizard are not taken from the preflight

- Status: closed (2026-10-14): decided — option (a): a preflight check per interface that is or may become a test
  interface (not the uplink or the management interface), made at each `GET /setup` and `GET /system/preflight`
  (`interface.<name>.os_config` with `interface`): the executor reads where netplan and NetworkManager configure an
  interface (`preflight.ReadOSConfig`, read operation `host_net`, `chaosgw exec --host-root /host`, the host's `/etc`
  mounted read-only at `/host/etc` in `deploy/compose.executor.yaml`; only the file, the entry and what it selects leave
  the executor), and the check names the file and the entry (or the NetworkManager connection) and what to remove, a
  warning for a candidate and a failure for an assigned test interface; `HostInterface.os_managed` comes from the same
  read. netplan's global NetworkManager renderer and an ethernet connection bound to no interface hand every interface
  to NetworkManager and are reported for each, with `unmanaged-devices` as the fix; an interface NetworkManager's
  `unmanaged-devices` names is not reported for it (NetworkManager's own defaults outside the files, such as an
  auto-default connection without a file, are not seen; docs/development.md). The report's `status` counts the host's
  checks and the assigned test interfaces', not a candidate's warning (before the setup the suggested uplink is a
  candidate too; after it an unused interface is the operating system's), so it does not stay `warn` for an interface
  the gateway does not use. The wizard shows the preflight's text for each chosen test interface ("Check again" reads it anew); the
  client-side `netplanHint` is removed. No CLI command shows the preflight; every client reads it from
  `GET /system/preflight`. `deploy/host-setup.sh` now says to delete the entry. Tests
  `TestThePreflightFindsTheOSConfigurationOfAnInterface`, `TestTheExecutorReadsTheHostsNetworkConfigurationWithoutItsContent`,
  `TestThePreflightSeesNetworkManagerTakeEveryInterface`,
  `TestThePreflightNamesTheNetplanEntryOrConnectionOfATestInterface`, `TestThePreflightSaysWhichOSConfigurationFileItCannotRead`,
  `TestThePreflightSaysNoCheckRanOnlyWhenNoneDid`, `TestAFailingReadOfTheHostsNetworkConfigurationIsLoggedOnce`,
  `TestExecutorContainerHardeningProfile` (the mount, read-only); `setup.spec.ts` "first start: the wizard sets up the
  gateway and confirms the revision" (the preflight's text for eth3, nothing for eth2 and the uplink, Check again)
- Severity: low
- Reason: needs-decision. M12 Scope says "netplan hints from the preflight", and plan §3.4 says the preflight checks that the
  test interfaces carry no OS configuration. The preflight (`internal/preflight`) has no per-interface netplan check yet;
  the wizard builds the hint itself from `HostInterface.os_managed` (`netplanHint` in `web/src/lib/setup.ts`). Since the
  M12 review it only says to delete the interface's entry, as §3.4 wants, and no longer offers an entry that turns the
  addressing off.
- Evidence: `web/src/lib/setup.ts` (`netplanHint`), `internal/preflight`.
- Task: implemented — the client-side hint. Options: (a) a preflight check per assigned or candidate interface that names
  the netplan file and entry (or the NetworkManager connection) to remove, shown by the wizard and the CLI alike, with
  M28's host-setup script; (b) keep the client-side hint. Recommendation: (a) with M28.
- Acceptance: with (a), the preflight reports the entry with a test, and the wizard shows the preflight's text in a Playwright test.
- Needs maintainer: no (decided 2026-10-14)
- Effort: S

### P3-M13-01 The last match of an access rule is the time the gateway first read the counter higher

- Status: closed (2026-10-14): decided — option (b): every access rule (in both hooks) and the anti-lockout rule carry
  the nftables `last` statement after their named counter (`compiler.lastStmt`); an apply carries the time of the
  kernel's rule of the same chain and counter over to the rule that replaces it (`Nft.Transaction`, `withLast`; late by
  at most the length of the apply), `Engine.ReadCounters` reads it from the ruleset listing (`linux.Ruleset.LastUsed`)
  and `Counter.last_match` is counted back from the read; `internal/api/matches.go` and its read-based time are gone.
  The technical view prints `last used …`. Tests `TestEveryAccessRuleAndTheAntiLockoutRuleNoteTheirLastMatch`,
  `TestATransactionCarriesTheLastMatchOverToTheRuleThatReplacesIt` (with `nft -c`), `TestTheLastStatementOfARuleIsReadByItsNamedCounter`,
  `TestRenderNftPrintsTheLastStatementAsNftDoes`, `TestARulesCounterSaysWhenItLastMatched`; on the real kernel (testbed)
  `TestTheKernelListsTheLastMatchAndTakesItOverFromTheRuleItReplaces` and `TestARulesLastMatchIsTheKernelsAndAnApplyKeepsIt`,
  which passed in the emulated VM (kernel 6.8.0-142, 2026-10-14) together with the kernel gates
  `TestEveryCompiledRulesetIsAcceptedByTheKernel`, `TestEveryCutWindowIsAcceptedByTheKernel` and the access rules' kernel tests
- Severity: low
- Reason: needs-decision. Plan §2.17 asks for "hits, bytes, last match" in the rule editor. nftables counters keep no
  time; the API now gives `Counter.last_match`, the time of the first read that found a rule's counter higher than the read
  before (`internal/api/matches.go`). It is exact to the interval between two reads (5 s while the rules screen is open),
  and unknown after a restart until a read sees the counter grow; a counter read rarely gives a late time.
- Evidence: `api/openapi.yaml` (`Counter.last_match`), `internal/api/matches.go`, `TestARulesCounterSaysWhenItLastMatched`.
- Task: implemented — the read-based time. Options: (a) keep it; (b) the nftables `last` statement on every rule's counter
  (kernel 5.17+, `last used <ms>` in the ruleset listing), compiled with the rule and checked by the kernel gates; (c) the
  engine reads the rule counters on its own every few seconds, so the time does not depend on a reader. Recommendation: (a)
  until a user needs it to the second, then (b).
- Acceptance: with (b) or (c), a kernel or engine test asserts the time.
- Needs maintainer: no (decided 2026-10-14)
- Effort: S (c), M (b)

### P3-M13-02 The concurrent-change dialog offers "Discard mine" next to "Reload and reapply mine"

- Status: closed (2026-10-14): decided — both buttons stay; plan §2.17 (the introduction's note on the prototype) and
  M13's Scope now say "reload and reapply mine · discard mine", as the states table does; `docs/development.md` (M13) too.
  The behavior is tested already (`rules.spec.ts` "a conflicting change made through the API opens the conflict dialog;
  reload and reapply mine", "the concurrent-change dialog can discard the draft instead")
- Severity: low
- Reason: plan-error (small contradiction). M13 Scope and the introduction of plan §2.17 name "reload and reapply"; the
  states table of §2.17 lists "reload and reapply mine · discard mine". The dialog offers both and never "overwrite theirs".
  Reapplying keeps the other side's fields and the user's where both changed the same field (P3-M13-07), and shows the
  combined result in the preview before anything is applied.
- Evidence: `web/src/components/apply/ConflictDialog.vue`, `web/src/stores/draft.ts` (`rebase`).
- Task: implemented — both buttons. Confirm, or drop "Discard mine" (the bar's Discard does the same afterwards).
- Acceptance: the decision is recorded here; the plan's two sentences say the same.
- Needs maintainer: no (decided 2026-10-14)
- Effort: S

### P3-M13-03 Custom profiles are not created or edited in the UI

- Status: closed (2026-10-14): decided — option (a): a profile editor on the profiles screen ("New profile"; "Edit" and
  "Delete" on a custom profile's card) with the name (unique, never a built-in profile's), the description, an
  impairment part with the fault dialog's impairment fields (each direction its own, burst loss, flapping, …) and an MTU
  part (size and mode), the parts the API accepts; a DNS or TLS part a profile has is kept. The profile goes into the
  draft (`profiles` in `src/stores/draft.ts`: changes in words, merge patch, Undo of a deletion, the gateway's problems,
  reapply after a concurrent change), the card is marked NEW, CHANGED or DELETED and is activated after the apply
  (`ProfileDialog.vue`, `src/lib/profileForm.ts`, `ProfilesView.vue`). Tests `profiles.spec.ts` "create a profile
  through the editor and the drawer, then activate it (P3-M13-03)", "edit and delete a custom profile through the
  draft; a refused profile shows why (P3-M13-03)" (also: saved unchanged it records nothing, the edit's merge patch),
  "a custom profile saved without a change is no change, also where the editor reads it in another form (P3-M13-03)";
  "delete an active custom profile: the card says its activations end, the drawer lists them and Apply waits for
  "Remove them" (P3-M13-03)"; `src/lib/profileForm.test.ts`; `src/stores/draft.test.ts` "a custom
  profile is added, changed and deleted through the draft (P3-M13-03)", "reapplies a profile on their revision and
  names a field both changed (P3-M13-03)", "a profile edit of burst loss values or of a part the chips do not show is a
  change (P3-M13-03)", "a profile loss changed on mine, burst loss on theirs: the dialog names the parameters,
  reapplying takes mine whole (P3-M13-03)". Review follow-ups (2026-10-14): since the editor switches between flat and
  per-direction parameters and between random and burst loss, "Reload and reapply mine" (P3-M13-07, still member by
  member) takes the user's impairment part whole where the merged one would be refused (flat with upload/download, loss
  with burst loss, blackout with flapping, a jitter above the latency; `impairmentCombines` in `src/lib/faults.ts`,
  `reapplyObject` in `src/stores/draft.ts`) and the conflict dialog names it as "parts impairment: their chips → mine";
  the `</> API` panel shows the merge patch the preview sends (`objectPatch`: against the base revision's profile, a
  new profile whole) instead of the whole profile
- Decision (2026-10-14): option (a) — a profile editor in the UI (create, edit, delete custom profiles with the impairment and MTU parts
  the API accepts) through the draft and the preview drawer; a Playwright test creates a profile through the drawer and
  activates it
- Severity: low
- Reason: needs-decision. M13 Scope lists "profile cards"; the prototype has a "New profile" button, plan §2.17's Profiles
  row does not. Custom profiles are objects of the configuration (P2-M11-02), so an editor would be a form over the draft
  like the fault dialog (parts per family). "Measure with probe" waits for the probes (M25).
- Evidence: `web/src/views/ProfilesView.vue`; plan §2.17 "Profiles", §5 M13, M25.
- Task: implemented — the cards show built-in and custom profiles, activate and deactivate; custom profiles are made through
  the API. Options: (a) a profile editor (impairment and MTU parts) over the draft in M14 or later; (b) leave it to the API and
  the configuration import. Recommendation: (a), small, after M14.
- Acceptance: with (a), a Playwright test creates a profile through the drawer and activates it.
- Needs maintainer: no (decided 2026-10-14)
- Effort: S

### P3-M13-04 The fault dialog edits impairment faults only

- Status: closed (2026-10-14): decided — option (a): the dialog creates and edits every fault kind the API offers. A new
  fault has a kind (Impairment, MTU / PMTUD, Tunnel; no tunnel on a device's page); an MTU fault has its traffic, the
  packet size and the mode (ICMP, black hole, MSS clamp, each with its effect in words), a tunnel fault the WireGuard
  client or link and only the parameters a tunnel takes (no target, no traffic); an impairment fault has under Advanced
  the delay distribution, burst loss (Gilbert-Elliott) or loss correlation, the queue limit and flapping besides rate,
  reorder, duplicate, corrupt and keep order, a destination of any kind (uplink, network, address or prefix, host name),
  and "Each its own" for different parameters per direction. The faults screen offers Edit for every impairment, MTU and
  tunnel fault, overlay or configured; `hiddenParameters` and the note "saving replaces them" are gone, since nothing is
  hidden any more. The impairment fields are one component the profile editor shares (`ImpairmentFields.vue`,
  `src/lib/netemForm.ts`; `src/lib/faultForm.ts`, `FaultDialog.vue`); plan §2.17's "Add fault" row lists them. Tests
  `fault-kinds.spec.ts` "an MTU fault: created with the kind MTU / PMTUD, then edited on the faults screen", "a tunnel
  fault: edited with the parameters a tunnel takes, and one configured on a link through the draft", "the advanced
  impairment parameters: burst loss, flapping, a distribution, a queue limit and each direction its own", "a configured
  fault saved without a change is no change; an edit of an MTU fault writes only what changed", "a configured fault
  whose burst loss changed is a change: the bar counts it, the row is CHANGED, the revision message names it";
  `src/lib/faultForm.test.ts` "every fault kind in the dialog (P3-M13-04)"; `src/stores/draft.test.ts` "names every
  fault field the dialog edits: burst loss values, keep order, any destination and the tunnel (P3-M13-04)" (the draft's
  changes in words cover every field the dialog edits). Review follow-ups (2026-10-14): "Reload and reapply mine" takes
  the user's parameter set of a configured fault (flat parameters with upload and download) whole where the member-by-
  member merge would make one the gateway refuses (`mixed_directions`, `mutually_exclusive`, a jitter above the
  latency), and the conflict dialog names it as "parameters: their chips → mine"; parameters that combine are still
  merged member by member. The `</> API` panel of a configured fault shows the merge patch the preview sends
  (`objectPatch`), so a flat fault made "Each its own" shows `"latency": null`. Tests `src/stores/draft.test.ts` "a
  fault made "each its own" on mine, a flat loss added on theirs: the dialog names the parameters, reapplying takes mine
  whole (P3-M13-04)", "blackout on mine, flapping on theirs: the merged parameters would be refused, so mine is taken
  whole", "parameters that combine are still merged member by member: their loss and my latency", "the merge patch of
  one fault or profile for the editors' API panel: what changed against the base revision, a new one whole";
  `src/lib/faults.test.ts` "impairment parameter sets"; `fault-kinds.spec.ts` "the API panel of a configured fault
  shows the merge patch the preview sends: a flat fault made "Each its own" deletes its flat parameters"
- Decision (2026-10-14): option (a) — the dialog creates and edits every fault kind the API offers: MTU/PMTUD and tunnel faults (M10)
  and the advanced impairment parameters (burst loss, flapping, distributions, different parameters per direction); no
  more "saving would replace hidden parameters"; Playwright tests edit one of each new kind
- Severity: low
- Reason: deferred. Plan §2.17 specifies the dialog with the impairment parameters (target, traffic, direction, presets,
  latency, jitter, loss, duration; rate, reorder, duplicate, corrupt, keep order). MTU and tunnel faults (M10) and burst
  loss, flapping, distributions and different parameters per direction are faults the API makes; the faults screen lists
  them, removes them and explains them, but offers no Edit for them, and the dialog names such parameters before saving
  would replace them.
- Evidence: `web/src/components/faults/FaultDialog.vue`, `web/src/lib/faultForm.ts` (`hiddenParameters`).
- Task: implemented as described. Decide whether MTU and tunnel faults (and the advanced impairment parameters) get fields
  in the dialog, and in which milestone.
- Acceptance: the milestone that takes them lists them; a Playwright test edits one.
- Needs maintainer: no (decided 2026-10-14)
- Effort: S

### P3-M13-05 The browser flow of M13 in the testbed

- Status: closed (2026-10-13): CI run 38411177290 on PR #56, job `testbed UI flow`, ran
  `TestAFaultCreatedInTheBrowserTakesEffectOnTheWire` and it passed
- Severity: low
- Reason: env-limit. `TestAFaultCreatedInTheBrowserTakesEffectOnTheWire` needs network namespaces; here it runs only in the
  QEMU VM under software emulation (level 1b) and in CI (job `testbed-ui`).
- Evidence: `internal/api/e2e_ui_test.go`, `web/e2e-testbed/faults.spec.ts`, `docs/development.md` "Web UI: faults, rules
  and profiles".
- Task: the local VM run passed (2026-10-13, recorded in docs/development.md: A 2.5 → 210.5 ms, B 6.8 → 68.6 ms); check
  the first CI run of the job.
- Acceptance: the job `testbed-ui` passes with both browser flows.
- Needs maintainer: no
- Effort: S

### P3-M13-06 Editing an overlay of another owner replaces it with the admin's

- Status: closed (2026-10-14): decided — options (a) and (d): an edit takes another owner's overlay over (as before) and
  keeps the overlay's remaining time unless the user chooses another duration. The dialog's first duration is "Keep:
  4:12 left of 5 min"; Apply sends the overlay's own TTL again and the remaining time on the gateway's clock in whole
  seconds as the new write-only API field `expires_in` (`OverlayRequest`, marked `writeOnly`; the code generators
  ignore the flag, so the read types carry an `expires_in` the gateway never sets; only with a `ttl` at least as long; the first
  removal comes after it, the overlay keeps `ttl` as written), which also covers the taken-over overlay, a new one of the
  admin. So the expiry stays where it was and the TTL stays the overlay's own: a TTL that is not one of the choices
  (`30m`) is shown as "30 min (its own TTL)" and kept, never turned into 15 minutes nor into the remaining seconds
  (`durationChoices`, `remainingTtl`, `ttlText` in `src/lib/faultForm.ts`; `overlay.Store.Put`,
  `domain.ValidateOverlay`). Tests `TestExpiresInKeepsTheRemainingTimeAndTheTTL` (`internal/overlay`),
  `TestOverlayRequestRules` "expires_in …", `TestNewOverlayFillsKindOwnerAndTimes` (`internal/domain`),
  `faults.spec.ts` "editing a fault keeps its expiry; a TTL the dialog does not list is shown and kept (P3-M13-06)",
  "edit a fault overlay of an API token: the admin's fault replaces it, and the dialog says so" (the token's 50 minutes
  kept), `src/lib/faultForm.test.ts` "an edited overlay keeps its remaining time unless another duration is chosen
  (P3-M13-06)", "a TTL that is not one of the choices is shown as such and kept (P3-M13-06)"; `internal/api` `TestAnEditWithExpiresInKeepsItsTTLAndExpiresAfterTheRemainingTime`
- Severity: low
- Reason: needs-decision. Plan §2.1.1 keys an overlay by *(owner, kind, target, selector)*, and a UI action's owner is the
  admin user (§2.15). Writing an edited overlay of an API token or a scenario run from the browser therefore creates a
  second overlay instead of replacing it. The plan does not say how the UI treats other owners' overlays.
- Evidence: `web/src/components/faults/FaultDialog.vue` (`submit`, the owner note), `web/e2e/faults.spec.ts` "edit a fault
  overlay of an API token: the admin's fault replaces it, and the dialog says so".
- Task: implemented — option (a), closest to what "Edit" promises: the dialog names the owner ("Set by the token
  ci-runner. Applying removes it and puts a fault of yours in its place."), writes the admin's overlay and removes the old
  one whenever the gateway answers with another id. Options: (a) take it over, as now; (b) show other owners' overlays
  read-only with their owner (remove only), as the overlay rules are. A run whose overlay was taken over keeps running and
  may write its step's overlay again. Second question: an edit sends the overlay's TTL again, so the countdown starts over
  (a 5-minute fault with 1 minute left gets 5 minutes), and a TTL the dialog does not list (e.g. `30m` from the API) becomes
  15 minutes. Options: (c) start a new TTL, as now; (d) keep the remaining time (send it as the TTL, or a `keep_ttl` field
  in the API). Recommendation: (a) and (c), with the dialog listing the overlay's own TTL when it is not one of its choices.
- Acceptance: the decision is recorded here; with (b), a Playwright test shows another owner's overlay without Edit; with
  (d), a test edits a fault and keeps its expiry.
- Needs maintainer: no (decided 2026-10-14)
- Effort: S

### P3-M13-07 "Reload and reapply mine" takes the user's value of a field both sides changed

- Status: closed (2026-10-14): decided — the per-field merge stays, with option (b): the conflict dialog lists the
  fields both sides changed under "Changed on both sides · theirs → yours" ("Rule no-dns-2 action: TCP reset → Reject";
  also an object they changed that the user deletes and one the user changed that they removed) before reapplying, and
  its text points at them as the fields where the user's value wins (`overlaps` in `src/stores/draft.ts`, `bothChanged`
  in `src/lib/mergePatch.ts`); tests `rules.spec.ts` "the conflict dialog names a field both changed, their value →
  mine, and reapplying takes mine (P3-M13-07)", "the conflict dialog lists no field when the two changes do not meet",
  "the conflict dialog names the rule order when both moved rules, and reapplying takes mine (P3-M13-07)" (the rule
  order is listed as "Rule order: theirs → yours" when both moved rules to different orders),
  `src/stores/draft.test.ts` "names the fields both sides changed, their value and mine, before reapplying (P3-M13-07)",
  "names the rule order when both moved rules to different orders (P3-M13-07)",
  "names a client field both sides changed; …", `src/lib/mergePatch.test.ts` "names the members both edits changed to
  different values, a scope as a whole". A scope or a destination both changed (a rule's From or destination, a
  fault's scope, a client's or a link's remote network) is listed as one field and reapplied as the user's whole, so
  reapplying does what the dialog says and never merges two choices into one scope (`reapplyMergePatch` in
  `src/lib/mergePatch.ts`); tests `rules.spec.ts` "the conflict dialog names a rule's From both changed, their scope →
  mine, and reapplying sends exactly mine (P3-M13-07)", `src/stores/draft.test.ts` "a scope or a destination both
  changed: the dialog names it, and reapplying takes mine whole (P3-M13-07)", `src/lib/mergePatch.test.ts` "reapplies
  my edit on theirs: their fields stay, a scope I changed is mine as a whole". With the editors of P3-M13-03 and
  P3-M13-04 an impairment parameter set merged member by member could become one the gateway refuses where neither
  side's was; such a set is now reapplied as the user's whole and listed as one field ("parameters", "parts
  impairment"), see those items
- Severity: low
- Reason: needs-decision. Plan §2.17 says only "reload and reapply mine". Reapplying merges field by field: the user's
  change of an object (a merge patch against the old base) is applied to the other side's version, so their fields stay and
  a field both changed takes the user's value. The dialog shows both changes and the preview shows the combined result, but
  it does not name the fields both sides changed.
- Evidence: `web/src/stores/draft.ts` (`rebase`), `web/src/components/apply/ConflictDialog.vue`, `src/stores/draft.test.ts`
  "reapplies field by field where both changed the same object: …", `rules.spec.ts` "reload and reapply where both changed
  the same rule: their field stays, mine is added".
- Task: implemented — the user's value wins per field. Options: (a) keep it; (b) the dialog names the fields both sides
  changed (their value → mine) before reapplying. Recommendation: (a), with (b) when users report surprises.
- Acceptance: the decision is recorded here; with (b), a Playwright test shows a field both changed in the dialog.
- Needs maintainer: no (decided 2026-10-14)
- Effort: S

### P3-M13-08 The scope selectors do not offer remote networks as a choice

- Status: closed (2026-10-14): decided — option (a): the fault dialog's target, the profiles' "Activate on" and the rule
  editor's From offer each WireGuard client with client networks ("Client networks · rA (10.50.0.0/24)"), each link with
  its prefixes and "Remote network · a prefix inside them…" with a field for the prefix, sent as `remote_network`
  (`src/lib/scopes.ts`, `src/components/ui/ScopeSelect.vue`); the prefix is checked under the selector, and the
  gateway's `unknown_remote_network` stands there too. A rule's To keeps the model's destinations (`Destination` has no
  `remote_network`; a remote prefix is an address or prefix there). Tests `faults.spec.ts` "a fault on a WireGuard
  client's networks: the target offers the remote networks and the request carries the scope (P3-M13-08)", "a fault on a
  prefix inside a remote network: …", `profiles.spec.ts` "activate on a link's remote networks or a prefix inside them
  (P3-M13-08)", `rules.spec.ts` "a rule from a prefix inside a WireGuard client's networks: From offers the remote
  networks and takes the prefix, the patch carries it (P3-M13-08)" (the prefix choice stays while its prefix is empty),
  `src/lib/scopes.test.ts` (the prefix is checked as the API's `remote_network.cidr`: a prefix length is required and the
  host bits must be zero, so `10.50.0.5` and `10.50.0.5/25` are refused in the browser), `src/lib/faultForm.test.ts` "keeps the choice "a prefix inside a remote network" while its
  prefix is still empty"
- Severity: low
- Reason: needs-decision. A fault, an overlay and an access rule can act on a `remote_network` (the client networks of a
  WireGuard client, the prefixes of a link, or a CIDR inside them; plan §2.4 puts it on the network level). The scope
  selector of plan §2.17 lists only everything, network, group and device, and so do the fault dialog, the profile scope and
  the rule editor's From.
- Evidence: `web/src/lib/faultForm.ts` (`targetOf`, `scopeOf`), `web/src/components/faults/FaultDialog.vue`,
  `web/src/views/ProfilesView.vue`, `web/src/components/rules/RuleEditor.vue`; `src/lib/faultForm.test.ts` "keeps a remote
  network target when a fault is read back and sent again unchanged".
- Task: implemented — an existing remote-network scope is kept as it is (shown as "Remote network 10.9.0.0/24", "Remote
  networks of link …", "Client networks of …" and sent back unchanged; before the M13 review it was read as "Everything"),
  but it cannot be picked for a new fault, profile activation or rule. Options: (a) offer the WireGuard clients and links
  (and a CIDR field) in the selectors, with M14's WireGuard views; (b) keep them API-only. Recommendation: (a), in the
  milestone that takes the client actions of P3-M14-01; M14 did not add them (it shows the clients' networks in the
  WireGuard views only).
- Acceptance: with (a), a Playwright test creates a fault on a client's networks and the request carries the scope.
- Needs maintainer: no (decided 2026-10-14)
- Effort: S

### P3-M14-01 Networks, clients after they were added and the routing configuration are not edited in the UI

- Status: closed (2026-10-14): option (a) in full. Part 1, the client actions — a WireGuard client of the gateway has
  Disable/Enable, Rotate keys (the next key generation for generated keys, the new public key for a client's own key)
  and Delete, each an edit of the configuration draft with Undo, through the preview drawer, the concurrent-change
  dialog and commit-confirm for a client of a management hub (`ClientsCard.vue`, `RotateKeysDialog.vue`,
  `src/lib/networks.ts`; the actions and Undo start from the client as the draft's base revision has it, so a disable
  and an enable leave no change also for a client with an explicit `enabled: true`, and the note "New keys once
  applied" compares with that client too, so a rebase onto another revision's key rotation shows no rotation of the
  draft); tests `networks.spec.ts` "client actions: …" (seven tests, among them "client actions: a draft rebased onto
  another revision's key rotation shows no rotation of its own (P3-M14-01)"), `src/lib/networks.test.ts` "the client
  actions (P3-M14-01)", `src/stores/draft.test.ts` "the client actions are changes in words and a merge patch of
  their fields (P3-M14-01)". Part 2, the network forms — Add network, and Edit, Delete and Undo of the selected network
  (`NetworkDialog.vue`, `NetworkSettings.vue`, `NetworksView.vue`, `src/lib/networkForm.ts`, `src/lib/networkChanges.ts`,
  the draft's `setNetwork`): a local test network with its gateway address and subnet, one or several ports, DHCP (pools,
  lease time, options), DNS (redirect, DoT, static entries), downstream routes and masquerade; a WireGuard hub or link
  with its role, listen port, MTU, endpoint and, for a link, the remote side (address, endpoint, keepalive, keys) and its
  static routes; the checks under the fields, the gateway's messages under the fields (Fix), and for a deleted network
  that something still refers to the gateway's report in the drawer ("refers to IoT, which this change deletes"; backend
  `TestDeletingAReferencedNetworkNamesEveryReference`). Part 3, the routing forms — the configuration card of the routing
  view with Settings (router id, local AS, external mode with its import filter) and Add protocol, Edit, Delete and Undo
  per protocol (`ProtocolDialog.vue`, `RoutingSettingsDialog.vue`, `src/lib/routingForm.ts`, the draft's `setRouting`,
  `setProtocol`): BGP, OSPF or Babel on a link with what it announces, its import filter, the settings of its type and a
  custom snippet. Everything goes through the draft, the preview drawer, the concurrent-change dialog and commit-confirm,
  which the gateway asks for when a WireGuard network with the role management is added, changed or deleted. Tests
  `network-forms.spec.ts` (nineteen tests: create, edit and delete a test network through the drawer; a new network
  undone or deleted again goes back to the list; DHCP off with a changed subnet drops the hidden pools that no longer fit; create and edit a
  hub and a link; the management hub admin deleted and confirmed; the link site-b deleted, first refused with its BGP
  protocol named, then together with the protocol; validation errors under the fields, also under Advanced, which opens
  for them; a port the configuration names by MAC only; a refused network fixed; the references of a deleted network — a
  device, a fault, rules, the access matrix and a routing protocol; a management hub waiting for confirmation; the
  conflict dialog; a LAN network with several ports; and the routing: a protocol added, edited, deleted, the BIRD
  settings, the hidden fields of the external mode switched off, validation errors and a refused protocol fixed), `src/lib/networkForm.test.ts`,
  `src/lib/routingForm.test.ts`, `src/stores/draft.networks.test.ts`; `docs/development.md` "Web UI: networks, WireGuard,
  routing and the technical view (M14)".
- Decision (2026-10-14): option (a) in full — client actions (disable/enable, rotate keys, delete), network forms (create, edit,
  delete LAN networks with subnet, several ports, DHCP, DNS, and WireGuard networks with kind, role, endpoint, …, as the
  model allows) and routing forms (the protocols of plan §2.2.2), each through the draft, the preview drawer, the conflict
  dialog and commit-confirm where lockout-relevant; a Playwright test for each through the drawer
- Severity: low
- Reason: needs-decision. M14 Scope lists "network cards and detail", "WireGuard networks with client list, client status,
  add-client dialog", "routing view" and "access matrix editing". Plan §2.2.1 also names creating, editing and deleting
  WireGuard networks, enabling/disabling clients, rotating their keys and deleting them; the prototype has "Add network";
  §2.2.2 configures the routing protocols in the model.
- Evidence: `web/src/views/NetworksView.vue`, `web/src/components/networks/`; plan §2.2.1 "Managing networks and clients",
  §2.17 "Networks".
- Task: implemented — the networks view shows every network, adds WireGuard clients and edits the access matrix through the
  draft; networks, the settings of existing clients and the routing are changed through the API or the configuration import.
  Options: (a) forms for a network (LAN: subnet, ports, DHCP, DNS; WireGuard: kind, role, endpoint), client actions (disable,
  rotate keys, delete) and the routing protocols, each through the draft, in a milestone of their own; (b) keep them API-only
  in V1. Recommendation: (a) for the client actions (small, the draft supports clients already), (b) for the rest until users
  ask.
- Acceptance: the milestone that takes them lists them; a Playwright test edits each through the drawer.
- Needs maintainer: no (decided 2026-10-14)
- Effort: S (client actions), M (network and routing forms)

### P3-M14-02 An access matrix change never waits for confirmation

- Status: closed (2026-10-14): option (a) — the matrix is not lockout-relevant and is applied at once; M14's Scope in plan
  §5 now says "access matrix editing (commit-confirm where lockout-relevant)", as §2.17 "Networks" does;
  `docs/development.md` (M14, its acceptance map) says the same. Tested already (`networks.spec.ts` "the access matrix: a
  click changes a cell in the draft, the bar counts it, the preview and the apply", the commit-confirm tests of a client
  of a management hub)
- Severity: low
- Reason: plan-error (small contradiction). M14 Scope says "access matrix editing with commit-confirm"; plan §2.17
  ("Networks") says "commit-confirm only if it is lockout-relevant", and the gateway's `LockoutRelevant` counts the
  management settings, the uplink in the two-port topology and management-role WireGuard networks, not the matrix: the
  control plane is protected by gateway protection and the anti-lockout rule, which the matrix cannot change.
- Evidence: `internal/engine/lockout.go`; `web/src/components/networks/AccessMatrix.vue`; `web/e2e/networks.spec.ts`.
- Task: implemented — the matrix goes through the draft, the preview and the apply, and the apply waits for confirmation
  when the gateway says so; the UI's commit-confirm is shown with the change of the networks view that is lockout-relevant, a
  client of a management hub (mocked API and testbed flow). Options: (a) keep it; (b) count matrix entries that touch the
  management network or a management-role WireGuard network as lockout-relevant. Recommendation: (a); reword M14's Scope to
  "access matrix editing (commit-confirm where lockout-relevant)".
- Acceptance: the decision is recorded here and the plan's two sentences say the same.
- Needs maintainer: no (decided 2026-10-14)
- Effort: S

### P3-M14-03 The technical view shows the kernel state per network and as a whole, not per device, rule or fault

- Status: closed (2026-10-14): decided — option (a): `GET /state/linux/{kind}/{id}` (`engine.LinuxObjectView`,
  `apply.RenderObject`) cuts the kernel state down to what the compiler made for one device (the identity map element
  with its numeral, the classification elements whose source covers its addresses, the queues of its own), access
  rule (its rule in both hooks with counter and `last`, the named counter, the source set; also an overlay rule and
  `system:anti_lockout`) or fault (its `mark_<id>` chains with counters, the elements that go to them, its tc classes,
  netem queues and filters, a tunnel fault's ingress filter, an MTU fault's chain and elements), found by the names and
  keys of the applied target; "Show Linux view" on the device detail, in the rule editor and on the row of a fault
  the compiler puts into the kernel (impairment, MTU, tunnel, a profile activation; a DNS, TLS, DHCP or WireGuard fault
  offers none) (`LinuxObjectExcerpt.vue`). Tests `TestTheLinuxViewOfOneRuleFaultOrDeviceShowsWhatTheCompilerMadeForIt`,
  `TestTheLinuxViewFindsARuleByANameThatOnlyOneRuleCanCarry`,
  `TestRenderObjectShowsOnlyWhatTheSelectionNames`, `TestTheLinuxViewSelectsAnObjectByTheNamesTheCompilerGaveIt`,
  `TestAMapKeyCoversAnAddressAsAnAddressPrefixOrRange`; `linux-view.spec.ts` ""Show Linux view" on a rule shows its
  compiled rule and counter; another rule closes it, one the kernel lacks says so (P3-M14-03)", ""Show Linux view" on
  a fault shows its mark chain, classes and netem queues with their counters below its row; a DNS fault has none
  (P3-M14-03)", ""Show Linux
  view" on a device shows its identity element and the classification of its address (P3-M14-03)"
- Severity: low
- Reason: deferred. Plan §2.17 "Technical view": "Per object, the compiled configuration next to the domain view …
  Reached from 'Show Linux view' / 'Show Linux changes' links". M14 Scope names the "compiled-state view (nftables, tc,
  routes, WireGuard)". The gateway renders the `nft -j` listing in nft syntax itself (`linux.RenderNft`); an expression the
  renderer does not know is shown as its JSON.
- Evidence: `internal/api/system.go` (`GetLinuxState`), `internal/apply/render.go`, `internal/linux/nftrender.go`,
  `web/src/views/LinuxStateView.vue`, `web/src/lib/linuxView.ts`.
- Task: implemented — `GET /state/linux` and the Linux view with its four parts and a word filter; "Show Linux view" on a
  network (its interface and subnet) and on the access matrix (the forward chain); the preview keeps its "Show Linux
  changes". Options: (a) "Show Linux view" on a device (its identity map elements and classification), a rule (its rule
  and counter) and a fault (its classes and netem queues), by the keys the compiler gives them; (b) keep the network and
  global views. Recommendation: (a) after V1, when users ask.
- Acceptance: with (a), a Playwright test opens the Linux view of a rule and of a fault.
- Needs maintainer: no (decided 2026-10-14)
- Effort: M

### P3-M14-04 The browser flow of M14 in the testbed

- Status: closed (2026-10-13)
- Severity: low
- Reason: env-limit. `TestAWireGuardClientCreatedInTheBrowserBringsUpItsTunnel` needs network namespaces; here it runs only
  in the QEMU VM under software emulation (level 1b) and in CI (job `testbed-ui`, `-run InTheBrowser`).
- Evidence: `internal/api/e2e_ui_test.go`, `web/e2e-testbed/wireguard.spec.ts`, `docs/development.md` "Web UI: networks,
  WireGuard, routing and the technical view".
- Task: the local VM run passed (2026-10-13, 18 minutes, recorded in docs/development.md); CI run 38447039664 (`5397d3b`)
  passed the job `testbed-ui` with the three browser flows.
- Acceptance: the job `testbed-ui` passes with the three browser flows.
- Needs maintainer: no
- Effort: S

### Phase 3 audit: choices made in the backend and security fixes (2026-10-14)

The audit's backend and security findings were fixed without a maintainer decision; where a fix had to choose, it took
the option closest to the plan, recorded here:

- `GET /setup` (no authentication before the setup) shares one read of the host's network configuration through the
  executor for 2 seconds among its callers (`osConfigCache`, as the health check's probe, M5-13). Its report does not
  quote the executor's error (the log has it), but it still names the files that cannot be read and the netplan entries
  and NetworkManager connections (ids included) that configure an interface: plan §3.4 wants the wizard to show the
  entry to remove before the setup. Sending these details only with a valid `X-Setup-Token` (the wizard asks for the
  token first, but only `POST /setup` checks it today) would need a change of the spec; confirmed by the maintainer on
  2026-10-15 (Phase 3 audit result, item 1 of the confirmed choices).
- A logout clears the query cache and the draft. An export-once WireGuard configuration that no dialog has shown yet
  (`ClientConfigDialog`'s module-level `unseen`) is kept: it is the only copy of the client's private key, there is a
  single admin, and dropping it would force a key rotation. An ended session (401) clears the cache, as before, and
  keeps the draft, so an expired session loses no edits.
- The executor keeps the host's whole `/etc` mounted read-only (the comment in `internal/executor/exec.go` was wrong):
  a bind mount needs its source, a host without netplan or NetworkManager has no such directory, and
  `create_host_path` would create them on the host. The executor is privileged anyway.
- netplan: a renderer per device type is no entry and does not hand every port to NetworkManager (netplan's global
  NetworkManager renderer does; a per-type one only affects the section's devices, which are entries anyway); a
  `match` by a key the preflight cannot evaluate (empty, or `path` and the like) counts for every interface, a warning
  rather than a missed configuration.

### Phase 3 audit: choices made in the M12, states and visual fixes (2026-10-14)

The audit's findings on the shell, the overview, the devices, the states of §2.17 and the visual system were fixed
without a maintainer decision; where a fix had to choose, it took the option closest to the plan:

- `DeviceView.effective` is filled by the gateway (the spec declared it, nothing sent it), not computed in the browser
  from `/overlays` and `/faults`: the precedence of plan §2.4 lives in one place. It is the device's winning fault of each
  family for its traffic without a destination, as `GET /explain` resolves it, and only for the active revision (overlays
  act on what runs); `overlays`, which the spec leaves open, lists the overlays among the device's candidates, winning or
  overridden. The UI draws the chips from the overlay or configured fault an entry names and falls back to the gateway's
  summary.
- The type scale: the prototype's 11 px badges and 15 px titles became 12 and 16 px, its 18 px dialog titles 20 px, so the
  plan's sizes hold; the plan was not given an exception.
- `text-3` became `#848E9D` (was `#7D8796`, 4.45:1 on `surface-2`) and plan §2.17's token table says it now reaches
  4.5:1 on `surface` and `surface-2`; on the accent tint the UI uses `text-2`.
- "Every item links to its detail" (plan §2.17 Overview): a fault on a group or on everything, which has no page, links
  to the faults screen (it has no filter by target yet); a revision event stays plain text (there is no revision screen
  before the history view); the gateway box links to the Linux view, its technical detail.
- The topology's "No test network" box, a placeholder rather than a screen's empty state, got an "Add a test network"
  link as well.
- The profile editor shows its own checks once a field changed or "Add to the changes" was pressed, like the network
  form; "Add to the changes" is disabled only after it was pressed with errors.
- After a dialog or drawer whose opener is gone (the sticky bar's button while it was hidden is kept mounted; Apply
  ends the draft), the focus goes to the page's heading; a Remove/Delete/Adopt swapped for Undo hands it to its
  replacement. Deleting a rule moves the editor to the next rule as before, but the focus to the deleted rule's Undo.


### Phase 3 audit: choices made in the M13, M14 and interplay fixes (2026-10-14)

The audit's findings on the faults, rules and profiles screens, the networks screen and the interplay of the draft with
other revisions were fixed without a maintainer decision; where a fix had to choose, it took the option closest to the
plan:

- A revision applied while an edit dialog is open (on a clean draft): the dialog saves against the revision it opened on
  (`editOrigin`/`editFrom`), so the next preview gets 409 and the concurrent-change dialog, whose reapply keeps the
  fields only the other revision changed. The other option, reapplying the edit silently onto the newer revision, was
  not taken: where both changed the same field, the user would not see that theirs is overwritten. Adding a new object
  needs no origin (there is nothing of theirs to meet).
- Edits made while the own revision waits for confirmation: when it is confirmed, the draft moves onto it silently
  (only for the revision this UI applied, and only while nothing else became active). Plan §2.17's "controls that would
  change more are disabled until resolved" was not extended to every editor: Preview & apply stays disabled while a
  revision waits, which keeps the plan's guarantee that nothing more is applied.
- A client edit whose hub the other revision deleted is dropped on reapply and named ("hub … deleted → my change
  dropped"); it is kept only when the draft brings the hub back itself.
- A TCP reset rule: choosing TCP reset selects TCP, and another protocol is refused under Access (as the gateway's
  `reset_requires_tcp`, which reports it on `/action`).
- The preview drawer cannot be closed while the apply runs, rather than reporting a failed apply in a toast after it
  was closed.
- An overlay that expired while its edit dialog was open: Keep says it expired ("Keep: expired at 19:31") and the
  dialog asks for a new duration, rather than writing the ended fault again for one second.
- WireGuard traffic of a client and of a link's peer is shown from the remote side (↑ what it sent = the gateway's
  `rx_bytes`), as plan §2.2.1 sets the directions of tunnel faults and the UI shows a device's traffic.
- A link exports its remote configuration and BIRD snippet in the UI (plan §2.2.1 "Keys and export"); the zip of a
  hub's client configurations stays an API/CLI export (the UI exports each client in its dialog, which keeps the
  "export once" handling in one place, and the web fetcher reads text).
- A new client of the draft has Edit and Undo of its own; a refused action on a client of the gateway is marked in its
  row, which keeps its actions (the add-client dialog's Fix would rebuild the client from the add form and lose its
  `enabled` and key generation).
- Plan §5 M14 Scope and the §2.17 Networks and Profiles rows now name what P3-M14-01, P3-M14-03 and P3-M13-08 added; M30's
  Scope names the recovery instructions and the safe-mode banner's link (P3-M12-05).

## Shutdown and host cleanup (decided 2026-10-16)

- Decision: **variant A.** When the service stops, the executor removes every kernel object Chaos Gateway created
  (the `inet chaosgw` nftables table, its policy rules and routing tables 100 and 102, the test bridges with their
  addresses, the service veth pair `svc0`/`svc1` and the name `cgsvc` under `/run/netns`; the namespace itself belongs to the
  `svcns` holder container and ends with it). Recorded as D35 in plan §7.1. The volumes stay, so `compose up` rebuilds
  the kernel state from the last confirmed revision (plan "Restart"). The plan's Stop lines (§3.11 and the Restart
  paragraph) now say the same.
- Why: after the stop, the test network's DNS was redirected to a proxy that no longer ran, and port 8443 stayed
  closed for non-management addresses. Found in the install test on 2026-10-16 (report
  `shutdown-report.md`, scratch). Variant B (keep bridge and veth, remove only the redirecting parts) was the other
  option; it was not chosen because a gateway that is stopped should leave nothing behind.
- Consequence: while the gateway is stopped, devices of a test network have no gateway. This replaces the plan's
  earlier sentence that they keep their normal connectivity.
- Not part of this decision: the networkd rule `/etc/systemd/network/10-chaosgw-eth0.network` and the netplan entry of
  the test interface. Both are host changes made by hand during the install test. Open: the executor or host-setup
  should write and remove them (needs `/etc` access, see the hardening profile in `compose.executor.yaml`).
- Open: `chaosgw teardown` (M27) does not exist yet; the cleanup above is what it has to do. Until then the cleanup
  is manual (see `docs/install.md`, Uninstall).
- Status: decided; **not implemented.** The executor still keeps the kernel state on stop, so until the change lands the
  plan describes the target and `docs/install.md` plus the manual cleanup apply.
- Needs maintainer: no (decided 2026-10-16).

## Phase 3.1 open items

Phase 3.1 (M35a, port forwarding) is being implemented on the branch `phase-3-1-port-forwarding`. The items below were found
while implementing it; each records the option that was implemented so the work was not blocked, and what the maintainer may
want to decide.

### P31-M35a-01 When does a BGP neighbor sit on a forward's entry side?

- Status: open; option (a) implemented
- Severity: low
- Reason: needs-decision. Plan §5 M35a refuses 179/tcp "while a BGP protocol of the routing configuration has a neighbor on
  the entry side". The BGP protocols of the routing configuration run on WireGuard links (M4c), and a link's neighbor must be
  the peer in the link's transfer network, so the BGP session itself never arrives on the uplink or the management interface;
  the link's encrypted traffic does, through whichever of them leads to the peer's endpoint, and that is a matter of the host's
  routes, not of the configuration.
- Evidence: `internal/domain/validate_forward.go` (`bgpRuns`), `TestAForwardLeavesTheBGPPortWhileBGPRuns`.
- Task: implemented (a) — 179/tcp is refused on both entry sides while an enabled BGP protocol is configured (a disabled one,
  or an OSPF or Babel protocol, leaves the port free). Options: (a) as implemented; (b) refuse it only on the uplink side
  (WireGuard runs over the uplink in the shipped topologies), so a three-port gateway may forward 179/tcp from the management
  network; (c) refuse it only when a link's peer endpoint is an address of the entry side's network (needs the host's
  addresses at validation time, which only the compiler has).
- Recommendation: (a): it is the safe reading of the plan sentence, and forwarding BGP into a test network is rare.
- Needs maintainer: yes
- Effort: S

### P31-M35a-02 Forwards overlap whatever their entry side and whether they are enabled

- Status: open; option (a) implemented
- Severity: low
- Reason: needs-decision. Plan §5 M35a: "no two forwards on the same protocol and external port". A forward on the uplink and
  one on the management interface (three-port topology) could share a port, since they come in on different interfaces, and
  a disabled forward does not use its port.
- Evidence: `internal/domain/validate_forward.go` (`portForwards`), `TestTwoForwardsOnTheSameProtocolMustNotOverlap`.
- Task: implemented (a) — two forwards on the same protocol whose external ranges overlap are refused
  (`overlapping_forward`), whatever their entry side and whether they are enabled. Options: (a) as implemented, the plan's
  wording; (b) compare only forwards of the same entry side; (c) compare only enabled forwards (enabling one may then fail).
- Recommendation: (a): one external port means one target, which keeps the UI's list and `explain` unambiguous, and enabling a
  forward never fails.
- Needs maintainer: yes
- Effort: S

### P31-M35a-03 The UI/API port is refused for udp and on both entry sides

- Status: open; option (a) implemented
- Severity: low
- Reason: needs-decision. Plan §5 M35a: "the external port is never the UI/API port". The UI and the API listen on TCP only,
  and in the three-port topology the uplink is not the management interface.
- Evidence: `internal/domain/validate_forward.go`, `TestAForwardNeverTakesTheUIPort`.
- Task: implemented (a) — the UI/API port (`management.ui_port`, else the port the API listens on, else the compiler's default
  443) is refused for tcp and udp on both entry sides. Options: (a) as implemented; (b) refuse it for tcp only; (c) refuse it
  only on the management side and on the two-port topology's shared interface, like 22/tcp.
- Recommendation: (a): the plan says "never", and it keeps a forward from shadowing the control plane whatever the topology.
- Needs maintainer: yes
- Effort: S

### P31-M35a-04 A connection through a forward that is disabled or deleted lives on until it ends

- Status: open; option (a) implemented
- Severity: low
- Reason: needs-decision. Plan §5 M35a: "a disabled or deleted forward leaves no DNAT and closes the path". The forward
  chain accepts established traffic before anything else (D11, spike S3), and the translation of a tracked connection is in
  its conntrack entry: a forwarded connection that exists when its forward goes keeps working until it ends, as a connection
  does when an access matrix entry that allowed it goes.
- Evidence: `internal/compiler/forward.go`, `TestAForwardReachesTheListenersOfADeviceOnTheRealKernel` (a new connection to
  a deleted forward's port reaches the gateway itself, not the device), `TestADisabledForwardLeavesTheKernel`.
- Task: implemented (a) — the forward leaves the kernel (no DNAT element, no accept rule, no counter); new connections are
  not translated. Options: (a) as implemented, like every other change of the access path (D11); (b) after the apply that
  removes a forward, delete the tracked connections it translated (the engine knows them by their reply source and port, as
  the cut of an access rule does), so the device's next packet meets no translation.
- Recommendation: (a): it is what a matrix change does, and a forward that should cut its connections can be paired with a
  rule with "also cut existing connections".
- Needs maintainer: yes
- Effort: S

### P31-M35a-05 The replies of a forward from the management side go to the management subnet only

- Status: open; option (a) implemented
- Severity: low
- Reason: needs-decision. A device's packets are looked up in the policy table 100 (§2.2), whose default route is the
  uplink. A forward from the management network (three-port topology) needs its replies to go back through the management
  interface, which table 100 did not know.
- Evidence: `internal/compiler/compile.go` (`compileRouting`), `TestAManagementForwardRoutesItsRepliesToTheManagementNetwork`,
  `TestForwardsReachTheDeviceFromBothSidesAndFollowItsAddress`.
- Task: implemented (a) — while a forward from the management side is compiled, table 100 has the management interface's
  connected subnet. A device's own traffic to that subnet then leaves through the management interface too; the forward
  chain's default drops it, but an access allow rule that matches it (P31-M35a-10) or a matrix entry network → management lets
  it through. A peer of the management side that is not in that subnet (an allowed
  source reached through a router of the management network) gets no answer. Options: (a) as implemented; (b) also route the
  `allowed_sources` outside the subnet through the management interface's default gateway; (c) route the replies by the
  connection (a conntrack mark set by the DNAT and a policy rule for it), independent of the peer's address.
- Recommendation: (a) for M35a: the management network is the management interface's subnet in the shipped topologies; (c)
  if forwards from routed management networks are needed.
- Needs maintainer: yes
- Effort: M

### P31-M35a-06 A forwarded port range is one DNAT element per port

- Status: open; option (a) implemented
- Severity: low
- Reason: needs-decision. nftables has no expression that shifts a port by an offset (the kernel's shifted port ranges are not
  in nft 1.0.9 or 1.1.6), so a range shifted one to one (8000-8010 to 9000-9010) needs one map element per port.
- Evidence: `internal/compiler/forward.go` (`compileForwards`), `TestTheDNATMapSendsEveryForwardedPortToTheTargetsCurrentAddress`.
- Task: implemented (a) — every forwarded port is one element of the DNAT map; ranges are not limited beyond 1-65535, so a
  forward of all ports is 65535 elements, and a device's address change moves all of them in one element transaction.
  Options: (a) as implemented; (b) limit the length of a range in the validation (e.g. 1024 ports); (c) an unshifted range
  (target port = external port) as one interval element without a port in its value.
- Recommendation: (a), with (b) if large ranges turn out to be slow on the smallest hardware (H1).
- Needs maintainer: yes
- Effort: S

### P31-M35a-07 The entry address is every address of the entry interface

- Status: open; option (a) implemented
- Severity: low
- Reason: needs-decision. Plan §5 M35a: "entry only on the chosen interface and its current address, which is followed through
  netlink like masquerade". The DNAT rule matches a packet that comes in on the entry interface and is addressed to an address
  of that interface (`fib daddr . iif type local`): the kernel follows a new address at once, without an apply, as masquerade
  does. An interface with more than one IPv4 address forwards on each of them.
- Evidence: `internal/compiler/forward.go` (`forwardDNAT`), `TestAForwardFollowsANewEntryAddressAndANewDeviceAddressOnTheRealKernel`,
  `TestAForwardFollowsANewUplinkAddress`.
- Task: implemented (a). Options: (a) as implemented; (b) match the one address the engine reads (the interface's first IPv4
  address, as the status and `explain` show it), compiled into the rule, so an address change takes a full apply.
- Recommendation: (a): it follows a DHCP renewal within the kernel, and a second address on the uplink is the operator's own.
- Needs maintainer: yes
- Effort: S

### P31-M35a-08 The explain card of the faults screen asks for a device as the source

- Status: open; option (a) implemented
- Severity: low
- Reason: needs-decision. Plan §5 M35a: "the preview and `explain` say that faults do not apply to forwarded connections".
  `GET /explain` answers a forwarded connection when `src` is a peer behind the entry side and `dst` the entry side's address
  (step 1). The explain card of the faults screen (M12) takes a device of the lab as the source, so it never asks for such a
  connection; it shows the forward, the access layer `port_forward` and the note when the gateway's answer has them.
- Evidence: `web/src/components/faults/ExplainCard.vue`, `web/e2e/port-forwards.spec.ts` ("explain shows a forwarded
  connection's forward …").
- Task: implemented (a) — the card renders the forward of an answer; a forwarded connection is explained through the API
  (`chaosctl` or `GET /explain?src=…&dst=…&protocol=…&port=…`). Options: (a) as implemented; (b) a source choice "an address
  behind the uplink or the management network" in the card, whose destination defaults to the entry side's address.
- Recommendation: (b) with the flow view (M26), where a forwarded connection can be picked and explained.
- Needs maintainer: yes
- Effort: S

### P31-M35a-09 A forward's counter counts what the forward lets through, in hits

- Status: open; option (a) implemented
- Severity: low
- Reason: needs-decision; found in the review of Phase 3.1. Plan §5 M35a: "a counter per forward" and "the counters count the
  forwarded connections". The counter sits on the forward's accept rule in the forward chain. That rule (1) counts every
  packet of a connection not yet established (one per connection, more for a retransmitted SYN or an unanswered UDP packet),
  which is why the UI shows it as hits, and (2) runs after the access rules: the responder form of an access rule (§2.4) ends
  in the rule's own verdict, so a forwarded connection that an `allow` rule selecting the device accepts (e.g. "device X:
  allow tcp", or "network Lab → uplink: allow") is never counted by the forward, which then shows 0 hits and no last match
  while traffic flows.
- Evidence: `internal/compiler/forward.go` (`forwardAccepts`), `internal/compiler/access.go` (the responder form),
  `internal/api` (`forwardCounter`), `web/src/lib/forwards.ts` (`counterText`), `TestAForwardHasTheCounterOfItsAcceptRule`.
- Task: implemented (a). Options: (a) as implemented: hits of the forward's accept rule, i.e. what the forward itself let
  through; (b) count each forward once per translated connection in the NAT prerouting chain (it sees only the first packet of
  a conntrack entry): a counter-only rule per forward (iif, protocol, external range) in front of the DNAT map rule, so the
  counter means "connections that arrived through the forward", including the ones an access rule then drops or accepts;
  (c) a counter-only rule per forward in front of `jump access_forward` with the accept rule's match, which counts hits of
  every forwarded connection that arrives, whoever accepts it; (d) a ct mark bit set by the DNAT and conntrack accounting.
- Recommendation: (b): it counts connections, as the plan says, is cheap (one rule per forward, first packets only) and is
  independent of the access rules; the UI then says "connections" again. The rule (and the API's `forwardCounter`) move to the
  NAT chain, the accept rule keeps its `last` for the last match or gets it from the same rule.
- Needs maintainer: yes
- Effort: S

### P31-M35a-10 A management-side forward makes the management subnet routable for the devices (three-port topology)

- Status: open; option (a) implemented
- Severity: low
- Reason: needs-decision; found in the review of Phase 3.1. For P31-M35a-05 the policy table 100 gets the management subnet
  while an enabled forward from the management side is compiled (even one whose target has no address). Table 100 is the table
  of all packets of the test networks, so a device's own new connection to the management subnet then leaves through the
  management interface with the device's address, instead of following the uplink default (masqueraded). The default stays
  closed: with no allowance the implicit matrix accepts only the uplink and the forward chain's final drop drops it. But in the
  three-port topology there is no guard in front of the access rules (the "cannot be overridden" guard of §2.4 exists only for
  the two-port topology), so an access allow rule without a destination, an allow rule with a `cidr` covering the management
  subnet, or a matrix entry network → management now carries a device into the management network, and only while a
  management forward exists. Plan §2.2 and §2.16 keep the management network separated from the test networks.
- Evidence: `internal/compiler/compile.go` (`compileRouting`), `internal/compiler/rules.go` (the forward chain's two-port guard),
  `TestAManagementForwardRoutesItsRepliesToTheManagementNetwork`.
- Task: implemented (a). Options: (a) as implemented: the route is present while a management forward exists; what the
  access rules and the matrix allow decides, as §2.4 says for a rule without a destination; (b) a three-port guard in front of
  the access rules, mirroring the two-port one: drop new connections from the test networks out to the management interface,
  with the matrix entries to management as the only exception; (c) P31-M35a-05 (c): route only the forward's replies (a ct
  mark set by the management-side DNAT, copied to the replies, and a policy rule `fwmark → management table`), so table 100
  gets no management route at all and the reachability no longer depends on a forward.
- Recommendation: (c): it removes the dependency on whether a forward exists and also answers peers behind a router of the
  management network (P31-M35a-05); with a golden test and a level-1 test (a device with an allow-all rule cannot open a
  connection into the management subnet while a management forward works).
- Needs maintainer: yes
- Effort: M
