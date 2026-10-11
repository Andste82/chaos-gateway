# Phase 1 audit and open items

Date: 2026-10-04. Audited against `main` at `c4d51d3` (M6b merged) and `docs/plan.md` §5 Phase 1 (M1–M6b).
Closed: 2026-10-05, work package 12.

## Result

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
2. **Decisions.** All open questions of Phase 1 were decided on 2026-10-04 (table below, and in each item's "Needs maintainer" line). Implement the decided option. Where it differs from the recommendation, the task was rewritten to match the decision. The Phase 2 items (M7 to M11) were decided on 2026-10-11: the table under "Phase 2 open items" lists them, the two new items (P2-M9-11, P2-M11-05) carry "Needs maintainer: yes", and the ten that stay open from before need no decision.
3. **Test levels.**
   - **Local:** unit tests (`make test`, `go test ./internal/...`), lint (`make lint`), `make check-generated`.
   - **CI only:** tests with build tag `testbed` (levels 1 and 1b; they need network namespaces, which the dev container lacks) and `appliance` (nightly job, needs KVM).
   - For those, push the branch, open a PR, and watch `gh pr checks <n> --watch`.
   - Read failures with `gh run view <run> --log-failed`.
   - When a testbed test fails without a clear reason, add diagnostics to the test (nft ruleset, routes, sockets) and push again; that is how M6b was debugged.
4. **Toolchain.**
   - Generated code: after editing `api/openapi.yaml`, run `make generate` and commit the generated files; CI runs `make check-spec check-generated check-clients`.
   - Formatting: run `gofmt` (CI lint fails on it) and keep `make lint` at 0 issues.
5. **Updating this file.** When an item is done, delete its block here in the same PR and mention the item id in the commit message. Keep the tables in "Result" up to date.
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
| M2: overlay `expires_at` / `lease_expires_at` not set by `NewOverlay` | M8a |
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
| M5: capabilities lists (`overlay_kinds`, `fault_families`, `step_types`) empty | M8a, M15 |
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

## Phase 2 open items

Phase 2 (M7 to M11) produced 39 items. On 2026-10-11 the maintainer decided 29 of them (table below); their blocks are gone from this
file, like the closed items of Phase 1, and the decisions are recorded where the behavior is documented: the milestone's section in
`docs/development.md`, the description in `api/openapi.yaml` where the behavior is user-visible, and `docs/plan.md` where the plan said
otherwise. The item ids stay as names: the code and the documents refer to them. Five decisions changed behavior and were implemented
with them (P2-M9-01, P2-M9-03, P2-M9-04, P2-M10-08, P2-M11-03); the others confirmed the interpretation the item had chosen. P2-M8a-07 was
resolved in M9. Ten items need no decision and stay (below), and two new ones came out of the review of M9 and M11.

### Decisions (made 2026-10-11)

| Item | Question | Decision |
|---|---|---|
| P2-M7-01 | Should M7 write the service-selection mark (bit 20)? | Confirmed: no, it stays deferred to M20/M21, where the real redirect-interaction test is |
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
| P2-M9-01 | An allow rule in the input path | **Changed:** it accepts, so it also opens the gateway's closed ports for its selector; the UI/API port rule for other sources and the anti-lockout rule stand in front of the rules |
| P2-M9-03 | The type of a `reject` | **Changed:** chosen per rule with `reject_with` (`port_unreachable` by default, `host_unreachable`, `admin_prohibited`, `no_route`) |
| P2-M9-04 | The network scope and the networks behind a test network's static routes | **Changed:** "network X" includes them, for rules and faults alike |
| P2-M9-05 | Fault counters count packets a rule drops afterwards | Confirmed: left as it is |
| P2-M9-07 | A rule's counter counts new connections only | Confirmed; documented in the spec |
| P2-M9-08 | `lockout_protected` is never produced | Confirmed: commit-confirm is enough, the code stays reserved |
| P2-M9-09 | Access rules select IPv4 only | Confirmed: IPv6 selectors come with M32 |
| P2-M9-10 | What "also cut existing connections" does at its edges | Confirmed |
| P2-M10-01 | Duplication as a draw plus a copy on the netdev egress hook | Confirmed |
| P2-M10-02 | When a flapping starts, and a boundary during an apply | Confirmed |
| P2-M10-03 | A rate below 8 bit/s is refused; the phase of a flapping in the API | Confirmed |
| P2-M10-04 | Black hole before the access rules, the route lock, the clamp's headers | Confirmed |
| P2-M10-05 | Directions and peer address of a tunnel fault | Confirmed |
| P2-M10-06 | The WireGuard actions | Confirmed |
| P2-M10-07 | One IFB with a fixed name; the host's ingress qdisc is shared | Confirmed |
| P2-M10-08 | The per-packet delay check of the update test on a loaded runner | **Changed:** the measured stream is at least 200 packets (sent 5 ms apart); the bounds and the rule that none is more than 10 ms outside stay |
| P2-M11-03 | The rate of the built-in Bad LTE end to end | **Changed:** a UDP measurement above the rate was added (`TestTheRateOfBadLTEShapesUDPSentAboveItEndToEnd`) |
| P2-M11-04 | The accuracy check of a profile with a rate and a jitter | Confirmed: a bound is enough for the ordered delay |

### Items that stay open

### P2-M7-02 Classification maps are keyed by address, not by the identity map's device number

- Status: decided in M8a (kept), confirmation open
- Severity: low
- Reason: needs-decision. "Identity maps keyed by address → device id REPLACE the per-device
  nftables sets used in Phase 1 for DHCP/device classification" can be read as meaning the
  classification lookup-chain maps themselves should be keyed by the device's numeral (stable
  across an address change), with only one indirection (the identity map) ever needing an element
  update. What is implemented instead follows plan §3.3's literal key tuples (`ct original ip
  saddr`, …): the classification maps (`cls_dev`, `cls_devdest`, `cls_devport`,
  `cls_devdestport`) are keyed directly by the device's current address, and the identity map
  (`ident4`, address → device number) is a separate structure that nothing yet consumes — it only
  replaces the Phase 1 per-device address sets (`dhcp.go`'s old `dev_<id>` sets), as M6a-07
  deferred.
- Evidence: `internal/compiler/classify.go` (`compileClassify`, `classifyLevels`);
  `internal/compiler/dhcp.go` (`compileIdentity`). Confirmed against the real `nft -c` parser
  (not simulated): a map lookup cannot be combined with other expressions such as the bitwise ops
  that write the fault id (`Expression type map not allowed in context (RHS, STMT, PRIMARY)`), so
  either design needs one small chain per classification id either way (`MarkChainName`) — keying
  by device number would not avoid that, only move which map's element changes on an address
  change (today: every level's entry for that device's old/new address; with the indirection:
  only the identity map's one entry).
- Task: chosen interpretation — keep address-keyed classification maps (simpler, and the plan's
  own key tuples read literally); M8a, which first populates these maps from
  `domain.Resolve`'s winners, is the natural point to revisit this if per-device churn at scale
  (many destinations/ports per device) turns out to need the device-number indirection instead.
- Acceptance: a maintainer confirms the interpretation, or asks for the device-number indirection
  before M8a builds on the current map shape.
- M8a outcome (decided with evidence, 2026-10-07): keep the address-keyed maps. M8a built them
  out as interval maps (a range of source addresses, a range of destinations, protocol, a port
  range), and the churn test `TestAnAddressChangeChangesAHandfulOfElements` measures what a device
  that gets a new address costs: 250 devices, each with its own per-device queue (rate fault on the
  network) and two further faults with destination and port entries, one device moves to another
  address: 6 element changes in all maps (the identity map and the classification maps), no fault id
  renumbered, no tc class touched. The elements that name the address are the device's own and the
  two stretches next to it that are cut at its old and new address. A device-number indirection
  would turn those 6 into 1-2 but would need the identity lookup on every packet and a second
  level of maps, and it would not help the destination and port entries, which are the bulk of a
  table. `TestAnElementTransactionMovesIntervalElementsOnTheRealKernel` shows on a real kernel that
  such a move (delete and add in one transaction, even where the new element overlaps one that was
  there) leaves the maps exactly as a full apply would. The engine's identity update takes these
  element changes too (`identityOps`): a device's address change is incremental also while faults
  are active.
- Needs maintainer: confirmation only
- Effort: S (design confirmation only)

### P2-M8a-06 An overlay or fault that wins only for a part of its selector is shown as effective

- Status: new
- Severity: low
- Reason: deferred (to M13, the faults screen, which is the first consumer). The spec's `EffectState`
  has four values (`effective`, `partially_overridden`, `overridden`, `disabled`). M8a decides
  `effective` and `overridden` from one fact the compiler reports: whether the fault wins for any
  traffic of any source (`Target.Winners`). Telling "wins for all of its traffic" from "wins for
  some" needs the coverage of every selector against every other one, which the per-source tables
  contain (`domain.Table`) but the compiler does not report per fault.
- Evidence: `internal/api/overlays.go` (`overlayContext.effectOf`), `internal/compiler/faults.go`
  (`Target.Winners`). `explain` answers the question per destination exactly.
- Task: M13 adds the per-fault coverage to the compiler's output (the number of table entries a
  fault wins out of those it matches) and the API reports `partially_overridden` from it.
- Acceptance: a test where a device fault covers part of a network fault's selector shows the
  network fault as `partially_overridden` and the device fault as `effective`.
- Needs maintainer: no
- Effort: S

### P2-M8b-02 Verification cannot see the HTB quantum or the netem distribution table

- Status: new (the distribution half is handled by the apply: see Task)
- Severity: low
- Reason: deferred. `tc -j` does not print an HTB class's `quantum` (the kernel does not report it)
  and does not print a netem qdisc's distribution table at all; both only matter through what the
  traffic does. The normalizer (`linux.NormTree`) therefore compares rate, ceil, prio and every
  printed netem attribute, but a class whose quantum was changed from outside, or a leaf whose table
  is not the one the target names, looks right. Proven in the VM: a `change` or `replace` that names
  no distribution keeps the old table (the share of round trips outside delay ± jitter stays at a
  third for a normal table, `TestADistributionSurvivesAChangeAndOnlyANewQdiscDropsIt`); only a
  qdisc that is created again is uniform.
- Evidence: `internal/linux/tcnorm.go` (`NormClass` has no quantum; `NetemSpec` has no distribution),
  the recorded listings under `internal/linux/testdata/tc`.
- Task: done for the apply (M8b): the `apply.Retirer` remembers the table it gave each leaf and makes
  a leaf that is to be uniform with a jitter again when it holds or may hold one
  (`TestTheDistributionOfAFaultFollowsAChangeBackToUniform`, `TestAChangeToAUniformJitterMakesTheLeafAgainOnlyWhereATableMayBe`).
  What stays open: a table that someone else put on a leaf, or that survived a restart of the
  gateway, is not seen when the leaf already has the configuration the target names (the apply leaves
  such a leaf alone, because re-creating every uniform leaf at every start would drop queues for
  nothing); it is replaced at the next change of that leaf. The class quantum is a constant of the
  compiler (`TCClassQuantum`), written whenever a class is created, not verified.
- Acceptance: a restart of the engine with a table in the kernel and a target without one ends uniform
  at the first change of the leaf; or a maintainer decides that the apply re-creates leaves it did not
  create.
- Needs maintainer: no
- Effort: S

### P2-M8b-04 The tc state of every assigned interface is read at every apply and every verify

- Status: new
- Severity: low
- Reason: deferred (measure first). An apply reads the qdisc listing of every assigned interface
  (one `tc` run each, to find a tree that is no longer wanted) and the whole tc state (three runs)
  of the ones that hold a tree, before it plans and again to verify. With three interfaces carrying
  the tree and a handful of ports that is about 20 tool runs per apply, which matters on a Raspberry
  Pi when an overlay burst costs one apply per few writes.
- Evidence: `internal/apply/state.go` (`readTC`).
- Task: if the measurement of M8b's performance step shows it, read the qdisc listing of all
  interfaces in one run (`tc -j qdisc show` without a device) and keep which interfaces carry a tree
  from the last apply, reading only those and the target's.
- Acceptance: the cost of an apply with ten assigned interfaces is measured on the arm64 runner and
  stays within the budget of plan §3.11.
- Needs maintainer: no
- Effort: S

### P2-M8b-07 A burst through a delay fault loses replies outside the queues on a slow environment

- Status: cause found in M8b (the echo server of the lab, not the gateway); the test accounts for it
- Severity: low
- Reason: needs-decision. `TestTheComputedQueueLimitHoldsABurstThatTheDefaultLimitOfNetemDrops` first
  required exact conservation through both queues (the download queue sends what the upload queue sent,
  because "the echo server answers every request"). On the hosted level 1b runner (kernel 6.8 in a VM on
  a VM) a burst of 4000 pings loses half of its packets between the two queues, with no drop in either of
  them: the download queue sent 1851 to 2011 of the 4000 that the upload queue sent (740 to 797 of 1000
  with the limit of 1000), and ping received exactly what the download queue sent. On the hosted level 1
  runner (kernel 6.17, native) every packet is counted by both queues; with KVM on a developer machine and
  emulated (kernel 6.8, TCG) the queues count every packet too, and only ping's own receive buffer loses
  replies.
- Evidence: the CI runs of PR #33 (level 1b), `internal/engine/integration_queues_test.go`. The first
  suspect, the receive queue of the next hop (`netdev_max_backlog`), is ruled out: `/proc/net/softnet_stat`
  did not move. The counters of every namespace, logged for the gap, name the place (run 37735996258): the
  gateway forwarded every packet it got (`ForwDatagrams` +5851 = 4000 requests and 1851 replies), the echo
  server received all 4000 requests (`Icmp.InEchos` +4000) and sent 1851 replies; for the other 2149 its
  `Icmp.OutErrors` and `Ip.OutDiscards` moved by exactly that number (260 of 1000 in the second burst).
  The replies are discarded in the echo server's own output path (`Icmp.OutErrors` is what the kernel
  counts when it fails to build or queue an ICMP reply, `Ip.OutDiscards` the same packets at the IP layer),
  not by the gateway. Why the server's output fails under a burst on a slow, nested CPU (a full send
  buffer of the kernel's ICMP socket is the likely reason) was not examined further.
- Task: done for the test — the upload queue keeps the exact law (sent + drops = the burst, no drop and
  no backlog with the computed limit); the download queue must not drop or hold anything with the
  computed limit; every packet missing between the two queues must be accounted for by a drop of the
  host's receive queues or by a reply the echo server could not send (its `Icmp.OutErrors`), else the test
  fails and prints the counters that moved. What stays open is only whether the lab should avoid the
  server's discards (for example a slower burst) so that the exact law holds on every runner.
- Acceptance: a maintainer confirms that accounting for the echo server's output errors is the right
  test, or the burst is paced so that the exact law holds without it.
- Needs maintainer: no (confirmation only)
- Effort: S

### P2-M8b-08 The first packets of the first flow of a lab wait about a second on a fast kernel

- Status: new
- Severity: low
- Reason: cause-unknown. In the CI runs of PR #33 the probes of the first flow measured from the Lab
  device C right after the lab came up (a 5 ms interval, no fault anywhere) had a median in microseconds and a
  95th percentile of 860 to 970 ms in the upload direction only, in 4 of 4 runs on the hosted level 1
  (kernel 6.17) and 1b (6.8 under KVM) runners, and in no run of the second flow of the same test or on the
  emulated kernel. All the probes sent in that first second reach the echo at about the same moment, which
  is what a packet queue waiting for a neighbor resolution (ARP retry after 1 s) looks like. The stream of
  `TestUpdatingOneFaultDoesNotDisturbTheOthers` (91 probes, 1.8 s) took it for a median of 142 ms and the
  test failed once on level 1.
- Evidence: the logs of the CI runs 37701957026 and 37711403220 (the `C to the server, before:` lines).
- Task: chosen interpretation — the cause is not found, and no fault is involved (no tc tree exists in the
  flows that show it), so the measurement tests bring the lab to its steady state first (`warmUp` in
  `startFaultLab`: three pings from each device, logged with the slowest round trip; on level 1 the first
  ping of C took 1.00 to 1.06 s in every test, A's and B's 0.1 ms, which is the evidence that it is C's
  first packet and one second; then `settleIdentity` waits for the engine to absorb the neighbors the pings
  made it meet). To find the
  cause: `ip neigh` and a capture on the gateway's `lan1` and `wan0` for the first second after `startFaultLab`.
  If it is the product (a port or an address that is not ready when the apply returns), the apply
  has to wait for it.
- Acceptance: the cause is named; the warm-up either goes or stays as a documented part of the lab.
- Needs maintainer: no
- Effort: M

### P2-M9-02 A rule that names a hostname is not compiled before M20

- Status: new
- Severity: medium
- Reason: deferred (to M20, the DNS-derived address sets). Plan §2.4 allows a hostname as the
  destination of a rule. Its addresses are known at run time only, so a drop rule that names one
  would compile to a rule that matches nothing, and the traffic the operator wants blocked goes through.
- Evidence: `internal/compiler/access.go` (`compileAccess`), `TestAHostnameRuleIsLeftOutWithAWarning`.
- Task: chosen interpretation — the rule stays out of the chain and the compile reports the warning
  `hostname_unresolved` (the code faults use), which the preview and the events show. M20 adds the
  DNS-derived set as the rule's destination.
- Acceptance: M20's test that a drop rule with a hostname destination blocks the addresses the device
  resolved.
- Needs maintainer: no
- Effort: S

### P2-M9-06 The limits of the access rules are not measured on small hardware

- Status: new
- Severity: low
- Reason: env-limit (H1). The compiler refuses more than 1000 rules (overlays included) and more than
  65536 source addresses in all rule sets with `capacity_exceeded`, on every architecture. Rules are
  evaluated for the first packet of each connection only (established traffic is accepted first), so
  the chain is a linear scan per new connection; nothing in the repository measures what 1000 rules
  cost a Raspberry Pi at the connection rates of a test lab.
- Evidence: `internal/compiler/access.go` (`DefaultRuleLimit`, `MaxRuleElements`).
- Task: chosen interpretation — one limit for all architectures (the class limit has two because tc
  classes cost memory and CPU per packet; rules cost per connection). H1 measures new connections per
  second with 1000 rules on the target hardware and sets an ARM64 default if needed.
- Acceptance: the H1 measurement exists and the limit is confirmed or lowered for ARM64.
- Needs maintainer: no
- Effort: S

### P2-M9-11 A broad allow in forward accepts before the matrix and the two-port guard, and the destination `uplink` includes learned prefixes and the service namespace

- Status: new
- Severity: medium
- Reason: needs-decision. Two things about how the access rules meet the rest of the packet path, found in the review of M9.
  (1) An `allow` rule in forward is a plain `accept` in front of the access matrix and in front of the two-port guard (the rule that
  stops devices under test from reaching the management network when it lies behind the uplink interface, plan §2.16). A broad allow, for
  example from the global scope to any destination, therefore opens the management network behind the uplink for the devices it selects.
  docs/development.md documents it as an exception to the matrix; no decision is
  recorded on whether the guard should stand in front of the rules as the anti-lockout rule does for the control plane.
  (2) The destination `uplink` means "in no configured network and not in the management prefixes" (`World.NonUplinkPrefixes`,
  `viaUplink`). Prefixes that BIRD learned (BGP, OSPF) and the service namespace's address 169.254.100.2 lie in no configured network,
  so a rule counts them as uplink: "drop to uplink" also drops traffic to a learned remote site and to the namespace of the DNS proxy.
- Evidence: `internal/compiler/rules.go` (the forward chain: the jump to `access_forward` stands before `matrixRules` and the guard),
  `internal/domain/access_selector.go` (`NonUplinkPrefixes`), `internal/domain/resolve.go` (`viaUplink`),
  `TestRulesMatchTheOriginalTupleAndNeverThePacketsOwnAddresses` (the non-uplink set), docs/development.md "Access rules (M9)".
- Task: none yet. For (1): put the guard in front of the jump, so that no rule can open the management network behind the uplink, or keep
  it as the power of an allow rule and say so in the spec. For (2): add the learned prefixes and 169.254.100.0/30 to the non-uplink set,
  or define `uplink` as "routed via the uplink interface" by the routing table, or document the present meaning in the spec.
- Acceptance: a decision, and tests of the chosen behavior (an allow rule and the guard on the real kernel; a rule with the destination
  `uplink` against a learned prefix and the namespace's address).
- Needs maintainer: yes
- Effort: M

### P2-M11-01 A profile with a DNS or TLS part is stored but cannot be activated before M20 and M21

- Status: new
- Severity: low
- Reason: needs-decision. The plan keeps the DNS and TLS profiles out of M11 ("built-in (except DNS/TLS profiles, which arrive with
  M20/M21)") and says a custom profile may add a DNS delay (§2.4). A custom profile can therefore be defined with a `dns` or `tls`
  part today (the model and the validation have had them since Phase 1), and the question is what its activation does before the
  family exists. Chosen reading: the activation is refused **as a whole** with `unsupported_feature` and the milestone (M20 for a
  `dns` part, M21 for a `tls` part), and `GET /profiles` lists the profile with `available: false` and an `unavailable_reason`. The
  alternative, activating only the impairment part, would silently produce another profile than the one the user named, and a test
  that expects the DNS part would pass for the wrong reason. The built-in `dns-broken` and `tls-broken` follow the same rule.
  A target that still holds such an overlay (it cannot be written) compiles the parts it can and ignores the others.
- Evidence: `engine.checkProfileAvailable`, `domain.UnavailableParts`, `TestAProfileWithAPartOfALaterMilestoneIsRefusedAndNothingChanges`,
  `TestACustomProfileWithADNSPartIsStoredButCannotBeActivated`, `TestAProfileWithOnlyALaterFamilyCompilesToNothing`.
- Task: M20 and M21 lift the check family by family (`domain.unavailableFamilies`); nothing else to do now.
- Acceptance: with M20 a profile with a `dns` part activates and its DNS part competes in the DNS family (E5).
- Needs maintainer: no
- Effort: S

### P2-M11-02 Custom profiles are created, changed and deleted with candidate revisions, not with endpoints of their own

- Status: new
- Severity: low
- Reason: needs-decision. The M11 entry asks for "custom profiles" and the task for CRUD with read-only built-ins. The spec has `GET
  /profiles` and `GET /profiles/{id}` and no write operation, as for every other configured object (networks, devices, groups,
  rules, faults): the configuration is edited as a candidate revision (`POST /revisions`, a merge patch or the whole document), previewed
  and applied (§2.1.1), and "profiles are part of each revision". Chosen reading: no `POST /profiles`; `profiles` of the configuration
  is the create/update/delete, the built-in profiles are not part of the configuration (a key equal to a built-in UUID or a name equal
  to a built-in name, in any case, is `reserved_name`), and deleting a profile that an overlay activates needs `?force=true` and lists
  `references[]` (`/profiles/<uuid>` is the object). The UI (M13) needs no more than that to offer profile cards.
- Evidence: `TestACustomProfileIsCreatedChangedAndDeletedWithRevisions`, `TestDeletingAnActivatedProfileNeedsForceAndListsTheReferences`,
  `TestARevisionThatDeletesAnActivatedProfileIsRefusedUnlessForced`.
- Task: none yet; if a maintainer wants single-profile endpoints they are sugar over a candidate revision and belong with M13/M18.
- Acceptance: a decision, or none needed.
- Needs maintainer: no
- Effort: S

### P2-M11-05 A revision can add a DNS or TLS part to a custom profile that is already activated

- Status: new
- Severity: low
- Reason: needs-decision. P2-M11-01 refuses the activation of a profile with a `dns` or `tls` part as a whole, so that no activation runs
  half a profile. The check (`engine.checkProfileAvailable`) runs when an activation is written. A candidate revision that adds a `dns` or
  `tls` part to a custom profile that an overlay already activates is accepted, because the profile validates and exists; the next compile
  expands the new definition under the same overlay id and ignores the parts of families it does not resolve yet (M20, M21). The
  activation then runs the impairment part only: the half profile that the check at the write is there to prevent.
- Evidence: `internal/engine/overlays.go` (`checkProfileAvailable`, called from the overlay write only), `domain.UnavailableParts`,
  docs/development.md "A revision changes the profile; the activation follows", `TestAnActivationFollowsTheNewDefinitionOfItsProfile`.
- Task: none yet. Options: (a) refuse the revision (`unsupported_feature` naming the profile and the activations that would run it) while an
  overlay activates the profile, which keeps the rule "never half a profile" whole; (b) accept it and report the activation as partially
  active (a warning in the preview, a note on the activation in `GET /profiles` and `GET /overlays`, the unavailable part named), so that the
  profile can be edited ahead of M20 and M21.
- Acceptance: a decision, and a test that writes the revision with an activation present.
- Needs maintainer: yes
- Effort: S
