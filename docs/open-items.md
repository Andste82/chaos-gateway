# Phase 1 audit and open items

Date: 2026-10-04. Audited against `main` at `c4d51d3` (M6b merged) and `docs/plan.md` §5 Phase 1 (M1–M6b).

## Result

**Phase 1 is not completely implemented.** Every milestone delivers its main scope, and every test the plan lists exists. CI on `main` is green (run 37147106957: level 0, arm64, e2e, testbed level 1 and 1b), and the level-2 smoke test passed once on a branch. What remains is a mix of:

- a few real functional gaps (Kea/BIRD not pinned, flow counters always 0, no health for the managed services, no persistence of the API generation, retention not wired, the service's own namespace check);
- test gaps where a plan test exists only weakly;
- decisions the plan leaves open or got wrong;
- plan and doc drift.

| Milestone | Verdict | Open items | High | Medium | Decided 2026-10-04 |
|---|---|---|---|---|---|
| M0 (spikes, docs) | – | 3 | 0 | 0 | 1 |
| M1 Repository, CI, testbed | incomplete | 10 | 0 | 2 | 2 |
| M2 Domain model, persistence | incomplete | 9 | 0 | 1 | 2 |
| M3 Executor | incomplete | 7 | 0 | 0 | 2 |
| M4 Compiler, preview, safe apply | incomplete | 9 | 0 | 0 | 2 |
| M4b WireGuard | incomplete | 6 | 0 | 0 | 2 |
| M4c Dynamic routing | incomplete | 7 | 0 | 3 | 5 |
| M5 REST API | incomplete | 5 | 0 | 2 | 4 |
| M5b Appliance harness | incomplete | 2 | 0 | 0 | 0 |
| M6a DHCP, devices | incomplete | 23 | 0 | 9 | 3 |
| M6b DNS, service namespace | incomplete | 11 | 0 | 4 | 2 |
| Cross-cutting | – | 6 | 0 | 0 | 0 |

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
4. **Devices and flows**: M6a-02, M6a-03, M6a-05, M6a-06, M6a-07 (limiter part), M6a-08, M6a-11, M6a-13 to M6a-22.
5. **Service namespace hardening**: M6b-01, M6b-03, M6b-04, M6b-06, M6b-07, M6b-08, M6b-10.
6. **Retention and domain**: M2-01, M2-02, M2-04 to M2-07, M2-09.
7. **Executor and engine robustness**: M3-02, M3-03, M3-05, M4-03 to M4-06, M4-10.
8. **Test infrastructure**: M1-02 (x86 matrix), M1-03 to M1-09, CC-04.
9. **Plan and docs sync**:
   - all `plan-error` items once their decisions are made: M1-01, M1-06, M1-10, M3-06, M4-09, M4b-03, M4b-07, M4b-08, M4c-11, M4c-16, M5-07, M5-23, M6a-04, M6a-07 (doc part), M6a-10, M6a-12, M6a-23, M6a-24, M6b-04 (M7 test), M6b-11;
   - doc items M0-01, M0-02, M3-07, M4-08, M4b-06, M6b-09, CC-01, CC-02.
10. **Decided changes**: M3-01, M4c-02, M4c-04, M4c-05, M5-02, M5-03, M5-10, M6a-09, M6b-02; close M4b-04, M4b-05 and M4c-12 with a doc sentence.
11. **Conntrack events** (effort L): M6a-04, then the rates of M6a-03 on top of it.

## Cross-cutting

### CC-01 Event types out of sync with the spec
- Status: new
- Severity: low
- Reason: forgotten. The engine event `observed_changed` (`internal/engine/events.go:18`, `owner.go:396`) is not in the spec's `EventType` enum (api/openapi.yaml:4661-4701). `service_restarted` is in the enum but is never produced.
- Evidence: as above.
- Task:
  1. Add `observed_changed` to the enum, with a description, and run `make generate`.
  2. Mark `service_restarted` as produced from M28 on in its description.
  3. Add a test in `internal/api` that every type the engine can publish is in the enum (a table of the engine's `Event*` constants).
- Acceptance: local unit test; `make check-generated`.
- Needs maintainer: no
- Effort: S

### CC-02 Plan §3.1: all containers check the executor protocol version
- Status: new
- Severity: low
- Reason: plan-error. `chaosgw dns` and `chaosgw kea-hook` do not talk to the executor; they use the internal API.
- Evidence: plan §3.1; `cmd/chaosgw/dns.go`, `keahook.go`.
- Task: amend §3.1: "containers that use the executor check its protocol version; the service containers use the internal API".
- Acceptance: doc review.
- Needs maintainer: no
- Effort: S

### CC-03 `internal/linkexport` has no unit tests
- Status: new
- Severity: low
- Reason: test-gap. It is covered only through the CLI (`cmd/chaosgw/wg_test.go:204`) and the API 404 path.
- Evidence: `internal/linkexport/`.
- Task: add `linkexport_test.go`. Cover a link with static routes: the `.conf` holds the peer, the endpoint and the AllowedIPs; the BIRD snippet passes `bird -p` (skip when bird is missing).
- Acceptance: local unit test.
- Needs maintainer: no
- Effort: S

### CC-04 The level-1 CI job hides skipped tests
- Status: new
- Severity: low
- Reason: test-gap. `go test` runs without `-v`, and testbed tests skip when a tool is missing (`integration_dhcp_test.go:47-50`), so CI could pass without running them. Together with M1-08 this hides missing tools.
- Evidence: `.github/workflows/ci.yml`, Makefile `test-privileged`.
- Task: fix it as part of M1-08: direct mode fails on skips unless `-allow-skip` is given.
- Acceptance: CI green with 0 skips.
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

### M0-01 README status stale
- Status: open
- Severity: low
- Reason: forgotten — README still says "Implementation starts with milestone M1".
- Evidence: README.md:15.
- Task: replace the sentence with the current state (M1–M6b merged; Phase 1 status as in this file; Q1 per M1-01).
- Acceptance: doc review.
- Needs maintainer: no
- Effort: S

### M0-02 Spike report §5 stale
- Status: open
- Severity: low
- Reason: forgotten — items on Debian (dropped by D1), the Kea check (done with Kea 3.0 in M6a) and DNS over TCP (done in M6b; persistent nft session → M20) are still listed as open.
- Evidence: docs/spikes/REPORT.md:468-476.
- Task: mark each item with its outcome: "dropped (D1)", "done in M6a/M6b", "→ M20", "→ H1", "→ M32", "→ M28".
- Acceptance: doc review.
- Needs maintainer: no
- Effort: S

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

Verdict: incomplete. The scope is delivered and the M1 tests pass in CI; open are the Q1 decision (an M1 scope item), the runner's exit code (plan wording) and hardening/drift.

| Plan item | Status | Evidence |
|---|---|---|
| Go module, Vue skeleton, oapi-codegen, Orval, openapi-python-client | done | `go.mod`, `web/`, `api/oapi-codegen-*.yaml`, `web/orval.config.ts`, Makefile `generate-python` |
| CI check: spec validates, examples validate, generated code compiles | done (validation, no style linter) | `make check-spec`, `check-generated`, `check-clients`; ci.yml level0 |
| Makefile, golangci-lint, go test -race, Vitest, Playwright skeleton | done | Makefile, `.golangci.yml`, `web/src/App.test.ts`, `web/e2e/smoke.spec.ts` |
| CI levels 0 and 1b "on the development VPS" | partial | level 1b runs on hosted runners with KVM, on the VPS only by hand (M1-03) |
| Level 1 job in hosted CI | done | ci.yml `testbed-privileged` |
| `internal/testbed` (two networks, bridges, management default route) | done | `internal/testbed/topology.go`; `TestDefaultTopology` |
| Level 1b runner (one VM, rw share, no terminal) | done | `internal/testbed/vmrun` |
| Devcontainer with tools and stock kernels | done (GA kernels only) | `.devcontainer/Dockerfile` |
| Injectable clock | done | `internal/clock` |
| arm64 image build | done | ci.yml `arm64` |
| Decision where KVM tests run (Q1) | missing | plan §7.2 Q1 still open (M1-01) |
| Shared module preflight | done | `internal/preflight`; `TestHostSetupLoadsExactlyTheModulesOfThePreflight` |
| T: ping through a forwarding namespace in level 1b | done (CI) | `TestClientPingsServerThroughPlainForwardingGateway` |
| T: netem delay visible | done (CI), fragile | `TestNetemDelayIsVisible` (M1-05) |
| T: runner returns the tests' exit code | partial | returns 0/1/2 (M1-06) |
| T: same test in a privileged container | done (CI) | ci.yml `testbed-privileged` |

### M1-01 Q1 not decided; the plan's evidence is stale
- Status: open
- Severity: medium
- Reason: needs-decision — the M1 scope includes "the decision where KVM-dependent tests run (Q1)"; the evidence exists (level 1b with `kvm=true` in run 37147106957, level 2 green on 24.04 and 26.04 in nightly run 37121687715 on hosted runners), but §7.2 still lists Q1 as open and says level 2 was "not yet tried".
- Evidence: plan.md:1279 (M1 scope), :1586 (Q1), :1131, :1134, :1165, :1216, :1316, D9 :1557; README.md:15; `.github/workflows/nightly.yml` job `appliance`.
- Task: 1. Move Q1 from §7.2 to §7.1 as `D33`: "KVM-dependent tests (level 1b with KVM, level 2 appliance, later measurements) run on hosted GitHub runners (`ubuntu-24.04` with `/dev/kvm`); no self-hosted runner is planned; measurement accuracy depends on the hosted hardware". Cite both run ids. 2. Update §4.4 (remove "no KVM-capable machine yet"; nightly level 2 and measurements on hosted runners), the §4.5 level-2 "When" column, "Limits of this setup" (:1216), the M5b text (:1316), the "After Phase 2" line (:1369) and D9. 3. Update docs/development.md ("Level 1b", "CI", "Appliance VMs"). 4. README.md:15: remove "except the open question Q1".
- Acceptance: doc review; `grep -n "Q1" docs/plan.md` shows no "open" wording.
- Needs maintainer: decided 2026-10-04: (b) hosted GitHub runners with `/dev/kvm` only; no self-hosted runner is planned. Record this as D33, and note that measurement accuracy depends on the hosted hardware.
- Effort: S

### M1-02 Nightly level 1b kernel matrix and arm64 level 1b missing
- Status: open
- Severity: medium
- Reason: forgotten — §4.4 promises nightly level 1b on 24.04 and 26.04 with GA and HWE kernels plus an arm64 level 1b run; no milestone owns it, CI only boots the default kernel.
- Evidence: plan.md:1134-1135; `nightly.yml` (only `fuzz` and `appliance`); `.devcontainer/Dockerfile` `ARG KERNELS` (two GA kernels); `vmrun.DefaultKernel` (run.go:21); `vmrun` has no arch option (an arm64 guest needs its own arm64 root file system).
- Task: 1. Add a `testbed-matrix` job to `nightly.yml` that builds `.devcontainer` as ci.yml does and runs `make test-vm ARGS="-kernel ${{ matrix.kernel }}"` for each kernel in `ARG KERNELS` (`6.8.0-142-generic`, `7.0.0-38-generic`). 2. Find the current 24.04 HWE kernel (`apt-cache policy linux-image-generic-hwe-24.04`), add it to `ARG KERNELS` and the matrix; 26.04 has no HWE kernel yet, so change §4.4 to "HWE kernels where they exist". 3. Arm64 per the decision: if deferred, change §4.4 to say arm64 level 1b comes with M28 (its test list already has "the arm64 image … in emulated level 1b").
- Acceptance: a manually started nightly run (`gh workflow run nightly.yml --ref <branch>`) shows one green `testbed-matrix` job per kernel.
- Needs maintainer: decided 2026-10-04: (a) arm64 level 1b comes with M28; amend §4.4. Do the x86 kernel matrix now.
- Effort: S (x86) / L (arm64)

### M1-03 The plan says level 1b runs "on the development VPS" on every commit
- Status: new
- Severity: low
- Reason: plan-error — CI runs level 1b on hosted runners with KVM; on the VPS level 1b runs only via `make test-vm`, so the emulated branches (`!testbed.Accurate()`) never run automatically.
- Evidence: plan.md:1133, :1279; ci.yml `testbed-vm`; `vmrun.HasKVM()` decides, no flag forces emulation.
- Task: 1. Change §4.4 "Every commit" and the M1 scope wording: levels 0, 1b (hosted, KVM) and 1 in hosted CI; `make test-vm` on the VPS before a merge (emulated). 2. Optional: add `-no-kvm` to `tools/testvm run` (passes `kvm=false` into `VNGArgs` and `GuestOptions.Emulated`), a unit test that it yields `--disable-kvm` and `CHAOSGW_TESTBED_EMULATED=1`, and a weekly job `make test-vm ARGS=-no-kvm`.
- Acceptance: doc review; with step 2 a local unit test and a green scheduled job.
- Needs maintainer: no
- Effort: S

### M1-04 KVM mode of CI not asserted
- Status: open
- Severity: low
- Reason: forgotten — the udev step ends in `|| true`; a runner without KVM silently runs emulated.
- Evidence: `.github/workflows/ci.yml:76`.
- Task: after "Enable KVM" add a step: `if [ -w /dev/kvm ]; then echo kvm=yes; else echo "::warning title=No KVM::level 1b runs emulated"; fi`. In `nightly.yml` job `appliance` fail instead (level 2 skips without KVM).
- Acceptance: CI shows the annotation only when KVM is missing.
- Needs maintainer: no
- Effort: S

### M1-05 Netem test tolerances can flake under emulation
- Status: open
- Severity: low
- Reason: test-gap — under TCG the baseline is noisy; the isolation and "after" checks use `ref+25ms`/`ref+20ms`.
- Evidence: `internal/testbed/topology_integration_test.go:184,191`.
- Task: when `!testbed.Accurate()`, assert `iso.Median() < got-25*time.Millisecond` and `after.Median() < got-25*time.Millisecond`; keep the `ref+…` bounds only when `testbed.Accurate()`.
- Acceptance: CI testbed green; `make test-vm ARGS="-run TestNetemDelayIsVisible ./internal/testbed"` green on the VPS (emulated).
- Needs maintainer: no
- Effort: S

### M1-06 Runner does not return the tests' exit code
- Status: open
- Severity: low
- Reason: plan-error — `testvm` returns 0 (pass), 1 (a test or the VM failed), 2 (run not carried out); documented and arguably better than a raw code, but the M1 text says "returns the tests' exit code".
- Evidence: `tools/testvm/main.go` (end of `runTests`), docs/development.md:78, plan.md:1280.
- Task: 1. Change the M1 test text to "exits 0 only when every package ran and passed, 1 when a test or the VM failed, 2 when the run could not be carried out, and prints per-package results". 2. Add `TestRunVMExitCodes` in `tools/testvm/main_test.go` with the fake `vng` of `vmrun/run_test.go` (pass → 0, failing package → 1, `-vm-timeout 1s` → 2).
- Acceptance: doc review plus a local unit test.
- Needs maintainer: no
- Effort: S

### M1-07 `Process.Wait` can hang; namespaces of a crashed level-1 run are not swept
- Status: open
- Severity: low
- Reason: forgotten — `Namespace.Start` sets no `WaitDelay`; nothing removes `tb…` namespaces left by a crashed direct run (level 1b gets a fresh VM and is not affected).
- Evidence: `internal/testbed/testbed.go:277-292` (Start), :319 (Stop).
- Task: 1. In `Start` set `p.cmd.WaitDelay = 5 * time.Second`. 2. Add `testvm sweep` in `tools/testvm/main.go`: for `ip netns list` entries matching `^tb[0-9a-f]{8}-`, kill `ip netns pids`, then `ip netns del`; print what was removed; require `-force` while another `testvm`/`.test` process runs. 3. Document in development.md. 4. Unit-test the name filter.
- Acceptance: local unit test; manual `testvm sweep` in a privileged container.
- Needs maintainer: no
- Effort: S

### M1-08 Skipped testbed tests count as passes
- Status: open
- Severity: low
- Reason: forgotten — `Summary.OK()` ignores skips, while development.md says testbed tests are "never skipped silently"; real skips exist (`internal/engine/integration_dhcp_test.go:49`, `internal/apply/integration_test.go:312`).
- Evidence: `internal/testbed/vmrun/results.go:58`.
- Task: 1. Add `AllowSkip bool` to `vmrun.Config` and `-allow-skip` to `testvm run`. 2. In `Summary.Problems()`/`OK()` report "N tests skipped" and fail unless allowed. 3. In direct mode parse the `-v` output the same way (reuse `ParseCounts`). 4. Test `TestCollectSkipsFailUnlessAllowed` with `testdata/fixture` `TestSkips`.
- Acceptance: local unit tests; CI level 1/1b stays green (0 skips today).
- Needs maintainer: no (recommendation: fail by default)
- Effort: S

### M1-09 Runner flag plumbing untested
- Status: open
- Severity: low
- Reason: test-gap — `vmrun` covers keep, timeout, cancel and stale results; `tools/testvm` flag plumbing (`-kernel`, `-keep`, `-vm-timeout`, `-v`) is untested.
- Evidence: `tools/testvm/main_test.go`.
- Task: extract a pure `parseRunFlags(args) (vmrun.Config, mode string, err)` from `runTests` and test it in `TestParseRunFlags` (each flag lands in the config; `TESTVM_KERNEL`/`TESTVM_MEM` defaults). Combine with M1-06.
- Acceptance: local unit test.
- Needs maintainer: no
- Effort: S

### M1-10 Plan §3.7 repository layout outdated
- Status: new
- Severity: low
- Reason: plan-error — the tree names `internal/apiserver/` (handlers), `scheduler/`, `routing/`, `dhcp/`; the code has `internal/api`, `engine`, `bird`, `kea`, `apply`, `model`, `schema`, `dnsproxy`, `tools/testvm`.
- Evidence: plan.md:948-970 vs `ls internal`; the docs/development.md "Layout" table is accurate.
- Task: replace the §3.7 tree with the current top-level packages (from the development.md Layout table), marking not-yet-existing dirs (`sidecars/`, `profiles/`, `scenarios/`, `capture/`, `tlsresponder/`) as "later".
- Acceptance: doc review.
- Needs maintainer: no
- Effort: S

## M2 (domain model, persistence)

Verdict: incomplete. Every scope and test item is implemented and tested; open is revision retention (never wired) plus documentation and small gaps.

| Plan item | Status | Evidence |
|---|---|---|
| Domain model from generated types | done | `internal/model/model.gen.go`, `domain.OverlayKey`, `NewOverlay` |
| Strict decoding | done | `domain/decode.go`; `TestUnknownFieldsInAConfigurationAreRejected`, `TestDuplicateKeysInAJSONDocumentAreRejected` |
| Rules the schema cannot express | done | `validate*.go` |
| Observed-state model | done | `domain/observed.go` |
| Precedence per family, overlays first | done | `domain/resolve.go`; `TestOverlaysWinWhateverTheLevel` |
| Atomic persistence, revisions with diff, schema versions | done | `store/files.go`, `store.Diff`, `store/migrate.go` |
| T: validation, precedence rows, E1–E8/E12, round-trip, corrupted files, examples, pointers and codes | done | `TestEveryPrecedenceLevel…`, `TestE1…`–`TestE12…`, corrupt_test.go, crash_test.go, `TestEveryExampleFileIsCheckedByTheDomain` |
| Retention 200 revisions (§3.6) | partial | `Store.Prune` never called (M2-01) |

### M2-01 Revision retention: `Store.Prune` is never called
- Status: open
- Severity: medium
- Reason: forgotten — §3.6 sets a default of 200 revisions (`settings.retention.revisions`, schema `minimum: 10, default: 200`); `Prune` exists and is tested but never called; revisions and stale candidates grow without bound. The M2 PR said "wire up in M5"; it was not done.
- Evidence: `internal/store/store.go:616`; only `wireguard.Prune` is called (`internal/engine/owner.go:287`); commit path `owner.go:547-558`; `api/openapi.yaml:2815`.
- Task: 1. In `internal/engine/owner.go` `prune(cfg)`, before the WireGuard prune, call `o.e.cfg.Store.Prune(keep)` with `keep = *cfg.Settings.Retention.Revisions` when set, else a new constant `store.DefaultRetainedRevisions = 200`; log removed ids at info, warn on error. 2. Call it once at engine start after the committed revision is loaded. 3. Test `TestCommittedRevisionsArePrunedToTheRetention` in `internal/engine` (kernelsim, `retention.revisions: 10`, commit 12; revisions 1–2 gone, active/LKG kept, a stale candidate removed). 4. Document in development.md.
- Acceptance: `go test ./internal/engine -run Prune` (local).
- Needs maintainer: no
- Effort: S

### M2-02 Validation rules beyond the spec are undocumented
- Status: open
- Severity: low
- Reason: forgotten — rules and codes (hold ≥ 3× keepalive, OSPF dead > hello, `matrix_self_entry`, `duplicate_matrix_entry`, BGP neighbor = link peer, lease/TTL ≥ 1 s, `duplicate_protocol`, `invalid_timers`, `duplicate_step_id`, `host_bits_set`, `mutually_exclusive`) exist only in code.
- Evidence: `internal/domain/validate_rules.go`, `validate_network.go:17-77`; `api/openapi.yaml:2181`.
- Task: 1. Add each rule as a sentence to the spec field descriptions concerned. 2. Add a "Validation codes" section to docs/development.md listing every `Code*` constant. 3. `TestEveryValidationCodeIsDocumented` in `internal/domain` (table of codes, each must appear in development.md).
- Acceptance: doc review; local unit test; `make check-generated` passes.
- Needs maintainer: no
- Effort: S

### M2-03 "Configured address beats a lease" not confirmed
- Status: open
- Severity: low
- Reason: needs-decision — identity resolution ranks a configured address above a DHCP lease, stronger than §2.3 says.
- Evidence: `internal/domain/observed.go:106-113`; `TestTwoDevicesClaimingOneAddressTheStrongerClaimWins`.
- Task: keep: add a sentence to plan §2.3 and development.md "DHCP and devices"; change: swap `claimLease`/`claimExplicit` and update the test.
- Acceptance: doc review (plus a unit test if changed).
- Needs maintainer: decided 2026-10-04: (a) configuration wins (current behaviour); document it.
- Effort: S

### M2-04 Reserved routing tables hard-coded in the domain
- Status: open
- Severity: low
- Reason: forgotten — `reservedTableFirst/Last = 100/110` duplicates `executor.OwnTableFirst/Last`, `compiler.PolicyTable`, `compiler.ServiceTable`; the comment says "fixed in M4".
- Evidence: `internal/domain/validate_rules.go:93-102`; `internal/executor/validate.go:27-28`; `internal/compiler/compile.go:19`; `internal/compiler/service.go`.
- Task: 1. Export `domain.OwnTableFirst = 100`, `domain.OwnTableLast = 110`. 2. Make `executor.OwnTableFirst/Last` refer to them; assert in a compiler test that `PolicyTable` and `ServiceTable` lie in the range. 3. Remove the stale comment.
- Acceptance: local unit tests.
- Needs maintainer: no
- Effort: S

### M2-05 `NewWorld` does not refuse an un-normalized configuration
- Status: open
- Severity: low
- Reason: forgotten — only a comment; there is no production caller yet (M7/M8a will use it).
- Evidence: `internal/domain/resolve.go:67-68`.
- Task: add `IsNormalized(cfg) bool` in `refs.go`; change the signature to `NewWorld(cfg, overlays) (*World, error)` (no callers yet) and return an error for names; test `TestNewWorldRejectsNames`.
- Acceptance: local unit test.
- Needs maintainer: no
- Effort: S

### M2-06 `Store.get` decodes loosely
- Status: open
- Severity: low
- Reason: forgotten — unknown fields in a stored revision are accepted silently; `Configuration.schema_version` is not re-checked.
- Evidence: `internal/store/store.go:287`.
- Task: decode with `DisallowUnknownFields`, return `&ErrCorrupt{…}` on error, check `cfg.SchemaVersion`; test `TestAStoredConfigurationWithAnUnknownFieldIsCorrupt` in corrupt_test.go (valid checksum, extra field).
- Acceptance: local unit test.
- Needs maintainer: no
- Effort: S

### M2-07 Missing edge tests
- Status: open (partial: nested clients in the diff are covered by `TestDiffShowsClientsAndLinkPeersAsObjectsOfTheirOwn`; `<`, `>`, `&` round-trip correctly today, verified)
- Severity: low
- Reason: test-gap.
- Evidence: `internal/store/store_test.go`, `internal/domain/diff_test.go`, `validate_network_test.go`.
- Task: `TestHTMLCharactersSurviveTheChecksum` (store), `TestDiffShowsARemovedClient` (domain), `TestAHubWithASlash30AndSeveralClients` (pin what the code returns for a second client in a /30 hub).
- Acceptance: local unit tests.
- Needs maintainer: no
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

### M2-09 Overlay and run-request examples bypass the strict Go decoder
- Status: new
- Severity: low
- Reason: test-gap — `overlays.yaml` and `run-request.yaml` are decoded with the non-validating `decodeInto`; strictness is checked only by the Python script (they pass today, verified).
- Evidence: `internal/domain/overlay_test.go:27-42`, `review_fixes_test.go:356-397`.
- Task: in `TestEveryExampleFileIsCheckedByTheDomain`, marshal each overlay item and run `DecodeOverlayRequest(raw, FormatJSON)` before `ValidateOverlay`; validate run-request with `Schemas().Validate("RunRequest", doc)` before decoding the inline scenario.
- Acceptance: local unit test.
- Needs maintainer: no
- Effort: S

## M3 (executor and state reader)

Verdict: incomplete. All scope and test items exist; open are one literal scope rule (the uplink may only get a qdisc), small robustness and test gaps, and outdated docs.

| Plan item | Status | Evidence |
|---|---|---|
| Typed closed operations, argument arrays, fixed paths, namespace targeting | done | `internal/executor/op.go`, `runner.go`; `TestRunnerUsesOnlyFixedAbsolutePaths`, `TestNamespaceIsPassedToEveryCommand` |
| Parsers `ip -j`, `nft -j`, `tc -j` | done | `internal/linux`; `parse_test.go` (testdata partly hand-trimmed, M3-05) |
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

### M3-02 `Server.Serve` goroutine leak and accept race
- Status: open
- Severity: low
- Reason: forgotten — the ctx goroutine never ends on a non-close `Accept` error while ctx is live; a connection accepted during the close loop is never closed, so `wg.Wait` can hang.
- Evidence: `internal/executor/server.go:81-119`.
- Task: 1. Replace the goroutine with `stop := context.AfterFunc(ctx, closeAll)`; `defer stop()`. 2. After `Accept`, under `s.mu`, check `ctx.Err()`; when cancelled close `c` and return, else register it. 3. `TestServeReturnsOnAcceptErrorWithoutLeaking` in server_test.go with a fake listener (goleak in TestMain catches the leak).
- Acceptance: `go test -race ./internal/executor`.
- Needs maintainer: no
- Effort: S

### M3-03 `FuzzFrame` covers only `Server.serve`
- Status: open
- Severity: low
- Reason: test-gap — the codec, the hello handshake and the `maxLine` limit are never fuzzed.
- Evidence: `internal/executor/fuzz_test.go:141-170`; `proto.go:77-95`.
- Task: add `FuzzConn` driving the connection handler over a `unix.Socketpair` (PeerCred works there) with an allow-all `Auth`: hello plus arbitrary bytes; assert no panic, the connection ends, `fakeRunner` ran only inert commands. Add it to `FUZZ_TARGETS` in the Makefile and lower `FUZZTIME` to 100s so CI stays at 5 min.
- Acceptance: CI `make fuzz`; nightly 60 min.
- Needs maintainer: no
- Effort: S

### M3-04 Executor generation restarts at 0
- Status: open
- Severity: low
- Reason: needs-decision — `Outcome.Generation` is per process; nothing in the plan requires persistence (the API generation lives in the engine, see M5-01).
- Evidence: `internal/executor/exec.go:57-60,251-262`.
- Task: doc comment on `Outcome.Generation`: "counts mutating requests since this executor started; not persistent".
- Acceptance: doc review.
- Needs maintainer: decided 2026-10-04: (B) document the counter as per-process.
- Effort: S

### M3-05 Parser test data is hand-trimmed
- Status: open
- Severity: low
- Reason: test-gap — e.g. `tc_class_htb.json` has `dev` only on the first entry; the parsers are not checked against real tool output.
- Evidence: `internal/linux/testdata/`.
- Task: in a CI testbed run (or a privileged container) record full outputs of `ip -j -d link show`, `ip -j addr`, `ip -j route show table all`, `ip -j rule`, `nft -j list table inet chaosgw`, `tc -j qdisc/class/filter show` after a first apply; add a small testbed helper test that writes them to `$CHAOSGW_RECORD_DIR` when set; replace the files and adjust `parse_test.go`.
- Acceptance: `go test ./internal/linux`.
- Needs maintainer: no
- Effort: S

### M3-06 Plan §2.2 still names a `uidrange` rule for the service user
- Status: open
- Severity: low
- Reason: plan-error — D29/S16 moved the services into the service namespace; their traffic enters through `iif svc0`, which the compiler emits; no `uidrange` exists or is needed.
- Evidence: docs/plan.md:173; `internal/compiler/service.go`; docs/development.md:138,208.
- Task: 1. plan.md:173: replace "and on the service user (`uidrange`)" with "and on `svc0`, the gateway side of the service namespace (§3.3, D29)"; "sockets of the service user" → "traffic of the service namespace". 2. Remove the `uidrange` mentions in development.md.
- Acceptance: `grep -rn uidrange docs/plan.md docs/development.md` finds only historical spike text.
- Needs maintainer: no
- Effort: S

### M3-07 docs/development.md executor section outdated
- Status: new
- Severity: low
- Reason: forgotten — the operation list (8) and the "not in M3" paragraph predate M4–M6b.
- Evidence: docs/development.md:119-121, :137-140; `op.go:12-27` (14 operations incl. `nft_del_elements`, `links`, `sysctl`, `wireguard`, `bird`, `service_ns`).
- Task: rewrite the list with all operations; replace the "not in M3" paragraph with the current state (persistent netlink path → M20, reader pool and time stamps → M8a).
- Acceptance: doc review.
- Needs maintainer: no
- Effort: S

## M4 (compiler, preview, safe apply)

Verdict: incomplete. All scope and test items exist and pass; open are a spec/code mismatch, loose or indirect tests, and docs/plan drift.

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
| T: preview matches applied state | partial | compares only hash and an empty diff (M4-04) |

### M4-02 The management matrix endpoint differs from the spec
- Status: open
- Severity: low
- Reason: needs-decision — the spec says endpoint `management` is "`Management.allowed_sources` and the management interface subnet"; the code uses only `allowed_sources` when they are set.
- Evidence: api/openapi.yaml:2560 vs :2294; `internal/compiler/compile.go:315-334`; `rules.go:277-281`.
- Task: (A) change the description at openapi.yaml:2560 to "`Management.allowed_sources`, or the management interface subnet when none are given"; run `make check-spec check-generated check-clients`. (B) add a set `mgmt_net` (allowed_sources ∪ interface subnet) used only in `matrixRules`, golden update, `TestTheManagementEndpointIncludesTheInterfaceSubnet`.
- Acceptance: doc review + `make check-spec` (A) or local unit test (B).
- Needs maintainer: decided 2026-10-04: (A) the spec follows the code.
- Effort: S

### M4-03 The lockout rollback test accepts too much
- Status: open
- Severity: low
- Reason: test-gap — `!contains("22,8443") && !contains("8443")` equals `!contains("8443")`.
- Evidence: `internal/engine/engine_test.go:318`.
- Task: read the ruleset (`apply.ReadState` or parse `h.nftText()`), assert the anti-lockout rule has the port set `{22, 8443}` and the drop rule `tcp dport 8443`; keep the post-rollback check.
- Acceptance: `go test ./internal/engine -run Lockout`.
- Needs maintainer: no
- Effort: S

### M4-04 The preview-equals-apply test is indirect
- Status: open
- Severity: low
- Reason: test-gap — compares only `Target.Hash` and an empty diff afterwards; the previewed plan is never compared with what the apply runs.
- Evidence: `internal/engine/engine_test.go:712-767`; `engine/api.go:151`.
- Task: in `TestPreviewShowsTheChangeAndChangesNothing`, record the summary of the plan the apply runs (expose `Applied.Plan []string` from the apply loop, or rebuild with `apply.BuildPlan` against the state before the apply) and assert it equals `p.Plan`; also assert `p.Linux` equals `apply.Diff(target, stateBeforeApply)`.
- Acceptance: `go test ./internal/engine -run Preview`.
- Needs maintainer: no
- Effort: S

### M4-05 No test for a network rename or a failed restore
- Status: open
- Severity: low
- Reason: test-gap — a rename only happens inside the injected-failure testbed test; no test covers a restore that fails too (`Restored:false`, retry).
- Evidence: `internal/engine/integration_test.go:272-320`; `owner.go:465-494`.
- Task: 1. `TestRenamingANetworkMovesItsPortsToTheNewBridge` in `internal/apply/apply_test.go` (kernelsim): rename "Lab" → "Extra", assert `br-extra` with port `lan1`, `br-lab` gone, `Verify` empty. 2. `TestARestoreThatFailsTooIsReportedAndRetried` in `internal/engine/engine_test.go`: `h.k.Fail` fails every `nft -f` until switched off; assert `ErrApplyFailed{Restored:false}`, `Snapshot().LastError != ""`, and after `clk.Advance(11s)` with failures off the committed revision is applied and verified.
- Acceptance: `go test ./internal/apply ./internal/engine`.
- Needs maintainer: no
- Effort: S

### M4-06 `FirstV4()` depends on the kernel's address order
- Status: open
- Severity: low
- Reason: forgotten — with several IPv4 addresses on the uplink or management interface a reordering changes `Uplink.Addr`, the table-100 route and the default management sources.
- Evidence: `internal/compiler/host.go:87-95`; compile.go:284,330.
- Task: carry the `secondary` flag from `ip -j addr` into `HostLink`; prefer non-secondary addresses, then the lowest; `TestFirstV4PrefersThePrimaryAddress`.
- Acceptance: `go test ./internal/compiler`.
- Needs maintainer: no
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

### M4-08 docs/development.md M4 notes outdated
- Status: new
- Severity: low
- Reason: forgotten — :208-209 says WireGuard and device identity sets are "not yet compiled" (both done); :247-249 calls the `wg` tool a deviation from plan §3.4, which now names it.
- Evidence: docs/development.md:208-209, :247-249.
- Task: rewrite :208-209 to list only what is still not compiled (faults and classification maps M7/M8b, PMTU tables M10); delete the deviation sentence.
- Acceptance: doc review.
- Needs maintainer: no
- Effort: S

### M4-09 Plan §2.14 asks for golden tests of the `nft -j`/`tc -j` normalizers
- Status: new
- Severity: low
- Reason: plan-error — there is no tc normalizer before faults (tc verify comes with M8b); the nft side uses recorded-output parser tests and comment hashes.
- Evidence: docs/plan.md:548; `internal/linux/parse_test.go`; `internal/apply/verify.go`.
- Task: amend plan.md:548: parsers have recorded-output tests; the tc normalizer and its golden tests come with fault verify in M8b; add "golden tests for the tc normalizer" to the M8b test list.
- Acceptance: doc review.
- Needs maintainer: no
- Effort: S

### M4-10 Observer breaks the §3.11 code rules
- Status: new
- Severity: low
- Reason: forgotten — `time.Sleep` outside `internal/clock`; the observer's goroutines are not supervised, so a panic there kills the process.
- Evidence: `internal/observer/netlink.go:74` (Sleep), :51,81,87,104 (raw `go`).
- Task: replace the sleep with a select on a timer from the injected clock and ctx; add a `recover` in the reader goroutine that closes the channels (or accept a `*supervisor.Supervisor`); keep `TestEventsAreDebouncedIntoOneTrigger` green.
- Acceptance: `go test -race ./internal/observer`.
- Needs maintainer: no
- Effort: S

Removed from the old list: "tc tokens allow `/` and `..`" — wrong: `..` is rejected (`internal/executor/validate.go:423`, test "tc path traversal token"). "Supervisor helper deferred to M8a" (Covered later) — wrong: the supervisor exists and is used; only the reader pool and time stamps go to M8a, overlay removal on stop to M27.

## M4b (WireGuard networks and clients)

Verdict: incomplete. All plan tests exist and passed in CI (run 37147106957); open are plan/doc fixes.

| Plan item | Status | Evidence |
|---|---|---|
| Hub and link as network type, `wg` tool, clients with client networks, reachable lists, static routes, policy rules | done | `internal/compiler/wireguard.go`; `TestWireGuardRoutesAndRules`; testbed `TestLocalDeviceReachesAClientNetworkWithoutNATAndTheReverseNeedsTheMatrix`, `TestALinkWithStaticRoutesCarriesTrafficToTheRemoteSite` |
| Routed without NAT to test networks | done | testbed `integration_wg_test.go:251,275,304` |
| Masqueraded towards the uplink | done | `TestMasqueradeTowardsTheUplinkOnlyAndForTheNetworksBehindClients` (a link matches by interface, covering learned routes too); testbed `TestAClientNetworkIsMasqueradedTowardsTheUplink` |
| Keys, preshared keys, export once, `.conf`/QR/zip | done | `internal/wireguard/*_test.go`; `TestQRCodesAndNetworkExports` |
| Client status and events, clients as devices, role management, MSS clamp, MTU | done | `engine/wireguard.go`; testbed `TestDisablingAClientStopsItsHandshakeAndEmitsTheEvent`, `TestRolesDecideWhoReachesTheControlPlane`, `TestTheMSSIsClampedOnTheTunnel` |
| T: export works in a fresh namespace, QR decodes, keys never in logs, re-apply keeps the tunnel | done | `TestPrivateKeysStayOutOfStoreSnapshotAndLogsAndAReapplyKeepsTheTunnel` |

### M4b-03 Plan §4.2 describes the old WireGuard testbed topology
- Status: open
- Severity: low
- Reason: plan-error — §4.2 says WireGuard is part of the default topology with an "internet" router; M4b's scope and the code use the opt-in `WithRemotes`.
- Evidence: docs/plan.md:1111 vs :1300; `internal/testbed/testbed.go:71`, `topology.go:111-125`.
- Task: rewrite plan.md:1111: "From M4b on the testbed option `WithRemotes` adds a remote client (hub client with a client network), two link sites and a switch on the uplink; tests that need WireGuard enable it."
- Acceptance: doc review.
- Needs maintainer: no
- Effort: S

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

### M4b-06 Development doc claims a deviation that is none
- Status: new
- Severity: low
- Reason: forgotten — development.md says §3.4 names wgctrl; §3.4 now names the `wg` tool (same as M4-08).
- Evidence: docs/development.md:247-249 vs docs/plan.md:852,861.
- Task: delete the "(Deviation from plan §3.4 …)" sentence; keep the reason as a plain statement.
- Acceptance: doc review.
- Needs maintainer: no
- Effort: S

### M4b-07 Plan §2.2.1 says private keys are "never" in exports; the API has `include_secrets`
- Status: new
- Severity: low
- Reason: plan-error — §2.16 says "excluded by default"; spec and code offer `include_secrets=true` (scope full, audited).
- Evidence: docs/plan.md:211 vs :625; api/openapi.yaml:609-631; `TestExportWithSecretsNeedsFullAndIsAudited`.
- Task: plan.md:211 → "…never included in configuration exports unless explicitly requested (`include_secrets`, scope full, audited), and never in logs."
- Acceptance: doc review.
- Needs maintainer: no
- Effort: S

### M4b-08 No "next free tunnel address" proposal
- Status: new
- Severity: low
- Reason: plan-error (no owner) — §2.2.1 "tunnel address (next free address proposed)"; `address` is required and nothing proposes one.
- Evidence: docs/plan.md:198; api/openapi.yaml:2490-2499.
- Task: add "the dialog proposes the next free tunnel address of the hub" to the M14 scope (plan.md:1384); computed client-side.
- Acceptance: doc review now; Playwright in M14.
- Needs maintainer: no
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
| Export only into own tables | done for table 100 | PMTU tables come with M10 (M4c-11) |
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

### M4c-11 Learned routes are not exported into the PMTU mirror tables
- Status: open
- Severity: low
- Reason: plan-error — §2.2.2 requires it; the mirror tables come with M10, whose scope does not mention BIRD.
- Evidence: docs/plan.md:226,1355; `render.go:185-187`.
- Task: add to the M10 scope: "BIRD exports learned routes into the PMTU mirror tables too (one kernel protocol per table)".
- Acceptance: doc review.
- Needs maintainer: no
- Effort: S

### M4c-12 Routing input rules have no source address match
- Status: open
- Severity: low
- Reason: deferred — accepts match only `iifname <link>`; on a point-to-point WireGuard link only the peer can send.
- Evidence: `internal/compiler/rules.go:101-113`.
- Task: optional `ip saddr <link peer>` for BGP.
- Acceptance: local compiler test.
- Needs maintainer: decided 2026-10-04: accepted as is; close the item with a sentence in docs/development.md.
- Effort: S

### M4c-16 Plan overstates "sessions stay up" on reconfigure
- Status: new
- Severity: low
- Reason: plan-error — BIRD restarts a protocol when its own parameters change (neighbor, AS, timers, OSPF area or interface); only filter and announcement changes reload without a reset.
- Evidence: docs/plan.md:225,1307.
- Task: amend §2.2.2 and the M4c test: "filter and announcement changes are applied without resetting sessions; changes of a protocol's neighbor, AS or timers restart that protocol only".
- Acceptance: doc review.
- Needs maintainer: no
- Effort: S

## M5 (REST API v1)

Verdict: incomplete. Every scope and test item exists and all 41 `x-milestone: M5` operations have handlers; open are an SSE resync signal, the setup without commit-confirm, documenting the loopback binding, the 413 oversized-body code, and the M8a scope note for `?force`/`references[]`.

| Plan item | Status | Evidence |
|---|---|---|
| problem+json, UUID or name in paths, cursor pagination, ETag/If-Match/428, merge-patch candidates, idempotency keys | done | `internal/api/{problem,helpers,middleware,revisions,idempotency}.go`; `TestProblemsAreProblemJSON`, `TestIdempotencyKeys` |
| SSE with ids, replay, keepalive | done, gap | `events.go`, `engine/events.go`; no signal for lost events (M5-02) |
| Generation (state, header, SSE `applied`) | done | persisted across restarts (`engine.Config.GenerationFile`); `TestGenerationContinuesAfterRestart`, `TestGenerationSurvivesAnAPIRestart` |
| Capabilities, candidate model, sessions + CSRF, hashed tokens with scopes, setup token | done | `system.go`, `revisions.go`, `auth/auth.go`; `TestLoginSessionCSRFAndLogout`, `TestTokenScopesAndTheirLifecycle` |
| Setup "applies with commit-confirm" (spec `POST /setup`) | missing | M5-03 |
| Admin password reset | done | `cmd/chaosgw/admin.go`: audited, interactive prompt |
| UI/API bound to the management network after setup | done, gap | `cmd/chaosgw/api.go`: also excludes test networks before setup; loopback binding still needs a doc note (M5-07) |
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

### M5-07 Loopback is always bound
- Status: open
- Severity: low
- Reason: plan-error — §2.16 says management network only; the container health check needs 127.0.0.1.
- Evidence: `cmd/chaosgw/api.go:240`.
- Task: plan §2.16: "plus the loopback address for the container health check; local processes on the host are trusted".
- Acceptance: doc review.
- Needs maintainer: decided 2026-10-04: (a) document the loopback binding.
- Effort: S

### M5-10 Oversized body gives 400 instead of 413
- Status: open
- Severity: low
- Reason: needs-decision — the spec's error table has no 413 code.
- Evidence: `internal/api/helpers.go:106-110`, `revisions.go:112-115`, `middleware.go:64`.
- Task: add `payload_too_large` (413) to `ErrorCode` and the spec table, regenerate; detect `*http.MaxBytesError` in `decodeJSON` and `CreateRevision`; `statusOf`/`titleOf`; `TestAnOversizedBodyIs413`.
- Acceptance: local unit test.
- Needs maintainer: decided 2026-10-04: add `payload_too_large` (413).
- Effort: S

### M5-23 `?force=` and `references[]` deferred without a home in the plan
- Status: open
- Severity: low
- Reason: deferred — M8a's scope text does not name `?force`, `references[]` or `overlay_orphaned` (§2.1.1).
- Evidence: `internal/api/revisions.go:294`; plan §2.1.1.
- Task: add "apply `?force=true`, `references[]` in `validation_failed`, event `overlay_orphaned` (§2.1.1)" to M8a scope and tests in docs/plan.md.
- Acceptance: doc review.
- Needs maintainer: no
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

Verdict: incomplete. Every plan test exists (testbed tests passed in CI); open are flow counters and fields, the conntrack-events wording, identity during a pending revision, DHCP option validation, the hook's fork per lease, and smaller gaps.

| Plan item | Status | Evidence |
|---|---|---|
| Kea container, pinned version | done | `deploy/Dockerfile`, `.devcontainer/Dockerfile` (`ARG KEA_VERSION`) |
| One subnet per network, pools, reservations via `config-set`, DHCP on/off | done | `internal/compiler/dhcp.go`, `internal/kea`; `TestTheClientDrivesARealKea`; testbed `TestDhcpOffOnOneNetworkLeavesItSilent` |
| Lease events via `run_script` | done | `cmd/chaosgw/keahook.go`, `api/devices.go:312`; `TestACommittedHookCarriesEveryLease` |
| Flow observer on conntrack events | partial | polled every second (M6a-04) |
| Flows API | partial | counters always 0, fields missing (M6a-02, M6a-03) |
| Discovery from leases, neighbors, conntrack, WG clients | partial | conntrack only inside tunnel prefixes (M6a-18) |
| Identity events, incremental updates | done, gaps | `owner.go`, `applyloop.go`; M6a-05, M6a-07, M6a-11, M6a-12 |
| Manual device merge | done (as a revision per the spec) | `domain/observed.go:278`; moving overlays waits for M8a (M6a-23) |
| Devices API | done, gaps | `upload_bps`, `download_bps`, `flows_active` missing (M6a-03) |
| §3.11 identity before plans | done | `TestIdentityUpdatesGoBeforeQueuedPlansAndNeverRunConcurrently` |
| T: lease, MAC/IP, reservation, DHCP off, discovered vs configured, identity within 1 s, flows | done | `internal/engine/integration_dhcp_test.go` |
| T: burst debounced into one identity update | partial | asserts ≤3 generations with new devices (M6a-08) |

### M6a-02 `nf_conntrack_acct` never enabled: flow counters always 0
- Status: open
- Severity: medium
- Reason: forgotten — the flows API and §2.3 "current flows (… bytes …)" need accounting; the kernel default is 0.
- Evidence: no reference in the code; `compiler/compile.go:378`; `executor/validate.go:488` (sysctl allowlist); `executor/plan.go:304` (`sysctlPath`).
- Task: allow sysctl `nf_conntrack_acct` (no dev, value 1) in validate.go and map it to `net/netfilter/nf_conntrack_acct` in `sysctlPath`; emit it in compile.go next to `ip_forward`; verify reads it like `ip_forward`; update goldens; extend testbed `TestFlowsOfADeviceAreListed` with `Upload.Bytes > 0 && Download.Bytes > 0`.
- Acceptance: compiler and executor unit tests; CI testbed.
- Needs maintainer: no
- Effort: S

### M6a-03 Flow and device fields of the spec missing
- Status: open (extended)
- Severity: medium
- Reason: partial — spec `Flow` lacks `started_at`, `service`; WireGuard flows have no `network`; `DeviceObserved` lacks `upload_bps`, `download_bps`, `flows_active` (openapi.yaml:4069-4071).
- Evidence: `internal/api/devices.go:29-37,236-249`; `engine/observe.go:288-292`.
- Task: 1. `network`: also match `snap.WireGuardInterfaces[].Address` and peer routes. 2. `service`: `dns_proxy` when the reply source is 169.254.100.2. 3. `started_at`: enable `nf_conntrack_timestamp` like M6a-02 and parse `conntrack -L -o ktimestamp` (check the output format in a CI testbed run first; otherwise leave it out and amend the spec). 4. `flows_active` per device in PollObserved. 5. `upload_bps`/`download_bps` from byte deltas between two polls (needs M6a-02). 6. Tests in `engine/observed_test.go`, `api/devices_test.go`.
- Acceptance: local unit tests; CI testbed.
- Needs maintainer: no
- Effort: M

### M6a-04 Conntrack is polled, not followed through events
- Status: open
- Severity: medium
- Reason: needs-decision — the M6a scope and §3.1/§3.4 name conntrack events; the code runs `conntrack -L` every second and on each `GET /flows`; the executor protocol has no streaming read.
- Evidence: `engine/observe.go:123,259`; `executor/plan.go:295`.
- Task: 1. Executor: add a streaming read. The protocol is request/response today, so add a new request kind on its own connection: `{"type":"watch","what":"conntrack","namespace":...}` after the hello. The server runs `conntrack -E -o id` (fixed path, argument array, in the target namespace) as a child of that connection and sends one JSON line per event (`new`/`update`/`destroy` with the parsed tuple, `internal/linux/conntrack.go`). It kills the child when the connection closes or the executor stops. Watches are reads: they do not take the write queue, need the same peer-credential check, and are limited to a few per client. 2. Client: `executor.Client.Watch(ctx, what, ns) (<-chan json.RawMessage, error)`, plus `executor.Redialing` support with reconnect. 3. Engine: `FollowConntrack(ctx, debounce)`, like `FollowNeighbors`, triggers `TriggerObserve` (debounced 100 ms). The 1 s `conntrack -L` poll becomes a fallback every 10 s. 4. kernelsim: a scripted event source for unit tests. 5. Tests: an executor unit test of the watch lifecycle with a fake runner (the child is killed on close; goleak), an engine unit test that an event triggers an observation, and a testbed test that a new flow from client A appears in `GET /flows` within 300 ms without waiting for the poll. 6. Wire it in `cmd/chaosgw/api.go`. Document it in docs/development.md (executor operations, M6a).
- Acceptance: doc review (a) or a CI testbed test that a new flow triggers an observation within 200 ms (b).
- Needs maintainer: decided 2026-10-04: (b) implement conntrack events now (see the revised task).
- Effort: S (a) / L (b)

### M6a-05 Identity resolved against the committed configuration only
- Status: open (partly fixed: commit triggers an observation, owner.go:557; confirm does not)
- Severity: medium
- Reason: forgotten — while a revision waits for confirmation the kernel runs it, but identity uses the previous configuration; its new devices get empty sets until confirm and the next poll.
- Evidence: `engine/owner.go:664-667` (`o.committed.Config`), :598 (confirm).
- Task: in `observed` use `o.current.Config` when set, else `o.committed`; call `o.e.TriggerObserve()` in `confirm`; `TestANewDeviceOfAPendingRevisionGetsItsAddressAtOnce` in `engine/observed_test.go` (lockout-relevant revision with a device and MAC, fake neighbor, `ObserveNow`: address present and set element compiled).
- Acceptance: local unit test.
- Needs maintainer: no
- Effort: S

### M6a-06 One bad custom DHCP option breaks every scope
- Status: open
- Severity: medium
- Reason: forgotten — custom options are checked only for duplicate codes; a managed code or bad data makes Kea reject the whole `config-set`; the apply "succeeds" with only `DHCPError`.
- Evidence: `internal/domain/validate_network.go:262-268`; `internal/kea/config.go:221-223`.
- Task: 1. Reject codes Kea or the compiler manage: 1, 3, 6, 12, 15, 28, 42, 50, 51, 53, 54, 55, 58, 59, 61, 82, 255 (code `invalid_value` or a new `reserved_option` at `/networks/<id>/dhcp/options/custom/<i>/code`). 2. In engine Preview, when DHCP is configured, run Kea's `config-test` (add `Test(ctx, *compiler.KeaTarget) error` to the `DHCP` interface) and report a `dhcp` problem with Kea's text. 3. Tests: domain validation; engine preview with a fake DHCP that rejects.
- Acceptance: local unit tests.
- Needs maintainer: no
- Effort: S

### M6a-07 A new device forces a full ruleset apply
- Status: open
- Severity: medium
- Reason: deferred (implicitly to M7, not written there) — per-device sets change the set count; a host rotating MACs forces one full apply per poll (registry bound 1024).
- Evidence: `engine/applyloop.go:137-139`; `compiler/dhcp.go:180-216`; `engine/devices.go:77`.
- Task: 1. Add to the M7 scope in docs/plan.md: "identity maps keyed by address → device id replace the per-device sets, so a new device is an element update". 2. Meanwhile rate-limit identity-driven full applies in `owner.observed` (at most one per 2 s, later ones coalesced). 3. Unit test with a fake clock.
- Acceptance: doc review; local unit test.
- Needs maintainer: no
- Effort: S

### M6a-08 Burst test does not assert one identity update
- Status: open
- Severity: medium
- Reason: test-gap — the plan test says "debounced into one identity update"; the test adds 40 new devices (full-apply path) and accepts ≤3 generations.
- Evidence: `internal/engine/integration_dhcp_test.go:346-369`.
- Task: `TestABurstOfNeighborChangesIsOneIdentityUpdate`: `PollObserved(ctx, time.Hour)` so only `FollowNeighbors` triggers; create 40 permanent neighbor entries, wait for the 40 devices; record the generation; in one `ip -batch` replace them with new IPs (same MACs); after 2 s the generation advanced by exactly 1, the sets show the new IPs, and no new ruleset hash was applied.
- Acceptance: CI testbed.
- Needs maintainer: no
- Effort: S

### M6a-09 The Kea hook forks a process with TLS per lease event
- Status: open
- Severity: medium
- Reason: needs-decision — `run_script` with `sync:false`, unbounded; a DHCP flood from an untrusted device forks `chaosgw` plus a TLS handshake per lease.
- Evidence: `internal/kea/config.go:185`; `cmd/chaosgw/keahook.go:22-49`.
- Task: `chaosgw kea-hook` writes one JSON line to a Unix datagram socket `/run/kea/chaosgw-events.sock` (created by the API in the shared `chaosgw-kea-run` volume), non-blocking, dropped when full; the API reads, coalesces and calls `Engine.LeaseEvent`; HTTP stays as fallback; unit tests for reader and hook.
- Acceptance: local unit tests.
- Needs maintainer: decided 2026-10-04: (a) a datagram socket to the API.
- Effort: M

### M6a-10 Kea container path never exercised
- Status: open
- Severity: medium
- Reason: env-limit / deferred — no Docker on the development VPS; the appliance smoke deploys only the executor; M28 covers the full deployment but does not name Kea.
- Evidence: `internal/appliance/appliance_test.go:208-213`; `deploy/compose.kea.yaml`, `kea-start.sh`.
- Task: add to the M28 tests in docs/plan.md: "Kea container starts from compose, the API configures it, a client gets a lease, the hook posts with the real token". Optional now: extend the appliance smoke with `compose.api.yaml` and `compose.kea.yaml`, `up -d`, wait for `kea` running, assert `/run/kea/kea4-ctrl.sock` with group 65532.
- Acceptance: nightly appliance.
- Needs maintainer: no
- Effort: M

### M6a-11 Each identity update reads and verifies the whole target
- Status: open
- Severity: low
- Reason: partial — correct and within 1 s, but every update reads all subsystems and any drift falls back to a full apply.
- Evidence: `engine/applyloop.go:122-129`.
- Task: verify only the nft sets (a restricted read, e.g. `apply.ReadSets(ctx, exec, ns, names)`), compare only the `DeviceSets` elements; unit test of the reads issued.
- Acceptance: local unit test.
- Needs maintainer: no
- Effort: S

### M6a-12 Kernel generation marker not updated by identity updates
- Status: open
- Severity: low
- Reason: needs-decision — §2.14 says the `generation` chain holds the applied generation; identity updates bump the generation but leave the marker at the last full apply.
- Evidence: `engine/applyloop.go:123`.
- Task: amend §2.14: "the marker names the last full apply; identity updates are element operations and do not rewrite it".
- Acceptance: doc review.
- Needs maintainer: decided 2026-10-04: (a) amend the plan.
- Effort: S

### M6a-13 ICMP flow ids collide
- Status: open
- Severity: low
- Reason: forgotten — `flowID` uses ports; ICMP `id=` is not parsed, so concurrent pings share an id and break cursor paging.
- Evidence: `engine/observe.go:315-317`; `internal/linux/conntrack.go:76-95`.
- Task: parse `id=`, `type=`, `code=` into the tuple; include them in `flowID` for icmp; parser test with a real ICMP line; engine test with two flows.
- Acceptance: local unit tests.
- Needs maintainer: no
- Effort: S

### M6a-14 Conntrack read failure is silent
- Status: open
- Severity: low
- Reason: forgotten — a failure (including the 16 MiB output cap) is logged at debug; the last active set is reused without a signal.
- Evidence: `engine/observe.go:142-146`; `executor/runner.go:44`.
- Task: warn (rate-limited); `Snapshot.ObserveError`; health detail "observation degraded"; unit test with a failing fake read.
- Acceptance: local unit test.
- Needs maintainer: no
- Effort: S

### M6a-15 Discovery `sources` `config` and `wireguard` never produced
- Status: open
- Severity: low
- Reason: partial — the spec enum has them; configured devices and WG clients get no sources.
- Evidence: `engine/devices.go:235`.
- Task: `config` for configured devices, `wireguard` for clients, plus `dhcp`/`neighbor` when matched; extend `devices_test.go`.
- Acceptance: local unit test.
- Needs maintainer: no
- Effort: S

### M6a-16 `device_identity_changed` carries no generation
- Status: new
- Severity: low
- Reason: forgotten — spec `Event.generation` is "set for … identity changes".
- Evidence: `engine/owner.go:668-679`.
- Task: in `observed` create the IdentityOnly desired state first and add `"generation": d.Generation` to each event; assert in `TestAnAddressChangeIsAnIdentityEventAndFaultsStayOnTheDevice`.
- Acceptance: local unit test.
- Needs maintainer: no
- Effort: S

### M6a-17 Discovered device sets can hold addresses of other devices
- Status: new
- Severity: low (medium from M7 on, when the sets classify traffic)
- Reason: forgotten — `compileDeviceSets` uses `d.IPs` of discovered devices instead of the resolved `id.Addresses[d.ID]`.
- Evidence: `compiler/dhcp.go:191-197` vs `domain/observed.go:262-268`.
- Task: use `id.Addresses[d.ID]`; `TestAnAddressIsInOneDeviceSetOnly` (configured device with an explicit IP, discovered device seen with the same IP).
- Acceptance: local unit test.
- Needs maintainer: no
- Effort: S

### M6a-18 Hosts behind a router in a LAN network are not discovered
- Status: open (rewritten; the old "address-only entries stay after a MAC sighting" is fixed, `devices.go:162,175`)
- Severity: low
- Reason: partial — conntrack sources become devices only inside WireGuard peer routes; LAN `routes` (downstream routers) are ignored, though §2.3 identifies such devices by IP.
- Evidence: `engine/observe.go:134,197-209`.
- Task: rename `tunnelPrefixes` → `routedPrefixes`, add each LAN network's compiled downstream routes; unit test in `observed_test.go`.
- Acceptance: local unit test.
- Needs maintainer: no
- Effort: S

### M6a-19 `lease4-get-all` every second, unpaged
- Status: open
- Severity: low
- Reason: forgotten.
- Evidence: `internal/kea/client.go:171`.
- Task: `lease4-get-page` (limit 1000, from `start`) in a loop; test against the real Kea in `kea_test.go`.
- Acceptance: local unit test (root and `kea-dhcp4`).
- Needs maintainer: no
- Effort: S

### M6a-20 `observer.WatchNeighbors` has no unit test
- Status: open
- Severity: low
- Reason: test-gap — only exercised indirectly by the testbed.
- Evidence: `internal/observer/netlink_test.go`.
- Task: `TestNeighborChangesTrigger` with build tag `testbed` (creates a namespace, `ip neigh add`, expects one trigger), mirroring `TestEventsAreDebouncedIntoOneTrigger`.
- Acceptance: CI testbed.
- Needs maintainer: no
- Effort: S

### M6a-21 Service token written 0644 in a 0755 directory
- Status: open (downgraded: the volume is reachable only by host root and the three containers that mount it)
- Severity: low
- Reason: forgotten.
- Evidence: `internal/auth/auth.go:411,420`.
- Task: directory 0750, file 0640 (owner 65532 = api and dns; Kea runs as 0:65532 with DAC_OVERRIDE); unit test of the modes.
- Acceptance: local unit test.
- Needs maintainer: no
- Effort: S

### M6a-22 Lease event: `subnet_id` not required; hook trusts any certificate
- Status: open (merged with the old "Hook API address default")
- Severity: low
- Reason: forgotten — the spec marks `subnet_id` required; the hook uses `InsecureSkipVerify` to 127.0.0.1.
- Evidence: `internal/api/devices.go:326`; `cmd/chaosgw/keahook.go:40`.
- Task: add `"subnet_id": body.SubnetID > 0` to `requireFields`; certificate pinning together with M6b-08; extend `TestLeaseEventsComeFromTheServiceOnly`.
- Acceptance: local unit test.
- Needs maintainer: no
- Effort: S

### M6a-23 Merge does not move overlays; WireGuard online state lags
- Status: open
- Severity: low
- Reason: deferred — the spec `Device` says a merge moves the overlays of the discovered entry, but overlays exist only from M8a, whose scope does not list it; WG online comes from the 5 s poll.
- Evidence: `cmd/chaosgw/api.go` (`--poll-interval` 5 s); openapi.yaml:2737-2741.
- Task: add to the M8a scope: "a merge revision moves overlays of the covered discovered device to the configured one".
- Acceptance: doc review.
- Needs maintainer: no
- Effort: S

### M6a-24 `online` semantics undocumented
- Status: wrong-in-list ("Online ignores leases" is intentional and tested: `integration_dhcp_test.go:272`; `devices.go:214-232`)
- Severity: low
- Reason: plan-error — the spec does not define `online`.
- Evidence: `internal/engine/devices.go:214-232`.
- Task: description of `DeviceObserved.online` in api/openapi.yaml: "seen now: a confirmed neighbor entry, a connection, or a WireGuard handshake; a lease alone does not count"; `make generate`.
- Acceptance: `make check-generated`.
- Needs maintainer: no
- Effort: S

Removed from the old list: "Only the first MAC is reserved" — the spec defines `fixed_ip` as "DHCP reservation for the device's first MAC" (openapi.yaml:2752). "Executor priority only unit-tested" — plan §3.11 prescribes exactly that (fake-executor test). "Online ignores leases" → M6a-24.

## M6b (DNS proxy and service namespace)

Verdict: incomplete. Scope and tests exist and the DNS testbed test passed in CI; open are the services' own namespace check (§3.8), a dead holder blocking applies, and test gaps for the holder, the prohibit path and systemd-resolved.

| Plan item | Status | Evidence |
|---|---|---|
| Holder `svcns`, `svc0` pair, table 102 with prohibit fallback | done | `cmd/chaosgw/svcns.go`, `compiler/service.go`, `apply/service_test.go` |
| Re-attach on holder change | done | `engine/service.go` (WatchService), `executor/exec.go`; `TestAHolderThatRestartsIsNoticedAndTheNamespaceReplaced` (simulated kernel) |
| Services leave a stale namespace (§3.8, risk 35) | missing | M6b-01 |
| DNAT for LAN and WG gateway addresses, UDP+TCP | done | `compiler/service.go`; testbed `TestDNSThroughTheServiceNamespace` |
| Forwarding, caching, AAAA removal, query log, registration | done | `internal/dnsproxy`, `api/dns.go` |
| Coexistence with systemd-resolved | done, not tested | M6b-06 |
| Upstream from the host | done, limits | M6b-09 |
| Managed services report health (§2.14, Health enum `kea`/`svcns`/`dns`) | done | `internal/api/system.go` GetHealth |
| T: LAN and WG client over UDP and TCP; restart of the proxy | done | `e2e_dns_test.go` |
| T: no bind on 127.0.0.53, resolved keeps working | partial | M6b-06 |
| T: holder restart healed | partial | M6b-03 |
| T: fail closed | partial | prohibit path never hit (M6b-04) |

### M6b-01 DNS proxy never leaves a stale service namespace
- Status: open (rewrites "`dns` must join the holder's new namespace")
- Severity: medium
- Reason: forgotten — §3.8 "each service checks that its namespace carries the svc0 peer address and exits when not" (S16 C3, risk 35); compose `depends_on … restart: true` covers only a Compose recreate.
- Evidence: `cmd/chaosgw/dns.go`; `deploy/compose.dns.yaml`.
- Task: 1. `func WatchNamespace(ctx, clk clock.Clock, want netip.Addr, interval, grace time.Duration, addrs func() ([]netip.Addr, error)) error` in `internal/dnsproxy`: error once `want` was present and then absent on 3 checks in a row, or never present within `grace`. 2. `runDNS` starts it with 169.254.100.2, 2 s, 60 s, `net.InterfaceAddrs()`; on error log and exit 3 (`restart: unless-stopped` brings it back in the holder's current namespace). 3. Flags `--namespace-check` (default true), `--service-addr`. 4. Fake-clock tests: `TestTheProxyExitsWhenItsNamespaceLosesTheServiceAddress` plus the never-present case. 5. development.md.
- Acceptance: local unit tests; CI testbed via M6b-03.
- Needs maintainer: no
- Effort: S

### M6b-02 A failing service-namespace step fails every apply
- Status: open
- Severity: medium
- Reason: needs-decision — a dead holder (PID file survives a crash in the named volume) makes every revision apply and every rollback fail.
- Evidence: `internal/executor/exec.go:335-341`; `internal/apply/plan.go:66-68`; docs/development.md.
- Task: before compiling, if `ServiceHolderPID()` names a process that does not exist, compile with `HolderPID=0` or skip `serviceOp` (table 102 and the forward guard keep traffic fail-closed); `Snapshot.ServiceError` plus a problem event; `TestADeadHolderDoesNotBlockARevisionApply` (simulated kernel: apply succeeds, `ServiceError` set, a later live PID re-attaches).
- Acceptance: local unit test; CI testbed e2e still passes.
- Needs maintainer: decided 2026-10-04: (a) degrade and report.
- Effort: M

### M6b-03 No test with a real holder process and automatic healing
- Status: open
- Severity: medium
- Reason: test-gap — the plan test "restarting the holder is healed by re-attach and service restart" runs only over the simulated kernel; the testbed deletes the namespace and calls `Refresh()` by hand.
- Evidence: `internal/api/e2e_dns_test.go:255-283`; `engine/service_test.go`.
- Task: 1. `holderPID func() int` in `options` (internal/api/harness_test.go) passed as `engine.Config.ServiceHolderPID`; start `g.e.WatchService(ctx, 200*time.Millisecond)`. 2. `TestAHolderRestartIsHealedWithoutHelp`: holder = `exec.Command("unshare","-n","sleep","infinity")`; wait for `svc0`, start the proxy in that namespace, resolve from A. 3. Kill the holder, start a new one; without `Refresh` wait until the executor re-attached. 4. The old proxy exits through M6b-01; start a new one; resolve from A.
- Acceptance: CI testbed.
- Needs maintainer: no
- Effort: M

### M6b-04 Table 102 and its `prohibit` fallback never hit by a packet
- Status: open
- Severity: medium
- Reason: test-gap — only the DNAT path is tested; nothing sets bit 20 before M7, and M7 does not list the test.
- Evidence: `compiler/service.go:60-75`; `e2e_dns_test.go:142-147`.
- Task: in `TestDNSThroughTheServiceNamespace`: with `svc0` present `ip route get 203.0.113.10 from <ClientAAddr> iif br-iot mark 0x100000` (GW namespace) says `dev svc0`; after the namespace is deleted the same command fails (prohibit / no route); add "fail closed via mark" to the M7 tests in docs/plan.md.
- Acceptance: CI testbed.
- Needs maintainer: no
- Effort: S

### M6b-06 Coexistence with systemd-resolved not tested
- Status: open
- Severity: low
- Reason: test-gap — "the proxy does not bind 127.0.0.53 and resolved keeps working".
- Evidence: `e2e_dns_test.go:200-206`; `TestHostResolversSkipTheLocalStub`.
- Task: in `TestDNSThroughTheServiceNamespace` start `dnsmasq --listen-address=127.0.0.53 --bind-interfaces --address=/stub.test/192.0.2.1` in the GW namespace before the apply as a stand-in for resolved; afterwards `dig @127.0.0.53 stub.test` in GW still answers and A still resolves through the proxy; add a real resolved check on the appliance to M28.
- Acceptance: CI testbed.
- Needs maintainer: no
- Effort: S

### M6b-07 AAAA for a missing name is NODATA; SVCB/HTTPS `ipv6hint` kept
- Status: open
- Severity: low
- Reason: forgotten — the AAAA reply is built before asking upstream.
- Evidence: `internal/dnsproxy/proxy.go:280-289,431-447`.
- Task: forward AAAA queries (cached like others), keep the upstream rcode with AAAA records stripped; strip `ipv6hint` from SVCB/HTTPS (`dns.SVCB.Value`); update `TestAAAAIsRemovedAndNeverAsked`, add an NXDOMAIN case.
- Acceptance: local unit test.
- Needs maintainer: no
- Effort: S

### M6b-08 Proxy and Kea hook trust the API certificate unchecked
- Status: open
- Severity: low
- Reason: forgotten — compose passes no `--api-cert-file`.
- Evidence: `internal/dnsproxy/apiclient.go:44-58`; `deploy/compose.dns.yaml`; `cmd/chaosgw/keahook.go:40`.
- Task: the API writes its certificate PEM to the `chaosgw-service` volume (`/var/lib/chaosgw/service/api.pem`) next to the token; compose.dns.yaml passes `--api-cert-file`; the Kea hook reads `CHAOSGW_API_CERT` (set in compose.kea.yaml); unit tests.
- Acceptance: local unit tests.
- Needs maintainer: no
- Effort: S

### M6b-09 Upstream from host files only, re-read every 5 s
- Status: open
- Severity: low
- Reason: partial — §2.6 "resolver configuration for the uplink"; per-link resolvers are not distinguished (resolved's file lists all links).
- Evidence: `internal/dnsproxy/hostresolv.go`; `internal/api/dns.go:107-123`.
- Task: document the limit in development.md (no code change for V1).
- Acceptance: doc review.
- Needs maintainer: no
- Effort: S

### M6b-10 Holder PID file can name a reused PID
- Status: open
- Severity: low
- Reason: env-limit / deferred to M28 — no Docker here; the PID file survives a crash in the named volume, and a reused PID of another process would be attached (the executor refuses only its own namespace).
- Evidence: `deploy/Dockerfile`, `compose.dns.yaml`, `internal/executor/exec.go`.
- Task: the holder writes its PID together with its netns inode (`stat -L /proc/self/ns/net`); the API passes both; the executor compares the inode of `/proc/<pid>/ns/net` with the expected one before attaching and refuses on mismatch; executor unit test; add the deployment check to the M28 test list.
- Acceptance: local unit test; nightly appliance with M28.
- Needs maintainer: no
- Effort: M

### M6b-11 Direct access to the proxy without DNAT
- Status: open
- Severity: low
- Reason: deferred — note for M9: a "drop UDP 53" access rule must also cover `169.254.100.2:53`.
- Evidence: `internal/compiler/service.go:100-113`.
- Task: add to the M9 scope/tests in docs/plan.md: "access rules on DNS also cover queries sent directly to 169.254.100.2".
- Acceptance: doc review.
- Needs maintainer: no
- Effort: S

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
