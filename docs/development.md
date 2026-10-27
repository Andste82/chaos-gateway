# Development

How to build, test and generate code. Everything runs in the devcontainer
([`.devcontainer/`](../.devcontainer/)); `make help` lists the targets.

## Layout

| Path | Content |
|---|---|
| `api/` | `openapi.yaml`, the source of truth for the domain model and the REST API; examples and their validator |
| `cmd/chaosgw`, `cmd/chaosctl` | the core binary and the CLI (`chaosgw exec` is the executor; `chaosgw tls` is the TLS responder (M21), which hands the connections of the case `intercept` to the proxy (M22); `cmd/chaosctl` is a `main` over `internal/ctl`) |
| `internal/ctl` | `chaosctl` (M18): the commands (Cobra), the API client with its token and certificate options, runs that are waited for with their lease, `wait device`, `with`; the commands of the application-layer kinds, `dns set`, `tls set` and `dhcp set` (M23b), and the interception, `intercept set`, `intercept flows` and `tls ca` (M23c) |
| `internal/clock` | injectable clock: real and fake, wall time apart from monotonic time |
| `internal/preflight` | kernel version, the one shared kernel-module list, namespace capability |
| `internal/testbed` | namespace topologies for integration tests; `vmrun` runs them in a VM; the starter of the interception proxy for the tests that run the real mitmproxy (M22) |
| `internal/model` | generated Go types of `api/openapi.yaml`: the domain model (no hand-written code) |
| `internal/schema` | validates JSON/YAML documents against the schemas of the spec: pointers, codes, unknown fields |
| `internal/domain` | what the model means: decoding, reference resolution, the rules the schema cannot express, built-in profiles, precedence resolution and the classification tables built from it, overlays and their keys, the overlays a revision orphans or moves, observed state and device identity, candidate creation (merge patch), domain diff |
| `internal/overlay` | the in-memory overlay store (M8a): owner, key, TTL, lease, renew, reset, expiry on the injected clock, retargeting after a merge; never persisted |
| `internal/run` | the engine of scenario runs (M15): create, queue per target, preconditions, the timeline on the monotonic clock with `wait` steps, lease, abort, the records of runs on disk, events, JSON and JUnit reports; the checks of a scenario and the zero-hit warnings (M16) |
| `internal/capture` | the packet capture engine (M17): the filter of a capture (a selector as a user space matcher and as a classic BPF program), the pcap and pcapng writers and reader, the files of a capture with ring buffer and size limit, and the session that reads AF_PACKET sockets (runs in the executor) |
| `internal/capturemgr` | the management of captures (M17): the scope of a request resolved to interfaces and filters, the records, the quota and the free-space limit (and the report of both for the Captures screen, M23a), the stream to the executor, the download, and the TLS key log of a capture with the clients a scope covers (M22) |
| `internal/store` | persistence: immutable revisions with checksum, status, commit-confirm, atomic writes, schema migrations |
| `internal/linux` | parsers for `ip -j`, `tc -j`, `nft -j` and `ethtool -k` output (recorded outputs in `testdata/`) |
| `internal/executor` | the privileged executor: closed set of typed operations, strict decoder, scope checks, command planning, serialized queue, Unix-socket protocol with version handshake and `SO_PEERCRED` check, client |
| `internal/compiler` | the pure function from configuration + observed host + generation to the target state: bridges, addresses, sysctls, routes and rules, the nftables layout; golden tests |
| `internal/apply` | reads the kernel state through the executor, plans the difference, applies it in one request and verifies it; `kernelsim` simulates the kernel's tools for tests |
| `internal/engine` | the state owner, immutable snapshots, the apply loop, commit-confirm, rollback, preview and the event bus |
| `internal/observer` | netlink events (links, addresses, routes, rules) debounced into triggers |
| `internal/supervisor` | starts goroutines: recovers panics, reports health, critical goroutines end the process |
| `internal/secrets` | the protected store for key material (mode 0600/0700, atomic writes); WireGuard keys by UUID |
| `internal/bird` | the BIRD 2 configuration: renderer (BGP, OSPFv2, Babel, static announcements, import filters, external mode), lexical checks of custom snippets, parsers for `birdc show protocols all`, the remote side's snippet |
| `internal/api` | the REST API server (M5): the handlers of the spec operations that exist, the guard (authentication, scopes, CSRF), problem+json, idempotency keys, Server-Sent Events, the HTTPS certificate and the listener binder |
| `internal/auth` | the admin password (argon2id), API tokens (hashes only), sessions with CSRF tokens, the one-time setup token, the login rate limit |
| `internal/audit` | the append-only audit log (JSON lines) |
| `internal/linkexport` | what a link's remote side needs besides WireGuard: its BIRD configuration |
| `internal/appliance` | the harness of test level 2 (M5b): Ubuntu cloud images with checksum verification, the cloud-init seed, the QEMU command line, the host topology (bridges, taps, namespaces), SSH into the VM, log collection |
| `internal/diag` | the diagnostics (M24): ping (the unprivileged ICMP datagram socket, the raw one as the fallback), TCP and UDP checks, DNS lookups, HTTP(S) requests, TLS handshake details and the path of DNS, route, TCP and TLS, from the stack of the process or of a network namespace; the structured results of `POST /diagnostics` |
| `internal/tlsresponder` | the TLS responder (M21): the two CAs (the test CA and the unknown CA), the certificates of the cases per name, the ClientHello reader, the connection handling (a case, a passed-through connection, reset on removal), the reports of the handshakes, the hand-over to the interception proxy and the control interface it asks (M22), the client of the internal API |
| `sidecars/tls-proxy` | the interception proxy (M22): the only Python of the project, a mitmproxy addon (`chaosgw_tlsproxy`: the rules, the API client, the addon, the launcher of the container) with its unit tests; the container is `deploy/compose.tlsproxy.yaml` |
| `internal/svcapi` | the client of the internal API that the service containers share (service token, the API's certificate from a file) |
| `internal/kea` | the Kea DHCPv4 server: configuration renderer (one subnet per network on its bridge, reservations), control socket client (`config-test`, `config-set`, leases), the `run_script` hook's environment as a lease event |
| `internal/wireguard` | key generation and derivation, provisioning of a configuration's keys, export of client and link configurations (wg-quick `.conf`, QR as PNG/SVG, zip, export once) |
| `internal/apiserver` | generated Gin server interface (imports the model; the handlers follow in M5) |
| `tools/testvm` | runs the testbed tests: directly or in a VM |
| `web/` | the web app (M12; scenarios M19; captures M23a; the forms of the DNS, TLS and DHCP kinds M23b; the interception M23c; the diagnostics M24): Vue 3, Vite, Tailwind 4, TanStack Query, Pinia, Reka UI, Vue Router, uPlot; `web/embed.go` embeds its build into `chaosgw` (tag `webui`) |
| `web/e2e`, `web/e2e-testbed` | Playwright: the screens against a mocked API, and the browser flows against the real stack in the testbed |
| `clients/` | generated TypeScript and Python clients (not committed), and `clients/examples`: the example suites for pytest and Jest and a scenario file (M18) |
| `deploy/` | the container image (multi-arch) and the compose files of the deployment ([`install.md`](install.md)); `internal/executor/hardening_test.go` pins their hardening |

## Setup

Start the devcontainer from the host with `.devcontainer/start.sh`: it builds the image `chaos-gateway-dev` from
`.devcontainer/Dockerfile` when it is missing (`start.sh rebuild` builds it again and replaces the container),
mounts the checkout at `/workspace` and attaches a shell; a second call attaches to the running container. Copy
`.devcontainer/.env.example` to `.devcontainer/.env` first and fill in `GITHUB_TOKEN` for `gh` and git over HTTPS
(the file is gitignored). The container is unprivileged; on a host with `/dev/kvm` the script passes it through,
so level 1b runs with KVM instead of software emulation. (The image is not `chaos-gateway`: that name is the
gateway's own image, which the compose files start.)

The devcontainer has all system tools. The Python tooling (`.venv`) and the web dependencies
(`web/node_modules`) are installed on demand: the make targets that need them depend on stamp
files, so `make test` or `make check-spec` work in a clean checkout. `make tools` installs both at
once. Playwright needs its browser once: `cd web && npx playwright install chromium`.

## Test levels

The plan (§4.5) defines levels; this is how they map to commands.

| Level | Command | What runs | Needs |
|---|---|---|---|
| 0 | `make test` | Go unit tests (`-race` when a C compiler is installed), web unit tests | no root; the web dependencies (installed by make) |
| 1 | `make test-privileged` | the testbed tests directly | root, kernel modules (a privileged container, or a VM) |
| 1b | `make test-vm` | the testbed tests in a QEMU VM with a stock Ubuntu kernel | `vng`, QEMU, the kernels in `/boot` (all in the devcontainer) |
| – | `make test-testbed` | level 1 if the preflight allows it, else level 1b | |
| – | `make test-arm64` | the unit tests as arm64 binaries under `qemu-user` | `qemu-aarch64` |
| – | `make test-e2e` | Playwright tests of the web app against a mocked API (port and server reuse: see below) | `npx playwright install chromium` once |
| – | `make test-examples` | the example suites of `clients/examples` (pytest with the generated Python client, Jest with the generated TypeScript client, both also driving `chaosctl`) against a gateway on the simulated kernel (M18; test tag `examples`) | network access for `pip` and `npm ci`; no root |
| 1/1b | `make test-vm ARGS='-tags testbed,browser -run InTheBrowser ./internal/api'` | the browser flows of the web app against the real stack in the testbed: first start (M12), a fault created in the UI and measured (M13) (also `test-privileged`) | `make build-web`, `cd web && npm ci && npx playwright install chromium` |

**Playwright port.** The e2e tests (`web/playwright.config.ts`) build the app and start `vite preview` on
the port in `E2E_PORT`, 4173 when it is unset; the tests use the same port as their base URL. Runs in
several worktrees can overlap when each has its own port, and each tests its own build:
`E2E_PORT=4174 make test-e2e`, or `cd web && E2E_PORT=4174 npx playwright test e2e/devices.spec.ts`.
A server that already listens on the port is reused only with `E2E_REUSE=1` (for example a
`npm run preview` you keep running while you write a test); without it a busy port fails the run
instead of silently testing another worktree's build. CI sets neither and never reuses a server (#112).

**Which level where.** The devcontainer is unprivileged on purpose: network tests must not be able
to touch the host's interfaces or other containers. There the testbed runs only as level 1b. In
the VM the tests are root with their own kernel, so the namespaces, veth pairs and nftables rules
exist only in the guest. `make test-testbed` is what you normally run: `tools/testvm preflight`
shows why a machine can or cannot run level 1.

**Testbed tests are never skipped silently.** A test that needs the testbed calls
`testbed.New` (or `testbed.NewDefault`), which fails with the way out when the machine cannot
create namespaces or lacks kernel modules. The tests carry the build tag `testbed`; plain unit
tests do not and run at level 0 only. `tools/testvm run` enforces this at the runner level too
(both `-mode direct` and `-mode vm`, CC-04): any `t.Skip` fails the run unless `-allow-skip` is
given, so a missing tool cannot make CI pass without actually running the test it was supposed
to gate. A run in which no test ran at all (a `-run` or a build tag that matches nothing) fails
too, and `-expect-pass TestA,TestB` fails the run unless each named test passed; the CI job
`testbed-ui` names its three browser flows this way, so a renamed flow cannot leave it green.

### Level 1b: the VM runner

`tools/testvm run -mode vm` (`internal/testbed/vmrun`) compiles the test binaries of all packages
with `testbed`-tagged tests, boots **one** VM and runs them all in it. Results (exit code and
output per package, a completion marker) come back through a read-write share, so a VM that dies
halfway is a failed run, not a silent pass: results of earlier runs are cleared before the boot,
and a run in which no test ran at all fails too. The exit code is 0 for success, 1 when a test or
the VM failed and 2 when the run could not be carried out (timeout, cancel, missing tools). On
a timeout or Ctrl-C the whole process tree of the VM is killed. A work directory the runner
created is kept after a failure and its path is printed; one you name with `-work` is never
removed. It sets `CHAOSGW_TESTBED_EMULATED=1` in the guest when
there is no `/dev/kvm`, which turns accuracy assertions into functional ones (`testbed.Accurate()`).

Things to know:

- **Boot time.** Without KVM the VM runs in software emulation and takes about 8 minutes to boot
  (virtme-ng waits for udev until its 300 s timeout; this happens even with a minimal rule set).
  That is why one VM runs all tests. Run `make test-vm` in the background while you work.
- **KVM.** The VPS has no `/dev/kvm`; hosted CI runners do, and that is where level 1b, the
  nightly kernel matrix, measurement tests and level 2 actually run with real timing. `-no-kvm`
  forces software emulation even where `/dev/kvm` exists, so the emulated branches
  (`!testbed.Accurate()`) can be exercised on a KVM-capable machine too, not only implicitly on
  the VPS; a weekly job (`weekly.yml`) runs `make test-vm ARGS=-no-kvm` for exactly that, in two jobs: every
  testbed package but `internal/engine` (`-test-timeout 100m -vm-timeout 3h`, the limits of `ci.yml` and the
  nightly matrix), and `internal/engine` alone (`-test-timeout 200m -vm-timeout 4h30m`). The engine job is where
  the functional assertions of the engine's measurement tests (M8b to M11) run automatically: the package needs
  about 60 minutes under KVM and about 1.5 to 1.7 times that emulated on a hosted runner, which once ran into the
  100-minute test timeout of the shared job (run 38158196621; P2-AUDIT-14). The engine job ran three times on the
  branch of P2-AUDIT-14 on 2026-10-12 (dispatched, 1 h 20 min to 1 h 40 min with the VM's boot, 285 tests, none
  skipped): 38271140920 failed `TestAFlappingTunnelBlacksOutOnSchedule` and 38283373614 failed it again, both for
  reasons of the emulated run fixed since ("What the persistent VM showed" in "Tunnel faults" below); 38294081018
  passed it. Every other failure of the three runs is one of the known emulated failures listed under "Fast kernel
  loop", with the reason given there: `TestABurstOfNeighborChangesIsOneIdentityUpdate` (a cancelled apply, then a
  full one) and `TestTheIdentityMapEntryFollowsAForcedAddressChangeWithinASecond` (3.4 s for a bound of 3 s) in the
  first, `TestAReservationIsHonored` (a cancelled first apply, the device not yet reachable) in the third. A hosted
  runner emulates faster than the VPS, so these fail there only now and then, and the rest of that list passed.
  So that the job is not red for failures already accepted, it judges its own result: the known emulated failures
  of `internal/engine` (the list under "Fast kernel loop", the `KNOWN` variable of the step) still run, but a run
  whose only failures are on that list passes with a warning and names them in the job summary; any other failure,
  a skip, or a run that did not finish as a test run fails the job. testvm prints the package's summary line also
  after a timeout or a panic of the test binary (run 38158196621: `panic: test timed out after 1h40m0s`, then
  `FAIL ... internal/engine 182 passed, 1 failed`, and a timeout names no failed test for the test that hung), so the
  step does not rely on it: it also requires testvm's only problem line to be the test binary's exit code 1 (a timeout
  and a panic exit with 2, a VM that ended early leaves no completion marker) and finds no `panic:` and no `test timed
  out` in the log. Against the logs of the three runs the judgement fails the first two (the flapping tunnel) and
  passes the third; the log of 38158196621 with its one failure renamed to a known one fails it, as does a known
  failure followed by a panic. The scheduled run 39110380996 on `0fb9176` (2026-10-18) failed
  `TestANewFlowUpdatesTheDeviceWithoutWaitingForThePoll`, which is not on the list and was not a timing bound: the
  udhcpc script of the DHCP tests logged the lease before it set the address, so the test's `ip route replace default
  via 10.10.0.1` could run on an interface without one ("Nexthop has invalid gateway"). The script now logs after it
  has set the address.
- **Terminal.** `vng` refuses to start without a pseudo-terminal; the runner wraps it in
  `script(1)` when there is none, so it works from scripts and CI.
- **Share.** `vng` shares the work directory with the explicit `--rwdir=<path>=<path>` form; with
  a bare absolute path it computes a relative guest path and rejects it.
- **Kernels.** `TESTVM_KERNEL` selects the kernel: `6.8.0-142-generic` (Ubuntu 24.04, the
  default and the minimum supported) or `7.0.0-38-generic` (Ubuntu 26.04).
- **Crashed direct runs.** `tools/testvm sweep` removes `tb...` namespaces a crashed `-mode
  direct` run left behind (level 1b always gets a fresh VM, so it is never affected). It refuses
  while another `testvm` or compiled test binary is running, unless `-force` is given.
- **Measurement record.** `-measurements <file>` (of `run` in both modes and of `vm run`; default
  `$CHAOS_MEASUREMENTS`) appends the measurement record of the run to the file: one JSON line per
  check of every accuracy assertion (see "The measurement record" below). In VM mode the guest writes
  it into the share and the runner appends it after the run, also after a failure or a timeout; the
  file is created even when nothing was recorded (an emulated VM asserts no accuracy).

### The measurement record

Plan §4.3 asks for the measured distributions to be stored per night, so that drift shows up as a
trend before it fails a test (P2-AUDIT-07, decided by the maintainer on 2026-10-12: a nightly
artifact). Every accuracy assertion of the testbed tests checks through `testbed.Measure(t, flow)`:
each check (`Latency`, `Unchanged`, `Spread`, `Delays`, `Loss`, `Share`, `BurstLoss`, `Flaps`, `Rate`,
`RateAtLeast`, `Duration`, or `Record` for a test's own check) is the check of `internal/testbed/stats.go` and also
appends one JSON line (`testbed.Measurement`) to the file `CHAOS_MEASUREMENTS` names: the test, the
assertion (the name `testbed.Statistically` gives it), the flow, the direction, the quantity and its
unit, the kernel, `accurate`, N, the configured value, the measured value, the bounds the check
accepts, the median, 5th and 95th percentile of the delays where the check had them, the loss where it
counted one, the number of the `Statistically` call in the test (`call`, absent outside one), the
attempt (1, or 2 for the repetition of the flakiness policy) and the verdict with the message of a
failure. An attempt of `Statistically` that recorded nothing leaves one line with its verdict
(`"quantity":"assertion"`); an attempt that stops the test (a helper's `t.Fatal`) still leaves the lines
it recorded. Checks outside `Statistically` are recorded as attempt 1: the stream
of `TestUpdatingOneFaultDoesNotDisturbTheOthers` (`Delays`), the largest lateness of the toggles of the
flapping tests, the withdrawal and the re-convergence of the BGP blackout test and the identity-map
convergence of P2-M7-03 (`TestTheIdentityMapEntryFollowsAForcedAddressChangeWithinASecond`,
`TestANewDeviceReachesTheIdentityMapWithoutAFullApply`; `Duration`), the throughput of the device a
rate does not name (`RateAtLeast`), the delays of the reordering test that are neither immediate nor
delayed, and the netem delay of `TestNetemDelayIsVisible`. Without
the variable nothing is written.

Where the record is kept:

- **Nightly:** every job of the kernel matrix (`nightly.yml`, `testbed-matrix`) runs with
  `-measurements measurements.jsonl` and uploads the file as the artifact `measurements-<kernel>`,
  kept for 90 days; the job summary gives the number of lines and of failed checks.
- **Every commit:** `ci.yml` uploads the record of level 1 (`measurements-level1`, native) and of
  level 1b (`measurements-level1b`, KVM) the same way.
- **Comparison:** `go run ./tools/measurements [-match REGEXP] [-tsv] FILE|DIR...` prints, for every
  check, one line per run, oldest first: the value, its ratio to the configured value and where it
  lies between the bounds (0 % the lower, 100 % the upper bound), with the last attempt and the
  verdict. A check a test makes more than once in a run (the phases of a profile switch, each with its
  own configured value) stays apart: the n-th of a run is shown as `#n` and compared with the n-th of
  the other runs, and a repetition replaces only the first attempt of its own call. A directory stands for the `*.jsonl` files below it, so the artifacts of several nights
  can be downloaded side by side and compared at once:

```
gh run list --workflow nightly.yml -L 10        # the nights
gh run download <run id> -D nights/<date>       # each night into its own directory
go run ./tools/measurements -match 'median delay' nights
```

### Fast kernel loop (persistent VM)

A testbed failure that only shows in CI costs a round of 10-20 minutes per guess. The persistent
VM turns the loop into minutes: boot once, then build, run and poke in the same running kernel.
Use the cheapest tool that can show the problem:

1. **kernelsim unit tests** (`go test`, seconds) for logic: what the compiler emits, what the
   engine decides. They cannot know what the kernel accepts.
2. **The persistent VM** for anything about the real kernel: nftables, tc, netem, WireGuard,
   conntrack. This is the default for debugging a failing testbed test.
3. **CI** to confirm. Do not push diagnostic commits to find out what the kernel does: reproduce
   it in the VM first.

```
make vm-up                                  # once: boots the VM in the background
make vm-test ARGS='-run TestX ./internal/apply'     # build the test binary, run it in the VM
make vm-exec CMD='nft list ruleset'         # any command in the guest (files are shared)
make vm-status                              # state, kernel, uptime, queue
make vm-down                                # power off; `-now` kills at once
```

The same as `go run ./tools/testvm vm up|run|exec|status|down`. `vm up` takes `-kernel`, `-mem`,
`-cpus` and `-no-kvm` (KVM is used where `/dev/kvm` exists); `vm run` takes the flags of `run`
that make sense for a booted VM (`-run`, `-tags`, `-test-timeout`, `-keep`, `-allow-skip`, `-expect-pass`, `-measurements`).
`vm exec` takes `-timeout` and `-C <dir>` (default: the current directory; the guest sees the
host's files at the same paths), and one argument is a shell command line, several are an
argument vector.

How it works (`internal/testbed/vmrun/persist.go`): the VM directory (`/tmp/chaosgw-vm`, or
`TESTVM_VM_DIR` / `-dir`) is shared with the guest. The guest runs a serve loop that takes
`q/<id>.job` files, runs them with `sh` and writes `q/<id>.out` and `q/<id>.rc`; the host writes a
job as `.tmp` and renames it, so a half-written job is never run. `vm run` builds the test
binaries on the host exactly like `run -mode vm` and queues one job that runs them. The VM
processes are recorded in `pids` together with their start time, so a pid that was reused by an
unrelated process is never killed.

Measured on the development VPS (6 CPUs, no KVM, software emulation, 2 CPUs and 2 GiB for the VM):

| step | time |
|---|---|
| `vm up`, cold boot to a ready guest | 5 min 50 s |
| `vm exec -- uname -r` | 2-4 s |
| `vm run` of `TestFirstApplyOnARealKernel` (a full real apply; the test itself takes 112 s emulated) | 130 s |
| `vm run` of the kernel gate below, passing | 50 s |

Things to know:

- **One job at a time.** Jobs run in the order they were queued; a second `vm run` waits for the
  first. Run one VM at a time: each takes 2 GiB and two CPUs, and a second would slow both.
- **State carries over.** The guest is not reset between jobs. The testbed tests create
  and remove their own namespaces, but a command you run with `vm exec` (an `ip netns add`, a
  loaded module) stays until `vm down`.
- **Stopping.** `vm down` asks the guest to power off and kills what is left of the recorded
  processes. Never stop the VM with `pkill -f qemu` or `pkill -f testvm`: `-f` matches the shell
  that runs the command (and other people's VMs). A VM whose process died shows as `stale` in
  `vm status`; `vm down` clears it. `vm up` removes the run directories of earlier runs; a failed
  `vm run` keeps its own for inspection.
- **Emulated timing.** Without KVM the guest sets `CHAOSGW_TESTBED_EMULATED=1`, so accuracy
  assertions (`testbed.Accurate()`) are not checked: a green run here proves the logic, CI on a
  KVM runner proves the numbers.
- **Tests that fail under emulation on `main` as well** (checked on 2026-10-07 by running them on
  `main` and on the M8a branch one after the other in the same VM, same log lines): five tests of
  `internal/engine` whose own waits are shorter than a software-emulated full apply takes, or
  longer than a WireGuard rekey: `TestAReservationIsHonored`, `TestAnAddressChangeIsAnEventWithinASecond`,
  `TestABurstOfNeighborChangesIsOneIdentityUpdate`,
  `TestTheIdentityMapEntryFollowsAForcedAddressChangeWithinASecond` (a cancelled first apply: "context
  canceled") and `TestPrivateKeysStayOutOfStoreSnapshotAndLogsAndAReapplyKeepsTheTunnel` (the test
  runs 260 s, WireGuard rekeys every 120 s and the test takes that for a changed session). The rest
  of the engine package, `internal/apply`, the executor and the compiler pass.
  Checked on 2026-10-08 during M9 (same VM, `main` and the M9 branch): `internal/api`'s
  `TestAHolderRestartIsHealedWithoutHelp` also fails on `main` under emulation (its 30 s wait for a
  proxy in the new holder's namespace is shorter than an emulated holder restart plus the first DNS
  answer). Two timing checks of M8b passed on a second run and failed once on the first
  (`TestDeletingALeafDropsItsQueueAndAKindCannotBeChangedInPlace`: its 4 s queue is over before the emulated
  steps are; `TestSwitchingADeviceToANewFaultIdThroughTheEngine...`: one packet in flight between the read
  of the old class and its deletion), neither touched by M9.
  One more since M8b, `TestAHundredWritesWithoutAHeldExecutorAreQuick`: its 5 s bound is far from
  what a native or KVM run needs (about 0.3 s) but an emulated apply of 100 faults takes 6.9 s since
  every apply reads the tc state of all assigned interfaces before and after (P2-M8b-04), where
  `main` needed 3.3 s (measured on 2026-10-07 in the same VM, one apply each; since then the qdiscs of
  all interfaces are listed in one tool run, "The tc tree in the apply"). The bound is not
  widened: it guards against a cost that grows with the square of the burst, and the run that
  counts is the native one.
  A full run of the engine package takes about two hours since M8b (`make vm-test ARGS='-tags testbed
  -test-timeout 170m -vm-timeout 4h ./internal/engine'`; the client gives up after `-vm-timeout`, default 1 h,
  while the guest goes on: the output is in `/tmp/chaosgw-vm/q/<job>.out` when it ends).
  One more in `internal/api`: `TestAHolderRestartIsHealedWithoutHelp` (DNS proxy and service
  namespace; its 20 to 30 s waits are shorter than the emulated restart; same failure on `main`,
  checked 2026-10-07). A whole `internal/api` run under emulation takes longer than the default
  20 min guest timeout: pass `-test-timeout 90m` or select tests with `-run`.

**Bisecting a batch the kernel refuses.** The kernel rejects a whole nftables transaction for one
command and says only `Could not process rule: Operation not supported`. Milestone M7 lost six CI
rounds to one rule that shifted the one-byte `ct direction` value (`ct direction << 16`). To find
such a command by hand:

1. capture the batch: in a kernelsim test set `k.Fail = func(argv []string, stdin string) *executor.Result`
   that stores `stdin` when `argv[0] == "nft"` and the arguments contain `-f`, and return `nil`;
2. ask the kernel without changing anything: `nft -j -c -f - < batch.json` in `make vm-exec`;
3. a prefix of the batch is a valid batch, so bisect on the number of commands.

`TestEveryCompiledRulesetIsAcceptedByTheKernel` (`internal/compiler/nftkernel_test.go`, build tag
`testbed`) does all of this for every configuration in `transactionScenarios`
(`nftsyntax_test.go`): it checks each compiled transaction with `nft -c` in a fresh namespace and,
when the kernel refuses, bisects and prints the first refused command with its index (and every
other refused one, each found by repeated bisection). Add a scenario there when a milestone adds a
kind of ruleset. Run it with `make vm-test ARGS='-run TestEveryCompiled ./internal/compiler'`
before the first push of any change to the compiler's nftables output.

### Writing a testbed test

```go
//go:build testbed

func TestSomething(t *testing.T) {
	top := testbed.NewDefault(t)                 // gateway, two test networks, server, management
	top.GW.Must("tc", "qdisc", "add", "dev", "wan0", "root", "netem", "delay", "50ms")
	r := testbed.MustPing(t, top.A, testbed.ServerAddr, 20, 100*time.Millisecond)
	if testbed.Accurate() { /* tight assertion */ } else { /* the effect is visible */ }
}
```

`NewDefault` builds the topology of plan §4.2 with a unique namespace prefix, so parallel runs
never collide, and removes everything at the end of the test, killing processes first. With
`WithPlainGateway(false)` the gateway is left unconfigured for tests of the product itself.

`NewDefault` returns when every veth end it made is operationally up and every switch port forwards
(`Bed.WaitLinks`; a test that builds its own topology with `Bed.Link` can call it too). "Up" alone was
not enough (P2-M8b-08): a bridge enables a port only once the port's operational state is up, which
the kernel's link-watch work sets, and that work handles a veth's carrier event at most once a second
when it does not count it as urgent. It counts it as urgent when the peer has another interface index,
but the indexes are per namespace: with the gateway's bare ports (`WithGatewayBridges(false)`, the
product tests) the gateway's `lan1` and Switch1's port `q1` are both the third interface of their
namespace, so `q1` stayed down for up to a second after the topology was built. Device C's first ARP
request was lost in that time and its first packets waited a second for the retry: on a native or KVM
kernel, where a test starts sending within that second, the first flow of the Lab network measured a
95th percentile of 0.9 to 1 s. `TestTheFirstPacketOfEveryDeviceGoesThroughRightAfterTheTopologyIsBuilt`
pins it. The product side was ready all along: a capture showed the gateway's `br-lab` up with its port
forwarding before the apply returned, the ARP request never reaching `lan1`, and Switch1 dropping it.
The measurement tests therefore wait for nothing before they measure. What `startFaultLab` still does
first (`meetDevices`, `settleIdentity`) is for the identity, not for timing: the gateway meets every
test device once, so that the engine has discovered them and applied the new identity map before a test
compiles the snapshot and compares it with the kernel (a host the gateway meets for the first time adds
its elements to the identity map; before P2-AUDIT-01 it also shifted the numerals of the others). A test
that compiles the snapshot passes the snapshot's numbering along (`Snapshot.DeviceNums`,
`DeviceNumHigh`), as the engine does with the last applied target.

## The executor

`chaosgw exec` is the only process that writes the kernel's network configuration (plan §3.1). It
accepts a closed set of operations (`nft_apply`, `nft_dup`, `nft_add_elements`, `nft_del_elements`, `routing`,
`tc`, `offloads`, `docker_user`, `assign_interfaces`, `links`, `sysctl`, `wireguard`, `bird`,
`service_ns`, `read`), each a JSON object with a `type` and an optional `namespace`. The decoder
(`executor.Decode`) is strict and is where most of the scope is enforced:
nftables only `inet chaosgw` (and, written by the executor itself from a list of interfaces, the table `netdev chaosgw_dup` of the duplication hook, M10), routes and rules only in tables 100-110 and always with protocol tag
201, tc arguments only from a token allowlist without the keywords that override the validated
fields, in Chaos Gateway's own handles (root `1:`, classes `1:<minor>`, leaves `<minor>:`, filters
on `1:`, M8b; see "tc state and tc operations (M8b)"). What depends on run-time state, the interfaces assigned to Chaos Gateway, is checked by the
executor's worker before the first operation of a request runs. The `ip`, `tc` and `iptables`
batches are built from validated fields only, never from text sent by the caller.

Adding an operation means: a type with `validate` in `validate.go`, its commands in `plan.go`, a
case in `Decode`, golden tests in `plan_test.go`, rejection tests in `decode_test.go` and a seed in
`fuzz_test.go`. The fuzz targets check that anything the decoder accepts yields inert commands that
stay in scope.

`make fuzz` runs both fuzz targets for `FUZZTIME` each (default 2m30s, 5 minutes in CI); the
nightly workflow runs them for an hour each. A crashing input lands in
`internal/executor/testdata/fuzz/`; commit it as a regression test with the fix.

Captures (M17) are not operations of this queue: a capture lives as long as a connection of its own, like a watch,
and changes nothing in the network configuration (see "Packet captures (M17)").

All operations the compiler needs are implemented (`links`, `sysctl`, `wireguard`, `bird` and
`service_ns` followed in M4-M6b, the persistent netlink connection for DNS-derived set updates in M20 as the operation `nft_dns_elements`, "DNS faults and hostname selectors (M20)"). The reader pool
and the operation time stamps came with M8a (see "Coalescing and the reader pool (M8a)"). Which interfaces count as assigned is decided by whoever
may call `assign_interfaces`: loopback and Docker's devices are
refused, the rest is trusted to the (root or allowed-uid) caller. An assigned interface can also be
named OS-owned (`os_owned`, a subset of the assigned devices): the uplink, and the management
interface when it is a NIC of its own, are tc, routing, offloads and DOCKER-USER targets like any
other assigned interface, but `links`, `sysctl`, `wireguard` and `service_ns` refuse to touch one
(`ErrOutOfScope`, M3-01) — the in-band management path and the host's own address stay outside what
a chaos scenario can bring down or reconfigure. The compiler reports `Target.OSOwned` alongside
`Target.Interfaces`. Routing batches use
`ip -force -batch` and treat "exists"/"does not exist" answers as success, so re-sending an
unchanged rule set is safe.

To try the executor by hand (as root, in a namespace you own):

```sh
sudo go run ./cmd/chaosgw exec -socket /tmp/e.sock -state /tmp/e-state.json &
go run ./cmd/chaosgw exec -health -socket /tmp/e.sock
```

## Concurrency

The model is plan §3.11 (D32). In short, for code from M3 on:

- One state-owner goroutine changes the desired state; everyone else sends it commands and reads
  immutable snapshots (`atomic.Pointer`). No mutex around domain state.
- One apply loop compiles the latest snapshot and coalesces concurrent writes into one apply.
- The executor is the only writer of kernel state: one writer goroutine with priorities, reads in
  parallel.
- No mutable package-level state. Mutexes only in leaf components (like `internal/store`), never
  held while sending on a channel or calling another component.
- Bounded channels; every blocking send or receive also selects on `ctx.Done()`.
- Time only through `internal/clock`, enforced by `golangci-lint`'s `forbidigo` (M5-24) for
  `time.Now|Since|Sleep|After|NewTimer|NewTicker|Tick` outside `internal/clock` itself; `_test.go`
  files and `cmd/` (process-lifetime CLI entrypoints, not logic this rule means to keep testable)
  are excluded. `internal/observer` (M4-10) and `internal/appliance` (M5b-04) still have raw calls
  and are excluded for now too, pending those items.
- Start goroutines through the supervisor helper (recovers panics, reports health).
- CI runs `go test -race`; packages that start goroutines run `goleak` in their `TestMain`.

## Domain model and validation

### Validation rules beyond the spec

A few rules span more than one field or need information the JSON Schema cannot express; the
schema's own field descriptions cover most of them (`Ipv4Cidr` "host bits must be zero", `Fault`
"loss and burst_loss are exclusive; blackout and flapping are exclusive", `StepId` "unique within a
scenario", `start` reserved). The ones worth calling out on top of that:

- A BGP neighbor on a link must be the link's own peer (`BgpSettings.neighbor_address`,
  `outside_subnet`).
- `hold_time` must be at least three times `keepalive_time` (BGP); `dead_interval` must be longer
  than `hello_interval` (OSPF); both report `invalid_timers`.
- An access matrix entry from an endpoint to itself is rejected (`matrix_self_entry`); the same
  `from`/`to` pair twice is rejected too (`duplicate_matrix_entry`).
- One link runs at most one routing protocol of a given type (`duplicate_protocol`).
- A DHCP lease time and a DNS fault's TTL have their own minimums (`invalid_duration`): a lease of
  at least 1s, a TTL that is not negative.

### Validation codes

Every `Code*` constant of `internal/domain` (`model.ValidationError.Code` of a `validation_failed`
problem). The codes are stable; new ones are added, never renamed.

| Code | Meaning |
|---|---|
| `invalid_id` | A map key that should be a UUID is not one in lower-case canonical form. |
| `duplicate_id` | The same UUID is used by two objects that share a namespace (devices, WireGuard clients and probes). |
| `duplicate_name` | Two objects of the same kind have the same name (case-insensitive). |
| `name_is_uuid` | A name has the syntactic form of a UUID. |
| `reserved_name` | A profile is named like a built-in profile, or its key is the UUID of one. |
| `unknown_reference` | A reference (name or UUID) does not resolve to an object of the expected kind. |
| `wrong_reference` | Reserved; not produced yet. |
| `invalid_network` | A network's `union` does not decode as the type its `type` field names. |
| `host_bits_set` | A prefix has non-zero host bits. |
| `overlapping_subnet` | Two prefixes that must be disjoint overlap. |
| `reserved_range` | A prefix falls in a range Chaos Gateway reserves (link-local service namespace, multicast, ...). |
| `invalid_prefix_length` | A prefix is longer or shorter than the field allows (e.g. a link needs exactly `/31`). |
| `invalid_address` | A string is not the kind of address the field expects (unicast IPv4, a host address, ...). |
| `outside_subnet` | An address must lie inside a given subnet (as a host address, or as the specific peer) and does not. |
| `duplicate_address` | An address is already used by another object in the same scope. |
| `duplicate_interface` | A host interface is assigned to more than one role (uplink, management, a test network, ...). |
| `duplicate_port` | A UDP listen port is already used by another WireGuard network. |
| `invalid_endpoint` | A `host:port` value does not parse, or names something other than an IPv4 address or host name. |
| `wrong_kind` | A field only makes sense for one kind of network (e.g. `clients` on a link, `peer` on a hub) and is set on the other. |
| `pool_order` | A DHCP pool's `end` comes before its `start`. |
| `pool_overlap` | Two DHCP pools of the same network overlap. A device's fixed address inside a pool is no error but the warning `fixed_ip_in_pool` (see below). |
| `invalid_duration` | A duration is outside the field's allowed range (a minimum, "not negative", ...). |
| `no_identifier` | A device has neither a MAC nor an IP identifier. |
| `duplicate_identifier` | A MAC or IP identifier already identifies another device. |
| `invalid_mac` | A string is not a unicast MAC address. |
| `fixed_ip_requires` | `fixed_ip` needs a local test network and the device's first MAC; one of those is missing. |
| `duplicate_member` | A group lists the same device twice. |
| `probe_network_not_lan` | A probe's network is a WireGuard network, not a local test network. |
| `matrix_self_entry` | An access matrix entry's `from` and `to` name the same endpoint. |
| `duplicate_matrix_entry` | The same `from`/`to` pair appears in the access matrix twice. |
| `management_overlaps_network` | The management network's sources overlap a test network's subnet. |
| `rule_order` | An access control rule is listed twice, or `access_rule_order` omits one that exists. |
| `ports_require_protocol` | `ports`/`port_ranges` are set without `protocol` being `tcp` or `udp`. |
| `invalid_port_range` | A port range's end comes before its start; a port forward's target range would end past 65535. |
| `reset_requires_tcp` | An access rule's action `reset` is used with a protocol other than `tcp`. |
| `cut_existing_requires_tcp` | `cut_existing` is set on a rule whose protocol is neither `tcp` nor `any`. |
| `duplicate_public_key` | A WireGuard public key (provided or generated) is already used by another peer. |
| `invalid_key_settings` | A key setting needs generated keys (`export_once`, `generation`) but the mode is `provided`. |
| `missing_field` | A field that a particular case, action or kind requires is absent. |
| `unexpected_field` | A field is set that the current case, action or kind does not use. |
| `invalid_protocol_settings` | A routing protocol's type-specific settings (`bgp`, `ospf`, `babel`) are set for the wrong `type`. |
| `invalid_timers` | A routing protocol's timers are inconsistent (hold vs. keepalive, dead vs. hello) or too short. |
| `invalid_table` | An external routing table number belongs to the system or to Chaos Gateway itself. |
| `unknown_remote_network` | A remote network scope's CIDR is not inside a client network or a route via a link. |
| `invalid_step_reference` | A scenario step's `remove` names a step that does not exist, created no overlay, or runs later; a `capture: stop` has no `capture: start` of the same target before it. |
| `invalid_parameter` | A run parameter (`POST /runs`, `parameters`, a dotted path at `/parameters/<path>`) cannot be applied to the scenario document: the path is not a path of dotted field names, or a segment or the value does not fit the document there. |
| `invalid_capture_limit` | A capture's `max_size` or the `file_size` of its ring buffer is below 64 KiB, or the ring buffer is larger than the quota, or `max_size` is. |
| `invalid_diagnostic` | A diagnostic request cannot be run as it is: the host is not an IPv4 address or a host name, the `dns_name` is a wildcard, the `url` or the `sni` is not valid, or a ping's echoes and timeout take longer than the 60 s of the request. |
| `target_widened` | A step's `target` is broader than the scenario's own target. |
| `invalid_target` | A scope is not valid where it is used (wrong kind for that context); a port forward's target is not a host in a local test network (a WireGuard client, a device of a WireGuard network, the management network, a gateway address, a network or broadcast address). |
| `tunnel_outside_target` | A tunnel fault or WireGuard action's target is not that WireGuard network or client. |
| `reserved_id` | A step uses the reserved id `start`. |
| `invalid_step` | A step or check names more than one, or none, of its mutually exclusive kinds. |
| `invalid_window` | A check's window is malformed: neither or both of `within`/`until` given, or `from`/`until` do not name real steps in order. |
| `duplicate_step_id` | Two steps of the same scenario share an id. |
| `jitter_exceeds_latency` | A fault's `jitter` is larger than its `latency`. |
| `reorder_requires_latency` | `reorder` is set without `latency`. |
| `mutually_exclusive` | Two fields that must not both be set are both set (`loss`/`burst_loss`, `blackout`/`flapping`). |
| `mixed_directions` | A fault mixes flat parameters with per-direction (`upload`/`download`) ones. |
| `mixed_family` | A fault sets parameters of a family (`impairment`, `tunnel`, `mtu`) other than the one its context allows. |
| `empty_fault` | A fault sets no parameter at all. |
| `invalid_mtu` | An MTU fault's size is outside 552-1500 bytes. |
| `cut_existing_with_allow` | `cut_existing` is set on an `allow` rule (only `drop`, `reject` and `reset` cut connections). |
| `reject_with_requires_reject` | `reject_with` (the ICMP type of a rejection) is set on a rule whose action is not `reject`. |
| `tunnel_parameter` | A tunnel fault sets a parameter other than latency, jitter, loss, burst_loss, blackout or flapping. |
| `invalid_dns_fault` | A DNS fault's action and its fields do not match (a field required by the action is missing, or set for the wrong one). |
| `invalid_tls_case` | A TLS case's fields do not match its case (protocol not tcp, interception fields outside `intercept`, ...). |
| `invalid_dhcp_action` | A DHCP action's fields do not match (`lease_time` only for `short_lease`, `options` only for `set_options`, `ip` only for `force_new_ip`), or the engine cannot carry out `force_new_ip` (the device has no MAC address or no local network, its network has DHCP off, the address named is not inside the network or belongs to another host, no address of the pools is free; the path says whether it is the target or the address). |
| `invalid_overlay` | An overlay request names zero, or both, of `client` and `link`. |
| `invalid_flapping` | A flapping fault's `up` or `down` is zero. |
| `invalid_rate` | A fault's `rate` is below 8 bit/s (netem limits in bytes per second: such a rate would not limit at all). |
| `invalid_name` | Reserved; not produced yet. |
| `duplicate_protocol` | A link already runs a routing protocol of the same type. |
| `missing_routing_settings` | Reserved; not produced yet. |
| `reserved_option` | A custom DHCP option has a code the gateway manages itself (router, lease time, DNS servers, ...). |
| `reserved_port` | A port forward's external ports take a port of the gateway: the UI/API port, a WireGuard listen port (udp), 22/tcp on the management side or the two-port topology's shared interface, 179/tcp while BGP runs (M35a). |
| `overlapping_forward` | Two port forwards of the same protocol share an external port (ranges included). |
| `port_range_length` | A port forward's target range is not as long as its external range. |
| `management_is_uplink` | A port forward enters on `management` in the two-port topology, where it is the uplink's interface. |

`TestEveryValidationCodeIsDocumented` in `internal/domain` checks that every `Code*` constant's
value appears somewhere on this page.

### Validation warnings

`domain.Warnings` checks a valid configuration for what is allowed but may surprise. A warning does
not stop the apply: the preview (`POST /revisions/{id}/preview`) lists each in `warnings` with the
JSON pointer of the field concerned, next to the compiler's warnings.

| Code | Meaning |
|---|---|
| `fixed_ip_in_pool` | A device's `fixed_ip` (path `/devices/<id>/fixed_ip`) lies inside a DHCP pool of its network: a configured pool, or the default pool (the second half of the subnet, split around the gateway's address when it lies inside, #110) when the network names none. Kea allows in-pool reservations, but while another client holds the address from the pool the device gets a different pool address until that lease is released; the other client is refused only when it renews. Not reported when DHCP is off in the network (no reservation is made). Decided in #76 (a warning, not an error): a pool or a reservation that is changed never turns a valid configuration invalid. In the web UI `reservationPool` and `reservationPoolHint` (`web/src/lib/networks.ts`) are the same rule, for the hint that does not block in the device and adopt dialogs (#73, #74); the e2e mock warns with it too. |
| `fixed_ip_dhcp_off` | A device's `fixed_ip` (path `/devices/<id>/fixed_ip`) is in a local test network with DHCP off (no `dhcp` scope, or `enabled: false`): the address stays in the configuration, but the compiler makes no reservation and nothing hands it out until DHCP is turned on there. Reported whether or not another network serves DHCP: it was a compiler warning that only appeared while a Kea target existed, and moved here in #94 so that it does not depend on one and sits at the field. In the web UI the device and adopt dialogs show the same as the hint `dhcp_off` (`fixedIpHints`, `web/src/lib/deviceForm.ts`); the e2e mock warns with it too. |

A new or changed reservation takes effect when the device renews its lease, which it does after half
the lease time at the latest.

## Compiler, apply and engine

Three layers keep the kernel on the committed revision (plan §2.14, §3.2, §3.11):

- **`compiler.Compile`** is pure: the same configuration, host and generation give the same target,
  byte for byte. Everything the kernel needs is in `compiler.Target`; what it cannot build is a
  `Problem` (an error stops the apply, a warning degrades one network). Golden files live in
  `internal/compiler/testdata`; `go test ./internal/compiler -update` rewrites them.
- **`apply.Apply`** reads the state (`ip -j`, `nft -j list`, sysctls, offloads, DOCKER-USER),
  plans the difference in a fixed order, runs it as one executor request and verifies by reading
  back. Stale routes and rules go before the links they refer to, new ones after. nftables is one
  atomic transaction that re-creates the structure, flushes and refills the compiled chains, changes
  the elements of the static sets and maps by their difference with what the kernel holds (the
  elements that are gone are deleted, the new ones added, the ones that stay are not touched; a set
  or map that is new, or whose listing cannot be read completely, is flushed and filled) and deletes
  what the target no longer names; dynamic sets and named counters are never flushed. Not flush and
  fill (P2-M10-09): a flush makes every element of an interval set old for the moment of the commit,
  and the kernel's interval set types can miss a lookup then (pipapo, the concatenated intervals of
  the classification maps such as `devdest`; rbtree, the single-field interval sets and maps such as
  `classify_nets` and the `dev` map; upstream worked on such false negatives in 2025, e.g. "netfilter:
  nft_set_pipapo: don't check genbit from packetpath lookups" and "netfilter: nft_set_rbtree: continue
  traversal if element is inactive"), so a packet of a flow whose fault did not change passed
  unclassified now and then while another fault was written (which explains the one probe in about 470
  that `TestUpdatingOneFaultDoesNotDisturbTheOthers` once saw pass without its fault). An element that is not touched is active in both
  generations and always found; an element whose value changes (traffic that moves to another fault)
  is deleted and added in the same commit and can still miss for that moment.
  `TestAFlowKeepsItsClassificationWhileOtherFaultsAreWritten` (`internal/compiler/classifykernel_test.go`)
  streams two flows (about 20,000 packets a second each) through the `dev` and the `devdest` map while
  the table is switched between two targets in a loop, first with flush and fill (the misses are
  logged, not asserted) and then with the difference (not one packet may miss). On the native CI
  runner (6.17.0-1022-azure) flush and fill missed 113 of 152,920 packets through `dev` and 67 of
  159,680 through `devdest` in 540 transactions, the difference none of about 155,000 each in 547
  transactions; in the level-1b VM (6.8.0-142-generic, the minimum supported kernel, with KVM) flush
  and fill missed 15 of 147,940 and 136 of 148,260 in 81 transactions, the difference none of 147,300
  and 156,600 in 81.
  Verify recognizes a rule by the hash of its expression in the rule's comment, a set by
  its elements, the generation by the comment of the one rule in the chain `generation`.
- **`engine.Engine`** owns the desired state: commands go to one goroutine, readers use
  `Snapshot()`. A failed apply restores the committed revision, a change that could lock the
  administrator out (`LockoutRelevant`) waits for `Confirm` and is rolled back when the window runs
  out, host changes from the netlink observer go into the next apply. `Preview` compares a
  candidate with the kernel and returns the domain diff, the plan and unified diffs per subsystem
  (the `routes` diff also covers bridges, addresses, sysctls and offloads).
  `PreviewAwaitingUplink` is the preview of an apply (`POST /revisions/{id}/apply` and `POST /setup`):
  when the candidate's only error is `uplink_no_address` (the OS has taken the uplink's address away for a
  moment, e.g. `netplan apply` or a Wi-Fi reassociation), it waits for the next host change the observer
  reports (the owner closes a channel when it takes a changed host) and compiles again, up to
  `api.Config.UplinkWait` (default `engine.DefaultUplinkWait`, 10 s); then the apply fails as before
  (issue #64). Tests: `TestAnApplyWaitsForTheUplinkAddressTheObserverReportsBack` and
  `TestAnApplyWaitsForTheUplinkAddressOnlyForABoundedTime` (`internal/engine/uplinkwait_test.go`),
  `TestAnApplyWaitsForTheUplinkAddressInsteadOfFailing` and
  `TestTheSetupWaitsForTheUplinkAddressInsteadOfFailing` (`internal/api/uplinkwait_test.go`).

Tests of these layers do not need privileges: `internal/apply/kernelsim` implements the executor's
`Runner` and answers like the real tools (an nftables transaction is atomic, a set that a rule
uses cannot be deleted, `add set` of another type fails). The real executor with its decoder and
scope checks sits on top of it. The same scenarios run against the real kernel in the testbed
(`integration_test.go` in `internal/apply`, `internal/engine` and `internal/executor`).

What M4 deliberately leaves to later milestones, and where it is weaker than it may look:

- **Verify recognizes rules by a hash in their comment**, not by comparing expressions: a rule
  changed in place with its comment kept goes unnoticed. Sets are compared by type, flags and
  elements, chains by hook, priority, policy and the order of their rule hashes. The next apply
  rewrites every rule anyway.
- **DOCKER-USER** gets accept rules for the bridges of the test networks only (`-i` and `-o`): a
  packet to or from the uplink has a bridge on its other side, and Docker's own bridges keep their
  isolation. Docker that starts after the last apply is not noticed until the next one.
- **Gateway protection** closes the UI port for everything but the management sources and drops
  all but DHCP, DNS and ping from test networks. SSH stays with the operating system.
- The PMTU mirror tables are M10's ("Extended faults (M10)", "MTU and PMTUD").
- A failed `chaosgw apply --file` without `--state-dir` leaves the kernel as the failed apply left
  it; with `--state-dir` the engine restores the previous revision.
- `Rollback` and `Observe` return when the owner has taken the command; `Barrier` waits for the
  apply that follows.

`chaosgw apply --file config.yaml --socket /run/chaosgw/exec.sock` applies a configuration without
the API; `--dry-run` shows the plan and the diffs, `--state-dir` also stores the configuration as the
active revision. In the testbed `--namespace` points it at the gateway namespace. `--ui-port` (default
443) is the port the API listens on (`chaosgw api --port`) when the configuration names no
`management.ui_port`: the file is validated against it (no port forward may take it) and the
anti-lockout rule and the compiler's backstop use it.

### Revision retention

After a revision becomes active (commit or confirm), the owner runs `store.Prune`: it bounds the
revision files to `settings.retention.revisions` (default `store.DefaultRetainedRevisions`, 200;
plan §3.6) and, whatever that count says, discards any candidate whose base is no longer the
active revision — it can never be committed any more (`store.ErrRevisionConflict`). The active
revision, the last known good and a revision still waiting for confirmation are never pruned. The
engine also runs the prune once at start, after loading the committed revision, so revisions left
over from before a restart are bounded too.

This runs synchronously, right after the commit, in the same owner turn: a sibling candidate based
on the revision that just got superseded is reclaimed immediately, so a concurrent apply of it that
is already in flight (a second admin editing at the same time) finds it gone (`not_found`) rather
than getting `revision_conflict` with the new active id. A deferred prune would preserve that
response, but on the fake clock this codebase tests with it would arm an extra timer indistinguishable
from the ones several tests wait for with `clock.Fake.BlockUntil`, which is worse: it was tried and
reverted after it silently broke unrelated WireGuard and routing status tests by letting their own
`Advance` fire before the timer they meant to wait for was even armed. Losing the richer conflict
response in this one race is the accepted trade-off.

## WireGuard

WireGuard networks (plan §2.2.1) are compiled like test networks: an interface `wg-<name>` per
network, table 100 routes for its subnet, the networks behind clients and the static routes of a link,
a policy rule per interface, masquerade towards the uplink, the MSS clamp and the access matrix
(including the `reachable` list of each client as implicit allow entries after the explicit ones).
A network of role `test` is untrusted like a bridge; the tunnel subnet of a `management` network joins
the sources that reach the control plane.

- **Key generations.** Every key generation has a record of its own (`wireguard.KeyID(id, generation)`):
  raising `key.generation` adds one and leaves the keys of the active revision where they are, so a
  revision that is rolled back, fails or is discarded still finds its keys. `Prune` (called by the
  engine when a revision becomes active, with the active configuration and all candidates) removes
  records that no configuration uses. A preshared key that is switched off is only no longer
  referenced. Verify compares the presence of a preshared key, not its value: a changed PSK value
  without a generation bump goes unnoticed, accepted as is since provisioning never does that (M4b-04).
- **Return traffic.** A network behind a tunnel or a router (client networks, static routes of links
  and of test networks) gets a rule `to <prefix> lookup 100` next to the rules for the interfaces:
  replies from the uplink arrive on the uplink interface, and the main table does not know the
  network.
- **Keys.** Private keys live in the secrets store (`--secrets-dir`, `internal/secrets`), never in a
  revision, an operation, a log or an event. `wireguard.Provision` generates the interface key of each
  network and the key pair (and preshared key) of every client in mode `generated`, rotates it when
  `key.generation` is raised, and returns the configuration with the public keys filled in: a
  candidate is provisioned before it is stored. The compiler is given the public keys of the
  interfaces (`Input.Keys`) and holds references only. The executor reads private keys itself through
  a key provider (`chaosgw exec --secrets-dir`, read access); an operation carries a `key_ref`.
- **Apply.** The executor creates the device with `ip link add type wireguard`, sets the MTU and
  synchronizes key, port and peers with `wg syncconf`, which leaves unchanged peers alone: a re-apply
  does not interrupt a tunnel, and apply plans the operation only when something differs. The `wg`
  tool works in any network namespace without entering it, which the testbed needs. `wg show <dev>
  dump` is the read; the parser drops the private key at once.
- **Status.** `Engine.PollWireGuard` reads the peers every interval; the snapshot has the state per
  peer, `wireguard_peer_online` and `wireguard_peer_offline` are emitted when a handshake becomes
  younger or older than three minutes or a peer disappears from the interface. The first poll after a
  restart has no prior state to compare against, so it announces every peer that is online at that
  moment as a fresh event; intended, not a bug (M4b-05).
- **Export.** `chaosgw wg export --state-dir D --secrets-dir S --network lab-hub --client rA
  [--format conf|png|svg|zip] [--out file]`, `--all` for a zip of the hub, `--link` for the remote side
  of a link. An export with a private key is written with mode 0600 (also over an existing file) and a warning; with `export_once`
  the private key is deleted after the export (`--keep-key` prevents that). A client with a provided
  key gets a placeholder. The testbed's remote machines (`testbed.WithRemotes`) bring tunnels up from
  these files with `wg-quick`.

## Dynamic routing (BIRD)

`routing` in the configuration (plan §2.2.2) becomes the configuration of one BIRD 2 instance,
`chaosgw`, run by its own container (`deploy/compose.bird.yaml`, not privileged) next to the
executor. The two share a volume: the executor writes `<bird-dir>/chaosgw.conf` and reconfigures the
running daemon through `<bird-dir>/chaosgw.ctl` (`chaosgw exec --bird-dir`). The daemon starts from
that file, so BIRD and the executor have to name the same path; the executor writes an idle
configuration at start when there is none.

BIRD is pinned to `bird2=2.18-1` (`ARG BIRD_VERSION` in `deploy/Dockerfile` and
`.devcontainer/Dockerfile`), the current Ubuntu 26.04 package version; the image build checks `bird
--version` against it and fails on a mismatch. Bump the two `ARG`s together when the package updates.

- **Compiler.** `Target.Bird` holds the model (`bird.Config`) and its rendered text. A protocol runs on
  one WireGuard link (neighbor = the link peer's address); the filters follow plan §2.2.2: the import
  filter accepts only the allowed prefixes, never a default route, never a protected prefix (the
  gateway's own networks, the management network, the uplink network and the tunnel subnets), and
  `import limit N action block` keeps the session up and simply stops importing past N prefixes
  (M4c-04; `ProtocolStatus.ImportLimitHit`, from BIRD's own `[HIT]` marker, is in `/routing/status`
  as `routes.limit_hit` and in `routing_routes_changed`). BIRD's kernel
  protocol exports only into table 100 (`learn off`, `export where source ~ [RTS_...]`), so learned
  routes never reach the main table. External mode reads another daemon's table through a pipe. The
  input chain accepts BGP (tcp 179), OSPF (ip protocol 89) and Babel (udp 6696) from the link's
  interface, because the interfaces of test networks are otherwise dropped; it matches only the
  interface, not the source address, accepted as is since a point-to-point WireGuard link only ever
  carries traffic from its one peer anyway (M4c-12).
- **Executor.** The `bird` operation (`check` | `apply`) writes the text to a temporary file, runs
  `bird -p -c` on it and only then replaces `chaosgw.conf` and runs `birdc configure`, which keeps
  established sessions. A rejected text never replaces the running one; BIRD's message comes back as
  the error (with the temporary path removed). The text is checked first: no `include`, `kernel
  table` only for Chaos Gateway's tables and the external table the configuration names. `read`
  with `what: bird` returns whether the daemon runs, the hash of the file and the protocols.
- **Apply.** The BIRD step is the last one of the plan, after the interfaces and the firewall it
  needs. It is planned when the file's hash differs from the target's text or the daemon does not
  run; switching routing off applies the idle configuration, which withdraws everything. Verify
  compares the same hash. A target with routing but an executor without `--bird-dir` fails in plan.
- **Preview.** `Engine.Preview` runs the `check` operation: an invalid custom snippet is an error
  problem with BIRD's own message, and nothing changes.
- **Status.** `Engine.PollRouting` reads the protocols every interval; the snapshot has them by name,
  `routing_session_changed` (data `state`: `up` or `down`) is emitted when an adjacency (BGP Established,
  OSPF/Babel up) changes, and `routing_routes_changed` (data `imported`, `exported`, `filtered`,
  `previous_imported`, `previous_exported`) is emitted when a protocol's route counts change. A poll
  that changes nothing publishes no snapshot and emits no event. `kernelsim` simulates BIRD
  (`SetBirdProtocols` sets the protocol table).
- **Remote side.** `chaosgw wg export ... --link --bird [--remote-interface wg0]` prints the BIRD
  configuration for the other end of the link: roles swapped, same timers, a static protocol for the
  remote site's own prefixes.
- **Protected prefixes** also contain the networks the executor routes itself (behind clients, links
  and routers): a more specific prefix from a neighbor would win over them in the kernel.
- **Babel (M4c-05).** Babel's wire protocol needs an IPv6 link-local address on the interface even
  for IPv4-only routing, which a WireGuard interface does not get on its own. For a link running
  Babel the compiler adds `fe80::<the IPv4 transfer address, in hex>/64` to the gateway's side
  (`WGInterface.LinkLocal`, applied as an ordinary `links addr_replace`; the executor's address
  check accepts `fe80::/64` as the one IPv6 exception) — the whole address, not just its last
  octet, so a transfer address ending in `.0` (the low end of a `/31`, as this link's own addressing
  does) does not produce `fe80::` itself, the reserved subnet-router anycast address; `RenderRemote`
  prints the matching `ip -6 addr add` as a comment, since the remote side is not Chaos Gateway's to
  configure (the testbed test plays that operator for its own BIRD instance instead, via
  `bird.RemoteLinkLocal`, before that instance starts: bird's babel does not pick up an address
  added to the interface afterwards). The link's peer also needs `::/0` alongside its usual
  `0.0.0.0/0` in `AllowedIPs` (the one IPv6 exception the executor's own check accepts for a
  WireGuard peer, same idea as the link address one): without it, WireGuard has no peer to route
  Babel's own IPv6 traffic to — including the `ff02::1:6` multicast its Hello and Update packets
  actually use even for IPv4-only routes — and silently drops most of it, confirmed with
  `ip -s link show`'s cumulative counters showing one side almost unable to transmit at all. Tested
  with a real session in the testbed (`TestBabelOverAWireGuardLink`).
- **A BIRD outage (M4c-02).** `runBird` probes the control socket with a harmless read before
  `configure`; when BIRD does not answer, it writes the new file anyway (BIRD reads it at its own
  start) and returns `BirdDownError` instead of restoring the previous one. `apply` and `verify`
  both treat this as best effort, not as a failure: the outage itself is visible through
  `routing_session_changed` (every adjacency the poller knew about goes `down`) and the health
  check (`bird`: no routing protocol reported).
- **Testbed.** `WithRemotes` adds a second remote site (`site2`, 203.0.113.50, network 10.70.0.10).
  The tests in `internal/engine/integration_bird_test.go` run BIRD in the gateway's namespace and in
  the remote ones.

## REST API

`chaosgw api` (plan §2.15, §2.16, M5) is the unprivileged API container. It owns the engine, which
applies through the executor's socket (`executor.Redialing` finds an executor that restarts), and the
stores: revisions (`--state-dir`), secrets and credentials (`--secrets-dir`, the executor mounts it
read-only), the audit log and the idempotency keys (`--data-dir`). `internal/api` implements the
generated `ServerInterface` of `api/openapi.yaml`; the 41 operations with `x-milestone: M5` have
handlers, every other operation answers `422 unsupported_feature` and names its milestone
(`internal/api/unsupported.go`; a test reads the spec and checks both lists).

- **Guard.** One middleware looks the operation up in the embedded spec (`x-required-scope`,
  `security`) and decides: a bearer token (`cgw_…`, only its hash is stored) or the session cookie
  (`chaosgw_session` over plain HTTP, `__Host-chaosgw_session` over TLS — a browser only ever
  accepts that prefix from exactly this origin, Secure and Path=/; a cookie is read under either
  name), the scope (`full` includes `overlays`
  includes `read`), the `X-CSRF-Token` of a session on every unsafe method, and `If-Match` on
  `POST /revisions` (`428` instead of the generated binding's `400`). Until the setup is finished only
  `GET/POST /setup` and the health check answer; everything else is `503 unavailable`. A wrong
  credential is `401`, never "anonymous". The login is rate-limited per client address (5 failures,
  then 5 s doubling to 15 min, `429` with `Retry-After`); the login page says until when to wait from
  `Retry-After` and offers no new attempt until then. Its alert names the time of day once, so a screen reader
  announces it once; the ticking countdown beside it is no live region (the Phase 3 audit, P3-AUDIT-36). A logout clears the page's query cache and the
  draft (`onLoggedOut` in `src/stores/session.ts`, wired in `src/main.ts`; the Phase 3 audit), as an
  ended session clears the cache.
- **Setup.** The first start prints `Setup token: …` to the log (only its hash is stored; every start
  of an unfinished setup prints a new one). `POST /setup` validates the configuration, provisions the
  WireGuard keys, creates revision 1, applies it and only then sets the admin password: a failed apply
  leaves the setup open. Until the setup is done the server listens on every address of the host
  except a test network's own (its bridge, or a test-role WireGuard tunnel —
  `api.NetworkInterfaceNames`, M5-21: a configuration can already be active before setup, e.g.
  `chaosgw apply --file`), afterwards on the management interface, the tunnel addresses of
  management-role WireGuard networks and the loopback (`api.Binder` follows the configuration
  without a restart).
  - **Commit-confirm (M5-03).** The setup's own apply always waits for confirmation
    (`engine.ApplyOptions.ForceConfirm`), the same as any other lockout-relevant change: `LockoutRelevant`
    itself never flags a first revision (there is nothing to compare it against), but a wrong
    management interface there locks the admin out just the same. The admin password is set
    immediately (the admin needs it to log in and confirm or to wait it out), but `api.Binder` keeps
    the broad, pre-setup addresses while the revision is pending (`Snapshot.Pending != nil`), not just
    before `SetupCompleted()` — narrowing down to what the new configuration says the management
    network is, before it is known to be reachable, is exactly the lockout this exists to prevent. A
    revision that is never confirmed rolls back like any other; since nothing is then committed
    (`Snapshot.Revision == 0`), the server's own rollback subscriber (`auditSystemEvents`) calls
    `auth.ReopenSetup()`, which clears the admin password and prints a fresh setup token: the
    password set by a setup that never took effect is of no use, and the operator starts over.
- **Revisions.** A candidate is a complete configuration (`application/json`, also the import) or a
  JSON Merge Patch (`application/merge-patch+json`) against the revision named in `If-Match`;
  `domain.NewCandidate` merges, resolves names to UUIDs, validates; a candidate that fails is not
  stored (`422` with JSON pointers). An import with a `secrets` block stores the keys
  (`wireguard.ImportSecrets`); the export with `include_secrets=true` needs the scope `full` and is
  audit-logged. Apply compiles first (compiler errors are `422`), then hands over to the engine:
  `409 revision_conflict` for a stale base, `409 confirm_pending` while another revision waits,
  `409 not_a_candidate`, `500 apply_failed` / `verify_failed`; a lockout-relevant change is
  `pending_confirm` until `…/confirm` or the window runs out. Writes return `Chaos-Generation`;
  configuration reads carry the active revision as `ETag`.
- **Views.** `/uplink`, `/networks`, `/networks/{id}/clients`, `/groups`, `/routing` are read-only
  views of the active revision (or of a candidate with `?revision=`, whose runtime state is
  `pending`), joined with the engine's snapshot: bridges and ports, WireGuard peers, BIRD protocols.
  Ids in paths are UUIDs or names. `/routing/routes` reads table 100 and classifies the routes
  (`connected`, `static`, `learned`).
- **Idempotency.** `Idempotency-Key` on `POST /revisions`, kept 24 h (persisted): a retry gets the
  recorded response (`Idempotent-Replay: true`), the same key with another request is `422
  idempotency_conflict`, concurrent requests with one key do one thing, an error is not recorded.
- **Events.** `GET /events` is SSE. The engine's bus keeps 15 minutes (at most 20 000 events) for
  `Last-Event-ID`; replay and live stream are joined atomically. A client whose writes block is
  closed after the write timeout (and the bus drops a subscriber whose buffer is full), neither delays
  the publisher or the other subscribers. Event names are the spec's (`EventType`). The events an API
  call causes directly (`revision_created`, `revision_applied`, `revision_confirmed`) carry `actor`
  and `subject`, set at the call site rather than discovered generically; a discarded revision stays
  without an event, since the revision simply stops existing as a candidate and there is nothing
  later to reconcile it against (the audit log still records the discard as a write).
- **Audit.** Every write is an entry (who, through which channel — `ui` for a session, `api` for a
  token —, what, which revision); reads are not. Secrets never enter it. Every entry is fsynced as
  it is appended (integrity over throughput: the log is small and writes are infrequent). Entries
  older than the retention (§3.6: 1 year, `audit.WithRetention` for tests) are dropped at open and
  once a day; a failing append (write or fsync, not a caller's marshal bug) marks the `api` health
  component unhealthy until the next successful one.
- **Password reset.** `chaosgw admin reset-password --secrets-dir D [--data-dir D]
  (--password-stdin | --password-file F | interactively on a terminal, confirmed twice)` changes
  the password from the host; the running API notices the new epoch in the file and ends all
  sessions (within `auth.RefreshInterval`, 500ms: an authenticated request stats the file at most
  that often, not on every request). The reset itself holds an advisory flock (`auth.json.lock`)
  across its own load-modify-save cycle, so a concurrent save by the running API does not lose
  either change. The reset is recorded in the audit log at `--data-dir` (the API's own, default
  `/var/lib/chaosgw/api`) as `auth.password_reset` by actor `{type: system, id: cli}`, distinct from
  whatever the running API's own audit subscriber would otherwise attribute the epoch bump to.
- **Contract tests.** `internal/api/harness_test.go` validates every request and response of every
  test against `api/openapi.yaml` (kin-openapi): status, headers, content type, body schema.
  `g.badRequest` skips the request half for a call that exists specifically to provoke a 4xx the
  spec itself would reject it with (an out-of-range parameter, a missing required field, …); the
  response is still checked. A YAML export is validated by converting it to JSON first (kin-openapi
  decodes YAML numbers differently than the schema check expects); SVG bodies skip the check
  (kin-openapi cannot decode them). Every received SSE event is validated against `Event`. The
  testbed test (`e2e_test.go`) configures a real gateway only through the API and brings up a
  tunnel from a downloaded client configuration.

## Appliance VMs (test level 2)

Level 2 (plan §4.5) tests what only a whole machine has: the host setup, Docker, the compose
deployment, the interfaces the operating system owns. `internal/appliance` boots a gateway VM from
the Ubuntu 24.04 or 26.04 cloud image and drives it over SSH; the tests are
`internal/appliance/appliance_test.go` (build tag `appliance`).

```
three ports:  server ns ── br-up ── [uplink NIC]  VM  [test NIC] ── br-lan ── client ns
                                                      [mgmt NIC] ── br-mgmt ── host (SSH, NAT to the Internet)
two ports:    the management network lives behind the uplink interface; there is no management NIC
```

- **The VM.** Three (or two) virtio NICs on tap devices. cloud-init (NoCloud seed, built with
  `cloud-localds`) creates the user with a throw-away SSH key and writes netplan for the uplink
  (`203.0.113.1/24`) and the management interface (`192.168.56.1/24` with the default route through
  the host, which masquerades towards the Internet). The test port has no address: Chaos Gateway
  assigns it. Images are downloaded and verified against Ubuntu's `SHA256SUMS`, and kept in a cache.
- **The smoke test** boots the VM, checks that netplan configured the OS-owned interfaces, runs
  `deploy/host-setup.sh` (Docker, modules, forwarding; a second run must change nothing), loads the
  current image, starts the executor with `deploy/compose.executor.yaml` until it is healthy, applies
  a configuration with `chaosgw apply --file` through the executor's socket, checks the rule in
  `DOCKER-USER`, and passes ping and HTTP from the client namespace through the VM to the server
  namespace (the server sees the gateway's uplink address: NAT). The same in the two-port topology.
  Then the port forwards (M35a, `portForwards`): the configuration (`appliance.GatewayConfig`) has a
  forward tcp 8088 on the uplink to the client namespace's port 9000, and with three ports one more,
  tcp 8090 on the management network; a container in the VM's Docker publishes 8089. The server
  namespace reaches the device through the forward (the device sees the server's own address: DNAT
  only) and the container through the published port on the same uplink address, the host reaches the
  device through the management forward, and every forward's counter counted. The listener on both
  ends is `testdata/echo`, a static Go binary the test builds (`appliance.BuildEcho`), so it runs in
  the gateway's own image without anything to pull. Logs (cloud-init, journal, Docker, nftables,
  console, QEMU) are collected into the artifacts directory even when the test fails.
- **Running it.** It needs root (taps, bridges, namespaces; the harness uses `sudo -n`), QEMU, `/dev/kvm`
  (without it the tests skip; `CHAOSGW_APPLIANCE_ALLOW_TCG=1` runs them emulated and very slowly) and
  the image as `docker save | gzip` in `CHAOSGW_APPLIANCE_IMAGE_TAR`. The nightly workflow's job
  `appliance` does all of that on a hosted runner (plan D33: they offer `/dev/kvm`); run it on a branch
  with `gh workflow run nightly.yml --ref <branch>`. `make test-appliance` is the same on a machine
  that has the prerequisites. The development VPS has no KVM: everything that needs none is unit-tested
  there (image verification, the cloud-init documents, the QEMU arguments, the topology plan, the
  configuration the test applies with its forwards, the echo helper).
- **Host setup.** `deploy/host-setup.sh` is shipped in the image
  (`/usr/local/share/chaosgw/host-setup.sh`): it installs Docker and the compose plugin when missing
  (Ubuntu's packages), writes `/etc/modules-load.d/chaos-gateway.conf` and
  `/etc/sysctl.d/90-chaos-gateway.conf`, loads the modules and enables forwarding now, tries
  `linux-modules-extra` when modules are missing, and prints the netplan hints. It never touches the
  uplink or management configuration. `internal/preflight` tests that its module list equals the
  preflight's.
- **The test interfaces before the wizard (issue #64).** `host-setup.sh --test-interface IF` also writes
  `/etc/systemd/network/10-chaosgw-IF.network` (`[Match] Name=IF`, `[Link] Unmanaged=yes`; it sorts
  before netplan's generated `10-netplan-*` and every distribution default such as dracut's
  `zzzz-dracut-default.network`), reloads systemd-networkd and then checks the interface. Before it changes
  anything it refuses (exit 2) an interface that carries the host's own connection, because the rule would make
  systemd-networkd drop it at once: the local address of an SSH session is one of its addresses (that of
  `$SSH_CONNECTION`, which `sudo` drops by default, and the local end of every established connection of
  sshd in `ss -Htnp state established`: sshd's process, or the local port 22; a guarded session cannot be
  told from the others, so any one counts), it has a
  default route (`ip -4|-6 route show default dev IF`), or it has a global IPv4 address (`ip -o addr show dev
  IF`); the message says to remove the port's configuration and address first if it really is a test port.
  `--remove-test-interface IF` removes the file again, only when its first line is the host setup's mark.
  `--check IF` changes nothing: the link (exists; no carrier is a warning), the netplan files that name the
  interface or its MAC (file and line; `/run` hides `/etc`, `/etc` hides `/lib`), the NetworkManager
  connections bound to it, and the first systemd-networkd `.network` file in networkd's order (`/etc`,
  `/run`, `/usr/local/lib`, `/usr/lib`, `/lib`; a name in an earlier directory hides the others) whose
  `[Match]` selects it; without `--prefix` also what NetworkManager and `networkctl` say of the running
  interface. It exits 1 while a step is missing, with the fix for each. The preflight of the setup wizard
  reads the `.network` files too (`internal/preflight/networkd.go`, entries with Manager
  `systemd-networkd` and their `[Match]` conditions; `Configuring` takes only the first file that selects an
  interface, and none when that one is `Unmanaged`), those under the host's `/etc` and `/usr/lib/systemd/network`
  the executor mounts, not `/run/systemd/network`, which exists only while networkd or netplan has written to it
  (docs/open-items.md I64-02, decided 2026-10-18; the host setup's check reads it). Both evaluate `[Match]` alike: lists of globs (a list that starts with
  `!` is negated item by item, an empty setting clears it), MAC addresses, the driver, Type `ether` and no
  Kind (a physical port); a WLAN condition never holds, any other condition (Path, Property,
  Virtualization, ...) is taken to hold, and a file without a condition is ignored, as networkd ignores it.
  Tests: `TestThePreflightFindsTheNetworkdFileThatConfiguresAnInterface`,
  `TestThePreflightEvaluatesANetworkdMatch`, `TestThePreflightNamesTheNetworkdFileOfATestInterfaceBeforeTheSetup`
  and `TestThePreflightNamesANetworkdDefaultUnderUsrLib` (API, `GET /setup`), `TestExecutorContainerHardeningProfile`
  (the mount, read-only, at the directory the preflight reads below the host root), `TestHostSetupChecksATestInterfaceAndKeepsNetworkdOffIt`,
  `TestHostSetupTestInterfaceOptions` and `TestHostSetupEvaluatesNetworkdLikeThePreflight` (the same table of
  `[Match]` cases through the script, against a fake sysfs and root under `--prefix`, and stubs of `ip` and `ss` on
  `PATH` for the routes, addresses and connections the guard asks about; an SSH session found by `ss` alone,
  as under `sudo`, over IPv6, link-local and IPv4-mapped, is refused too); the appliance test runs `--check` on its test port, which
  cloud-init's netplan entry keeps failing, and `--test-interface wan0`, which the guard refuses without
  writing the rule.

## DHCP and devices (M6a)

Kea (plan §2.7) runs in its own container (`deploy/compose.kea.yaml`: host network, `NET_RAW` and
`NET_BIND_SERVICE`, read-only root; `kea-start.sh` prepares the restricted paths `/run/kea` and
`/var/lib/kea` and starts it from a configuration without scopes). The API configures it; the engine
learns what the gateway sees and works out which device has which address (plan §2.3).

**Configured address vs. a DHCP lease (M2-03).** `domain.ResolveIdentity` ranks a device's configured
address above an observed lease for the same device: `claimExplicit` always wins over `claimLease`
when both claim an address. This is intentionally stronger than plan §2.3's wording, which only
describes discovery; decided 2026-10-04 to keep the current (stronger) behaviour and document it here,
see `TestTwoDevicesClaimingOneAddressTheStrongerClaimWins`.

Kea is pinned to `kea-dhcp4-server=3.0.3-1` (`ARG KEA_VERSION` in `deploy/Dockerfile` and
`.devcontainer/Dockerfile`), the current Ubuntu 26.04 package version; the image build checks `kea-dhcp4
-V` against it and fails on a mismatch. Bump the two `ARG`s together when the package updates.

Lease caching is off (`"cache-threshold": 0.0` in the rendered configuration). Kea 3 caches by default (threshold 0.25): a
client that asks again within a quarter of its lease is answered with the whole seconds its lease has left, so a short lease
of 9 s renewed a second later was 8 s, which the DHCP actions of M23 promise not to do. Kea wants a float there (an integer
`0` is a syntax error), so the value is a `json.Number` and a test that decodes the rendered document keeps numbers as
written. `TestAClientThatAsksAgainSoonGetsTheWholeLeaseTimeNotWhatIsLeft` (in `M23_LEVEL1`) asks again after 1.2 s; it was
found by a failure of `TestALeaseClassGivesItsClientsTheirOwnLeaseTimeAndNoOtherClient` in the nightly run 40288147095,
whose second ask crossed a second boundary.

- **Compiler.** `Target.Kea` has one subnet per network with `dhcp` switched on, bound to the network's
  bridge: pools (default, `kea.DefaultPool`: the second half of the subnet, split around the gateway's address
  when it lies inside, #110), lease time (default 1 h), router and DNS
  (default: the gateway), NTP, domain, custom options, and reservations from devices with `fixed_ip` and a
  MAC. A subnet id is derived from the network's UUID, so it does not change when other networks come
  and go (Kea keys its leases by it). Every device, configured or discovered, also has a nftables set
  `dev_<id>` with its current addresses (`Target.DeviceSets`); the rules of the fault milestones will
  match them.
- **Kea.** `kea.Client` speaks the control socket; `Apply` runs `config-test` before `config-set`
  (a failed `config-set` can leave Kea without its lease database). `engine.KeaDHCP` sends the
  configuration after the kernel state is verified; a Kea that is down does not fail the apply: the
  problem is in the snapshot (`DHCPError`) and the configuration is sent again every 5 s, also after Kea
  restarted (its configuration hash changed).
- **Kea's packet sockets (issue #64).** Kea opens its packet socket on an interface only while it is
  configured (`config-set`, which also detects the interfaces again: `re-detect`), and skips one that is
  down or has no carrier (Kea: "is not running"); a bridge has a carrier only while one of its ports is
  up. A configuration that arrived while the test port flapped left the bridge without a socket, and
  DHCP silent on that network, until Kea was restarted. The configuration lets Kea retry the sockets it
  could not open, every 5 s for a minute (`service-sockets-max-retries`, `kea.SocketRetries`);
  `status-get` then reports the sockets as `retrying` instead of `failed`. No test shows that a retry
  opens the socket once the link is back, and the gateway does not count on it: the engine follows the
  links of Kea's interfaces. Every reading of the host after a netlink event (`Engine.Refresh`,
  `apply.ReadHostLinks`, the bridges included) compares their index, whether they are running (`UP` and
  `LOWER_UP`) and the kernel's count of carrier changes (`carrier_changes`, read with `ip -j -d -s -s link
  show`) with the reading before, and when one is running and was not, was created again (a new index: a
  socket bound to the old one receives nothing) or had its carrier go and come back (a flap within the
  500 ms debounce of the readings, which both see the link running) it sends the configuration again
  although Kea runs it already (`DHCP.Reopen`). The reading only takes note: `runDHCPRetry`, the engine's
  DHCP goroutine, sends, so a slow or hung Kea never holds up the following of the host (the uplink
  address, the forwards' entry addresses). The comparison starts from the links the apply read back just
  before it sent the configuration (`Result.After`), so a bridge the apply created is not configured a
  second time, while a carrier that changes during the send is caught. Because `DHCP.Apply` does not send
  a configuration Kea runs already, the apply first compares its read-back with the last reading in the
  same way: an interface of the running configuration that came up or was created again since then (an
  apply that creates a deleted bridge again with an unchanged scope) is sent with `DHCP.Reopen`
  (`Engine.syncDHCP`). A bridge going down, another
  interface and a reading without a change send nothing. Tests: `TestTheDHCPServerIsConfiguredAgainWhenItsBridgeComesUp`
  (engine: a carrier, a flap between two readings, a recreated bridge and the first reading after the
  apply that created the bridge in the simulated kernel), `TestAnApplyThatCreatesTheBridgeAgainConfiguresTheServerAgain`
  (engine: a deleted bridge the next apply creates again, and a bridge that came up before an apply, both
  with an unchanged DHCP configuration, are sent with `Reopen` once), `TestADHCPServerThatHangsDoesNotHoldUpTheReadingOfTheHost`
  (a `Reopen` that hangs; the readings return and observe the uplink's new address),
  `TestTheHostReadingCountsACarrierFlapOnARealKernel` (testbed: a veth flap counts two carrier changes,
  same index, running before and after), `TestTheConfigurationLetsKeaRetryTheSocketsItCouldNotOpen`,
  `TestKeaRetriesASocketItCouldNotOpen` (the real Kea on the loopback, which it never opens a socket on,
  reports `retrying`), `TestReopenConfiguresKeaAgainWithAnUnchangedConfiguration` (the real Kea:
  `status-get`'s `reload` shows that `Reopen` configured it and `Apply` did not) and the testbed test
  `TestDHCPAnswersAgainAfterTheTestPortFlappedDuringAnApply` (the switch side of the test port goes
  down, a changed scope is applied, Kea reports the bridge not running; the port comes back, a client
  gets a lease and the engine log shows the reconfiguration). Both testbed jobs name the four tests
  that need root with `-expect-pass $I64_LEVEL1`. The older tests against the real Kea, `TestTheClientDrivesARealKea`
  and `TestLeasesArePagedPastTheLimit` (`startKea`), carry the build tag `testbed` in `internal/kea/kea_testbed_test.go`
  too, and both testbed jobs name them with `$KEA_LEVEL1`: before, they skipped at level 0 (no root, no Kea) and
  had run only by hand since M6a (I64-05). `$KEA_LEVEL1` also names the two tests against the real Kea without the
  tag, `TestKeaAcceptsTheRenderedConfiguration` (internal/kea) and `TestTheConfigurationIsSentAgainToAKeaThatRestarted`
  (internal/engine, root only): their packages have tagged tests, so the testbed runner builds and runs them, and a
  skip there fails the job (the review of 2026-10-18).
- **Observation.** `Engine.PollObserved` reads the neighbor table, conntrack and Kea's leases every
  second and when asked (`TriggerObserve`: netlink neighbor events through `FollowNeighbors`, lease
  events); a burst of triggers is one reading. The state owner's `tracker` (`devices.go`, a pure state
  machine with unit tests) keeps the registry of discovered devices (the UUID is a hash of the MAC, so it
  is stable across restarts), resolves identity with `domain.ResolveIdentity`, sets online state and
  emits `device_discovered`, `device_online`, `device_offline` and `device_identity_changed`.
  Online state and traffic rates follow plan §2.3 (issue #79): a device is online until `offlineAfter`
  (60 s, `Config.OfflineAfter`) after its last sign of life; the tracker keeps each connection's
  counters of the last conntrack read (keyed by `flowID`) and each device's per-read deltas of the last
  `rateWindow` (10 s). An observation that did not read conntrack (`ConntrackRead` false) keeps the last
  rates and carries the last read's `Active` and `UnknownSources` over. The API tests run the engine on
  the real clock, so `checksGateway` sets `OfflineAfter` to a nanosecond: there a device is offline at the
  first observation without a sign of life, as before.
  The acceptance of #79 runs on the real kernel too (issue #92, `integration_observe_test.go`, `$I79_LEVEL1`),
  with the observation wired as `cmd/chaosgw/api.go` wires it: `TestASteadyTransferShowsASteadyRateInEverySampleOnTheRealKernel`
  (a TCP transfer shaped to 1 MB/s by tbf on the server, sampled every 5 s for a minute: every sample known
  and within ±20 %, under emulation only known, non-zero and below twice the link rate),
  `TestADeviceThatTalksEveryThirtySecondsStaysOnlineWithoutFlapsOnTheRealKernel` (a UDP datagram every 30 s
  that is never answered, the product's 60 s, watched for 150 s: five intervals, each going through the same
  states, instead of the 10 minutes of #79) and `TestAnUnpluggedDeviceWithAnOpenConnectionGoesOfflineAfterOfflineAfterOnTheRealKernel`
  (`OfflineAfter` 20 s; the device stays online for `OfflineAfter` after its last sighting, goes offline within a
  poll after `OfflineAfter` has passed since its last sign of life, and its TCP connection is still established).
  A last sign of life can come after the unplug: what the device sent just before it is counted by the next
  conntrack read (up to `conntrackFallback`, 10 s, later), and a neighbor entry that was REACHABLE at the unplug
  stays a sign of life until the kernel turns it STALE (up to one and a half times `base_reachable_time`, 45 s).
  The identity does not flap either (issue #93, option (a), decided 2026-10-24): `ResolveIdentity` keeps
  the stale (STALE, DELAY, PROBE) neighbor entry of a configured device as a lingering claim of its
  address (`claim.yielding`, the device's least current address) that gives way to any claim of another
  device on the address (`yieldToOthers`), a discovered device seen with it included (a lease or neighbor
  entry of a MAC no configured device has), so a device with a static address set on itself (no lease, no
  fixed IP) keeps its address between two confirmations and no `device_identity_changed` is sent. A
  probe's stale entry still counts only while the address carries connections. Tests:
  `TestAStaleNeighborOfAConfiguredDeviceKeepsItsAddress`, `TestAStaleNeighborClaimGivesWayToAnotherDevice`,
  `TestANeighborEntryBetweenConfirmationsDoesNotFlap` (with a lease and without one),
  `TestAStaleEntryGivesItsAddressToTheNewHolder`.
- **Conntrack events (M6a-04).** `Engine.FollowConntrack` asks the executor to watch conntrack
  (`conntrack -E`, the executor's `Watch`/`Event` frames over the same Unix socket as ordinary
  requests, one dedicated connection per watch) and debounces the events into `TriggerObserve`, the
  same way `FollowNeighbors` debounces netlink neighbor events. While the watch is up, a ticked
  (non-triggered) poll skips its own conntrack read and reuses the last flows for up to 10 s
  (`conntrackFallback`); past that, or when the watch is down (unsupported executor, or the watch
  dropped and is being redialed), the ticked poll reads conntrack itself as before, so flows never
  go stale for longer than the fallback window. `GET /flows` and `Engine.Flows()` always read conntrack
  live on demand, independent of the watch or the poller. Each flow also carries `StartedAt`
  (M6a-03, `nf_conntrack_timestamp`, read with `conntrack -o ktimestamp`'s `start=` field) and the API
  exposes it as `started_at` when the kernel reports one. A `[DESTROY]` event carries the connection's
  final counters (`nf_conntrack_acct`); the engine keeps them (`noteConntrackEvent`, at most
  `maxEnded`) until the next conntrack read takes them as `observation.Ended` (issue #91), and the
  tracker adds what each moved after the last read that saw it (`tracker.readFlows`). The read takes
  them before it runs, so a connection that ends during the read waits for the next one; a destroy
  reported only after the read that already missed the connection finds its counters among the
  tracker's `gone` connections, kept for one read, so nothing counts twice.
- **Identity updates.** A change of the addresses of known devices makes a desired state flagged
  `IdentityOnly`; the apply loop then sends `NftAddElements` and `NftDelElements` for the changed sets
  instead of rebuilding the ruleset, and verifies. A device that has no set yet (a new one) needs a full
  apply. The executor takes such requests before queued plans (a second queue class) and never runs two
  requests at once. The generation rule in the kernel keeps naming the last full apply.
- **Lease events.** Kea's `run_script` hook starts `chaosgw-kea-hook`, which runs `chaosgw kea-hook`: it
  writes the event as one JSON datagram to the Unix socket the API serves at `--kea-events-socket`
  (`CHAOSGW_KEA_EVENTS_SOCKET`, default `/run/chaosgw-kea-events/events.sock`, M6a-09). The directory
  is the volume `chaosgw-kea-events`, owned by the API's user (65532, mode 0750, made so in
  `deploy/Dockerfile`); the Kea container (group 65532) mounts it read-only, which is enough to send to
  the socket. It used to be Kea's control socket directory, which Kea keeps at `root:65532` 0750, so the
  API could not create the socket there (issue #57 F3); the CI job `arm64` (`ci.yml`, step "the Kea
  hook sends to the API's events socket through a read-only mount") sends a datagram from a
  container with the read-only mount, the Kea container's user and no capability to a socket of user
  65532 on a fresh volume. A datagram needs no connection and no TLS handshake, so a DHCP flood from an untrusted device costs the
  hook a `write(2)` and the API a few bytes, not a forked process and a handshake each. The kernel
  drops a datagram rather than blocking the hook if the API is slow or the socket's buffer is full.
  When the socket is missing or the write fails for any reason, the hook falls back to
  `POST /api/v1/internal/dhcp/lease-events` with the service token (scope `service`, the only scope
  that reaches `/internal`; written by the API to the `chaosgw-service` volume, which Kea mounts
  read-only) — the only path before M6a-09, kept for exactly this case. Either way the event is
  published as `dhcp_lease` and makes the poller read at once.
- **API.** `GET /devices`, `/devices/{id}` (configured, WireGuard clients, probes and discovered devices
  with their observed state), `GET /networks/{id}/leases`, `GET /flows` (from conntrack, with the device
  of each flow). For the active revision a device also carries `effective`, the winning fault of each family
  for its traffic without a destination (what `GET /explain` resolves for the device, the same `FaultRef`s),
  and `overlays`, the overlays among the candidates, winning or overridden (`effectiveOf` in
  `internal/api/devices.go`; the Phase 3 audit: the device list's Faults column and the device detail read
  it). Adopting a discovered device and merging two devices are revisions (the spec's
  `Device`).
- **Tests.** The tracker, the engine (with a fake DHCP server) and the API are unit-tested; the real
  Kea is exercised against the loopback (`internal/kea`, root and `kea-dhcp4` needed, otherwise skipped)
  and in the testbed (`integration_dhcp_test.go`: udhcpc clients get a lease, a reservation is honored,
  DHCP off leaves a network silent, identity events within a second, a burst of neighbor changes, flows).

## DNS proxy and the service namespace (M6b)

The gateway services run in a **service namespace** of their own (plan §3.3, D29), so that the faults of
a device apply to its connections to them like to any other traffic. M6b adds the namespace and the DNS
proxy; the TLS responder (M21) joined it, see "TLS responder: certificate cases (M21)".

- **The pair.** The executor operation `service_ns` (closed, link-local only: `169.254.100.0/30`) creates
  the named namespace when it is missing, or attaches the namespace of the holder process with
  `ip netns attach NAME PID`, then creates the veth pair `svc0` (gateway, `169.254.100.1`) and `svc1`
  (namespace, `169.254.100.2`), brings both up and points the namespace's default route at the gateway.
  `svc0` is an assigned interface; the operation refuses any other. `Read service_ns` tells whether the
  namespace exists and whether it is the holder's (same inode as `/proc/PID/ns/net`). The holder writes
  its PID file as `<pid> <inode>` (`chaosgw svcns`, its own `/proc/self/ns/net`); when the API passes
  both on to an `ensure`, the executor refuses to attach if the PID's current namespace inode no longer
  matches, since the PID was reused by some other process since the holder last reported it (M6b-10);
  0 (a PID file from before this, or the API not passing it) skips that check.
- **Compiler.** With `Input.ServiceNS` set, `Target.Service` holds the pair; routes and rules follow:
  `169.254.100.0/30 dev svc0` and `iif svc0` use table 100 (what the services send upstream leaves
  through the uplink, masqueraded; since M21 what a passed-on connection sends to a server in a test network or behind a tunnel leaves through that interface, masqueraded the same way, decision 13 of M21), table 102 has `default via 169.254.100.2 dev svc0` and a `prohibit
  default` fallback with the higher metric, and the rule `fwmark 0x100000/0x100000 lookup 102` stands
  before the policy rules (the classification of M7 sets the mark). Queries to a network's or a
  WireGuard network's gateway address (UDP and TCP 53) are DNAT-ed to `169.254.100.2:53` in a
  `prerouting` chain; the client's address stays, so the proxy sees the device. **Fail closed:** a
  packet for `169.254.100.0/30` that does not leave through `svc0` is dropped in the forward chain, never
  routed to the uplink. The services reach the API's port on `169.254.100.1` and nothing else of the
  gateway (input chain).
- **Apply and verify.** The plan creates the pair before routes and nftables; verify reads the namespace
  (peer address, state, default route, holder). A holder that restarted leaves the old namespace behind
  (the name keeps it alive): the executor deletes the pair and the name and attaches the new one. No
  netlink event announces that, so `Engine.WatchService` (every 2 s) reads the namespace and the PID
  file and applies again when the holder changed or the namespace is wrong (a persistent failure is
  retried every 30 s); a vanished pair is noticed by the host watcher. The executor refuses a holder
  that does not exist or sits in its own network namespace, and the operation is pinned to `svc0`/`svc1`
  and `169.254.100.0/24`.
- **DNS proxy** (`internal/dnsproxy`, `chaosgw dns`). `miekg/dns`, UDP and TCP, in the namespace. It
  forwards to the upstream resolvers in order (a truncated UDP answer is asked again over TCP; a UDP
  client gets the truncation it asks for), caches until the TTL ends (negative answers 30 s, errors not;
  bounded; the key includes the DO bit and the OPT record is added per client), answers the static entries of a network (`dns.static_entries`), never forwards `.invalid`,
  and answers AAAA queries with no data and removes AAAA records from other answers (the V1 networks
  are IPv4 only). Until it has a configuration it answers SERVFAIL. The upstream resolvers are the
  uplink's `dns_upstream`, else the host's: `/run/systemd/resolve/resolv.conf` first (the stub
  `127.0.0.53` of systemd-resolved is not reachable from the namespace, and the proxy binds nothing on
  the host), then `/etc/resolv.conf`, loopback addresses left out. **Limit (V1):** resolved's file
  lists all of the host's links together, so per-link upstream resolvers (plan §2.6) are not
  distinguished; every network gets the same host resolver set.
- **API.** The proxy keeps no state: `GET /internal/dns/config` (long poll, `after` = generation) gives
  the networks (gateway, client range, static entries), the upstream and the strip flag; the generation
  starts at the boot time in milliseconds and moves with the content. `POST /internal/dns/queries` takes
  its query log in batches (the API adds the device from the observed state); `GET /dns/queries` lists
  them newest first (filters `device`, `name` with `*.suffix`, `since`; the last 20000 are kept in
  memory). The API listens on `169.254.100.1` too once the pair exists.
- **Containers.** `deploy/compose.dns.yaml`: `svcns` (`chaosgw svcns`: no network, the host's PID
  namespace, writes its PID) and `dns` (`network_mode: service:svcns`, `NET_BIND_SERVICE`, the service
  token read-only). The executor runs in the host's PID namespace to attach the holder; the API reads
  the holder's PID file (`--service-holder-pid-file`) and the namespace name (`--service-ns`). After the
  holder container was recreated, `dns` has to be restarted to join the new namespace: it checks every
  2s that its own namespace still carries the service address `169.254.100.2`
  (`dnsproxy.WatchNamespace`, `--namespace-check`, on by default), and exits (code 3) once that address,
  having been there, is gone on three checks in a row, or never showed up within 60s. Compose's
  `depends_on … restart: true` then restarts `dns` and it joins the holder's current namespace; a plain
  crash-only restart policy would leave it stuck in the old, now-empty one. The TLS responder (`chaosgw tls`)
  and the launcher of the interception proxy (`tls-proxy`, M22, P5-AUDIT-06) watch their namespace the same way.
- **Tests.** The operation, the compiler, apply and verify (over the simulated kernel, including the
  holder that dies and the holder that restarts), the proxy (fake upstream, real sockets), the API and
  the commands are unit-tested. The testbed test `TestDNSThroughTheServiceNamespace` runs the proxy inside a real
  service namespace with `dnsmasq` as the upstream: clients of a test network and a WireGuard client
  resolve over UDP and TCP, the log shows the queries, a restarted proxy resolves again, and without
  the namespace the redirected queries are dropped by the guard. `TestAHolderRestartIsHealedWithoutHelp`
  uses a real holder process (not the simulated kernel's bookkeeping): killing it and starting a
  replacement is noticed by `Engine.WatchService` on its own, without the test calling `Refresh`; the
  proxy left behind in the now-orphaned namespace exits on its own once the pair into it is torn down
  (`dnsproxy.WatchNamespace`, M6b-01), and a freshly started one resolves again once it joins the new
  holder's namespace.
- **Limits.** With `strip_aaaa` (on by default, V1 networks are IPv4 only), an AAAA query only ever
  loses its own AAAA record, never the whole answer: a name that exists gets NOERROR/NODATA, a name
  that does not exist still gets upstream's NXDOMAIN. The same policy removes a SVCB/HTTPS record's
  `ipv6hint` parameter (RFC 9460), the only other place an answer can carry a usable IPv6 address;
  every other parameter of that record is untouched. A holder that has died (its PID is gone, not
  merely restarted under a new one) does not fail the apply or any later revision (M6b-02): the
  engine compiles with `ServiceHolderPID` 0 instead of asking the executor to attach a PID it cannot
  find, the existing namespace is left as it is, and `Snapshot.ServiceError` names the degradation
  (cleared again once a live holder is found and reattached — `WatchService`'s own probe tells the two
  cases apart, since a holder that is simply not attached yet is not degraded). `/run/systemd/resolve` is mounted read-only into the API container so that the
  proxy finds the real resolvers of a systemd-resolved host; `TestDNSThroughTheServiceNamespace` checks
  the reverse too, with a stand-in for resolved's local stub: it keeps answering on `127.0.0.53`
  throughout, undisturbed by the proxy or the compiled ruleset (M6b-06; the appliance's own M28 smoke
  test checks it against the real `systemd-resolved`).
  The proxy and the Kea hook verify the API's certificate against the one it publishes to the
  `chaosgw-service` volume (`--api-cert-file`/`CHAOSGW_API_CERT`, set by default in
  `compose.dns.yaml`/`compose.kea.yaml`); without it, either trusts whatever is presented (they only
  ever talk to the gateway over a private link). The proxy connects to `169.254.100.1`
  (`compiler.ServiceHostCIDR`), so the API's self-signed certificate has that address besides the
  loopback (issue #64); a self-signed certificate of an earlier version without it is issued again at
  the API's start with the same key, names and addresses, and one someone installed is left alone
  (`TestTheCertificateIsValidForTheServiceAddress`,
  `TestAnOlderSelfSignedCertificateGetsTheServiceAddress`). The proxy reads the published
  certificate at every request and makes a new client when it changed, so it trusts a certificate the
  API issued again without a restart (`TestTheAPIClientTrustsTheCertificateTheAPIIssuedAgain`).
  Until the setup has been applied the proxy cannot reach the API (the link to its namespace is made by
  the first apply). `Follow` warns once, with a hint, and again only when the error changes; the
  attempts in between are logged at debug level, and the recovery once (issue #57 F6,
  `TestTheProxyWarnsOnceWhileTheAPICannotBeReached`). The pause between two attempts doubles from 1 s to at most 10 s while no
  configuration has been in use, and stays at 2 s once one has (P5-AUDIT-14): a restart of the gateway drops the overlays, and the
  DNS faults of the configuration the proxy keeps while the API is away go within 2 s of its return
  (`TestTheProxyAsksAgainWithinTwoSecondsOnceAConfigurationWasInUse`).
- **Not in M6b:** DNS faults, hostname selectors and the redirect of hardcoded resolvers (M20, done: "DNS faults and hostname selectors (M20)"), the TLS responder (M21, "TLS responder: certificate cases (M21)"),
  `/internal/dns/resolutions` (M20, done), per-device query statistics.

## Classification (M7)

Plan §3.3's lookup chain (`internal/compiler/classify.go`): on prerouting, write the winning fault's
id and the packet's direction into reserved mark bits, for test/WireGuard/remote-network traffic
only. M7 built the mechanism and proved it with test ids; since M8a the maps are driven by the
winners of `domain.Resolve` and the ids are real (see "Faults in the compiler (M8a)" below, which
also changes two things said here: the maps are interval maps, and an element `goto`s its chain).

- **Mark bits.** Bits 4-15 hold the fault id (12 bits, up to the 4095 of plan §3.3's capacity limit),
  bit 16 the direction (0 = original/upload, 1 = reply/download, read from `ct direction`). Bits
  17-19 (PMTU), 20 (service selection, already compiled by M6b and written by the TLS cases since M21, `tls_redirect`: the
  maintainer confirmed on 2026-10-11 that writing it stays with M20/M21, because setting it changes which
  policy-routing rule wins for real DNS/TLS traffic, `ServiceRulePriority` 900 before the policy table's 1000, a
  behavior change that needs the real redirect-interaction test, P2-M7-01) and 21-23 (reserved) are untouched by this chain; so are bits 0-3
  and 24-31. `MarkKeepOnIDWrite` (0xffff000f) is the mask kept when the id is (re)written: it leaves
  the direction bit alone. `TestMarkMasksKeepTheDirectionBit` pins this against spike S15's bug
  (0xfffe000f), which cleared the direction bit on every classification and gave both directions of a
  connection the upload side's parameters.
- **Direction bit.** Written by two mutually exclusive rules at the top of the lookup chain
  (`ct direction reply` sets bit 16, `ct direction original` clears it), not by one
  `mark | ct direction << 16` expression: the kernel refuses to shift the 1-byte `ct direction`
  inside a bitwise expression (EOPNOTSUPP on 6.8.0-142, the minimum supported kernel), which fails the
  whole atomic nft batch. `nft -c` in the dev container cannot catch this (no `CAP_NET_ADMIN`);
  reproduce kernel rejections with `make test-vm ARGS='-no-kvm -run <regex>'` or by bisecting the
  JSON batch with `nft -j -c -f -` inside `vng -r 6.8.0-142-generic --disable-kvm --exec ...`.
- **The guard.** A set of test, WireGuard and remote-network prefixes (`classify_nets`, built from
  every bridge's and WireGuard interface's address and routes) decides whether a packet is classified
  at all: neither the source nor the destination (the conntrack original tuple, so NAT does not
  defeat it) being in that set returns at once, before the mark is read or written. The gateway's own
  traffic — management, updates, BIRD, the WireGuard underlay — is never touched here, unless a
  tunnel fault targets it from M10 onward.
- **The lookup chain.** Four nftables verdict maps, most specific first: device+destination+port,
  device+destination, device+port, device. These are all the levels: M8a added group, network and
  global selectors by folding them into the entries of the sources they apply to (every device, and the
  stretches of a network's addresses no device owns), not as levels of their own (P2-M8a-01, see
  "Classification tables" in M8a), and kept the maps keyed by address (P2-M7-02, below). A
  protocol-only selector ("all UDP", ICMP) gets no map keyed on `meta l4proto`: it becomes a level-3
  element (level 1 with a destination) over the full port range of its protocol, `1-65535` for tcp and
  udp and `0-65535` for ICMP, which has no ports (`domain.portSels`, `compiler.clsKey`). Each level's key is a concatenation of conntrack-original fields (`ipv4_addr`, `ipv4_addr`,
  `inet_proto`, `inet_service`, as needed) and its value is a verdict that jumps to a per-id chain
  (`mark_<id>`) and ends; a classification map cannot combine the lookup with the bitwise mark
  write in one nft statement (confirmed against the real `nft` parser), hence the extra indirection
  instead of a map whose value is the shifted id itself. (M7 wrote `jump` here and a `return` after
  the lookup. A jump comes back to the next RULE of the calling chain, not to the rest of the rule,
  so with two levels holding an entry for the same traffic both ran and the last one won. M8a's
  real faults showed it, `TestTheMoreSpecificLevelWinsWhenTwoLevelsHoldEntries` caught it on the
  kernel, and the elements now `goto` their chain, which ends the base chain's evaluation.)
- **Identity map.** The Phase 1 per-device address sets (`dev_<id>`, M6a-07) are replaced by one
  nftables map, `ident4` (address → device number), built by `compileIdentity` from the same known
  devices (configured, WireGuard clients, probes, and discovered devices from the observed state) the
  old per-device sets used. `DeviceNums` assigns each device a number (0 reserved for
  "no device"); the classification maps above are meant to be keyed on device number once group
  resolution needs it, but M7's own four maps still key on address directly, since without faults to
  resolve there is nothing yet that must look a device number back up (P2-M7-02). A device's address change is
  now one incremental element update of this single map, the same way `applyloop.go`'s `identityOps`
  already diffed per-device sets one at a time in M6a: a changed key is deleted and re-added in the
  same incremental request (a map add refuses an existing key), a full apply still fills the map from
  the latest observed state.
  **Stable device numbers (P2-AUDIT-01, decided 2026-10-12 as option (a)).** M7 first numbered the
  devices by their position in the sorted id list, so a new device renumbered the others and took a
  full apply, throttled to one per two seconds. Now the apply loop hands the numbering of the last
  applied target back to the compiler (`Input.DeviceNums`, like `Input.FaultIDs` for fault ids): a
  device keeps its number while it is known, a new one takes the number above the highest one handed
  out in this process (`Input.DeviceNumHigh`, carried across failed applies too), and a device that
  goes frees its number without anyone taking it over. The reuse policy: a number is not handed to
  another device while the process runs, so an element that may still carry it (an apply that failed
  half-way; later, a consumer that copies the number into packet or connection state) never names the
  wrong device; only when the 2^31 − 1 numbers are used up does a new device take the lowest free
  one. The numbering is not persisted: nothing outside the identity map holds a device number today
  (no rule reads the map's values, no mark or conntrack label carries one), so after a restart the
  first full apply numbers the devices afresh in id order and replaces the map's elements by their
  difference with the kernel, which is correct whatever the previous process had numbered. A
  consumer that writes the number into state that outlives the process must persist the allocation
  (or flush that state at start). The identity map exists also while no device is known, so the first
  device and the last one that goes are element updates as well. `identityOps` therefore accepts a
  device that came or went as long as every other device keeps its number and the fault structure is
  unchanged: the new device's `ident4` elements, and its classification elements where a fault of its
  group or network covers it, go in by element operations (no nft transaction but the element
  transaction, `TestANewDeviceIsAnElementUpdateNotARebuild`, and on the real kernel within the 3 s of
  P2-M7-03, `TestANewDeviceReachesTheIdentityMapWithoutAFullApply`). A new device still takes a full
  apply when it changes the tc tree: a rate, queue limit or "keep order" on its group or network gives
  it a queue of its own (D18). The window of M6a-07 (`identityConvergeWindow`, 2 s) now holds back only
  that case: the owner converges every identity change at once, and the apply loop delays a full apply
  that devices which came or went need until two seconds after the last one, taking the newest desired
  state then (`TestNewDevicesThatNeedAFullApplyConvergeOncePerWindow`); an address change of a known
  device is never held back (`TestAnAddressChangeIsNotHeldBackByTheWindow`), and neither is a desired
  state that also carries another change since the last applied one, such as an overlay written while
  an apply ran whose own desired state the loop skipped for the newer one a device made (`desired.Changed`,
  `TestAnOverlayChangeThatCoalescedWithANewDeviceIsNotHeldBack`). `Nft` gained `Maps` alongside `Sets` (flushed and refilled like a set at
  a full apply, since P2-M10-09 changed by their difference like a set, with the same object-cleanup handling `nft.go`'s `Transaction` already had for sets,
  and a latent bug fixed there: a removed map was deleted with `delete set`, not `delete map`);
  `kernelsim` simulates map objects (add/flush/delete, element add/delete, jump-target and
  set/map reference checks) so the apply and engine test suites exercise the new mechanism without a
  real kernel; `verify.go`'s `VerifyIdentityMap` replaces `VerifyDeviceSets`.
- **Tests.** `classify_test.go` covers the mask, the guard, the single direction-bit write, the lookup
  chain's order, `TestClassifyIDs`' per-id chains, `classifyNets`' coverage of test/WireGuard/remote
  prefixes, and that the chain runs before the service redirect (`ClassifyPriority` -150, before
  `service.go`'s prerouting at -100) without touching its mark. In the testbed,
  `internal/apply/integration_classify_test.go` gives devices real faults (overlays), installs the
  compiled tc tree and checks the per-class counters (and the faults' own nft counters) increase only for
  matching traffic in both directions, behind NAT, across two test networks, with non-test
  (management) traffic's mark left untouched, and with a change of the maps moving an already
  established, long-lived connection to its new class without waiting for it to end ("per packet, not
  per connection", plan §3.3: see `TestClassificationMarksOnlyMatchingTrafficBehindNAT`,
  `TestClassificationAcrossTwoTestNetworks`, `TestNonTestTrafficKeepsItsMarkUntouched`,
  `TestAMapChangeMovesAnEstablishedConnectionToItsNewClass`).
  `internal/engine/integration_classify_test.go` runs the same checks for a WireGuard
  client network host, as both initiator and destination, and over a WireGuard link
  (`TestClassificationForAWireGuardClientNetworkAsInitiatorAndAsDestination`,
  `TestClassificationOverAWireGuardLink`). `TestTheIdentityMapEntryFollowsAForcedAddressChangeWithinASecond`
  checks the real kernel's identity-map entry (not only the API event) follows a forced address
  change: within 3 seconds, the bound the test asserts on the shared CI runners (the convergence path adds two
  real `nft` round trips; one level-1 run took 1.96 s and a loaded level-1b VM 4.7 s before the bound was
  loosened, and no extra throttling is on the path), and within the plan's one second on dedicated hardware
  (H1), which is the target. Decided by the maintainer on 2026-10-11 (P2-M7-03); what the test asserts functionally,
  that the map reaches the new address and not a stale one, is unchanged. In the persistent VM (software
  emulation) the test says nothing, because the first full apply outlasts its wait. Two tests over the
  simulated kernel (a `blockingNftExec` wrapper above the kernel's own lock holds a full apply's nft
  write) show that a full apply compiled with an old address does not win with it:
  `TestAConcurrentFullApplyDoesNotRestoreAStaleDeviceAddress` lets the address change while the write
  is held and observes it after the apply returned, and `TestAnObservationDuringAFullApplyIsAppliedAfterIt`
  (P2-AUDIT-04) delivers the observation into the owner while the write is held: once the full apply
  has landed, the loop diffs the identity update against what that apply wrote and moves the element
  with an element update, without a further observation.
- **Not in M7:** real fault ids (M8a onward), group- and network-level selectors (M8a/M9; the access
  rules of M9 expand their scopes into address sets, not into this lookup chain), the output
  hook and IFB for tunnel faults (M10), writing the service-selection mark bit from real traffic
  (M20/M21, confirmed by the maintainer on 2026-10-11, P2-M7-01) and the connections-redirected-to-a-gateway-service
  classification test that goes with it.

## Overlays and precedence (M8a)

The domain layer of M8a, built on `domain.Resolve` (Phase 1) and on M7's classification maps, the
compiler's side of it (the subsection "Faults in the compiler") and the state owner's and the API's
side ("Overlays in the engine and over the API"). Coalescing and the reader pool are described under
"Coalescing and the reader pool (M8a)"; orphaning and the merge of discovered devices under "What
applying a revision does to overlays" in "Overlays in the engine and over the API (M8a)".

- **Store** (`internal/overlay`). Holds the active overlays in memory; a restart starts empty
  (plan §2.1.1). `Put` takes an owner and a validated request (`domain.ValidateOverlay`, references
  as UUIDs); the key is `domain.OverlayKey` (owner, kind, target, selector, family part of a
  fault's selector); an existing key is replaced, keeping id and `created_at` (change `Updated`,
  HTTP 200), otherwise the overlay is new (`Created`, 201). `updated_at` increases strictly, even
  with a standing or backward-jumping wall clock, so "newer wins" (D26) is a total order. TTL and
  lease deadlines are monotonic (`clock.Clock.Monotonic`); `expires_at` and `lease_expires_at` are
  derived from them for display. `Renew` restarts a lease (no event, no generation); a replacement
  restarts both timers. A write with `expires_in` (only with a `ttl` at least as long; `domain.ValidateOverlay` says
  `missing_field` or `invalid_duration` otherwise) runs its first TTL for that time instead, and the overlay keeps `ttl`
  as written; `expires_in` is write-only and never part of an overlay as read. An edit in the web UI keeps an overlay's
  remaining time this way (P3-M13-06; `TestExpiresInKeepsTheRemainingTimeAndTheTTL`, `internal/api`
  `TestAnEditWithExpiresInKeepsItsTTLAndExpiresAfterTheRemainingTime`, `TestOverlayRequestRules`
  "expires_in …", `TestNewOverlayFillsKindOwnerAndTimes`). Mutations return `Change`s (`Created`, `Updated`, `Removed`, `Expired`,
  `Orphaned`, with a reason and the event name of the spec); the store sends nothing itself. The
  store is a passive structure with its own mutex; the state owner is its only writer, publishes the
  changes and assigns the generation (`PutOptions.Generation`). Expiry is one call, `Expire`; `NextDeadline` and
  `Expirer` (a timer on the injected clock that calls back) let the state owner schedule it. Nothing
  removes an overlay implicitly, so a write that arrives before the expiry command is ordered
  before it. `Reset(owner)` removes one owner's overlays (`nil`: all, for `?owner=all`; the caller
  checks the scope), `Delete`, `Orphan(ids, reason)` and `Retarget(moves, generation)` complete it.
  `Retarget` moves overlays between devices after a merge revision; two overlays that then share a
  key keep the newer one (reason `merged`). `Move` moves named overlays by id (a commit-confirm
  rollback undoes a merge's moves with it), and `Amend(change, checkpoints...)` makes a change in
  checkpoints too, so a later `Restore` keeps it.
- **What a revision does to overlays** (`domain/orphans.go`). `OrphanedOverlays(current, next,
  overlays)` lists the overlays that refer to an object of the current configuration that the next
  one lacks, with the JSON pointers of the deleted objects for `references[]`; the engine rejects the
  revision with `validation_failed` unless `?force=true`, then calls `Store.Orphan`. A discovered
  device is no object of the configuration. `DiscoveredMerges(next, discovered)` maps a discovered
  device that `next` now covers (by MAC, or by address) to the configured device that owns the
  identifiers; the engine calls `Store.Retarget` with it before it looks for orphans.
- **Precedence** (`domain/resolve.go`). One winner per family per query, overlays before
  configuration, ten levels, newer wins on the same level (D26), no merging of parameters. The pick
  within a level is per scope: each scope puts forward its champion (a fault beats a profile part
  of the same scope, E8; otherwise the newer entry), and the newest champion wins. A pairwise order
  over all candidates is not transitive (E6 and E8 together) and made the winner depend on the
  input order; `TestTheWinnerOfALevelDoesNotDependOnTheInputOrder` pins the fix. The tests E1–E8
  and E12 exist twice: as pure domain tests (`resolve_test.go`, with the example configuration) and
  through the store (`internal/overlay/precedence_test.go`: write, replace, expire, reset, then
  resolve); E9 is the compiler's golden of M10 (`TestE9ABadLTEProfileOnANetworkGivesEveryDeviceItsOwnQueueWithTheFullRate`, measured on the real kernel by `TestAnIotRateOfTwoMbitGivesEveryDeviceOfTheNetworkItsOwnTwoMbit`), E10 follows with the tunnel faults, E11 in M21. E5 resolves the DNS family, which takes
  effect in the DNS proxy since M20 (`compiler/dns.go`, "DNS faults and hostname selectors (M20)").
- **Classification tables** (`domain/table.go`, `domain/sources.go`). `World.Sources(identity)` lists
  the sources of traffic (every device with its addresses, every address range that identifies a
  device, and the stretches of network addresses that no device owns); `World.Table(source,
  family)` turns the resolution into the entries of the four lookup levels of plan §3.3 for the
  families that select by destination, protocol and port (impairment, MTU). The destination space
  (IPv4) and the protocol/port space are cut at the boundaries of the faults' selectors into
  disjoint pieces, each piece is resolved with the same rules as `Resolve`, and the entries are
  the minimum that makes the first-match lookup give the resolved winner everywhere. `Table.Lookup`
  simulates the chain; `TestTheLookupChainGivesWhatResolveSays` checks it against `Resolve` for
  random worlds. `uplink` is the complement of the known prefixes; hostnames are not in a table
  (their addresses exist at run time only, M20) and are listed in `Table.Unresolved`. Group and
  network scopes are folded into the tables of the sources they apply to, and not given lookup levels of
  their own as plan §3.3 first described them (amended to the four levels, P2-AUDIT-03): the winner of a key can come from any scope, and that is what makes the
  precedence rules (D24, E1) hold with a first-match lookup (with separate levels an overlay at a less
  specific scope could never override a more specific configured fault). The price is map size (devices
  times entries instead of one entry per network); if that turns out too large, a device's entries that
  equal what its stretch's table gives can be dropped (`Table.Signature` makes the comparison cheap).
  Decided by the maintainer on 2026-10-11 (P2-M8a-01).
  *Coverage* (`Table.Coverage`, P2-M8a-06). The pieces span a grid: the destination pieces plus the addresses no
  destination selector names, times the protocol/port pieces plus the protocols and ports no selector names. On a cell
  of the grid nothing changes, so every candidate matches all of it or none, and one wins all of it. `Table.Coverage`
  counts, per candidate (`Candidate.Key`, `layer:id:family`), the cells it matches and the cells it wins
  (`domain.Cover`). The build looks only at the cells where a candidate names both a destination and a port, or where a
  destination-only and a port-only candidate meet; the other cells are counted per row and column (their winner is the
  row's, the column's or level 4's), and the candidates the build drops before it looks at a cell (an older one of the
  same scope and level) are counted as matching every cell of their row, column or the whole grid. The counts are not
  traffic volumes; they tell "all", "some" and "none" apart. `TestTheCoverageOfATableIsWhatResolveSaysCellByCell` asks
  `Resolve` for one traffic of every cell of random worlds and compares; `TestTheCoverageCountsDroppedCandidatesAndAFullDestinationCover`
  adds the dropped candidates and destinations that cover every address. The compiler sums the coverage of every
  source's table into `Target.Coverage` (impairment and MTU). A fault can be the winner of an entry that no traffic
  reaches (the level-4 entry below destination faults that cover every address); it wins nothing, so `Target.Winners`
  and `Target.Effective` leave it out (`TestAFaultWhoseEntryNoTrafficReachesWinsNothing`).
  *The prefixes of a network* (`domain.prefixesOf`, which scope matching, the destination `network`,
  `ResolveAccess`, the fault tables, `NetworkOf` and the access rules' source sets all use) are its subnet and,
  for a local test network, the networks behind its downstream routes (plan §2.3: devices behind another router in the
  network), like the networks behind a hub's clients and a link's routes. So "network IoT" in a rule or in a fault
  selects a host at 10.30.0.5 behind IoT's router although no device is configured for it, the destination
  `uplink` does not include it, and the classification guard (`classify_nets`) covers the same addresses.
  Decided by the maintainer on 2026-10-11 (P2-M9-04; before, a local network meant its subnet only).
  `TestANetworkIncludesTheNetworksBehindItsDownstreamRoutes`, `TestARuleForANetworkSelectsTheHostsBehindItsRouter`,
  `TestAFaultForANetworkReachesTheHostsBehindItsRouter` and `TestExplainSelectsAHostBehindANetworksRouterByTheNetwork` pin it, and the
  random worlds of `TestTheLookupChainGivesWhatResolveSays` and `TestTheCompiledLookupGivesTheResolvedWinner` include such a host.
  *Cost.* The first version looked at every cell of the (destination piece x port piece) grid and
  scanned all candidates per cell: cubic in the number of overlays that name their own destination
  and port (4.5 ms for 10, 11.7 s for 160, 42 s for 240), and the compile runs inside the state
  owner. A candidate is now one of four classes (names a destination, a port selector, both or
  neither); a cell can only differ from what the levels below give where a candidate names both for
  that very cell, or where a destination-only and a port-only candidate meet, so only those cells
  are looked at, with the candidates reduced to one champion per (layer, level, scope) first. 10000
  overlays with their own destination and port build in 0.3 s. The work is bounded: a table that
  needs more than `domain.MaxTableCells` (8192) cells is refused with `*TableTooLargeError`, the
  tables of all sources that differ by `compiler.MaxCompileCells` (32768), the classification maps
  by `compiler.MaxClassElements` (8192); each is `capacity_exceeded` naming the faults that select by
  destination only or by port only (their product is what grows). Sources that the same candidates
  apply to share one table (`Table.Cells` is 0 for the later ones). The worst accepted set compiles
  in about 0.1 s; `TestABurstOfOverlaysThatOverflowsTheClassificationIsRefusedAtTheLimit`
  throws such a burst at the engine.

### Faults in the compiler (M8a)

`compiler.Compile` turns the winning impairment faults into ids, classification elements, mark
chains with counters and a tc tree. It is still a pure function: the engine gives it the overlays
(`Input.Overlays`), the allocation of the previous compile (`Input.FaultIDs`) and the limits
(`Input.ClassLimit`, `Input.QueueBudget`), and gets `Target.Faults`, `Target.FaultIDs` (feed it back)
and `Target.TC`. `apply.Apply` puts the tree in the kernel and verifies it (M8b, "The tc tree in the apply").

- **What is resolved** (`compiler/faults.go`). For every source of traffic (`World.Sources`: each
  device with its addresses, each range that identifies a device, the stretches of the networks'
  addresses that no device owns) the impairment family's `Table` gives the winner per piece of the
  destination and port space. MTU, DNS, TLS, DHCP and tunnel faults are not compiled here, and the access rules have a compile step of their own (`access.go`, "Access rules (M9)"); a
  fault that names a hostname is not in the tables either: since M20 its rules stand in front of the lookup levels (`hostnames.go`, "DNS faults and hostname selectors (M20)").
- **Fault ids.** One id (12 bits, 1 to 4095; 0 is "no fault") per winning fault, per matched device
  when the fault has a rate, an explicit queue limit or keep order in either direction (D18); the
  addresses no device owns share one id per winning fault (the devices in between cut those addresses into
  several stretches and a fault can name several scopes that reach them, so "one queue per scope" is "one per
  winning fault"; decided by the maintainer on 2026-10-11, P2-M8a-03). The key is
  `layer:overlay-or-fault-uuid:family[@device]`. A key that had an id keeps it
  (`Input.FaultIDs`), a new key takes the lowest id the previous allocation did not use at all (a
  released id is not handed out again while packets queued under it may still be in flight; the
  make-before-break of M8b relies on old and new ids differing), and only when none is left a released
  one. A fault that impairs nothing (`latency: 0ms`) still wins and shadows the less specific faults:
  its elements go to the chain `mark_0`, which clears the id; it has no class.
- **Classification maps.** The four maps of M7 are `flags interval` maps: an element is a range of
  source addresses, a range of destinations, a protocol and a range of ports, disjoint from every
  other element of its map, because the compiler splits overlapping selectors (`Table`) and the
  sources' claims (`partitionSources`: a device's own address, then the smaller range, then the
  stretch). Elements are written in the form nft prints them (an address, a prefix when a range is
  exactly one, `first-last`, `tcp`/`udp`/`icmp`, ports as `n` or `first-last`), so verify can compare
  them with `nft -j list`; sources with the same entries and touching addresses become one element.
  A hostname, `mtu`, `dns` ... is not an element. `TestTheCompiledLookupGivesTheResolvedWinner`
  compares the whole path (Resolve, ids, elements, the first-match lookup) for random worlds
  (75,600 lookups per run); `TestNoTwoElementsOfAClassificationMapOverlap` checks the elements.
- **Mark chains and counters.** `mark_<id>` writes the id (mask `0xffff000f`), counts the packet in
  the named counters `fault_<hash of key>_up` and `_down` (by `ct direction`) and ends. The counters
  are named by the fault's key, not by its id, so they survive every apply and every renumbering and
  are deleted with the fault (plan §3.2).
- **The tc tree** (`compiler/tc.go`, `compiler/netem.go`), identical on every interface classified
  traffic leaves through (the bridges, the WireGuard interfaces, the uplink, `svc0`'s host side): an
  HTB root `1:` with default class `1:1`, per active (id, direction) a class `1:<0x10+2·id+dir>`
  (`rate 10gbit quantum 60000`: HTB only classifies; limits are netem's), a netem leaf whose
  handle is the class's minor, and an `fw` filter `handle 0x000a0/0x1fff0` (id 10 upload,
  `0x100a0/0x1fff0` download; `protocol ip prio 1`, `flowid` the class). A direction the fault does
  not impair has no class and meets the default one. The netem configuration is always complete:
  `limit`, `delay D J 0%`, `distribution` (only with a jitter), `loss random P C` or
  `loss gemodel p r 1-h 1-k`, `reorder P 0%`, `duplicate 0%` (since M10 always 0: the copy is not netem's), `corrupt P 0%`, `rate R` with `0bit`
  for none. Blackout is `loss 100%`, flapping compiles the phase the engine holds (`Input.FlapPhase`) and `Netem.Down()` is the blackout
  (the engine toggles it, "Extended faults (M10)"), `keep_order` is a rate (the fault's, else 1 Gbit/s). The queue limit is the explicit one,
  or delay+jitter × rate / 1500 bytes (rate: the fault's, else 1 Gbit/s), at least 1000 and at most the
  class's share of the interface's memory budget. The three numbers are assumptions plan §2.5 does not give
  (it names neither a packet size nor a budget, and the host description has no link speed): a packet of 1500
  bytes (an Ethernet MTU), a budget of 256 MiB per interface (`Input.QueueBudget`) shared by the active classes
  of the interface, and never below netem's own 1000 packets (so a tree of thousands of classes keeps 1000 each and
  may exceed the budget; the class limit bounds the memory then); an explicit `queue_limit` is never changed. The
  rate of a fault without one is the 1 Gbit/s cap itself, `keep_order` without a rate uses the same 1 Gbit/s as
  netem's `rate`, and the link speed from `ethtool` (and 100 Mbit/s links) stays a later refinement. Decided by the
  maintainer on 2026-10-11 (P2-M8a-02).
- **What the VM proved first** (kernel 6.8.0-142, iproute2 6.19, nftables 1.1.6), before any compiler code
  was written around it: an HTB root cannot be replaced or changed once it exists (`Change
  operation not supported by specified qdisc`, so the root entry is `add` and only when it is missing
  — `TCTarget.Entries(dev, withRoot)`), while classes, netem leaves and `fw` filters accept `replace`
  repeatedly; a netem change keeps the correlations it is not given (`loss random 100%` after a 25%
  correlation stays 25%) and the loss model, so every correlation is written, and `loss random 0% 0%`
  does clear a gemodel; `reorder 0%` without a delay is fine, `reorder 25%` without one is refused;
  `distribution` without a jitter is refused (`distribution specified but no latency and jitter
  values`, found by the tc gate, so the compiler drops it) and `uniform` has no table (a leaf that goes back to uniform is made again, see "The tc tree in the apply");
  `htb rate 10gbit` without a `quantum` warns "quantum of class ... is big"; interval maps with
  concatenated ranges, prefixes, protocols and port ranges are accepted by the kernel, and nft
  prints an aligned range as a prefix, a one-address range as the address and `53-53` as `53`.
- **Capacity.** More fault ids than 4095, or more classes than `Input.ClassLimit` (default 1000 on
  x86, 200 on ARM64, counting the default class: a tree of 999 fault classes fits the default limit of 1000), is a
  `capacity_exceeded` problem (an error: the target is not applied). Every interface carries the same tree (the direction bit
  makes (id, direction) to class the same on all of them), so one limit counts the classes of the tree for all
  interfaces alike; a count per interface would need trees that differ between interfaces. The problem names the scope of the
  biggest cause (`Problem.Scope`, "network IoT") and lists the overlays or faults that contribute (`Problem.Faults`, the
  biggest first), so the API can name the overlay that tipped the balance. Decided by the maintainer on 2026-10-11 (P2-M8a-04).
- **Incremental identity updates** (`engine/applyloop.go`). The classification maps are keyed by
  address, so a device's new address changes their elements too. `identityOps` diffs every map
  (`compiler.DiffMap`), applies the identity map as before and the classification maps in one atomic
  nft transaction (`Nft.ElementTransaction`: deletes first, then adds; an interval map takes no element
  that overlaps one still there), and takes the full apply as soon as anything but map elements
  differs (a fault that came or went: another id, chain, counter or class). `apply.VerifyMaps` checks
  all maps afterwards. P2-M7-02 records the decision to keep the address keys, with the churn figure; decided by
  the maintainer on 2026-10-11: the classification maps stay keyed by address, without the device-number indirection.
- **Kernel gates.** The fault scenarios (`faults-mixed`, `faults-nested`, `faults-neutral`) are in
  `transactionScenarios`, so `TestEveryCompiledRulesetIsAcceptedByTheKernel` runs them through
  `nft -c`; `TestEveryCompiledTCTreeIsAcceptedByTheKernel` runs the executor's own `tc -batch`
  lines for each tree twice on dummy interfaces and reads qdiscs and filters back
  (`make vm-test ARGS='-run TestEveryCompiled -tags testbed ./internal/compiler'`). Both belong in
  front of any change to the compiler's nft or tc output.
- **M7's tests** use real faults now (`TestClassifyIDs` is gone). New: the more specific level wins
  when two levels hold entries (`TestTheMoreSpecificLevelWinsWhenTwoLevelsHoldEntries`) and a
  reordered chain is caught (`TestAReorderedLookupChainIsCaught`, on the kernel, and the structural
  `TestAReorderedLookupChainIsNotTheLookupChain`). The executor accepts verdict elements only for
  `mark_0` to `mark_4095` (`TestDecodeRejects`), range keys and `0xa0/0x1fff0`-style filter
  handles.
- **Applying the tree** is `apply.Apply`'s job since M8b (in-place updates, make-before-break, verify:
  "The tc tree in the apply" below). The engine, API and `explain` side of M8a follows below.

### Overlays in the engine and over the API (M8a)

- **Commands** (`internal/engine/overlays.go`). `PutOverlay`, `DeleteOverlay`, `RenewOverlay`,
  `ResetOverlays` and the expirer's `cmdOverlayExpire` go to the state owner, which alone writes the
  `overlay.Store`. A write is validated there (`domain.ValidateOverlay` against the configuration the
  kernel runs, the discovered devices count as known), the store's content is compiled once to see
  whether the target still fits (`capacity_exceeded`, `fault_invalid`: the write is refused and the
  store goes back to its checkpoint, nothing else is affected), and then it makes a generation and a
  desired state that carries the overlays (`desired.Overlays`; the apply loop hands them to the
  compiler together with the fault ids of its last verified target, so ids stay stable).
  Overlays of kinds whose milestone is not in the build (`CheckOverlaySupported`; none is left: the kind `dns` has been supported since M20, the kind `tls` since M21, with its case `intercept` since M22, the kind `dhcp` since M23) would be refused with `unsupported_feature` before they reach the owner. The
  kind `rule` has been supported since M9 (see "Access rules (M9)"), the WireGuard action and the
  mtu and tunnel families since M10 (see "Extended faults (M10)"), the kind `profile` since M11 (see
  "Profiles (M11)").
- **Verify and take back.** The writer is answered when the apply loop has verified a generation
  that is at least the one of its change, with that generation (`OverlayResult.Generation`: it can be
  newer than the one the change made, when later changes were applied together with it). The owner
  keeps a store checkpoint per unconfirmed change and the last verified one; when an apply that
  contains an unconfirmed change fails, the store is restored to the verified checkpoint, every
  waiting writer gets `ErrApplyFailed` (`apply_failed` over the API), and a restore desired state is
  made, which supersedes everything converged since. The events of a change (`overlay_created`,
  `overlay_updated` with reason `replaced` or `moved`, `overlay_removed` with reason `deleted` or
  `reset`, `overlay_expired` with reason `ttl` or `lease`, `overlay_orphaned`, with reason `rollback`
  after a commit-confirm rollback) go out when the change
  is verified, with the generation it made, the actor and the subject; a change that is taken back
  never announced itself. Renewing a lease moves a deadline only: no generation, no event, no apply.
- **Expiry** is the `overlay.Expirer` on the engine's injected clock: it arms one timer for the next
  deadline, re-armed after every change and renewal; a renewal is also written into every
  checkpoint the store can still be restored to (`Store.Renew(id, checkpoints...)`), so a failed
  apply or a refused write of somebody else does not take it back; when it fires, `cmdOverlayExpire` makes the
  owner call `Store.Expire`. The tests move the fake clock (`TestTheTTLRemovesAnOverlayAndAnEventSaysSo`,
  `TestALeaseNeedsRenewingAndRunsOnTheMonotonicClock`, which also jumps the wall clock).
- **Restart.** The store lives in the owner and nowhere else, so a new engine starts without
  overlays and recompiles the kernel from the committed revision and the observed state
  (`TestARestartDropsTheOverlays`, `TestARestartOfTheAPIDropsTheOverlays`).
- **Snapshot.** `Snapshot.Overlays` (what the desired state holds, oldest first), `Faults`, `FaultIDs`,
  `Winners` (which faults win for some traffic), `Coverage` (how much of its traffic each impairment and MTU fault
  wins) and `FaultEpochs` (the generation in which a fault first
  appeared in an applied target: the epoch of its counters, which restart when the fault is new)
  come from the last applied target; the API reads the snapshot and, for the counters, the named nft
  counters through one executor read per response (`Engine.ReadCounters`).
- **API** (`internal/api/overlays.go`). `POST /overlays` answers 201 with `Location`, or 200 when the
  key existed (the overlay keeps its id), both with `Chaos-Generation`; `GET /overlays` filters by
  kind, owner (`self` or an owner id), device (overlays whose target contains it) and network;
  `DELETE` and `renew` are limited to the caller's own overlays for a token with the scope
  `overlays`, the scope `full` and the admin may touch every overlay; `POST /reset` removes the
  caller's overlays, `?owner=all` needs the scope `full` (403 otherwise) and reports
  `aborted_runs: 0` until M15. The owner is the token itself, or the admin user for what the UI does
  (plan §2.15); runs become owners with M15. Create, replace, delete and reset are audited
  (`overlay.create`, `overlay.replace`, `overlay.delete`, `overlay.reset`, `overlay.apply_failed`);
  a renewal is a heartbeat and is not. An overlay's `state` says how much of the traffic it selects it
  wins over every source of its scope (`overlayContext.effectOf`, from `Snapshot.Coverage`, P2-M8a-06):
  `effective` all of it, `partially_overridden` a part (a device fault took one device of its network, a
  fault took one of its ports), `overridden` none. A fault of a family without a classification table
  (tunnel), or one that names a hostname, is `effective` when it is a winner at all (`Snapshot.Winners`).
  Its `counters` are the sum of the named counters of its faults.
  `GET /faults` and `/faults/{id}` show the configured faults with their state (the same rule; `disabled`
  for `enabled: false`), counters and, for an overridden or partially overridden one, the faults that
  beat it (the winners over the devices of its scope at a destination and port it selects); a revision
  other than the active one shows the configuration alone. `explain` names the winner of one traffic
  tuple and agrees with the state: it is computed by the same resolution
  (`TestAFaultThatWinsOnlyAPartOfItsTrafficIsPartiallyOverridden` over the API,
  `TestCoverageTellsAFaultThatWinsAllSomeOrNoneOfItsTraffic` in the compiler).
  `GET /capabilities` lists the overlay kinds and fault families of the build (see the milestone sections),
  and its `limits` the two capacity limits of plan §3.3: `fault_ids` (4095, the mark layout) and
  `class_limit_per_interface` (`Engine.ClassLimit`: the setting `settings.class_limit_per_interface` of the
  applied configuration, else the engine's configured limit, else the default of the architecture, 1000 on
  x86-64 and 200 on arm64). `TestSystemEndpoints` checks both, also with a configured limit (P2-AUDIT-15).
  Every compile takes the setting of the configuration it compiles (`Engine.classLimitOf`), so a preview
  counts a candidate's classes against the candidate's own limit; `engine.Config.ClassLimit` has no
  production flag and is the tests' default (`TestTheClassLimitOfTheConfigurationIsTheOneEnforced`,
  P2-AUDIT-17).
- **`explain`** (`Engine.Explain`, `GET /explain`). It resolves one traffic tuple over the snapshot of
  the moment: the source (a device by name or UUID, configured or discovered, or an address that the
  identity maps to a device), the access verdict (`domain.World.AccessDecision` since M9: the
  control plane, the access rules, then the gateway's protection or the matrix; before M9 only the matrix and the
  protection, P2-M8a-07), the
  winner and the overridden candidates of every family that has candidates (`domain.World.Resolve`),
  the fault id and the two marks the compiler gave the impairment winner, `dns_proxy` for a query to
  the gateway's own address on port 53, and the route. The route is the kernel's answer to
  `ip route get DST from SRC iif IF` (the executor read `route_get`: plain IPv4 arguments only, the
  interface is the one the source's network arrives on), so the policy rules and the tables they
  select are evaluated by the kernel and not re-implemented; `table` is 100 for traffic from a test
  network, and "no route" is an answer (`unreachable`, with the kernel's message), not an error. A
  hostname destination has no address to look up: no route (and no kernel section). Since M20 `explain` judges
  the rules and the DNS faults that name it, and a destination that is an address takes the names the device got for it.
- **A data race this found.** `domain.IsNormalized` (every compile calls it through `NewWorld`) stored
  each map entry back into the configuration it only looked at; with the state owner validating an
  overlay while the apply loop compiled, `-race` reported it. A read-only visit does not write any more
  (`TestIsNormalizedAndTheWorldOnlyReadTheConfiguration`).
- **What applying a revision does to overlays** (`internal/engine/overlays_revision.go`). Overlays are
  not part of a revision, so the state owner decides it in the step that starts the apply
  (`startApply`), against the committed configuration and the discovered devices of the identity.
  A revision that deletes an object an active overlay refers to is refused with
  `*ErrOverlaysOrphaned` (`validation_failed` with `references[]`, one entry per overlay and deleted
  object: `kind: overlay`, `id`, `owner`, `object` as the JSON pointer in the active configuration);
  nothing changes and the candidate stays. With `ApplyOptions.Force` (`?force=true`) the overlays are
  removed in the same desired state the revision makes, the answer lists them (`removed_overlays`,
  `Applied.RemovedOverlays`) and each announces `overlay_orphaned` (with the deleted `objects`) once
  that generation is verified. A merge (`domain.DiscoveredMerges`: a configured device now owns the
  MAC or address of a discovered one) moves the overlays of the discovered device to the configured
  one (`Store.Retarget`: same id, same age, `overlay_updated` with reason `moved`; two overlays that
  then share a key leave the newer one, the other is `overlay_removed` with reason `merged`). A merge
  orphans nothing, so it needs no force. Both go through the overlay marks, so an apply that fails
  takes them back with the other unconfirmed overlay changes: the orphans return, the moves are
  undone. `Preview` reports the same `references` and compiles the target without the orphans and
  with the moved targets, so a refusal is never a compile error of the overlay that the revision
  orphans. Runs are not part of this yet (`aborted_runs` follows with M15). Tests:
  `internal/engine/overlays_revision_test.go`, `TestARevisionThatDeletesAReferencedObjectIsRefusedUnlessForced`
  in `internal/api/overlays_test.go`.
- **What a commit-confirm rollback does to overlays** (P2-AUDIT-05, `owner.rollback`,
  `rollbackOverlays` in `internal/engine/overlays_revision.go`). In the confirmation window overlays
  are validated against the pending revision, which the kernel runs, so they can name what only that
  revision has. A rollback (the confirmation timeout, an expired confirm, `Engine.Rollback`) settles
  the overlays for the way back, in the desired state that restores the committed revision: the
  overlays the pending revision's merge moved go back to the discovered device they were written for
  (`startApply` keeps each move, overlay id with the device it came from, in the pending state;
  `Store.Move`, `overlay_updated` with reason `moved`; an overlay that is gone by then is left
  alone), then the
  overlays that refer to an object of the pending configuration the committed one lacks are removed
  (`domain.OrphanedOverlays(pending, committed, …)`, `overlay_orphaned` with reason `rollback` and the
  `objects`). That covers an overlay written in the window for a device, network, group or custom
  profile the revision added, also for the configured device a merge moved overlays to when the
  committed configuration does not have it. The discovered devices the rule spares are those the
  tracker has seen, resolved against the committed configuration, so a device the pending revision
  adopted is discovered again and keeps its overlays. An activation of a profile that the committed
  configuration gives a dns or tls part is removed the same way (P2-M11-05: a rollback cannot be
  refused). A rollback needs no force and is never refused; the actor of its events is `system`.
  Overlays a forced revision removed and those a merge replaced (`merged`) stay removed: their removal
  was announced. Unlike a revision's own overlay changes, a rollback's are not taken back when an apply
  fails, since the revision stays rolled back whatever the kernel does: they are made in every store
  checkpoint the owner can restore (`Store.Amend`); when the apply fails and the store goes back to
  the last verified content, what the rollback did to that content is announced instead
  (`overlayMark.kept`), and the retry applies the restore without the orphans. What the rollback did is
  kept per checkpoint (the verified content and the content after each unconfirmed overlay change): an
  overlay write that was in flight when the rollback came can be verified before the rollback's apply
  fails, its events announce its own content, and what the rollback did to that content is what is
  announced then (a removal is not announced twice, an overlay written then and removed by the
  rollback has its removal announced). The rollback also triggers an observation, so the identity follows the committed configuration.
  Tests: `TestARollbackOrphansTheOverlaysOfADeviceOnlyThePendingRevisionHad`,
  `TestARollbackMovesTheOverlaysOfARolledBackMergeBackToTheDiscoveredDevice`,
  `TestARollbackWhoseApplyFailsStillLeavesTheOrphanRemoved`,
  `TestARemovalVerifiedBeforeAFailedRollbackIsAnnouncedOnce`,
  `TestAWriteVerifiedBeforeAFailedRollbackHasItsRemovalAnnounced` (`overlays_revision_test.go`),
  `TestARollbackThatGivesAnActivatedProfileBackItsDNSPartRemovesTheActivation` (`profiles_test.go`),
  `TestAnUnconfirmedRevisionTakesItsOverlayChangesBackWithIt` (`internal/api/overlays_test.go`).
- **Not in M8a:** applying the tc tree to the kernel (`apply.Apply` did not apply or verify `Target.TC`; the testbed tests installed it with the executor), in-place tc updates, make-before-break when a fault's id changes, and measurement tests of the impaired traffic. M8b does them: the reader, the normalizer and the executor's tc operations are under "tc state and tc operations (M8b)", the apply under "The tc tree in the apply (M8b)".

## tc state and tc operations (M8b)

The first step of M8b, the one every later step stands on: reading the kernel's tc state in a form
that can be compared with the compiler's tree, and the executor operations that change it. Applying
the tree (`apply.Apply`) and make-before-break are described in "The tc tree in the apply (M8b)"
below; the queue statistics follow in a later step.

### The `tc -j` normalizer

`linux.NormalizeTC(dev, qdiscs, classes, filters)` (`internal/linux/tcnorm.go`) turns the three
listings of one interface into a `linux.NormTree`; the executor's read `tc` (`ReadTC`, `Dev`
required) runs `tc -s -j qdisc|filter show dev X` and `tc -s -d -j class show dev X` (the class
listing with details, for the quantum) and returns it. What it does, and why each
step was needed (all of it read from real output, recorded by `internal/linux/testdata/tc/record.sh`
on kernel 6.8.0-142 with iproute2 6.19.0 into the fixtures next to it):

- **One identity per object.** A qdisc is `handle` + `parent` (`root`, `ingress`, `clsact` or a class),
  a class its id with the parent qdisc or class, a filter its parent, priority and selector. The tool
  prints no `dev` when it is asked for one interface (the caller names it; with all interfaces the
  entries of the other ones are dropped), a class under the root as `"root":true` without a parent,
  a class's `leaf` as `"0x24"` (older versions: `"24:"`), the classid of a filter as `classid` and
  the selector of an `fw` filter as `{"fw":{"mark":"0xa0","mask":"0x1fff0"}}` (older: `handle`), and
  a bare header entry before each filter. The order is not stable (classes come out in hash order);
  the normalizer sorts by number (`1:5` before `1:24`).
- **The values the kernel reports.** Probabilities are 32-bit fractions in the kernel and `%g`
  floats with six significant digits in the listing: 99.99999 % reads back as `1`, 0.0001 % as
  `1.00001e-06`, 33.333333 % as `0.333333`. Delays are seconds with the same six digits
  (`12.345678 s` prints `12.3457`), rates bytes per second (`7bit` is no rate). `NetemProb`,
  `NetemTime` and `NetemRate` compute what the listing prints for a request, so that the compiler's
  side (`compiler.TCTarget.Norm`, `compiler.Netem.Norm`) and the kernel's side meet. Both sides are
  compared as `float64` after the same rounding; nothing is parsed back into percents.
  `TestTheNormOfEveryNetemShapeIsWhatTheKernelReports` applies 21 shapes (the edge values above,
  every distribution, gemodel, rate truncation, a 12 s delay) to a real kernel, then changes the same
  leaves in place to each other's shape and to the neutral set, and requires the normalized state to
  equal the prediction every time; `TestEveryCompiledTCTreeIsAcceptedByTheKernel` does the same for
  every compiled scenario.
- **Configuration apart from state.** `NormTree.Spec()` drops what is not configuration: counters
  (`Stats`, only with `-s`), the netem `seed`, the `burst`/`cburst` the kernel computes from the rate,
  and the HTB root's `r2q` and `direct_qlen` (the kernel's defaults, the latter the interface's
  queue length). `DiffTC(want, have)` compares Specs and lists missing, unexpected and different
  objects, one line each. `Subtree("1:")` is the own tree: the root `1:` and everything below it;
  the host's `mq` with its queues, a `noqueue` and the `ingress` qdisc are not part of it.
- **The seed is the identity of a netem instance on some kernels only.** The kernel draws a random
  seed when the qdisc is created. Kernels 6.8 and 7.0 keep it through every `change` and `replace`
  (fifteen listings of one qdisc between as many changes: one seed; two creations of the same handles:
  two seeds), kernel 6.17 (the hosted runner, `6.17.0-1022-azure`, seen in CI) draws a new one at
  every change although the queue and the counters stay. So the seed is not compared, a different
  seed does not say that the qdisc was created again, and the tests tell a re-created qdisc by its
  counters, which start at zero (the packets it has sent are the packets sent since its creation).
  A *created* qdisc has another seed than the one it replaces on every kernel. iproute2 versions before
  6.x do not print it (`Seed` is then 0).
- **Nothing is dropped silently.** A netem option the code does not know (`slot` distribution,
  `loss state`, whatever a later iproute2 adds) ends in `Extra` as canonical JSON, a filter of another
  kind keeps its options canonical; a listing that does not parse or whose handle is not hex is an
  error. `FuzzNormalizeTC` runs the normalizer on arbitrary bytes.
- **The quantum.** The kernel reports an HTB class's quantum, but tc prints it only in the listing
  with details (`-d`), and only for a class without children; that is why the executor's class
  listing has `-d` (P2-M8b-02). The details add the class's `level` and what the tool derives from the
  rate (`burst_cell`, `cburst_cell`) and print the link layer and the minimum packet units even at
  their defaults (`linklayer` ethernet, `mpu_rate`/`mpu_ceil` 0); the normalizer drops those (a
  value other than the default stays in `Extra`), so a class reads the same with and without details,
  plus `NormClass.Quantum`, which is part of the class's configuration (`Line`). The compiler writes
  `TCClassQuantum` (60000 bytes) at every class it creates or writes again and predicts it in
  `TCTarget.Norm`; a quantum changed from outside is a difference verify reports and the next apply
  writes the class again in place, its leaf and counters stay
  (`TestTheDetailsOfAClassListingShowTheQuantum` on the recordings `class_details_*.json`,
  `TestAQuantumChangedFromOutsideIsFoundAndRepaired`, on the real kernel
  `TestTheRealKernelShowsAChangedQuantumAndTheApplyRepairsIt`). Measured on 6.8.0-142 (emulated VM)
  and 6.17.0-1022-azure (the level 1 runner), both with iproute2 6.19.0: `-d` prints `"quantum":60000`
  for a class given 60000, the value a `class change ... quantum 1514` gave it afterwards, and the
  kernel's own 200000 for a class written without one.
- **What the listing cannot show.** The netem distribution table is not printed at all (P2-M8b-02;
  the apply's memory of the tables is in "The tc tree in the apply"). The `-s` counters of the three listings come from three tool runs and are
  not one instant.

Counters, as the recordings show them (`NormStats`): a netem qdisc's `packets` and `bytes` count what
left it, so a packet that waits for its delay is not in them but in `backlog`/`qlen`; `drops` counts
everything the qdisc dropped, the loss it is configured to produce and the packets that did not fit
its limit alike (`overlimits` stays 0: 25 packets into a limit of 10 are `drops 25`); the HTB root's
and classes' counters count a packet when it leaves the class, that is after the delay of its netem leaf
(a few packets into a class with a 3 s delay: 0 counted right away, all of them after 4 s, on 7.0; so a class that traffic was
moved away from keeps counting for the delay of its leaf, and the faults' nft counters, which count when
the packet is classified, are the ones that say where a packet went at once). A class's leaf is not in its counters
(`lended`, `borrowed`, `tokens` are HTB's own).

### tc operations on the kernel

What the kernel does with the operations the fault engine will use, measured in the persistent VM
before the code was written and pinned by tests (`internal/executor/tcops_integration_test.go`, tag
`testbed`; run them with `make vm-test ARGS='-run "TestAChangeKeeps|TestReplacingALeaf|TestDeleting|TestDeleteOrder|TestTheRootCannot|TestADistribution" -tags testbed ./internal/executor'`).

| operation | result |
|---|---|
| `qdisc change/replace` of a netem leaf that exists, same kind | in place: queue, counters and the release time of the packets already queued stay (and the seed on kernels 6.8 and 7.0; 6.17 draws a new one at every change). 30 packets waiting in a 4 s queue stayed 30 through a change to 100 ms and left at the old time. A lower `limit` than the queue holds drops nothing queued; the packets that arrive over the limit are the qdisc's `drops`. |
| ... attributes it is not given | `change` sends netem's base structure every time and resets what is not given: `limit` (to 1000), `delay` and `jitter`, `loss` and `duplicate` (the probabilities) and the reorder `gap` (so a `reorder` that stays has gap 0 and does nothing). The optional attributes stay unless they are given: the **correlations** (a delay's, a loss's, a duplicate's: `loss random 1%` after a 50 % correlation is 1 % at 50 %), `reorder`, `corrupt`, `rate` (with its overheads), a loss model and the **distribution table**. The listing hides the correlation of a loss or a duplicate while its probability is 0. A loss model is replaced only by a `loss` that names the other one. Only the compiler's complete parameter set resets everything, including a gemodel (`TestAChangeKeepsWhatItIsNotGivenForTheOptionalAttributes`, one attribute at a time). |
| `qdisc replace` under the handle of a leaf with another kind (`pfifo`) | refused: `Invalid qdisc name`; the leaf and its queue are untouched. Another kind needs a delete and an add (or a new handle). The compiler's leaves are always netem, so this does not occur in the tree. |
| `qdisc delete` of a leaf, then add | the queued packets are gone with it (they appear in no counter), the new qdisc starts at zero with a new seed. This is why a delayed delete after "largest delay + 1 s" exists in plan §3.2. |
| distribution | a `change` or `replace` without `distribution` keeps the old table; `distribution pareto` swaps it; only a created qdisc is uniform. |
| `class replace/change` of an HTB class with identical or changed parameters | in place: the leaf, the queue and the counters stay. |
| `class delete` while a filter selects it | refused: `HTB class in use`. Delete the filter first. The leaf qdisc goes with the class. |
| `filter delete` | needs `parent`, `handle` (the selector with its mask), `protocol P prio N fw`; deleting the last filter removes the chain. |
| HTB root: `change`, `replace` (identical or not) or `add` when it exists | `Change operation not supported by specified qdisc` / `Exclusivity flag on`: the root is created once and left alone. |
| root `add` over a root the host put there (mq, fq_codel) | refused: `NLM_F_REPLACE needed to override`. `replace` takes the root over (with the host's queues below it). `qdisc delete ... root handle 1:` gives the default qdisc back. **The apply uses `replace`, not `add`, when the interface's root is not ours** (`add` is only right on a `noqueue`, which the compiler's `Entries(dev, withRoot)` assumes; proven on a bridge, a dummy and an interface with Chaos Gateway's own root). |
| `delete` of what is not there | exit 2 and one of: `RTNETLINK answers: No such file or directory`, `Error: Specified class not found.`, `Error: Failed to find qdisc with specified handle.`, `Error: Failed to find qdisc with specified classid.` (a leaf below a class that is gone; kernels 6.17 and 7.0, not seen on 6.8), `Error: Specified filter handle not found.`, `Error: Cannot find specified filter chain.`, `Error: Parent Qdisc doesn't exists.`, `Error: Invalid handle.` (also the answer for a root with a handle that is not there) and, for the default qdisc, `Cannot delete qdisc with handle of zero`, which the executor never sends. All but the last are "benign" in a deletion step (below). |
| a duplicating netem with any other netem on the interface | refused in either order: `netem: cannot mix duplicating netems with other netems in tree` (P2-M8b-01). The compiler never writes one since M10: every leaf says `duplicate 0%` and the copy is made by an egress hook (P2-M10-01, "Extended faults (M10)"). |

**Executor operations.** The operation is still `tc` with `TCEntry` entries (`object` qdisc | class |
filter, `action` add | replace | change | delete), now closed in two more ways (`tcgrammar.go`):

- *Own handles.* A root qdisc has the handle `1:` (also for `delete`), a class `1:<minor>` below `1:` or
  another class, a qdisc below a class `<minor>:` (its handle is the minor of its parent), a filter hangs
  below `1:` and may select only a class of the tree, plus `ingress`/`clsact` and the filters of `ffff:`
  as before. An entry that names the host's `mq` (`8001:`), the handle zero or another tree is refused
  by the decoder, on any assigned interface including the uplink, so a scope that is right for the
  interface cannot reach what the operating system put there.
- *Grammar of the kinds the compiler emits.* `netem` takes `limit`, `delay`, `distribution
  normal|pareto|paretonormal` (a file name is not a table: `distribution` is an enumeration),
  `loss [random|gemodel]`, `reorder`, `gap`, `duplicate`, `corrupt`, `rate`, `seed`, `ecn`, each at most once, with
  values of the right shape (durations with a unit, percentages up to 100, rates with a unit); `htb`
  qdisc `default`, `r2q`, `direct_qlen`; `htb` class `rate` (required), `ceil`, `burst`, `cburst`,
  `prio`, `quantum`; an `fw` filter is `protocol P prio N fw [flowid C]` with a handle. Other kinds keep the
  token rules. A deletion takes no arguments (a filter: `protocol P prio N fw|u32`).
- *Deletions are idempotent.* The plan puts a run of deletions into a step of its own, `tc -force
  -batch -`, marked idempotent: with `-force` one "not there" answer does not keep the lines behind it
  from running, and a step whose every error line is one of the answers listed above succeeds; any other
  answer (`HTB class in use` first of all) fails the operation. The other entries are one `tc -batch -`
  step as before and stop at the first failure.

Rejection cases are in `tcops_test.go` (`TestTCRejectsWhatLeavesTheOwnTree`, over 60 cases) and
`decode_test.go`; the fuzz targets check that every tc line of an accepted operation stays in the own
handles (`checkTCLineInScope`), and have seeds with a complete netem set, an in-place change and a run of
deletions.

### The tc tree in the apply (M8b)

`apply.Apply` carries `Target.TC` into the kernel and verifies it like the rest of the state.
Everything below is in `internal/apply` (`tcplan.go`, `retire.go`) and `internal/engine/applyloop.go`.

- **Reading.** `ReadState` lists the qdiscs of all interfaces in one tool run (`tc -j qdisc show`
  without a device, every entry names its interface; P2-M8b-04) and reads the whole tc state
  (`ReadTC`, with counters: three tool runs, four with an ingress qdisc) only of the interfaces that
  hold a root `1:` or an ingress qdisc among the ones the target names (`Target.TCCandidates`: the
  bridges, the WireGuard interfaces, the uplink, the service namespace's host side) and the assigned
  ones. `State.TC` maps an interface to its tree; an interface without one is not in it. The
  interfaces of the host's own (the management NIC, the ports) cost nothing beyond the one listing:
  an apply with ten assigned interfaces and the tree on three runs tc 2 × (1 + 3 × 3) = 20 times to
  read, before the plan and to verify, where a listing per interface made it 2 × (10 + 9) = 38
  (`TestAnApplyWithTenAssignedInterfacesListsTheQdiscsOnce`). No memory of the interfaces that held a
  tree at the last apply is kept: the listing tells it at the cost of one run, and it also finds a tree
  that a previous run of the gateway or someone else left on an assigned interface. `ReadHost` (the
  compiler's input, read after every burst of network events) reads only the links, addresses and
  routes, in one executor round trip, and no tc state at all.
- **The diff** (`planTC`) compares the target's normalized tree (`TCTarget.Norm`) with the interface's
  own subtree (`NormTree.Subtree("1:")`) object by object and plans per interface:
  the root (`qdisc replace ... root handle 1: htb default 1`, only when `1:` is not there: `replace`
  creates it and takes over the host's `noqueue` or `mq`; an HTB root is never changed), the default
  class, and per class of the target the class, the netem leaf and the `fw` filter, each written
  (`replace`) only when it is missing or its configuration differs. A class and a leaf that exist are
  changed in place, so their queue, their counters and their seed stay; the unchanged ones are not
  written at all, which is why a re-apply of an unchanged target runs no tc command
  (`TestAReApplyOfTheSameTargetTouchesNoTC`, and on the real kernel the seeds of all qdiscs stay).
  Damage that the kernel cannot repair in place is repaired first, by deleting and creating again: a
  root of another kind or default class, a class whose parent or kind is wrong or whose leaf is not
  the compiler's netem, a filter that is not the target's. These drop queued packets, but they are
  not objects a fault put there.
- **Order inside the apply.** Links, sysctls, offloads and routes as before, then the tc operation
  that creates and changes (on ALL interfaces, before anything classifies into a new class), then
  the nftables transaction, then (only without a retirer) the deletion of what the target no longer
  wants, then DOCKER-USER and the rest. The plan text (`Plan.Summary`, the preview) says
  `tc: br-iot: 6 objects created, 2 changed in place; ...`. The creations and changes are one
  executor operation per interface (and the deletions too), in the same order: an interface at the
  class limit has about three entries per class (3000 on x86-64), and the executor takes at most
  `executor.MaxTCEntries` (4096) in one operation, so all interfaces together would not fit
  (`TestATreeAtTheClassLimitIsAppliedOnEveryInterface`, which names the limit it assumes: 1000 on
  x86-64, 200 on arm64). `tcOps` also splits an interface's entries above the cap, keeping the order.
- **Make before break, the second half.** A class that no fault id uses any more (`TCStale`) is not
  deleted by the apply that stops classifying into it. `ApplyWith(..., retirer)` hands it to the
  `Retirer`, which deletes it (its filters first, then the class; the leaf goes with it) when
  `largest delay + jitter of any netem leaf in the kernel or in the target + 1 s` (`Plan.Grace`) have
  passed on the injected clock since it first saw the class. When no fault impairs anything the whole
  tree is stale: the root goes (and with it everything below), and the interface has its own queue
  again. `Retirer.Reap` looks at the leaf before it deletes: a class whose qdisc still has a backlog
  is looked at again every second, for at most five minutes (`retireBacklogCap`: a netem with a low
  rate can hold a queue for minutes; after that the class goes with its queue). A class that is
  already gone, or whose interface is gone or no longer assigned, is forgotten; a deletion that
  fails is retried every second for half an hour (`retireGiveUp`). `Apply` without a retirer (the one-shot
  `chaosgw apply`, tests) deletes the stale class right after the transaction. These limits are choices, not numbers of
  the plan (§3.2 says only "the largest configured delay plus 1 s"), and so is a consequence of the design: the classes of
  the old and the new ids exist side by side for that time, so an interface can carry up to twice the class limit for a
  moment (`Input.ClassLimit` counts the target's classes; the compiler does not count the ones that wait; a limit of half
  the number per interface would make 1000 on x86 effectively 500, should that be wanted). An interface that leaves Chaos
  Gateway's control (the uplink changes) loses its tree in the apply itself, with the packets queued in it, because nobody
  can delete anything on it later. Decided by the maintainer on 2026-10-11 (P2-M8b-03).
- **What the retirer remembers.** Only when it first saw each stale class, and which distribution
  table each leaf was last given (below). Every apply recomputes the stale set from the live tc
  state, so a class that a fault wants again drops out (nothing is deleted, its queue stays), an
  apply that failed half-way or a restore of the previous revision leaves nothing behind that the next
  apply does not find, and after a restart of the gateway the classes it finds stale get their time
  from the first apply: they go later than needed, never earlier. An apply that comes while classes
  wait does not move their time (`TestAnApplyBeforeTheDeletionFiresLeavesItsTimeAlone`); when the last
  fault went (the whole tree waits) and another comes, the old classes become stale one by one and keep
  the time of the tree they were in (`Retirer.treeWith`).
- **Fault ids.** `compiler.Input.RetiringIDs` (the engine passes `Retirer.IDs()`) keeps a new fault
  from taking an id whose class is still waiting, so old and new ids differ for as long as both are in
  the kernel, not only across one transition.
- **The distribution table** is not in the listing (P2-M8b-02), and a change that names none keeps the
  old table. The retirer therefore remembers the table it gave each leaf. A leaf that is to be uniform
  with a jitter (the only thing a table shapes) while it holds or may hold a table is made again
  (delete, create: a new seed, the queue is dropped); a leaf that is to get a table gets it
  in place. A leaf the retirer has no memory of (the first apply after a restart of the engine finds
  leaves it did not write) counts as one that may hold a table: a table that survived the restart, or
  that someone else put there before the start, is invisible in the listing. When such a leaf is
  already what the target says, the apply writes the table of one that is to have a table in place
  (the queue stays) and leaves one that is to be uniform alone: making every uniform leaf with a jitter
  again at every start would drop their queues for nothing, so a table left from before the start
  stays until the first change of that leaf, which makes the leaf again (P2-M8b-02,
  `TestAChangeToAUniformJitterMakesTheLeafAgainOnlyWhereATableMayBe`, on the real kernel
  `TestAfterARestartTheFirstChangeOfALeafDropsTheTableThePreviousRunLeft`); from then on the memory
  knows it. A leaf without a jitter is never made again for its table (a table shapes nothing there),
  and an apply without a retirer (`apply.Apply`, the CLI's one-shot apply, `apply.Preview`) has no
  memory and leaves every leaf that is what the target says alone. The memory keeps the tables of the
  leaves of the stale classes and trees as long as they wait for their deletion: a class that a fault
  wants again before that keeps its leaf, its queue and what is known of its table
  (`TestAnIdThatReturnsBeforeTheDeletionKeepsItsClass`, `TestAClassThatReturnsBeforeTheDeletionKeepsItsLeaf`).
  A table that someone else writes while the engine runs,
  into a leaf whose listing it leaves as it was, is not seen until the next change of that leaf. An apply that fails while it
  is carried out may have written a table that the memory would not know: the leaves its tc operations
  wrote are then remembered as holding an unknown table (`Retirer.failed`), so the restore of the
  previous revision makes a leaf that is to be uniform again even though the listing shows no
  difference (`TestAFailedApplyLeavesTheTablesUnknownSoTheRestoreMakesTheLeavesAgain`).
- **Verify.** `apply.Verify` compares every interface of the target and every interface with a
  tree: the wanted tree equals the observed own subtree (`linux.CompareTC`), an interface that is not
  to hold a tree holds none. What the apply left standing for the retirer (`State.TCRetiring`, set by
  `ApplyWith` from `Plan.Stale`; `Engine.RetiringTC()` for the tests of the engine) is accepted as
  unexpected, nothing else.
- **The engine** (`runApplyLoop`) owns the retirer, calls `ApplyWith` and arms a timer on the engine's
  clock for the next deletion (`Retirer.Next`); the timer's case in the loop's `select` runs
  `Reap` and arms the next. The preview (`Engine.Preview`) plans with the same retirer
  (`Retirer.BuildPlan`): stale classes stay for the grace period, and the leaves it announces to be
  made again are the ones the apply would make again. An overlay write is answered after the apply that contains it verified,
  and that verify includes the tc tree; a failing tc operation (`apply: execute`) takes the batch back
  like every other failure, and nothing of the failed plan is handed to the retirer.
- **The preview's tc diff** (`LinuxDiff.TC`, `linux.tc` of the revision preview, plan §2.14; P2-AUDIT-02)
  compares what the plan and the verify compare: the interfaces of `tcWanted` (the target's candidates and
  every interface with a tree of ours) and the ingress sides of `ingressSides`, the wanted tree against
  the own subtree the kernel lists, both as `NormTree.Spec().Lines()`. So seeds, counters, burst sizes
  and the HTB root's r2q and direct queue length never show, and a tree that verifies has an empty diff.
  Every line starts with the interface (`br-iot: qdisc 12: parent 1:12 netem limit=… delay=0.15 …`,
  `wan0 ingress: filter …`), so every hunk names it. A class the retirer still holds shows as going.
  Tests: `TestTheLinuxDiffShowsTheTCTreesAsTheApplyWouldChangeThem` and
  `TestTheLinuxDiffShowsTheIFBTreeAndTheIngressFiltersOfATunnelFault` (simulated kernel: a new fault, the
  applied target, a changed delay, a leaf changed by hand, the last fault going, a roaming peer),
  `TestThePreviewsTCDiffIsEmptyForTheKernelsTreeAndAChangeIsItsLeaves` (tag `testbed`: the real kernel's
  listing of an applied tree with loss, rate, corrupt, reorder and a queue limit has no diff, a change of
  parameters is its leaves only).
- **kernelsim** (`internal/apply/kernelsim/tc.go`) simulates the tc tool for the cases above: it keeps
  the root, classes, leaves and filters per interface, answers `-s -j qdisc|class|filter show` in the
  recorded format (so the real normalizer reads it), the class listing with details (`-d`: the quantum
  a class was given, or the kernel's own from the rate) and the qdisc listing of all interfaces
  (`tc -j qdisc show`, every entry with its `dev`), refuses what the kernel refuses (a second root,
  a class a filter selects, a class below a root that is not there), makes the answers of a
  `-force` batch the executor knows as benign, keeps the seed through a `replace` of a leaf and draws a
  new one for a new leaf. It keeps the distribution table a leaf was given (`TCTable`, which the
  listing does not show) through a change that names none, as the kernel does; `SetTCTable` gives a
  leaf a table behind the apply's back (one a previous run left). It does not queue
  packets (`SetTCStats` sets backlog and counters for the tests of the retirer) and does not keep the
  other attributes of a `change` that is not given (the compiler's sets are complete). `Kernel.SetAfter`
  sets a function that runs after each command, outside the lock, to change the kernel behind the caller's back
  (a drift that verify has to find).
- **Tests.** `tcapply_test.go` (simulated kernel: apply and verify on all interfaces, re-apply,
  in-place change, moved id with the order tc-before-nft, grace period and backlog guard, a whole tree
  that goes, restart, injected failure, repair, preview) and `internal/engine/tc_test.go` (the engine:
  the tree is in the kernel when the write returns, classes stay for `largest delay + 1 s` on the fake
  clock, a failed tc operation reverts the write). On the real kernel
  `tcapply_integration_test.go` (tag `testbed`: the verified tree, a re-apply that changes no seed,
  a 600 ms fault changed twice (to 900 ms and 50 ms) under load with every datagram of a numbered UDP
  stream delivered and no drop counted, a move of the fault id under load with the same conservation
  and every datagram counted by the old or the new class, the old classes' deletion, the tree that
  goes with the last fault, a normal distribution going back to uniform, a table that a previous run
  left behind dropped by the first change of the leaf after the restart, a quantum changed from outside found and
  written again). The load is a sender that
  runs until the test stops it, not a fixed-length ping: an apply takes minutes on the emulated
  kernel and the change has to happen while packets are queued: `make vm-test ARGS='-run "TestTheTreeOfAFault|TestChangingAFaultOf600ms|TestMovingADevice|TestWithoutFaultsTheTree|TestTheDistribution|TestAfterARestartTheFirstChange|TestTheRealKernelShowsAChangedQuantum|TestThePreviewsTCDiff" -tags testbed -test-timeout 30m ./internal/apply'`.

### Queue statistics and counter epochs (M8b)

Plan §2.12: "per netem queue: packets dropped and delayed ... a queue that is re-created starts a new
counter epoch". `internal/engine/queues.go`, `internal/api/overlays.go`.

- **A queue** is the netem leaf below the class of one (fault id, direction) on one interface. The
  statistics are the kernel's counters of that leaf, read when the API asks (`Engine.ReadQueues`: one
  `ReadTC` per interface of the tree, so three tool runs each; nothing is polled; P2-M8b-04 made the
  apply's own reads cheaper, not these): packets and bytes that left it, drops (the loss the fault configures and the packets that did
  not fit into the limit, both are `drops` on a netem leaf; `overlimits` stays 0), the backlog in
  packets and bytes. They are what `QueueStats` in the spec carries (`sent_bytes` and `backlog_bytes`
  were added in M8b) on `Overlay.queues` and on `FaultView.queues`, one entry per interface and
  direction the fault impairs, with `device` set for the queue of one device (D18).
- **The epoch** of a queue is the generation of the apply that made its leaf. What the kernel does
  decides (measured on 6.8.0-142): `tc qdisc replace` of a leaf of the same kind and `tc class
  replace/change` keep every counter, a deletion and a new leaf start them at zero. The plan therefore
  names the leaves it makes new (`apply.Plan.QueuesCreated`: a leaf that did not exist, a class made
  again after damage, a leaf deleted and made again because a distribution table has to go,
  P2-M8a-05) and the engine gives those the generation of the apply (`trackQueues`); a leaf that
  stays keeps its epoch through any change of the fault's parameters. A queue the engine has not seen
  before (the first apply after a restart finds leaves it did not make) gets the epoch of that apply, which
  is more than strictly needed and never less. After an apply that failed nothing is known about what it
  did to the leaves, so every queue that was there starts a new epoch. Two readings with the same epoch
  may be subtracted, readings with different ones must not be. A fault that is removed and written
  again has new queues (its old classes retire, a new id is taken) and so a new epoch.
- **The nft counters** (`Counter.epoch` of overlays and faults) are the generation in which the fault
  first appeared in an applied target (`trackFaults`), as before. The state's `counter_epoch` is the
  epoch of all of them together (`trackCounters`): the generation of the engine's first apply, and of
  every apply that finds the table of Chaos Gateway missing (`Plan.NftNew`: a reboot, `chaosgw teardown`),
  and then every fault counter starts a new epoch as well. A gateway that merely restarts cannot tell
  whether the counters it finds are the ones it left (it keeps no counter state, and the kernel shows no creation time), so
  its first apply starts new epochs for every queue and fault although the counters did survive; a consumer then discards
  one delta and never subtracts across a reset. The generation is persisted (`GenerationFile`), so the numbers never repeat.
  The plan (§2.12) says only that counters reset on reboot and that a re-created queue starts a new epoch; that an epoch is
  the generation of the apply that made the object, and that a restart starts a new one, is decided by the maintainer on
  2026-10-11 (P2-M8b-05). Epochs that survive a restart would need the engine to store the epoch of every queue and fault
  with the checkpoint and to compare the kernel's counters with the stored ones.
- **Tests.** `internal/engine/queues_test.go` (simulated kernel: the readings, the epoch through a
  change in place, a leaf made again, an update that leaves another fault's queue and epoch alone, a
  fault that comes back, a failed apply, the counter epoch and a restart), `internal/api`
  `TestAnOverlayAndAFaultShowTheirNetemQueuesWithEpochs` (the fields and their contract),
  `internal/apply` `TestThePlanNamesTheLeavesItMakesNewAndATableThatIsNew`, and on the real kernel
  `internal/engine/integration_queues_test.go` (`TestTheQueuesCountTheKernelsPacketsAndKeepOrRestartTheirEpoch`).
- **The queue limit** (P2-M8a-02): `TestTheComputedQueueLimitHoldsABurstThatTheDefaultLimitOfNetemDrops`
  sends a burst of 4000 echo requests back to back (`ping -l`) through a 600 ms fault. With the
  compiler's computed limit (44739 packets here: the 1 Gbit/s cap would need 50000, the budget share of
  four classes allows 44739) the upload and the download queue each carry all 4000 and drop none; with
  an explicit limit of 1000 the upload queue passes 2000 and drops 2000 (tail drop while the first 1000
  wait), and every packet is accounted for by the queues: upload sent + dropped = 4000, download sent +
  dropped = what left the upload queue. What `ping` itself receives is not a measure (a burst of replies
  overruns its socket buffer: 2000 to 2500 of 4000 on the emulated kernel with no drop in the queues). The
  outcome: without a queue limit the compiler's limit holds all of it with no drop in either direction, and with the
  limit of 1000 (netem's own default, which is also what a limit below it is raised to) the queue passes the first
  2000 and drops the other 2000 while the first wait. A 600 ms fault with the default limit is therefore a rate limit of
  about 1667 packets per second (about 20 Mbit/s of 1500-byte packets) that nobody configured, which is why the
  computed limit exists. The price is memory: a full queue of 44739 packets holds up to that many socket buffers (an
  estimate, not measured: about 2 KiB each with the packet itself, roughly 90 MB for one queue), and the 256 MiB budget of
  the interface is shared by its queues, so a burst through one queue is covered but many full queues are bounded by the
  budget, not by the delay. A packet size of 1500 bytes under-counts small packets (a queue of 64-byte packets holds more of
  them than the estimate), which only makes the limit larger in packets than needed. On the hosted nested-virtualisation runner (level 1b) about half of the
  replies of the 4000-packet burst vanish between the two queues without a drop in either and without a
  drop in the host's receive queues: the echo server's own ICMP output fails (`Icmp.OutErrors`) while the
  gateway forwards everything. The test accounts for exactly those (and for softnet drops) and fails on
  any packet that nothing counts, printing the counters of every namespace that moved (P2-M8b-07). Decided by the
  maintainer on 2026-10-11: accounting for the echo server's own output errors is the right test; the burst is not paced.

### Measurement tests of the fault engine (M8b)

Plan §4.3 in the code: `internal/testbed/probe.go` and `stats.go`, the tests in
`internal/engine/integration_faults_*_test.go`. Every test writes its faults through the engine's
overlay writes (`PutOverlay`), so the apply loop, the verify and the retirer are the product's.

- **One-way probes.** A ping gives the sum of the two directions, and the faults are per direction. A
  probe is a numbered UDP datagram with the sender's clock; the echo (`testbed.StartEcho`) answers with the
  upload delay it computed and its own clock, the sender computes the download delay (all namespaces of a
  bed share one machine and so one clock). The echo logs every datagram it gets, so the loss of each
  direction is counted exactly: `ProbeResult.UpLoss` is what the upload lost, `DownLoss` what the download
  lost of the datagrams that arrived. `Echo.Probe` sends a fixed number; `Echo.Begin` starts a run that goes
  on while the test changes faults and `Stop` ends it (the load of the tests below). A host behind a client
  (remote network) sends from its own address (`ProbeOptions.Src`).
- **Statistics.** `BinomialBounds` is the central 99.9 % interval of the count of lost packets (`CheckLoss`),
  `CheckLatency` the tolerance of ±2 ms + 5 %, `CheckSpread` compares the 5th to 95th percentile spread
  of the upload with that of a uniform jitter (1.8 x jitter; the plan gives no tolerance for the spread,
  the test's own is 0.5 to 1.5 times plus 2 ms, wide on purpose: with 200 to 2000 probes the percentiles are stable to a
  few percent, and the point is to tell a jitter from none and from a wrong one). An unaffected flow is "unchanged" when
  its median in each direction is within ±2 ms + 5 % of what the same flow measured before the fault and it lost
  nothing (plan §4.3 gives tolerances for the median, the loss and the rate only). Decided by the maintainer on 2026-10-11
  (P2-M8b-06); the spread bounds can be tightened if nightly measurements on KVM show they are never near their limits:
  the nightly artifacts `measurements-<kernel>` hold every spread with its bounds (`"quantity":"delay spread p5-p95"`,
  "The measurement record" above; `go run ./tools/measurements -match spread <downloaded nights>` shows the position
  of each between its bounds).
  `Statistically` is the flakiness policy: an attempt that
  fails is run once more (the attempt measures anew), only a second failure fails the test, and the
  message carries both measurements.
- **What runs where.** The functional assertions always run: the effect is present, the upload is
  longer than the download by about the configured difference, the loss is there and only in the
  direction it was configured, a flow the fault does not name loses nothing and did not get more than
  25 ms slower than before the fault. The accuracy assertions (median within ±2 ms + 5 %, spread, loss in the
  interval, unaffected flows within ±2 ms + 5 % of before) run when `testbed.Accurate()`, with N = 2000 probes
  (6 ms apart; at least 200 for the delay) instead of 300 under emulation (20 ms apart: the smallest loss the tests configure, 3 %, is missed by 300 probes with a chance of 1e-4 per flow, by 120 with 2.6 %).
- **The acceptance list of the plan, by test** (`make vm-test ARGS='-run "..." -tags testbed -test-timeout 120m ./internal/engine'`;
  an overlay write takes tens of seconds on the emulated kernel, so a test takes minutes):

| plan M8b test | test |
|---|---|
| measurement, device scope (+ a ping's round trip) | `TestADeviceFaultImpairsThatDeviceAsConfiguredAndNoOther` |
| measurement, group scope (members in two networks) | `TestAGroupFaultImpairsEveryMemberOfTheGroupAndNoOther` |
| measurement, network scope | `TestANetworkFaultImpairsEveryDeviceOfTheNetworkAndNoOtherNetwork` |
| between two test networks | `TestFaultsBetweenTwoTestNetworksFollowTheInitiatorAndTheDirection` |
| test network and WireGuard client network | `TestFaultsBetweenATestNetworkAndAWireGuardClientNetworkAreMeasuredPerDirection` |
| route learned via BGP | `TestAFaultOnTrafficOverARouteLearnedByBGPIsMeasuredPerDirection` |
| isolation (every fault test above) | `expectUnaffected`: a device or flow the fault does not name, measured before and after |
| updating one fault does not disturb others | `TestUpdatingOneFaultDoesNotDisturbTheOthers` (also holds every delay of C's stream during the changes to the fault's bounds with `testbed.CheckDelays`: at most 1 % (and always one packet) outside, none more than 10 ms outside, the tests' own tolerance since the plan gives one for the median only; the stream is at least 200 packets, sent 5 ms apart, so that the allowance is two packets and means something: decided by the maintainer on 2026-10-11, P2-M10-08, after a loaded native runner put two of 61 packets outside the bounds; the one probe in about 470 that a native runner once saw pass without its fault, unclassified, is what the flush and fill of the set elements produces on that runner's kernel, measured; the apply no longer does it, P2-M10-09 in "Compiler, apply and engine") |
| 600 ms fault changed under load loses no queued packet | `TestChangingAFaultOf600msThroughTheEngineUnderLoadLosesNoQueuedPacket` (engine, sent = delivered + the fault's own drops) and `TestChangingAFaultOf600msUnderLoadLosesNoPacket` (apply) |
| make before break | `TestSwitchingADeviceToANewFaultIdThroughTheEngineLosesNoPacketAndTheOldClassesGoLater` (engine) and `TestMovingADeviceToANewFaultIdLosesNoPacketAndTheOldClassesGoLater` (apply), `internal/apply/tcapply_test.go` for the plans |
| golden tests for the `tc -j` normalizer | `internal/linux/tcnorm_test.go` on `internal/linux/testdata/tc` |
| a write returns after the kernel verified; an injected tc failure reverts | `TestAnOverlayWriteWaitsForTheKernelsTreeAndAFailedTCOperationTakesItBack` (real kernel), `internal/engine/tc_test.go` (simulated) |

  The engine resolves the addresses of configured devices into its identity only when it observes the
  host, so the tests with devices and groups start the engine on the real clock and `PollObserved`
  (`resolveDevices`) before they write a fault.

### Coalescing and the reader pool (M8a)

Plan §3.11 in the code, with the tests that pin it (`internal/engine/coalesce_test.go`,
`internal/executor/readers_test.go`).

- **The apply loop** was already a group commit: it compiles the latest desired state, so whatever the
  state owner decides while an apply runs goes into the next one. A writer returns when a verified
  apply has a generation at least as new as its change, and gets that generation
  (`settleOverlays`). An executor failure takes back every unconfirmed change and answers every
  waiting writer with `apply_failed`.
- **Validation is batched, too.** Checking a write means a dry compile of the whole store
  (`capacity_exceeded`, a fault that cannot be built), so one compile per write grows with the square
  of a burst: 100 writes cost about 0.9 s of the owner's time on a development machine, more than
  the plan's budget for the whole burst. The state owner therefore keeps a `putBatch` open: an
  overlay write is validated and stored at once but not answered, the owner takes the writes that are
  already waiting in its command channel (at most 256, and it never waits for more), and one dry compile
  checks them all. When it passes, the batch is one generation and one desired state, and the
  writers share that generation as the generation of their change. When it does not, the batch is
  taken back and its writes are made one at a time with their own compile, as if they had arrived one
  by one: the valid ones are accepted, the ones the compiler refuses are answered with
  `capacity_exceeded`, and neither affects the other. Every other command closes the batch first, so
  nothing is reordered. 100 writes now cost one apply and about 0.5 s on the simulated kernel
  (0.9 s under `-race`); 200 held writes cost two applies.
- **The executor's reader pool.** A request that consists of `read` operations only does not enter
  the writer's queue: it runs at once, in the goroutine of the caller, on one of four reader slots
  (`readerSlots`) beside whatever the writer does. A request that mixes reads and writes is a write.
  The writer still takes identity updates before plans and runs one operation at a time. `Close`
  refuses new reads and waits for the running ones. Reads that verify an apply are made by the apply
  loop after the apply returned, so they see its result; a read made by someone else can see the middle
  of a plan, which the kernel tools apply step by step.
- **One connection per concurrent read.** The server answers a connection's requests in order, so
  a read behind a plan on the same connection would still wait. `executor.Client` therefore opens
  extra connections for read-only requests (at most four, dialed on demand, kept idle, closed with the
  client; a client that was not made by `Dial` has none). They go through the same handshake and
  peer-credential check as the main one, and a read falls back to the main connection when no extra
  one can be made.
- **Operation time stamps.** `executor.Outcome` carries `Enqueued` and `Started` (wall time of the
  executor's clock, `WithClock`), and `QueueWait()` is the difference. A request that waited behind a
  plan shows it; a read has no wait for the writer. The fields are additive, so the protocol version
  stays 1. The scenario engine will use them for the queue wait of a step and `step_late` (§2.10).
- **The simulator** (`kernelsim`) used to parse every existing map element for every new one, which
  made a burst of hundreds of fault elements quadratic in the test, not in the product; it indexes the
  keys once per operation now.

## Access rules (M9)

Plan §2.2 and §2.4: the ordered allow, drop, reject and TCP-reset rules, in the configuration
(`access_rules` with `access_rule_order`) and as overlays (kind `rule`), compiled into two chains
(`internal/compiler/access.go`). The first half of this section describes the compiler's output, the
second ("Rules in the engine and over the API") what the engine does with it: overlays, the cut, the
counters, `explain`, the endpoints and the preview.

### Where the rules stand

```
input     jump cut_input*                    only when a rule can cut (below)
          ct state established,related accept
          iifname lo accept
          (the service namespace's answers)
          ANTI-LOCKOUT: management sources -> SSH and the UI port: counter "anti_lockout", accept
          the UI port, for everybody else: drop
          jump access_input                  <- the rules
          (BGP, OSPF, Babel of the links)
          the gateway's protection of the test networks: DHCP, DNS, ICMP echo, drop
forward   jump cut_forward*
          ct state established,related accept
          (service guard, switched traffic, invalid, IPv6)
          jump mgmt_guard                    the management guard, in both topologies
          jump access_forward                <- the rules
          the access matrix, the service rules, the default
```

- **The rules decide before the matrix.** `allow` in forward is `accept`: an exception to the matrix
  ("IoT may not reach Lab, but this device may reach that host on port 22"). Traffic that no
  rule selects goes on to the matrix, which stays the default policy per network. The management
  network is the exception: the guard below stands in front of the rules.
- **The management guard stands in front of the rules.** When the management interface is the uplink interface (the
  two-port topology, plan §2.16) the management network lies behind the uplink, and a device under test must not
  reach it. The rule that drops this (`ifs_test` to the uplink, destination in `mgmt_src`, counter `forward_drop`) is the
  last rule of the chain `mgmt_guard`, which forward jumps to in front of `jump access_forward`, as the anti-lockout rule
  stands in front of the input rules. No access rule, an `allow` rule for every source and every destination included,
  and no overlay opens the management network behind the uplink for a device under test (a local test network, a
  WireGuard network of the role test and its clients). The one thing that still does is an explicit entry of the
  access matrix that allows the device's network to reach the management network: the chain starts with a `return` for
  each such entry, so that traffic goes on to the rules and meets the entry in its place in the matrix. What a client's
  `reachable` list implies does not open it, and an explicit entry "to the uplink" does not either (it matches the
  interface only). `domain.World.AccessDecision` checks the same guard (`mgmtGuard`) before the rules and answers a drop by the
  access matrix without a rule, so `explain` and the kernel agree. Decided by the maintainer on 2026-10-11 (P2-M9-11;
  before, the guard stood behind the jump, and an `allow` rule for such a destination opened the management network).
  **The three-port topology has the guard too** (decided on 2026-10-17, P31-M35a-10). The policy table 100 has no route to
  the management network, but routing alone does not keep a device out: when the table loses its default (the uplink
  loses its address, e.g. an expired DHCP lease; the kernel deletes the routes via the device and the next compile fails
  with `uplink_no_gateway`) a device's packet falls past the policy rules to the main table, which knows the management
  subnet, and a router behind the uplink that routes to the management subnet carries it there through the uplink. So
  `mgmt_guard` drops, behind the same `return`s of the explicit matrix entries, new connections from `ifs_test` whose
  destination is in `mgmt_src` (whatever interface they leave through), to the management subnet when `allowed_sources`
  do not cover it, and whatever leaves through the management interface:

  ```
  chain mgmt_guard {
      meta iifname "br-lab" ip daddr @mgmt_src return                 # one per explicit matrix entry
      meta iifname "br-lab" ip daddr 192.168.56.0/24 return           # its subnet, when the sources do not cover it
      meta iifname @ifs_test ip daddr @mgmt_src counter "forward_drop" drop
      meta iifname @ifs_test ip daddr 192.168.56.0/24 counter "forward_drop" drop   # only when the sources do not cover the subnet
      meta iifname @ifs_test meta oifname "mgmt0" counter "forward_drop" drop
  }
  ```

  Replies (established and related, the replies of a management-side port forward included) are accepted before the
  jump, so the guard sees new connections only. An entry's return mirrors the drops by destination: table 100 has no
  route to the management network, so a packet an entry lets through leaves through the uplink (to a router there that
  knows the management network), not through `mgmt0`; the matrix endpoint `management` as a destination is
  `ip daddr @mgmt_src` in this topology for the same reason (P31-AUDIT-01). `explain` names the guard in both
  topologies (a drop by the access matrix, no rule; the reason names "behind the uplink" in the two-port topology only)
  and judges it by the set the kernel matches: the allowed sources, in the three-port topology also the management
  interface's subnet (`AccessFacts.ManagementSubnet`); in the two-port topology a host on the uplink's subnet outside
  the allowed sources is no management host. No `prohibit` default is added to table
  100 instead: the PMTU mirror tables copy every route of table 100 with an MTU, and the executor refuses an MTU on a route
  that is not unicast. Tests: `TestThreePortTopologyGuardsTheManagementNetworkByDestinationAndInterface`,
  `TestAnExplicitMatrixEntryReturnsFromTheThreePortGuard`, the `access-threeport` golden scenario (also in the kernel gate),
  `TestExplainNamesTheManagementGuardOfTheThreePortTopology` (engine) and the domain tests above; on the real kernel
  `TestWithoutTheUplinkDefaultAnAllowAllRuleStillDoesNotOpenTheManagementNetwork` (the uplink default deleted from table
  100, the management host given a route back: an allow-all rule does not carry A there, the guard counts, `explain`
  agrees, the management forward still answers), `TestAMatrixEntryOpensTheManagementNetworkBehindAManagementInterfaceOfItsOwn`
  and `TestAMatrixEntryOpensTheManagementNetworkThroughTheUplink` (the default kept: the entry opens a management address
  behind the uplink, also with allowed sources narrower than the subnet); with narrow allowed sources
  `TestTheGuardCoversWhatTheKernelGuardDropsWithNarrowAllowedSources` (domain) and
  `TestExplainUsesTheKernelsManagementSetWithNarrowAllowedSources` (engine, both topologies).
  **The WireGuard networks of the role management are part of the management network for the guard, in both
  topologies** (P31-AUDIT-11, option (b), decided 2026-10-18). Their tunnel subnets were always in `mgmt_src` (the
  anti-lockout rule lets them reach the control plane), so the three-port guard dropped them while the two-port
  guard, which drops only towards the uplink, did not, and a matrix entry towards such a network had no return.
  Now the guard ends, in both topologies, with a drop of whatever a device under test sends out through the
  interface of such a network (`meta iifname @ifs_test meta oifname "wg-admin" counter "forward_drop" drop`: its
  clients and the networks behind them too), and an explicit allow entry returns by its destination: an entry
  towards the network (`oifname "wg-admin"`) or towards one of its clients (`oifname` and the client's prefixes)
  returns as it is, and the endpoint `management` as a destination also stands for each such interface (one
  return and one matrix rule more per interface), so "IoT -> management" opens them as it opens the management
  network itself. A client's `reachable` list does not open the guard: it is an implicit entry, and only an
  explicit matrix entry opens the management network (plan §2.16; a test client that names a management-role
  network gets the matrix accept, but the guard drops it). `explain` agrees: `World.matrixEndpoints` counts an
  address in such a network (or behind its client) as the management network, and the guard's reason names "a
  WireGuard network of the role management". Tests: the golden files `access-threeport-wg` and
  `access-twoport-wg` (`TestGoldenTheGuardOfManagementRoleWireGuardNetworks`, also in the parser and kernel gate),
  `TestAMatrixEntryTowardsAManagementRoleWireGuardNetworkReturnsFromTheGuard`,
  `TestAManagementRoleWireGuardNetworkIsPartOfTheManagementNetworkForTheGuard` (domain, both topologies, the
  network, its client and `management` as the entry, the reachable list),
  `TestExplainGuardsAManagementRoleWireGuardNetworkInBothTopologies` (engine), and on the real kernel
  `TestTheGuardKeepsADeviceOutOfAManagementRoleWireGuardNetworkUntilAMatrixEntryOpensIt` (both topologies: an
  allow rule for everything does not carry A to the administrator's tunnel address, the guard counts, an entry
  "IoT -> admin" opens it, `explain` agrees). The audit's alternative to P31-AUDIT-01, refusing or warning on an
  explicit entry to the management network in the three-port topology, was not taken (decided 2026-10-18): such
  an entry stays allowed without a warning.
  Tests: `TestNoAccessRuleCanOpenTheManagementNetworkBehindTheUplink`, `TestAnExplicitMatrixEntryReturnsFromTheTwoPortGuard`,
  `TestNoAccessRuleOpensTheManagementNetworkBehindTheUplink` (the domain layer), the `access-twoport` scenario of the kernel
  gate, and on the real kernel `TestNoAllowRuleOpensTheManagementNetworkBehindTheUplinkOnTheRealKernel` and
  `TestAnExplicitMatrixEntryStillOpensTheManagementNetworkBehindTheUplinkOnTheRealKernel` (`explain` against the packets).
  **The review of these decisions (2026-10-18)** made the endpoint `management` follow the rule that the more
  specific endpoint comes first, also for the WireGuard networks of the role management, and as a source as well:
  - *Precedence.* What `management` stands for through such a network (`oifname "wg-admin"` as a destination,
    `iifname "wg-admin"` as a source) has its own rank after the networks (`rankMgmtWG` in `matrixRules` and in
    `domain.matrixEndpoint.rank`; an entry's rank is `from*5+to`), so "IoT -> admin: deny" wins over "IoT ->
    management: allow" and the reverse, and the same from the other side. In the three-port topology the address
    match of `management` as a destination (`ip daddr @mgmt_src`, which holds the tunnel subnets) leaves those
    interfaces out (`meta oifname != "wg-admin"`), so it cannot decide first for them. `explain` agrees:
    `World.matrixEndpoints` counts an address in such a network or behind its client as the management network
    through the network (`mgmtViaWG`, the lower rank) instead of through the allowed sources.
  - *Management as a source.* An entry `management -> X` also matches what comes in through a WireGuard network of
    the role management (`iifname "wg-admin"`, its clients and the networks behind them), in both topologies, as
    `explain` already counted the tunnel subnet as the management network when it was the source. Nothing of it
    returns from the guard: such traffic comes in on no test interface.
  - *Warnings.* An explicit entry from or towards `management` while WireGuard networks of the role management
    exist gets the warning `matrix_management_wireguard` ("the access matrix entry network IoT -> management also
    covers the WireGuard network of the role management admin, which is part of the management network"): an entry
    written before 2026-10-18 covers more than it did, in the two-port topology a whole network more (the upgrade
    note of plan §2.16). This is no warning on the entry to the management network as such (P31-AUDIT-01's
    alternative stays not taken): without such a network it does not come. A client of a test-role network whose
    `reachable` list names the management network, a WireGuard network of the role management or a client of one
    gets the warning `reachable_guarded` ("the client rA (lab-hub) names network admin in its reachable list, but
    the management guard drops a device under test there: only an explicit access matrix entry opens it").
  Tests: the golden files `access-{three,two}port-wg-specific-{deny,allow}`
  (`TestGoldenTheSpecificEntryWinsOverTheManagementEntryForAManagementRoleWireGuardNetwork`, two of them also in the
  parser and kernel gate), `TestTheSpecificEntryWinsOverTheManagementEntryForAManagementRoleWireGuardNetwork`,
  `TestAManagementEntryAlsoMatchesTrafficFromAManagementRoleWireGuardNetwork`,
  `TestAManagementEntryWarnsThatItCoversTheManagementRoleWireGuardNetworks`, `TestAReachableListThatTheGuardDropsWarns`
  (compiler, both topologies), `TestExplainLetsTheSpecificEntryWinOverTheManagementEntryForAManagementRoleWireGuardNetwork`
  and `TestExplainCountsAManagementRoleWireGuardNetworkAsTheManagementSource` (domain, both orders and topologies),
  `TestExplainRanksTheManagementEntryAfterAManagementRoleWireGuardNetwork` (engine).
- **`allow` accepts, in input too.** In input an allow rule is `accept`: it opens the gateway's own ports that
  the protection of the test networks closes (the protection answers DHCP, DNS and ICMP echo and drops
  the rest) for the rule's selector, and the routing protocols' accepts behind the rules no longer
  matter for it. Two rules stand in front of the jump to the access rules and keep standing: the
  anti-lockout rule (below), and the UI port, which is dropped for everybody but the management
  sources, so the UI/API stays unreachable from the test networks whatever a rule or an overlay says,
  and no rule locks the management network out. A drop, reject or reset rule in input acts before the
  gateway answers, so "drop UDP 53" silences the DNS proxy for a device, and a rule can block BGP on a
  link. Decided by the maintainer on 2026-10-11 (P2-M9-01; before, an allow rule in input was a
  `return` to the protection). `TestAnAllowRuleOpensTheGatewaysClosedPortsButNeverTheUIAndNeverLocksOutManagement`
  proves it on the real kernel and compares `explain` with the packets.
- **The anti-lockout rule is not part of the rules.** It stands in front of the jump, nothing is
  inserted before it, and `TestTheAntiLockoutRuleCannotBeOverriddenByAnyRuleOrOverlay` compares every rule in
  front of the jump with the chain compiled without rules. It has a counter (`anti_lockout`) and is
  listed, locked, as `AntiLockoutRule` (`system_rules` of `GET /rules`). The cut chain of input starts
  with the same match, so a cut window never resets the control plane. No rule and no overlay can take the control
  plane away, so there is nothing for the code `lockout_protected` (422) to refuse for rules; what can still lock the
  administrator out is a configuration change that moves the UI port or the management interface or its sources
  (`engine.LockoutRelevant` flags those and the apply waits for confirmation, commit-confirm, rolled back when it runs
  out: the protection plan §2.16 names for risk 20). The maintainer decided on 2026-10-11 (P2-M9-08) that commit-confirm
  is enough: no change is refused outright and `lockout_protected` stays reserved in the spec.
- **New connections only.** Established traffic is accepted before the rules (spike S3, C2; D11), so
  a rule changes the fate of new connections. The rule's counter counts the packets the rule decided,
  which are those of new connections: a hit means "this rule just refused something", and it does not
  grow while an established connection runs through. For an allow rule that is the first packets of
  the connections it let through (and the retransmitted SYNs of dropped ones): counting every packet of a decided
  connection would need a conntrack mark per rule, or a counting rule per allow rule behind the established
  accept. The spec says so (`AccessRuleView.counters`). Decided by the maintainer on 2026-10-11 (P2-M9-07).
- **The original tuple.** Every rule matches `ct original ip saddr` (a set), `ct original ip daddr`,
  `meta l4proto` and `ct original proto-dst`, never the packet's own addresses or interfaces, so a
  connection the gateway redirected is judged by what the device meant (plan §2.2, E11), and a reply
  is judged like the packet that opened the connection. A rule on `udp/53` therefore covers queries to
  the gateway's DNS address (redirected into the service namespace in forward) and queries sent
  directly to `169.254.100.2`. Since M35a, while a port forward is compiled, each rule also has a second,
  responder form for forwarded connections that matches the device by `ct reply ip saddr` and the port by
  `ct reply proto-src`; see [Port forwarding (M35a)](#port-forwarding-m35a).

### The compiled list

`Target.Access` (`AccessPlan`) is the effective order: the overlay rules, newest `updated_at` first
(the order `domain.World.ResolveAccess` uses), then the configured rules in `access_rule_order`, disabled
rules left out. A rule has a key (`overlay:<id>` or `config:<id>`), its position, its action, the
resolved source addresses and a named counter `rule_<10 hex>` derived from the key: the counter
keeps its name while the rule stays, however the order changes, survives every apply and goes with a
removed rule (plan §3.2). An overlay that is written again keeps its id and so its counter.

- **Sources** are sets `asrc_<96 bits of the scope's hash>_<hash of the type>`, interval sets of IPv4 prefixes
  (two scopes whose names collide with different addresses are a compile error, never a shared set),
  refilled at every apply from the identity of the devices (`domain.World.ScopePrefixes`): a device
  or group is the addresses of its devices (and the ranges that identify them), a network its
  prefixes (the subnet and the networks behind its downstream routes, the networks behind a hub's clients, a link's routes) and the addresses of the
  devices that belong to it, a remote network its prefixes. The global scope is the classification's
  set of test, WireGuard and remote networks, never the management network or the uplink. A scope
  without an address yet keeps its rule with an empty set (the rule takes effect with the first
  address, the counter exists).
- **Destinations**: an address or prefix, the prefixes of a network, or `uplink`, which is everything
  outside the networks and the management network (`anon_uplink_*` is the set that is excluded;
  `domain.World.NonUplinkPrefixes`, `domain.World.viaUplink`). The meaning is the configuration's, not the routing
  table's: prefixes that BIRD learned (BGP, OSPF) and the service namespace's 169.254.100.0/30 lie in no configured
  network, so they count as the uplink, and "drop to uplink" also drops traffic to a learned remote site and to the
  namespace of the DNS proxy. A rule that must leave them out names its destination with a `cidr`. Decided by the
  maintainer on 2026-10-11 (P2-M9-11), documented in the spec (`Destination`); `TestRulesMatchTheOriginalTupleAndNeverThePacketsOwnAddresses`
  pins the set. A hostname needs the DNS-derived sets of M20: until then such a rule stayed out of the
  chain with a `hostname_unresolved` warning (P2-M9-02). Transferred to the plan on 2026-10-11 and delivered by M20: the rule looks up
  the set of the name, and a drop rule blocks the addresses the device resolved.
- **Ports** are merged into disjoint ranges (an anonymous interval set refuses overlapping elements).
- **Verdicts**: `allow` is `accept` in both hooks; `drop`; `reject` is `reject with icmpx type <t>`, one statement for
  IPv4 and IPv6, where `<t>` is chosen per rule (below); `reset` is `reject with tcp reset` (validation requires protocol `tcp`).
  V1's selectors are IPv4 (IPv6 selectors come with M32; decided by the maintainer on 2026-10-11, P2-M9-09), and forwarded IPv6
  is dropped before the rules.
- **The type of a reject** is chosen per rule with the optional `reject_with` of the rule and of a rule overlay
  (`AccessRuleBody`): `port_unreachable` (the default and the only behavior before the field existed; the output of a rule
  without the field is byte for byte what it was), `host_unreachable`, `admin_prohibited` and `no_route`, which compile to
  `reject with icmpx type port-unreachable|host-unreachable|admin-prohibited|no-route`. A client sees "connection refused" for the first,
  "no route to host" for the second and third (told apart by the ICMP code on the wire: 3/3, 3/1, 3/13) and "network unreachable" for the
  last (3/0). Validation refuses `reject_with` on any other action (`reject_with_requires_reject`), the preview carries it,
  `explain` names it. Decided by the maintainer on 2026-10-11 (P2-M9-03; plan §2.4 says "reject (ICMP)" without the type).
  Tests: the `access-reject` golden (every type, a rule without a type, an overlay rule), `TestARejectRuleAnswersWithItsOwnIcmpType`,
  the kernel gate (the `access-reject` scenario is one of `transactionScenarios`) and
  `TestARejectAnswersWithTheIcmpErrorTheRuleNamesOnTheRealKernel`, which reads type and code off the sender's socket error queue.
- **Limits** (`capacity_exceeded`, a compile error that names the overlays that contributed): 1000 rules,
  overlays included (`Input.RuleLimit`, `DefaultRuleLimit`, the same on every architecture) and 65536
  source addresses in all sets (`Input.RuleElementLimit`). `TestTooManyRulesAreRefused...` names the limit it assumes.
- `AccessPlan.Winner(Tuple)` evaluates the plan in Go like the kernel does. It is the oracle of the
  engine's conntrack deletion, and `TestTheCompiledRulesDecideLikeTheDomainLayer` checks it against
  `domain.World.ResolveAccess` (the specification) over a grid of sources, destinations and ports.

### Also cut existing connections

The kernel keeps accepting an established connection after a drop rule arrived; deleting its conntrack
entry alone does not cut it either behind NAT (spike S3, C4/C5). The cut is C6: a window of a
second or less in which `jump cut_forward` / `jump cut_input`, in front of the established accept, run
rules that reset the packets of established connections, then the window is closed. The chains exist
(empty) only when some rule can cut (`HasCuts`: `cut_existing` with a drop, reject or reset action).

- `AccessPlan.CutRules(keys)` builds the window for the rules of the keys (the ones that are new or
  changed since the last apply, the engine's business): the effective list again, in order. A rule
  that cuts becomes "its selector, `ct state established`, `ct direction original`, TCP, `reject with
  tcp reset`"; every other rule becomes "its selector, `return`". A connection belongs to the first
  rule that selects it, so a cutting rule behind an allow rule (or a rule that does not cut) leaves
  that rule's connections alone. Only the original direction is reset: the device gets the reset,
  the server side stays half-open as in a real outage. Rules behind the last cutting rule are not in the
  window; the window skips what the hooks let no rule judge, as the hooks do in front of the rules: the
  forward chain starts with `iifname B oifname B return` for every bridge (traffic switched inside one
  test network, visible in forward when `br_netfilter` is loaded, plan §2.2 "never ours to impair or
  drop"), the input chain with `iifname lo return` (a connection of the gateway to its own bridge
  address has a test network's address as original source) and then the anti-lockout match and a
  `return`. These skips stand in the cut chains because the chains run in front of the established accept
  and so in front of the bridge accept and the loopback accept of the hooks
  (`TestACutWindowLeavesSwitchedAndGatewayOriginatedTrafficAlone`, which fails without them).
- `AccessPlan.NotJudged(tuple)` is the Go side of those skips (`Switched`, `Segments` and `GatewayAddrs` of the
  plan): a connection the gateway opened (original source one of its bridge, WireGuard, service
  namespace or uplink addresses, e.g. a BGP session it opened to a device) or one with both ends in one
  bridge's segment (its network and downstream routes) is never taken by the cut, whatever the rules
  select. A device towards the gateway's own address is input traffic, which the rules judge.
- `AccessPlan.CutTransaction(keys)` is the nftables JSON that replaces the two chains' content;
  without keys it closes the window. The conntrack entries of the rule are deleted afterwards for what
  the reset cannot reach (UDP, ICMP): the engine reads them and deletes those for which
  `Winner(tuple)` is a cutting rule of the window.

What the cut does and does not do at the edges (decided by the maintainer on 2026-10-11, P2-M9-10):

- The comparison is between the plans, and a plan holds the resolved source addresses. A device whose
  addresses first appear after the daemon's first apply (the set was empty at start and the identity
  fills it on a later apply) enters the scope of a long-standing `cut_existing` rule at that moment, and
  the rule cuts that device's existing connections then. That is the rule doing what it says for a device
  it now selects, but it is not caused by an edit of the rule.
- An address change of a device in a rule's scope changes the plan, so the identity update is a full
  apply instead of an incremental one; with a cutting rule that apply also reads the whole conntrack
  table. Nothing is wrong at the nft level; it costs a read of the table per such change.
- Only conntrack state ESTABLISHED counts as a TCP connection a window of resets reaches. A half-closed
  connection (FIN_WAIT, CLOSE_WAIT, SYN_RECV) still matches `ct state established` in the kernel's
  chains, keeps passing data, and is deleted from the table without a reset: the device sees a stall
  until its next packet meets the rule as a new connection, not an immediate reset. When no tracked
  connection of the cut is ESTABLISHED no window opens at all. Plan §2.4's "immediate reset" holds for
  established connections.

The alternatives, should they be wanted later: a cut on an edit of the rule only (the comparison would ignore the
sources of the old plan), and a window whenever a cutting rule selects a TCP connection in any state but TIME_WAIT, CLOSE
and LAST_ACK.

### Tests

- Unit and golden: `access_test.go` (order, verdicts, selectors, scopes, counters, capacity, cut
  windows, the Go oracle against the domain layer), goldens `access.golden.txt`,
  `access-cut.golden.txt` (readable) and `access.nft.golden.json` (the transaction).
- Kernel gate: `access` and `access-cut` are `transactionScenarios`, so
  `TestEveryCompiledRulesetIsAcceptedByTheKernel` runs them through the kernel with `nft -c`, and
  `TestEveryCutWindowIsAcceptedByTheKernel` checks every window the plan can open and the one that
  closes it on an applied ruleset.
- Kernel behavior (`accesskernel_test.go`, testbed): the compiled transaction on a gateway namespace with
  a device, a server and a management host. Established connections continue under a drop rule and new
  ones hang (C1/C2), reject and reset refuse at once, order and overlays, an allow rule as an exception
  to the matrix, the DNS rule in input, an allow rule that opens a closed port of the gateway (not the UI port),
  the four ICMP types of a reject, the anti-lockout rule under a global drop-everything rule, and
  the cut window with a connection that an earlier rule owns, and (on a bridged bed with `br_netfilter`
  loaded by the test) a window that leaves switched traffic between two devices and the gateway's own
  loopback connection alone. These tests run in the persistent VM
  (`make vm-test ARGS='-run "TestAccessRule|TestACutWindowResets|TestAnAllowRuleOpens|TestARejectAnswers" -tags testbed -test-timeout 15m ./internal/compiler'`);
  python3 starts slowly under emulation, so a run takes about six minutes.

### Rules in the engine and over the API

- **Rule overlays** take the road of the fault overlays: the same store (owner, key, TTL, lease, renew,
  replace, reset), the same write path (validate against the live configuration, a dry compile, one
  generation, the writer is answered when the apply loop has verified it, taken back when the apply
  fails). A rule overlay's key is owner, kind, target and selector, so writing the same selector again
  replaces it and keeps the id, and with it the rule's key (`overlay:<id>`), counter and counter epoch
  (`TestReplacingARuleOverlayKeepsItsIdItsPlaceInTheKeyAndItsCounter`). The capacity limit is a compile
  error the engine turns into `*CompileError` (HTTP 422 `capacity_exceeded`), the write is refused and
  nothing changes (`TestTooManyRulesAreRefusedWithCapacityExceededAndNothingChanges`; the test names its
  limit, `engine.Config.RuleLimit`, the production one is `compiler.DefaultRuleLimit` on every
  architecture). When a TTL or a lease runs out the rule leaves the next compile, and with it its chain
  entry, its counter and its sets (`TestTheTTLAndTheLeaseOfARuleOverlayRemoveItsRuleAndItsCounter`).
- **Faults and rules.** A rule overlay and a fault overlay are independent: faults are resolved per
  family, rules are one ordered list, and the kernel evaluates the rules first (plan §2.4), so a packet a
  rule refuses never reaches a fault's queue. `explain` shows both, and the verdict says which one counts
  (`TestARuleOverlayAndAFaultCombineWithTheRuleFirst`, simulated). On the kernel,
  `TestARuleActsBeforeAFaultOnTheSameDeviceOnTheRealKernel` gives one device a latency fault of 300 ms in
  both directions and rules on three ports: the rejected connection is refused in far less than the delay,
  the dropped one never comes up (it is not merely late), the port no rule names is delayed by the fault as
  before, and a device without fault or rule is not delayed. The fault counters of the classification
  still count what a rule drops afterwards: the counters say how many packets were classified, the queue
  statistics (`QueueStats`) how many a fault handled, and a rule's own counter what it dropped. Decided by the
  maintainer on 2026-10-11 (P2-M9-05; counting after the rules would move the counting rules behind the decision,
  for instance into a postrouting chain).
- **The cut** (`internal/engine/cut.go`). It runs in the apply loop right after a full apply (not after
  an incremental identity update) that verified, so the writer of the overlay waits for it, and it
  never fails the apply. The engine keeps the target of the last verified apply (`verified`, kept
  across a failed one) and compares: `cutFlows` takes the tracked connections (`conntrack -L` through the
  executor), asks `AccessPlan.Winner` of the new and of the old plan for each one on its original
  tuple, and takes it when the new winner cuts and the old one was not the same rule with the same
  effect. That makes the cut idempotent by construction (an apply that does not change the rule finds
  nothing; `TestACutIsNotRepeatedByAnApplyThatDoesNotChangeTheRule`), and it follows reorders, the
  removal of an earlier allow rule and a change from drop to reject
  (`TestACutFollowsWhatChangedBetweenTheOldAndTheNewRules`). The first apply of a process cuts nothing
  (the engine does not know what the kernel ran before). A connection that an earlier rule owns, and
  one an allow rule owns, is left alone; connections that are over (TIME_WAIT, CLOSE, LAST_ACK) and the
  control plane (a management source towards SSH or the UI port, the anti-lockout rule's match) are
  never taken, whatever a rule says. Since M35a the cut also finds forwarded connections by their reply
  source (the device behind the forward), as the responder form of the rules matches them; see
  [Port forwarding (M35a)](#port-forwarding-m35a).
  Then, for established TCP connections only, `cutWindowRun` opens the window (`CutTransaction(keys)`
  through `NftApply`), waits `Config.CutWindow` on the engine's clock (500 ms; a negative value is
  no wait, for tests), and closes it (`CutTransaction(nil)`, also when the wait or the opening failed, with
  a context that is not cancelled). The entries are deleted last, in batches of at most
  `executor.MaxConntrackFlows`, with the new operation below. A failure is logged and reported in the
  `applied` event (`cut_error`); the rules are in force either way. Closing the window is tried three
  times (`CloseAttempts`, `CloseRetryDelay` apart, on the engine's clock) because a window that stays open
  resets every established connection of the cutting rules' selectors and no overlay or configuration owns
  that: when all three fail the cut error says `close the cut window`, `Snapshot.CutWindowError` holds it
  (and `GET /system/health` reports the API component degraded), and the next apply closes the window: a
  full apply flushes the chains, an incremental identity update tries the close again
  (`TestAFailedCloseOfTheCutWindowIsRetriedBeforeItIsReported`,
  `TestAWindowThatStaysOpenIsReportedAndClosedByTheNextApply`,
  `TestAnIncrementalIdentityUpdateClosesACutWindowThatStayedOpen`). The event carries `cut_rules` (the
  keys that cut) and `cut_connections` when a cut happened.
  `TestACuttingRuleOverlayResetsAndDeletesTheConnectionsItOwnsAndNothingElse` checks the order
  (open, close, delete) and exactly which entries go.
- **`conntrack_delete`** is a closed executor operation (`internal/executor`): a list of flows by original
  tuple (tcp or udp with ports, icmp with type, code and id; IPv4), one `conntrack -D -f ipv4 -p ...
  --orig-src ... --orig-dst ...` per flow. It takes tuples, never a filter, so it cannot delete more than it
  names. The tool exits 1 with "0 flow entries have been deleted." for a flow that is gone; that is
  benign (the operation is idempotent). The kernel simulator deletes the lines of its scripted
  `conntrack -L` text.
- **Snapshot and counters.** `Snapshot.Access` is the `AccessPlan` of the last applied target and
  `Snapshot.RuleEpochs` the generation in which each rule key first appeared (like the fault epochs; a
  new table resets them all). A rule's counter is read with `Engine.ReadCounters` by `AccessRule.Counter`.
- **`explain`** (`domain.World.AccessDecision`). Towards the gateway: the control plane first (a
  management source is allowed to SSH and the UI port, exactly what the anti-lockout rule guarantees, and the
  reason says so; on any other port the explanation says that the protection covers the test networks only and
  nothing is guaranteed to a management source; the UI port is dropped for everybody else, whatever a rule says), then
  the rules, then the gateway's protection (DHCP, DNS, ICMP echo). Towards anything else: the rules, then the
  matrix. The first matching rule decides, overlay rules first (`ResolveAccess`); `access.layer` is
  `overlay_rule` or `config_rule` and `access.rule` its id. An allow rule in front of the gateway's own
  address allows (the reason says that it opens the gateway's port, P2-M9-01), as the kernel does; a reject
  names its ICMP type. A drop rule on `udp/53`
  is what `explain` names for the gateway's DNS address and for 169.254.100.2 alike
  (`TestExplainNamesTheRuleThatDecidesAndFollowsOverlaysAndOrder`). A hostname destination has no address
  to judge; since M20 the rules that name hostnames are compiled and `explain` matches them against the names the device got for the address.
  This resolves P2-M8a-07.
- **Endpoints.** `GET /rules` lists the configured rules in the order of `access_rule_order` with `state`
  (`effective` when the rule is in the packet path, `disabled` for `enabled: false` and for a rule the
  kernel does not run: before M20 the hostname rule) and `counters` (the active revision only), behind
  `system_rules` (the anti-lockout rule with its counter). `GET /rules/{id}` takes an id or a name.
  `GET /overlays` and `/overlays/{id}` show a rule overlay with `state` and `counters` too;
  `/overlays?kind=rule` lists them. `GET /capabilities` has the overlay kind `rule` and the feature
  `rules`.
- **Preview.** `POST /revisions/{id}/preview` has `rules`: the effective list after the change, overlay
  rules first, each with its key, layer, place, action, selector in words and `new` when the kernel does
  not run it in this form yet. A hostname rule is in the list since M20 (before, it was left out with the warning
  `hostname_unresolved`); a reject carries its `reject_with`; more rules than the limit is `capacity_exceeded` at preview and apply
  (`TestARevisionWithMoreRulesThanTheLimitIsRefusedAtPreviewAndApply`).
- **Tests.** Simulator and fake clock: `internal/engine/access_test.go`, `cut_internal_test.go`,
  `internal/api/rules_test.go`, `internal/executor/conntrack_delete_test.go`. Real kernel (testbed, run in
  the persistent VM): `internal/engine/integration_access_test.go` writes rule overlays through the engine
  into a lab of three devices and checks new connections, the reset of a TCP connection, the deletion of a
  UDP flow, isolation of the other devices and the TTL; `internal/api/e2e_access_test.go` creates a drop rule on
  `udp/53` over HTTP and shows that the DNS proxy is silenced for queries to the gateway's address and to
  169.254.100.2 alike (TCP stays), that the overlay's counter counts the dropped queries, and that `explain`
  names the overlay. Both passed in the persistent VM (emulated, so no timing is asserted).

### Acceptance map (plan M9 "Tests")

Every bullet of the plan's test list and every cell of spike S3's behavior matrix has a test, with one
exception that is a substitute and says so: C1 (a rule that sees every packet, which would stall an
established stream) is not a ruleset the product can build (D11), so no test reproduces the cell; the
test asserts the order that rules it out. The "server side afterwards" column is asserted where the
matrix gives it: half-open after C4 and C6 and after C3 with `cut_existing`, closed normally after C0, C2
and C5 (`serverSideEnds`). The real-kernel ones are testbed tests; run them in the persistent VM (`make vm-test`). Accuracy is not
the subject of M9: all assertions are functional and hold under emulation. In the persistent VM (emulated)
the S3 matrix takes about 10 minutes, `TestRuleOrderOverlaysAndExplainAgreeWithTheKernel` about 10 minutes (every
probe that is dropped waits out a 2 s client timeout), the two DNS tests of `internal/api` about 5 minutes each
(the setup applies the whole configuration; the e2e harness gives its HTTP client 6 minutes for it), the
compiler tests 1 to 2 minutes each. Select them with `-run`, and pass `-test-timeout 40m` for the engine ones.

| Plan bullet | Test (real kernel unless noted) |
|---|---|
| S3 C0, no change | `TestTheBehaviorMatrixOfS3OnTheRealKernel/C0_no_change` (engine, with NAT) |
| S3 C1, a rule that sees every packet | not reproduced: the product cannot build that ruleset (the established accept comes first, D11). Substitute: `.../C1_C2_a_drop_rule_changes_new_connections_only` asserts the order in the kernel's forward chain (the established accept stands before `jump access_forward`); the compiler-level run is `TestAccessRulesOnTheRealKernelChangeNewConnectionsOnly` |
| S3 C2, established accept then drop | `.../C1_C2_...` (stream continues, new connections hang, the rule's counter counts, another device is not touched) |
| S3 C3, reject with tcp reset | `.../C3_a_reset_rule_refuses_new_connections_and_cuts_when_asked` (refused at once; the stream continues, and is reset with `cut_existing`) |
| S3 C4, C2 plus a conntrack deletion | `.../C4_a_drop_rule_and_a_conntrack_deletion_hang_the_stream` (stream hangs, server side half-open, no entry comes back) |
| S3 C5, a conntrack deletion alone | `.../C5_a_conntrack_deletion_alone_does_not_cut_behind_NAT` (stream continues, the flow is re-created) |
| S3 C6, a one-shot cut | `.../C6_a_cut_resets_the_stream_and_leaves_the_server_half_open` (reset, server half-open, window closed when the write is answered, the device reconnects once the rule is gone); the compiler-level run is `TestACutWindowResetsEstablishedConnectionsOnTheRealKernel` |
| Rules before faults (plan §2.4, task scope) | `TestARuleActsBeforeAFaultOnTheSameDeviceOnTheRealKernel` (a rejected port is refused without the fault's delay, a dropped one does not come up, an allowed port of the same device is still delayed; functional), simulated: `TestARuleOverlayAndAFaultCombineWithTheRuleFirst` |
| The cut leaves what no rule judges | `TestACutWindowLeavesSwitchedAndGatewayOriginatedTrafficAlone` (kernel: switched traffic between two devices and the gateway's loopback connection survive, a routed one is reset), `TestThePlanKnowsWhatNoRuleJudges`, `TestACutLeavesGatewayOriginatedAndSwitchedConnectionsAlone` (units) |
| A cut window that cannot be closed | `TestAFailedCloseOfTheCutWindowIsRetriedBeforeItIsReported`, `TestAWindowThatStaysOpenIsReportedAndClosedByTheNextApply`, `TestAnIncrementalIdentityUpdateClosesACutWindowThatStayedOpen` (simulated) |
| Rule order, first match wins | `TestAccessRuleOrderAndOverlaysOnTheRealKernel` (compiled ruleset), `TestRuleOrderOverlaysAndExplainAgreeWithTheKernel` (engine: configured order, an allow rule as an exception to the matrix) |
| Overlay rules before configuration rules | the same two tests (an overlay in front of configured rules; the newest of two overlays wins); simulated: `TestOverlayRulesComeBeforeConfigurationRulesNewestFirst` |
| The anti-lockout rule cannot be overridden | `TestTheAntiLockoutRuleCannotBeOverriddenByAnyRuleOrOverlay` (ruleset, unit), `TestAccessRulesInInputAndTheAntiLockoutRuleOnTheRealKernel`, `TestTheAntiLockoutRuleHoldsAgainstRulesThatSelectTheManagementHost` (rules that really select the host: UDP is refused, SSH and the UI are not, a window with every rule does not reset the host's SSH), `TestNoRuleOrOverlayLocksTheManagementNetworkOut` (the same through the engine) |
| A drop rule on UDP 53 blocks the DNS proxy, queries to the gateway address and direct queries to 169.254.100.2 | `TestARuleOverlayCreatedOverTheAPIRefusesTheDNSQueriesOfItsNetworkAndCountsThem`, `TestAConfiguredDropRuleOnUDP53SilencesTheDNSProxyForBothAddresses` (a configured rule over a revision); input path in the compiled ruleset: `TestAccessRulesInInputAndTheAntiLockoutRuleOnTheRealKernel` |
| "Also cut existing connections" | `TestARuleOverlayRefusesNewConnectionsAndCutsExistingOnesOfItsDeviceOnly` (TCP reset, UDP flow deleted), `TestACutTakesOnlyWhatTheSelectorOwnsOnTheRealKernel` (the named port only, another device's connection and the entries of the others stay; a tracked ping flow is deleted), simulated: `TestACuttingRuleOverlayResetsAndDeletesTheConnectionsItOwnsAndNothingElse`, `TestACutLeavesTheControlPlaneFinishedConnectionsAndOtherProtocolsAlone` |
| reject and reset variants | `TestRejectResetAndDropAnswerWithTheirOwnPacketsOnTheWire` (ICMP port unreachable, TCP RST from the server's address, nothing for drop), `TestARejectAnswersWithTheIcmpErrorTheRuleNamesOnTheRealKernel` (the four reject types in forward and input: the sender reads ICMP type and code 3/3, 3/1, 3/13 and 3/0 off its socket's error queue) |
| An allow rule in input opens the gateway's closed ports, but not the UI | `TestAnAllowRuleOpensTheGatewaysClosedPortsButNeverTheUIAndNeverLocksOutManagement` (a device reaches SSH and a closed port of the gateway through an allow rule, never the UI port, the management host keeps SSH and the UI under a drop rule for everybody; `explain` agrees with the kernel for every probe), `TestAnAllowRuleOpensTheGatewayButNoRuleChangesItsControlPlane` (engine, simulated) |
| Named per-rule counters | `TestEachRuleHasACounterOfItsOwnThatCountsWhatItDecided` (exact counts per rule, an allow rule counts the first packet only, P2-M9-07), `TestTheCounterOfARuleFollowsTheRuleNotItsPlace` (unit) |
| IPv6 | `TestForwardedIPv6StaysBlockedWhateverTheRulesAllow` (an allow rule does not open forwarded IPv6, no rule counts an IPv6 packet; V1 selectors are IPv4, D7), `TestEveryActionHasItsVerdictInForwardAndInput` (one reject statement for both families). Rules select IPv4 only until M32 (decided by the maintainer on 2026-10-11, P2-M9-09) |
| `capacity_exceeded` | `TestTooManyRulesAreRefusedAndTheOverlaysThatCausedItAreNamed`, `TestTooManyRulesAreRefusedWithCapacityExceededAndNothingChanges`, `TestARevisionWithMoreRulesThanTheLimitIsRefusedAtPreviewAndApply` (each names its limit) |
| Explain and preview of the effective result | `TestExplainNamesTheRuleThatDecidesAndFollowsOverlaysAndOrder`, `TestTheExplanationOfARejectNamesItsIcmpType`, `TestThePreviewListsTheEffectiveRulesInOrderAndMarksTheNewOnes`; against the kernel: `TestRuleOrderOverlaysAndExplainAgreeWithTheKernel` (for every probe, explain's verdict is the one the packets get, and every rule that decided counted) |

The cases C4 and C5 hold their connection with one message per second and act right after an answer; C6 sends
every 300 ms, so that the 500 ms window of the cut is sure to meet a message, and opens the window right after an
answer.
With a message every 100 ms an echo is on its way back to the device in a few percent of the cases when the
entry is deleted or the cut window opens; it reaches the gateway without an entry (or inside the window),
the gateway answers the server with a reset, and the server's side is gone by chance instead of staying
half-open (seen once on level 1b, then reproduced in the persistent VM: 3 of 40 deletions). That is what a
real outage does as well; the test only has to stay out of that moment.

## Extended faults (M10)

Plan §2.5 and M10. This section describes the non-tunnel half of M10, built on the fault engine of M8a
and M8b: rate and queue limit per device (D18), reorder, duplicate, corrupt, burst loss (Gilbert-Elliott),
blackout and flapping, and, in its own subsection below, the MTU family (MTU and PMTUD). The tunnel faults, the
WireGuard-action overlays and E10 are in "Tunnel faults and WireGuard actions" after the MTU tests. What was already there and is only tested now:
the model and the validation (`NetemParams` has had every one of these since Phase 1), the netem leaf with
its complete parameter set (`compiler/netem.go`), the per-device fault ids of D18 (`compiler/faults.go`) and
the class limit with `capacity_exceeded`.

### What the VM proved first

Before any code was written around them, in the persistent VM (kernel 6.8.0-142, iproute2 6.19, nftables
1.1.6; `make vm-exec`), and again on the 7.0.0-38 kernel of Ubuntu 26.04 for the constructs the compiler
emits (below, "Kernel matrix"):

- **Every netem shape is accepted** and reads back as the compiler predicts: `loss gemodel` at its
  edges (p or r 0 %, 100 %, `0.000000001 %`), `corrupt 100 %`, `reorder 100 %` with a delay, `rate 8bit`
  and `rate 100Gbit`, `limit 1` and `limit 1000000`, `loss random 100 %`. The edges are in
  `TestTheNormOfEveryNetemShapeIsWhatTheKernelReports`.
- **A flapping toggle is a `replace` of the leaf.** `replace` (or `change`) from `loss gemodel ...` to
  `loss random 100% 0%` and back keeps the seed and the counters, and the gemodel's state is simply used
  again; `loss random 100% 0%` clears a gemodel, as `loss random 0% 0%` did.
- **A duplicating netem cannot share an interface** with any other netem (P2-M8b-01). The tree has every
  class on every interface, so a duplicating fault in both directions would not even fit by itself. The
  copy is therefore made outside netem, below.
- **First try, and why it is gone: a tc hook.** `tc qdisc replace dev D clsact` and `tc filter replace dev D
  egress handle 0x200000/0x200000 protocol ip prio 1 fw action skbedit mark 0x0/0x200000 pipe action
  mirred egress mirror dev D`, with a packet mark with bit 21 set from nftables (`meta mark set mark |
  0x200000`), duplicated 103 of 1000 pings flagged with 10 % on **6.8.0-142**, and the copy went through the
  same HTB class and netem. On **7.0.0-38** the same filter does nothing: the mirred action counts an
  overlimit for every packet and sends no copy (no message in `dmesg`; with or without the `skbedit`; the
  action seems to refuse a mirror back to the device the packet is leaving through, which 6.8 allows). The matrix has both
  kernels, so the first version of the hook (clsact, `skbedit`, `mirred`, the executor's grammar for the
  egress block, the read of it, an action normalizer) was built, passed the 6.8 measurement and failed the
  7.0 one, and was replaced.
- **The hook that works on both: nftables.** A table of the netdev family with a base chain on the `egress`
  hook of the interface (kernel 5.16 and later), the rule `meta mark & 0x200000 == 0x200000  meta mark set
  meta mark & 0xffdfffff  dup to "D"`. The hook runs before the qdisc, so the clone passes the whole egress
  path again, tc included; duplicated 56 of 200 pings flagged with 30 % on 7.0 and the copies were delayed
  by the 20 ms of the netem leaf. nft's JSON for it is `{"dup": {"addr": "D"}}` (the device name goes into
  `addr`), a netdev base chain prints its interface as `"dev"`. A table is replaced in one transaction by
  `add table`, `delete table`, `add table` and the chains (nothing meets half of it); a chain on an interface
  that does not exist is refused by the kernel (`Could not process rule: No such file or directory`).

### Duplication (P2-M10-01)

The decision of P2-M8b-01, confirmed by the maintainer on 2026-10-11 (P2-M10-01; P2-M8b-01 is resolved by it). Netem's
`duplicate` is never used: `Netem.Args()` writes `duplicate 0% 0%` in every leaf and `Netem.Duplicate` only keeps the
fault's probability. The alternative that was not chosen is netem's duplicate, allowed only when the tree holds no
other netem (`capacity_exceeded` naming it, and a delete-before-add order in the apply). The consequences below are
the ones the maintainer weighed: the copy is made after the fault's classification, so the fault's nft counters do not
count it and the queue statistics do; the table is a second one next to `inet chaosgw`; it needs the netdev egress hook
(kernel 5.16 or later, `nft_dup_netdev`, both in the plan's minimum kernel and in the preflight list); the bit is
one of the reserved routing mark bits; the cost of the clone on a Raspberry Pi is not measured (H1).

```
classify (prerouting)   mark_<id>: ... meta mark set mark | 0x200000     with probability p, per direction
                                    (ct direction original|reply, numgen random mod 10^9 < p × 10^7)
table netdev chaosgw_dup, one chain per interface of the tc tree, hook egress:
   egress_<dev>: meta mark & 0x200000 == 0x200000 -> meta mark set mark & 0xffdfffff -> dup to "<dev>"
   -> (the packet and its copy) -> HTB root -> fw filter by the id bits -> the class of the fault -> its netem leaf
```

- **The draw** is a rule of the fault's mark chain (`markChain`, `classify.go`): one rule per direction that
  duplicates, `ct direction original|reply`, `numgen random mod 1000000000 < N`, `meta mark set mark |
  MarkDupBit`; 100 % is the rule without the draw (nft refuses a comparison against the modulus), and a
  probability that rounds to zero parts in 10^9 has no rule. The resolution is 10^-7 percent.
- **The copy** is a clone of the packet, sent through the interface's whole egress path again: the same
  HTB class, so it has the delay, loss and rate of the original (a duplicate in netem is delayed as well),
  and is dropped or limited like it. The flag is cleared on the original before the clone is made, so
  neither is copied again. The original and the copy are two packets of the class; the fault's named
  counters count the packet once (they count the classification), the queue statistics count both.
  `TestADuplicatingFaultDuplicatesAsConfiguredNextToOtherFaults` shows it on the real kernel (P2-AUDIT-13): with
  `{latency: 50ms, duplicate: 10%}` every copy arrives with the delay and the netem leaf of the class sends the
  originals and the copies (emulated: 600 datagrams, 66 copies, the leaf sent 666, the earliest copy after 56 ms);
  with `{loss: 100%, duplicate: 50%}` nothing arrives and the leaf drops the copies next to the datagrams (emulated:
  154 copies next to 300). The probes keep the delays of the copies apart (`ProbeResult.UpCopies`, `DownCopies`).
  The share of the copies and the medians are checked with native execution or KVM.
- **Mark bit 21** was one of the reserved routing bits; the layout in plan §3.3 now names it. The fw
  filters of the classes mask it out (`MarkMask` is `0x1fff0`), and `TestTheDuplicationBitIsAReservedRoutingMarkBitThatNothingElseUses`
  pins that nothing else overlaps.
- **The executor's operation** is `nft_dup` (`executor.NftDup`, `internal/executor/nftdup.go`): a list of
  interfaces, no free-form rule. The executor writes the table itself (`DupTransaction`: chain
  `egress_<dev>` of type filter, hook egress, priority 0, policy accept, bound to the interface, and the
  one rule `DupRuleExpr`, with a comment that is a hash of the rule so that a read-back can tell it from
  another one), the interfaces must be assigned to Chaos Gateway (the scope check), and `read` `nft_dup`
  returns the table (`nft -j list table netdev chaosgw_dup`; a missing table is an empty ruleset). The
  table is not the table `inet chaosgw` of `nft_apply`, which stays closed to every other table.
- **The apply** (`internal/apply/dup.go`). `Target.DupDevs` is the interfaces of the tc tree while some class
  duplicates. `dupProblems` compares them with the table the kernel holds: a chain missing, on an interface
  that is not wanted, of another shape, or without the one rule is a difference; a table with nothing
  to do is one too. The verify reports them (`nft: duplication hook: ...`); the plan writes the table before
  the transaction that makes packets ask for a copy and deletes it after the transaction that stopped them
  (a flagged packet that meets no hook is merely not copied; the flag is harmless). A re-apply of the
  same target writes nothing for the hook. The simulated kernel has the table too (`kernelsim/nftdup.go`).
- **What it does not do.** A packet that leaves through an interface without the hook (the management
  interface, a port) is not duplicated: only test traffic is classified and it leaves through the tree's
  interfaces. A packet that is forwarded from one test network to another is duplicated once, on the
  interface it leaves through.
- **An interface that goes takes its chain along** (found in the VM): the kernel removes the base chain
  bound to a deleted interface and does not bring it back when an interface of that name returns. The
  apply that deletes an interface (a network that is no longer wanted) sees the chain in the state it read
  and writes the table again; an interface that is deleted and created again behind the gateway's back
  loses its hook until the next apply, whose verify reports it (`nft: duplication hook: X has no hook`)
  and whose plan writes the table again. Nothing checks in between (drift detection is M38).
- **Modules:** `nft_dup_netdev` was in the shared list for the capture of M17; M10 uses it as well. M10 adds `sch_ingress` (the uplink's ingress qdisc of the tunnel faults) and `nft_numgen` (the duplication draw), so that the preflight, not the apply, names a kernel that lacks them. `nft_exthdr`, which the MSS clamp (`tcp option maxseg`) needs, is not a module of its own: it is compiled into `nf_tables` (Ubuntu 6.8: no `nft_exthdr.ko` in `modules.dep`, the source file is part of `nf_tables.ko`), so it is not on the list; a preflight that required it would fail on every kernel.

### Flapping (`internal/engine/flap.go`)

A flapping fault is up for `up`, then a blackout (netem `loss random 100%`, everything else of the leaf
unchanged) for `down`, and so on, starting up. The phase is not configuration; it is the clock's.

- **The schedule.** The flapper holds one entry per flap key (`compiler.FlapKey`: the fault's key without the
  device, and the direction: all classes of a fault flap in step on all devices), with the spec and the
  start. The start is the moment an apply containing the flapping was verified (`flapper.sync` after
  every successful full apply): a write that is answered starts its fault's first up phase. A
  flapping whose other parameters change keeps its schedule; changed times are a new flapping and start
  up. A restart starts every flapping up (overlays do not survive one either).
- **The boundaries** are `start + k × (up + down)` and `+ up`, from the start, on the injected clock
  (`Monotonic`), never by adding up timer delays: a timer that fires late does not move the next one.
  The apply loop arms one timer for the nearest boundary; when it fires, `toggleFlaps` computes the phase
  the schedule says now, and replaces the leaf of every class of every flapping that is not in that phase
  (one `tc -batch` for all interfaces since the tunnel faults, because a run of the tool costs seconds on a small or emulated machine and a toggle made interface by interface was late by the sum of them; a clock that jumped over several boundaries lands in the right phase
  with one toggle). Toggles run in the apply loop's goroutine, so they never overlap an apply: a boundary
  that falls into an apply is made right after it.
- **The compiler writes the phase** (`Input.FlapPhase`, `TCClass.Down`, `TCClass.Config()`): a full apply
  of the down phase writes the blackout where the leaf holds the up configuration, an apply in the up
  phase ends nothing early, and `Verify` compares the phase it was compiled with. The phase is asked with
  the spec the compile is about to write, so a changed flapping is up. `sameFaultStructure` compares the
  trees with the phase, and the toggle updates the target the loop holds, so an identity-only update
  stays incremental through a flapping.
- **Only the running gateway flaps.** The one-shot `chaosgw apply` leaves a flapping fault in its up phase
  and exits: nothing toggles it afterwards.
- **A failed toggle** is tried again a second later (`flapRetry`); the schedule does not get stuck, and the
  next full apply writes the phase anyway.
- **What is decided, and what is not** (maintainer, 2026-10-11, P2-M10-02): the first up phase starts when the apply
  that contains the flapping has been verified (the moment the overlay write is answered); the schedule is the start
  plus multiples of up and down on the injected clock; all classes of one fault and direction, on all devices, flap in
  step, and upload and download of a flat flapping have separate schedules that happen to coincide; a toggle runs in
  the apply loop's goroutine, so one that falls into an apply is made right after it (a full apply of a large target
  takes seconds on small hardware, the figure is for H1). A toggle that cannot be late (one between the operations of an
  apply) and a phase that survives a restart were not asked for.
- **Timing tolerance.** Plan §2.10 gives scenario steps ±100 ms on a native or KVM machine, counted at the
  executor's commit; a boundary is a step of the same kind, and `engine.FlapTolerance` (100 ms) is the
  same figure: from the scheduled moment to the moment the executor answered (`FlapChange.Late`). The
  figure is asserted on the real kernel where `testbed.Accurate()`; under emulation the schedule itself
  (the exact distances of the toggles) is asserted and the lateness is only logged. A probe stream sees an
  outage to the precision of its spacing, so `testbed.CheckFlaps` adds two intervals of the stream to
  the engine's tolerance.
- **What the API shows.** `QueueStats.flapping` (`phase`, `since`, `next_change_at`) on the queues of a
  flapping fault (`Engine.Flaps`); `Engine.FlapLog` keeps the last 1024 toggles with the scheduled and the
  commit time, for the tests and later for the run timeline.
- **A defect this found.** `&clock.Real{}`, which `engine.New` and the API server use by default, had a
  zero origin, so `Monotonic()` stood still at 292 years: no deadline computed from it came (overlay TTLs
  and leases, the retirer's grace period). The zero value is a working clock now (the origin is the first
  reading; `TestTheZeroValueOfRealIsAClockWhoseMonotonicTimeMoves`).

### Rate, queue limit and the class limit (D18)

Compiler and capacity were M8a's; M10 measures them.

- A fault with a `rate`, an explicit `queue_limit` or `keep_order` gets one fault id per matched device (plus
  one for the addresses of the network that no device owns), so a 2 Mbit/s "Bad LTE" on a network is 2 Mbit/s
  per device, not 2 Mbit/s for the network (E9: `TestE9ABadLTEProfileOnANetworkGivesEveryDeviceItsOwnQueueWithTheFullRate`).
  `TestAnIotRateOfTwoMbitGivesEveryDeviceOfTheNetworkItsOwnTwoMbit` transfers with iperf3 from two devices
  at the same time, in each direction, and from a device of another network that the fault does not name.
  The throughput iperf3 reports is the TCP payload and netem's rate counts the IP packet (1448 of 1500
  bytes), so 2 Mbit/s shows as 1.93 Mbit/s, inside the plan's ±10 %.
- **Rates below 8 bit/s are refused** (`invalid_rate`): netem counts bytes per second, so `rate: 7bit` would
  not limit at all (a rate of zero as "no limit" is a parameter that does nothing, and was not chosen). The queues of a flapping
  fault carry `flapping: {phase, since, next_change_at}` (optional and additive), because the UI needs to say that a device is in
  the down phase and the spec had nothing for it. Decided by the maintainer on 2026-10-11 (P2-M10-03).
- **`capacity_exceeded` in the preview** names the scope and the limit and is refused at the apply too:
  `TestThePreviewOfAPerDeviceRateThatDoesNotFitTheClassLimitIsCapacityExceeded` (engine; 30 known devices
  need 63 classes at a limit of 50; the limit it names is the one it sets),
  `TestAConfiguredFaultSetThatDoesNotFitTheClassLimitIsRefusedByThePreviewWithItsScope` (API, limit 6),
  and the compiler's `TestARateLimitedNetworkOf250DevicesNeeds500Classes` (1000 on x86-64, 200 on arm64:
  the tests that depend on it set the limit they mean).
- **The preview shows what a scope costs before the limit is reached** (plan §2.4, D18; P2-AUDIT-02):
  `queues` has one entry per winning fault or profile part that needs classes (`Target.Queues`: its fault
  ids, so for a rate, a queue limit or keep order one per matched device plus the one that the addresses no
  device owns share, otherwise one), `capacity` the classes of every interface that carries a tree, the
  IFB device included (`Target.ClassesPerInterface`), with the limit, and `linux.tc` the tc change (M8b
  section). Both are absent when no fault needs a class. Tests: the API's
  `TestThePreviewOfARateOnANetworkShowsItsQueuesTheClassesAndTheTCChange` (2 Mbit/s on a network of three
  known devices: 4 queues, 9 classes on each of the five interfaces at a limit of 50, eight 2 Mbit/s leaves
  per interface in `linux.tc`, nothing left in tc after the apply), the compiler's
  `TestTheQueuesOfEachFaultAndTheClassesOfEachInterfaceAreCounted` and the tunnel case in
  `TestATunnelFaultHasAClassOnEachSideOfTheTunnelAndIsFoundByTheEndpointOfItsPeer`.
- **An explicit queue limit** is a small buffer: `TestAnExplicitQueueLimitBoundsTheDelayAndDropsTheRest`
  sends a burst of 300 datagrams of 1000 bytes (back to back, from a script: a paced flood is only as steady
  as the sender's timer, which under emulation is not) into a 1 Mbit/s fault while probes run through it.
  With `queue_limit: 20` the queue holds 20 packets, drops the rest at its tail (261 to 264 of 300 in the
  runs below) and the slowest probe waits 141 to 161 ms (20 packets of about 1000 bytes at 1 Mbit/s
  are 165 ms); with the computed limit (1000 packets) nothing is dropped and the slowest probe waits 2.0
  to 2.3 s behind the burst (300 packets are 2.4 s). A sample of the queue's length does not work as the
  evidence: reading it takes longer than the queue lives under emulation.
  Device B, whom the fault does not name, probes next to the burst as the control (§4.3): it loses nothing and its delay stays
  within 25 ms of its baseline (within ±2 ms + 5 % with `Accurate()`, with the flakiness policy).

### MTU and PMTUD (plan §2.5, spike S13)

The MTU family caps the packet size of the traffic it selects, in one of three modes. It resolves on its own
(plan §2.4: the families are independent, so a device can have an impairment winner and an MTU winner at the
same time), has the same selectors as the impairment family (device, group, network, any; destination;
protocol and ports) and the same four-level lookup (`domain.World.Table(src, FamilyMTU)` was there since M8a).
The compiler is `internal/compiler/pmtu.go`.

| Mode | What the compiler writes | What happens to a packet that is longer than `size` |
|---|---|---|
| `icmp` | mark bits 17-19 = the index of a PMTU mirror table, an `ip rule` on that mark, the mirror table | the kernel itself answers "fragmentation needed, mtu `size`" to the sender, in both directions (a forwarded DF packet is checked against the route's MTU in `ip_forward`) |
| `blackhole` | a rule in the fault's chain: `meta length > size`, counter, `drop` | dropped without a word: no ICMP, a transfer of full-size segments stalls |
| `mss_clamp` | a rule in the fault's chain: `tcp flags & syn == syn`, `tcp option maxseg size > size - 40`, set it to `size - 40` | nothing: only the MSS option of a SYN or SYN-ACK is lowered, so neither side sends a segment that makes a packet longer than `size` (a connection with timestamps: 12 bytes of its own options come out of the segment). UDP, ICMP and existing connections are not touched |

**What the VM proved first** (kernel 6.8.0-142, iproute2 6.19, nftables 1.1.6, `make vm-exec`):

- `ip route replace D dev X table 103 proto 77 mtu lock 1280` is accepted, a second `replace` of the same key
  with another size changes the route in place (and one without `mtu` removes the size); `ip -j route show`
  prints `"metrics":[{"mtu":1280}]` and **does not show the lock**, even with `-d` (the plain `ip -d route`
  does: `mtu lock 1280`). The verify therefore compares the size, not the lock (P2-M10-04).
- `ip rule add priority 950 fwmark 0x20000/0xe0000 table 103 protocol 77` reads back as
  `{"fwmark":"0x20000","fwmask":"0xe0000","table":"103"}`; the apply's `stateRule` joins the two the way the
  compiler writes them (`0x20000/0xe0000`).
- A `jump` from the base chain to a regular chain whose only rules are verdict-map lookups (`goto` elements)
  comes back to the base chain's next rule when the goto target ends with `return`: the MTU lookup is the
  first of two independent lookups in the `classify` chain. The impairment lookup after it still ends the
  chain with its goto.
- `nft` rewrites `meta mark & 0xfff1ffff | 0x20000` into the canonical `& 0xfff3ffff` with an xor on its listing;
  verify compares rule hashes (the comment), not the printed expressions, so this does not matter.
- The ICMP mode with a ping of 1400 bytes and DF: "Frag needed and DF set (mtu = 1280)" from the gateway; the black
  hole: 100 % loss, nothing in return; the clamp: the server's accepted socket reports `TCP_MAXSEG` 1348 for a
  clamp of 1360 (1400 minus 40 minus the 12 bytes of the timestamp option, the other devices 1448).
- **BIRD attaches a kernel protocol to a routing table of its own**: a second `protocol kernel` on `master4`
  is refused (`Kernel syncer (gw_table) already attached to table master4`). A mirror therefore has
  `ipv4 table pmtu103;`, a `protocol pipe` from `master4` into it with the same export filter as the policy table's
  protocol, and `protocol kernel gw_pmtu103` with `export filter { krt_mtu = 1280; krt_lock_mtu = true; accept; }`
  (`internal/bird/render.go`, `bird.Config.Mirrors`).

**Classification.** With at least one MTU winner the classify chain jumps to `classify_pmtu` after it wrote the
direction bit; that chain has the four lookups (`pmtu_devdestport_*`, `pmtu_devdest_*`, `pmtu_devport_*`,
`pmtu_dev_*`: interval maps on the conntrack original tuple, built by the same `classElements` as the impairment
maps) whose elements go to the chain `pmtu_<hash of the winner's key>`. The chain counts the packet in
`pmtu_<hash>_up` or `_down` (by `ct direction`) and does the mode's work. Without an MTU fault nothing of this exists
(the classify chain has the rules it had before; the golden files of M7 to M9 did not change).

**Mark bits 17-19** hold the mirror table's index (1 to 7), written by the chain with `mark & 0xfff1ffff | index << 17`
(`pmtuKeep`: every other bit, the id, the direction, the service selection and the duplication bit, survives;
`TestThePMTUMarkBitsAreTheOnesOfThePlanAndOverlapNothing`). The index belongs to a **size**: two faults of the same
size share one table. `Input.PMTUTables` is the allocation of the previous compile (the engine feeds
`Target.PMTUTables` back, like the fault ids): a size that stays keeps its index while others come and go, a new size
takes the lowest free one, and the eighth distinct size is `capacity_exceeded` (the message names the scope that needs
the most, like the class limit's). Blackhole and clamp faults need no table and do not count.

**Mirror tables 103 to 109** (table 100 is the policy table, 101 the table of the replies of a management-side port forward
(M35a), 102 the service table; the executor's range
is 100 to 110) hold a copy of **every route of table 100** (connected networks, the uplink, downstream routes,
WireGuard networks and the networks behind clients, the default route) with `mtu lock <size>`
(`executor.Route.MTU`), and `ip rule priority 950 fwmark <index<<17>/0xe0000 lookup <table>` selects them, after the
service selection (900: `ServiceRulePriority`) and before the policy rules (1000). A packet of a download is
classified by the conntrack original tuple, so it carries the mark of its device too and the rule sends it
through the mirror (the route to the device's network then has the size). That is also why the server learns the
size: the kernel's answer for a reply goes to the server, which caches it for the gateway's NAT address (plan §2.5
and risk 23, the documented side effect; `TestAnIcmpMTUFault...` shows the server's cache, and measures the control device
before the fault, because after it the server's cache would make the control look limited too).

**Learned routes.** `Target.Bird` is rendered once without and once with the mirrors; `bird.Config.Mirrors` is
the list of (table, size). Every mirror gets a table, a pipe and a kernel protocol of its own, with the export filter
of the policy table's protocol (`export where source ~ [ RTS_BGP, ... ]`, so imported external routes are included, never
the connected or static routes of BIRD itself), and the route attributes `krt_mtu`, `krt_lock_mtu`. A size that comes or
goes changes the configuration, which is a `birdc configure` (the sessions are kept: `TestLearnedRoutesAreExportedIntoThePMTUMirrorTablesToo`).
Without protocols there is nothing to export and no mirror in the file.

**Apply and verify.** A route is `Table|Dst|Via|Dev|Type` (`routeKey`) plus the size (`routeSig`): the plan treats a
wanted route whose size differs as a route to **replace** (`ip route replace` changes the size in place), never as one
to delete and add, so a table whose size changes has no moment without its route. The verify compares the signature
(`missing 10.10.0.0/24 dev br-iot mtu lock 1280 table 103`; the lock itself is not visible in `ip -j`), the rules with their
fwmark (the preview line is `rule 950 fwmark 0x20000/0xe0000 iif  lookup 103`). Only routes with the executor's protocol tag
are ever deleted, so BIRD's routes in the mirror tables are left to BIRD. The nft side is verified like every chain: by
the hash of the rules.

**Removal found a defect.** The transaction deleted a removed chain before the map that names it with a verdict, and the real
kernel refuses that (`Could not process rule: Device or resource busy`): the last MTU fault's maps and chains go together.
`Nft.Transaction` empties a removed map before it deletes any chain; the simulated kernel now refuses to delete a chain that a
map element or a rule still names, and `TestTheLastMTUFaultTakesItsMapsAndChainsAlong` fails without the fix.

**Engine, API.** `Snapshot.PMTU` and `PMTUTables`; the counters of an MTU overlay or configured fault are the packets classified
into it in both directions (`counters` of the overlay: the same `Counter` as an impairment's; the black hole's drops are the counter
`pmtu_<hash>_drop` of the fault, read with the others by `Engine.ReadCounters`). `explain` has the MTU family's winner as
for every family and `kernel.pmtu_table` (the index; absent for a black hole, a clamp and when no MTU fault matches; `fault_id` and the
marks are absent when only an MTU fault matches). `GET /capabilities` lists the family and the feature `faults.mtu`. A new
overlay or revision whose MTU faults need an eighth table is refused with `capacity_exceeded`, in the preview as well.

**Where it is weaker than it looks** (decided by the maintainer on 2026-10-11, P2-M10-04).

- The black hole drops in the classify chain (prerouting), before the access rules of M9 run in the forward chain (plan §2.4 and the
  spec's `explain` say access rules are evaluated first and name this as the one exception; the drop sits there because the classify chain is the one place that knows the winner without a mark bit, and mark
  bits 17-19 are the index of the seven mirror tables, so they cannot also carry black-hole sizes): a packet that
  is both too long and rejected by a rule (tcp reset, icmp unreachable) is dropped silently instead of being rejected, and its drop is counted by the MTU fault;
  drop rules are not affected (both drop). The alternative, a second lookup in the forward chain after the access rules, needs a
  second set of maps and a mark bit or a second table of sizes.
- A clamp's MSS is `size - 40`, the headers of IPv4 and TCP without options: a connection with options (timestamps: 12 bytes) sends
  packets shorter than `size`, one without options exactly `size`, so a clamp never produces a packet longer than the size, but it
  does not make every packet that long.
- The verify checks the size of a mirror route and not the lock, because `ip -j route` does not print whether `mtu` is locked: a route
  whose lock was cleared behind the gateway's back (same size, no lock) is not noticed until the next apply, which rewrites it. (The
  alternative is the executor reading mirror routes with the plain `ip -d route`.)
- `meta length` is the length of the skb, so a black hole is only right where the kernel does not merge packets (GRO). The compiler
  switches the offloads off on the ports, the bridges and the uplink (plan §3.4), and a WireGuard interface has none.
- An icmp fault limits what the gateway forwards. A packet to the gateway itself (DNS proxy, UI) is not routed through a mirror
  table; the answers of the DNS proxy come back through `svc0` and are classified, but the service rule (900) wins over the PMTU rule
  (950) for traffic selected for a service.
- IPv4 only, like the rest.

### Tests

The compiler: `internal/compiler/extended_test.go` (E9, the draw and the leaf that never duplicates, the
hook's interfaces, the bit, the phase of a flapping, every shape side by side, the mark chains as golden)
and the scenario `faults-shapes` (a golden file; also in the kernel gates). The gates run the new shapes
and the hook's table on the real kernel: `TestEveryCompiledRulesetIsAcceptedByTheKernel` (nft),
`TestEveryCompiledTCTreeIsAcceptedByTheKernel`, `TestTheNormOfEveryNetemShapeIsWhatTheKernelReports`
(tc; the duplicating shapes are in the main list now, not on an interface of their own) and
`TestTheKernelAcceptsTheDuplicationHookAndReadsItBackAsTheVerifyExpects` (the executor's transaction, its
replacement by another set of interfaces, its deletion, an interface that does not exist).

The apply on the simulated kernel: `internal/apply/dup_test.go` (the table before the classification, a
re-apply that writes nothing, its removal after the transaction while the tree stays, damage found and
repaired, a table nobody wants, the preview), `tcapply_test.go` for the phase of a flapping. The executor:
`internal/executor/nftdup_test.go` (the operation, the transaction, the rule, the scope, the read). The engine:
`flap_test.go` and `flap_internal_test.go` (the schedule on the fake clock to the exact boundary, a jump over
cycles, a failed toggle, a replaced fault, a configured fault), `extended_test.go` (the hook with a write, the
preview). The API: `TestTheQueuesOfAFlappingFaultShowTheirPhase`. The domain: `invalid_rate` and the validation
rules the extended faults have (`validate_fault_test.go`, `overlay_test.go`), E9 through the store
(`internal/overlay/precedence_test.go`).

The measurements on the real kernel, `internal/engine/integration_faults_extended_test.go`, one test per
fault type, each with the isolation of a flow the fault does not name (plan §4.3); run in the persistent VM with
`make vm-test ARGS='-run "TestADuplicating|TestAReordering|TestACorrupting|TestABurstLoss|TestABlackout|TestAFlapping|TestAnIotRate|TestAnExplicitQueue" -tags testbed -test-timeout 170m -vm-timeout 4h ./internal/engine'`
(about 4 minutes each, emulated; the test binary is built on the host, so a kernel other than the default is
`vm up -kernel 7.0.0-38-generic` first). What is asserted where: the functional assertions always (the effect is
there, in its direction, the flow that is not named is untouched), the accuracy ones with native execution or
KVM (`testbed.Accurate()`), with the flakiness policy (a failed attempt is measured again).

| Fault type | Functional (always) | Accuracy (§4.3, `Accurate()`) |
|---|---|---|
| duplicate | copies of the datagrams (upload) or of the answers (download) only, none in the other direction, another device's delay fault intact next to it, the hook there and gone | share of duplicates in the 99.9 % binomial interval of the configured one (N = 2000) |
| reorder | datagrams arrive after later ones, none lost or duplicated | share sent at once in the interval of the configured one |
| corrupt | datagrams lost, the receiver counts checksum and header errors, never more than the loss | share lost in the interval of the configured one times (1 - 6/72) (a flipped bit of the frame's source address is not caught) |
| burst loss | loss, in runs of more than 1.5 packets, none in the other direction | losses in the Gilbert-Elliott interval (`GilbertLossBounds`, with the autocorrelation of the model) and runs of about 1/r (`CheckBurstLoss`) |
| blackout | nothing gets through, also a stream that was running; the queues count the drops; the next write ends it | |
| flapping | the engine's toggles are the exact distance of the cycle apart and never early; at least two complete outages of about the down time | each toggle within `FlapTolerance` of its time; the outages of the stream (down time, cycle) within that plus two intervals |
| rate (D18) | each of two devices at 1.4 to 2.4 Mbit/s in each direction (a shared queue would give each 1), the device of another network above three times the limit | each within ±10 % of 2 Mbit/s; the other network's device above ten times the limit |
| queue limit | tail drops (at least 200 of a burst of 300), a probe waits below 600 ms; without the limit nothing is dropped and a probe waits more than a second | |

### What the persistent VM showed, on both kernels of the matrix

The functional assertions passed on **6.8.0-142** and on **7.0.0-38** (software emulation, 2 CPUs; the
accuracy assertions of plan §4.3 do not run there and are the first thing CI verifies). The measured
distributions the tests log, for the record (one run each, 600 probes 20 ms apart unless said otherwise;
emulation adds tens of milliseconds of noise to delays, which is why the timing figures are not asserted):

| | 6.8.0-142 | 7.0.0-38 |
|---|---|---|
| upload duplicate 10 % | 59 of 600 (9.8 %), the echo answers each copy | 58 of 600 (9.7 %) |
| download duplicate 20 % | 127 copies of answers, none of datagrams | 117 |
| reorder 25 % + 50 ms (300 probes) | 81 sent at once (27 %), 109 arrived late | 70 (23 %), 102 |
| corrupt 10 % | 54 lost (9.0 %), the server counted 46 header and checksum errors | 59 lost (9.8 %), 45 |
| burst loss p 5 %, r 25 % | 97 lost (16.2 %; the model says 16.7 %) in runs of 3.9 (model: 4) | 140 (23.3 %) in runs of 4.2 |
| flapping 6 s up, 4 s down | outages of 3.6, 3.2 and 3.85 s | 4.1, 3.3 and 3.65 s |
| 2 Mbit/s per device, two devices at once | download 1.78 and 1.87, upload 1.86 and 1.90 Mbit/s | 1.89 and 1.90, 1.90 and 1.92 |
| queue limit 20 at 1 Mbit/s, a burst of 300 | 233 dropped, slowest probe 165 ms (the model: 165) | 266 dropped, 158 ms |
| the same without a limit | nothing dropped, slowest probe 2.08 s (model: 2.4) | 2.29 s |

Both burst-loss runs lie inside the 99.9 % interval of the model (`GilbertLossBounds`: 4.7 % to 28.7 % of 600
packets, 10.1 % to 23.2 % of 2000 for these parameters), which is wide because the losses come in bursts: a run
of 600 probes has about 25 of them.

**Kernel matrix.** The kernel gates (`TestEveryCompiledRulesetIsAcceptedByTheKernel`,
`TestEveryCompiledTCTreeIsAcceptedByTheKernel`, `TestTheNormOfEveryNetemShapeIsWhatTheKernelReports`,
`TestTheKernelTakesTheLongestValuesTheAPIAccepts`, `TestTheKernelAcceptsTheDuplicationHookAndReadsItBackAsTheVerifyExpects`)
and the eight measurement tests ran on both. A kernel other than the default is `make vm-down`, then
`go run ./tools/testvm vm up -kernel 7.0.0-38-generic` (the guest uses the container's iproute2 6.19 and nftables
1.1.6 on both).

### Tests of the MTU family

Compiler (`internal/compiler/pmtu_test.go`): the mark bits overlap nothing, an icmp winner with its chain, jump, map, mirror
routes and rule, black hole and clamp without a table, no MTU fault leaves the target as it was, the families resolve
independently (a device's winner beats the network's, a destination refines it, an impairment next to it), seven sizes fit and
the eighth is `capacity_exceeded`, a size keeps its table, BIRD gets one kernel protocol per mirror, a hostname selector is
reported; the golden `pmtu.golden.txt` (`TestGoldenPMTUTarget`: maps, chains, mirror routes and rules). The kernel gate
`TestEveryCompiledRulesetIsAcceptedByTheKernel` has the scenario `pmtu`; `internal/bird` tests the mirror configuration
(and `bird -p` parses it), `internal/executor` the line `mtu lock N`, its validation and, on the real kernel, the write, the
read-back, the replace in place, the rule on the mark and `ip route get ... mark` (`TestARouteWithALockedPathMTUIsWrittenReplacedInPlaceAndDeleted`).
The apply on the simulated kernel (`internal/apply/pmtu_test.go`): the mirror and its rule, a size that changes without a
delete, removal, the last fault taking its maps and chains along, damage found and repaired, the preview. The engine
(`internal/engine/pmtu_test.go`): the table in the kernel when the write returns and its removal, the eighth size refused with
nothing changed, explain, the preview of eight configured faults.

Real kernel (`integration_pmtu_test.go`, `integration_pmtu_wg_test.go`), run in the persistent VM with
`make vm-test ARGS='-run "TestAnIcmpMTUFault|TestABlackholeDrops|TestAnMSSClamp|TestTwoMTUFaults|TestAPMTUFault|TestLearnedRoutesAreExported" -tags testbed -test-timeout 90m -vm-timeout 3h ./internal/engine'`.
There is nothing statistical in them (a transfer completes or stalls, the kernel answers or it does not, a segment has a size), so
the same assertions run under emulation and on a native or KVM kernel.

| Test | What it shows |
|---|---|
| `TestAnIcmpMTUFaultMakesTheKernelAnswerLargePacketsAndTheTransferCompletes` | A (1280, icmp): the DF ping of 1400 bytes gets "Frag needed and DF set (mtu = 1280)" from the gateway, 1200 bytes go through, the control B is not limited (measured first, as in S13), A's 300 KB transfer completes, the server's route cache for the gateway holds `mtu 1280` (the download is cut: the documented side effect), the mirror table has only locked routes and the rule is on the mark, the counters count, and when the overlay goes the table is gone and A is no longer limited |
| `TestABlackholeDropsLargePacketsSilentlyAndTheTransferStalls` | A (1280, blackhole): the large ping gets nothing back (no ICMP, no mtu in the output), the small one passes, B passes, A's transfer receives 0 bytes in 8 s while B's completes, the drop counter counts, the server has no cache entry, no mirror table; the transfer completes after the overlay is deleted |
| `TestAnMSSClampLimitsTheSegmentsOfTheSelectedDeviceOnly` | A (1400, clamp): the connection negotiates 1348 on both sides (1360 minus the 12 bytes of timestamps), B 1448; a ping of 1472 bytes from A is not touched; the server's cache stays empty |
| `TestTwoMTUFaultsAndAnImpairmentOfTheSameDeviceDoNotInterfere` | A (icmp 1280) with an impairment, B (blackhole 1000), C nothing: each is limited by its own fault only, the delay of A is still there |
| `TestAPMTUFaultAppliesToTrafficThroughATunnel` | a fault on a network limits traffic into a WireGuard client network: a packet that fits the tunnel (1420) but not the fault is answered with `mtu = 1280`, and what fits goes through; the mirror table holds the route into the tunnel |
| `TestLearnedRoutesAreExportedIntoThePMTUMirrorTablesToo` | BGP over a link: the learned route is in table 103 with `proto bird` and the size locked, traffic to the learned network is limited, a second size feeds table 104 without losing the session, a size that goes empties its table, the main table never has the route |
| `TestPMTUFaultsOfTheThreeModesHoldATCPTransferThroughATunnel` | the 300 KB transfer of S13 with a WireGuard interface (MTU 1420) on the way, from a device of the lab to a host in the client's network and back: before any fault both devices complete with segments of 1368; icmp 1280 (the mirror table holds the tunnel route, a large DF ping gets `mtu = 1280`) completes with segments of at most 1240 (the tunnel's own MSS clamp reads the locked route), the control with 1368; black hole 1280 stalls A (0 bytes in 8 s) while B completes, the drops are counted; MSS clamp 1400 completes with 1348 on both sides, the tunnel's clamp to the interface's 1420 does not undo it, B keeps 1368 and a large ICMP packet of A is not touched |

Kernel matrix: the gate (`TestEveryCompiledRulesetIsAcceptedByTheKernel`, scenario `pmtu`), the route test of the executor and all seven real-kernel tests passed on **6.8.0-142** and on **7.0.0-38** (`go run ./tools/testvm vm up -kernel 7.0.0-38-generic`). What the persistent VM showed (6.8.0-142, emulation, one run each): the transfers of 300 KB took 0.4 to 1.0 s; the black hole
received 0 bytes in 8 s. One defect was found: the removal of the last MTU fault was refused by the kernel (busy chain), see
above. Two assertions of the tests were wrong and fixed: the client's Python thread printed a traceback into the JSON when the
black hole stalled it, and a device that was told a path MTU keeps it in its own route cache (the tests flush it).

### Tunnel faults and WireGuard actions (plan §2.2.1, spike S15)

A tunnel fault impairs the encrypted UDP of one WireGuard peer, a hub client or the remote side of a link, so everything inside
the tunnel is impaired, routing sessions included, and the fault stacks with the faults of the traffic inside it (E10). It is a
fault of its own family (`family: tunnel`, `tunnel: {client|link}`, latency, jitter, loss, burst loss, blackout, flapping; no
selector, no source) resolved per tunnel (`domain.World.ResolveTunnels`: overlays before the configuration, the newest wins, the
parameters are not merged). The WireGuard actions are overlays of the kind `wireguard`. Both were `unsupported_feature` until M10.

**What the VM proved first** (kernel 6.8.0-142, iproute2 6.19, nftables 1.1.6; the constructs of the gates run on 7.0.0-38 as well):

- `tc qdisc replace dev X ingress` creates the ingress qdisc and takes over one that exists (`add` twice: `Exclusivity flag on`);
  `tc filter replace dev X parent ffff: protocol ip prio 10 handle N flower ip_proto udp src_ip A src_port P action mirred egress
  redirect dev ifb-cgw` is accepted twice and changes the filter in place (a new selector with the same handle: the action's
  counters start again, an unchanged one keeps them); the same filter with `flowid 1:M` below an HTB root selects a class
  (`classid` and `flowid` are both read; the executor writes `flowid`, because `classid` is among the words it refuses).
- `tc filter show dev X` lists the egress filters only; the ingress ones are `tc -s -j filter show dev X ingress`, which prints
  `[]` for a device without an ingress qdisc too. An ingress filter is listed with `"parent":"ffff:"`, `options.handle`,
  `options.keys.{ip_proto,src_ip,src_port}` and `options.actions[0]` (`mirred`, `redirect`, `egress`, `to_dev`) with
  its own `stats`. Deleting by `handle N protocol ip prio P flower` works; deleting an HTB class that a flower filter
  selects is `HTB class in use`.
- A packet marked in a base chain on the **output** hook of the `inet` table (`ip daddr . udp dport vmap @map`, element
  `goto chain`) takes the class the mark selects on the interface it leaves through; the plain underlay traffic of the same hosts
  is not touched. The encrypted packets of a tunnel have a conntrack entry of their own, in whichever direction the peer spoke first,
  so the direction bit of the tunnel's mark is written by the chain (a tunnel is always "downstream" towards the peer) and not from
  `ct direction`.
- `ip link add name ifb-cgw type ifb` is `File exists` the second time; `ip link del dev wg-a type ifb` **deleted a WireGuard
  interface**: the kind has to be read first (as for the bridges).
- The ifb module must be loaded: the persistent VM's first minutes have no `ifb`, a job that starts right after the boot may see
  `Operation not permitted`.

**The design.**

```
packets towards the peer (download)         packets from the peer (upload)
 output hook, prio -150, table inet chaosgw   ingress qdisc of the uplink (ffff:)
   tunnel_out: ip daddr . udp dport vmap      flower: udp, src_ip, src_port -> mirred redirect to ifb-cgw
   tun_<id>: mark = mark & 0xfffe000f           ifb-cgw: HTB root 1:, class 1:<minor>, netem leaf
             | id << 4 | 0x10000; counter                 flower (same selector) -> flowid 1:<minor>
 -> the fw filters of the interface's tree -> the class of the fault -> its netem leaf
```

- **The directions are the remote side's** (decided by the maintainer on 2026-10-11, P2-M10-05; plan §2.2.1 says the output hook
  classifies the encrypted UDP "towards the peer" by its endpoint and an IFB handles the packets "from the peer", and does not say
  which direction is which): `upload` is what the client or the remote site sends, `download` what it receives, as for a
  device. The alternatives that were not chosen: the directions the other way round (upload as the gateway sends), a selection by
  the interface's listen port for a link (no address needed, but it does not follow a roaming peer), and the ingress side on every
  interface instead of the uplink only. `Fault.Upload` is the IFB class, `Fault.Download` a class of the interfaces' tree (one per interface, like every
  other fault). Ids come from the same 12-bit pool and the same allocation as the other faults (`overlay:<id>:tunnel` keys,
  stable across compiles), the class minors are `ClassIDOf(id, dir)`, so queue statistics, epochs, the retirer and the flapper
  treat them like any class.
- **The peer's address** (part of the decision P2-M10-05 of 2026-10-11; `compiler.Input.PeerEndpoints`, `Target.Endpoints`): the engine reads `wg show dump` of the applied
  interfaces right before it compiles when `compiler.NeedsPeerEndpoints` says something selects by address (a tunnel fault or a
  blocked endpoint, in the configuration or in an overlay), in the apply loop, in the preview and when an overlay is written
  (so the check of the write, `capacity_exceeded` included, compiles what the apply will). A link's configured endpoint is the
  fallback when it is an address. `PollWireGuard` finds a peer that moved (`roamed`: the endpoint in `Snapshot.PeerEndpoints`
  differs from the one seen) and makes a new desired state, a full apply; a peer with no address is in `Target.Endpoints` with
  `""`, so the poll applies as soon as it is seen. The overlay of a fault that is not compiled shows `disabled`.
- **Only IPv4 endpoints** (`compiler.UsableEndpoint`): WireGuard listens on both families, so `wg show` can report a peer at
  `[2001:db8::1]:5000`. The flower filter, the `tun_out` and `wgblk_*` maps (`ipv4_addr`) and the executor's grammar are IPv4
  only, so an endpoint is usable when it is an IPv4 address with a port (IPv4 in its IPv6 form is unmapped); any other is
  treated like no address: the `tunnel_endpoint_unknown` warning, the overlay shows `disabled`, the apply goes on without the
  fault or the block (the configured endpoint of a link is not used either: that is not where its packets go), and the poll
  does not apply again for a peer that stays there (`roamed` compares what the compiler would use). The fault takes effect when
  the peer is seen at an IPv4 address.
- **Two tunnels at one endpoint** (`tunnel_endpoint_shared`, a warning): a stale endpoint or a NAT that reuses a mapping can
  give two peers the same address and port. A verdict map cannot hold a key twice (`nft`: `Could not process rule: File exists`,
  tried on 6.8.0-142, and the transaction is refused) and the packets cannot be told apart, so the tunnel that sorts first by
  its key (`client:` before `link:`, then the id) keeps its fault or block and the later one is left out with the warning.
- **The target** (`compiler/tunnel.go`): `Target.IFB` (`Dev`, `Uplink`, `TC`: an ordinary `TCTarget` whose classes carry the
  peer's `Endpoint`, so that `TCClass.FilterEntry` writes a flower filter with the fault id as handle where the other trees
  have an fw filter) with `IngressEntries` (the ingress qdisc and a redirect filter per fault, pref 10, handle = id) and
  `IngressNorm`. `Target.TCTrees()` and `TreeOf(dev)` are the two trees; everything that walked `Target.TC` (apply, verify,
  queues, flapper) walks both. The IFB has its own class limit (default class plus one per fault; `capacity_exceeded` names
  the scope), and takes `ifb-cgw` into `Target.Interfaces`.
- **Flapping** is the existing flapper: both sides carry flap keys of the fault (`|upload`, `|download`), the toggle replaces the
  leaves on the interfaces of the tree and on the IFB in one pass, and the phase the engine holds is written into both trees by
  every compile.
- **The apply** (`apply/ingress.go`, `plan.go`, `retire.go`): the IFB is created (`add_ifb` if it is missing, a probe makes it
  idempotent) and brought up before its tree; the ingress qdisc and the filters are the switch of the direction from the peer
  and stand between the classes that are created or changed and the nftables transaction; a filter is `replace`d only when its
  selector or redirect differs (its counters would start again; `Plan.IngressRestarted` tells the engine, which starts a new epoch
  for the fault's counter). A filter of the uplink's ingress qdisc is Chaos Gateway's when it is a flower filter that redirects
  to the IFB (`ownIngressFilters`); the host's own are left alone and the ingress qdisc goes only with the last filter of ours
  (decided by the maintainer on 2026-10-11, P2-M10-07: one IFB, `ifb-cgw`, the only name the executor creates, brings up or
  deletes and the only target of a flower redirect; it exists while some tunnel fault impairs the packets from a peer; the
  kernel needs the `ifb` module, which the preflight lists; the alternatives were one IFB per uplink, and deleting the
  ingress qdisc only when the gateway made it, which would have to be remembered across a restart). When the last fault that impairs the packets from a peer ends, its filter goes at once, the class and the IFB wait for the
  retirer (the IFB stays in the executor's scope: `keepIFB`), the retirer deletes the tree and then the device (`Retirer.Reap`), and the
  next apply takes it out of the assigned interfaces. Without a retirer (the one-shot apply) the device goes at the end of
  the plan, after everything that refers to it. An IFB that nobody wants and nobody assigned (a gateway that died) is
  assigned for the duration of the plan, and deleted with its tree. A device named `ifb-cgw` that is not an IFB is refused.
  The verify compares the IFB link (exists, kind, up), its tree and the ingress side.
- **The executor** (`executor/op.go`, `tcgrammar.go`, `exec.go`): `links` takes `add_ifb`, `delete_ifb` (the name `ifb-cgw` only,
  the kind read before a deletion) and `up`; a flower filter is a closed grammar, either `protocol ip prio N flower ip_proto udp
  src_ip A src_port P action mirred egress redirect dev ifb-cgw` below `ffff:` or `... flowid 1:M` below `1:`, with a decimal
  handle, an IPv4 address and a port; the scope check sees the redirect target as an interface that has to be assigned. The
  tc read adds the ingress filters of a device that has an ingress qdisc (`linux.NormalizeTCIngress`, `NormFilter.Flower`,
  `NormTree.Ingress`).
- **The counters**: the packets towards the peer are an nft counter of the chain `tun_<id>` (`Fault.CounterDown`); the packets
  from the peer are the packets the redirect action of the ingress filter has matched, read with the tc state of the uplink
  (`Engine.ReadCounters` reports them under `Fault.CounterUp`), before any netem has dropped or delayed one, so that a blackout
  counts what it swallows. The queues show the drops per class (the IFB's for the upload, the interfaces' for the download).
- **Explain** has a `tunnel` entry per tunnel the traffic crosses (`domain.World.TunnelsCrossed`: the client or link the
  source or the destination lies behind by the configuration, and, for a route BIRD learned, the link the kernel's route leads
  into), with the winner and what it overrode, and `kernel.tunnels` (fault id, endpoint, mark).

**The WireGuard actions** (P2-M10-06) are decided where the interface is compiled (`compileWireGuardNetwork`): `disable` leaves
the peer out (the same as `enabled: false`: its routes go with it), `key_mismatch` gives the peer a key derived from the
overlay, `block_endpoint` is nftables: a base chain on the output hook and one on the input hook at priority -300 (raw), each a
verdict map keyed on peer address, peer port and the interface's port, one chain with one named counter per overlay (`wgblock_<id>`).
`Target.WGActions` lists the overlays that change something; the API shows `effective` for them and `disabled` for the others (a
peer that is off its interface already, a block with no address), and the counter of a block. Decided by the maintainer on
2026-10-11 (P2-M10-06): `disable` is the configured `enabled: false` (the peer is off the interface, so are the routes of its networks
and, for a link, the peer, and the engine reports the peer offline at its next poll); `key_mismatch` gives the gateway the key
sha256("chaosgw key_mismatch", overlay id, real key) in base64, so that a compile stays a pure function, a re-apply keeps the kernel as
it is, and nobody holds the private key; `block_endpoint` keeps the peer on the interface (its handshake ages) and cannot select a peer
whose address is not known (the compiler warns, the overlay shows `disabled`, an overlay whose peer is not on an interface shows
`disabled` too). A random key, `block_endpoint` by a link's listen port (works without an address) and a `disable` that keeps the
routes were not asked for.

**Tests.** Compiler (`tunnel_test.go`): both sides of the tunnel, the endpoint map, the chain and its mask, a peer without an address,
the resolution per tunnel, E10 (golden `e10`), flapping on both sides, the class limit of the IFB, disable, key mismatch, block endpoint
(golden `wgblock`), a target without any of it, the scenario `tunnel` (golden `tunnel`, also in the kernel gates), E9 as a golden
file (`e9`). Executor (`ifb_test.go`, `integration_ifb_test.go`): the grammar, the scope, the plan, the kind check, the ingress
read, the whole life of an IFB and of the filters on a real kernel. Linux (`tcnorm_test.go`): listings recorded on a real kernel.
Apply (`apply/tunnel_test.go`, simulated kernel): the order of the plan, a peer that moves, the end of a fault with the IFB
waiting for the retirer, the one-shot apply, leftovers (with a retirer and without), a filter of the host, damage found and repaired,
a device that has the IFB's name, the preview. Engine (`tunnel_test.go`): the write that reads the address, a peer that has not connected
and one that moves, the end of a fault, flapping, the class limit, explain, the actions, the counters. API (`tunnel_test.go`): state,
counters and queues, explain, the actions' states. Overlay (`precedence_test.go`): E10 through the store. The gates
`TestEveryCompiledRulesetIsAcceptedByTheKernel` (scenario `tunnel`) and `TestEveryCompiledTunnelTreeIsAcceptedByTheKernel` (the
trees and the ingress side, twice, the normalized state compared with the prediction, the deletions).

Real kernel (`engine/integration_tunnel_test.go`), run in the persistent VM with
`make vm-test ARGS='-run "TestATunnelFault|TestE10TheDeviceFault|TestATunnelBlackout|TestAFlappingTunnel|TestAClientThatMoves|TestTheIFBAndItsFilters|TestWireGuardActionsCut" -tags testbed -test-timeout 170m -vm-timeout 4h ./internal/engine'`:

| Test | What it shows |
|---|---|
| `TestATunnelFaultImpairsEverythingInTheTunnelAndNothingElse` | the flows of two devices into the client's network and the flow the client's network starts are impaired in the directions of the fault (latency, loss); a flow to the server through the same interface and a flow from the client machine to the gateway's uplink address are not; the counters of both directions, the queues of both sides, the handshake goes on. Accuracy (§4.3) with `Accurate()`, the flakiness policy |
| `TestE10TheDeviceFaultAndTheTunnelFaultOfTheClientAddUp` | E10: 40 ms of the device and 50 ms of the tunnel are 90 ms for the device's flow into the tunnel, 50 ms for another device through it, 40 ms for the device's flow to the server |
| `TestATunnelBlackoutCutsTheTunnelInTheDirectionItIsWrittenForAndNothingElse` | upload alone: the requests arrive, no answer; download alone: nothing arrives; both: nothing in any flow, the tunnel's ping included; flows outside unaffected; drops and counters; the tunnel returns by itself |
| `TestAFlappingTunnelBlacksOutOnSchedule` | the toggles of both sides within `FlapTolerance`, the outages of the flow within the tolerance (accurate), whole outages of about the down time (emulated; the emulated run is counted in toggles, below) |
| `TestAClientThatMovesTakesItsTunnelFaultWithIt` | the client listens on another port: the poll finds it, the filters move, the flow is impaired again |
| `TestTheIFBAndItsFiltersAreCreatedAndRemovedWithTheTunnelFaultsAndLeftoversAreCleanedUp` | a leftover IFB, tree and filter go with the first apply and the host's filter stays; the IFB comes with the first fault and goes with the last |
| `TestWireGuardActionsCutTheTunnelAndEndWithTheirOverlay` | disable (peer off the interface, offline event), key mismatch (another key on the interface), block endpoint (the counter counts); each ends with its overlay and the tunnel returns; flows outside unaffected |
| `TestATunnelBlackoutOnABGPLinkWithdrawsTheLearnedRoutesAndTheReconvergenceIsReported` | the routes are withdrawn within the hold time (9 s) and the margin; the blackout ends when both sides have dropped the session; the routes return within 15 s of the answer of the write that ends it (accurate; 90 s emulated); the times are logged, taken from the events of the routing poll and written into the measurement record |

**What the persistent VM showed** (software emulation, 2 CPUs, so the accuracy assertions of §4.3 did not run and CI verifies them;
the functional ones passed, one run each unless said):

- Impairment through the tunnel, 5 % loss and 150 ms towards the peer and 30 ms from it: 14 to 19 of 300 probes lost per flow
  (4.67 %, 3.67 % and 5.0 % in one run, 5.0 %, 6.0 % and 6.33 % in another), only in the direction the loss was configured for, medians
  of the delays above the configured ones by the noise of the machine (tens of milliseconds).
- Blackouts: with the upload alone 60 of 60 requests reached the echo of the client machine and no answer returned, with the download
  alone none reached it; with both nothing passed in the flows of two devices, the flow the client's network starts, or the tunnel's
  own ping, while a flow to the server through the same uplink and a flow from the client machine to the gateway's uplink address
  were unchanged.
- Flapping, 6 s up and 4 s down: outages of 3.8, 3.3, 3.9 and 2.85 s (probes every 50 ms); the toggles of both sides were one `tc`
  batch each. Before the toggles were one batch per interface, a toggle was 16 s late (six runs of the tool at about 2.5 s each).
  The emulated run of the flapping tests is counted in toggles, not in probes (`emulatedFlapRun`, P2-AUDIT-14): it goes on until
  the engine has committed two whole down phases after the first probe arrived. A fixed run of 700 probes 50 ms apart held only one
  whole outage on an emulated hosted runner (weekly run 38158196621: one outage of 2.9 s, 58 of 700 lost), since there the sender
  and the toggles fall behind their schedules by seconds: in the persistent VM on 2026-10-12 the sender sent 337 probes in 27 s
  instead of 540, and the toggles of the tunnel test were committed 1.5 to 3.8 s late (one `tc` batch on six interfaces). With the
  toggle-counted run both flapping tests passed there (outages of 3.35 and 4.15 s through the tunnel, 3.8 and 4.4 s for a device);
  each test logs every toggle with its lateness. The first weekly run of the engine job (38271140920) then failed the tunnel test
  for another reason: the schedule skipped two whole cycles, the toggle back up was scheduled 24 s after the one into the blackout
  instead of 4 s (every toggle was committed within 0.25 s of its time), so the flow saw one outage of 24 s. The flapper makes a
  boundary that falls into an apply right after it, in the phase the clock says (`flap.go`), so an emulated apply that takes longer
  than a phase skips the boundaries in between. The second run (38283373614), with the applies and the engine's log in the test's
  output, showed the apply: "an identity update needs a full apply ... the number of devices changed from 6 to 8" (the gateway
  meets more devices of the lab while the flapping runs; on that branch a new device was still a full apply, which P2-AUDIT-01 has
  since made an element update unless the device needs a queue of its own), answered 13 s later; it held
  one boundary 2.9 s and skipped one cycle, and the down phase whose start it held lasted 1.3 s, too short for the outage the test
  counted. The emulated branch of both flapping tests therefore accepts a distance between toggles that is longer by whole cycles,
  and an outage longer by them, only when an apply of a later generation than the flapping's own ran (`checkFlapSchedule`); the
  run counts only down phases of at least half the down time between the commits of their toggles; and an outage counts from a
  quarter of the down time (the sender falls behind as well). The tests log the applies and the changes that start one, and the
  tunnel test the engine's warnings and apply messages when it fails. With native execution or KVM the schedule stays exact.
- The BGP link: the session went down and the routes were withdrawn, and came back after the blackout. The write of the overlay
  took 84 s on this machine (an emulated apply of the whole lab is that slow: a write of a device fault took 1 min 44 s next to
  2 min 20 s for the tunnel fault, the larger plan), so the blackout had held for most of the hold time when the write was answered;
  the test therefore takes the times from the call and from the answer and from the events, and bounds the withdrawal by the
  protocol timers (6 to 9 s after the blackout began) plus the length of the write. On a native or KVM machine S15's figures are
  the expectation: withdrawn after about 6 s, back a few seconds after the blackout ended (S15: 2.5 s; CI: 4.0 s natively, 1.1 s
  under KVM after the answer of the write, the session down for 5.5 and 6.0 s).

**BGP re-convergence after a blackout (P2-AUDIT-12).** Before 2026-10-12 the routes came back 39 to 56 s after a tunnel blackout under
KVM and 4 s natively, not after S15's 2.5 s. The cause was the remote side's BIRD configuration that `chaosgw wg export --bird` writes
(`bird.RenderRemote`): it had the gateway's hold and keepalive times but BIRD's defaults for the rest, `error wait time 60, 300` and
`connect retry time 120`. A hold timer that runs out is a protocol error in BIRD (`Hold timer expired`); the protocol starts again only
after the error wait time and refuses incoming connections until then (state `Idle`, "Error wait: x/60"). The gateway's side had
`connect retry time 5; error wait time 1, 30;` and tried every 5 s, and the remote side reset each connection until a minute after its own
hold time had run out (the BIRD states of both sides, watched every 0.5 s in CI: the remote side `Passive` again at 62 s, the session up
at 66 s). Natively the blackout usually ended right after the gateway's hold time ran out and before the remote side's did: the remote
side then lost its old connection to a reset and started again without an error wait, which is why the native figure was 4 s; when the
remote side's hold time ran out as well, the native run took 53 s too. `RenderRemote` now gives the remote side the gateway's reconnect
timers (`bird.reconnectTimers`, also the active side's connect retry when the gateway is passive), and the test ends the blackout only
when both sides have dropped the session, which is the case that took a minute, and bounds the re-convergence: the remote side is
`Passive` 1 s after its hold time ran out, the gateway connects 5 s (BIRD's connect delay) after it started again and every 5 s while
the tunnel is dead, and WireGuard sends a handshake every 5 s once it has had no answer for 15 s, so 15 s after the answer of the write
that ends the blackout is the accurate bound.
- The roaming test found the client at its new port after one apply; an apply took minutes here, and the engine made one desired
  state for it, not one per poll (`TestAPeerThatRoamsMakesOneDesiredStateWhileTheApplyForItIsOnItsWay`: before the rule, the polls made
  six).
- A flow the fault does not name was measured at 13 ms before a fault and at 41 ms and 397 ms in the same run, while the fault was on
  another flow: the emulated machine's own load. The functional isolation assertion of these tests (`isolated`) therefore has the
  flakiness rule of §4.3 (a failed attempt is measured once more), as the accurate branch always had.

**Kernel matrix.** The gates (`TestEveryCompiledRulesetIsAcceptedByTheKernel` with the scenario `tunnel`,
`TestEveryCompiledTunnelTreeIsAcceptedByTheKernel`, `TestEveryCompiledTCTreeIsAcceptedByTheKernel`), the executor's
`TestTheIFBAndTheFlowerFiltersOnARealKernel` and the nine real-kernel tests above ran on **6.8.0-142** and on **7.0.0-38** and passed
on both (7.0.0-38: the tunnel fault 150 ms / 5 % loss in the same three flows, flapping outages of 3.5, 4.4, 3.7 and 2.4 s for a down
time of 4 s, the BGP session down 40.7 s and up again 36.4 s after the calls of the writes, on a machine that took 69 s over the write).
A kernel other than the default is `make vm-down`, then `go run ./tools/testvm vm up -kernel 7.0.0-38-generic`.

**Accuracy assertions of M10, reviewed by hand before CI** (the persistent VM, 6.8.0-142, emulated: the 24 M10 tests of
`internal/engine`, the gates and the other packages with testbed tests pass; the 55 other testbed tests of the engine package pass except
`TestAConfigurationChangeKeepsTheSessionAndAnInvalidSnippetIsRefused`, which fails on `main` in the same VM with the same line, a
1 ms difference in BIRD's "since" time; it showed once on level 1b with KVM too, and since 2026-10-12 the test takes two
times within 10 ms as the same, since BIRD converts its monotonic time to the wall clock at every listing). The assertions that run only with `Accurate()` are the shares of duplicates, reordered and
corrupted packets (99.9 % binomial interval, N of `impairedRun`), the Gilbert-Elliott losses and runs, the flapping toggles
(`FlapTolerance`) and outages (`CheckFlaps`: tolerance plus two probe intervals, at least three complete outages in a 19 s run of a
3 s up, 2 s down cycle), the tunnel latency and loss, the rate (±10 % of 2 Mbit/s), the BGP re-convergence bound (15 s, P2-AUDIT-12) and the BGP withdrawal bound (at most hold time
+ 5 s after the answer of the write). The one with the least margin is the rate (under the flakiness policy of §4.3 like the others, a tool failure of iperf3 is repeated once as well): netem counts the frame, so TCP payload shows as
1.91 Mbit/s and the lower bound is 1.8; the emulated VM measured 1.82 to 1.90. If CI reports a value below 1.8, read the logged
throughputs first; the bound is the plan's and is not to be widened without an open item.

### Acceptance map (plan M10 "Tests")

Every bullet of the plan's test list has a test, named here. The real-kernel ones are testbed tests of `internal/engine` (run them in
the persistent VM, the commands are in the sections above); the others run on the simulated kernel or without any. What is asserted
under emulation and what only with native execution or KVM is the split of "Tests" in the sections above: the functional assertions
(the effect is there, in its direction, nothing else changes) always, the accuracy ones of plan §4.3 with `testbed.Accurate()`, with the
flakiness policy (a failed attempt is measured once more, only the second failure fails). Every real-kernel test ran on both kernels of
the matrix (6.8.0-142 and 7.0.0-38).

| Plan bullet | Test |
|---|---|
| One measurement test per fault type | latency, jitter, loss and rate of a device, a group, a network, between two networks, through a tunnel and over a route BIRD learned: the M8b tests (`TestADeviceFaultImpairsThatDeviceAsConfiguredAndNoOther` and the others of `integration_faults_test.go`, `TestFaultsBetweenATestNetworkAndAWireGuardClientNetworkAreMeasuredPerDirection`, `TestAFaultOnTrafficOverARouteLearnedByBGPIsMeasuredPerDirection`); M10: duplicate `TestADuplicatingFaultDuplicatesAsConfiguredNextToOtherFaults`, reorder `TestAReorderingFaultSendsTheConfiguredShareAheadOfTheDelayedOnes`, corrupt `TestACorruptingFaultCorruptsAsConfiguredAndTheReceiverCountsTheChecksumErrors`, burst loss `TestABurstLossFaultLosesInBurstsAsTheModelSays`, blackout `TestABlackoutDropsEverythingOfItsDeviceAndTheQueuesCountIt`, flapping `TestAFlappingFaultBlacksOutOnSchedule`, rate `TestAnIotRateOfTwoMbitGivesEveryDeviceOfTheNetworkItsOwnTwoMbit`, queue limit `TestAnExplicitQueueLimitBoundsTheDelayAndDropsTheRest`, MTU the four tests of `integration_pmtu_test.go`, tunnel latency and loss `TestATunnelFaultImpairsEverythingInTheTunnelAndNothingElse` and `TestATunnelFaultOfALinkImpairsTheTrafficOverItAndNotTheOtherTunnel`, tunnel blackout `TestATunnelBlackoutCutsTheTunnelInTheDirectionItIsWrittenForAndNothingElse`, tunnel flapping `TestAFlappingTunnelBlacksOutOnSchedule`, the WireGuard actions of a client `TestWireGuardActionsCutTheTunnelAndEndWithTheirOverlay` and of a link (disable, block endpoint) `TestWireGuardActionsOnALinkCutTheLinkAndNotTheClientTunnelAndEndWithTheirOverlay` |
| Flapping timing within tolerance | on the real kernel every toggle of the engine is within `engine.FlapTolerance` of its time, never early, and the outages of the stream are the down time to the precision of the probe spacing (`CheckFlaps`): `TestAFlappingFaultBlacksOutOnSchedule` and `TestAFlappingTunnelBlacksOutOnSchedule`; on the injected clock to the exact boundary: `internal/engine/flap_test.go` (`TestAFlappingFaultStartsUpAndTogglesOnTheClock`, `TestAClockThatJumpsOverFlappingBoundariesLandsInThePhaseItSays`, `TestAnApplyDuringTheDownPhaseKeepsTheBlackout`) |
| PMTUD: 300 KB TCP transfer completes with ICMP, stalls in black-hole mode, the control device is unaffected (S13) | `TestAnIcmpMTUFaultMakesTheKernelAnswerLargePacketsAndTheTransferCompletes` and `TestABlackholeDropsLargePacketsSilentlyAndTheTransferStalls` (device A limited, device B the control, measured before and next to the fault; the stall is asserted as a stall, not as a slow transfer: nothing at all comes back, the client waited its whole timeout, the drop counter grew meanwhile and the control finished in under half of that time, `assertStalled`) |
| MSS clamp limits the segment size of the selected device only | `TestAnMSSClampLimitsTheSegmentsOfTheSelectedDeviceOnly` (the segments of both sides, the control, the other protocols of the device, the server's cache) |
| A tunnel fault affects everything inside that tunnel and nothing else | `TestATunnelFaultImpairsEverythingInTheTunnelAndNothingElse` (two devices' flows into the tunnel, the flow the client's network starts, the tunnel's own ping; not the flow to the server through the same interface, not the client machine's flow to the uplink address), `TestATunnelFaultOfALinkImpairsTheTrafficOverItAndNotTheOtherTunnel`, `TestATunnelBlackoutCutsTheTunnelInTheDirectionItIsWrittenForAndNothingElse` |
| ... and stacks with inner faults (S15, E10) | `TestE10TheDeviceFaultAndTheTunnelFaultOfTheClientAddUp` (40 ms of the device and 50 ms of the tunnel are 90 ms, 50 ms for another device through the tunnel, 40 ms to the server) |
| A tunnel blackout on a BGP link withdraws the learned routes, the re-convergence time is reported | `TestATunnelBlackoutOnABGPLinkWithdrawsTheLearnedRoutesAndTheReconvergenceIsReported` (the times are logged and taken from the call, the answer and the `routing_session_changed` events) |
| PMTU faults through a tunnel | `TestAPMTUFaultAppliesToTrafficThroughATunnel` (the kernel's answer, the mirror route into the tunnel), `TestPMTUFaultsOfTheThreeModesHoldATCPTransferThroughATunnel` (the three modes with the 300 KB transfer through a WireGuard interface, the control device), `TestLearnedRoutesAreExportedIntoThePMTUMirrorTablesToo` (the routes BIRD learned over a link) |
| Golden tests E9 and E10 | compiler: `TestE9ABadLTEProfileOnANetworkGivesEveryDeviceItsOwnQueueWithTheFullRate` (golden `e9.golden.txt`) and `TestE10ADeviceFaultAndATunnelFaultStack` (golden `e10.golden.txt`); through the overlay store: `TestE9ABadLTEOverlayOnTheNetworkReachesEveryDeviceOfIt` and `TestE10ADeviceFaultAndTheTunnelFaultOfAClientStack` (`internal/overlay/precedence_test.go`) |
| Per-device rate (D18): 2 Mbit/s on a network, two devices at once get 2 Mbit/s each (±10 %) | `TestAnIotRateOfTwoMbitGivesEveryDeviceOfTheNetworkItsOwnTwoMbit` (both directions; a third device of another network transfers at the same time and is not held); the ±10 % are asserted with `Accurate()` and the flakiness policy (`testbed.Statistically`: a failed measurement is made again, after the queues drained, and only a second failure fails), the functional bound (1.4 to 2.4 Mbit/s, a shared queue would give 1) always |
| Exceeding the class limit returns `capacity_exceeded` in preview | `TestAConfiguredFaultSetThatDoesNotFitTheClassLimitIsRefusedByThePreviewWithItsScope` (API, assumes the limit it sets: 6), `TestThePreviewOfAPerDeviceRateThatDoesNotFitTheClassLimitIsCapacityExceeded` (engine, limit 50, 31 queues per direction), `TestThePreviewOfMoreThanSevenIcmpSizesIsCapacityExceededWithTheScope`, `TestTheIFBTreeCountsAgainstTheClassLimitAndAFaultThatDoesNotFitIsRefused` |

## Profiles (M11)

Plan §2.4, §2.9 and M11. A profile is a named bundle of parts, one per family (impairment, MTU, and DNS and TLS
once those families exist); activating it on a scope makes each part compete in its own family like a fault on that
scope. Almost everything it needs was already there: the model (`Profile`, `ProfileParts`), the built-in catalogue
(`domain/builtin.go`), the expansion of an activation into candidates (`World.profileParts`) and the precedence rules
(E8, E9). M11 lifts the overlay kind, adds the views, the availability rule and the preview's account of origins,
and tests the precedence of the plan end to end: in the compiler's goldens, the engine, the API and on the real
kernel. No kernel construct is new: every part a profile can have in this build is an impairment (netem and HTB, as
M8b and M10 apply them) or an MTU fault (M10), and the kernel gates cover profile-expanded targets.

- **The catalogue** (`domain/builtin.go`). `normal`, `lte`, `bad-lte`, `satellite`, `congested-wifi`, `offline`,
  `intermittent` have fixed UUIDs (version 5 of the name) and reserved names; `dns-broken` and `tls-broken` exist in the
  catalogue; `dns-broken` has been available since M20 and `tls-broken` since M21. They are not part of any configuration: a key equal to a built-in
  UUID and a name equal to a built-in name (any case) are `reserved_name`, so a configured profile can never shadow one.
  Custom profiles are the `profiles` of the configuration, keyed by UUID. They are created, changed and deleted with a
  candidate revision like every configured object (P2-M11-02; decided by the maintainer on 2026-10-11: no endpoints of
  their own, single-profile writes would only be sugar over a candidate revision); `GET /profiles` and `GET /profiles/{id}` (UUID or name) show
  both kinds with `builtin`, `available` and `unavailable_reason`, and for the active revision the `activations` (the
  overlays that activate the profile, with target, owner, expiry and state). Another revision shows the definitions alone.
- **Validation.** A profile's parts are validated like the faults they stand for (`profileParts`: the same rules as an
  impairment fault, an MTU fault, a DNS fault, a TLS case); a profile needs at least one part (the schema). A profile with a
  `dns` or `tls` part can be defined. Its **activation** is refused as a whole with `unsupported_feature` and the milestone
  (`engine.checkProfileAvailable`, after `ValidateOverlay` resolved the reference, P2-M11-01), so a half-working profile never
  runs; the compiler ignores the parts of families it does not resolve yet. Transferred to the plan on 2026-10-11: M20 and M21
  lift the check family by family (`domain.unavailableFamilies`), and their acceptance criteria include that a profile with a
  `dns` part (M20) or a `tls` part (M21) activates and its part competes in that family. Both are done, and so is the case `intercept` of a `tls` part (M22): every family is available, and `unavailableFamilies` is empty (the rules about a family that is not are kept, and the tests name one with `domain.SetUnavailableFamilies`).
- **Activation** is an overlay of kind `profile` with the key *(owner, kind, target)*: one profile per owner and target, so
  writing the activation again with another profile **switches** it (`200`, the same overlay id, the same TTL rules as any
  overlay: `ttl`, `lease`, `renew`, reset, restart). The fault ids follow the existing rules (M8b): a switch between
  profiles that both have a per-device rate or both have none keeps the fault id and changes the leaf in place; a switch that
  adds or drops a rate (a queue per device, D18) makes new ids make-before-break. `normal` has an empty impairment part: it wins
  its family and impairs nothing, which is how a device is taken out of a wider scope's impairment.
- **Precedence** is the existing one and not re-implemented: each part is a candidate of its family on the scope of the
  activation, in the overlay layer. A device fault beats a network profile by level (E2, E3 style), a fault beats a profile part
  of the same scope whichever is newer (E8), a part of another family is not affected (a fault replaces only its own family's
  part), an overlay profile beats a configuration fault whatever the levels (D24). Parameters are never merged. The goldens are
  `TestGoldenProfilePrecedence` (`profiles-effective.golden.txt`, the effective list) and the scenarios `faults-profiles` and
  `faults-builtin-profiles` of `faultScenarios` (the tc tree and the maps of the same targets, so every property test and both
  kernel gates run on them; the MTU variant `profiles-mtu` is a scenario of the nftables gate only, because the MTU family
  compiles to jumps that the impairment scenarios must not contain).
- **State of an activation** (`GET /overlays`, `GET /profiles`). Each part is counted like a fault of its family (P2-M8a-06):
  `effective` when every part wins all of its traffic, `overridden` when none wins anything, `partially_overridden` otherwise
  (the fault of the same scope took the impairment part and the MTU part stayed, or a device fault took one device of the
  activation's network), `disabled` when the profile is gone or has no part this build resolves. `counters` and `queues` are the
  sums over the faults of the activation, as for a fault overlay; the faults of an activation name it
  (`compiler.Fault.Profile`).
- **A revision changes the profile; the activation follows** (plan §2.1.1). Overlays hold the UUID of the profile, so editing
  or renaming it keeps them, and the next compile expands the new definition under the same overlay id (and so the same fault
  ids). Deleting a profile that an overlay activates is the orphan rule of M8a: `validation_failed` with `references[]`
  (`object` is `/profiles/<uuid>`), or with `?force=true` the overlay is removed and `overlay_orphaned` is announced.
  Deleting an unreferenced profile needs nothing. A profile that a scenario step refers to is a missing reference in the
  scenario (M15). The activation follows an edit of the impairment and MTU parts, but not into a profile that has a
  part this build cannot resolve: a revision that gives a custom profile a `dns` or `tls` part while an overlay activates it
  is **refused** with `unsupported_feature`, naming the profile and the milestone (none since M22: M20 and M21 lifted `dns` and `tls`, M22 the case `intercept`), by the
  same rule as the write of the activation (`checkProfileAvailable`), so no revision leaves an activation with a half profile
  (`engine.unavailableProfileRevision`, `UnsupportedProfileRevisionError`, called where the apply settles the overlays
  of a revision and by the preview, which reports it as a problem; the API answers 422 before it applies). `force` does
  not lift it: it removes orphans, and this is no orphan. The remedy is to remove the activation first; the
  revision is then accepted, the profile is stored and listed with `available: false`, and its activation is refused as
  before. Decided by the maintainer on 2026-10-11 (P2-M11-05; the alternative, to accept it and report the activation as partly active, was not
  chosen). Tests: `TestARevisionThatAddsADNSOrTLSPartToAnActivatedProfileIsRefused` (engine: refused with nothing changed, the
  preview, accepted without the activation, an impairment-only change accepted) and
  `TestARevisionThatAddsADNSPartToAnActivatedProfileIsRefusedOverTheAPI`.
- **Preview and explain.** `compiler.Target.Effective` lists every winner in the user's terms (layer, source, family, scope, the
  parameters as the fault or the profile gave them, and the profile when the winner is one of its parts), including a winner that
  impairs nothing (`normal`), and the tunnel faults (scope "tunnel of client rA", also one whose peer has no address yet;
  `TestTheEffectiveFaultsListTheTunnelFaultsToo`: every key of `Winners` is in `Effective`); the preview answers it as `faults`. It is computed from the overlays as they are, so a revision that
  edits a profile shows what its activations will produce. `explain` has always named the profile in `winner.profile` and in the
  overridden entries (`FaultRef.profile`), with the reason a candidate lost (`level 4 beats level 8`, `a fault beats a profile
  part at the same scope`).

### Tests

| What | Where |
|---|---|
| Activating/switching profiles yields the configured parameters (compiler) | `TestEveryBuiltinProfileCompilesToItsConfiguredParameters` (every activatable built-in: delay, jitter, loss, rate, burst loss, flapping, blackout), `TestSwitchingTheProfileOfAnActivationYieldsTheNewParameters`, `TestTheNormalProfileWinsItsFamilyAndImpairsNothing`, `TestAnActivationFollowsTheNewDefinitionOfItsProfile` (`internal/compiler/profiles_test.go`) |
| ... and measured values (integration) | `TestActivatingAndSwitchingProfilesYieldsTheMeasuredValues` (a custom profile, another, the built-in Bad LTE, Normal, Offline on one activation; the other network never changes), `TestAnActivationFollowsTheNewDefinitionOfItsProfileOnTheWire`, `TestAProfileWithARateGivesEveryDeviceOfTheNetworkItsOwnRate` (E9 for a profile), `TestTheRateOfBadLTEShapesUDPSentAboveItEndToEnd` (the built-in profile's rate with UDP above it, P2-M11-03) (`internal/engine/integration_profiles_test.go`) |
| A device fault overrides the network profile's impairment part (same layer) | `TestADeviceFaultOverridesTheNetworkProfilesImpairmentPart` (compiler), `TestADeviceFaultOverridesTheNetworkProfileForThatDeviceOnly` (wire: no merge of the profile's jitter, loss or rate; the other device keeps the profile; it comes back when the fault ends) |
| A fault on the same scope replaces only the profile part of its family | `TestAFaultOnTheSameScopeReplacesOnlyTheProfilePartOfItsFamily` (compiler, both ways: impairment fault, MTU fault), `TestAFaultOnTheSameScopeReplacesOnlyThePartOfItsFamilyOnTheWire` (80 ms replaced by the fault, the MTU of 1280 stays), `TestAFaultOnTheSameScopeOverridesOnlyOnePartOfTheActivationOverTheAPI` (`partially_overridden`, then `overridden`), E8 `TestE8AFaultBeatsTheProfilePartOnTheSameScopeInTheCompiledTarget` |
| An overlay profile beats a configuration fault | `TestAnOverlayProfileBeatsAConfigurationFault` (compiler), `TestAnOverlayProfileBeatsAConfigurationFaultOnTheWire` |
| Origin in explain and preview | `TestExplainAndThePreviewNameTheProfileAsTheOrigin` (engine), `TestThePreviewNamesTheProfileOfEachEffectivePart` (API) |
| Deletion rules | `TestARevisionThatDeletesAnActivatedProfileIsRefusedUnlessForced` (engine), `TestDeletingAnActivatedProfileNeedsForceAndListsTheReferences` (API) |
| TTL and lease of an activation | `TestAProfileActivationExpiresWithItsTTL`, `TestAProfileActivationWithALeaseIsRenewedAndExpiresWithItsFaults`, `TestSwitchingAProfileRestartsItsTTLAndExpiryRemovesWhateverProfileIsActive` (engine, fake clock), `TestAProfileActivationEndsWithItsTTLOnTheWire` (the profile leaves the kernel, the configuration fault it replaced is back, a device only the profile named is clean) |
| Force delete on the wire | `TestForceDeletingAnActivatedProfileTakesItOffTheWire` (refused without force and still impairing; with force the scope is back to baseline, the activation on the other network untouched) |
| Catalogue, custom CRUD, availability | `internal/api/profiles_test.go` (`TestTheProfileCatalogueListsTheBuiltInProfilesAndMarksTheUnavailableOnes`, `TestACustomProfileIsCreatedChangedAndDeletedWithRevisions`, `TestACustomProfileWithADNSPartIsStoredButCannotBeActivated`, `TestAProfileIsActivatedSwitchedAndListedOverTheAPI`), `TestAProfileWithAPartOfALaterMilestoneIsRefusedAndNothingChanges` |
| The kernel accepts profile-expanded targets | `TestEveryCompiledTCTreeIsAcceptedByTheKernel` (`faults-profiles`, `faults-builtin-profiles`), `TestEveryCompiledRulesetIsAcceptedByTheKernel` (the same and `profiles-mtu`) |

What is asserted under emulation and what only with native execution or KVM is the split of the M8b tests: the
functional assertions (effect present, direction, isolation) always, the accuracy ones of plan §4.3 (median ±2 ms + 5 %,
loss within the 99.9 % binomial interval, jitter spread, rate ±10 %) with `testbed.Accurate()` and the flakiness policy of
`testbed.Statistically`.

**The rate of Bad LTE** (decided by the maintainer on 2026-10-11, P2-M11-03) is measured end to end with UDP:
`TestTheRateOfBadLTEShapesUDPSentAboveItEndToEnd` sends 4 Mbit/s downstream to a device on a network that has Bad LTE
(`iperf3 -u -b 4M`, datagrams of 1400 bytes) and reads off what the receiver gets, next to a device of another network that the
profile does not name. A TCP transfer would not do: at 150 ms ± 50 ms and 3 % loss a TCP flow is held to a few hundred kbit/s by
the loss (the Mathis bound is about 0.2 Mbit/s), so the result would be the loss' and not the rate's. The functional part always
runs (what arrives is well below what is offered, 1 to 3 Mbit/s, and the unnamed device gets its load through); with
`Accurate()` the throughput must lie within ±10 % of 2 Mbit/s times the 97 % that Bad LTE delivers (`testbed.Statistically`; the
datagrams are 1400 bytes in 1428 on the wire, which is inside the tolerance). The compiled rate is asserted by
`TestEveryBuiltinProfileCompilesToItsConfiguredParameters`, and E9 measures the rate of a profile whose only parameter is a rate.

**The delay of a profile with a rate** (decided by the maintainer on 2026-10-11, P2-M11-04: a bound is enough). A rate is netem's
`rate`, which keeps the packets in order (plan, the latency row of the fault table and risk 4: "keep order uses netem rate, which shifts
the delay distribution"): a packet leaves no earlier than the one sent before it. On the native runner both directions of a Bad LTE
device measured a median of 175 to 181 ms and a 5th to 95th percentile of 145 to 199 ms instead of 150 ms and a uniform spread of 90 ms,
with the loss inside its interval. That is the documented effect and not a mistake of the compiler (the compiled netem carries delay
150 ms, jitter 50 ms and the rate), and the §4.3 definition (median within ±2 ms + 5 %) describes a netem without ordering, so applying it
to a profile with a rate would assert something the kernel does not do. For a flow that keeps its order (`flowShape.keepsOrder`)
the tests assert the loss as before, and for the delay the bound that follows from the ordering (`checkOrdered`): the median from the delay
up to delay + jitter (plus the 2 ms) and the 5th percentile not below delay - jitter. The medians stay exact for every profile without a
rate. An exact number would be a simulation of the netem queue (the distribution of the ordered delay depends on the probe interval), not a
tolerance, and was not asked for.

**What the persistent VM showed** (kernel 6.8.0-142, emulated, so the functional assertions only): the seven measurement tests of
`integration_profiles_test.go` pass in one run of about 45 minutes (`make vm-test ARGS='-run "TestActivatingAndSwitching|..."
-tags testbed -test-timeout 80m -vm-timeout 90m ./internal/engine'`; a lab takes 3 to 4 minutes to build under emulation, the
switching test measures twelve flows). The two tests added when the acceptance list was closed (the TTL on the wire and the force delete) took 8 and 7 minutes each and passed; the TTL test measures under a long TTL and then writes the activation again with a short one, because a measurement under emulation takes longer than any TTL worth waiting for. No tc or nftables construct was new, so nothing was proven by hand first; the gates
`TestEveryCompiledRulesetIsAcceptedByTheKernel` and `TestEveryCompiledTCTreeIsAcceptedByTheKernel` accepted the profile-expanded
targets on the first run. A final run of all nine profile tests together with the overlay tests on a real kernel (`TestOverlaysReachTheRealKernelAndLeaveItAgain`, `TestAnOverlayTheExecutorFailsOnIsTakenBackOnARealKernel`, `TestAnOverlayShapesTheTrafficOfItsScopeOnARealKernel`, `TestAFailedBatchIsRevertedOnARealKernelAndEveryWriterGetsApplyFailed`) passed 14 of 14 in 108 minutes, `internal/compiler` passed 304 of 304 (both kernel gates on `faults-profiles`, `faults-builtin-profiles` and `profiles-mtu`) and `TestAnOverlayCreatedOverTheAPIIsInTheKernelAndItsCountersComeBack` passed. The 7.0.0-38 kernel of the matrix was not run locally; the nightly matrix does. The accuracy
assertions (medians, loss intervals, jitter spread, the rate) are written from plan §4.3 and run in CI; if one fails there, read
the printed distribution before touching a tolerance (a tolerance below the §4.3 definition needs an open item).

## Web UI (M12)

Plan §2.17, §3.7 and M12: the shell of the web app with its visual system and shared components, login and the first-start
setup wizard, the overview, the device list and the device detail, live updates over the event stream, and the empty,
offline and safe-mode states. The screens of M13 (faults, rules, profiles, the preview-and-apply drawer, the `</> API`
panel) and of M14 (networks, routing, technical view) follow in their own sections below; panels whose backend comes
later (running scenario, captures, diagnostics, DHCP and TLS test actions) are left out, and a device's flows and recent
DNS queries come with the flow view of M26 (P3-M12-02), and so does the path view of §2.17 (device → gateway → uplink →
destination with the fault badge), which is drawn from those flows (P3-AUDIT-37). M26 built them: "Metrics and flow view
(M26)" below.

### How it works

- **Served by the binary.** `internal/api` serves the build of the app for every `GET` or `HEAD` outside `/api/`
  (`api.Config.UI`, `ui.go`): a file of the build as it is, any other path `index.html`, so a reload of a deep link works.
  Hashed files under `assets/` are cached for good, `index.html` never (`no-store`) and carries the content security policy
  (`default-src 'self'`, inline style attributes only, `frame-ancestors 'none'`). A missing build file is a 404 problem, not
  the page; nothing under `/api/` is ever the page; the page loads before the setup is finished (the wizard is part of it).
  `make build` builds the app and `chaosgw` with the tag `webui`, which embeds `web/dist` (`web/embed.go`; without the tag,
  `web/noembed.go` embeds nothing, so `go build ./...` and the unit tests work without a web build). `chaosgw api --web-dir
  <dir>` serves a build from disk instead. The image builds the app in a Node stage and embeds it.
- **Data.** Server data lives in the TanStack Query cache, loaded with the functions Orval generates from the spec
  (`npm run generate`, run before every build, type check and unit test run by npm's `pre` scripts). The fetch wrapper
  (`src/api/fetcher.ts`) puts the paths under `/api/v1`, sends the session's CSRF token on unsafe methods, turns
  problem+json into `ApiError`, and reports every request: answered (online), not answered (`UnreachableError`, also for a
  proxy's 502/504) or refused with 401 outside the login (the session ended: back to the login with `next`). Lists follow
  `next_cursor` to the end (`fetchAll`), so a lab with more than 500 devices shows all of them. Pinia holds UI state only:
  the session, the connection, toasts, the events seen, the uplink's rate history.
- **Live updates.** `startEventStream` (`src/composables/eventStream.ts`) opens `GET /api/v1/events` with the session cookie,
  listens to every `EventType` (the stream sends named events) and invalidates the queries the event changes (table
  `invalidates`: a device event the devices and networks, an overlay event the overlays, faults and devices, an applied,
  confirmed or rolled-back revision the configuration's views with the groups, whose names the pages show, …).
  `events_lost` and every reconnect refetch everything; the browser's own reconnect (after a network error) sends
  `Last-Event-ID`, so the gateway replays what was missed. An answer that is not a stream (a 503 when too many streams are
  open, a proxy's 502 while the gateway restarts) makes the browser give the `EventSource` up for good (`readyState`
  `CLOSED`); the app then opens a new one after a pause that doubles from 2 s to at most 30 s and starts over once one is
  open (it sends no `Last-Event-ID`, so its open refetches everything). An expired overlay, a rollback, an apply failure and safe mode also raise a toast. Counters
  that grow without an event (traffic rates, affected packets) are polled every 5 s.
- **Shell and states** (`AppShell.vue`). Above every page: the red safe-mode banner (`GatewayState.safe_mode`, with the
  reason and a link to recovery), the offline banner (from the first request that did not reach the gateway until the next
  one that did; the page keeps its last data, greyed out, with "Last updated" and the time) and the orange commit-confirm
  banner while a revision is `pending_confirm` (live countdown, Confirm; controls that would change more, such as Adopt and
  removing an overlay, are disabled until it is resolved and while the gateway is offline; the rollback at the deadline is
  announced by the event's toast). The timer is not a live region; a hidden one announces 30 s, 10 s and the end, so a
  screen reader is not read every second. A gateway that does not answer the very first question (setup? session?) gets a
  page of its own with Try again, which asks again and runs the router's guard again (a forced navigation), so an open
  setup leads to the wizard and a missing session to the login; the page stays until that navigation is done. The page
  header shows the context line (revision, time of the last apply, sync state), health, the active-faults counter and the
  page's action.
- **Data that did not load.** A list or card whose query failed before it delivered anything says so with the gateway's
  answer and Try again (`LoadError.vue`, `firstError` in `src/composables/gateway.ts`) instead of falling through to its
  empty state: an empty state is an answer of the gateway, and "no active faults: all traffic passes unimpaired" while the
  faults are unknown would be wrong data. This covers the overview's active faults (overlays or configured faults), its
  topology (networks; the path badge and the fault count say "faults unknown"), its recent events (the audit log), the
  device list, the device detail (any error but 404, which says the device does not exist) and its faults; the header's
  counter says "Faults unknown" (and "Loading faults…" before the first answer), never "No faults active". A query that
  fails on a refetch keeps its last data (the offline state greys it out). Since the Phase 3 audit also: the access
  rules need the overlay rules (they are evaluated first; without them the list is a load error, not a shorter list or
  "No access rules yet"); the topology says "Loading the networks…" and the uplink "Loading…" or "Uplink not loaded"
  rather than "No test network" or "up", the uplink traffic card has its own load error; the recent events and the
  routing sessions say they are loading before "Nothing happened yet" and "No routing session". The Linux views
  (`LinuxStateView`, `LinuxObjectExcerpt`, `LinuxExcerpt`) keep the last kernel state when a read again fails while
  the gateway answers, greyed out under "Not updated: …; last read …" (`StaleNotice.vue`). The offline state greys
  the page out without `aria-busy`, which would tell a screen reader to wait; the alert banner says it is offline.
- **The gateway's clock.** Deadlines, TTLs, lease ends and "last seen" are times on the gateway's clock, and a Raspberry Pi
  without a real-time clock may be minutes off before NTP. Every countdown therefore runs on the gateway's clock as the
  browser estimates it (`src/lib/clock.ts`, used by `useNow`): each answer's `Date` header bounds the offset between the two
  clocks (at least the header's second when it arrived, below the next second when it was asked for); the bounds of all
  answers are intersected, and an answer outside them (the gateway's clock was set) starts over.
- **Login and setup.** The router asks the gateway first (`GET /setup`, then `GET /auth/session`): an open setup sends
  every page to the wizard, no session to the login. The wizard (`SetupWizard.vue`, logic in `src/lib/setup.ts`) asks for
  the setup token and shows the preflight with its fixes; then uplink (proposed: the interface of the default route; the
  selects name an interface by name and address, its MAC stands below) with
  its router (optional for the interface of the default route, required for any other, since the gateway refuses an uplink
  without a default route and without a gateway; it must lie in the uplink's network) and management (proposed: another
  interface with an address, or the uplink in the two-port case); then the test
  interfaces, each becoming a LAN test network with a proposed name and gateway address (10.10.0.1/24, 10.20.0.1/24, …;
  the first proposal whose name and prefix no other network of the draft, the uplink or the management network uses) and
  DHCP; then the admin password. A chosen test interface that the OS configures shows the preflight's check of it
  (P3-M12-07, below): the netplan file and entry or the NetworkManager connection, and what to remove (plan §3.4: a test
  interface has no entry), with "Check again"; the wizard computes no hint of its own. Each step is
  validated under its fields (each error tied to its field with `aria-describedby` while it is shown) before the next (name pattern of the spec, address with prefix length, no network or
  broadcast address, no overlap with another test network or with the uplink's and management's prefixes, password of 12
  characters typed twice). Plan §2.17 "Validation": the messages appear once the step's Next was pressed (not while the
  first field is still being typed) and then follow every change, and Next stays disabled until the step is valid; Apply
  setup is disabled while any step is invalid; a token the gateway refused keeps Next disabled until the token changes. The
  request is checked against the generated Zod schema of `POST /setup` before it is sent.
  The gateway applies revision 1 with commit-confirm; the wizard logs in with the new password and offers Confirm with the
  countdown. When the address the browser used is not served any more (the UI moves to the management network), it says
  where to confirm instead. Only the gateway knows whether it rolled back: when the countdown ends in the browser, Confirm
  stays and the wizard asks `GET /setup` every 2 s; an open setup (or a Confirm refused after it reopened) says the setup
  was rolled back, puts the session store back to "setup" (the session of the login after the apply and its CSRF token
  belong to a setup that is gone) and offers Start again: step 1 with the token and the password cleared (the gateway
  printed a new token) and the interface and network choices kept, since the rollback is often about one of them.
  A rollback while the browser is already in the shell (the login on the management address opened it) is noticed app-wide:
  the gateway then answers the API with 503 `unavailable`, which the fetch wrapper reports (`onUnavailable`); since safe
  mode and a missing executor answer the same, the app asks `GET /setup`, and if the setup is open again it puts the
  session store back to "setup", clears the cached data and sends the browser to the wizard with a toast that a new setup
  token is in the container log. The rollback toast of revision 1 says that the setup is open again, not that a previous
  revision is active.
- **Overview.** Topology (test networks → gateway → uplink, devices per network, the fault badge on the path while faults
  are active, profiles and where they are active; a management network, a WireGuard hub of role `management`, is no test
  network: it has a row of its own below the gateway, off the faulted path and without a fault badge, and the gateway
  counts test and management networks apart, P3-AUDIT-38), the active faults (impairing overlays and enabled configured faults:
  target and destination in words, the effect as fault chips, affected packets, the remaining TTL counting down or
  "configured"), the uplink traffic of the last ten minutes (uPlot; the rates between two readings of the uplink's byte
  counters from `/sys/class/net/<if>/statistics`, which `GET /uplink` reports as `status.rx_bytes`/`tx_bytes`; since
  issue #63 the API itself reads them every `--uplink-traffic-interval` (5 s) and keeps the rates in memory for
  `--uplink-traffic-period` (10 min), a reading of another interface or with a lower counter giving no rate, evicted by
  the monotonic clock, empty again after a restart of the API; `GET /uplink/traffic` returns that history, the chart
  loads it at once on a page load or reload and then asks every interval with `since` for the samples after its newest
  one, so none is missing or drawn twice; the store keeps the points when the user leaves the overview) and the
  recent events (the stream's, before that the audit log's last entries). Every item links to its detail (plan §2.17;
  `src/lib/links.ts`): a fault's target to the device or the network (a group or everything to the faults screen), an
  event to the device, network, overlay (faults) or rule its subject or data names, the topology's networks to their
  detail, the uplink to the networks and the gateway to the Linux view; "No test network" offers "Add a test
  network". The chart's time axis is the app's 24-hour clock; before the gateway's first sample it ends at now (not at the
  epoch) and says "Collecting the first readings…" visibly, and it is drawn again once the Plex Mono font is loaded.
- **Devices.** All devices with name, IP, MAC, network, traffic rates, the winning faults as chips (a `normal` profile is
  not one; `DeviceView.effective`, drawn from the overlay or configured fault it names in the overview's compact form,
  "↑↓ 200 ms ±50", "profile LTE", the gateway's summary only when that is not at hand) and status with a label, Adopt in
  a column of its own; search over name, addresses, MACs and the lease's host name (every word must match);
  the filter chips All / Online / With faults / Not adopted with their counts, as buttons with `aria-pressed`. A discovered
  device is adopted in a dialog: a name (proposed from the lease's host name), what changes in words, then a merge-patch
  candidate under the same UUID with the device's MACs (or addresses) and network, `If-Match` the active revision, applied
  and verified; the toast says "Revision N applied and verified in X ms". Since M13 the adoption goes through the
  configuration draft, the preview drawer and the concurrent-change dialog (section "Web UI: faults, rules and profiles");
  until it is applied the device's row and page mark it ("ADOPTED as … · not applied yet") with Undo instead of Adopt.
  Without any device the list says so in one sentence with the primary action "Check the test networks".
- **Device detail.** Identity (status, addresses, MACs, network, groups, origin, where it was seen, fixed IP, test CA),
  the DHCP lease with its remaining time, the active faults (the device's overlays and those of its network or group with
  "via …", the winning configured faults from `DeviceView.effective` with their chips and counters), with parameters,
  affected packets and remaining TTL; an overlay can be removed,
  one of the network or group only after a dialog that says it is lifted for every device there. The network card shows
  the router and the DNS servers the device is given (`deviceNetworkInfo` in `src/lib/devices.ts`): in a local network the
  DHCP options `router` and `dns_servers`, each defaulting to the network's gateway address, "(proxy)" only when the DNS
  server is that address; for a WireGuard client its configuration's DNS setting and no router.
  The traffic counters: rates, active connections, last seen.
- **Visual system** (`src/styles.css`, `src/components/ui/`). The tokens of §2.17 as Tailwind theme variables; IBM Plex
  Sans and Mono are bundled (`@fontsource`), nothing is loaded from outside. Shared components: `FaultChip`,
  `AccessChip`, `StatusDot` (always with its label), `SegmentedControl` (also the filter chips), `SwitchToggle`
  (`role="switch"`), `SectionCard`, `EmptyState`, table rows (`.data-row`), `ToastHost`, `StickyActionBar`, `SideDrawer`,
  `ModalDialog` (Reka UI dialogs: focus trap, Escape, labelled by the title), `CountdownBanner`, `AppBanner`, `AppIcon`
  (16 px stroke icons), `DisclosureButton` (issue #63: every toggle that shows more options — "Advanced", "Show Linux
  view"/"Show Linux changes", the configuration text, the dialogs' `</> API` part — has a triangle in front of its text,
  ▸ collapsed and ▾ expanded, decorative and not animated, with `aria-expanded` and `aria-controls` on the region it opens). The focus ring is the accent (`--color-accent`) at 2 px on every control (the device search's
  box shows it for its borderless input). Since the Phase 3 audit: `text-3` is `#848E9D`, at least 4.5:1 on `surface` and
  `surface-2` (selected and hovered rows); on the accent tint only `text` and `text-2` are used. Text sizes are the plan's
  12 / 13 / 14 / 16 / 20–26 px (the prototype's 11 and 15 px badges and titles are 12 and 16 px, dialog titles 20 px).
  `color-scheme: dark` and `accent-color` make native checkboxes, select popups and scrollbars dark and blue. A dialog's
  action row (`.dialog-actions`) sticks to its bottom edge while a tall form scrolls. Focus (`src/lib/focus.ts`): a dialog
  or drawer gives the focus back to the control that opened it, or to the page's heading when that control is gone (the
  sticky bar is hidden, not removed, while the drawer is open); a Remove, Delete or Adopt that is replaced by Undo (and
  back) hands the focus to the control that takes its place.
- **The preflight's check of the test interfaces** (P3-M12-07, decided option (a)). Plan §3.4 wants the preflight to
  check that the test interfaces carry no OS configuration. The executor reads where netplan (`/run/netplan`,
  `/etc/netplan`, `/lib/netplan`, one file per name as netplan takes it: `/run` shadows `/etc`, `/etc` shadows `/lib`)
  and NetworkManager (`system-connections` in `/etc` and `/run`) configure an interface
  (`preflight.ReadOSConfig`: a netplan entry by its id or its `match` by name glob, MAC or driver glob, all that are
  set; a `match` by none of them, empty or by another key, counts for every interface; a section's own `renderer`
  is no entry; a device that cannot be read is still named by its id and the file's other devices are kept; a port of a netplan bridge or
  bond, the link of a VLAN, a NetworkManager keyfile by `interface-name` or `mac-address`; and what hands every
  interface to NetworkManager: netplan's global `renderer: NetworkManager`, the last one by file name winning, and an
  ethernet keyfile bound to no interface that NetworkManager brings up by itself; for these the fix is
  `unmanaged-devices=interface-name:<name>` in `/etc/NetworkManager/conf.d/99-chaos-gateway.conf`, and an interface
  that `unmanaged-devices` under `[keyfile]` in `NetworkManager.conf` or `conf.d` names, by `interface-name:` or
  `mac:`, is not reported for NetworkManager, only for its netplan entries; what NetworkManager does by its own
  defaults outside these files, such as an auto-default "Wired connection N" without a file, is not seen) from the
  host root it is given
  (`chaosgw exec --host-root /host`), each directory only when it is there under that root. `deploy/compose.executor.yaml`
  mounts the host's `/etc`, read-only at `/host/etc` (netplan's and NetworkManager's files are root's and the API
  container's user cannot read them; the whole `/etc`, because a bind mount needs its source and a host without
  netplan or NetworkManager has no such directory), and systemd-networkd's defaults, `/usr/lib/systemd/network`
  read-only at `/host/usr/lib/systemd/network` (part of systemd's package, so always there; I64-02, decided
  2026-10-18; the CI job `mounts` lists it in the started executor container, and docs/install.md names it as a
  requirement of the host), so the shipped deployment reads `/etc/netplan`, `/etc/NetworkManager/system-connections` and the
  `.network` files of `/etc` and `/usr/lib`, and sees nothing of `/run` and `/lib`; the keyfiles netplan's NetworkManager
  renderer generates under `/run` come from its YAML in `/etc`, which is read. The read operation `host_net` returns
  only the file, the section, the entry and what it selects (`executor.HostNetState`), never a file's content (no
  address, no key, no Wi-Fi secret); a file that is not valid netplan YAML is named with the section and the line
  (`not valid netplan YAML (network.ethernets.enp3s0, line 4)`), never with the YAML library's text, which quotes the
  offending value. The API (`preflightReport` in `internal/api/system.go`)
  adds to the checks of the host made at start one check per interface that is or may become a test interface (every
  interface but the uplink and the management interface), made at each `GET /setup` and `GET /system/preflight`:
  `interface.<name>.os_config` with `interface`, `ok` when nothing configures it, otherwise `warn` for a candidate and
  `fail` for an assigned test interface (the report's `status` counts the host's checks and those of the assigned
  test interfaces, not a candidate's warning: before the setup the future uplink is a candidate too, and after it an
  interface the gateway does not use is the operating system's), the message naming the file and the entry ("eth3 is configured by netplan
  (/etc/netplan/60-lab.yaml, "lab" under "ethernets")") and `fix` what to remove (`delete the entry "lab:" under
  "ethernets" in /etc/netplan/60-lab.yaml, then run "sudo netplan apply"`, `sudo nmcli connection delete "…"`); a file
  that cannot be read or parsed is a warning of its own. A failing read is logged as a warning once (again when its
  message changes, its end once), not at each request of a polling screen. `GET /setup` needs no authentication, so
  its requests share one read through the executor for 2 seconds (`osConfigCache`, like the health check's probe,
  M5-13), and its report says only that the configuration cannot be read, not the executor's error (the log has
  it); the files, entries and connections it names are what the wizard must show (the Phase 3 audit). The check "no preflight check has run in
  this process" is added only when there is no report of the checks at start, not when that report has no time (the
  host could not be inspected: its own `host` warning). `HostInterface.os_managed` comes from the same read. The wizard
  shows the host's checks on its first step and the check of each chosen test interface where the test interfaces are
  chosen; the client-side hint (`netplanHint`) is gone. No CLI command shows the preflight; every client reads it from
  `GET /system/preflight`. `deploy/host-setup.sh` says to delete the entry too (it showed one that turns DHCP off).

### Tests

| What | Where |
|---|---|
| Fetch wrapper: base path, CSRF on unsafe methods, JSON default, problem+json, `Retry-After`, reachability, ended session, 503 `unavailable` | `src/api/fetcher.test.ts` |
| The gateway's clock from the `Date` headers: offset, narrowing, a clock that was set, learned by every request | `src/lib/clock.test.ts` |
| Formatting, fault chips and selectors in words, the device list's filters, search, status and order, its fault chips in the compact form of the other screens and a profile by its display name, the router and DNS servers a device is given (the Phase 3 audit) | `src/lib/format.test.ts`, `src/lib/faults.test.ts`, `src/lib/devices.test.ts` |
| A device carries the winning fault of each family and the overlays among its candidates (`DeviceView.effective`, `overlays`; the Phase 3 audit) | `TestADeviceCarriesTheFaultsThatWinForIt` (`internal/api/devices_test.go`); in the testbed, device A's row, "With faults" and its page show the network's configured fault: `web/e2e-testbed/faults.spec.ts` |
| The text colours reach 4.5:1 on the surfaces they are used on; native controls dark and in the accent (the Phase 3 audit) | `src/styles.test.ts` |
| Setup wizard rules, free proposals and the request it builds (checked against the generated schema), an interface in a select by name and address | `src/lib/setup.test.ts` |
| The OS configuration of an interface: netplan entries by id, match by name glob and MAC, bridge and bond ports, VLAN links, NetworkManager connections by name and MAC, what to remove, nothing of the files' content (not even the value a YAML error quotes), an unreadable file; netplan's NetworkManager renderer for all devices and an unbound ethernet connection for every interface, `unmanaged-devices` turning NetworkManager off for an interface (P3-M12-07); a renderer per device type, a device that cannot be read, `/run` shadowing `/etc` shadowing `/lib`, a match by driver or by a key the preflight cannot evaluate (the Phase 3 audit) | `TestThePreflightFindsTheOSConfigurationOfAnInterface`, `TestThePreflightSeesNetworkManagerTakeEveryInterface`, `TestThePreflightReadsANetplanFileWithARendererPerDeviceType`, `TestThePreflightTakesOnlyTheNetplanFileThatShadowsTheOthers`, `TestThePreflightEvaluatesANetplanMatchByDriver` (`internal/preflight/netplan_test.go`), `TestTheExecutorReadsTheHostsNetworkConfigurationWithoutItsContent` (`internal/executor/hostnet_test.go`) |
| The preflight checks every candidate and test interface: a warning before the setup (the suggested uplink too) that the report's status does not count, a failure for an assigned test interface, the uplink and the management interface not checked, `os_managed`, an unreadable file, "no preflight check has run" only without a report, a failing read logged once (P3-M12-07) | `TestThePreflightNamesTheNetplanEntryOrConnectionOfATestInterface`, `TestThePreflightSaysWhichOSConfigurationFileItCannotRead`, `TestThePreflightSaysNoCheckRanOnlyWhenNoneDid`, `TestAFailingReadOfTheHostsNetworkConfigurationIsLoggedOnce`, `TestTheUnauthenticatedSetupSharesOneReadOfTheHostsNetworkConfiguration` (`internal/api/preflight_test.go`); the wizard shows the preflight's text: `setup.spec.ts` "first start: the wizard sets up the gateway and confirms the revision" |
| Stores: session (setup, login, logout with CSRF, one round trip, a setup reopened by a rollback, a logout that drops what the session read), connection, toasts, events, uplink rates and the chart's time range before the first rate (now, not the epoch) | `src/stores/stores.test.ts` |
| Event stream: listeners, invalidation per type (the groups with every revision that becomes active), `events_lost`, toasts (the rollback of the setup's revision 1 in its own words), reconnect, a new stream with a growing pause after the browser gave one up | `src/composables/eventStream.test.ts` |
| Shared components and their accessibility (labels, `aria-pressed`, `role="switch"`, countdown with its milestones for screen readers and on the gateway's clock, Escape in a dialog, toasts) | `src/components/ui/components.test.ts` |
| Router: setup first, login with `next`, no login or setup with a session, an unreachable gateway | `src/router.test.ts` |
| Serving the app: deep links, assets and their caching, CSP, 404s, nothing under `/api/` is the page, before the setup | `TestTheWebAppIsServedNextToTheAPI`, `TestWithoutAWebAppEveryOtherPathIsNotFound`, `TestCheckUIWantsAnIndexPage` (`internal/api/ui_test.go`), `TestWebUIComesFromTheDirectoryOrTheBinary` (`cmd/chaosgw`) |
| The uplink's byte counters | `TestTheUplinkReportsItsByteCounters` |
| The uplink traffic history (issue #63): rates from two readings, none for a reset or wrapped counter, another uplink or a reading without counters, eviction after the period by the monotonic clock, `since`; the API samples every interval on its clock; the chart draws the history at once after a reload and appends the live samples, none twice | `TestTheUplinkTrafficHistoryTurnsReadingsIntoRates`, `TestTheUplinkTrafficHistoryEvictsByTheMonotonicClock` (`traffic_internal_test.go`), `TestTheUplinkTrafficIsSampledByTheAPIAndKeptForThePeriod`; `src/stores/stores.test.ts` "appends the gateway samples newer than its newest point, over the last ten minutes"; `overview.spec.ts` "the uplink chart draws the gateway history at once after a reload, then appends the live samples" |
| Disclosure toggles with a triangle, `aria-expanded` and `aria-controls` (issue #63) | `src/components/ui/components.test.ts` "a disclosure button toggles its region, with aria-expanded, aria-controls and a decorative triangle"; `network-forms.spec.ts` "network forms: a check of a field under Advanced opens Advanced; …", `linux-view.spec.ts` ""Show Linux view" on a rule …" |
| Screens against a mocked API (Playwright, `make test-e2e`) | `web/e2e/*.spec.ts` (table below) |
| The browser flow against the real stack | `TestFirstStartInTheBrowserUntilADiscoveredDevice` (`internal/api/e2e_ui_test.go`, tags `testbed,browser`) with `web/e2e-testbed/first-start.spec.ts` |

**The mocked API** (`web/e2e/mock.ts`): `MockGateway` answers `/api/v1` through Playwright's request routing from data typed
with the generated model (a change of the spec breaks the fixtures at compile time; `npm run typecheck` checks the e2e code
too), records every request with its headers and body, paginates like the gateway (so the device list must follow the
cursor), refuses unsafe requests without the CSRF token, can be made unreachable (every request aborted) or answer a path
with an error (`failing`) or only once a promise settles (`holding`, for the loading states), can run its clock
off the browser's (`clockSkewMs`: a `Date` header and deadlines on that clock) and can roll the setup back (`rollBackSetup`). The browser's
`EventSource` is replaced by a fake the test drives (`gw.emit`, `__sse.fail`, `__sse.reopen`), so a live update is
asserted deterministically instead of by a poll.

**Timing under load** (`web/playwright.config.ts`, the Phase 3 audit): assertions wait up to 10 s, the `open()` helpers of
`networks.spec.ts` and `network-forms.spec.ts` wait up to 15 s for the first heading after `page.goto` (a cold first render
on a busy runner took longer than 5 s), and a test that navigates after an edit first asserts the edit landed (the switch's
`aria-checked`, the unapplied-changes bar) and then the new URL and heading. In CI a failed test runs once more; the report
lists it as "flaky", so a timing problem stays visible without failing the job on one slow start.

**The browser flow in the testbed.** `TestFirstStartInTheBrowserUntilADiscoveredDevice` builds the default topology, starts
the gateway on the real executor in the gateway's namespace with the built app (`web/dist`) and an open setup, lets device
A ping its gateway address every second, and runs `playwright test --config playwright.testbed.config.ts` with the
gateway's address, the setup token and device A's MAC and address in the environment. Chromium opens a page, is sent to the
wizard, enters the token, assigns wan0 (uplink, with the server 203.0.113.10 as its router, since the gateway's default route
leaves through mgmt0), mgmt0 (management) and lan0 (test network IoT, 10.10.0.1/24), sets the
password, applies (minutes under emulation), confirms, and waits in the device list until device A appears as discovered,
then opens it. The Go test then asserts what the browser saw: revision 1 active and confirmed, A a discovered device. It
needs the build, `web/node_modules` and Playwright's Chromium and fails with the command to run when one is missing; it is
never skipped. The tag `browser` keeps it out of the other testbed runs (which have neither); CI runs it in the job
`testbed-ui` (level 1, privileged container, which installs the three first). Locally:
`make build-web && make test-vm ARGS='-tags testbed,browser -run TestFirstStartInTheBrowser ./internal/api'` (the VM boots
from the devcontainer's file system, so it finds the browser in `~/.cache/ms-playwright`; everything Playwright writes
goes to the test's temporary directory).

**What the VM showed** (kernel 6.8.0-142, emulated, 4 CPUs, 3 GB): the browser flow passed in 11 minutes, 3.9 of them in
the browser (`make test-vm ARGS='-tags testbed,browser -run TestFirstStartInTheBrowser -mem 3G -cpus 4 -test-timeout 60m
-vm-timeout 90m ./internal/api'`). Its first run found a real gap: the testbed's gateway has its default route through
mgmt0, and the gateway refuses an uplink without a default route and without a gateway; the wizard had no field for the
router, and showed the refusal in its review step. It now asks for the uplink's router when the default route does not
leave through the chosen uplink. The privileged-container job of CI has not run on this branch yet.

### Acceptance map (plan M12 "Tests")

| Plan M12 Tests | Test |
|---|---|
| Playwright against a mocked API for every state: empty | `overview.spec.ts` "empty: no faults, no devices, no networks", `devices.spec.ts` "empty: no devices yet" (one sentence, the primary action), `device-detail.spec.ts` "a device without faults says its traffic is unimpaired" |
| … loading | `overview.spec.ts` "while the networks and the events load, the overview says so instead of "none"", "the uplink chart says visibly that it collects the first readings, until it has them"; `networks.spec.ts` "the routing sessions say they are loading, not that there are none" (the Phase 3 audit) |
| … many devices | `devices.spec.ts` "many devices: all pages are listed, search and filter chips narrow them" (620 devices, two pages) |
| … data that did not load | `states.spec.ts` "a list the gateway did not deliver says so and does not claim to be empty", "devices and a device the gateway did not deliver: an error, not an empty list or a blank page"; `overview.spec.ts` "the uplink is not claimed "up" while it is not known, and a failed read says so"; `rules.spec.ts` "the overlay rules not loaded: the list says so instead of leaving out the rules evaluated first"; `networks.spec.ts` "the Linux view read again fails: the old counters stay, greyed out, and it says they are not live" (the Phase 3 audit) |
| … offline | `states.spec.ts` "offline: a banner, live data greyed out with the time it was last updated, and back", "offline from the start: the page says so and tries again", "offline from the start, then the setup is open: Try again leads to the wizard", "offline from the start, then no session: Try again leads to the login and back to the page"; `device-detail.spec.ts` "faults cannot be removed while a revision waits for confirmation or the gateway is offline" |
| … safe mode | `states.spec.ts` "safe mode: a full-width red banner with the reason and the way to recovery" |
| … the other states of §2.17 that M12 brings: commit-confirm, expiring overlays, live updates, validation, login, setup | `states.spec.ts` (commit-confirm, its timeout, live updates, expiring overlays), `setup.spec.ts` (the wizard with its validation and Next disabled until a step is valid, free proposals, errors tied to their fields, the request, the confirmation, a countdown on the gateway's clock, Confirm until the gateway rolled back, Start again after a rollback, the session store following it, a rollback while the browser is in the shell), `login.spec.ts` (a logout drops the draft and the data read, a locked-out login says how long to wait, its alert announced once while the countdown beside it ticks), `overview.spec.ts` (every item links to its detail), `devices.spec.ts` (rows with the compact fault chips, adopt, an adoption marked until it is applied and its Undo, a refused base, the search field's focus ring), `device-detail.spec.ts` (removing a network's fault asks first, a configured fault that wins, the router and DNS servers the network hands out, an adoption marked) |
| Keyboard focus and the visual system (plan §2.17 Accessibility, Visual system; the Phase 3 audit) | `faults.spec.ts` "keyboard: Remove and Undo hand the focus to each other, and the drawer gives it back to the bar or the page", `rules.spec.ts` "keyboard: deleting a rule puts the focus on its Undo, and Undo on the rule again", "the selected rule is marked for assistive technology too, not only by its colour", `devices.spec.ts` "an adoption in the changes is marked in the list, Undo takes it back and keeps the focus there", `network-forms.spec.ts` "a tall form keeps its action row in view at 1440 × 900", `networks.spec.ts` "the access matrix keeps its columns readable and scrolls inside its card when there are many networks", `src/styles.test.ts` |
| The overview topology draws a management network apart from the test networks (plan §2.17 Overview; P3-AUDIT-38) | `overview.spec.ts` "a management network is drawn apart from the test networks, off the faulted path (P3-AUDIT-38)" |
| An E2E flow in the testbed: first start with setup token → assign interfaces → see a discovered device | `TestFirstStartInTheBrowserUntilADiscoveredDevice` + `web/e2e-testbed/first-start.spec.ts` (CI job `testbed-ui`) |

## Web UI: faults, rules and profiles (M13)

Plan §2.17, §2.4, §2.9 and M13: the add/edit fault dialog with its live preview, the access-rules screen (ordered list,
rule editor IF / THEN, counters), the faults screen (by family and scope, "overridden by", explain), the
unapplied-changes bar, the preview-and-apply drawer, the concurrent-change dialog (reload and reapply mine · discard
mine), the profile cards, the TTL display and the `</> API` panel. M14's screens (networks, WireGuard, routing, access
matrix, compiled state) follow in the next section; "Measure with probe" on the profile cards comes with the probes
(M25).

### How it works

- **The configuration draft** (`src/stores/draft.ts`). Every change of the configuration the UI makes — access rules and
  their order, configured faults, custom profiles, an adopted device — goes into one draft, edited against the revision that was active when
  the first edit was made (`GET /revisions/active`; a clean draft follows the active revision, `track`, also when its last
  edit was undone by hand while another revision became active, so the screens show what is active). The draft holds the
  edited objects whole (`null` for a deleted one) and the order of the rules when it was touched; the changes in words are
  computed field by field against the base (`changes`: rule, field, old → new; a moved rule is a change of its position, a new
  rule at the end moves none), and an edit back to the base is no change. Nothing reaches the gateway before "Preview &
  apply": the draft then becomes a JSON Merge Patch against the base (`patch`, `src/lib/mergePatch.ts`: changed members, whole
  new objects, `null` for what went, arrays replaced), sent as `POST /revisions` with `Content-Type:
  application/merge-patch+json` and `If-Match` the base. The draft lives in the shell, so a rule, a fault and an adoption go
  into one revision, and a screen change does not lose it.
- **Unapplied-changes bar** (`src/components/apply/DraftChanges.vue`, plan §2.17 "Unapplied changes"): the sticky bar "N
  changes in M rules · not applied yet" (or faults, devices) with Discard and Preview & apply; changed rows are marked
  CHANGED (a new one NEW), and a deleted rule or configured fault stays at its place, struck through and marked DELETED,
  with Undo (`restoreRule` puts a rule back at its place in the order, `restoreFault`), until the draft is applied or
  discarded. Every field the dialogs edit is a change in words (`changes`): a configured fault's name, description,
  target, tunnel (`describeTunnel`), traffic with a destination of any kind (`describeDestination`: uplink, network,
  prefix, host name) and parameters as chips that carry their values (burst loss "burst loss 5 %/30 % h 1 % k 99 %",
  "keep order"), a profile's name, description and parameters; an edit the words do not show is still one ("settings:
  as before → changed"), so the bar, the CHANGED marks and the revision message never say "0 changes".
  Preview & apply stays disabled while a rule of the draft is not valid (plan §2.17 "Validation"),
  while a revision waits for confirmation and while the gateway does not answer.
- **Preview-and-apply drawer** (`PreviewDrawer.vue`, "Preview", "Applying / applied", "Apply failed"): marked "dry run ·
  nothing changed yet", it creates the candidate and shows the gateway's dry run (`POST /revisions/{id}/preview`): what
  changes in domain terms (the domain diff with its fields, old → new), warnings, whether the change needs a confirmation,
  overlays the change would orphan (applying them away needs a tick, `force=true`), and on request the Linux changes
  (the unified diffs of nftables, tc, routes, WireGuard, BIRD and the services, and the verify step). Back to editing (or
  Escape) discards the candidate and keeps the draft; closed (or opened again) while the candidate is still being created,
  the drawer discards that candidate when it arrives and does not preview it (each open counts as a generation). "Apply revision N" (busy while it runs) answers with the toast
  "Revision N applied and verified in X ms" (or "waiting for confirmation", and the commit-confirm banner takes over). A
  candidate the gateway refuses (422) lists the problems with the object and field they name ("Rule no-dot · ports: …"),
  and the editor shows each under its field until the object is edited (`setServerErrors`; for a configured fault the
  faults screen marks the row NEEDS FIXING with the messages, and its dialog puts them under the fields the pointer names
  and the rest below the form). A failed apply
  (`apply_failed`, `verify_failed`) stays in the drawer with the failing step, the gateway's message and the revision that
  is active again ("Revision 42 restored; nothing of revision 43 is active"), and the draft stays for another try. While
  the apply runs, the drawer cannot be closed (Back to editing, the close button and Escape wait), so its result always
  has a place to show (the Phase 3 audit). An apply that answers `pending_confirm` notes the revision
  (`notePending`): edits made while it waits are based on the revision before it, and when it is confirmed the draft
  moves onto it without the concurrent-change dialog (`track` rebases it), since those edits met nobody else's change.
- **Concurrent-change dialog** (`ConflictDialog.vue`, "Concurrent change"): a base that is not the active revision any
  more (409 `revision_conflict`, at the candidate or at the apply) opens it: "Revision 43 was applied by token ci-runner
  while you were editing (your change is based on revision 42)" — when several revisions were applied since the base,
  all of them with their authors ("Revisions 43 and 44 were applied by token ci-runner and admin", from
  `GET /revisions`), since the diff covers them all —, their change (`GET /revisions/{active}/diff?base=42`) and the
  user's (the draft's changes in words). "Reload and reapply mine" puts the draft on the active revision (`rebase`: the
  user's change of each object, a merge patch against the old base, applied to their version of it, so a field only they
  changed keeps their value and a field both changed takes the user's; an object they removed comes back as the user has
  it, an edit they made already drops out, a deletion of what is gone drops out, rules they added keep their place in
  the order) and opens the drawer again, so the user sees the preview of the combined result before anything is applied.
  Before that the dialog names what both sides changed, under "Changed on both sides · theirs → yours" (`overlaps`,
  P3-M13-07: per object the members both merge patches touch with different values, `bothChanged` in
  `src/lib/mergePatch.ts`, a scope or destination compared as a whole and reapplied as the user's whole, `reapplyMergePatch`,
  so a From both changed is never merged into a scope with two choices — "Rule no-dns-2 access: TCP reset → Reject", a
  rule's members named as in the editor and the user's change (from, to, access, cut existing) —; an
  impairment parameter set (a configured fault's flat parameters with upload and download, a profile's
  `parts.impairment`) merged member by member that the gateway would refuse where neither side's was (flat parameters
  next to upload/download, loss next to burst loss, blackout next to flapping, a jitter above the latency;
  `impairmentCombines` in `src/lib/faults.ts`) is taken as the user's whole and named as one field, "Fault slow
  parameters: ↑↓ 20 ms, 2 % loss → ↑ 20 ms, 1 % loss" (P3-M13-03, P3-M13-04); an
  object they changed that the user deletes, one the user changed that they removed, and the rule order when both moved
  rules to different orders — "Rule order: no-dns, no-dot, test-web → test-web, no-dot, no-dns", over the rules all
  three revisions have; the user's order wins and rules they added keep their place; a pair of the access matrix has two
  policies, so both changing it agree), and its text points at those fields as the ones where the user's value wins;
  these rows are tinted in the accent, not in the fault colour, which means impaired traffic. Their change names a rule
  they changed field by field in the same words as the user's ("Rule no-dns access: Drop → TCP reset", `theirWords` in
  `DraftChanges.vue` from the base and their configuration), every other object with the gateway's summary (the Phase 3
  audit). "Discard mine" drops the draft. There is no "overwrite theirs" (plan §2.17: the prototype's three-way dialog is
  superseded).
- **Edit dialogs and a revision applied meanwhile** (`editOrigin`, `editFrom` in `src/stores/draft.ts`; the Phase 3
  audit). A clean draft follows the active revision, but an edit dialog's form is built when it opens. The dialogs that
  edit an object of the configuration (a configured fault, a custom profile, a network, a routing protocol, the routing
  settings, a client's key rotation) note the base they opened on and save against it: a clean draft whose base moved
  meanwhile goes back to that revision for the edit, so the patch carries only what the user changed there, and the
  next preview meets the newer revision in this dialog (409), where "Reload and reapply mine" keeps the fields only they
  changed. Without it the form's untouched fields would have overwritten the other revision's change under If-Match of
  the newer revision. Reapplying a client edit whose hub their revision deleted drops it and names it ("WireGuard client
  rA client: hub lab-hub deleted → my change dropped"), unless the draft brings the hub back itself.
- **Access rules** (`src/views/RulesView.vue`, `src/components/rules/RuleEditor.vue`, `src/lib/rules.ts`). The list in
  evaluation order: the system rule "Control plane access" locked at the top (SYSTEM, its hits, a switch that cannot be
  turned off), the overlay rules (OVERLAY, newest first, their remaining time), then the configured rules with position,
  name, selector in words ("Network IoT → uplink · TCP 80, 443"), access chip, hits and the enable switch (`role="switch"`).
  The selected rule opens in the editor: name and enabled; **IF** from (everything, a network, a group, a configured device),
  to (anywhere, the uplink, a network, an address or prefix, a host name — with the hint that host names are matched from
  DNS answers, best effort), protocol, ports ("80, 443, 1000-2000"); **THEN** Allow · Drop · Reject · TCP reset as a
  segmented control (`aria-pressed`), the ICMP type of a reject, "also cut existing connections" for a refusing rule
  (switched off for another protocol than TCP or any, with the hint why under it). The selected rule's button carries
  `aria-current`, not only its colour. The
  matches box shows hits, bytes and the last match, and the effect in words ("Rejected for new connections with ICMP port
  unreachable, so the sender fails at once; existing connections are cut once. 1 overlay rule is checked first."). Add rule,
  Delete, Move up / Move down change the draft. The editor checks a rule as the spec does (the name pattern, ports only with
  TCP or UDP, port numbers and ranges, an address or prefix, a host name, cut existing only for a refusing rule with TCP or
  any, a TCP reset only with TCP — choosing TCP reset selects TCP, and another protocol is refused under Access, as
  the gateway's `reset_requires_tcp`) and says it under the field (`aria-describedby`); a rule that needs fixing is marked in the list. An overlay rule is
  shown read-only with its owner and can be removed.
- **Last match** (backend, the nftables `last` statement, decided option (b) of P3-M13-01). Every access rule (in both
  hooks) and the anti-lockout rule carry `last` right after their named counter (`compiler.lastStmt`, kernel 5.17 or
  newer, nftables 1.0.1 or newer; the target is 6.8): the kernel notes when the rule last matched and `nft -j list`
  prints `{"last": {"used": <ms>}}` (milliseconds before the listing) or `{"last": null}` (never). The compiled rule
  says never; since an apply flushes the compiled chains and fills them anew, `Nft.Transaction` carries the time of
  the kernel's rule of the same chain and named counter over to the rule that replaces it (`withLast`,
  `linux.Ruleset.LastUsedIn`): the kernel takes `{"last": {"used": N}}` as "matched N ms ago", so the time is
  late by at most the length of the apply (between the read of the state and the commit). The comment (the hash of
  the compiled expression) is unchanged, so verify recognizes the rule. `Engine.ReadCounters` gives every named
  counter the most recent `last` of the rules counting into it (`CounterValue.LastUsed`, `linux.Ruleset.LastUsed`),
  and the API's `Counter.last_match` is the end of the read minus it (`overlayContext.lastMatch`). It replaced the
  read-based time of `internal/api/matches.go` (the first read that found the counter higher), which depended on
  someone reading. Absent while the rule has not matched since it was made (a new rule, a table made anew). The
  technical view prints the statement as nft does (`last used 2s`, `last used never`, `linux.RenderNft`); the
  simulated kernel keeps the time per rule and sets it when a test bumps a counter (`kernelsim.BumpCounter`). Tests:
  `TestEveryAccessRuleAndTheAntiLockoutRuleNoteTheirLastMatch`, `TestATransactionCarriesTheLastMatchOverToTheRuleThatReplacesIt`
  (also `nft -c` parses the carried statement), `TestTheLastStatementOfARuleIsReadByItsNamedCounter`,
  `TestRenderNftPrintsTheLastStatementAsNftDoes`, `TestARulesCounterSaysWhenItLastMatched` (a read 2 s after the match
  gives the match's time; an overlay's apply keeps it; a new rule has none); on the real kernel
  `TestTheKernelListsTheLastMatchAndTakesItOverFromTheRuleItReplaces` (the kernel gate: never after a first apply, a
  carried 1.5 s counts on) and `TestARulesLastMatchIsTheKernelsAndAnApplyKeepsIt` (a ping the rule drops, read 3 s
  later, then a second overlay's apply). The kernel gates (`TestEveryCompiledRulesetIsAcceptedByTheKernel`,
  `TestEveryCutWindowIsAcceptedByTheKernel`) and the access rules' kernel tests run with the statement; all of them passed
  in the emulated VM (`make test-vm ARGS='-no-kvm -run ...' ./internal/compiler ./internal/api`, kernel 6.8.0-142,
  2026-10-14; the API test needs a commit-confirm window longer than the emulated first apply).
- **Faults** (`src/views/FaultsView.vue`). Every fault grouped by family (impairment, MTU, tunnel, profiles, DNS, TLS, DHCP,
  WireGuard actions) and sorted by scope (device → group → network → remote network → everything → tunnel; at the same
  scope the overlay first), each marked OVERLAY or CONFIGURATION (with its name), with its selector, parameters as fault
  chips, its state (effective, partially overridden, overridden, disabled), affected packets, the remaining time (or
  "configured", "no TTL") and "overridden by X" where another fault wins: the configured faults' `overridden_by` and, since
  M13, the overlays' (backend: `Overlay.overridden_by` for fault and profile overlays, the winners over the devices of the
  target at a destination and port it selects, counted per part for a profile; `TestAFaultThatWinsOnlyAPartOfItsTrafficIsPartiallyOverridden`
  asserts it for a network overlay, a group overlay and a profile activation). An overlay is removed at once, a configured
  fault through the draft. **Explain** (`ExplainCard.vue`): device, destination (address or host name), protocol and port
  → `GET /explain`: the access verdict and what decided it, and per family the winner (layer, level) with the faults that
  lost and why ("fault iot-slow lost: level 2 beats level 8"); a tunnel family is named by its tunnel ("Tunnel of rA",
  "Tunnel of link site-b"), not by the API's `client:<id>` key.
- **Add/edit fault dialog** (`src/components/faults/FaultDialog.vue`, `src/lib/faultForm.ts`, plan §2.17 "Add fault").
  A new fault has a kind (P3-M13-04): Impairment, MTU / PMTUD or Tunnel (not on a device's page: a tunnel fault has
  no target). Target (fixed to the device on its page), traffic (all, MQTT, MQTTS, HTTPS, HTTP, DNS, or protocol, ports
  and a destination: anywhere, the uplink, a network, an address or prefix, a host name), direction (Both / Upload /
  Download / Each its own — then "Parameters of Upload | Download" switches the fields between the two parameter sets,
  and a problem of the other set is named beside it), "Start from" LTE, Bad LTE, Satellite, Offline (the parameters of
  the built-in profiles of the same name), latency, jitter, loss and duration (1 min to 1 hour, or until removed);
  Advanced: rate, queue limit, reorder, duplicate, corrupt, the delay distribution (uniform, normal, pareto,
  pareto-normal), keep packet order, the loss model (random with its correlation, or Gilbert-Elliott burst loss with
  good → bad, bad → good and the delivery in each state) and flapping (up and down in seconds). The impairment fields are
  one component (`ImpairmentFields.vue`, `src/lib/netemForm.ts`) the profile editor shares. An MTU fault has the target,
  the traffic, the packet size (552–1500 bytes) and the mode (ICMP · fragmentation needed, Black hole · dropped
  silently, MSS clamp · TCP only), each mode with its effect in words (the server's cached path MTU for ICMP); a tunnel
  fault has the WireGuard client or link (`tunnelOptions`), no traffic, and only the parameters a tunnel takes (latency,
  jitter, loss, burst loss, blackout, flapping; the directions are the peer's, "Upload · from the peer"). The live
  preview is one sentence ("ESP32-42 · MQTT · TCP 8883 · upload & download → ↑↓ 200 ms ±50, 5 % loss · expires after 5
  min", "ESP32-42 · HTTPS · TCP 443 → packets above 1280 bytes: Black hole · until removed", "Tunnel of rA · upload only
  → …"). The checks (at least one parameter — the gateway refuses a fault that sets none —, jitter ≤ latency,
  percentages up to 100, a rate of at least 8 bit/s in the units of the spec, reorder needs a latency, burst loss needs
  good → bad and bad → good, a queue limit of 1 to 1000000 packets, flapping needs both times and no blackout, an MTU
  of 552 to 1500, a tunnel, ports and destination) stand under the fields, Apply waits until the form is valid, and what
  the gateway refuses (422) goes under the field its path names (a download parameter under the download set's field). Apply writes an overlay
  (`POST /overlays`, answered after the verify): it appears with its TTL counting down and a toast "Fault active on
  ESP32-42" ("Expires after 5 min."). A loss of 100 % is a blackout. Editing an overlay writes it again under the same
  key (owner, target, family and selector: it keeps its id and gets the new TTL); whenever the gateway answers with
  another overlay — another traffic selector, or an overlay of an API token or a run, which the browser's write cannot
  replace because the key includes the owner — the old one is removed, so the edit replaces it (the dialog says "Set by
  the token ci-runner. Applying removes it and puts a fault of yours in its place."; P3-M13-06). An edit keeps the
  overlay's remaining time unless another duration is chosen (P3-M13-06): the duration's first choice is "Keep: 4:12
  left of 5 min" (`durationChoices`), and Apply sends the overlay's own TTL again with the remaining time on the
  gateway's clock in whole seconds as `expires_in` (`remainingTtl`, at most the TTL), also for an overlay of another
  owner, which becomes a new overlay of the admin: the expiry stays where it was ("Expires at 12:46 as before.") and the
  overlay keeps its TTL (`30m` stays `30m`, so the next edit says "Keep: … left of 30 min" again). A TTL that is not one
  of the choices (`30m` from the API) is offered as such ("30 min (its own TTL)", `ttlText`) instead of becoming 15 minutes.
  An overlay that expires while its dialog is open has no time left to keep: Keep says "expired at 19:31", the
  duration asks for a new choice under the field (`keepError`), and Apply waits for it, instead of writing the ended
  fault again for a second. Under "</> API" the dialog shows
  the requests it would send (a configured fault: the merge patch of `POST /revisions` against the base revision's
  fault, `objectPatch`, so a flat fault made "Each its own" shows its flat parameters as `null`). On the faults screen a fault can instead be saved in the configuration ("Configuration ·
  after preview", with a name; it goes into the draft), and a configured fault is edited there. Every fault the API
  makes is edited with all its parameters (the faults screen offers Edit for impairment, MTU and tunnel faults, overlays
  and configured ones), so saving never replaces one the dialog does not show; a configured fault keeps its description
  and its switch. A configured fault saved without a change records nothing (the defaults the dialog fills in, such as
  the MTU mode `icmp`, a latency in ms or the same parameters of both directions as flat ones, are no change), and an
  MTU fault written without a mode keeps leaving the default out when another field is edited (`mtuOf`). The device detail has "Add fault", "Edit" for the device's own faults and "Apply profile".
- **Profiles** (`src/views/ProfilesView.vue`, `src/lib/profiles.ts`). A card per profile (built-in first, custom marked):
  its display name ("Bad LTE"), description, parameters as chips, where it is active (target, remaining time, state, owner)
  and, for a profile whose family comes later, why it cannot be activated. The scope selector (everything, network, group,
  device) and the duration (until removed, 1 min to 1 hour) apply to the buttons: "Activate on Network IoT" writes the
  activation (`POST /overlays` with `profile` and `ttl`; another profile on the same scope replaces it, one profile per
  scope), "Deactivate on Network IoT" removes the activations of that scope. The device page's "Apply profile" opens the
  profiles with the device as the scope (`?scope=device:<id>`). The buttons name the profile for a screen reader
  ("Activate Bad LTE on Network IoT"); one button changes its action, so the focus stays on it.
- **Profile editor** (`src/components/profiles/ProfileDialog.vue`, `src/lib/profileForm.ts`; P3-M13-03). "New profile"
  in the header, "Edit" and "Delete" on a custom profile's card (a built-in profile has neither). The editor has the
  name (the pattern of the spec, unique among all profiles case-insensitively, never a built-in profile's name), the
  description and the parts the editor makes: an impairment part with the fault dialog's impairment fields (direction
  including each its own, presets, latency, jitter, loss and the advanced parameters, `ImpairmentFields.vue`) and an
  MTU part (size and mode); a DNS or TLS part the profile already has (defined over the API) is kept as it is and said
  so. A profile needs at least one part. The editor's own checks show once a field was changed or "Add to the changes"
  was pressed, as in the network form and the setup (not in a dialog just opened). The preview shows its chips ("no
  impairment" as plain text, not an orange chip), `</> API` the merge patch of `POST /revisions` (`objectPatch`: what changed against the base revision's profile, `null` for a member the edit removes; a new profile whole).
  A custom profile is an object of the configuration (`profiles`), so "Add to the changes" puts it into the draft
  (`setProfile`, `restoreProfile`, the patch, the changes in words, `overlaps` and `rebase` like a configured fault):
  the bar counts it ("1 change in 1 profile"), the card is marked NEW, CHANGED or DELETED (struck through, with Undo)
  and says it can be activated after the changes are applied (Activate waits until then), and Preview & apply takes it
  to the gateway. A profile the gateway refused in a candidate is marked NEEDS FIXING with the messages, and its editor
  puts them under the fields the pointer names (`profileFieldOf`, a download parameter under the download set's
  field). An existing profile saved without a change records nothing: the editor fills in what the API leaves out
  (the MTU mode `icmp`, a latency in ms, the same parameters of both directions as flat ones), which is no change, so
  the card is not marked and the bar stays hidden; an MTU part written without a mode keeps leaving the default out
  when the profile is edited elsewhere (`mtuOf`). The screen reads the active revision (`useActiveConfig`) as the
  draft's base. Deleting a profile that is active says so on its card ("Removed when the changes are applied, and with
  it its activation: the preview lists it and asks first."): the gateway lists the activations as `references`, and
  the drawer's "Remove them when applying" applies with `force=true`.
- **Remote networks in the scope selectors** (`src/lib/scopes.ts`, `ScopeSelect.vue`; P3-M13-08). The fault dialog's
  target, the profiles' "Activate on" and the rule editor's From offer, after everything, the networks, groups and
  devices, the remote networks of plan §2.4: "Client networks · rA (10.50.0.0/24)" for each WireGuard client with client
  networks, "Remote networks · link site-b (10.60.0.0/24, 10.70.0.0/24)" for each link (its static routes and the routes
  it learned), and "Remote network · a prefix inside them…", which shows a field for the prefix beside the selector.
  They are sent as `remote_network` (`client`, `link` or `cidr`). The prefix is checked in the browser as an IPv4 prefix
  (the message under the selector; Apply, Activate and Preview & apply wait); whether it lies inside a client network or
  a link's route the gateway decides (`unknown_remote_network`, a link with dynamic routing takes any prefix), and its
  message goes under the target too (`fieldOf` maps `…/remote_network/…` to the target). A scope the options do not have
  any more stays selectable under its name. A rule's To keeps the destinations of the model (a remote prefix is an
  address or prefix there, a link or hub its WireGuard network); `Destination` has no `remote_network`.
- **TTL display.** Every overlay and profile activation shows its remaining time on the gateway's clock (M12's `useNow`),
  in the device detail, the overview, the faults screen, the overlay rules and the profile cards; the event stream's
  `overlay_expired` raises the toast and refreshes the lists (overlay events now also refresh the profiles).
- **`</> API` panel** (`src/components/layout/ApiPanel.vue`, `src/stores/apiPanel.ts`, plan §2.17 "every UI action has an
  API equivalent, and the UI can show it"). A toggle in every page header (`aria-pressed`) opens the panel below it. It shows
  what the open dialog would send ("This dialog: Add fault"), else the requests of the last action on the screen ("Last
  action: Add fault" — the `POST /api/v1/overlays` with its body; the candidate, preview and apply of a revision with
  `If-Match`; an explain; a profile activation), else what the screen reads, as HTTP or as curl with `Authorization: Bearer $CHAOSGW_TOKEN`. A new screen
  forgets the action of the one before.
- **Adopt** (M12's dialog) now goes through the draft and the drawer: the dialog asks for the name and says what changes,
  "Preview & apply" puts the device into the draft and opens the drawer; a conflict is the dialog above (P3-M12-03).
- **Commit-confirm and offline** hold the changing controls as in M12: Preview & apply, Apply fault, activate and
  deactivate and removing an overlay are disabled while a revision waits for confirmation or the gateway does not answer;
  editing the draft stays possible.

### Tests

| What | Where |
|---|---|
| Merge patch: no change, changed members, `null` for what went, arrays whole, patch applied gives the target | `src/lib/mergePatch.test.ts` |
| Rules in words, ports, field changes, the effect in words, the editor's checks | `src/lib/rules.test.ts` |
| Fault dialog: the request (checked against the generated Zod schema of `POST /overlays`), one direction, advanced, blackout, custom traffic, a configured fault, an existing fault read back, the checks (also a fault without any parameter), the preview sentence, presets, the gateway's pointers to the form's fields; every fault kind (P3-M13-04): an MTU fault, a tunnel fault, burst loss, flapping, a distribution, loss correlation, a queue limit, each direction its own, every destination, each read back and sent unchanged | `src/lib/faultForm.test.ts` ("every fault kind in the dialog (P3-M13-04)") |
| Draft: base tracking (also after edits undone by hand), change counting, the merge patch with order and deletions, undoing a deletion, devices, invalid rules and the gateway's errors (a fault's by pointer), rebase on another revision, forgotten at a logout; the API panel as HTTP and curl | `src/stores/draft.test.ts` |
| Profile editor: the profile of the form (checked against the generated Zod schema of `POST /revisions`), read back unchanged, each direction its own, an MTU part without a mode kept without it, a kept DNS part, the checks (name, parts, fields), the gateway's pointers below a profile; the draft's profiles (changes, patch, delete and Undo, the gateway's problems, reapply and the fields both changed) | `src/lib/profileForm.test.ts`, `src/stores/draft.test.ts` "a custom profile is added, changed and deleted through the draft (P3-M13-03)", "reapplies a profile on their revision and names a field both changed (P3-M13-03)" |
| Owners in words | `src/lib/faults.test.ts` |
| Overlays name what overrides them | `TestAFaultThatWinsOnlyAPartOfItsTrafficIsPartiallyOverridden` (`internal/api/overlays_test.go`) |
| A rule's last match (the nftables `last` statement, P3-M13-01) | `TestARulesCounterSaysWhenItLastMatched` (`internal/api/rules_test.go`), `TestATransactionCarriesTheLastMatchOverToTheRuleThatReplacesIt`, `TestTheKernelListsTheLastMatchAndTakesItOverFromTheRuleItReplaces` (testbed), `TestARulesLastMatchIsTheKernelsAndAnApplyKeepsIt` (testbed) |
| Screens against the mocked API (Playwright) | `web/e2e/rules.spec.ts`, `faults.spec.ts`, `fault-kinds.spec.ts`, `profiles.spec.ts`, `devices.spec.ts` (adopt through the drawer and the conflict dialog) |
| The browser flow against the real stack | `TestAFaultCreatedInTheBrowserTakesEffectOnTheWire` (`internal/api/e2e_ui_test.go`, tags `testbed,browser`) with `web/e2e-testbed/faults.spec.ts` |

**The mocked API** (`web/e2e/mock.ts`) grew the active revision's configuration (`config`, `GET /revisions/active` with
`created_by`), candidates with their base (an apply on a base that is not active is a 409), `applyFromApi` (someone else
applies a revision, with the domain diff `GET /revisions/{id}/diff` answers), a preview of the candidate's patch, the rules
with counters (`withRules`), `POST /overlays` with the replacement by key (a fault's family and tunnel in the key),
profiles with their activations (custom profiles are the active configuration's, so a revision adds, changes and
removes them, and the preview names them), `GET /explain`,
and switches for a refused candidate (`rejectRevision`), a candidate that answers late (`revisionGate`), a refused overlay
(`rejectOverlay`) and a failed apply (`failApply`); every recorded request has its query parameters.

**The browser flow in the testbed.** `TestAFaultCreatedInTheBrowserTakesEffectOnTheWire` builds the testbed with the
configuration of `testbed_wg.yaml` (network IoT on lan0), lets devices A and B ping their gateway address so the gateway
knows them, measures their round trip to the server, creates a token `ci-runner` and runs `faults.spec.ts`. In Chromium: log
in; on the faults screen add a configured fault of 30 ms on the network IoT (the bar says "1 change in 1 fault"); meanwhile
the spec applies a revision with the token (`POST /revisions` with a new group, then apply); Preview & apply meets the
conflict dialog with the token's name and its change, Reload and reapply mine opens the preview on the new base (Linux
changes with the netem leaf), Apply closes the drawer and the fault is no longer NEW (the spec asserts the lasting state:
the toast is gone after 6 s, which an emulated apply can outlast); on device A's page Add fault shows the jitter error and keeps
Apply disabled, then 100 ms for an hour is applied and appears with its TTL; the faults screen shows the network's fault
overridden by A's. Back in Go: the configured fault, A's overlay and the token's group are in the gateway, A's round trip
grew by 2 × 100 ms and B's by 2 × 30 ms (180–400 ms and 50–150 ms, functional bounds that hold under emulation). Both browser
flows share `runBrowser`, which runs one spec and logs the page's accessibility tree on a failure. CI runs both in the job
`testbed-ui` (`-run InTheBrowser`). Locally: `make build-web && make test-vm ARGS='-tags testbed,browser -run InTheBrowser
-mem 3G -cpus 4 -test-timeout 100m -vm-timeout 150m ./internal/api'`.

**What the VM showed** (kernel 6.8.0-142, emulated, 4 CPUs, 3 GB): both browser flows ran with `-run InTheBrowser`; the
first-start flow passed again after its move onto `runBrowser` (13 minutes). The M13 flow passed in 20 minutes, 10 of them
in the browser (`make test-vm ARGS='-tags testbed,browser -run TestAFaultCreatedInTheBrowser -mem 3G -cpus 4 -test-timeout 80m
-vm-timeout 120m ./internal/api'`): A's median round trip to the server went from 2.5 ms to 210.5 ms, B's from 6.8 ms to
68.6 ms. Its earlier runs found three faults of the spec itself, not of the UI: the TTL expected right after the apply had
lost minutes to the emulated apply, the device's name was read before its page had loaded, and the verified toast had
closed before the assertion looked; each was fixed in the spec. The privileged-container job of CI has not run on this
branch with the M13 flow yet.

### Acceptance map (plan M13 "Tests")

| Plan M13 Tests | Test |
|---|---|
| Playwright — create a fault in the UI, then verify the measured effect in the testbed | `TestAFaultCreatedInTheBrowserTakesEffectOnTheWire` + `web/e2e-testbed/faults.spec.ts` (CI job `testbed-ui`); against the mocked API `faults.spec.ts` "add a fault on the device: presets, the live preview, Apply → the fault with its TTL, a toast and the API panel", "a configured fault goes through the unapplied-changes bar, the preview and the apply" |
| validation errors are shown | `faults.spec.ts` "validation errors are shown under the fields, and Apply waits until the form is valid" (also the gateway's 422 under the field and a fault without any parameter), "a configured fault the gateway refused in a candidate is marked, and its dialog shows why", `rules.spec.ts` "validation: the message stands under the field and Preview & apply waits until the rule is valid", "the gateway's refusal of a candidate is shown in the drawer and under the field"; in the testbed flow the jitter error; `rules.spec.ts` (the same test) a TCP reset selects TCP and another protocol is refused, `src/lib/rules.test.ts`; `faults.spec.ts` "an overlay that expires while its edit dialog is open is not written again for a second: …", `src/lib/faultForm.test.ts` (`keepError`); `rules.spec.ts` "while the apply runs the drawer stays open, so a failed apply is still shown"; `faults.spec.ts` "Explain names …" (a tunnel family by its tunnel) |
| a conflicting change made through the API triggers the conflict dialog | `rules.spec.ts` "a conflicting change made through the API opens the conflict dialog; reload and reapply mine", "reload and reapply where both changed the same rule: their field stays, mine is added" (two revisions named with their authors), "the conflict dialog names a field both changed, their value → mine, and reapplying takes mine (P3-M13-07)", "the conflict dialog names the rule order when both moved rules, and reapplying takes mine (P3-M13-07)", "the conflict dialog names a rule's From both changed, their scope → mine, and reapplying sends exactly mine (P3-M13-07)", "the conflict dialog lists no field when the two changes do not meet", "the concurrent-change dialog can discard the draft instead"; `src/stores/draft.test.ts` "reapplies field by field where both changed the same object: …", "names the fields both sides changed, their value and mine, before reapplying (P3-M13-07)", "names the rule order when both moved rules to different orders (P3-M13-07)", "a scope or a destination both changed: the dialog names it, and reapplying takes mine whole (P3-M13-07)", "names a client field both sides changed; a matrix pair both changed agrees (P3-M13-07)"; `src/lib/mergePatch.test.ts` "names the members both edits changed to different values, a scope as a whole", "reapplies my edit on theirs: their fields stay, a scope I changed is mine as a whole"; "reapplying drops an edit that their revision made already, …", "reapplies a renamed device on theirs: …"; `devices.spec.ts` "adopting while the draft is based on an older revision opens the conflict dialog: reload and reapply"; in the testbed flow with a real token; `concurrent-edit.spec.ts` (a revision applied while an edit dialog is open: a configured fault, a custom profile, a network, a routing protocol and the routing settings, a key rotation — each meets it in the conflict dialog and reapplying keeps their field; the Phase 3 audit); `src/stores/draft.networks.test.ts` "a client edit whose hub their revision deleted is named as dropped and leaves the draft, …" |
| the faults screen marks an overridden fault | `faults.spec.ts` "the faults screen groups by family, sorts by scope, marks overlays and configuration, and marks an overridden fault"; in the testbed flow on the real gateway; backend `TestAFaultThatWinsOnlyAPartOfItsTrafficIsPartiallyOverridden` |
| Scope: access-rules screen (ordered list, editor IF/THEN, counters) | `rules.spec.ts` "the ordered list: …", "when the selected overlay rule is gone, the first rule is selected", "editing a rule marks it CHANGED …", "add, move and delete rules …", "a deleted rule is shown struck through at its place, and Undo brings it back there", "empty: no rules yet …" |
| Scope: unapplied-changes bar, preview-and-apply drawer | `rules.spec.ts` "editing a rule marks it CHANGED and the bar counts the changes; Discard takes them back", "preview and apply: the drawer says what changes, Linux changes on request, Apply → verified toast", "Back to editing discards the candidate and keeps the draft", "a deleted rule is shown struck through …", "a failed apply says which step failed and which revision the gateway restored", "Preview & apply waits while a revision waits for confirmation"; `faults.spec.ts` "closing the drawer while the candidate is created discards it when it arrives"; `src/stores/draft.test.ts` "edits undone by hand leave a clean draft on the newest active revision" |
| Every fault kind in the dialog (P3-M13-04) | `fault-kinds.spec.ts` "an MTU fault: created with the kind MTU / PMTUD, then edited on the faults screen", "a configured fault saved without a change is no change; an edit of an MTU fault writes only what changed", "a configured fault whose burst loss changed is a change: the bar counts it, the row is CHANGED, the revision message names it", "a tunnel fault: edited with the parameters a tunnel takes, and one configured on a link through the draft", "the advanced impairment parameters: burst loss, flapping, a distribution, a queue limit and each direction its own"; `src/lib/faultForm.test.ts` "every fault kind in the dialog (P3-M13-04)"; `src/stores/draft.test.ts` "names every fault field the dialog edits: burst loss values, keep order, any destination and the tunnel (P3-M13-04)", "a fault made \"each its own\" on mine, a flat loss added on theirs: the dialog names the parameters, reapplying takes mine whole (P3-M13-04)", "blackout on mine, flapping on theirs: …", "parameters that combine are still merged member by member: …", "the merge patch of one fault or profile for the editors' API panel: …"; `fault-kinds.spec.ts` "the API panel of a configured fault shows the merge patch the preview sends: a flat fault made \"Each its own\" deletes its flat parameters"; `src/lib/faults.test.ts` "impairment parameter sets" |
| Scope: faults screen explain, edit, remove | `faults.spec.ts` "Explain names …" (with the query it sends), "edit a fault overlay on the faults screen …", "edit a fault overlay of an API token: the admin's fault replaces it, and the dialog says so", "an edit whose old overlay cannot be removed closes with the new fault active and says the old one is left", "removing: an overlay at once, a configured fault through the draft" (the deleted fault marked, Undo), "empty: no faults …" |
| Scope: profile cards | `profiles.spec.ts` (cards, activate and deactivate on a scope with a duration, Apply profile from the device, disabled while a revision waits for confirmation) |
| Custom profiles made in the UI (P3-M13-03) | `profiles.spec.ts` "create a profile through the editor and the drawer, then activate it (P3-M13-03)", "edit and delete a custom profile through the draft; a refused profile shows why (P3-M13-03)" (also: saved unchanged is no change, the edit's merge patch), "a custom profile saved without a change is no change, also where the editor reads it in another form (P3-M13-03)", "delete an active custom profile: the card says its activations end, the drawer lists them and Apply waits for "Remove them" (P3-M13-03)"; `src/lib/profileForm.test.ts`; `src/stores/draft.test.ts` (the P3-M13-03 cases, among them "a profile edit of burst loss values or of a part the chips do not show is a change (P3-M13-03)", "a profile loss changed on mine, burst loss on theirs: the dialog names the parameters, reapplying takes mine whole (P3-M13-03)") |
| Remote networks in the scope selectors (P3-M13-08) | `faults.spec.ts` "a fault on a WireGuard client's networks: the target offers the remote networks and the request carries the scope (P3-M13-08)", "a fault on a prefix inside a remote network: the prefix is checked under the target, the gateway's refusal stands there too (P3-M13-08)" (also a configured fault on a link's prefixes), `profiles.spec.ts` "activate on a link's remote networks or a prefix inside them (P3-M13-08)", `rules.spec.ts` "a rule from a prefix inside a WireGuard client's networks: From offers the remote networks and takes the prefix, the patch carries it (P3-M13-08)"; `src/lib/scopes.test.ts`, `src/lib/faultForm.test.ts` "keeps the choice "a prefix inside a remote network" while its prefix is still empty", `src/lib/rules.test.ts` "checks a rule before it can be applied", `src/lib/faults.test.ts` "names the source by its object" |
| Scope: TTL display | `faults.spec.ts` "an expiring fault counts down on the faults screen and says so when it is gone", the TTLs in the add-fault and profile tests, "editing a fault keeps its expiry; a TTL the dialog does not list is shown and kept (P3-M13-06)" (also the token's overlay taken over in "edit a fault overlay of an API token: …"); `src/lib/faultForm.test.ts` "an edited overlay keeps its remaining time unless another duration is chosen (P3-M13-06)", "a TTL that is not one of the choices is shown as such and kept (P3-M13-06)"; M12's `device-detail.spec.ts` |
| Scope: `</> API` panel | `faults.spec.ts` (the `POST /api/v1/overlays` after Add fault; an open dialog's request in the panel and in the dialog after an action), `rules.spec.ts` (the candidate, preview and apply as HTTP and curl), `src/stores/draft.test.ts` (also an apostrophe in a body or header, quoted for the shell) |

## Web UI: networks, WireGuard, routing and the technical view (M14)

Plan §2.17 ("Networks", "Technical view", "Commit-confirm"), §2.2, §2.2.1, §2.2.2, §2.7 and M14: the network cards and the
detail of a network, the DHCP pool and leases, the WireGuard networks with their client list and the clients' status, the
add-client dialog with the next free tunnel address proposed in the browser, the QR dialog and the download of a client's
configuration, the routing view, the access matrix edited through the draft (with commit-confirm where the gateway asks for
it) and the compiled-state view. The client actions of plan §2.2.1 (disable/enable, rotate keys, delete; P3-M14-01, part
1), the networks themselves (add, edit and delete local test networks with one or several ports, DHCP and DNS, and
WireGuard hubs and links; part 2, which also closes P3-M12-04) and the routing configuration of plan §2.2.2 (the BIRD
settings and the protocols on the links; part 3) go through the draft too.

### How it works

- **Networks** (`src/views/NetworksView.vue`, `/networks` and `/networks/:id`). A card per test network and WireGuard
  network (name, kind, subnet, devices or clients online, DHCP, "degraded" when a port is missing), the management network
  and the uplink (both owned by the OS, not selectable); a card is a link, so the selected network has its own URL (the
  overview's topology links there too). The selected network's settings (`NetworkSettings.vue`) in the user's words: for a
  local network the subnet, the gateway address, the attachment (its ports with presence and link), DHCP (on or off, lease
  time, active leases, the pool — the configured one, or the spec's default, the second half of the subnet without the
  gateway's address (two ranges when the gateway lies inside it, #110), computed by `effectivePools` — with the free addresses the gateway reports, router and DNS options), DNS (the proxy, redirected
  resolvers, DoT blocked, static entries), masquerade, downstream routes and "IPv6 blocked"; for a WireGuard network its
  kind and role, tunnel subnet and gateway address, endpoint (by default the uplink's address from `GET /uplink` with the listen port, "192.168.1.57:51820 (the uplink address)"), MTU, public key and the clients
  online; for a link the remote side's address and endpoint, its online state with the last handshake and bytes, and the
  static and learned routes with a link to the routing view. The bytes of a client and of a link's peer are the remote
  side's, as a device's traffic and a tunnel fault's directions (plan §2.2.1): the gateway's received bytes (`rx_bytes`)
  are what the client or site sent (↑), its sent bytes what it received (↓). A link of the gateway exports, under
  **Export**, its **Remote configuration** (`GET /networks/{id}/export?format=conf`, with the warning that it holds the
  remote side's private key and the text on request) and, when a routing protocol of the active configuration runs on
  it, the **BIRD snippet** (`format=bird`), each as a file (`LinkExportDialog.vue`; plan §2.2.1 "Keys and export"). The
  zip of a hub's client configurations stays an API and CLI export: the UI exports each client in its dialog (the
  fetcher reads text, and one dialog per client keeps the "export once" handling in one place). The Linux names (bridges, WireGuard interfaces) appear only in
  the Linux view (D14). The leases of a local network (`LeasesCard.vue`, `GET /networks/{id}/leases`): address, MAC, host
  name, the device it belongs to (a link) and the remaining time on the gateway's clock.
- **WireGuard clients** (`ClientsCard.vue`, `GET /networks/{id}/clients`, polled every 5 s and refreshed by the
  `wireguard_peer_*` events): name, tunnel address, client networks, Online/Offline with a label (a handshake younger than
  3 min, plan §2.2.1) and the last handshake, the endpoint the gateway sees it at and its bytes. A client of the draft is
  marked NEW ("not applied yet", without its configuration until it is applied), with **Edit** (the add-client dialog
  with its fields, "Edit client of <hub>") and **Undo** (it leaves the draft, the other changes stay); one the gateway
  refused is marked NEEDS FIXING, its row says why as text ("The gateway refused it: …"), and **Fix** opens it again in
  the add-client dialog. A refused action on a client of the gateway (a rotation, a disable) is marked the same way in
  its row, which keeps its actions (rotate again, Undo).
- **Client actions** (`ClientsCard.vue`, `RotateKeysDialog.vue`, `withEnabled`, `withRotatedKeys`, `rotatedKeyError`,
  `keysText` in `src/lib/networks.ts`; plan §2.2.1, P3-M14-01). A client of the gateway has **Disable**/**Enable**,
  **Rotate keys** and **Delete** next to Configuration. Each is an edit of the configuration draft (`setClient`):
  disabling sets `enabled: false`, enabling drops it (the default); rotating raises `key.generation` for keys the
  gateway generates (it makes a new key pair and a new preshared key at the apply, the dialog says the client needs its
  new configuration then, and the row says so until the apply) or, for a client with its own key, sets the new public
  key the dialog asks for (checked under the field: a WireGuard key, not the current one; the gateway refuses a
  generation for such a key); deleting marks the row DELETED. A changed row is marked CHANGED, and **Undo** takes the
  client back to what the gateway has. The changes in words name the field ("enabled: yes → no", "keys: generation 1 →
  generation 2"), the merge patch carries only the changed members (`{"enabled": false, "key": {"generation": 2}}`,
  `null` for a deleted client), and Preview & apply goes through the drawer, the concurrent-change dialog (reapplying on
  their version of the client) and commit-confirm, which the gateway asks for when the client belongs to a management
  hub.
- **Add client** (`AddClientDialog.vue`, logic in `src/lib/networks.ts`). The tunnel address is proposed in the browser
  (`nextFreeAddress`): the lowest host address of the hub's subnet that is not the gateway's own, not the network or broadcast
  address and not taken by a client of the gateway or of the draft. Name and address are the common case; Advanced has the
  client networks, what the client may reach (networks, other clients' networks as `{client: <id>}`, the Internet as a full tunnel; `reachChoices`), its DNS server (the gateway, none,
  other servers), the keepalive and the keys (generated by the gateway, or the client's own public key; a preshared key;
  "export once"). The checks of the spec stand under their fields (`clientFormErrors`: the name pattern and the device
  namespace, an address inside the hub's subnet that is free, client networks as prefixes outside the hub's subnet, DNS
  servers, a duration, a WireGuard public key), from the first Preview & apply on; the address from the start, since it
  was proposed. "Preview & apply" puts the client into the configuration draft (`setClient`: a merge patch below its hub,
  `networks.<hub>.clients.<new uuid>`) and opens the preview-and-apply drawer, like adopting a device; the `</> API` panel
  shows the candidate while the dialog is open. A hub with the role management makes the change lockout-relevant, and the
  dialog says that the apply will wait for confirmation. A client the gateway refused (422, its errors mapped by
  `setServerErrors` from `/networks/<hub>/clients/<id>/<field>`) opens again as "Fix client of <hub>" with its fields as
  they were (`formFromClient`), each message under the field it is about (`clientFieldOf`; one about the client as a whole,
  or about a part the form does not show, below the form) until the form is changed; the unchanged form is not sent again,
  and the next candidate carries the same client id.
- **QR code and download** (`ClientConfigDialog.vue`, `src/lib/qr.ts`). The dialog exports the configuration once
  (`GET /networks/{id}/clients/{client}/export?format=conf`, audited by the gateway) and makes both the QR code (in the
  browser, with `qrcode-generator`: byte mode, error correction M, drawn as an SVG path with a quiet zone) and the `.conf`
  download (a Blob) from that one text. So the code and the file are always the same, and a client with "export once" is
  exported once only: the gateway deletes its key after the first export, which a second request for the PNG or SVG would
  count as. The dialog warns that the configuration holds the client's private key (or says that it has the placeholder
  `<PRIVATE_KEY>` when the gateway holds none), and shows the text on request. Every opening counts: an export that
  answers after the dialog was closed or opened for another client is dropped, so the dialog never shows another client's
  code or file under this client's name. An "export once" text is the only copy of the key and is never dropped: an
  opening for the same client while its export is in flight waits for it instead of exporting again (which would only get
  the placeholder), and an answer no opening showed is kept in memory and shown by the next opening.
- **Access matrix** (`AccessMatrix.vue`, logic in `src/lib/networks.ts`). Rows are the test and WireGuard networks, columns
  the Internet (uplink), the networks and the management network; a cell is Allowed or Blocked with a text label, "default"
  when no entry exists (the spec's default: a local test network reaches the uplink, everything else is blocked; a column
  is at least 96 px wide, its header truncated with the name as its title, and with many networks the matrix scrolls
  sideways inside its card), and a
  button with `aria-pressed` and a label that says the pair, the state and whether it is the default or changed. A click
  changes the draft (`toggleMatrix`): the policy flips, and an entry that would only say what the default says is removed
  instead, so the configuration keeps the entries that matter; an entry may name a network by its name or UUID. The bar
  counts the changed pairs ("2 changes in the access matrix"), the drawer previews them, and the merge patch carries the
  whole `access_matrix.entries` array (a merge patch replaces arrays). Reapplying after a concurrent change keeps their
  entries and puts the pairs the user changed on top (`rebase`, pair by pair). Entries of a single WireGuard client and the
  implicit allows of a client's `reachable` list are named below the matrix, not edited there. "Show Linux view" shows the
  compiled forward chain. The entries come from the active revision (`GET /revisions/active`): until it is loaded the
  matrix says "Loading the access matrix…", and when it fails "The access matrix is not loaded" with Try again, never the
  defaults as if they were the policy. The uplink card and the management card say "Not loaded" the same way.
- **Commit-confirm** (plan §2.17 "Commit-confirm only if it is lockout-relevant"). The gateway decides (`LockoutRelevant`:
  the management settings, the uplink in the two-port topology, WireGuard networks with the role management); the preview
  says so, the apply answers `pending_confirm`, and the orange banner counts down with **Confirm** and, since M14, **Roll
  back now** (P3-M12-01): `POST /revisions/{id}/rollback` runs the rollback the deadline would run. The rollback event now
  carries `data.restored`, the revision that is active again, and its toast says "It was not confirmed in time. Rolled back
  to revision 42." (or "Rolled back on request. …"). The resource views (`/networks`, its clients, …) show the active
  revision, and while a revision waits for confirmation the active one is still the one before (the gateway's store), so a
  client of a management hub is listed once it is confirmed; the dialog says so. A confirmation (the button or the event
  `revision_confirmed`) therefore refreshes the views of the configuration, not only the state.
  An access matrix change is not lockout-relevant in the backend (the control plane is gateway protection, not the matrix),
  so it is applied at once; a client of a management hub is the change of the networks view that waits for confirmation
  (decided 2026-10-14, P3-M14-02: M14's Scope says "access matrix editing (commit-confirm where lockout-relevant)").
- **Network forms** (`NetworkDialog.vue`, logic in `src/lib/networkForm.ts` and `src/lib/networkChanges.ts`; plan §2.2,
  §2.2.1, §2.7, §2.6; P3-M14-01, part 2). **Add network** in the page header opens the form with the kind (Test network,
  WireGuard hub, WireGuard link) and a proposal (`proposeAddress`: the first free 10.N.0.1/24 from 10.30 on, a hub from
  10.90 on, a link the first free /31 of 10.255.0.0/24; `proposeListenPort`: the first free UDP port from 51820 on). A
  local network has its name, gateway address with the prefix (the subnet is shown as the hint), its **ports** — one or
  several physical interfaces of its bridge, chosen from the host's interfaces (`GET /system/interfaces`, with MAC, driver,
  link and "configured by the OS"); the uplink and the management interface and the ports of other networks of the draft
  cannot be chosen, and a port the network has that the host does not show stays listed — DHCP (on/off, pools as
  "start-end", lease time; under Advanced the router, DNS, domain and NTP options and custom options as "code value"
  lines), DNS (redirect hardcoded resolvers, block DoT; static entries as "name address, …" lines), downstream routes
  ("10.40.0.0/24 via 10.30.0.254") and masquerade. A WireGuard network has its role (test, or management: the form says
  that the apply will wait for confirmation), listen port, MTU and public endpoint; a link the remote side's address (the
  other address of the /31, following the transfer network), its endpoint (the gateway connects when it is set), keepalive,
  keys (generated, or the remote side's public key; a preshared key) and enabled switch, and its static routes. The checks
  of the spec and of the gateway's validation that the browser can make stand under their fields (`networkFormErrors`:
  the name pattern and a name no other network has, a host address with a prefix of at most /30 — a link /31 — that
  overlaps no other network of the draft and no reserved range (0/8, 127/8, 169.254/16, 224/4, 240/4), at least one port
  and none of the uplink, the management network or another network, pools inside the subnet, in order, without the
  gateway address and not overlapping, durations, a lease time of at least 1 s, IPv4 options, option codes once, from 1
  to 254 and none the gateway sets itself (`reservedDHCPOptions`: 1, 3, 6, 12, 15, 28, 42, 50, 51, 53, 54, 55, 58, 59,
  61, 82, 255), host names, route destinations without host bits and outside the reserved ranges, routers inside the
  subnet, a free listen port, MTU 1280 to 1500, endpoints as host:port, the peer inside the transfer network, a WireGuard
  key); the address from the start, the rest from the first Preview & apply on. DHCP switched off hides its fields and
  takes them out of the checks; the pools stay in the network for when it is switched on again, unless the subnet changed
  so that they no longer fit (the gateway checks them also while DHCP is off), and a message of the gateway about a hidden
  DHCP field stands with the messages no field shows. `networkFromForm` builds the network from the one the draft has, so what the form does
  not edit stays (a hub's clients, a link's key generation, an explicit default), a new port is named by name and MAC like
  the setup does, and a port the configuration names by MAC only keeps that reference: the port picker shows it ticked on
  its host interface (`hostPortKey`), once, and unticking removes it. A check of a field under **Advanced** opens Advanced
  (`advancedFields`); hidden again, "Fix the fields under Advanced." says where the problem is. The network goes into the draft
  (`setNetwork`: the network's own settings, without the clients of a hub, which `clients` holds) and the drawer opens; the
  merge patch carries a new network whole and of a changed one only the changed members (`{"interfaces": [...], "dhcp":
  {"pools": [...]}}`), with the client edits of a hub in the same `networks.<id>` entry. The selected network has **Edit**,
  **Delete** and, for a network of the draft, **Undo**; its card and its settings are marked NEW, CHANGED or DELETED and
  show the draft's settings (a new network is listed as "not applied yet", without leases or clients until the apply; undone,
  deleted or discarded before the apply, the page goes back to the list). A
  deleted network takes the client edits of its hub along (the patch is `null`). The gateway refuses a deletion while
  something still refers to the network (`unknown_reference` at the referring object: a device, a probe, a rule, a fault,
  a matrix entry, a routing protocol, a client's `reachable`); the drawer lists each by its object and field and says
  "refers to IoT, which this change deletes; remove the reference first or keep the network", and an overlay the deletion
  would orphan is listed under "Applying removes what refers to a deleted object" by its object ("Network IoT"). A network
  the gateway refused (422, mapped by `setServerErrors` from `/networks/<id>/<pointer>`) says so in its settings, and
  **Fix** opens the form with each message under the field it is about (`networkFieldOf`) until the form is changed. The
  changes in words (`networkChanges`) name a new network ("added (test network 10.30.0.0/24, ports eth3, eth4)") and the
  fields of a changed one (ports, DHCP, DNS, routes, role, listen port, MTU, endpoint, peer, and the peer's keepalive,
  key, preshared key and enabled switch). The concurrent-change dialog
  compares and reapplies networks like the other objects (their settings without the clients). Commit-confirm is the
  gateway's: a WireGuard network with the role management, added, changed or deleted, is lockout-relevant.
- **Routing forms** (`RoutingView.vue` card "Configuration", `ProtocolDialog.vue`, `RoutingSettingsDialog.vue`, logic in
  `src/lib/routingForm.ts`; plan §2.2.2; P3-M14-01, part 3). The card shows the routing as the draft has it: router id,
  local AS, external mode, and a row per protocol (name, type, link, its settings, what it announces and imports,
  "unmanaged snippet", NEW/CHANGED/DELETED, Edit/Fix, Delete, Undo). **Settings** edits the router id, the local AS (required
  while a BGP protocol exists) and the external mode (kernel table — not one of the system's or Chaos Gateway's own,
  100 to 110 and 253 to 255 — with its import filter; switched off, its hidden fields are neither checked nor sent, and
  the table and filter stay as they were). **Add protocol** chooses BGP, OSPF or Babel, its link (the WireGuard
  links of the draft; one protocol of each type per link), what it announces (the networks, the clients of the hubs with
  client networks, further prefixes), the import filter (allowed prefixes one per line, "10.70.0.0/16 le 24" for longer
  ones up to /24; the default route; the most prefixes), the settings of its type (BGP: neighbor AS, neighbor address —
  the link's peer by default —, hold and keepalive time, at least three keepalives per hold time, passive; OSPF: area,
  hello and dead interval, the dead one longer, cost; Babel: hello interval, receive cost), the enabled switch and a custom
  BIRD snippet. `protocolFromForm` keeps what the form does not edit and drops the settings of another type (the gateway
  refuses them). The routing goes into the draft whole (`setRouting`, `setProtocol`, `restoreProtocol`; a routing back at
  its base is no change), its merge patch against the base (`routing.protocols.<id>` whole for a new protocol, the changed
  members otherwise, `null` for a deleted one), and its changes in words per protocol ("protocol site-b-bgp neighbor AS:
  65002 → 65003", each timer and switch of its type as a change of its own: hold and keepalive time, passive, hello and
  dead interval, cost, rxcost). Announced prefixes and the prefixes of an import filter with host bits set are refused
  under their field, as the gateway does. The gateway's messages about `/routing/protocols/<id>/…` and `/routing/…` go to the protocol and to the
  settings (`protocolFieldOf`, `routingSettingsFieldOf`); the concurrent-change dialog names a field both changed by the
  protocol's name and reapplies member by member. The status of the sessions below stays the gateway's.
- **Routing** (`src/views/RoutingView.vue`, `/networks/routing`): the BIRD instance (running, version, router id, ASN, last
  configuration, last error, external mode), a card per session (`GET /routing/status`, every 5 s; BGP, OSPF, Babel) with
  its link, its state with BIRD's detail ("up · Established") and since when, imported, filtered and exported routes, the
  import limit when it is hit, the announced and the received prefixes, and "unmanaged snippet" for a protocol with a custom
  snippet; then the routes of Chaos Gateway's tables (`GET /routing/routes`) with filter chips by origin (connected, static,
  client, learned, external). The `routing_*` events refresh it. ASN, the link of a session and the external mode come
  from the routing configuration (`GET /routing`); when it fails, the BIRD card says so.
- **Technical view** (`src/views/LinuxStateView.vue`, `/linux`, and `LinuxExcerpt.vue` behind "Show Linux view" on a
  network and on the matrix). Backend: `GET /state/linux` (`internal/api/system.go`, `engine.LinuxView`) reads the kernel
  state of the applied target through the executor (read operations only, `apply.ReadState` with the target's tc
  interfaces) and renders it as text (`apply.Render`): the table `inet chaosgw` in nft syntax with the counters of its rules
  and named counters (`linux.RenderNft`, from the `nft -j` listing: matches, verdicts, counters, sets and maps with their
  elements, vmaps, mangle, reject, NAT; an expression it does not know is shown as its JSON, so nothing is hidden; a set
  shows at most 256 elements and how many more; the generation chain is left out), the tc trees with the counters of every
  qdisc, class and filter, the bridges with their addresses and ports, the policy rules and routes of Chaos Gateway's tables,
  and the WireGuard interfaces with their peers (public keys, allowed IPs, endpoint, handshake, bytes; never a private or
  preshared key). The page shows the four parts (or one), filtered by a word, and reads again every 5 s, and at once after
  an event that changes what the gateway applied (an apply, a revision, an overlay); its query key `['linux-state']` is
  outside the `state` prefix, so the many events that refresh the state (counters, a degraded network, a pending
  confirmation) do not each run nft, tc, ip and wg. The excerpt of a
  network keeps the lines that name its interface, its subnet or its gateway address (whole words), with the table and the
  chain, set or interface around them (`src/lib/linuxView.ts`).
- **The Linux view of one object** (P3-M14-03, decided option (a); plan §2.17 "Per object, the compiled configuration next
  to the domain view … reached from 'Show Linux view'"). "Show Linux view" on a device (the device detail's identity card),
  an access rule (the rule editor, also an overlay rule and the system rule) and a fault (a row of the faults screen of an
  impairment, MTU or tunnel fault or a profile activation; DNS, TLS, DHCP and WireGuard faults are carried out by the
  gateway's services and offer none, and an empty view of a fault says so) opens
  `LinuxObjectExcerpt.vue`, which reads `GET /state/linux/{kind}/{id}` every 5 s while it is open. The gateway cuts the
  kernel state down to the objects the compiler made for it, by the names and keys the applied target gave them
  (`engine.LinuxObjectView`, `apply.RenderObject`): for a rule (by id, name, or `system:anti_lockout`) its rule in both
  hooks with `counter name` and `last used`, its named counter and the set of its sources; for a fault (a configured fault
  or an overlay, a profile activation too) the chain `mark_<id>` of every fault id it has with its counters, the
  classification elements that go to it (the maps' elements whose value is the chain), the tc classes `1:<minor>` on every
  interface with their netem leaves and the fw filters that lead to them, a tunnel fault's ingress filter, and an MTU
  fault's chain, counters and elements; for a device (by id or name) the identity map element with its numeral and the
  elements of the classification maps (`cls_*`, `pmtu_*`) whose source covers one of its addresses, and the queues of the
  faults that have a queue of their own for it. The map names' bases are `compiler.IdentityMapBase`, `ClassifyMapBase` and
  `PMTUMapBase`. `keys` lists every kernel object found ("counter rule_…", "chain mark_7", "map ident4_… 10.10.0.31",
  "class br-iot 1:12"); an object the kernel holds nothing of (an unknown id, a disabled rule) is answered with empty
  texts, and the UI says so.
- **Navigation.** Networks is a link now, and a device's network (in the device table and the device detail) links to
  `/networks/{id}`; Routing and the Linux view are reached from the Networks page (plan §2.17: the
  technical view is "never needed for normal use").

### Tests

| What | Where |
|---|---|
| IPv4 of the networks, the default DHCP pool, the next free tunnel address, the add-client checks and the client it builds (checked against the generated Zod schema), the matrix: defaults, entries by name, a toggle that keeps entries minimal, changes in words | `src/lib/networks.test.ts` |
| The QR code decodes (jsQR) to the configuration it was made from; the SVG path | `src/lib/qr.test.ts` |
| The Linux excerpt: whole words, nftables lines with their table and chain, a chain kept whole, groups of routes and tc | `src/lib/linuxView.test.ts` |
| Draft: a client as a change, a merge patch below its hub and the gateway's errors for it; a matrix cell flipped and back, the entries replaced whole; reapplying both on their revision | `src/stores/draft.test.ts` |
| The rollback toast names the restored revision, at the deadline and on request | `src/composables/eventStream.test.ts` |
| Routes of the networks, routing and Linux views | `src/router.test.ts` |
| `POST /revisions/{id}/rollback`: rolled back at once, the event with `reason` and `restored`, refused afterwards, audited once, as the caller's and not also as the system's (the Phase 3 audit) | `TestARevisionWaitingForConfirmationIsRolledBackOnRequest` (`internal/api/revisions_test.go`) |
| `GET /state/linux`: the table in nft syntax with counters, the matrix rule, a netem queue with counters, bridges and routes, WireGuard peers, no key | `TestTheLinuxStateShowsTheKernelWithItsCounters` (`internal/api/linux_test.go`) |
| The nft renderer: the recorded `nft -j` listing, named counters, maps, cut sets, the expressions of the compiler, an unknown one as JSON | `internal/linux/nftrender_test.go` |
| `GET /state/linux/{kind}/{id}`: a rule's rules, counter and source set, a fault's mark chain, counters, elements, classes and netem queues, a device's identity and classification elements, nothing of another object, by id and name (a name only one rule can carry), empty for an object the kernel lacks (P3-M14-03) | `TestTheLinuxViewOfOneRuleFaultOrDeviceShowsWhatTheCompilerMadeForIt`, `TestTheLinuxViewFindsARuleByANameThatOnlyOneRuleCanCarry` (`internal/api/linuxobject_test.go`), `TestRenderObjectShowsOnlyWhatTheSelectionNames` (`internal/apply/objectview_test.go`), `TestTheLinuxViewSelectsAnObjectByTheNamesTheCompilerGaveIt`, `TestAMapKeyCoversAnAddressAsAnAddressPrefixOrRange` (`internal/engine/linuxview_internal_test.go`) |
| A client of the draft opens again as the form that made it; the gateway's messages by field | `src/lib/networks.test.ts` "opens a client of the draft again as the form that made it", "puts the gateway's messages under the field they are about" |
| The PNG QR code of the gateway (`wireguard.QRPNG`): every module a square of whole pixels, 100 codes of random configurations decode | `TestEveryQRCodeDecodes` (`internal/wireguard/export_test.go`) |
| The network form: a test network with several ports, DHCP and DNS and a hub and a link built as the spec accepts them (the generated Zod schema), the checks under the fields (also reserved ranges and option codes, host bits, the shortest lease), the hidden pools with DHCP off and a changed subnet, the form of a network and what it keeps, a port named by MAC only in the port picker, the fields under Advanced, the gateway's messages by field, the proposals (P3-M14-01) | `src/lib/networkForm.test.ts` |
| The routing forms: durations and prefix filters, an OSPF protocol and the BIRD settings as the spec accepts them, the checks (the external mode's filter only while it is on; host bits in announced and allowed prefixes), the changes in words of each timer, what they keep, the gateway's messages by field (P3-M14-01) | `src/lib/routingForm.test.ts` |
| Draft: a network added (several ports), changed (a hub without its clients, with a client in the same entry), deleted (with its client edits), the gateway's messages for networks, protocols and the routing settings, the routing's changes and merge patch, reapplying networks and the routing on their revision (P3-M14-01) | `src/stores/draft.networks.test.ts` |
| Deleting a network that is referenced: `unknown_reference` at every referring object with the network's id in the message; deleted with its references, it goes | `TestDeletingAReferencedNetworkNamesEveryReference` (`internal/domain/candidate_test.go`) |
| Screens against the mocked API (Playwright) | `web/e2e/networks.spec.ts`, `web/e2e/network-forms.spec.ts` (the network and routing forms) |
| The browser flow against the real stack | `TestAWireGuardClientCreatedInTheBrowserBringsUpItsTunnel` (`internal/api/e2e_ui_test.go`, tags `testbed,browser`) with `web/e2e-testbed/wireguard.spec.ts` |

**The mocked API** (`web/e2e/mock.ts`) grew the networks of the testbed's configuration (`withNetworks`: IoT and Test, the
hub lab-hub with the client rA, the management hub admin, the link site-b with a BGP session), network views whose
configuration follows the active revision (a client an apply added is listed), the clients with their status
(`clientStatus`), leases, a client export (a made-up configuration, the placeholder after an "export once"), the routing
status and routes, the Linux state, commit-confirm as the gateway decides it (a change of the management settings or of a
management hub answers `pending_confirm` with a deadline `confirmWindowMs` away and blocks other applies), `POST
/revisions/{id}/rollback`, and `rollBackPending`, which rolls the waiting revision back and returns the event the gateway
would send. The preview names added clients and the matrix change and says whether the change needs a confirmation.
`rejectRevision` may be a function of the merge patch (a refusal that names the new client's id), and `exportGate` holds
the next client export back (a slow export). For the network and routing forms (P3-M14-01) it lists the host's interfaces
(`GET /system/interfaces`, `hostInterfaces`), lists the networks of the active configuration once it has networks (an
applied new network appears, a deleted one goes; a local network's ports follow its configuration), answers `GET
/routing` from the active configuration, names added, changed and removed networks and routing protocols in the preview,
and refuses a candidate that deletes a network something still refers to (`danglingReferences`: rules, faults, devices,
the matrix, routing protocols, each as `unknown_reference` at its pointer), as the gateway does. In `withNetworks` the
management network has an interface of its own (`eth5`).

**The browser flow in the testbed.** `TestAWireGuardClientCreatedInTheBrowserBringsUpItsTunnel` builds the testbed with the
configuration of `testbed_wg.yaml`, polls the WireGuard state every second and gives commit-confirm a window of 90 s. In
Chromium: log in; on the hub lab-hub add the client rC (the proposed address 10.99.0.3), preview it (the Linux changes
have its peer), apply; open its configuration, decode the QR code from a screenshot of it (jsQR) and compare it with the
downloaded file, save the file where the Go test waits for it. The Go test brings the tunnel up from that file in the
remote client's namespace (`wg-quick up`) and pings the hub through it; the browser waits until the client list shows rC
online. Then on the management hub admin add a client (proposed 10.98.0.3): the preview says that the change can cut the
admin off, the apply waits for confirmation, the banner counts down with Roll back now and Confirm; nobody confirms, and
the client is not listed while the revision waits (the views show the active revision), the toast "Revision N rolled back ·
It was not confirmed in time. Rolled back to revision …" appears and the banner goes. Back in Go: the saved configuration has rC's address and a private key, the tunnel
carries traffic, the API has rC online, the management hub's client does not exist, and nothing waits for confirmation.
CI runs it with the other browser flows in the job `testbed-ui` (`-run InTheBrowser`). Locally: `make build-web && make
test-vm ARGS='-tags testbed,browser -run TestAWireGuardClientCreatedInTheBrowser -mem 3G -cpus 4 -test-timeout 80m
-vm-timeout 120m ./internal/api'`.

**What the VM showed** (kernel 6.8.0-142, emulated, 4 CPUs, 3 GB): the M14 flow passed in 18 minutes, 9 of them in the
browser (`make test-vm ARGS='-tags testbed,browser -run TestAWireGuardClientCreatedInTheBrowser -mem 3G -cpus 4
-test-timeout 80m -vm-timeout 120m ./internal/api'`): the QR code decoded from the screenshot equalled the download, the
tunnel came up from the saved file and rC showed online. Its first run found a real gap: the spec expected the management
hub's client in the list while its revision waited for confirmation, but the views show the active revision, which is
still the one before; the dialog now says that the client is listed once confirmed, a confirmation refreshes the views,
and the mocked API keeps the active revision while one waits, as the gateway does. The privileged-container job of CI has
not run on this branch with the M14 flow yet.

### Acceptance map (plan M14 "Tests")

| Plan M14 Tests | Test |
|---|---|
| Playwright | `networks.spec.ts` (all tests below), against the mocked API; the flow against the real stack in the last row |
| commit-confirm — an unconfirmed change is rolled back after the timeout and the UI shows it | `networks.spec.ts` "commit-confirm: a client of the management hub waits for confirmation; unconfirmed, it is rolled back after the timeout and the UI shows it", "commit-confirm: Confirm makes the client of the management hub active", "commit-confirm: Roll back now ends the waiting at once"; `states.spec.ts` "commit-confirm timeout: the rollback is announced"; in the testbed flow on the real gateway; backend `TestAnUnconfirmedRevisionIsRolledBack`, `TestARevisionWaitingForConfirmationIsRolledBackOnRequest`; `networks.spec.ts` "edits made while the own revision waits for confirmation move onto it once confirmed, with no conflict dialog" |
| create a WireGuard client in the UI, scan its QR in the test (decoded content equals the download) and bring the tunnel up in the testbed | `TestAWireGuardClientCreatedInTheBrowserBringsUpItsTunnel` + `web/e2e-testbed/wireguard.spec.ts` (CI job `testbed-ui`); against the mocked API `networks.spec.ts` "add a client: the next free address is proposed, …", "the QR code of a client decodes to the configuration the download saves, from one export", "a client exported once: …"; `src/lib/qr.test.ts` |
| Scope: network cards and detail, DHCP pool/leases | `networks.spec.ts` "network cards and the detail of a test network: subnet, gateway, attachment, DHCP pool and leases, DNS, IPv6", "empty and failed: …", "failed: an access matrix, an uplink or a routing configuration the gateway did not deliver says so instead of showing defaults"; `src/lib/networks.test.ts` (the default pool) |
| Scope: WireGuard networks with client list, client status | `networks.spec.ts` "a WireGuard hub lists its clients with their status; a link shows its peer and its routes", "a client that goes offline is shown offline after the next poll", "backing out of the drawer keeps the new client in the draft, marked NEW, …", "a new client not applied yet can be edited and undone on its own; the other changes of the draft stay", "a refused action on a client of the gateway is marked in its row, with what the gateway said and its actions" (the traffic arrows are the remote side's in the first test) |
| Scope: add-client dialog (proposes the next free tunnel address of the hub, computed client-side) | `networks.spec.ts` "add a client: …" (the proposal, the checks under the fields, the request, the next proposal after the apply), "a new client the gateway refuses says why in its row; Fix opens it again with the message under its field"; `src/lib/networks.test.ts` "the next free tunnel address of a hub (plan M14, computed client-side)", "the add-client form"; other clients' networks as a choice: "add a client: …", `src/lib/networks.test.ts` "offers the other clients' networks as what a client may reach, …" |
| Scope: QR dialog and config download | `networks.spec.ts` "the QR code of a client decodes to the configuration the download saves, from one export", "a client exported once: …", "a slow export of one client never shows up in the dialog of another", "an export-once key is never lost: …", "an export-once answer that arrives while no dialog is open …"; the testbed flow; a link's remote configuration and BIRD snippet (plan §2.2.1): "a link exports its remote side's configuration, with the secret warning, and the BIRD snippet of the protocol on it", "a link without a routing protocol offers no BIRD snippet" |
| Client actions: disable/enable, rotate keys, delete through the draft, the drawer, the conflict dialog and commit-confirm (P3-M14-01, part 1) | `networks.spec.ts` "client actions: disable and rotate the keys of a client through the draft, the drawer and the apply; Undo takes them back (P3-M14-01)", "client actions: a client the configuration has with enabled: true is no change after Disable and Enable, and Undo goes back to it (P3-M14-01)", "client actions: a client changed meanwhile through the API opens the conflict dialog, and reapplying keeps their change (P3-M14-01)", "client actions: a draft rebased onto another revision's key rotation shows no rotation of its own (P3-M14-01)", "client actions: delete a client, Undo brings it back, deleted through the drawer it is gone (P3-M14-01)", "client actions: a client with its own key rotates by its new public key, checked under the field (P3-M14-01)", "client actions: disabling a client of the management hub waits for confirmation (P3-M14-01)"; `src/lib/networks.test.ts` "the client actions (P3-M14-01)"; `src/stores/draft.test.ts` "the client actions are changes in words and a merge patch of their fields (P3-M14-01)" |
| Network forms: create, edit and delete local test networks (several ports, DHCP, DNS) and WireGuard hubs and links through the draft, the drawer, the conflict dialog and commit-confirm; validation errors under the fields; what still refers to a deleted network (P3-M14-01, part 2) | `network-forms.spec.ts` "network forms: add a test network with several ports through the drawer; after the apply it is listed with both ports (P3-M12-04)", "network forms: edit a test network — a second port, a pool and DNS; the patch has only what changed, Undo takes it back (P3-M12-04)", "network forms: delete a test network through the drawer; Undo before brings it back", "network forms: deleting a network that a device, a fault, rules, the access matrix and the routing still refer to shows what the gateway reports", "network forms: a port the configuration names by MAC only is shown ticked on its host interface, once, and can be removed", "network forms: a check of a field under Advanced opens Advanced; hidden again, the form says where the problem is", "network forms: the checks stand under the fields, and Preview & apply waits until the form is valid", "network forms: a network the gateway refuses says why; Fix opens it again with the gateway's message under its field", "network forms: a WireGuard hub is added, and edited — its clients stay out of the patch", "network forms: a WireGuard link is added with its peer and routes, and edited", "network forms: a management WireGuard network is lockout-relevant — the apply waits for confirmation, Confirm makes it active" (and the management hub admin deleted, confirmed and gone), "network forms: deleting a WireGuard link its routing protocol runs on is refused with the protocol named; deleted with the protocol, the link is gone", "network forms: a network changed meanwhile through the API opens the conflict dialog, and reapplying keeps their change", "network forms: a new network of the draft that is undone or deleted again goes back to the list, not to "not found"", "network forms: DHCP switched off and the subnet changed — the hidden pools that no longer fit leave the network, so the gateway does not refuse them"; `src/lib/networkForm.test.ts`; `src/stores/draft.networks.test.ts`; backend `TestDeletingAReferencedNetworkNamesEveryReference` |
| A LAN network with several ports (P3-M12-04) | `network-forms.spec.ts` "network forms: add a test network with several ports through the drawer; after the apply it is listed with both ports (P3-M12-04)", "network forms: edit a test network — a second port, a pool and DNS; the patch has only what changed, Undo takes it back (P3-M12-04)"; `src/lib/networkForm.test.ts` "a test network with several ports, DHCP and DNS: no problem, and the network the spec accepts" |
| Routing forms: the BIRD settings and the protocols of plan §2.2.2 through the draft and the drawer; validation errors under the fields (P3-M14-01, part 3) | `network-forms.spec.ts` "routing forms: add an OSPF protocol on a link through the drawer; it is listed with what it announces and imports", "routing forms: edit and delete a protocol and change the BIRD settings through the draft; Undo takes each back", "routing forms: the external mode switched off again takes its hidden fields out of the checks and keeps them out of the patch", "routing forms: the checks stand under the fields; a protocol the gateway refuses opens again with the gateway's message under its field"; `src/lib/routingForm.test.ts`; `src/stores/draft.networks.test.ts` |
| Scope: routing view (BIRD sessions, received and announced prefixes) | `networks.spec.ts` "the routing view: BIRD, the sessions with their announced and received prefixes, the routes by origin", "the routing view without dynamic routing says so", "failed: an access matrix, an uplink or a routing configuration …" |
| Scope: access matrix editing (commit-confirm where lockout-relevant) | `networks.spec.ts` "the access matrix: a click changes a cell in the draft, the bar counts it, the preview and the apply" and the commit-confirm tests above; `src/stores/draft.test.ts` (the matrix in the draft and its rebase); `src/lib/networks.test.ts` "the access matrix" |
| Scope: compiled-state view (nftables, tc, routes, WireGuard) | `networks.spec.ts` "the Linux view: the kernel state with its counters, by part and filtered", the Linux view of a network and of the matrix; `src/lib/linuxView.test.ts`; backend `TestTheLinuxStateShowsTheKernelWithItsCounters`, `internal/linux/nftrender_test.go` |
| "Show Linux view" on a device, an access rule and a fault (plan §2.17 "Technical view", P3-M14-03) | `linux-view.spec.ts` ""Show Linux view" on a rule shows its compiled rule and counter; another rule closes it, one the kernel lacks says so (P3-M14-03)", ""Show Linux view" on a fault shows its mark chain, classes and netem queues with their counters below its row; a DNS fault has none (P3-M14-03)", ""Show Linux view" on a device shows its identity element and the classification of its address (P3-M14-03)"; backend `TestTheLinuxViewOfOneRuleFaultOrDeviceShowsWhatTheCompilerMadeForIt` |

## Port forwarding (M35a)

Phase 3.1, pulled forward ahead of Phase 4 (plan D34): a device in a local test network is reachable
from behind the uplink or, in the three-port topology, from the management network, through a DNAT
on the entry side's address. The access rules decide forwarded connections with the device as
responder; faults do not apply to them before M35b. Its open items in `docs/open-items.md` are closed: P31-M35a-01 to -04 and -06 to -08 as implemented
(decided on 2026-10-17 by best practice; the explain card's source choice moves to M26), and
P31-M35a-05, -09 and -10 (the routing of management-side replies and the forward's counter) were
decided on 2026-10-17 and are implemented as described below. The audit's items P31-AUDIT-11 to -14 were decided
on 2026-10-18 and are implemented as described below and in "Access rules (M9)" (the guard and the WireGuard networks
of the role management, the limit of forwarded ports, the replies of a removed management forward, a forward's
target with a stale neighbor entry).

### How it works

- **Configuration and API.** `port_forwards` is a map keyed by UUID next to the access rules, without
  an order (`internal/domain/forward.go`). A forward has `protocol` (tcp, udp), `entry` (`uplink`,
  `management`), `external_port` and optionally `external_port_end`, a `target` with exactly one of
  `device` and `address`, `target_port` (default: the external port) and optionally `target_port_end`,
  `enabled`, `name`, `description`. A range is shifted one to one (`Forward.TargetPortOf`); one target
  start port is enough. It is revisioned, merge-patched, diffed (kind `port_forward`) and resolved
  (device names to UUIDs) like the access rules, and a device a forward targets cannot be deleted
  (`unknown_reference`). `GET /port-forwards` and `/port-forwards/{forwardId}` (id or name,
  `?revision=`, paging) return each forward with its configuration, its `status` (`active`, or
  `inactive` with `disabled`, `device_without_address`, `device_outside_test_network`,
  `management_is_uplink`, `entry_without_address`; its message; the target address now) and, for the
  revision the kernel runs, its counter: the connections that arrived through it, with the time of the
  last one (`forwardCounter`, see *Counting* below). The status comes from
  `Index.ForwardStatus` with the facts the engine observes (`Engine.ForwardFacts`: the devices'
  addresses, whether management and uplink are one interface, whether each entry side has an
  address); the entry sides are judged once the host has been observed. The capability
  `port_forwards` is listed.
- **Validation** (`internal/domain/validate_forward.go`, every refusal `validation_failed` with the
  pointer into the forward):

  | Refused | Code | Pointer |
  |---|---|---|
  | two forwards of a protocol sharing an external port (ranges too, whatever the entry side, disabled ones too: P31-M35a-02) | `overlapping_forward` | `/external_port` |
  | a range that ends before it starts | `invalid_port_range` | `/external_port_end`, `/target_port_end` |
  | a target range past 65535 | `invalid_port_range` | `/target_port` |
  | ranges of different length | `port_range_length` | `/target_port_end` |
  | the UI/API port (`management.ui_port`, else the port the API listens on, passed as `domain.WithUIPort`; P31-M35a-03), a WireGuard listen port (udp), 22/tcp on the management side or the two-port topology's shared interface, 179/tcp while an enabled BGP protocol exists (P31-M35a-01) | `reserved_port` | `/external_port` |
  | `management` in the two-port topology | `management_is_uplink` | `/entry` |
  | enabled forwards that forward more than `domain.MaxForwardedPorts` (16384) ports together (P31-AUDIT-12, option (a), decided 2026-10-18): every forwarded port is an element of the DNAT map, and the move of a target device deletes and adds all of them in one nft transaction, which the executor takes up to 8 MiB (`maxOperationBytes`; 16384 ports are about 1.6 MB for such a move with the example's faults) | `invalid_port_range` | `/external_port_end` (or `/external_port`) of the forward that crosses the limit, in the order of the names |
  | a target that is a WireGuard client, a device of a WireGuard network, in the management network, a gateway address, a network or broadcast address, or outside the local test networks | `invalid_target` | `/target/device`, `/target/address` |

  A forward is never lockout-relevant: the ports of the control plane are refused, so it never waits
  for commit-confirm. The compiler is the backstop: a forward the validation would refuse (an old
  revision) is left out with the warning `forward_refused`, the others are compiled; that includes a forward
  that would take the compiled forwards past `domain.MaxForwardedPorts` (`forwardRefusal`). `chaosgw apply
  --file` validates the file against the API's port it is given (`--ui-port`, default 443) and
  compiles with it, so a file cannot take the port of an API that listens elsewhere either
  (`TestApplyFileRefusesAForwardOnTheAPIsPort`).
- **The packet path** (`internal/compiler/forward.go`, the overview at its top):
  - *DNAT.* One rule in the `prerouting` NAT chain (priority -100, after the service redirect of
    M6b): IPv4 (`meta nfproto ipv4`: the inet NAT chain sees IPv6 too, and the OS-owned entry
    interfaces keep their IPv6 addresses, P31-AUDIT-02), in on an entry interface, to an address of
    that interface (`fib daddr . iif type local`),
    `dnat ip to` the element of the map `fwd_dnat_<hash>` keyed on interface, protocol and port. The
    kernel's lookup follows a new entry address at once, like masquerade: no apply, no element
    changes (P31-M35a-07: every address of the interface answers). The map's value is the target's
    address and port; it has one element per forwarded port (nft cannot shift a range, P31-M35a-06).
  - *A device target follows its address.* The element holds the device's address now: of its
    addresses in a local test network the most current one (`Identity.Current`: an explicit address,
    then leases, the newest first, then the neighbor table (the entry that appeared last first, since an old entry stays REACHABLE for a while after a move), a reservation, and last an address that
    stays mapped only because a connection of the device still comes from it), so an old address
    that lingers on with a long-lived connection is no target while the device has a newer one. An
    address change is an element update through the identity path of M7 (`internal/engine/forward.go`,
    `owner.go`), never a full apply. Only for a device that a forward sends to does the order of its
    addresses alone make a new generation; a neighbor entry going STALE and back reorders the others
    without one (P31-AUDIT-03, `TestTheOrderOfADevicesAddressesMattersOnlyForAForwardTarget`). A device without an address keeps its forward compiled, with no
    elements and the status `device_without_address`.
  - *A forward's target keeps a stale neighbor entry* (P31-AUDIT-14, option (a), decided 2026-10-18). A neighbor
    entry in STALE, DELAY or PROBE counts for a device's identity only while its address carries connections; a
    forwarded connection counts for the device it was sent to (the reply's source of a translated connection,
    `forwardTarget` in `internal/engine/observe.go`), and a device that an enabled forward sends to keeps a stale
    entry also without any connection, as a lingering claim (the least current address) that gives way to any
    claim of another device on the address (`ResolveIdentity`, `claim.yielding`; since issue #93 every
    configured device keeps a stale entry this way). So an idle listener known
    only from the neighbor table (a static address, no lease, no reservation) stays the forward's target; the
    kernel probes the entry when a forwarded connection comes, and an entry that fails leaves the table and the
    forward goes inactive. Tests: `TestAForwardKeepsItsTargetWhileTheDevicesNeighborEntryIsStale`,
    `TestAForwardedConnectionMarksTheDeviceItWasSentToAsInUse`, level 1
    `TestAForwardReachesAnIdleDeviceWhoseNeighborEntryIsStale` (a short `base_reachable_time_ms` on the bridge,
    A's entry stale (STALE or DELAY, with a long first-probe delay) and its connections deleted, a new forwarded connection reaches A).
  - *Counting* (P31-M35a-09, option (b), decided 2026-10-17). One counting rule per forward in the
    same NAT chain, in front of the DNAT rule: IPv4, in on the forward's entry interface, to an address of
    that interface, its protocol and `th dport` in its external ports, the named counter
    `fwd_<hash of the id>` and `last`, no verdict. A NAT chain sees the first packet of a connection
    only, and the rule runs before any access rule, so the counter counts connections: each one that
    arrives through the forward once, whether an access `allow` rule, the forward's accept rule or
    nothing lets it through (a TCP connection whose SYN a rule drops is counted per attempt, since a
    dropped packet leaves no conntrack entry). The last match comes from the same rule's `last`, so
    counter and time always describe the same packets; the accept rule carries neither. The API's
    `forwardCounter` reads it, and the UI shows "N connections".
  - *Accept.* One rule per forward in `forward`, after `jump access_forward` and before the matrix:
    `ct status dnat`, in on the forward's entry interface, out to a local test network, its protocol
    and `ct original proto-dst` in its external ports. The forward is the allowance; the matrix
    default keeps everything else from the uplink out of the test networks.
  - *Management side* (P31-M35a-05 and -10, option (c), decided 2026-10-17). The policy table 100,
    which every packet of a test network is looked up in, never has a route to the management
    subnet: a device's own new connection to the management network follows the uplink default
    (masqueraded) and the forward chain like any other. Routing alone does not keep the device out,
    though (the table can lose its default with the uplink's address, and a router behind the uplink
    may know the management subnet): the management guard in front of the access rules does, so no
    access rule, an allow rule without a destination included, carries a device into the management
    network; only an explicit matrix entry opens it (decided 2026-10-17). Only the replies of a connection
    that came in through a management-side forward go back through the management interface, by
    a mark: the forward's counting rule sets bit 22 of the conntrack mark (`ct mark set ct mark |
    0x400000`; plan §3.3: bit 22 is the reply mark of a management-side forward, bit 23 stays reserved for routing
    marks), the base chain `fwd_reply`
    (filter, prerouting, priority -140: after the classification, before the NAT chain and the
    routing decision) copies it onto the packet mark of the replies that come in from a local test
    network (`iifname @ifs_lan ct direction reply ct mark & 0x400000 == 0x400000 meta mark set meta
    mark | 0x400000`), and the policy rule `fwmark 0x400000/0x400000 lookup 101` (priority 925,
    between the service selection at 900 and the PMTU mirrors at 950) sends them to table 101: the
    management interface's connected subnet, a default via the management interface's own default
    gateway (the main table's default route on that interface, when there is one), and a `prohibit`
    default with metric 4096 as the fallback, so such a reply never falls through to the uplink.
    So a peer behind a router of the management network is answered too. A peer that the host reaches
    through another route of the management interface (a static route to an admin network while the
    main default goes via the uplink) is not in table 101 and gets no reply; when the management
    interface has no default route at all the compiler warns (`forward_replies_subnet_only`: "the
    replies of management forwards reach only <subnet>", `TestAManagementForwardWithoutAManagementDefaultRouteWarnsThatItsRepliesReachTheSubnetOnly`),
    and the form of a management forward in the UI says the same. The table, the rule and
    the chain are compiled in the three-port topology whether or not a forward from the management side
    exists (P31-AUDIT-13, option (b), decided 2026-10-18): a connection that came in through a management
    forward lives on after the forward is deleted or disabled (D11, P31-M35a-04), and its replies, whose
    conntrack mark still has the bit, would otherwise be looked up in table 100 and leave through the uplink.
    Only a management forward's counting rule sets the bit, so without one the chain marks nothing new; the
    warning `forward_replies_subnet_only` comes only with a management forward. The executor's scope already
    covers them (table 101 lies in Chaos Gateway's tables 100-110, the route's device is the assigned
    management interface); bit 22 is read or written nowhere else. Tests:
    `TestAManagementForwardRoutesItsRepliesToTheManagementNetwork` (also without a management forward and
    without any forward; nothing in the two-port topology), level 1
    `TestTheRepliesOfAManagementForwardedConnectionStayOffTheUplinkAfterTheForwardIsDeleted` (an open
    connection through the management forward keeps getting its replies after the forward is deleted, and
    nothing of it leaves through the uplink).
  - *DOCKER-USER.* The existing per-bridge accept rules (`-o <bridge>`) already cover the path from
    the entry side to a test-network bridge (plan §3.4); no new rule.
  - *Hairpin* (a device using the entry address) is not supported: the DNAT matches the entry
    interfaces only.
  - *Without an enabled forward the forwards leave nothing*: no map, no counting rule, no DNAT, no accept, no
    responder form, and a disabled forward gives the same output and hash as none
    (`TestWithoutAForwardInThePacketPathNothingChanges`). Before P31-AUDIT-13 the output without a forward was
    byte-identical to before M35a; since then the three-port topology always has `fwd_reply`, rule 925 and
    table 101 (above), so the golden files of the three-port scenarios have them.
- **Access rules with the device as responder** (plan §2.4, D34; `internal/compiler/access.go`). While
  a forward is compiled, every rule, configured and overlay, gets a second form right after its own,
  with the same counter and verdict: `ct status dnat`, the rule's source against `ct reply ip saddr`
  (the target), its destination against `ct original ip saddr` (the peer; `uplink` matches every peer
  behind the uplink and excludes the management network), its ports against `ct reply proto-src` (the
  target port). The management network a peer is excluded from is its `allowed_sources` and the
  interface's own subnet, as `explain` has it: when `allowed_sources` is unset or narrower than the
  subnet, the responder forms test the peer against a set of their own (`anon_uplink_peer`,
  `AccessPlan.PeerNonUplink`), so a device's "offline" rule (device → uplink: drop) does not stop a
  forward from the management side in a default installation; otherwise they share `anon_uplink`
  (`TestAnUplinkRuleDoesNotMatchAManagementPeerOfAForwardedConnection`). An ICMP rule has no responder form, and the input chain never sees a forwarded
  connection. "Also cut existing connections" has these forms in its cut window too (the reset goes
  to the peer), and the engine's conntrack cut finds forwarded flows by their reply source
  (`internal/engine/cut.go`). The domain mirror is `World.ForwardedDecision`: the first matching rule
  decides, overlays first; none decides, the forward accepts (access layer `port_forward`).
  `TestTheCompiledRulesDecideForwardedConnectionsLikeTheDomainLayer` holds the two together. A
  connection through a forward that is disabled or deleted lives on until it ends; new ones are not
  translated (P31-M35a-04).
- **Faults do not apply** (M35b): the classification runs on the conntrack original tuple, whose ends
  (the peer and the entry address) lie in no test network, so it never marks a forwarded connection.
  The preview lists the forwards (`port_forwards`, each with `faults_apply: false`) and warns
  `faults_not_on_forwards` at a forward whose target a fault selects; `explain` of a connection from
  an outside address to an entry address on a forwarded port names the forward (target, port, device,
  status, the note), the deciding rule or `port_forward`, and no fault.
- **UI** (on the screens of M14 and M12): `web/src/lib/forwards.ts` (parsing, the checks under the
  fields, the forward the form builds, the server's pointers to fields, the rows),
  `components/networks/ForwardList.vue` on the uplink and management cards of the networks view
  (protocol, external port or range, target, target port, name, counter, status; NEW, CHANGED,
  DELETED; Edit or Fix, Disable/Enable, Delete, Undo), `components/networks/ForwardDialog.vue` (a
  device or an address as the target; in the two-port topology only the uplink and no Add on the
  management card). Forwards go through the draft (`stores/draft.ts`), the unapplied-changes bar, the
  preview-and-apply drawer (a "PORT FORWARDS" section with the note on faults) and the concurrent-change
  dialog like every other change. The device detail has a "Port forwards" card with the forwards to
  the device; Edit links to `/networks?forward=<id>`, Add to `?forward=new&device=<id>`. The forwards
  come from the active revision: until it is there, the uplink and management cards and the device
  detail say "Loading the port forwards…", and "The port forwards are not loaded" with Try again when
  it failed, never that there is no forward (P31-AUDIT-07, P31-AUDIT-08). DELETED is in the blocked
  tone on the cards and the device detail alike (P31-AUDIT-09).
  `usePortForwards()` reads `/port-forwards` and is refreshed by the revision, apply, identity, DHCP
  and `uplink_changed` events. The explain card shows the forward when the answer has one
  (P31-M35a-08: it cannot ask about a forwarded connection itself yet).

### Tests

The compiler's unit and golden tests (`internal/compiler/forward_test.go`, `testdata/forward.golden.txt`,
`forward.nft.golden.json`), the domain's (`internal/domain/forward_test.go`), the API's
(`internal/api/forwards_test.go`, every response checked against the spec) and the engine's
(`internal/engine/forward_test.go`, `cut_internal_test.go`) run at level 0. At level 1,
`internal/compiler/forwardkernel_test.go` runs the packet path on the bed of the access rules, and
`internal/engine/integration_forward_test.go` the engine in the three-port topology; both CI testbed
jobs name these tests and the Docker test of `internal/apply` with `-expect-pass $M35A_LEVEL1`
(`.github/workflows/ci.yml`), so a renamed or vanished one fails the job. The "within 1 s" of the plan
is recorded in the measurement record against the 3 s bound of P2-M7-03 where timing is accurate (KVM);
under software emulation it is recorded without the bound. Level 2 is the port forward part of the
appliance smoke test (see "Appliance VMs (test level 2)"). The UI: `web/src/lib/forwards.test.ts`,
`web/src/stores/draft.forwards.test.ts` and `web/e2e/port-forwards.spec.ts` against the mocked API
(`withForwards()` in `web/e2e/mock.ts`).

### Acceptance map (plan M35a "Tests")

| Plan M35a Tests | Test |
|---|---|
| golden tests of the nftables output (DNAT map, forward accept, access rules with the device as responder) | `TestGoldenForwards`, `TestGoldenForwardNftTransaction` (`forward.golden.txt`, `forward.nft.golden.json`); `TestTheDNATMapSendsEveryForwardedPortToTheTargetsCurrentAddress`, `TestTheDNATRuleStandsInThePreroutingNATChainNextToTheServiceRedirect`, `TestEachForwardCountsItsConnectionsInTheNATChainInFrontOfTheDNAT`, `TestTheForwardAcceptStandsAfterTheAccessRulesAndBeforeTheMatrix`, `TestAManagementForwardRoutesItsRepliesToTheManagementNetwork` (the golden file shows the counting rules, `fwd_reply`, tables 100 and 101 and the fwmark rules), `TestEveryRuleMatchesForwardedConnectionsWithTheTargetAsResponder`, `TestTheCompiledRulesDecideForwardedConnectionsLikeTheDomainLayer`, `TestWithoutAForwardInThePacketPathNothingChanges`, `TestMapElementsWithInterfaceNamesAndConcatenatedValues`; the kernel takes the ruleset and its cut windows: `TestEveryTransactionIsAcceptedByNftsParser`, `TestEveryCompiledRulesetIsAcceptedByTheKernel`, `TestEveryCutWindowIsAcceptedByTheKernel` (scenario `forward`) |
| level 1 — a server namespace behind the uplink reaches a TCP and a UDP listener of a test-network client through a forward, and without a forward its connection is dropped and counted | `TestAForwardReachesTheListenersOfADeviceOnTheRealKernel`; through the engine `TestForwardsReachTheDeviceFromBothSidesAndFollowItsAddress` |
| a forward to a device follows the device's new address within 1 s of an address change | `TestForwardsReachTheDeviceFromBothSidesAndFollowItsAddress` (the engine, measured), `TestAForwardFollowsANewEntryAddressAndANewDeviceAddressOnTheRealKernel` (the element update on the kernel); the new address also while the old one lingers on with a connection of the device or its neighbor entry is still REACHABLE: `TestAForwardSendsToTheDevicesMostCurrentAddressNotToOneThatLingersOn`, `TestANeighborEntryKeepsWhenItWasFirstSeen`, `TestTheDNATMapSendsToTheDevicesMostCurrentAddress`; `TestADeviceForwardFollowsTheAddressByElementsOnly`, `TestADeviceForwardFollowsTheAddressByAnElementUpdate`, `TestAFullApplyKeepsTheForwardsElements`; an idle device known only from a stale neighbor entry stays the target (P31-AUDIT-14): `TestAForwardKeepsItsTargetWhileTheDevicesNeighborEntryIsStale`, level 1 `TestAForwardReachesAnIdleDeviceWhoseNeighborEntryIsStale`; the move of the largest allowed forwards fits the executor's limit (P31-AUDIT-12): `TestTheLargestAllowedForwardsAndADeviceMoveFitTheExecutorsLimit` |
| a drop, reject and reset rule on the device stops forwarded connections, and "also cut existing connections" resets an established one | `TestRulesOnTheTargetDecideForwardedConnectionsOnTheRealKernel`, `TestACutWindowResetsAnEstablishedForwardedConnectionOnTheRealKernel`, `TestARuleOnTheDeviceCutsAnEstablishedForwardedConnection`; `TestACutWindowAlsoResetsForwardedConnections`, `TestACutTakesTheForwardedConnectionsOfItsTarget` |
| a device fault does not impair a forwarded connection, and the preview says so | `TestADeviceFaultDoesNotImpairAForwardedConnection`; `TestThePreviewListsTheForwardsAndSaysThatFaultsDoNotApply`, `TestThePreviewNotesTheFaultsThatDoNotApplyToAForward`; `port-forwards.spec.ts` "port forwards: the preview says that faults do not apply to forwarded connections, and names the fault of the target" |
| an existing rule "device → uplink: drop" stops a forwarded connection to that device once the forward exists | `TestRulesOnTheTargetDecideForwardedConnectionsOnTheRealKernel` (and not one from the management network, also with `allowed_sources` unset or narrower); `TestAnUplinkRuleDoesNotMatchAManagementPeerOfAForwardedConnection`, `TestAccessRulesJudgeAForwardedConnectionWithTheDeviceAsResponder`, `TestExplainNamesTheForwardAndTheRuleThatDecidesAForwardedConnection` |
| a forward on the UI/API port, on a WireGuard listen port, on 22/tcp on the management side, on 179/tcp while a BGP neighbor is on its side, overlapping another forward, with ranges of different lengths, or with a target in the management network or on the gateway is rejected with its pointer | `TestAForwardNeverTakesTheUIPort`, `TestAForwardNeverTakesAWireGuardListenPort`, `TestAForwardTakesSSHOnlyOnAnUplinkOfItsOwn`, `TestAForwardLeavesTheBGPPortWhileBGPRuns`, `TestTwoForwardsOnTheSameProtocolMustNotOverlap`, `TestForwardRangesMustBeOrderedAndOfTheSameLength`, `TestAForwardTargetLiesInALocalTestNetwork`, at most 16384 forwarded ports together (P31-AUDIT-12) `TestTheEnabledForwardsForwardAtMostTheLimitOfPortsTogether`; over the API with the pointer `TestARefusedForwardIsReportedAtItsField`; the compiler's backstop `TestAForwardTheValidationRefusesIsLeftOutWithAWarning` (with "more ports than allowed") |
| a port range is forwarded one to one | `TestAForwardReachesTheListenersOfADeviceOnTheRealKernel` (tcp 9049-9050 → 7049-7050); `TestTheDNATMapSendsEveryForwardedPortToTheTargetsCurrentAddress`; `port-forwards.spec.ts` "port forwards: a range to an address is shifted one to one; ranges of different length are refused under the field" |
| a forward from the management network (three-port topology) reaches the device, and `management` is refused in the two-port topology | `TestForwardsReachTheDeviceFromBothSidesAndFollowItsAddress`, `TestAManagementForwardRoutesItsRepliesToTheManagementNetwork` (the mark, table 101 and its rule, no management subnet in table 100), `TestTheReplyMarkOfAManagementForwardIsAFreeRoutingBit`; a peer behind a router of the management network is answered, and a device with an allow rule without a destination does not reach the management network (P31-M35a-05, -10): `TestAManagementForwardAnswersAPeerBehindARouterOfTheManagementNetwork`, `TestAnAllowAllRuleDoesNotOpenTheManagementNetworkWhileAManagementForwardWorks`; the management guard keeps a device out also when table 100 has lost its default, and an explicit matrix entry opens it (decided 2026-10-17): `TestWithoutTheUplinkDefaultAnAllowAllRuleStillDoesNotOpenTheManagementNetwork`, `TestAMatrixEntryOpensTheManagementNetworkBehindAManagementInterfaceOfItsOwn`; the replies of a connection that outlives its management forward stay off the uplink (P31-AUDIT-13): `TestTheRepliesOfAManagementForwardedConnectionStayOffTheUplinkAfterTheForwardIsDeleted`; the WireGuard networks of the role management are part of the management network for the guard (P31-AUDIT-11): `TestTheGuardKeepsADeviceOutOfAManagementRoleWireGuardNetworkUntilAMatrixEntryOpensIt`; the warning when the management interface has no default route: `TestAManagementForwardWithoutAManagementDefaultRouteWarnsThatItsRepliesReachTheSubnetOnly`; `TestTheManagementSideIsRefusedInTheTwoPortTopology`, `TestAManagementForwardInTheTwoPortTopologyIsNotCompiled`; `port-forwards.spec.ts` "port forwards: from the management network in the three-port topology", "port forwards: in the two-port topology the management card offers no forward and the form only the uplink"; level 2 `TestSmokeOnCleanUbuntuHosts/<release>/three-ports`, `TestTheTwoPortConfigurationWouldRefuseAManagementForward` |
| a forward follows a new uplink address (DHCP) within 1 s | `TestAForwardFollowsANewUplinkAddress` (the address replaced as a new lease does, measured, and the status follows), `TestAForwardFollowsANewEntryAddressAndANewDeviceAddressOnTheRealKernel` |
| a device without a known address leaves its forward inactive with its reason in the status | `TestAForwardFollowsItsDeviceAndSaysWhyItIsInactive`, `TestPortForwardsAreListedWithTheirStatus`, `TestADeviceForwardFollowsTheAddressByElementsOnly`, `TestAForwardOfAnEntryWithoutAddressIsInactive`; `port-forwards.spec.ts` "port forwards: the uplink and the management card list their forwards with target, ports, counter and status" |
| the counters count the forwarded connections | `TestAForwardReachesTheListenersOfADeviceOnTheRealKernel`, `TestForwardsReachTheDeviceFromBothSidesAndFollowItsAddress`; also the connections an access `allow` rule accepts, and the ones a rule drops (P31-M35a-09): `TestAForwardCountsTheConnectionsAnAllowRuleAcceptsOnTheRealKernel`, `TestAnAllowAllRuleDoesNotOpenTheManagementNetworkWhileAManagementForwardWorks`, `TestRulesOnTheTargetDecideForwardedConnectionsOnTheRealKernel`; the counting rule in the NAT chain `TestEachForwardCountsItsConnectionsInTheNATChainInFrontOfTheDNAT`; an IPv6 connection to the entry interface is not counted (P31-AUDIT-02): `TestAForwardDoesNotCountIPv6ConnectionsToItsEntryInterfaceOnTheRealKernel`; over the API `TestAForwardCountsItsConnections`; level 2 `TestSmokeOnCleanUbuntuHosts`. The UI shows "N connections": `src/lib/forwards.test.ts` "the rows: …", `port-forwards.spec.ts` "port forwards: the uplink and the management card list …", "port forwards: the device detail lists …" |
| a disabled or deleted forward leaves no DNAT and closes the path | `TestAForwardReachesTheListenersOfADeviceOnTheRealKernel` (deleted), `TestADisabledForwardLeavesTheKernel`, `TestWithoutAForwardInThePacketPathNothingChanges` (disabled), `TestTheDNATMapSendsEveryForwardedPortToTheTargetsCurrentAddress` |
| Playwright against the mocked API for the forward list | `port-forwards.spec.ts` "port forwards: the uplink and the management card list their forwards with target, ports, counter and status", "port forwards: enabling a disabled forward and changing a target say that the change is not applied yet", "port forwards: the status not loaded says so with Try again on the cards and the device detail; the forwards stay listed", "port forwards: the configuration not loaded says so with Try again on the cards and the device detail, never "none"" (P31-AUDIT-07, P31-AUDIT-08), "port forwards: a deleted forward is marked in the blocked tone on the cards and the device detail alike" (P31-AUDIT-09), "port forwards: without forwards the cards list none and offer Add"; `src/lib/forwards.test.ts` "the rows: …", "a changed row says that the gateway's status is the applied forward's: …" |
| the form (add, edit, disable, delete through the drawer, a refused port shown at its field, a range) | `port-forwards.spec.ts` "port forwards: add a forward to a device through the drawer — no commit-confirm, and it is listed after the apply", "port forwards: edit, disable and delete through the draft and the drawer; Undo takes each back", "port forwards: a port the gateway refuses is shown at its field, and Fix opens the forward again with the gateway's message", "port forwards: a range to an address is shifted one to one; ranges of different length are refused under the field", "port forwards: a forward changed meanwhile through the API opens the conflict dialog, and reapplying keeps their change"; `src/lib/forwards.test.ts` "the port forward form (M35a)"; `src/stores/draft.forwards.test.ts` "the port forwards in the draft (M35a)" |
| the device detail's list | `port-forwards.spec.ts` "port forwards: the device detail lists the forwards to the device, and Edit opens the forward on the networks page", "port forwards: a discovered device is offered no forward; it is adopted first, and the form refuses it as the gateway does", "port forwards: a configured device without a network (identified by MAC) is offered a forward, and the gateway takes it", "port forwards: the configuration not loaded says so with Try again on the cards and the device detail, never "none"" (P31-AUDIT-07), "port forwards: a deleted forward is marked in the blocked tone on the cards and the device detail alike" (P31-AUDIT-09); `src/lib/forwards.test.ts` "a device target the gateway would refuse is refused under the field: …", "forwardDeviceProblem" (what the gateway accepts and refuses) |
| the preview's note on faults | `port-forwards.spec.ts` "port forwards: the preview says that faults do not apply to forwarded connections, and names the fault of the target"; explain through the API only: `TestExplainNamesTheForwardAndTheRuleThatDecidesAForwardedConnection` (the explain card cannot ask about a forwarded connection, P31-M35a-08; "port forwards: explain shows a forwarded connection's forward, the access layer of the forward and its note on faults" only renders such an answer) |
| level 2 — a forward in the two-port topology, next to a port published by a Docker container | `TestSmokeOnCleanUbuntuHosts/<release>/two-ports` (`portForwards` in `internal/appliance/appliance_test.go`, nightly job `appliance`); at level 0 `TestTheLevel2ConfigurationIsValidWithItsPortForwards`, `TestTheEchoHelperAnswersWithItsTagAndThePeer` |
| Scope: the `DOCKER-USER` accept rule covers the path from the entry side to the test-network bridges | `TestAForwardPassesDockersForwardPolicyThroughDockerUser`, `TestDockerUserCoversThePathOfAForward`; level 2 |
| Scope: the API resource, revisioned like the access rules | `TestPortForwardsAreListedWithTheirStatus`, `TestTheCapabilitiesNamePortForwarding`, `TestForwardsAreChangedThroughAMergePatchLikeAccessRules`, `TestForwardNamesAreUniqueAndTheKeysAreUUIDs`, `TestAForwardIsOfTcpOrUdpWithAKnownEntrySide`, `TestTheExampleForwardIsValidAndFillsItsDefaults`, `TestMatchForwardFindsTheEnabledForwardOfAPort` |

## Scenarios and runs (M15)

Plan §2.10 and M15. A scenario is a timeline of steps; it is part of every revision and the domain checks
it (`domain.ValidateScenario`: step ids, kinds, narrowing, the order of `remove`, tunnel steps inside the
target, check windows). M15 adds the engine that executes one, `internal/run`, and the API around it: the
scenarios of the active revision (`GET /scenarios`, `…/{id}` as JSON or, with `Accept: application/yaml`, as the
file `chaosctl run -f` takes in M18), the runs (`POST /runs` and `POST /scenarios/{id}/runs`, `GET /runs`,
`…/{id}`, `…/abort`, `…/renew`, `…/report.json`, `…/report.xml`, `…/events`), `GET /system/busy`, the
`runs_active` of `GET /state`, `aborted_runs` of `POST /reset`, and the step types of `GET /capabilities`.

### How it works

- **A run is a goroutine** (`Manager.execute`) that sends commands to the state owner through the engine
  (`run.Gateway`, which `*engine.Engine` implements), like every other writer (plan §3.11). A step that
  creates or replaces an overlay is an `OverlayWrite` whose owner is the run itself (`model.Owner{Type: run,
  Id: <run id>}`, `OverlayWrite.Run` names the step); the overlay store already replaces an overlay with the
  key *(owner, kind, target, selector)*, so "a step with the same key replaces the earlier one, its
  parameters complete, never merged" needs no code of its own. `domain.StepRequest` turns a step into the
  request (the step's target, else the scenario's; a tunnel fault and a WireGuard action have none).
  `remove` deletes the overlay under the key of an earlier step (`DeleteOverlay`; if it is gone already the
  step is `skipped` with the reason), `restore` resets the owner. A run never writes a revision.
  The goroutine is the only writer of its own record's steps; the manager's mutex guards the table and
  the records and is never held across a call out of the package.
- **Creating a run** (`Manager.Create`). The scenario is the stored one (by name or UUID) or the inline one;
  `domain.ParameterizeScenario` applies `parameters` (dotted paths, `domain.ApplyParameters`: a segment into
  a list is a position or a step id, a value is a string except for `true`, `false`, whole numbers and
  `[…]`/`{…}` JSON, a member of a `target` replaces the whole target) to the generic document and checks the
  result against the `Scenario` schema, so an inline scenario may leave out what the parameters give;
  `domain.ResolveScenario` validates it against the active configuration (discovered devices count as known)
  and resolves every reference to its UUID. That resolved scenario is the run's `snapshot`, with the revision
  that was active, and what it executes: renaming an object, or editing the stored scenario, changes nothing
  for a run that has started (plan §2.1.1). A scenario the build cannot run is `422 unsupported_feature`
  naming the milestone: a `tls` step
  with the case `intercept` is supported since M22 (the interception step), a `dhcp` step since M23 (a `dns` step, a profile with a DNS part and a host name in a `wait` or a check are supported since M20, a `tls` step and a profile with a certificate or handshake case since M21), and, on a gateway
  without a capture volume, `capture: true` or a `capture` step (see Packet captures (M17)); `GET
  /capabilities` lists `step_types` (profile, fault, rule, dns since M20, tls since M21, dhcp since M23, wireguard, capture, wait, remove, restore) and, since M16, the
  `check_types` (the checks `tls_rejected` and `tls_accepted` since M21; one for a host name works since M20). Nothing is stored
  for a refused scenario.
- **One run per target, the queue.** Two targets overlap when they share a device or a network
  (`scopeTokens`: the ids of a scope's devices from the snapshot, a group's members whether the snapshot knows
  them or not, and for a network the network itself; `global` overlaps everything). A run starts at once when
  no queued or running run overlaps it, else it is `queued` (`run_queued` event) or, with `?queue=false`,
  refused with `409 target_busy` naming the run that holds the target. `schedule` starts queued runs in
  creation order and **no run overtakes an earlier one it would have waited for**: a run is held back by every
  running run and every earlier queued run it overlaps, so a long queue cannot starve its first member. At most
  200 runs are queued or running (`capacity_exceeded`).
- **Preconditions** (`Manager.preconditions`, before the first step; a failure ends the run as `error` with
  `precondition <id> failed: …`; a scenario skips ids with `preconditions.skip`). `target_online`: a device
  target must be online; for a group or network at least one of its devices must be (the snapshot's
  `DeviceState.Online`). `uses_gateway_dns`, only when `domain.ScenarioUsesDNS` (a DNS step, a wait for a DNS
  query, a host name in a step's or a check's destination): each device checked (the device, or every online
  device of a group or network) has asked the gateway's DNS proxy (the query log) or has a lease from a
  network whose DHCP options hand out the gateway (no `dns_servers`, or the gateway among them).
  `no_foreign_overlays`: no overlay of another owner overlaps the target (`overlayTokens`: its target, or for
  a tunnel fault or WireGuard action the client or link it names); overlays of a token are foreign to a
  run, whoever started the run.
- **The timeline** (`Manager.timeline`). Steps run in the order of `domain.TimelineOrder` (by `at`, list
  order for equal `at`); each step is due at *start + at + shift* on the monotonic clock
  (`sleepUntil` arms one timer, rechecks the clock after arming it, and ends on cancel). The report lists the
  steps in that order. A `wait` step stops the timeline: it polls every 250 ms (`waitPoll`) until its
  condition holds and then `shift` becomes the time it waited, so the later steps move by it; a timeout fails
  the step and ends the run as `error`. `online`: at least one device of the step's target is online.
  `connected`: a connection of the target that was not there when the wait began (the flow ids at that moment
  are remembered), is established (TCP past the handshake, others answered) and matches the protocol, ports
  and destination (address, prefix, network, `uplink`; `matchFlow`). `dns_query`: the DNS log has a query for
  the name from a device of the target at or after the time the wait began. The run ends as `passed` right after its
  last step; since M16 it lasts until its last check is decided (see Checks and zero hits (M16)).
- **Recording a step.** After the write returns (the engine answers when an apply containing it is
  verified) the step has `status: applied`, `applied_at` (the wall time, which stands for the executor's commit
  time stamp), `offset_ms` (monotonic, from the start of the timeline), `start_offset_ms` (the same clock, when the
  timeline handed the change to the engine), `generation` (the one the engine
  answered with, which can be newer than the change's own), the `overlay` and `queue_wait_ms`; `run_step`
  announces it. A write that fails (`CompileError`, `apply_failed`, a validation problem) fails the step with
  the error, the run is `error` with `step <id>: <error>`, the steps after it are `skipped` (`not run: …`) and
  the run's overlays are removed. A call that ends because the run was cancelled is no failure of the step, but the
  engine goes on after its caller's context ends, so the step in flight is recorded as `error` with "cancelled while
  it was being applied: the change may have taken effect (<reason of the abort>)" (`callFailed`) and not as "skipped,
  not run"; the run still ends `aborted` with the reason of the abort.
- **Queue wait and `step_late`.** `engine.OverlayResult.QueueWait` is new with M15: the time from the moment
  the state owner took the write in until the apply loop took up the desired state that carries it (the wait
  behind an apply in flight, `applyResult.started`), plus the time that apply's plan waited in the
  executor's queue (`executor.Outcome.QueueWait`, plan §3.11's operation time stamps). It is what
  `RunStep.queue_wait_ms` reports (added to the spec with this milestone); a step with more than
  `run.StepLateThreshold` (100 ms) gets the run warning `step_late` naming the step and the overlay. The
  hand-over lateness is the second trigger (P4-AUDIT-14, decided on 2026-10-19): the moment the timeline hands the change
  over (`start_offset_ms`) minus the scheduled offset plus the shift of the waits before the step (`due`) is compared
  with the same threshold, in `handOver` for the steps that change the kernel and in the timeline for `wait` and
  `capture`, also for a `remove` that is skipped. A step has one warning: when it is late for both reasons `warnLate`
  appends the second cause to the message. The message names the cause: the step before it was still being applied
  (its end is on the timeline), the timeline woke late (a clock that jumps, an overloaded host), or the counters
  that are read before a replace, a remove or a restore took their time. That read is on the timeline: the stamp
  `began` is taken after it, immediately before the engine call (P4-AUDIT-17), so `start_offset_ms`, the windows of
  checks that open at the step and the warning contain it, and `offset_ms - start_offset_ms` is again only the
  apply.
- **The accuracy of a step is where it is handed over, not when its apply is done** (decided while fixing CI run
  39208509316; confirmed by the maintainer on 2026-10-19, option 3: plan §2.10 now says that the ±100 ms promise is
  about the hand-over, and that the report holds both moments, so the apply time shows). An overlay write returns when the apply that carries it is verified. The apply reads the
  kernel, plans, writes and reads back: about 0.1 to 0.2 s on a native kernel with the configuration of the testbed
  (container job: 136, 159 and 193 ms) and 2 to 5 s in the VM of the CI on a shared runner (nested virtualization
  makes every `nft`, `tc` and `ip` of the apply slow; the steps were 3.4, 4.4 and 6.3 s late with a queue wait of
  3 ms). The first version of the level 1 test measured `offset_ms` against the schedule, so it asserted the speed
  of the kernel tools and not the timeline, and could not pass in either job. `start_offset_ms` (new in the spec,
  `RunStep`) is the moment the timeline hands the change over; `offset_ms - start_offset_ms` is the time the apply
  took. A step is handed over after the one before has been answered (the timeline is sequential), so a step that is
  due while the apply of the one before runs is handed over late; the level 1 test keeps its steps 10 s apart for
  that reason. The executor's commit time stamp itself (the end of its request, before the read-back) is not carried
  to the run: the change takes effect somewhere between the two moments, and what needs the exact moment (the
  windows of checks) is placed so that it does not matter, see "Windows" in the M16 section. The hand-over of a step
  that replaces, removes or restores overlays contains the read of their final counters (P4-AUDIT-17), which is a kernel
  read like the apply: 0.6 s in the VM of the CI (run 39343558893, the step `end` was handed over 604 ms late), a few
  milliseconds on a native kernel. The product warns (`step_late`, naming "the counters ... were read for N ms first"),
  and the level 1 test (`stepsOnTime`) requires that warning for every step more than 100 ms late and takes the
  read the warning names off the lateness before it holds the rest to 100 ms, so the timeline's own accuracy stays
  asserted and the cost of the observation is shown, not hidden.
- **The end of a run, always the same way** (`Manager.finish`): read the final counters of the overlays that are
  still there (they are also read before a `remove` or `restore` takes overlays away, so the report holds the
  final counter of every overlay the run created, `RunReport.counters`), reset the run's owner through the
  engine (up to three attempts a second apart; a run whose overlays cannot be removed is `error` even if it
  passed, and an `aborted` or `error` run says so next to its own reason, `...; the overlays of the run could not be
  removed: <why>`; the manager then keeps trying every 10 s, `retryCleanup`, until it works, the record is pruned or
  the manager closes, so the overlays do not wait for the next successful apply or `POST /reset`), write the record, emit `run_finished`, wait until the recorder has written that event to the
  run's event file, and start the runs that waited. Every way to end goes through it: the last step, an error,
  a failed precondition, an abort.
- **Abort, lease, reset, stop.** `POST /runs/{id}/abort` cancels the run's context with the reason
  (`aborted by <token name>`; a queued run is finished at once, a running one finishes itself) and answers
  when the overlays are gone, with the generation that removed them in `Chaos-Generation` (when they could not
  be removed the answer is the problem `apply_failed` naming the run, `run.CleanupError`: the run is recorded as
  `aborted` with the reason, and never a clean 200 with generation 0); aborting an ended run returns it unchanged. A **lease** (`lease`, at least 1 s) starts when the run is created and runs on
  the monotonic clock (`clock.AfterFunc`, reset by `POST /runs/{id}/renew`, which answers `422` for a run
  without a lease, one that has ended and one that is being aborted: it is cancelled but stays `running` until it has cleaned up); when it runs out the run is `aborted` with the reason `lease
  expired`, also while it is still queued. A client that disconnects changes nothing: the run does not
  depend on the request that made it. A token with the scope `overlays` may abort and renew only its own runs
  (`runLimit`), the scope `full` and the admin any. `POST /reset` aborts the caller's runs (every run with
  `owner=all`) first, which removes their overlays, then resets the overlays that are left; `aborted_runs` counts
  them, and `removed_overlays` counts the overlays of those runs too (`Manager.AbortOwned` returns how many their
  removal took away) next to the ones the reset removed. `Server.Close` (the shutdown of `chaosgw api`, before the engine closes) aborts what is left with
  `service stopping`.
- **Records, restart, retention.** `<data dir>/runs/<id>/run.json` (the run as the API shows it, the final
  counters, schema version 1, written atomically each time the run changes, in order) and `events.jsonl` (the
  events of the run, one API `Event` per line: the manager's own `run_queued`, `run_started`, `run_step`,
  `run_finished` and the engine's overlay events of the run's overlays, which now carry `run`; a recorder
  goroutine follows the event bus; a recorder the bus dropped subscribes again with `SubscribeFrom` the newest event it took and writes what the bus replays first, so the file loses nothing unless the bus has not kept it any more (then the log says how many events are missing)).
  `Manager.Recover` (called by `api.New`, once the engine runs) loads the records: a run that was queued or
  running ends as `aborted` with the reason `service restart` (its overlays died with the process, plan
  §2.1.1) and an event, a record of an ended run older than the retention (30 days, plan §3.6) is
  deleted, as are expired ones whenever a run ends. The retention is `settings.retention.runs` of the active
  configuration, asked again each time (`Config.RetentionOf`, `Server.runRetention`, as `captureQuota` does for the
  captures), else `--run-retention`, which stays as the default of a deployment for a configuration that does not
  set it (it is not deprecated: it applies at recovery before the first configuration is active and to every
  configuration without the setting; the default of the flag and of the spec, 720 h, is the same); and a record that cannot be read is left alone with a
  warning. `GET …/report.json`, `…/report.xml` and `…/events` of a run that has not ended answer `404
  not_found` ("the report is there once the run has ended"); they wait for the end of the run to reach the
  event file, so a client that sees `passed` reads a complete file.
- **Reports.** `report.json` is the run with its `counters`. `report.xml` is JUnit (`junitOf`): one test
  suite named after the scenario with the run's id, status, revision, owner and reason as properties, a test
  case `run` (an `<error type="run_error">` for `error`, `run_aborted` for `aborted`, with the reason), a test
  case `step <id>` with an `<error>` for each step that failed, a test case per check (M16: failure for
  `failed`, error for `error`, skipped for one that was not decided), and the warnings, the timeline with queue waits and the final counters as
  `system-out`.
- **Revisions.** A revision that deletes an object a queued or running run refers to (`Manager.Orphaned`:
  `domain.OrphanedScenario` over the run's snapshot) is refused with `validation_failed` listing the runs
  as `references` of `kind: run` next to the overlays it would orphan, and `preview` lists the same;
  `apply?force=true` first aborts those runs (`AbortReferences`, reason `revision N deletes <object>`, which
  removes their overlays) and then applies, so the engine finds nothing of the run's to orphan; the answer names
  them in `aborted_runs` (in the order the runs were made), and their overlays are not in `removed_overlays`.
  Nothing is aborted for an apply the engine would refuse at once: `ApplyRevision` asks `Engine.CheckApplicable`
  (another revision waits for confirmation, this one is no candidate or not on the active base: the checks
  `startApply` makes first, the same function) before it looks at the references, so a refused apply, forced or not,
  answers `confirm_pending` and the like and leaves the runs alone. A hook inside the engine's owner loop would
  close the gap between the question and the apply, but the owner cannot wait for the runs to remove their
  overlays (they do it through the owner), so the question is asked first. The runs stay aborted if the apply
  then fails at the kernel. The check and the apply are not one step: a run started in between is not seen.
- **API details.** `Idempotency-Key` now also works on `POST /runs` and `POST /scenarios/{id}/runs`. The owner
  of a run is `ownerOf(principal)`, like an overlay's (the token, or the admin user for the UI). A stored
  scenario's YAML has UUIDs as references, like the export of a revision. `GET /runs` is newest first without
  steps, checks and snapshot. `GetState.runs_active` and `GET /system/busy` count the running runs; the busy
  answer also lists queued ones and the ids.

### Decisions

Taken by best practice where the plan leaves room (none needs the maintainer, so there is no Phase 4 item in
`open-items.md` for M15):

1. **Records on disk, not in memory only**, because the plan has a restart end runs as `aborted` and keeps the
   reports of the last 30 days (§3.6); the layout is the plan's `runs/<id>/…` below the API's data directory.
2. **`remove` removes the overlay under the key of the named step**, which a later step with the same key may
   have replaced (`remove: slow` after `lossy` replaced `slow` removes the fault of `lossy`): the key is the
   overlay's identity. A second `remove` of the same step finds nothing and is `skipped`.
3. **`applied_at` is the time the verified write returns**; the executor's own commit time stamp is only a part
   of `queue_wait_ms`.
4. **`step_late` is the queue wait and the hand-over lateness.** The milestone's test says "waits behind a revision
   apply for more than 100 ms", and that stays a trigger. Until the Phase 4 audit the scheduler's lateness was only in
   the report (`start_offset_ms` against `scheduled_offset`), although plan §2.10 says that a step handed over late
   because the one before is still being applied gets the warning; the maintainer decided on 2026-10-19 (P4-AUDIT-14,
   option a) that it warns too, one warning per step naming the cause.
5. **A group or network target is online when one device is** (and `uses_gateway_dns` asks every online device),
   because a network that has a device that has been off for a month would otherwise never run.
6. **Checks, capture, zero-hit warnings are M16 and M17.** (M16 delivered the checks and the zero-hit warnings, M17 the
   capture.) A scenario that has them was refused with
   `unsupported_feature` rather than run without them, because a `passed` run whose checks were not evaluated
   would be a lie (the same reason as the zero-hit warning of plan §2.10). `RunWarning.capture_missing` is used
   since M17.
7. **Aborting twice, and a report that is not there yet.** Aborting an ended run returns it (idempotent); a
   report or JUnit file of a run that has not ended is `404 not_found` (its events are readable while it goes on: the ones written so far); renewing a run that has
   ended, or has no lease, is `422 validation_failed`. The error codes of the plan have no better ones.
8. **A run that cannot remove its overlays at its end is `error`**, whatever it was, so the failure is not
   hidden in a green report.
9. **The runs of a revision that is applied with `force` are aborted before the apply**, not after: an
   orphaned run would otherwise find its next step without its target.

### Tests

Level 0 (all run here, with `-race`): `internal/run` on a fake clock, a fake gateway and a fake observer
(`steps_test.go`, `lifecycle_test.go`, `report_test.go`, `scope_internal_test.go`); the domain's
(`internal/domain/scenario_run_test.go`: parameters, the run request, steps as overlay requests, the timeline, host
names, orphans); the engine's (`internal/engine/queuewait_test.go`, a write behind a held apply on the fake
clock); the API's on the simulated kernel with the real engine, a fake API clock and every response checked
against the spec (`internal/api/runs_test.go`, `runs_hold_test.go`). Level 1 (`internal/api/e2e_runs_test.go`,
build tag `testbed`; both CI testbed jobs name the tests with `-expect-pass $M15_LEVEL1`): the steps of a run on the
real kernel and clock, within 100 ms of their schedule where timing is accurate (recorded without the bound under
software emulation), the effect on the wire (a delayed ping, a dropped ping, a clean path afterwards), the
counters in the report, an abort and an expired lease that take the overlays out. These three need root and
namespaces; the unprivileged container of the development environment has no `/dev/kvm`, so they are built
(`go vet -tags testbed ./internal/api`) and wired into CI but were not run there: not run in the sandbox; run by CI. No UI in this milestone (M19).

### Acceptance map (plan M15 "Tests")

| Plan M15 Tests | Test |
|---|---|
| step order and semantics with the fake clock | `TestStepsRunInTheOrderOfTheTimelineAtTheirTimes` (order by `at`, list order for equal `at`, nothing a millisecond early, the report in timeline order), `TestEveryStepRecordsTheGenerationItWasAppliedIn`, `TestAStepWithTheKeyOfAnEarlierOneReplacesItsOverlay`, `TestTheOverlaysOfAStepAreOwnedByTheRun`, `TestRemoveDeletesOneStepsOverlayAndRestoreAllOfThem`, `TestAStepNarrowsTheTargetAndATunnelStepHasNone`, `TestAWaitStepShiftsTheLaterSteps`, `TestAWaitThatTimesOutEndsTheRunAsError`, `TestAWaitForAConnectionNeedsANewEstablishedMatchingOne`, `TestAWaitForADNSQueryNeedsAQueryAfterTheWaitBegan`, `TestAStepThatCannotBeExecutedEndsTheRunAsError`; the helpers: `TestAConnectionIsEstablishedPastTheHandshake`, `TestAConnectionMatchesAProtocolPortsAndDestination`, `TestTheTimelineIsByOffsetAndThenByPosition`, `TestAStepIsTheOverlayItWrites`; over the API `TestARunFromAFileAppliesItsStepsAndLeavesAReport`, `TestEveryStepTypeOfThisBuildRunsOverTheAPI` (profile, fault, rule, WireGuard action, remove, restore against the real engine) |
| step timing within ±100 ms on a machine with KVM | `TestAConnectionMadeWhileTheRestoreIsBeingAppliedBelongsToTheWindowAfterIt` (the step records when it was handed over and when it was answered); level 1 `TestAScenarioRunsOnTheRealKernelAndItsStepsAreOnTime` (the lateness of the hand-over of every step is recorded and bounded where `testbed.Accurate()`, the time of the apply is logged); not runnable here |
| abort removes the run's overlays | `TestAbortRemovesTheOverlaysOfTheRun`, `TestAnAbortOfAQueuedRunNeverStartsIt`, `TestAbortOwnedAbortsTheRunsOfOneOwnerOrAll`, `TestCloseAbortsTheRunsThatAreStillThere`; over the API `TestASecondRunOnTheSameTargetWaitsAndTheQueueFalseRefusesIt`, `TestRunsFollowTheTokenScopesAndResetStopsTheCallersRuns`; level 1 `TestAnAbortOnTheRealKernelTakesTheOverlaysOfTheRunOut` |
| a disconnecting client does not stop a run, an expired lease does | `TestADisconnectingClientDoesNotStopARun`, `TestAnExpiredLeaseAbortsTheRunAndARenewalKeepsItAlive` (also with the wall clock jumping), `TestTheLeaseOfAQueuedRunRunsOut`, `TestARunWithoutALeaseCannotBeRenewed`; over the API `TestARunKeepsGoingWhenTheRequestIsOver`, `TestTheLeaseOfARunIsRenewedOverTheAPIAndExpires`; level 1 `TestAnExpiredLeaseOnTheRealKernelRemovesTheFault` |
| a restart ends a running run as `aborted` | `TestARestartEndsARunningRunAsAbortedAndKeepsTheFinishedOnes` (running and queued records, retention, an unreadable record), `TestAFinishedRunSurvivesARestart`, `TestOldRecordsAreDeletedAsRunsEnd`; over the API `TestARestartEndsARunThatWasRunningAsAborted` |
| a second run on the same target waits in `queued` | `TestASecondRunOnTheSameTargetWaitsInQueued`, `TestRunsOnOverlappingTargetsWaitInTheOrderTheyCameIn`, `TestTargetsOverlapWhenTheyShareADeviceOrANetwork`; over the API `TestASecondRunOnTheSameTargetWaitsAndTheQueueFalseRefusesIt` |
| a failed precondition ends the run as `error` | `TestAFailedPreconditionEndsTheRunAsErrorWithTheReason`, `TestAScenarioMaySkipAPrecondition`, `TestAGroupOrNetworkTargetNeedsOneDeviceOnline`, `TestOverlaysOfOtherOwnersOnTheTargetFailTheBaseline`, `TestAnOverlayActsOnTheTunnelItNames`, `TestAScenarioWithHostNamesNeedsADeviceThatUsesTheGatewaysDNS`; over the API `TestAFailedPreconditionEndsTheRunAsErrorOverTheAPI` |
| an inline scenario runs without changing the active revision | `TestARunFromAFileAppliesItsStepsAndLeavesAReport` (the active revision is the same after the run, nothing is stored), `TestParametersSetTheTargetOfAnInlineScenario`, `TestAParameterThatNamesNothingIsRefused`; the parameters and the request: `TestParametersChangeAScenarioByDottedPath`, `TestSettingAMemberOfATargetReplacesTheTarget`, `TestAParameterThatCannotBeAppliedIsReportedAtItsPath`, `TestParameterizeScenarioChecksTheResult`, `TestDecodeRunRequest`, `TestDecodeRunOptions`; `TestARunIsRefusedForWhatTheBuildDoesNotHaveAndForWhatDoesNotFit` |
| the report contains all steps with their queue wait (`RunStep.queue_wait_ms`) | `TestTheReportHasAllStepsTheirQueueWaitAndTheFinalCounters`, `TestTheJUnitReportOfAPassedRunHasOneSuccessfulCase`, `TestTheJUnitReportOfARunThatEndedAsErrorHasAnErrorCase`, `TestAStepErrorIsATestCaseOfItsOwn`, `TestAnAbortedRunIsAnErrorInTheJUnitReport`, `TestTheEventsOfARunAreKeptInItsFile`; over the API `TestARunFromAFileAppliesItsStepsAndLeavesAReport` (report.json, report.xml, events) |
| a step that waits behind a revision apply for more than 100 ms gets `step_late` | `TestAStepThatWaitsMoreThan100msGetsTheWarningStepLate` (100 ms is not late, 130 ms is), `TestAStepHandedOverMoreThan100msLateGetsTheWarningStepLate`, `TestAStepHandedOver100msLateIsNotLate`, `TestAStepLateForBothReasonsHasOneWarning`, `TestAStepTheTimelineReachesLateSaysSo` and `TestTheReadOfCountersBeforeAStepIsPartOfItsHandOver` (the hand-over, on the fake clock with `putDelay` and `readDelay`), `TestAWriteBehindAnApplyInFlightReportsItsQueueWait` (the engine's queue wait on the fake clock), over the API `TestAStepBehindARevisionApplyWaitsAndTheRunSaysSo` (a held apply of a real revision) |
| Scope: stored scenarios, `POST /scenarios/{id}/runs`, owner, snapshot, revisions | `TestAStoredScenarioIsListedFetchedAsYAMLAndRunByName`, `TestAStoredScenarioThatCapturesNeedsACaptureVolume`, `TestStepTypesAndChecksOfLaterMilestonesAreRefused`, `TestAScenarioWithAnUnknownReferenceIsRefused`, `TestAnIdempotencyKeyMakesOneRun`, `TestARevisionThatDeletesWhatARunRefersToOrphansIt`, `TestARevisionThatDeletesWhatARunRefersToIsRefusedUnlessForced`, `TestARevisionOrphansTheScenarioOfARunThatRefersToWhatItDeletes` |
| Scope: queue and owner, scopes of tokens | `TestAnAbortIsLimitedToTheOwnerOfTheRunForAScopedCaller`, `TestRunsFollowTheTokenScopesAndResetStopsTheCallersRuns` |

## Checks and zero hits (M16)

Plan §2.10 and M16. A scenario's `checks` evaluate what the gateway observes of the scenario's target inside a window
that is tied to a step, and a fault, rule or profile of a run that matched no packet is reported, because a `passed` run
whose fault never hit proves nothing. M15 refused a scenario with checks (`unsupported_feature`, M16); this build runs
them: `GET /capabilities` lists `check_types` `reconnected`, `no_connection`, `traffic_seen`, `traffic_not_seen` and
`dns_query_seen` and the feature `checks`. `tls_rejected` and `tls_accepted` came with M21 (`422 unsupported_feature` before); a check for a host name
(a `destination` or an `except` with `hostname`) came with M20.

### How it works

- **A goroutine beside the timeline** (`Manager.runChecks`, `internal/run/checks.go`). The timeline never waits for a
  check, so a slow read of the connections cannot delay a step (`TestTheChecksNeverDelayTheSteps` holds every read
  of the connections for the whole run). The one observation on the timeline is the read of the counters that a step
  makes before it replaces, removes or restores an overlay (the overlay must still be there to be read): it is bounded
  by `stepCounterTimeout` (2 s), after which the overlay has no final reading and so no zero-hit verdict; the read at
  the end of the run is bounded by 10 s. The goroutine starts with the timeline (after the
  preconditions) when the scenario has checks, and the timeline waits for it after the last step: **a run lasts until
  its last check is decided**, not only until its last step, and its overlays stay until then (a window may reach past
  the last step: `from: restore, within: 30s` of the plan's example). A check that is decided early lets the run end
  early: the run ends when every check is decided, never before its last step.
- **Windows.** A check's window opens at the step `window.from` (`start`: the beginning of the timeline) and closes
  `within` after it opened, or at the step `until`. A step has two moments (`stepMoments`): the timeline hands its
  change to the engine (`Manager.began`), and the engine answers when the apply is verified (`Manager.reached`; also
  for a step that has nothing to do, a `remove` that finds no overlay is `skipped`, and for a `wait` step when its
  condition held; the actual monotonic offset and wall time, so a wait that shifted the timeline moves the windows
  with it). The change takes effect in the kernel somewhere between the two, and an apply takes seconds in a nested
  virtual machine: a device that retries every 500 ms made its connection after the restore had been written and
  before it was answered, and the window of the answer failed `no_connection from cut until restore` with that
  connection and missed it for `reconnected from restore` (CI run 39208509316: the level 1 tests of M16 and M18).
  So the moment depends on what the check claims (`opensAt`, `closesAt`). A **positive** check (`reconnected`,
  `traffic_seen`, `dns_query_seen`) looks for what the change makes possible, which cannot happen before the change
  takes effect and may happen before the answer: its window opens when the step is **handed over**, and an `until`
  closes it at the **answer**. A **negative** check (`no_connection`, `traffic_not_seen`) claims that nothing happens
  while the change is in force: its window opens at the **answer** (the change is in force for sure), and an `until`
  closes it when the closing step is **handed over** (from then on the change may be gone). So a window never counts
  a moment in which a negative check's change may not have been in force, and never leaves out one in which a
  positive check's change may have been. On the fake clock, where an apply takes no time, the two moments are the
  same. `CheckResult.window_start` and `window_end` are the wall times. Windows are checked by the domain
  (`from` is `start` or a step; `until` comes after `from`; exactly one of `within` and `until`).
- **What a poll sees.** While a window of a connection check is open the goroutine reads the tracked connections
  (`Observer.Flows`, conntrack) every 250 ms (`checkPoll`) and keeps what it saw (`flowTracker`). What counts is what
  **changed between two polls** inside the window: a connection that appeared (`created`), one that is established now
  and was not before (`established`), one whose packet counts grew (`traffic`). The first poll after a window opened is
  its baseline: connections that were there then, established or not, are not news, with one exception: a connection
  whose `StartedAt` (conntrack's time stamp, when `nf_conntrack_timestamp` reports it) is at or after the wall time of
  the step that opened the window began inside the window, however late the baseline poll read it, and counts (a
  device that retries every 500 ms would otherwise fail `reconnected` now and then when its connection is made
  between the step and the baseline). Without a start time a connection found by the baseline poll is never news. The
  same tuple with another start time, or fewer packets than before, is a new connection. The baseline is the first poll
  that **worked**: a check is armed (`liveCheck.armed`) only when the connections could be read, so a read that fails
  where the window opens does not make the next poll's pre-window connections news (P4-AUDIT-02,
  `TestAFailedReadWhereTheWindowOpensDoesNotArmTheCheck`). The events of the poll before a check opened (consumed for
  another check, while the step was being handed over) are kept for one iteration and given to the check as far as the
  connection began inside its window (`prevEvents`; `TestAConnectionOfThePollBeforeTheOpeningBelongsToTheCheckThatOpens`).
  The other end is dated too: once a window has closed (`ended`; a fixed-length window from the start, an `until` window
  from the hand-over of the closing step), a connection whose `StartedAt` is after the end of the window is not the window's,
  however late the poll reads it (`observes`; the read takes seconds in a nested virtual machine and the restored path lets
  a connection through right after the hand-over; P4-AUDIT-19). Only connections of the scenario's target count (`flowInScope`: the initiator is a device of the
  target, or the connection starts in the target network) and only those the selector selects (`matchFlow`: protocol,
  ports, port ranges, destination address, prefix, network or the uplink). The two address destinations use the
  same sets as the rules and the capture selector (`domain.World`, built once per snapshot as `flowNets`): `network` is
  `World.NetworkPrefixes` (the subnet, the networks behind a hub's clients, a link's routes and a LAN's downstream
  routes), `uplink` is "not in `World.NonUplinkPrefixes()`" (every network and the management prefixes; P4-AUDIT-04). A
  check that needs them when the world cannot be built (a configuration that is not normalized) is an `error`. A
  configured device that declares no network is in the network of its DHCP lease, else the one its address lies in
  (resolved once in `tracker.step`), so a network target covers it as `explain` does (P4-AUDIT-05).
- **The five types.**
  `reconnected`: an `established` event inside the window (TCP past the handshake, any other flow once it was
  answered; `established()` of M15's `wait: connected`). A SYN that is dropped never becomes one, nor does a
  refused attempt: a TCP entry in CLOSE counts only when the originator sent two packets or more, a handshake
  and not one SYN answered by a RST.
  `no_connection`: the same event is a failure.
  `traffic_seen`: any event (a new connection in any state, an established one that became answered, or packets on an
  old one).
  `traffic_not_seen`: the same event is a failure unless its destination matches an entry of `except`.
  `dns_query_seen`: the DNS log has a query for the name (exact, or `*.suffix` for its subdomains, not the suffix
  itself, as the spec's `Hostname`) by a device of the target, asked at or after the window opened and before it
  closed (`Observer.DNSQueries`, new; the log's time stamps decide, not the polls). The log is in the order of arrival,
  not of time: the proxy stamps a query with its start and logs it after the answer, so the scan of
  `runObserver.DNSQuerySeen` and `DNSQueries` goes through the whole log and never stops at an older entry
  (P4-AUDIT-03). The same reason makes the last look at the end of the window late: the log arrives in batches every
  500 ms and by one request, so the decision of a `dns_query_seen` check waits `dnsSettle` (1 s) after the window has
  closed, still filtering on the time stamp (a query asked at the end of the window counts, one stamped after it does
  not; P4-AUDIT-20).
- **When a check is decided.** A positive check (`reconnected`, `traffic_seen`, `dns_query_seen`) is `passed` at the
  first observation and `failed` when the window closes without one. A negative check (`no_connection`,
  `traffic_not_seen`) is `failed` at the first observation and `passed` when the window closes. `error` is for a
  connection check that cannot read the connections at the poll where its window closes (`could not be read`): it
  cannot say either way. A result has a `message` that names the connection or query that decided it and when (offset
  into the window), and up to ten observations as `evidence` (`kind: flow` with protocol, addresses, ports, state and
  the time of the poll; `kind: dns_query`). The name of a check is the scenario's or one derived from the type, the
  selector and the window (`reconnected tcp/8883, from restore within 30s`). A check is announced with the event
  `run_check` (`check`, `check_name`, `status`, `message`) as it is decided and the record of the run is written, so a
  client that polls `GET /runs/{id}` sees the results come in.
- **How the run ends** (`Manager.verdict`). A check that is `error` makes the run `error` (reason: the check and its
  message), else a check that is `failed` makes it `failed`, else it is `passed`. A run that ends otherwise (abort,
  lease, a step that failed, a restart) leaves its undecided checks `pending` with the message `not decided: the run
  ended as <status> (<reason>)`; the JUnit file shows them as skipped.
- **Zero hits** (`internal/run/zerohit.go`). A step of the kind fault, rule or profile whose overlay matched no packet
  is the run warning `zero_hit` (`step`, `overlay`, `message`). The counters are the overlays' counters (`Observer.Counters`,
  the same ones `GET /overlays` shows), read before the overlay is replaced by a later step, before `remove` and
  `restore` take overlays away, and at the end. A step that replaces the overlay of an earlier one (same key, same id)
  is judged by what the overlay matched from the moment it took over: the reading before the replacement is its
  baseline, and a counter whose epoch changed (it started over) counts from zero. So `lossy` is not excused by the
  packets `slow` matched. The warnings are made when the run reaches its end as `passed` or `failed`; a run that ends
  as `error` or `aborted` gets none, because its steps may never have had the time to match. With `zero_hit: error` in
  the scenario a run that would have passed is `failed` (reason `zero hits: the overlay of step X matched no
  packet`) and the JUnit file gets a failed test case for each such step; a run that failed anyway keeps its reason.
  A step is not judged when its counter could not be read (no verdict, no warning), when it is a WireGuard action or
  other kind without a packet counter, or when it is meant to match nothing: a profile with no parameter in any part,
  like `normal`.
- **API.** `runObserver.Counters` returns a zero counter for a fault, rule or profile that is in the store but has
  nothing in the kernel (a rule that is not in the packet path, a fault that another one overrides), and nothing at all
  when the counters could not be read (`overlayContext.countersFailed`), so a failed read is never taken for zero hits.

### Decisions

Taken by best practice where the plan leaves room (decision 9 was put to the maintainer in the Phase 4 audit,
P4-AUDIT-15):

1. **Polls, not an event stream.** Observations are placed in a window by the poll that sees them, so they are exact
   to 250 ms. A connection that is opened and gone between two polls would be missed; conntrack keeps a TCP connection
   in TIME_WAIT for 120 s and a UDP flow for 30 s, so in practice only entries destroyed by a cut (`cut_existing`)
   vanish quickly, and those are not what the checks look for. The cost is one conntrack read per 250 ms while a
   connection window is open (none otherwise).
2. **A positive check decides at the first observation.** This ends a run early (no waiting for the 30 s), as a test
   suite would want; a negative check can only be decided when the window closes, or earlier if it fails.
3. **A window of `within` starts at the actual time of its step.** It is not shifted or stretched by waits or late
   steps; its length is the one in the scenario.
4. **Error beats failed.** A check that cannot be decided is an infrastructure problem (`error`, exit code 2 of
   `chaosctl`, M18), a failed check a verdict about the device (`failed`, exit code 1); a run with both is `error`
   because its verdict is incomplete.
5. **Traffic to the gateway's own services counts.** A device's DNS queries are connections to the gateway (redirected
   into the service namespace) and appear in `traffic_seen` and `traffic_not_seen`; a `traffic_not_seen` without a
   destination fails on them. Name the gateway in `except` (or select a destination) when that is not wanted.
6. **`zero_hit: error` makes the run `failed`**, not `error`: the engine executed every step; the verdict is that the
   scenario did not exercise what it set up. The warning stays in the list in both modes.
7. **A fault that is overridden or a rule that is not in the packet path is a zero hit** (it matched nothing), and so is
   an overlay with no kernel object, but a counter that could not be read is no verdict.
8. **`*.suffix` in a check or a `wait` for a DNS query is the spec's `Hostname`** (subdomains only). The query log's
   `?name=` filter (M6b) also takes the suffix itself; that was left as it is.
9. **The traffic checks see the connection table, not the packets.** `reconnected`, `no_connection`, `traffic_seen` and
   `traffic_not_seen` read conntrack, and a packet that is dropped in the forward path (a `drop` rule, a cut) is
   never confirmed and leaves no entry (the same limit as the counters of a port forward). So while a drop is in force
   `traffic_not_seen {destination: broker}` passes for a device that hammers the broker, and `traffic_seen` of the same
   destination fails; the rule's own counter (the zero-hit counters of the run) is what counts such packets. This
   is documented in the spec (`Check`, `traffic_seen`, `TrafficNotSeenCheck`) and pinned by the level 1 test
   `TestTheTrafficChecksDoNotSeeWhatTheGatewayDropsOnTheRealKernel`; reading the counters of the run's drop rules as
   traffic (a second observation source with windows of its own), or NFLOG (plan §2.11, after V1), is left (P4-AUDIT-15,
   decided by the maintainer).

### Tests

Level 0 (all run here, with `-race`):
`internal/run/checks_test.go` (a manager on a fake clock, a fake observer whose connections, DNS log and counters the
test edits, and the observer's reads as the sync points of the tests), `internal/run/checks_internal_test.go` (the
tracker, what a check observes, the derived names), `internal/domain/scenario_run_test.go` (`CheckKind`) and, over the
API with the simulated kernel (conntrack text, the proxy's query log, bumped counters) and the fake clock of the API,
`internal/api/runs_checks_test.go`. Level 1 (`internal/api/e2e_checks_test.go`, build tag `testbed`; both CI testbed jobs
name the tests with `-expect-pass $M16_LEVEL1`): a scripted device (a Python client that holds a connection to a broker
and retries every 500 ms after a reset) against the real gateway: the outage of the plan's example with the three
connection checks passes when the device comes back and fails the reconnection when it gives up, and a fault that
matches nothing next to one that the test's pings hit gives exactly one zero-hit warning, or a failed run with
`zero_hit: error`. Since the Phase 4 audit also `TestTrafficNotSeenWithExceptDecidesOnTheRealKernel` (a device that talks
to the allowed server passes, one that also talks to another fails, the message names the connection),
`TestDNSQuerySeenDecidesForADeviceOnTheRealKernel` (a device that resolves a name through the gateway's DNS inside the
window passes and the log entry carries the device, one that does not ask fails; on the DNS bed of the access tests with
the configured device client-a) and `TestTheTrafficChecksDoNotSeeWhatTheGatewayDropsOnTheRealKernel` (decision 9). These need root and namespaces; the unprivileged container of the development environment has no
`/dev/kvm`, so they are built and vetted (`go vet -tags testbed ./internal/api`), wired into CI and were not run
there: not run in the sandbox; run by CI.

### Acceptance map (plan M16 "Tests")

| Plan M16 Tests | Test |
|---|---|
| checks pass and fail correctly with scripted client behavior in the testbed | level 1 `TestChecksPassForADeviceThatReconnectsOnTheRealKernel` (a scripted device reconnects after the cut: `reconnected`, `no_connection` and `traffic_seen` pass, the JUnit file has no failure), `TestChecksFailForADeviceThatDoesNotReconnectOnTheRealKernel` (the device gives up: `reconnected` and `traffic_seen` fail, `no_connection` passes, the run is `failed`, two failures in the JUnit file); not runnable here. The same behavior on the fake clock: `TestReconnectedPassesOnANewEstablishedConnectionInsideTheWindow` (a connection of before the window, of another port or device and a handshake that is not through do not count), `TestReconnectedFailsWhenTheWindowClosesWithoutAConnection` (the window is 30 s from the restore, the run `failed`, the failure case of the JUnit file), `TestAConnectionAnsweredInsideTheWindowCountsEvenIfItBeganBefore`, `TestAConnectionThatBeganBetweenTheOpeningOfTheWindowAndTheBaselineCounts`, `TestAConnectionThatBeganBeforeTheWindowIsNotNewAtTheBaseline`, `TestTheSameTupleAgainAfterItsEntryWentIsANewConnection`, `TestNoConnectionFailsOnAnEstablishedConnectionAndNotOnAHandshake`, `TestNoConnectionPassesWhenTheWindowToItsEndStepCloses`, `TestTrafficSeenCountsNewConnectionsAndPacketsOfOldOnes`, `TestTrafficNotSeenIgnoresTheExceptionsAndFailsOnAnyOtherTraffic`, `TestADNSQueryIsSeenOnlyInsideTheWindowFromADeviceOfTheTarget`, `TestADNSQueryAfterTheWindowDoesNotCount`, `TestAWindowFromStartOpensWithTheTimeline`, `TestACheckThatCannotReadTheConnectionsIsAnErrorNotAPass`, `TestAnAbortedRunLeavesItsChecksPendingWithTheReason`, `TestAStepThatFailsEndsTheRunWithItsChecksPending`, `TestTheChecksNeverDelayTheSteps`, `TestTheDecisionOfACheckIsAnEventOfTheRun`, `TestTheNameOfACheckIsGivenOrDerived`; the helpers `TestTheFlowTrackerReportsWhatChangedBetweenTwoPolls`, `TestACheckObservesTheConnectionsOfItsKind`, `TestTheCheckKindsOfAScenarioAreNamedAndWindowsDescribed`, `TestCheckKindNamesTheOneTypeOfACheck`; over the API `TestChecksOfEveryTypeAreEvaluatedOverTheAPI` (the four types with windows of both kinds, the report, the JUnit file, the events), `TestAFailedCheckFailsTheRunAndIsAFailureInTheJUnitFile`, `TestAWildcardDNSCheckMatchesTheSubdomainsAndNotTheSuffix`, `TestARunIsRefusedForWhatTheBuildDoesNotHaveAndForWhatDoesNotFit` (TLS checks M21, host names M20, `check_types` and the feature in the capabilities) |
| a scenario whose fault never matches reports the zero-hit warning | level 1 `TestAFaultThatNeverMatchesIsAZeroHitWarningOnTheRealKernel` (the fault on the server is hit by the test's pings and gives no warning, the one on an unused address gives exactly one, and with `zero_hit: error` the run is `failed`); not runnable here. Level 0: `TestAFaultThatMatchedNoPacketIsAZeroHitWarning`, `TestAReplacingStepIsJudgedByWhatTheOverlayMatchedSinceItTookOver` (the counter goes on, grows, starts over), `TestZeroHitErrorFailsARunThatWouldHavePassed`, `TestNoZeroHitWarningWithoutAReadingOrForARunThatDidNotFinish`, `TestAFailedCheckKeepsItsReasonWhenAZeroHitIsAnErrorToo`, `TestAProfileThatImpairsNothingIsNoZeroHit`; over the API `TestAFaultThatMatchedNoPacketIsAZeroHitWarningOverTheAPI` (the counters of the simulated kernel, the warning in the report and the JUnit output, `zero_hit: error`) |
| Scope: checks with windows relative to named steps (`from` a step or `start`, `within` or `until`) | `TestNoConnectionPassesWhenTheWindowToItsEndStepCloses`, `TestAWindowFromStartOpensWithTheTimeline`, `TestChecksOfEveryTypeAreEvaluatedOverTheAPI`, `TestAConnectionMadeWhileTheRestoreIsBeingAppliedBelongsToTheWindowAfterIt` and `TestAConnectionMadeWhileTheCutIsBeingAppliedIsNotInTheWindowOfANegativeCheck` (the moments of a step a window starts and ends at); the window rules are the domain's (`TestScenarioCheckRules` of M15) |
| Scope: zero-hit warnings (optionally errors) | as above; `RunWarning.zero_hit` and `Scenario.zero_hit` are in the spec since M15 and are described in it now |

## Packet captures (M17)

Plan §2.11 and M17. A capture records the packets of a network, a device or a selector, or of the uplink, into a pcapng or
pcap file that can be downloaded while it runs; a scenario attaches captures to its run. M17 adds the capture engine
(`internal/capture`), its management (`internal/capturemgr`), a connection type of the executor protocol for captures, the
operations of the spec that were declared with it (`GET`/`POST /captures`, `GET`/`DELETE /captures/{id}`, `POST
…/stop`, `GET …/download`), the `capture` step type and `capture: true` of scenarios, the events `capture_started`,
`capture_stopped` and `disk_low`, the feature `captures` and the volume `chaosgw-captures` of the deployment.

### How it works

- **The engine is plain Go, in the executor** (`internal/capture`). The plan names "libpcap processes started by the executor";
  the executor runs a goroutine per capture instead, with one AF_PACKET socket per interface, so the image needs no
  `tcpdump` or libpcap, the filter is tested against a user space specification (below) and the whole thing runs in the
  unit tests of this repository on the loopback interface (a process with `CAP_NET_RAW`). A session
  (`capture.Start`) makes each socket without a protocol, reads the interface's index and hardware type
  (`SIOCGIFINDEX`, `SIOCGIFHWADDR`: Ethernet and the loopback interface are `LINKTYPE_ETHERNET`, a WireGuard interface
  `LINKTYPE_RAW`; anything else is refused), attaches the filter, switches on the kernel's time stamp (`SO_TIMESTAMPNS`) and
  `PACKET_AUXDATA` (the original length of a packet the filter cut), sets a 16 MiB queue, and only then binds it with
  `ETH_P_ALL`, so no packet is queued before the filter is in place. A socket in a named network namespace
  (`CaptureOpen.Namespace`, the testbed's gateway) is made by a thread that joined the namespace and went back
  (`inNamespace`). Each socket has a reader goroutine (a 100 ms receive timeout, so it notices a stop); the packets are
  written through one `Output`; the controlling goroutine flushes the file every 250 ms, ends the capture at its
  duration and checks the disk. A **stop drains**: the readers go on until their socket has been quiet for 100 ms (at most
  65536 more packets), so everything that reached the socket before the stop is in the file. The kernel's drop counter
  (`PACKET_STATISTICS`) is summed as `Capture.dropped`.
- **The filter** (`capture.Filter`) names a *local* side (Ethernet addresses, or addresses and prefixes) and a *peer* side
  (prefixes, or prefixes it is not in), a protocol (tcp, udp, icmp) and the peer's ports. A packet is selected when either
  direction fits: `local(source) and peer(destination) and the port is the destination port`, or the reverse with the
  source port. A connection of a device is therefore captured with its replies, which is what "exact against a capture on
  the device itself" (spike S9) asks for. Every empty part selects everything; a packet that is not IP is never selected
  when the peer, the protocol or the ports are given, and is selected by the local side alone (a device capture holds the
  device's ARP and DHCP frames). `Matcher.Match` is the specification; `Matcher.BPF` compiles the same filter to a classic
  BPF program (`golang.org/x/net/bpf`; every condition is three instructions with 32-bit jumps, because the offsets of a
  conditional jump are 8 bits wide and a group has a long list of addresses). The two are tested against each other on
  random packets, with fragments, IP options, truncation, IPv6 and other protocols
  (`TestTheKernelFilterSelectsExactlyWhatTheUserSpaceFilterSelects`, with the BPF virtual machine of `x/net`), and the
  kernel accepts the programs (the loopback tests). A filter whose program passes the kernel's 4096 instructions (14 per
  Ethernet address: about 290 of them) falls back to a program that takes every packet and filters in the reader
  (`TestAFilterTooLargeForTheKernelIsAppliedInUserSpace`). Not supported: 802.1Q tags (the filter reads an untagged
  Ethernet header) and IPv6 extension headers before the transport header.
- **The files** (`capture.Output`, `capture.Writer`). pcapng: a section header, one interface description block per
  interface (name, description, nanosecond resolution), an enhanced packet block per packet with the interface, the original
  length and the direction (`epb_flags`: inbound or outbound, from `PACKET_OUTGOING`); a file may hold interfaces of
  different link types. pcap: microseconds, one link type (a request that mixes link types in a pcap file is refused with
  `validation_failed` at `/format`). Every block is written by one call into a 256 KiB buffer; a reader that opens
  the file while it is written may see half a block, so a download copies only complete records
  (`capture.CopyComplete`). A **ring buffer** (`ring_buffer`) writes `files` files of about `file_size` bytes, each a
  complete pcap or pcapng file of its own (`capture-00001.pcapng`, …), and deletes the oldest when a new one starts; a
  download joins them (pcapng files by concatenation: each starts a new section; pcap files without the repeated global
  header). Without a ring buffer one file `capture.pcapng` grows up to `max_size`, headers included, and the capture ends with
  the reason `size` before a packet would pass it, so the file never does. The files are created with `O_EXCL` below an
  `os.Root` of the capture's directory (no symbolic link leads out of it, no existing file is overwritten), mode 0640 less
  the executor's umask (0600), and given to the owner of the directory (below).
- **The executor protocol.** A capture is not an operation of the request queue: it changes nothing in the network
  configuration and must not wait behind a plan. Like a watch it has a connection of its own (`Frame.Capture` as the one
  message after the hello; the executor answers with `Frame.CaptureEvent`s: `started` with the interfaces and their link
  types, `stats` once a second, `stopped` with the reason; the client may send `CaptureCtl{Stop}`), and **a capture lives as long
  as the connection**: if the API ends, its captures end with it and no capture is left behind in the executor. The request
  (`executor.CaptureOpen`) is checked by `Validate` (the name of the directory, the interface names, the formats, the
  limits) and by the scope: every interface must be one assigned to Chaos Gateway, the same set the other operations are
  limited to (`ErrOutOfScope`), so a capture cannot read the host's own interfaces other than the uplink and the management
  interface, which are assigned as OS-owned. The directory is `Dir` below the executor's `--captures-dir`, opened with
  `os.Root` (a link to elsewhere is refused); it must exist, and the executor gives the files to the owner of that
  directory. At most `executor.MaxCaptures` (8) captures run in one executor. `Client.OpenCapture`, `Redialing.OpenCapture` and
  `apply.Local.OpenCapture` are the three ways to open one (they implement `capturemgr.Opener`); the refusals come back
  as the sentinel errors `ErrOutOfScope`, `ErrTooManyCaptures`, `ErrDiskLow` and `ErrNoCaptureDir` on both sides of the
  socket (error codes `out_of_scope`, `capture_limit`, `disk_low`, `captures_unavailable`).
- **What a scope stands for** (`capturemgr.Resolve`, with the snapshot of the moment). A **network** is its bridge
  (`br-<name>`; no filter), a WireGuard network its interfaces (raw IP). A **device** is the interface of its network
  filtered by its Ethernet addresses; a device without one (behind a router, a WireGuard client) by its addresses; a device
  without either is `validation_failed` at `/scope/device`. A **selector** takes the source as a device, a group (one
  interface per network of its members, the members of that network in its filter), a network or `global` (every bridge and
  the WireGuard interfaces of the role test), and adds the protocol, the ports and the destination: an address or prefix, a network
  (`domain.World.NetworkPrefixes`, the same prefixes an access rule uses), or `uplink` (every address outside the configured
  networks and the management network, `World.NonUplinkPrefixes`, as the filter's `NotPeers`). A host name is
  the addresses the gateway's DNS answered for it since M20 (`422 validation_failed` `unresolved_hostname` before one has), a `remote_network` source `validation_failed`. The **uplink** is the uplink interface, all
  packets, after NAT. A scope that spans more than 16 interfaces is refused.
  **The admin tunnel is never recorded.** A WireGuard network of the role `management` is part of the management network
  (plan §2.16) and carries the admin's TLS and SSH sessions, so no scope resolves to its interface: `global` skips it, and
  a network, a device on it, a group with such a device and a selector with such a source are `validation_failed` with the code
  `management_network` at the pointer of the scope (`interfacesOf` is the one place all of them go through). A run whose
  target is such a network gets no capture and the warning `capture_missing` with that message (the run goes on, decision 11).
  The `uplink` scope stays: in the two-port topology it records the management plane too, which is the uplink's traffic after
  NAT and not attributable to a device (plan §2.11). Decided 2026-10-19 (P4-AUDIT-16).
- **What an interface shows.** A capture sees a packet as the kernel hands it to the taps of the interface: on the bridge,
  what the gateway receives from and sends to the network, before NAT on the way in (the S9 result), so everything a device
  exchanges with the uplink or another network is in it; frames the bridge forwards between two ports of the same network
  (device to device) never reach the bridge device and are not (the same reason as in `install.md`'s DHCP hint). On the way
  out the tap sees a packet when the interface sends it, so a packet that an egress fault delays is captured after the delay,
  and one it drops is not captured; a packet is captured on the way in before the ingress fault (the IFB redirect) acts. The
  capture is the evidence of what the device saw, not of what the gateway intended.
- **The manager** (`capturemgr.Manager`). `Start` resolves the scope, refuses a mixed `pcap`, a request while the volume is
  below the free-space limit (`ErrDiskLow`) and a ninth running capture (`ErrTooMany`), makes room in the quota (below), makes
  the directory `<dir>/<id>` (the API's user owns it), opens the stream in the executor, takes its `started` event for the
  interfaces and writes the record `capture.json` (atomically; schema version 1) before it answers `201` with the running
  capture. A goroutine per capture follows its stream: `stats` events update packets, size and drops (a client that polls
  `GET /captures/{id}` sees them with at most a second of delay), `stopped` ends the capture (`finish`): status `stopped`
  and the reason of the executor, or `failed` when the reason is `error` or when the executor reports an error of the files
  (the last flush or the close failed) with any reason but `disk_low`: the tail of the file is missing, so a stop by the
  user or a duration end with that error is `failed`, reason `error`, with the message, and a run lists the capture with
  `capture_missing`; `capture_stopped` (and
  `disk_low` for that reason) on the event bus; a stream that closes without an end (the executor died) is `failed` too.
  `Stop` asks the executor and returns when the files are closed (`user`; the runs use `run_end`); a capture that has
  ended is returned as it is. `Delete` refuses one that runs. `Recover` (called when the API starts) loads the records: a
  capture that was `running` ended with the process (the executor closes the files when the connection breaks), so it
  is `failed`, reason `error`, message "the service restarted…", with the size of the files it left; a directory without a
  readable record is left alone with a warning. `Close` stops what still runs the same way, with the message "the service
  stopped"; when its context expires before the executor has answered the stops, it closes the streams (which ends the captures
  on this side) and returns, so an executor that is connected but silent cannot hold the shutdown. `OpenCapture` of the client
  ends with its context too: a cancelled or expired request closes the connection of the open. Time stamps are the API's clock; files and records are all that survives.
- **Quota** (plan §3.6: 5 GiB, oldest first). The quota is the configuration's `settings.retention.captures_quota`, else
  `--capture-quota` of `chaosgw api` (5 GiB). `Start` decides first and deletes last: it works out which of the oldest
  *stopped* captures would have to go for the captures to take at most `quota − 64 KiB` (a ring buffer: `quota − its
  files`), refuses the request when even all of them would not make room (`ErrQuota`, `capacity_exceeded`: the running
  captures use the quota), and cuts the capture's own `max_size` to what would be left (the stop reason `size` then comes
  with the message "the capture reached the space left in the quota"). Those stopped captures are deleted only after the
  executor has accepted the capture (its `started` event), so a request that is refused afterwards (volume low, interface
  gone, out of scope, executor unavailable) deletes nothing, and a client that retries a failing request cannot erase the
  evidence of its runs (decision 8). A `max_size` or ring buffer larger than the whole quota is `validation_failed`
  (`invalid_capture_limit`; the files of a ring buffer are compared with `quota / files` before they are multiplied, so a
  huge file size cannot overflow the check). While captures run, every `stats` event checks the sum, under the same lock
  that takes its counters (so of two captures whose counters arrive at once only the one that makes the sum pass the
  quota is stopped: the first to be taken decides on the counters the other had reported before): the oldest stopped
  captures go first as far as that gets the sum within the quota, and if it does not, nothing is deleted and the capture
  whose counters just arrived is stopped with the reason `size` and the message "the captures take … more than their
  quota" (the running captures may together pass the quota by a second of their traffic). A capture the quota stopped
  (and that ended with the reason `size`) is never deleted by a `stats` event (decision 8): it stays, and the next
  `Start` makes room by deleting it if it must; this lives in memory, so after a restart it is a stopped capture like
  any other. The final record of a capture is written before its end can be seen, so a capture that reads as stopped has
  it on disk.
  A run's captures are deleted with its record (30 days, plan §3.6) and, if the quota needs the room, before.
- **Disk low** (plan §3.6: below 10 % free). The API refuses a new capture when the volume is below the limit
  (`422 capacity_exceeded`); the executor checks again when it opens the files (`ErrDiskLow`, also `capacity_exceeded`: the
  two containers see the same volume, and a gateway whose executor has a stricter `--capture-min-free` is still told) and
  every 250 ms while a capture runs: below the limit the capture stops with the reason `disk_low`, status `stopped`, what was
  recorded stays, and an `ENOSPC` of a write is the same reason. The limit is `--capture-min-free` (10) of both
  `chaosgw exec` and `chaosgw api`; tests give the free space with a function.
- **Download** (`GET /captures/{id}/download`). The Content-Type is `application/x-pcapng` or `application/vnd.tcpdump.pcap`
  and the file name `<name>.pcapng`/`.pcap` (the name as `[A-Za-z0-9._-]`, else `capture-<id>`); the body is streamed from
  the files, never held in memory: the complete records of every file of the capture in the order they were written,
  while the capture runs too (a running capture's file is as current as the last flush, at most 250 ms behind). A file
  the ring deletes while the download runs is skipped. `part=keylog` is `404 not_found` for a capture that has no key log (since M22 a capture
  has one when the interception proxy handled a connection of its device while it ran, "TLS interception and HTTP faults (M22)");
  `part=keylog` and a capture marked `sensitive` need the scope `full`, which the check in the handler already
  applies.
- **API details.** `POST /captures` takes `Idempotency-Key` and the scope `overlays`; it writes `capture.start`,
  `capture.stop` and `capture.delete` to the audit log; a deployment without a capture volume (`--captures-dir` empty)
  answers every operation with `422 unsupported_feature`, and its `GET /capabilities` lacks the step type `capture` and
  the feature `captures`. The spec gained the fields `message`, `dropped` and `interfaces` (name, link type, the filter in
  words) of `Capture`, text for the operations and the request (the default and the minimum limits), and the example
  `api/examples/capture-requests.yaml` (validated by `make check-spec` and decoded by the domain tests).
- **Runs** (`internal/run/captures.go`). With `capture: true` the run starts a capture of its target (a device or a
  network as it is, a group as a selector with the group as its source) *before* the timeline starts, so the first step is in
  it, and stops it with the reason `run_end` as the first thing `finish` does (before the overlays go, for every way a run
  can end). `capture: start` starts a capture of the step's target (a narrower one than the run's is fine); a start while a
  capture of that target from an earlier step runs is `skipped`; `capture: stop` stops the captures of earlier steps of
  that target with the reason `user` (`skipped` when none runs; the domain refuses a `stop` that has no `start` of the same
  target before it, `invalid_step_reference`). `capture: true` is stopped by no step. A capture that cannot be started
  (the executor refuses, the disk is low) never ends the run: `capture_missing` names it (with the step) and the step is
  `skipped`; a capture that stops before its run ends (`disk_low`, `size`, `error`) is reported the same way at the end,
  with its reason and message, and keeps what it recorded. `POST /captures/{id}/stop` has no owner (decision 10) and records
  the reason `user` as the `capture: stop` step does, so `user` is the run's own only for a capture whose stop step
  has run (`runCapture.running` is false); a capture that a user stopped before that, in `finishCaptures` or when the stop
  step finds it stopped, is `capture_missing` ("stopped by a user before the run ended", "...before the step X stopped
  it"). The captures of a run are its artifacts (`kind: capture`, the download URL, the
  size as of reading, the capture's id) in the report, also the ones that ended early; the record of a run that a restart
  aborted gets the captures that process left. A gateway without a capture volume refuses a scenario that records
  (`unsupported_feature`, "the gateway has no capture volume") instead of running it without.
- **Deployment.** The volume `chaosgw-captures` is mounted at `/var/lib/chaosgw/captures` in the executor and the API
  container (`--captures-dir`), the image creates the directory owned by 65532 so that a new volume starts with the owner the API
  needs, and `TestTheCapturesVolumeIsTheSameInTheExecutorAndTheAPI` and the hardening tests pin it. The executor needs no
  capability it does not have (it is privileged); the API container still has none.

### Decisions

Taken by best practice where the plan leaves room (none needs the maintainer, so there is no Phase 4 item in
`open-items.md` for M17):

1. **Native Go capture instead of libpcap processes** (above): testable here, one binary, no extra package in the executor
   image; the formats are the same files libpcap writes, checked against Wireshark's dissector in
   `TestWiresharkReadsBothFormats` and the level 1 tests.
2. **A capture lives as long as its connection**, and a restart makes it `failed`, not resumed. A capture has no meaning
   without the process that wants its files; no executor-side registry means nothing to leak or to recover.
3. **The API makes the directory, the executor writes in it as the directory's owner.** The privileged process never
   follows a path the API chooses, and the API can delete what it serves without extra privileges.
4. **A capture is taken on the bridge, not on its ports.** One socket per network, the addresses of the gateway's side; the
   cost (no device-to-device frames) is the one `install.md` already names for the DHCP debugging hint, and a port-level
   capture would need one socket per physical port and a merge. A capture of the physical ports stays possible with
   `tcpdump` on the host.
5. **`uplink` as a destination is the complement of the configured networks** (the definition of the spec), done by
   `NotPeers` in the filter.
6. **`pcapng` is the default; `pcap` refuses mixed link types.** pcapng is the format that holds several interfaces,
   directions and original lengths.
7. **A capture with no limit stops at 1 GiB** (`domain.DefaultCaptureMaxSize`), and every file has at least 64 KiB; a
   ring buffer takes no `max_size` (it is bounded by its files). A forgotten capture must not fill the volume.
8. **A capture stopped by the quota stays.** Deleting the capture that was just stopped, to get under the quota,
   would destroy the evidence of a run to make room for nothing; the next start deletes it if it must.
9. **Errors of the executor are `failed`, stops are `stopped`.** `disk_low`, `size` and `duration` are the capture doing its
   job, so a run that waits for the file can use it.
10. **Captures have no owner.** The spec gives no owner to a capture and scope `overlays` covers `captures`; a token with
    that scope may stop and delete any capture (unlike overlays and runs). Downloading needs `read`.
11. **A problem with a capture never ends a run**; the run says so in `capture_missing`. A run is the evidence the CI
    reads, and its verdict is about the device, but a report must not silently lack the file it promised.
12. **The tests that open AF_PACKET sockets carry the build tag `testbed`.** The hosted level 0 job is not root and has no
    `CAP_NET_RAW`; the testbed jobs are, and name the tests with `-expect-pass $M17_LEVEL1`, so a test that skips or is no
    longer built fails there. Everything else (the filter against the BPF virtual machine, the writers, the manager, the
    API with a fake executor side) runs in level 0.

### Tests

Level 0 (all run here, with `-race`): `internal/capture` (`filter_test.go`, `format_test.go`, `output_test.go`: the filter
against the BPF virtual machine on random packets, the readers and writers, Wireshark reading both formats, the ring and the
size limit); `internal/executor/capture_validate_test.go` (a request refused before any socket) and the hardening and
deployment tests; `internal/capturemgr` (the resolution of scopes against a snapshot, the lifecycle with a fake executor
stream, the records across a restart, the download of a ring cut in the middle of a record, the captures of a run);
`internal/domain` (the request and its limits, byte sizes, `capture: stop` ordering, the example file);
`internal/run/captures_test.go` (`capture: true`, steps, skipped steps, a capture that cannot start or ends early, abort and
error, restart, retention); and over the API with the simulated kernel, the real engine and the real manager
(`internal/api/captures_test.go`, `runs_captures_test.go`; the executor's side is `capture_fake_test.go`, which writes real
capture files and applies the real filters to the frames a test puts on an interface). The tests with the build tag `testbed`
that need only `CAP_NET_RAW` ran here (as root) with `go test -tags testbed -run …
./internal/capture ./internal/executor` (`M17_LEVEL1` names them): sessions on the loopback interface with the filters of the
plan's Tests line (the kernel accepts them; frames injected through a raw socket with the addresses of devices that do not
exist), the duration, the size limit, the ring, the free-space limit, the executor over its socket with a stop, a lost
connection, the refusals and the owner of the files. The level 1 tests of the API on the real kernel
(`internal/api/e2e_captures_test.go`, a capture on the interface of the device itself as the ground truth) are written,
vetted with `-tags testbed` and wired into both CI testbed jobs, but were not run here (no KVM, no namespaces): run by CI.
CI run 39208509316 (their first) found these faults of the tests, none of the capture: the pcapng reader knows its
interfaces only after the first packet; a `step_late` warning (the engine was busy with another change when the first
step came, as it is in a slow VM) is no statement about a capture, so the two run tests ignore it (`captureWarnings`)
and every other warning still fails them; and the ground truth of the test of a run was started after the run and
stopped before its end, while the capture of the run spans all of it (28 packets against 27 in the container job). The
truth now spans the whole run. Whether that was the whole difference the log does not say, so a mismatch of two captures
prints the frames that only one of them holds (`sameMultiset`, `frameSummary`): if the test fails again, the next run
names the extra packet.

P4-AUDIT-52: the ground truth of the device, selector and run tests was a second `capture.Session` read back with
`capture.NewReader`, so a common fault of the read loop, the BPF generator, the writers or the reader would have cancelled
out. It is now `tcpdump -w` in the device's namespace (`startTcpdump`: `-Z root`, `--immediate-mode`, ended with SIGINT, the
filter an expression written by hand: `ether host <mac>`, plus `and tcp port 8883` for the selector); only the test of the
WireGuard interface still uses a session of the engine. The files the API serves are read by Wireshark (`tsharkFields`):
the device download must have as many packets for tshark as for the reader, the selector download as many packets of
`tcp.port == 8883`, and the ping of the WireGuard client must be found. A file that tshark rejects, or any other failure of
tshark, fails the test with tshark's message; only a missing tshark skips it (the CI jobs name these tests with
`-expect-pass`, so a skip fails them there). tcpdump is part of the devcontainer image, a missing one fails the test.

### Acceptance map (plan M17 "Tests")

| Plan M17 Tests | Test |
|---|---|
| the capture contains exactly the selected traffic | the filter: `TestAFilterSelectsBothDirectionsOfTheLocalSide`, `TestPeersProtocolAndPortsNarrowTheSelectionAndPortsAreThePeersPorts`, `TestAFilterWithPeersNeverSelectsAFrameThatIsNoIPPacket`, `TestHostsSelectRawIPPacketsOfBothFamilies`, `TestNotPeersExcludeAddressesAndNeedAnIPPacket`, `TestTheKernelFilterSelectsExactlyWhatTheUserSpaceFilterSelects` (the BPF program equals the specification on random packets); the sockets (loopback interface, tag `testbed`): `TestACaptureOnTheLoopbackHoldsExactlyTheSelectedTraffic`, `TestADeviceCaptureHoldsTheFramesOfItsAddressInBothDirectionsAndNoOthers`, `TestASelectorCaptureHoldsOnlyTheMatchingTrafficOfTheDevice`, `TestAFilterTooLargeForTheKernelIsAppliedInUserSpace`, `TestTheSnapLengthCutsPacketsAndKeepsTheOriginalLength`; over the API: `TestACaptureOfADeviceHoldsExactlyItsFramesAndCanBeDownloadedWhileItRuns` (also while running), `TestASelectorCaptureHoldsOnlyTheTrafficItNames`, `TestTheDestinationOfASelectorMayBeTheUplinkOrANetwork`, `TestNetworksAndTheUplinkAreCapturedOnTheirInterfaces` (bridge, WireGuard interface as raw IP, uplink, `global`), `TestADeviceOfAWireGuardNetworkIsSelectedByItsAddress`; on the real kernel against a capture on the device itself: `TestACaptureOfADeviceIsExactlyTheCaptureOnTheDeviceItselfOnTheRealKernel`, `TestASelectorCaptureHoldsTheConnectionsItNamesOnTheRealKernel`, `TestAWireGuardNetworkIsCapturedAsRawIPOnTheRealKernel`, `TestTheUplinkCaptureShowsThePacketsAfterNATOnTheRealKernel`; not runnable here |
| quota enforcement | `TestTheQuotaDeletesTheOldestStoppedCapturesAndLimitsTheRunningOnes` (the oldest stopped capture goes, the new one gets the room that is left and ends with `size`, the total stays within the quota, a limit above the quota is refused), `TestARefusedCaptureDeletesNoStoppedCapture` (a request the quota or the executor refuses leaves every stopped capture), `TestARingBufferWhoseFilesOverflowTheQuotaCheckIsRefused`, `TestAnErrorWhileClosingTheFilesFailsTheCaptureWhateverStoppedIt`, `TestNoScopeResolvesToAWireGuardNetworkOfTheRoleManagement`, `TestARunWhoseTargetIsAManagementNetworkIsNotRecorded`, `TestClosingTheManagerEndsTheStreamsOfAnExecutorThatDoesNotAnswerWhenTheContextExpires`, `TestOpeningACaptureEndsWithTheContextOfAnExecutorThatDoesNotAnswer`, `TestRunningCapturesThatTogetherPassTheQuotaAreStopped`, `TestOfTwoCapturesWhoseCountersArriveAtOnceOnlyTheOneThatPassesTheQuotaStops`, `TestTheQuotaWhileCapturesRunDeletesNothingForNothingAndKeepsWhatItStopped`, `TestTheQuotaIsASettingOfTheConfiguration`; the limits per capture: `TestAMaximumSizeEndsTheCaptureWithTheReasonSize`, `TestARingBufferCaptureKeepsTheNewestFilesAndDownloadsAsOne`, `TestTheDefaultLimitAndTheDurationReachTheExecutor`, and on the files `TestAnOutputWithASizeLimitStopsBeforeItWouldPassIt`, `TestARingKeepsTheNewestFilesAndEveryFileStandsAlone`, `TestTheSizeLimitEndsTheCaptureAndTheFileStaysWithinIt` |
| a run with capture attaches it to the report | `TestACaptureTrueRecordsTheTargetForTheWholeRun`, `TestTheCaptureOfARunHasTheScopeOfTheTarget`, `TestCaptureStepsRecordAPartOfTheRun`, `TestACaptureStepWithoutAStopEndsWithTheRunAndAWholeRunCaptureIsNotStoppedByAStep`, `TestCaptureStepsThatHaveNothingToDoAreSkipped`, `TestCapturesEndWithARunWhateverEndsIt`, `TestARestartAttachesTheCapturesOfARunThatWasRunning`, `TestTheCapturesOfAnExpiredRunAreDeletedWithIt`; over the API with the real engine: `TestARunWithCaptureTrueAttachesTheCaptureOfItsTargetToTheReport` (the artifact is the file with exactly the device's frames; `?run=`; the events of the run), `TestCaptureStepsOverTheAPIRecordOnlyTheirWindow`, `TestAnAbortedRunStopsItsCapture`; `TestADeploymentWithoutACaptureVolumeCannotCapture` (a scenario that records is refused); on the real kernel `TestARunWithCaptureTrueAttachesTheTrafficOfItsTargetOnTheRealKernel`; not runnable here |
| low disk stops captures but not the run | `TestALowDiskRefusesNewCapturesAndStopsRunningOnes` (refusal below 10 %, `disk_low`, what was recorded stays, the events), `TestALowDiskStopsTheCaptureOfARunButNotTheRun` (the run passes with `capture_missing` and the file), `TestARunWhoseCaptureCannotStartRunsWithoutItAndSaysSo`, `TestARunGoesOnWhenTheExecutorRefusesItsCapture`, `TestACaptureThatEndsEarlyLeavesItsFileAndAWarning`, `TestACaptureThatCannotStartIsAWarningAndTheRunGoesOn`; the executor's side: `TestACaptureStopsWhenTheDiskIsAlmostFullAndRefusesToStartOnOne`, `TestACaptureOnAFullDiskIsRefusedAndStopsWhenTheDiskFills`, `TestAFullDiskIsTheDiskLowReason`; on the real kernel, on a 4 MiB file system that the traffic fills: `TestALowDiskStopsTheCaptureButNotTheRunOnTheRealKernel`, `TestTheExecutorRefusesACaptureOnAVolumeBelowItsLimitOnTheRealKernel`; not runnable here |
| Scope: capture by network (incl. WireGuard interfaces, raw IP link type), device or selector (AF_PACKET per S9) | as the first row; `TestPcapngHoldsInterfacesOfDifferentLinkTypes`, `TestWiresharkReadsBothFormats` (raw IP next to Ethernet in one file), `TestACaptureRequestThatDoesNotFitIsRefusedWithItsPointer`, `TestACaptureRequestIsDecodedAndItsLimitsAreChecked` |
| Scope: ring buffer, quotas, retention, disk-low behavior, download (also while running) | as the second and fourth rows; `TestCopyCompleteCutsARecordThatIsBeingWritten`, `TestADownloadJoinsTheFilesOfARingAndCutsAFileThatIsBeingWritten`, `TestTheRecordsOfCapturesSurviveARestart`, `TestARestartEndsACaptureThatWasRunningAsFailed`, `TestCapturesFollowTheScopesOfTokens`, `TestAnIdempotencyKeyMakesOneCapture`, `TestOnlyEightCapturesRunAtOnce` |
| Scope: capture step type; attachment to runs | as the third row |

## Command line client and example suites (M18)

Plan §2.15 and M18. `chaosctl` is the client of the REST API for CI pipelines: it applies a profile or a fault with a TTL,
runs a scenario from a file and returns its result as the exit code, waits for a device, and runs a command under a fault
that is always taken away again. It has no other access to the gateway than the API with a bearer token. M18 also adds the
example suites for pytest and Jest that use the generated clients, and runs them in CI. The generated TypeScript and Python
clients themselves exist since M5 (`make generate`, "Generated code" below); M18 gave them their examples and documented
what they leave out.

### How it works

- **The commands are package `internal/ctl`, `cmd/chaosctl` is a three-line `main`.** The plan names Cobra (§3.7), which is
  what the package uses. `ctl.Run(args, Env)` takes the streams, the environment, the signal channel, the clock and the
  poll interval as arguments, so the tests run the whole CLI in-process against an `httptest` server of the API
  (`internal/api/ctl_test.go`) and no test needs a binary on the `PATH`.
- **The gateway and the token** come from flags or the environment: `--url`/`CHAOSGW_URL` (default `https://127.0.0.1:8443`,
  with or without `/api/v1`), `--token`/`CHAOSGW_TOKEN`, `--token-file`/`CHAOSGW_TOKEN_FILE`. The certificate of a gateway with
  a self-signed certificate is trusted by `--fingerprint` (SHA-256 of the certificate, which pins it without a copy of it),
  by `--ca-cert` (a PEM file), or not verified with `--insecure`; the default is the system's trust store. The three (and
  `CHAOSGW_FINGERPRINT`, `CHAOSGW_CA_CERT`, `CHAOSGW_INSECURE`) are one setting whose source is decided as a whole: a trust flag on
  the command line (`--insecure=false` included) means the trust variables are not read, so a variable of the shell cannot
  override a pin; `--insecure` next to a pin or a CA file from the same source is a usage error (2), and `--insecure` prints a
  warning on standard error (P4-AUDIT-11). A token with the
  scope `overlays` does everything except `wg export` (scope `full`) and `reset --all`.
- **Output.** Standard output is the result and nothing else: the id of the overlay or run a command created (so
  `id=$(chaosctl fault set …)` works), the result of a waited run, a table. Progress and explanations go to standard error
  (`[  12.3s] step cut applied at +10.0s`). `--json` prints the API's JSON where a command has a result.
- **Commands.** `profile apply <profile>` and `fault set` (impairment flags, or `--body` for a whole fault) create an
  overlay for `--device`, `--group`, `--network` or `--global` with `--ttl` and `--lease`; `overlay create -f`, `overlay
  list`, `overlay delete`, `overlay renew`, `reset [--all]`; `run [scenario] [-f file] [--set k=v] [--wait] …`, `abort`, `wait run`, `wait
  device`; `with`; `explain`; `wg export`; `version`. Every command's `--help` has the details and examples.
- **`run`.** The scenario is a stored one (by name or UUID) or a YAML/JSON file (`-f`, `-` for standard input), sent as
  `inline` with the `parameters` of `--set`; the server validates it (the CLI interprets nothing of it). Without `--wait` the
  command prints the run id and returns 0. With `--wait` it polls the run (every second), reports the steps, checks and
  warnings as they change, prints the result and returns 0 for `passed`, 1 for `failed` (a check), 2 for `error` and
  `aborted`. `--junit <file>` saves `report.xml`; `--artifacts <dir>` saves `report.json`, `report.xml`, `events.jsonl` and
  the captures of the run as `capture-<id>.pcap[ng]` (a key log as `keylog-<id>.txt`; a 403 on an artifact that needs scope
  `full` is a warning, any other failed download makes a passed run return 2). A download is streamed to `<name>.part` and renamed when it is complete (removed on an
  error; nothing is buffered in memory, a capture can be larger than the runner's memory), the name taken from the headers; the artifacts
  directory is made with mode 0700 and its files have 0600 (captures hold raw traffic, a key log is a secret), the JUnit file 0644 (P4-AUDIT-30).
  `--no-queue` is `?queue=false` (`409 target_busy`).
- **`--to` of the fault flags** is `uplink`, an address (a bare one is a /32, an IPv6 one a /128) or prefix, a hostname (`mqtt.example.com`,
  `*.example.com`; the gateway accepts it since M20), or the name of a network; `network:<name>` and
  `hostname:<name>` say which is meant where the form is ambiguous (a network may be called `lab.net`) (P4-AUDIT-35).
- **The lease is renewed automatically.** A run that is waited for gets a lease (30 s unless `--lease` says otherwise, `0`
  for none) and the CLI renews it at a third of its length until the run has ended, so a CI job that is killed leaves no
  fault behind: the gateway aborts the run when the renewals stop (plan §2.10). `--lease` without `--wait` is refused, as
  nobody would renew it.
- **Interrupts and timeouts.** A SIGINT, SIGTERM or SIGHUP (a lost ssh session, a CI agent that gives up) during `run --wait`,
  and `--timeout`, abort the run (and wait for the gateway to say that its overlays are gone) and return 2; if the run has
  ended in the meantime and the abort is refused, the command reads the run once more and returns the code of its real result.
  `wait run` does not own the run: a signal or its `--timeout` only stops waiting (2). The signals are subscribed to once,
  before the first request of `run`, `wait` and `with`, so none of them kills `chaosctl` (143) between the start and the wait:
  a signal while `POST /runs` is in flight lets the request finish (at most 5 s after the signal) and aborts the run it made;
  one before the run exists is 2. Every request of a wait (a poll, a renewal) runs under a context that the signal, the
  `--timeout` and the cancellation of the command end, and has 10 s of its own (a poll that hangs is a failed poll), so a
  gateway that accepts the connection and does not answer neither stretches `--timeout` nor delays the abort; the create and
  export calls keep the 2 minutes of `--request-timeout` (P4-AUDIT-33, -34). Ten failed polls in a row (a gateway that does not
  answer; a 404/401/403 ends at once) end the wait with 2.
- **`wait device <name> [--online] [--connected tcp/8883]`.** Polls `GET /devices/{name}` and, for `--connected`,
  `GET /flows?device=`: the device is online (the observed state of the API: a confirmed neighbor entry, a connection or a
  WireGuard handshake) and has a connection in the state `ESTABLISHED` to the port (`udp/…`: an answered flow), optionally to
  one address (`tcp/10.0.0.5:8883`). A device the gateway does not know yet is waited for. 0 when it holds, 1 at
  `--timeout` (default 60 s) naming what is missing, 2 when the gateway cannot be asked: ten failed polls in a row, or the
  time runs out while the latest poll failed (or hung for a poll interval) or none was answered ("timed out after 3s, and the
  gateway could not be asked: …"), so a script that reads 2 as an infrastructure error does not take a dead gateway for a failing
  device (P4-AUDIT-32).
- **`explain (--device <name> | --src <ip>) --dst <ip|hostname> [--protocol tcp] [--port 8883]`** is a thin `GET /explain` (plan §2.4,
  M8a): the verdict of the access rules, the winning fault of each family with the ones it overrides, and the route, in words;
  `--json` prints the API's answer. P31-AUDIT-06 and P31-M35a-08 promised it for M18; it was built with the audit
  (P4-AUDIT-47). It sends no packet and needs the scope `read`.
- **`with [flags] -- command args`.** Creates the overlays (`--profile`, the fault flags, `--overlay <file>` any number of
  times), runs the command with the streams of chaosctl, and removes the overlays however the command ends. They get a lease
  (30 s, `--lease 0` for none; the default applies to the requests that name no lease, so the `lease:` of an `--overlay` file stays
  and an explicit `--lease` replaces it, each overlay renewed at a third of its own lease; P4-AUDIT-35) that is renewed while the command runs, so the gateway removes them if chaosctl itself is
  killed without a chance to clean up (SIGKILL, a lost machine), and the TTL of `--ttl`. The command's environment has
  `CHAOSCTL_OVERLAYS`. A SIGTERM or SIGHUP goes on to the command as it came; a SIGINT does not (it is the terminal's Ctrl-C, which reaches the
  command in the same process group, and passing it on would interrupt the command twice); a second signal kills it.
  Either way chaosctl waits for the command to end and then cleans up. If the second overlay is
  refused the first is removed again and the command does not run.
- **The exit code of `with`** is the command's (128 + n when a signal n ended it). Failures of chaosctl itself are **125**, a
  command that cannot be run **126**, one that is not found **127** (the conventions of `timeout(1)` and `env(1)`), because
  `with` is the one command whose result is somebody else's exit code and 2 could be the command's own. If the command
  succeeded but an overlay could not be removed, the result is 125; if the command failed, its code stays and the failed
  removal is reported on standard error. An overlay that is gone already (its TTL ran out) is not an error.
- **`wg export <network> <client>`** (plan §2.2.1) downloads a client's configuration (`conf`, `png`, `svg`) from the API;
  the file is written with mode 0600 because it holds the private key, also over an existing file (it is made anew next to the
  target and renamed over it, so the mode of what was there does not count and a symbolic link is replaced, not followed; as
  `chaosgw wg export` does; P4-AUDIT-31).

### Example suites

`clients/examples` (see its README): `pytest/` (the generated Python client; fixtures for the client, the device, and a
`degraded` helper that applies faults with a TTL and removes them in the teardown), `jest/` (the generated TypeScript client;
`setup.ts` points its relative `fetch('/overlays')` calls at the gateway and adds the token), `scenarios/link-outage.yaml`.
Both suites also drive `chaosctl run … --wait` and `chaosctl with …` as a CI job would. `chaosctl` is a Go program and ignores `NODE_EXTRA_CA_CERTS`, so
against a self-signed gateway it needs its own setting (`CHAOSGW_CA_CERT`, `CHAOSGW_FINGERPRINT` or `CHAOSGW_INSECURE`, the variables pytest reads too); the Jest
helper passes `NODE_EXTRA_CA_CERTS` on as `CHAOSGW_CA_CERT` (`helpers.test.ts`), and the README says so (P4-AUDIT-50). The examples test itself uses plain HTTP, so no TLS
path of the suites runs in CI; the certificate checks of `chaosctl` are covered by `internal/ctl`. The tests of a suite share one device, so Jest runs its files one after the other (`maxWorkers: 1`): in parallel, a
fault of `degraded.test.ts` on the device made the precondition of the scenario run (no foreign overlay on the target) fail. `make test-examples` generates the
clients, installs the suites' dependencies (`.venv`, `npm ci` in `clients/examples/jest`) and runs
`TestTheExampleSuitesPassAgainstTheGateway` (`internal/api/examples_test.go`, build tag `examples`): the test starts a gateway on
the simulated kernel with the real engine and the real clock, mints a token with the scope `overlays`, builds `chaosctl`,
runs pytest and Jest as subprocesses with `CHAOSGW_URL`, `CHAOSGW_TOKEN`, `CHAOSGW_DEVICE` and `CHAOSCTL` in their environment
and reads their JUnit files: at least eight tests each, none failed, **none skipped** (the pytest fixture of the CLI skips
when `chaosctl` is missing in a user's environment; in CI that would fail the test). Afterwards the gateway must hold no
overlay. The `level0` CI job runs `make test-examples` after `make test`.

### Decisions

Taken by best practice where the plan leaves room (none needs the maintainer, so there is no Phase 4 item in `open-items.md`
for M18):

1. **Standard output is data, standard error is talk.** A CI script captures the id or the result; progress lines in the
   output would break `id=$(…)`.
2. **A waited run has a lease by default (30 s).** The plan lists "automatic lease renewal" among the CLI's features; a CLI
   that can be killed and a run that goes on forever are what the lease is for, and renewal costs one request per 10 s.
   `--lease 0` opts out. A run that nobody waits for has none (nobody could renew it).
3. **A signal or `--timeout` aborts the run the CLI started,** and returns 2 (`aborted`); a cancelled CI job must not leave a
   fault on a lab device. The run the CLI only waits for (`wait run`) is left alone.
4. **`with` returns the command's exit code and uses 125/126/127 for itself.** The plan's exit codes 0/1/2 are those of `run`
   and the waits; a wrapper that returns the wrapped command's code cannot also use 1 and 2 for itself.
5. **The lease of `with` is on by default as well,** for the same reason as 2: `with` is cleaned up by its `defer` and by the
   signals it catches, the lease covers the cases it cannot (SIGKILL, power loss).
6. **`wait device --connected` wants the state `ESTABLISHED`,** not "past the handshake" as the checks of a run do: a wait asks
   whether the device holds its connection now; a connection in `TIME_WAIT` after the device went away must not satisfy it.
7. **A device that is not known yet is waited for** (a discovered device appears when it first speaks), so `wait device` can
   be the first command of a job that powers a device on.
8. **`wg export` is in the CLI** because §2.2.1 names it (`chaosctl wg export <client>`; the network is the first argument as
   a client is addressed within its network in the API). The offline `chaosgw wg export` of M4b stays for the host.
9. **The CLI sends no `Idempotency-Key`.** It does not retry a request; a CI job that retries a step starts a new run.
10. **The generated clients are used as they are,** not extended. What they cannot do is documented in `clients/README.md`:
    they read every answer as JSON (the JUnit report and the capture downloads are fetched with `fetch`/`httpx`), and the
    TypeScript client calls `fetch` with a path relative to the API and no credentials, which the Jest example's `setup.ts`
    supplies. A change to the Orval configuration (a base URL, a mutator) would also change the web app's hooks, which are
    generated from the same spec.
11. **The example suites run in the level 0 CI job against the simulated gateway,** not in the testbed: they need no kernel
    and test the integration with the clients (typing, authentication, the pattern of a fixture that cleans up), not the
    kernel. The testbed's level 1 tests of the CLI are for the effects on the wire.

### Tests

Level 0 (all run here, with `-race`): `internal/ctl/ctl_test.go` (the address, fingerprints, the certificate checks against a TLS
server, the flags of a fault, documents, `--set`, connections, the exit code of a run, and the commands against a fake
gateway that fails on request: a gateway that stops answering, an overlay that cannot be removed, a refused second overlay,
renewals at a third of the lease, a second signal, a run that ends between the timeout and the abort), `cmd/chaosctl/main_test.go`, and `internal/api/ctl_test.go`: the whole CLI
in-process against the API on the simulated kernel with the real engine (a run on the fake clock of the API, or paced in real
time where the lease matters; every command of the list above, the files it writes, the exit codes, the state of the
gateway afterwards). Its tests are groups of subtests on one gateway (`TestChaosctlOverlays`, `TestChaosctlRuns`,
`TestChaosctlWith`: a gateway's setup takes seconds under `-race`, a command milliseconds), so the package, which needs
nearly ten minutes under `-race` in the development container, grows by a minute and not by two; `make test` gives
`go test` a timeout of 20 minutes for the same reason. Turning the renewals off fails the two lease tests. The example suites:
`make test-examples` (8 pytest and 8 Jest tests; ran here). Level 1 (`internal/api/e2e_ctl_test.go`, build tag `testbed`, both
CI testbed jobs name the tests with `-expect-pass $M18_LEVEL1`): `chaosctl with` under a delay on the wire with a failing
command and a missing one, runs from a file with exit code 0 and 1 against a scripted device, an abort on a signal, `wait
device`, and a fault whose lease is not renewed; these need namespaces, which the unprivileged container of the development
environment has not: vetted with `-tags testbed`; not run in the sandbox; run by CI.

### Acceptance map (plan M18 "Tests")

| Plan M18 Tests | Test |
|---|---|
| CLI tests against the testbed incl. exit codes | `TestChaosctlRuns/RunsAScenarioFromAFileAndSavesItsReports` (0), `TestChaosctlRuns/Returns1ForAFailedCheck` (1), `TestChaosctlRuns/Returns2ForARunThatEndsAsError`, `TestChaosctlRuns/AbortsTheRunWhenTheTimeoutRunsOut`, `TestTimeoutRacingTheEndOfTheRunUsesTheFinalStatus`, `TestChaosctlRuns/AbortsItsRunOnASignalAndLeavesAnotherRunAlone` (2), `TestChaosctlRuns/ReportsAScenarioTheGatewayRefuses`, `TestChaosctlOverlays/ShowsWhatTheGatewayRefusesAndExitsWith2`, `TestChaosctlOverlays/WaitsForADeviceToBeOnlineAndConnected` (0 and 1), `TestTheExitCodeOfARunIsItsStatus`; on the real kernel (the testbed proper) `TestChaosctlRunPassesForADeviceThatReconnectsOnTheRealKernel` (0), `TestChaosctlRunFailsForADeviceThatDoesNotReconnectOnTheRealKernel` (1), `TestChaosctlAbortsARunOnASignalAndTakesTheFaultOutOnTheRealKernel` (2), `TestChaosctlWaitsForADeviceAndItsConnectionOnTheRealKernel`; not runnable here |
| cleanup of `chaosctl with` when the command fails | `TestChaosctlWith/WithRunsACommandUnderAFaultAndCleansUp` (exit 3, overlays gone), `TestChaosctlWith/WithCleansUpWhenTheCommandCannotRun` (not found 127, not executable 126, killed 137), `TestChaosctlWith/WithPassesASignalOnAndFailsWith125` (signal, refused setup, the first overlay removed when the second is refused), `TestWithRemovesTheFirstOverlayWhenTheSecondIsRefused`, `TestWithReportsAnOverlayItCannotRemove`, `TestWithTreatsAnOverlayThatIsGoneAlreadyAsRemoved` (the removal that finds a 404 keeps the command's code), `TestChaosctlWith/WithAnOverlayWhoseTTLRanOutDuringTheCommand`, `TestASecondSignalKillsACommandThatIgnoresTheFirst`, `TestAnInterruptIsNotPassedOnToTheCommand`, `TestWithCleansUpOnAHangUp`, `TestARunIsAbortedWhenAHangUpArrivesWhileItIsBeingCreated`, `TestWithKeepsTheLeaseOfAnOverlayFileAndAnExplicitLeaseReplacesIt`; on the real kernel `TestChaosctlWithDelaysThePathAndCleansUpAfterAFailingCommandOnTheRealKernel` (the delay on the wire while the command runs, a clean path after it); not runnable here |
| the example test suites run in CI | `TestTheExampleSuitesPassAgainstTheGateway` (`make test-examples`, the `level0` job) |
| Scope: apply profile, set fault with TTL | `TestChaosctlOverlays/AppliesAProfileAndAFaultWithATTLAndRemovesThem`, `TestChaosctlOverlays/ResetAndTheReplacementOfAFault`, `TestChaosctlOverlays/ExplainsTheTrafficOfADevice`, `TestExplainAsksTheGatewayAboutOneTrafficTuple`, `TestChaosctlOverlays/CreatesAnOverlayFromARequestFile`, `TestTheFlagsOfAFaultBecomeItsBody` |
| Scope: `run -f <file> --set …`, `--wait`, `--junit`, `--artifacts` | `TestChaosctlRuns/RunsAScenarioFromAFileAndSavesItsReports`, `TestChaosctlRuns/StartsAStoredScenarioAndWaitsForItLater`, `TestChaosctlRuns/QueuesBehindARunAndNoQueueRefuses`, `TestChaosctlSavesTheCapturesOfARunInTheArtifactsDirectory`, `TestAnArtifactIsStreamedToItsFileAndIsPrivate`, `TestSetArgumentsAreKeyValuePairs`, `TestDocumentsAreReadAsYAMLOrJSON` |
| Scope: automatic lease renewal | `TestChaosctlRuns/RenewsTheLeaseOfTheRunItWaitsFor` (a 1 s lease outlives a 3 s run), `TestChaosctlOverlays/RenewsTheLeaseOfAnOverlayItDoesNotKeepAlive` (`overlay renew`), `TestChaosctlWith/WithRenewsTheLeaseAndALeaseThatIsNotRenewedExpires`, `TestWithRenewsAtAThirdOfTheLease`; on the real kernel `TestAFaultWhoseLeaseIsNotRenewedLeavesTheRealKernelOnItsOwn`; not runnable here |
| Scope: `wait device <name> --online [--connected tcp/8883] --timeout …` | `TestChaosctlOverlays/WaitsForADeviceToBeOnlineAndConnected`, `TestChaosctlOverlays/WaitsForADeviceThatComesOnline`, `TestAConnectionIsGivenAsProtocolAndPort`, `TestOnlyAnEstablishedConnectionMatches` |
| Scope: `with --profile … --ttl … -- <command>` | as the second row |
| Scope: generated TypeScript and Python clients; examples for pytest and Jest | `make check-clients` (they compile and import), `TestTheExampleSuitesPassAgainstTheGateway` (`clients/examples`) |
| Connection, token, certificates | `TestTheAddressOfTheGatewayIsNormalized`, `TestAFingerprintIsReadInTheUsualForms`, `TestTheCertificateOfTheGatewayIsCheckedByFingerprintByCAOrNotAtAll`, `TestTheTrustFlagsWinOverTheTrustVariablesAndInsecureNeverMixesWithAPin`, `TestChaosctlOverlays/ReadsTheTokenFromAFile`, `TestWaitingForARunGivesUpOnAGatewayThatDoesNotAnswer`, `TestWaitDeviceThatRunsOutOfTimeWithoutAnAnswerFromTheGatewayExits2`, `TestAPollThatHangsIsCutOffAndTheWaitGoesOn`, `TestTheTimeoutAbortsARunWhileThePollIsHanging` |
| `wg export` | `TestChaosctlExportsAWireGuardClient`, `TestWgExportWritesTheKeyWithMode0600OverAnExistingFileAndALink` |

## Scenario UI (M19)

Plan §2.10, §2.17 "Scenarios" and M19. The screen of the scenarios: the list with target, length, steps and last result; for
the chosen scenario the tabs Timeline, YAML and Runs; the editors of its settings, steps and checks; running it and following
the run with its progress, results and artifacts; the page of one run; and the running scenarios on the overview. M19 adds no
backend: the scenarios, runs, checks and captures are those of M15 to M17, and the screen is built on the API of those
milestones and on the configuration draft of M13.

### How it works

- **Routes** (`src/router.ts`): `/scenarios` (the list; the first scenario is shown), `/scenarios/:id` (id or name; `?tab=yaml|runs`,
  `?run=<id>` shows a run) and `/runs/:id` (the page of one run, also of an inline run that has no scenario of its own). The
  navigation entry Scenarios is a link now; Captures stayed a disabled entry until M23a (P4-M19-01, closed; see "Captures UI (M23a)").
- **A scenario is an object of the configuration,** so it is changed like a profile (M13): the dialogs "Add to the changes" put the
  scenario into the draft (`useDraftStore().setScenario`, `scenarios`), the unapplied-changes bar counts it ("2 changes in 1
  scenario"), the preview drawer and the concurrent-change dialog take it to the gateway as a merge patch (`scenarios` in
  `POST /revisions`), and the list marks a scenario NEW, CHANGED or DELETED (with Undo). The steps and the checks are lists,
  which a merge patch replaces whole. A scenario the gateway would refuse as it is (no step, a check whose window names a step that
  is gone, a name that is taken) holds back Preview & apply (`draft.invalidScenarios`, the bar says "a scenario needs fixing
  first") and is listed on the scenario's page; what the gateway says about a candidate is kept by pointer
  (`draft.serverErrors[<id>]["steps/2/at"]`) and shown on the scenario and under the fields of the step or check dialog it names.
- **The list** (`ScenariosView.vue`): name, target, "5 steps · 1 min 15 s · 2 checks" (`scenarioMeta`; the length is the last
  step or the end of the last check window, without the time a `wait` step will take), and the last result from `GET /runs`
  ("Failed · 2 min ago", "Never run"; the list of runs has no checks, so no count).
- **The timeline** (`ScenarioTimeline.vue`) shows the steps in the order the engine runs them (by `at`, then by the list), each
  with its time, a chip for its type (orange for what impairs traffic, red for a rule, blue for the rest) and a line in words
  (`stepText`: "↑↓ 200 ms ±50 → TCP 8883", "Drop · cut existing → TCP 8883", "Wait for a DNS query for … · timeout 60 s"). The
  checks stand next to it (`ChecksCard.vue`: "Reconnected: TCP 8883 · within 30 s of step restore") and the artifacts below
  (`ArtifactsCard.vue`). Edit and Delete are on each step and check, Add step and Add check under them. A step a check or
  a `remove` step refers to cannot be deleted (the button says why), and a step that is renamed takes those references along.
- **The editors.** `StepDialog.vue` (`lib/stepForm.ts`) has the id, the time, "Acts on" (the scenario's target or a narrower one:
  devices, groups and networks) and the fields of the type: a profile, a fault (the fault dialog's fields without target and
  duration: kind, traffic, parameters; `FaultStepFields.vue`, `TrafficFields.vue`, which the fault dialog uses now too), an access
  rule (access, reject type, traffic, also cut existing), a WireGuard action, a capture start or stop, a wait (online, a new
  connection, a DNS query; timeout), the removal of an earlier step's overlay, or the restore. `CheckDialog.vue`
  (`lib/checkForm.ts`) has the type, the traffic or the name of the query, the allowed destinations of "traffic not seen" and the
  window (opens at the start or a step; closes after a time or at a step). `ScenarioDialog.vue` (`lib/scenarioForm.ts`) has name,
  description, target, capture, how a zero-hit fault is reported and the preconditions to skip. Each message stands under its field
  (the checks mirror `domain.ValidateScenario`'s structural rules: `scenarioProblems` in `lib/scenarios.ts`, and the seven that
  need the target or the other steps in `lib/scenarioRules.ts` — a step that widens the target, a `remove` of a step that makes no
  overlay, the reserved id `start`, a `capture: stop` without the start of the same target before it, a tunnel outside the target,
  a `reset` with another protocol than TCP, and a wait timeout or a window of `0s`; P4-AUDIT-36; each case is a case of the Go
  tests with the same pointer, and what the configuration does not tell — is the device in the group — is not a problem in the UI,
  the gateway has the last word), appears once a field was
  changed or the button was pressed, and the gateway's refusal (422) of a candidate opens the dialog with it under the field its pointer
  names. The step editor also offers only what passes them: "Acts on" lists the scenario's target and what lies inside it, the
  tunnels are those inside the target, and a `remove` lists the earlier steps that made an overlay. Every dialog shows the request its button leads to under `</> API`.
- **The YAML tab** (`ScenarioYaml.vue`, `lib/scenarioYaml.ts`) shows the scenario as the file `chaosctl run -f` takes, with the
  names of the objects where the configuration has UUIDs, in the order of the spec. The text can be edited; while it is typed it is
  read (syntax with the line, a mapping at the top, names resolved to UUIDs, the generated Zod schema of the spec, the fields the
  format does not have, `scenarioProblems`) and "Apply to the changes" puts a valid text into the draft. "Download" saves it,
  "Import YAML" (list header) makes a new scenario of a pasted text or a file. The library is `yaml` (2.9.1, in the scenario's chunk
  only, it loads with the lazy route).
- **Run** (`RunDialog.vue`, `lib/runForm.ts`): parameters as `path=value` lines (`chaosctl run --set`), "wait in the queue when the
  target is busy" (off: `?queue=false`, `409 target_busy`). A scenario of the active revision starts with
  `POST /scenarios/{id}/runs`; one with changes that are not applied yet is sent as `inline` with `POST /runs` and stored with the
  run only, no revision is made. The gateway's refusals appear in the dialog (parameter paths under the field).
- **Following a run.** The scenario's page follows the run it started, an active run of the scenario, or the one named by `?run=`
  (the Runs tab's "Show"); "Back to the scenario" lets go of it. `RunProgress.vue` shows the state with a label (queued, running,
  passed, failed, error, aborted), who started it and when, the steps that are through, the time against the length of the
  timeline, a progress bar, the lease left, the reason of an `error`, `aborted` or `failed` run and the warnings of the report
  (`zero_hit`, `step_late`, `capture_missing`). On the timeline each step is pending, applied, skipped or error (a label, not
  only a colour), with the two moments of a step: when the timeline handed it over, against its schedule, and when the engine answered that it was
  applied ("handed over at 10.0 s (+3 ms), applied at 13.4 s (apply 3.4 s) · generation 51"; `start_offset_ms` and `offset_ms`,
  P4-AUDIT-18), the wait behind an apply, "now" on the step in effect and "next in 6 s" on the next one; a pending `wait` says
  "waiting for the condition (timeout 60 s)" once its time has come, and the steps after a finished wait count down against
  the schedule shifted by the time it waited (P4-AUDIT-42). A run that the page cannot read is not "gone": only a 404 says that
  (the gateway keeps the runs of 30 days), any other failure shows the error with Try again (P4-AUDIT-12). The checks show PENDING until their windows close, then
  PASSED, FAILED or ERROR, with the message and the observations that decided them. The artifacts are links to the API:
  the capture (pcapng, with its size), `report.xml` (JUnit), `report.json`, `events.jsonl`, and the key log; they are saved as
  `run-<first 8 characters of the run id>-<file>` (`run-0a1b2c3d-events.jsonl`), so the files of two runs differ (P4-AUDIT-41; the
  `download` attribute of the link, because the events and the reports send no `Content-Disposition`). Abort removes the
  run's overlays (the toast quotes the gateway). A queued run says it waits.
- **The page of a run** (`RunView.vue`, `/runs/:id`) is the same for any run, with the counters of the overlays the run made
  from `report.json` once it has ended (a rule that matched no packet shows in orange).
- **Live.** A run is read every second while it is queued or running (`useRun`) and the events `run_queued`, `run_started`,
  `run_step`, `run_check` and `run_finished` invalidate the runs (`composables/eventStream.ts`; the end also the overlays, devices
  and faults); the lists of runs are read every 5 s. A revision invalidates the scenarios.
- **The run lists are windows, and what must not be missed is asked for by the gateway's filters** (P4-AUDIT-39). `useRuns` returns
  one page (the newest 200 for the list's last results). The runs that are going on come from `status=running` and `status=queued`
  (`useActiveRuns`: the overview and the page's followed run), the Runs tab from `scenario=<id>` and `scenario=<name>` (the runs of
  an inline scenario carry the name only) with 50 runs and "Show older runs" (100 more, up to 500), and a scenario that the
  newest 200 do not reach, while the gateway has more, gets its last result with `scenario=<id>&limit=1` (`useLastRuns`). While
  the runs load the list says "Loading…", and when they could not be read "Last result unknown" with Try again; "Never run" and
  "has not been run yet" are answers of the gateway only (P4-AUDIT-12).
- **The overview** has "Running scenarios" (`RunningScenarios.vue`): each queued or running run with its target, steps through,
  the step in effect and a progress bar, linking to the scenario with the run shown (or to the run's page for an inline scenario
  and for a scenario that was deleted meanwhile: a run goes on after its scenario is deleted, on its own copy, so the preview does not
  list it; the run's page offers "Open the scenario" only while the scenario exists, and the page of a scenario that is not there
  links to `/runs/<id>` when the address names a run, P4-AUDIT-40).
- **What the build runs.** The step types and the check types the editors offer come from `GET /capabilities`; a step of a type the
  build does not run yet (DNS, TLS, DHCP) is shown and kept, its dialog says it is edited in the YAML view.
- **Other changes.** The custom traffic fields of the fault dialog moved into `TrafficFields.vue`, the pure part into `lib/trafficForm.ts`
  (the fault dialog's checks and tests are unchanged); `GET /runs`, `GET /scenarios` and `GET /capabilities` have composables in
  `composables/gateway.ts`; the entry Captures says "Arrives with a later milestone" (it said M17, whose backend is done).

### Decisions

Taken by best practice where the plan leaves room (the one that needs the maintainer is `P4-M19-01` in `open-items.md`):

1. **Scenarios are edited through the draft, not written to the API one by one.** They are objects of the revision (§2.1.1), there is
   no write endpoint for them, and the draft gives preview, apply, undo and the concurrent-change dialog for free.
2. **The timeline editor is a list of forms, not a canvas.** Times are typed (`10s`), the order follows from them; a dragged bar
   would be less exact than the `±100 ms` the engine keeps, and every field of the spec needs a form anyway.
3. **A scenario with unapplied changes is run from the draft (`inline`),** so a change can be tried before a revision is made;
   its run is marked "inline scenario, not stored".
4. **A run from the UI has no lease.** A lease needs a client that renews it, and a browser tab may close; plan §2.10 says a run
   keeps going when the client disconnects. Abort, the scenario's end and the overlay TTLs are the safety net, as without a lease.
5. **A new scenario needs a step before it can be applied** (and the other structural rules): the UI mirrors them so that the
   mistake is found at the field and not as a refusal of the whole candidate. The references (does the device exist) the gateway checks.
6. **The YAML view shows names, not UUIDs,** and reads names or UUIDs, so a file from a repository and the view are the same
   format. A name that two objects share is shown as the UUID; a reference that matches nothing is refused in the view, because
   the candidate would be (`unknown_reference`). Fields the format does not have are refused too, as the API's strict decoding does.
7. **When a revision applied meanwhile and the draft both changed the steps (or the checks) of a scenario, the draft's list wins as a
   whole,** and the concurrent-change dialog names `steps` with both lists (the ids). A merge patch replaces a list whole, so a merge
   member by member would invent a timeline neither side wrote. Changes to other members (description, target) merge as for the other objects.
8. **The progress of a running run is the time against the timeline's length, below 100 % until the run ends:** a `wait` step or a
   check window that reaches past the last step makes a run longer than its steps say. The steps that are through are shown next to it.
9. **No Captures screen in M19.** The captures of a run are artifacts with download links; a screen of captures and the device
   detail's panel have no milestone and no design (open item P4-M19-01).
10. **The figure against the schedule is the hand-over, and it is not shown for or after a `wait` step,** which moves every later step by
    the time it waited (the report has the actual offsets anyway). Until the Phase 4 audit the UI printed the apply time
    (`offset_ms` minus the schedule) as the lateness, which read as a missed promise on a host where an apply takes seconds
    (P4-AUDIT-18). It is `start_offset_ms` minus the scheduled offset (a run without `start_offset_ms` shows the apply moment as before),
    and the apply time is the difference of the two. The countdown to the next step does add the shift of the waits that are through
    (the largest of `offset_ms` minus the scheduled offset, as `exec.go` computes it), P4-AUDIT-42.
11. **The browser tests run against the mocked API,** as plan M19 says. The engine's timing, checks and artifacts are covered by the
    level 0 and level 1 tests of M15 to M17; the fixtures use the generated types, so a change of the spec breaks them at compile time.

### Tests

Level 0, all run here. Web unit tests (`make test-web`, vitest, 352 tests in 34 files with the older ones): `lib/scenarioRules.test.ts` (the rules of the gateway in a scenario, in the step and check forms, case by case after the Go tests), `typeScale.test.ts` (no size outside the type scale of §2.17 in the sources), `lib/scenarios.test.ts` (types, order, length, words, the problems of a
scenario including a text that is wrong in its types, editing, the changes in words), `lib/scenarioYaml.test.ts` (the references both
ways, the plan's example in and out, the problems by line and by pointer), `lib/runs.test.ts`, `lib/stepForm.test.ts` and
`lib/checkForm.test.ts` (every step and check of the example through the form and back, the checks under the fields, the pointers of
the gateway's messages), `lib/scenarioForm.test.ts` (settings, parameters of a run, the selector of traffic), `stores/draft.scenarios.test.ts`
(changes, patch, held-back apply, messages by pointer, overlaps and reapply) and the routes in `router.test.ts`. Playwright against the mocked
API (`make test-e2e`, `e2e/scenarios.spec.ts`, 33 tests): the list; the empty state; editing a step, adding a step and a check and
deleting one, through the preview to the patch that is sent; renaming a step; the fields' messages; building a scenario from nothing
(settings, three steps, a check, the apply held back until it has a step); delete and Undo and a refused candidate; the YAML tab and
the import; running a scenario and following it to a failed run (the step in effect, timing, queue wait, warning, checks, evidence,
artifacts, the Runs tab, the page of the run with its counters); a passed run; abort; a queued run and a refused busy target; refused
parameters; running a draft inline; the overview; a step of a type the build cannot edit; no run while a revision waits for
confirmation. The Phase 4 audit added: the runs that load or fail and a followed run that cannot be read (P4-AUDIT-12), the run lists that are asked
for by status and scenario and paged (P4-AUDIT-39), the hand-over and the apply of a step and a wait that holds the countdown
(P4-AUDIT-18, -42), the names of the downloads (P4-AUDIT-41), a run of a deleted scenario (P4-AUDIT-40), the focus after a deletion and the
tab keys (P4-AUDIT-37), and the editors' rules (P4-AUDIT-36). The whole Playwright suite (223 tests) passed with them. One test keeps the browser and the gateway together:
`lib/scenarioYaml.test.ts` compares the YAML that the UI writes for the example of plan §2.10 with `internal/domain/testdata/scenario-ui-export.yaml`
byte for byte, and `TestTheYamlOfTheScenarioUiIsAcceptedByTheGateway` (`internal/domain`) decodes and validates the same file against the
example configuration (the device is found by its name). Apart from that test, `go build ./...` and `go vet ./...` are unchanged (no Go
code changed). There is no test that needs a VM: nothing of M19 runs in the testbed, and no browser flow of the testbed was added (decision 11).

### Acceptance map (plan M19 "Tests" and Scope)

| Plan M19 | Test |
|---|---|
| Tests: Playwright — build a scenario | `a new scenario: settings, steps and a check, then the preview and the apply`, `edit a step, add a step and a check, delete a step …`, `the YAML view …`, `import a scenario from YAML …`, `a step that is renamed …`, `a step id that another step has …`; the unit tests of the forms and of the draft |
| Tests: … and run it | `run a scenario and follow it …`, `a scenario with changes that are not applied yet runs from the draft …`, `a queued run waits for the target …`, `the gateway refuses the parameters of a run …`, `abort a run …` |
| Tests: … then check the result | `run a scenario and follow it …` (state Failed, reason, PENDING then FAILED and PASSED with message and evidence, timing, warning, counters on the run's page), `a run that passed …` |
| Tests: … and the artifact display | `run a scenario and follow it …` (capture 1.8 MB, JUnit, JSON, events and their links; none before the run ended), `a run that passed …` |
| Scope: scenario list | `the list: target, steps, length, last result …`, `no scenarios …`, `delete a scenario and undo it …` |
| Scope: timeline editor | the editing tests above; `lib/stepForm.test.ts`, `lib/checkForm.test.ts`, `lib/scenarios.test.ts` |
| Scope: YAML view | `the YAML view …`, `import a scenario from YAML …`, `lib/scenarioYaml.test.ts` |
| Scope: run view with live progress, results and artifacts | `run a scenario and follow it …`, `abort a run …`, `the overview shows the running scenarios …`, `lib/runs.test.ts` |
| §2.17 run states, Abort removes the run's overlays | `run a scenario and follow it …` (Running, Failed), `a queued run waits …` (Queued), `abort a run …` (Aborted); the removal of the overlays is the engine's (`TestAbortRemovesTheOverlaysOfTheRun`, M15) |
| §2.17 concurrent change, unapplied changes | `stores/draft.scenarios.test.ts` |

## DNS faults and hostname selectors (M20)

Plan §2.4, §2.6, §3.3, §3.11 and M20, with the acceptance carried over from Phase 2 (P2-M9-02: rules with a hostname destination,
P2-M11-01: profiles with a `dns` part). The DNS proxy of M6b answers faults now, the kernel matches the addresses the proxy
answered for the names that rules and faults select, the executor writes them on a netlink connection of its own, and the
checks, waits and captures that name a host use the same addresses.

### How it works

- **DNS-derived sets** (`compiler/hostnames.go`). One nftables set per hostname selector in use (exact name or `*.suffix`, in its
  canonical form `domain.HostnamePattern`: lower case, no trailing dot), of the type `ipv4_addr . ipv4_addr` with the flag
  `timeout`: the requesting device's address and the address in the answer. Rules and faults look the pair up on the conntrack
  original tuple, `ct original ip saddr . ct original ip daddr @dns_<id>_<hash>`, so both directions of a connection, a redirected one
  and one behind NAT find their element. The id is `dns_` and ten hex digits of the hash of the pattern and is what the proxy and
  the API call the set (`DNSSetID`, the `set` of the spec); the name in the kernel adds a hash of the definition (type and flags,
  plan §3.2), so a changed definition makes a new set. The sets are `SetDef.Dynamic`: an apply creates a missing one and never flushes,
  fills or compares the elements of one that stays (S11; verify compares type and flags, not elements); one that no rule or fault
  needs any more is deleted with its elements. `Target.DNSSets` lists them with the clients whose resolutions are recorded (the
  sources of the rules and faults that use the selector).
- **Access rules** (`compiler/access.go`). A rule with a hostname destination is a rule of its place in the list whose destination match
  is the lookup (`DestKind` `hostname`); the responder form of a forwarded connection (D34) looks up the device that answers and the peer
  that opened it. `AccessPlan.Matches` takes the names a source resolved for an address (`Tuple.Names`), which the engine knows
  (`Engine.HostNames`): `explain`, the cut of existing connections and the checks use it. The warning `hostname_unresolved` and the rule's
  state `disabled` for it are gone.
- **Faults with a hostname** (impairment and MTU). The addresses are not known at compile time, so such a candidate is not part of a
  `domain.Table` (`Table.Unresolved`, now in the order of its rank). The compiler writes one rule per fault (and per device for a fault with a queue of
  its own, D18) in front of the four lookup levels, strongest first: the source set (`hsrc_<hash>`, static, refilled at every apply), the
  protocol and ports, the lookup, `goto mark_<id>`; a winner that impairs nothing goes to `mark_0`. The MTU lookup chain has the same rules in front of its
  levels. Decision 2 says what that costs.
- **The proxy** (`dnsproxy`). `hostname_sets` of its configuration tell which names to report: after the answer is known and before it
  is sent, the question and the CNAME targets of the answer are matched against the selectors (and the client against the set's
  sources), and (client, name, A addresses, smallest TTL) is posted to `POST /internal/dns/resolutions`; the answer waits for the call, at most
  2 s from the moment the answer is known (a deadline of its own, not the rest of the query's bound on the upstream, which a `delay` fault may
  have used up, P5-AUDIT-11).
  Reports of concurrent queries gather for at most `BatchWindow` (2 ms) and make one call; a report for the same client, set and addresses made
  in the last minute is not made again (a new configuration forgets them). The report is of the answer that was sent: a wrong answer
  reports the addresses it gave, a short TTL its TTL. A call that fails does not stop the answer (decision 7).
- **The engine** (`engine/hostsets.go`). `RecordResolutions` keeps a mirror of the sets (client, address, the time each element runs
  out, the names it has), writes only what is new or has less than half of its lifetime left, and gives the writes of all callers that
  arrive meanwhile to the executor as one request (group commit; more than `executor.MaxDNSElements` elements go in several requests, and a request that a plan's replacement of the set overtook is not entered into the mirror). The lifetime is max(TTL, 10 minutes). The mirror takes the
  flows of every read of the connections (`refresh`): an element with a flow of its client to its address and less than half of its
  lifetime left is written again with the full lifetime, so a long-lived connection keeps its fault past a short TTL and past ten
  minutes. After a restart the elements the kernel still holds are adopted from the listing the first apply reads back, with the time they have left.
- **A device that changes its address** (`hostSets.rekey`, plan §2.3: "DNS-derived hostname-set entries keyed on the old address are re-keyed";
  P5-AUDIT-01). After every apply that succeeded, full or incremental, the engine compares the owners of the addresses with those of the last one: an element
  (A, x) of a device that has the address B now (too, or instead) is written as (B, x) with the time it has left, into every set whose sources hold B, so a device
  that keeps its cached answer after a "force new IP" keeps its hostname faults and rules without a new query. The element (A, x) is taken out when A went to
  another device (the new holder wins, as in the identity map) or left a device that has other addresses; an old address that stays mapped for its connections
  keeps its elements. A failed write is logged, and the device's next query writes the element.
- **The executor** (`executor/dnssets.go`, `netlinkset.go`). `nft_dns_elements` takes the elements of the sets of this one kind
  (`dns_<10 hex>_<6 hex>`, valid unicast IPv4 addresses, 1 s to 30 days) and runs beside the writer: it never enters the queue (as a read
  does not), one request at a time on a persistent netlink connection per namespace (`google/nftables`, lasting, opened at the first update;
  `executor.New` uses it when the runner is the real one and the nft tool through the runner otherwise, which is what the tests against
  the simulated kernel run). The client sends such requests over connections of its own, so they do not wait for a plan on the main one either. The kernel
  does not change the lifetime of an element it has, so a refresh deletes and adds in one transaction, and a delete that finds nothing
  (the element ran out) is answered with the adds alone. An element with `delete` is taken out (the re-keying above); when the transaction fails, these
  deletes go one by one after the adds, and one that finds nothing is no error.
- **A plan that replaces a set.** Updates for a set whose name the plan about to run changes wait for it (`hostSets.begin`/`end` around the apply of the
  apply loop) and then go into the new set; the report names the selector (the id), not the set. The new set starts empty, the mirror drops what it
  held, and the event `dns_set_epoch` says so. A failed plan changes nothing. Updates for other sets, and all of them while no plan replaces
  anything, are written while the plan runs.
- **DNS faults** (`compiler/dns.go`, `dnsproxy/faults.go`). They are overlays (`dns`, or the `dns` part of an activated profile; "DNS broken" and a custom profile
  with a `dns` part activate, `domain.unavailableFamilies` has only `tls` left). The compiler gives each source the DNS faults that apply to
  it in the order of §2.4 (`domain.RankOrder`: overlay before configuration, level, a fault before a profile part on its scope, the newer first), sources with the
  same list share their entries (`Target.DNSFaults`), and the API's `GET /internal/dns/config` hands them to the proxy
  (`faults`, with the overlay and the sources). The proxy answers a query with the first entry whose sources hold the client and whose names match (or that has none):
  `nxdomain` and `servfail` answer without the upstream; `timeout` sends nothing (UDP and TCP; the log says `TIMEOUT`); `delay` waits `delay` after the answer
  is known; `wrong_answer` replaces the addresses of an A query (AAAA gets no data, other types are answered as usual); `truncate` sends over UDP an
  empty answer with the TC bit, over TCP the real one; `short_ttl` rewrites every TTL (`0s` is allowed), after the proxy's cache, which keeps the real one. The log
  entry names the overlay when the fault changed the answer, and the engine publishes `dns_fault_triggered` (at most once per overlay and device in 10 s).
  A DNS fault competes only with DNS faults (E5). The state of a DNS overlay is `effective` while it is in a list; `GET /overlays` names what overrides it.
  The counter of a DNS overlay, and of the `dns` part of an activated profile, is the number of answers the fault changed (packets, no bytes), kept in memory like the log (its epoch is the start of the API; a counter that adds it to the kernel counters of a profile's other parts
  has an epoch that changes when either part's does, `mixEpochs`, so a kernel fault that is made anew mid-run is not taken for a counter that went on, P5-AUDIT-13;
  the counts of overlays that are not in the store any more are forgotten at the first read of a new snapshot, and a read copies only those of the overlays in a DNS or TLS list, P5-AUDIT-15);
  a DNS step that changed no answer is a `zero_hit` of the run ("the DNS fault of step X changed no answer"); a profile step, "DNS broken" included, is judged by the counter of its activation, which adds
  the answers its `dns` part changed to the packets of its other parts, and says "the profile of step X matched no packet".
- **Hardcoded resolvers** (`compiler/service.go`). A network with `dns.redirect_hardcoded` gets the DNAT of the gateway's address for every other destination of UDP and TCP port 53 too, except what lies in
  its own segments (the network and the networks behind its routers: switched or routed back out of the bridge, never ours, §2.2); an access rule judges a redirected query by
  the resolver the device meant. `dns.block_dot` drops TCP 853 of the network in the chain `dot_block`, jumped to after the access rules (an `allow` rule is an exception) and before the
  access matrix, counted in `dot_<network>`. Both settings are of local networks (the model has them there only). Without a service namespace nothing can be
  redirected and the compile warns.
- **Hosts in checks, waits and captures.** A destination that names a host matches the flows whose addresses the gateway's DNS answered the device for names the
  selector matches. The names come from two places (`api.Server.hostNames`): the proxy's query log (the question and the CNAME targets of every answer, indexed by client and
  address for an hour) and the sets' mirror, so a name no rule or fault selects works too. `ScenarioUsesDNS` counts the `except` list, so the precondition
  `uses_gateway_dns` applies. A capture whose selector names a host captures the traffic to the addresses answered so far, as they are when it starts (`capturemgr.ResolveWith`);
  a host with no answer yet is `422 validation_failed` with the code `unresolved_hostname`, as a filter without an address would capture nothing and say so to nobody.
- **The rest.** `explain` takes the names a device got for an address as the names of the destination and, for a host name as `dst`, asks the DNS family too; the flows API's `dst_hostname`
  is the first name; `dns.faults` and `dns.hostname_selectors` are in the capabilities, `dns` in the step types of runs, `dns_fault_triggered` and `dns_set_epoch` on the event
  stream; the UI labels host names "DNS-derived" and shows what a DNS fault does ("DNS delay 2s · broker.example.com").

### Decisions

Taken by best practice where the plan leaves room (the one that needed the maintainer is `P5-M20-01` in `open-items.md`, decided 2026-10-25: the limit of decision 2 is accepted):

1. **One set per selector, keyed on the device and the address, not one set per rule or per device.** Rules and faults of several scopes that name the same host share the set, the key
   keeps the devices apart, and the compiler keeps the clients of each use (`DNSSet.Sources`) so the proxy reports what something looks up and nothing else.
2. **Hostname faults stand in front of the lookup levels, strongest first.** The plan says "ordered rules against the per-device DNS-derived sets, placed at their level"; the levels of the maps are shapes
   of keys, not ranks, so the exact place of a rule would have to be between the elements of the maps. M20 puts them first, orders them among themselves by §2.4 (`RankOrder`), and removes from a source the hostname
   faults a stronger fault of the whole source covers (`rankHostnames`). A stronger fault with a destination or ports loses to the hostname rule on the traffic both select; the three ways to be exact
   are in the open item. The limit is accepted by decision of the maintainer (2026-10-25, `P5-M20-01`, closed): the lookup tables are split by rank only when a real scenario shows a wrong result.
3. **Overlays only for DNS faults.** §2.6: "DNS faults are overlays; the proxy keeps no state". A configured fault does not have the DNS family (`faultFamily` only knows impairment and MTU for the configuration).
4. **Lifetime max(TTL, 10 minutes), refreshed from the connection table at half of it.** The refresh needs no timer of its own: it runs after every read of the connections, which the watch triggers on changes
   and the poll repeats. An element a flow keeps alive is written about every five minutes.
5. **Group commit instead of a batching timer in the API.** Plan §3.11 puts the ≤ 2 ms batch window in the proxy; the engine's side gathers whatever arrives while a request is on its way, which costs no latency when
   there is no queue.
6. **The gate is the engine's, not the executor's.** Which set a plan replaces is known to the apply loop (the names of the old and the new target), not to the executor, which would have to read
   the nft JSON of every plan; the executor's own operation is the concurrent path, the engine orders the exception.
7. **A report that fails does not stop the answer.** A gateway that cannot write the sets (the executor does not answer) would otherwise take the device's DNS down with it; the answer goes out, the proxy
   logs a warning and the next answer reports again (the failed report is not remembered). The faults of the name may miss the first packets.
8. **`delay` is not cut by the upstream's timeout.** The wait after the answer is known is as long as the fault says; a client with a longer timeout gets its late answer.
9. **A refresh is a delete and an add in one transaction,** because the kernel keeps the lifetime of an element that exists; a delete of an element that ran out fails the transaction, and the adds alone are the answer.
10. **The redirect leaves the network's own segments alone and the DoT block drops.** A resolver of the lab on the same bridge is not the gateway's to take; a blocked port 853 that answers with a reset would make
    clients that fall back to plain DNS do so at once, which is what a test of the fallback may want, so the choice is the silent drop (as a firewall that blocks DoT does); the access matrix and `reject` rules stay available.
11. **Hosts in checks and captures come from the proxy's log as well as from the sets.** A check names hosts no rule selects, and the sets only know those.
12. **`dst_hostname` is what the gateway knows** (a name of an answer of the last hour, or one a selector matches), not what the device asked at some other resolver.
13. **The netlink connection is the default of the real executor,** the nft tool the default of every other runner: the level 0 tests run the same operation against the simulated kernel, the level 1 tests against the real one.
14. **Known limit of the refresh retry.** When one element of a refresh batch ran out in the kernel, the whole transaction fails and the retry writes the adds without the deletes; the other elements of that batch then keep their old, shorter lifetime while the mirror believes they were extended. The window is milliseconds around the expiry of an element, and the next refresh a flow causes or the next resolution of the name repairs it. Not changed: it can only be verified on the real kernel (level 1), which the first implementation could not run.

### Tests

Level 0, all run here: `compiler/hostnames_test.go` (sets, rules, order, per-device rules, MTU, the nft parser accepts the transaction: `scenarioHostnames` is in `transactionScenarios`), `dns_test.go` (the entries of the proxy's list, E5, order),
`dnsredirect_test.go`, `forward_test.go` (the responder form of a hostname rule), `domain` (the unavailable families, the matching of `*.suffix`), `executor/dnssets_test.go` (decode and scope of the operation, the transaction's JSON, refresh and its fallback, an element taken out and its fallback, an update beside a plan on the executor and over the socket, one request at a time),
`apply/dnssets_test.go` (the elements survive every apply on the simulated kernel, verify does not compare them, a set that goes), `engine/hostsets_test.go` and `hostsets_internal_test.go` (the set holds the report when the call returns, no second write,
refresh at half, the group commit, a flow keeps an element alive, an update beside a held plan, an update for a replaced set waits and lands in the new one, a request that was on its way while a plan replaced the set is not remembered as written, a large batch is split into requests the executor takes, adoption after a restart, the names, the elements of a device follow it to its new address: `TestTheElementsOfADeviceFollowItToItsNewAddress`; through the apply loop after a forced new IP: `dhcpaction_test.go` `TestTheDNSDerivedElementsOfADeviceFollowItAfterTheForcedNewIP`),
`dnsproxy/faults_test.go` (every action over UDP and TCP, sources, names, order, the report before the answer, also after a delay past the upstream's bound, CNAMEs, batching, dedup, a failing report, `short_ttl` in the authority and additional sections and in a negative answer),
`api/counters_internal_test.go` (the epoch of a profile counter with a `dns` part, the counters of removed overlays), `api/dnsfaults_test.go` (the configuration, state, resolutions endpoint, events, capabilities), `run/hostnames_test.go`
(checks and a wait for a host), the capture of a host in `api/captures_test.go`. Web: `lib/faults.test.ts` (DNS chips), `lib/rules.test.ts`; the Playwright specs of profiles, rules, the Linux view and the scenarios pass.

Level 1 (written, vetted with `-tags testbed`, run by CI with `$M20_LEVEL1`, not run here: no KVM, no namespaces): `api/e2e_dnsfaults_test.go`, `executor/dnssets_integration_test.go` and `engine/integration_dhcpactions_test.go` `TestAHostnameFaultStillActsAfterTheForcedNewIPWithoutANewQueryOnTheRealKernel`, listed in `.github/level1-tests.env`.

### Acceptance map (plan M20 "Tests" and the acceptance of Phase 2)

| Plan M20 | Test |
|---|---|
| `dig` from clients shows each fault | `TestDigFromAClientShowsEachDNSFaultOnTheRealKernel` (level 1), `dnsproxy/faults_test.go` (level 0) |
| A hostname-selector fault affects only traffic to the resolved IPs | `TestAHostnameFaultAffectsOnlyTrafficToTheResolvedAddressesOnTheRealKernel`, `hostnames_test.go` (compiler) |
| A long-lived connection keeps its hostname fault past a 1 s TTL | `TestALongLivedConnectionKeepsItsHostnameFaultPastAOneSecondTTLOnTheRealKernel`, `TestAFlowKeepsAnElementAliveAndWithoutOneItRunsOut` |
| The set survives 10 overlay changes | `TestTheDNSDerivedSetSurvivesTenOverlayChangesOnTheRealKernel`, `TestDNSDerivedElementsSurviveEveryApplyAndVerifyDoesNotCompareThem` |
| Hardcoded DNS is redirected | `TestHardcodedDNSIsRedirectedAndDoTIsBlockedOnTheRealKernel`, `dnsredirect_test.go` |
| Download and upload latency apply to the queries of a device (S16) | `TestUploadAndDownloadLatencyApplyToTheQueriesOfADeviceOnTheRealKernel` |
| A set update during a long plan is applied without waiting for it | `TestASetUpdateDuringALongPlanIsAppliedWithoutWaitingForIt`, `TestADNSSetUpdateIsWrittenWhileALongPlanRuns`, `TestADNSElementIsWrittenWhileTheExecutorAppliesALargeTransactionOnTheRealKernel`, `TestSetUpdatesAreWrittenBesideAppliesOnTheRealKernel` |
| … and one for a set the plan replaces lands in the new set | `TestAnUpdateForASetThePlanReplacesWaitsAndLandsInTheNewSet` |
| §2.3: DNS-derived entries keyed on the old address are re-keyed (P5-AUDIT-01) | `TestTheElementsOfADeviceFollowItToItsNewAddress`, `TestTheDNSDerivedElementsOfADeviceFollowItAfterTheForcedNewIP`, `TestAHostnameFaultStillActsAfterTheForcedNewIPWithoutANewQueryOnTheRealKernel` (level 1) |
| The added answer latency for hostname-set names meets §3.10 | `TestTheAddedAnswerLatencyForHostnameSetNamesMeetsThePlanOnTheRealKernel` (3 times the target on a VM; the measurement is logged; every measured name has an address of its own, so each answer makes a report and a write, and the set's elements after the run say so, P5-AUDIT-12) |
| Acceptance: an access rule with a hostname destination compiles to the DNS-derived set; a drop rule blocks the resolved addresses (P2-M9-02) | `TestADNSDerivedSetIsDynamicKeyedOnTheDeviceAndTheAddressAndHasATimeout`, `TestADropRuleWithAHostnameBlocksTheResolvedAddressesOnTheRealKernel` |
| Acceptance: a profile with a `dns` part, "DNS broken" included, activates and competes in the DNS family (E5); the family is lifted (P2-M11-01) | `TestADeviceLatencyAndADNSBrokenProfileOnTheNetworkCombine`, `TestTheDNSBrokenProfileActivatesAndCombinesWithALatencyFaultOnTheRealKernel` |
| Scope: DoT blocking | `TestDNSOverTLSIsDroppedForTheNetworksThatBlockItAfterTheAccessRules`, `TestHardcodedDNSIsRedirectedAndDoTIsBlockedOnTheRealKernel` |
| Scope: DNS scenario step type | `dns` in `run.SupportedStepTypes`, `TestARunIsRefusedForWhatTheBuildDoesNotHaveAndForWhatDoesNotFit` (capabilities) |

## TLS responder: certificate cases (M21)

Plan §2.4 (E11), §2.8, §3.3 and M21, with the acceptance carried over from Phase 2 (P2-M11-01: profiles with a `tls` part). The TLS family
of the precedence rules has a mechanism now: selected new connections of a device are routed unchanged into the service namespace and end at
the TLS responder, which shows a deliberately broken certificate (or breaks the handshake) and reports what the device did with it. The check types
`tls_rejected` and `tls_accepted` and the step type `tls` of M16 work, "TLS broken" activates, and the `tls` family is lifted from
`domain.unavailableFamilies` (it is empty now; the case `intercept` was refused with `unsupported_feature` until M22, which brings it).

### How it works

- **The two CAs** (`tlsresponder/ca.go`). The API makes them once at its start in `--tls-ca-dir` (`/var/lib/chaosgw/tlsca`, the volume
  `chaosgw-tlsca`, which the responder's container mounts read-only): `test-ca.pem` (the *test CA*, which a development firmware may install; it signs the
  cases `expired`, `not_yet_valid` and `hostname_mismatch`) and `unknown-ca.pem` (the *unknown CA*, never distributed; it signs `untrusted_ca`), each with its key
  (`*.key`, 0600, the user of the responder is the one of the API). Both are ECDSA P-256 and last ten years; they start five years back, so the certificate of `expired`
  (ran out a day ago, began three months before) lies inside the CA's validity. A CA that exists is never replaced (the devices that installed it would trust nothing
  any more); `POST /tls/ca/regenerate` is M22's (below). `GET /tls/ca` serves `test-ca.pem` as a download (`Content-Disposition: attachment`) for every scope that reads, never a key
  and never the unknown CA.
- **The certificates** (`tlsresponder/certs.go`). One per case and name, the name being the server name of the ClientHello (without one: the name the DNS proxy
  answered last for the destination address, else the address itself in an IP SAN). `untrusted_ca`: valid, signed by the unknown CA. `self_signed`: its own issuer.
  `expired` (valid until a day ago) and `not_yet_valid` (valid from in two days): signed by the test CA, right name. `hostname_mismatch`: signed by the test CA, valid, for
  `chaosgw-mismatch.invalid`. Each is made twice, with an ECDSA and with an RSA key, so a TLS stack that knows only one of them still judges a certificate instead of failing the
  handshake (crypto/tls takes the first the client can use); they are cached for an hour. The responder serves TLS 1.2 and 1.3.
- **The redirect** (`compiler/tls.go`). Every TLS case (an overlay of the kind `tls`, or the `tls` part of an activated profile) is a candidate of the TLS family: `World.TLSCandidates` gives
  each source its cases in the order of §2.4 (`RankOrder`), sources with the same list share their entries (`Target.TLSCases`; `groupCandidates` is the helper of the DNS family too). The kernel
  selects, per case, what it can see: source, destination (an address, a prefix, a network, the uplink, or a host name through the DNS-derived set of M20), TCP and the ports (without ports 443 and
  8883, `domain.TLSPorts`). The chain `tls_redirect` (prerouting, priority -145, after the classification) first leaves the packets that come from `svc0` and the replies alone, then holds two rules
  per case: the first selects a *new* connection (`ct state new`) and sets bit 23 of the conntrack mark (`TLSConnMarkBit`) and the service mark of the packet (bit 20, which table 102 routes into
  `svc0`); the second gives the packets of a connection with that conntrack mark the service mark, as long as the case still selects the connection. So a connection that was established before the
  case came stays with the real server ("new connections"), and the connection the responder holds follows the redirect for its whole life. With `cut_existing` the first rule has no `ct state new`: a
  connection that is there already is selected at its next packet, which arrives at the responder's namespace as a packet of a connection nobody knows there and is answered with a reset; that is the cut,
  and it needs no window (the cut of an access rule is a window of `reject with tcp reset` because the rule drops, here the responder answers). The forward chain accepts what goes to `svc0` with the service mark
  after the access rules, the DoT block and the port forwards, before the matrix (decision 3). Inside the namespace the executor writes a fixed chain when it makes the namespace (`executor.serviceRedirectRuleset`,
  the last step of `service_ns ensure`): TCP that arrives on `svc1` for another address than the namespace's own is redirected to port 9443, where the responder listens and reads `SO_ORIGINAL_DST`
  (`tlsresponder.OriginalDst`). `apply` reads that chain back with the state of the namespace (`ServiceState.Redirect`); a namespace of an earlier version without it is made anew by the next apply, and verify finds
  a missing one.
- **The decision of a connection.** Which case decides a connection depends on what only the connection knows: the server name of its ClientHello. The responder reads the first handshake message (`readClientHello`, also over several
  records), asks `POST /internal/tls/resolve` (client, original destination, server name) and gets the decision of `domain.World.Resolve` — the one `explain` shows, with the DNS names the client was answered for the
  address (`Engine.ResolveTLS`, `api.Server.hostNames`) — with the overlay, the case, the device and its `trusts_test_ca`, and the generation of the configuration the decision belongs to. No case means: the
  gateway's selection was wider than the case (a server name that no case names, a CDN address, a case that went in between). Then the responder passes the connection through to the destination, unchanged
  (the bytes of the ClientHello, then both directions; the namespace's own connection leaves through `svc0`, is accepted by the forward chain towards the uplink, the test networks and the WireGuard tunnels of the role test and is masqueraded on the way out, decision 13), but only if `passthrough` says the gateway would have forwarded the connection (the access rules, the
  protection and the matrix on the destination the device meant, `AccessDecision`); otherwise it resets it (decision 4). Bytes that are no ClientHello (plain text on port 443) go the same way; a connection that says nothing at all is closed after ten seconds. A decision that cannot be had (the API is away) resets the connection and never passes it: the gateway fails closed.
- **What the responder does** (`tlsresponder/cases.go`). The certificate cases are a `crypto/tls` handshake over the connection with the ClientHello put back in front of it: it *completes* (the device took the certificate, outcome `accepted`),
  or ends with an alert of the device (`rejected`, with the alert's RFC name: `bad_certificate`, `unknown_ca`, ...; a TLS 1.3 client built on OpenSSL sends it in the clear, which crypto/tls cannot decrypt, so the responder reads the first records the client sent after its ClientHello itself, `tapConn` and `plainAlert`), or with a close or reset without an alert (`rejected`), or times out after ten seconds (`timeout`); something that is none of these
  (no shared cipher, a version the responder does not speak, bytes that are no TLS) is `client_closed` with a token that says which, because it says nothing of the certificate. `handshake_reset` answers the ClientHello with a reset
  (`reset_sent`), `handshake_close` with a FIN and nothing more (`close_sent`), `handshake_stall` with silence for `StallFor` (30 s) or until the client gives up (`stalled`). The version of a report is the negotiated one when the handshake completed,
  else the highest the client offered. **Reset on removal:** the responder follows `GET /internal/tls/config` by long poll (`Follow`); a connection that a case holds is reset when a configuration of a *later generation than its decision's* does not hold the case
  any more (`SetConfig`), so a stalled device sees the end of the stall at once and goes on to the real server; the generation of the decision keeps a connection decided after the configuration was computed from being reset by an older one.
- **The reports** (`POST /internal/tls/handshakes`). The responder batches them and posts the first at once. The API makes each an event `tls_handshake` (subject: the device; data: client, device, original destination, server name, overlay, case, outcome, alert,
  version, `expected` (`reject` for the certificate cases, `any` for the handshake cases, `domain.TLSExpected`), `violation` (the device completed the handshake of a case it must reject) and `tested` (a rejection that tests what the case is for:
  always for `untrusted_ca` and `self_signed`; for `expired`, `not_yet_valid` and `hostname_mismatch` only when the device's `trusts_test_ca` is set, because a device without the test CA fails them all at the first check as an unknown
  issuer, `domain.TLSTested`)), counts it for its overlay (the counter of a TLS overlay, and of the `tls` part of a profile, is the number of connections the responder held, in packets, kept in memory like the DNS log) and keeps it in a
  log of 20000 entries that the checks read. The state of a TLS overlay is `effective` while some source has it in its list; `GET /overlays` names what overrides it. `explain` says `service: tls_responder` when a case decides the traffic
  and nothing in front of the redirect (an access rule, the protection, the management guard) stops it; the flows API marks a connection with the conntrack mark as `tls_responder`; the health check lists the component `tls` (degraded without
  a poll in 60 s, `disabled` without a service namespace); `tls.responder` and `tls.cases` are in the capabilities.
- **The checks and the step.** `tls_rejected` and `tls_accepted` (`run/checks.go`, `lookTLS`) read the handshakes of the devices of the target inside the window, matched by the selector against the original destination (the same matching as the traffic checks,
  host names included) and by the outcome: `tls_rejected` passes at the first `rejected` handshake, `tls_accepted` at the first `accepted` one. A handshake case does not satisfy either: the device does not abort anything there, the responder does (a scenario checks the
  device's reaction with a traffic check). A check that fails says what the responder saw ("the responder saw 1 accepted ... a device took a certificate it must reject", or "saw no handshake of it (is a TLS case selecting this connection?)"); the report
  comes after the handshake, so the final look waits one second after the window (`tlsSettle`). A `tls` step writes the overlay like the other kinds, and a TLS step or the "TLS broken" profile step that held no connection is a `zero_hit` ("the TLS case of step X held no connection").
- **Containers.** `deploy/compose.tls.yaml`: `tls` (`network_mode: service:svcns`, every capability dropped, the service token and the CAs read-only, health check `chaosgw tls --health`), started with the others (`docs/install.md`);
  `compose.api.yaml` mounts the volume `chaosgw-tlsca` and passes `--tls-ca-dir`; `deploy/Dockerfile` creates the directory for the API's user; `--namespace-check` as for `dns`.

### Decisions

Taken by best practice where the plan leaves room (what needed the maintainer is `P5-M21-01` and `P5-M21-02` in `open-items.md`, decided 2026-10-25: the forms are M23b, and a passed-through connection reaches the test networks and the tunnels too, decision 4 and 13):

1. **The responder asks the API for the decision of each connection.** The plan's configuration shape (`cases` with `sources` and `expected`, "the service looks up a connection by client address and original destination")
   cannot express what a case selects: networks, the uplink and host names are known to the compiler and the engine only, and the server name only to the connection. One local HTTPS call per connection (a kept-alive connection to the
   private link) reuses `World.Resolve` and is the same answer `explain` gives; the configuration is still served and followed, for the reset on removal and for the health check.
2. **Selection by the kernel is the union of the cases, the decision is the connection's.** A case with an `sni` selects at the kernel by everything but the name (the name is in the ClientHello only); a connection whose name no case names falls back to the next case of the
   precedence rules, or, with none, is passed through. A case that must not touch other names on the same address names the destination too.
3. **The redirect is accepted past the access matrix, not past the access rules.** E11: the access rules (and the anti-lockout rule, the management guard) judge the redirected packets by the original tuple in the forward chain before the accept; the matrix (`allow`/`deny` between networks, the
   uplink and the implicit rules) names interfaces, and the packets leave through `svc0`, so it cannot judge them: a case that selects a network the matrix keeps from the server still shows the device the responder's certificate, the answer coming from the gateway.
4. **A passed-through connection must still be allowed by the matrix.** The namespace's own connection to the destination leaves through the gateway's service path (accepted towards the uplink, the test networks and the tunnels for the services,
   decision 13), which the matrix does not see. Passing a connection that the redirect took through it
   would be a way round the matrix, so the API judges the original connection (`AccessDecision`, which also covers the rules and the management guard) and the responder resets what the gateway would not have forwarded. The judgment mirrors the forward chain in front of them too: traffic whose source and destination are in the same local test network (a host behind the network's own downstream router, plan §2.2.2) leaves through the bridge it came in on and is accepted before the guard, the rules and the matrix (`iifname br-x oifname br-x accept`), so it is passed on, and `explain` says allow for it (P5-AUDIT-03).
   Until 2026-10-25 the destination was reached through the uplink only and a server in another test network or behind a tunnel saw the device reset (`P5-M21-02`, decided: option (b)); it is reached now (decision 13).
5. **New connections only, by a conntrack mark.** Per-packet marks (the rest of the classification) would redirect a connection that was established before the case at its next packet; the plan asks for new connections, so the selection is kept in bit 23 of the conntrack mark and given to the packets
   while the case still selects the connection (the second rule), not for good: a case that goes (the chain is not compiled without a case) stops redirecting at once and the responder resets what it holds. That includes a connection it passed through: once no case selects it, its later packets take the uplink without the namespace's translation (the binding was settled at the first packet, which left through `svc0`) and the connection would stall, so the responder resets it as well (decision 7, P5-AUDIT-02).
6. **`cut_existing` is the redirect without the "new" condition.** The established connection meets a namespace that knows nothing of it and is answered with a reset by the kernel's TCP (nothing listens for it, the listener's port answers a segment that is no SYN with RST); no window of
   `reject with tcp reset` is needed and nothing has to be closed again afterwards. The reset reaches the device with the original server address as its source (the namespace's NAT undoes the redirect).
7. **Reset on removal is the responder's, not the gateway's.** The responder knows which connections it holds per case; the gateway's conntrack does not (a delete would not cut a connection behind NAT either, S3). The decision's generation keeps a late configuration from resetting a connection that a newer decision made, and a connection whose case went while its decision was on the way back (`hold` finds a configuration of a later generation that does not hold the case) is reset at once; the API reads the generation before it decides, so a case that goes in between is reset, never kept. A passed-through connection is held too, with the client, the original destination and the generation of its decision: a configuration of a later generation in which no case selects it any more by its client (a case's sources) and its port (the case's ports, else 443 and 8883) resets it, and so does `hold` when such a configuration came while it was being passed on (P5-AUDIT-02). The destination is checked where the configuration names it by address (`cidr`); a network, the uplink or a host name is the compiler's knowledge, so a case that only narrows its destination leaves the connection as it is (a stall stays possible there; resetting every passed-through connection at every change of the cases would cut connections the kernel still sends through the responder).
8. **The expected result is the same for every certificate case, whatever the device trusts; `tested` says what the rejection proves.** `trusts_test_ca` decides whether a rejection of `expired`, `not_yet_valid` and `hostname_mismatch` says anything about the device's date and name checks (S4: it fails them all at the first check as an unknown issuer
   otherwise). The plan's "expected results from `trusts_test_ca`" is this: `expected` stays `reject`, `tested` follows the trust.
9. **Handshake cases act after the ClientHello.** S4's modes: the responder reads the ClientHello first (the case needs the connection to have started), so a client that never says anything is `client_closed` or `timeout` with no case applied.
10. **A connection to the responder's port that did not come through the redirect is dropped.** The health check (`--health`) connects to the port directly and gets a connection that is closed at once: there is no original destination, so there is nothing it could mean.
11. **`rp_filter`.** The replies of the responder come in on `svc0` with the server's address as their source. A host with strict reverse-path filtering (`rp_filter=1`) on `svc0` drops them; the default of Ubuntu is loose (`2`). A per-interface value cannot lower the maximum of `all` and the interface, so the executor does not set it. The preflight does not check it.
12. **Both CAs are in one directory.** The responder has to sign with the keys of both CAs, and the API (which makes them and serves the certificate) runs as the same user, so the two containers share one volume with nothing else in it, and the API's secrets volume is not mounted into the responder.
13. **A passed-through connection reaches the test networks and the tunnels too (P5-M21-02, decided 2026-10-25, option (b)).** What the namespace sends out through `svc0` is accepted by the forward chain towards three kinds of interface
    (`compiler.serviceForward`): the uplink (as before), and the set `ifs_test` (the bridges of the test networks and the WireGuard interfaces of the role test, links included). The management interface, the interfaces of the WireGuard networks of the role
    management and the namespace's own subnet (the fail-closed guard in front) are not among them. In the two-port topology the management network lies behind the uplink, and the accept towards the uplink does not tell it apart: there the kernel lets the namespace reach the management network, and only
    the API's `AccessDecision` (its management guard) keeps a passed-through or intercepted connection out of it (`TestTheResponderKeepsAConnectionOutOfTheManagementNetworkBehindTheUplinkInTheTwoPortTopology`); a guard by address in the kernel would also block a DNS upstream in the management network (P5-AUDIT-17). The accept stands where the one towards the uplink stood, after the access rules, the management guard and the matrix; they cannot judge the packets (they
    come in on `svc0` from the namespace's address, the matrix names interfaces), so the judge is the API's `AccessDecision` of the original connection before the connection is passed on (decision 4), which refuses what a rule, the guard or the matrix refuses,
    towards the management network as towards any other. The same accept serves the interception proxy's own connection (M22, same namespace) and the DNS proxy's queries to a resolver in a test network, which the forward chain dropped before. The server sees the gateway,
    not the device. The plan's wording is "the namespace's address": the namespace's address is translated on the way out, as it is at the uplink (`compiler.serviceMasquerade`), so the server sees the gateway's own address in the network it is in (the bridge's address in a
    test network, the tunnel address of the interface in a WireGuard network). Left as it is, the link-local address `169.254.100.2` is not reachable for a server that has no default route through the gateway, and a WireGuard client drops it (the AllowedIPs of a
    client's configuration carry the tunnel address of the gateway and the networks the client may reach, never this address); the translation is what makes the path usable for a client behind a tunnel. The device's own address as the source (option (c)) stays out:
    the server answers the device's address through the gateway only when the gateway is its router, and the transparent path it needs was not chosen. This is the masquerade of the service namespace and not the `nat` setting of a network, which stays "towards the uplink
    only" (plan §2.2): no traffic of a device is translated towards a test network.
14. **A batch of handshake reports the API refuses is dropped (P5-AUDIT-18).** The responder keeps a batch the API did not take and sends it again, so that a restart of the API loses nothing; a batch the
    API refuses for its content (a 4xx other than 408 and 429: an unknown field or outcome, a bad address) would be refused on every try and hold back every later report until 5000 more pushed it out. It is dropped
    with a warning and counted (`Server.RefusedReports`), as the interception proxy's batcher does on the same endpoint; a transport error, a 5xx, 408 and 429 keep it (`svcapi.Refused`).

### Tests

Level 0, all run here: `tlsresponder` (the CAs and their file modes, that a CA is never replaced, the download holds the certificate only; the certificate of each case fails exactly the check it is meant for under Go's verifier, for both keys; the ClientHello reader on real hellos of TLS 1.2 and 1.3, split over records, malformed;
real TLS clients against the responder on the loopback: every certificate case with TLS 1.2 and 1.3 is rejected and reported with its alert, an insecure client completes the handshake and is reported, the name of the certificate, the handshake cases, a connection that never says ClientHello, passthrough to a real
TLS server, a decision that cannot be had, the reset on removal and the generation that spares a newer decision, a connection decided just before its case went, a passed-through connection that no case selects any more is reset (P5-AUDIT-02), no connection is held after it ends, a refused report batch is dropped (P5-AUDIT-18); the API client), `compiler/tls_test.go` (the responder's list, the rules of the chain for a new connection and for `cut_existing`, every kind of destination, hostname set, shared uplink set, order and grouping, no service namespace, **the golden test of E11**
(`testdata/e11.golden.txt`), the transaction is accepted by nft's parser: `scenarioTLS` is in `transactionScenarios`), `executor/plan_test.go` (the fixed ruleset of the namespace), `apply/service_test.go` (the namespace holds the redirect, a namespace without it is found by verify and made anew), `domain` (the profile's parts, the intercept case was unavailable until M22, what each case expects and what a rejection tests, the default ports),
`engine/tls_test.go` (the snapshot and the kernel's chain, "TLS broken" competing with a device's case, the decision by port, server name, destination and client, the matrix and passthrough, among them a destination in another test network that the matrix allows or keeps apart and the management network that stays closed, a host behind the network's own downstream router (P5-AUDIT-03), the management network behind the uplink in the two-port topology (P5-AUDIT-17), `explain`), `api/tlsservice_test.go` (the configuration by long poll, the decision, host names and the name without SNI, malformed requests, events and counters, the test CA download, health, `explain` and E11), the flows of the API, `run/checks_tls_test.go` (both checks, windows, the report that comes late, selectors, zero hits),
`cmd/chaosgw` (the daemon makes the CAs, `tls --health`), the hardening test of `compose.tls.yaml`, `tools/ciconfig` (the new volume in the fresh-volume check and the upgrade note). Web: `lib/faults.test.ts` (TLS chips), the Playwright specs of profiles ("TLS broken" activates) and scenarios (a TLS check is added and sent).

Level 1 (written, vetted with `-tags testbed`, run by CI with `$M21_LEVEL1`, not run here: no KVM, no namespaces): `api/e2e_tls_test.go`, listed in `.github/level1-tests.env`. P5-M21-02 added `compiler/service_test.go` (`TestAServiceReachesTheTestNetworksAndTheTunnelsButNotTheManagementNetwork`: the set of the accepted interfaces, no management interface or management tunnel, the accept behind the guard and the access rules, the masquerade, nothing without a namespace) and the level 1 test `TestAConnectionNoCaseDecidesReachesAServerInAnotherTestNetworkAndBehindAWireGuardClientOnTheRealKernel` (a server in a second test network and one behind a WireGuard client are reached through a TLS-handled path, each says whom it sees, and the matrix still refuses). The kernel side is observed in CI, not here: the chain and the marks, the redirect inside the
namespace and `SO_ORIGINAL_DST`, the cut by a reset from an unknown connection, the masquerade of a passed-through connection and the faults on the path through `svc0` ran green with `M21_LEVEL1` in run 40196370688 (on `6a0ec5e`, PR #120), 40258339299 (on `aebbae8`, PR #121, the test of P5-M21-02 among them) and 40270141299 (on `eab804c`, PR #121).

Two things seen while building M21 on a machine with a nearly full disk, neither caused by it: `internal/executor` `TestACaptureIsRefusedOutsideTheScopeAndForBadRequests` fails there with "the disk is almost full" (the same on a clean `main`; the executor refuses to start a capture with less than 10 % free), and the M20 test `run: TestADNSStepMakesADNSOverlayOwnedByTheRunAndRemoveTakesItAway` failed once while Playwright ran in parallel (the overlay list was empty; it passed five times alone and in the full package run), whose cause is not known: the Phase 5 audit (2026-10-26) did not reproduce it in 1200 runs under `-race` with a low `GOMAXPROCS` and found no mechanism that removes a run's overlays before its end (`run.sleepUntil` works on absolute deadlines, and the scenario's last step at 10 s is never reached). It is not M21 code and was not changed; a CI failure of it should be looked at as an M20 matter.

### Acceptance map (plan M21 "Tests" and the acceptance of Phase 2)

| Plan M21 | Test |
|---|---|
| `openssl s_client`/`curl` with TLS 1.2 and 1.3: untrusted, expired, wrong-host, self-signed are rejected by a correct client | `TestACorrectClientRejectsEveryCertificateCaseWithTLS12And13OnTheRealKernel`, `TestOpenSSLAndCurlRejectTheCertificatesOfTheCasesOnTheRealKernel` (level 1), `TestACorrectClientRejectsTheCertificateOfEveryCase`, `TestTheCertificateOfEachCaseFailsTheCheckItIsMeantFor` (level 0) |
| A deliberately insecure client is flagged by the check | `TestADeviceThatTakesTheBrokenCertificateIsFlaggedByTheCheckOnTheRealKernel`, `TestADeviceThatCompletesTheHandshakeFailsTheTLSRejectedCheck`, `TestADeviceThatDoesNotVerifyCompletesTheHandshakeAndIsReported` |
| Golden test E11 | `TestE11AnAccessRuleDecidesBeforeTheRedirectIsAccepted` (`e11.golden.txt`), `TestE11AnAccessRuleDecidesBeforeTheRedirectOnTheRealKernel`, `TestE11AnAccessRuleDecidesBeforeAnyRedirect` (domain) |
| A client trusting the test CA passes the "untrusted CA" case | the first case of `TestACorrectClientRejectsEveryCertificateCaseWithTLS12And13OnTheRealKernel` (the client trusts the test CA and rejects it), `untrusted_ca` in `TestTheCertificateOfEachCaseFailsTheCheckItIsMeantFor` |
| Upload and download latency apply to connections to the responder (S16) | `TestUploadAndDownloadLatencyApplyToConnectionsToTheResponderOnTheRealKernel` |
| The responder reads the original destination | `TestACorrectClientRejectsEveryCertificateCaseWithTLS12And13OnTheRealKernel` (the event's `original_dst`), `TestTheServiceNamespaceHoldsTheRedirectToTheResponderOnTheRealKernel` |
| The same cases work for a WireGuard client | `TestTheSameCasesWorkForAWireGuardClientOnTheRealKernel` |
| Scope: transparent redirect, new connections, "cut existing" on activation, reset on removal | `TestOnlyNewConnectionsAreRedirectedAndCutExistingAndRemovalResetOnTheRealKernel`, `TestTheKernelSelectsNewConnectionsOfTheDeviceForTheResponder`, `TestACaseThatCutsExistingConnectionsSelectsEveryPacket`, `TestTheConnectionsOfARemovedCaseAreReset` |
| Scope (P5-M21-02): a connection that no case decides reaches servers in other test networks and behind WireGuard clients, and is still refused where the access rules or the matrix refuse it | `TestAConnectionNoCaseDecidesReachesAServerInAnotherTestNetworkAndBehindAWireGuardClientOnTheRealKernel` (level 1), `TestAServiceReachesTheTestNetworksAndTheTunnelsButNotTheManagementNetwork` and `TestAConnectionNoCaseDecidesIsPassedToAnotherTestNetworkOnlyWhereTheMatrixAllowsIt` (level 0), the golden `forward.golden.txt` |
| Reset on removal covers a passed-through connection that no case selects any more (P5-AUDIT-02) | `TestAPassedThroughConnectionIsResetWhenNoCaseSelectsItAnyMore` |
| A connection to a host behind the network's own downstream router is passed on: the same-bridge accept stands in front of the rules and the matrix, in the verdict and in `explain` (P5-AUDIT-03) | `TestAConnectionNoCaseDecidesIsPassedToAHostBehindTheNetworksOwnRouter`, `TestTrafficRoutedBackIntoItsOwnNetworkIsAllowedBeforeTheRulesAndTheMatrix` |
| Two-port topology: the API alone keeps a passed-through or intercepted connection out of the management network behind the uplink (P5-AUDIT-17) | `TestTheResponderKeepsAConnectionOutOfTheManagementNetworkBehindTheUplinkInTheTwoPortTopology` |
| A batch of handshake reports the API refuses (4xx other than 408 and 429) is dropped and counted, and does not hold back the next (P5-AUDIT-18) | `TestABatchOfReportsTheAPIRefusesIsDroppedAndTheNextOneGoesThrough` |
| Scope: no-SNI fallback | `TestTheCertificateIsForTheNameOfTheClientHelloOrTheFallbacks`, `TestAHostnameDestinationAndTheNameForAConnectionWithoutServerNameComeFromTheGatewaysDNS` |
| Scope: test CA with download, the unknown CA never distributed | `TestTheTestCADownloadIsTheCertificateOnly`, `TestTheDownloadIsTheTestCAsCertificateAndNothingElse` |
| Scope: expected results from `trusts_test_ca`, events per handshake | `TestEveryHandshakeIsAnEventAndCountedForItsOverlay`, `TestTheStreamTellsAHandshakeInWords` |
| Scope: check type "TLS rejected/accepted" | `run/checks_tls_test.go` (all of it), `TestADeviceThatTakesTheBrokenCertificateIsFlaggedByTheCheckOnTheRealKernel` |
| Scope: "TLS broken" profile, TLS scenario step type | `TestTheTLSBrokenProfileActivatesAndCompetesInTheTLSFamily`, `TestTheTLSBrokenProfileResetsTheHandshakeOnTheDefaultPortsOnTheRealKernel`, `TestATLSCaseThatHeldNoConnectionIsAZeroHitWarning` |
| Acceptance: a profile with a `tls` part, custom or "TLS broken", activates and competes in the TLS family; the family is lifted (P2-M11-01) | `TestADeviceLatencyAndATLSBrokenProfileOnTheNetworkCombine`, `TestACustomProfileWithAnInterceptPartIsAvailableAndActivatesLikeOneWithADNSOrTLSPart`, `TestOnlyProfilesWithAPartOfALaterFamilyAreUnavailable` |

## TLS interception and HTTP faults (M22)

Plan §2.8 (interception with inspection), §2.11 (the key log of a capture), §3.1, §3.8 and M22. A TLS case with the case `intercept` hands the selected new connections to
the interception proxy, mitmproxy with an addon of this project in its own container, which terminates the device's TLS with a certificate of the test CA, opens its own
connection to the real server, applies the HTTP rules of the case and reports what it sees; the key log of its connections goes to the captures that run while the device talks.
The step type of the interception is the step `tls` with the case `intercept` (the settings are `intercept`), and a profile with such a `tls` part activates: no part of a
profile waits for a milestone any more.

### How it works

- **The path of a connection.** The kernel side is that of M21: the chain `tls_redirect` selects the new connections of the case (the case's ports, 443 and 8883 by default), the
  service mark routes them unchanged into the service namespace, where the fixed chain redirects them to the responder's port (`SO_ORIGINAL_DST` keeps where the device was
  going). The responder reads the ClientHello (or, for a connection that is no TLS, the first bytes), asks the API for the decision (`Engine.ResolveTLS`: the case by port, server
  name, destination and client, as for the other cases) and, for the case `intercept`, does not answer the connection but hands it over (`tlsresponder/intercept.go`): it opens a
  connection to the proxy (`127.0.0.1:9444`, the loopback of the service namespace, which the proxy's container shares), sends `CONNECT <original destination> HTTP/1.1` with the
  headers `X-Chaosgw-Connection` (an id), `-Client`, `-Overlay`, `-Device` and `-Sni`, waits for the `200`, writes what the device sent first (the ClientHello, which the responder had read) and
  relays both directions. mitmproxy runs in `regular` mode: the CONNECT gives it the destination (the address, as the device meant it) and the headers tell the addon who the
  device is (the connection it accepts comes from the responder, so the device's address is not otherwise known to it). From the first bytes mitmproxy sees TLS, HTTP or a stream that is no HTTP
  and acts accordingly. Its own connection to the server leaves the namespace through `svc0` like the passed-through connections of M21 (towards the uplink, the test networks and the tunnels of the role test, masqueraded on the way out, M21 decision 13).
- **What the responder adds.** The hand-over is a connection a case holds, so *reset on removal* works for it (the device's connection is reset and the proxy's closed when a later configuration
  does not hold the overlay); a connection that the gateway would not have forwarded (the access matrix keeps the device from the server) is reset without asking the proxy
  (`passthrough` is part of the decision of an interception case, too); the responder reports `intercepted` when the proxy took the connection (that is the report that counts the connection for the overlay),
  `proxy_unavailable` when it could not be reached or refused (the connection is reset and counted in `ProxyFailures`) and `server_unreachable` when the proxy answered `502` or `504` to the CONNECT, which is what mitmproxy
  does when its connection to the server fails (the device is reset like by a server that is down, the proxy is not at fault). A connection that says nothing is not handed over. The first bytes of a connection that is no TLS are
  sent to the proxy in one piece (`firstBytes`): mitmproxy decides between HTTP and a raw stream from the first chunk it gets, and a request line that arrives in two pieces is taken for a stream.
- **The control interface** (`ControlHandler`, `127.0.0.1:9445`, the loopback of the namespace only). Two things need the connection the responder holds: the bytes towards the device pass through it (and are TLS, which the
  responder cannot read), so a rule that paces the response asks for it by connection id (`POST /throttle {connection, rate_bps}`, `rate_bps` 0 removes the pacing); and an abort wants a reset (a close of the proxy's connection would reach the device as an
  orderly end), so the proxy asks for it (`POST /abort`). The pacing sends the bytes in pieces of what the rate allows in 50 ms, each piece when the bytes before it and itself have had their time at that rate (`send`), so
  the last byte of a response arrives when it would on a link of that speed and a long response trickles.
- **The addon** (`sidecars/tls-proxy/chaosgw_tlsproxy`, Python, the only Python of the project). `rules.py` is pure (no mitmproxy): the rules, the matching, the decision for one request, the changes. `addon.py` is the mitmproxy
  addon: it follows `GET /internal/tls/config?consumer=proxy` by long poll (`api.py`, standard library only), where the cases of the kind `intercept` are entries with their settings, and applies the rules in the hooks
  `request` (delay, status, block, abort, the request headers, pacing), `responseheaders` (response headers, `Alt-Svc`, streaming), `response` (the body a rule replaces, the report), the WebSocket and TCP hooks; it reports in batches through `Batcher` (an API that is away or failing keeps the batch for another try and the oldest reports are dropped past 5000; a batch the API refuses for its content, a 4xx other than 408 and 429, is dropped with a warning, because kept it would stand in front of every report after it for good; a 413 splits the batch).
  A connection is decided by the case of the overlay the responder names in the CONNECT. While the proxy does not have that case (its first configuration is on its way, or the long poll has not brought a case the responder already decided with) the hook waits for it, up to `CASE_WAIT` (5 s);
  a connection it then still has no case of is refused (decision 12, P5-AUDIT-04): the CONNECT is answered `503` (the responder resets the device and reports `proxy_unavailable`), a request or a stream on a connection whose case went away is killed, the proxy's own connection to the server
  refuses every certificate whatever the installation's policy says, the key lines of the connection are dropped, and the exchange is reported with the error `the case of the connection is not known to the proxy`.
  `launcher.py` is the container's entry point: it waits for the test CA, gives mitmproxy its configuration directory (`mitmproxy-ca.pem` is the key and the certificate in one file) in a tmpfs, starts `mitmdump` with the addon and
  starts it again when the CA files change. Like the DNS proxy and the TLS responder it watches its namespace (`NamespaceWatch`, the check of `dnsproxy.WatchNamespace`, P5-AUDIT-06): every 2 s the service address `169.254.100.2` has to be among the namespace's local addresses (`/proc/net/fib_trie`, which needs no capability);
  gone on three checks in a row, or never there within 60 s, the launcher stops mitmproxy and exits with code 3, and the restart policy brings the container into the namespace of the holder that replaced the old one (Compose's `depends_on ... restart: true` acts on Compose operations only).
  `--no-namespace-check` or `CHAOSGW_NAMESPACE_CHECK=0` turns the check off (the level 0 and level 1 tests, whose namespaces have no service address), `--service-addr` / `CHAOSGW_SERVICE_ADDR` names another address (the smoke test). At its start the launcher warns when the installation's CA file
  (`CHAOSGW_UPSTREAM_CA_FILE`) cannot be read or holds no certificate (P5-AUDIT-26): mitmproxy reads the file only for a connection, so such a proxy looks healthy while every connection verified against it fails. `deploy/compose.tlsproxy.yaml` is the container (no capability, read-only, the two volumes read-only, the namespace of the holder), `deploy/Dockerfile` has mitmproxy in a virtualenv
  of its own (`/opt/chaosgw/tls-proxy/venv`, `MITMPROXY_VERSION`, the same version as in `.devcontainer/Dockerfile` and the level 0 job of `ci.yml`: `tools/ciconfig` checks the three).
- **The rules** (`intercept.http_rules`, the text of `HttpRule` in the spec). They are looked at in the order written; every rule whose `match` (host, path prefix without the query, method) selects the request applies, until a terminal action has acted.
  `block` answers 403 with a short text, `status` the given code with an empty body (neither reaches the server), `abort` resets the connection, `delay` holds the request back (the delays of several rules add up, and a delay before a
  terminal action holds its answer back), `modify` sets or removes (empty value) request and response headers and replaces text in the body of the response (the body is buffered; a compressed body is decoded and encoded again; its response headers are also set on the answer of a later `block` or `status` rule, a `Retry-After` on a 503, and that answer keeps its own `Content-Length`, P5-AUDIT-05),
  `throttle` paces the response to the request. A connection that is no HTTP (MQTT over TLS) is decided by the rules that name neither a path nor a method (`decide_connection`): `block` and `abort` reset it, `delay` holds it back once, `throttle`
  paces it, `status` and `modify` do nothing there; its bytes are counted, not kept. mitmproxy tells HTTP from a stream by the first bytes (`rawtcp`): a stream that starts like a request line (text such as `ping 0`) is taken for HTTP.
  A WebSocket is an HTTP exchange (the handshake, which every rule can act on) and then a connection whose messages are counted. With `block_quic` (on unless the case turns it off) the `Alt-Svc` headers lose their `h3` entries.
  The API validates what the proxy would otherwise meet in a hook: a delay of more than zero and at most ten minutes, a rate of at least 8 bit/s, a method that is a token, at most 64 rules, header names that are tokens, header values without a line break or a NUL byte, and a
  body replacement that searches for something (`domain.validate_fault.go`, `tlsCase`).
- **Reports.** What the device did with the proxy's certificate is a report of `POST /internal/tls/handshakes` with the case `intercept` (`accepted` with the version, `rejected` with the alert, `client_closed`, `timeout`), so the events `tls_handshake`, the log and the checks
  `tls_accepted` and `tls_rejected` work for it as for the responder's cases (a device that trusts the test CA completes the handshake: `accepted`; the expectation of an interception case is `any`, neither is a failure of the device). The API counts for
  the overlay only the reports of the responder (`intercepted`, `proxy_unavailable`, `server_unreachable`), not the proxy's again (`countsForTheOverlay`). Every exchange is a report of `POST /internal/intercept/flows` (method, host, path, status, bytes, duration, the actions that acted, the
  error) that the API keeps (the newest 20000, in memory, like the DNS log) and lists (`GET /intercept/flows`: by device, host, time and whether a rule acted); a rule that acts announces itself with `http_fault_triggered`, at most once per overlay, device and ten seconds.
- **The test CA.** `POST /tls/ca/regenerate` (scope `full`, audit `tls.ca_regenerate`) writes a new test CA (`tlsresponder.RegenerateTestCA`: the key, then the certificate, which is the file the services watch) and returns the certificate; the unknown CA is not touched.
  `loadCA` checks that the certificate belongs to the key, so a service that reads between the two writes keeps what it has and loads the new pair on its next look; the responder reloads within five seconds (`WatchCAs`, which looks at the sizes and times of the certificates and of the keys, so a pair put there in any order is loaded once it is complete), the launcher restarts mitmproxy
  (the connections that are open end, as the devices' trust does).
- **The key log.** mitmproxy writes the secrets of every connection it handles through `mitmproxy.net.tls.log_master_secret`; the addon replaces that with a sink that attributes each line to the device (`KeylogSink`): the second word of a line is the client random, which is
  known for the device's connection from `tls_clienthello` and for the proxy's own connection to the server once its handshake is over (lines that arrive before are held for a minute). The lines go to `POST /internal/intercept/keylog` with the device's address.
  `capturemgr.AppendKeylog` gives them to every running capture whose scope covers the client (`Covers`: a device by its identity or address, a network by its prefixes, a selector by its source, the uplink every client, a WireGuard network of the role management
  none) and writes them to `keylog.txt` next to the packet files; the first line marks the capture `keylog` and `sensitive` (the record is saved), and the download of the log, and of a capture that holds one, needs the scope `full`. A capture that has ended, or is ending, takes no more lines
  (a batch checks the end again before it writes and sets the flags before the first line, and the running record is saved only while the capture is not ending, under the lock of the record's file, so a key log never lands in a capture whose final record does not say so, P5-AUDIT-20);
  nothing is kept when no capture runs (secrets of a connection that no capture saw decrypt nothing); `keylog: false` of the case keeps the secrets of its connections from the captures (the proxy drops those lines, and the API drops a batch whose `overlay` names no interception case in force or one with `keylog: false`). The key log is not part of the quota (the quota counts the packet files the executor
  reports), so it is bounded by itself: a capture keeps at most 16 MiB of it (about ten thousand connections), the batch that does not fit and everything after it are dropped, and the gateway logs that once (`maxKeylogBytes`). The log has the secrets of both sides of the proxy: the device's (decrypting a capture of the device
  or of the network) and its connection to the server (decrypting a capture of the uplink). A connection that began before the capture did cannot be decrypted (its handshake is not in the capture), so its lines are not needed.
- **`block_quic`.** `compiler/tls.go` adds one rule per interception case to the chain `tls_redirect`: the UDP packets to port 443 of the case's clients (and its destination) are dropped, in front of the selection, so a client that tries QUIC meets no answer and falls back to TCP.
  QUIC itself is not intercepted (plan §2.8).
- **Health and capabilities.** The proxy says who it is when it polls (`consumer=proxy`); the health check lists the component `tls_proxy`: `disabled` while the gateway has never seen it poll and no interception case is in force, `degraded` while a case is in force and it has not polled for 60 s (the connections of the case are reset) or while it has stopped polling, `healthy` otherwise.
  `tls.intercept` is in the capabilities. `explain` says `service: tls_proxy` when the winning TLS case is an interception (the responder's flows, in the flows API, stay `tls_responder`: the connection mark does not say which case decided).
- **The upstream** (P5-M22-03, decided 2026-10-25, option (b)). How the proxy treats the certificate of the real server is a field of the case, `intercept.upstream`, with three values: `verify` (the certificate must be valid for the name the device asked for and chain to the bundle of mitmproxy, `certifi`),
  `own_ca` (it must chain to the CA file the installation gave the proxy, `CHAOSGW_UPSTREAM_CA_FILE`, and to no other CA) and `insecure` (the certificate is not verified). The field is not required: a case without it has the installation's policy, which `CHAOSGW_UPSTREAM_INSECURE=1` (`insecure`) and
  `CHAOSGW_UPSTREAM_CA_FILE` (`own_ca`) set for the whole container and which is `verify` where neither is (`rules.installation_policy`; no verification beats a CA file, as in mitmproxy). The two variables are the launcher's `--upstream-insecure` and `--upstream-ca-file` and become the options `ssl_insecure` and `ssl_verify_upstream_trusted_ca` of mitmproxy,
  which its own addon `tlsconfig` applies to every connection. The addon of this project changes what a case's policy differs in (`Addon.tls_start_server`, `rules.upstream_plan`): the hook runs before mitmproxy's own in the chain, so it calls the `tls_start_server` of `tlsconfig` first (which then finds the connection made and leaves it),
  and replaces the connection's trust store (`Connection.set_context` with a client context that only differs in what it trusts, kept per CA file and its time; the secrets of the connection still go to the key log, because OpenSSL keeps that callback with the session context, which stays mitmproxy's) and its verification mode (`SSL_set_verify`). The versions, ciphers, host name and ALPN that mitmproxy set on the
  connection are those of mitmproxy. A case whose policy is the installation's is left to mitmproxy altogether. A server whose certificate is refused is a failed connection to the server: the proxy reports an exchange of the protocol `tcp` with the error `TLS to the server failed: Certificate verify failed: <reason>` (self-signed certificate, unable to get local issuer certificate, hostname mismatch, certificate has expired),
  mitmproxy tells the device with a `502 Bad Gateway` page over the proxy's own certificate that names the reason (the device's handshake with the proxy succeeded, so it is `accepted`), the exchange of the request is reported too (no status, the error), and the server sees no request. `docs/install.md` has the operator's side.

### Decisions

Taken by best practice where the plan leaves room (what needs the maintainer is under "Phase 5 open items" in `open-items.md`):

1. **The responder decides, the proxy follows.** The alternative, a second redirect inside the namespace that sends the interception cases' connections to mitmproxy's transparent mode, would need a way to tell the connections apart there (the marks do not cross the veth pair) and
   would make the proxy decide by server name by itself. The hand-over reuses everything the responder already has (the decision per connection by server name, the access check, reset on removal, the reports) and needs no change in the kernel's rules; its price is one more hop on the loopback and a CONNECT that carries the device's
   identity. mitmproxy's transparent mode of the S4 spike is not used.
2. **The device's identity travels in the CONNECT request.** The alternatives (a SOCKS5 username, a side channel by source port) need more of mitmproxy's internals; the headers of the request arrive in the hook `http_connect`, a documented place, and are the same for every protocol the tunnel carries.
3. **Delay holds the request, not the response.** The device sees the response that much later in both cases, and a delay before a synthesized answer (`status`, `block`) needs the same code; the server sees the request that much later, too, which a delay of the response alone would not do. The plan says "delay or throttle responses": for the device it is that.
4. **Throttle is the responder's.** mitmproxy streams through callables that cannot wait, so a pacing inside it would stop every connection; the responder sees the bytes of the connection and owns the pacing, and the proxy asks for it per request: the rate holds for the response to the request the rule matched, the next request sets its own (none when no throttle rule matches it). On a multiplexed HTTP/2 connection the streams share it.
5. **An abort is a reset made by the responder.** The proxy closing its side would reach the device as an orderly end; the report of the exchange is made before the abort, because the handling of the flow ends with the connection.
6. **The expectation of an interception case is `any`.** A device that trusts the test CA completes the handshake and one that does not rejects the certificate: both are what the firmware is, not a failure. The case makes the check `tls_accepted` meaningful for a development firmware (it must trust the CA) and `tls_rejected` for a production one.
7. **The proxy reports its handshakes to the responder's endpoint.** One log, one event type and the same checks; the API counts a connection once (the responder's report). A new endpoint for the same data would have needed a second source for the checks.
8. **The key log is given to the gateway, not read from a file.** A file in a volume would need a second place the API reads from, and the attribution to a device (a capture of device A must not hold the secrets of device B's connections) is only possible in the addon, which knows the connection. The batches are small and go over the link the addon has anyway.
9. **No capture, no log.** The gateway does not store secrets that decrypt nothing; the cost is that a capture started after a connection began cannot decrypt it, which it could not anyway.
10. **The log of the exchanges is in memory.** Like the DNS log and the handshake log: it is for a test in progress; the report of a run keeps what it needs (the counters and the events). A run's check on HTTP exchanges is not part of M22 (the scope names the checks `tls_*` only).
11. **Responses are streamed unless a rule needs the body whole.** mitmproxy reads a body whole before it passes it on, unless the addon streams it, and a proxy that holds a response back changes what the device sees (a server-sent event or a long poll would never arrive,
   a firmware image would cost its size in the proxy's memory). The addon streams the body of every response (`responseheaders` sets a callable that counts the bytes) except one whose request matched a rule with `response_body_replace`, which needs the text and is buffered, and a protocol
   switch (101). The changes of the rules to the headers are made in that same hook, before the body goes on. The body of a *request* is buffered (the rules decide on the whole request, and a delay or a block must happen before anything reaches the server).
12. **The proxy is optional and says so.** Its absence is a health state, `proxy_unavailable` reports and a reset: never a silent pass-through, which would let a device reach a server that a case meant to be between.
   The same holds inside the proxy (P5-AUDIT-04): a connection whose case the proxy does not know, after a bounded wait for its configuration, is refused, not passed without the rules of its case or with the installation's policy for the servers' certificates.
13. **Python only here.** `sidecars/tls-proxy` is the only Python of the project, as plan §3.1 says; the rules are a pure module so that their tests need no mitmproxy, and the rest of the tests start the real one.
14. **The policy for the servers' certificates is `verify`, `own_ca` or `insecure` per case, the installation's two variables are its default (P5-M22-03).** The maintainer decided the three values and the default on 2026-10-25; what the decision leaves open is settled here. *The own CA* is the CA file of the installation (`CHAOSGW_UPSTREAM_CA_FILE`: a lab's private CA, which is what a
   user who runs private servers has and the bundle does not): a case chooses between the bundle, that file and no check, per device, and the file itself stays an operator's matter (a volume of the container). It is not the test CA, which signs what the device is shown and no server's certificate; trusting it upstream would let the proxy accept a certificate that the gateway itself made. A case that asks for `own_ca`
   where the installation has no file is refused, not served: failing open would give the device a connection to a server nobody checked (the proxy logs it once per connection, and the error of the exchange names it). *No default in the spec*: an omitted `upstream` means the installation's policy, which is the point of keeping the variables; a value that this version does not know (a newer gateway) is read as omitted.
   *`insecure` of a case does not set `OP_LEGACY_SERVER_CONNECT`*, which `CHAOSGW_UPSTREAM_INSECURE=1` does through mitmproxy's `ssl_insecure` (a server that renegotiates the old way): the policy is about the certificate. *Where the policy is applied*: a user addon is after mitmproxy's own in the chain for most hooks, but this addon is loaded before `tlsconfig` (found by the first test: the connection was not made yet when
   the hook ran), so the hook makes the connection itself by calling that of `tlsconfig` and changes only the verification, instead of copying the 50 lines in which mitmproxy builds a connection (the SNI and the ALPN, the verification of the host name, the client certificates), which are tied to its version. A failure of the hook ends in a refusal, never in the policy of the installation. *The answer to the device* is mitmproxy's: a 502 page that
   names the reason, over the proxy's certificate; making it a reset (as for a server that is down) would hide from the device's logs why, and the exchange log has the reason either way.

### Tests

Level 0, all run here (the tests that need mitmproxy run with an interpreter that has it: `CHAOSGW_MITM_PYTHON`, `/opt/mitm-venv/bin/python` in the testbed image or the one beside a `mitmdump` on the `PATH`; without one they are skipped, and `CHAOSGW_REQUIRE_MITMPROXY=1`, which the level 0 job of CI sets, turns the skip into a failure):

- `tlsresponder/mitm_test.go` runs the real mitmproxy with the addon, the real responder, a fake API (this test) and a Go client that trusts the test CA: modified responses (status, block, the headers and the body, the requests a rule answers do not reach the server), delays (timed, added up, before a terminal action), connection reuse over HTTP/1.1 and HTTP/2
  (one hand-over, one handshake, an exchange each), the key log (every line of the device's own log is in what the proxy gave, and more), `keylog: false`, a device that does not trust the CA, throttle (paced and then not paced), abort (a reset), a connection that is no HTTP (counted, aborted by a host rule), WebSocket (messages counted, a handshake blocked),
  plain HTTP on a port of the case, a new test CA (the launcher restarts mitmproxy), a server that is down, a connection without a server name.
- `tlsresponder/intercept_test.go` with a fake proxy: the CONNECT and its headers, the replay of the first bytes, a connection that is no TLS handed over in one piece, no proxy / a proxy that refuses / one that is not configured / a server that is down, a connection the gateway would not forward, silence, reset on removal, the control interface (pacing, abort, bad requests, ended connections), nothing kept after a connection ends. `ca_test.go`: the regeneration, the pair check, the watcher between the two writes.
- `sidecars/tls-proxy/tests` (run by `tlsresponder.TestThePythonTestsOfTheSidecarPass` and with `python3 -m unittest discover -s tests -t .`): durations, bit rates, host patterns, the decision for a request and for a stream, the changes, `Alt-Svc`, the classification of mitmproxy's failure text.
- `tlsresponder/mitm_headers_test.go` (the request headers of a `modify` rule reach the server, the `Alt-Svc` header of a response with `block_quic` and without it, devices of TLS 1.2 and TLS 1.3 with their secrets, a response that is not over reaches the device while the server is still sending), `sidecars/tls-proxy/tests/test_batcher.py` (what the batches do with an API that is away, that refuses or that takes less), `compiler/tls_test.go` (the selection and the drop of QUIC, the option off, the destination, the transaction is accepted by nft's parser: `scenarioIntercept`), `domain` (the validation of the rules: the limits, a method, a rate; every action with its field; the profile part), `engine/tls_test.go` (the snapshot, the kernel's chain, the profile, passthrough, `explain`),
  `run/checks_tls_test.go` (the interception step), `api/intercept_test.go` (the configuration for the proxy and the health, the decision and passthrough, the handshakes as events and their counting, the log with its filters and pages, the event of a rule, validation and the service scope, the CA regeneration, the key log: which captures take it, the scope `full`, a capture that ended; **the key log decrypts the capture of a run, read with tshark**),
  `capturemgr/keylog_test.go` (which clients a scope covers, the log and the flags, the bound of the log), `executor/hardening_test.go` (the container, the addresses the responder and the proxy share, the tmpfs that the proxy's user owns, the image's virtualenv, the one mitmproxy version), `tools/ciconfig` (the version in three places, every test of the level 1 lists exists, the smoke job of the container). Web: `lib/faults.test.ts` (the chips of an interception case).
- The policy for the servers' certificates (P5-M22-03). `tlsresponder/mitm_upstream_test.go` (the real mitmproxy; the rig's server has a certificate for `example.com` that no CA of the bundle signed, and the rig is started with the installation's side of the policy: a CA file with the server's certificate, one that has not, none, or `CHAOSGW_UPSTREAM_INSECURE=1`): a server whose certificate is not valid is refused by default, with `verify` and with `own_ca` where the installation has no file (the 502, the two reports, the reason,
  and the server saw no request) and served with `insecure`; `own_ca` is the installation's file and no other CA; `verify` asks for the bundle where the installation names a file or verifies nothing; a certificate for another name is refused by every policy but `insecure`; several cases with different policies at once, decided by their overlay; a new policy takes effect for the next connection without a restart, and an open connection is not touched; the secrets of the proxy's own connection reach the key log when the case changed its verification; a CA file that cannot be read when a case asks for `own_ca` (`TestACaseWhoseCAFileCannotBeReadIsRefusedAndNeverServedUnverified`: the file is removed while the proxy runs) is a refusal that says the policy could not be applied, never a connection nobody verified, and the other policies and the file coming back are not affected.
  `sidecars/tls-proxy/tests` (`UpstreamPolicyTests`: the three values, the installation's policy from the two variables, what a case changes and what it leaves to mitmproxy, `own_ca` without a file; `ConfigTests`: the policy of each case by overlay, a value that is not known is none), `domain/validate_fault_test.go` (every value is accepted, none is accepted, an unknown value and the field on a certificate case are refused), `api/intercept_test.go` `TestTheUpstreamPolicyOfACaseReachesTheProxyAndAnInvalidOneIsRefused` (the settings the proxy fetches carry the policy, one overlay per policy; the refusals).
- A connection whose case the proxy does not know (P5-AUDIT-04): `tlsresponder/mitm_unknown_test.go` (the real mitmproxy on an installation that verifies nothing and a server whose certificate no CA signed: a connection of an overlay that is not in the fake API's configuration is refused, never reaches the server and is reported with the reason, and no secret leaves the proxy; a connection whose case arrives a second later waits for it and is served),
  `sidecars/tls-proxy/tests/test_addon.py` (`UnknownCaseTests`: the CONNECT answered `503` with nothing loaded and with an unknown overlay, a case that arrives while the CONNECT waits, a request and a stream killed and reported, the key lines dropped for an unknown case and for `keylog: false`, the server's certificate refused for an unknown case on an installation that verifies nothing),
  `api/intercept_test.go` `TestAKeyLogBatchOfACaseWithoutKeyLogOrOfNoCaseReachesNoCapture`. The response headers of a `modify` rule on a `status` and a `block` answer (P5-AUDIT-05): `tlsresponder/mitm_headers_test.go` `TestTheResponseHeadersOfAModifyRuleAreOnTheAnswerOfAStatusOrBlockRule` and `test_addon.py` `ResponseHeaderTests`.
  The namespace watch and the CA file of the launcher (P5-AUDIT-06, -26): `sidecars/tls-proxy/tests/test_launcher.py` (an address that goes away three times in a row, a short absence and a failed read, an address that never appears, the local addresses of `/proc/net/fib_trie`, exit code 3, the check on by default; a CA file that is missing or holds no certificate is warned of) and `deploy/smoke-tlsproxy.sh`
  (the container gets a network whose address stands in for the service address and a stand-in for the API with one case: it carries a request of that case, refuses one of an unknown overlay, leaves the namespace when the network is disconnected, comes back through the restart policy and carries a request again).
  A key line during the end of a capture (P5-AUDIT-20): `capturemgr/keylog_test.go` `TestACaptureThatEndsWhileABatchOfKeyLinesIsOnItsWayTakesNone` and `TestKeyLinesThatArriveWhileACaptureEndsNeverLeaveItRunningOnDisk`. The real-mitmproxy tests wait for the proxy to have the configuration (`waitApplied`) instead of a fixed time (P5-AUDIT-21).
- An abort over HTTP/2 (`tlsresponder/mitm_h2abort_test.go`, the real mitmproxy): the request that matches the rule fails, so does a request in flight on the same connection (the abort is a reset of the connection, as for HTTP/1.1), the exchange is reported with `aborted by a rule`, and the next connection is HTTP/2 again and served.

Level 1 (written, vetted with `-tags testbed`, run by CI with `$M22_LEVEL1`, not run here: no KVM, no namespaces): `api/e2e_intercept_test.go`, listed in `.github/level1-tests.env`: modified responses, delays and a reused connection through the real mitmproxy in the service namespace seen by `curl` from a client of the testbed network and in `GET /intercept/flows`;
`block_quic` on UDP 443 (dropped for the client of the case, not for another client or port, back when the case goes or turns it off); the checks `tls_accepted` and `tls_rejected` of runs with an interception step; the key log with a capture of the real executor read by tshark; the reset when the proxy is not there. The level 0 tests with the real mitmproxy are in the same list (`M22_LEVEL1`) so that a testbed image without mitmproxy cannot skip them; the level 1 runs take only
packages that have a test file under the build tag `testbed`, so `tlsresponder/level1_test.go` (it checks that the image has mitmproxy) is what makes them run the package.
Three level 1 tests were added with P5-M22-03 and the open points of the milestone (all in `api/e2e_intercept_test.go`, all in `M22_LEVEL1`, written and vetted here, not run here): `TestTheUpstreamPolicyOfACaseDecidesWhetherTheProxyAcceptsTheServersCertificateOnTheRealKernel` (the proxy of the bed has the CA of the real server as its installation's file, which is no CA of the bundle: a case without a policy, `own_ca` and `insecure` are served, `verify` is a 502 and the log says why; the cases are replaced while the proxy runs),
`TestAQUICClientFallsBackToTCPAndIsInterceptedOnTheRealKernel` (a client with a real QUIC stack, aioquic, which mitmproxy brings: without a case its Initial packets reach the server and it falls back to TCP, which nothing intercepts; with a case that blocks QUIC no datagram arrives, its own timeout ends the attempt and the TCP connection is the one the proxy serves with the modified response) and
`TestAnInterceptCaseReachesAServerInAnotherTestNetworkAndBehindAWireGuardClientOnTheRealKernel` (P5-M21-02 with the proxy: the device is served with the modified response, the server sees the gateway's address in its network and not the device's, the exchange is in the log with the address the device meant, and the matrix keeps the device from both networks).
The kernel side (the drop of UDP 443, the hand-over inside the namespace, the proxy's connection through `svc0` and the masquerade) is verified by CI only until its run is green.
What these tests cannot show: the bed has one real server, so the level 1 test of the upstream policy cannot tell `own_ca` from `insecure` (both serve it); the level 0 tests, which make the rig's server with a certificate that a CA file does or does not hold, are what pin that difference, and the level 1 test proves that each policy that serves is handed over and `verify` is refused on the real kernel.

**The container.** `deploy/smoke-tlsproxy.sh` starts the container from `deploy/compose.tlsproxy.yaml` (with an override that gives it a network of its own and bind mounts with a CA and a token made for the test, so that only Docker is needed), waits for the healthcheck of the compose file, checks that mitmproxy loaded the addon and listens on 127.0.0.1:9444 and that the log shows no error, and sends a request through the proxy
(a `CONNECT` with the responder's headers, then plain HTTP in the tunnel to a server of the container). The CI job `tlsproxy-image` builds the image and runs it; `tools/ciconfig` checks that the job, the script, the compose file and the Dockerfile agree on the virtualenv and the mounted directories. The image's build was already run by the jobs `arm64` and `mounts` (run 40196370688 on the branch `phase-5`: green; no job started the container, which the new job does). The proxy has no HTTP endpoint of its own: its health is that its
port accepts a connection (the healthcheck of the compose file) and, for the gateway, that it polls (the component `tls_proxy`). The job ran in CI run 40258339299 on `aebbae8` and is green (it was written where no Docker was at hand). Reading the compose file and the image for what a first start would show found one thing, which is fixed: a tmpfs belongs to root, the proxy runs as 65532 and makes its configuration directory in `/tmp`, so the tmpfs of the compose file has `uid=65532,gid=65532` now (pinned by `TestTLSProxyContainerHardeningProfile`). Whatever the first run shows beyond that is for the next change to fix.
The nightly workflow has no job that builds the image (the jobs that do, `arm64` and `mounts`, are in `ci.yml` only), so the job is not repeated there.

### Acceptance map (plan M22 "Tests")

| Plan M22 | Test |
|---|---|
| A client trusting the CA sees the modified responses | `TestADeviceTrustingTheTestCASeesTheModifiedResponses` (level 0, real mitmproxy), `TestADeviceTrustingTheTestCASeesTheModifiedResponsesAndDelaysOnTheRealKernel` (level 1) |
| Timing of delayed responses | `TestADelayedResponseArrivesAfterTheDelay`, the `time_total` of the level 1 test |
| QUIC fallback | `TestBlockQUICDropsUDP443OfTheClientsOfTheCaseOnTheRealKernel` (the drop that forces the fallback), `TestAnInterceptionCaseSelectsItsConnectionsAndDropsQUICOfTheSameClients`, `TestBlockQUICIsOffWhenTheCaseSaysSoAndForTheOtherCases` (the rule), the `Alt-Svc` test in `test_rules.py` |
| Connection reuse across requests | `TestConnectionReuseAcrossRequestsIsOneHandoverAndManyExchanges` (HTTP/1.1 and HTTP/2), the `num_connects` of the level 1 test |
| The key log decrypts the run's capture | `TestTheKeyLogDecryptsTheCaptureOfARun` (tshark, level 0), `TestTheKeyLogDecryptsTheCaptureOfARealConnectionOnTheRealKernel` (level 1), `TestTheKeyLogHoldsTheSecretsOfTheDevicesConnection` (the proxy's lines against the device's own) |
| Scope: mitmproxy sidecar management and control from the core | the launcher and `deploy/compose.tlsproxy.yaml` (`TestTLSProxyContainerHardeningProfile`), `GET /internal/tls/config?consumer=proxy`, the health component `tls_proxy`, the control interface (`TestTheControlInterface...`) |
| Scope: test CA management (generate, download) | `TestTheTestCAIsReplacedOnRequestAndNothingElseOfTheCAsChanges`, `TestTheTestCAIsReplacedAndTheUnknownCAIsNot`, `TestANewTestCAIsUsedByTheProxyAfterItsFilesChange`; the download is M21's |
| Scope: HTTP(S)/WebSocket inspection | `TestTheExchangesOfTheProxyAreLoggedListedAndAnnouncedWhenARuleActs`, `TestAWebSocketIsInspectedAndItsHandshakeCanBeBlocked`, `TestAConnectionThatIsNoHTTPIsCountedAndAbortedByAHostRule` |
| Scope: URL blocking, error codes, delay/throttle, modification, connection abort | the tests of the first row, `TestAThrottleRulePacesTheResponseOfTheRequestItMatches`, `TestAnAbortRuleResetsTheConnectionOfTheDevice` |
| Scope: key log for captures | `TestTheKeyLogGoesToTheRunningCapturesThatCoverTheDeviceOnly`, `TestACaptureKeepsTheKeyLogItWasGivenWhileItRan` |
| Scope: block-UDP-443 option | the compiler tests of the QUIC row, the level 1 test |
| P5-M22-03: a server whose certificate is not valid is refused by default and passes with "do not verify" | `TestAServerWhoseCertificateIsNotValidIsRefusedByDefaultAndServedWithInsecure`, `TestAServerWhoseCertificateIsForAnotherNameIsRefusedUnlessTheCaseSaysInsecure` (level 0, real mitmproxy), `TestTheUpstreamPolicyOfACaseDecidesWhetherTheProxyAcceptsTheServersCertificateOnTheRealKernel` (level 1) |
| P5-M22-03: the other values of `upstream` each have a test | `verify`: `TestACaseCanAskForTheBundleWhereTheInstallationNamesACAFile`, `TestTheInstallationThatVerifiesNothingIsOverriddenByACase`; `own_ca`: `TestOwnCAIsTheCAFileOfTheInstallationAndNoOtherCA`, `TestOwnCAOfAnInstallationThatVerifiesNothingIsTheCAFileItNames`; the installation's default: the same tests and `TestCasesWithDifferentPoliciesAreInForceAtTheSameTime`; the spec, the settings and the validation: `TestTheUpstreamPolicyOfACaseReachesTheProxyAndAnInvalidOneIsRefused`, `TestEveryUpstreamPolicyOfAnInterceptionCaseIsAccepted`, `UpstreamPolicyTests` |
| P5-M21-02: an `intercept` case to a server in another test network and behind a WireGuard client | `TestAnInterceptCaseReachesAServerInAnotherTestNetworkAndBehindAWireGuardClientOnTheRealKernel` (level 1; the path is M21's) |
| QUIC fallback, the part that was untested: a client that falls back to TCP | `TestAQUICClientFallsBackToTCPAndIsInterceptedOnTheRealKernel` (level 1; a real QUIC stack as the client; no browser or curl with HTTP/3 in the testbed image) |
| An abort over HTTP/2, untested before | `TestAnAbortRuleOverHTTP2ResetsTheConnectionAndTheRequestsInFlightOnIt` (level 0, real mitmproxy) |
| The image with mitmproxy builds and the container starts | the CI job `tlsproxy-image` with `deploy/smoke-tlsproxy.sh`; the build was run by the jobs `arm64` and `mounts` |
| Scope: interception scenario step type | `TestAnInterceptionStepWritesTheOverlayWithItsRulesAndCountsConnections`, `TestAnInterceptionStepOfARunIsSeenByTheTLSChecksOnTheRealKernel` |

## DHCP test actions (M23)

Plan §2.4 (the DHCP family), §2.7 (test actions) and M23. DHCP is a test instrument as well as infrastructure: five actions change what Kea does, for a
network or for one device, as overlays (`POST /overlays` with `dhcp`, or the step type `dhcp`), with an owner, a TTL and a lease like every other overlay. They are
`short_lease` (the lease time, below 120 s if you like), `delete_lease` (acts once), `force_new_ip` (the device's reservation moves, its renewal is refused),
`set_options` (router, DNS servers, domain, NTP servers, custom options) and `silence` (Kea does not answer). The server cannot make a client renew at once (plan §2.7
and risk 11), so every test of an action that a client notices at its renewal uses a short lease: the lease time says when the client comes back.

### How it works

- **Resolution** (`domain/dhcp.go`). The family competes like the others (§2.4): the more specific scope wins, a device (level 4) over its network (level 8), and the newer overlay wins
  at the same scope, so a device has one effective action and an action of the network does not apply to a device with an action of its own. `World.DHCPActions` gives the
  winner of every network and every device an action names; `delete_lease` is no state and is not among them (`World.DHCPDeletions`). The scopes are a device and a
  network (`invalid_target` for a group, the global scope or a remote network), and `force_new_ip` takes a device only: a reservation is one device's.
- **Kea** (`compiler/dhcpaction.go`, laid over the configuration `compileKea` made, after the faults are compiled). The target's Kea configuration carries the effects, so
  `config-set` is all the engine does, and every apply sends the configuration with the actions in force (and without those whose overlay went). The actions of a device follow
  where it is seen (below), so an update of the identity alone, which the apply loop applies incrementally, sends the configuration as well when the DHCP part of the target changed
  (`dhcpChanged` in `applyloop.go`, P5-AUDIT-27):

  | Action | Network | Device |
  |---|---|---|
  | `short_lease` | the subnet's `valid-lifetime`, with `min-valid-lifetime` and `max-valid-lifetime` set to it | a client class (`cgw-lease-<s>-<min>-<max>`, one per lease time, the MACs of its devices) with the same three values: Kea applies the class before the subnet |
  | `set_options` | the subnet's options with the fields of the action replaced | the device's host reservation (no address of its own unless it has one) with the configured options and the action's: Kea applies a reservation before the subnet |
  | `silence` | the class `DROP` with the test `pkt.iface == '<bridge>'`, less the devices an action of their own takes out | the class `DROP` with `pkt4.mac == 0x...` for every MAC of the device |
  | `force_new_ip` | (refused) | the reservation of the device's first MAC (§2.7) gets the address of the overlay; the device's `fixed_ip` is replaced while the overlay lasts |
  | `delete_lease` | `Target.Kea.Deletions`: every lease of the subnet | the same with the device's MACs |

  What the network's action changed and the device's does not is put back for the device (the device's action wins, §2.4): a device with `set_options` while its network has a
  short lease gets a lease class with the configured lease time, a device with `short_lease` or `force_new_ip` while its network has `set_options` a reservation with the
  configured options, and a device with another action than `silence` is an exception of the network's DROP. `kea.Config` has `LeaseClasses`, `Silenced` (one `DROP` class of all
  of them), reservations with options and no address, and the lease bounds of a subnet; Kea does not take an empty `client-classes` list, so the key is left out without classes.
- **The address of `force_new_ip`** (`engine/dhcpaction.go`). The compiler needs a stable address (a compile of every apply must give the same reservation, and the lease the device
  takes must not change it), so the engine chooses it when the action is written and the overlay keeps it as `dhcp.ip`. A request may name one (inside the device's network, not
  the gateway's, the network's or the broadcast address, and not one another host holds); without one the engine takes the last address of the pools, going down, that is not taken: leased
  (whoever holds the lease), held by a known device, the `fixed_ip` of a device, or the address of another overlay of the kind (read from the store, so the writes of one batch
  get different addresses, P5-AUDIT-28). The DHCP server allocates from the start of a pool,
  so the address a device is moved to is the one it takes last. The write is refused when the device has no MAC address (a configured device's MACs come from the configuration, so
  a device that was not seen yet works), its network has no DHCP, or the pools are full (`invalid_dhcp_action` at `/target` or `/dhcp/ip`). Writing the action again chooses again:
  while the device has not taken the address it is the same one, and once it holds it the device is moved on.
- **`delete_lease`** acts when it is written and stays in the list until its TTL or its owner removes it. The compiler lists it in `Target.Kea.Deletions` with a key of the overlay's
  id and the time it was written (a new write is a new key), and the engine does the deletions right after Kea took the configuration (`Engine.deleteLeases`, in `syncDHCP`, so the
  answer of a write comes after the deletion): `lease4-wipe` for a network, the leases of the device's MACs (`lease4-get-page` and `lease4-del`) for a device. A key is done once,
  and the engine remembers it while its overlay exists, also while the target leaves it out (its network has DHCP off for a while, P5-AUDIT-30); it is forgotten when the overlay is
  gone and the target the server is given no longer lists it (`Engine.forgetLeaseDeletions`). A deletion Kea could not do is a `DHCPError` of the snapshot ("the DHCP server could not delete leases") and is tried again with
  the configuration every five seconds, so a Kea that was down when the action was written does it when it is back, while the overlay exists. The event `dhcp_leases_deleted` (overlay,
  network, device, count) says what was done. A client that renews gets the same address again (spike S6); the action is for the test of what a device does without its lease, not of a new address.
- **The state of the overlay.** `effective` while Kea is configured with it (a deletion: once it is listed), `overridden` with `overridden_by` while a newer action of its scope wins, `disabled` while
  nothing could use it: its network has DHCP off, or its device has no MAC address the server could know it by, or no network (the compile says so with the warning `dhcp_action_inactive`).
  The network of a device is the one it names, else the one it is seen in (P5-AUDIT-07): for a configured device without `network` the compiler takes the network its current addresses lie
  in, the newest lease first (`compiler.configuredNetwork`), which is the network of `DeviceState.Network` (its lease's, else its address's) that the engine's choice of the address of
  `force_new_ip` takes, and a discovered device's is the one it was seen in. The
  winner of a scope is known from the overlays, not from the traffic of the devices, so a device that was not seen yet overrides as well.
- **The rest.** `dhcp` is among the overlay kinds, the fault families and (as `dhcp.actions`) the features of the capabilities, and `dhcp` among the step types: a step creates the overlay of the run
  like any other (`domain.StepRequest`), `force_new_ip` of a device of a network without DHCP ends the run as `error` with the reason, and a `restore` or the end of the run removes the actions.
  The actions have no counter, so a `dhcp` step is never a zero hit. The UI names an action in its chips ("DHCP short lease 7s", "DHCP new IP 10.10.0.200", "DHCP options · router, DNS"), refreshes the
  devices and networks on `dhcp_leases_deleted` and shows the event in the recent events (P5-M23-01 is the form).

### Decisions

Taken by best practice where the plan leaves room (the one that needed the maintainer is `P5-M23-01` in `open-items.md`, decided 2026-10-25: the form is M23b):

1. **The actions compete, they do not combine.** The overlay key of a DHCP action is its `action` (§2.15), so a short lease and a silence of one device are two overlays, and the domain's precedence (a device beats its network, the
   newer wins at one scope; `TestDHCPActionsOnADeviceBeatThoseOnTheNetwork` is the test of the resolution from Phase 2) says one of them is in force for a device. Layering them as Kea does by itself would make a state that the resolution (`explain`,
   `overridden_by`) cannot describe. A device that needs two changes at once is not served by this; `set_options` carries all the options of one action, and a lease time of its own is a second action of the same device that would replace it.
2. **`force_new_ip` is a device's action.** The plan says "change the reservation", and a reservation is a device's (a network has leases, not reservations); a network-wide move would be one reservation per lease, chosen at the
   time of the write, which a device that comes later would not get. `ip` is optional on write (the gateway chooses) so a test that needs a known address can name it, and always there when the overlay is read.
3. **The address is chosen when the action is written and kept in the overlay.** Derived at every compile it would follow the leases (the lease the device takes would change the address, and the next apply would
   move it again). The cost is one more field of the API, `dhcp.ip`, which also tells the tester where the device is going.
4. **The reservation is for the first MAC, and it replaces the `fixed_ip` while the overlay lasts.** As §2.7 says for `fixed_ip`; Kea refuses two reservations for one client in a subnet, so the action takes the place of the configured one and the configured one is back
   when the overlay goes (the client is refused once more and goes back to its address at its next renewal).
5. **A short lease sets the bounds as well.** A client that asks for a lease time (option 51) is given it within `min-valid-lifetime` and `max-valid-lifetime`, which a subnet keeps at 5 s and an hour (or the lease time, if that is more): a client that asks for an hour
   would get the hour from a subnet whose lease time is seven seconds. `TestAShortLeaseOfASubnetIsWhatEveryClientGetsEvenOneThatAsksForAnHour` shows it with the real Kea.
6. **Silence is Kea's class `DROP`.** The packet is dropped before a subnet is selected, so the client gets no offer, no acknowledgement and no NAK, whichever message it sends (`TestSilencedClientsAreNotAnsweredAndTheOthersAre` sends a
   discover, a renewal and a restart); the class is exact and does not depend on how Kea answers a packet that no subnet fits. A network is silenced by the interface its packets come in on (`pkt.iface`, Kea 2.x and later, the version the image pins is 3.0.3) and a device by its MAC addresses (`pkt4.mac`).
7. **An action that nothing can use is accepted and says so.** A network with DHCP off, or a device that has no MAC (a WireGuard client, a probe), is a valid target: the configuration can change under the overlay (DHCP is switched on, the device
   gets a MAC), so the overlay is `disabled` with the warning and starts to act when it can. `force_new_ip` is the exception, since its address cannot be chosen without a network that serves DHCP, so it is refused at the write.
8. **`set_options` replaces fields.** A field the action names replaces the configured one; an empty list of servers sets nothing (as in the scope of a network, where an empty list leaves the default); `domain` set to an empty string removes it. A device's options
   are the configured ones with its own, not the network's overlay (decision 1), with the limit of Kea's merge: a reservation overrides the options it lists, and the subnet's
   other options stay. An option that the network's action sets and the configuration does not have (the domain, NTP servers) therefore reaches the device as well; one the
   configuration has (the router, the DNS servers) is put back by the reservation. A reservation cannot take an option away that the subnet hands out, and the real Kea
   test of M23 (`TestSetOptionsChangeWhatTheClientIsToldOnTheRealKernel`) pins this.
9. **A deletion is done once per write.** The overlay stays in the list, and the key (overlay id and the time of the write) makes the next apply skip it, a rewrite do it again, and a Kea that restarted not repeat it. It is tried again while it failed and the overlay exists;
   if that is too long for a use, the TTL of the overlay is the bound.
10. **The tests talk DHCP to the real Kea on the loopback.** `internal/kea/dhcpwire_testbed_test.go` makes the test a relay agent at 127.0.0.2 (`giaddr`), so Kea, started with UDP sockets on `lo`, selects its subnet by the relay's address and answers on port 67; the offer, the
    acknowledgement, the NAK and the silence are read off the packets. It needs root and Kea (no namespace), so it runs here and in the testbed jobs, and pins what no mock could: the lease classes, `DROP` with an interface test, the options of a reservation and the NAK.
11. **Kea has the last word on an action when it is written.** The state owner asks the server's own check (`config-test`, as the preview of a revision does) about the configuration an action makes, and refuses the write
    with `validation_failed` when Kea refuses it: an option whose data Kea cannot parse, or a lease time it does not take. Without this the write would be accepted, the server would keep the configuration it has, and the retry would
    send the same one every five seconds, so nothing that comes later (another action, a network) would reach the server until the overlay went. Only a refusal of the server counts (`*kea.Error`): a server that is down takes the
    configuration when it is back, and when the server refuses the configuration without the DHCP actions as well (an interface it has not detected yet, #64) the action is not the cause, so it is accepted. The check runs only
    while a DHCP action exists, inside the dry compile of the write (`owner.dhcpProblems`), with a timeout of three seconds.

### Tests

Level 0, all run here: `kea/kea_test.go` (the rendering of lease classes, `DROP`, bounds and option reservations, the refusals of the checks, and Kea's own `-t` accepting them), `domain/overlay_test.go` and `validate_scenario_test.go` (the rules of the action's fields, `ip`, the targets),
`compiler/dhcpaction_test.go` (each action for a network and a device, a discovered device, the combinations in which the device's action beats the network's and what is put back, the newer action of a scope, actions that reach nobody, the deletions and their keys, the document is deterministic, Kea's check, a configured device without `network` in the network it is seen in),
`engine/dhcpaction_test.go` (the configuration of the server with and without the overlay, the address of `force_new_ip` and its checks, a full pool, the pools it goes through (the last one first),
two devices that each get their own address and concurrent writes of one batch that do as well, a configured device without `network` whose actions act in the network of its lease, an identity update that moves a silenced device into a network with DHCP, `delete_lease` not done again when its network had DHCP off and on, the fault of the device that stays attached after the move at level 0, an action Kea refuses and one it is not refused for, `delete_lease` once and again and for a device, a deletion that failed and is done later, an action that cannot reach anybody), `api/dhcpaction_test.go` (the states and `overridden_by`, the address over the API, the refusals with their paths, the 422 of an action Kea refuses, the deletion and its event on the stream,
a run with a `dhcp` step and one that ends as an error) with the capabilities of `misc_test.go`, `overlays_test.go` and `runs_test.go`. Web: `lib/faults.test.ts` (the chips), `e2e/dhcp-actions.spec.ts` (Playwright: the faults screen lists the actions under DHCP with what each does). The Kea tests of `kea/dhcpwire_testbed_test.go` (build tag `testbed`, root and Kea) pass here: `TestAShortLeaseOfASubnetIsWhatEveryClientGetsEvenOneThatAsksForAnHour`,
`TestALeaseClassGivesItsClientsTheirOwnLeaseTimeAndNoOtherClient`, `TestSilencedClientsAreNotAnsweredAndTheOthersAre`, `TestAReservationOfAnotherAddressNaksTheRenewalAndTheClientGetsTheReservedAddress`, `TestAReservationWithOptionsOnlyChangesTheOptionsOfItsClient` and `TestDeletingLeasesRemovesThoseOfTheClientsOrTheSubnetAndTheClientGetsTheSameAddressBack`.

Level 1 (written, vetted with `-tags testbed`, run by CI with `$M23_LEVEL1`, not run here: no KVM, no namespaces): `engine/integration_dhcpactions_test.go`, with busybox's `udhcpc` in the client namespaces against the real Kea in the gateway's namespace (the lines of the client's script say each event and the
lease, the router, the DNS server and the domain it was given). The script of the bed does not flush the address at a `renew` any more (the address does not change then, and the flush took the default route of a test that was measuring). busybox's `udhcpc` (1.36 and later) raises a lease below 30 s to 30 s for its own timers and renews at half of that, so a client of a short lease renews about 15 s after it is bound, not at half of the lease it was given; the tests wait 25 s for that renewal, which still comes before the 30 s of a client of the network's own lease of a minute (a wait of 15 s raced it and failed twice in the VM job, CI runs 40351451158 and 40393245421).

### Acceptance map (plan M23 "Tests" and Scope)

| Plan M23 | Test |
|---|---|
| `udhcpc` in the testbed observes each behavior: short leases | `TestAShortLeaseOfTheNetworkIsWhatUdhcpcGetsAndItRenewsSoonOnTheRealKernel`, `TestAShortLeaseOfADeviceReachesOnlyThatDeviceOnTheRealKernel` (level 1), `TestAShortLeaseOfASubnetIsWhatEveryClientGetsEvenOneThatAsksForAnHour`, `TestALeaseClassGivesItsClientsTheirOwnLeaseTimeAndNoOtherClient` (the real Kea, here) |
| … lease deletion | `TestDeletingTheLeaseRemovesItAndTheClientGetsTheSameAddressAtItsRenewalOnTheRealKernel` (level 1), `TestDeletingLeasesRemovesThoseOfTheClientsOrTheSubnetAndTheClientGetsTheSameAddressBack` (the real Kea), `TestDeleteLeaseDeletesOnceWhenItIsWrittenAndAgainWhenItIsWrittenAgain` |
| … forced new IP via reservation change (NAK) | `TestForceNewIPMovesTheClientAtItsRenewalOnTheRealKernel` (level 1), `TestAReservationOfAnotherAddressNaksTheRenewalAndTheClientGetsTheReservedAddress` (the real Kea: the NAK of the renewal and of the restart, then the reserved address) |
| … option changes | `TestSetOptionsChangeWhatTheClientIsToldOnTheRealKernel` (level 1), `TestAReservationWithOptionsOnlyChangesTheOptionsOfItsClient`, `TestSetOptionsReplacesTheFieldsItNamesOfTheNetworksOptions`, `TestSetOptionsOfADeviceIsItsReservationWithTheConfiguredOptionsAndItsOwn` |
| … silence | `TestSilenceLeavesTheClientWithoutAnAnswerOnTheRealKernel` (level 1), `TestSilencedClientsAreNotAnsweredAndTheOthersAre` (the real Kea) |
| A device fault stays attached after the forced new IP | `TestADeviceFaultStaysAttachedAfterTheForcedNewIPOnTheRealKernel` (level 1: the fault's delay at the new address, the kernel's identity map, the fault count), `TestADeviceFaultStaysAttachedAfterTheForcedNewIPAndTheDeviceFollowsItsAddress` (level 0: the identity map and the fault of the device after the move) |
| Scope: DHCP scenario step type | `dhcp` in `run.SupportedStepTypes`, `TestARunWithADHCPStepAppliesItAndTakesItOut`, `TestARunWhoseDHCPStepCannotBeCarriedOutEndsAsAnError`, `TestARunIsRefusedForWhatTheBuildDoesNotHaveAndForWhatDoesNotFit` (capabilities) |
| Scope: the overlay kind `dhcp` (refused with `unsupported_feature` until M23, plan M8a) | `TestADHCPActionReachesTheServersConfigurationAndLeavesWithItsOverlay`, `TestADHCPActionIsEffectiveWhileTheServerRunsItAndDisabledWhereNothingServesDHCP`, `TestCapabilitiesNameWhatOverlaysThisBuildTakes` |

## Captures UI (M23a)

Plan §2.17 "Captures" and M23a. The Captures screen and the capture panel of the device detail, on the API and the engine of M17.
The one addition to the API is `GET /captures/storage`; everything else is the web app. The design of the screen is part of the
milestone and is in plan §2.17 (the row "Captures" of the table of screens; it was listed under "Not yet designed" before).

### How it works

- **Storage.** The spec has `GET /captures/storage` (`getCaptureStorage`, scope `read`, `x-milestone: M23a`) with the schema
  `CaptureStorage`: `quota_bytes` (the configuration's `settings.retention.captures_quota`, else `--capture-quota`), `used_bytes` (the
  sum of the sizes of all captures, a running one as the executor last reported it, the number the quota decides on),
  `volume_total_bytes`, `volume_free_bytes` and `free_percent` (left out when the volume cannot be read, so the UI does not make up a
  free space), `min_free_percent` (10), `disk_low` (below the limit, decided with the same number as the stop and the refusal),
  `running` and `max_running` (8). `capturemgr.Manager.Storage` makes it, `Server.GetCaptureStorage` serves it; a deployment without a
  capture volume answers it with `422 unsupported_feature` like every capture operation. Nothing else on the server changed: the
  list, start, stop, delete and download are M17's.
- **Routes and navigation** (`src/router.ts`, `SidebarNav.vue`): `/captures`. The navigation entry Captures is a link (it was a disabled
  entry until M23a). An event whose subject is a capture links to it (`lib/links.ts`).
- **The screen** (`views/CapturesView.vue`): the storage card (`StorageCard.vue`: the quota as a bar with `1.0 GiB of 5.0 GiB`, the
  free space of the volume with the limit, the running captures; a red notice when the volume is below the limit, a plain one when the
  quota is used up) and the list (`CaptureRow.vue` in a grid: name with the format and the mark SENSITIVE, scope, state, size, packets,
  dropped, start, run and the actions). The list is `useCaptures` (`composables/gateway.ts`: every page of `GET /captures`, read every 2 s
  while one runs and every 15 s otherwise), the storage `useCaptureStorage` (the same rhythm). A list that cannot be read says so
  (`LoadError`) instead of showing the empty state.
- **The state** (`lib/captures.ts` `captureState`) is a dot and a label: *Running*, *Stopped* (a user), *Ended · duration*, *Ended · size limit*,
  *Ended with its run* (all grey, as asked), *Stopped · quota*, *Stopped · disk low* and *Failed* (red, the gateway's `message` under the label:
  the capture is shorter than asked or damaged; what it recorded stays and can be downloaded). The API gives no separate reason for
  the quota: a stop by `size` that has a `message` is the quota (the capture reached the space left in it, or the captures together
  passed it); the size limit the user chose ends without a message (`capturemgr.finish`).
- **Actions** (`CaptureActions.vue`, shared by the screen and the panel): *Stop* (a running capture; answers when the files are closed),
  *Download* and *Key log* (plain links to `GET /captures/{id}/download` and `?part=keylog`), *Delete* (a stopped capture only, after a
  dialog that names its size). Every action is recorded for the `</> API` panel and refreshes the list and the storage.
- **New capture** (`CaptureDialog.vue`, `lib/captures.ts` `CaptureForm`): the form of the request of `POST /captures`. Scope: Device,
  Network, Selector (a source of everything, a network, a group or a device, and the traffic fields the fault dialog and the scenario
  steps use: `TrafficFields.vue`) or Uplink; duration; size limit; under *Advanced* the format, the packet length and a ring buffer that
  replaces the size limit (the request never has both, as the API refuses it). `captureErrors` mirrors the limits of the gateway
  (a size of at least 64 KiB, 2 to 100 files, a packet length of 64 to 262144, a duration of at least a second) so Start waits until the form is
  valid; what only the gateway can know (a device without an address, a host name nobody asked for yet, a size above the quota) comes back
  as `422` with a pointer, `captureFieldOf` finds the field, and a refusal of an advanced field opens *Advanced*. The refusals without a
  field (`capacity_exceeded`: a low disk, eight captures running, the quota used by running ones) stand under the form. From a device's page
  the scope is fixed to the device.
- **The device detail** (`CapturePanel.vue`): the captures whose scope is the device, by UUID or by name, or a selector whose source is the
  device (`isDeviceCapture`), newest first, in the compact form of the row, with the same actions; *Capture* opens the dialog with the
  device as its scope, *All captures* links to the screen.
- **Live updates** (`composables/eventStream.ts`): `capture_started`, `capture_stopped` and `disk_low` invalidate `['captures']` (the list and
  the storage) and, for the first two, the runs as before. Three toasts: the quota stopped a capture (`capture_stopped` with the reason `size`
  and a message), a capture failed (`error`), and the disk ran low (`disk_low`); a stop by a user or by the duration is no news to the
  user who asked for it.

### Decisions

Taken by best practice where the plan leaves room (none needs the maintainer, so there is no Phase 5 item in `open-items.md` for M23a):

1. **The storage is an operation of its own (`GET /captures/storage`)** and not a field of `CapturePage`: the free space of the volume is not
   a property of a page of captures (a filtered list or a later page would repeat it), and a client that wants only the quota should not read
   the list. It is additive (plan §2.15: the version stays `/api/v1`).
2. **A download is a plain link, not a request that the page buffers.** A capture can be a gigabyte (the default limit is 1 GiB), the browser
   streams a link to disk, and the file name comes from the gateway's `Content-Disposition` (`attachment`, the name of the capture), so the
   link has no `download` attribute: a link with one is not seen by Playwright's request interception, so the test could not check the request.
   If the capture is gone meanwhile, the browser shows the gateway's error page instead of a file (the list is at most 2 s old and does not
   offer a link for a capture it does not have).
3. **The key log is offered only for a capture that has one (`keylog: true`) and only with the scope `full`;** a capture marked `sensitive` is
   downloaded only with `full`, too (the gateway checks both, the UI does not offer what the gateway would refuse). The admin's session has
   `full`, so the cases of a token with a smaller scope are a test of the mock (`sessionScope`), not of a screen anyone will see.
4. **The management networks are not offered in the dialog.** The gateway never records the admin's tunnel and refuses a scope that names one
   (`management_network`, decided 2026-10-19, P4-AUDIT-16); the dialog leaves the WireGuard networks of the role management out of the
   choices of a network and of a selector's source, so the refusal is not the way to find that out. The refusal is still shown under the field
   when a name is typed in by hand through another client.
5. **A capture the quota stopped is a red state, a stop by the size limit the user chose is not.** The first is shorter than the user can
   know without looking (they asked for 1 GiB and the quota had less), the second is the capture doing what was asked. The tone follows the
   plan's colours: red for what failed, grey for what ended as asked, blue for what runs.
6. **Delete asks first,** unlike Stop: the files of a capture, and often a bug that took an hour to reproduce, are gone for good, and the
   gateway has no undo. The dialog names the size.
7. **The screen needs the capability.** Until `GET /capabilities` has answered nothing asks for captures (a deployment without a capture volume
   would answer every call with `422 unsupported_feature`); a gateway whose features lack `captures` gets one sentence instead of the list and
   no *New capture*, here and in the device's panel. Capabilities that cannot be read do not keep the screen from trying.
8. **No filter and no search yet.** The list is every capture newest first; the quota keeps it short (the oldest stopped captures go first),
   and the runs' captures are reached from their run (the Run column links to it). A filter by run or device is a later refinement, not
   part of plan M23a's scope.

### Tests

Level 0, all run here. Go: `internal/api/captures_test.go` `TestTheStorageOfTheCapturesIsTheQuotaTheSizesAndTheFreeSpace` (the
quota of the configuration, the sizes of a running and a stopped capture, the volume against the limit and exactly at it, the delete that
gives the size back, the token that only reads) with the response checked against the spec like every test of that file, the token
scopes and the deployment without a volume in the tests that list their operations, and `misc_test.go` (the operation belongs to a milestone
that is in the build). Web unit tests (`make test-web`, vitest, 505 tests in 39 files with the older ones): `lib/captures.test.ts` (the states and
the quota tell from the size limit, the sizes, the storage view, the scope in words and the captures of a device, the form and the request it
makes, the checks of the limits, the field a refusal names) and `composables/eventStream.test.ts` (the events read the captures, the three
toasts and the ends that have none) and the route in `router.test.ts`. Playwright against the mocked API (`make test-e2e`,
`e2e/captures.spec.ts`, 20 tests): the navigation link; the list with all its columns and the storage; the counters of a running capture
following the gateway; starting a capture on a device, seeing it run in the panel and in the list, downloading it while it runs, stopping it,
downloading it; the panel of a device lists only its captures; a capture the quota, a low disk or a failure ended is shown as such and one the
size limit ended is not; the live stop by the quota and the low disk with their toasts; a capture started elsewhere appearing in both places;
deleting from the list and from the panel with the question; a capture that is gone when it is stopped or deleted (the quota took it) says so and leaves the list at once; the key log and the mark SENSITIVE, and the same with a smaller scope; New
capture for a network, a selector, the uplink with a ring buffer and a pcap file, with the request that is sent; the messages under the
fields and Start that waits; New capture without the clients of the admin tunnel, whose device page says why they cannot be captured (P5-AUDIT-31); the refusals of the gateway under their fields, opening *Advanced* and below the form; the request in the API
panel and in the dialog and no admin tunnel among the choices; a gateway without a capture volume; a list that cannot be read; the API
panel of the screen.

Nothing of M23a needs a VM or a namespace: the captures themselves are M17's level 1 tests, and the screen runs against the mocked API
(plan M23a "Tests"), so no test is left to CI beyond the ones it runs anyway.

### Acceptance map (plan M23a "Tests" and Scope)

| Plan M23a | Test |
|---|---|
| Tests: start a capture on a device, see it running in the list and in the device detail, stop it and download it | `a capture of a device: start it on the device, see it running there and in the list, stop it and download it`, `the device's panel lists only the captures of that device …` |
| Tests: a capture that hit the quota or the free-space limit is shown as such | `a capture the quota or a low disk ended is shown as such, one the size limit ended is not`, `a capture that the quota or the disk stops while the screen is open shows up at once, with a toast`; `lib/captures.test.ts` (the states) |
| Tests: deleting a capture removes it from both places | `deleting a capture removes it from the list and from the device, after a question` |
| Scope: list of captures (name, scope, state, size, packets, dropped, started, the run) | `the list shows name, scope, state, size, packets, dropped, start and run, and the storage`, `the counters of a running capture follow the gateway` |
| Scope: "New capture" with the scope of the request, format, snap length, duration, size or ring buffer | `New capture: a network, a selector and the uplink, with the limits, as the request has them`, `New capture: each message stands under its field …`, `New capture: the gateway refuses a field …`, `New capture shows the request in the API panel …`; `lib/captures.test.ts` (the form) |
| Scope: stop, download, the key log only with scope `full`, delete | the tests above, `the TLS key log is offered only for a capture that has one …`, `without the scope full there is no key log …` |
| Scope: the quota and the free space | `TestTheStorageOfTheCapturesIsTheQuotaTheSizesAndTheFreeSpace` (the API), the storage assertions of the list tests (the card, the low-disk notice) |
| Scope: the device detail's panel with a "Capture" action | `a capture of a device: …`, `the device's panel lists only the captures …` |
| Scope: the events keep the list live | `a capture that the quota or the disk stops while the screen is open …`, `a capture that starts elsewhere … appears … without a reload`; `composables/eventStream.test.ts` |
| Scope: the navigation entry becomes a link | `the navigation entry Captures is a link to the screen`; `router.test.ts` |
| Scope: the design is added to §2.17 | plan §2.17, the row "Captures" of the table of screens; "Not yet designed" no longer lists it |

## Forms for the application-layer kinds (M23b)

Plan §2.17 ("Forms for the application-layer kinds") and M23b. M20 to M23 deliver the DNS faults, the TLS cases and the DHCP test actions in the API; the
milestone makes them in the web app and with `chaosctl`: a DNS fault and a TLS case in the "Add fault" dialog and as the steps `dns` and `tls`, the DHCP
actions on the device detail and the network card and as the step `dhcp`, the download of the test CA, and the commands `chaosctl dns set`, `tls set` and
`dhcp set`. There is no change of the API: everything the forms send is the request of M20, M21 and M23 as the spec has it (`OverlayRequest` with `dns`,
`tls` or `dhcp`, `Step`), and the generated code is unchanged. The design is in §2.17; it closes P5-M20-02, P5-M21-01 and P5-M23-01.

### How it works

- **Three libraries, one per family** (`web/src/lib/dnsForm.ts`, `tlsForm.ts`, `dhcpForm.ts`). Each has the form's state (the text of the fields, as typed), `…Errors` (the
  checks under the fields, by field name: `dnsDelay`, `tlsSni`, `leaseTime`, …; host names, server names and durations use the regular expressions of the generated Zod schemas, so a field is valid
  where the gateway's schema accepts it, and the ports and the destination the checks of the other selectors), `…Body` (the `dns`, `tls` or `dhcp` part of the request: only what the chosen action uses, since the gateway refuses the other
  fields), `…ToForm` (an existing fault or step read back: sending it unchanged is the identity, which the tests pin), the words (`dnsEffect`, `tlsEffect`,
  `dhcpEffect`, `tlsScope`) the preview sentences are made of, and `…FieldOf` (the field a problem of the gateway belongs to, by its JSON pointer below the part).
- **The three components** (`components/faults/DnsFields.vue`, `TlsFields.vue`, `components/dhcp/DhcpFields.vue`) are the fields and nothing else; the "Add fault"
  dialog, the DHCP dialog and the step dialog put them in their forms, so a fault from the dialog and the step that makes it are the same fields. Labels and
  messages are tied as everywhere (`aria-invalid`, `aria-describedby` to the message under the field, else to the hint); the case, the action and the options have
  a sentence of what they do.
- **The Add fault dialog** (`FaultDialog.vue`, `lib/faultForm.ts`). `FaultFamily` has two more members, `dns` and `tls`: the kind choice of the dialog, not a family of a fault
  body. `formToOverlay` makes `{target, dns}` or `{target, tls}` for them (and the `ttl` of the shared duration choice, now `lifetimeOf`, which the DHCP dialog uses too),
  `overlayToForm` opens an overlay of any of the three kinds (an edit keeps its remaining time as before), `previewSentence` says what it does, `fieldOf` maps the pointers
  `/dns/…` and `/tls/…`. The two kinds are overlays only (decision 1): the dialog takes *Save as* away while one is chosen and does not offer them while it is
  set to Configuration. A device the TLS case is aimed at is looked up for `trusts_test_ca`, which decides the note under the three cases signed by the test CA.
- **The DHCP card and dialog** (`components/dhcp/DhcpActionsCard.vue`, `DhcpActionDialog.vue`). The card is on the device detail (in the aside, under the Network card) and on the
  selected local test network of the Networks screen (above the leases). It lists the `dhcp` overlays whose target is exactly its device or network (read with the
  overlays of the screens, `useOverlays`), and a button per action opens the dialog with the action chosen. The dialog's request is
  `{target, dhcp, ttl}`; its action is fixed when an action is edited, and a refusal of the gateway (`/dhcp/ip`, `/dhcp/lease_time`, `/dhcp/options/…`) goes under the
  field. The faults screen edits a DHCP action in the same dialog.
- **The address of a forced new IP** is chosen by the gateway when the action is written and is only in the overlay as it reads back (`dhcp.ip`, decision 3 of M23): the dialog
  reads the answer of `POST /overlays` and says it in the success toast, and the card says it in the line of the action for as long as the action exists.
- **The test CA** (`components/ui/TestCaLink.vue`): a plain link to `GET /tls/ca` (no `download` attribute: the browser saves the gateway's file named by its
  `Content-Disposition`, as for a capture), in the TLS fields of both dialogs, as the button **Test CA** in the header of the Faults screen and in the *Test CA* row of the device's identity card.
- **The step dialog** (`StepDialog.vue`, `lib/stepForm.ts`). `editableTypes` has `dns`, `tls` and `dhcp` (the step types follow the capabilities as before; a gateway without
  `dhcp` does not offer it), `StepForm` has the three forms, `stepFormErrors` their checks and `stepFieldOf` the pointers `dns/…`, `tls/…`, `dhcp/…`. The target of a DHCP step is the
  step's narrowing or the scenario's target: `dhcpTargetProblem` (`lib/scenarioRules.ts`) is the gateway's `dhcpTarget` (a device or a network, a forced new IP a device) and is applied by the
  dialog, which shows it under *Acts on* as soon as the type is chosen, and by `scenarioProblems`, so the YAML view and an import show it too. The timeline's line of a step
  is the chips of the faults screen (`dnsChips`, `tlsChips`, `dhcpChips`), which say what the step carries ("DNS answer 10.9.0.1 · broker.example.com").
- **`chaosctl`** (`internal/ctl/appfamilies.go`). `dns set <action>`, `tls set <case>` and `dhcp set <action>` take the action or the case as their argument (dashes or underscores:
  `wrong-answer`, `not-yet-valid`) and the flags of the fields (`--name`, `--delay`, `--answer`, `--answer-ttl`; `--port`, `--sni`, `--to`, `--cut-existing`; `--lease-time`, `--ip`, `--router`,
  `--dns-server`, `--domain`, `--no-domain`, `--ntp-server`, `--option code=value`), the target of `fault set` (`--device`, `--group`, `--network`, `--global`; a DHCP action only `--device` or
  `--network`, and its refusal without a target names only these two, P5-AUDIT-34) and `--ttl` and `--lease`. What the command line can see is refused with exit 2 before a request (an unknown action, a field the action needs and lacks or does not use);
  what only the gateway knows is its refusal with the pointer, exit 2 as for every command. Standard output is the id of the overlay (`--json`: the overlay), `dhcp set force-new-ip` says the
  address on standard error.

### Decisions

Taken by best practice where the plan leaves room (none needs the maintainer; the milestone closes P5-M20-02, P5-M21-01 and P5-M23-01; the two small points the review left in the code, `P5-M23b-01` and `P5-M23b-02` in `open-items.md`, are closed):

1. **DNS and TLS are kinds of the Add fault dialog, DHCP is not.** The plan names the DNS and the TLS "in the Add fault dialog" and the DHCP actions "on the device detail and the network card"; a DHCP action is no fault of a traffic selector (it
   changes what a server does, for a device or a network, never for a group or everything) and §2.7 says DHCP is shown in the network and device views. The faults screen lists all three, and edits the DHCP actions in their dialog.
2. **A DNS fault and a TLS case are overlays only.** The API has no configured DNS fault or TLS case (`ConfigFault` has three families; M20 decision 3), so the dialog does not offer to save them in the configuration instead of hiding the choice and failing at the
   preview: while one of the two kinds is chosen there is no *Save as*, and while it is set to Configuration there are no such kinds, each with its sentence.
3. **A TLS case has no protocol field.** TLS runs over TCP and the API takes ports without a protocol for it; a new case sends none, and a case that was written with `protocol: tcp` keeps it when it is opened and saved again (the form never drops what it does not show).
   The checks of the ports use TCP (`tlsErrors`), so "ports need the protocol TCP or UDP" is not shown for a field that has no protocol.
4. **The interception case is kept, not edited.** A `tls` overlay or step with the case `intercept` is opened with its settings and its rules and sends them back as they were (the case is shown as "Interception · its settings and rules are kept as they
   are"); a new case is made of the eight cases of the responder. The form of the rules is M23c's, which builds on this dialog (done: the settings and rules are edited now, see "Interception UI (M23c)"). Changing the case of an opened interception to another drops its settings, because the gateway refuses them with any other case.
5. **The test CA is a link, in three places.** A plain link keeps the file out of the page's memory and names it as the gateway does (decision 2 of M23a); it is where a tester needs it (the TLS fields, the device whose *Test CA* row says "not trusted") and
   where a person looks for the certificate without a case in mind (the Faults screen). The regeneration, and any question before it, is M23c's.
6. **The trust note comes from the device.** §2.8: the expected result of a case comes from `trusts_test_ca`. The three cases that the test CA signs say, for one configured device, what that device does with them, and for anything else what a device that trusts the CA does; the
   other cases say nothing, since every device must reject them.
7. **DHCP actions are off where they could change nothing, with the reason shown.** A network with DHCP off, and a forced new IP of a device without a MAC address (the gateway refuses it), disable their buttons and say why under them, instead of the refusal or an overlay in the state
   `disabled` that nobody asked for. The gateway accepts the other case (an action on a network without DHCP is `disabled` and starts to act when DHCP is switched on, M23 decision 7), so the card is stricter than the API and a client of the API is not.
8. **An edited DHCP action keeps its action.** The key of an action is its action (§2.15), so changing it would be another action; the select is fixed in the edit dialog, and the way to another action is its button. An action of another owner (a token, a run) that is edited is
   written as the admin's and the old one is removed, as the fault dialog does.
9. **The same duration choice for every overlay dialog.** The fault dialog's choices (1 min to until removed, keeping the remaining time of an edited overlay, an own TTL that is not one of them) are `lifetimeOf`, `untilText` and `overlayLifetime` in `lib/faultForm.ts` and serve the DHCP
   dialog too; a deleted lease is done once, when the action is written, and "Duration" says how long it stays in the list.
10. **A step names what it carries.** The timeline's line for the three types is the chips of the faults screen (the delay of a DNS step, the addresses of a wrong answer, the ports and the cut of a TLS case, the lease time and the address of a DHCP action), where it said the action
    only; the order of the types in the step dialog puts the three after Fault (`stepTypes`), where they were last and not offered.
11. **`chaosctl` takes the action as an argument and has its own flag for the TTL of the answers.** `fault set` is flags only because a fault has no kind to name; these three have an action or a case. The TTL of a `short_ttl` answer is `--answer-ttl`, because `--ttl` is the lifetime of the
    overlay for every command. `tls set intercept` is refused with the way to make it (`overlay create -f`): its rules had no flags until M23c's `intercept set` (the message names it now). `chaosctl with` takes these overlays as `--overlay` files, as it does for every kind it has no flags for.
12. **The mock's capabilities are those of the gateway.** `e2e/mock.ts` now lists the overlay kinds `dns`, `tls` and `dhcp` and their step types, as `GET /capabilities` does; two older Playwright tests that pinned the offered step types (`scenarios.spec.ts`) and a DNS step that "the editor does not make, kept as it is" were changed on purpose: the step is edited with its form, and a type the gateway does not list (a gateway of an older build) is not offered, and the DHCP card is left out where `overlay_kinds` has no `dhcp`, as the kinds DNS and TLS of the dialog are (`a gateway that has no DHCP actions has no card for them`).
13. **No level 1 test.** Nothing of the milestone needs a kernel: the forms send the requests of M20, M21 and M23 whose behavior on the real kernel those milestones' level 1 tests pin, and a form's request against the real API is shown by the other two levels: every body the Playwright tests of the forms capture is parsed with the spec's Zod schema (`CreateOverlayBody`, the same document the contract tests of the API use), and
    `chaosctl`, which sends the same requests, is run in-process against the real API on the simulated kernel (`TestChaosctlOverlays/MakesDNSFaults`, `MakesTLSCases`, `MakesDHCPActions`), including the gateway's refusals with their pointers that the forms put under their fields.

### Tests

Level 0, all run here. Web unit tests (`make test-web`): `lib/dnsForm.test.ts`, `tlsForm.test.ts` and `dhcpForm.test.ts` (the body of each action and case checked against the spec's schema, the checks under each field, an existing fault read back and sent unchanged, the words, the pointers), `lib/faultForm.test.ts`
(the DNS and TLS kinds: the request, the checks, an overlay opened and sent back, the preview, the pointers, the shared lifetime), `lib/stepForm.test.ts` (the three step types through the form, their checks, the DHCP target, the pointers, an interception step that comes back as it was),
`lib/scenarioRules.test.ts` (the target of a DHCP action in the scenario's problems) and `lib/scenarios.test.ts` (the lines of the steps). Playwright against the mocked API (`make test-e2e`, `e2e/app-layer-forms.spec.ts`, 22 tests: 19 since M23c replaced the one of the kept interception settings, one for P5-M23b-02 and two of the Phase 5 audit, P5-AUDIT-08 and -32): a DNS fault from the dialog of a device with its checks, the request, the list and an edit that keeps the overlay;
a DNS fault for a network from the faults screen and the kinds that are not offered for a configured fault; the gateway's refusal of a DNS fault and of a TLS case under their fields; a TLS case with every field, its trust note, the download of the test CA and the request; the handshake cases, the defaults, a case edited so that its key changes (an interception case that kept its settings was a test here until M23c made them editable); the test CA from the
faults screen and the device detail; the DHCP card of a device (a short lease with its checks, the request, the list, an edit, the removal), a forced new IP with the address the gateway chose and the address that is named, delete lease, set options (the checks, the reserved codes, the domain taken away) and silence, the refusals under their fields, the buttons that are off (a device without a MAC, a network without DHCP), a device that is in no network the gateway serves (P5-M23b-02: the card says so and its buttons are off), a network without a `dhcp` block read as DHCP off (P5-AUDIT-08), the card while the overlays load (P5-AUDIT-32), the network card
with the four actions of a network, an action that another overrides and the edit from the faults screen; the three step types made in the step dialog, shown on the timeline and sent in the revision, a DHCP step of a scenario that targets a group, a DHCP step on a network and its edit; the keyboard path (a DNS fault chosen and applied with the keys, a DHCP action opened from the card, applied with Enter, and the focus returned to the control that opened the dialog by Escape). Go: `internal/ctl/appfamilies_test.go` (the flags of the three commands and the bodies they become, the checks before a request),
`internal/api/ctl_appfamilies_test.go` (the commands against the real API on the simulated kernel: each action and case with the overlay read back, the same key replacing the overlay, `--json`, the refusals of the command line and of the gateway with their pointers, the address of a forced new IP, deleting what was made).

No test is left to CI beyond the ones it runs anyway: the Playwright suite and the Go tests run in the jobs that run them for every milestone.

### Acceptance map (plan M23b "Scope" and "Tests")

| Plan M23b | Test |
|---|---|
| A DNS fault made from its form, the request sent matches the API's | `a DNS fault: made in the Add fault dialog of a device …`, `a DNS fault for a network from the faults screen …`; `lib/dnsForm.test.ts` (every action is a fault the spec accepts), `lib/faultForm.test.ts` |
| A TLS case made from its form | `a TLS case: its selection, the checks, the note about the test CA, the request, and the download of the CA`, `a TLS case: handshake cases, the default ports …`; `lib/tlsForm.test.ts` |
| A DHCP action made from its form, the address `force_new_ip` is given shown | `the DHCP card of a device …`, `a forced new IP shows the address the gateway chose, and the address that is named`, `delete lease, set options and silence of a device …`, `the network card …`, `a device that is in no network the gateway serves says so under its DHCP card, and its buttons are off` (P5-M23b-02); `lib/dhcpForm.test.ts` |
| Each of the three as a step in the step dialog | `the steps dns, tls and dhcp: made with the same fields …`, `a DHCP step of a scenario that targets a group …`, `a DHCP step on a network offers no forced new IP …`, `a DNS step is edited with the DNS fields`; `lib/stepForm.test.ts` |
| The validation messages | the `toHaveAccessibleDescription` assertions of the tests above, the refusals under the fields (`the gateway's refusal of a DNS fault`, `… of a TLS case`, `… of a DHCP action stands under the field it names`); `…Errors` and `…FieldOf` in the unit tests |
| The CA download | `a TLS case: its selection …` (the TLS fields), `the test CA is downloaded from the faults screen and from the device detail` |
| The `chaosctl` flags tested in-process against the API on the simulated kernel | `TestChaosctlOverlays/MakesDNSFaults`, `MakesTLSCases`, `MakesDHCPActions`; `TestTheFlagsOfADNSFaultBecomeItsBody`, `TestTheFlagsOfATLSCaseBecomeItsBody`, `TestTheFlagsOfADHCPActionBecomeItsBody`, `TestADHCPActionWithoutATargetNamesTheTargetFlagsTheCommandHas` |
| One level 1 flow only if something needs the kernel | none needed (decision 13) |
| The design is added to §2.17 | plan §2.17, "Forms for the application-layer kinds (M23b)" and the rows of the table of screens |

## Interception UI (M23c)

Plan §2.17 ("Interception UI") and M23c. M22 delivers the interception in the API: the case `intercept` of the TLS kind with its settings and its HTTP rules, the exchanges of the proxy (`GET /intercept/flows`), the replacement of the test CA (`POST /tls/ca/regenerate`). The milestone makes it in the web
app and with `chaosctl`: the form of the settings and of the rules in the "Add fault" dialog and the step dialog, the Interception screen with the table of the exchanges (filters, live update) and the CA buttons, a panel of a device's exchanges on the device detail, and the commands
`chaosctl intercept set`, `intercept flows` and `tls ca`. There is no change of the API: the forms send the `tls` part of `OverlayRequest` and `Step` as the spec has it, the screen reads the log with the parameters it has, and the generated code is unchanged. It closes P5-M22-01. It is not part of V1.

### How it works

- **The form is a library and a component** (`web/src/lib/interceptForm.ts`, `components/faults/InterceptFields.vue`). The library has the state (the settings, the list of rules, each rule with the text of its fields as typed), `interceptErrors` and `ruleErrors` (the checks under the fields, by field name: `interceptUpstream`,
  `interceptRules`, `rule.<n>.<field>`; the host name and the durations use the regular expressions of the generated Zod schemas, the rest the rules of `internal/domain`), `interceptBody` and `ruleBody` (the `intercept` part of the case: only what the action uses and only what differs from the defaults),
  `interceptToForm` (a case read back: sending it unchanged is the identity, which the tests pin), the words (`matchText`, `actionText`, `ruleSummary`), `unreachableRules` and `interceptFieldOf` (the field a problem of the gateway belongs to, by its pointer below `intercept`). The component is the fields and nothing else.
- **A case of the TLS kind** (`lib/tlsForm.ts`, `TlsFields.vue`). `TlsForm` has the `intercept` form; `tlsBody` sends it only with the case `intercept` (the settings of another case are refused by the gateway, so changing the case keeps them in the form and leaves them out of the request); `tlsErrors` checks them only for that case;
  `tlsFieldOf` hands `/intercept/…` to `interceptFieldOf`. The Case choice has a third group, Interception; it is offered for a new case only when `GET /capabilities` lists `tls.intercept` (`useCanIntercept`), and a case that is edited keeps it. The dialog (`FaultDialog.vue`) and the step dialog (`StepDialog.vue`) pass the capability to `TlsFields`;
  nothing else of them changed, which is the point of having made the frame in M23b. `FaultDialog` has a `preset` for the Interception screen's **Add interception**.
- **The screen** (`views/InterceptView.vue`, route `/interception`, a navigation entry after Captures). The cases in force are `useActiveFaults()` narrowed to the overlays of the case `intercept`, in the list of the faults (`ActiveFaultList`: Edit and Remove as on the device detail), and the notice of the proxy is the gateway's own sentence of the
  health component `tls_proxy` when it is not healthy. The table (`components/intercept/FlowTable.vue`, `FlowRow.vue`) is `useInterceptFlows` (`composables/gateway.ts`): the key is `['intercept', 'flows', params]`, `lib/intercept.ts` makes the parameters of the filters (`flowParams`) and the words of a row (`requestText`, `outcomeText`,
  `sizeText`, `durationText`, the labels of the rules that acted). The filters are in the address (`?device=&host=&faulted=1`), so the device detail's link lands on them; the host settles for 250 ms before it is asked and is checked at the field (a host that is no name is not sent).
- **Live.** An exchange that no rule acted on comes with no event, so the query refetches every 3 s while the screen is open, and the event `http_fault_triggered` (new in `invalidates` of `composables/eventStream.ts`) reads the log at once. *Pause* does not stop the query: it takes a copy of what is shown (a pause that disabled the query would show nothing for a changed filter and
  would lose the data); a change of a filter resumes. A page is the newest 100, *Show older exchanges* asks for 100 more up to 500 (the API's limit per request: the table is a window onto the newest exchanges, and a longer look is `chaosctl intercept flows --all`); `placeholderData` keeps the shown page while a larger one loads. The rows have no id, so a row is keyed by time,
  client, destination and request (`flowKeys`), and the *Details* that are open stay with their exchange when new ones arrive above.
- **The CA** (`components/intercept/TestCaCard.vue`). The download is `TestCaLink` of M23b; the replacement is `regenerateTestCa()` after a `ModalDialog` that says what it costs, with the button off for a session without the scope `full` (`session.scope`), the gateway's refusal shown in the dialog, and after a success a reminder in the card and a toast.
- **The device detail** (`components/intercept/DeviceExchanges.vue`) is the same table in its compact form for `device=<id>`, limit 8; it is shown while an interception case acts on the device (an overlay of the case on the device's page: its own or a wider scope's) or the log holds exchanges of the device.
- **`chaosctl`** (`internal/ctl/intercept.go`). `intercept set` makes the case with the selection flags of `tls set` (now `tlsFlags.selection`, shared), the target and lifetime flags, `--no-keylog`, `--allow-quic`, `--upstream` and the repeated `--rule`; a rule is parsed by `parseHTTPRule` (items separated by `;`: `host=`, `path=`, `method=`, one of `block`, `abort`,
  `status=`, `delay=`, `throttle=`, or the changes `request-header=Name:Value`, `response-header=`, `remove-request-header=Name`, `remove-response-header=`, `replace=SEARCH=>REPLACEMENT`). `intercept flows` reads `GET /intercept/flows` with `--device`, `--host`, `--since` (a time or how long ago), `--faulted`, `--limit` and `--all` (follows the cursor), and prints a table (the device by its
  name: one more read of `/devices`, only when an exchange has a device; what the device sent, host, path, method and error, with every character that is not printable escaped as `\xNN`, so that an ESC or a BEL
  does not reach the terminal, P5-AUDIT-40; a line on stderr when older exchanges exist and `--all` was not given) or, with `--json`, the page of the API (with its `next_cursor`; with `--all` the exchanges of every page and no cursor, P5-AUDIT-39). `tls ca` downloads the certificate (to standard output or `--out`, mode 0644: it is public), `tls ca regenerate --yes` replaces it and prints the new one; without `--yes` it refuses and says what it costs. `tls set intercept` is still refused, and now names `intercept set`.

### Decisions

Taken by best practice where the plan leaves room (none needs the maintainer; the milestone closes P5-M22-01):

1. **The interception is a case of the TLS kind, not a kind of its own.** It is the case `intercept` of a `tls` overlay and of the step `tls` in the API, and a kind or a step type of the UI that is none in the API would need a second mapping in the dialog, the step dialog, the preview, the pointers and the scenario's YAML. The price is discoverability: the Case choice has a group of its own with a sentence,
   and the Interception screen's **Add interception** opens the dialog with the case chosen.
2. **A screen and a panel.** The plan says "as a screen or a panel of the device detail". The table of all exchanges with its filters is a screen (a log is read across devices, and the CA and the cases in force belong beside it); the device detail gets the compact table of its own exchanges, shown only where there is something to show, so that a gateway that never intercepts has no empty panel.
3. **The settings are sent only where they differ from the defaults.** The API's defaults for `keylog` and `block_quic` are true; a form that wrote `keylog: true` into every case would turn the defaults into data the day they change, and an edit would rewrite a case that did not change. A case that only watches sends no `intercept` at all, as `{case: intercept}` is valid (the proxy only watches).
4. **The form is stricter than the gateway in four places, each with its sentence.** A path prefix starts with `/` (a request path always does, so a prefix without it never matches), a `modify` rule must change something (an empty one is a mistake that the gateway accepts), a header with no value must be ticked *Remove* (the API's empty value means the same, and a field left empty by accident
   should not take a header away), and a header is named once per list whatever its case (HTTP header names have no case; the gateway accepts `X-A` and `x-a` side by side, and the later one in the object would silently win). `chaosctl intercept set` has the same rule for the empty value: `request-header=X-A:` is refused and names `remove-request-header=X-A`. A rule of the API that has one of these is opened and shown with the message, and saving needs it corrected; a client of the API is not held to it.
5. **A rule that can never act is marked, not refused.** The first rule that ends the rules (block, answer, abort) for every request makes the rules after it unreachable; the form says so under each (`unreachableRules`), because a list in which half the rules never act is what a person edits by mistake. A terminal rule with a match is no such rule, since another request goes on.
6. **Live is a poll and an event, and Pause is a copy.** The event of a rule that acts is announced at most every 10 s per device and case (M22), so it alone would show a busy device late; the poll (3 s) is what shows an exchange nothing acted on, and the event makes the table current at once when a rule did. The screen reads the log only while it is open, and the panel of a device while the device is.
7. **The filters are the API's.** Device, host and `faulted` are `GET /intercept/flows` parameters, so the table never filters a page in the browser and a filtered table shows the newest 100 that match, not those of the newest 100 that happen to; `since` is not offered (it is for `chaosctl`).
8. **Replacing the CA is one button with one question.** The API takes no argument, so the question is all the confirmation there is; it states the cost (the old certificate stops working for every device, the cases signed by the test CA and the interception are rejected until the new one is installed), what does not change (the unknown CA) and that the audit log has it. No typed confirmation: the action is
   recoverable by downloading the new certificate, and the other destructive questions of the UI (deleting a capture, removing a fault for everyone) are one click as well. A session without the scope `full` has the button off, with the reason, instead of a refusal after the question.
9. **`chaosctl intercept set` has its own rule syntax, not JSON.** A rule is a line of items; a list of rules is a repeated flag in the order given. The alternative, a JSON argument, would be a second way to write what `overlay create -f` takes and what a person has in a scenario file; the line keeps the common case (a status for a path, a delay for a host) in one flag and is checked before a request (a rule with two actions, an item that is none).
   Header values with a `;` and texts with `;` or `=>` in them cannot be written as flags, and are made with a document.
10. **`tls ca regenerate` needs `--yes`.** The command has no terminal to ask on in a pipeline, and a mistyped command that replaces the CA of a lab is not recoverable by the next command. `--yes` is the question's answer, as in other tools; the message says what the question would have said.
11. **The mock has the feature `tls.intercept`.** `e2e/mock.ts` lists it in `GET /capabilities` as the gateway does, and answers `GET /intercept/flows` with the filters of the API (device by id or name, host exact or `*.suffix`, `faulted`, `since`, the cursor) and `POST /tls/ca/regenerate`. Two older tests were changed on purpose: the list of the cases of the TLS dialog (it has a ninth, in a third group), and the test that an interception case is opened with its settings
    "kept as they are" (they are edited now: `an interception case is edited with its rules …`).
12. **A level 1 test of the commands.** Unlike M23b, the commands here read what only the real proxy writes (the log, with the device the observed state says the client is) and replace the CA that the real proxy loads, so one level 1 test makes a case with the flags, lets the device be served by it, reads the exchanges with `chaosctl` and replaces the CA while the device trusts the old one; the forms need none (decision 13 of M23b: every body is checked against the spec, and the commands send the same requests).

### Tests

Level 0, all run here. Web unit tests (`make test-web`): `lib/interceptForm.test.ts` (the settings and the rules of each action checked against the spec's schema, the checks under every field, the modify rule's headers and texts, a case read back and sent unchanged, the unreachable rules, the words, the pointers), `lib/intercept.test.ts` (the filters and the parameters they make, the words of a row, the keys), `lib/tlsForm.test.ts` (the ninth case, the checks only for it, the effect in words, the pointers) and
`router.test.ts` (the route and its query). Playwright against the mocked API (`make test-e2e`, `e2e/interception.spec.ts`, 17 tests): a case with five rules of every action made in the dialog of a device (the checks under each field, the order, the request checked against the schema, the preview, the chips of the list); a case edited with its rules and a rule that can never act marked; the gateway's refusals under the field of the rule; a gateway without the interception (the case is not offered, the screen says so,
nothing is asked); the keyboard path; the step `tls` with the case; the table (the columns of each kind, the rules that acted, the details that stay open, the filters and the address, the empty states, a link with filters); an empty log, the notice of a proxy that does not run and **Add interception**; a case in force edited and removed on the screen; the live table (the event, Pause and Resume, *Show older exchanges*; a second test for the two ways a row arrives: the poll for an exchange no rule acted on, and the event within 2 s of a poll, which only the event can do, since the next poll is 3 s away); a log that cannot be loaded; the cases in force when the overlays cannot be read but the configured faults can (P5-AUDIT-36); the CA (the download, the question, Cancel, a refusal, the replacement, the reminder, a session without the scope `full`); the device detail's panel and its link, and a device without one. Go: `internal/ctl/intercept_test.go` (the rules of the command line and the
settings they become, the selection shared with `tls set`, the words of a row, `--since`), `internal/api/ctl_intercept_test.go` (the commands against the real API on the simulated kernel: the case with every kind of rule read back from the API, the refusals of the command line and of the gateway with the pointer of the rule, the table and the filters, `--all`, `--json`, a token that only reads, the download and the replacement of the CA, with and without the scope).
Level 1 (written and vetted with `-tags testbed`, run by CI with `M22_LEVEL1`): `internal/api/e2e_intercept_test.go` `TestChaosctlMakesAnInterceptionCaseAndReadsItsExchangesAndTheCAOnTheRealKernel`.

### Acceptance map (plan M23c "Scope" and "Tests")

| Plan M23c | Test |
|---|---|
| The form of the HTTP rules in the Add fault dialog, with the `upstream` field | `an interception case with several rules: made in the Add fault dialog …`, `an interception case is edited with its rules …`, `the gateway's refusal of a rule stands under the field of that rule`; `lib/interceptForm.test.ts` |
| … and in the step dialog | `the step tls with the case intercept: its rules are made in the step dialog and sent in the scenario` |
| A case with several rules made from the form | the first test above: five rules, every action, the request equals the API's |
| The table of the exchanges with its filters (device, host, rule acted) | `the exchanges: the table, the details of a row, the filters in the address, and the empty states`; `lib/intercept.test.ts` |
| … and its live update from `http_fault_triggered` | `the table is live: it follows the gateway, can be paused, and shows older exchanges on request`, `an exchange no rule acted on comes with the poll, one a rule acted on with its event, before the next poll` (breaking either the poll or the event's invalidation fails it), and `composables/eventStream.test.ts` |
| A screen or a panel of the device detail | both: the screen above, `the device detail has a panel of the exchanges of the device while a case acts on it …`, `a device that no case acts on … has no panel of exchanges` |
| The CA buttons: download, and the regeneration after a question | `the test CA: downloaded from the screen, and replaced only after the question says what it costs`, `a session without the scope full cannot replace the test CA, and says why` |
| `chaosctl intercept flows` and `chaosctl tls ca` | `TestChaosctlListsTheExchangesOfTheProxyWithTheFiltersOfTheAPI`, `TestChaosctlDoesNotPrintTheControlCharactersADeviceSent`, `TestChaosctlDownloadsAndReplacesTheTestCA` (and `TestChaosctlMakesInterceptionCasesWithTheirRules` for `intercept set`); `internal/ctl/intercept_test.go` |
| The `chaosctl` commands in-process | the tests of the row above, against the API on the simulated kernel |
| A level 1 flow only if something needs the kernel and the proxy | `TestChaosctlMakesAnInterceptionCaseAndReadsItsExchangesAndTheCAOnTheRealKernel` (decision 12) |
| The design is added to §2.17 | plan §2.17, "Interception UI (M23c)", the row "Interception" of the table of screens and the changes to the rows of the device detail and the Add fault dialog |

## Diagnostics (M24)

Plan §2.12 and M24. Ping, a TCP and a UDP check, a DNS lookup, an HTTP(S) request, the details of a TLS handshake and the path that strings DNS, route,
TCP and TLS together, each run from the gateway's own network stack, with a structured result; `POST /diagnostics`, the package `internal/diag`,
and the Diagnostics screen. Probes (a diagnostic `from.probe`) are M25; traceroute, path MTU and iperf3 are after V1 (M38).

### How it works

- **The operation.** `POST /diagnostics` (`runDiagnostic`, scope `overlays`, `x-milestone: M24`) takes a `DiagnosticRequest` and answers a
  `DiagnosticResult` when the diagnostic is done (at most 60 s). The spec had both schemas from the start; M24 added to them, additively
  (plan §2.15: the version stays `/api/v1`): the request fields `payload` (the text of a UDP datagram) and `tls` (a path without the TLS step), the
  result fields `address` (the IPv4 address a name resolved to) and `dns.name`, and the text of the descriptions, which says which field belongs
  to which type, what `ok` means for each and what the 60 s are made of. `GET /capabilities` lists the feature `diagnostics` and
  `diagnostic_types` (`domain.DiagnosticTypes`).
- **What is refused and what is a result.** A target that fails (no reply, refused, a name that does not resolve, a certificate that does not verify,
  a status of 400 or more) is a `200` with `ok: false` and `error`, because the failure is the answer. A request that cannot be run is `422`:
  `validation_failed` (`domain.DecodeDiagnosticRequest`: the schema, then `ValidateDiagnosticRequest`), `unsupported_feature` for `from.probe`
  (the problem names M25), `capacity_exceeded` when `api.MaxDiagnostics` (8) run already. The rules the schema cannot say: the host is an IPv4 address or
  a host name (no URL, port, path, wildcard, IPv6 address or malformed address: `ValidDiagnosticHost`); `tcp`, `udp` and `path` need a `port`; a field
  that the type does not use is `unexpected_field` (a `count` for a TCP check is a mistake, not something to ignore); `dns_name` has no wildcard; an `http`
  `url` is http or https with a host and without credentials; the timeout is 10 ms to 60 s; a ping's echoes and its timeout fit the 60 s
  (`(count-1) × 200 ms + timeout`). The request context carries the 60 s as well, so a `tls` or `path` with long timeouts ends there.
  The run is recorded in the audit log (`diagnostic.run`, with the type, the target and ok or failed).
- **Where it runs.** `internal/diag.Runner` takes a `Net` (a dial for TCP and UDP, and the ICMP socket): `HostNet{Namespace}` is the stack of
  the process, or the network namespace `Namespace` (`Config.Namespace`: the gateway's namespace in the testbed, where the API runs in the root
  namespace of the VM). The sockets are made on a thread that entered the namespace and stay in it (as the capture and the observer do). In production the API
  runs in the host's network namespace without a capability, so a diagnostic starts from the gateway's own addresses: it does not take the forward chain
  of the devices, and with it none of their faults (plan §2.12, risk 15; `TestDiagnosticsFromTheGatewayDoNotPassThroughTheFaultsOfDevices`). The
  timing of the sockets uses the real clock (`internal/clock`) whatever clock the server has; the server's clock gives `started_at` and the time at which a
  certificate is verified.
- **The types** (`ok` in brackets):
  - `ping` (one reply or more, as `ping(8)`): `count` echoes (5), 200 ms apart, then the wait for the last reply up to `timeout`; a reply ends the wait early.
    Sent, received, loss, minimum, average, maximum and deviation of the round trip times, as `ping` prints them. The socket is the unprivileged
    ICMP datagram socket (`net.ipv4.ping_group_range`, see below), with the raw socket as the fallback for a process that has `CAP_NET_RAW`. On a raw socket
    the identifier and the sender decide which reply is ours; the datagram socket only delivers ours. An ICMP error that comes back (unreachable) is named in the
    sentence of a ping without a reply.
  - `tcp` (connected): the time to connect; `connection refused`, `no route to host`, `network is unreachable`, `timed out after 5s` are the kernel's words.
  - `udp` (a reply came back): sends `payload` (an empty datagram by default) and waits. A closed port answers with ICMP port unreachable, which a connected UDP
    socket reports as `connection refused`; no answer at all is `no reply within …` and the sentence says why that proves nothing (open or filtered, or a service
    that ignores this datagram). The check cannot say more than UDP does.
  - `dns` (NOERROR with an A record): `dns_name` asks the server in `host` (`port`, 53); without it `host` is the name and the server is the gateway's DNS proxy
    (`169.254.100.2:53` in the service namespace, `Config.Diagnostics.Proxy`). The proxy answers a client outside its networks with the static entries of all of them
    and its upstream answer, so the gateway sees what a device sees without the faults that name a device. NXDOMAIN, SERVFAIL, REFUSED and an answer without an A
    record are results; the answers list the A records and the CNAMEs. A truncated UDP answer is asked again over TCP.
  - `http` (a status below 400): one GET with the user agent `chaos-gateway/<version>`, no proxy from the environment, no redirects followed (the status
    and `Location` are the result), the headers of the response (the first 64, each cut at 1 KiB), the time until the response arrives.
  - `tls` (the certificate verifies for the SNI, else the host): connects, makes the handshake and reports the version (down to TLS 1.0, so an old server can be
    shown), the cipher, the ALPN protocol and the whole chain with its subjects, issuers, validity and DNS names. The handshake does not stop at a certificate that
    does not verify: it is made without the client's check and verified afterwards, so the details of an expired or unknown certificate are there next to
    `verify_error`, in the words `certificate has expired`, `certificate is not yet valid`, `certificate signed by unknown authority`, `certificate is valid for
    a, not b`. A handshake that fails (a server that is no TLS server, a reset, a stall) has no `tls` object and the error says `TLS handshake failed: …`.
  - `path` (every step): `dns` (an address is nothing to resolve; a name is resolved and the step says by which server and how long it took), `route` (a connected
    UDP socket sends nothing but makes the kernel find the way: it says the source address, or `network is unreachable`), `tcp`, `tls` (left out with `tls: false`);
    it stops at the first step that fails, the steps after it are not in the result, and `error` is `<step>: <why>`. The `tls` object is filled when the TLS step ran.
    The plan's example, `DNS ✓ Route ✓ TCP ✓ TLS ✗ (certificate expired)`, is this result.
- **Names.** A host name is resolved (IPv4 only, the A record) by the gateway's DNS proxy first and by the uplink's resolvers (`uplink.dns_upstream` of the configuration,
  else the host's) only when the proxy cannot be reached at all (nothing listens, no route); a slow or negative answer of the proxy is the answer and is not hidden by a
  second server (`Config.Diagnostics.Servers` replaces the list; the tests give their own). The address that was used is `address` in the result.
- **Certificates.** The roots are the system's and the test CA of the TLS responder (M21, read from the CA directory at every request, since it can be replaced), so
  a case signed by the test CA (expired, not yet valid, hostname mismatch) shows its own reason instead of an unknown authority, and the case `untrusted_ca` and
  `self_signed` show the unknown authority.
- **The host setup.** The API runs unprivileged in the host's network namespace; the ICMP datagram socket needs its group in `net.ipv4.ping_group_range`, a sysctl of the host.
  `deploy/host-setup.sh` writes `net.ipv4.ping_group_range=0 2147483647` next to `ip_forward` in `/etc/sysctl.d/90-chaos-gateway.conf` and sets it. A host set up before M24
  needs the script run again (it is safe to run again) or the sysctl set by hand; until then `ping` answers `cannot open an ICMP socket (…)` as its error and the other types work.
- **The screen** (`views/DiagnosticsView.vue`, `components/diagnostics/`, `lib/diagnostics.ts`, `stores/diagnostics.ts`): plan §2.17 "Diagnostics". The form chooses the type (a
  sentence says what it does) and shows the fields of that type; `lib/diagnostics.ts` `diagnosticErrors` mirrors the gateway's rules so that Run waits for a valid form, and the
  gateway's refusal is put under the field it names. The result card says succeeded or failed (a dot and a word), the sentence (the gateway's `error`, else what was measured),
  and the part of the type: the four steps of a path with their details (a step after the failed one is "not run"), the echoes, the connection, the answers of DNS, the status
  and the headers of HTTP, the TLS handshake with each certificate. The runs of the session (the last ten) are kept in the store and listed below; a click shows an earlier one.
  The device detail has **Diagnose** (a link to `/diagnostics?type=ping&host=<its address>`, for a device with an address); the form is filled in from that query. The `</> API` panel shows
  the request the form would send, and after a run the one that was sent.

### Decisions

Taken by best practice where the plan leaves room; none needs the maintainer:

1. **A target that fails is `200 ok: false`,** as the spec's response says ("also when the diagnosed target fails"); a `4xx` is a request the gateway cannot run. A client
   can tell its own mistake from the target's failure by the status.
2. **The `dns` type reads the spec's description as:** with `dns_name` the host is the server, without it the host is the name and the server is the gateway's proxy ("default the
   gateway DNS proxy"). `GET /dns/queries` shows the gateway's queries with the client `169.254.100.1`, which no device has.
3. **No redirects are followed and a status of 400 or more fails the check.** The diagnostic shows what the server answered; following a redirect would hide the 301 that is the
   answer. A client that wants the final page follows `Location`.
4. **`tls` fails (`ok: false`) when the certificate does not verify, and still returns the details.** The point of the type is to see what the server presented; a client
   that wants only the details reads `tls` and ignores `ok`.
5. **The ping interval is 200 ms,** the smallest an unprivileged `ping` may use, so that ten echoes take two seconds and not ten; the 60 s of the request decide the largest `count`
   with the timeout (rejected at the request, not cut silently).
6. **`from.probe` is `unsupported_feature` until M25;** the schema already has the field and the capabilities do not claim it.
7. **Every run is audited.** A diagnostic opens connections from the gateway to any address its caller names, so a run shows up in the audit log with its target; the scope
   `overlays` that the spec gives it is for automation tokens as well.
8. **The sysctl is written by the host setup** and not set by the container: the API shares the host's network namespace, where a container cannot set a sysctl. The value
   is the Docker and Ubuntu default (every group), not a narrower one, because the API's group is a numeric one that the host setup does not know.

### Tests

Level 0, run here. Go: `internal/diag` (against servers on the loopback, a DNS server of the test, certificates of a test CA, a scripted ICMP socket, a stack that blocks or has no route:
`connect_test.go` TCP and UDP and the resolution of names, `dns_test.go`, `http_test.go`, `tls_test.go`, `path_test.go`, `ping_test.go`; the ping of the loopback on the real stack skips with its reason
when the process cannot open an ICMP socket), `internal/domain/diagnostic_test.go` (the request and its refusals, the examples of `api/examples/diagnostic-requests.yaml`),
`internal/api/diagnostics_test.go` (every type over the API against its target, the TLS cases of the responder with the reasons they show, the test CA trusted and the unknown one not,
the failure of every type, the refusals with their fields, the scopes, eight at once and a ninth refused, a canceled request, the audit entry and the capabilities) with the response checked against the
spec like every test of that package, `misc_test.go` (the operation belongs to a milestone in the build), `internal/preflight/hostsetup_test.go` (the sysctl). Web unit tests (`make test-web`):
`lib/diagnostics.test.ts` and the route in `router.test.ts`. Playwright against the mocked API (`e2e/diagnostics.spec.ts`, 18 tests).

Level 1, **not run here** (no `/dev/kvm`, no namespaces in this container; CI runs them, the list `M24_LEVEL1`):
`internal/api/e2e_diagnostics_test.go` `TestEveryDiagnosticAgainstTheServerNamespaceOfTheTestbed` (the reference services in the server namespace — TCP, a UDP echo, HTTP, DNS, and TLS with the
responder's expired and mismatched certificates and one signed by the test CA — against which every type runs from the gateway's namespace, with a host nobody answers for, a closed port, a name
that does not exist, a 503 and the path of the plan's example) and `TestDiagnosticsFromTheGatewayDoNotPassThroughTheFaultsOfDevices` (a fault that drops everything of the test network stops a device's
ping and does not touch the gateway's).

### Acceptance map (plan M24 "Scope" and "Tests")

| Plan M24 | Test |
|---|---|
| Scope: ping with loss and RTT statistics | `TestPingCountsLossAndMeasuresTheRoundTrips`, `TestPingTheLoopbackOnTheRealStack`, `TestPingOverTheAPI`, level 1 `TestEveryDiagnosticAgainstTheServerNamespaceOfTheTestbed` |
| Scope: TCP and UDP check | `TestTCPReachesAnOpenPort`, `TestTCPSaysWhyAPortIsNotReachable`, `TestUDPNeedsAReply`, `TestUDPKnowsAClosedPortFromTheICMPErrorAndASilentOneFromTheTimeout` |
| Scope: DNS | `dns_test.go`, `TestEveryTypeOfDiagnosticRunsAgainstItsTarget` |
| Scope: HTTP(S) | `http_test.go`, `TestHTTPSVerifiesTheCertificate` |
| Scope: TLS details | `tls_test.go`, `TestDiagnosticsReportTheTLSCasesOfTheResponder`, `TestDiagnosticsTrustTheTestCAAndNotTheUnknownOne` |
| Scope: structured results (and the path of §2.12) | the result types in every test above; `path_test.go`, `lib/diagnostics.test.ts` (`the steps of a path`) |
| Scope: API (OpenAPI, generated clients) | `api/openapi.yaml`, `make check-spec`, `make check-clients`; the contract check of every request and response of `internal/api/diagnostics_test.go` |
| Scope: UI panel | `e2e/diagnostics.spec.ts` |
| Tests: each diagnostic against known testbed targets | level 0: servers on the loopback (`internal/diag`, `internal/api/diagnostics_test.go`); level 1: the server namespace (`e2e_diagnostics_test.go`, `M24_LEVEL1`) |
| Tests: failure cases | `TestTCPSaysWhyAPortIsNotReachable`, `TestTCPTimesOutWhereNobodyAnswers`, `TestANameThatDoesNotResolveIsAResultNotAnError`, `TestDNSFailuresAreResults`, `TestDNSTimeoutAndUnreachableServer`, `TestHTTPFailuresAreResults`, `TestHTTPStatusesFromAndAboveFourHundredFail`, `TestTLSCertificatesThatDoNotVerifyAreResultsWithTheirDetails`, `TestTLSHandshakeFailuresAreResults`, `TestPathStopsAtTheFirstStepThatFails`, `TestPathFindsNoRouteWhereThereIsNone`, `TestPingWithoutAReplyFails`, `TestDiagnosticsOfATargetThatFailsAreAnswersNotErrors` |
| The diagnostics of the gateway do not pass through device faults (§2.12) | level 1 `TestDiagnosticsFromTheGatewayDoNotPassThroughTheFaultsOfDevices` |
| The design is added to §2.17 | plan §2.17, the row "Diagnostics" of the table of screens; "Not yet designed" no longer lists it (probes stay) |

## Probes and calibration (M25)

Plan §2.12 and M25. A probe is a virtual client: a network namespace whose veth interface is a port of the bridge of a test network (spike S12), with an
address from the network's DHCP like a real device's. It is a device of the namespace (plan §2.3), so rules, faults and profiles target it, a diagnostic of M24
runs from it, and the self-test "measure this profile" compares what it measures with what the profile should produce within the tolerances of plan §4.3.
`GET /probes`, `POST /probes/{probeId}/calibrate`, `from.probe` of `POST /diagnostics`, the packages `internal/probe` and `internal/calibrate`, the executor operations
`probe_ns` and `probe_run`, and the Probes card of the Diagnostics screen.

### How it works

- **The probe in the kernel.** The configuration's `probes` (name, network, `trusts_test_ca`) compile to `Target.Probes` (`compiler.ProbeTarget`): the namespace `cgp-xxxxxxxx` and
  the gateway's end `pvxxxxxxxx` take the first eight hex digits of the probe's UUID (two probes whose ids start alike are a compile error, `probe_name_clash`), the end in the
  namespace is `eth0` with a MAC derived from the id (`02:` and five bytes of a hash: the same after a restart or a re-creation, so a lease or a reservation by MAC stays), the host name
  sent by the DHCP client is the probe's name as a DNS label. A probe of a network that has no bridge in the target (a WireGuard network, which validation refuses, or a network with an
  error) is left out. The plan (`apply.probeOps`) comes after the links step, when the bridges exist: a probe that is gone from the target is deleted; one that is missing, not a port of its
  bridge, down, with another MAC or without a DHCP client is ensured. `executor.ProbeNS ensure` runs fixed commands only (`ip netns add`, `ip link add pv.. type veth peer name eth0 address MAC
  netns cgp-..`, `ip link set master`, `ethtool -K .. gro off gso off tso off lro off` on both ends, the links up) and starts the client; `delete` stops the client and removes the pair and the
  namespace. The gateway's end is not among the assigned interfaces (it is not Chaos Gateway's in the sense of tc and routing: the traffic of a probe is classified on the bridge and the uplink like
  a device's, S12) and `apply.ReadState` takes it out of the links, so the preview and the verify of a bridge's ports do not see it; `Verify` checks the probes by `ReadProbes`
  (`probes: ...` mismatches). The executor refuses a bridge that is not assigned or is OS-owned, and a namespace or interface that does not follow the pattern.
- **The address.** The DHCP client is `internal/probe.Client`, a DHCPv4 client in Go (RFC 2131): DISCOVER, OFFER, REQUEST, ACK with the broadcast flag and the client id, the retry with the doubling wait,
  renewing by unicast at T1, rebinding by broadcast at T2, the loss of the lease when it runs out or the server says NAK, an infinite lease kept for ever. Its socket is UDP port 68 bound to
  `eth0` inside the namespace (`ListenDHCP`, `SO_BINDTODEVICE`, `SO_BROADCAST`), made on a thread that entered the namespace. The executor runs one client per probe in its own process
  (`probeClients`) and configures the interface with the lease: `ip addr replace` (the old address is deleted), the default route via the lease's router. A client that is gone (the executor
  restarted) is started again by the next apply (the read `probes` says `dhcp: false`). Why not `udhcpc` as the testbed's clients do: the production image has no busybox, and a client
  in Go is a unit of logic that level 0 tests with a scripted server and a fake clock.
- **Observation and identity.** `ReadProbes` (a read of the executor) lists the gateway ends with their namespaces, the MAC, the addresses and the lease; the engine feeds `Observed.Probes`
  (the MAC and the IPs by probe id) to the identity resolution that M6a prepared, so the probe's address is a claim of the device (and its MAC keeps the lease from becoming a "discovered"
  device); the device state has the probe's MAC, the lease of Kea and the sources `probe`, `dhcp`. `GET /probes` is the devices of the origin `probe` joined with their configuration.
- **A diagnostic from a probe.** `POST /diagnostics` with `from.probe` (a probe of the active revision, by id or name) asks the executor for a `probe_run` of the kind `diagnostic`: the
  executor runs `diag.Runner` on `diag.HostNet{Namespace: cgp-..}` because the API (unprivileged) cannot enter a namespace. The request carries the DNS servers of the probe's network (its
  gateway address) as the fallback, the executor prefers the servers of the probe's lease, as a device does; the roots of the certificate check are the system's plus the test CA only
  when the probe has `trusts_test_ca`. The result says `from: probe:cgp-xxxxxxxx`. A probe that does not exist is `404 not_found`, one that is not built or has no address is `503
  unavailable` (the reason names DHCP).
- **The self-test.** `POST /probes/{probeId}/calibrate` (scope `overlays`, audited as `probe.calibrate`): (1) the request is decoded (`domain.DecodeCalibrationRequest`; the code
  `invalid_calibration` is a host that is no IPv4 address; the others are `mutually_exclusive` for a profile and a fault together, `invalid_duration` for a duration outside 5 s to 5 min,
  and the codes of the impairment validation for a `fault`), the probe and the profile are looked up, one calibration of a probe runs at a time (`409 target_busy`); (2) the unimpaired
  reference: 100 UDP datagrams, 10 ms apart, from the **gateway's own namespace** to the same echo server (`probe_run` of the kind `echo` without a probe): the faults of a device do not
  touch the gateway's traffic (plan §2.12), so this is the path without any fault, wherever the probe's faults come from; (3) with a `profile` or `fault`, an overlay owned by the caller
  targets the probe (a TTL of the duration plus three minutes as the net behind the explicit removal), `PutOverlay` returns when it is applied and verified; without either, the
  expectation is the impairment that wins for the probe's traffic to the echo server now (`domain.World.Resolve`); (4) the probe sends numbered datagrams, one every 10 ms (at most 20 000;
  2000 in the 20 s the loss statistic needs) and times the answers; with a rate limit in the expectation the last third of the duration is a TCP echo loaded as fast as the path allows
  (`probe.Rate`: the bytes that come back in the last three quarters of the run); (5) the overlay is removed whatever happened, also when the caller has gone; (6) `calibrate.Evaluate`:
  - **delay**: the median of the round trips minus the reference median is the added delay; it must lie within ±(2 ms + 5 % of the configured added delay) of the sum of the delays of the two
    directions (upload and download are both on the round trip);
  - **jitter**: the 95th percentile minus the median is at most the sum of the two jitters plus the delay's tolerance;
  - **loss**: the number of datagrams without an answer must lie in the 99.9 % acceptance interval of the binomial distribution of the datagrams sent and the round trip loss `1 - (1-p_up)(1-p_down)`
    (exact, by the summed probability mass); fewer than 2000 datagrams are said to be indicative only; correlated loss (`loss_correlation`, `burst_loss`: the steady-state share of the Gilbert-Elliott
    model) gets the wider tolerance of ±max(2 points, half the expected loss), because bursts are not independent; flapping has no rate and is not judged;
  - **rate**: the throughput of the TCP echo within ±10 % of the smaller limit of the two directions;
  - reordering, duplication and corruption are named in `detail` as not measured. `loss_ci99` is the 99 % Wilson interval of the measured loss, the verdict uses the 99.9 % interval of the expectation.
- **The echo server** is an RFC 862 echo (UDP and TCP on one port, 7 by default) at an IPv4 address that lies beyond the gateway: the faults of the upload act on the egress of the uplink (or of
  another test network's bridge), so an echo on the probe's own bridge would see none of them. The testbed's server namespace has one (`testbed.StartPlainEcho`). The gateway runs none
  of its own.
- **The screen.** Plan §2.17 "Probes": the card on the Diagnostics screen (`components/diagnostics/ProbesCard.vue`), the choice *From* in `DiagnosticForm.vue` (the query `?from=<name or id>`), the dialog
  `CalibrateDialog.vue` (`lib/calibration.ts` mirrors the gateway's checks), **Measure with probe** on the profile cards (`ProfilesView.vue`). The probes are read with the key `['devices', 'probes']`, so the events
  that change a device read them again; the card and the button are there only when `GET /capabilities` lists the feature `probes`.

### Decisions

Taken by best practice where the plan leaves room; the one that needs the maintainer is P6-M25-01 in `docs/open-items.md`:

1. **The client is in Go and runs in the executor,** not `udhcpc` in a process per probe: no new package in the image, and the lease logic is tested at level 0.
2. **The probe's MAC is derived from its id,** not random per creation: a probe keeps its lease and any reservation across restarts.
3. **The gateway's end of the pair is not an assigned interface** and is not among the ports of the bridge in the configuration's sense: probes come and go with the configuration and are read by
   their own read, so the preview of a bridge shows the ports of the configuration only.
4. **The unimpaired reference is the gateway's own echo,** not a measurement before the overlay: the probe may have faults of its own before the calibration, and the gateway's traffic never meets them.
5. **The verdict of the loss uses the binomial interval of the expectation (plan §4.3),** and the field `loss_ci99` of the spec stays the 99 % interval of the measurement; both are in the answer.
6. **The datagram interval is fixed at 10 ms** (100 per second): 2000 datagrams in 20 s; a duration under 20 s still runs and is marked indicative.
7. **A calibration needs the scope `overlays`** like the overlays it makes; a token with that scope removes its own overlay (the engine removes it with no owner limit, as the call made it).
8. **The probe_run operation is a read for the executor** (it changes nothing in the kernel): it runs beside the writer on a reader slot and does not wait for an apply that is running.
9. **Probes are configured, not created by a dedicated operation:** the spec has `probes` in the configuration and no `POST /probes`; the web UI lists, diagnoses and measures them (P6-M25-01 is the form).

### Tests

Level 0, run here. Go: `internal/probe` (the DHCP client against a scripted server and a fake clock: the four messages, the retry with the doubling wait, renewal by unicast at T1, rebinding by
broadcast at T2, the loss of the lease and the new discover, NAK, an infinite lease, the server's T1 and T2, messages that are no replies; the real socket on the loopback; the echo and the rate
measurement against servers on the loopback with a delay, a loss and a throttle), `internal/calibrate` (the expectation of flat and per-direction parameters, burst loss, flapping, the exact binomial interval
against the normal approximation and by simulation, the Wilson interval, every verdict: inside and outside of each tolerance), `internal/executor/probe_test.go` (the operations decoded strictly, the commands of
`probe_ns`, the scope, the read, the client's start and stop, the lease configuring the interface, the diagnostic with the servers of the lease, the runs from the stack of a probe and beside the writer),
`internal/apply/probes_test.go` (the plan, the verify, the healing and the removal against the simulated kernel), `internal/domain/calibration_test.go`, `internal/api/probes_test.go` (the list, paging, the
address and the device, a diagnostic from a probe in the namespace the simulated kernel has, the test CA only for the probe that trusts it, the self-test of a delay, a loss and a rate against a stack
that delays, drops and throttles like netem, the current faults, every refusal, one calibration at a time, the scopes, the audit entry), `misc_test.go` (the operations belong to M25 in the build). Web unit tests
(`make test-web`): `lib/calibration.test.ts`, `lib/probes.test.ts` and the probe cases of `lib/diagnostics.test.ts`. Playwright against the mocked API (`e2e/probes.spec.ts`, 14 tests).

Level 1 (the list `M25_LEVEL1`), run on the emulated VM here (kernel 6.8.0-142, software emulation: the accuracy assertions of §4.3 did not run, the functional ones did; CI runs them natively):
`internal/engine/integration_probes_test.go` against the real Kea (`TestAProbeGetsALeaseFromKeaAndReachesTheGatewayADeviceAndTheServer`, `TestAProbeSurvivesApplysAndIsRemovedWithItsConfiguration`,
`TestFaultsAndRulesTargetTheProbeAndNobodyElse`, `TestTheEchoRunOfAProbeSeesTheDelayOfItsFaultAndTheGatewayDoesNot`) and `internal/api/e2e_probes_test.go` over the API
(`TestAProbeRunsDiagnosticsAgainstTheServerOnTheRealKernel`, `TestAFaultOnTheProbeIsSeenByItsDiagnosticsAndNotByTheGatewaysOnTheRealKernel`, `TestTheMeasurementOfAProfileMatchesItsParametersOnTheRealKernel`: the delay, the
loss and the rate of a profile within the tolerances under KVM or native execution, recorded in the measurement record like the other accuracy assertions).

### Acceptance map (plan M25 "Scope" and "Tests")

| Plan M25 | Test |
|---|---|
| Scope: virtual clients as bridge ports of test networks (S12) | `TestAProbeIsABridgePortWithItsOwnNamespaceAndMAC`, `TestAProbeIsMadeFromFixedCommandsAndItsBridgeMustBeAssigned`, level 1 `TestAProbeGetsALeaseFromKeaAndReachesTheGatewayADeviceAndTheServer` |
| Scope: DHCP address | `internal/probe/dhcp_test.go`, `TestTheLeaseOfTheClientConfiguresTheProbesInterface`, level 1 `TestAProbeGetsALeaseFromKeaAndReachesTheGatewayADeviceAndTheServer` (Kea's lease with the host name) |
| Scope: targeting by rules/faults | level 1 `TestFaultsAndRulesTargetTheProbeAndNobodyElse`, `TestAFaultOnTheProbeIsSeenByItsDiagnosticsAndNotByTheGatewaysOnTheRealKernel` |
| Scope: diagnostics of M24 run from a probe | `TestADiagnosticFromAProbeRunsInTheProbesNamespace`, `TestAProbeTrustsTheTestCAOnlyWhenItSaysSo`, `TestAProbeRunMeasuresFromTheProbesStack`, level 1 `TestAProbeRunsDiagnosticsAgainstTheServerOnTheRealKernel` |
| Scope: self-test "measure this profile" | `internal/calibrate`, `TestAProfileIsMeasuredOnTheProbeAndTheOverlayIsRemoved`, `TestLossOfAFaultIsComparedWithItsBinomialInterval`, `TestARateLimitIsMeasuredWithATCPEcho`, `e2e/probes.spec.ts` |
| Tests: a probe reaches the gateway address, devices and the server | level 1 `TestAProbeGetsALeaseFromKeaAndReachesTheGatewayADeviceAndTheServer`, `TestAProbeRunsDiagnosticsAgainstTheServerOnTheRealKernel` |
| Tests: its measurement matches the configured profile within the §4.3 tolerances | level 1 `TestTheMeasurementOfAProfileMatchesItsParametersOnTheRealKernel` (delay, loss and rate; accuracy under KVM or native), `TestTheEchoRunOfAProbeSeesTheDelayOfItsFaultAndTheGatewayDoesNot`; level 0 `TestAProfileThatDoesNotProduceWhatItSaysFailsTheSelfTest` |
| The faults of a device do not touch the gateway's diagnostics (§2.12) | level 1 `TestFaultsAndRulesTargetTheProbeAndNobodyElse` (the gateway's ping next to the probe's), `TestTheEchoRunOfAProbeSeesTheDelayOfItsFaultAndTheGatewayDoesNot` |
| The design is added to §2.17 | plan §2.17, the row "Probes"; "Not yet designed" no longer lists them |

## Metrics and flow view (M26)

Plan §2.12, §2.17 and M26 (optional for V1, built at the maintainer's request). `GET /metrics` is the Prometheus endpoint (the package `internal/metrics` writes the text format, `internal/api/metrics.go`
collects the values); the Flows screen lists the connections the gateway tracks; the device detail has the panels *Path*, *Flows* and *Recent DNS queries*; the explain card of the Faults screen takes an
address behind the uplink or the management network as its source. It closes `docs/open-items.md` P3-M12-02 (a device's flows and recent DNS queries), P3-AUDIT-37 (the path view) and
P31-M35a-08 (the explain card's source).

### How it works

- **The endpoint.** `GET /metrics` (scope `read`, so a scraper needs an API token; `Content-Type: text/plain; version=0.0.4`) answers from the engine's snapshot and the kernel at the moment of the
  request: nothing is kept between two scrapes, no client library is linked (`internal/metrics.Set` collects the series of one scrape and writes them sorted, with the escapes of the format, and reports a
  name or label the format does not allow instead of writing it). The spec lists every family under `/metrics`. All names start with `chaosgw_`:
  - **Counters of M7 to M9 and M35a** are read by the code that serves them over the REST API (`readOverlayContext`, `counterOf`, `withDNS`, `withTLS`, `ruleCounter`, `forwardCounter`), so the two
    cannot disagree: `chaosgw_fault_{packets,bytes}_total{layer,id,kind,name}` per configured fault (`layer="config"`, `kind` is the family) and per overlay (`layer="overlay"`, `kind` the overlay's kind:
    fault, profile, dns, tls, wireguard), `chaosgw_rule_{packets,bytes}_total{layer,id,name,action}` per access rule of the compiled plan (configured and overlay rules) and for the anti-lockout rule
    (`layer="system"`), `chaosgw_forward_{connections,bytes}_total{id,name,entry}` per forward in the kernel, and `chaosgw_counter_epoch`, the epoch `GET /state` reports (a counter that starts over is a
    counter reset for Prometheus; the per-object epochs stay in the REST API).
  - **Queues.** `chaosgw_queue_{sent_packets,sent_bytes,dropped_packets,overlimits}_total` and the gauges `chaosgw_queue_{backlog_packets,backlog_bytes,epoch}` per netem leaf (`fault_id`, `layer`, `source`,
    `interface`, `direction`), from `Engine.ReadQueues` with the epoch the engine tracks (the label is the fault id, which is unique per leaf; an overlay and a profile can have several leaves).
  - **Interfaces.** `chaosgw_interface_up` and `chaosgw_interface_{receive,transmit}_{bytes,packets,errors,dropped}_total{interface,role}` for the interfaces the last apply made or uses (`role` uplink,
    management, network for a bridge, wireguard): one executor read `links` (`ip -j -s -s link`) in the gateway's namespace, whose `stats64` the package `internal/linux` now parses (`linux.LinkStats`; the
    API container does not necessarily share the namespace of the interfaces, so it is not `/sys/class/net`).
  - **Devices and totals.** `chaosgw_device_online`, `chaosgw_device_{upload,download}_bytes_per_second` and `chaosgw_device_flows_active` (the last three while the traffic is known, as in `DeviceView`) with
    `device`, `name`, `network`, `origin`; `chaosgw_devices`, `chaosgw_devices_online`, `chaosgw_flows_active`, `chaosgw_overlays_active`, `chaosgw_leases`, `chaosgw_runs_active`, `chaosgw_runs_queued`.
  - **System.** `chaosgw_health_ok` and `chaosgw_component_status{component,status}` from the same check as `GET /system/health` (`healthReport`, split out of the handler), `chaosgw_system_cpus`,
    `chaosgw_system_load{1,5,15}` and `chaosgw_system_memory_{total,available}_bytes` (from `/proc`, `Config.ProcDir` for tests), `chaosgw_disk_{free,total}_bytes{volume}` for the state volume and the capture
    volume (statfs, or the capture manager's own reading), `chaosgw_build_info`, `chaosgw_start_time_seconds`, `chaosgw_uptime_seconds`, `chaosgw_revision`, `chaosgw_apply_ok`.
  - **`chaosgw_collector_up{source}`** is 0 for a source that could not be read at this scrape (`counters`, `queues`, `interfaces`); the series that come from it are left out then and are never reported as 0,
    so an alert on a rate does not see a drop to zero that was a failed read. The feature `metrics` is in `GET /capabilities`.
- **Flows.** `GET /flows` lists the connections of the test networks (M6a). Since M26 a connection that came in through a port forward (M35a) is listed as well, as a flow of the forward's target device with
  `forwarded: true`: its `src` is the peer behind the entry side, its `dst` the entry side's address and its `dport` the external port, which is what `GET /explain` needs to explain it
  (`engine.Flows` takes the reply's source as the target, as the rate computation does). A connection to the gateway's own ports that no forward translated is still no flow.
- **The Flows screen** (`views/FlowsView.vue`, `components/flows/ConnectionTable.vue`, `lib/flows.ts`). The table is read every 3 s (`useFlows`; Pause holds what is shown) and sorted newest first by the start
  conntrack noted. The device and the network filter on the gateway's side, the text (addresses, host name, ports, service), the protocol and "only forwarded connections" here; all five are in the address, so
  the link *All flows* of the device detail lands on the device's connections. A row shows the service (the well-known port's name, `MQTT/TLS`, `NTP`, else the protocol), the device (a link), the source, the
  destination (the name the DNS proxy returned for it, else the address, with the port), the state, the bytes in both directions, the age and *Explain*. Up to 200 rows are drawn, 200 more with *Show more*.
- **The device detail.** *Path*: the device, the gateway, the uplink and one destination of the device's connections (`pathTargets`: the one that moved the most first, a forwarded connection and a protocol that
  cannot be explained left out; a choice when there are several), and `GET /explain` for the device and that destination, address, protocol and port (key `['explain', ...]`, read every 5 s and on the events that
  change faults or the revision). The fault that wins in each family stands as a badge on the line out of the device ("2 faults", or the single fault's summary); the line is blue when nothing acts, orange while
  a fault does, red while the access verdict drops or rejects the connection, and a sentence under it says the same in words ("No fault acts on the way: …", "A fault acts on the way from … to …", "Dropped by a
  configured rule: … cannot reach …"). *Flows*: the eight newest connections of the device, a forwarded one marked *FORWARDED* with its peer, and the link to all of them. *Recent DNS queries*: the ten newest the
  DNS proxy logged of the device (`GET /dns/queries?device=&limit=10`), with the type, the answer, the time it took, an answer a DNS fault changed in orange with "changed by a DNS fault", and one that is no
  answer (NXDOMAIN, SERVFAIL, a timeout) in red with its code.
- **The explain card.** *Source*: a device, an address behind the uplink, an address behind the management network (offered only when a host interface is assigned the role management: in the two-port topology
  the management network is behind the uplink), or another address. For the two entry sides the destination defaults to the address of the entry interface (`GET /uplink`, `GET /system/interfaces`), until it is
  edited, and a sentence says that a connection a port forward carries comes from a peer behind that side to that address on the forward's external port. The source address is checked as an IPv4 address. *Explain* on a
  row of the Flows screen opens the Faults screen with the card filled in (`?explain=1&device=|src=&dst=&protocol=&port=`), explains at once and scrolls to the card; a source address whose destination is the
  uplink's or the management interface's address is shown as "an address behind the uplink" (or management) once those addresses are known.

### Decisions

Taken by best practice where the plan leaves room; none needs the maintainer.

1. **The text format is written here, not by `client_golang`:** the endpoint keeps no state, so there is nothing to register, and no dependency is added to an appliance image for two hundred lines.
2. **A series that cannot be read is left out, not zero,** and `chaosgw_collector_up` says which source failed; a gap is what Prometheus alerts on correctly.
3. **Labels are the id and the name, not only one:** the id is stable and unique, the name is what a dashboard shows. A label with an empty value is not written.
4. **Overlays of every kind count under one family per thing (`chaosgw_fault_*`) with `kind`,** and rules under `chaosgw_rule_*`: a query "all packets that faults impaired" is one sum, and the REST API's `counters` of the same objects are the same numbers.
5. **The interface counters are the interfaces the gateway applied,** not every interface of the machine: a metric per veth of every namespace would be a series per probe and capture that nobody asked for.
6. **The endpoint needs a token (`read`).** The health check is the one unauthenticated endpoint (for the container runtime); metrics name devices and faults.
7. **A forwarded connection is a flow of the target device,** with the peer as its source, rather than a separate list: the device's rates already count it for the target (M35a), and the flow view is where it is picked.
   The scenario runs (M15, M16) do not treat it as the device's own: `flowInScope` skips a forwarded flow, because a check on "the device's connections" means the ones the device starts, not the ones a peer starts to it.
8. **No per-device DNS query metric:** the DNS proxy keeps a bounded log of queries, not a counter, so a count over it would shrink as the log rotates and look like a counter reset. §2.13's "DNS queries" stay with `GET /dns/queries` and the Recent DNS queries card; a counter in the proxy would be a change of M6b and is not part of M26.
8. **The path view asks about an address, not a host name:** a flow has the address; a hostname fault acts on the addresses the DNS proxy resolved (the DNS-derived sets), which the explanation covers.
9. **The badge sits on the way out of the device,** where the gateway impairs the traffic (the prototype's place), and shows the winner per family; "2 faults" when several, the list under the line.
10. **The Flows screen is polled (3 s) like the Interception screen,** not driven by events: connections come and go without one.

### Tests

Level 0, run here. Go: `internal/metrics` (the format: sorting, escapes, whole numbers, what is refused), `internal/linux` (`TestParseLinkInterfaceCounters`), `internal/api/metrics_test.go`
(the counters of a configured fault, an overlay, an access rule, the anti-lockout rule, a forward, the queues and the interfaces against the values the simulated kernel is given and against the REST API's
views; the rise after more traffic; the device, the totals, the health, the system numbers from a fake `/proc`; the overlay kinds; the token; the sources that fail; a forwarded connection as a flow and
its explanation), the list of operations in `misc_test.go` (`/metrics` belongs to M26 in the build), `tools/ciconfig` (the list `M26_LEVEL1` in every testbed job). Web unit tests (`make test-web`):
`src/lib/flows.test.ts` (names, filters, the explain request and its address, the destinations of the path, the badge, the DNS rows) and the route in `router.test.ts`. Playwright against the mocked API
(`e2e/flows.spec.ts`, 19 tests): the Flows screen (the rows, the filters and the address, Pause, the empty and the failed state), the device detail (its flows, a forwarded one included, the more-link, its DNS
queries with an answer a fault changed and one that failed, the empty and failed states), the path view with and without a fault on it (blue, orange with the badge, red when a rule drops, another destination,
a fault that appears), and the explain card (an address behind the uplink and the management network, the validation, no management source in the two-port topology, a connection picked in the flow view
explained at once, for a device and for a forwarded one).

Level 1 (the list `M26_LEVEL1`): `internal/api/e2e_metrics_test.go` (`TestMetricsCountKnownTrafficOnTheRealKernel`: ten echo requests of a device through a fault, and the counters of the fault, its queues and the
interfaces of the bridge and the uplink against them). It ran in the emulated VM of the development machine (kernel 6.8.0-142, software emulation: 292 s) and passed; CI runs it natively, the nightly matrix also on 7.0.0-38.

### Acceptance map (plan M26 "Scope" and "Tests")

| Plan M26 | Test |
|---|---|
| Scope: Prometheus metrics endpoint (counters from M7–M9, interface and queue statistics, system health) | `TestMetricsReportTheCountersOfFaultsRulesForwardsAndQueuesAgainstKnownTraffic`, `TestMetricsCountOverlaysByKind`, `TestMetricsLeaveOutWhatCannotBeReadAndSayWhichSourceFailed`, `TestMetricsNeedAToken`, level 1 `TestMetricsCountKnownTrafficOnTheRealKernel` |
| Scope: flow view in the UI (flow data from M6a) | `e2e/flows.spec.ts` (the Flows screen, its filters, Pause), `lib/flows.test.ts`, `TestAForwardedConnectionIsAFlowOfTheTargetDevice` |
| Scope: a device's flows and recent DNS queries on the device detail (P3-M12-02) | `e2e/flows.spec.ts` (the device detail's panels) |
| Scope: the explain card's source behind the uplink or the management network, destination defaulting to the entry side's address, a forwarded connection picked in the flow view explained (P31-M35a-08) | `e2e/flows.spec.ts` (the explain card), `TestAForwardedConnectionIsAFlowOfTheTargetDevice` (the flow and its explanation) |
| Scope: the device detail's path view with the fault badge (P3-AUDIT-37) | `e2e/flows.spec.ts` (the path view) |
| Tests: metric values against known traffic | `TestMetricsReportTheCountersOfFaultsRulesForwardsAndQueuesAgainstKnownTraffic` (level 0, the simulated kernel's counters), level 1 `TestMetricsCountKnownTrafficOnTheRealKernel` |
| Tests: Playwright for the flow view, the device detail's flows and recent DNS queries, and the path view with and without a fault | `e2e/flows.spec.ts` |

## Generated code

`api/openapi.yaml` is the source of truth (spec first). `make generate` creates:

| Output | Generator | Committed |
|---|---|---|
| `internal/model/model.gen.go`, `internal/apiserver/server.gen.go` | `go tool oapi-codegen` (version pinned in `go.mod`), configs `api/oapi-codegen-model.yaml` and `api/oapi-codegen-server.yaml` | yes; `make check-generated` fails when they are stale |
| `web/src/api/generated/` | Orval: Vue Query hooks, Zod schemas | no |
| `clients/typescript/src/` | Orval: plain fetch client | no |
| `clients/python/chaosgw-client/` | openapi-python-client (version pinned in `tools/requirements.txt`) | no |

`make check-spec` validates the spec and the examples; `make check-clients` generates the web and
Python clients and checks that they compile and import. CI runs all three.

## CI

`.github/workflows/ci.yml`: level 0 with the generated-code checks and the example suites of `clients/examples` (`make test-examples`, M18), the Playwright tests, the
testbed tests in a VM and in a privileged container, the browser flows of the web app against the real stack
(`testbed-ui`, a privileged container that builds the app and installs Chromium first; the flows of M12, M13 and M14), the arm64 job (multi-arch image build,
unit tests under `qemu-user`) and the job `tlsproxy-image` (the image is built and the container of the interception proxy starts from its compose file: `deploy/smoke-tlsproxy.sh`, M22). Where the hosted runner offers `/dev/kvm`, the VM job uses it; where
it does not, it runs emulated. KVM-dependent tests run on hosted GitHub runners (plan D33).

The privileged-container job mounts the runner's `/lib/modules` and runs the tests directly
(`make test-privileged`); it does not fall back to a VM, so a hosted kernel without netem would
fail it visibly.
The privileged job's `go test` timeout is 60 minutes (`ARGS='-test-timeout 60m'`): the first run of M10 stopped
the engine package at the default 20 minutes with every test up to then passing (234 of 256 tests
had started), so the limit, not a test, was the failure.
The VM job gives the package 100 minutes (`-test-timeout 100m -vm-timeout 3h`): under KVM the first
124 tests took 25 minutes, so the 256 of M10 need about 50. The same run showed a hang of its own, not
of a limit: `parallel` of the iperf3 rate test called `t.Fatalf` from goroutines, which ends the goroutine
without the signal the wait is for, so an iperf3 that failed at the end of an upload through a 2 Mbit/s
queue (`unable to receive results`) kept the test until the 60 minute timeout. The transfers now return
their error, and a failure of the tool repeats the three transfers once after the queues drained.

Both testbed jobs and the nightly kernel matrix pass `-expect-pass $M35A_LEVEL1,$M15_LEVEL1,$M16_LEVEL1,$M17_LEVEL1,$M18_LEVEL1,$M20_LEVEL1,$M21_LEVEL1,$M22_LEVEL1,$M23_LEVEL1,$M24_LEVEL1,$M25_LEVEL1,$M26_LEVEL1,$I64_LEVEL1,$KEA_LEVEL1,$I79_LEVEL1`. The lists are in one file, `.github/level1-tests.env`, which each of these jobs loads into its environment in a step of its own (P4-AUDIT-49: the nightly matrix ran the same tests without the list, so a test that skipped on one kernel stayed green). The level 1
tests of port forwarding (with those of the Phase 3.1 audit), those of scenario runs (M15), those of checks and zero hits (M16), those of packet captures (M17: the AF_PACKET sockets
need CAP_NET_RAW, so their tests carry the build tag `testbed`), those of `chaosctl` (M18), those of the DNS faults and hostname selectors (M20, with the executor's netlink connection), those of the TLS responder (M21), those of the interception proxy (M22, with the real mitmproxy, which the level 0 job installs too and requires), those of the DHCP test actions (M23, udhcpc and the real Kea), those of the probes and the calibration (M25, the real Kea, the real executor and the real netem), the one of the metrics (M26), those of Kea's packet socket after a link flap
(issue #64), the tests against the real Kea (I64-05) and those of the observed state of a device (the acceptance of
issue #79, issue #92) must each pass, so one that is renamed, skips or is no
longer built fails the job. `tools/ciconfig` checks that every job passes every list of the file and that the list quoted here names them all
(`TestEveryTestbedRunNamesTheLevel1Lists`, `TestTheDevelopmentGuideQuotesEveryLevel1List`, P5-AUDIT-42).

The fresh-volume check of the `arm64` job (issue #57) runs the image as user 65532 against a new named volume for every directory the API container mounts writable: state, secrets, api, service, captures (the API makes the directory of every capture, M17, and the executor writes into it), tlsca (the CAs of the TLS responder, M21) and the Kea events directory; `docs/install.md` has the same list in its upgrade note.

What the first CI run (2026-10-02) showed about hosted `ubuntu-24.04` runners: they offer
`/dev/kvm`, so the VM job ran with `accurate=true` (a topology builds in about 7 s instead of 70 s,
a 50 ms netem delay measured 50.2 ms), and their kernel has the modules, so the privileged job ran
the testbed tests directly in 12 s.

### Image workflow

`.github/workflows/image.yml` builds the multi-arch image (amd64, arm64) and pushes it to
`ghcr.io/andste82/chaos-gateway`.

- **Manual:** Actions > Image > Run workflow, or `gh workflow run image.yml -f tag=test` (add
  `--ref <branch>` for a branch). It pushes `:<tag>` and `:sha-<commit>`; with `push` off it only
  builds. Use it for test containers.
- **Release:** pushing a tag `v1.2.3` pushes `:1.2.3`, `:1.2`, `:1` and `:latest` (no `:0` for `v0.x`).
- The first push creates the package as private; make it public under the package settings on
  GitHub if it should be pulled without login. Pulling a private image needs
  `docker login ghcr.io` with a token that has `read:packages`.
- The workflow needs no secret: it logs in with the job's `GITHUB_TOKEN`.

For a host without the registry, `make image-local` builds the image for the machine's own architecture with
the version build arguments, loads it and tags it `chaos-gateway:latest` (what the compose files use) and
`chaos-gateway:$(VERSION)`; `make image` is the multi-arch build without a local image (issue #57 F7,
`TestMakeImageLocalBuildsAVersionedLocalImage` checks the command `make -n` prints).

The image holds `chaosgw`, `chaosctl`, the pinned Kea and BIRD and the interception proxy (mitmproxy in its own virtualenv `/opt/chaosgw/tls-proxy/venv` with the addon of `sidecars/tls-proxy`, M22, run by `deploy/compose.tlsproxy.yaml`); M28 adds tcpdump and the rest of the container deployment.
