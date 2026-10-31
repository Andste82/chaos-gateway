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
  under [Phase 3 open items](#phase-3-open-items), right after the Phase 3 audit
  result; the maintainer decided those items on 2026-10-14 and every one is closed (P3-M12-05, the recovery
  instructions the safe-mode banner links to, was the last; closed with M30 on 2026-10-29). The choices the audit's fixes made follow the
  items ("Phase 3 audit: choices made in …").
- **Phase 3.1** (M35a): merged to `main` on 2026-10-17 (PR #68, `0fb9176`; CI run 39057670486 on `0fb9176` green), audited on
  2026-10-17. The result is in [Phase 3.1 audit result](#phase-31-audit-result), right before the Phase 3.1 open items; its
  items P31-AUDIT-01 to -03 and -05 to -10 were fixed on the branch `close-phase-3-1`, merged to `main` with PR #69 on 2026-10-18 (`4ec8987`; CI run 39140329787 on `4ec8987` green); P31-AUDIT-04 (level 2 on the final
  code) is closed by the Nightly run on that branch (39084099383, green); P31-AUDIT-11 to -14 were decided by the maintainer on
  2026-10-18 and are closed on that branch (each implemented with its tests), and the audit's alternative to P31-AUDIT-01 was
  decided against (the entry stays allowed, no warning), see [Phase 3.1 audit items for the maintainer](#phase-31-audit-items-for-the-maintainer).
  A review of those decisions found six minor items, fixed on the branch (see the audit result). Every item of the audit is closed. What the
  implementation could not settle is under [Phase 3.1 open items](#phase-31-open-items), right after the Phase 3.1 audit result:
  P31-M35a-01 to P31-M35a-10, all closed on 2026-10-17: P31-M35a-05, -09 and -10 decided by the maintainer, the others decided
  by best practice in the context of the project with the maintainer's agreement.
- **Issue #64** (branch `close-phase-3-1`, merged with PR #69, which closed issues #57, #63 and #64 on 2026-10-18): I64-01 to I64-03 were decided by the maintainer on 2026-10-18 (I64-01 and I64-03
  as implemented, I64-02 by mounting the host's `/usr/lib/systemd/network` into the executor), I64-04 records the plan's
  wording that the issue made outdated (fixed), and I64-05 (the tests against the real Kea in the testbed jobs) is closed;
  all are closed, see [Issue #64 open items](#issue-64-open-items) further down.
- **Phase 4** (M15–M19, branch `worktree-phase-4`): audited on 2026-10-19 at `89d1090` (CI run 39253445201 green). The result is in
  [Phase 4 audit result](#phase-4-audit-result), right before the Phase 4 open items: 13 items of major and 46 of minor severity,
  P4-AUDIT-01 to P4-AUDIT-59, all closed on the branch `worktree-phase-4` (merged to `main` with PR #71 on 2026-10-20, `9ab7a24`; CI run 39384159501 green): 56 fixed, three fixed
  differently (P4-AUDIT-06, -29, -41), none rejected or left undone, see the verdict table and
  [the remaining points](#phase-4-audit-result-of-the-round-and-remaining-points). The maintainer decided P4-AUDIT-14 to -16 on
  2026-10-19 (option (a) each), see [Phase 4 audit items for the maintainer](#phase-4-audit-items-for-the-maintainer). What the implementation could not settle and
  that no later milestone completes is under [Phase 4 open items](#phase-4-open-items) at the end of the file. P4-M19-01 (the Captures screen has no milestone) was decided on 2026-10-19: option (a), milestone M23a in Phase 5; every Phase 4 open item is closed.
- **Phase 5** (M20–M23c, M20 to M23a merged to `main` with PR #120, the rest with PR #121, merged to `main` on 2026-10-27, `1c969c4`, CI run 40434567365 on `1c969c4` green in every job): audited on
  2026-10-26 at `eab804c` (CI run 40270141299 green). The result is in [Phase 5 audit result](#phase-5-audit-result), right before
  the Phase 5 open items: 10 items of major and 33 of minor severity, P5-AUDIT-01 to P5-AUDIT-43. The round after the audit
  (2026-10-26, branch `phase-5b`, PR #121, merged to `main` on 2026-10-27, `1c969c4`; CI run 40335480067 on `9bfd4cf` green in every job) closed 42 of them: 38 fixed, four fixed differently (P5-AUDIT-09, -15, -26,
  -35), none rejected or left undone, see the verdict table and
  [the remaining points](#phase-5-audit-result-of-the-round-and-remaining-points). The one item that needed the
  maintainer, P5-AUDIT-10 (what a test network without a `dhcp` block means), was decided on 2026-10-27 (option (b)) and is
  fixed on the same branch, see [Phase 5 audit items for the maintainer](#phase-5-audit-items-for-the-maintainer). The nine open items P5-M20-01 to P5-M23-01 were decided by the maintainer on 2026-10-25, see [Phase 5 open items](#phase-5-open-items) at the end of the file: eight closed (the forms and the interception UI became the milestones M23b and M23c; P5-M21-02 and P5-M22-03, the per-case field `upstream`, are implemented on the branch `phase-5b` and ran green in CI run 40258339299 on `aebbae8`, so M21 and M22 are done) and one waiting for H1 (P5-M22-02).
- **Phase 6** (M24 to M26a: diagnostics, probes and calibration, observability, probe management; merged to `main` with PR #125 on 2026-10-29, `6a3e9f2`; CI run 40771332746 on `7048cae` and CI run 40787584917 on `815441e`, the head of the PR, green in every job): audited on
  2026-10-28 at `b80fa86`. The result is in [Phase 6 audit result](#phase-6-audit-result), right before the Phase 6 open items: 14 items of major and 51 of minor
  severity, P6-AUDIT-01 to P6-AUDIT-69 except -39, -56, -57 and -68, which the verifiers refuted. All 65 are closed on the branch (seven fix commits, `c29d100` to `b00df9c`):
  63 with a change of their own, two (P6-AUDIT-16 and -30) already fixed by another item of the round; six are fixed in part or differently (P6-AUDIT-13, -22, -25,
  -42, -43, -55), see the verdict table and [the remaining points](#phase-6-audit-result-of-the-round-and-remaining-points). No audit item needed the maintainer. CI run 40771332746
  covers the fix commits and the CI round after the audit and is green in every job. The open items are
  under [Phase 6 open items](#phase-6-open-items) at the end of the file. P6-M24-01 (the destinations of a diagnostic) and P6-M25-01 (probe management in the web UI and with
  `chaosctl`, which is M26a) were decided by the maintainer on 2026-10-28 (option (a) each) and are closed; P6-M26a-01 (whether M26a belongs to V1) was decided by the maintainer on 2026-10-29 (option (a): part of V1) and is closed. No item of Phase 6 is open.

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
| M1-02 | Arm64 level 1b now or with M28? | With M28, amend §4.4 | with M28 (done as an emulated level 2 smoke test and `deploy/smoke-image.sh`; the namespace testbed on arm64 is P7-M28-01) |
| M2-03 | Configured address vs. DHCP lease: who wins? | Configuration wins (current) | configuration wins |
| M2-08 | Persistence layout: named volumes or §3.6 bind mounts? | Bind mounts in M28; amend §3.6 now for the status files | bind mounts in M28 (done) |
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
| ~~M1: container image has no non-root user (`deploy/Dockerfile`)~~ — done in M29: the image has `USER 65532:65532`, every compose file names its user (`TestEveryContainerNamesItsUserAndTheImageDefaultsToTheUnprivilegedOne`) | M29 |
| ~~M2: overlay `expires_at` / `lease_expires_at` not set by `NewOverlay`~~ — set since M8a (`internal/overlay/store.go`, P2-AUDIT-16) | M8a |
| ~~M3: DNS set updates spawn `nft` per call, no persistent netlink connection~~ — done in M20: the executor operation `nft_dns_elements` writes them on a persistent netlink connection (google/nftables) beside the plans | M20 (plan §3.4) |
| M4: verify matches rules by comment hash only (expressions not compared) | M38 drift detection (partial) |
| M4: Docker started after the last apply is not noticed (`DOCKER-USER`) | M38 drift detection (partial) |
| M4c: received-prefix list in routing view | M14 |
| ~~M5: certificate loaded once at start, SANs fixed (`/system/certificate`)~~ — done in M29: `GET`, `PUT` and `DELETE /system/certificate` replace the certificate for new connections without a restart, the installed certificate has the names its owner chose (`api.Certificates`, `TestTheCertificateIsReplacedForNewConnectionsAndCanBeReset`) | M29 |
| ~~M5: `lockout_protected` never produced~~ — decided 2026-10-11 (P2-M9-08): the anti-lockout rule cannot be overridden and lockout-relevant configuration changes take commit-confirm, which is enough; the code stays reserved | decided |
| ~~M5: `capacity_exceeded` never produced~~ — produced for classes, fault ids, map cells and rules since M8a; the preview refuses with it (M10 test) | M8a, M10 |
| ~~M5: `target_busy` never produced~~ — produced since M15: 409 on `POST /runs?queue=false` for a target with a run | M15 |
| M5: IPv6 management addresses never bound | M32 |
| ~~M5: `counter_epoch` always 0~~ — done in M8b (P2-M8b-05 for what it means) | M8b |
| ~~M5: capabilities list `step_types` empty~~ — filled since M15 (profile, fault, rule, wireguard, wait, remove, restore; the types of M17, M20, M21 and M23 are added by those milestones) | M15 |
| ~~M5b: netplan hints are static text~~ — done in M28: the final host setup names the file and line of the entry and the fix, and exits 1 until the entry is gone (`TestHostSetup*`; level 2 `TestContainerDeploymentOnCleanUbuntuHosts`: the hint, the removal, ready) | M28 |
| ~~M5b: only the executor is deployed, x86-64 only~~ — done in M28: `deploy/compose.yaml` runs every container (`tools/composegen`, `tools/deployscripts`), the image is built for amd64 and arm64 (`deploy/smoke-image.sh`, the weekly emulated level 2 smoke test); the image smoke tests, the compose smoke test and the level 2 tests are written and run in the CI jobs `arm64`, `deployment`, the nightly and the weekly workflow (not run on the development machine: no Docker, no KVM); the arm64 testbed is P7-M28-01 | M28 |
| M0: S8 on real hardware (HTB with 500 classes, 50/250 faults, offloads, loss) | H1 |
| M0: S1 testbed on Raspberry Pi | H1 |
| M0: DNS proxy at least 1000 queries/s on Pi | H1 |
| M0: scenario step timing on real hardware | H1, M15 |
| ~~M0: S7 coexistence with netplan, NetworkManager, systemd-networkd~~ — test written in M28 (`TestContainerDeploymentOnCleanUbuntuHosts`: `netplan apply`, a restart of `systemd-networkd`, NetworkManager installed and running leave the bridge, its address, the table and the traffic alone); it runs in the nightly workflow (not run on the development machine: no KVM) | M28 |
| ~~M0: S4 TLS 1.2 vs 1.3, connection reuse, control from the core~~ — done in M21 for the certificate and handshake cases (both versions in the tests of the responder and of the real kernel; only new connections are selected, an established one stays with its server unless `cut_existing`; the responder is controlled by the API's configuration and decisions) and in M22 for the interception (`TestDevicesOfTLS12AndTLS13AreInterceptedAndTheirSecretsReachTheGateway`, `TestConnectionReuseAcrossRequestsIsOneHandoverAndManyExchanges`; the proxy is controlled by the API's configuration, its rules and the responder's control interface) | M22 |
| M0: IPv6 never spiked | M32 |
| ~~M0: persistent nft session for DNS-derived sets (DNS over TCP is done in M6b)~~ — done in M20 | M20 |
| ~~M0: `image.yml` has never run (ghcr push, release tags)~~ — done in M28 as written: it builds the two architectures, runs the image smoke test, pushes the release tags (`:latest` and the major and minor tags only for a release without a pre-release part) and attaches the deployment files to the release; its first run is the first tag (a pre-release tag such as `v0.0.0-rc.1` tries it without moving `:latest`), which needs a runner with the registry and cannot happen on the development machine | M28 |
| M0: persistent faults for DNS, TLS, DHCP and profile activations (D31) | M39 |
| M0: three-way merge, NFLOG capture, extra diagnostics, drift detection, .deb (D28) | M38 |
| Stubs: ~~`chaosgw tls`~~ — done in M21; ~~`chaosctl` beyond `version`~~ — done in M18 (`chaosctl explain` with P4-AUDIT-47) | M21 |
| ~~Image holds only `chaosgw`/`chaosctl` and executor tools; full image with Kea, tcpdump, mitmproxy~~ — done in M28 (tcpdump added; `tools/ciconfig/deploy_test.go` pins the files of the image) | M28 |
| ~~Identity maps (address → device), classification and faults not compiled yet~~ — done in M7, M8a, M8b | M7, M8a, M8b |
| ~~Reader pool and operation time stamps (M3 deferral)~~ — done in M8a | M8a |
| ~~Overlay removal on stop~~ — done in M27 (`Server.Shutdown`, run by `serveAPI` before the engine ends) | M27 |
| ~~M6b: DNS faults, hostname selectors, redirect of hardcoded resolvers, DoT blocking, `/internal/dns/resolutions`~~ — done in M20 | M20 |
| ~~M6b: classification sets the service mark (bit 20) that table 102 routes on~~ — done in M21 (`compiler/tls.go`: the chain `tls_redirect` writes it right after the classification, with the real redirect-interaction test) | M21 (confirmed 2026-10-11, P2-M7-01) |
| ~~M6b: TLS responder joins the service namespace~~ — done in M21 (`deploy/compose.tls.yaml`) | M21 |

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
| P2-M7-01 | Should M7 write the service-selection mark (bit 20)? | Confirmed: no, it stays deferred to M20/M21, where the real redirect-interaction test is. **Delivered by M21:** the chain `tls_redirect` writes the mark for the TLS cases (E11 and the redirect on the real kernel are tested) |
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
| P2-M9-02 | A rule that names a hostname is not compiled before M20 | **Transferred to the plan, delivered by M20:** the rule looks up the DNS-derived set of the name and a drop rule blocks the addresses the device resolved (`TestADropRuleWithAHostnameBlocksTheResolvedAddressesOnTheRealKernel`, `TestADNSDerivedSetIsDynamicKeyedOnTheDeviceAndTheAddressAndHasATimeout`); the warning `hostname_unresolved` is gone |
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
| P2-M11-01 | A profile with a DNS or TLS part is stored but cannot be activated before M20 and M21 | **Transferred to the plan:** M20 (`dns`) and M21 (`tls`) lift `domain.unavailableFamilies` family by family, and their acceptance criteria include that such a profile activates and its part competes in its family; until then the activation is refused with `unsupported_feature`. **The `dns` family is lifted (M20):** `dns-broken` and a custom profile with a `dns` part activate and compete in the DNS family (E5); **The `tls` family is lifted (M21):** "TLS broken" and a custom profile with a `tls` part activate and compete in the TLS family; only the case `intercept` of a `tls` part stays unavailable until M22 |
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
only P3-M12-05 stayed open until M30 wrote the recovery instructions (closed 2026-10-29).

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
  Built in M26 (2026-10-27): the Path card of the device detail (`docs/development.md`, "Metrics and flow view (M26)").
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
and how the decision was implemented. As of 2026-10-14 every Phase 3 item was closed except P3-M12-05, which waited for
M27/M30 and was closed with M30 on 2026-10-29.

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
  Tests (Playwright for the device detail's flows and recent DNS queries); `docs/development.md` (M12) says so.
  Built in M26 (2026-10-27): the Flows and Recent DNS queries cards of the device detail.
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

- Status: closed (2026-10-29, M30): `docs/recovery.md` is the recovery instructions (safe mode, the last known good revision, the
  teardown, the update that went wrong, backup and restore), and the link "How to recover" of the banner in
  `web/src/components/layout/AppShell.vue` leads to `docs/recovery.md#safe-mode`. The tests that hold it:
  `TestTheBannerOfSafeModeLinksToTheRecoveryInstructions` (`internal/docscheck`: the link points into the file and at a heading that
  exists) and the href check in `web/e2e/states.spec.ts` ("safe mode: a full-width red banner ..."); the examples of the document run in
  CI (`make test-docs`, the job `docs`)
- Severity: low
- Reason: deferred. Safe mode itself came with M27 (the API reports `safe_mode` and `safe_mode_reason`, which the banner
  shows); there was no recovery document yet. The banner's link pointed to plan §3.9 "Updates and Recovery". What was left is
  the link target, which M30 wrote.
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
  like the fault dialog (parts per family). "Measure with probe" is on the profile cards since M25.
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
  (the `inet chaosgw` nftables table, its policy rules and routing tables 100 to 109 (the policy table 100, the table 101
  of the replies of a management-side port forward, the service table 102 and the PMTU mirror tables 103 to 109; the own
  range is 100 to 110), the test bridges with their
  addresses, the service veth pair `svc0`/`svc1`, the probes' namespaces `cgp-<8 hex digits>` and the gateway ends `pv<8 hex digits>` of their veth pairs (M25) with their DHCP clients (goroutines of the executor, stopped first; the ends are removed by name because a deleted bridge does not delete its ports) and the name `cgsvc` under `/run/netns`; the namespace itself belongs to the
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
  the test interface. Both are host changes made by hand during the install test. The host setup now writes and removes
  the networkd rule (`host-setup.sh --test-interface`, `--remove-test-interface`, issue #64); whether the executor should
  do it is I64-03. The netplan entry stays the operator's to remove.
- Implemented in M27: the executor removes all of it on `SIGTERM` (`cmd/chaosgw/exec.go`, `apply.Teardown`), `chaosgw teardown`
  does the same for a gateway that was killed, and `--purge` empties the volumes through the `teardown` service of
  `deploy/compose.teardown.yaml`, which mounts every volume writable (`docs/install.md`, "Stop, restart and recovery").
- Status: **implemented** (M27). What stays out of scope is the host's: the networkd rule and the netplan entry of a test
  interface (I64-03).
- Needs maintainer: no (decided 2026-10-16).

## Phase 3.1 audit result

Date: 2026-10-17. Audited on `main` at `0fb9176` (PR #68 merged), against `docs/plan.md` §5 Phase 3.1 (M35a Scope and Tests),
D34, D35, §2.2, §2.4, §2.15 to §2.17, §3.3, §3.4, §3.8, §3.11, `api/openapi.yaml`, the Phase 3.1 open items and
`docs/development.md` "Port forwarding (M35a)". The last part of the phase, the three-port management guard and its
follow-ups (P31-M35a-10), had had no independent review before. The audit read `main` through several lenses: backend and
security, the management guard in both topologies, regressions against the state before Phase 3.1 (`3aa5767`), the kernel
(the compiled nftables, routing and conntrack against the executor's limits), the UI and its states (§2.17), the documents,
and the tests and CI. Two review rounds of the fixes followed on the branch. Fixed on the branch `close-phase-3-1` (the "close
Phase 3.1" round), each with a test that fails without the fix or with the corrected document: P31-AUDIT-01 to -03 and -05 to
-10 (table below; P31-AUDIT-04 is closed by the Nightly run 39084099383 on `d9b5e87`: the appliance tests on Ubuntu 24.04 and 26.04 and the level-1b kernel matrix passed). P31-AUDIT-11 to -14 needed the maintainer and are
recorded with their options under [Phase 3.1 audit items for the maintainer](#phase-31-audit-items-for-the-maintainer); the
maintainer decided them on 2026-10-18 and they are implemented on the branch with their tests, as is the decision against the
audit's alternative to P31-AUDIT-01. Six further findings were checked and rejected. The branch was merged to `main` with PR #69 on 2026-10-18 (`4ec8987`; CI run 39140329787 on `4ec8987` green).

| Verdict | Items |
|---|---|
| Fixed on the branch, with a test or the corrected document | P31-AUDIT-01 to -03, -05 to -10 |
| Closed by the Nightly run on the branch (39084099383, green) | P31-AUDIT-04 |
| Decided by the maintainer on 2026-10-18 and implemented on the branch | P31-AUDIT-11 (b), -12 (a), -13 (b), -14 (a); P31-AUDIT-01's alternative not taken |
| Checked and rejected | 6 findings |

| Item | Severity | Finding | Fix |
|---|---|---|---|
| P31-AUDIT-01 | major | An explicit matrix entry to the management network could not open the three-port guard on the normal path: its return matched `oifname mgmt0`, but table 100 has no route to the management network, so the packet leaves through the uplink and the guard dropped it, while `explain` said allow. With allowed sources narrower than the management subnet, `explain` also counted the whole subnet as the management network in both topologies (two-port: a broker on the uplink's subnet was reported as guarded, the kernel let it through). | The three-port return and the matrix endpoint `management` as a destination match the destination only (`ip daddr @mgmt_src`), and an entry also returns for the management subnet the guard drops. `explain` uses the set the kernel matches: the allowed sources, in the three-port topology also the subnet (`AccessFacts.ManagementSubnet`). Tests: `TestAnExplicitMatrixEntryReturnsFromTheThreePortGuard`, the `access-threeport` golden, `TestTheGuardCoversWhatTheKernelGuardDropsWithNarrowAllowedSources`, `TestExplainUsesTheKernelsManagementSetWithNarrowAllowedSources`, level 1 `TestAMatrixEntryOpensTheManagementNetworkThroughTheUplink` (in `M35A_LEVEL1`). The audit's alternative, to refuse or warn on such an entry in the three-port topology, was decided against by the maintainer on 2026-10-18: the entry stays allowed, without a warning (no code change). |
| P31-AUDIT-02 | minor | The forwards' counting rules and the DNAT rule in the inet NAT chain also matched IPv6: an IPv6 connection to an entry interface's own address (link-local, SLAAC on the OS-owned uplink) on a forwarded port was counted, noted as the last match and, on the management side, marked with bit 22, although it is never forwarded. | `meta nfproto ipv4` in both rules. Tests: `TestEachForwardCountsItsConnectionsInTheNATChainInFrontOfTheDNAT`, `TestTheDNATRuleStandsInThePreroutingNATChainNextToTheServiceRedirect`, the `forward` goldens, level 1 `TestAForwardDoesNotCountIPv6ConnectionsToItsEntryInterfaceOnTheRealKernel` (in `M35A_LEVEL1`). |
| P31-AUDIT-03 | minor | Since `Identity.Current` was compared, a neighbor entry going STALE and back (with a connection still on its address) reordered a device's addresses and made a new generation, an applied event and an empty identity apply, also without any port forward. | The order counts only for a device that a forward sends to. Test: `TestTheOrderOfADevicesAddressesMattersOnlyForAForwardTarget`. |
| P31-AUDIT-04 | minor | Level 2 (the appliance smoke, M35a Tests) never ran on the final Phase 3.1 code: the last runs were on `97cd058` (before the counting rule in NAT, the replies by mark bit 22 and table 101, and the three-port guard) and on `main` before the merge. | Closed: the Nightly run 39084099383 on `d9b5e87`: the appliance tests on Ubuntu 24.04 and 26.04 and the level-1b kernel matrix passed. |
| P31-AUDIT-05 | minor | The status lines (`docs/plan.md`, this file, `README.md`) still said "on the branch … merges to main with the PR" and cited a CI run from before P31-M35a-05/-09/-10; P31-M35a-09's evidence named a test that no longer exists. | Status lines updated (merged as `0fb9176`, CI run 39057670486, audited); the test name in P31-M35a-09 marked as the state before the decision. |
| P31-AUDIT-06 | minor | P31-M35a-08 named `chaosctl` as a way to explain a forwarded connection; `chaosctl` supports only `version` until M18. | The item names the API (`GET /explain`, or the generated clients); `chaosctl` gets `explain` with M18. |
| P31-AUDIT-07 | major | The device detail said "No port forward leads to this device." while the active revision, which the forwards come from, was loading or had failed to load; the error of the revision was not shown (the class of P3-AUDIT-10). | The card shows "Loading the port forwards…" until the revision is there and "The port forwards are not loaded" with Try again when it failed; the empty sentence only with the revision loaded. Test: e2e `port forwards: the configuration not loaded says so with Try again on the cards and the device detail, never "none"`. |
| P31-AUDIT-08 | minor | The uplink card and the management card listed no forward and said nothing when the active revision was not loaded; Add port forward was disabled without a reason. | Both cards say "Loading the port forwards…" or "The port forwards are not loaded" with Try again until the revision is there. Test: the same e2e case. |
| P31-AUDIT-09 | minor | The device detail marked a deleted forward DELETED in the accent tone; the cards of the networks view mark it in the blocked tone (§2.17: red removed, blue as configured). | The device detail uses the blocked tone for DELETED like `ForwardList`. Test: e2e `port forwards: a deleted forward is marked in the blocked tone on the cards and the device detail alike`. |
| P31-AUDIT-10 | minor | Documents behind the decisions of 2026-10-17: `docs/development.md` gave an `allow` rule into the management network as the example of an exception to the matrix, which the management guard now drops, and called mark bits 22–23 reserved after §3.3 gave bit 22 to the replies of a management-side forward; the M35a Scope accepted a forwarded connection "in from the uplink" only, though `management` is an entry side; `docs/install.md` did not say where the replies of a management-side forward can go; the Phase 3 bullet of this file put its open items "at the end of the file"; the M35a Status left out the three-port guard of P31-M35a-10. | The example uses a test network ("IoT may not reach Lab …") and names the guard as the exception; §3.3 is cited as bit 22 for the replies, bit 23 reserved; the Scope says "in on the forward's entry interface"; install.md's "Things to know" says that a management-side forward answers the management subnet and peers behind the management interface's default gateway only, and that the preview warns without a default route; the bullets point to the section after the audit result; the M35a Status names the guard. |

**Review of the decisions (2026-10-18).** An independent review of the commits that implement the maintainer's decisions
of 2026-10-18 (`a5f5ec0..83ef072`: P31-AUDIT-11 (b) to -14, I64-02, I64-05) found no blocker and no major finding and six
minor ones, all fixed on the branch following the plan's existing rules, each with its tests:

1. *Matrix precedence.* The extra matches of an entry towards `management` for the WireGuard networks of the role
   management kept the management rank, so "IoT -> management: allow" won over "IoT -> admin: deny" (and the reverse
   dropped). They rank after the networks now (`rankMgmtWG`), in `matrixRules` and in `explain`
   (`domain.matrixEndpoint.rank`, `World.matrixEndpoints`), and in the three-port topology `ip daddr @mgmt_src` of a
   `management` entry carries `meta oifname != <the management-role interfaces>`. Tests: the golden files
   `access-{three,two}port-wg-specific-{deny,allow}`, `TestTheSpecificEntryWinsOverTheManagementEntryForAManagementRoleWireGuardNetwork`,
   `TestExplainLetsTheSpecificEntryWinOverTheManagementEntryForAManagementRoleWireGuardNetwork` (domain),
   `TestExplainRanksTheManagementEntryAfterAManagementRoleWireGuardNetwork` (engine).
2. *Upgrade widening.* An existing "X -> management: allow" opens the management-role WireGuard networks now (in the
   two-port topology they were not covered before). The compiler warns (`matrix_management_wireguard`) and names
   them; plan §2.16 and docs/development.md carry the note. Test: `TestAManagementEntryWarnsThatItCoversTheManagementRoleWireGuardNetworks`.
3. *Ineffective `reachable`.* A test client whose `reachable` list names a management-role network, its client or
   `management` compiles to an accept the guard drops: the compiler warns (`reachable_guarded`), naming the client
   and the endpoint and saying that an explicit matrix entry is needed. Test: `TestAReachableListThatTheGuardDropsWarns`.
4. *I64-05 completeness.* `TestKeaAcceptsTheRenderedConfiguration` and `TestTheConfigurationIsSentAgainToAKeaThatRestarted`
   are in `KEA_LEVEL1` (their packages have tagged tests, so the testbed runner builds them; both pass locally as root).
5. *CI mounts job.* The job `mounts` lists `/host/usr/lib/systemd/network` in the started executor container and
   fails otherwise; docs/install.md names `/usr/lib/systemd/network` as a requirement of the host.
6. *Management as a source.* `explain` counted a management hub's tunnel subnet as `management` as a source while the
   kernel's `management -> X` matched only the management interface. Following P31-AUDIT-11 (b), an entry
   `management -> X` also matches what comes in through a management-role WireGuard interface (`iifname`, ranked
   after the networks), in both topologies, and `explain` counts the networks behind its clients too (plan §2.16).
   Tests: `TestAManagementEntryAlsoMatchesTrafficFromAManagementRoleWireGuardNetwork`,
   `TestExplainCountsAManagementRoleWireGuardNetworkAsTheManagementSource` (domain), the engine test of item 1.

### Phase 3.1 audit items for the maintainer

Found by the audit of 2026-10-17 and confirmed in the code (with a scratch compile or test where noted). Each needed a decision;
none blocked the round. The maintainer decided all four on 2026-10-18; each is closed with what was done and its tests.

#### P31-AUDIT-11 The three-port guard closes management-role WireGuard networks to their own matrix entries

- Status: closed (2026-10-18): decided — option (b). The WireGuard networks of the role management (with their clients and
  the networks behind them) are part of the management network for the guard in both topologies: `mgmt_guard` ends with
  `meta iifname @ifs_test meta oifname <interface> counter "forward_drop" drop` for each such network, in the two-port
  topology too. An explicit allow entry towards such a network or one of its clients returns from the guard by its
  destination (the entry's own match, `oifname` and for a client its prefixes), and the endpoint `management` as a
  destination also stands for each such interface (a return and a matrix rule more per interface), so "IoT -> management"
  opens them as it opens the management network (as since P31-AUDIT-01). A client's `reachable` list is not an explicit
  entry and does not open the guard (choice made following plan §2.16 and §2.2.1 and the existing code, where only
  explicit entries return: the implicit entries are what the client may reach, while §2.16 names an explicit matrix
  entry as the one way into the management network; a test client that names a management-role network in
  `reachable` gets the matrix accept but is dropped by the guard). `explain` agrees with the kernel:
  `World.matrixEndpoints` counts an address in such a network as the management network, the guard opens for an entry
  towards the management network or towards the management-role network (or client) the destination lies in, and its
  reason names "a WireGuard network of the role management". Recorded in plan §2.2, §2.4 (the guard bullet) and §2.16,
  `api/openapi.yaml` (`AccessRuleBody`) and docs/development.md "Access rules (M9)". Code: `internal/compiler/rules.go`
  (`matrixRules`, the guard), `internal/compiler/wireguard.go` (`topo.mgmtWG`, `inMgmtWG`), `internal/domain/access.go`
  (`endpointsTo`, `inMgmtWG`, `mgmtGuard`). Tests: the golden files `access-threeport-wg` and `access-twoport-wg`
  (`TestGoldenTheGuardOfManagementRoleWireGuardNetworks`, also in the nft parser and kernel gate),
  `TestAMatrixEntryTowardsAManagementRoleWireGuardNetworkReturnsFromTheGuard` (both topologies),
  `TestAManagementRoleWireGuardNetworkIsPartOfTheManagementNetworkForTheGuard` (domain, both topologies),
  `TestExplainGuardsAManagementRoleWireGuardNetworkInBothTopologies` (engine), the `wireguard` and `routing` goldens, and
  level 1 `TestTheGuardKeepsADeviceOutOfAManagementRoleWireGuardNetworkUntilAMatrixEntryOpensIt` (both topologies, in
  `M35A_LEVEL1`: the testbed's management hub with its administrator in the remote machine "site").
  The review of the decisions (2026-10-18, see [Phase 3.1 audit result](#phase-31-audit-result)) completed it: what
  `management` stands for through these networks ranks after the networks (the more specific endpoint first), in the
  compiler and in `explain`, and the three-port address match of `management` leaves their interfaces out; and,
  following this decision (they are part of the management network), an entry `management -> X` also matches what
  comes in through their interfaces, in both topologies, as `explain` already counted their tunnel subnet as the
  management source (recorded in plan §2.16). The compiler warns on an entry from or towards `management` that now
  covers them (`matrix_management_wireguard`, the upgrade note of plan §2.16) and on a test client's `reachable` list
  that names them or the management network (`reachable_guarded`).
- Severity: major
- Reason: needs-decision. `finishManagementSources` (`internal/compiler/wireguard.go`) adds the subnet of every management-role
  WireGuard network to the management sources, so it is in `mgmt_src`. The three-port guard drops every test interface's packet
  to `@mgmt_src`, whatever it leaves through. An explicit entry `IoT → admin` (the WireGuard network of the role management)
  compiles to an accept behind the guard and never takes effect, and a test-role client whose `reachable` list names that
  network is cut off the same way; nothing warns. Before Phase 3.1 the three-port forward chain had no guard, and the two-port
  guard still drops only `oifname <uplink> ip daddr @mgmt_src`, so the two topologies disagree. Since P31-AUDIT-01 an entry
  `IoT → management` returns from the guard by `ip daddr @mgmt_src` and so opens the management-role WireGuard networks too.
  `explain` agrees with the kernel (it sees the tunnel subnet as management with no management entry).
- Evidence: a scratch compile of `testdata/wireguard.yaml` with the entry `IoT → admin`: `jump mgmt_guard`, then
  `meta iifname br-iot meta oifname wg-admin accept`; `mgmt_guard` drops `iifname @ifs_test ip daddr @mgmt_src` with
  `mgmt_src = {10.98.0.0/24, 192.168.56.0/24}`. `internal/compiler/rules.go` (the three-port branch of the guard).
- Options: (a) the guard covers the management interface's sources and subnet only, as in the two-port topology and before
  Phase 3.1; a management-role WireGuard network is closed by the matrix default like any other network; (b) the guard covers
  management-role WireGuard networks in both topologies, and an explicit entry towards such a network returns from the guard by
  its destination (as the entry `management` does since P31-AUDIT-01), recorded in plan §2.16; (c) as now, plus a compiler
  warning on an entry the guard makes ineffective. Either way a three-port golden and a test for an entry towards a
  management-role WireGuard network, and the same answer in both topologies.
- Recommendation: (b): an admin tunnel is as much the management network as `mgmt0`, and an explicit entry stays the one way to
  open it, as §2.16 says.
- Needs maintainer: no (decided 2026-10-18)
- Effort: S

#### P31-AUDIT-12 Wide forwarded port ranges make an nft transaction the executor refuses

- Status: closed (2026-10-18): decided — option (a) with 16384. The validation refuses a configuration whose enabled
  forwards forward more than `domain.MaxForwardedPorts` (16384) ports together: `validation_failed`, code
  `invalid_port_range`, at `/external_port_end` (or `/external_port`) of the forward that crosses the limit, in the order
  of the names (`OverForwardedPorts`, `validator.forwardedPorts` in `internal/domain/validate_forward.go`). The compiler's
  `forwardRefusal` leaves out, with the warning `forward_refused`, a forward that would take the compiled forwards past
  the limit (the backstop for an old revision). The limit is in `api/openapi.yaml` (`PortForward`), plan M35a Scope and
  docs/development.md "Port forwarding (M35a)". The largest allowed configuration (tcp and udp 1024-9215 to one device of
  the example, whose faults select it too) is a full transaction of 1.07 MB and a move of 1.56 MB, both decoded by the
  executor. Tests: `TestTheEnabledForwardsForwardAtMostTheLimitOfPortsTogether` (domain: the limit, one port more, a
  disabled forward, the order), `TestAForwardTheValidationRefusesIsLeftOutWithAWarning` ("more ports than allowed"),
  `TestTheLargestAllowedForwardsAndADeviceMoveFitTheExecutorsLimit` (level 0: `Nft.Transaction` and the element
  transaction of the move through `executor.Encode` and `executor.Decode` within `maxOperationBytes`). Option (c) stays
  for later if wide unshifted ranges are needed.
- Severity: major
- Reason: needs-decision. Every forwarded port is one element of the DNAT map (P31-M35a-06), about 66 bytes of nft JSON, and
  the whole transaction is one `NftApply`, which the executor refuses above 8 MiB (`maxOperationBytes`). The validation bounds a
  range only by 1–65535, and `maxNftItems` does not count the elements of one `add element` command. tcp 1024–65535 plus udp
  1024–65535 to one device is accepted by `PUT /draft` and its first full apply is 8495777 bytes, refused as a whole. tcp
  1024–65535 plus udp 30000–51000 applies (5.6 MB), but when the device gets a new address the element transaction (delete and
  add every element) is 8453568 bytes and is refused, so the forward stops following its device, and the fallback full apply
  is as large. This reopens the reasoning of P31-M35a-06 (decided on the assumption that large ranges are only slow).
- Evidence: a scratch test with `accessWorld`/`addForward` (both pass `domain.Validate`), `Nft.Transaction` and
  `executor.Encode`/`Decode`: "invalid operation: 8495777 bytes exceed the limit of 8388608". `internal/compiler/forward.go`
  (`compileForwards`), `internal/executor/op.go`, `internal/domain/validate_forward.go`.
- Options: (a) refuse, with the pointer, a configuration whose enabled forwards together forward more than a fixed number of
  ports (e.g. 16384, about 2.2 MB for a move; code `invalid_port_range`), with the same check in `forwardRefusal` as the
  backstop, the limit in `api/openapi.yaml`, and a level 0 test that compiles the largest allowed configuration and a device
  move and decodes both operations within the limit; (b) split the element transaction into several operations (gives up the
  atomic move of a forward); (c) an unshifted range (target port = external port) as one interval element (P31-M35a-06 (c)),
  which helps only unshifted ranges; (d) raise `maxOperationBytes` (only moves the limit).
- Recommendation: (a) with 16384, and (c) later if wide unshifted ranges are needed.
- Needs maintainer: no (decided 2026-10-18)
- Effort: S

#### P31-AUDIT-13 After the last management forward goes, the replies of its open connections take the uplink

- Status: closed (2026-10-18): decided — option (b). In the three-port topology (with the management subnet known) the
  chain `fwd_reply`, the policy rule 925 and table 101 are compiled whether or not a management forward exists
  (`managementReplies` in `internal/compiler/forward.go`), so a reply whose conntrack mark has bit 22 never takes the
  uplink; D11 is kept (the connection lives on, no conntrack work). Only a management forward's counting rule sets the
  bit, and the warning `forward_replies_subnet_only` comes only with a management forward. The goldens of the three-port
  scenarios (`routed`, `routing`, `wireguard`, and the nft transactions `access.nft`, `faults.nft`, `routed.nft`) have the
  chain, the rule and the table now; the two-port ones do not. Recorded in plan §2.2 and §3.3 (bit 22) and docs/development.md "Port
  forwarding (M35a)". Tests: `TestAManagementForwardRoutesItsRepliesToTheManagementNetwork` (it asserted "nothing without
  a management forward"; now the chain, the rule and the table with a disabled management forward and without any
  forward, and nothing in the two-port topology), `TestPolicyRoutingTable100`, the PMTU tests of `internal/apply` and
  `internal/compiler` (they leave the reply rule out of the mirrors' rules), and level 1
  `TestTheRepliesOfAManagementForwardedConnectionStayOffTheUplinkAfterTheForwardIsDeleted` (in `M35A_LEVEL1`: an open
  connection through the management forward, the forward deleted, the connection keeps getting its replies through the
  management interface and a probe table counts none of its packets leaving through the uplink).
- Severity: minor
- Reason: needs-decision. A forwarded connection lives on after its forward is deleted or disabled (P31-M35a-04, D11). For a
  forward from the management side its replies are routed by mark bit 22 through `fwd_reply`, the policy rule 925 and table
  101 (P31-M35a-05). Those are compiled only while a management forward exists; once the last one goes, the replies of a
  connection that is still open are looked up in table 100, whose only way off the gateway is the uplink default, and leave
  through the uplink with the management interface's address as source, although P31-M35a-10 promises that such a reply never
  takes the uplink.
- Evidence: `internal/compiler/forward.go` (`managementReplies`, `forwardReplyChain`, `forwardReplyRouting`), the forward
  chain's `ct state established,related accept` (golden line 2).
- Options: (a) on an apply that removes the last management forward (or any forward), delete the conntrack entries it
  translated through the executor's `ConntrackDelete`, as the cut of an access rule does; (b) in the three-port topology compile
  `fwd_reply`, rule 925 and table 101 always, so a reply carrying bit 22 never takes the uplink, whatever forwards exist;
  (c) accept and document it.
- Recommendation: (b): static, no conntrack work, and it keeps D11 (connections live on until they end) without the leak. A
  level 1 test: an open management-forwarded connection after the delete sends no reply out of the uplink.
- Needs maintainer: no (decided 2026-10-18)
- Effort: S

#### P31-AUDIT-14 A forward to a device known only from the neighbor table goes inactive once the entry is stale

- Status: closed (2026-10-18): decided — option (a). Before the decision a forwarded connection already marked the device it
  was sent to as in use (the reply's source of a translated connection, `forwardTarget` in `internal/engine/observe.go`;
  test `TestAForwardedConnectionMarksTheDeviceItWasSentToAsInUse`). Now `ResolveIdentity` keeps a stale neighbor entry
  (STALE, DELAY, PROBE) without connections as a lingering claim for a device that an enabled forward sends to
  (`claim.forwardOnly`), unless another device claims the address (`yieldToOthers`: the new holder wins); it is the
  device's least current address, so a newer one is the target. An idle listener known only from the neighbor table stays
  the forward's target; the kernel probes the entry when a forwarded connection comes, and an entry that fails leaves
  the table. The retry loop of `TestADeviceFaultDoesNotImpairAForwardedConnection` is dropped (one measurement framed by
  A's own round trips). Recorded in docs/development.md "Port forwarding (M35a)". Tests:
  `TestAForwardKeepsItsTargetWhileTheDevicesNeighborEntryIsStale` (domain: a stale entry and no connection keeps the
  target; not without an enabled forward; another device's confirmed entry or lease wins; a newer address is the target),
  `TestAStaleNeighborOnlyCountsWhileTheAddressCarriesConnections` (a device no forward sends to, as before), level 1
  `TestAForwardReachesAnIdleDeviceWhoseNeighborEntryIsStale` (in `M35A_LEVEL1`: `base_reachable_time_ms` 500 on the
  bridge, A's connections deleted and its entry stale (STALE or DELAY, with a long first-probe delay), the forward still goes to A and a new forwarded connection
  reaches it).
- Severity: major
- Reason: needs-decision. `ResolveIdentity` keeps a neighbor claim in the kernel states STALE, DELAY or PROBE only while
  the address is an active source; before the fix only a connection's original source counted, which for a forwarded
  connection is the peer, not the device. A device with a static address, identified by its MAC (no lease, no reservation)
  therefore lost its forward (`device_without_address`) about 15–45 s after its last own packet, and the entry cycles through
  DELAY and PROBE even while forwarded traffic flows (forwarded packets give no neighbor confirmation). With the fix, an idle
  listener still loses its forward once its entry is stale, and nothing sends to it any more, so the forward stays inactive
  until the device itself sends.
- Evidence: a scratch test with `forwardWorld` (esp32-42 without `fixed_ip`): a REACHABLE entry gives Active with target
  10.10.0.42; the same entry Stale with `ActiveSources = {203.0.113.10}` gives `{Active:false Reason:device_without_address}`.
  The testbed's device A in `startForwardLab` is this case; the comment and the retry loop of
  `TestADeviceFaultDoesNotImpairAForwardedConnection` ("A's neighbor entry can lapse") hide it.
- Options: (a) keep a stale neighbor claim as a lingering claim for a device that an enabled forward sends to, unless another
  device claims the address (the new holder already wins); (b) document that a forward to a device needs a lease, a
  reservation or `ipv4` identifiers. With (a): a domain test (a stale entry and no connection keeps the forward's target), a
  level 1 test in `M35A_LEVEL1` (a short `base_reachable_time_ms` on the gateway, A's entry STALE with no connection, a new
  forwarded connection still reaches A), and the retry loop of `TestADeviceFaultDoesNotImpairAForwardedConnection` dropped.
- Recommendation: (a): the plan says the forward follows the device's address, and a server that only answers is the common
  target of a forward.
- Needs maintainer: no (decided 2026-10-18)
- Effort: S

### Phase 3.1 audit: checked and not recorded as items

Six further findings of the lenses were checked and rejected (the behavior matched the plan or the spec, or the finding was a
duplicate of an item above); they are not recorded as items.

## Phase 3.1 open items

Phase 3.1 (M35a, port forwarding) is implemented on `main` (PR #68, merged 2026-10-17 as `0fb9176`). The items below were found
while implementing it; each records the option that was implemented so the work was not blocked. All were closed on 2026-10-17:
P31-M35a-05, -09 and -10 by the maintainer's decision, the others by best practice with the maintainer's agreement.

### P31-M35a-01 When does a BGP neighbor sit on a forward's entry side?

- Status: closed (2026-10-17): decided by best practice in the context of the project, with the maintainer's agreement — option (a), as implemented: 179/tcp is refused on both entry sides while an enabled BGP protocol is configured; the safe reading of the plan, and forwarding BGP into a test network is not a use case
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
- Needs maintainer: no (decided 2026-10-17)
- Effort: S

### P31-M35a-02 Forwards overlap whatever their entry side and whether they are enabled

- Status: closed (2026-10-17): decided by best practice in the context of the project, with the maintainer's agreement — option (a), as implemented: one external port has one target, across both entry sides and whether a forward is enabled or not; the list and `explain` stay unambiguous and enabling a forward never fails, as on common routers
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
- Needs maintainer: no (decided 2026-10-17)
- Effort: S

### P31-M35a-03 The UI/API port is refused for udp and on both entry sides

- Status: closed (2026-10-17): decided by best practice in the context of the project, with the maintainer's agreement — option (a), as implemented: the UI/API port is refused for tcp and udp on both entry sides; the plan says "never", and no forward can shadow the control plane in any topology
- Severity: low
- Reason: needs-decision. Plan §5 M35a: "the external port is never the UI/API port". The UI and the API listen on TCP only,
  and in the three-port topology the uplink is not the management interface.
- Evidence: `internal/domain/validate_forward.go`, `TestAForwardNeverTakesTheUIPort`.
- Task: implemented (a) — the UI/API port (`management.ui_port`, else the port the API listens on, else the compiler's default
  443) is refused for tcp and udp on both entry sides. Options: (a) as implemented; (b) refuse it for tcp only; (c) refuse it
  only on the management side and on the two-port topology's shared interface, like 22/tcp.
- Recommendation: (a): the plan says "never", and it keeps a forward from shadowing the control plane whatever the topology.
- Needs maintainer: no (decided 2026-10-17)
- Effort: S

### P31-M35a-04 A connection through a forward that is disabled or deleted lives on until it ends

- Status: closed (2026-10-17): decided by best practice in the context of the project, with the maintainer's agreement — option (a), as implemented: a connection through a forward that is disabled or deleted lives on until it ends, like every other change of the access path (D11); a forward that should cut its connections is paired with a rule with "also cut existing connections"
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
- Needs maintainer: no (decided 2026-10-17)
- Effort: S

### P31-M35a-05 The replies of a forward from the management side go to the management subnet only

- Status: closed (2026-10-17): decided — option (c), together with P31-M35a-10. The replies of a management-side forward
  are routed by their connection, not by the peer's address: the forward's rule in the prerouting NAT chain sets bit 22 of
  the conntrack mark (plan §3.3), the chain `fwd_reply` copies it onto the packet mark of the replies from the test networks,
  and the policy rule `fwmark 0x400000/0x400000 lookup 101` (priority 925) sends them to table 101 (the management subnet,
  the management interface's default gateway, a `prohibit` fallback). A peer behind a router of the management network is
  answered; table 100 has no management route. Tests: `TestAManagementForwardRoutesItsRepliesToTheManagementNetwork`,
  `TestTheReplyMarkOfAManagementForwardIsAFreeRoutingBit`, `TestGoldenForwards`, `TestGoldenForwardNftTransaction`; level 1
  `TestAManagementForwardAnswersAPeerBehindARouterOfTheManagementNetwork` (the testbed's management host is the management
  interface's default router; an address on its loopback outside the management subnet stands for a host behind it),
  `TestForwardsReachTheDeviceFromBothSidesAndFollowItsAddress`. Limitation (found in the review of 2026-10-17, documented):
  table 101 knows the management interface's connected subnet and its default gateway (`Host.DefaultGateway`, the main
  table's default route on that interface) only. A peer that the host reaches through another route of the management
  interface, such as an admin network behind a static route via `mgmt0` while the main default goes via the uplink, gets no
  reply to a forwarded connection. When the management interface has no default route at all the compiler warns
  (`forward_replies_subnet_only`: "the replies of management forwards reach only <subnet>";
  `TestAManagementForwardWithoutAManagementDefaultRouteWarnsThatItsRepliesReachTheSubnetOnly`), the spec's `PortForwardEntry`
  says so, and the forward form shows it as the hint of the entry `management`. The text below is the state before the
  decision.
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
- Needs maintainer: no (decided 2026-10-17)
- Effort: M

### P31-M35a-06 A forwarded port range is one DNAT element per port

- Status: closed (2026-10-17): decided by best practice in the context of the project, with the maintainer's agreement — option (a), as implemented: one DNAT element per port, ranges not limited beyond 1-65535; a limit (option (b)) follows only if the measurements on the smallest hardware (H1) show large ranges to be slow
- Severity: low
- Reason: needs-decision. nftables has no expression that shifts a port by an offset (the kernel's shifted port ranges are not
  in nft 1.0.9 or 1.1.6), so a range shifted one to one (8000-8010 to 9000-9010) needs one map element per port.
- Evidence: `internal/compiler/forward.go` (`compileForwards`), `TestTheDNATMapSendsEveryForwardedPortToTheTargetsCurrentAddress`.
- Task: implemented (a) — every forwarded port is one element of the DNAT map; ranges are not limited beyond 1-65535, so a
  forward of all ports is 65535 elements, and a device's address change moves all of them in one element transaction.
  Options: (a) as implemented; (b) limit the length of a range in the validation (e.g. 1024 ports); (c) an unshifted range
  (target port = external port) as one interval element without a port in its value.
- Recommendation: (a), with (b) if large ranges turn out to be slow on the smallest hardware (H1).
- Needs maintainer: no (decided 2026-10-17)
- Effort: S

### P31-M35a-07 The entry address is every address of the entry interface

- Status: closed (2026-10-17): decided by best practice in the context of the project, with the maintainer's agreement — option (a), as implemented: the forward enters on every address of the entry interface, so the kernel follows a new DHCP address at once, without an apply; a second address on the uplink is the operator's own
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
- Needs maintainer: no (decided 2026-10-17)
- Effort: S

### P31-M35a-08 The explain card of the faults screen asks for a device as the source

- Status: closed (2026-10-17): decided by best practice in the context of the project, with the maintainer's agreement — option (a) for M35a, as implemented: a forwarded connection is explained through the API (`GET /explain`, or the generated clients; `chaosctl` gets `explain` with M18, P31-AUDIT-06); the source choice "an address behind the uplink or the management network" in the explain card (option (b)) moves to M26, whose flow view lets a forwarded connection be picked (plan §5 M26 Scope). Built in M26 (2026-10-27): the source choice in the explain card, and `GET /flows` lists a forwarded connection so that it can be picked in the flow view.
- Severity: low
- Reason: needs-decision. Plan §5 M35a: "the preview and `explain` say that faults do not apply to forwarded connections".
  `GET /explain` answers a forwarded connection when `src` is a peer behind the entry side and `dst` the entry side's address
  (step 1). The explain card of the faults screen (M12) takes a device of the lab as the source, so it never asks for such a
  connection; it shows the forward, the access layer `port_forward` and the note when the gateway's answer has them.
- Evidence: `web/src/components/faults/ExplainCard.vue`, `web/e2e/port-forwards.spec.ts` ("explain shows a forwarded
  connection's forward …").
- Task: implemented (a) — the card renders the forward of an answer; a forwarded connection is explained through the API
  (`GET /explain?src=…&dst=…&protocol=…&port=…`, or the generated clients; `chaosctl` gets `explain` with M18). Options: (a) as implemented; (b) a source choice "an address
  behind the uplink or the management network" in the card, whose destination defaults to the entry side's address.
- Recommendation: (b) with the flow view (M26), where a forwarded connection can be picked and explained.
- Needs maintainer: no (decided 2026-10-17)
- Effort: S

### P31-M35a-09 A forward's counter counts what the forward lets through, in hits

- Status: closed (2026-10-17): decided — option (b). A counting rule per forward in the prerouting NAT chain, in front of the
  DNAT rule (in on the entry interface, to an address of it, the protocol and the external ports, the forward's named counter
  and `last`, no verdict), counts the first packet of each connection that arrives through the forward, whatever an access
  rule decides afterwards (a TCP connection whose SYN a rule drops is counted per attempt). The last match comes from the
  same rule's `last`, so the count and the time describe the same packets; the forward's accept rule counts nothing. The
  API's `forwardCounter` reads it, the spec's `PortForwardView.counters` says connections, and the UI shows "N connections"
  (`counterText`, `counterHint`). Tests: `TestEachForwardCountsItsConnectionsInTheNATChainInFrontOfTheDNAT`,
  `TestTheForwardAcceptStandsAfterTheAccessRulesAndBeforeTheMatrix`, `TestGoldenForwards`, `TestGoldenForwardNftTransaction`,
  `TestAForwardCountsItsConnections` (API); level 1 `TestAForwardCountsTheConnectionsAnAllowRuleAcceptsOnTheRealKernel`,
  `TestAnAllowAllRuleDoesNotOpenTheManagementNetworkWhileAManagementForwardWorks`,
  `TestRulesOnTheTargetDecideForwardedConnectionsOnTheRealKernel`; `src/lib/forwards.test.ts` "the rows: …",
  `port-forwards.spec.ts` "port forwards: the uplink and the management card list …", "port forwards: the device detail
  lists …". The text below is the state before the decision.
- Severity: low
- Reason: needs-decision; found in the review of Phase 3.1. Plan §5 M35a: "a counter per forward" and "the counters count the
  forwarded connections". The counter sits on the forward's accept rule in the forward chain. That rule (1) counts every
  packet of a connection not yet established (one per connection, more for a retransmitted SYN or an unanswered UDP packet),
  which is why the UI shows it as hits, and (2) runs after the access rules: the responder form of an access rule (§2.4) ends
  in the rule's own verdict, so a forwarded connection that an `allow` rule selecting the device accepts (e.g. "device X:
  allow tcp", or "network Lab → uplink: allow") is never counted by the forward, which then shows 0 hits and no last match
  while traffic flows.
- Evidence: `internal/compiler/forward.go` (`forwardAccepts`), `internal/compiler/access.go` (the responder form),
  `internal/api` (`forwardCounter`), `web/src/lib/forwards.ts` (`counterText`), `TestAForwardHasTheCounterOfItsAcceptRule` (the state before the decision; the test was
  replaced by `TestEachForwardCountsItsConnectionsInTheNATChainInFrontOfTheDNAT`).
- Task: implemented (a). Options: (a) as implemented: hits of the forward's accept rule, i.e. what the forward itself let
  through; (b) count each forward once per translated connection in the NAT prerouting chain (it sees only the first packet of
  a conntrack entry): a counter-only rule per forward (iif, protocol, external range) in front of the DNAT map rule, so the
  counter means "connections that arrived through the forward", including the ones an access rule then drops or accepts;
  (c) a counter-only rule per forward in front of `jump access_forward` with the accept rule's match, which counts hits of
  every forwarded connection that arrives, whoever accepts it; (d) a ct mark bit set by the DNAT and conntrack accounting.
- Recommendation: (b): it counts connections, as the plan says, is cheap (one rule per forward, first packets only) and is
  independent of the access rules; the UI then says "connections" again. The rule (and the API's `forwardCounter`) move to the
  NAT chain, the accept rule keeps its `last` for the last match or gets it from the same rule.
- Needs maintainer: no (decided 2026-10-17)
- Effort: S

### P31-M35a-10 A management-side forward makes the management subnet routable for the devices (three-port topology)

- Status: closed (2026-10-17): decided — option (c) (with P31-M35a-05 (c)), and the guard of option (b) on top of it
  (decided 2026-10-17 after the review of (c)). Table 100 never has a route to the management subnet; only the replies of a
  management-side forward go back through the management interface, by bit 22 of the conntrack mark, the chain
  `fwd_reply`, the rule `fwmark 0x400000/0x400000 lookup 101` and table 101 (see P31-M35a-05). A device's own new
  connection to the management network follows the uplink default and the forward chain as any other, whether a
  management forward exists or not. Routing alone does not keep it out, though: when table 100 loses its default (the
  uplink loses its address, e.g. an expired DHCP lease: the kernel deletes the routes via the device, and the next compile
  fails with `uplink_no_gateway`) the packet falls past rule 1000 to the main table, which knows the management subnet, and
  a router behind the uplink that routes to the management subnet carries it there through the uplink. So the three-port
  topology has the management guard too, exactly like the two-port one: the chain `mgmt_guard`, jumped to in front of the
  access rules, drops new connections from the test interfaces (`ifs_test`: local test networks and WireGuard networks of
  the role test) whose destination is in `mgmt_src` (whatever interface they leave through), to the management subnet
  when the sources do not cover it, and whatever leaves through the management interface; the one exception is an explicit
  access-matrix entry network → management: allow (a `return` in front of the drops). Replies, those of a management-side
  forward included, are accepted before it. So no access rule, an allow rule without a destination included, carries a
  device into the management network, whatever the routing. `explain` reports the guard as in the two-port topology (a
  drop by the access matrix, no rule). No `prohibit` default is added to table 100 (the PMTU mirror tables copy its routes
  with an MTU, and the executor refuses an MTU on a route that is not unicast). Tests of the guard:
  `TestThreePortTopologyGuardsTheManagementNetworkByDestinationAndInterface`,
  `TestAnExplicitMatrixEntryReturnsFromTheThreePortGuard`, the golden scenario `access-threeport`,
  `TestNoAccessRuleOpensTheManagementNetworkBehindTheUplink` (domain, both topologies),
  `TestExplainNamesTheManagementGuardOfTheThreePortTopology`; level 1
  `TestWithoutTheUplinkDefaultAnAllowAllRuleStillDoesNotOpenTheManagementNetwork` and
  `TestAMatrixEntryOpensTheManagementNetworkBehindAManagementInterfaceOfItsOwn`. The two-port topology is unchanged
  (`management` is refused there, and its guard's output is the same). The executor's scope covers the new objects as they are
  (table 101 lies in Chaos Gateway's tables 100-110, the route's device is the assigned management interface). Tests:
  `TestAManagementForwardRoutesItsRepliesToTheManagementNetwork` (no management subnet in table 100; the mark, the restore,
  the rule and table 101; none of it without a management forward), `TestGoldenForwards`, `TestGoldenForwardNftTransaction`;
  level 1 `TestAnAllowAllRuleDoesNotOpenTheManagementNetworkWhileAManagementForwardWorks`,
  `TestAManagementForwardAnswersAPeerBehindARouterOfTheManagementNetwork`. The text below is the state before the decision.
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
- Needs maintainer: no (decided 2026-10-17)
- Effort: M

## Issue #64 open items

Found while implementing issue #64 on the branch `close-phase-3-1` (merged with PR #69 on 2026-10-18, `4ec8987`). Each item records the option that was implemented so the
work was not blocked. The maintainer decided I64-01 to I64-03 on 2026-10-18; every item is closed.

### I64-01 Detecting a lost Kea packet socket directly

- Status: closed (2026-10-18): decided — option (a), as implemented: the known cause is prevented (Kea's socket retries,
  the engine configures Kea again when an interface of it comes up, is created again or flapped), with no direct
  detection; no code change. (b) stays the diagnostic to add should a lost socket ever be seen with another cause.
- Severity: low
- Reason: needs-decision. Issue #64 item 3 asks to detect a lost socket. The cause found is that Kea opens its packet socket on
  an interface only while it is configured and skips one that is down or has no carrier; a configuration that arrives while
  the test port flaps leaves the bridge without a socket. That case is now prevented, not detected: Kea retries the sockets it
  could not open for a minute (`kea.SocketRetries`), and the engine configures Kea again whenever one of its interfaces comes
  up, is created again or had its carrier go and come back since the last reading or the send of the configuration
  (`Engine.followDHCPLinks` and, for an apply that creates the bridge again, `Engine.syncDHCP`; the kernel's
  `carrier_changes`, `DHCP.Reopen`). A socket lost in another way (Kea closing it
  after an error of its own) would still go unnoticed.
- Evidence: `internal/kea/config.go`, `internal/engine/dhcp.go`, `TestDHCPAnswersAgainAfterTheTestPortFlappedDuringAnApply`,
  docs/development.md "Kea's packet sockets (issue #64)".
- Task: implemented (a). Options: (a) as implemented: prevent the known cause, no direct detection; (b) read Kea's
  `status-get` in the engine's 5 s DHCP loop and report `sockets` other than `ready` in `GET /system/health` (Kea's view: it
  names the interfaces it skipped, but also a network whose test port is simply unplugged, which is not a fault); (c) a new
  executor read of `/proc/net/packet` in the gateway's namespace, and configure Kea again when a running interface of Kea has
  no packet socket bound to its index (direct, but another executor operation, and it cannot tell Kea's socket from a
  capture's).
- Recommendation: (a) until a lost socket is seen with another cause; (b) as a diagnostic in the health detail if it is.
- Needs maintainer: no (decided 2026-10-18)
- Effort: S ((b)), M ((c))

### I64-02 The wizard's preflight sees only the systemd-networkd files under /etc

- Status: closed (2026-10-18): decided — option (b) for `/usr/lib/systemd/network` only. `deploy/compose.executor.yaml`
  mounts the host's `/usr/lib/systemd/network` read-only at `/host/usr/lib/systemd/network`, where the preflight reads it
  below the executor's host root (`--host-root /host`, `preflight.NetworkdDirs`, no code change in the preflight); the
  directory is part of systemd's package, so the bind mount always has its source. `/run/systemd/network` is not
  mounted (it exists only while networkd or netplan has written to it; the host setup's check reads it). The mount is not
  nested in another (`/host/etc` and `/host/usr/lib/systemd/network`), so the CI job `mounts` is unchanged. The fix text of
  an unreadable OS configuration names both mounts (`internal/api/system.go`). Recorded in plan §3.8 (the `exec` row),
  docs/development.md (the host setup and the preflight). Tests: `TestExecutorContainerHardeningProfile` (eight mounts, the
  new one read-only, no other part of `/usr` and nothing of `/run`, and its target is a directory of
  `preflight.NetworkdDirs` below the host root), `TestNoContainerMountsAVolumeInsideAnother`,
  `TestThePreflightNamesANetworkdDefaultUnderUsrLib` (API: `GET /setup` names
  `/usr/lib/systemd/network/99-default-ether.network` for a test interface, with the host setup's rule as the fix).
- Severity: low
- Reason: needs-decision. Issue #64 item 2: a distribution's default `.network` file (dracut's
  `/run/systemd/network/zzzz-dracut-default.network` on a Raspberry Pi) ran DHCP on the test port. The preflight now reads
  systemd-networkd's files (`internal/preflight/networkd.go`) from every directory networkd reads, but the executor mounts
  only the host's `/etc` (`deploy/compose.executor.yaml`), so the setup wizard names a file in `/etc/systemd/network` and
  not one in `/run/systemd/network` or `/usr/lib/systemd/network`. The host setup's check (`host-setup.sh --check`, run on
  the host before the wizard, docs/install.md step 2) reads all of them, and its rule in `/etc` keeps networkd off the port
  whatever the defaults are.
- Evidence: `deploy/compose.executor.yaml` (the `/host/etc` mount and why it is the whole `/etc`),
  `TestThePreflightNamesTheNetworkdFileOfATestInterfaceBeforeTheSetup`, `TestHostSetupChecksATestInterfaceAndKeepsNetworkdOffIt`.
- Task: implemented (a). Options: (a) as implemented: the wizard sees `/etc`, the host setup's check sees everything; (b)
  mount the host's `/run/systemd/network` and `/usr/lib/systemd/network` read-only at `/host/run/...` and `/host/usr/lib/...`
  (the preflight then reads them without a code change; but a bind mount needs its source, and `/run/systemd/network`
  exists only while systemd-networkd or netplan has written to it, so the executor would fail to start on a host without
  it unless `create_host_path` creates it on the host); (c) mount the host's whole `/run` and `/usr` read-only (always
  there, but much more of the host in the privileged container, including the sockets under `/run`).
- Recommendation: (a); (b) for `/usr/lib/systemd/network` only, if a default there is ever seen to take a test port.
- Needs maintainer: no (decided 2026-10-18)
- Effort: S

### I64-03 The executor writing the systemd-networkd rule of a test interface itself

- Status: closed (2026-10-18): decided — option (a), as implemented: the host setup writes and removes the rule
  (`host-setup.sh --test-interface`, `--remove-test-interface`), its check and the wizard's preflight report a networkd
  file that configures the port; the executor's hardening profile stays (its `/etc` read-only); no code change.
- Severity: low
- Reason: needs-decision. Issue #64 proposes that the executor writes `10-chaosgw-<interface>.network` when an interface is
  assigned to a test network and removes it on release. The executor sees the host's `/etc` read-only, so this changes its
  hardening profile (`compose.executor.yaml`). Implemented instead: the host setup, which runs as root on the host, writes
  the rule (`host-setup.sh --test-interface IF`) and removes it (`--remove-test-interface IF`). Neither tool reports a
  missing rule as such: the host setup's check (every networkd directory) and the wizard's preflight (only `/etc`, see
  I64-02) report a systemd-networkd file that configures the port, which the rule is the fix for (docs/install.md step 2).
- Evidence: `deploy/host-setup.sh`, `TestHostSetupChecksATestInterfaceAndKeepsNetworkdOffIt`; docs/open-items.md "Shutdown and
  host cleanup" (the rule was a host change made by hand).
- Task: implemented (a). Options: (a) as implemented: the host setup writes and removes the rule, its check and the
  preflight report a networkd file that configures the port; (b) the executor writes and removes it on assignment and release (mount `/etc/systemd/network` writable,
  a new executor operation scoped to `10-chaosgw-*.network`, a `networkctl reload` from the container through D-Bus or
  `nsenter`, and the removal joins M27's stop cleanup and `teardown`); (c) like (b), but the executor only writes it in the
  setup and never removes it.
- Recommendation: (a): the rule is a host change made once per port, like the netplan entry the operator removes; (b)
  only if interfaces are reassigned often.
- Needs maintainer: no (decided 2026-10-18)
- Effort: M ((b))

### I64-04 The plan names netplan and NetworkManager only, not systemd-networkd

- Status: closed: fixed on the branch `close-phase-3-1` (merged with PR #69 on 2026-10-18)
- Severity: low
- Reason: plan-error (outdated). Plan §3.4 says that the preflight checks "no OS configuration (netplan/NetworkManager)" and
  that test interfaces "must not be configured by netplan/NetworkManager"; §3.8 says that the executor reads where netplan and
  NetworkManager configure an interface and that the host setup prints the netplan changes. Since issue #64 the preflight
  also reads systemd-networkd's `.network` files (`internal/preflight/networkd.go`, `api/openapi.yaml`: "netplan,
  NetworkManager or systemd-networkd"), and the host setup writes and removes the systemd-networkd rule of a test interface
  and checks it (`host-setup.sh --test-interface`, `--check`, `--remove-test-interface`). The code extends the plan; it
  does not contradict it.
- Evidence: commit `3185482`, `internal/preflight/networkd.go`, `deploy/host-setup.sh`, docs/install.md step 2.
- Task: §3.4 and §3.8 say "netplan, NetworkManager or systemd-networkd", and §3.8 names the host setup's systemd-networkd
  rule and check of the test interfaces (done).
- Needs maintainer: no
- Effort: S

### I64-05 The tests against the real Kea did not run in any CI job

- Status: closed (2026-10-18): every test of `internal/kea/kea_test.go` that calls `startKea`, `TestTheClientDrivesARealKea`
  and `TestLeasesArePagedPastTheLimit`, moved with `startKea` into `internal/kea/kea_testbed_test.go` (build tag
  `testbed`), and both testbed jobs name them with `-expect-pass $KEA_LEVEL1` (`.github/workflows/ci.yml`), so a run in
  which one skips or is not built fails. Before the move both passed against the real Kea as root in the devcontainer
  (`go test -tags testbed ./internal/kea -count=1 -v`, kea-dhcp4 of the devcontainer; no fix was needed).
  `TestKeaAcceptsTheRenderedConfiguration` stays untagged: it needs `kea-dhcp4` but no root, and runs `kea-dhcp4 -t`
  only. Completed by the review of the decisions (2026-10-18): `$KEA_LEVEL1` also names it and the engine's
  `TestTheConfigurationIsSentAgainToAKeaThatRestarted` (root and Kea): both sit in packages with tagged tests, so the
  testbed runner builds and runs them, and a skip fails the job; both passed locally as root
  (`go test -tags testbed ./internal/kea ./internal/engine -run '...' -count=1 -v`).
- Severity: low
- Reason: found when the CI of the branch `close-phase-3-1` failed (run 39084143071): `TestKeaRetriesASocketItCouldNotOpen`
  was expected to pass in the testbed jobs but sat in an untagged file, so the testbed runner, which builds only packages
  with `testbed` tests, never ran it. It now carries the build tag (`internal/kea/kea_testbed_test.go`, commit `c834fd1`) and
  passed in the level-1 job of run 39087710677. The older tests of `internal/kea/kea_test.go` that start the real Kea
  (`startKea`) are untagged too: they skip at level 0 (no root, no Kea on the CI runner) and are not built at level 1, so they
  have run only by hand since M6a.
- Evidence: `internal/kea/kea_test.go` (`startKea` skips without `kea-dhcp4` or root), `internal/kea/kea_testbed_test.go`.
- Task: move every test that calls `startKea` into the tagged file and add them to the testbed jobs' expected tests, so the
  runner fails a run in which one skips; first check that each passes on the real Kea (they have not run in CI).
- Needs maintainer: no (a test-coverage fix, no behavior change; closed 2026-10-18 without a decision)
- Effort: S

## Phase 4 audit result

Date: 2026-10-19. Audited on the branch `worktree-phase-4` at `89d1090` (PR #71, everything since `main`), against `docs/plan.md`
§5 Phase 4 (M15 to M19) and the sections it names (§2.1.1, §2.10, §2.11, §2.15 to §2.17, §3.x, the D-decisions),
`api/openapi.yaml`, `docs/development.md` (the sections for M15 to M19) and the earlier audits in this file. The audit read the
branch through ten lenses: M15 (runs and scenarios), M16 (checks and zero hits), M17 (captures), M18 (`chaosctl` and the example
suites), M19 (the scenario UI), security, concurrency and shutdown, regressions to code that existed before Phase 4, spec, docs and
CI, and test quality (including about 25 mutation experiments on a scratch copy). The audit was read-only: it built the code, ran
`go vet` and single-package tests, reproduced some findings in scratch copies outside the repository, and changed nothing. The level 1
tests on the real kernel were not run here; CI ran them (run 39253445201, green). Every finding was checked a second time by two
independent verifiers who tried to refute it; where they disagreed on the severity, the lower or the better-argued one is recorded.

**Phase 4 is implemented and its tests pass, but it is not yet clean.** The audit found no blocker. It found 13 items of major
severity and 46 of minor severity (59 items after merging duplicates, 6 further findings checked and rejected). The core scheduling
of runs held up under every lens. The major items are in the edges: the cleanup, shutdown and refusal paths of runs
(P4-AUDIT-06, -07), three places where a check or a scope reads the wrong set of connections, names or addresses (P4-AUDIT-02 to
-05), a crash of the privileged executor after a failed ring rotation (P4-AUDIT-01), two losses of captures (P4-AUDIT-08, -09), and
the scenario list that reads a loading or failed query as "Never run" (P4-AUDIT-12). Three items need the maintainer (P4-AUDIT-14 to
-16, with their options below). **Result of the round after the audit (2026-10-19):** all 59 items are closed on the branch
`worktree-phase-4` (merged to `main` with PR #71 on 2026-10-20, `9ab7a24`; CI run 39384159501 green): 56 are fixed with a test that fails without the fix or with the corrected document,
three were fixed differently from the proposal (P4-AUDIT-06, -29 and -41, see the Fix column), none was rejected as wrong after
all (one detail of the proposal for P4-AUDIT-43 was wrong and is corrected), and none is not done. The maintainer decided P4-AUDIT-14 to -16 on 2026-10-19 (option (a) each); the
two choices taken by best practice are P4-AUDIT-47 (`chaosctl explain` was built) and P4-AUDIT-10 (`settings.retention.runs` is
the authority for run retention, `--run-retention` is the default for a configuration without it). The Fix column starts with the
outcome and the commit; the audit's own proposal follows as *Audit proposal*. Not run in the round (no KVM, no netns): the level 1
tests, among them the ones the round added or changed for P4-AUDIT-15, -16, -51 and -52 (they are vetted with `-tags testbed` and
are for CI to run), and a new CI run on the final commit is not known. What stays open as a remark is under "Remaining points" below.

| Verdict | Items |
|---|---|
| Fixed on the branch `worktree-phase-4` (a test that fails without the fix, or the corrected document) | 56 items: P4-AUDIT-01 to -59 except -06, -29 and -41, among them the three decided by the maintainer (-14, -15, -16) and the two taken by best practice (-47, -10) |
| Fixed differently from the audit's proposal | P4-AUDIT-06 (`CheckApplicable` before the abort instead of a hook in `startApply`, which would deadlock the owner loop), -29 (a `disk_low` end with an error stays `stopped`), -41 (the web option: `download` attribute, no `Content-Disposition` headers) |
| Rejected as wrong after all | none (a detail of P4-AUDIT-43 was wrong: `window_end` of a window of `within` is set when it opens, as the planned end; the text follows the code) |
| Not done | none (optional parts of -23, -49 and -50 were not done, see the Fix column) |
| Checked and rejected during the audit | 6 findings |

| Item | Severity | Finding | Fix |
|---|---|---|---|
| P4-AUDIT-01 | major | A failed rotation of a ring-buffer capture leaves `Output` without a file. `closeCurrent` sets `o.f` and `o.w` to nil even when the flush or close fails; if `openNext` then fails (ENOSPC, EDQUOT, EMFILE, EIO, a file that exists), `rotate` returns the error but `o.w` stays nil. The reader (`internal/capture/session.go`, `read`) only calls `s.fail` and goes on reading up to the drain limit (65536 packets), and the next `Write` evaluates `o.w.Size()` (`internal/capture/output.go:121-161`, `format.go:220`) on a nil pointer. Neither `read` nor `control` has a `recover`, so the privileged executor process exits. Reproduced in a scratch copy: with `capture-00002.pcapng` pre-created the first `Write` fails with "file exists" and the second panics. No test covers a failed rotation. | **Fixed (28ad9a0):** `Output` stores the first rotation error and returns it from every later `Write`, `Flush` and `Close` (no nil writer); the `read` and `control` goroutines recover a panic and end only that capture (`ReasonError`). Test: `TestAFailedRotationIsStickyAndLeavesNoNilWriter` (a nil-pointer panic without the fix), `TestAPanicInAGoroutineOfASessionEndsOnlyThatSession`, `TestAFailedRotationEndsTheCaptureWithAnErrorAndNotTheProcess` (testbed). *Audit proposal:* Make a failed rotation sticky: store the error in `Output` and return it from every later `Write` (check `o.w == nil` at its top). Add a `recover` to the `read` and `control` goroutines that ends only that capture through `s.fail`. Test: `openNext` fails (a pre-created next file name or a small quota) and a second `Write` follows. |
| P4-AUDIT-02 | major | A failed read of the baseline arms the check. In `runChecks` (`internal/run/checks.go:340-372`) `c.armed = true` is set for every opened check at the end of the iteration whether or not `Observer.Flows` succeeded. If the read at the iteration where the window opens fails (executor timeout, a slow conntrack dump during the apply of the opening step), the baseline exemption (`startedSince`) is skipped, and the next successful poll feeds every connection to the check unfiltered. With a cold tracker each connection that existed before the window is "created" and, if established, "established". In the outage scenario of §2.10 a device's ESTABLISHED `tcp/8883` connection from before the cut makes `reconnected from restore` pass at once, `no_connection` fail on pre-window connections, and `traffic_seen`/`traffic_not_seen` decide on them. `endWindow` reports an error only when the read fails at the poll where the window closes, so nothing marks the lost baseline; no test fails the first read after a window opens. | **Fixed (667ac8c):** A check is armed only when the read of the connections worked, so the baseline exemption (`startedSince`) still applies to the first successful poll after a failed read where the window opened. Test: `TestAFailedReadWhereTheWindowOpensDoesNotArmTheCheck` (internal/run). *Audit proposal:* Set `c.armed` only when `flowErr == nil` (while the baseline is outstanding keep applying `startedSince` to the first successful poll). Level 0 test: fail the first `Flows` read after the window opens, then return a connection that began before `wstartWall`, and expect the check still pending. |
| P4-AUDIT-03 | major | `dns_query_seen` and `wait for dns_query` scan the DNS log as if it were sorted by time, but it is not. `runObserver.DNSQuerySeen` and `DNSQueries` (`internal/api/runs.go:105-130`) walk from the newest entry and `break` at the first entry whose `Time` is before `since` ("the log is oldest first"). The proxy stamps an entry with the start of the query but adds it only after the answer (`internal/dnsproxy/proxy.go:162,174`, up to three times the upstream timeout later), and `PostDnsQueries` (`internal/api/dns.go:193`) appends in arrival order. One slow query that began before the window and was logged late hides every entry before it, including a matching query inside the window; after a cut that also blocks the upstream, `dns_query_seen` false-fails and the wait step misses the query. `ListDnsQueries` uses `continue` and is correct. | **Fixed (ed94ff0):** `DNSQuerySeen` and `DNSQueries` scan the whole DNS log (`break` replaced by `continue`). Test: `TestASlowQueryLoggedLastDoesNotHideAQueryInTheWindow` (internal/api). *Audit proposal:* Replace `break` with `continue` in both functions (the log is bounded to 20000 entries, a full scan every 250 ms is cheap) or keep the slice sorted on insert. Test: the log `[in-window match, older non-match]` finds the match. |
| P4-AUDIT-04 | major | The `uplink` and `network` destinations of checks use a narrower address set than the spec and the rest of the gateway. `networkPrefixes` and `allLocalPrefixes` (`internal/run/scope.go:253-296`) take only `snap.Bridges` and the interface address of `snap.WireGuardInterfaces`. `domain.World.NetworkPrefixes` and `NonUplinkPrefixes` (`internal/domain/access_selector.go:100-114`), which the M17 capture selector and the compiler use, also hold the networks behind a hub's clients and a link's routes, the LAN's downstream routes and the management prefixes (`api/openapi.yaml`, `Destination.uplink`). So `traffic_not_seen {destination: {uplink: {}}}` fails on a flow to a network behind a WireGuard client (say `192.168.50.0/24`) that the rules treat as not uplink, `except: [{network: <wg network>}]` does not cover the networks behind that network's clients, and traffic to an allowed management address counts as uplink; `no_connection`, `reconnected` and `wait: connected` are wrong in the same way (callers `checks.go:253,263`, `exec.go:629`). | **Fixed (667ac8c):** The `network` and `uplink` destinations of checks and of `wait: connected` use `domain.World` (`NetworkPrefixes`, `NonUplinkPrefixes`, built once per snapshot); a check that needs them when `NewWorld` fails ends as `error`, `wait:` reports the cause. The old `networkPrefixes`/`allLocalPrefixes` are removed. Test: `TestAConnectionMatchesAProtocolPortsAndDestination` (hub client network, downstream route, management prefix, broken world), `TestTheUplinkAndNetworkDestinationsOfACheckAreTheOnesOfTheRules`. The error path of a world that cannot be built is pinned only for `matchFlow`, not for the check and the `wait` message (found in review). *Audit proposal:* Build the sets once per poll from `domain.NewWorld(snap.Config, snap.Overlays)`: `w.NetworkPrefixes(id)` for `network`, "not in `w.NonUplinkPrefixes()`" for `uplink`; handle the error of `NewWorld` (refuse the check). Tests with a WireGuard client route and a management prefix. |
| P4-AUDIT-05 | major | A configured device without a declared network is outside every network target. `scopeDevices` and `scopeTokens` (`internal/run/scope.go:93-125`) select the devices of a network target by `DeviceState.Network`, which for a configured device is only the optional config field `network` (`internal/engine/devices.go:330`; the spec calls it "informational", the shipped example device `lab-host` has none). `explain` (`internal/engine/explain.go:187-194`) and the API already fall back to `world.NetworkOf`. Consequences: a run with target `{network: IoT}` and a second run with target `{device: lab-host}` do not overlap and start in parallel, against "only one run may be active per target" (§2.10); `no_foreign_overlays` does not see a foreign overlay on that device for a network-target run; `target_online` fails with "no device of the network IoT is online" when `lab-host` is the only device there; checks and `wait` steps miss it. The only scope test sets `Network` explicitly. | **Fixed (ec40472):** The engine resolves a device's network once in `tracker.step` (declared network, else the network of its DHCP lease, else `world.NetworkOf` of its address), so scope, `target_online`, `no_foreign_overlays`, checks, `wait`, the capture manager and `explain` agree. Fixed at the source, so the test is in the engine, not in the run's fake gateway. Test: `TestAConfiguredDeviceWithoutANetworkIsInTheNetworkOfItsLeaseOrAddress` (internal/engine). *Audit proposal:* Resolve a device's network once, when the engine builds `DeviceState`: the declared network, else the network of its DHCP lease (`Lease.Network`), else `world.NetworkOf` of its address, so all consumers agree. Level 0 test: a configured device without `network`, a network-target run and a device-target run, the second queued. |
| P4-AUDIT-06 | major | `POST /revisions/{id}/apply?force=true` aborts the runs before the engine has checked that the apply can happen. `ApplyRevision` (`internal/api/revisions.go:376-390`) calls `s.runs.AbortReferences`, which ends the runs and removes their overlays, and only then `Engine.Apply`; `startApply` (`internal/engine/owner.go:552-571`) is the first place that refuses with `confirm_pending`, not-a-candidate and `revision_conflict` (base is not the active revision), and `Preview` checks none of them. With revision 7 pending confirmation, or a stale candidate that therefore "deletes" the objects added since its base, a forced apply aborts every run that uses them and then answers 409 with nothing applied; the same holds when the apply later fails. Before Phase 4 a refused forced apply changed nothing. A non-forced call with a pending confirmation answers `validation_failed` with run references instead of `confirm_pending`. The only forced-apply test (`runs_test.go:630`) covers the success path. | **Fixed differently (cbcb460):** `ApplyRevision` asks the new `Engine.CheckApplicable` (confirm pending, not a candidate, base is not active; the function `startApply` uses) before the run references and the abort, so a refused apply, forced or not, answers `confirm_pending` etc. and leaves the runs alone. Not the preferred hook in `startApply`: the owner loop cannot wait for runs to remove their overlays (they go through the owner), so it would deadlock. A kernel failure after the abort still leaves the runs aborted (documented in `development.md`). Test: `TestARefusedForcedApplyLeavesTheRunsAlone` (api), `TestCheckApplicableNamesWhyAnApplyWouldBeRefused` (engine). *Audit proposal:* Abort only when the apply will start: give `ApplyOptions` a hook that `startApply` calls after the three checks and `revisionOverlays` and before the commit (preferred), or run `Store.PendingConfirm`, the candidate status and `base == ActiveID` in the API before the abort. Tests: a forced apply with a pending confirmation and with a stale candidate leave the runs running. |
| P4-AUDIT-07 | major | A failed cleanup of the run's overlays is reported only for runs that would end `passed` or `failed`. `Manager.finish` (`internal/run/exec.go:404-440`) tries `ResetOverlays` three times and turns a failure into `error` only for those two statuses (line 434); for `aborted` (abort, lease, reset, forced apply, shutdown) and `error` it only logs, `Abort.Generation` stays 0 and `Abort` answers 200. Then `releaseAll` forgets the overlays and nothing retries; the overlays carry no TTL or lease, and after a failed apply `settleOverlays` restores the store, so they stay under owner `run:<id>` and come back with the next successful apply. With the executor down while a client aborts a run, the run is recorded as cleanly aborted, the impairment returns when the executor does, and every later run on the target fails `no_foreign_overlays` naming an ended run, until `POST /reset` or a restart. Plan §2.10: "Either way, its overlays are removed". Only a run that would have passed is tested (`steps_test.go:292`). | **Fixed (1e303c9):** A failed overlay removal is part of the end for every status (`passed`/`failed` become `error`; `aborted`/`error` append the cause to the reason); `Abort` answers `apply_failed` instead of a clean 200; the manager retries every 10 s until it works, the record is pruned or it closes. Remaining: `POST /reset` still gives no error answer for a run whose cleanup failed (the retry removes the overlays later). Test: `TestARunWhoseOverlaysCannotBeRemovedSaysSoWhateverItsEnd`, `TestAnAbortWhoseOverlaysStayIsAnApplyFailure`. *Audit proposal:* Treat a cleanup failure as part of the end whatever the status: append "the overlays of the run could not be removed: ..." to the reason or add a run warning, and never answer an abort as a clean 200 with generation 0. Keep a manager-owned retry (for example every 10 s by owner until it succeeds, the record is pruned or the manager closes), or let `schedule()` and the `no_foreign_overlays` precondition remove leftover overlays of ended runs. Tests with `resetErr` for abort, lease and error endings. |
| P4-AUDIT-08 | major | `Start` of a capture deletes the oldest stopped captures before it knows the new capture can start. `makeRoom(quota, need)` (`internal/capturemgr/manager.go:362`) removes stopped captures and their files until the request fits or none are left; after that `Start` can still return `ErrQuota` (a ring buffer larger than what the running captures leave, `avail` below 64 KiB, lines 364-370) or fail at `Mkdir`, `OpenCapture` (`disk_low`, `out_of_scope`, unavailable, interface gone) or `firstEvent`, and nothing restores what was deleted. With a 10 GiB quota, 8 GiB used by running captures and two stopped ones (one of them the evidence of a run within its 30 days), a ring buffer of 4 x 1 GiB deletes both and answers 422 `capacity_exceeded`; a client that retries a failing request with a new `Idempotency-Key` per attempt keeps erasing captures. This contradicts decision 8 ("do not destroy the evidence of a run to make room for nothing"); `development.md` says only that the request is refused. | **Fixed (3506eb5):** `Start` works out which stopped captures the quota needs gone (`roomLocked`) and refuses with `ErrQuota` first, but deletes them only after the executor accepted the capture; the `max_size` clamp uses the same preview. `development.md` describes the order. A concurrent `Start` could still pick the same room (the old `makeRoom` had the same window; `enforceQuota` catches an overshoot). Test: `TestARefusedCaptureDeletesNoStoppedCapture`. *Audit proposal:* Decide before deleting: refuse with `ErrQuota` first when the running usage plus `need` exceeds the quota; call `makeRoom` only after the executor accepted the capture (`OpenCapture` and `firstEvent`), then re-check. Test: a refused ring request and one the executor refuses leave every stopped capture in place. Document the order in `development.md`. |
| P4-AUDIT-09 | major | Ring-buffer files from number 100000 on are invisible to download, size and recovery. The ring names its files `capture-%05d` (`internal/capture/output.go:82`), so file 100000 is `capture-100000.pcapng`; the manager lists and sums with `^capture(-[0-9]{5})?\.(pcap\|pcapng)$` (`internal/capturemgr/manager.go:695`, used by `packetFiles`, `joinFiles`, `dirSize`), which does not match six digits, and `sort.Strings` would order them before the five-digit ones. The executor deletes the old files normally, so after file 99999 the directory holds only files the manager ignores: `GET /captures/{id}/download` answers 200 with an empty body while `size_bytes` and `packets` go on counting, and `Recover` sizes a failed capture as 0. It takes about 28 hours at 1 MB/s with 1 MiB files, or 6.4 GB with the minimum file size of 64 KiB. | **Fixed (a62586f):** `packetFile` matches `[0-9]{5,}` and `packetFiles` orders by file number, so files from 100000 on are listed, summed, downloaded and recovered. Test: `TestTheFilesOfARingBeyondNumber99999AreDownloadedInOrderAndCounted`. *Audit proposal:* Widen the pattern to `[0-9]{5,}` and order the files numerically (by length, then lexicographically), or use a width that cannot overflow (`%010d`). Test with a sequence above 99999. |
| P4-AUDIT-10 | major | `settings.retention.runs` is validated but never used. The spec (`RetentionSettings.runs`, default 720h), the validator (`internal/domain/validate_network.go:35`) and plan §3.6 ("configurable") have it, but the run manager takes its retention only from the `--run-retention` flag (`cmd/chaosgw/api.go:211`, `internal/api/runs.go:47`, default 30 days), used in `Recover` and `prune`. Setting `2160h` to keep reports for 90 days is accepted and records are deleted at 30 days anyway; `24h` is ignored the same way. `captures_quota` is read from the active configuration (`captureQuota()`); `docs/install.md` does not mention the flag. (`retention.events` and `retention.audit` are equally unused, which predates Phase 4.) | **Fixed (211afc7):** `settings.retention.runs` is read each time (`run.Config.RetentionOf`, `Server.runRetention`, like `captureQuota`) and is the authority; `--run-retention` stays as the default for a configuration without the setting (not deprecated; flag help, `development.md` and `install.md` say so). `settings.retention.events` and `.audit` stay unused (they predate Phase 4). Test: `TestTheRetentionOfRunsIsTheSettingOfTheActiveConfiguration` (its own race with the prune fixed in 73dc67a). *Audit proposal:* Make the manager's retention a function that reads the active snapshot's `settings.retention.runs`, falling back to the flag and then to 30 days, as `captureQuota` does. API test with a short retention; document the flag and the setting in `install.md`. If the setting should stay unused, remove it from spec and validation and say so in plan §3.6. |
| P4-AUDIT-11 | major | `CHAOSGW_INSECURE=1` in the environment silently overrides an explicit `--fingerprint` or `--ca-cert`. `client()` (`internal/ctl/ctl.go:187-196`) copies the variable into `c.insecure` whenever the flag is not set and `newClient` (`internal/ctl/client.go:88-120`) switches on `insecure` first, then fingerprint, then CA, whatever the source; `--insecure=false` cannot be told from the default. `CHAOSGW_INSECURE=1 chaosctl --fingerprint <wrong> reset` connects and sends the bearer token to an unverified peer (without the variable it exits 2 "not the pinned one"), and `CHAOSGW_FINGERPRINT=<wrong> chaosctl --ca-cert c.pem` ignores the CA flag. This contradicts "flags win over the environment". The table test uses the environment alone or flags alone. (Plain `http://` URLs are accepted on purpose and tested; that part is not a defect.) | **Fixed (425342e):** A trust flag on the command line (`--insecure`, `--fingerprint`, `--ca-cert`) replaces the three `CHAOSGW_*` trust variables as a group; `--insecure` next to a pin or CA file from the same source is a usage error (exit 2), including mixes of environment variables only (stricter than the audit's minimum); `--insecure` warns on stderr for https URLs. Help, `install.md` and `development.md` updated. Test: `TestTheTrustFlagsWinOverTheTrustVariablesAndInsecureNeverMixesWithAPin` (15 mixes, no request reaches an unverified peer). *Audit proposal:* Decide by source: if any trust flag was set on the command line (`cmd.Flags().Changed`), ignore the trust environment variables; make `--insecure` together with a pin or CA a usage error (exit 2) and print a warning on stderr when verification is off. Table-test cases for flag-versus-environment mixes. |
| P4-AUDIT-12 | major | The scenario list and the Runs tab read a loading or failed runs query as an answer, and a failed fetch of a followed run reads as "not there any more". `allRuns` is `runs.data.value ?? []` and the page gate (`web/src/views/ScenariosView.vue:264`) ignores the runs query, so while `GET /runs` loads or after it failed every scenario shows "Never run" (:287) and the Runs tab "has not been run yet" (:394); `runGone` (:138) is true for any error of `GET /runs/{id}` without data, not only 404, and then states "This run is not there any more (the gateway keeps the runs of 30 days)" with "Back to the scenario" (:350). `RunView` does this right (404 against `LoadError`). This is the rule of §2.17 that the Phase 3 audit fixed; none of the 18 Playwright tests makes `/runs` fail or be slow. | **Fixed (fcc92b8):** The scenario list and the Runs tab show Loading / Last result unknown with Try again instead of Never run; `runGone` needs an `ApiError` 404, any other failure shows `LoadError` with a retry. Test: e2e (`scenarios.spec.ts`): the runs are slow, the runs failed, a run that cannot be read is not gone. *Audit proposal:* Show "Loading..." while `runs.isLoading` and "Last result unknown" with Try again when `runs.error` has no data, in the list and the Runs tab; make `runGone` require an `ApiError` with status 404 and show `LoadError` with retry otherwise. Playwright tests with `/runs` and `/runs/{id}` failing with 500 and slow. |
| P4-AUDIT-13 | major | Five API tests assert the exact list of run warnings on a real-clock engine, so a `step_late` makes them fail. `internal/api/runs_captures_test.go:76,170,204,220` and `runs_checks_test.go:250` check `len(codes) == 0/1`, but the engine uses the real clock (only the API and run scheduler use the fake one), and `step_late` fires whenever the first apply of a step waits more than 100 ms (`internal/run/exec.go:207`), which under `-race` (`make test`) it regularly does. Reproduced twice with `go test -race -count=25` of the five tests: 2 of 125 runs failed on a quiet host, 15 of 125 on a loaded one (e.g. `warnings [capture_missing, step_late 'step slow waited 107 ms ...']`). The level 0 job can go red with nothing broken. | **Fixed (a9feff7):** `warningCodes` drops `step_late`, a new `warningsOf(run, code)` replaces the `[0]` indexing, the zero-hit test counts only `zero_hit`; the five tests ran 12 times with `-race`, green. Test: the five tests in internal/api (`TestARunWithCaptureTrueAttaches...`, `TestARunWhoseCaptureCannotStart...`, `TestARunGoesOnWhenTheExecutorRefuses...`, the `disk_low` test, the zero-hit test). *Audit proposal:* Assert the warning under test, not the whole list: let `warningCodes` (`runs_captures_test.go:33`) drop `step_late`, and count only `zero_hit` in `runs_checks_test.go:249`. `step_late` stays covered deterministically by the fake-clock tests in `internal/run` and `TestAStepBehindARevisionApplyWaitsAndTheRunSaysSo`. |
| P4-AUDIT-14 | minor | `step_late`: plan §2.10 (rewritten on 2026-10-19) says "A step that starts more than 100 ms late gets the run warning `step_late`" and that a step due while the apply of the one before is still running "is handed over late"; §3.11 says the same. The code warns only when the engine-reported queue wait exceeds 100 ms (`internal/run/exec.go:207`), as `docs/development.md` decision 4, `api/openapi.yaml` (`queue_wait_ms`, ~5305) and the M15 test line say. The hand-over lateness (`start_offset_ms` minus the scheduled offset plus the wait shift) is never compared with the threshold, and no test covers it. Steps at 0 s and 1 s with a 3 s apply: the second is handed over 2 s late, queue wait about 0, no warning (`development.md` records steps 3.4, 4.4 and 6.3 s late with 3 ms queue wait on the CI VM). Needs a decision, see below. | **Fixed (decided: option a) (d563f5a):** A step handed over more than 100 ms after its due time (schedule plus wait shift) gets the run warning `step_late`, one per step, naming the cause (previous step still applying, timeline woke late, counter read, executor queue); also for `wait`, `capture` and a skipped `remove`. Plan §2.10/§3.11, the spec (`queue_wait_ms`, `start_offset_ms`, `RunWarning`) and `development.md` (decision 4, queue wait, test table) updated. Test: `TestAStepHandedOverMoreThan100msLateGetsTheWarningStepLate`, `TestAStepHandedOver100msLateIsNotLate`, `TestAStepLateForBothReasonsHasOneWarning`, `TestAStepTheTimelineReachesLateSaysSo`. *Audit proposal:* See the maintainer item: either warn also on a late hand-over (a second trigger in `applied` and the skip, remove and wait paths, one warning per step, fake-clock test with `putDelay`) or reword plan §2.10 and §3.11. |
| P4-AUDIT-15 | minor | The traffic checks (`traffic_seen`, `traffic_not_seen`, `reconnected`, `no_connection`) read conntrack only (`internal/run/checks.go:115-116,340`), so a packet dropped in the forward path, which leaves no conntrack entry, is invisible to them. `development.md` says so for the port-forward counters (~4324) but the M16 section and the `Check`, `traffic_seen` and `traffic_not_seen` texts of the spec do not. `traffic_not_seen {destination: broker}` with a `cut` that drops `tcp/8883` passes for a device that hammers the broker, and `traffic_seen` of the same destination during the outage always fails. No level 1 test runs the two checks while a drop is in force. Needs a decision, see below. | **Fixed (decided: option a) (7cdc264 (test, ci), df78ecf (docs, spec)):** The limit (checks observe conntrack; packets dropped in the forward path leave no entry) is documented in the spec (`Check`, `traffic_seen`, `TrafficNotSeenCheck`), plan §2.10 and `development.md` M16 (decision 9), and pinned by a level 1 test (drop rule and a retrying device: `traffic_not_seen` and `no_connection` pass, `traffic_seen` fails, the rule counter shows the dropped packets); the test is in `M16_LEVEL1`. Test: `TestTheTrafficChecksDoNotSeeWhatTheGatewayDropsOnTheRealKernel` (level 1; vetted with `-tags testbed`, not run here; assumes a dropped SYN leaves no conntrack entry). *Audit proposal:* See the maintainer item (recommended: document the limit in the spec and `development.md` and pin it with a level 1 test). |
| P4-AUDIT-16 | minor | A capture scope can name a WireGuard network of the role `management`, while `global` deliberately excludes it. `global()` (`internal/capturemgr/resolve.go:296-326`) skips interfaces with `Role == "management"`; `network()`, `device()` (a client or device on such a network), group and selector sources and the `capture: true` and capture steps of a run (`captureScopeOf`, `internal/run/captures.go:43-62`) go through `interfacesOf` (:97-135), which has no role filter. A token with scope `overlays` can record and a token with `read` can download the raw packets of the admin tunnel. The documents do not forbid it (plan §2.11 says "network, device or selector"; the `uplink` scope already records the management plane in the two-port topology), so it is a design question rather than a spec violation. No test has a management-role interface. Needs a decision, see below. | **Fixed (decided: option a) (a97d387):** `interfacesOf`, the one place network, device, group and selector sources go through, refuses a management-role WireGuard interface with code `management_network` at the scope path, as `global()` does. A run with such a target starts no capture and gets `capture_missing` (it is not refused at create: the run goes on, decision 11). `global()` comment, `development.md`, `openapi.yaml` and plan §2.11 updated. Test: `TestNoScopeResolvesToAWireGuardNetworkOfTheRoleManagement`, `TestACaptureRequestThatDoesNotFitIsRefusedWithItsPointer` (admin cases), `TestARunWhoseTargetIsAManagementNetworkIsNotRecorded`. *Audit proposal:* See the maintainer item. |
| P4-AUDIT-17 | minor | The hand-over moment of a replace, remove or restore step is stamped before a counter read that can delay it by up to 2 s. `timeline` calls `m.began` (`internal/run/exec.go:105`), then `overlayStep` (replacing an alive overlay), `removeStep` and `restoreStep` call `captureCounters(r, stepCounterTimeout)` (:249, 269, 284), a synchronous executor read of all sets and counters bounded at 2 s, before the engine call. `start_offset_ms`, the windows of positive checks (opened at `began`) and `step_late` (from the engine's `QueueWait`, which starts later) do not contain the read: it shows up only as "apply time" (`offset_ms - start_offset_ms`), so `stepsOnTime` (`e2e_runs_test.go:28`) passes with a `restore` that reached the engine up to 2 s late. The comment on `stepCounterTimeout` admits the read is on the timeline, `development.md` does not say it hides in the report. | **Fixed (d563f5a):** `began` is stamped immediately before the engine call, after the counter read of `replace`, `remove` and `restore`, so `start_offset_ms`, check windows and `step_late` contain the read; a captures test that jumped the fake clock over a step was corrected. The level 1 test `stepsOnTime` was not changed (not run here). Test: `TestTheReadOfCountersBeforeAStepIsPartOfItsHandOver` (restore, remove, replace). *Audit proposal:* Stamp `began` immediately before the engine call, after `captureCounters` (a helper in the three step functions), or add the read time to the step's queue wait, or take the read off the critical path. Test with a slow `Counters` fake that checks `start_offset_ms`. |
| P4-AUDIT-18 | minor | The scenario UI shows the apply time as the lateness of a step. `lateness()` (`web/src/lib/runs.ts:97-101`) is `offset_ms` (apply verified) minus the scheduled offset, printed as `at 13.4 s (+3400 ms)` (`ScenarioTimeline.vue:56,119`); the UI never reads `start_offset_ms`, although the decision of 2026-10-19 (plan §2.10, `RunStep`) makes the hand-over the accuracy of a step. On a host where an apply takes 2 to 5 s every step handed over on time reads as a missed 100 ms promise although no `step_late` was raised, and the hand-over moment is shown nowhere. The vitest and Playwright fixtures (`runs.test.ts:91`, `scenarios.spec.ts:462,548`, `mock.ts`) carry only `offset_ms`, and `development.md:5240` ("against its schedule") enshrines the old meaning. | **Fixed (0ee2c73):** The timeline shows `handed over at X (+n ms), applied at Y (apply Z)` from `start_offset_ms`/`offset_ms` (falling back to `offset_ms`); still suppressed after a wait, and now also for the wait step itself. `development.md` decision 10 and the following-a-run text updated. Test: e2e: a slow apply handed over on time shows both moments; vitest `runs.test.ts` (the timing of a step). *Audit proposal:* Show both moments, "handed over at 10.0 s (+3 ms), applied at 13.4 s (apply 3.4 s)": the figure from `start_offset_ms` minus the scheduled offset (fall back to `offset_ms` when absent), the apply time from `offset_ms - start_offset_ms`; keep suppressing the figure after a wait step. Add `start_offset_ms` to the mock and the tests (a slow apply with an exact hand-over); update `development.md`. |
| P4-AUDIT-19 | minor | A connection that appears after the window closed can still decide a check. The closing step ends a negative check's window at its hand-over; the loop calls `closeWindow` and then reads the connections in the same iteration, and `feed` never looks at `wendWall` (only `lookDNS` does, `checks.go:489`), so with a slow read (seconds in the nested VMs) a connection the restored path let through after the hand-over fails `no_connection`; fixed-length windows have the same effect at `wend`. Mirror: events a poll consumed for other checks before a window opens are never replayed to a check that opens in the next iteration. Existing level 0 tests add the flow only after the check is decided. | **Fixed (667ac8c):** Once a window has ended, `observes` drops connections that began after its end (all events of such a connection; flows without a start time are kept); the events of the previous successful poll are kept one iteration and given to a newly opened check as far as they began at or after the window start. Test: `TestACheckWhoseWindowHasClosedIgnoresConnectionsThatBeganAfterIt`, `TestAConnectionThatBeganAfterTheWindowClosedDoesNotFailNoConnection`, `TestAConnectionOfThePollBeforeTheOpeningBelongsToTheCheckThatOpens`. *Audit proposal:* After a window has ended, drop created events whose `flow.StartedAt` is after `wendWall` in `feed` (flows without a start time cannot be dated); keep the previous poll's events one iteration and give a newly opened check those with `StartedAt` at or after `wstartWall`. Level 0 test with a flow in the closing iteration that started after the hand-over. |
| P4-AUDIT-20 | minor | A `dns_query_seen` check can fail at window end for a query asked inside the window but not delivered yet. `endWindow` (`internal/run/checks.go:501-512`) looks at the API log once at `wend` and decides; the log arrives after the answer (up to 6 s with a dead upstream), in 500 ms batches (`internal/dnsproxy/querylog.go:20`) plus the post. A query at +29.8 s of a 30 s window, delivered at +30.4 s, is missed, although `development.md` (~4733) says the log's time stamps decide, not the polls. | **Fixed (667ac8c):** The final look of a `dns_query_seen` check is delayed by `dnsSettle` (1 s) after the window closed, still filtering on the window end; `nextWake` follows. Stated in the `DnsQueryCheck` description and `development.md`. Test: `TestADNSQueryOfTheWindowThatIsLoggedAfterItsEndStillCounts`. *Audit proposal:* Delay the final look for `dns_query_seen` by a settle time (a little over the flush interval plus post time, about 1 s), still filtering `q.Time <= wendWall`; state it in the `Check` description. |
| P4-AUDIT-21 | minor | The event recorder of a run drops events instead of replaying them. When the bus drops the recorder (buffer of 256 full), `startRecorder` (`internal/run/events.go:18-40`) subscribes again with plain `Subscribe()`, keeping no `Seq`, and the events in between are lost, although the bus replays (`SubscribeFrom`, `engine.go:352`; the SSE handler uses it) and plan §3.11 says writers of run files never drop records. `run.Gateway` has only `Subscribe`. A burst can lose `run_step`, `overlay_*` or `run_finished` in `events.jsonl`; without `run_finished` `awaitRecorded` waits the full `recordWait` of 5 s, so every abort answer, report and event read of that run is 5 s late. | **Fixed (efa4e4c):** The recorder resubscribes with `SubscribeFrom(last seq)` after a drop (`run.Gateway` gained it) and writes the replay first; it warns with the number of lost events only when the bus no longer holds them. Test: `TestTheRecorderReplaysWhatTheBusDroppedWhileItFellBehind`, `TestTheRecorderSaysWhenTheBusKeptTooLittleToReplay`. *Audit proposal:* Remember the last `Event.Seq`, add `SubscribeFrom` to `run.Gateway` (and its fake), replay after a drop, and warn only when `OldestEventSeq() > lastSeq+1`. |
| P4-AUDIT-22 | minor | A run's capture that someone stops through the API is never reported as missing. `finishCaptures` (`internal/run/captures.go:172`) accepts stop reason `user` as normal for every capture, but it is right only for a capture that the run's own `capture: stop` step stopped; `POST /captures/{id}/stop` has no ownership (decision 10) and `capturemgr.Stop` records `user`. For `capture: true` or a step capture whose stop step has not run, a user stop at 20 s of a 60 s run gives a 20 s artifact with no `capture_missing`, against the spec text of `capture`. No test covers it. | **Fixed (0d9c630):** A user stop is the run's own only when its `capture: stop` step ended the capture; otherwise the run adds `capture_missing` ("stopped by a user before the run ended / before the step X stopped it"). Test: `TestACaptureThatAUserStopsBeforeTheRunEndsIsMissing`, `TestACaptureOfAStepThatAUserStopsBeforeItsStopStepIsMissing`. *Audit proposal:* When the record says `stopped` with reason `user` and the run's `c.running` is still true, add `capture_missing` ("stopped by a user before the run ended"); have `captureStep` check the returned record's reason for a stop that preceded the stop step. Test with the fake `Captures` and a user stop mid-run. |
| P4-AUDIT-23 | minor | The step in flight when a run is aborted is reported as "skipped, not run". The engine goes on after the caller's context ends (`overlayCmd`), so the overlay is stored and applied and `overlay_created` is in the events, but `failStep` (`internal/run/exec.go:221-228`) records nothing when `r.ctx` is cancelled and `failRemaining` (`manager.go:766-777`) turns the still-pending step into "skipped, not run: <reason>" without overlay id, generation or applied time; its counters are not read. The overlay itself is cleaned up. | **Fixed (d563f5a):** A call to the engine that ends because the run was cancelled records the step as `error` ("cancelled while it was being applied: the change may have taken effect") instead of `skipped, not run`; the run still ends `aborted`. The optional attachment of the overlay found by (owner, step) was not done. Test: `TestTheStepInFlightWhenARunIsAbortedSaysSo`. *Audit proposal:* On a cancelled call mark the step "cancelled while it was being applied: the change may have taken effect" (as `error` or with its own text); optionally attach the overlay found by (owner, step). |
| P4-AUDIT-24 | minor | `POST /runs/{id}/renew` answers 200 with a fresh lease for a run that is already being aborted. After the lease timer fired (or an abort began) the run is cancelled but stays `running` until `finish` has read counters, stopped captures and removed overlays (seconds are possible); `Renew` (`internal/run/manager.go:667-691`) rejects only ended runs, so it resets the timer, sets a new `lease_expires_at` and answers 200, and the run ends `aborted` a moment later. | **Fixed (bb42823):** `Renew` refuses a run whose context is cancelled (`ErrEnded`, naming the abort reason) without moving the lease; the API text says ended or being aborted. Test: `TestARunThatIsBeingAbortedCannotBeRenewed`. *Audit proposal:* In `Renew`, reject when `r.ctx.Err() != nil` (`ErrEnded` or an error naming the abort reason) before resetting the timer; test with a renewal after the lease fired. |
| P4-AUDIT-25 | minor | `POST /reset` reports `removed_overlays` without the overlays of the aborted runs, against its own comment. `Reset` (`internal/api/overlays.go:496-510`) aborts the caller's runs first, which removes their overlays, and returns `res.Removed` of the second call only, while the comment says they "count as removed overlays too"; `runs_test.go:545` asserts `removed_overlays == 0` next to `aborted_runs == 1` for a run with a fault. `chaosctl` prints "removed 0 overlays, aborted 1 runs". The spec leaves the field undescribed. | **Fixed (a6024f8):** `Manager.AbortOwned` returns the overlays the aborted runs removed; `Reset` counts them in `removed_overlays` and the audit detail; `ResetResult` described in the spec. Test: `TestAbortOwnedAbortsTheRunsOfOneOwnerOrAll`, `TestRunsFollowTheTokenScopesAndResetStopsTheCallersRuns`. *Audit proposal:* Count them (have `AbortOwned` return the number of overlays its runs removed and add it, also to the audit detail; fix the assertion), or describe in the spec that the field counts only overlays that belong to no run and correct the comment. Recommendation: count them. |
| P4-AUDIT-26 | minor | `ApplyResult.aborted_runs` of the spec is never returned. `applyResultBody` (`internal/api/revisions.go:33-52`) has no such field; `AbortReferences` returns only a count, used for the audit detail (:386). The runs' own overlays are not in `removed_overlays` either, as the runs removed them first. `runs_test.go:630` checks only the status. | **Fixed (cbcb460):** `AbortReferences` returns the ids in creation order; the apply result has `aborted_runs` (omitted when empty), filled by `ApplyRevision`; spec description added, model regenerated. Test: `TestARevisionThatDeletesWhatARunRefersToIsRefusedUnlessForced` (asserts `aborted_runs`). *Audit proposal:* `AbortReferences` returns the ids of the runs it aborted; add `AbortedRuns []model.Uuid` (`aborted_runs,omitempty`) to `applyResultBody`, fill it and assert it in the forced-apply test. |
| P4-AUDIT-27 | minor | `capturemgr.Close` can block forever after its context expired, and `OpenCapture` ignores cancellation. `Close` (`internal/capturemgr/manager.go:251-276`) waits for `c.done` with the context but then calls `m.wg.Wait()` unconditionally; with an executor that is connected but does not answer the stop, `follow` never ends (the stream has no read deadline), so after the 30 s `Server.Close` never reaches `stopTrafficSampler`, `unsubEvents` or `idem.close()` and shutdown depends on the supervisor's SIGKILL. `Client.OpenCapture` (`internal/executor/capture_client.go:33-39`) honors only a context deadline, so a cancelled request context leaves a goroutine and a socket per request against a hung executor. | **Fixed (5ae56da):** `Manager.Close` closes the streams of captures still running once its context has expired, then waits; `Client.OpenCapture` closes its connection from `context.AfterFunc` while the open is in progress and returns the context error. Test: `TestClosingTheManagerEndsTheStreamsOfAnExecutorThatDoesNotAnswerWhenTheContextExpires`, `TestOpeningACaptureEndsWithTheContextOfAnExecutorThatDoesNotAnswer`. *Audit proposal:* After `ctx.Done()` in `Close` call `c.stream.Close()` for every capture still running, then wait; in `OpenCapture` close the connection from `context.AfterFunc(ctx, ...)` while the open is in progress. |
| P4-AUDIT-28 | minor | The ring-buffer size check overflows `int64` and lets a ring of huge files past the quota validation. `ParseByteSize` accepts up to 2^62 bytes (`4194304TiB`) and `internal/capturemgr/manager.go:330` computes `need = int64(rb.Files) * size` unchecked: two files wrap to a negative number, 100 files to 0, so `need > quota`, `makeRoom` and the room check pass and `POST /captures` answers 201 instead of 422 `invalid_capture_limit`. The executor's `CaptureOpen.Validate` (`executor/capture.go:151-154`) checks only `RingFileSize > 0`. The first file never fills; `enforceQuota` stops the capture when the quota is exceeded (about one second of traffic over it) and deletes other stopped captures on the way. | **Fixed (b76643d):** `Start` compares size against quota/files before multiplying and refuses at `/ring_buffer` with `invalid_capture_limit`; `CaptureOpen.Validate` bounds the ring and `max_size` by 2^62 overflow-safely. Test: `TestARingBufferWhoseFilesOverflowTheQuotaCheckIsRefused`, new cases in `TestACaptureIsRefusedOutsideTheScopeAndForBadRequests`. *Audit proposal:* Refuse when `size > quota/int64(files)` before multiplying (error at `/ring_buffer`, `invalid_capture_limit`), and add an overflow-safe bound to `CaptureOpen.Validate`. Test with files 2 and 100 and `4194304TiB`. |
| P4-AUDIT-29 | minor | An error while closing the capture files is dropped unless the stop reason is already `error`. `Session.control`'s defer (`internal/capture/session.go:244-253`) calls `s.end(ReasonError, err)`, a no-op once a reason is set, so `Result` is `{Reason: user/duration/size/run_end, Err: err}`; the executor copies the error into `CaptureEvent.Error`, but `Manager.finish` (`internal/capturemgr/manager.go:538-554`) reads `ev.Error` only for reason `error`. A failed final flush (ENOSPC, EIO) at a user stop or a duration end is recorded as a normal stop without message, the file lacks its tail, and a run lists the artifact without `capture_missing`. | **Fixed differently (0ec0287):** `finish()` treats a non-empty executor error with reason `user`, `duration`, `size` or `run_end` as a failure (status `failed`, reason `error`, message with the error), so a run lists `capture_missing`. A `disk_low` reason with an error stays `stopped`/`disk_low`: a full disk is the documented normal end (decision 9). The unit test feeds the executor event; no test has an `Output` whose `Close` fails (`Session.control` already carries the error into the result). Test: `TestAnErrorWhileClosingTheFilesFailsTheCaptureWhateverStoppedIt`. *Audit proposal:* In `finish` treat a non-empty `ev.Error` with any reason as a failure (status `failed` with the message, or `stopped` with the message filled and a warning). Test with an `Output` whose `Close` fails. |
| P4-AUDIT-30 | minor | `run --artifacts` buffers each artifact, captures included, completely in memory. `fetchTo` (`internal/ctl/runs.go:533-559`) downloads into a `bytes.Buffer` (`download` copies without a limit) and only then writes the `.part` file, so its comment ("a failed download leaves nothing half written") is misleading; the peak is up to twice the size while the buffer grows. A capture of a run is limited to 1 GiB by default (`DefaultCaptureMaxSize`), so an OOM needs a larger explicit `max_size`, a ring buffer or several captures, but a small CI runner can still be killed after the run has passed (exit 137 instead of 0/1/2). The `.part` file and the directory are created with 0644 and 0755; `wg.go:34-46` buffers tiny files only. | **Fixed (8fb68f1):** `fetchTo` streams into `<name>.part` (`O_EXCL`, removed on error) and renames; the file name comes from `Content-Disposition` after the headers; the artifacts directory is 0700, files 0600 (JUnit stays 0644); no `bytes.Buffer`. Comment and `development.md` corrected. Test: `TestAnArtifactIsStreamedToItsFileAndIsPrivate` (in review: against a buffering mutant it hangs until the go test timeout instead of ending in a plain FAIL). *Audit proposal:* Stream: `download` writes into `dir/name.part` with `io.Copy`, rename on success, remove on error; decide the final name from `Content-Disposition` after the headers; consider 0600 and 0700 for artifacts (captures hold raw traffic, the key log of M22 will be a secret). |
| P4-AUDIT-31 | minor | `chaosctl wg export` writes the client's private key with `os.WriteFile(out, ..., 0o600)` (`internal/ctl/wg.go:43`), and the mode applies only when the file is created: an existing `--out` file keeps its mode (0644) and a symlink is followed, although the help and `development.md:5101` promise 0600 (`chaosgw wg export` does it "also over an existing file", `development.md:668`). No test checks the mode. | **Fixed (8fb68f1):** `wg export` writes through `writeFileAtomic` (new `.part` file with 0600, then rename): an existing 0644 file ends 0600, a symlink is replaced instead of followed; non-regular targets such as `/dev/stdout` are written in place. Help and `development.md` updated. Test: `TestWgExportWritesTheKeyWithMode0600OverAnExistingFileAndALink`, `TestAFileIsWrittenInPlaceOnlyWhenItIsNotARegularFile`. *Audit proposal:* Write a temporary file in the same directory with `O_CREATE\|O_EXCL\|O_WRONLY` 0600 and rename it over the target (or `Chmod` 0600 after the write). Test: export over an existing 0644 file ends with 0600. |
| P4-AUDIT-32 | minor | `chaosctl wait device` exits 1 instead of 2 when the timeout runs out during failing polls, with an empty message. `waitDevice` (`internal/ctl/wait.go:131-184`) returns 2 only after 10 failed polls in a row; if `--timeout` is shorter than about ten poll intervals or a poll hangs, the deadline branch returns `ExitFailed` with `missing` still empty ("timed out after 3s: "). Reproduced against an unreachable port with `--timeout 3s`: exit 1. A CI script that treats 2 as an infrastructure error misreads a dead gateway as a device failure. The existing test covers only the ten-failures path. | **Fixed (aa64440):** `waitDevice` keeps the last poll error: when the time runs out it exits 2 ("timed out after X, and the gateway could not be asked: <err>") if the latest poll failed or none was answered, else 1 with the missing text; no poll starts after the wait has ended; a poll cut off by the deadline counts as failed only after 1 s in flight. Test: `TestWaitDeviceThatRunsOutOfTimeWithoutAnAnswerFromTheGatewayExits2`. *Audit proposal:* Keep the last poll error; in the deadline branch return exit 2 ("timed out after X, and the gateway could not be asked: <err>") when the latest poll failed or none succeeded, exit 1 with the missing text otherwise. |
| P4-AUDIT-33 | minor | Signals are subscribed too late and SIGHUP is not handled. `signal.Notify` covers SIGINT and SIGTERM only (`internal/ctl/ctl.go:208-215`); in `run --wait` it happens in `await` (`runs.go:212`) after `POST /runs` returned, so a SIGTERM during the POST kills `chaosctl` with 143 (not 2) and the run is neither aborted nor reported, and a SIGHUP (ssh gone, some CI agents) kills `with` and `run --wait` without the cleanup the plan promises. The lease is the net (30 s by default); with `--lease 0` and no `--ttl`, `with` leaves a permanent fault. (`with` subscribes before it creates anything; only SIGHUP is missing there.) | **Fixed (aa64440):** SIGINT, SIGTERM and SIGHUP are subscribed at the start of `run`, `wait run`, `wait device` and `with`, before the first request; a signal during `POST /runs` lets the request finish (5 s grace) and then aborts the run, a signal before the run exists is exit 2; `with` passes SIGHUP on to the command. Help and `development.md` updated. Test: `TestARunIsAbortedWhenAHangUpArrivesWhileItIsBeingCreated`, `TestWithCleansUpOnAHangUp`. *Audit proposal:* Subscribe to SIGINT, SIGTERM and SIGHUP once, before the first request, and hand the channel to `await` and `runWith`; a signal before the run exists maps to exit 2, and the run is aborted when the POST already succeeded. |
| P4-AUDIT-34 | minor | Polls ignore `--timeout` and signals while a gateway hangs. The poll inside `await` and `waitDevice` runs on `cmd.Context()` with the 2 minute request timeout (`internal/ctl/client.go`, `call`); the signal, deadline and `ctx.Done()` cases are read only in the `select` between polls (`runs.go:285`, `wait.go:132`). A gateway that accepts the connection but does not answer lets `--timeout 60s` overrun by up to two minutes per poll, and the abort of an owned run does not start until the poll returns, so a SIGKILL 30 s after a SIGTERM comes first. | **Fixed (aa64440):** `watch()` gives one context that ends with the signal, the `--timeout` or the parent; every poll and renewal runs under it with its own 10 s timeout (`Env.PollTimeout`), so a hanging gateway neither stretches `--timeout` nor delays the abort. Create and export calls keep the 2 minutes. Test: `TestAPollThatHangsIsCutOffAndTheWaitGoesOn`, `TestTheTimeoutAbortsARunWhileThePollIsHanging`. *Audit proposal:* Derive each poll's context from the signal, the deadline and `ctx.Done()` (or use a per-poll timeout of a few poll intervals, say 10 s, for polls and renewals); keep the 2 minutes for create and export calls. |
| P4-AUDIT-35 | minor | Two flag mappings of `chaosctl` are wrong. `destinationOf` (`internal/ctl/overlays.go:243-251`) treats any `--to` value with `.`, `/` or `:` as a CIDR, so `--to mqtt.example.com` is sent as `{cidr: "mqtt.example.com/32"}` (a confusing validation error; the flag cannot express the `hostname` destination of the spec, and a bare IPv6 address gets `/32`). And in `with` the default lease `30s` is applied to every request after the `--overlay` files were read (`with.go:90-104`, `overlays.go:85-92`), so a `lease: 5m` in an overlay file is replaced and `--lease 0` does not remove a file's lease. | **Fixed (aa64440):** `destinationOf` classifies with `netip` (a bare address becomes /32 or /128, a hostname with a dot or `*.suffix` becomes `hostname`, other names are networks; `network:<name>` and `hostname:<name>` disambiguate; unparsable address text goes to the gateway as a cidr for its validation error). In `with` the default lease is set only on requests without a lease key, an explicit `--lease` replaces a file's lease, `--lease 0` deletes it, each overlay is renewed at a third of its own lease; a negative `--lease` is refused. The renewal rate per overlay is not pinned by a test (found in review). Test: `TestTheFlagsOfAFaultBecomeItsBody` (destination table), `TestWithKeepsTheLeaseOfAnOverlayFileAndAnExplicitLeaseReplacesIt`. *Audit proposal:* Classify `--to` with `netip.ParseAddr` and `ParsePrefix` (bare address gets /32 or /128 by family) and send a valid hostname as `{hostname: ...}` (refused as `unsupported_feature` until M20); set the default lease only on requests without a `lease` key and let an explicit `--lease 0` delete it. |
| P4-AUDIT-36 | minor | The step and check editors offer choices, and miss checks, that the gateway always refuses; the refusal comes only at Preview and apply for the whole candidate. Missing in `StepDialog.vue:145,158`, `stepForm.ts` and `scenarios.ts` (`scenarioProblems`): `target_widened` ("Acts on" lists every device, group and network), `remove` of a step that creates no overlay (all earlier steps are listed), the reserved step id `start`, `capture: stop` without an earlier start of the same target, `tunnel_outside_target` (all tunnels are listed), action `reset` with a protocol other than tcp (the check exists in `rules.ts:162` but the step dialog does not use it), and a wait timeout or `within` of `0s`. `development.md` says the checks mirror `ValidateScenario`'s structural rules. | **Fixed (fcdf197):** New `lib/scenarioRules.ts` holds the seven rules (target widened, `remove` of a non-overlay step, reserved id `start`, capture stop without start, tunnel outside the target, `reset` needs tcp via the shared `resetError`, zero wait timeout or window) used by `stepFormErrors`, `checkFormErrors` and `scenarioProblems`; Acts on, tunnels and removable are filtered; topology from the loaded config, lenient where unknown. Test: vitest `scenarioRules.test.ts` (cases mirror `validate_scenario_test.go` with the same pointers); e2e: the step editor offers only the targets inside the scenario, the rules the gateway always refuses are shown under the fields. *Audit proposal:* Filter "Acts on" and the tunnels to the scenario target and list only earlier overlay-creating steps in `removable`; add the seven rules to `stepFormErrors`, `scenarioProblems` and `checkFormErrors`, reusing the `rules.ts` check, with unit tests against the Go validator's expectations. |
| P4-AUDIT-37 | minor | Focus management and tab semantics of the scenario screen do not meet §2.17. `refocusAfterSwap` (`ScenariosView.vue:302,306`) returns at once without a `data-focus-group` ancestor and the header with Delete scenario and Undo has none, so after Delete scenario then Undo focus falls to `<body>`, as after Delete step (`ScenarioTimeline.vue`) and Delete check (`ChecksCard.vue`), where the focused button leaves the DOM; the other screens were fixed in Phase 3. The Timeline/YAML/Runs bar (:339) has `role=tablist` and `role=tab` but no tabpanel, `aria-controls`, roving tabindex or arrow keys. | **Fixed (fdc9f7e):** `data-focus-group` on the header actions; `focusAfterUpdate` after deleting a step or a check (next/previous Edit or Add); Timeline/YAML/Runs are Reka Tabs with tabpanels, `aria-controls`, roving tabindex and arrow keys. Test: e2e: the focus stays on the page when a scenario, a step or a check is deleted; the Timeline, YAML and Runs tabs are tabs with their panels. *Audit proposal:* Add `data-focus-group` to the header actions and call `focusAfterUpdate` after removing a step or check; use Reka's Tabs or implement the tab pattern completely; Playwright focus assertion. |
| P4-AUDIT-38 | minor | The check result badge uses 11 px text, outside the §2.17 type scale (12/13/14/16/20-26). `web/src/components/scenarios/ChecksCard.vue:71,76` (`text-[11px]`) are the only 11 px text in `web/src`; P3-AUDIT-16 converted exactly these badges to 12 px. No test guards the scale. | **Fixed (950764f):** Check badges are `text-xs`; a new vitest scans all app sources (Tailwind named and arbitrary sizes, `font-size` declarations) against the 12/13/14/16/20-26 scale. Test: `src/typeScale.test.ts` (fails with `text-[11px]`). *Audit proposal:* `text-xs` on both; optionally a vitest that scans the `.vue` sources for arbitrary text sizes outside the scale. |
| P4-AUDIT-39 | minor | The run lists are windows (the newest 200 on Scenarios, the newest 50 on the overview) but are presented as complete. `useRuns` (`web/src/composables/gateway.ts:121-127`) fetches one page and ignores the next cursor; the `scenario` and `status` filters of the API are unused. After 200 newer runs (every `chaosctl run` of an inline scenario is one, kept 30 days) a stored scenario's older runs read "Never run" and "has not been run yet"; on the overview a queued run behind more than 50 newer ones is not shown (`RunningScenarios.vue:13`). | **Fixed (01dfaa2):** `useRuns` returns the page; the overview and the followed run ask for `status=running`/`queued`, the Runs tab uses `scenario=<id>` and `scenario=<name>` with 50 runs and Show older runs, scenarios outside the newest 200 get their last result with `scenario=<id>&limit=1`. Remaining (found in review): the name fallback request takes its result without the inline-only check, so a scenario without its own run in the newest 200 can show the last result of another scenario with the same name. Test: e2e: overview finds a queued run behind more than 50 newer runs; last result older than the newest 200 runs; Runs tab pages the runs of the scenario; vitest `runs.test.ts` (`isRunOf`, `mergeRuns`). *Audit proposal:* Runs tab: query with `scenario=<id>` (and page; keep the inline-by-name matching); overview: `status=running` and `status=queued`; last result: one request per visible scenario with limit 1, or say "older than the latest 200 runs" when the page has a next cursor. |
| P4-AUDIT-40 | minor | A run of a deleted scenario ends in a dead end, and the hint about it is untrue. `Orphaned()` lists only runs whose snapshot refers to a deleted device, network or profile (`internal/run/manager.go:701`, snapshot independence, §2.10), never a run of the deleted scenario itself, so the sentence "Runs that are queued or running are listed by the preview, which asks first" (`ScenariosView.vue:325`) is wrong for a scenario deletion; the run keeps going. The overview row and the run page link to `/scenarios/<id>?run=...` (`RunningScenarios.vue:30`, `RunView.vue:74`), which says "There is no scenario <id>" (:421) without a link to `/runs/<id>`. | **Fixed (3afc981):** The deletion hint is corrected (the run keeps going on its copy); the overview links to `/runs/<id>`, the run page hides Open the scenario when the scenario is not among the loaded ones; the not-found scenario card links to `/runs/<id>` when `?run=` is set. Test: e2e: a run of a deleted scenario is reached by its own page; deleting a scenario says that its runs go on. *Audit proposal:* Correct the sentence (the run keeps going on its snapshot); link to `/runs/<id>` when the scenario id is not among the loaded scenarios, hide "Open the scenario" then, and let the not-found card link to `/runs/<id>` when `?run=` is set. |
| P4-AUDIT-41 | minor | Run artifacts are downloaded under names that cannot be told apart. `ArtifactsCard.vue:28` uses a bare `download`; the events URL `/api/v1/runs/<id>/events` has no extension and `GetRunEvents` and the report handlers send no `Content-Disposition` (other download handlers do), so the browser saves `events`, `report.json` and `report.xml` for every run; `artifactLabels[...].file` (`web/src/lib/runs.ts:115`) holds the intended names and is read nowhere. | **Fixed differently (f74139b):** The web option: the artifact link sets `download` to `run-<id8>-<file>` (from `artifactLabels`). No `Content-Disposition` headers were added in the three Go handlers, so `curl -OJ` and `chaosctl` keep the bare names. Test: e2e: the artifacts of a run are saved under the name of the kind and the run; vitest `runs.test.ts` (`artifactFileName`). *Audit proposal:* Send `Content-Disposition: attachment; filename="run-<id8>-events.jsonl"` (and for the reports) in the three handlers, which also helps `curl -OJ` and `chaosctl`; or set `:download` from the label; drop `file` if unused. |
| P4-AUDIT-42 | minor | A pending `wait` step and every step after a wait are counted against the unshifted schedule. `stepMarks` (`web/src/lib/runs.ts:75-89`) takes the first pending step and compares `scheduled_offset` (the static `at`, `manager.go:263`) with the elapsed time, but a wait shifts the later steps (§2.10, `exec.go:93-116`). After a wait that took 20 s the next step at 30 s shows "next in 5 s" or "applying..." although the engine runs it 20 s later; `lateness()` special-cases waits, `stepMarks` does not (and the same function feeds `RunningScenarioRow`). The wait row itself showing "applying..." is by design, but its wording says nothing about the condition. | **Fixed (0ee2c73):** `stepMarks` adds the shift of finished waits (largest `offset_ms` minus the scheduled offset, as in `exec.go`) to the countdown; a pending wait whose time has come says it waits for the condition (timeout 60 s). Test: vitest `runs.test.ts` (the next step after and at a wait); e2e: a pending wait says what it waits for. *Audit proposal:* For a pending `wait` show "waiting for the condition (timeout 60 s)"; show no countdown ("after the wait") for steps after a wait that is not finished (or add the wait's `offset_ms` shift). Vitest cases. |
| P4-AUDIT-43 | minor | The spec text for check windows is out of date against the implemented two-moment windows. `api/openapi.yaml:3781-3787` (`Check`) and 3841, 3847 (`CheckWindow.from`, `until`) say the window opens when the step "has been applied", closes when `until` "has been applied" and that an observation counts "if the poll that sees it lies inside it"; `CheckResult.window_start` and `window_end` (:5327) are undescribed. The code (decided 2026-10-19, `development.md`) opens a positive window at the hand-over of `from` and closes a negative one at the hand-over of `until`, places DNS observations by the log's time stamp and the baseline poll by conntrack `StartedAt`. | **Fixed (3b64e60 (earlier texts), ddaf21a (`window_end`)):** One detail of the proposal was wrong: `checks.go` sets `window_end` when a window of `within` opens (the planned end) and `checks_test.go` asserts it; only an `until` window has none while open. `CheckResult.window_end` is worded after the code, the Go model regenerated; the other texts are as proposed. Test: corrected document (`make check-spec`, `make check-generated`, `make check-clients`). *Audit proposal:* Rewrite the three texts: positive windows start at the hand-over of `from` and end at the answer of `until`; negative windows start at the answer of `from` and end at the hand-over of `until`; `window_start` and `window_end` are the wall times of those moments; describe the placement of DNS and of the first poll. `make generate`, `make check-spec`. |
| P4-AUDIT-44 | minor | Spec and plan text drift in M17. The operation text of `startCapture` (`api/openapi.yaml` ~1772) says "Without `max_size`, `duration` or `ring_buffer` the capture stops at 1 GiB", but the code (`manager.go:335-345`) and `TestTheDefaultLimitAndTheDurationReachTheExecutor` apply the 1 GiB limit whenever `max_size` is absent, also with a `duration` (`CaptureRequest` says "1 GiB when absent"). Plan §3.1 (:735 "capture processes", :749 "libpcap processes started by the executor") and §2.11 (:489 "AF_PACKET (libpcap)") still describe the old design; M17 is plain Go in the executor (plan :496, :1471, `development.md:4828`). | **Fixed (3b64e60 (spec), ca4ace2 (plan)):** `startCapture` says 1 GiB when `max_size` is absent, also with a duration, a ring buffer bounded by its files; plan §2.11 and §3.1 describe the capture engine as plain Go AF_PACKET in the executor. Test: corrected document (`make check-spec`). *Audit proposal:* The operation text: "1 GiB when `max_size` is absent, also with a `duration`; a ring buffer is bounded by its files"; plan §3.1 and §2.11: "capture engine in the executor (AF_PACKET, plain Go)". |
| P4-AUDIT-45 | minor | Details of the Phase 4 operations are undeclared in the spec where the code behaves specifically. `POST /captures` answers 201 with a `Location` header (`internal/api/captures.go:191`), but the `startCapture` 201 declares none (`createRun` and `runScenario` do); `getRunReportJson` and `getRunReportJunit` answer 404 `not_found` for a run that has not ended (`ErrNotEnded`, decision 7, `problem.go:205`) without saying so (`getRunEvents` does not: it returns the events written so far); `renewRun` has no description (own runs only for scope `overlays`; 422 `validation_failed` for a run without a lease or one that has ended); `createRun` can answer 422 `capacity_exceeded` (`ErrTooManyRuns`, 200 queued or running) and the text names only 409 `target_busy`. | **Fixed (3b64e60 (spec), 0551c24 (decision 7)):** `Location` header on the `startCapture` 201; descriptions for `renewRun`, the two report operations (404 for a run not ended), `getRunEvents` (events so far) and 422 `capacity_exceeded` for `createRun`; `development.md` decision 7 corrected. Test: corrected document (`make check-spec`). *Audit proposal:* Add the `Location` header to the `startCapture` 201 and one sentence each to the two report operations, `renewRun` and `createRun` (and, for `getRunEvents`, that it returns the events so far while the run goes on). No code change. |
| P4-AUDIT-46 | minor | Status lines contradict the closed item and do not cite the green CI run. `docs/plan.md:5` still ends "open: P4-M19-01", which this file, plan :1456 and `development.md:5196` call closed on 2026-10-19; the M15 to M18 status lines say the level 1 tests "were not run here ... run by CI" without naming CI run 39253445201 (green), as the Phase 3.1 status cites its run; `README.md` ends "then Phase 4 (automation)" although the plan says M15 to M19 are done on the branch; the M17 line (plan :1471) says "the ten level 1 tests that need only CAP_NET_RAW" while `M17_LEVEL1` has 31 names (14 in `internal/capture`, 10 in `internal/executor`, 7 in the API). | **Fixed (ca4ace2):** `plan.md` first line and Phase 4 status no longer say "open: P4-M19-01" and cite the green CI run 39253445201; the M15 to M18 level 1 sentences cite it; `README.md` says Phase 4 is implemented on the branch, PR open. The wrong "ten" of the M17 line was dropped, not replaced (it cannot be recounted without privileges). Test: corrected document. *Audit proposal:* Remove "open: P4-M19-01" from the first line, add "CI run 39253445201 green" to the level 1 sentences, correct or drop "ten", and say in `README.md` that Phase 4 is implemented on the branch. |
| P4-AUDIT-47 | minor | `chaosctl explain` was promised for M18 by closed items and was neither built nor re-planned. P31-AUDIT-06 and P31-M35a-08 (this file) say "`chaosctl` gets `explain` with M18"; `chaosctl help` has no such command, plan M18 and `development.md` do not list or drop it, and the row "Stubs: `chaosgw tls`, `chaosctl` beyond `version`" under "Covered later" (M21, M18) is stale although `chaosctl` is a full client. | **Fixed (built) (097ff16):** `chaosctl explain` built (`--device` or `--src`, `--dst`, `--protocol`, `--port`, `--json`): a thin `GET /explain` printing verdict, winners with overridden candidates, forward/service and route; usage errors exit 2 without a request. Plan M18 scope, `development.md` and `install.md` updated; the stale stub row under "Covered later" is struck by the same round. Test: `TestExplainAsksTheGatewayAboutOneTrafficTuple`, `TestChaosctlOverlays/ExplainsTheTrafficOfADevice` (real engine, `--json` equals the API answer). *Audit proposal:* Add `chaosctl explain --src/--device --dst --protocol --port` (a thin `GET /explain`, with `--json`) or write in `development.md` (M18) and in the three items that it was not built and why; strike the `chaosctl` part of the stub row and keep `chaosgw tls` (M21). |
| P4-AUDIT-48 | minor | `docs/development.md` names a test that does not exist, omits a validation code, and has a wrong count. The M15 acceptance map (:4667, row "Scope: stored scenarios") names `TestAStoredScenarioNeedsAllItsMilestones`; the test is `TestAStoredScenarioThatCapturesNeedsACaptureVolume` (`internal/run/steps_test.go:351`) after a rename (it is the only missing name of the 227 in the M15 to M19 maps). `CodeInvalidParameter` (`invalid_parameter`, `internal/domain/scenario_run.go:22`, run parameters at `/parameters/<path>`) is in none of the docs although the table claims to list every `Code*` constant; `TestEveryValidationCodeIsDocumented` (`review_fixes_test.go:433`) checks a hand-written list that has neither it nor `CodeCaptureLimit`. The M19 section says "321 tests in 32 files" (:5289); vitest reports 322. | **Fixed (0551c24):** The M15 map names `TestAStoredScenarioThatCapturesNeedsACaptureVolume`; the `invalid_parameter` row is in the validation-code table; `TestEveryValidationCodeIsDocumented` reads the `Code*` constants with `go/parser`. The proposed test count change no longer applied. Test: `TestEveryValidationCodeIsDocumented`. *Audit proposal:* Replace the test name (or add the test), add an `invalid_parameter` row to the table, write 322; make `TestEveryValidationCodeIsDocumented` read the `Code*` constants of the package with `go/parser` instead of a list. |
| P4-AUDIT-49 | minor | The capture volume is missing from the CI fresh-volume check and the upgrade note. The check of issue #57 (`.github/workflows/ci.yml:251`, "every named volume it mounts writable") loops over state, secrets, api, service and the Kea events directory but not `/var/lib/chaosgw/captures`, which the API writes and `deploy/Dockerfile:63-64` gives to 65532; a volume left root-owned would fail every `POST /captures` and every `capture: true` run on a fresh host unnoticed. `docs/install.md:259` (`for v in state secrets api service`) lacks `captures`, plan §3.8 does not list the writable volume in the `exec` and `api` rows (§3.x :912 does), and `nightly.yml` runs the testbed packages without `-expect-pass`, so a Phase 4 level 1 test that skips on one kernel stays green (as for M35a). | **Fixed (8e727a6):** `/var/lib/chaosgw/captures` is in the CI fresh-volume loop and the `install.md` upgrade loop; plan §3.8 names the writable volumes; the `M*_LEVEL1` lists live in `.github/level1-tests.env`, are loaded in both CI testbed jobs and passed as `-expect-pass` by `nightly.yml` (a legitimate skip on the older nightly kernel will fail the first night). The optional level 2 capture smoke was not done. Test: `TestTheFreshVolumeCheckNamesEveryVolumeTheAPIWrites`, `TestTheUpgradeNoteChownsEveryVolumeTheAPIWrites`, `TestEveryTestbedRunNamesTheLevel1Lists`. *Audit proposal:* Add `/var/lib/chaosgw/captures` to the CI loop and `captures` to the `install.md` loop; add the volume to the plan §3.8 rows; pass the `M*_LEVEL1` lists as `-expect-pass` in `nightly.yml` (a shared list); optionally let the level 2 smoke start a capture of a few seconds. |
| P4-AUDIT-50 | minor | The example README tells Jest users `NODE_EXTRA_CA_CERTS` only, but the Jest tests spawn `chaosctl` (`clients/examples/jest/helpers.ts:41-43`), which ignores it and needs `CHAOSGW_CA_CERT`, `CHAOSGW_FINGERPRINT` or `CHAOSGW_INSECURE` (`ctl.go:190-197`): against a self-signed gateway the API tests pass and the three `chaosctl` tests fail with "x509: certificate signed by unknown authority" and no hint. `conftest.py:50` accepts `CHAOSGW_INSECURE` as 1/true only, `chaosctl` also yes. `TestTheExampleSuitesPassAgainstTheGateway` (`internal/api/examples_test.go:44-47`) uses plain HTTP and clears the TLS variables, so no TLS path of the suites runs in CI. | **Fixed (ccca8cf):** The Jest helper passes `NODE_EXTRA_CA_CERTS` on as `CHAOSGW_CA_CERT` unless `CHAOSGW_CA_CERT`, `CHAOSGW_FINGERPRINT` or `CHAOSGW_INSECURE` is set; the README states which setting each tool reads; `conftest.py` accepts `CHAOSGW_INSECURE yes` like `chaosctl`; `development.md` notes that the examples test is plain HTTP. The optional TLS run of the examples test was not done. Test: `clients/examples/jest/helpers.test.ts` (3 Jest tests). *Audit proposal:* State in the README that `chaosctl` needs its own setting in addition (`CHAOSGW_CA_CERT=<same PEM>` or `CHAOSGW_FINGERPRINT`), or let `helpers.ts` pass `NODE_EXTRA_CA_CERTS` on as `CHAOSGW_CA_CERT`; optionally run the examples test once against an `httptest` TLS server. |
| P4-AUDIT-51 | minor | `dns_query_seen` and `traffic_not_seen` (with `except`) never run on the real kernel. `M16_LEVEL1` (`ci.yml:27`) holds three tests (`e2e_checks_test.go`), which use `reconnected`, `no_connection` and `traffic_seen` only; the other checks are tested over a fake or simulated conntrack and a posted log (`internal/run`, `runs_checks_test.go`). Plan M16 asks for scripted client behavior in the testbed. Not covered on the real kernel: that the client address the proxy sees maps to a device (an entry with `Device` nil is ignored); `e2e_dns_test.go` does not check the `Device` field of a logged query. | **Fixed (7cdc264):** Two level 1 tests added to `M16_LEVEL1`: `dns_query_seen` with a device that resolves through the gateway DNS (pass, log entry carries the device) and one that does not ask (fail), on the DNS bed (`newDNSBedOn`); `traffic_not_seen` with `except` against a device talking to an allowed and a disallowed server. Test: `TestDNSQuerySeenDecidesForADeviceOnTheRealKernel`, `TestTrafficNotSeenWithExceptDecidesOnTheRealKernel` (level 1; vetted, not run here). *Audit proposal:* Two level 1 tests, a device that resolves a name through the gateway's DNS inside a window (pass) and not at all (fail), and `traffic_not_seen` with `except` against a scripted device that talks to an allowed and a disallowed server (the existing `dnsBed` can be reused); assert `Device` of a real query-log entry; add both to `M16_LEVEL1`. |
| P4-AUDIT-52 | minor | The ground truth of the level 1 capture tests is made by the code under test, and the tshark check can pass silently. `startTruth` (`internal/api/e2e_captures_test.go:41-85`) records "exactly the capture on the device itself" with a second `capture.Session` and reads it back with `capture.NewReader`: the same AF_PACKET reader, BPF generator, writer and reader on both sides, so a common fault in the read loop, snap length or timestamps cancels out (the unit tests cover the BPF and `TestWiresharkReadsBothFormats` covers the raw IP interface block). The only tshark check at level 1 (:368) is `err == nil && !strings.Contains(...)`, so a file tshark rejects, or a missing binary, passes. | **Fixed (43d90c8):** The device, selector and run level 1 tests use `tcpdump -w` in the device namespace as ground truth (hand-written `ether host`/`tcp port` expressions); `tshark` is checked strictly (`tsharkFields`: any error fails, only a missing binary skips) and also on the device and selector downloads. The WireGuard test keeps the engine session as truth. `development.md` updated. Compiled and vetted only (no netns here). Test: `TestACaptureOfADeviceIsExactlyTheCaptureOnTheDeviceItselfOnTheRealKernel`, `TestASelectorCaptureHoldsTheConnectionsItNamesOnTheRealKernel`, `TestARunWithCaptureTrueAttachesTheTrafficOfItsTargetOnTheRealKernel`, `TestAWireGuardNetworkIsCapturedAsRawIPOnTheRealKernel` (level 1). *Audit proposal:* Make the tshark check fail on any error except `exec.ErrNotFound` (skip then), with stderr as in `format_test.go`; use `tcpdump -w` in the device namespace as an independent truth for at least the device and selector tests. |
| P4-AUDIT-53 | minor | The lease-renewal tests do not fail when `Renew` stops extending the lease. Mutation: replace `r.leaseTimer.Reset(r.lease)` (`internal/run/manager.go:684`) by a no-op; `TestAnExpiredLeaseAbortsTheRunAndARenewalKeepsItAlive` (`lifecycle_test.go:123`) and `TestTheLeaseOfARunIsRenewedOverTheAPIAndExpires` (`runs_test.go:457`) still pass in every run: they check the returned `lease_expires_at`, a `running` status read right after `Advance(29s)` (the abort takes several goroutine hops, so it is still `running`) and a final "aborted, lease expired" that holds for the original lease. Only the `chaosctl` test kills the mutant. | **Fixed (bb42823):** After the `Advance` that crosses the original deadline the tests renew the run (refused for a cancelled run), which proves the deadline moved; a mutant with a no-op `Reset` now fails the run test. Test: `TestAnExpiredLeaseAbortsTheRunAndARenewalKeepsItAlive`, `TestTheLeaseOfARunIsRenewedOverTheAPIAndExpires`. *Audit proposal:* After the `Advance` that crosses the original deadline wait until the run goroutine had the chance to react (a barrier: a second run on another target that must start and finish, or `eventually` holding for 50 ms) before asserting, or expose the pending lease deadline and assert it moved. |
| P4-AUDIT-54 | minor | `TestADNSQueryAfterTheWindowDoesNotCount` (`internal/run/checks_test.go:487-499`) never sends a query after the window: no query is added to the observer, so it shows only that a check without any query fails. Removing the guard `if c.ended && q.Time.After(c.wendWall) { continue }` (`checks.go:489`) passes the whole of `internal/run` (reproduced); the fake observer's `DNSQueries` filters only on `since`, so that guard is the only thing that applies the end of the window. | **Fixed (667ac8c):** The test now appends a query stamped after the window end and still expects the check to fail (it fails if the guard in `lookDNS` is removed); a new test pins the inclusive boundary. Test: `TestADNSQueryAfterTheWindowDoesNotCount`, `TestADNSQueryAtTheLastMomentOfTheWindowCounts`. *Audit proposal:* Append `query{device: idESP, name: "broker.example.com", at: <after the window end>}` before the final tick and assert the check fails; keep a query at exactly the window end as an inclusive-boundary test. |
| P4-AUDIT-55 | minor | A cleanup retry that succeeds on a later attempt is not tested. `finish` retries `ResetOverlays` three times (`internal/run/exec.go:415`, `cleanErr = nil`); the only test (`steps_test.go:292`) makes it fail permanently. Deleting `cleanErr = nil` passes all of `internal/run` (reproduced), although a transient failure followed by a success would then report `error: the overlays of the run could not be removed` for a passing run. | **Fixed (1e303c9):** The fake gateway's `resetFailures` makes the first reset fail and the next work; the run passes with no leftover overlays. Deleting `cleanErr = nil` now fails. Test: `TestARemovalOfTheOverlaysThatWorksOnALaterAttemptKeepsTheVerdict`. *Audit proposal:* A `fakeGateway` hook that fails the first reset and succeeds the second (`resetErr` cleared after one call); advance the clock by 1 s; assert the run is `passed` and has no overlays. |
| P4-AUDIT-56 | minor | The limit of queued and running runs has no test. `Config.MaxRuns` (`internal/run/manager.go:302`, `ErrTooManyRuns` mapped to 422 `capacity_exceeded` at `internal/api/problem.go:213`) is set or provoked by no test; changing `active >= m.cfg.MaxRuns` to an unreachable bound passes the whole suite, and the API mapping and the freeing of a slot after a run ends are unchecked. | **Fixed (edc5788):** A manager test with `MaxRuns` 2 (queued runs count, a refused run leaves nothing, the slot is freed when a run ends); `api.Config.RunMax` plus a harness option; an API test for 422 `capacity_exceeded`. Test: `TestTheNumberOfQueuedAndRunningRunsIsLimited`, `TestTooManyRunsAreRefusedAsCapacityExceeded`. *Audit proposal:* In `internal/run`: `Config{MaxRuns: 2}`, two runs on different targets, the third `Create` returns `ErrTooManyRuns`, after one ends a new `Create` succeeds; in `internal/api` one test for 422 `capacity_exceeded` (a harness option for `MaxRuns`). |
| P4-AUDIT-57 | minor | The cleanup of `chaosctl with` that finds the overlay already gone is untested. `removeAll` treats a 404 on `DELETE` as success ("its TTL ran out", `internal/ctl/with.go:144`); changing the check passes `internal/ctl` and every `TestChaosctl*` of `internal/api` (reproduced); the fake gateway only answers 500 (`TestWithReportsAnOverlayItCannotRemove`), and no test lets a `--ttl` run out during the command. With the check broken `with --ttl 1s ... -- sleep 3` reports "could not remove overlay" and exits 125 for a successful command. | **Fixed (aa64440):** The 404-on-DELETE cleanup of `with` is pinned: a fake-gateway test (`deleteStatus` 404) expects the command's own exit code and no "could not remove"; an API-level test lets a command outlive a 1 s TTL. Changing the 404 check fails the first. Test: `TestWithTreatsAnOverlayThatIsGoneAlreadyAsRemoved`, `TestChaosctlWith/WithAnOverlayWhoseTTLRanOutDuringTheCommand`. *Audit proposal:* A fake-gateway test with `deleteStatus` 404 expecting the command's own exit code (0 for `true`, 4 for `exit 4`) and no "could not remove" on stderr; an API-level test with a command that outlives a 1 s TTL. |
| P4-AUDIT-58 | minor | Two more mutants survive in `internal/run`. Removing the `failPendingChecks(&r.rec, model.RunStatusAborted, "service restart")` call in `Recover` (`manager.go:197`) passes all tests (the restart tests use records without checks), so the message "not decided: the run ended as aborted (service restart)" is untested. In the flow tracker the branch `packets < old.packets` (`checks.go:235`: the same key with counters that started over) is not exercised: `TestTheFlowTrackerReportsWhatChangedBetweenTwoPolls` removes and re-adds the entry, the `!known` branch (a real case is rare, since the flow key includes `StartedAt`). | **Fixed (035da9b):** The restart test has a run with a pending check and asserts "not decided: the run ended as aborted (service restart)"; the flow tracker test has a same-key entry with smaller counters expecting "created traffic". Both mutants now die. Test: `TestARestartEndsARunningRunAsAbortedAndKeepsTheFinishedOnes`, `TestTheFlowTrackerReportsWhatChangedBetweenTwoPolls`. *Audit proposal:* Add a record with a pending check to `TestARestartEndsARunningRunAsAbortedAndKeepsTheFinishedOnes` and assert the message; add a tracker update where the same flow id keeps its place with smaller packet counts and expect `created traffic`. |
| P4-AUDIT-59 | minor | The burst test lost its guard against a cost that grows with the square of the burst. In Phase 4 (commit `606e21f`) `TestAHundredWritesWithoutAHeldExecutorAreQuick` (`internal/engine/coalesce_test.go:229-239`) changed its bound from 5 s to 30 s and added `applies > 10`. Its comment says it guards against "one compile per write", but the number of applies does not measure that (writes are validated in `validPut` without a compile, the batch is compiled once in `finishBatch`; a compile per write in `validPut` would still coalesce into few applies). The test does the 100 writes in about 0.3 s, so the 30 s bound has a hundredfold margin. | **Fixed (cc6e195):** The engine counts the dry compiles that check overlay writes (`DryCompiles` via `export_test.go`); the burst test requires at most 10 compiles and 10 applies, the 30 s bound stays only as a hang guard. Test: `TestAHundredWritesWithoutAHeldExecutorAreQuick`. *Audit proposal:* Keep the applies check and assert the cost directly: count compiles (a counter on the engine or a test hook) and require far fewer than 100, or compare the time of 100 writes with that of 10 (ratio bound) instead of an absolute 30 s; correct the comment. |

### Per milestone

| Milestone | Verdict | Audit items (all open) |
|---|---|---|
| M15 Runs, scenarios, overlays by step | done with gaps | P4-AUDIT-05, -06, -07, -10 (major); -14, -17, -21, -23, -24, -25, -26, -53, -55, -56, -58 |
| M16 Checks and zero hits | done with gaps | P4-AUDIT-02, -03, -04 (major); -15, -19, -20, -43, -51, -54 |
| M17 Captures | done with gaps | P4-AUDIT-01, -08, -09 (major); -16, -22, -27, -28, -29, -44, -49, -52 |
| M18 `chaosctl` and the example suites | done with gaps | P4-AUDIT-11 (major); -30, -31, -32, -33, -34, -35, -47, -50, -57 |
| M19 Scenario UI | done with gaps | P4-AUDIT-12 (major); -18, -36, -37, -38, -39, -40, -41, -42 |
| Cross-cutting (tests, spec, docs, CI) | done with gaps | P4-AUDIT-13 (major); -45, -46, -48, -59 |

What each milestone delivers, as the lenses read it. **M15**: the order and replace/remove/restore semantics of steps, the wait shift,
preconditions, the lease, abort and renew (including the queued case and the owner scopes), the FIFO queue per overlapping target,
`target_busy`, the recovery after a restart, the close order against the engine, cleanup by owner, `Idempotency-Key` on both create
operations, and the JSON and JUnit reports hold. **M16**: the window placement per polarity, the reserved id `start`, pending checks
at the end of a run, the zero-hit baselining across replaced steps, the profile that impairs nothing and the `zero_hit:error` case
hold. **M17**: the BPF filter against the Go matcher, directions, `os.Root` and `O_EXCL` handling of the directory (no traversal or
symlink escape), the download cutting incomplete records, the executor slots and connection lifecycles and the run attachment hold;
all 31 `M17_LEVEL1` tests exist and are in the CI lists. **M18**: exit codes 0/1/2 of `run` and `wait`, the abort of an owned run on a
signal or timeout, the lease renewal at a third, `with` cleanup and its exit-code mapping, the fingerprint pinning, the token file and
the generated clients (regenerated and compiled in `check-clients`) hold. **M19**: every step and check type of the editors maps to
the schema (dns, tls and dhcp kept as decided), the YAML round trip and the marks, the capability gating and the `FaultDialog`
refactor hold; vue-tsc is clean and vitest passes (322 tests). Regressions to code from before Phase 4: the spec diff against `main` is
additive only (nothing required, no enum value, status or code removed), `ValidateScenario` keeps its behavior, the event list
matches the spec enum, and the Redialing client implements `OpenCapture`; the regression found is P4-AUDIT-06 (a refused forced
apply changed nothing before Phase 4), with P4-AUDIT-25 (the count of `/reset`) and P4-AUDIT-59 (a weaker test).

### Evidence

- **CI on the branch:** run 39253445201, the level 1 tests on the real kernel, green. Not run in the audit (no KVM, no netns).
- **Built and run in the audit:** `go build ./...`, `go vet` on `run`, `domain`, `api`, `ctl`; `go test` of `internal/run`,
  `internal/domain`, `internal/capture`, `internal/capturemgr`, `internal/ctl`, `internal/schema`, `cmd/...` and parts of
  `internal/api` (`-race -count=20` of `internal/run` was green); vitest, 322 tests, green; the Playwright list (208 tests in 16
  files) was listed but not run; `oapi-codegen` showed no diff against the committed generated Go code, the Python and TypeScript
  clients generated and compiled, and `api/examples/validate.py` passed.
- **Reproduced in scratch copies outside the repository:** P4-AUDIT-01 (the panic after a failed rotation), P4-AUDIT-13 (flaky
  warnings), P4-AUDIT-32 (exit 1 with an empty message), and P4-AUDIT-53, -54, -55, -57 and -58 (mutants that survive the suite).

### Phase 4 audit: result of the round and remaining points

The round after the audit (2026-10-19, branch `worktree-phase-4`, merged to `main` with PR #71 on 2026-10-20) closed every item, see the Fix column of the
table. It was built and checked without KVM and without network namespaces: `go build ./...`, `go vet ./...` (also with
`-tags testbed` and `-tags testbed,browser`), `gofmt`, `make check-spec`, `make check-generated` and `make check-clients` were green,
and all runnable tests of the packages the items touched passed (`-race`; `internal/api` takes about 650 s under `-race`, so it needs
`-timeout` of 25 minutes or more, as the Makefile has with 20m; a last full run of `internal/api` after the final fix was not
repeated). Decisions taken by best practice, not by the maintainer: **P4-AUDIT-47**, `chaosctl explain` was built (plan M18) rather
than struck, and the stale stub row under "Covered later" now reads `chaosgw tls` only; **P4-AUDIT-10**, `settings.retention.runs`
is the authority for run retention and the flag `--run-retention` is kept as the default for a configuration without the setting
(like `captures_quota`, the setting of the active configuration wins; a deployment that sets the flag keeps working), documented in
`docs/development.md` and `docs/install.md`. A design choice of P4-AUDIT-11 goes beyond the audit: `--insecure` together with a pin
or CA file from the same environment source is a usage error too, not only the flag pair.

Not verified in the round (for CI, the first run on the final commit): the level 1 tests added or changed for P4-AUDIT-15, -51 and
-52 (their assumptions: a SYN dropped in the forward path leaves no conntrack entry, the device of the DNS bed passes the run
preconditions, `ether host`/`tcp port` give the same packet set as the earlier truth, `tcpdump -Z root --immediate-mode` works in
CI, the strict `tshark` check holds on the device and selector downloads), `stepsOnTime` (P4-AUDIT-17, unchanged, should now also
catch a late restore), and the nightly `-expect-pass` lists of P4-AUDIT-49 (a test that legitimately skips on the older kernel of
the matrix fails the first night). `internal/run` `TestAnExpiredLeaseAbortsTheRunAndARenewalKeepsItAlive` failed twice in full
package `-race` runs under heavy CPU load and passed on repeat; it looks load-sensitive and is not proven so.

CI run 39343558893 (the first on the final commit of the round) failed four level 1 tests on a real kernel, answered in the
follow-up commits: `stepsOnTime` (the counter read before a `restore` took 0.6 s in the VM and is now part of the hand-over by
design; the test requires `step_late` and takes the named read off), the two capture tests of P4-AUDIT-52 (one frame more on the
device than in the capture: the ground truth was stopped by hand after the capture and is now cut to the period of the capture,
and a mismatch prints when the frame was on the wire), and `TestDNSQuerySeenDecidesForADeviceOnTheRealKernel` (the step never got
its status; the test now waits for a logged query that is assigned to the device, and a timeout of `waitStep` and `waitRun`
prints the run). The causes of the last two were not provable from the logs; see the commit messages.

Remaining points of minor weight that the round left, none of them a new item:

- P4-AUDIT-06: if the apply fails at the kernel after the runs were aborted, the runs stay aborted (documented).
- P4-AUDIT-07: `POST /reset` gives no error answer for a run whose cleanup failed; the retry removes the overlays later.
- P4-AUDIT-08: a concurrent `Start` could pick the same room between the check and the deletion (as `makeRoom` before);
  `enforceQuota` catches an overshoot.
- P4-AUDIT-04: the error path for a world that cannot be built is pinned for `matchFlow` only, not for the check and the `wait`
  message.
- P4-AUDIT-29: no test has an `Output` whose `Close` fails; the unit test feeds the executor event.
- P4-AUDIT-30: the streaming test, against a buffering mutant, hangs until the test timeout instead of ending in a plain FAIL.
- P4-AUDIT-35: the renewal rate per overlay in `with` is not pinned by a test.
- P4-AUDIT-39: the name fallback request of the last result takes its result without the inline-only check.
- P4-AUDIT-41: `GetRunEvents` and the report handlers send no `Content-Disposition`, so `curl -OJ` and `chaosctl` keep the bare names.
- P4-AUDIT-10: `settings.retention.events` and `settings.retention.audit` are still unused (before Phase 4).
- Optional parts not done: a level 2 capture smoke (P4-AUDIT-49), a TLS run of the examples test (P4-AUDIT-50), attaching the
  overlay found by (owner, step) to a step cancelled in flight (P4-AUDIT-23).

### Phase 4 audit items for the maintainer

Found by the audit of 2026-10-19 and confirmed in the code. Each needed a decision about the meaning of a promise or the boundary of a
privilege; none blocked the round, and the fixes of the other items did not depend on them. The maintainer decided all three on
2026-10-19 (option (a) each) and they are implemented on the branch `worktree-phase-4`.

#### P4-AUDIT-14 What does `step_late` promise: a long queue wait or a late hand-over?

- Status: closed (2026-10-19): decided by the maintainer, option (a), implemented on the branch `worktree-phase-4` (`d563f5a`). A
  step that is handed over more than 100 ms after its due time (its scheduled offset plus the shift of the waits before it) gets
  the run warning `step_late`, one warning per step, which names the cause: the previous step was still applying, the timeline
  woke late, the counter read before the step, or the wait for the executor (the queue wait of the old warning is merged into
  the same warning when both apply). It covers `wait`, `capture` and a skipped `remove` steps too. Recorded in plan §2.10 and
  §3.11, in `api/openapi.yaml` (`queue_wait_ms`, `start_offset_ms`, `RunWarning`) and in `docs/development.md` (decision 4, the
  queue wait section and the test table). The level 1 tests filter warnings by code, so they allow `step_late` as
  `captureWarnings` did. Tests: `TestAStepHandedOverMoreThan100msLateGetsTheWarningStepLate`,
  `TestAStepHandedOver100msLateIsNotLate`, `TestAStepLateForBothReasonsHasOneWarning`, `TestAStepTheTimelineReachesLateSaysSo`
  (fake clock, `putDelay`/`readDelay`).
- Severity: minor
- Reason: needs-decision. Plan §2.10 (rewritten on 2026-10-19 around the hand-over) says "A step that starts more than 100 ms late
  gets the run warning `step_late`" and, in the same paragraph, that a step due while the apply of the one before is still running
  "is handed over late"; §3.11 says "starts more than 100 ms late". The code warns only when `OverlayResult.QueueWait` (the wait
  behind an apply in flight plus the executor queue) exceeds 100 ms (`internal/run/exec.go:207`), which is also what decision 4 in
  `docs/development.md`, `api/openapi.yaml` (`queue_wait_ms`, "A step that waited for more than 100 ms") and the M15 test line say.
  The timeline is sequential, so a step handed over late behind a slow apply has a queue wait near 0 and gets no warning; the
  decision of 2026-10-19 covered the promise and the two moments in the report, not the warning.
- Evidence: `internal/run/exec.go:99-117,181-210`, `docs/plan.md:484,1094,1461`, `docs/development.md` (~4537-4551, decision 4),
  `internal/run/steps_test.go:220`, `internal/api/runs_hold_test.go:61`; `internal/api/e2e_captures_test.go:383`
  (`captureWarnings` filters `step_late` out of the level 1 captures).
- Options: (a) also warn when `start_offset_ms` minus the scheduled offset (plus any wait shift) exceeds 100 ms, one warning per
  step naming both causes, in `applied` and in the skip, remove and wait paths, with a fake-clock test (`putDelay`); the plan, the
  spec text of `queue_wait_ms`/`RunWarning` and decision 4 are updated; the level 1 tests that fail on unexpected warnings need the
  `step_late` allowance that `captureWarnings` already has, and every slow CI run will carry the warning. (b) Keep the warning on the
  queue wait, reword plan §2.10 and §3.11 to "waited more than 100 ms for the executor" and say that the lateness of the hand-over is
  read from `start_offset_ms`.
- Recommendation: (a): the plan's reason for the warning is that a passed run whose steps came late proves less, the sentence names
  the sequential-timeline case itself, and the decision of 2026-10-19 chose to record both moments; (b) is the cheaper edit.
- Needs maintainer: no (decided 2026-10-19)
- Effort: S

#### P4-AUDIT-15 Do the traffic checks have to see packets the gateway drops?

- Status: closed (2026-10-19): decided by the maintainer, option (a), implemented on the branch `worktree-phase-4` (`7cdc264` the test and
  its CI entry, `df78ecf` the texts). The limit is documented: the checks observe conntrack, and a packet dropped in the forward
  path leaves no entry, so while a `cut` or a `rule: drop` is in force `traffic_not_seen` and `no_connection` pass for a device
  that keeps sending and `traffic_seen` of the blocked destination fails. The texts are in `api/openapi.yaml` (`Check`,
  `traffic_seen`, `TrafficNotSeenCheck`), plan §2.10 and `docs/development.md` M16 (decision 9). A level 1 test pins it:
  `TestTheTrafficChecksDoNotSeeWhatTheGatewayDropsOnTheRealKernel` (a drop rule and a retrying device; the rule counter shows
  the dropped packets), in `M16_LEVEL1`. It is vetted with `-tags testbed` and was not run in the round; it assumes that a
  dropped SYN leaves no conntrack entry, which CI shows.
- Severity: minor
- Reason: needs-decision. The checks observe conntrack only, and a packet dropped in the forward path leaves no entry
  (`development.md` ~4324 says so for the port-forward counters). While a `cut` or a `rule: drop` is in force, `traffic_not_seen`
  passes for a device that keeps sending and `traffic_seen` of the blocked destination always fails; the M16 text and the spec do not
  say so, and the spec describes `traffic_seen` as "a packet of a connection of the target". No level 1 test runs the two checks
  under a drop.
- Evidence: `internal/run/checks.go:115-116,340`, `internal/engine/observe.go:371`, `api/openapi.yaml` (~3779-3835),
  `internal/api/e2e_checks_test.go`.
- Options: (a) document the limit in the `Check`, `traffic_seen` and `traffic_not_seen` descriptions and in the M16 decisions, and
  pin it with a level 1 test (a drop rule and a device that keeps retrying: `traffic_not_seen` passes, `traffic_seen` fails); (b)
  additionally read the packet counters of the run's own drop rules and treat a rise in the window as traffic matching the rule's
  selector; (c) NFLOG-based observation, which plan §2.11 puts after V1.
- Recommendation: (a) for V1: it costs a paragraph and a test, and (b) would add a second observation source with its own windows.
- Needs maintainer: no (decided 2026-10-19)
- Effort: S

#### P4-AUDIT-16 May a capture scope name a WireGuard network of the role `management`?

- Status: closed (2026-10-19): decided by the maintainer, option (a), implemented on the branch `worktree-phase-4` (`a97d387`).
  `interfacesOf` in `internal/capturemgr/resolve.go`, the one place through which the network, device, group and selector
  sources pass, refuses a WireGuard interface of the role management with the validation code `management_network` at the
  scope path, which is what `global()` already did by leaving them out. A run whose target is such a network or one of its
  clients starts no capture and gets the run warning `capture_missing` (it is not refused at create: a run goes on without its
  capture, decision 11). The `global()` comment, `docs/development.md`, `api/openapi.yaml` and plan §2.11 say so. Tests:
  `TestNoScopeResolvesToAWireGuardNetworkOfTheRoleManagement` (every source kind, with a management-role interface),
  `TestACaptureRequestThatDoesNotFitIsRefusedWithItsPointer` (the admin cases) and
  `TestARunWhoseTargetIsAManagementNetworkIsNotRecorded` (the run path).
- Severity: minor
- Reason: needs-decision. `global` skips WireGuard interfaces of the role `management` (`internal/capturemgr/resolve.go:296-326`),
  but a network scope, a device scope for one of its clients, group and selector sources and a run's `capture: true` or capture
  steps with such a target all resolve through `interfacesOf`, which has no role filter. A token with scope `overlays` can then
  record, and a token with `read` download, the raw packets of the admin tunnel. The documents do not forbid it: plan §2.11 says
  "Record by network, device or selector", the `uplink` scope already records the management plane in the two-port topology, only a
  key log or a sensitive capture is restricted to scope `full`, the tunnel carries TLS and SSH, and an administrator may want to
  debug it; against that, the `global()` comment and `development.md` ("every bridge and the WireGuard interfaces of the role test")
  make the exclusion look deliberate and §2.16 treats these networks as the management network.
- Evidence: `internal/capturemgr/resolve.go:97-135,177-215,296-326`, `internal/run/captures.go:43-62`,
  `internal/compiler/wireguard.go:132` (the role is in the snapshot), plan §2.11 and §2.16; the capturemgr test fixture has only
  `Role: "test"`.
- Options: (a) refuse any scope that resolves to a management-role WireGuard interface in `Resolve` (one place), with a validation
  error at the scope path (for example `management_network`), for network, device, group and selector sources alike, so it matches
  `global()`; a run with such a target gets a refusal at create or a `capture_missing` warning; (b) allow them only for scope `full`
  (the gate of sensitive captures); (c) allow them for every token with `overlays`, document it as intended and correct the
  `global()` comment and `development.md`.
- Recommendation: (a): consistent with §2.16 and the existing `global()`, and the one place to enforce it. Either way, add a test with
  a management-role interface, including the run path.
- Needs maintainer: no (decided 2026-10-19)
- Effort: S

### Phase 4 audit: checked and not recorded as items

Six further findings were checked and rejected (the behavior matched the plan or the spec, the finding rested on a wrong reading of the
code, or the consequence did not follow); they are not recorded as items:

1. *The browser tests of the scenario UI run only against the hand-written mock, no flow against the real gateway.* Plan M19 asks for
   Playwright tests of building and running a scenario without a testbed flow (the status line says "Nothing of M19 needs a VM or a
   namespace"); two of the three claimed mock deviations were wrong (a queued run in the mock does carry snapshot, steps and checks;
   artifacts appear at the end only, as in the server), and the generated types check the mock's shapes. Only the default check name
   differs, which is cosmetic.
2. *Every active run polls the full conntrack table every 250 ms through the executor and so delays the applies.* Reads go through the
   executor's separate reader slots (`doRead`, four permits, "never waits for the writer", plan §3.11), not the writer's queue, and
   only runs with an open flow window poll at all. An efficiency idea at most.
3. *A capture whose stop fails at the end of a run stays running and is dropped from the artifacts.* `Manager.Stop` issues the stop
   request before it waits, so the capture ends on the executor either way; what remains (the capture missing from the run's
   artifacts after a timeout, with a `capture_missing` warning) is minor and falls under the remedies of P4-AUDIT-22 and -27.
4. *Renew succeeds for a run that is already being aborted* (the concurrency lens' version). Rejected there on the grounds that the
   answer is true when it is given and the race cannot be removed; the M15 lens' version of the same finding was confirmed by both
   verifiers and is recorded as P4-AUDIT-24.
5. *A capture directory without a readable record is never counted, cleaned up or retried.* The window is milliseconds, not 30 s,
   nothing preallocates, so an orphan is a few kilobytes, and "left alone with a warning" for an unreadable record is a decided,
   tested behavior (`development.md:4586`, `TestTheRecordsOfCapturesSurviveARestart`).
6. *A 3 s lease in a real-kernel test is shorter than an apply.* A lease that runs out during apply N removes the fault only with apply
   N+1, which starts after the test's check; the 20 s lease of the sibling test is a style point, not a defect.

## Phase 4 open items

Phase 4 (M15 to M19, automation) was implemented on the branch `worktree-phase-4` and merged to `main` with PR #71 on 2026-10-20. Items below are what the implementation
found that no later milestone of the plan completes and that need a decision of the maintainer.

### P4-M19-01 The Captures screen and the capture panel of the device detail have no milestone

- Status: closed (2026-10-19): decided by the maintainer, option (a): the screen is the new milestone M23a (Captures UI, S) in Phase 5 of `docs/plan.md`, part of V1; its design is part of that milestone
- Severity: medium
- Reason: needs-decision. The plan's navigation (§2.17) lists Captures, and the device detail is to show the device's captures
  (§2.17, "Everything is connected: a device shows its … captures"), but the section "Not yet designed" names Captures, and no
  milestone builds either. M17 delivered the engine and the API (`/captures`, download, quota), M19 shows the captures of a
  run as artifacts of the run (the capture of `capture: true` or of the capture steps is a download on the run's page and the
  scenario's), and M12 left the device detail's capture panel out "until its backend comes". With M19 done the backend is
  there and the UI for it is not: a capture of a device, a network or a selector can be started and stopped only over the API or
  with `chaosctl` (which has no capture command either), and the nav entry says "Arrives with a later milestone". Whether this
  is a milestone of its own before V1, a part of M24 (diagnostics), or left to the API until after V1 is a decision about the
  scope of V1 and about the screen's design, which does not exist.
- Evidence: `web/src/components/layout/SidebarNav.vue` (Captures is not a link), plan §2.17 "Screens" (no row for Captures) and
  "Not yet designed", `docs/development.md` "Scenario UI (M19)" (decision 9).
- Task: decide when the screen is built and what it has. A proposal to design against: a list of captures (name, scope,
  state, size, packets, dropped, started, the run it belongs to), "New capture" (the scope as the capture request has it:
  network, device, selector or the uplink; format, snap length, duration, size or ring buffer), stop, download (the key log
  only with scope `full`), delete, the quota and the free space; the device detail's panel lists the device's captures with
  a "Capture" action; the events `capture_started` and `capture_stopped` keep the list live (the event stream already
  invalidates the runs on them).
- Options: (a) a new milestone "Captures UI" (S) in Phase 5 or 6, before V1; (b) part of M24, which adds the diagnostics panel
  to the same screens; (c) after V1, the API and the run's artifacts being enough for the first release.
- Recommendation: (a): a small, self-contained screen whose backend is done and tested; tests need the capture of a run, which
  M19's artifacts already show.
- Acceptance: the plan has the milestone (or the decision to leave it out) and its design.
- Needs maintainer: no (decided 2026-10-19: option (a))
- Effort: M

## Phase 5 audit result

Date: 2026-10-26. Audited on the branch `phase-5b` at `eab804c` (PR #121, everything since `767af94`), together with the part
of Phase 5 that reached `main` with PR #120 (the commits of M20 to M23a between `9ab7a24` and `767af94`), against `docs/plan.md`
§5 Phase 5 (M20 to M23c) and the sections it names (§2.2.2, §2.3, §2.6 to §2.8, §2.16, §2.17, §3.x, the D-decisions),
`api/openapi.yaml`, `docs/development.md` (the sections for M20 to M23c), the Phase 5 open items and the earlier audits in this file.
The audit read the phase through nine lenses: M20 (DNS faults and hostname selectors), M21 (the TLS responder, certificate cases and
pass-through), M22 (interception and HTTP faults, the mitmproxy sidecar, its compose file and CI job), M23 and M23a (DHCP test
actions and the Captures UI), M23b and M23c (the forms of the application-layer kinds, the Interception UI and `chaosctl`), security,
concurrency, shutdown and regressions, spec, docs and CI, and test quality (49 mutation experiments on a scratch copy across Go,
Python and the web app). The audit was read-only: it built the code, ran `go vet` (also with `-tags testbed`), single-package tests
(`-race` on every Phase 5 package, the real mitmproxy tests of `internal/tlsresponder`, the real-Kea tests of `internal/kea`),
vitest and scratch Playwright specs, reproduced findings in scratch copies outside the repository, and changed nothing. The level 1
tests on the real kernel were not run here; CI ran them (run 40270141299 on `eab804c`, green in every job, the VM job included).
Every finding was checked a second time by two independent verifiers who tried to refute it; where they disagreed on the severity
or on a detail, the lower or the better-argued one is recorded.

**Phase 5 is implemented and its tests pass, but it is not yet clean.** The audit found no blocker. It found 10 items of major
severity and 33 of minor severity (43 items after merging three pairs of duplicates and two doc findings of the same section; no
finding was refuted by both verifiers). The
core of every milestone held up: the DNS proxy's faults and the hostname sets, the nft chains of the TLS redirect and its guards, the
upstream policy of the proxy, the resolution of DHCP actions and the forms against the spec. The major items are in the edges: a
promise of §2.3 that nothing implements (DNS-derived set elements are not re-keyed when a device gets a new address, P5-AUDIT-01),
two connection paths of the responder that the gateway would have forwarded but that stall or are reset (P5-AUDIT-02, -03), the
interception proxy failing open while it does not know a connection's case (P5-AUDIT-04), a `modify` rule's headers lost on a
`status` answer (P5-AUDIT-05), the proxy container stuck in an orphaned service namespace (P5-AUDIT-06), DHCP actions that silently do
nothing for a configured device without `network` (P5-AUDIT-07), two web defects (the DHCP card's reading of a network without a
`dhcp` block, P5-AUDIT-08, and the compact exchanges table, P5-AUDIT-09), and the disagreement between the network form and the
gateway on what a missing `dhcp` block means (P5-AUDIT-10). One item needed the maintainer (P5-AUDIT-10, decided on 2026-10-27, option (b), see below). The
nightly kernel matrix has not run any Phase 5 level 1 list yet (P5-AUDIT-43). **Result of the round after the audit (2026-10-26):** 42 of the
43 items are closed on the branch `phase-5b` (PR #121, merged to `main` on 2026-10-27, `1c969c4`): 38 are fixed with a test that fails without the fix or with the
corrected document, four were fixed differently from the proposal (P5-AUDIT-09, -15, -26 and -35, see the Fix column), none was
rejected as wrong after all, and none is not done. P5-AUDIT-10 was decided by the maintainer on 2026-10-27 (option (b), the
gateway's rule stays) and is fixed on the same branch, so all 43 items are closed; P5-AUDIT-08 and the network form follow the
gateway's rule through one shared helper (`dhcpServed`). The choices taken by best practice are listed under "Result
of the round" below. The Fix column starts with the outcome and the commit; the audit's own proposal follows as *Audit proposal*.
Not run in the round (no KVM, no netns, no Docker): the level 1 tests, among them the ones the round added or changed for
P5-AUDIT-01, -04, -05, -12 and -43 (vetted with `-tags testbed` and listed below, for CI to run), and the new step of
`deploy/smoke-tlsproxy.sh` (P5-AUDIT-04, -06; it runs in the CI job `tlsproxy-image`). CI ran them on the final commit of the
round: run 40335480067 on `9bfd4cf` (PR #121) is green in every job, the level 1 tests in the privileged container and in the VM
and the job `tlsproxy-image` with the new smoke step included; it needed no fix round.

| Verdict | Items |
|---|---|
| Fixed on the branch `phase-5b` (a test that fails without the fix, or the corrected document) | 38 items: P5-AUDIT-01 to -43 except -09, -10, -15, -26 and -35 |
| Fixed differently from the audit's proposal | P5-AUDIT-09 (plan §2.17 removes only the device column, so the compact table keeps Size and Took and gets the missing headers), -15 (the counters are pruned to the overlays in the store, not to those in a DNS or TLS list, so an overlay that is overridden for a while keeps its count and gives no false `zero_hit`), -26 (an operator compose file `deploy/compose.local.yaml` named in `COMPOSE_FILE`, not a `compose.override.yaml`, which Compose does not read when `COMPOSE_FILE` is set; the launcher warns about an unreadable CA file), -35 (the count is 22, not 20, after the round's tests of P5-AUDIT-08 and -32; the stale counts of M23a and M23c were corrected too) |
| Rejected as wrong after all | none |
| Not done | none (P5-AUDIT-10, the one item for the maintainer, was decided on 2026-10-27, option (b), and is fixed) |
| Checked and rejected during the audit | none (no finding was refuted by both verifiers; three pairs of duplicates were merged, see the end) |

| Item | Severity | Finding | Fix |
|---|---|---|---|
| P5-AUDIT-01 | major | DNS-derived set elements are not re-keyed when a device changes its address. Plan §2.3 (`docs/plan.md:246`, identity changes during a test) promises "DNS-derived hostname-set entries keyed on the old address are re-keyed". The elements are keyed on (client, answered address) (`internal/engine/hostsets.go:62` `hostKey{client, addr}`, the kernel key in `compiler/hostnames.go:183`, `ct original saddr . daddr`); `hostSets.applied` (hostsets.go:386-414) only follows renamed and removed sets and adopts kernel listings after a restart, and `refresh` (446-492) only extends elements whose client is the flow's source. Nothing in `internal/` re-keys, the identity paths of `applyloop.go:166-184` only call `begin`/`applied`, and `docs/development.md` (M20) neither mentions the promise nor lists it as a limit; no test covers it. A device that resolved `broker.example.com` before a `dhcp force_new_ip` step keeps its cached answer, so after the move its connections to the broker escape a hostname latency fault or a hostname drop rule until it resolves again, while the UI shows the fault attached; a device that is given the old address in the same scope inherits the elements. | **Fixed (15c2dbb):** after every apply that succeeded, full or incremental, `hostSets.rekey` compares the owners of the addresses with those of the last apply: an element (A, x) of a device that has the address B now (too, or instead) is written as (B, x) with the time it has left into every set whose sources hold B, and (A, x) is taken out when A went to another device or left a device that has other addresses (an old address that stays mapped for its connections keeps its elements). Beyond the proposal, `nft_dns_elements` takes elements to delete (`delete`, no timeout): the netlink and the nft paths delete them in the transaction, and when it fails because an element is gone the deletes go one by one after the adds. The level 1 test drives the engine's report directly (what the proxy posts) in the DHCP bed of M23 instead of `dig`, as that bed has `force_new_ip` and no proxy. Tests: `TestTheElementsOfADeviceFollowItToItsNewAddress` (level 0, the executor sees the write and the delete), `TestTheDNSDerivedElementsOfADeviceFollowItAfterTheForcedNewIP` (through the apply loop on the simulated kernel), `TestADNSElementIsTakenOutAndOneThatRanOutDoesNotStopTheOthers` (executor), the added delete case of `TestTheNetlinkConnectionWritesAndRefreshesDNSElementsInTheGatewayNamespace` and `TestAHostnameFaultStillActsAfterTheForcedNewIPWithoutANewQueryOnTheRealKernel` (testbed, in `M20_LEVEL1`); the engine tests fail without the fix. *Audit proposal:* When an apply or an identity update moves a device from address A to B (the same device in the identity), copy the mirror's elements (A, x) of every set whose sources contain B to (B, x) with their remaining lifetime through `NftDNSElements`, and drop the old ones when A is leased to another device (the new holder wins, as §2.3 says for the identity map). Tests: a level 0 engine test (`hostsets_internal_test.go`) in which an identity change A to B moves (A, x) to (B, x) and the executor sees the write; a level 1 test in `M20_LEVEL1` in which a hostname latency fault still acts after `force_new_ip` without a new DNS query. Both fail today. |
| P5-AUDIT-02 | major | A passed-through connection stalls when the case that selected it goes. The responder registers a passed-through connection with an empty overlay (`internal/tlsresponder/server.go:336` `s.hold(c, "", 0)`), and `SetConfig` resets only held connections with `h.overlay != ""` (server.go:157). On the kernel side a later packet gets the service mark only while the case still selects the connection (`compiler/tls.go:163`, with ct mark bit 23), and without any case `tls_redirect` is not compiled at all (tls.go:194-196). Once the case goes, the later packets take the uplink and are accepted by `ct state established` (rules.go:225), but leave without NAT: the first packet left through svc0, where no masquerade rule matched (`oifname <uplink>` only, rules.go:309-321), and the binding is settled at the first packet. The namespace's own connection to the server stays open and idle. Decision 5 (`development.md:5759`, "the responder resets what it holds") and plan §2.8 / M21 ("reset on removal") do not cover this; the only test, `TestTheConnectionsOfARemovedCaseAreReset`, is about held connections. A device whose MQTT-over-TLS connection was passed on (another SNI) hangs until its TCP timeout after a run's restore step removes the case, and a `reconnected` check right after fails although the case never applied to it. | **Fixed (a281240):** the responder holds a passed-through connection too, with its client, original destination and the generation of its decision (`hold` with overlay ""); `SetConfig` with a configuration of a later generation resets it, counted in `Resets()`, when no case of that configuration selects it any more by its client (a case's `sources`) and its port (the case's ports, else 443 and 8883), and `hold` does the same when such a configuration came while the connection was being passed on. A destination named by address (`cidr`) is checked too; a network, the uplink or a host name is the compiler's knowledge and counts as selecting, so a case that only narrows its destination leaves the connection as it is: this is the proposal's first form, not "all of them when the case list changes", which would cut passed-through connections the kernel still sends through the responder. Decisions 5 and 7 of M21 say so. Test: `TestAPassedThroughConnectionIsResetWhenNoCaseSelectsItAnyMore` (a configuration that still selects the connection and one of the generation of the decision keep it; another port, another client and no case at all reset it; it failed without the fix: the connection stayed). *Audit proposal:* In `SetConfig`, also reset passed-through connections that a configuration of a later generation no longer selects (the simplest correct form: reset every passed-through connection whose client is in no case's sources for its port, or all of them when the case list changes, since the kernel stops redirecting them either way), and say so in decisions 5 and 7. Test (`tlsresponder/server_test.go`): pass a connection through, call `SetConfig` with a later generation without the case, and assert that the client sees a reset and `Resets()` counts it. Without the fix the connection stays open. |
| P5-AUDIT-03 | major | The pass-through decision resets a connection to a host behind the device's own network's downstream router, which the gateway forwards. The kernel accepts routed hairpin traffic of a bridge before the access rules and the matrix (`internal/compiler/rules.go:233`, `meta iifname br-iot meta oifname br-iot accept`, in `testdata/forward.golden.txt`), and hosts behind a network's downstream routes belong to that network (plan §2.2.2). `domain.AccessVerdict` has no counterpart to that rule and falls to the default `drop` (`internal/domain/access.go:411`), and the TLS selection (`tlsMatch`) does not leave the network's own prefixes out, unlike the DNS redirect (`serviceRedirect`). So such a connection is redirected, `engine/tls.go:107` sets `Passthrough=false`, and the responder resets it, against §2.8 ("passed on ... only if the gateway would have forwarded it"). Reproduced in two scratch copies with `withIoTRouter`: `AccessDecision(10.10.0.31 -> 10.30.0.5, tcp/443)` = {drop, access_matrix, "the default: no entry allows this traffic"}. The same wrong verdict reaches `explain` for any traffic to such hosts. | **Fixed (e147b2d):** `World.sameBridge` mirrors the forward chain's `iifname br-x oifname br-x accept`: source and destination in the same local test network (the longest prefix, the subnet and the downstream routes, as the gateway's routes pick the bridge) give allow in `AccessVerdict` and in `AccessDecision` before the management guard, the access rules and the matrix, so the pass-through decision and `explain` both follow the kernel. M21 decision 4 says so. Tests: `TestTrafficRoutedBackIntoItsOwnNetworkIsAllowedBeforeTheRulesAndTheMatrix` (domain: both directions and switched traffic, the network's drop rule is not asked, another network and the uplink are unchanged), `TestAConnectionNoCaseDecidesIsPassedToAHostBehindTheNetworksOwnRouter` (engine: `ResolveTLS(10.10.0.31 -> 10.30.0.5:443)` has `Passthrough=true`, another test network stays refused, `explain` says allow); both failed without the fix. *Audit proposal:* Make `AccessVerdict` and `AccessDecision` mirror the same-bridge accept (source and destination in the same local test network, which the bridge routes back out the same interface, give allow before the rules and the matrix, as the kernel does); that fixes `explain` too. Leaving the network's own prefixes out of the TLS selection, as `serviceRedirect` does for DNS, is the narrower alternative. Test: an engine test like `TestAConnectionNoCaseDecidesIsPassedToAnotherTestNetworkOnlyWhereTheMatrixAllowsIt` with downstream routes, where `ResolveTLS(10.10.0.31 -> 10.30.0.5:443, SNI other)` has `Passthrough=true`, and a domain test of the verdict. Both fail today. |
| P5-AUDIT-04 | major | The interception proxy fails open while it does not know a connection's case. The responder decides each connection against the API's current snapshot (`POST /internal/tls/resolve`, `internal/api/tlsservice.go` `ResolveTlsConnection`) and names the overlay in `X-Chaosgw-Overlay`; the proxy learns the cases separately by long poll (`api.py` `Follower`, the API looks for a new snapshot every 100 ms, backing off up to 10 s after a failed poll). `Addon.case_of` (`sidecars/tls-proxy/chaosgw_tlsproxy/addon.py:252-258`) returns None for an overlay it does not have, and also after a bare 5 s wait when no configuration has arrived, although its comment says "a request must not pass before the rules that decide it are known". Every caller then fails open: `tls_start_server` returns (:307), so the installation's upstream policy applies instead of the case's `upstream` (while its own `except` branch refuses every certificate); `request` (:374) and `tcp_start` (:513) apply no rule; `keyline` drops lines only for `case is not None and not case.keylog` (:568), and `PostInterceptKeylog` (`internal/api/intercept.go:233-273`) ignores the batch's `overlay`, so the secrets reach a running capture. This contradicts decisions 12 and 14 (`development.md:5902`, `:5908`) and the `keylog: false` promise (:5868). The window is the long-poll lag after a new case, and broad after a reboot or proxy restart: the launcher starts mitmproxy as soon as the CA files exist (`launcher.py:120-131`). `deploy/smoke-tlsproxy.sh:111` depends on the fall-through. Reproduced twice with the real addon and mitmproxy 12.2.3 test flows (no response from the rules, key lines queued). No test covers an unknown or unloaded case. (Found by the M22 and the security lens; merged.) | **Fixed (d496742):** `Addon.case_of` waits for the case of the named overlay up to `CASE_WAIT` (5 s; the first configuration, or a case the long poll has not brought yet) and returns None after that, and every caller refuses: `http_connect` (now async) answers the CONNECT `503` (the responder resets the device and reports `proxy_unavailable`), `request` and `tcp_start` kill the flow, `tls_start_server` makes the connection through `tlsconfig` and applies `UpstreamPlan(verify=True, refuse=True)` whatever the installation says, `keyline` keeps lines only of a known case with `keylog` on; each refusal is reported with the error `the case of the connection is not known to the proxy`. `PostInterceptKeylog` drops a batch whose `overlay` names no case `intercept` in force or one with `keylog: false` (a batch without `overlay` is still taken: the field is optional in the spec, the proxy always sends it, and the existing tests post without it). `smoke-tlsproxy.sh` now serves the proxy one case from a stand-in API in the container, expects that case's request carried and a CONNECT of another overlay refused. Development.md (the addon, decision 12) and the spec say so. Tests: `TestAConnectionOfACaseTheProxyDoesNotKnowIsRefusedAndNeverReachesTheServer` (real mitmproxy, `CHAOSGW_UPSTREAM_INSECURE=1`, a server whose certificate no CA signed: no request at the server, the report, no secrets) and `TestAConnectionWaitsForTheCaseThatTheProxyReceivesAMomentLater` (both failed without the fix: the first was served, the second got a 502 from the installation's policy), `test_addon.py` `UnknownCaseTests` (seven cases; failed without the fix), `TestAKeyLogBatchOfACaseWithoutKeyLogOrOfNoCaseReachesNoCapture` (failed without the fix: the capture was marked). The two real-mitmproxy tests are in `M22_LEVEL1`. *Audit proposal:* For an overlay the addon does not know (or before anything is loaded), wait for the next configuration for a bounded time, then refuse: kill the flow in `http_connect`/`tcp_start`/`request` (or ask the responder to abort the connection) and report the exchange with an error such as "the case is not known to the proxy"; apply `UpstreamPlan(verify=True, refuse=True)` in `tls_start_server`; drop the key lines in `keyline`. As defense in depth, `PostInterceptKeylog` drops a batch whose `overlay` names a case with `keylog: false` or no case in force. Change `smoke-tlsproxy.sh` to expect the refusal or serve a configuration. Tests: a level 0 test with the real mitmproxy whose rig sends an overlay missing from the fake API's configuration on an installation with `CHAOSGW_UPSTREAM_INSECURE=1` and an invalid server certificate, expecting no request at the server; a unit test of `Addon.request`/`keyline`/`tls_start_server` with an unknown overlay; an API test that a keylog batch for a `keylog: false` case does not reach a running capture. |
| P5-AUDIT-05 | major | A `modify` rule's response headers are dropped from a `status` answer but applied to a `block` answer, and the log says `modify` acted. `_change_response_headers` (`addon.py:421`) applies `d.response_headers` only when `d.terminal` is not `status`, and the `status` answer is made with only `Content-Length: 0` (:390). `rules.decide` still lists `modify`, the exchange is reported with actions ["modify","status"] and `http_fault_triggered` announces it. Neither `api/openapi.yaml` (`HttpRule`: rules apply until a terminal action has acted; `modify` "changes the request and response"; `status` answers "with an empty body") nor `development.md:5850` documents the exception, and no test combines `modify` with `status` on the response side (`test_rules.py:72` checks only the action list). The natural back-off test, `modify` Retry-After: 30 then `status` 503, gives a 503 without Retry-After. Reproduced with the real addon: `503 {'Content-Length': '0'} ['modify', 'status']` against `403 {..., 'Retry-After': '30'} ['modify', 'block']`. | **Fixed (d496742):** `_change_response_headers` applies `d.response_headers` to the answer of a `status` rule as to a `block` one and then sets `Content-Length` to the answer's own body, so a `modify` header cannot make it wrong. The spec (`HttpRule.action`) and development.md (the rules) say so. Tests: `TestTheResponseHeadersOfAModifyRuleAreOnTheAnswerOfAStatusOrBlockRule` (`mitm_headers_test.go`, real mitmproxy: `modify{Retry-After: 30}` then `status 503` and `block`, both answers carry the header and their length, the server saw nothing, the reports list `modify` first; failed without the fix: 503 without Retry-After) in `M22_LEVEL1`, `test_addon.py` `ResponseHeaderTests` (failed without the fix). *Audit proposal:* Apply `d.response_headers` to the synthesized `status` response as for `block`, keeping `Content-Length` correct. Test: a level 0 test in `tlsresponder/mitm_headers_test.go` (or a unit test of the addon) with rules `modify{Retry-After: 30}` then `status 503`, expecting the 503 to carry `Retry-After: 30`. It fails today. |
| P5-AUDIT-06 | major | The interception proxy container stays in an orphaned service namespace after a holder restart and reports itself healthy. The DNS proxy and the TLS responder leave a namespace that lost the service address (`--namespace-check`, on by default, `cmd/chaosgw/tls.go:31,68-73`, `cmd/chaosgw/dns.go`), because Compose's `depends_on ... restart: true` acts only on Compose operations (`development.md:1170-1176`). The `tls-proxy` container has no such check: `deploy/compose.tlsproxy.yaml` relies on `depends_on: svcns: restart: true`, `launcher.py:118-146` watches only the CA files and mitmdump's exit, and the health check connects to 127.0.0.1:9444 in whatever namespace the container is in. After the daemon restarts the holder (a crash under `restart: unless-stopped`, `docker restart`), `dns` and `tls` come back in the new namespace and `tls-proxy` does not: every `intercept` connection is reset with `proxy_unavailable` until someone restarts it by hand; `/health` shows `tls_proxy` degraded but nothing heals it. Plan M6b Tests (`plan.md:1447`, "healed by re-attach and service restart") and §3.8 say otherwise; it is the class of the fixed M6b-01. | **Fixed (d496742):** the launcher has `NamespaceWatch`, the check of `dnsproxy.WatchNamespace`: every 2 s the service address (`--service-addr`, `CHAOSGW_SERVICE_ADDR`, 169.254.100.2) has to be among the namespace's local addresses, read from `/proc/net/fib_trie` (no capability, no tool of the image); gone on three checks in a row or never there within 60 s, it stops mitmproxy and exits with code 3, while it waits for the CA too. `--no-namespace-check` / `CHAOSGW_NAMESPACE_CHECK=0` turns it off; `testbed.StartProxy` passes it, since a test's namespace has no service address. Documented in development.md (M6b containers and the launcher), the compose file and install.md. Tests: `test_launcher.py` `NamespaceWatchTests` (gone three times in a row, a short absence and a failed read forgiven, never there within the grace, the parser of `fib_trie`, `main` returns 3, the defaults; they need the new code to run at all) and a step of `deploy/smoke-tlsproxy.sh`: the container gets a network whose fixed address stands in for the service address, `docker network disconnect` makes the launcher exit, the restart policy starts it again, and after the reconnect it carries a request (run by the CI job `tlsproxy-image`, not here: no Docker). The holder restart of `TestAHolderRestartIsHealedWithoutHelp` was not extended: the bed has no container restart policy that would bring the proxy back. *Audit proposal:* Give the launcher the namespace watch of `dnsproxy.WatchNamespace`: check every 2 s that an interface carries 169.254.100.2, and exit with code 3 when the address is gone on three checks in a row or never appears within 60 s, so the restart policy brings the container back into the current namespace (and document it beside the DNS proxy's). Tests: a Python unit test of the watch with a fake interface reader (there, then gone three times, exits non-zero); a step in `deploy/smoke-tlsproxy.sh` (or an extension of `TestAHolderRestartIsHealedWithoutHelp`) that restarts the holder and expects the proxy to answer CONNECT in the new namespace. |
| P5-AUDIT-07 | major | A configured device without `network` gets no DHCP action, and `force_new_ip` is accepted with an address that never reaches Kea. `compileDHCPActions.known` (`internal/compiler/dhcpaction.go:72-84`) takes a configured device's network only from the configuration (`idx.Devices[id].Network`, filled only when `device.network` is set, `domain/index.go:288`), so the device loop (:138-152) reports "DHCP is off on the network of the device" and the deletion loop (:178-186) drops its `delete_lease`. The engine's lookup for `force_new_ip` falls back to the observed network (`internal/engine/dhcpaction.go:94-115` `dhcpDevice`), so the write is accepted and an address chosen, and the UI takes the network from the observed state too (`DeviceDetailView.vue:78-82`) and offers every action. The spec calls `Device.network` "informational except for `fixed_ip`", and P4-AUDIT-05 puts such a device in the network of its lease. Reproduced in a scratch engine harness: a device with a MAC and no `network`, leased in IoT, gets no lease class for `short_lease`, no reservation for `force_new_ip` (answered with `dhcp.ip: 10.10.0.254`) and a `dhcp_action_inactive` with a false reason. A device configured by MAC only is a common setup. | **Fixed (0dae17e):** `compileDHCPActions.known` takes the network of a configured device that names none from where it is seen: `compiler.configuredNetwork` gives the network its current addresses lie in (`Identity.Current`, the newest lease first, then `Identity.Addresses`, by `World.NetworkOf`), which is the network of `DeviceState.Network` (its lease's, else its address's) that `owner.dhcpDevice` takes for the address of `force_new_ip`; the compiler has no leases, and a lease's address lies in its subnet's network, so both lookups give the same network without a second copy of the rule. Development.md (the state of the overlay) says so. Tests: `TestADHCPActionOfAConfiguredDeviceWithoutANetworkActsInTheNetworkItIsSeenIn` in `internal/compiler` (the lease class, DROP, the options reservation, the moved reservation, the deletion, a device seen in Lab, one seen nowhere) and in `internal/engine` (the five actions reach the fake server with no `dhcp_action_inactive`, the deletion and its event); both failed without the fix. *Audit proposal:* In `compileDHCPActions.known`, take the network of a configured device from the observed identity when the configuration names none, as the discovered branch does, so that the compiler and `owner.dhcpDevice` use one lookup (or share one function). Tests: a compiler test and an engine test (`TestADHCPActionOfAConfiguredDeviceWithoutANetworkActsInTheNetworkItIsSeenIn`): such a device, seen with a lease in IoT, gets the lease class, the DROP MAC, the options reservation, the moved reservation and the deletion, and no `dhcp_action_inactive`. Both fail today. |
| P5-AUDIT-08 | major | The DHCP card treats a network without a `dhcp` block as DHCP on; the gateway treats it as off. The card turns its buttons off only for `dhcp?.enabled === false` (`web/src/views/DeviceDetailView.vue:83`, `web/src/views/NetworksView.vue:324`), while the gateway serves DHCP only where `n.Lan.Dhcp != nil` and enabled (`internal/compiler/dhcp.go:40`, `internal/engine/dhcpaction.go:44-46`, `internal/domain/warnings.go:71,98`), and the web app itself uses that rule in `lib/networks.ts:119`. Decision 7 of M23b and plan §2.17 say the buttons are off "where the action could change nothing: a network with DHCP off". On such a network the card offers all four actions without a reason: Short lease writes a `disabled` overlay nobody asked for, and Force a new IP is refused by the gateway after the dialog. The case is common because the network form writes no `dhcp` block for a LAN saved with DHCP on and the defaults (P5-AUDIT-10). | **Fixed (f2e35a7):** `dhcpServed` in `lib/networks.ts` is the web app's one rule for "DHCP is off" (the gateway's: a `dhcp` block that is not switched off); `DeviceDetailView.vue`, `NetworksView.vue` and `reservationPool` use it, so a decision on P5-AUDIT-10 changes it in one place. Test: e2e (`app-layer-forms.spec.ts`) "a network without a dhcp block has DHCP off, as the gateway reads it, and its DHCP card says so" (device detail and Networks screen). *Audit proposal:* Compute `dhcpOff` on both callers with the gateway's rule (`!lan.dhcp \|\| lan.dhcp.enabled === false`), sharing the helper of `lib/networks.ts`; if the maintainer chooses option (a) of P5-AUDIT-10, the helper follows the new rule in one place. Test: a Playwright test with a sample network that has no `dhcp` block: its buttons are off and "DHCP is off in this network" is shown. It fails today. |
| P5-AUDIT-09 | major | The compact table of the device detail's HTTP exchanges panel renders 8 cells per row under 6 column headers. `web/src/components/intercept/FlowTable.vue:32,37,38` leave out the Device, Size and Took headers when compact and use a 6-column grid (:22); `FlowRow.vue:47-48` render the Size and Took cells with `role=cell` in every case and leave out only the Device cell (`v-if="!compact"`). Plan §2.17 ("the latest eight in the compact form of the table, without the device column") removes only the device column. Reproduced with a scratch Playwright spec: 6 columnheaders and 8 cells per row; the bytes sit under the empty Details header, the duration and the Details button wrap under Time and Kind, the request breaks over five lines, and screen readers get rows that do not match the headers. | **Fixed differently (f2e35a7):** Plan §2.17 removes only the device column, so the compact table keeps Size and Took: `FlowTable.vue` gives the compact header and grid those two columns (8 columns, `min-w-[700px]`) instead of leaving the cells out. Test: the e2e test of the device detail's exchanges panel (`interception.spec.ts`) checks the eight columnheaders and eight cells in each row. *Audit proposal:* Put `v-if="!compact"` on the Size and Took cells of `FlowRow.vue` (or give the compact grid and header those columns too). Extend the Playwright test of the device detail's exchanges panel with a check that each row has as many cells as the table has columnheaders. It fails today with 8 against 6. |
| P5-AUDIT-10 | major | The network form and the gateway disagree on what a missing `dhcp` block means, so a LAN added in the web form with DHCP on gets no DHCP. `networkFromForm` (`web/src/lib/networkForm.ts:464-482`) writes `enabled` only as `false` or as a `true` the network had before, and `compact()` returns undefined for an empty object, so a LAN saved with DHCP on and the defaults has no `dhcp` block (scratch vitest: `networkFromForm(emptyNetworkForm('lan','10.30.0.1/24'))` gives `{type:'lan',name:'lab2',address:'10.30.0.1/24',interfaces:[]}`). The compiler hands Kea no subnet for it (`compiler/dhcp.go:40`), and the engine, the warnings and `lib/networks.ts` read it as off; the spec's `DhcpScope.enabled` defaults to `true`, which reads as "on". The setup wizard writes `dhcp: {enabled: ...}` and is not affected. Found by the M23b lens; the root cause is in the Phase 3 network form. Needs the maintainer, see below. | **Fixed (decided by the maintainer on 2026-10-27, option (b)):** the gateway's rule stays; `networkFromForm` writes a `dhcp` block whenever DHCP is on, `formFromNetwork` reads a LAN without it as DHCP off through `dhcpServed`, and the spec's `DhcpScope` says that the block switches DHCP on. Tests: `networkForm.test.ts` "the dhcp block of a LAN (P5-AUDIT-10)" and the Playwright test "network forms: a test network added with DHCP on and its defaults is saved with a dhcp block, so the gateway serves it and its DHCP card is on (P5-AUDIT-10)". *Audit proposal:* Decide which side changes (options in "Phase 5 audit items for the maintainer"): (a) the gateway treats an absent block of a LAN as `dhcp: {}`, (b) the form writes `dhcp: {}` (or `enabled: true`) when DHCP is on and the spec says that an absent block means off. Either way a vitest of `networkFromForm` and, for (a), compiler and engine tests pin it. |
| P5-AUDIT-11 | minor | A `delay` DNS fault longer than three upstream timeouts sends the answer before the hostname-set report has gone through. `ServeDNS` bounds the query with `context.WithTimeout(..., 3*s.opt.Timeout)` (`internal/dnsproxy/proxy.go:173`, 6 s by default), the `delay` fault waits without that bound (proxy.go:306), and `s.report(ctx, ...)` (proxy.go:183) derives its context from the expired one (`hostsets.go:220`), so `reporter.report` returns `ctx.Err()` at once (hostsets.go:104-109) while the flush goroutine posts later, and the answer is written. This breaks §2.6 ("The set is updated before the answer is sent") and logs the warning for every such query; an upstream answering near the end of the window does the same. Reproduced in a scratch copy (Timeout 100 ms, delay 500 ms, a blocking sink: the answer arrives first). | **Fixed (f95cd8b):** `Server.report` takes its deadline (`callTimeout`, 2 s) from `context.Background()` when the answer is known; `ServeDNS` no longer hands it the query's upstream context. Test: `TestAnAnswerADelayFaultHoldsPastTheUpstreamBoundIsStillReportedBeforeItIsSent` (Timeout 100 ms, delay 500 ms, a blocking sink: the answer came first without the fix). *Audit proposal:* Give the report its own deadline from the moment the answer is known (`context.WithTimeout(context.Background(), callTimeout)` in `Server.report`), not one derived from the query's upstream context. Test (`dnsproxy/faults_test.go`): a delay longer than 3×Timeout with a hostname set and a blocking sink; the answer must not arrive before the sink is released. It fails today. |
| P5-AUDIT-12 | minor | The level 1 test of the added answer latency (§3.10) never measures a report. `TestTheAddedAnswerLatencyForHostnameSetNamesMeetsThePlanOnTheRealKernel` (`internal/api/e2e_dnsfaults_test.go:668-701`) asks 40 names under `*.set.test`, which dnsmasq answers all with 203.0.113.51 (`--address=/set.test/203.0.113.51`), from one client. The proxy's dedup key is the set, the client and the addresses, without the name (`internal/dnsproxy/hostsets.go:71-75`), and a key seen in the last minute is skipped (:86). So only the first query calls `POST /internal/dns/resolutions` and writes an element; the median measures the deduplicated path and passes whatever the report and netlink path cost, yet the acceptance map cites it as the proof. | **Fixed (f95cd8b):** each of the 40 measured names `hN.set.test` has an address of its own (`--address=/hN.set.test/198.18.0.N+1`), so every answer makes a report and a write, and the test asserts afterwards that the set holds at least one element of A per answer; the `plain.test` baseline stays. Level 1, vetted with `-tags testbed`, run by CI. *Audit proposal:* Give every measured name its own address (one `--address=/hN.set.test/203.0.113.N` per name), so each query makes a call and writes an element, and assert the set's element count (or the proxy's call count) after the run; keep the comparison with the `plain.test` names. |
| P5-AUDIT-13 | minor | A profile counter that adds DNS hits takes the DNS epoch, which hides a restart of its kernel counters. `withDNS` (`internal/api/overlays.go:170-187`) sets `out.Epoch = max(out.Epoch, oc.dnsEpoch)`; the kernel epoch is an engine generation (a small integer, `engine/overlays.go:822-866`), `dnsEpoch` a Unix time in milliseconds (`api/dns.go:517-536`), so the maximum is always the DNS epoch, which changes only when the API restarts. `stepHits.count` (`internal/run/zerohit.go:44-51`) relies on an epoch change to spot a counter that started over, so for a profile with a `dns` and a kernel part whose kernel fault is reborn mid-run (overridden, then back in a new generation) the new counter is subtracted from the old base: a false `zero_hit`, or a failed run with `zero_hit: error`. | **Fixed (f1fd57d):** `withDNS` and `withTLS` combine the epochs with `mixEpochs` (an FNV hash of both when both are set, the other one when one is 0), so a change of either part's epoch changes the counter's, and a reading is never subtracted across a kernel fault made anew. Test: `TestTheEpochOfAProfileCounterWithADNSPartChangesWhenItsKernelFaultIsMadeAnew` (api, internal: same parts give the same epoch, a reborn kernel fault or a restarted API another; fails without the fix). *Audit proposal:* Make the combined epoch change whenever either part's epoch changes (a pair encoded into one int64, or a hash), or give the DNS hits an epoch in the engine's generation space. Test (api or run): a profile with impairment and dns parts whose kernel fault is recreated mid-run keeps counting the step's packets and gives no `zero_hit`. It fails today. |
| P5-AUDIT-14 | minor | The DNS proxy keeps applying DNS faults that a gateway restart dropped, for up to 16 s after the API is back. `dnsproxy.Server.Follow` (`internal/dnsproxy/apiclient.go:120-155`) keeps the configuration in use while the API cannot be reached and doubles its pause while it is below 10 s (1, 2, 4, 8, then 16 s), so the cap is 16 s, not the 10 s the code reads as. Overlays are dropped on a restart (plan §1), the kernel faults go at the first apply and the TLS responder fails closed, but a device keeps getting, say, NXDOMAIN from a dropped DNS fault, and the query log names an overlay that no longer exists. | **Fixed (f95cd8b):** `Follow` doubles its pause from 1 s up to a true 10 s (`followPauseMax`) while no configuration has been in use and caps it at 2 s (`followPauseInUse`) once one has; documented in the M6b section. Test: `TestTheProxyAsksAgainWithinTwoSecondsOnceAConfigurationWasInUse` (fake clock: eight failed polls each followed by the next within 2 s, then the configuration without the dropped fault is in use; fails at the fourth attempt without the fix). *Audit proposal:* Cap the retry pause at a small value (2 s) once a configuration has been in use, or make the cap a true 10 s and document the window as a limit. Test with the fake clock: after a run of failed polls the next attempt comes within the cap, and a configuration without faults served then is in use within it. |
| P5-AUDIT-15 | minor | The per-overlay DNS and TLS hit counters grow without bound and are copied on every overlay read. `countFault` (`internal/api/dns.go:517-523`) and `countTLS` (`internal/api/tlsservice.go:336-342`) add one entry per overlay id that ever changed an answer or held a connection, and nothing deletes from `s.dns.faultHits` or `s.tls.hits`; `readOverlayContext` copies both maps in full under the log mutex (`overlays.go:85-90`) for every overlay list and run counter read while a DNS fault or TLS case is applied. The logs beside them are bounded (20000); a lab running scenarios around the clock accumulates entries for overlays long gone. | **Fixed differently (f1fd57d):** the counters are pruned to the overlays in the store (`snap.Overlays`), not to those in a DNS or TLS list: an overlay that is overridden for a while keeps its count, so its counter goes on with the same epoch when it is back (pruning it would restart the count under an unchanged epoch, a false `zero_hit`). The pruning runs at the first overlay read of a new snapshot (`dnsHitsSnapshot`, `tlsHitsSnapshot`, with or without a fault in force), and a read copies only the ids of `snap.DNSFaults` and `snap.TLSCases`. Test: `TestTheDNSAndTLSCountersOfARemovedOverlayAreForgotten` (a removed overlay's counter is gone, an overridden one keeps its count, the copies hold the overlays in a list only; fails without the fix). *Audit proposal:* Forget the counters of overlays no longer in the store (prune to the ids of `snap.DNSFaults`, `snap.TLSCases` and the running runs' overlays when a snapshot changes), and copy only the ids a view needs. Test: the counter of a removed overlay is gone after the next snapshot, one of an overlay in force keeps its count. |
| P5-AUDIT-16 | minor | `short_ttl` rewriting the authority and additional sections is untested; a mutation survives. `shorten()` (`internal/dnsproxy/faults.go:173`) rewrites Answer, Ns and Extra, and applies to negative answers too, where the SOA in Ns sets how long a client caches the NXDOMAIN or NODATA. `TestAShortTTLFaultRewritesTheTTLAndTheCacheKeepsTheRealOne` (`faults_test.go:267-289`) checks only `r.Answer[0]`; the mutation `[][]dns.RR{m.Answer, m.Ns, m.Extra}` to `[][]dns.RR{m.Answer}` passes the whole package (verified twice). | **Fixed (f95cd8b):** test `TestAShortTTLFaultRewritesTheAuthorityAndAdditionalSectionsAndNegativeAnswers`: the upstream answers with an NS in the authority and a glue A record in the additional section, and an NXDOMAIN with an SOA; every non-OPT record of the three sections has TTL 1 under `short_ttl` 1s. It fails under the mutation `[][]dns.RR{m.Answer}` (verified). *Audit proposal:* Let the upstream fake answer with an authority SOA and an additional record (and one NXDOMAIN with an SOA), and assert TTL 1 on every non-OPT record of all three sections under `short_ttl` 1s. The test fails under the mutation. |
| P5-AUDIT-17 | minor | Two-port topology: nothing in the kernel keeps the service namespace out of the management network, contrary to the plan's wording, and no engine test pins the API's judgment there. Plan §3.3 (`plan.md:937`), the `serviceForward` comment (`compiler/service.go:131-136`) and decision 13 say svc0 is never accepted towards the management network; in the two-port topology the management network lies behind the uplink, `meta iifname svc0 meta oifname <uplink> accept` (service.go:147) has no daddr exclusion, and the guard matches only `iifname @ifs_test` (rules.go:254-259). Only `AccessDecision` refuses a passed-through or intercepted connection there; it is covered at the domain level (`access_twoport_test.go`), but no `ResolveTLS` test runs with `TwoPort=true`. | **Fixed (e147b2d):** plan §3.3 (and the as-built note of M21), M21 decision 13 and the `serviceForward` comment now say that in the two-port topology the accept towards the uplink lets the namespace reach the management network behind it and only the API's `AccessDecision` (its management guard) keeps a passed-through or intercepted connection out (a guard by address in the kernel would also block a DNS upstream there). Test: `TestTheResponderKeepsAConnectionOutOfTheManagementNetworkBehindTheUplinkInTheTwoPortTopology` (engine, two-port harness, management sources `203.0.113.5/32` on `wan0`, with an allow rule for everything of the network so that the guard alone decides: a management source has `Passthrough=false` without a case and with an `intercept` case, the rest of the uplink `true`). Without the allow rule the matrix's default already refused the management source, so the test adds it; with the guard's two-port branch removed it fails. *Audit proposal:* Correct plan §3.3, decision 13 and the `serviceForward` comment: in the two-port topology the kernel accepts svc0 towards the uplink and thus the management network, and the API's `AccessDecision` alone refuses it (a kernel guard would also block a DNS upstream in the management network). Add an engine test in a two-port harness: `ResolveTLS` for a destination in the management sources has `Passthrough=false`, also for an intercept case. |
| P5-AUDIT-18 | minor | The TLS responder retries a batch of handshake reports the API refuses forever, blocking every later report. `reporter.flush` (`internal/tlsresponder/report.go:99-105`) keeps the head batch on any sink error; the API refuses content for good (`DisallowUnknownFields`, `api/helpers.go:130`; 422 for an unknown outcome or bad address, `tlsservice.go:229-241`). Until 5000 more handshakes push it out, no `tls_handshake` event is sent, the counters do not move and `tls_rejected`/`tls_accepted` checks time out undecided. The proxy's Batcher on the same endpoint drops such a batch for exactly this reason (`api.py:133-137,182-185`). The trigger is weak (the responder and the API ship in one image, so it takes a partial recreate or a bug). | **Fixed (a281240):** `svcapi.Failure` returns a `*svcapi.StatusError` (same message), and `svcapi.Refused` says whether an answer is a 4xx other than 408 and 429; `reporter.flush` drops such a batch with a warning and counts its reports (`Server.RefusedReports`), and keeps a batch only on a transport error, a 5xx, 408 or 429, as the proxy's batcher does. M21 decision 14 says so. Test: `TestABatchOfReportsTheAPIRefusesIsDroppedAndTheNextOneGoesThrough` (through the real `APIClient` against a fake API: 422 drops and counts the batch and the next report goes through alone; 503, 429 and 408 keep the batch and it is sent again; it failed without the fix: the refused batch stayed pending). *Audit proposal:* Classify the sink's error: drop the batch with a warning on a 4xx other than 408 and 429, as the Batcher's `is_rejection` does, and keep it only for transport errors and 5xx. Test: a sink that answers 422 to the first batch and accepts the next; the second report arrives and a refused counter says so. |
| P5-AUDIT-19 | minor | `docs/development.md` (M21) has two stale or broken statements. :5789-5790 still says the kernel side (the chain and marks, the redirect, the cut, the masquerade of a passed-through connection, the faults through svc0) is "written as assumptions ..., not observed" until CI is green, while plan M21's status (`plan.md:1587`) and this file record `M21_LEVEL1` green in runs 40196370688, 40258339299 and 40270141299. The acceptance map (:5812, row P2-M11-01) names `TestACustomProfileWithAnInterceptPartIsStoredButCannotBeActivatedAndOneWithADNSOrTLSPartCan`, which no longer exists; since M22 it is `TestACustomProfileWithAnInterceptPartIsAvailableAndActivatesLikeOneWithADNSOrTLSPart` (`internal/api/profiles_test.go:159`). | **Fixed (13ba588):** the sentence of M21's Tests in `docs/development.md` says that the kernel side (the chain and the marks, the redirect inside the namespace and `SO_ORIGINAL_DST`, the cut, the masquerade of a passed-through connection, the faults through `svc0`) is observed in CI and names the runs that ran `M21_LEVEL1` green (40196370688 on `6a0ec5e`, 40258339299 on `aebbae8`, 40270141299 on `eab804c`); the acceptance map's row P2-M11-01 names `TestACustomProfileWithAnInterceptPartIsAvailableAndActivatesLikeOneWithADNSOrTLSPart`. Doc fix. *Audit proposal:* Replace the sentence with the CI runs that observed the kernel side, and the test name with the current one. Doc fix. |
| P5-AUDIT-20 | minor | A key log line that arrives while a capture ends can overwrite its final record with the running one and leave secrets in an unmarked capture. `Manager.AppendKeylog` (`internal/capturemgr/keylog.go:65-77`) picks the running, not-ending captures under `m.mu` and releases it; `appendKeylog` writes `keylog.txt` without checking `c.ending` again (:86-108) and calls `m.save(c)` whenever `first` is true (:111-116), even when `ending` kept it from setting the flags. `finish` (`manager.go:610-651`) writes the final record from a copy and assigns `c.rec = v` only afterwards, so a `save` in between writes the running record over the stopped one, and `Recover` later marks a normally stopped capture `failed`. This contradicts keylog.go:51 ("A capture that has ended, or is ending, does not take them") and manager.go:645-646. The download stays refused (`openKeylog` checks `rec.Keylog`). | **Fixed (82cec72):** `appendKeylog` checks again under `m.mu` that the capture is not ending (before it opens the file and again before it writes), sets `keylog` and `sensitive` before it writes the first line, and saves the running record through `saveUnlessEnding`, which checks `ending` and writes in one step under the lock of the record's file, so `finish`'s final record (written under the same lock after it set `ending`, from a copy that has the flags) always comes last. A test hook between the choice of the captures and the writes (`keylogTargetsChosen`, set in `export_test.go`) makes the race reproducible. Development.md (the key log) says so. Tests: `TestACaptureThatEndsWhileABatchOfKeyLinesIsOnItsWayTakesNone` (no `keylog.txt`, the record stopped and unmarked; failed without the fix: the file was written) and `TestKeyLinesThatArriveWhileACaptureEndsNeverLeaveItRunningOnDisk` (50 rounds of concurrent batches and an end: the record on disk is stopped, and marked when the file holds lines; failed without the fix in about one round of 25). *Audit proposal:* In `appendKeylog`, check `c.ending` under `m.mu` before opening the file and return when the capture is ending; call `save` only when the flags were set. Tests: `AppendKeylog` on an ending capture creates no `keylog.txt` and leaves the record unchanged; a concurrent test of `AppendKeylog` and `finish` never finds a `running` record on disk after `finish` returned. |
| P5-AUDIT-21 | minor | Eleven real-mitmproxy tests wait a fixed 500 ms for the proxy to pick up the rules instead of using `waitApplied`. `internal/tlsresponder/mitm_test.go:448, 534, 736, 768, 804, 889, 946, 996` (in a loop) and `mitm_headers_test.go:28, 74, 198` call `ir.gw.setRules(...)` and then `time.Sleep(500 * time.Millisecond)`; the fake gateway answers the long poll in 100 ms steps and the addon must parse and apply the rules, and `WaitReady` only waits for the TCP listener. `fakeGateway.waitApplied` (mitm_test.go:210) exists and `mitm_upstream_test.go` and `mitm_h2abort_test.go` use it. Several of these tests are in `M22_LEVEL1` and run on the loaded testbed and VM runners, where a slow first fetch serves the first request without the rule. No flake from it has been observed. | **Fixed (6969798):** the ten 500 ms sleeps after `setRules` and the start of a rig in `mitm_test.go` and `mitm_headers_test.go`, the 700 ms one after the host rule in `TestAConnectionThatIsNoHTTPIsCountedAndAbortedByAHostRule`, the 600 ms one after `keylog: false` and the five 300 ms waits after `newInterceptRig` are `ir.gw.waitApplied(t)`. The twelfth sleep (`mitm_test.go:996`) is a retry pause of the loop that waits for the new test CA, not a wait for the rules, and stays; the negative wait for no key lines (1.5 s) stays too. No test can fail without this fix (it removes a timing dependence); the package passes with the real mitmproxy and three of the tests passed five runs each beside the API's race run. *Audit proposal:* Replace each sleep after `setRules` or `setUpstream` with `ir.gw.waitApplied(t)`. A stress run (`go test -count=20` of two of these tests next to a CPU load) then no longer depends on timing. |
| P5-AUDIT-22 | minor | The image's own descriptions still say mitmproxy comes only with M28. `deploy/Dockerfile:3-5` says "M1 builds what exists so far, chaosgw and chaosctl; the full image with Kea, BIRD, tcpdump and the mitmproxy sidecar follows with M28", while the same file installs pinned Kea, BIRD and mitmproxy in `/opt/chaosgw/tls-proxy/venv` with the addon (:41-56) and `compose.tlsproxy.yaml` runs it; `docs/development.md:6433` (image workflow) says "The image holds `chaosgw` and `chaosctl` only until M28 adds the rest". (Found by the M22 and the docs lens; merged.) | **Fixed (13ba588):** the header of `deploy/Dockerfile` and the image section of `docs/development.md` say that the image holds `chaosgw`, `chaosctl`, the pinned Kea and BIRD and the interception proxy (mitmproxy in `/opt/chaosgw/tls-proxy/venv` with the addon, M22), and that M28 adds tcpdump and the rest of the container deployment. Comment and doc fix (`tools/ciconfig`, which reads the Dockerfile, passes). *Audit proposal:* Say that the image holds chaosgw, chaosctl, Kea, BIRD and the interception proxy (mitmproxy with the addon, M22), and that M28 adds tcpdump and the rest. Comment and doc fix. |
| P5-AUDIT-23 | minor | M22's Tests line in the plan and its acceptance map mark finished, CI-green work as "not run yet" or "To do". `plan.md:1591` says "**Done, level 1 not run yet (P5-M21-02):** an `intercept` case to a server in another test network and to one behind a WireGuard client works", while the M22 status (:1593) and P5-M21-02 record the test green in run 40258339299; `development.md:5957-5959` label three rows "To do (P5-M22-03)" / "To do (P5-M21-02)" although every test they name exists and is in `M22_LEVEL1` or passes at level 0; `plan.md:1587` still calls that test M22's own "To do". | **Fixed (13ba588):** plan M22's Tests say "**Done (P5-M21-02):** ... (the level 1 test of M21 with the proxy, green in CI run 40258339299 ...)", the M21 status says that test is M22's "done and green in CI run 40258339299" instead of M22's own "To do", and the three rows of M22's acceptance map in `docs/development.md` are labelled "P5-M22-03" and "P5-M21-02" without "To do". Doc fix. *Audit proposal:* "**Done (P5-M21-02):** ... (level 1, green in CI run 40258339299)" at :1591, drop the "To do" prefixes at development.md:5957-5959, and update the M21 status sentence at :1587. Doc fix. |
| P5-AUDIT-24 | minor | The comment above `M22_LEVEL1` (`.github/level1-tests.env`) miscounts its level 1 tests and leaves out a file. It says "Eight of them are level 1 tests on the real kernel (internal/api/e2e_intercept_test.go)" and names `mitm_test.go`, `mitm_upstream_test.go` and `mitm_h2abort_test.go`; the list holds nine tests of `e2e_intercept_test.go` (M23c added `TestChaosctlMakesAnInterceptionCaseAndReadsItsExchangesAndTheCAOnTheRealKernel`) and four of `mitm_headers_test.go`. | **Fixed (13ba588):** the comment above `M22_LEVEL1` counts nine level 1 tests of `e2e_intercept_test.go` (the first five, the three after the first level 0 tests, and the one of M23c further on, `TestChaosctlMakesAnInterceptionCaseAndReadsItsExchangesAndTheCAOnTheRealKernel`, with what it covers); `mitm_headers_test.go` was already named (P5-AUDIT-05 added it). Checked against the list: 9 tests of `e2e_intercept_test.go`, 27 level 0 tests of the five `mitm*_test.go` files. Comment fix. *Audit proposal:* "Nine of them ..., and the chaosctl flow of M23c", and add `mitm_headers_test.go` to the files named. Comment fix. |
| P5-AUDIT-25 | minor | Nothing carries the promised release-notes warning that the proxy is not validated on ARM64. The P5-M22-02 decision and the M22 status (`plan.md:1593`) say that until H1 has measured mitmproxy "the release notes say that the proxy is not validated on ARM64"; no release notes exist, M30's Scope (`plan.md:1653-1656`), the milestone that writes them, does not mention the note, and `docs/install.md:125-141` says nothing about ARM64, although the image is multi-arch and the arm64 job builds the venv. | **Fixed (13ba588):** M30's Scope in `docs/plan.md` says that the release notes name what is unvalidated on ARM64 until H1 has run: the Raspberry Pi targets of §3.10 and the interception proxy (P5-M22-02). `docs/install.md` section 3 says under `compose.tlsproxy.yaml` that the proxy's image is built for ARM64 but the proxy is not validated on ARM64 hardware until the hardware lab (H1) has measured it. Plan and doc fix. *Audit proposal:* Add to M30's Scope: "the release notes name what is unvalidated on ARM64 until H1 has run: the §3.10 targets and the interception proxy (P5-M22-02)", and optionally one sentence under `compose.tlsproxy.yaml` in install.md section 3. Plan and doc fix. |
| P5-AUDIT-26 | minor | `docs/install.md` tells the operator to mount a CA file for `CHAOSGW_UPSTREAM_CA_FILE`, but neither it nor the compose file says how. install.md:130-131 says "a CA file in a volume you mount into the container"; `deploy/compose.tlsproxy.yaml` passes only the variable, and its container is `read_only` and mounts only `chaosgw-service` and `chaosgw-tlsca`. The launcher passes the path to mitmproxy unchecked (`launcher.py:76-77`), and mitmproxy reads it per connection, so a host path in `.env` gives a healthy container whose every upstream handshake fails with 502 "TLS to the server failed" (the refusal `TestACaseWhoseCAFileCannotBeReadIsRefusedAndNeverServedUnverified` pins). | **Fixed differently (d496742):** install.md section 3 shows the mount as a compose file of the operator's own (`deploy/compose.local.yaml`: the variable set to the path in the container, a read-only bind of the file, readable by 65532) named in `COMPOSE_FILE` after `compose.tlsproxy.yaml`, not as `compose.override.yaml`: with `COMPOSE_FILE` set, as section 3 sets it, Compose does not read an override file by itself. The launcher warns at its start when the file cannot be read or holds no PEM certificate (`check_upstream_ca_file`); the compose file's comment points to install.md. Tests: `test_launcher.py` `UpstreamCAFileTests` (a missing file and one without a certificate are warned of, a readable one and none are not). *Audit proposal:* Add a `compose.override.yaml` example to install.md section 3 (a read-only bind mount of the CA file for `tls-proxy` and the variable set to the in-container path), and let the launcher log a warning when the named file cannot be read at start (a level 0 test of the launcher pins it). |
| P5-AUDIT-27 | minor | An identity-only (incremental) apply never resends the DHCP configuration, though the DHCP actions depend on the identity. `internal/engine/applyloop.go:202-212` calls `syncDHCP` only when `!incremental`, and `identityOps` and `sameFaultStructure` (:344-434) never compare `Target.Kea`; yet `compileDHCPActions` resolves a discovered device through `in.Identity.Discovered` (`compiler/dhcpaction.go:77-83`), and the retry (`resendDHCP`) resends the old `e.dhcp.target`. Meanwhile the snapshot's winners and problems come from the new target. Reproduced: a discovered device silenced while in Lab (DHCP off) shows up in IoT; the warning goes and the overlay reads as in force, but Kea saw no new Apply and its `Silenced` stays empty. The fix of P5-AUDIT-07 makes this reachable for configured devices too. | **Fixed (0dae17e):** The apply loop keeps the target applied last and, after an incremental (identity-only) apply, calls `syncDHCP` when `dhcpChanged` says the DHCP part differs (the rendered Kea document, or the deletions by key, subnet and MACs); the retry then resends the new target. Development.md (the Kea paragraph of M23) says so. Test: `TestAnIdentityUpdateThatMovesADeviceIntoANetworkWithDHCPSendsTheServerItsAction` (internal/engine: a configured device without `network`, silenced while seen in Lab, shows up in IoT with a lease in the same revision; the server gets its MAC in DROP); it failed without the fix. *Audit proposal:* In the incremental branch, compare the Kea document (and the deletions) of the last and the new target and call `syncDHCP` when they differ. Test: the scenario above as an engine test, asserting the DROP MAC in Kea's configuration after the identity update. It fails today. |
| P5-AUDIT-28 | minor | Concurrent `force_new_ip` writes for different devices in one put batch are all given the same address. `completeDHCPAction` runs in `validPut` and reads the taken addresses from `o.ov.list` (`internal/engine/dhcpaction.go:144-153`), which is refreshed only in `finishBatch` (`overlays.go:495-507,573`), so every write of a batch picks from the same stale list, against the rule "never the address of another overlay of the kind" (development.md, M23). With a running Kea the config-test refuses the duplicate (checked with Kea 3.0.3) and the batch is split; when Kea is down or the 3 s check times out, `dhcpProblems` accepts it (overlays.go:436-444), the duplicate is stored, and Kea refuses every configuration until one overlay goes. Reproduced: 12 parallel writes all get 10.10.0.254. | **Fixed (0dae17e):** `dhcpTaken` reads the overlays from the store (`o.ov.store.Overlays()`), which holds the writes of the open batch, instead of `o.ov.list`, the list of the last finished batch; a batch taken back is made one write at a time with the store restored, so each write sees the ones before it there as well. Development.md says so. Test: `TestConcurrentForceNewIPWritesGiveEachDeviceItsOwnAddress` (12 parallel writes for 12 devices, a fake that accepts everything: 12 distinct addresses and the same reservations at the server); it failed without the fix (all got 10.10.0.254). *Audit proposal:* In `dhcpTaken`, read the overlays from the store (`o.ov.store.Overlays()`) instead of `o.ov.list`, or add the addresses already handed out in the open batch. Test: N parallel `force_new_ip` writes for N devices with a DHCP fake that accepts everything give N distinct addresses. It fails today. |
| P5-AUDIT-29 | minor | No test covers `force_new_ip` avoiding the address another `force_new_ip` overlay holds; a mutation survives. `dhcpTaken` (`internal/engine/dhcpaction.go:143-153`) marks the addresses other `dhcp` overlays move devices to (development.md:5997), but every `force_new_ip` test moves one device. Replacing `taken[a] = true` with `_ = a` passes the whole `internal/engine` package (verified twice); in a scratch test two devices then both get 10.10.0.254 instead of .254 and .253. | **Fixed (0dae17e):** `TestForceNewIPOfTwoDevicesGivesEachItsOwnAddress` (internal/engine): two devices in IoT get 10.10.0.254 and .253 and two reservations; the first written again keeps its address (the `continue` for the same device), an explicit `ip` of the other device's overlay is refused and the device's own is not. It fails under both mutations (`taken[a] = true` replaced by `_ = a`; the `continue` disabled). *Audit proposal:* Add `TestForceNewIPOfTwoDevicesGivesEachItsOwnAddress` (two configured devices in IoT, `force_new_ip` on both: different `ip` fields and two Kea reservations with different addresses), and check that rewriting the first device's own action may reuse its address (the `continue` for the same device). It fails under the mutation. |
| P5-AUDIT-30 | minor | `delete_lease` runs again when its key drops out of the target and comes back. `Engine.deleteLeases` (`internal/engine/dhcpaction.go:190-212`) forgets every done key the current target does not list, and is called with a nil target when no network serves DHCP; the compiler leaves a deletion out while it is inactive (`compiler/dhcpaction.go:178-190`: DHCP off, a device without a MAC or unknown). When it is listed again under the same key (overlay id plus `UpdatedAt`), it runs a second time: within a 10 min TTL, a revision with IoT's DHCP off and then on wipes IoT's leases again and emits a second `dhcp_leases_deleted`. Decision 9 promises one deletion per write; the sentence "a key the target no longer lists is forgotten" conflicts with it, and `TestDeleteLeaseDeletesOnceWhenItIsWrittenAndAgainWhenItIsWrittenAgain` does not cover the case. | **Fixed (0dae17e):** A done deletion is remembered with its overlay (`dhcpState.deleted` maps the key to the overlay id) and forgotten only when the overlay is gone from the desired state of an apply (`Engine.forgetLeaseDeletions`, called by the apply loop) and the target the server is given no longer lists it (so the retry, which resends the last target until a newer one replaces it, cannot delete twice); a target that leaves it out while its network has DHCP off no longer forgets it. Development.md (the `delete_lease` paragraph) is corrected. Test: `TestADeleteLeaseIsNotDoneAgainWhenItsNetworkHasDHCPOffAndOnAgain` (one deletion and one event after DHCP off and on; the overlay removed and written anew deletes again); it failed without the fix with two. *Audit proposal:* Keep a done key as long as its overlay exists (forget it only when the overlay id is gone from the store), and correct the sentence in development.md. Test: `delete_lease` on IoT, then DHCP off and on: one deletion and one event. It fails today with two. |
| P5-AUDIT-31 | minor | New capture offers the clients of a management WireGuard network as a Device and as a selector source, which the gateway always refuses. `CaptureDialog.vue` filters only the networks (`recordable`, ~78-80); `deviceOptions` and the `device:` entries of `sourceOptions` list every device, and the clients of every hub are devices (`domain/index.go:293-305`), the management hubs included. The gateway refuses them with `management_network` (`internal/capturemgr/resolve.go:17-18,118-121`, P4-AUDIT-16), and the device detail's Capture button stays enabled for such a client. Decision 4 of M23a and plan §2.17 ("so the refusal is not the way to find that out") aim at exactly this; the Playwright test (`web/e2e/captures.spec.ts:469`) checks only the network choices. | **Fixed (f2e35a7):** `isRecordable` and `isRecordableDevice` in `lib/captures.ts`: `CaptureDialog.vue` leaves the clients of a management WireGuard network out of the devices and the selector sources, and `CapturePanel.vue` turns Capture off for such a device with a sentence ("…its packets are the admin's tunnel, which the gateway does not record"). Test: e2e (`captures.spec.ts`) "New capture never offers a client of the admin tunnel as a device or a source, and its device page says why it cannot be captured". *Audit proposal:* Leave the devices whose network is a WireGuard network of the role management out of `deviceOptions` and the selector sources, and disable Capture in `CapturePanel.vue` for such a device with a sentence. Extend the Playwright test with a management client that is not offered. It fails today. |
| P5-AUDIT-32 | minor | The DHCP card says "No action is in force" while the overlays are still loading. `web/src/components/dhcp/DhcpActionsCard.vue:130-131` handles the error (`firstError(overlays)`) and then the empty list, with no loading branch, so on a slow gateway the device detail and the Networks screen say there is no action while the read lasts (reproduced with `gw.holding['/overlays']`), against the rule that a loading query must not read as "none" (the fault list just above says "Loading…", DeviceDetailView.vue:276). | **Fixed (f2e35a7):** `DhcpActionsCard.vue` says "Loading…" (`role=status`) while the overlays load, before the empty state. Test: e2e (`app-layer-forms.spec.ts`) "while the overlays load, the DHCP card says so instead of “No action is in force”" (holds `/overlays`). *Audit proposal:* Add `<p v-else-if="overlays.isLoading.value" role="status">Loading…</p>` before the empty state. Playwright test: hold `/overlays`, expect Loading and no `dhcp-none`. |
| P5-AUDIT-33 | minor | A TLS case with a network destination shows the network's UUID in its chip. `tlsChips` (`web/src/lib/faults.ts:123`) writes `network ${t.destination.network}` and gets no name resolver, while the gateway stores the reference as a UUID (`domain/refs.go:231` `visitTLS`); for a `tls` overlay `describeMatch` is empty, so the chip is the only place the destination shows: the Faults screen, the device detail, the Interception screen and the scenario timeline (`scenarios.ts:165`) read "TLS expired · network 5d1f0c2e-…" where the dialog's preview said "to IoT". | **Fixed (f2e35a7):** `tlsChips` takes the names and writes the network's name (its UUID only when the name is not at hand); `overlayChips` passes them on (a third parameter), and the faults screen, the device detail, the devices list (`FaultBodies.names`), `useActiveFaults` (the Interception screen, the overview) and `stepText` give them. Test: vitest `faults.test.ts` "name the network a TLS case is aimed at, not its UUID" and a case in `scenarios.test.ts` ("TLS expired · network IoT"). *Audit proposal:* Pass the name resolver into `tlsChips` (as `describeDestination` gets it) and write the network's name. Vitest in `faults.test.ts`: a TLS case with `destination: {network: <id>}` and a names map gives "network IoT". |
| P5-AUDIT-34 | minor | `chaosctl dhcp set` without a target names flags the command does not have. `dhcp set` registers only `--device` and `--network` (`internal/ctl/overlays.go:39-42`, `addDeviceOrNetwork`, whose comment claims the message "then names exactly one of the two"), but the shared `targetFlags.scope()` (:58-59) fails with "name exactly one target: --device, --group, --network or --global"; `--group` then gives "unknown flag". | **Fixed (52fadd7):** `addDeviceOrNetwork` marks the `targetFlags` it registers, and `scope()` then fails with "name exactly one target: --device or --network"; the other commands still name all four targets. `TestADHCPActionWithoutATargetNamesTheTargetFlagsTheCommandHas` (internal/ctl/appfamilies_test.go) checks no target and two targets, that `--group` and `--global` are neither registered nor named, and the message of the four-target commands; it fails without the fix. development.md (M23b, `chaosctl`) says it. *Audit proposal:* Let `scope()` name only the registered target flags (remember in `targetFlags` that `addDeviceOrNetwork` was used), so the message is "name exactly one target: --device or --network". A case in `appfamilies_test.go` checks it. |
| P5-AUDIT-35 | minor | `docs/development.md:6271` still counts 19 Playwright tests in `e2e/app-layer-forms.spec.ts` and does not name the test added for P5-M23b-02. The file has 20 tests since `eab804c` added "a device that is in no network the gateway serves says so under its DHCP card, and its buttons are off"; the Tests paragraph and the acceptance map of M23b do not name it, although plan M23b's status says the text comes "with its Playwright test". | **Fixed differently (13ba588):** the Tests paragraph of M23b counts 22 Playwright tests in `e2e/app-layer-forms.spec.ts` (the file has 22 since the fix round added the tests of P5-AUDIT-08 and -32 to the 20 of `eab804c`) and names the test of P5-M23b-02 and the two of the audit; the acceptance map's DHCP row names `a device that is in no network the gateway serves says so under its DHCP card, and its buttons are off`. The same kind of stale count was corrected for M23a (`e2e/captures.spec.ts`, 20 tests, with the test of P5-AUDIT-31) and M23c (`e2e/interception.spec.ts`, 17 tests, with the test of P5-AUDIT-36). Doc fix. *Audit proposal:* Change the count to 20 and add the test to the DHCP card's Playwright tests (after "the buttons that are off ..."). Doc fix. |
| P5-AUDIT-36 | minor | The Interception screen says "No interception case is in force" when the overlays failed to load but configured faults did. `web/src/views/InterceptView.vue:136` shows the LoadError only when `faults.error.value && !faults.items.value.length`; `useActiveFaults().items` (`composables/gateway.ts:388-430`) holds the overlays and the enabled configured faults, while the cases are overlays only (:94). With `/overlays` failing and one configured fault loaded, line 137 tells the tester that no device's traffic goes through the proxy (reproduced with `MockGateway.sample()` and `failing['/overlays']=500`), against the rule that a failed query must not read as "none". | **Fixed (f2e35a7):** The card "Interception in force" of `InterceptView.vue` is based on the overlays query (`useOverlays`, the same cache): Loading while it loads, `LoadError` with Try again when it failed without data, whatever the configured faults did. Test: e2e (`interception.spec.ts`) "the cases in force are not called none when the overlays cannot be read but the configured faults can" (`failing['/overlays']=500`, then Try again). *Audit proposal:* Base the card on the overlays query (expose its error from `useActiveFaults` or use `firstError(overlays)`) and offer retry. Playwright test with `gw.failing['/overlays']=500` on the sample gateway: the load error shows and `no-cases` does not. |
| P5-AUDIT-37 | minor | The "Replace the test CA?" question shows the previous refusal again when it is reopened. `TestCaCard.vue` clears `error` only when `regenerate()` starts (:33), and the open button only sets `askOpen = true`, so after a refusal, Cancel and reopen show the old alert before anything was sent (reproduced with `gw.rejectRegenerate`). Plan §2.17 ("A refusal of the gateway stays in the question") means the attempt that was refused. | **Fixed (f2e35a7):** `TestCaCard.vue` clears the refusal whenever the question opens (a watch on `askOpen`, so every way of opening is covered). Test: the e2e test of the test CA (`interception.spec.ts`): refusal, Cancel, reopen, no alert. *Audit proposal:* Clear `error` when the question opens (`@click="error = ''; askOpen = true"` or a watch on `askOpen`). Extend the Playwright test of the test CA: after a refusal, Cancel and reopen, no alert. |
| P5-AUDIT-38 | minor | The messages of the `modify` rule (headers, replacements, "changes nothing") and of "cut existing" are not tied to any control. In `web/src/components/faults/InterceptFields.vue` the header inputs (:217-218), the replacement inputs (:232-233) and the rule's messages (:210, 224, 236) have no `aria-invalid` and no `aria-describedby`, and the messages have no id; the rate field in the same file has both. In `TlsFields.vue:90,96` the cut-existing checkbox has `aria-invalid` but no `aria-describedby`. M23b's rule ("Labels and messages are tied as everywhere") covers them; `e2e/interception.spec.ts:137,141` check them only with `toBeVisible`. | **Fixed (f2e35a7):** `InterceptFields.vue` gives the messages of a modify rule ids; the inputs of a header list and of the replacements are described by their list's message, and the row the message names (`headerProblemRow`, `replaceProblemRow` in `lib/interceptForm.ts`; every row when the gateway named only the list) is `aria-invalid`; "changes nothing" describes the rule's Action, which is then `aria-invalid`. `TlsFields.vue` ties the cut-existing checkbox to its message. Test: `interception.spec.ts` uses `toHaveAccessibleDescription` (and `aria-invalid`) for both messages; `app-layer-forms.spec.ts` checks a refusal of `/tls/cut_existing` on the checkbox. *Audit proposal:* Give each message an id, point the row's inputs (and the checkbox) at it with `aria-describedby` and set `aria-invalid` where the message names the row. Change the assertions at interception.spec.ts:137/141 to `toHaveAccessibleDescription`; they fail today. |
| P5-AUDIT-39 | minor | `chaosctl intercept flows --json` drops the API page's `next_cursor`, and the table does not say that older exchanges exist. `internal/ctl/intercept.go:375` prints `model.InterceptFlowPage{Items: items}` without `NextCursor`, also without `--all`, where only the first page (default 50) is read; plan §2.17 and development.md (M23c, :6317) say "with `--json`, the page of the API". `printFlows` says nothing when more exist. | **Fixed (52fadd7):** without `--all`, `--json` prints the API page as it came, `next_cursor` included; with `--all` it prints the exchanges of every page and no cursor (none is left). The table writes "more exchanges: use --all or a larger --limit" on stderr when the page had a cursor and `--all` was not given. `TestChaosctlListsTheExchangesOfTheProxyWithTheFiltersOfTheAPI` now checks `--json --limit 2` (a cursor), `--limit 2 --all` and the whole log in one page (none), and the hint with `--limit 2` and its absence without a cursor or with `--all`; it fails without the fix. development.md (M23c, `chaosctl`) says it. *Audit proposal:* Without `--all`, print the API page unchanged, `next_cursor` included; in the table, add a line on stderr ("more exchanges: use --all or a larger --limit") when a cursor came back. Extend `TestChaosctlListsTheExchangesOfTheProxyWithTheFiltersOfTheAPI`: `--json --limit 2` on a longer log has `next_cursor`. |
| P5-AUDIT-40 | minor | `chaosctl intercept flows` writes device-controlled host, path and method to the terminal unescaped. The proxy reports `host` (`req.pretty_host`), `path` and `method` as the device sent them (`addon.py:450-456`; the SNI for TCP streams, :539-541), the API stores them as they are (`flowOf`, `internal/api/intercept.go:178-192`), and `printFlows`/`flowRequest` (`internal/ctl/intercept.go:405-470`) print them raw. mitmproxy's HTTP/1 reader accepts ESC and BEL in the request target and in the Host header (checked with mitmproxy 12.2.3). A device under an interception case can rewrite the admin's window title, hide rows (an exchange with a `block` action) or, where OSC 52 is honoured, write to the clipboard. Plan §2.16 treats devices as untrusted; the web UI renders the strings as text. | **Fixed (52fadd7):** `printable` (internal/ctl/intercept.go) escapes every rune outside `unicode.IsPrint` as `\xNN` (`\uNNNN` above U+00FF) and every byte of invalid UTF-8, and the table passes every string column through it (the request with host, path, method and error, the device or client, the kind, the actions); a tab is escaped too, so a device cannot shift the columns. `--json` keeps the strings, JSON-encoded. `TestChaosctlDoesNotPrintTheControlCharactersADeviceSent` (internal/api/ctl_intercept_test.go) reports an HTTP exchange with ESC and BEL in host, path and method (a window-title OSC and line erasures) and a stream with BEL in the host and an OSC 52 in the error: the table holds no raw ESC or BEL and shows them escaped, `--json` has the path as sent; it fails without the fix. development.md (M23c) names the test. *Audit proposal:* Escape non-printable runes (outside `unicode.IsPrint`, as `\xNN`) in every device-supplied field of the table output (host, path, method, error, original_dst); `--json` stays as it is. Test: an in-process `chaosctl intercept flows` test with ESC and BEL in path and host asserts that the output holds no raw ESC or BEL byte. |
| P5-AUDIT-41 | minor | `docs/plan.md` still says PR #120 is not merged. PR #120 was merged on 2026-10-25 (`767af94` on `main`), and this file says so, but `plan.md:5` (the status line) says "Phase 5 (M20 to M23a) implemented on the branch `phase-5` (PR #120, not merged; ...)" and the M20, M23 and M23a statuses (:1576, :1599, :1605) say "on the branch `phase-5` (PR #120, not merged ...)", while the M21 and M22 statuses already say "the first part is on `main` with PR #120". (Found by the M20 and the docs lens; merged.) | **Fixed (13ba588):** the statuses of M20, M23 and M23a say "on `main` (PR #120, merged 2026-10-25, `767af94`; CI run 40196370688 on `6a0ec5e` green)", and the status line (`plan.md:5`) says the same for M20 to M23a and gives the state of `phase-5b` with the audit and the nightly runs; `grep -rn "PR #120, not merged" docs/` finds only this row and the audit's summary. Doc fix. *Audit proposal:* Change the four places to "on `main` (PR #120, merged 2026-10-25, `767af94`; CI run 40196370688 on `6a0ec5e` green)", and give the status line the state after the PR #121 merge when the phase closes. A grep for "PR #120, not merged" in `docs/` finds nothing. |
| P5-AUDIT-42 | minor | `docs/development.md`'s CI section leaves `M22_LEVEL1` and `M23_LEVEL1` out of the `-expect-pass` list it quotes. :6400 quotes `-expect-pass $M35A_LEVEL1,...,$M20_LEVEL1,$M21_LEVEL1,$I64_LEVEL1,$KEA_LEVEL1,$I79_LEVEL1`, and the prose names no M23 list, while `.github/workflows/ci.yml:101,145` and `nightly.yml:128` pass `$M22_LEVEL1,$M23_LEVEL1` too. The workflows are right; a maintainer who edits them from the document could drop the two lists and let their tests skip unnoticed, the gap P4-AUDIT-49 closed. | **Fixed (13ba588):** `docs/development.md`'s CI section quotes `-expect-pass` as `ci.yml` and `nightly.yml` have it (with `$M22_LEVEL1,$M23_LEVEL1`) and names "those of the DHCP test actions (M23, udhcpc and the real Kea)". The optional check is taken: `tools/ciconfig` `TestTheDevelopmentGuideQuotesEveryLevel1List` checks that the quoted list names every list of `.github/level1-tests.env` and no other; it fails without the doc fix (it names `$M22_LEVEL1` and `$M23_LEVEL1` as missing). *Audit proposal:* Quote the list as the workflows have it and name "those of the DHCP test actions (M23, udhcpc and the real Kea)" in the sentence. Doc fix (optionally `tools/ciconfig` checks that the quoted list equals the workflows'). |
| P5-AUDIT-43 | minor | The nightly kernel matrix has never run the Phase 5 level 1 lists. `nightly.yml:128` runs `M20_LEVEL1` to `M23_LEVEL1` on 6.8.0-142-generic and 7.0.0-38-generic, but the latest Nightly is 40173399853 (2026-10-25 07:36, on `e439e16`), before PR #120 reached `main`, and none ran on `phase-5b`. The CI VM job uses 6.8.0-142-generic (`internal/testbed/vmrun/run.go:23`) and the privileged job the runner's kernel, so no Phase 5 level 1 test, nor the nft sets, redirects and tc paths they use, has run on 7.0. The plan statuses say "wired into ... the nightly matrix", not that it ran; the Phase 3.1 close ran the nightly before its merge (P31-AUDIT-04). | **Fixed (13ba588; run 40314226553):** the nightly on `main` after the merge of PR #120 (run 40288147095, scheduled, on `767af94`) ran the Phase 5 lists of that part on both kernels: every test passed on 6.8.0-142-generic, and on 7.0.0-38-generic one failed, `TestALeaseClassGivesItsClientsTheirOwnLeaseTimeAndNoOtherClient` (`M23_LEVEL1`, the real Kea on the loopback): the client asked for an hour 21 ms after it took its 9 s lease, Kea's lease caching answered with what was left of the lease, and the clock had crossed a second in between (8 s). Not a kernel matter but a flaky test, reproduced here (2 failures in 150 runs against the real Kea); the test now asks for the hour before it takes the lease (300 runs green). `gh workflow run nightly.yml --ref phase-5b` then ran on `eab804c` (run 40314226553, 2026-10-26): green, 1917 tests passed, none failed or skipped, on 6.8.0-142-generic and on 7.0.0-38-generic, `-expect-pass` with every list, `M20_LEVEL1` to `M23_LEVEL1` included; the appliance jobs green (the fuzz job runs only on the schedule). The run is recorded in the plan's status line. The commits of the fix round after `eab804c` were not on `origin/phase-5b` then; CI on PR #121 runs them, and the nightly runs again on `main` after the merge. *Audit proposal:* Before the merge, run `gh workflow run nightly.yml --ref phase-5b`, record the run in this section and in the plan's status line, and fix what fails there before closing. No code change unless that run fails. |

### Per milestone

| Milestone | Verdict | Audit items (state after the round of 2026-10-26) |
|---|---|---|
| M20 DNS faults and hostname selectors | done with gaps | P5-AUDIT-01 (major); -11, -12, -13, -14, -15, -16: all fixed (-15 differently) |
| M21 TLS responder, certificate cases, pass-through | done with gaps | P5-AUDIT-02, -03 (major); -17, -18, -19: all fixed |
| M22 TLS interception and HTTP faults, the proxy sidecar | done with gaps | P5-AUDIT-04, -05, -06 (major); -20, -21, -22, -23, -24, -25, -26: all fixed (-26 differently) |
| M23 DHCP test actions | done with gaps | P5-AUDIT-07 (major); -27, -28, -29, -30: all fixed |
| M23a Captures UI | done with gaps | P5-AUDIT-31: fixed |
| M23b Forms for the application-layer kinds, `chaosctl dns/tls/dhcp set` | done with gaps | P5-AUDIT-08 (major); -32, -33, -34, -35: all fixed (-35 differently) |
| M23c Interception UI, `chaosctl intercept`, `tls ca` | done with gaps | P5-AUDIT-09 (major); -36, -37, -38, -39, -40: all fixed (-09 differently) |
| Cross-cutting (the Phase 3 network form, plan, docs, CI) | done with gaps | P5-AUDIT-10 (major): decided by the maintainer (option (b)) and fixed; -41, -42, -43: all fixed |

What each milestone delivers, as the lenses read it. **M20**: every action of the proxy (nxdomain, servfail, timeout over UDP
and TCP, delay, wrong_answer for A/AAAA only, truncate with the TCP fallback, short_ttl behind a cache that copies on put and get),
the first matching entry wins with names normalized by `HostnameMatches`; the hostname-set reports follow CNAMEs, take the smallest
TTL, batch, deduplicate for a minute and forget on a new generation; the compiler ranks DNS entries with `RankOrder`, keeps one
dynamic set per selector keyed on the original saddr and daddr, puts hostname fault rules in front of the lookup levels as
P5-M20-01 accepts, leaves the network's own segments out of the hardcoded-DNS redirect and drops DoT after the access rules; the
engine mirror (lifetime max(TTL, 10 min) capped at 30 days, the write at half the lifetime, group commit, request splitting with the
namespace, the gates for replaced sets, adoption after a restart, refresh from flows) and the executor's netlink path with its
ENOENT retry hold. **M21**: the redirect is accepted only for `iif @ifs_cg` with the service mark after `mgmt_guard`, the access rules,
the DoT block and the port forwards (E11); a spoofed packet from the uplink falls to the final drop; `ifs_test` never holds the
management interface; the fail-closed guard for 169.254.100.0/30, the prohibit fallback of table 102, the IPv6 drops and the marks
(ct bit 23, meta bit 20, no clash with classify, PMTU and forward) hold; decision errors fail closed, the generation guards of
`SetConfig` and `hold()` hold, and the test CA download never returns a key or the unknown CA. **M22**: the upstream policy switches
only the trust store and the verify mode and keeps mitmproxy's host-name check; a CA file that cannot be read or an exception in
the hook ends in a refusal; inside the CONNECT tunnel mitmproxy runs in transparent mode, so a device cannot escape the responder's
access decision; the container runs as 65532, read-only, `cap_drop: ALL`, `no-new-privileges`, listening on loopback only; the
batchers are bounded and drop refused batches; the health window and the smoke test match the compose file. **M23**: the resolution
(a device beats its network, the newer action wins at one scope, `delete_lease` apart), the compiled Kea lease classes, DROP and
option reservations, the address rules of sequential `force_new_ip` writes, the refusal of an action Kea refuses (also in the
preview), the deletion once per key with the retry, the reset when an overlay goes and the API states hold; the real-Kea tests pass.
**M23a**: the storage endpoint, the states, the actions and their scopes, the form's limits against `domain.ParseByteSize`, the
event invalidations and the capability gating hold. **M23b and M23c**: the form bodies agree with the spec and the gateway's checks,
only the fields an action uses are sent, the pointer mapping of problems is right, DNS and TLS are overlay-only kinds, an edit that
changes the key removes the old overlay, the Interception screen's filters, Pause, Show older and poll hold, and `chaosctl`'s rule
parser, flag checks and exit codes, `tls ca regenerate --yes` and the CA file mode hold. Regressions to code from before Phase 5:
dynamic sets are never flushed, the verification of type and flags covers them, the incremental identity path calls
`hosts.applied`, the redirect probe is idempotent, and the run scheduler's hostname and zero-hit changes and the capture quota rework
hold; the race detector found no data race in any Phase 5 package.

### Evidence

- **CI on the branch:** run 40270141299 on `eab804c`, every job green, the level 1 tests on the real kernel and the VM job
  included (and 40258339299 on `aebbae8`; PR #120's run 40196370688 on `6a0ec5e`). Not run in the audit (no KVM, no netns). No
  Nightly ran on any Phase 5 commit (P5-AUDIT-43).
- **Built and run in the audit:** `go build ./...`, `go vet ./...` and `go vet -tags testbed ./...`; `go test -race -count=1` of
  `internal/tlsresponder`, `engine`, `run`, `api` (with `-timeout 45m`), `svcapi`, `kea`, `dnsproxy`, `capturemgr`, `compiler`,
  `ctl`, `apply/...`, `domain`, `cmd/chaosgw` and `cmd/chaosctl`, no race found; `internal/tlsresponder` with the real mitmproxy
  (`CHAOSGW_MITM_PYTHON=/opt/mitm-venv/bin/python`, 128 s); the real-Kea tests of `internal/kea` (Kea 3.0.3, as root); the
  sidecar's Python tests; `make check-spec`, `make check-generated` and `make check-clients`, all green (the Python generator drops
  the PEM bodies of `/tls/ca` and `/tls/ca/regenerate`, the known pattern of `report.xml`); vitest and scratch Playwright specs
  (`E2E_PORT=4571`).
- **Mutation experiments:** 49 mutations across Go, Python and the web app (unit tests and Playwright), 46 killed; the survivors
  with a consequence are recorded as P5-AUDIT-16 and -29, and one more survivor (the lease loop of `dhcpTaken`) is harmless, see
  below.
- **Reproduced in scratch copies outside the repository:** P5-AUDIT-03 (the access verdict for a downstream host), -04 and -05 (the
  real addon with mitmproxy 12.2.3 test flows), -07, -27 and -28 (engine harness with a fake DHCP), -10 (a vitest of
  `networkFromForm`), -09, -31, -32, -36 and -37 (scratch Playwright specs), -11 (a blocking sink), -16 and -29 (surviving
  mutations), -40 (ESC and BEL accepted by mitmproxy's HTTP/1 reader). The scratch copies are under
  `/root/.claude/jobs/cee5d0c4/tmp/audit5/`.
- **The two known flakes** (`TestADNSStepMakesADNSOverlayOwnedByTheRunAndRemoveTakesItAway`,
  `TestARunKeepsGoingWhenTheRequestIsOver`) did not reproduce in 1200 and 240 runs under `-race` with low `GOMAXPROCS`.

### Phase 5 audit: result of the round and remaining points

The round after the audit (2026-10-26, branch `phase-5b`, PR #121, merged to `main` on 2026-10-27, `1c969c4`) closed every item, P5-AUDIT-10 after the maintainer's decision of 2026-10-27, see the Fix
column of the table. It was built and checked without KVM, without network namespaces and without Docker, on the final commit of
the fixes (`3975e57`): `go build ./...`, `go vet ./...` and `go vet -tags testbed ./...`, `gofmt -l` (no file),
`golangci-lint run ./...` (0 issues), `make check-spec`, `make check-generated` and `make check-clients` were green (the Python
generator still drops the PEM bodies of `/tls/ca` and `/tls/ca/regenerate` and the JSONL events, the known pattern). `go test -race
-count=1 -timeout 40m ./...` passed in every package but one: `internal/executor`
`TestACaptureIsRefusedOutsideTheScopeAndForBadRequests` fails with "the disk is almost full" because the machine has less than 10 %
free (known, the same on `main`); the rest of the package passes. `internal/api` took 829 s under `-race`, `internal/tlsresponder`
170 s with the real mitmproxy (found at `/opt/mitm-venv`), so the tests of the real proxy, among them the new and changed ones of
P5-AUDIT-04, -05 and -21 and the API client test of P5-AUDIT-18, ran here. The sidecar's Python tests (45, with mitmproxy, none
skipped) passed, and so did the web app: `npm run typecheck`, vitest (562 tests in 44 files) and the full Playwright suite (317
tests, `E2E_PORT=4571`). Nothing failed that needed a fix.

Not run here, the level 1 tests the round added or changed, all vetted with `-tags testbed`. CI run 40335480067 on `9bfd4cf` ran
each of them in the privileged container and in the VM job and every one passed (none skipped); the nightly kernel matrix runs them
after the merge:

- `TestAHostnameFaultStillActsAfterTheForcedNewIPWithoutANewQueryOnTheRealKernel` (new, `internal/engine/integration_dhcpactions_test.go`,
  added to `M20_LEVEL1`, P5-AUDIT-01). Its assumption: the engine report it sends directly (what the DNS proxy posts) gives the same
  elements as a real query in the M23 DHCP bed, which has `force_new_ip` but no proxy.
- `TestTheNetlinkConnectionWritesAndRefreshesDNSElementsInTheGatewayNamespace` (`internal/executor/dnssets_integration_test.go`,
  `M20_LEVEL1`), changed: a delete case for the elements the re-keying takes out (P5-AUDIT-01).
- `TestTheAddedAnswerLatencyForHostnameSetNamesMeetsThePlanOnTheRealKernel` (`internal/api/e2e_dnsfaults_test.go`, `M20_LEVEL1`),
  changed: each of the 40 measured names has its own address, and the set
  must hold an element of client A per answer (P5-AUDIT-12).
- `TestAConnectionOfACaseTheProxyDoesNotKnowIsRefusedAndNeverReachesTheServer`,
  `TestAConnectionWaitsForTheCaseThatTheProxyReceivesAMomentLater` (new, `internal/tlsresponder/mitm_unknown_test.go`, P5-AUDIT-04) and
  `TestTheResponseHeadersOfAModifyRuleAreOnTheAnswerOfAStatusOrBlockRule` (new, `mitm_headers_test.go`, P5-AUDIT-05), added to
  `M22_LEVEL1`. They run the real mitmproxy on the loopback and passed here; they are in the list so that a testbed image without
  mitmproxy cannot skip them. The same holds for the tests of `mitm_test.go` and `mitm_headers_test.go` whose fixed waits became
  `waitApplied` (P5-AUDIT-21).
- `TestALeaseClassGivesItsClientsTheirOwnLeaseTimeAndNoOtherClient` (`internal/kea/dhcpwire_testbed_test.go`,
  `M23_LEVEL1`), changed: it asks for the hour before it takes the lease, so Kea's lease caching cannot answer with the
  remaining time (P5-AUDIT-43; 300 runs against the real Kea were green here).
- The new step of `deploy/smoke-tlsproxy.sh` (P5-AUDIT-04 and -06): the stand-in API that serves one case, and the launcher that
  exits when the service address goes, is restarted by the restart policy and carries a request after the reconnect. It runs only
  in the CI job `tlsproxy-image`; its embedded scripts were checked here against the real launcher and mitmproxy.

The nightly kernel matrix ran every Phase 5 level 1 list green on `eab804c` (run 40314226553, 6.8.0-142-generic and
7.0.0-38-generic); it did not cover the fix commits after `eab804c`, which CI run 40335480067 on `9bfd4cf` covers (green in every
job).

Decisions taken by best practice, not by the maintainer:

- **P5-AUDIT-02**: a passed-through connection is reset only when no case selects it any more by its sources and port (and its
  destination, when that is a CIDR); "reset all when the case list changes" would also cut connections that the kernel still sends
  through the responder. Network, uplink and hostname destinations count as selecting (M21 decisions 5 and 7).
- **P5-AUDIT-04**: the proxy fails closed while it does not know a connection's case (`CASE_WAIT`, 5 s), and a key-log batch that
  names no overlay is still accepted, because the field is optional in the spec.
- **P5-AUDIT-06**: the launcher's namespace watch exits with code 3 after three missed checks or 60 s without the service address,
  and can be turned off (`--no-namespace-check`), which `testbed.StartProxy` does.
- **P5-AUDIT-15**: the counters are pruned to the overlays in the store, so an overridden overlay keeps its count.
- **P5-AUDIT-18**: a 4xx other than 408 and 429 is a refusal and drops the batch (counted); a transport error, a 5xx, 408 or 429
  keeps it (M21 decision 14).
- **P5-AUDIT-26**: the CA file of the installation is mounted with an operator compose file named in `COMPOSE_FILE`, not with a
  `compose.override.yaml`.
- **P5-AUDIT-30**: a done `delete_lease` is forgotten only when its overlay is gone and the current DHCP target no longer lists it,
  not when the store alone no longer holds the overlay (the retry would re-send an old target).
- **P5-AUDIT-42**: the optional check was built: `tools/ciconfig` checks the `-expect-pass` list quoted in `docs/development.md`
  against `.github/level1-tests.env`.
- The M20 remark about `TestADNSStepMakesADNSOverlayOwnedByTheRunAndRemoveTakesItAway` in `docs/development.md` no longer calls the
  failure a race between the last step and the removal of the overlays; it says the cause is not known and names the audit's runs
  (observation 3 below).

Remaining points of minor weight that the round left, none of them a new item:

- After the round: the nightly run 40288147095 on `767af94` (2026-10-26) failed one test on 7.0.0-38-generic,
  `TestALeaseClassGivesItsClientsTheirOwnLeaseTimeAndNoOtherClient` (an offer of 8 s where the lease class gives 9 s). The cause
  is Kea 3's lease caching, on by default: a client that asks again within a quarter of its lease is answered with what is
  left, in whole seconds, so the answer depends on a second boundary between two messages, and a short lease renewed soon was
  shorter than the action said. Fixed on `main` after the merge of PR #121, with PR #122 on 2026-10-27 (`3b87aac`; CI run
  40456176789 on `3b87aac` green in every job): the rendered configuration switches caching off (`docs/development.md`, Kea),
  with `TestAClientThatAsksAgainSoonGetsTheWholeLeaseTimeNotWhatIsLeft` (in `M23_LEVEL1`), which fails without the fix every
  time. The nightly run 40476181292 on `3b87aac` (started by hand) passed every job, both tests on 6.8.0-142-generic and on
  7.0.0-38-generic included.
- P5-AUDIT-10 was decided by the maintainer on 2026-10-27 (option (b)) and is fixed (below): the network form writes a `dhcp`
  block whenever DHCP is on, so a LAN added with the defaults is served; a configuration written by hand without the block still
  has no DHCP, as the spec now says.
- P5-AUDIT-06: `TestAHolderRestartIsHealedWithoutHelp` was not extended to the proxy container, because its bed has no restart
  policy; the smoke step covers the exit and the restart.
- P5-AUDIT-21: the twelfth sleep (`mitm_test.go`, the retry pause of the loop that waits for the new CA) stays; it is not a wait for
  rules. No test can fail without this fix, since it only removes a timing dependence.
- P5-AUDIT-02: a case that selects by a network, the uplink or a host name keeps a passed-through connection (documented in M21
  decisions 5 and 7).
- `internal/api` needs a `-timeout` above the Makefile's 20 minutes under `-race` on a loaded small machine (829 s here, earlier
  runs of the round hit 10 minutes with the default timeout). Under `qemu-user` the same package took 351 s to 585 s in the job
  `arm64` and passed the default 10 minutes of `go test` in CI run 40412746566 on `e771b60`; `make test-arm64` now gives it
  `-timeout 30m`.
- `TestForceNewIPMovesTheClientAtItsRenewalOnTheRealKernel` (`M23_LEVEL1`) failed in the VM job of CI run 40351451158 on
  `66a06cc` (a commit that changed only docs; the rerun of the job passed) and again in run 40393245421 on `2d42d10`: the client
  had its new address, but no renewal of it came within 15 s. The client's log, which the check now prints, showed the cause:
  busybox's `udhcpc` (1.36 and later) raises a lease below 30 s to 30 s for its own timers and renews at half of that, so the
  renewal of the 6 s lease comes about 15 s after the client is bound, not at 3 s as the test assumed, and the wait of 15 s raced it. The
  test and `TestAShortLeaseOfTheNetworkIsWhatUdhcpcGetsAndItRenewsSoonOnTheRealKernel`, which had the same wait, now wait 25 s,
  still before the 30 s at which a client of the network's own lease of a minute renews; their comments and the M23 section of
  `docs/development.md` say why. Not a Phase 5 code defect: the gateway gave the leases it should.

### Phase 5 audit items for the maintainer

Found by the audit of 2026-10-26 and confirmed in the code. It needs a decision about the meaning of the configuration; it does not
block the fix round (P5-AUDIT-08 is fixed with the gateway's current rule and follows the decision through one shared helper). It
is still open after the round of 2026-10-26.

#### P5-AUDIT-10 What does a test network without a `dhcp` block mean: DHCP off, or DHCP on with the defaults?

- Status: closed (2026-10-27): decided by the maintainer, option (b): the gateway's rule stays (a LAN without a `dhcp` block has
  no DHCP). `networkFromForm` writes the block whenever DHCP is on (`dhcp: {}` with the defaults), `formFromNetwork` reads a LAN
  without it as DHCP off through the shared `dhcpServed`, and `api/openapi.yaml` (`DhcpScope` and its `enabled`) says that the
  block switches DHCP on and that `enabled` defaults to true only inside it. The unit test of the form that pinned the old output
  (the defaults leaving no block) now expects `dhcp: {}`. Tests: `networkForm.test.ts` "the dhcp block of a LAN (P5-AUDIT-10)"
  (both fail without the fix), and the Playwright test "network forms: a test network added with DHCP on and its defaults is
  saved with a dhcp block, so the gateway serves it and its DHCP card is on (P5-AUDIT-10)"
- Severity: major
- Reason: needs-decision. The gateway reads a LAN without a `dhcp` block as DHCP off: the compiler gives Kea no subnet for it
  (`internal/compiler/dhcp.go:40`), `force_new_ip` is refused with "DHCP is off on the network" (`internal/engine/dhcpaction.go:44-46`),
  the warnings and the web app's `lib/networks.ts:119` agree. The spec's `DhcpScope.enabled` has `default: true`, which a reader
  takes as "on unless switched off", and the network form relies on that: `networkFromForm` (`web/src/lib/networkForm.ts:464-482`)
  writes `enabled` only as `false` or as a `true` the network had, and drops an empty block, so a LAN added in the form with DHCP on
  and the default settings is saved without a block and gets no DHCP. The setup wizard writes `dhcp: {enabled: ...}` and is not
  affected. Either side can be made consistent with the other; which one is right is a question of what existing configurations
  mean.
- Evidence: `web/src/lib/networkForm.ts:430-482` (`put`, `compact`, `networkFromForm`), `web/src/lib/setup.ts:193`,
  `internal/compiler/dhcp.go:36-45`, `internal/engine/dhcpaction.go:44-46`, `internal/domain/warnings.go:71,98`,
  `web/src/lib/networks.ts:119`, `api/openapi.yaml` (`DhcpScope`, ~2750-2756), `internal/compiler/dhcpaction_test.go:167` ("a
  network without DHCP" is one with no block); scratch vitest
  `networkFromForm(emptyNetworkForm('lan','10.30.0.1/24'))` → `{type:'lan',name:'lab2',address:'10.30.0.1/24',interfaces:[]}`.
- Options: (a) the gateway treats an absent block of a LAN as `dhcp: {}` (on with the defaults), as the spec's default reads: the
  compiler, the engine, the warnings and `lib/networks.ts` change in one shared rule; a configuration that has a LAN without a block
  and meant it without DHCP gets DHCP after the upgrade and must say `dhcp: {enabled: false}` (an upgrade note). (b) The gateway's
  rule stays: the form writes `dhcp: {}` (or `enabled: true`) whenever DHCP is on, the spec's `Network.dhcp` and `DhcpScope`
  descriptions say that an absent block means DHCP off and that `enabled` defaults to true inside a block, and the form shows a
  network without a block with DHCP off. (c) Like (b), and the configuration validation warns about a LAN without a block.
- Recommendation: (b): it keeps the meaning of every stored configuration, the change is in one function of the form and in the spec
  text, the wizard already writes the block, and the gateway's four readers stay as they are. Tests: a vitest that a LAN saved with
  DHCP on has a `dhcp` block, and a Playwright test that a network added with DHCP on shows its leases card and DHCP card enabled.
- Needs maintainer: yes
- Effort: S

### Phase 5 audit: checked and not recorded as items

No finding was refuted by both verifiers, so nothing was rejected during the audit. Merged as duplicates (one item each): the
interception proxy's fail-open (the M22 and the security lens, P5-AUDIT-04, recorded as major), the stale image descriptions (the
M22 and the docs lens, P5-AUDIT-22) and the plan's "PR #120, not merged" (the M20 and the docs lens, P5-AUDIT-41); the two stale
statements of the M21 section of `docs/development.md` are one item (P5-AUDIT-19). Details of findings that the verifiers corrected
are recorded in the corrected form: the scenario of P5-AUDIT-17 (a nil `snap.Applied` makes the guard stricter, not weaker), the
twelfth sleep of P5-AUDIT-21 (in a loop at `mitm_test.go:996`), and `767af94`, which is a commit on `main`, not a merge commit.
Further observations of the lenses that are not defects:

1. *`ResolveTLS` returns `Passthrough=true` for 169.254.100.1 on the UI port.* Not reachable: the kernel delivers a device's packet
   to any address of the host through the local table (ip rule priority 0, before the service rule at 900), so the TLS redirect never
   sees it and the input chain decides; the svcns redirect chain excludes the peer address.
2. *The mutation of the lease loop in `dhcpTaken` survives.* Harmless: a leased address is always also in `snap.Devices`, so the loop
   is redundant in practice.
3. *The two known flakes.* Neither reproduced (see Evidence), and no mechanism that removes a run's overlays before its end was found;
   `run.sleepUntil` works on absolute deadlines. The M20 remark in `docs/development.md:5792` ("looks like a race between the last
   step and the run's removal") is not supported by the code as it is (the scenario's last step at 10 s is never reached); it was
   reworded in the round of 2026-10-26 (the cause is not known, and the audit's runs are named).
4. *Other fixed sleeps in the Phase 5 level 0 tests* (`dnsproxy/faults_test.go:212`, `tlsresponder/ca_test.go:223`) only check that
   something did not happen, so they cannot fail falsely.
5. *The Python client drops the PEM response bodies of `/tls/ca` and `/tls/ca/regenerate`.* The known generator pattern of
   `report.xml` and the JSONL events; the TypeScript client and `chaosctl` read them.

## Phase 5 open items

Phase 5 (M20 to M23c, application layer) is implemented: M20 to M23a are on `main` (PR #120; CI run 40196370688 on `6a0ec5e` green), the rest (M21, M22, M23b, M23c) was implemented on the branch `phase-5b` and merged to `main` with PR #121 on 2026-10-27 (`1c969c4`; CI run 40434567365 on `1c969c4` green). Items below are what the implementation found that no later milestone of the plan completes and that needed a decision of the maintainer. The maintainer decided all nine on 2026-10-25:

- **Closed:** P5-M20-01 (option (a), the approximation is accepted), P5-M20-02, P5-M21-01 and P5-M23-01 (option (b) each, the forms are the new milestone M23b), P5-M22-01 (option (b), the interception UI is the new milestone M23c), P5-M22-04 (option (a), left for V1), P5-M21-02 (option (b), implemented: a passed-on connection reaches the test networks and the tunnels) and P5-M22-03 (option (b), the per-case field `upstream`). All of it is built on the branch `phase-5b` (PR #121, merged to `main` on 2026-10-27, `1c969c4`) and ran in CI run 40258339299 on `aebbae8`, green: M21, M22, M23b and M23c are done. The two small points the review of M23b found, P5-M23b-01 and P5-M23b-02, are closed too (neither needed the maintainer).
- **Waiting for H1:** P5-M22-02 (the proxy is part of H1; the release notes say that it is not validated on ARM64 until the measurement exists).

### P5-M20-01 A hostname selector of a fault is tried in front of the lookup levels, not at its rank among them

- Status: closed (2026-10-25): decided by the maintainer, option (a): the approximation is accepted for now and the limit stays documented (`docs/development.md`, "DNS faults and hostname selectors (M20)", decision 2); the lookup tables are split by rank (option (b)) only when a real scenario shows a wrong result
- Severity: minor
- Reason: needs-decision. Plan §3.3 says "hostname selectors are ordered rules against the per-device DNS-derived sets, placed at their level"; §2.4 resolves faults by a total precedence (overlay before configuration, then ten levels of specificity, then the newer). The addresses of a hostname selector are known at run time only, so the compiler cannot put it into the interval maps whose elements are the resolved winners (the table of `domain.Table`). M20 puts the rules of the hostname faults in front of the four lookup levels, strongest first among themselves (overlay before configuration, level, newer), and takes out of a source's rules every hostname fault that a fault of a higher rank without destination and ports (the whole device, group, network or global scope) covers for all of its traffic. What it does not do: a fault of a higher rank that names a destination or ports (an overlay "device A → port 8883" against a configured "device A + host X", or a device fault of one destination against a group fault of a host name) loses to the hostname rule for the traffic both select, where §2.4 would give it to the stronger one. The exact resolution needs one of the alternatives below, each with a cost the plan did not weigh.
- Evidence: `docs/development.md` "DNS faults and hostname selectors (M20)", decision 2; `domain.rankHostnames` (`internal/domain/table.go`), `compiler.hostFaultRules` (`internal/compiler/hostnames.go`); `TestHostnameFaultsAreTriedStrongestFirstAndAFaultOfTheWholeSourceOfAHigherRankTakesThemOut`.
- Task: decide whether the approximation is acceptable for V1. The deviation only shows with two faults of one family that overlap in traffic and rank the other way round.
- Options: (a) accept it and keep the documentation of the limit (what M20 does); (b) split the lookup tables by rank: the static candidates between two hostname candidates form a segment with its own four maps, and the chain alternates segment, hostname rules, segment (exact, costs four maps per distinct rank of a hostname fault and a table build per segment); (c) let the engine write, at every resolution, the winner for (device, address) into a verdict map of its own at level 2 (exact for traffic without a port selector, needs a port-aware second map for the others and a rebuild when a fault changes).
- Recommendation: (a) until a scenario shows a wrong result; (b) when it does.
- Acceptance: the plan states which of the three holds, and the tests of §2.4 (E1 to E12) have a case with a hostname selector if (b) or (c) is chosen.
- Needs maintainer: no (decided 2026-10-25: option (a))
- Effort: L

### P5-M20-02 DNS faults cannot be made in the web UI or with `chaosctl`

- Status: closed (2026-10-25): decided by the maintainer, option (b): one form for the DNS, TLS and DHCP kinds, designed once, in the new milestone M23b (Forms for the application-layer kinds, M) in Phase 5 of `docs/plan.md`; built in M23b on the branch `phase-5b`
- Severity: minor
- Reason: needs-decision. M20 delivers the DNS family in the API (an overlay of the kind `dns`, the `dns` part of profiles, the step type `dns`) and the UI labels and lists them (the chips of a DNS fault, the "DNS broken" profile card, a `dns` step in a scenario). The plan names no UI work for it: §2.17 lists the kinds of the "Add fault" dialog as impairment, MTU/PMTUD and tunnel, the step dialog of M19 edits the step types of M15 to M17, and `chaosctl` (M18) has flags for impairment faults and profiles only. No later milestone of the plan adds a DNS form (M21 to M23a are TLS, DHCP and the Captures screen). A DNS fault is made today with the API or the generated clients, in the YAML of a scenario (the step list keeps a `dns` step it does not edit), or by activating a profile with a `dns` part.
- Evidence: `web/src/lib/stepForm.ts` (`editableTypes` has no `dns`), `web/src/lib/faultForm.ts` (the fault kinds), `internal/ctl/overlays.go` (the flags of `chaosctl fault`); `docs/development.md` "DNS faults and hostname selectors (M20)".
- Task: decide whether V1 needs a DNS kind in the "Add fault" dialog (action, names, delay, answers, TTL; target and TTL as for the other kinds), a `dns` step in the step dialog and a `chaosctl` flag, and in which milestone.
- Options: (a) leave it: the API, the clients and scenario files are the way to make DNS faults, the UI shows them; (b) a DNS kind in the "Add fault" dialog and the step dialog, with Playwright tests, and `chaosctl dns` flags (one form for DNS, TLS and DHCP kinds would share the work of M21 and M23, which have the same gap).
- Recommendation: (b) together with the TLS form of M21, so the dialog gets its application-layer kinds once.
- Acceptance: the plan has the milestone (or the decision to leave it out) and the dialog's design in §2.17.
- Needs maintainer: no (decided 2026-10-25: option (b), M23b)
- Effort: M

### P5-M21-01 TLS cases cannot be made in the web UI or with `chaosctl`

- Status: closed (2026-10-25): decided by the maintainer, option (b): one form for the DNS, TLS and DHCP kinds, designed once, in the new milestone M23b (Forms for the application-layer kinds, M) in Phase 5 of `docs/plan.md`; built in M23b on the branch `phase-5b`; the download button for the test CA is part of it
- Severity: minor
- Reason: needs-decision. M21 delivers the TLS family in the API (an overlay of the kind `tls`, the `tls` part of profiles, the step type `tls`, the check types `tls_rejected` and `tls_accepted`) and the UI labels and lists them (the chips of a TLS case, the "TLS broken" profile card that activates, a TLS check in the check editor, a `tls` step in a scenario). The plan names no UI work for it: §2.17 lists the kinds of the "Add fault" dialog as impairment, MTU/PMTUD and tunnel, the step dialog of M19 edits the step types of M15 to M17, and `chaosctl` (M18) has flags for impairment faults and profiles only (`chaosctl overlay create` takes a request document of any kind, a TLS case included). No later milestone of the plan adds a TLS form (M22 is interception, M23 DHCP, M23a the Captures screen). A TLS case is made today with the API or the generated clients, with `chaosctl overlay create` and a document, in the YAML of a scenario (the step list keeps a `tls` step it does not edit), or by activating a profile with a `tls` part. It is the same gap as P5-M20-02 for the DNS family.
- Evidence: `web/src/lib/stepForm.ts` (`editableTypes` has no `tls`), `web/src/lib/faultForm.ts` (the fault kinds), `internal/ctl/overlays.go` (the flags of `chaosctl fault`); `docs/development.md` "TLS responder: certificate cases (M21)".
- Task: decide with P5-M20-02 whether V1 needs a TLS kind in the "Add fault" dialog (case, ports, server names, destination, cut existing; target and TTL as for the other kinds), a `tls` step in the step dialog, a flag of `chaosctl`, and the download of the test CA in the UI (`GET /tls/ca` has no button yet), and in which milestone.
- Options: (a) leave it: the API, the clients and scenario files are the way to make TLS cases, the UI shows them; (b) a TLS kind in the "Add fault" dialog and the step dialog, a "Download the test CA" button, with Playwright tests, and `chaosctl tls` flags (one form for the DNS, TLS and DHCP kinds, as P5-M20-02 recommends, so the dialog gets its application-layer kinds once).
- Recommendation: (b), together with the DNS form of P5-M20-02.
- Acceptance: the plan has the milestone (or the decision to leave it out) and the dialog's design in §2.17.
- Needs maintainer: no (decided 2026-10-25: option (b), M23b)
- Effort: M

### P5-M21-02 A connection that no TLS case decides reaches only servers behind the uplink

- Status: closed (2026-10-25): decided by the maintainer, option (b), and implemented in M21 (branch `phase-5b`): the forward chain accepts the service namespace towards the test networks and the WireGuard tunnels of the role test (never towards the management network), after the access rules and the matrix, which still judge the original connection (`AccessDecision`); the namespace's address is translated on the way out, so the server sees the gateway's own address in its network, not the device's (`docs/development.md`, "TLS responder: certificate cases (M21)", decision 13). The interception proxy's own connection takes the same path (M22). Level 1 test `TestAConnectionNoCaseDecidesReachesAServerInAnotherTestNetworkAndBehindAWireGuardClientOnTheRealKernel` (listed in `M21_LEVEL1`, not run here: no KVM; ran green in CI run 40258339299 on `aebbae8`); the decision is closed, the milestone M21 is "done" in the plan
- Severity: minor
- Reason: needs-decision. Plan §2.8: a connection that the kernel selected and no case decides (a server name no case names, a shared CDN address) is passed on to its destination unchanged, and only if the gateway would have forwarded it. The responder terminates the connection, so the pass-through is a second connection that starts in the service namespace and leaves it through `svc0`; the forward chain accepts that path towards the uplink only (`serviceForward`, `internal/compiler/service.go`) and the namespace is masqueraded there, as for the DNS proxy's upstream queries. A server in another test network or behind a WireGuard link is not reached: the device sees a reset, where the gateway would have forwarded the connection. It matters when a case selects traffic that is wider than its server names (no destination, so every server on port 443 or 8883) and a device talks to a server of that kind inside the test setup. Reaching those servers means accepting the namespace's connections towards the test networks and tunnels in the forward chain (after the access rules and the matrix, which judge the original connection as for the uplink, `AccessDecision`) and a decision on the source address the server sees (the namespace's, which the server answers through the gateway because the gateway routes `169.254.100.0/30`, or the device's, which needs a TPROXY-like path); the plan weighed neither. M21 resets what it cannot pass on instead of choosing one. **M22:** the interception proxy's own connection to the server takes the same path (`svc0`, the uplink only), so a case `intercept` that selects traffic to a server inside the test setup resets it too; the decision of the item covers both.
- Evidence: `docs/development.md` "TLS responder: certificate cases (M21)", decision 4; `compiler.Target.serviceForward`; `engine.TLSDecision.Passthrough`; `TestAConnectionNoCaseDecidesAndTheGatewayWouldNotForwardIsResetNotPassedThrough`.
- Task: decide whether a pass-through has to reach every destination the gateway forwards to, or only the uplink (a case that must not touch other servers names its destination, which the kernel then selects exactly).
- Options: (a) leave it: pass-through reaches the uplink only, a case with a destination or with the server's network in it does not touch other servers; (b) accept the namespace towards the test networks and the tunnels, the server sees the namespace's address; (c) (b) with the device's address as the source, which needs a transparent path from the namespace and a level 1 test of it.
- Recommendation: (a) for V1, (b) when a user needs a case with a wide selection next to servers inside the test setup.
- Acceptance: the plan states which destinations a pass-through reaches, and a level 1 test pins it.
- Needs maintainer: no (decided 2026-10-25: option (b))
- Effort: M

### P5-M22-01 The interception cannot be made, watched or administered in the web UI or with `chaosctl`

- Status: closed (2026-10-25): decided by the maintainer, option (b), built in full (the form of the HTTP rules, the table of the exchanges from `GET /intercept/flows`, the CA buttons, the `chaosctl` commands): the new milestone M23c (Interception UI, M) in Phase 5 of `docs/plan.md`, beside M23b so that M23b stays at size M; built in M23c on the branch `phase-5b` (the form of the settings and of the HTTP rules in the Add fault dialog and the step dialog, the Interception screen with the table of the exchanges and its filters and live update, the CA buttons with the question before the replacement, a panel on the device detail, and `chaosctl intercept set`, `intercept flows` and `tls ca`; its level 1 test ran green in CI run 40258339299 on `aebbae8`, M23c is done)
- Severity: minor
- Reason: needs-decision. M22 delivers the interception in the API (the case `intercept` of the overlay kind `tls`, of a profile part and of the step `tls`, `GET /intercept/flows`, `POST /tls/ca/regenerate`, the key log of a capture) and the UI shows what exists (the chips of an interception case with the number of its rules). The plan names no UI work for it: M22's scope and tests are about the proxy, §2.17 lists no interception screen, and no later milestone adds one (M23 is DHCP, M23a the Captures screen, whose download of the key log is for scope `full` as the API says). The form of an interception case has more fields than any other (ports, server names, destination, and a list of HTTP rules with a match, an action and the fields of the action) and the log of the exchanges wants a table with filters (device, host, rule acted) and a live update from `http_fault_triggered`, so it is a design, not a variant of the DNS and TLS forms of P5-M20-02 and P5-M21-01. Today an interception is made with the API or the generated clients, with `chaosctl overlay create` and a document, in the YAML of a scenario or by activating a profile with a `tls` part of the case `intercept`; the exchanges are read with `GET /intercept/flows`; the test CA is downloaded with `GET /tls/ca` and replaced with `POST /tls/ca/regenerate`.
- Evidence: `web/src/lib/stepForm.ts` (`editableTypes`), `web/src/lib/faultForm.ts`, `internal/ctl/overlays.go`; `docs/development.md` "TLS interception and HTTP faults (M22)".
- Task: decide whether V1 needs an interception kind in the "Add fault" dialog (the case, the settings and the list of HTTP rules), a view of the exchanges (a screen or a panel of the device detail), the download and the regeneration of the test CA in the UI, `chaosctl` commands for them (`chaosctl intercept flows`, `chaosctl tls ca`), and in which milestone. M22 is optional for V1 (plan §5), and so would be its UI.
- Options: (a) leave it: the API, the clients, scenario files and profiles are the way to use the interception, the UI shows its cases; (b) the form of the rules in the "Add fault" dialog and the step dialog, the table of the exchanges, the CA buttons and the `chaosctl` commands, with Playwright tests (with the TLS and DNS forms of P5-M21-01 and P5-M20-02, so the dialog gets its application-layer kinds once).
- Recommendation: (a) for V1 (the interception is optional for V1 and a scenario file is the natural way to write a list of rules); (b) after the DNS and TLS forms, when users of the interception ask for it.
- Acceptance: the plan has the milestone (or the decision to leave it out) and the design of the form and of the exchange table in §2.17.
- Needs maintainer: no (decided 2026-10-25: option (b), M23c)
- Effort: L

### P5-M22-02 mitmproxy has not been measured on small hardware, and bodies are buffered

- Status: decided (2026-10-25): the proxy is added to H1 (Scope and Tests of H1 in `docs/plan.md`: the memory and the CPU of mitmproxy with buffered bodies on a Raspberry Pi 4 and 5, and a target for §3.10 once measured); until then the release notes say that the proxy is not validated on ARM64. The measurement itself waits for H1 (env-limit)
- Severity: minor
- Reason: hardware limit. Plan risk 19: "mitmproxy throughput and resource use on small hardware: TLS proxy limits throughput; only selected traffic is proxied; targets in §3.10". §3.10 names no target for the proxy and H1 (hardware validation) lists none; the tests of M22 ran on x86. The proxy streams responses and buffers a body only to change it (`response_body_replace`, decision 11 of "TLS interception and HTTP faults (M22)") and the body of a request, so a device that uploads a large body through an interception case costs its size in the memory of the proxy's container, and mitmproxy's CPU use per connection on a Raspberry Pi is not known. Nothing in the gateway limits either (the container has no memory limit in `deploy/compose.tlsproxy.yaml` either, because no limit was measured to be safe).
- Evidence: `docs/development.md` "TLS interception and HTTP faults (M22)", decision 11; `sidecars/tls-proxy/chaosgw_tlsproxy/addon.py` (`responseheaders`, `response`), `deploy/compose.tlsproxy.yaml`.
- Task: when hardware exists (H1), measure the proxy's throughput, its memory with a large body and with 50 connections, and its CPU on a Raspberry Pi 4 and 5; then give §3.10 a target, set a memory limit of the container and decide what `response_body_replace` does for a body above a size (skip it with a warning in the exchange's report, or refuse the rule) and whether the body of a request is limited.
- Options: (a) leave it: the interception is for test setups with a few devices, the release notes say that the proxy is unvalidated on ARM64 as the other targets are; (b) add the proxy to H1 and act on its results.
- Recommendation: (b) with H1, (a) until then.
- Acceptance: H1's tests include the proxy and §3.10 states its target.
- Needs maintainer: no (decided 2026-10-25: option (b)); the hardware is still needed for the measurement
- Effort: S

### P5-M22-03 The proxy verifies the servers' certificates against mitmproxy's bundle and the policy is an environment variable

- Status: closed (2026-10-25): decided by the maintainer, option (b), and implemented on the branch `phase-5b`: a per-case field `upstream` of `InterceptSettings` (`verify` against the default bundle, `own_ca` against the CA file of the installation, `insecure` for no check), the two environment variables stay as the installation default of a case without the field. The spec field in `api/openapi.yaml`, the proxy addon, the validation and the tests at every level are done (`docs/development.md`, "TLS interception and HTTP faults (M22)", the upstream and decision 14); the form of the field is built in the interception UI (M23c, *Servers' certificates*)
- Severity: minor
- Reason: needs-decision. The proxy opens its own connection to the server a device meant and has to decide whether to verify its certificate. M22 verifies it (a proxy that accepts any certificate would show the device a working connection to a server whose certificate is wrong, and a test of the device's own checks would be a test of the proxy), against the bundle of mitmproxy (`certifi`); a server with a private CA fails the connection, which the proxy logs and reports as an exchange with the error `TLS to the server failed`. Two environment variables of the container change it for all cases at once: `CHAOSGW_UPSTREAM_CA_FILE` (other CAs) and `CHAOSGW_UPSTREAM_INSECURE=1` (verify nothing). The plan names neither. A per-case setting (`intercept.upstream_verify`, or the CAs of a network) would be a field of the API with a UI and a validation, and the choice between "verify", "verify against these CAs" and "do not verify" is a product decision.
- Evidence: `deploy/compose.tlsproxy.yaml`, `sidecars/tls-proxy/chaosgw_tlsproxy/launcher.py` (`--upstream-ca-file`, `--upstream-insecure`), `docs/install.md`.
- Task: decide whether the policy needs to be a setting of the case (or of the configuration), with which values.
- Options: (a) leave it: the two variables of the container, set once for the installation; (b) a field `upstream` of `InterceptSettings` (`verify` default, `insecure`, and the CAs by a name in the configuration's settings), applied by the addon per connection.
- Recommendation: (a) until a user needs different policies for different devices at the same time.
- Acceptance: the plan states where the policy is set, and a test pins that a server with a certificate that is not valid is refused by default.
- Needs maintainer: no (decided 2026-10-25: option (b))
- Effort: S
- Implemented: the field and its text in `api/openapi.yaml`, `rules.upstream_plan` and `Addon.tls_start_server`, and the tests: `TestAServerWhoseCertificateIsNotValidIsRefusedByDefaultAndServedWithInsecure` pins the refusal by default and the pass with `insecure`, the tests of the other values and of the cases with different policies at once in `internal/tlsresponder/mitm_upstream_test.go`, the level 1 test `TestTheUpstreamPolicyOfACaseDecidesWhetherTheProxyAcceptsTheServersCertificateOnTheRealKernel`. One reading of the decision is recorded in decision 14: "the own CA" is the CA file of the installation (`CHAOSGW_UPSTREAM_CA_FILE`), not the test CA.

### P5-M22-04 Runs and their reports know nothing of the HTTP exchanges of the proxy

- Status: closed (2026-10-25): decided by the maintainer, option (a): left for V1, a test reads `GET /intercept/flows` itself
- Severity: minor
- Reason: needs-decision. The exchanges the proxy reports are kept in memory (the newest 20000) and read with `GET /intercept/flows`; a run's check can look at the TLS handshakes of the proxy (`tls_accepted`, `tls_rejected`) and its counters count connections, but there is no check on what the device asked or got ("the device requested `/v1/telemetry` within 30 s after the restore", "no request got a 5xx") and the report of a run has no list of the exchanges. The plan's M22 scope and tests name the proxy, its rules and the key log only; the check types of M16 are connection, traffic, DNS and TLS checks. Adding a check type is a change of the scenario schema and of the checks' evidence, and the report would need the log to be kept per run (a size and retention decision).
- Evidence: `internal/run/checks.go` (the check types), `api/openapi.yaml` (`Check`), `docs/development.md` "TLS interception and HTTP faults (M22)", decision 10.
- Task: decide whether a run needs a check type for HTTP exchanges (e.g. `http_request_seen`, `http_status_seen` with a host, a path prefix, a method and a window) and the exchanges in its report (with the artifacts), and in which milestone.
- Options: (a) leave it: a test reads `GET /intercept/flows` itself (`chaosctl` or the client library) after the run, the run's captures with the key log are the evidence; (b) a check type `http_exchange` (host, path prefix, method, status, with the window of the others) and a log of the run's exchanges as an artifact (`kind: http_exchanges`, a JSONL file kept with the run).
- Recommendation: (a) for V1; (b) after a user needs a pass or a fail of a run by what the device asked.
- Acceptance: the plan has the check type (or the decision to leave it out) and the artifact.
- Needs maintainer: no (decided 2026-10-25: option (a))
- Effort: M

### P5-M23-01 DHCP test actions cannot be made in the web UI or with `chaosctl`

- Status: closed (2026-10-25): decided by the maintainer, option (b): one form for the DNS, TLS and DHCP kinds, designed once, in the new milestone M23b (Forms for the application-layer kinds, M) in Phase 5 of `docs/plan.md`; built in M23b on the branch `phase-5b` (the actions on the device detail and the network card as §2.17 names them, a `dhcp` step in the step dialog, `chaosctl` flags)
- Severity: minor
- Reason: needs-decision. M23 delivers the DHCP family in the API (an overlay of the kind `dhcp` with the actions `short_lease`, `delete_lease`, `force_new_ip`, `set_options` and `silence`, the step type `dhcp`, the event `dhcp_leases_deleted`) and the UI labels and lists them (the chips of a DHCP action, a `dhcp` step in a scenario). §2.17 lists "DHCP lease and DHCP test actions" among the contents of the device detail, and §2.7 says the UI shows DHCP inside the network and device views, but the milestones that build those views (M12 to M14) deliver the lease and no actions, and M23's scope names the scenario step type, not a form. No later milestone of the plan adds one (M23a is the Captures screen). `chaosctl` (M18) has flags for impairment faults and profiles only (`chaosctl overlay create` takes a request document of any kind, a DHCP action included). A DHCP action is made today with the API or the generated clients, with `chaosctl overlay create` and a document, or in the YAML of a scenario (the step list keeps a `dhcp` step it does not edit). It is the same gap as P5-M20-02 and P5-M21-01 for the DNS and TLS families, with two differences: the actions belong on the device detail and the network card (a button "Force a new IP", "Delete lease", a lease time and options form) rather than in the "Add fault" dialog, and `force_new_ip` is a device's action whose address the gateway chooses and shows.
- Evidence: `web/src/lib/stepForm.ts` (`editableTypes` has no `dhcp`), `web/src/views/DeviceDetailView.vue` (the header comment names the DHCP test actions as arriving later), `internal/ctl/overlays.go`; `docs/development.md` "DHCP test actions (M23)".
- Task: decide with P5-M20-02 and P5-M21-01 whether V1 needs DHCP actions in the UI (the actions of the device detail and of the network card, a `dhcp` step in the step dialog) and in `chaosctl` (`chaosctl dhcp`), and in which milestone.
- Options: (a) leave it: the API, the clients and scenario files are the way to make DHCP actions, the UI shows them; (b) the actions on the device detail and the network card and a `dhcp` step in the step dialog, with Playwright tests, and `chaosctl dhcp` flags (the DNS and TLS forms of P5-M20-02 and P5-M21-01 are decided together, so the application-layer kinds are designed once).
- Recommendation: (b) for the device detail (it is in §2.17 already and is where a tester looks for "force a new IP"), together with the DNS and TLS forms; M23 is optional for V1 (plan §5), and so is its UI.
- Acceptance: the plan has the milestone (or the decision to leave it out) and the design of the actions in §2.17.
- Needs maintainer: no (decided 2026-10-25: option (b), M23b)
- Effort: M

### P5-M23b-01 The branch of the step dialog for a step type without a form is unreachable and no test covers it

- Status: closed (2026-10-26): option (b), the recommendation: the branch stays for a step type a later build adds, and `stepForm.test.ts` opens a step of a type outside `editableTypes` (the set is emptied of one type for the length of the test) and shows that it is kept and sent back with only its id, time and narrowing changed
- Severity: low
- Reason: found by the review of M23b. M23b gives `dns`, `tls` and `dhcp` their forms, so every step type of this build is edited with a form. `StepForm.kept`, `editableTypes` and the branch of `StepDialog.vue` that says "This step is edited in the YAML view" were made for a type that the dialog cannot edit; none is reachable now, and the Playwright test that covered them (a DNS step "the editor does not make, kept as it is") was changed on purpose, decision 12 of M23b. The code was left in place for a step type that a later build adds. It is dead code, not a defect, and nothing tests it.
- Evidence: `web/src/lib/stepForm.ts` (`editableTypes`, `StepForm.kept`), `web/src/components/scenarios/StepDialog.vue`, `docs/development.md` "Forms for the application-layer kinds (M23b)", decision 12.
- Task: remove the branch and `kept`, or keep them and cover them with a unit test of `lib/stepForm.ts` that opens a step of a type outside `editableTypes` and sends it back unchanged.
- Options: (a) remove them, a later type brings its own form or the branch back with its test; (b) keep them and add the unit test.
- Recommendation: (b) while M23c or a later milestone may still add a step type (the YAML view is then the way to edit it until its form exists); (a) when none does.
- Acceptance: either the branch is gone with `kept` and `editableTypes`, or a test opens a step of a type that has no form and shows that it is kept and sent back unchanged.
- Needs maintainer: no (a choice between two cleanups of the code, the recommendation applies unless the maintainer prefers the other)
- Effort: S

### P5-M23b-02 The DHCP card of a device that is in no served network says "DHCP is off in this network"

- Status: closed (2026-10-26): option (a), the recommendation: `DhcpActionsCard` takes `noNetwork` and says "This device is in no network with DHCP: an action has nothing to change."; the buttons stay off; the Playwright test is "a device that is in no network the gateway serves says so under its DHCP card, and its buttons are off"
- Severity: low
- Reason: found by the review of M23b. The device detail shows the DHCP card for a device in a network the gateway serves, and for a WireGuard client; a device that is in no network the gateway serves and is no WireGuard client gets the card with the text "DHCP is off in this network", which says something about a network it has none of. It is a rare case (a device that has been seen only on a network that was removed from the configuration) and the card is correct in its effect (its buttons are off).
- Evidence: `web/src/components/dhcp/DhcpActionsCard.vue` (the DHCP card of the device detail), `docs/development.md` "Forms for the application-layer kinds (M23b)", decision 7.
- Task: say what is true for such a device ("This device is in no network with DHCP"), or leave the card out for it as for a device of a gateway without DHCP actions.
- Options: (a) another text under the card for a device without a served network; (b) no card for it.
- Recommendation: (a), because the card is where a tester looks for the lease of a device and the reason is better shown than the card missing.
- Acceptance: a Playwright test of a device without a served network shows the card with the new text (or no card), and its buttons are off.
- Needs maintainer: no (the wording of a text and the choice of (a) or (b) follow the recommendation unless the maintainer prefers the other)
- Effort: S

## Phase 6 audit result

Date: 2026-10-28. Audited on the branch `worktree-phase-6` at `b80fa86` (PR #125, everything since `main`: M24, M25, M26 and M26a, with
the decisions P6-M24-01 and P6-M25-01 of the same day), against `docs/plan.md` §5 Phase 6 (M24 to M26a) and the sections it names (§2.12,
§2.13, §2.15, §2.16, §2.17, §3.x, §4.3, the D-decisions), `api/openapi.yaml`, `docs/development.md` (the sections for M24, M25, M26 and
M26a), the Phase 6 open items and the earlier audits in this file. The audit read the phase through seven lenses: probes (correctness of
M25 and its DHCP client), diagnostics and security (M24, the new `overlays` reach), tests (mutation experiments), API, spec and docs, the web
app (diagnostics, probes, flows), metrics and the flow view (M26), and the completeness of the plan (M24 to M26a against the plan's own
promises). The audit was read-only: it built the code, ran single-package tests, `make check-spec`, `make generate-go`, `make check-clients`,
vitest (636 tests) and the Playwright specs of the diagnostics, probes, probe management and flows screens against the mocked API, reproduced
findings in scratch copies outside the repository (`go test -overlay`, scratch Playwright specs), and changed nothing. No level 1 test was
run (no KVM, no network namespaces); CI ran them (run 40639560679 on `b80fa86`, green in every job, the VM job included). Every finding was
checked a second time by two independent verifiers who tried to refute it; where they disagreed on the severity or on a detail, the lower
or the better-argued one is recorded.

**Phase 6 is implemented and its tests pass, but it was not clean.** The audit found no blocker. It confirmed 65 items, 14 of major
severity and 51 of minor severity (P6-AUDIT-01 to -69 except -39, -56, -57 and -68, which the verifiers refuted, see the end). The core
of every milestone held up: the diagnostics' results and their bounds, the namespace and veth pair of a probe and its DHCP lease, the
targeting of a probe by rules and faults, the statistics of the self-test, the Prometheus text format and the flow view. The major items
are in the edges: a probe's DHCP client that never follows a probe moved to another network or a re-made namespace (P6-AUDIT-01), probe
runs that took the executor's reader slots for minutes (P6-AUDIT-02, -16), a jitter check that failed a correct `normal` profile
(P6-AUDIT-03), the calibration overlay with no test for its cleanup (P6-AUDIT-04), the audit entry of a diagnostic that named a
placeholder instead of the URL fetched, which is the compensating control of P6-M24-01 (P6-AUDIT-14, -30, -60), a flood of cheap
diagnostics that evicted the audit history (P6-AUDIT-15), a diagnostic test that could not fail (P6-AUDIT-21), a calibration that replaced
and deleted the caller's own overlay (P6-AUDIT-29), a Measure dialog that could be closed mid-run (P6-AUDIT-41), a path badge that counted
DHCP and DNS faults which do not act on a connection (P6-AUDIT-50) and a metrics scrape with no deadline per source (P6-AUDIT-51). No item
needed the maintainer: the two questions the phase raised, P6-M24-01 and P6-M25-01, were decided on 2026-10-28 before the audit and are not
reopened. **Result of the round after the audit (2026-10-28):** all 65 confirmed items were closed on the branch `worktree-phase-6`, seven
commits (`c29d100`, `d10a94e`, `a92abff`, `d6efa02`, `ecc5ac8`, `4d4dbd3`, `b00df9c`): 63 have a change of their own, two (P6-AUDIT-16 and
-30) were already fixed by another item of the round and needed none. Six of them are fixed in part or differently from the audit's
proposal (P6-AUDIT-13, -22, -25, -42, -43, -55), and the parts that were not done are listed under "Remaining points". The CI run
that covers the fix commits has not happened yet.

| Verdict | Items |
|---|---|
| Fixed on the branch (a test that fails without the fix, or the corrected document) | 57 items: P6-AUDIT-01 to -69 except -13, -16, -22, -25, -30, -39, -42, -43, -55, -56, -57 and -68 |
| Fixed in part or differently from the audit's proposal | P6-AUDIT-13 (the cost per probe and observation is bounded by concurrency, not removed), -22 (the default name list is tested at the API level; the level 1 case came with P6-AUDIT-62), -25 (the edges are pinned and the level 1 test compares the rate and the loss itself; the loose loss bound of the emulated kernel stays), -42 (only `RemoveProbeDialog` needed the fix; the device dialog was right), -43 (a profile measurement waits for a pending confirmation too, a probe's faults do not), -55 (the pages of one listing share a read of at most 5 s; there is no snapshot token in the cursor) |
| Already fixed by another item of the round | P6-AUDIT-16 (by -02), -30 (by -14); the optional parts of -30 (an audit object for the probe, refusing `host` together with `url`) were not done |
| Rejected as wrong after all | none |
| Not done | none that the audit required; the remainders of -13, -25 and -30 and the reasons are under "Remaining points" |
| Checked and rejected during the audit | P6-AUDIT-39, -56, -57 and -68, see the end |
| Waiting for the maintainer | none |

| Item | Severity | Finding | Fix |
|---|---|---|---|
| P6-AUDIT-01 | major | A probe's DHCP client is never restarted. `startProbeClient` (`internal/executor/probe.go:284-289`) returns as soon as `e.probes.m` has an entry for the namespace name, and only a `delete` calls `stopProbeClient`; an `ensure` that moves the veth end to another bridge (the M26a Edit) or makes the namespace again leaves the old client, so the probe keeps the old network's lease (the old address and default route) until a renewal meets a Kea that NAKs, or has a client whose socket lives in a dead namespace. Reproduced with a scratch test: the socket factory was opened once before and after the network change and after `ip netns delete`; `TestAProbeThatWasTakenOutOfItsBridgeOrWentIsHealed` checks neither the client nor the address. | **Fixed (c29d100):** `startProbeClient` compares the bridge, the MAC and the namespace inode the running client was started for; on a mismatch it stops the client, flushes the old address (in the same namespace) and starts a new one. Tests in `internal/apply`: the socket-open counts for a network change and for a re-made namespace, and the address gone after the move; both fail without the fix. |
| P6-AUDIT-02 | major | Probe runs take the executor's four reader slots for up to five minutes and starve every read, the reads of an apply included. `isReadOnly` counts `*ProbeRun` as a read (`exec.go:286`), a diagnostic from a probe runs up to 60 s and a calibration's echo and rate run up to 6 min (`MaxProbeRunMs`), `POST /diagnostics` from a probe had no per-probe limit. Reproduced: four blocked `probe_run` diagnostics, then a plain `read links` waited 2 s and returned `context deadline exceeded`. | **Fixed (c29d100):** probe runs have their own two slots (`probeRunSlots`) and take no reader slot; a probe runs one diagnostic at a time (`409 target_busy`). Executor test `TestProbeRunsThatFillTheirSlotsDoNotStarveTheReads` fills the slots with blocked runs and a read still returns at once. |
| P6-AUDIT-03 | major | The jitter check fails a correctly working impairment with the distribution `normal` when both directions jitter. The limit is the sum of the two jitters plus a tolerance (`calibrate.go:186-190`), but a normal round trip has a sigma of J times the square root of 2, so its 95th percentile above the median is 2.33 J, over the limit of 2 J. Simulated: 142 of 200 correct runs failed for 50 ms jitter 25 ms and 176 of 200 for 100 ms and 50 ms; no test used a distribution. | **Fixed (c29d100):** `Expectation.SpreadMs` follows the distribution (uniform: the trapezoid of two directions, `a+b-sqrt(0.4ab)`; normal: `1.645*sqrt(a2+b2)`; mix as normal; pareto is not checked and the result's detail says so) and the check allows 1.2 times that spread plus the tolerance. Tests: simulated uniform and normal round trips pass at least 198 of 200 runs (the old limit failed 172 of 200 for normal 100 ms and 50 ms), and twice the jitter still fails. |
| P6-AUDIT-04 | major | No test can show that the calibration overlay is removed on an error path, a cancel or an executor failure. The only assertion (`TestACalibrationThatCannotRunIsRefused`) is after a case that fails at the gateway's reference echo, before the overlay exists; the deferred `DeleteOverlay` discarded its error, so a leftover impairment stayed on the probe until its TTL. | **Fixed (c29d100):** API tests for a cancel of the request, for a failing `probe_run` after the overlay exists and for an echo server that goes after the reference; each asserts that no overlay is left and that the probe can be calibrated again (mutation-checked). A failing `DeleteOverlay` is logged at warn level. |
| P6-AUDIT-05 | minor | A cancelled or truncated echo run is judged and audited as a calibration verdict. `probe.Echo` returns the partial result with no error when the context ends or a write fails, and `CalibrateProbe` never compares `run.Sent` with the planned count, so 100 datagrams of 2000 were judged like a full run and written to the audit log as `within tolerance`. | **Fixed (c29d100):** an echo run must be complete (context not cancelled, `sent` equal to the planned count) for the reference and for the probe run; otherwise `503`, and the audit entry says "no verdict". A cancelled rate run is treated the same. Tests: a cancel, and a socket cut short after 50 of 500 datagrams. |
| P6-AUDIT-06 | minor | The DHCP client accepts malformed leases. `leaseOf` (`internal/probe/dhcp.go:297-322`) never validated `yiaddr`, took a missing option 51 and a lease time of 0 as an infinite lease, silently fell back to /24 for a mask with holes, and `acquire` accepted an OFFER without a server id and an ACK from any server. Reproduced with scratch tests. | **Fixed (c29d100):** `leaseOf` and `acquire` refuse an unusable `yiaddr`, an OFFER or ACK without a server id, an ACK from another server, no lease time, a lease time under 4 s and a mask with holes or zero. Table tests for each malformed reply in `dhcp_test.go`. |
| P6-AUDIT-07 | minor | The DHCP client has no pacing. A server that sets T1 to 0 made it renew in a tight loop (14727 renewals in 300 ms in a scratch test), a NAK was answered with an immediate new DISCOVER, and every retransmission of a renewal drew a new xid, so a late ACK was dropped. | **Fixed (c29d100):** T1 has a floor of 1 s and T2 is at least T1 plus 1 s; after a NAK or an unusable reply the client pauses, doubling from the retry time to the maximum; one renewal keeps its xid across retransmissions; an unusable renewal ACK is not taken. Fake-clock tests: T1 of 0 gives at most one renewal a second, the pause grows after NAKs, the xid stays, a late answer is taken. |
| P6-AUDIT-08 | minor | The fixed 3 s settle time (`calibrationSettle`) counts slow answers as lost for an impairment with a round trip above about 3 s: with `latency: 2s` in each direction 5.9 % loss was measured against 0 % expected. | **Fixed (c29d100):** settle time is `max(3 s, 2*(AddedRTT+Jitter)+1 s)`, capped at 60 s. API test with a 3.2 s round trip (4.2 % loss without the fix). The reference run keeps its 1 s settle, see the remaining points. |
| P6-AUDIT-09 | minor | The latency expectation ignores the serialization delay of a rate limit. At 56 kbit/s the 74-byte probe frame adds about 8.6 ms per direction, 17 ms on the round trip, against a tolerance of 2 ms plus 5 % of the added delay, so `latency: 50ms, rate: 56kbit` failed. | **Fixed (c29d100):** `calibrate.ExpectFor` adds `wireBytes*8/rate` for each rate-limited direction and the API passes the frame size (`probe.WireBytes`). Unit test for 56 kbit/s and an API test on the expected round trip. |
| P6-AUDIT-10 | minor | A namespace without its veth is never found. `readProbes` lists only the host links `pv<8 hex>`, and the delete plan removes the pair first and the namespace second, so a crash between the two steps, or a pair removed by hand, leaked `cgp-<8 hex>` for good: no plan, no Verify mentioned it. | **Fixed (c29d100):** `ReadProbes` also lists `cgp-<8 hex>` namespaces without a pair (`no_pair`); the plan deletes them when they are unwanted or makes the pair again (and the namespace is reused); `Verify` reports it. `kernelsim` got `netns list`. Test removes the pair only. |
| P6-AUDIT-11 | minor | `ensure` could not heal a probe whose MAC differs although the plan and Verify said it would: `planProbeNS` had no step that sets the address, so every apply planned the same `ensure` and the mismatch stayed. | **Fixed (c29d100):** `ensure` runs `healProbeMAC` (down, `ip link set address`, up) when the MAC differs; `kernelsim` supports `link set address` (and `addr flush`, `route delete`). Apply test sets a wrong MAC and sees it healed. |
| P6-AUDIT-12 | minor | After an executor restart a probe has no DHCP client and nothing re-applies until some other apply happens. The namespaces and addresses survive, Kea's lease expires after the lease time and the address can go to a device. `ReadProbes` reported `dhcp:false` but only an apply acted on it. | **Fixed (c29d100):** the observation (`engine` `readProbes`) sends `cmdResync` (at most every 30 s) when a configured probe's state says `dhcp:false`. Engine test restarts the executor (a swapped executor with a shared state file) and expects a client again, then no further clients. See also P6-AUDIT-63. |
| P6-AUDIT-13 | minor | Every second the engine starts `ip netns exec ... ip -j addr show` for each probe, sequentially in one reader slot; the cost is linear in the number of probes. | **Fixed in part (c29d100):** `ReadProbes` reads the insides of the namespaces concurrently, four at a time. The proposal to avoid `ip netns exec` altogether (addresses reported by the clients, an in-process netlink read after `setns`) was not done; see the remaining points. `docs/development.md` (M25) is updated for this item and for P6-AUDIT-01 to -12. |
| P6-AUDIT-14 | major | The audit entry `diagnostic.run` names the wrong target for an http run with a `url`. `diagnosticDetail` built the line from `req.Type`, `req.Host` and `req.Port`, but `host` is ignored when `url` is set (`internal/diag/httpd.go:25-30`); `dns_name`, the udp payload and a run `from.probe` were not named either. The entry is the compensating control of P6-M24-01: a run with `host: example.com` and `url: http://127.0.0.1:9100/admin/quit` was recorded as `http example.com ok`. | **Fixed (d10a94e):** the entry names what the run really asks: for http the scheme, host, port and path (the query is cut to `?...`, at most 200 characters), the DNS name and server, the size (not the text) of a udp payload, the resolved address and `from probe <ref>`. Tests: `TestTheAuditEntryOfADiagnosticNamesWhatWasContacted`, the exact ok and failed suffixes in `TestADiagnosticIsAuditedAndTheCapabilitiesListThem`, and the probe entries in `TestADiagnosticFromAProbeRunsInTheProbesNamespace`. |
| P6-AUDIT-15 | major | A loop of cheap diagnostics floods the audit log: one fsynced entry per run, no rate limit, and `maxEntries` was enforced only at open and once a day, so the file and the in-memory slice grew without bound and the daily prune left only `diagnostic.run` entries. | **Fixed (d10a94e):** a token bucket per principal on `POST /diagnostics` (`api.DiagnosticRate` 5 a second, `api.DiagnosticBurst` 20; `429 rate_limited` with `Retry-After`), and the audit log enforces its bound in `Append` (`audit.WithMaxEntries`, `audit.WithBulkActions`): `diagnostic.run` entries go first, then the oldest half. Tests: `TestADiagnosticLoopIsRefusedAfterTheBurst`, `TestTheBoundHoldsWhileTheLogRunsAndBulkEntriesGoFirst`, `TestTheOldestHalfGoesWhenTheBoundIsExceeded`. Found on the way and fixed: after the daily prune `Run` rewrote the file but `Log` kept appending to the replaced file, so later entries were lost (`compact()` now rewrites and reopens under the lock, `TestEntriesAfterTheDailyPruneReachTheFile`). |
| P6-AUDIT-16 | major | Four diagnostics from a probe take all four executor reader slots for up to 60 s and starve every other read (the same defect as P6-AUDIT-02, found by the security lens). | **Already fixed by P6-AUDIT-02 (c29d100):** `ProbeRun` has its own `probeRunSlots` and takes no reader slot. `TestProbeRunsThatFillTheirSlotsDoNotStarveTheReads` starts `readerSlots+2` blocked runs and checks that a read still completes. A third probe run waits for a probe-run slot until its context ends (up to 60 s) and holds one of the eight API places meanwhile, so many probe diagnostics can make gateway diagnostics answer `capacity_exceeded`; it affects diagnostics only. |
| P6-AUDIT-17 | minor | The http diagnostic reported the headers after Go's transparent gzip handling: the transport added `Accept-Encoding: gzip` itself and deleted `Content-Encoding` and `Content-Length` of a gzip answer, against decision 3 ("shows what the server answered"). | **Fixed (d10a94e):** the transport has `DisableCompression`, so it sends no `Accept-Encoding` and decodes nothing. `TestHTTPShowsTheContentEncodingOfAGzipAnswerAndSendsNoAcceptEncoding`. |
| P6-AUDIT-18 | minor | Response header names and the size of the header block were not bounded: a header name of 2 MiB was accepted and returned, and `MaxResponseHeaderBytes` was the default 10 MiB. | **Fixed (d10a94e):** header names are cut at 1 KiB like the values, `MaxResponseHeaderBytes` is 64 KiB (error "the response headers are too large"), and the first 64 are taken by sorted name, because `resp.Header` is a map and the old selection was random. Tests: `TestHTTPBoundsTheHeadersOfTheResult`, `TestHTTPRefusesAHeaderBlockThatIsTooLarge`. |
| P6-AUDIT-19 | minor | On the raw ICMP fallback concurrent pings to the same host took each other's replies: the identifier was the pid and the sequence numbers were 1 to count in every run. | **Fixed (d10a94e):** each run has its own ICMP identifier (pid plus a run counter) and the run number in the last four bytes of the payload, which the read loop compares. Tests: `TestTwoPingsAtOnceOnRawSocketsDoNotTakeEachOthersReplies` (fails without the fix) and `TestPingOnARawSocketIgnoresAReplyWithAnotherPayload`; `fakeICMP` gives the reply identifier symbolically and fans replies out to other sockets. |
| P6-AUDIT-20 | minor | The bounds of the result and the 60 s ceiling of the request had no test. | **Fixed (d10a94e):** `api.DiagnosticCeiling` (a variable, default `domain.MaxDiagnosticDuration`) is used by `RunDiagnostic`; `TestADiagnosticEndsAtTheCeilingAndFreesItsPlace` checks that a path with 60 s timeouts against a blocking `Net` ends at the ceiling with `ok: false` and frees its place. The header bounds are tested under P6-AUDIT-18. `docs/development.md` and the `runDiagnostic` description (429, audit) are updated. |
| P6-AUDIT-21 | major | `TestACanceledRequestEndsItsDiagnostic` could not fail: it waited for the client's own `Do` to return, which happens whether or not the server notices; replacing `contextOf(c)` by `context.Background()` in `diagnostics.go:123` still printed `ok`. | **Fixed (a92abff):** `blocking.DialContext` reports when its context ended and the test fails if the cancel does not reach the dial. Mutation-checked: with the context replaced the test now fails. |
| P6-AUDIT-22 | minor | The production DNS server list and the proxy address of the diagnostics were tested by nothing: every API test and the level 1 bed override `Servers`; a default list that returned nothing or a changed proxy port left the tests green. | **Fixed in part (a92abff):** `TestAHostNameIsResolvedByTheProxyThenTheUplinkThenTheHost` with a zero `DiagnosticsConfig` and a recording `Net` asserts the proxy `169.254.100.2:53` first, then the host resolvers, and with `uplink.dns_upstream` applied the proxy then the uplink's server; a changed proxy port and an empty uplink list make it fail. The level 1 half was done by P6-AUDIT-62. |
| P6-AUDIT-23 | minor | `TestAProbeRunDoesNotWaitForTheWriter` separated "waits" from "does not wait" by about 10 ms and relied on a sleep; under load the mutant passed. | **Fixed (a92abff):** the writer's `tc` command is blocked on a channel that the test releases only after `probe_run` returned, with a 10 s limit; no time bound remains. It fails when `ProbeRun` is removed from `isReadOnly` and passes with `-race`. |
| P6-AUDIT-24 | minor | A TLS chain of more than one certificate was never tested, although the Scope names "the whole chain" and `verify` builds an intermediates pool. | **Fixed (a92abff):** `pki.intermediate` and `pki.chained` in `internal/diag/helpers_test.go`; `TestTLSVerifiesAndListsTheWholeChainOfAServer` serves leaf plus intermediate from a root-only pool and asserts ok, verified, two certificates with the right subject and issuer, and the unknown-authority result when the intermediate is missing. Dropping `Intermediates` and listing only the leaf each make it fail. |
| P6-AUDIT-25 | minor | The accuracy tolerances of §4.3 were not pinned at their edges (any delay tolerance from 1.5 ms to 9.9 ms passed), and the level 1 loss and rate verdicts were `calibrate.Evaluate`'s own. | **Fixed in part (a92abff):** new edge tests: delay at 92.9, 93.1, 106.9 and 107.1 ms (and the relative part at 400 ms), rate at 1.79, 1.81, 2.19 and 2.21 Mbit, loss at and one datagram beyond `BinomialInterval(2000, 5%, 99.9%)`; with `LatencyRelative`, `RateRelative` and `LossConfidence` widened they fail. The level 1 test (`e2e_probes_test.go`) now compares the rate against 1.8 to 2.2 Mbit/s and the loss against an interval it computes itself; it compiles (`go vet -tags testbed`), the CI run is still to come. The loose 5 to 25 % loss bound of the emulated-kernel branch stays. |
| P6-AUDIT-26 | minor | M26a: a scenario that targets the probe blocks its removal, and no test checked it. | **Fixed (a92abff):** a Playwright test in `web/e2e/probe-management.spec.ts` (a scenario that targets the probe and one with a step that targets it block the removal; the reference texts, the link, the disabled confirm button, no `POST /revisions`, no drawer; it fails without the early return in `submit()`), and a vitest case in `draft.probes.test.ts` through `deviceReferences`. |
| P6-AUDIT-27 | minor | `TestADiagnosticIsAuditedAndTheCapabilitiesListThem` checked the audit entries only loosely. | **Fixed (a92abff):** the details were already compared exactly after P6-AUDIT-14; the test now also asserts the actor (token, name `test-full`, id set), the `via` field of `api`, and no object or revision on both entries (helper `auditEntries`). |
| P6-AUDIT-28 | minor | Several metric families of `GET /metrics` were asserted by no test (`chaosgw_leases`, `runs_active`, `runs_queued`, `start_time_seconds`, `uptime_seconds`). | **Fixed (a92abff):** `TestMetricsReportTheLeasesTheRunsAndTheClockOfTheProcess` asserts the start time and the uptime exactly on a fake clock, `leases` = 2 from the DHCP fake, and `runs_active` and `runs_queued` through a run, a queued run and two aborts; mutations of the start time unit, the lease slice and the active count make it fail. |
| P6-AUDIT-29 | major | `POST /probes/{id}/calibrate` replaced and then deleted the caller's own overlay of the same key on the probe: the overlay of the measurement has the owner, kind and target of a profile activation (or a match-less fault), so the store replaced the user's activation and the deferred `DeleteOverlay` removed it. | **Fixed (d6efa02):** `CalibrateProbe` builds the overlay of the measurement first and refuses with `409 target_busy` if the caller already has an overlay with the same key on the probe (`calibrationRequest`, `calibrationClash`, `calibrationOverlay` in `internal/api/probes.go`); the spec text is updated. `TestACalibrationDoesNotReplaceAnOverlayOfTheCallerOnTheProbe` covers a profile and a fault and fails with the check disabled. The few milliseconds between the check and `PutOverlay` are not closed atomically. |
| P6-AUDIT-30 | major | The audit entry `diagnostic.run` names `host` and `port`, not the target the diagnostic really hit, and never the probe (the same defect as P6-AUDIT-14, found by the API lens). | **Already fixed by P6-AUDIT-14 (d10a94e):** `diagnosticDetail` builds the line from the effective target; `TestTheAuditEntryOfADiagnosticNamesWhatWasContacted` and the probe test cover http with `url`, dns with `dns_name` and a probe run. Not done, both optional in the audit: an `audit.Object` for the probe (the object is deliberately nil on gateway runs, which an existing test asserts), and refusing `host` and `port` together with `url`. |
| P6-AUDIT-31 | minor | The spec did not say that diagnostics may reach every destination, as the acceptance of P6-M24-01 (a) requires. | **Fixed (d6efa02):** the `runDiagnostic` description and the `overlays` row of the scope table in `api/openapi.yaml` say that the scope may reach every destination and that each run is audited as `diagnostic.run`. `make generate-go` gives no diff, `make check-spec` is green. |
| P6-AUDIT-32 | minor | The plan header and the M24 status still said P6-M24-01 and P6-M25-01 wait for the maintainer. | **Fixed (d6efa02):** both say they were decided on 2026-10-28 (option (a) each). |
| P6-AUDIT-33 | minor | `docs/development.md` (M24) still described `from.probe` as `unsupported_feature` (decision 6) and said no decision needed the maintainer. | **Fixed (d6efa02):** the `unsupported_feature` text is removed, decision 6 is marked as superseded by M25, and decision 7 and the "none needs the maintainer" line name P6-M24-01 as decided. |
| P6-AUDIT-34 | minor | The package table of `docs/development.md` lacked `internal/probe`, `internal/calibrate` and `internal/metrics`. | **Fixed (d6efa02):** a row for each. |
| P6-AUDIT-35 | minor | The M26 decisions in `docs/development.md` had two entries numbered 8 (eleven decisions, "1 to 10" quoted). | **Fixed (d6efa02):** renumbered 1 to 11 (the DNS counter stays 8), and the plan's M26 status says "decisions 1 to 11" (the later counts are P6-AUDIT-69). |
| P6-AUDIT-36 | minor | The M25 section did not mention the `rp_filter` steps that `probe_ns` runs (commit `7be4264`), although it lists the executor's fixed commands. | **Fixed (d6efa02):** the sysctls are in the command list of "The probe in the kernel", and the new M25 decision 10 says why (the DHCP answers come from an address the client has no route to yet; `rp_filter=2` dropped them, CI run 40578380960). The plan's M25 status says "decisions 1 to 10". |
| P6-AUDIT-37 | minor | `docs/open-items.md` had no Phase 6 entry in its index, named a `config patch` command that `chaosctl` does not have, cited D27 for a feature that is not one of section 8, and the plan gave M26a "Depends on: M25, M26". | **Fixed (d6efa02):** a Phase 6 entry in the index; "config patch" is replaced by the API merge patch or the YAML import; D27 is no longer cited; M26a says "Depends on: M25". |
| P6-AUDIT-38 | minor | The stop and teardown scope (plan 3.11, M27, D35, the open item "Shutdown and host cleanup") did not list the probes' namespaces `cgp-<8 hex>` and veth ends `pv<8 hex>`. | **Fixed (d6efa02):** both are in the Stop paragraph, the M27 Scope, D35 and the open item; P6-AUDIT-64 adds the DHCP clients and the remark about ports of a deleted bridge. |
| P6-AUDIT-40 | minor | The plan's token-scope table (2.15) said `overlays (overlays, runs, own reset)` while the spec's table and §2.12 put diagnostics and calibration into that scope. | **Fixed (d6efa02):** the row reads "overlays, runs, captures, diagnostics, probe calibration, own reset". |
| P6-AUDIT-41 | major | Closing the Measure dialog during a measurement left it busy and put the old result into the next opening: `ModalDialog` had no busy guard, `watch(open)` did not reset `busy`, and the pending `calibrateProbe` assigned its result into whatever opening was current (reproduced with a scratch Playwright run). | **Fixed (ecc5ac8):** `ModalDialog` has a `busy` prop (Escape, outside click, the X and Close wait); the Measure dialog sets it, so the old result and the busy state can no longer land in a later opening. Playwright test closes mid-run and reopens for another probe. |
| P6-AUDIT-42 | minor | The first removal of a probe was saved without the revision the dialog was opened on: `RemoveProbeDialog`'s `watch(open)` was not immediate and the dialog is mounted with `v-if` in the same tick as it opens, so `editFrom(undefined)` kept the newer base. | **Fixed in part (ecc5ac8):** `RemoveProbeDialog` has an immediate watcher; the Playwright test (first Remove, a revision applied meanwhile) meets the concurrent-change dialog and fails without the fix. The same claim for `RemoveDeviceDialog` was wrong: `DevicesView.remove()` awaits `nextTick` before it opens the dialog, and a test written against it passed without a change, so the change and the test were dropped. |
| P6-AUDIT-43 | minor | The probe dialog ignored a revision that waits for confirmation (Add and Edit), unlike every other dialog and `RemoveProbeDialog`. | **Fixed differently (ecc5ac8):** `ProbeDialog` disables the submit and says why under `pending_confirm`. Decision on Measure: a profile measurement also waits (the dialog and `ProfilesView`), because a rollback of the pending revision removes the calibration overlay; a probe's faults make no overlay and stay allowed. Tests in `probe-management.spec.ts` and `profiles.spec.ts`. |
| P6-AUDIT-44 | minor | A diagnostic from a probe named the probe by its UUID, and the session list did not name the source at all. | **Fixed (ecc5ac8):** a run keeps the probe's name from its start; the result says "from the probe probe-a" and each history row says "from probe-a" or "from the gateway". The assertion in `probes.spec.ts` is the name, not the UUID. |
| P6-AUDIT-45 | minor | The session list labelled every failed run "Refused", also a 5xx and an unreachable gateway. | **Fixed (ecc5ac8):** `DiagnosticRun` has a failure kind and the history shows Refused (4xx), Gateway error (5xx) or No answer (unreachable, aborted). Playwright test with 422, 500 and an aborted request, plus a unit test. |
| P6-AUDIT-46 | minor | Run stayed disabled with no visible reason when the invalid field was under a collapsed Advanced section. | **Fixed (ecc5ac8):** `DiagnosticForm` lists the messages of the Advanced fields below the button while the section is collapsed, and Run has `aria-describedby` to the list. Playwright test. |
| P6-AUDIT-47 | minor | A failed read of a polled list was silent: the probes list showed "status unknown" with no alert and the Flows table stayed "Live" with frozen rows. | **Fixed (ecc5ac8):** the probes card shows a `LoadError` (with Try again) beside the rows it keeps and no longer claims "no probes"; the Flows screen shows "Not updating" plus a `LoadError` while keeping the rows. Playwright tests for both. |
| P6-AUDIT-48 | minor | Show more on the Flows screen dropped the keyboard focus when it removed its own button. | **Fixed (ecc5ac8):** on the last page the focus moves to the count line (`focusAfterUpdate`). Playwright `toBeFocused` test. |
| P6-AUDIT-49 | minor | The result of a diagnostic and of a measurement was not announced. | **Fixed (ecc5ac8):** persistent `role=status` regions (screen reader only) announce the end of a run, a calibration and an explanation; the `aria-live` of `ExplainCard` on a node inserted with its content is removed. Assertions in the diagnostics, probes and faults specs. `docs/development.md` has decisions 9 to 12 in the M26a section and the corrected test count. |
| P6-AUDIT-50 | major | The path badge turned orange for DHCP and DNS faults, which do not act on the path. `pathState()` put every family with an `explain` winner on the badge, but a DHCP action overlay is a candidate for every destination and a nameless DNS fault matches an IP too; a DNS fault on exactly the destination's name left the line blue. Confirmed against the real API in a copy of the tree; the Playwright test enshrined the behaviour. | **Fixed (4d4dbd3):** `pathState` puts only impairment, mtu, tls and tunnel on the badge; the DNS and DHCP winners go into a separate line "In force for the device, not on the way". Tests: vitest, Playwright (the old "slow-dns in the badge" case now uses mtu), and a Go API test that pins what `explain` returns for a DHCP overlay and a nameless DNS overlay. A TLS fault restricted to an SNI is still not matched by the path view, because `explain` has no `sni` parameter. |
| P6-AUDIT-51 | major | `GET /metrics` had no deadline: a hanging executor read made the whole scrape fail with no body, so `chaosgw_collector_up` could not say which source failed and scrapes piled up. | **Fixed (4d4dbd3):** each source (counters and queues, links) has a deadline of 3 s (`Config.MetricsReadTimeout`) besides the 3 s of health; a hanging read leaves its source out with `collector_up` 0 and the scrape answers 200. Tests for a hanging nft, tc and links read (the nft case hangs forever without the fix); the harness got `apiExecWrap` and `metricsTimeout`. |
| P6-AUDIT-52 | minor | `chaosgw_flows_active` counted a connection between two devices twice (the per-device count adds one to the source and one to the destination, and the total summed them). | **Fixed (4d4dbd3):** the total counts each connection that has a device as a party once (a tracker `countFlows` feeds `Snapshot.FlowsActive` and `FlowsKnown`; the series is left out while unknown). The spec and the docs say that a connection between two devices counts for both in the per-device series. Test with two devices and one connection (1 each, 1 in total), then 2 for a second connection. |
| P6-AUDIT-53 | minor | Path view: a TCP reset read as "Dropped", and a blocked path still listed winning faults as if they acted. | **Fixed (4d4dbd3):** reset reads "Reset by ..."; a blocked path with winning faults prints "These faults take effect only if a rule allows the traffic." Unit test for reset and a Playwright case for a blocked path with a fault. |
| P6-AUDIT-54 | minor | The path view's "To" selector was blank until the user picked a destination (Vue sets `selectedIndex = -1` when no option matches). | **Fixed (4d4dbd3):** the select is bound through a computed that falls back to the drawn destination. The Playwright test asserts the checked option before anything is chosen. |
| P6-AUDIT-55 | minor | Every poll of the flow list re-read and parsed the whole conntrack table once per page (up to ten dumps per poll for 5000 connections). | **Fixed differently (4d4dbd3):** requests that carry a cursor reuse the conntrack read of the first page for up to 5 s (`flowReadCache`); a request without a cursor always reads, so a polling client sees the table as it is. Test: a listing of three pages reads conntrack once and the next listing reads once again. No snapshot token in the cursor; the decision note is in `docs/development.md`. |
| P6-AUDIT-58 | minor | `Explain` (and so the path view) took the numerically lowest address of a device as its source, not the current one, and the path view never sent the flow's `src`. | **Fixed (4d4dbd3):** `Explain` uses `Identity.Current[device][0]` first, then the device state's addresses, then `Identity.Addresses[0]`; `PathTarget` keeps the connections' `src` and `PathView` sends it with `device`. Engine test with two addresses where the sort order differs from the current one (fails without the fix); the e2e query assertions include `src`. |
| P6-AUDIT-59 | minor | The metrics tests did not cover the failure paths, the re-apply epochs or escaping through the endpoint, and `parseScrape` did not check grouping or order. | **Fixed (4d4dbd3):** `parseScrape` checks that HELP precedes TYPE, that there is one TYPE per family and that a family's samples are contiguous. New tests: failing queue and links reads, hanging reads, counters and epochs across a re-apply and across an overlay that makes a new queue, and label escaping through the endpoint (configuration names cannot contain quotes, so it uses the build version, the only free-text label). Spec text, `docs/development.md` and `docs/install.md` are updated. |
| P6-AUDIT-60 | major | The audit line of an http diagnostic named a placeholder host, not the URL fetched, and never said the run came from a probe (the same defect as P6-AUDIT-14, found by the plan lens). | **Fixed (b00df9c) beyond P6-AUDIT-14:** the http, dns and probe parts were already fixed and tested; a tls or path run now names its SNI, and the audit entry of a calibration names the echo server `host:port`. Tests cover both. |
| P6-AUDIT-61 | minor | The spec said nothing of the destinations of a diagnostic or of its audit entry (the same as P6-AUDIT-31, for `calibrateProbe`'s echo host too). | **Fixed (b00df9c):** `runDiagnostic` already said it (P6-AUDIT-31); `calibrateProbe` now says the same for the echo host and the `probe.calibrate` audit entry. `make check-spec` and `make generate-go` ran, nothing generated changed. |
| P6-AUDIT-62 | minor | A name was resolved at level 1 only through a stand-in DNS server, never through the gateway's DNS proxy in the service namespace, from the gateway or from a probe. | **Fixed (b00df9c):** the level 1 test `TestNamesAreResolvedThroughTheDNSProxyOfTheServiceNamespace` (list `M24_LEVEL1`) uses the default `DiagnosticsConfig`, the real DNS proxy and a probe, runs `dns` and `path` by name from the gateway and from the probe, and checks the query log for the clients `169.254.100.1` and `10.10.0.150`. It compiles and vets with `-tags testbed`; it did not run here. |
| P6-AUDIT-63 | minor | A probe's DHCP client dies with the executor process, and the recovery path was untested (`has no DHCP client` had no test; a comment in `integration_probes_test.go` described a restart that the code under it did not do). | **Fixed (b00df9c):** the recovery exists since P6-AUDIT-12 but was unreliable: the existing resync test failed in about 1 run in 5 because an identity update that followed a resync at once was applied as element updates although a newer change was waiting. `internal/engine/applyloop.go` now applies such a state in full (60 of 60 runs passed afterwards). New apply test for the `has no DHCP client` case on the simulated kernel with a restarted executor; it fails when the case is disabled. The misleading comment is corrected. |
| P6-AUDIT-64 | minor | The stop cleanup and `chaosgw teardown` (D35, M27) listed no probe namespaces, pairs or DHCP clients. | **Fixed (b00df9c):** the probes' DHCP clients (stopped first) and the remark that a deleted bridge does not delete its ports are in the Stop line, the Restart paragraph, D35, the M27 Scope and test sentence, the open item and the M25 notes in `docs/development.md` (the names were added under P6-AUDIT-38). |
| P6-AUDIT-65 | minor | M26a: the block of a probe's removal by a scenario that targets it was not tested at all levels. | **Fixed (b00df9c):** the Playwright and draft-store tests already existed (P6-AUDIT-26); the missing `chaosctl` case is added: a scenario that targets the probe, or a step of it, makes `probe remove` exit 2 with `/scenarios/` in the error and store no revision. |
| P6-AUDIT-66 | minor | Flows screen: "unknown host" and "Show more" (200 rows) were promised in §2.17 and not tested. | **Fixed (b00df9c):** two Playwright tests in `flows.spec.ts`: a flow without a device ("unknown host", no link, the network under it only where it is known), and 450 flows (200 drawn, Show more twice, the list kept across a refetch). |
| P6-AUDIT-67 | minor | A probe could be added to a network without DHCP and was silently never reachable. | **Fixed (b00df9c):** `ProbeDialog` shows a non-blocking note under the network field when the chosen network serves no DHCP (`dhcpServed`), and `chaosctl probe add` prints the same warning on stderr. A Playwright test and a `chaosctl` test. |
| P6-AUDIT-69 | minor | Stale and inconsistent status text in the plan and `docs/development.md` for Phase 6. | **Fixed (b00df9c):** the M24 status and the numbering of the M26 decisions were already fixed (P6-AUDIT-32, -35); the four "no CI run yet" statuses name PR #125 and CI run 40771332746 on `7048cae` (green in every job, the VM job included); the decision counts of M26 (14) and M26a (13) are corrected; the M24 level 1 count is three. |

### Per milestone

| Milestone | Verdict | Audit items (state after the round of 2026-10-28) |
|---|---|---|
| M24 Diagnostics | done with gaps | P6-AUDIT-14, -15, -21, -30, -60 (major); -17, -18, -19, -20, -22, -24, -27, -31, -33, -45, -46, -61, -62: all fixed (-22 in part, -30 by -14) |
| M25 Probes and calibration | done with gaps | P6-AUDIT-01, -02, -03, -04, -16, -29, -41 (major); -05 to -13, -23, -25, -36, -43 (Measure), -44, -63: all fixed (-13, -25 in part, -16 by -02) |
| M26 Metrics and flow view | done with gaps | P6-AUDIT-50, -51 (major); -28, -34, -35, -47, -48, -49, -52, -53, -54, -55, -58, -59, -66: all fixed (-55 differently) |
| M26a Probe management | done with gaps | P6-AUDIT-26, -42, -43 (the dialog), -65, -67: all fixed (-42 in part, -43 differently) |
| Cross-cutting (plan, spec, docs, stop and teardown) | done with gaps | P6-AUDIT-32, -37, -38, -40, -64, -69: all fixed |

What each milestone delivers, as the lenses read it. **M24**: the seven types and their structured results (a target that fails is a `200` with
`ok: false`), the path of §2.12, the bounds of the response and the 60 s ceiling, the TLS chain and the reason a certificate does not verify,
the capabilities, the unprivileged ICMP socket and `ping_group_range` in `deploy/host-setup.sh`, and the Diagnostics screen. **M25**: the
namespace `cgp-<8 hex>` and the veth end `pv<8 hex>` as a port of the bridge, the MAC derived from the id, the DHCPv4 client that gets,
renews, rebinds and loses a lease, the observation that identifies the probe like any device, the faults and rules that act on the probe
only, `probe_run` in the executor with the probe's lease servers and the test CA only when it trusts it, and the self-test with its
reference run, its overlay that is removed afterwards and the verdicts of §4.3. **M26**: the text format and the counters as the REST
API shows them, the epochs of the queues, a source left out with `collector_up` 0, the Flows screen and the device detail's cards and the
path view. **M26a**: the probe dialog and its checks over the draft, Remove with the objects that name the probe, `chaosctl probe`.
Regressions to code from before Phase 6: none found.

### Evidence

- **CI on the branch:** run 40639560679 on `b80fa86`, every job green, the level 1 tests in the privileged container and the VM job
  included. Not run in the audit (no KVM, no netns). No CI run has covered the fix commits of the round yet. No Nightly ran on any Phase 6
  commit.
- **Built and run in the audit:** `go build ./...`, single-package Go tests of `api`, `domain`, `ctl`, `cmd/chaosctl`, `tools`, `diag`,
  `calibrate`, `metrics`, `make check-spec`, `make generate-go` (no diff), `make check-clients` (green, only the known
  `application/x-pem-file` warnings), vitest (636 tests) and 62 Playwright tests of the diagnostics, probes, probe management and flows
  specs against the mocked API. Not run in the audit: any level 1 test, the rest of the Playwright suite, `golangci-lint`.
- **Mutation experiments** on scratch copies, mostly through `go test -overlay` so that nothing was written to the repository: the survivors
  with a consequence are P6-AUDIT-04, -21, -22, -23, -24, -25 and -28.
- **Reproduced in scratch copies outside the repository:** P6-AUDIT-01, -02, -03, -06, -07, -17, -18 (Go scratch tests), -14, -30 and -60
  (the audit line of an http run), -41, -42, -43, -45, -46, -47, -48 and -49 (scratch Playwright specs), -50 and -51 (the real API in a copy
  of the tree), -52 (a unit test with two devices).

### Phase 6 audit: result of the round and remaining points

The round after the audit (2026-10-28, branch `worktree-phase-6`, merged to `main` with PR #125 on 2026-10-29) closed every confirmed item, see the Fix column of
the table. The seven fix commits, by lens: `c29d100` (probes, P6-AUDIT-01 to -13), `d10a94e` (diagnostics and security, -14 to -20),
`a92abff` (tests, -21 to -28), `d6efa02` (API, spec and docs, -29 to -38 and -40), `ecc5ac8` (web app, -41 to -49), `4d4dbd3` (metrics
and the flow view, -50 to -55, -58, -59) and `b00df9c` (plan completeness, -60 to -67 and -69). The final checks of the round and what
ran are in the last paragraph of this section. No item needed the maintainer, so the round decided nothing on the maintainer's behalf.

The CI round after the audit (2026-10-28, runs 40680300170 to 40771332746 on PR #125) found what the tests of this machine cannot: level 1 needs
namespaces, a real kernel and a host that is shared by several gateways. Five defects of the code and three of the tests, all fixed on
the branch; the run 40771332746 on `7048cae` is green in every job:

| Commit | Found by | What |
|---|---|---|
| `7be4264` | M25 level 1 (privileged container) | The probe's namespace has `rp_filter` off: on a host with `rp_filter` 2 (Ubuntu) the answers of the DHCP server, which come from an address the client has no route to yet, were dropped, so no probe got a lease. |
| `0b61040` | unit tests | The simulated kernel knows the sysctl directories `all` and `default` of every namespace. |
| `be42a74` | M25 level 1 | The read of the probe's interface inside its namespace is repeated twice before the probe counts as having no address. |
| `88399d3` | M25 level 1 (the test printed the namespace) | The probe's pair is deleted and made again only when its namespace cannot be opened: a failing `ip netns exec` for a moment was read as "the namespace is gone", and the pair with the probe's address was replaced. |
| `60a8623` | M25 level 1 | A namespace of the probes' name counts as a probe without its pair only when its interface is certainly missing: the list of namespaces is the host's, and the engine of a test that was still running removed the namespace of the next test's probe (P6-AUDIT-13's discovery of namespaces without a pair). In production one gateway owns every `cgp-*` namespace of its host, so this showed in the testbed only. |
| `627d329` | arm64 (qemu-user) | The test of a renewal time of zero moves the fake clock in steps until the renewal comes (`BlockUntil(1)` also counts the timer of the answered request). |
| `6948ddd`, `7048cae` | M24 and M25 level 1 | The DNS level 1 test accepts `REFUSED` from the resolver of the server namespace; "a path without a fault loses nothing" is a statistical assertion like the others, repeated once (plan §4.3), because one datagram in 1000 was lost by chance on the real kernel. |

One remark for the maintainer, not an open item: for an unimpaired path the self-test expects no loss at all, because the 99.9 % interval
of 0 % is 0 itself (plan §4.3), so a single lost datagram makes "measure the current faults" report "not within the tolerances".

Decisions taken by best practice, not by the maintainer:

- **P6-AUDIT-03**: the jitter limit is 1.2 times the spread of the distribution plus the tolerance of §4.3; a pareto distribution is not
  checked and the result's detail says so.
- **P6-AUDIT-07**: the DHCP client floors T1 at 1 s and T2 at T1 plus 1 s, and doubles its pause after a NAK from the retry time to the
  maximum.
- **P6-AUDIT-15**: 5 diagnostics a second per principal with a burst of 20, and the audit log drops `diagnostic.run` entries first when its
  bound is exceeded, then the oldest half.
- **P6-AUDIT-29**: a calibration that would replace an overlay of the caller on the probe is refused with `409 target_busy`, not run with
  the overlay saved and restored.
- **P6-AUDIT-43**: a profile measurement waits for a pending confirmation, a probe's faults do not.
- **P6-AUDIT-55**: the pages of one listing share one conntrack read of up to 5 s; a request without a cursor always reads.

Remaining points of minor weight that the round left, none of them a new item:

- **P6-AUDIT-13**: the cost of one process start (`ip netns exec`) per probe and observation stays, now bounded by four concurrent reads and
  with one more process (`ip netns list`) per read. The proposal to report the addresses from the clients and read the namespaces in
  process after `setns` was not done; it could not be measured here (no namespaces).
- **P6-AUDIT-03**: the fix assumes, as the audit did, that netem's `normal` table has a sigma equal to the jitter. This was not checked on
  a real kernel (level 1 runs in CI); if the sigma differs, the margin of 1.2 may be too tight or too loose.
- **P6-AUDIT-08**: the reference run keeps its fixed 1 s settle time, because the path is unimpaired and its round trip is not known
  beforehand; its completeness is checked instead (P6-AUDIT-05).
- **P6-AUDIT-16**: a third probe run waits for a probe-run slot until its context ends (up to 60 s) and holds one of the eight API places
  meanwhile, so many probe diagnostics can make gateway diagnostics answer `capacity_exceeded`. Diagnostics only; the reads are not affected.
- **P6-AUDIT-25**: the loose 5 to 25 % loss bound of the emulated-kernel branch of `e2e_probes_test.go` stays; it cannot tell 10 % from
  the configured 14.5 %, and a tighter bound for the emulator would be a guess.
- **P6-AUDIT-29**: the check and `PutOverlay` are not one atomic step; an overlay the caller creates in those few milliseconds is still
  replaced.
- **P6-AUDIT-30**: no `audit.Object` for the probe on a probe run, and `host` and `port` are not refused together with `url`; both were
  optional in the audit.
- **P6-AUDIT-50**: a TLS fault restricted to an SNI is not matched by the path view, because `GET /explain` has no `sni` parameter and the
  view sends none.
- **P6-AUDIT-63**: the fix of `applyloop.go` has no deterministic unit test, only the existing resync test, which failed in about 1 run in
  5 and passed 60 of 60 afterwards; it was not rerun under `-race`.
- **Not run for the new level 1 tests**: `TestNamesAreResolvedThroughTheDNSProxyOfTheServiceNamespace` (P6-AUDIT-62) and the changed
  accuracy assertions of `e2e_probes_test.go` (P6-AUDIT-25) are vetted with `-tags testbed` and run in CI, which has not covered the fix
  commits yet.

### Phase 6 audit: rejected on verification

Four findings were refuted by the skeptics and are not items; they are listed so that the round is auditable.

| Id | Finding as reported | Why it was rejected |
|---|---|---|
| P6-AUDIT-39 | The Phase 7 milestones do not carry the follow-ups of Phase 6 (the token-scope review of diagnostics, `ping_group_range` in the final host setup). | Both verifiers refuted it. The text claims are accurate (M29's Scope says "token scopes" without naming diagnostics, M28's says "modules, ip_forward, netplan hints" without `ping_group_range`), but both follow-ups have an owner: the plan (§2.12) and the closed item P6-M24-01 name M29 and M30 for the scope review, which "token scopes" covers, and `deploy/host-setup.sh` already writes `net.ipv4.ping_group_range=0 2147483647` (`docs/install.md` and `docs/development.md` say so), which M28's "final host-setup script" finalises. A rewrite that dropped the sysctl would fail `internal/preflight/hostsetup_test.go`, which asserts the content of `/etc/sysctl.d/90-chaos-gateway.conf`. What remains is a wording nit in the milestone scope text, not a defect. |
| P6-AUDIT-56 | Fault, rule and forward counters carry no epoch, and the fault series is a sum that can fall on a re-apply. | One verifier confirmed the code (`counterOf` sums the compiled faults of a source and the metrics path writes only packets and bytes; only the whole table `chaosgw_counter_epoch` and the per-queue epoch are exported), the other refuted it as a documented M26 design choice: `docs/development.md` ("The endpoint") and `api/openapi.yaml` say that a counter that starts over is a counter reset for Prometheus and that the per-object epochs stay in the REST API. Plan §2.12 and §2.13 only require that the API reports the counter epoch. A series that vanishes and reappears with a deleted and re-created fault is ordinary Prometheus series behaviour, and a reset is hidden only if the whole cycle happens between two scrapes and the new value reaches the old one. At most an optional enhancement; no later milestone or open item promises per-object epoch gauges. |
| P6-AUDIT-57 | Queue series multiply by every interface of the tree, with no bound written down. | One verifier confirmed the arithmetic (faults times directions times interfaces times 7 series, with no filter on "has ever sent"), the other refuted it as the documented design: the queue family is "per netem leaf" (`fault_id`, `layer`, `source`, `interface`, `direction`) in `docs/development.md` and `api/openapi.yaml`, the REST view iterates `TCDevs` the same way, and plan §2.12 asks for statistics per netem queue. Decision 5 bounds only the interface series. The proposed fix, to emit a queue only once it has sent a packet, would make series appear late, which loses the first sample of `rate()` and conflicts with the rule that a read series is reported and an unread one left out. The scale in the scenario (100 faults on 6 interfaces, 8400 series) is hypothetical for a single-appliance lab gateway. What remains is a one-line documentation note, not a defect. |
| P6-AUDIT-68 | No test shows a scenario or run targeting a probe, although §2.12 offers probes for "testing a scenario before connecting real hardware". | One verifier confirmed the coverage gap (no run, check or capture test targets a probe; `grep -rli probe internal/run` finds nothing), the other refuted a defect: the run machinery is origin-agnostic (`scopeDevices` filters by id, group member or network, never by origin; `claimDevice` registers probes as devices; the engine builds a `DeviceState` for a probe like for any device), so a probe has an id, network, addresses and an online state like a configured device or a WireGuard client, which the run tests exercise. Nothing in the code contradicts the plan's promise; what remains is a possible low-priority regression test, not a plan-completeness defect. |

Further observations of the lenses that are not defects:

1. *The M26 fault, rule and forward counters have no epoch, and the queue series are many.* Both are the documented design, see P6-AUDIT-56
   and -57 above.

### Phase 6 audit: final checks of the round

The round was built and checked on the final commit of the fixes (`b00df9c`, plus the docs commit that writes this section) without KVM,
without network namespaces and without Docker: `go build ./...`, `go vet ./...` and `go vet -tags testbed ./...`, `gofmt -l` (no file),
`golangci-lint run ./...` (two `unconvert` findings in `internal/api/diagnostics.go`, left by P6-AUDIT-14, fixed in the docs commit, then
0 issues), `make check-spec`, `make check-generated` (no diff in the committed generated code) and `make check-clients` were green (the
Python generator prints its known warning about the PEM bodies). `go test -count=1 -timeout 40m ./...` passed in every package but two:
`internal/executor` `TestACaptureIsRefusedOutsideTheScopeAndForBadRequests` fails with "the disk is almost full" because the machine has
less than 10 % free (known, the same on `main`; the rest of the package passes), and `internal/tlsresponder`
`TestACAThatChangesIsReloadedIntoTheIssuer` failed once while the web checks ran at the same time and passed 5 of 5 runs alone (a package
no Phase 6 commit touched). The tests of the round that use a Unix socket in `t.TempDir()` need a short `TMPDIR` (the run used
`/root/t48`), because a long one passes the 108 bytes of a socket address. The web app: `npm run typecheck`, vitest (642 tests) and the
whole Playwright suite (397 tests against the mocked API, `E2E_PORT=4571`) passed. Not run here: every level 1 test (the testbed in the
privileged container and in the VM), the testbed Playwright config and the nightly kernel matrix; the level 1 tests the round added or
changed (`TestNamesAreResolvedThroughTheDNSProxyOfTheServiceNamespace` in `M24_LEVEL1` and the accuracy assertions of `e2e_probes_test.go`
in `M25_LEVEL1`) are vetted with `-tags testbed` and run in the next CI run, which has not covered the fix commits yet.

## Phase 6 open items

Phase 6 (M24 to M26a, diagnostics, observability and probe management) is complete: M24, M25, M26 and M26a are implemented and closed after their reviews and merged to `main` with PR #125 on 2026-10-29; M26 found nothing that needs a decision, M26a one (P6-M26a-01, decided on 2026-10-29: part of V1). Items below are what the implementation of a milestone found that no later milestone of the plan completes and that needs a decision of the maintainer.

### P6-M24-01 A diagnostic can be pointed at any address the gateway can reach, by a token with the scope `overlays`

- Status: closed (2026-10-28): decided by the maintainer, option (a): the scope `overlays` reaches every destination and every run is audited (`diagnostic.run`); plan §2.12 says so; the scopes are looked at again with the review of the token scopes in Phase 7 (M29, M30)
- Severity: minor
- Reason: needs-decision. The spec gives `POST /diagnostics` the scope `overlays` (the scope of automation: overlays, runs, own reset), and a diagnostic opens its connections from the gateway's own network stack, which in production is the host's (plan §3.8: the API container uses the host network namespace). A token with that scope can therefore make the gateway connect to the loopback of the host, to the management network (which the matrix and the management guard of plan §2.16 keep the test networks out of), to the Docker networks and to every address behind the uplink, and read what comes back: the status line and the headers of an HTTP service that only the gateway can reach, the certificate of a TLS service, whether a port answers. The plan names the scope and says nothing of the destinations; M24 limits none and records each run in the audit log (`diagnostic.run`, with the type and the target).
- Evidence: `api/openapi.yaml` (`runDiagnostic`, `x-required-scope: overlays`), `internal/api/diagnostics.go`, `docs/development.md` "Diagnostics (M24)", decision 7; plan §2.12 ("diagnostics from the gateway's own address do not pass through device-specific faults"), §2.16.
- Task: decide whether the destinations of a diagnostic are limited, and by which scope.
- Options: (a) as built: the scope `overlays`, any destination, audited; (b) the scope `full` for every diagnostic (the CI pipelines that want a ping then need a full token); (c) the scope `overlays` for destinations in the test networks and behind the uplink, and the scope `full` for the loopback, the link-local addresses, the Docker networks and the management network (the API refuses the others with `forbidden` and the reason).
- Recommendation: (a) for V1: the gateway is a lab appliance whose owner makes the tokens, the audit log records every run, and a destination that is limited by address would also have to follow what the DNS proxy resolves a name to. Look at (c) with the review of the token scopes in Phase 7 (M29, M30), where the other uses of the scopes are decided.
- Acceptance: the spec says which scope each destination needs, and a test of the API shows a token with the smaller scope refused for a destination that needs the larger one (for (b) and (c)); or the plan and the spec say that the scope `overlays` may reach every destination (for (a)).
- Needs maintainer: yes
- Effort: S

### P6-M25-01 A probe cannot be added, changed or removed in the web UI or with `chaosctl`

- Status: closed (2026-10-28): decided by the maintainer, option (a): a probe dialog over the draft and `chaosctl probe add|remove|list`, in the new milestone M26a (Probe management, M) after M26 in Phase 6 of `docs/plan.md`; built in M26a on the branch `worktree-phase-6` on 2026-10-28 (not merged yet, no CI run yet): the probe dialog with Remove and the preview, `chaosctl probe add|remove|list`, the Playwright test, the `chaosctl` test, the empty state that opens the dialog
- Severity: minor
- Reason: needs-decision. The plan's Scope for M25 names the probes themselves (virtual clients, DHCP address, targeting, diagnostics, the self-test) and not a form for them. A probe is an object of the configuration (`probes`: name, network, `trusts_test_ca`, labels), like a custom profile or a port forward: it is made with a revision over the API (a merge patch, or the YAML import), and the web UI lists the probes, runs a diagnostic from one and measures a profile with one, but has no dialog that adds one to the draft, edits its network or removes it, and `chaosctl` has no `probe` command. The draft of the web app (`web/src/stores/draft.ts`) keeps one set of functions per kind of object (the changes, the patch, the rebase and the overlap check), so a form for probes is a piece of work of its own, not a dialog over an existing kind.
- Evidence: `docs/development.md` "Probes and calibration (M25)", decision 9; `web/src/components/diagnostics/ProbesCard.vue` (the empty state names `probes` of the configuration); `api/openapi.yaml` (`probes` of `Configuration`; no `POST /probes`).
- Task: decide whether a probe can be made in the UI and with `chaosctl` in V1, and in which milestone.
- Options: (a) a probe dialog over the draft (name, network, trusts the test CA) with Remove and the preview of the changes, plus `chaosctl probe add|remove|list` (an M26 follow-up or a milestone of its own); (b) only the dialog, `chaosctl` has no command for it and a probe is also made with the API (a merge patch); (c) as built: probes are configuration, made over the API and the YAML import, until a user asks.
- Recommendation: (a) after Phase 6: a tester who wants to try a scenario "before the hardware is connected" (plan §2.12) should be able to add the probe where they work, and the draft machinery for it is the same as for the port forwards (M35a).
- Acceptance: for (a) and (b), a Playwright test adds a probe through the dialog and the drawer, sees it in the list with its address, and removes it; for (a) a `chaosctl` test lists, adds and removes one; for (c) the plan says that probes are made over the API.
- Needs maintainer: yes
- Effort: M

### P6-M26a-01 Whether probe management (M26a) belongs to V1

- Status: closed (2026-10-29): decided by the maintainer, option (a): M26a is part of V1, like M25; the "V1 Scope Summary" of `docs/plan.md` names it
- Severity: minor
- Reason: needs-decision. The V1 scope summary of `docs/plan.md` names M24 and M25 and does not name M26a, which the maintainer added on 2026-10-28 (P6-M25-01, option (a)) as the part of M25 that lets a user add, change and remove a probe in the web UI and with `chaosctl`. M25 itself is in V1. Without M26a a probe is made only by editing the configuration (a revision made with the API, a merge patch, or the YAML import), so the probes of V1 (testing "before the hardware is connected", plan §2.12) would be reachable from the web UI for diagnostics and measurements but not creatable there. Whether the feature ships with V1 is a scope decision; the plan's rule is that nothing is in V1 unless the maintainer put it there (the "V1 Scope Summary" of `docs/plan.md` lists what is).
- Evidence: `docs/plan.md` ("V1 Scope Summary", M26a, §2.17 "Probes"), `docs/development.md` "Probe management (M26a)", `docs/open-items.md` P6-M25-01.
- Task: decide whether M26a is part of V1, optional for V1, or ships after V1.
- Options: (a) part of V1, like M25 (the summary lists M26a; nothing else changes because the code exists and passes its level 0 tests); (b) optional for V1, like M26 (it ships if it is merged and green at the release, and the release notes say that probes are made over the API otherwise); (c) after V1, like M23c (the code stays on the branch, hidden or merged behind no flag; the UI would then have to lose *Add probe* before the release).
- Recommendation: (a): M25 is in V1 and its probes are of little use if a tester has to write YAML to make one; M26a adds no API and no spec change, only a form and a `chaosctl` command over the draft machinery that is in V1 already (M35a), so it costs V1 nothing beyond the CI run it needs anyway.
- Acceptance: the V1 scope summary of `docs/plan.md` names M26a (for (a)), lists it among the optional milestones (for (b)) or among those that ship after V1 (for (c)); for (c) the UI also loses *Add probe*.
- Needs maintainer: yes
- Effort: S

## Phase 7 open items

### P7-M29-01 Who reviews the executor's interface from outside, when, and for how long

- Status: closed (2026-10-31): decided by the maintainer, option (c): the external review of the executor interface is not part of V1 and moves to M38 (After V1); the plan (M29 Scope, M38), `docs/development.md` and the release notes say so
- Severity: major
- Reason: needs-decision. The plan's M29 Scope ends with the "external review of the executor interface". A review by someone who did not write the code is a human activity: it needs a reviewer, a date and a scope, and it is a gate of the release (M30). What can be prepared is done and checked in: the threat model, the accepted risks, the table of mitigations with the test that holds each, the six fuzz targets (`make fuzz`, nightly with 45 minutes each), the source list and the command lines to run the tests, all in `docs/development.md` "Security hardening (M29)" ("Preparation for the external review"). No code can decide who reviews or whether V1 may ship without the review.
- Evidence: `docs/plan.md` M29 Scope, `docs/development.md` "Security hardening (M29)", "Preparation for the external review" (the hand-over package), `Makefile` (`fuzz`), `.github/workflows/nightly.yml` (job `fuzz`).
- Task: decide who performs the review of the executor interface (the Unix socket protocol, the operation decoder and planner, the capture path, the privileged container), when it takes place, how long it may take, and whether V1 is released without it.
- Options: (a) a person from outside the project reviews before the V1 release, for a fixed time (about two days for the 6,500 lines of the interface plus the fuzz runs), and its findings are fixed or recorded before the tag; (b) the release goes out with the self-review and the fuzz runs, the external review follows within a named period after V1, and the release notes say the interface has not been reviewed from outside yet; (c) the review is dropped from V1 and moves to M38 (After V1) with the other deferred items.
- Recommendation: (a) if a reviewer is available before the release: the interface is the one place where a taken-over API process becomes root on the host, and two days of a second pair of eyes are cheap beside that. (b) if no reviewer is found: the release notes must say so (M30).
- Acceptance: the plan's M29 text names the reviewer and the date (for (a)) or the period after V1 (for (b)); for (a) the findings are in `docs/open-items.md` and closed before the tag.
- Needs maintainer: no
- Effort: S for the decision, M for the review itself

### P7-M29-02 Kea runs without `no-new-privileges`

- Status: decided (2026-10-31): option (b) by the maintainer; `no-new-privileges: true` is set on Kea in `deploy/compose.kea.yaml` and `deploy/compose.yaml`, the exception is out of `TestNoContainerWidensItsSandbox`, the compose test, the level 2 deployment test and the container table; the CI job `deployment` passed with the option on 2026-10-31 (PR #127, run 41205381960: every container, Kea included, came up), and it stays open until the nightly level 2 DHCP test shows that Kea leases with it (if it does not, take the option out again and restore the exception with the reason)
- Severity: minor
- Reason: needs-decision. The review of the containers (M29) found one container that does not set `no-new-privileges`: `kea` (user 0:65532 with the capabilities `NET_RAW`, `NET_BIND_SERVICE`, `CHOWN`, `FOWNER`, `DAC_OVERRIDE`). Kea's Debian package may start its binaries through file capabilities, which `no-new-privileges` would break, and nothing here could start the container to try (no Docker on the development machine). The exception is pinned in `TestNoContainerWidensItsSandbox` (no other container may lack the option) and in the level 2 deployment test, so it does not widen by accident.
- Evidence: `docs/development.md` "Security hardening (M29)" (the container table, decision 7), `deploy/compose.kea.yaml`, `internal/executor/hardening_review_test.go`.
- Task: decide whether the exception stays for V1 or `no-new-privileges` is required once a level 2 run shows Kea works with it.
- Options: (a) accept the exception for V1: the container has a read-only root file system and five capabilities (raw sockets for DHCP in the host network namespace), and the tests keep the exception from growing; (b) try `no-new-privileges: true` on the nightly level 2 run, and if the DHCP test passes keep it, remove the exception from the review test, the level 2 test and the table; if it fails keep (a) and note why in the table.
- Recommendation: (b): it costs one nightly run, and (a) stays the fallback if Kea's package needs the file capabilities.
- Acceptance: for (a) the table in `docs/development.md` names the reason as accepted; for (b) the compose file sets the option, the tests no longer except `kea`, and the level 2 DHCP test passes.
- Needs maintainer: no
- Effort: S

### P7-M28-01 The arm64 image is tested by an emulated level 2 smoke test and `smoke-image.sh`, not by the namespace testbed in emulation (level 1b)

- Status: closed (2026-10-31): decided by the maintainer, option (a): the emulated level 2 smoke test and `smoke-image.sh` are the functional test of the arm64 image; the plan (M28 Tests, section 4.1 and 4.5) says so, and the testbed on arm64 runs with H1
- Severity: minor
- Reason: needs-decision. The plan's M28 Tests say that the arm64 image "passes the functional smoke test in emulated level 1b", and the table of §4.5 puts the arm64 image there (decision M1-02: "with M28"). What was built is the image smoke test `deploy/smoke-image.sh` under `qemu-user` (the binaries start and have the architecture asked for, the tools run at the pinned versions, Kea accepts its configuration, the schema check runs as the unprivileged user; the executor is not started, so there is no handshake check) and the weekly job `appliance-arm64`: the level 2 smoke test on an emulated aarch64 Ubuntu 24.04 machine, the three-port topology only, with a budget of five hours. The level 1 testbed (`make test-privileged`) on arm64 under emulation is not built: it needs a privileged arm64 container with network namespaces under `qemu-user` or a VM, and the kernel features it uses (nftables, `tc`, WireGuard) are those of the emulating host. The emulated level 2 test proves that the image and the deployment work on the architecture; it does not run the testbed there.
- Evidence: `docs/plan.md` M28 Tests and §4.5, `deploy/smoke-image.sh`, `.github/workflows/weekly.yml` (`appliance-arm64`), `docs/development.md` "Container deployment (M28)".
- Task: decide whether the emulated level 2 smoke test replaces the emulated level 1b in the plan or the testbed runs on arm64 too.
- Options: (a) accept the emulated level 2 smoke test and `smoke-image.sh`, and amend the M28 Tests and the table of §4.5 to say so; the testbed on arm64 runs on real hardware with H1; (b) build the testbed on arm64 in emulation (a weekly job with a privileged `linux/arm64` container under QEMU, which takes much longer than the budget of the weekly job and is slow enough to make timing tests unusable, so only the functional tests could run).
- Recommendation: (a): the functional smoke test of the plan is what the emulated level 2 test does, and the testbed's logic does not depend on the architecture; the Raspberry Pi targets are validated with H1 anyway.
- Acceptance: for (a) the plan's M28 Tests and §4.5 name the emulated level 2 smoke test; for (b) a weekly job runs the functional testbed tests on arm64.
- Needs maintainer: no
- Effort: S for (a), L for (b)

### P7-M27-01 Safe mode drops the management-role WireGuard networks, so a gateway administered over a tunnel loses its access

- Status: closed (2026-10-31): decided by the maintainer, option (a): safe mode is the management interface alone; the documents of M30 already say so
- Severity: minor
- Reason: needs-decision. Plan §3.9 says safe mode keeps "management access only". `domain.SafeModeConfiguration` keeps the uplink, the management interface (with its allowed sources and the UI port) and the settings, and drops every network, so a WireGuard network with the role `management` (a management access path in plan §2.14) is gone: the tunnel is down in safe mode and the gateway is reachable through the management interface only. Keeping the tunnel is not a copy: its clients' `reachable` lists name test networks that safe mode does not have, its keys are generated secrets, and a failing WireGuard apply in the configuration of last resort would end safe mode in a failed start.
- Evidence: `internal/domain/safemode.go`, `internal/domain/safemode_test.go` (`Networks == nil`), `docs/development.md` "Recovery and stop (M27)", decision 4.
- Task: decide whether a management-role WireGuard network stays up in safe mode.
- Options: (a) no: safe mode is the management interface alone (what M27 does); `docs/install.md` and the recovery instructions of M30 say that a tunnel is down and the way in is the management interface; (b) yes: the safe configuration keeps the networks of the role `management` with their clients, without the `reachable` entries, and a failing apply of the tunnel does not fail the safe configuration (the tunnel is reported as degraded).
- Note: the documents of M30 (`docs/install.md`, `docs/recovery.md`, `docs/release-notes/v1.0.0.md`) and `TestTheDocumentsStateWhatSafeModeAndAKeyRotationDo` assume option (a); a decision for (b) changes them.
- Recommendation: (a) for V1: the management interface is the access that does not depend on a key, an interface that can fail to come up or a peer, which is what the last resort should rely on; the recovery instructions (M30) say it. (b) is worth doing if operators administer the gateway only over the tunnel.
- Acceptance: for (a) the recovery instructions state that the tunnel is down in safe mode; for (b) a test of the compiler and the engine shows the management tunnel up in safe mode and a test network's client cut off.
- Needs maintainer: no
- Effort: S for (a), M for (b)

### P7-M27-02 A fallback to a revision from before a key rotation cannot be applied, because the rotation retired the key

- Status: closed (2026-10-31): decided by the maintainer, option (a): a rotation retires the old key for good and a fallback across it ends in safe mode; the documents of M30 already say so
- Severity: minor
- Reason: needs-decision. The commit of a revision that raises the key generation of a WireGuard client or link peer retires the old generation's keys (`wireguard.Prune`, pinned by `TestRotatingAClientKeyKeepsTheOldKeysUntilTheCommitAndAFailedApplyStillRestores`). The start applies the last known good revision when the active one fails (M27). `wireguard.PruneKeeping` keeps the keys that a later revision dropped altogether (a network, a client or a peer that is gone), so a fallback to a revision from before such a deletion works. A fallback to a revision from before a rotation does not: the compile finds no key for the client, and the gateway enters safe mode although that revision once worked. Keeping the retired generation would let a compromised key that was rotated away come back with the fallback, which is what a rotation is for.
- Evidence: `internal/wireguard/provision.go` (`PruneKeeping`, `keepKeysOfGone`), `internal/engine/owner.go` (`prune`), `internal/engine/recovery.go`, `docs/development.md` "Recovery and stop (M27)".
- Task: decide whether the keys of a generation that a rotation replaced stay for the fallback.
- Options: (a) no (what M27 does): a rotation retires the old key for good; the fallback to a pre-rotation revision ends in safe mode, and the way out is a new revision (a clone with the current keys); the recovery instructions (M30) say so; (b) yes, until the next commit: the generation before the last rotation stays as long as its revision is the fallback, and the recovery log names the key that came back.
- Note: the documents of M30 (`docs/recovery.md`, `docs/release-notes/v1.0.0.md`) and `TestTheDocumentsStateWhatSafeModeAndAKeyRotationDo` assume option (a); a decision for (b) changes them.
- Recommendation: (a): safe mode is the safe answer when the only revision to go back to needs a key the administrator retired on purpose; a clone of that revision with the current key applies in one step.
- Acceptance: for (a) the recovery instructions state it; for (b) a test where R2 rotates a client key and fails at start falls back to R1 and applies it.
- Needs maintainer: no
- Effort: S for (a), S to M for (b)

### P7-M27-03 The level 2 recovery test does not read `safe_mode` from `GET /state`

- Status: closed (2026-10-29): not a decision, only work, done in review: `TestRecoveryAndStopOnCleanUbuntuHosts` makes revision 1 with `POST /setup`, confirms it in a session, creates a token and reads `GET /state` (option (b)); written, not run yet (no KVM here), it runs in the nightly job
- Severity: minor
- Reason: the review found that this was no decision (the plan puts "Boot, recovery, safe mode" at level 2 and the Scope has the API report it). Plan §4 puts "Boot, recovery, safe mode, upgrades" at level 2 and the M27 Scope has the API report `safe_mode` and `safe_mode_reason`. `TestRecoveryAndStopOnCleanUbuntuHosts` sees safe mode in the log line, over SSH, in the forwarding that stopped and in the table of the safe configuration, but it never calls `GET /state`: it makes its revisions with `chaosgw apply --file` while the API is stopped, so no administrator exists and the service token has the scope `service` only. The report is checked at level 0 over HTTP (`TestSafeModeIsReportedAndHoldsBackWhatNeedsAForwardingGatewayUntilARevisionWorks`). The test cannot run here (no KVM), so a change of its flow would be unverified until the nightly run.
- Evidence: `internal/appliance/recovery_test.go`, `internal/api/recovery_test.go`, `internal/api/system.go` (`GetState`).
- Task: decide whether the level 2 test reads the API.
- Options: (a) leave it: the REST report is level 0, the real kernel's safe mode is level 2; (b) make the first revision with `POST /setup` (the API running, the setup token from its log, then login and confirm), so that the test has a session and asserts `safe_mode`, a non-empty `safe_mode_reason` and `safe_mode: false` after the restart; the revision numbers of the flow shift by one.
- Recommendation: (b) once a level 2 run is available to prove the changed flow (the nightly job, or the first CI run with KVM), because the report is the contract the UI banner relies on; until then (a).
- Acceptance: for (b) the level 2 test asserts the three values.
- Needs maintainer: no
- Effort: S

### P7-M27-04 No test kills a real executor process in the middle of an apply

- Status: closed (2026-10-29): not a decision, only work, done in review: option (c) in another form, `TestKillingTheExecutorProcessInTheMiddleOfAnApplyIsRecoveredOnTheRealKernel` runs the executor as a process of its own in the gateway's namespace and kills it with SIGKILL while it runs the nftables transaction of an apply; it is the test binary run again (the VM of the testbed has no toolchain to build the `chaosgw` binary), serving with the same executor and server code; it passed in CI on 2026-10-31 (PR #127, run 41205381960: the container and the VM job), it is in `M27_LEVEL1`
- Severity: minor
- Reason: the review found that this was no decision (the plan's clause is "killing the executor mid-apply is recovered"). The M27 test clause is "killing the executor mid-apply is recovered". Level 1 (`TestKillingTheExecutorInTheMiddleOfAnApplyIsRecoveredOnTheRealKernel`) uses an `apply.Exec` wrapper that runs the operations in front of the nftables transaction and fails every later request, on the real kernel with the real tools: the kill falls between the operations of one request. The testbed runs the executor in the test process, so a SIGKILL would end the test. Level 2 kills the executor's container with `docker kill` but in a settled state, then runs `teardown`. The recovery does not depend on where the process ended (the next start compiles and applies the committed revision against what the kernel holds, `TestAnInterruptedApplyIsRecoveredByRecompilingTheCommittedRevision`), but a cut inside an operation (half of an `ip -batch`, a half-written executor state file) is not exercised.
- Evidence: `internal/engine/integration_recovery_test.go` (`killedExec`), `internal/appliance/recovery_test.go`, `docs/development.md` "Recovery and stop (M27)", "Tests".
- Task: decide whether a real kill is worth a test.
- Options: (a) leave it: the recovery is state-independent and the operation-granularity cut covers the engine's side; (b) add a level 2 step that kills the executor container with `docker kill` while a large apply runs (the API applies a revision with many bridges or faults; the kill is timed on a marker such as the first new bridge), restarts the stack and asserts the kernel equals the committed revision; (c) a level 1 variant that runs `chaosgw exec` as a subprocess over its socket and SIGKILLs it.
- Recommendation: (b) when a level 2 run is available: it uses the shipped containers and needs no new harness; (c) needs the binary and a root harness in the testbed for one test.
- Acceptance: the chosen test kills the process while a request is running and finds the committed revision in the kernel afterwards.
- Needs maintainer: no
- Effort: M
