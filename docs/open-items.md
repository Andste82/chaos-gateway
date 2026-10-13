# Audits and open items

This file holds the audits of two phases of `docs/plan.md` §5, what they left open, and the open items of the phase in progress:

- **Phase 1** (M1–M6b): audited on 2026-10-04, closed on 2026-10-05. The result, the decisions and the
  per-milestone verdicts follow directly below.
- **Phase 2** (M7–M11): audited on 2026-10-12, its items closed on 2026-10-12. The result is in
  [Phase 2 audit result](#phase-2-audit-result), right before the Phase 2 open items; the items the audit
  found, P2-AUDIT-01 to P2-AUDIT-16, and P2-AUDIT-17, which the work on them found, are closed (table
  "Closed after the audit (2026-10-12)" under [Phase 2 open items](#phase-2-open-items)). One Phase 2 item
  stays open: P2-M9-06, a hardware measurement for H1.
- **Phase 3** (M12–M14): in progress. What the implementation of a milestone could not settle is under
  [Phase 3 open items](#phase-3-open-items), at the end of the file.

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

## Phase 3 open items

Phase 3 (M12 to M14) is in progress. The items below were found while implementing M12 and M13; each records the option that was
implemented so the work was not blocked, and what the maintainer may want to decide. The reasons are the ones of "Why items
stayed open".

### P3-M12-01 The commit-confirm banner has no "Roll back now"

- Status: new
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
- Needs maintainer: yes
- Effort: S

### P3-M12-02 The device detail's flows and recent DNS are not shown yet

- Status: new
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
- Needs maintainer: yes
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

- Status: new
- Severity: low
- Reason: plan-error (underspecified). M12 says "assign test interfaces"; a LAN network can have several ports (plan §2.2).
- Evidence: `web/src/lib/setup.ts` (`buildSetupRequest`).
- Task: implemented — each chosen test interface becomes a LAN network of its own with a proposed name, gateway address
  and DHCP switch; more ports, WireGuard networks and the access matrix are edited afterwards (M14's networks view).
  Confirm, or ask for grouping several ports into one network in the wizard.
- Acceptance: the decision is recorded here; a change gets a Playwright test.
- Needs maintainer: yes
- Effort: S

### P3-M12-05 The safe-mode banner links to the plan, not to recovery instructions

- Status: new
- Severity: low
- Reason: deferred. Safe mode itself comes with M27 (the API reports `safe_mode` and `safe_mode_reason`, which the banner
  shows); there is no recovery document yet. The banner's link points to plan §3.9 "Updates and Recovery".
- Evidence: `web/src/components/layout/AppShell.vue`.
- Task: when M27/M30 write the recovery instructions, point the link at them (served with the app or in the docs).
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

- Status: new
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
- Needs maintainer: yes
- Effort: S

### P3-M13-01 The last match of an access rule is the time the gateway first read the counter higher

- Status: new
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
- Needs maintainer: yes
- Effort: S (c), M (b)

### P3-M13-02 The concurrent-change dialog offers "Discard mine" next to "Reload and reapply mine"

- Status: new
- Severity: low
- Reason: plan-error (small contradiction). M13 Scope and the introduction of plan §2.17 name "reload and reapply"; the
  states table of §2.17 lists "reload and reapply mine · discard mine". The dialog offers both and never "overwrite theirs".
  Reapplying keeps the other side's fields and the user's where both changed the same field (P3-M13-07), and shows the
  combined result in the preview before anything is applied.
- Evidence: `web/src/components/apply/ConflictDialog.vue`, `web/src/stores/draft.ts` (`rebase`).
- Task: implemented — both buttons. Confirm, or drop "Discard mine" (the bar's Discard does the same afterwards).
- Acceptance: the decision is recorded here; the plan's two sentences say the same.
- Needs maintainer: yes
- Effort: S

### P3-M13-03 Custom profiles are not created or edited in the UI

- Status: new
- Severity: low
- Reason: needs-decision. M13 Scope lists "profile cards"; the prototype has a "New profile" button, plan §2.17's Profiles
  row does not. Custom profiles are objects of the configuration (P2-M11-02), so an editor would be a form over the draft
  like the fault dialog (parts per family). "Measure with probe" waits for the probes (M25).
- Evidence: `web/src/views/ProfilesView.vue`; plan §2.17 "Profiles", §5 M13, M25.
- Task: implemented — the cards show built-in and custom profiles, activate and deactivate; custom profiles are made through
  the API. Options: (a) a profile editor (impairment and MTU parts) over the draft in M14 or later; (b) leave it to the API and
  the configuration import. Recommendation: (a), small, after M14.
- Acceptance: with (a), a Playwright test creates a profile through the drawer and activates it.
- Needs maintainer: yes
- Effort: S

### P3-M13-04 The fault dialog edits impairment faults only

- Status: new
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
- Needs maintainer: yes
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

- Status: new
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
- Needs maintainer: yes
- Effort: S

### P3-M13-07 "Reload and reapply mine" takes the user's value of a field both sides changed

- Status: new
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
- Needs maintainer: yes
- Effort: S

### P3-M13-08 The scope selectors do not offer remote networks as a choice

- Status: new
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
  (and a CIDR field) in the selectors, with M14's WireGuard views; (b) keep them API-only. Recommendation: (a) in M14.
- Acceptance: with (a), a Playwright test creates a fault on a client's networks and the request carries the scope.
- Needs maintainer: yes
- Effort: S
