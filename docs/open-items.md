# Phase 1 audit and open items

Date: 2026-10-04. Audited against `main` at `c4d51d3` (M6b merged) and `docs/plan.md` §5 Phase 1 (M1–M6b).

## Result

**Phase 1 is not completely implemented.** Every milestone delivers its main scope, and every test the plan lists exists. CI on `main` is green. Work packages 1-9 of the "Suggested work packages" list below are done (merged, or for package 9 committed directly as plan/doc fixes); packages 10 and 11 remain. What remains is a mix of:

- a handful of decided but not yet implemented functional changes (package 10: the executor's `os_owned` class, BIRD outage/max-prefix/Babel behaviour, an SSE resync signal, setup commit-confirm, 413 for oversized bodies, the Kea hook's datagram socket, a dead service-namespace holder not blocking applies);
- conntrack followed through events instead of polled (package 11, effort L);
- test gaps where a plan test exists only weakly (M5b, CC-03);
- small env-limited or deferred items with no further action needed beyond a decision already recorded.

| Milestone | Verdict | Open items | High | Medium | Decided 2026-10-04 |
|---|---|---|---|---|---|
| M0 (spikes, docs) | – | 1 | 0 | 0 | 1 |
| M1 Repository, CI, testbed | done | 0 | 0 | 0 | 0 |
| M2 Domain model, persistence | incomplete | 2 | 0 | 0 | 2 |
| M3 Executor | incomplete | 2 | 0 | 0 | 2 |
| M4 Compiler, preview, safe apply | incomplete | 2 | 0 | 0 | 2 |
| M4b WireGuard | incomplete | 2 | 0 | 0 | 2 |
| M4c Dynamic routing | incomplete | 5 | 0 | 3 | 5 |
| M5 REST API | incomplete | 3 | 0 | 2 | 3 |
| M5b Appliance harness | incomplete | 2 | 0 | 0 | 0 |
| M6a DHCP, devices | incomplete | 3 | 0 | 2 | 2 |
| M6b DNS, service namespace | incomplete | 2 | 0 | 1 | 2 |
| Cross-cutting | – | 3 | 0 | 0 | 0 |

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
2. **Decisions.** All open questions were decided on 2026-10-04 (table below, and in each item's "Needs maintainer" line). Implement the decided option. Where it differs from the recommendation, the task was rewritten to match the decision.
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
4. **Devices and flows** (done, `phase1-devices-flows`; M6a-03 and M6a-07 only narrowed, see their remaining blocks below; M6a-22 closed outright once M6b-08 added certificate pinning): M6a-02, M6a-03, M6a-05, M6a-06, M6a-07 (limiter part), M6a-08, M6a-11, M6a-13 to M6a-22.
5. **Service namespace hardening** (done, `phase1-svcns-hardening`): M6b-01, M6b-03, M6b-04, M6b-06, M6b-07, M6b-08, M6b-10.
6. **Retention and domain** (done, `phase1-retention-domain`): M2-01, M2-02, M2-04 to M2-07, M2-09.
7. **Executor and engine robustness** (done, `phase1-executor-engine-robustness`): M3-02, M3-03, M3-05, M4-03 to M4-06, M4-10.
8. **Test infrastructure** (done, `phase1-test-infrastructure`): M1-02 (x86 matrix), M1-03 to M1-09, CC-04.
9. **Plan and docs sync** (done, one commit directly on `main`; M6a-04 was listed here originally but excluded, since its decision was to implement conntrack events in code, see package 11):
   - all `plan-error` items once their decisions are made: M1-01, M1-06, M1-10, M3-06, M4-09, M4b-03, M4b-07, M4b-08, M4c-11, M4c-16, M5-07, M5-23, M6a-07 (doc part), M6a-10, M6a-12, M6a-23, M6a-24, M6b-04 (M7 test), M6b-11;
   - doc items M0-01, M0-02, M3-07, M4-08, M4b-06, M6b-09, CC-01, CC-02.
10. **Decided changes**: M3-01, M4c-02, M4c-04, M4c-05, M5-02, M5-03, M5-10, M6a-09, M6b-02; close M4b-04, M4b-05 and M4c-12 with a doc sentence.
11. **Conntrack events** (effort L): M6a-04, then the rates of M6a-03 on top of it.

## Cross-cutting

### CC-03 `internal/linkexport` has no unit tests
- Status: new
- Severity: low
- Reason: test-gap. It is covered only through the CLI (`cmd/chaosgw/wg_test.go:204`) and the API 404 path.
- Evidence: `internal/linkexport/`.
- Task: add `linkexport_test.go`. Cover a link with static routes: the `.conf` holds the peer, the endpoint and the AllowedIPs; the BIRD snippet passes `bird -p` (skip when bird is missing).
- Acceptance: local unit test.
- Needs maintainer: no
- Effort: S

### CC-05 CI actions trigger Node 20 deprecation warnings
- Status: new
- Severity: low
- Reason: forgotten.
- Evidence: `.github/workflows/*.yml` (`checkout@v4`, `setup-go@v5`, `build-push-action@v6`, …).
- Task: bump each action to its current major version and check the release notes for breaking inputs.
- Acceptance: CI green without the deprecation annotations.
- Needs maintainer: no
- Effort: S

### CC-06 The scheduled nightly has never run on `main`
- Status: new
- Severity: low
- Reason: env-limit. The workflow reached `main` after its 02:17 UTC slot.
- Evidence: `gh run list --workflow nightly.yml`.
- Task: after 2026-10-04 02:17 UTC, check that a `schedule` run exists and is green (fuzz and appliance). If it did not start, check the cron syntax and the default branch. See also M5b-01.
- Acceptance: a green scheduled run.
- Needs maintainer: no
- Effort: S

Also seen: the doc comment of `vmrun.GuestOptions` mentions a `Verbose` field that does not exist (`internal/testbed/vmrun/guest.go`). Fix it when touching the file.

## M0 (spikes, plan, docs)

### M0-03 ESP32 QEMU fork not evaluated
- Status: open
- Severity: low
- Reason: deferred — §4.5 calls it "a candidate … has to be evaluated first"; no milestone owns it.
- Evidence: plan.md:1168.
- Task: remove the sentence about the Espressif QEMU fork from §4.5 (plan.md:1168); firmware tests keep using real devices.
- Acceptance: doc review.
- Needs maintainer: decided 2026-10-04: remove the ESP32 QEMU candidate from §4.5 (see the revised task).
- Effort: S

Removed from the old list: "Devcontainer rebuild never verified" — wrong: CI builds `.devcontainer/Dockerfile` on every run (jobs `testbed-vm`, `testbed-privileged`); only the `start.sh rebuild` wrapper is untested. "Q1 evidence outdated" moved to M1-01, "Nightly kernel matrix" to M1-02.

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

Verdict: incomplete. Every scope and test item is implemented and tested; open is documentation and small gaps (revision retention and the validation rules/codes are done).

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

### M2-03 "Configured address beats a lease" not confirmed
- Status: open
- Severity: low
- Reason: needs-decision — identity resolution ranks a configured address above a DHCP lease, stronger than §2.3 says.
- Evidence: `internal/domain/observed.go:106-113`; `TestTwoDevicesClaimingOneAddressTheStrongerClaimWins`.
- Task: keep: add a sentence to plan §2.3 and development.md "DHCP and devices"; change: swap `claimLease`/`claimExplicit` and update the test.
- Acceptance: doc review (plus a unit test if changed).
- Needs maintainer: decided 2026-10-04: (a) configuration wins (current behaviour); document it.
- Effort: S

### M2-08 Persistence layout differs from plan §3.6
- Status: new
- Severity: low
- Reason: needs-decision — §3.6 specifies `/etc/chaos-gateway/{config.json,revisions/}`, `/var/lib/chaos-gateway/{secrets,runs,captures,state}`, `/var/log/chaos-gateway/audit.jsonl`; the code uses flags and named volumes at `/var/lib/chaosgw/{state,secrets,api}`; revisions also have `<id>.status.json`.
- Evidence: plan.md:891-903; `deploy/compose.api.yaml`; `store.revPath`/`statusPath` (store.go:133-138).
- Task: per the decision, update §3.6 to the real paths, volumes and status files, or switch the compose files to bind mounts at the §3.6 paths in M28 (record in the M28 scope).
- Acceptance: doc review.
- Needs maintainer: decided 2026-10-04: (b) bind mounts at the §3.6 paths in M28. Amend §3.6 now for the status files and the path flags, and add the switch to the M28 scope.
- Effort: S

## M3 (executor and state reader)

Verdict: incomplete. All scope and test items exist; open are one literal scope rule (the uplink may only get a qdisc), the per-process generation decision, and plan/doc drift.

| Plan item | Status | Evidence |
|---|---|---|
| Typed closed operations, argument arrays, fixed paths, namespace targeting | done | `internal/executor/op.go`, `runner.go`; `TestRunnerUsesOnlyFixedAbsolutePaths`, `TestNamespaceIsPassedToEveryCommand` |
| Parsers `ip -j`, `nft -j`, `tc -j` | done | `internal/linux`; `parse_test.go` |
| Unix socket, version handshake, `SO_PEERCRED` | done | `proto.go`, `server.go`; `TestPeerCredentialsAreChecked`, version-mismatch tests |
| Scope: nft only `inet chaosgw`; routing only own tables/rules | done | `validate.go`; testbed `TestNftablesApplyIsAtomicAndScoped`, `TestExecutorCannotDeleteForeignRulesOrRoutes` |
| Scope: tc only assigned interfaces **and the uplink qdisc** | partial | the uplink is a fully assigned interface (M3-01) |
| Single `DOCKER-USER` operation | done | `DockerUser`; `TestDockerUserAcceptRulesAreMaintained` |
| Serialized queue with identity priority | done | `exec.go`; `TestIdentityUpdatesGoBeforeQueuedPlansAndNeverRunConcurrently` |
| Container hardening profile | done | `deploy/compose.executor.yaml`; `TestExecutorContainerHardeningProfile` |
| T: integration reads the gateway namespace; out-of-scope rejected; fuzz 5 min/nightly; version mismatch | done | testbed `TestExecutorReadsTheGatewayNamespace`; `TestDecodeRejects`; `FuzzDecode`/`FuzzFrame`, Makefile, nightly.yml |

### M3-01 The executor has no rule for OS-owned interfaces (uplink)
- Status: open
- Severity: low
- Reason: needs-decision — plan M3 says "tc only assigned interfaces and the uplink qdisc"; the uplink is a fully assigned interface, so `links` (down, addr_delete, enslave), `sysctl`, `wireguard` and `service_ns` ops are accepted on it; in the two-port topology it is also the management NIC.
- Evidence: `internal/executor/scope.go:67-134`; `internal/compiler/compile.go:365-372`; docs/development.md:141-142.
- Task: 1. Add `OSOwned []string \`json:"os_owned,omitempty"\`` to `AssignInterfaces` (`op.go`), validated as a subset of `Devs`, stored in `Scope` and in the state file. 2. In `Scope.Check` refuse `Links` entries, per-device `Sysctl` entries, `WireGuard` and `ServiceNS` naming an OS-owned interface (`ErrOutOfScope`). 3. Compiler: `Target.OSOwned` (uplink, plus the management interface when assigned), passed in `internal/apply/plan.go` where `AssignInterfaces` is built. 4. Tests: `TestOSOwnedInterfacesTakeOnlyTrafficControlRoutesAndOffloads` (exec_test.go), rejection cases in decode_test.go, a fuzz seed. 5. Update development.md.
- Acceptance: `go test ./internal/executor ./internal/apply ./internal/compiler`; CI testbed level 1/1b green.
- Needs maintainer: decided 2026-10-04: (A) implement the `os_owned` class.
- Effort: M

### M3-04 Executor generation restarts at 0
- Status: open
- Severity: low
- Reason: needs-decision — `Outcome.Generation` is per process; nothing in the plan requires persistence (the API generation lives in the engine, see M5-01).
- Evidence: `internal/executor/exec.go:57-60,251-262`.
- Task: doc comment on `Outcome.Generation`: "counts mutating requests since this executor started; not persistent".
- Acceptance: doc review.
- Needs maintainer: decided 2026-10-04: (B) document the counter as per-process.
- Effort: S

## M4 (compiler, preview, safe apply)

Verdict: incomplete. All scope and test items exist and pass; open are a spec/code mismatch and a documented deferral.

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

### M4-02 The management matrix endpoint differs from the spec
- Status: open
- Severity: low
- Reason: needs-decision — the spec says endpoint `management` is "`Management.allowed_sources` and the management interface subnet"; the code uses only `allowed_sources` when they are set.
- Evidence: api/openapi.yaml:2560 vs :2294; `internal/compiler/compile.go:315-334`; `rules.go:277-281`.
- Task: (A) change the description at openapi.yaml:2560 to "`Management.allowed_sources`, or the management interface subnet when none are given"; run `make check-spec check-generated check-clients`. (B) add a set `mgmt_net` (allowed_sources ∪ interface subnet) used only in `matrixRules`, golden update, `TestTheManagementEndpointIncludesTheInterfaceSubnet`.
- Acceptance: doc review + `make check-spec` (A) or local unit test (B).
- Needs maintainer: decided 2026-10-04: (A) the spec follows the code.
- Effort: S

### M4-07 `chaosgw apply --file` without `--state-dir` has no rollback
- Status: open
- Severity: low
- Reason: deferred — documented (development.md:210); without a store there is nothing to restore.
- Evidence: `cmd/chaosgw/apply.go`.
- Task: print a warning on stderr when `--state-dir` is missing; assert it in `TestApplyFileFailures`.
- Acceptance: `go test ./cmd/chaosgw`.
- Needs maintainer: decided 2026-10-04: (B) warn on stderr.
- Effort: S

Removed from the old list: "tc tokens allow `/` and `..`" — wrong: `..` is rejected (`internal/executor/validate.go:423`, test "tc path traversal token"). "Supervisor helper deferred to M8a" (Covered later) — wrong: the supervisor exists and is used; only the reader pool and time stamps go to M8a, overlay removal on stop to M27.

## M4b (WireGuard networks and clients)

Verdict: incomplete. All plan tests exist and passed in CI (run 37147106957); open are two accepted-as-documented deferrals.

| Plan item | Status | Evidence |
|---|---|---|
| Hub and link as network type, `wg` tool, clients with client networks, reachable lists, static routes, policy rules | done | `internal/compiler/wireguard.go`; `TestWireGuardRoutesAndRules`; testbed `TestLocalDeviceReachesAClientNetworkWithoutNATAndTheReverseNeedsTheMatrix`, `TestALinkWithStaticRoutesCarriesTrafficToTheRemoteSite` |
| Routed without NAT to test networks | done | testbed `integration_wg_test.go:251,275,304` |
| Masqueraded towards the uplink | done | `TestMasqueradeTowardsTheUplinkOnlyAndForTheNetworksBehindClients` (a link matches by interface, covering learned routes too); testbed `TestAClientNetworkIsMasqueradedTowardsTheUplink` |
| Keys, preshared keys, export once, `.conf`/QR/zip | done | `internal/wireguard/*_test.go`; `TestQRCodesAndNetworkExports` |
| Client status and events, clients as devices, role management, MSS clamp, MTU | done | `engine/wireguard.go`; testbed `TestDisablingAClientStopsItsHandshakeAndEmitsTheEvent`, `TestRolesDecideWhoReachesTheControlPlane`, `TestTheMSSIsClampedOnTheTunnel` |
| T: export works in a fresh namespace, QR decodes, keys never in logs, re-apply keeps the tunnel | done | `TestPrivateKeysStayOutOfStoreSnapshotAndLogsAndAReapplyKeepsTheTunnel` |

### M4b-04 Verify checks preshared keys by presence only
- Status: open
- Severity: low
- Reason: deferred (documented) — a changed PSK value without a generation bump is not noticed; provisioning never does that.
- Evidence: `internal/apply/plan.go:595-596`; docs/development.md:228-233.
- Task: none, or a key-hash fingerprint compared with the executor's read.
- Acceptance: doc review.
- Needs maintainer: decided 2026-10-04: accepted as documented; close the item with a sentence in docs/development.md.
- Effort: S

### M4b-05 The first poll after a restart announces every online peer
- Status: open
- Severity: low
- Reason: deferred (by design).
- Evidence: `internal/engine/wireguard.go:128,145`; `TestPollingTwiceIsRefusedAndTheFirstPollAnnouncesWhatIsOnline`.
- Task: one sentence in docs/development.md.
- Acceptance: doc review.
- Needs maintainer: decided 2026-10-04: intended; close the item with a sentence in docs/development.md.
- Effort: S

## M4c (dynamic routing, BIRD)

Verdict: incomplete. The high bug (external mode) is fixed; four medium items remain, all `needs-decision` or `test-gap` on real-kernel behaviour the dev environment cannot run.

| Plan item | Status | Evidence |
|---|---|---|
| BIRD instance, own config/socket/container | done | `deploy/compose.bird.yaml`, `executor/exec.go:662-758` |
| BGP, OSPFv2 | done | testbed `TestThreeSitesWithBGPAndOSPFLearnRoutesOnlyIntoTheOwnTable` (now also exercises the OSPF import filter) |
| Babel | partial | config only; probably does not run (M4c-05) |
| Static, router id/ASN/neighbors/areas/timers, announcements | done | `compiler/routing.go`; `TestTheBirdConfigurationFollowsTheModel` |
| Import filters | done | `bird/render.go`; the management subnet is protected with explicit `allowed_sources` too, and `allow_default` is honored with an allowed list |
| Export only into own tables | done for table 100 | PMTU tables come with M10 (plan.md updated) |
| `bird -p` + `birdc configure` part of the revision | done | `executor/exec.go:705-758`, `apply/bird.go` |
| …and of preview/diff | done | `Preview.linux.bird` and `.wireguard` are filled |
| Custom snippets | done | `bird/lexical.go` |
| External mode | done | fixed (RTS_INHERIT, not RTS_PIPE); `TestExternalModeImportsAnotherDaemonsTable` |
| Neighbor and route status and events | done | `routing_session_changed` and `routing_routes_changed`; `TestRouteCountChangesAreEvents` |
| Remote-side snippet in link exports | done | `bird/remote.go`, `internal/linkexport`, API `format=bird` |
| T: three sites, learned only into own tables, filters (BGP and OSPF), max-prefix, link down, withdrawal, config change, invalid snippet, failed apply, confirm-timeout rollback | done | see the M4c test names in `internal/engine`, `internal/apply` |

### M4c-02 A BIRD outage fails every apply, including rollbacks
- Status: open
- Severity: medium
- Reason: needs-decision — `planBird` plans `birdc configure` whenever the daemon is not running, so unrelated revisions and restores fail.
- Evidence: `internal/apply/bird.go:40-55`; `executor/exec.go:739-756`; docs/development.md:299.
- Task: 1. In `executor.runBird`, when `birdc configure` fails because the socket is missing or refused, keep the new file (BIRD reads it at start) and return a typed `BirdDownError` instead of restoring. 2. `apply` treats `BirdDownError` as success with a warning `routing_daemon_down`. 3. `verifyBird` reports "not running" as a warning, not as drift. 4. Emit an event (`routing_session_changed` with state `daemon_down`, or add `routing_daemon_down` to the spec). 5. `TestABirdThatIsDownDoesNotFailTheApply` (kernelsim, BIRD not running).
- Acceptance: unit tests in `internal/apply`, `internal/executor`.
- Needs maintainer: decided 2026-10-04: (a) best effort: write the file, warn, do not fail.
- Effort: M

### M4c-04 A protocol disabled by max-prefix never recovers
- Status: open
- Severity: medium
- Reason: needs-decision — `import limit N action disable` leaves the protocol down until its configuration changes; no flag or event says "limit hit".
- Evidence: `internal/bird/render.go:204-209`; docs/development.md:299-300.
- Task: 1. In `internal/bird/render.go` `importLimit`, emit `import limit N action block`: the session stays up and the routes over the limit are not imported. 2. Parse BIRD's import-limit state from `show protocols all` (the "Import limit" line; check the exact output of BIRD 2.18 in CI or with a local instance) into `ImportLimitHit bool` in `bird.ProtocolStatus`. 3. Show the flag in the routing status of the API (extend the spec field if missing and run `make generate`), and send `routing_routes_changed` (M4c-03) with `limit_hit: true` when it turns on. 4. Update the goldens. Rewrite `TestMoreRoutesThanTheLimitDisableTheSession` as `TestMoreRoutesThanTheLimitAreBlocked`: the session stays established, table 100 holds at most the limit, and the status reports the limit hit. 5. Document the behaviour in docs/development.md.
- Acceptance: unit tests in `internal/bird`; CI testbed.
- Needs maintainer: decided 2026-10-04: (b) `action block` (see the revised task).
- Effort: M

### M4c-05 Babel probably does not work and is not tested
- Status: open (raised from low)
- Severity: medium
- Reason: partial — Babel needs an IPv6 link-local address; WireGuard interfaces get none and nothing adds one (unverified here, no netns).
- Evidence: `internal/bird/render.go:276-281`; `bird/remote.go`; `compiler/rules.go:111-112`; no `fe80`/`addrgenmode` in the code.
- Task: 1. Testbed test `TestBabelOverAWireGuardLink` like `TestBGPOverAWireGuardLinkExchangesRoutes` with `Type: model.RoutingProtocolTypeBabel`. 2. If it fails for lack of a link-local address: the compiler emits `fe80::<last octet of the transfer address>/64` for link interfaces running Babel (new address entry in `WGInterface`; the executor `links` address action accepts `fe80::/64` only). 3. `RenderRemote` prints the matching `ip -6 addr add` hint. 4. Forwarded IPv6 stays dropped.
- Acceptance: CI testbed.
- Needs maintainer: decided 2026-10-04: (a) make Babel work.
- Effort: M

### M4c-10 The preview does not show the effective route for a destination
- Status: open
- Severity: low
- Reason: needs-decision — §2.2.2 promises it; no milestone schedules it.
- Evidence: docs/plan.md:230; `internal/engine/api.go:110-150`.
- Task: amend the M8a scope (`/explain`) to include the table-100 lookup (`ip route get <dst> from <src> iif <dev>` via an executor read) and change §2.2.2 to "shown by explain".
- Acceptance: doc review.
- Needs maintainer: decided 2026-10-04: (a) move it to M8a `/explain`.
- Effort: S (doc) / M (code)

### M4c-12 Routing input rules have no source address match
- Status: open
- Severity: low
- Reason: deferred — accepts match only `iifname <link>`; on a point-to-point WireGuard link only the peer can send.
- Evidence: `internal/compiler/rules.go:101-113`.
- Task: optional `ip saddr <link peer>` for BGP.
- Acceptance: local compiler test.
- Needs maintainer: decided 2026-10-04: accepted as is; close the item with a sentence in docs/development.md.
- Effort: S

## M5 (REST API v1)

Verdict: incomplete. Every scope and test item exists and all 41 `x-milestone: M5` operations have handlers; open are an SSE resync signal, the setup without commit-confirm, and the 413 oversized-body code.

| Plan item | Status | Evidence |
|---|---|---|
| problem+json, UUID or name in paths, cursor pagination, ETag/If-Match/428, merge-patch candidates, idempotency keys | done | `internal/api/{problem,helpers,middleware,revisions,idempotency}.go`; `TestProblemsAreProblemJSON`, `TestIdempotencyKeys` |
| SSE with ids, replay, keepalive | done, gap | `events.go`, `engine/events.go`; no signal for lost events (M5-02) |
| Generation (state, header, SSE `applied`) | done | persisted across restarts (`engine.Config.GenerationFile`); `TestGenerationContinuesAfterRestart`, `TestGenerationSurvivesAnAPIRestart` |
| Capabilities, candidate model, sessions + CSRF, hashed tokens with scopes, setup token | done | `system.go`, `revisions.go`, `auth/auth.go`; `TestLoginSessionCSRFAndLogout`, `TestTokenScopesAndTheirLifecycle` |
| Setup "applies with commit-confirm" (spec `POST /setup`) | missing | M5-03 |
| Admin password reset | done | `cmd/chaosgw/admin.go`: audited, interactive prompt |
| UI/API bound to the management network after setup | done | `cmd/chaosgw/api.go`: also excludes test networks before setup; the loopback binding for the container health check is documented (plan §2.16) |
| Audit log | done | `internal/audit`: retention, failure surfaced as unhealthy, system-originated rollbacks audited |
| T: contract tests, clients compile, E2E testbed, conflicts, confirm_pending, SSE reconnect, slow subscriber, concurrent applies | done | `harness_test.go` `checkContract` (now validates requests and SSE events too), `make check-clients`, `e2e_test.go`, `revisions_test.go`, `events_test.go` |

### M5-02 SSE has no resync signal after lost events
- Status: open
- Severity: medium
- Reason: needs-decision — event ids are per boot; a `Last-Event-ID` older than the buffer or from another boot replays partially or nothing, and the client is not told.
- Evidence: `internal/engine/events.go:64-76`; `internal/api/events.go:153-178`.
- Task: 1. Ids `<boot_id>-<seq>` (`engine/events.go`), parse both parts. 2. If the boot differs or seq is below the oldest buffered seq − 1, first send `event: events_lost` with `{"reason":"restart"|"expired"}`, then the live stream. 3. Add `events_lost` to `EventType` in api/openapi.yaml, `make generate`, add to `knownTypes`. 4. `TestAStaleLastEventIDGetsEventsLost`, `TestAnIdFromAnotherBootGetsEventsLost`.
- Acceptance: local unit tests; `make check-generated`.
- Needs maintainer: decided 2026-10-04: (a) synthetic `events_lost` event.
- Effort: S

### M5-03 Setup applies without commit-confirm
- Status: open (the old list had it twice, under M12 and M8a/M10; neither milestone covers it)
- Severity: medium
- Reason: needs-decision — the spec (`POST /setup`, openapi.yaml:145, "the revision waits for confirmation") requires commit-confirm; the code applies directly because `LockoutRelevant` is false without a current configuration; a wrong management interface at setup locks the admin out.
- Evidence: `internal/api/system.go:403`; `internal/engine/lockout.go:15-16`; `TestTheSetupNeedsTheTokenAndCreatesRevisionOne`.
- Task: 1. `ForceConfirm bool` in `engine.ApplyOptions`, used by `CompleteSetup`. 2. Keep the binder on the pre-setup addresses until the revision is confirmed (pass `Pending != nil` into `listenAddrs`). 3. On rollback (no active revision) reopen setup (`auth.ReopenSetup()` prints a new token). 4. `TestTheSetupWaitsForConfirmation`, `TestAnUnconfirmedSetupIsRolledBackAndReopened` (short confirm timeout); update e2e_test.go:54 to confirm first.
- Acceptance: local unit tests in `internal/api` and `cmd/chaosgw`; CI testbed for e2e_test.go.
- Needs maintainer: decided 2026-10-04: (a) implement commit-confirm for the setup.
- Effort: M

### M5-10 Oversized body gives 400 instead of 413
- Status: open
- Severity: low
- Reason: needs-decision — the spec's error table has no 413 code.
- Evidence: `internal/api/helpers.go:106-110`, `revisions.go:112-115`, `middleware.go:64`.
- Task: add `payload_too_large` (413) to `ErrorCode` and the spec table, regenerate; detect `*http.MaxBytesError` in `decodeJSON` and `CreateRevision`; `statusOf`/`titleOf`; `TestAnOversizedBodyIs413`.
- Acceptance: local unit test.
- Needs maintainer: decided 2026-10-04: add `payload_too_large` (413).
- Effort: S

## M5b (appliance VM harness, level 2)

Verdict: incomplete. Scope done; the level-2 smoke is green with the current deploy config (24.04 and 26.04, three and two ports, run 37171706227, on branch `phase1-deploy-pin-health`, which also added BIRD to the deployment); still open are an SSH check that reuses an old connection and the checksum verification.

### M5b-02 Anti-lockout check reuses the old SSH connection
- Status: open (reworded: not a no-op, but it runs over the connection opened before the apply, which conntrack keeps)
- Severity: low
- Reason: test-gap — no new connection from the management network after the apply.
- Evidence: `internal/appliance/appliance_test.go:250-251`, `vm.go:109-124`.
- Task: `func (v *VM) DialFresh(ctx) error` in `vm.go` (new `ssh.Dial`, run `true`, close); replace `must("true")` with it; also assert the client namespace cannot open TCP 22 on the gateway's LAN address (`nc -z -w 3 <GatewayLAN> 22` fails).
- Acceptance: nightly appliance; `go vet -tags appliance ./internal/appliance`.
- Needs maintainer: no
- Effort: S

### M5b-03 Cloud image checksums not signature-verified
- Status: open
- Severity: low
- Reason: forgotten — `SHA256SUMS` trusted via HTTPS only.
- Evidence: `internal/appliance/images.go:61-68`.
- Task: commit the Ubuntu cloud-image signing key as `internal/appliance/testdata/ubuntu-cloudimage-keyring.gpg`; fetch `SHA256SUMS.gpg` and verify (`gpgv --keyring <file>` or an OpenPGP library); unit test with a locally generated key (httptest) for pass and fail.
- Acceptance: local unit test; nightly appliance green.
- Needs maintainer: no
- Effort: S

(Q1 and the level-2 wording of the plan: see M1-01.)

## M6a (DHCP and device discovery)

Verdict: incomplete. Every plan test exists (testbed tests passed in CI); open are conntrack events, the hook's fork per lease, and a verified `started_at` format.

| Plan item | Status | Evidence |
|---|---|---|
| Kea container, pinned version | done | `deploy/Dockerfile`, `.devcontainer/Dockerfile` (`ARG KEA_VERSION`) |
| One subnet per network, pools, reservations via `config-set`, DHCP on/off | done | `internal/compiler/dhcp.go`, `internal/kea`; `TestTheClientDrivesARealKea`; testbed `TestDhcpOffOnOneNetworkLeavesItSilent` |
| Lease events via `run_script` | done | `cmd/chaosgw/keahook.go`, `api/devices.go:312`; `TestACommittedHookCarriesEveryLease` |
| Flow observer on conntrack events | partial | polled every second (M6a-04) |
| Flows API | done, gap | `started_at` needs a format verified on a real kernel (M6a-03) |
| Discovery from leases, neighbors, conntrack, WG clients | done | discovery by address also covers a LAN network's own downstream routes |
| Identity events, incremental updates | done | `owner.go`, `applyloop.go`; `device_identity_changed` carries its generation |
| Manual device merge | done (as a revision per the spec) | `domain/observed.go:278`; moving overlays to the configured device waits for M8a (plan.md updated) |
| Devices API | done | `upload_bps`, `download_bps`, `flows_active` |
| §3.11 identity before plans | done | `TestIdentityUpdatesGoBeforeQueuedPlansAndNeverRunConcurrently` |
| T: lease, MAC/IP, reservation, DHCP off, discovered vs configured, identity within 1 s, flows | done | `internal/engine/integration_dhcp_test.go` |
| T: burst debounced into one identity update | done | `TestABurstOfNeighborChangesIsOneIdentityUpdate` |

### M6a-03 `started_at` needs a verified conntrack timestamp format
- Status: open (narrowed 2026-10-04: `network` now also matches WireGuard interfaces and routed client/link networks, `service` is set for traffic redirected to the DNS proxy, and `DeviceObserved.upload_bps`/`download_bps`/`flows_active` are filled from conntrack's per-address byte counters — done, see `phase1-devices-flows`)
- Severity: low
- Reason: test-gap — `started_at` needs `nf_conntrack_timestamp` enabled and parsing `conntrack -L -o ktimestamp`, whose exact field name and format cannot be verified without a real kernel; left out rather than guessed at.
- Evidence: `internal/engine/observe.go` (`Flow.Service`, `Flow.Network` done; no `StartedAt`); `internal/linux/conntrack.go`.
- Task: in a CI testbed run (or a privileged container), enable `nf_conntrack_timestamp`, capture `conntrack -L -o ktimestamp` output and confirm the field, parse it into `Flow.StartedAt`, add `started_at` to `flowView`.
- Acceptance: CI testbed; local unit test.
- Needs maintainer: no
- Effort: S

### M6a-04 Conntrack is polled, not followed through events
- Status: open
- Severity: medium
- Reason: needs-decision — the M6a scope and §3.1/§3.4 name conntrack events; the code runs `conntrack -L` every second and on each `GET /flows`; the executor protocol has no streaming read.
- Evidence: `engine/observe.go:123,259`; `executor/plan.go:295`.
- Task: 1. Executor: add a streaming read. The protocol is request/response today, so add a new request kind on its own connection: `{"type":"watch","what":"conntrack","namespace":...}` after the hello. The server runs `conntrack -E -o id` (fixed path, argument array, in the target namespace) as a child of that connection and sends one JSON line per event (`new`/`update`/`destroy` with the parsed tuple, `internal/linux/conntrack.go`). It kills the child when the connection closes or the executor stops. Watches are reads: they do not take the write queue, need the same peer-credential check, and are limited to a few per client. 2. Client: `executor.Client.Watch(ctx, what, ns) (<-chan json.RawMessage, error)`, plus `executor.Redialing` support with reconnect. 3. Engine: `FollowConntrack(ctx, debounce)`, like `FollowNeighbors`, triggers `TriggerObserve` (debounced 100 ms). The 1 s `conntrack -L` poll becomes a fallback every 10 s. 4. kernelsim: a scripted event source for unit tests. 5. Tests: an executor unit test of the watch lifecycle with a fake runner (the child is killed on close; goleak), an engine unit test that an event triggers an observation, and a testbed test that a new flow from client A appears in `GET /flows` within 300 ms without waiting for the poll. 6. Wire it in `cmd/chaosgw/api.go`. Document it in docs/development.md (executor operations, M6a).
- Acceptance: doc review (a) or a CI testbed test that a new flow triggers an observation within 200 ms (b).
- Needs maintainer: decided 2026-10-04: (b) implement conntrack events now (see the revised task).
- Effort: S (a) / L (b)

### M6a-09 The Kea hook forks a process with TLS per lease event
- Status: open
- Severity: medium
- Reason: needs-decision — `run_script` with `sync:false`, unbounded; a DHCP flood from an untrusted device forks `chaosgw` plus a TLS handshake per lease.
- Evidence: `internal/kea/config.go:185`; `cmd/chaosgw/keahook.go:22-49`.
- Task: `chaosgw kea-hook` writes one JSON line to a Unix datagram socket `/run/kea/chaosgw-events.sock` (created by the API in the shared `chaosgw-kea-run` volume), non-blocking, dropped when full; the API reads, coalesces and calls `Engine.LeaseEvent`; HTTP stays as fallback; unit tests for reader and hook.
- Acceptance: local unit tests.
- Needs maintainer: decided 2026-10-04: (a) a datagram socket to the API.
- Effort: M


Removed from the old list: "Only the first MAC is reserved" — the spec defines `fixed_ip` as "DHCP reservation for the device's first MAC" (openapi.yaml:2752). "Executor priority only unit-tested" — plan §3.11 prescribes exactly that (fake-executor test). "Online ignores leases" → M6a-24.

## M6b (DNS proxy and service namespace)

Verdict: incomplete. Scope and tests exist and the DNS testbed test passed in CI; open are a dead holder blocking applies and a port default.

| Plan item | Status | Evidence |
|---|---|---|
| Holder `svcns`, `svc0` pair, table 102 with prohibit fallback | done | `cmd/chaosgw/svcns.go`, `compiler/service.go`, `apply/service_test.go`; the holder's PID is checked against the netns inode it reported, so a reused PID is refused |
| Re-attach on holder change | done | `engine/service.go` (WatchService), `executor/exec.go`; `TestAHolderThatRestartsIsNoticedAndTheNamespaceReplaced` (simulated kernel), `TestAHolderRestartIsHealedWithoutHelp` (real holder process) |
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

### M6b-02 A failing service-namespace step fails every apply
- Status: open
- Severity: medium
- Reason: needs-decision — a dead holder (PID file survives a crash in the named volume) makes every revision apply and every rollback fail.
- Evidence: `internal/executor/exec.go:335-341`; `internal/apply/plan.go:66-68`; docs/development.md.
- Task: before compiling, if `ServiceHolderPID()` names a process that does not exist, compile with `HolderPID=0` or skip `serviceOp` (table 102 and the forward guard keep traffic fail-closed); `Snapshot.ServiceError` plus a problem event; `TestADeadHolderDoesNotBlockARevisionApply` (simulated kernel: apply succeeds, `ServiceError` set, a later live PID re-attaches).
- Acceptance: local unit test; CI testbed e2e still passes.
- Needs maintainer: decided 2026-10-04: (a) degrade and report.
- Effort: M


### M6b-12 `ui_port` default in the spec (443) differs from the code
- Status: open
- Severity: low
- Reason: needs-decision — M6b made the API's own port (`--port`, 8443) the default of the gateway's input rules; the spec still says `default: 443`.
- Evidence: api/openapi.yaml:2296-2300; `compiler.Input.DefaultUIPort`, `cmd/chaosgw/api.go`.
- Task: replace `default: 443` with the description "Default - the port the API listens on (8443 in the shipped compose files)"; `make generate`.
- Acceptance: `make check-generated`.
- Needs maintainer: decided 2026-10-04: (a) the spec follows the code.
- Effort: S

Removed from the old list: "Query log in memory only" — the plan asks only for `/dns/queries` with a device filter (§2.13); persistence is not required.

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
| M5: `lockout_protected` never produced | M9 |
| M5: `capacity_exceeded` never produced | M8a, M10 |
| M5: `target_busy` never produced | M15 |
| M5: IPv6 management addresses never bound | M32 |
| M5: `counter_epoch` always 0 | M8b |
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
| Identity maps (address → device), classification and faults not compiled yet | M7, M8a, M8b |
| Reader pool and operation time stamps (M3 deferral) | M8a |
| Overlay removal on stop | M27 |
| M6b: DNS faults, hostname selectors, redirect of hardcoded resolvers, DoT blocking, `/internal/dns/resolutions` | M20 |
| M6b: classification sets the service mark (bit 20) that table 102 routes on | M7 |
| M6b: TLS responder joins the service namespace | M21 |
