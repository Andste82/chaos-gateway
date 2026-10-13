# Development

How to build, test and generate code. Everything runs in the devcontainer
([`.devcontainer/`](../.devcontainer/)); `make help` lists the targets.

## Layout

| Path | Content |
|---|---|
| `api/` | `openapi.yaml`, the source of truth for the domain model and the REST API; examples and their validator |
| `cmd/chaosgw`, `cmd/chaosctl` | the core binary and the CLI (`chaosgw exec` is the executor; the other subcommands and the CLI are skeletons until M5/M6b/M18/M21) |
| `internal/clock` | injectable clock: real and fake, wall time apart from monotonic time |
| `internal/preflight` | kernel version, the one shared kernel-module list, namespace capability |
| `internal/testbed` | namespace topologies for integration tests; `vmrun` runs them in a VM |
| `internal/model` | generated Go types of `api/openapi.yaml`: the domain model (no hand-written code) |
| `internal/schema` | validates JSON/YAML documents against the schemas of the spec: pointers, codes, unknown fields |
| `internal/domain` | what the model means: decoding, reference resolution, the rules the schema cannot express, built-in profiles, precedence resolution and the classification tables built from it, overlays and their keys, the overlays a revision orphans or moves, observed state and device identity, candidate creation (merge patch), domain diff |
| `internal/overlay` | the in-memory overlay store (M8a): owner, key, TTL, lease, renew, reset, expiry on the injected clock, retargeting after a merge; never persisted |
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
| `internal/kea` | the Kea DHCPv4 server: configuration renderer (one subnet per network on its bridge, reservations), control socket client (`config-test`, `config-set`, leases), the `run_script` hook's environment as a lease event |
| `internal/wireguard` | key generation and derivation, provisioning of a configuration's keys, export of client and link configurations (wg-quick `.conf`, QR as PNG/SVG, zip, export once) |
| `internal/apiserver` | generated Gin server interface (imports the model; the handlers follow in M5) |
| `tools/testvm` | runs the testbed tests: directly or in a VM |
| `web/` | the web app (M12): Vue 3, Vite, Tailwind 4, TanStack Query, Pinia, Reka UI, Vue Router, uPlot; `web/embed.go` embeds its build into `chaosgw` (tag `webui`) |
| `web/e2e`, `web/e2e-testbed` | Playwright: the screens against a mocked API, and the browser flows against the real stack in the testbed |
| `clients/` | generated TypeScript and Python clients (not committed) |
| `deploy/` | the container image (multi-arch); `compose.executor.yaml`, the executor's hardening profile |

## Setup

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
| – | `make test-e2e` | Playwright tests of the web app | `npx playwright install chromium` once |
| 1/1b | `make test-vm ARGS='-tags testbed,browser -run InTheBrowser ./internal/api'` | the browser flows of the web app against the real stack in the testbed: first start (M12), a fault created in the UI and measured (M13) (also `test-privileged`) | `make build-web`, `cd web && npm ci && npx playwright install chromium` |

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
to gate.

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
  failure followed by a panic.
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
that make sense for a booted VM (`-run`, `-tags`, `-test-timeout`, `-keep`, `-allow-skip`, `-measurements`).
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

All operations the compiler needs are implemented (`links`, `sysctl`, `wireguard`, `bird` and
`service_ns` followed in M4-M6b). Still deferred: the persistent netlink connection for
DNS-derived set updates (`nft_add_elements` starts one `nft` per call until M20). The reader pool
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
| `pool_overlap` | Two DHCP pools of the same network overlap. |
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
| `invalid_port_range` | A port range's end comes before its start. |
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
| `invalid_step_reference` | A scenario step's `remove` names a step that does not exist, created no overlay, or runs later. |
| `target_widened` | A step's `target` is broader than the scenario's own target. |
| `invalid_target` | A scope is not valid where it is used (wrong kind for that context). |
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
| `invalid_dhcp_action` | A DHCP action's fields do not match (`lease_time` only for `short_lease`, `options` only for `set_options`). |
| `invalid_overlay` | An overlay request names zero, or both, of `client` and `link`. |
| `invalid_flapping` | A flapping fault's `up` or `down` is zero. |
| `invalid_rate` | A fault's `rate` is below 8 bit/s (netem limits in bytes per second: such a rate would not limit at all). |
| `invalid_name` | Reserved; not produced yet. |
| `duplicate_protocol` | A link already runs a routing protocol of the same type. |
| `missing_routing_settings` | Reserved; not produced yet. |

`TestEveryValidationCodeIsDocumented` in `internal/domain` checks that every `Code*` constant's
value appears somewhere on this page.

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
active revision. In the testbed `--namespace` points it at the gateway namespace.

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
  then 5 s doubling to 15 min, `429` with `Retry-After`).
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
  Logs (cloud-init, journal, Docker, nftables, console, QEMU) are collected into the artifacts
  directory even when the test fails.
- **Running it.** It needs root (taps, bridges, namespaces; the harness uses `sudo -n`), QEMU, `/dev/kvm`
  (without it the tests skip; `CHAOSGW_APPLIANCE_ALLOW_TCG=1` runs them emulated and very slowly) and
  the image as `docker save | gzip` in `CHAOSGW_APPLIANCE_IMAGE_TAR`. The nightly workflow's job
  `appliance` does all of that on a hosted runner (plan D33: they offer `/dev/kvm`); run it on a branch
  with `gh workflow run nightly.yml --ref <branch>`. `make test-appliance` is the same on a machine
  that has the prerequisites. The development VPS has no KVM: everything that needs none is unit-tested
  there (image verification, the cloud-init documents, the QEMU arguments, the topology plan).
- **Host setup.** `deploy/host-setup.sh` is shipped in the image
  (`/usr/local/share/chaosgw/host-setup.sh`): it installs Docker and the compose plugin when missing
  (Ubuntu's packages), writes `/etc/modules-load.d/chaos-gateway.conf` and
  `/etc/sysctl.d/90-chaos-gateway.conf`, loads the modules and enables forwarding now, tries
  `linux-modules-extra` when modules are missing, and prints the netplan hints. It never touches the
  uplink or management configuration. `internal/preflight` tests that its module list equals the
  preflight's.

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

- **Compiler.** `Target.Kea` has one subnet per network with `dhcp` switched on, bound to the network's
  bridge: pools (default: the second half of the subnet), lease time (default 1 h), router and DNS
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
- **Observation.** `Engine.PollObserved` reads the neighbor table, conntrack and Kea's leases every
  second and when asked (`TriggerObserve`: netlink neighbor events through `FollowNeighbors`, lease
  events); a burst of triggers is one reading. The state owner's `tracker` (`devices.go`, a pure state
  machine with unit tests) keeps the registry of discovered devices (the UUID is a hash of the MAC, so it
  is stable across restarts), resolves identity with `domain.ResolveIdentity`, sets online state and
  emits `device_discovered`, `device_online`, `device_offline` and `device_identity_changed`.
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
  exposes it as `started_at` when the kernel reports one.
- **Identity updates.** A change of the addresses of known devices makes a desired state flagged
  `IdentityOnly`; the apply loop then sends `NftAddElements` and `NftDelElements` for the changed sets
  instead of rebuilding the ruleset, and verifies. A device that has no set yet (a new one) needs a full
  apply. The executor takes such requests before queued plans (a second queue class) and never runs two
  requests at once. The generation rule in the kernel keeps naming the last full apply.
- **Lease events.** Kea's `run_script` hook starts `chaosgw-kea-hook`, which runs `chaosgw kea-hook`: it
  writes the event as one JSON datagram to the Unix socket the API serves at `--kea-events-socket`
  (`CHAOSGW_KEA_EVENTS_SOCKET`, default `/run/kea/chaosgw-events.sock` in the shared `chaosgw-kea-run`
  volume, M6a-09) — no connection, no TLS handshake, so a DHCP flood from an untrusted device costs the
  hook a `write(2)` and the API a few bytes, not a forked process and a handshake each. The kernel
  drops a datagram rather than blocking the hook if the API is slow or the socket's buffer is full.
  When the socket is missing or the write fails for any reason, the hook falls back to
  `POST /api/v1/internal/dhcp/lease-events` with the service token (scope `service`, the only scope
  that reaches `/internal`; written by the API to the `chaosgw-service` volume, which Kea mounts
  read-only) — the only path before M6a-09, kept for exactly this case. Either way the event is
  published as `dhcp_lease` and makes the poller read at once.
- **API.** `GET /devices`, `/devices/{id}` (configured, WireGuard clients, probes and discovered devices
  with their observed state), `GET /networks/{id}/leases`, `GET /flows` (from conntrack, with the device
  of each flow). Adopting a discovered device and merging two devices are revisions (the spec's
  `Device`).
- **Tests.** The tracker, the engine (with a fake DHCP server) and the API are unit-tested; the real
  Kea is exercised against the loopback (`internal/kea`, root and `kea-dhcp4` needed, otherwise skipped)
  and in the testbed (`integration_dhcp_test.go`: udhcpc clients get a lease, a reservation is honored,
  DHCP off leaves a network silent, identity events within a second, a burst of neighbor changes, flows).

## DNS proxy and the service namespace (M6b)

The gateway services run in a **service namespace** of their own (plan §3.3, D29), so that the faults of
a device apply to its connections to them like to any other traffic. M6b adds the namespace and the DNS
proxy; the TLS responder (M21) joins it later.

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
  through the uplink, masqueraded), table 102 has `default via 169.254.100.2 dev svc0` and a `prohibit
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
  crash-only restart policy would leave it stuck in the old, now-empty one.
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
  ever talk to the gateway over a private link).
- **Not in M6b:** DNS faults, hostname selectors and the redirect of hardcoded resolvers (M20),
  `/internal/dns/resolutions` (M20), per-device query statistics.

## Classification (M7)

Plan §3.3's lookup chain (`internal/compiler/classify.go`): on prerouting, write the winning fault's
id and the packet's direction into reserved mark bits, for test/WireGuard/remote-network traffic
only. M7 built the mechanism and proved it with test ids; since M8a the maps are driven by the
winners of `domain.Resolve` and the ids are real (see "Faults in the compiler (M8a)" below, which
also changes two things said here: the maps are interval maps, and an element `goto`s its chain).

- **Mark bits.** Bits 4-15 hold the fault id (12 bits, up to the 4095 of plan §3.3's capacity limit),
  bit 16 the direction (0 = original/upload, 1 = reply/download, read from `ct direction`). Bits
  17-19 (PMTU), 20 (service selection, already compiled by M6b but not yet written by real traffic: the
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
  restarts both timers. Mutations return `Change`s (`Created`, `Updated`, `Removed`, `Expired`,
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
  resolve); E9 is the compiler's golden of M10 (`TestE9ABadLTEProfileOnANetworkGivesEveryDeviceItsOwnQueueWithTheFullRate`, measured on the real kernel by `TestAnIotRateOfTwoMbitGivesEveryDeviceOfTheNetworkItsOwnTwoMbit`), E10 follows with the tunnel faults, E11 in M21. E5 resolves the DNS family, which only takes
  effect in the kernel from M20; its resolution is already tested.
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
  fault that names a hostname is left out with a `hostname_unresolved` warning (M20).
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
  Overlays of kinds whose milestone is not in the build (`CheckOverlaySupported`: DNS M20, TLS M21,
  DHCP M23) are refused with `unsupported_feature` before they reach the owner. The
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
  hostname destination has no address to look up: no route (and no kernel section) until M20 resolves
  names.
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
          jump mgmt_guard*                   only with the management network behind the uplink
          jump access_forward                <- the rules
          the access matrix, the service rules, the default
```

- **The rules decide before the matrix.** `allow` in forward is `accept`: an exception to the matrix
  ("IoT may not reach management, but this device may reach that host on port 22"). Traffic that no
  rule selects goes on to the matrix, which stays the default policy per network.
- **The two-port guard stands in front of the rules.** When the management interface is the uplink interface (the
  two-port topology, plan §2.16) the management network lies behind the uplink, and a device under test must not
  reach it. The rule that drops this (`ifs_test` to the uplink, destination in `mgmt_src`, counter `forward_drop`) is the
  last rule of the chain `mgmt_guard`, which forward jumps to in front of `jump access_forward`, as the anti-lockout rule
  stands in front of the input rules. No access rule, an `allow` rule for every source and every destination included,
  and no overlay opens the management network behind the uplink for a device under test (a local test network, a
  WireGuard network of the role test and its clients). The one thing that still does is an explicit entry of the
  access matrix that allows the device's network to reach the management network: the chain starts with a `return` for
  each such entry, so that traffic goes on to the rules and meets the entry in its place in the matrix. What a client's
  `reachable` list implies does not open it, and an explicit entry "to the uplink" does not either (it matches the
  interface only). `domain.World.AccessDecision` checks the same guard (`twoPortGuard`) before the rules and answers a drop by the
  access matrix without a rule, so `explain` and the kernel agree. Decided by the maintainer on 2026-10-11 (P2-M9-11;
  before, the guard stood behind the jump, and an `allow` rule for such a destination opened the management network).
  Tests: `TestNoAccessRuleCanOpenTheManagementNetworkBehindTheUplink`, `TestAnExplicitMatrixEntryReturnsFromTheTwoPortGuard`,
  `TestNoAccessRuleOpensTheManagementNetworkBehindTheUplink` (the domain layer), the `access-twoport` scenario of the kernel
  gate, and on the real kernel `TestNoAllowRuleOpensTheManagementNetworkBehindTheUplinkOnTheRealKernel` and
  `TestAnExplicitMatrixEntryStillOpensTheManagementNetworkBehindTheUplinkOnTheRealKernel` (`explain` against the packets).
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
  directly to `169.254.100.2`.

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
  pins the set. A hostname needs the DNS-derived sets of M20: such a rule stays out of the
  chain with a `hostname_unresolved` warning (P2-M9-02). Transferred to the plan on 2026-10-11: M20's acceptance
  criteria include a drop rule with a hostname destination that blocks the addresses the device resolved.
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
  never taken, whatever a rule says.
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
  to judge and is not explained by rules (the rules that name hostnames are not compiled before M20).
  This resolves P2-M8a-07.
- **Endpoints.** `GET /rules` lists the configured rules in the order of `access_rule_order` with `state`
  (`effective` when the rule is in the packet path, `disabled` for `enabled: false` and for a rule the
  kernel does not run: the hostname rule before M20) and `counters` (the active revision only), behind
  `system_rules` (the anti-lockout rule with its counter). `GET /rules/{id}` takes an id or a name.
  `GET /overlays` and `/overlays/{id}` show a rule overlay with `state` and `counters` too;
  `/overlays?kind=rule` lists them. `GET /capabilities` has the overlay kind `rule` and the feature
  `rules`.
- **Preview.** `POST /revisions/{id}/preview` has `rules`: the effective list after the change, overlay
  rules first, each with its key, layer, place, action, selector in words and `new` when the kernel does
  not run it in this form yet. A hostname rule is not in the list and the warning
  `hostname_unresolved` says why; a reject carries its `reject_with`; more rules than the limit is `capacity_exceeded` at preview and apply
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

**Mirror tables 103 to 109** (table 100 is the policy table, 102 the service table, 101 stays free; the executor's range
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
  catalogue and stay unavailable until M20 and M21. They are not part of any configuration: a key equal to a built-in
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
  `dns` part (M20) or a `tls` part (M21) activates and its part competes in that family.
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
  is **refused** with `unsupported_feature`, naming the profile and the milestone (M20 for `dns`, M21 for `tls`), by the
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
panel) follow in their own section below; M14's (networks, technical view) are in the navigation but not yet links; panels
whose backend comes later (running scenario, captures, diagnostics, DHCP and TLS test actions, the flow view of M26) are
left out.

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
  `invalidates`: a device event the devices and networks, an overlay event the overlays, faults and devices, …).
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
  fails on a refetch keeps its last data (the offline state greys it out).
- **The gateway's clock.** Deadlines, TTLs, lease ends and "last seen" are times on the gateway's clock, and a Raspberry Pi
  without a real-time clock may be minutes off before NTP. Every countdown therefore runs on the gateway's clock as the
  browser estimates it (`src/lib/clock.ts`, used by `useNow`): each answer's `Date` header bounds the offset between the two
  clocks (at least the header's second when it arrived, below the next second when it was asked for); the bounds of all
  answers are intersected, and an answer outside them (the gateway's clock was set) starts over.
- **Login and setup.** The router asks the gateway first (`GET /setup`, then `GET /auth/session`): an open setup sends
  every page to the wizard, no session to the login. The wizard (`SetupWizard.vue`, logic in `src/lib/setup.ts`) asks for
  the setup token and shows the preflight with its fixes; then uplink (proposed: the interface of the default route) with
  its router (optional for the interface of the default route, required for any other, since the gateway refuses an uplink
  without a default route and without a gateway; it must lie in the uplink's network) and management (proposed: another
  interface with an address, or the uplink in the two-port case); then the test
  interfaces, each becoming a LAN test network with a proposed name and gateway address (10.10.0.1/24, 10.20.0.1/24, …;
  the first proposal whose name and prefix no other network of the draft, the uplink or the management network uses) and
  DHCP; then the admin password. An interface the OS configures (`os_managed`) gets the netplan hint: delete its entry
  (plan §3.4: a test interface has none). Each step is
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
  are active, profiles and where they are active), the active faults (impairing overlays and enabled configured faults:
  target and destination in words, the effect as fault chips, affected packets, the remaining TTL counting down or
  "configured"), the uplink traffic of the last ten minutes (uPlot; rates from two readings of the uplink's byte counters,
  which `GET /uplink` reports since M12 as `status.rx_bytes`/`tx_bytes` from `/sys/class/net/<if>/statistics`) and the
  recent events (the stream's, before that the audit log's last entries).
- **Devices.** All devices with name, IP, MAC, network, traffic rates, the winning faults as chips (a `normal` profile is
  not one) and status with a label; search over name, addresses, MACs and the lease's host name (every word must match);
  the filter chips All / Online / With faults / Not adopted with their counts, as buttons with `aria-pressed`. A discovered
  device is adopted in a dialog: a name (proposed from the lease's host name), what changes in words, then a merge-patch
  candidate under the same UUID with the device's MACs (or addresses) and network, `If-Match` the active revision, applied
  and verified; the toast says "Revision N applied and verified in X ms". Since M13 the adoption goes through the
  configuration draft, the preview drawer and the concurrent-change dialog (section "Web UI: faults, rules and profiles").
- **Device detail.** Identity (status, addresses, MACs, network, groups, origin, where it was seen, fixed IP, test CA),
  the DHCP lease with its remaining time, the active faults (the device's overlays and those of its network or group with
  "via …", the winning configured faults), with parameters, affected packets and remaining TTL; an overlay can be removed,
  one of the network or group only after a dialog that says it is lifted for every device there.
  The traffic counters: rates, active connections, last seen.
- **Visual system** (`src/styles.css`, `src/components/ui/`). The tokens of §2.17 as Tailwind theme variables; IBM Plex
  Sans and Mono are bundled (`@fontsource`), nothing is loaded from outside. Shared components: `FaultChip`,
  `AccessChip`, `StatusDot` (always with its label), `SegmentedControl` (also the filter chips), `SwitchToggle`
  (`role="switch"`), `SectionCard`, `EmptyState`, table rows (`.data-row`), `ToastHost`, `StickyActionBar`, `SideDrawer`,
  `ModalDialog` (Reka UI dialogs: focus trap, Escape, labelled by the title), `CountdownBanner`, `AppBanner`, `AppIcon`
  (16 px stroke icons). The focus ring is the accent (`--color-accent`) at 2 px on every control.

### Tests

| What | Where |
|---|---|
| Fetch wrapper: base path, CSRF on unsafe methods, JSON default, problem+json, reachability, ended session, 503 `unavailable` | `src/api/fetcher.test.ts` |
| The gateway's clock from the `Date` headers: offset, narrowing, a clock that was set, learned by every request | `src/lib/clock.test.ts` |
| Formatting, fault chips and selectors in words, the device list's filters, search, status and order | `src/lib/format.test.ts`, `src/lib/faults.test.ts`, `src/lib/devices.test.ts` |
| Setup wizard rules, free proposals, the netplan hint and the request it builds (checked against the generated schema) | `src/lib/setup.test.ts` |
| Stores: session (setup, login, logout with CSRF, one round trip, a setup reopened by a rollback), connection, toasts, events, uplink rates | `src/stores/stores.test.ts` |
| Event stream: listeners, invalidation per type, `events_lost`, toasts (the rollback of the setup's revision 1 in its own words), reconnect, a new stream with a growing pause after the browser gave one up | `src/composables/eventStream.test.ts` |
| Shared components and their accessibility (labels, `aria-pressed`, `role="switch"`, countdown with its milestones for screen readers and on the gateway's clock, Escape in a dialog, toasts) | `src/components/ui/components.test.ts` |
| Router: setup first, login with `next`, no login or setup with a session, an unreachable gateway | `src/router.test.ts` |
| Serving the app: deep links, assets and their caching, CSP, 404s, nothing under `/api/` is the page, before the setup | `TestTheWebAppIsServedNextToTheAPI`, `TestWithoutAWebAppEveryOtherPathIsNotFound`, `TestCheckUIWantsAnIndexPage` (`internal/api/ui_test.go`), `TestWebUIComesFromTheDirectoryOrTheBinary` (`cmd/chaosgw`) |
| The uplink's byte counters | `TestTheUplinkReportsItsByteCounters` |
| Screens against a mocked API (Playwright, `make test-e2e`) | `web/e2e/*.spec.ts` (table below) |
| The browser flow against the real stack | `TestFirstStartInTheBrowserUntilADiscoveredDevice` (`internal/api/e2e_ui_test.go`, tags `testbed,browser`) with `web/e2e-testbed/first-start.spec.ts` |

**The mocked API** (`web/e2e/mock.ts`): `MockGateway` answers `/api/v1` through Playwright's request routing from data typed
with the generated model (a change of the spec breaks the fixtures at compile time; `npm run typecheck` checks the e2e code
too), records every request with its headers and body, paginates like the gateway (so the device list must follow the
cursor), refuses unsafe requests without the CSRF token, can be made unreachable (every request aborted) or answer a path
with an error (`failing`), can run its clock
off the browser's (`clockSkewMs`: a `Date` header and deadlines on that clock) and can roll the setup back (`rollBackSetup`). The browser's
`EventSource` is replaced by a fake the test drives (`gw.emit`, `__sse.fail`, `__sse.reopen`), so a live update is
asserted deterministically instead of by a poll.

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
| Playwright against a mocked API for every state: empty | `overview.spec.ts` "empty: no faults, no devices, no networks", `devices.spec.ts` "empty: no devices yet", `device-detail.spec.ts` "a device without faults says its traffic is unimpaired" |
| … many devices | `devices.spec.ts` "many devices: all pages are listed, search and filter chips narrow them" (620 devices, two pages) |
| … data that did not load | `states.spec.ts` "a list the gateway did not deliver says so and does not claim to be empty", "devices and a device the gateway did not deliver: an error, not an empty list or a blank page" |
| … offline | `states.spec.ts` "offline: a banner, live data greyed out with the time it was last updated, and back", "offline from the start: the page says so and tries again", "offline from the start, then the setup is open: Try again leads to the wizard", "offline from the start, then no session: Try again leads to the login and back to the page"; `device-detail.spec.ts` "faults cannot be removed while a revision waits for confirmation or the gateway is offline" |
| … safe mode | `states.spec.ts` "safe mode: a full-width red banner with the reason and the way to recovery" |
| … the other states of §2.17 that M12 brings: commit-confirm, expiring overlays, live updates, validation, login, setup | `states.spec.ts` (commit-confirm, its timeout, live updates, expiring overlays), `setup.spec.ts` (the wizard with its validation and Next disabled until a step is valid, free proposals, errors tied to their fields, the request, the confirmation, a countdown on the gateway's clock, Confirm until the gateway rolled back, Start again after a rollback, the session store following it, a rollback while the browser is in the shell), `login.spec.ts`, `overview.spec.ts`, `devices.spec.ts` (rows, adopt, a refused base), `device-detail.spec.ts` (removing a network's fault asks first) |
| An E2E flow in the testbed: first start with setup token → assign interfaces → see a discovered device | `TestFirstStartInTheBrowserUntilADiscoveredDevice` + `web/e2e-testbed/first-start.spec.ts` (CI job `testbed-ui`) |

## Web UI: faults, rules and profiles (M13)

Plan §2.17, §2.4, §2.9 and M13: the add/edit fault dialog with its live preview, the access-rules screen (ordered list,
rule editor IF / THEN, counters), the faults screen (by family and scope, "overridden by", explain), the unapplied-changes
bar, the preview-and-apply drawer, the concurrent-change dialog (reload and reapply), the profile cards, the TTL display
and the `</> API` panel. M14's screens (networks, WireGuard, routing, access matrix, compiled state) are still listed in
the navigation without a link; "Measure with probe" on the profile cards comes with the probes (M25).

### How it works

- **The configuration draft** (`src/stores/draft.ts`). Every change of the configuration the UI makes — access rules and
  their order, configured faults, an adopted device — goes into one draft, edited against the revision that was active when
  the first edit was made (`GET /revisions/active`; a clean draft follows the active revision, `track`). The draft holds the
  edited objects whole (`null` for a deleted one) and the order of the rules when it was touched; the changes in words are
  computed field by field against the base (`changes`: rule, field, old → new; a moved rule is a change of its position, a new
  rule at the end moves none), and an edit back to the base is no change. Nothing reaches the gateway before "Preview &
  apply": the draft then becomes a JSON Merge Patch against the base (`patch`, `src/lib/mergePatch.ts`: changed members, whole
  new objects, `null` for what went, arrays replaced), sent as `POST /revisions` with `Content-Type:
  application/merge-patch+json` and `If-Match` the base. The draft lives in the shell, so a rule, a fault and an adoption go
  into one revision, and a screen change does not lose it.
- **Unapplied-changes bar** (`src/components/apply/DraftChanges.vue`, plan §2.17 "Unapplied changes"): the sticky bar "N
  changes in M rules · not applied yet" (or faults, devices) with Discard and Preview & apply; changed rows are marked
  CHANGED (a new one NEW). Preview & apply stays disabled while a rule of the draft is not valid (plan §2.17 "Validation"),
  while a revision waits for confirmation and while the gateway does not answer.
- **Preview-and-apply drawer** (`PreviewDrawer.vue`, "Preview", "Applying / applied", "Apply failed"): marked "dry run ·
  nothing changed yet", it creates the candidate and shows the gateway's dry run (`POST /revisions/{id}/preview`): what
  changes in domain terms (the domain diff with its fields, old → new), warnings, whether the change needs a confirmation,
  overlays the change would orphan (applying them away needs a tick, `force=true`), and on request the Linux changes
  (the unified diffs of nftables, tc, routes, WireGuard, BIRD and the services, and the verify step). Back to editing (or
  Escape) discards the candidate and keeps the draft. "Apply revision N" (busy while it runs) answers with the toast
  "Revision N applied and verified in X ms" (or "waiting for confirmation", and the commit-confirm banner takes over). A
  candidate the gateway refuses (422) lists the problems with the object and field they name ("Rule no-dot · ports: …"),
  and the editor shows each under its field until the object is edited (`setServerErrors`). A failed apply
  (`apply_failed`, `verify_failed`) stays in the drawer with the failing step, the gateway's message and the revision that
  is active again ("Revision 42 restored; nothing of revision 43 is active"), and the draft stays for another try.
- **Concurrent-change dialog** (`ConflictDialog.vue`, "Concurrent change"): a base that is not the active revision any more
  (409 `revision_conflict`, at the candidate or at the apply) opens it: "Revision 43 was applied by token ci-runner while you
  were editing (your change is based on revision 42)", their change (`GET /revisions/{active}/diff?base=42`) and the user's
  (the draft's changes in words). "Reload and reapply mine" puts the draft on the active revision (`rebase`: the edited
  objects as the draft has them — where both changed the same object, the user's wins —, a deletion of what is gone drops
  out, rules they added keep their place in the order) and opens the drawer again, so the user sees the preview of the
  combined result before anything is applied; "Discard mine" drops the draft. There is no "overwrite theirs" (plan §2.17:
  the prototype's three-way dialog is superseded).
- **Access rules** (`src/views/RulesView.vue`, `src/components/rules/RuleEditor.vue`, `src/lib/rules.ts`). The list in
  evaluation order: the system rule "Control plane access" locked at the top (SYSTEM, its hits, a switch that cannot be
  turned off), the overlay rules (OVERLAY, newest first, their remaining time), then the configured rules with position,
  name, selector in words ("Network IoT → uplink · TCP 80, 443"), access chip, hits and the enable switch (`role="switch"`).
  The selected rule opens in the editor: name and enabled; **IF** from (everything, a network, a group, a configured device),
  to (anywhere, the uplink, a network, an address or prefix, a host name — with the hint that host names are matched from
  DNS answers, best effort), protocol, ports ("80, 443, 1000-2000"); **THEN** Allow · Drop · Reject · TCP reset as a
  segmented control (`aria-pressed`), the ICMP type of a reject, "also cut existing connections" for a refusing rule. The
  matches box shows hits, bytes and the last match, and the effect in words ("Rejected for new connections with ICMP port
  unreachable, so the sender fails at once; existing connections are cut once. 1 overlay rule is checked first."). Add rule,
  Delete, Move up / Move down change the draft. The editor checks a rule as the spec does (the name pattern, ports only with
  TCP or UDP, port numbers and ranges, an address or prefix, a host name, cut existing only for a refusing rule with TCP or
  any) and says it under the field (`aria-describedby`); a rule that needs fixing is marked in the list. An overlay rule is
  shown read-only with its owner and can be removed.
- **Last match** (backend, `internal/api/matches.go`). The kernel keeps no time of a rule's last match, so the API notes
  every reading of the rules' counters and gives `Counter.last_match`: the time of the first read that found the counter
  higher than the read before (a counter that started over and counts again also). The time lies between two reads; the
  rules screen reads every 5 s. Absent until a read saw the counter grow since the gateway started. Test:
  `TestARulesCounterSaysWhenItLastMatched` (configured, system and overlay rules).
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
  lost and why ("fault iot-slow lost: level 2 beats level 8").
- **Add/edit fault dialog** (`src/components/faults/FaultDialog.vue`, `src/lib/faultForm.ts`, plan §2.17 "Add fault"). Target
  (fixed to the device on its page), traffic (all, MQTT, MQTTS, HTTPS, HTTP, DNS, or protocol, ports and destination),
  direction (Both / Upload / Download), "Start from" LTE, Bad LTE, Satellite, Offline (the parameters of the built-in
  profiles of the same name), latency, jitter, loss and duration (1 min to 1 hour, or until removed); Advanced: rate,
  reorder, duplicate, corrupt, keep packet order. The live preview is one sentence ("ESP32-42 · MQTT · TCP 8883 · upload &
  download → ↑↓ 200 ms ±50, 5 % loss · expires after 5 min"). The checks (jitter ≤ latency, percentages up to 100, a rate
  of at least 8 bit/s in the units of the spec, reorder needs a latency, ports and destination) stand under the fields,
  Apply waits until the form is valid, and what the gateway refuses (422) goes under the field its path names. Apply writes
  an overlay (`POST /overlays`, answered after the verify): it appears with its TTL counting down and a toast "Fault active
  on ESP32-42" ("Expires after 5 min."). A loss of 100 % is a blackout. Editing an overlay writes it again under the same key
  (owner, target, family and selector: it keeps its id and gets the new TTL); with another traffic selector it is a new overlay
  and the old one is removed. On the faults screen a fault can instead be saved in the configuration ("Configuration · after
  preview", with a name; it goes into the draft), and a configured fault is edited there; parameters the dialog does not show
  (burst loss, flapping, different directions, …) are named before they would be replaced. The device detail has "Add
  fault", "Edit" for the device's own faults and "Apply profile".
- **Profiles** (`src/views/ProfilesView.vue`, `src/lib/profiles.ts`). A card per profile (built-in first, custom marked):
  its display name ("Bad LTE"), description, parameters as chips, where it is active (target, remaining time, state, owner)
  and, for a profile whose family comes later, why it cannot be activated. The scope selector (everything, network, group,
  device) and the duration (until removed, 1 min to 1 hour) apply to the buttons: "Activate on Network IoT" writes the
  activation (`POST /overlays` with `profile` and `ttl`; another profile on the same scope replaces it, one profile per
  scope), "Deactivate on Network IoT" removes the activations of that scope. The device page's "Apply profile" opens the
  profiles with the device as the scope (`?scope=device:<id>`).
- **TTL display.** Every overlay and profile activation shows its remaining time on the gateway's clock (M12's `useNow`),
  in the device detail, the overview, the faults screen, the overlay rules and the profile cards; the event stream's
  `overlay_expired` raises the toast and refreshes the lists (overlay events now also refresh the profiles).
- **`</> API` panel** (`src/components/layout/ApiPanel.vue`, `src/stores/apiPanel.ts`, plan §2.17 "every UI action has an
  API equivalent, and the UI can show it"). A toggle in every page header (`aria-pressed`) opens the panel below it. It shows
  the requests of the last action on the screen ("Last action: Add fault" — the `POST /api/v1/overlays` with its body; the
  candidate, preview and apply of a revision with `If-Match`; an explain; a profile activation), else what the open dialog
  would send, else what the screen reads, as HTTP or as curl with `Authorization: Bearer $CHAOSGW_TOKEN`. A new screen
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
| Fault dialog: the request (checked against the generated Zod schema of `POST /overlays`), one direction, advanced, blackout, custom traffic, a configured fault, an existing fault read back, the checks, the preview sentence, presets, the overlay key | `src/lib/faultForm.test.ts` |
| Draft: base tracking, change counting, the merge patch with order and deletions, devices, invalid rules and the gateway's errors, rebase on another revision; the API panel as HTTP and curl | `src/stores/draft.test.ts` |
| Overlays name what overrides them | `TestAFaultThatWinsOnlyAPartOfItsTrafficIsPartiallyOverridden` (`internal/api/overlays_test.go`) |
| A rule's last match | `TestARulesCounterSaysWhenItLastMatched` (`internal/api/rules_test.go`) |
| Screens against the mocked API (Playwright) | `web/e2e/rules.spec.ts`, `faults.spec.ts`, `profiles.spec.ts`, `devices.spec.ts` (adopt through the drawer and the conflict dialog) |
| The browser flow against the real stack | `TestAFaultCreatedInTheBrowserTakesEffectOnTheWire` (`internal/api/e2e_ui_test.go`, tags `testbed,browser`) with `web/e2e-testbed/faults.spec.ts` |

**The mocked API** (`web/e2e/mock.ts`) grew the active revision's configuration (`config`, `GET /revisions/active` with
`created_by`), candidates with their base (an apply on a base that is not active is a 409), `applyFromApi` (someone else
applies a revision, with the domain diff `GET /revisions/{id}/diff` answers), a preview of the candidate's patch, the rules
with counters (`withRules`), `POST /overlays` with the replacement by key, profiles with their activations, `GET /explain`,
and switches for a refused candidate (`rejectRevision`), a refused overlay (`rejectOverlay`) and a failed apply
(`failApply`).

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
| validation errors are shown | `faults.spec.ts` "validation errors are shown under the fields, and Apply waits until the form is valid" (also the gateway's 422 under the field), `rules.spec.ts` "validation: the message stands under the field and Preview & apply waits until the rule is valid", "the gateway's refusal of a candidate is shown in the drawer and under the field"; in the testbed flow the jitter error |
| a conflicting change made through the API triggers the conflict dialog | `rules.spec.ts` "a conflicting change made through the API opens the conflict dialog; reload and reapply mine", "the concurrent-change dialog can discard the draft instead"; `devices.spec.ts` "adopting while the draft is based on an older revision opens the conflict dialog: reload and reapply"; in the testbed flow with a real token |
| the faults screen marks an overridden fault | `faults.spec.ts` "the faults screen groups by family, sorts by scope, marks overlays and configuration, and marks an overridden fault"; in the testbed flow on the real gateway; backend `TestAFaultThatWinsOnlyAPartOfItsTrafficIsPartiallyOverridden` |
| Scope: access-rules screen (ordered list, editor IF/THEN, counters) | `rules.spec.ts` "the ordered list: …", "editing a rule marks it CHANGED …", "add, move and delete rules …", "empty: no rules yet …" |
| Scope: unapplied-changes bar, preview-and-apply drawer | `rules.spec.ts` "editing a rule marks it CHANGED and the bar counts the changes; Discard takes them back", "preview and apply: the drawer says what changes, Linux changes on request, Apply → verified toast", "Back to editing discards the candidate and keeps the draft", "a failed apply says which step failed and which revision the gateway restored", "Preview & apply waits while a revision waits for confirmation" |
| Scope: faults screen explain, edit, remove | `faults.spec.ts` "Explain names …", "edit a fault overlay on the faults screen …", "removing: an overlay at once, a configured fault through the draft", "empty: no faults …" |
| Scope: profile cards | `profiles.spec.ts` (cards, activate and deactivate on a scope with a duration, Apply profile from the device, disabled while a revision waits for confirmation) |
| Scope: TTL display | `faults.spec.ts` "an expiring fault counts down on the faults screen and says so when it is gone", the TTLs in the add-fault and profile tests, M12's `device-detail.spec.ts` |
| Scope: `</> API` panel | `faults.spec.ts` (the `POST /api/v1/overlays` after Add fault), `rules.spec.ts` (the candidate, preview and apply as HTTP and curl), `src/stores/draft.test.ts` |

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

`.github/workflows/ci.yml`: level 0 with the generated-code checks, the Playwright tests, the
testbed tests in a VM and in a privileged container, the browser flows of the web app against the real stack
(`testbed-ui`, a privileged container that builds the app and installs Chromium first), and the arm64 job (multi-arch image build,
unit tests under `qemu-user`). Where the hosted runner offers `/dev/kvm`, the VM job uses it; where
it does not, it runs emulated. KVM-dependent tests run on hosted GitHub runners (plan D33).

The privileged-container job mounts the runner's `/lib/modules` and runs the tests directly
(`make test-privileged`); it does not fall back to a VM, so a hosted kernel without netem would
fail it visibly.
Its `go test` timeout is 60 minutes (`ARGS='-test-timeout 60m'`): the first run of M10 stopped
the engine package at the default 20 minutes with every test up to then passing (234 of 256 tests
had started), so the limit, not a test, was the failure.
The VM job gives the package 100 minutes (`-test-timeout 100m -vm-timeout 3h`): under KVM the first
124 tests took 25 minutes, so the 256 of M10 need about 50. The same run showed a hang of its own, not
of a limit: `parallel` of the iperf3 rate test called `t.Fatalf` from goroutines, which ends the goroutine
without the signal the wait is for, so an iperf3 that failed at the end of an upload through a 2 Mbit/s
queue (`unable to receive results`) kept the test until the 60 minute timeout. The transfers now return
their error, and a failure of the tool repeats the three transfers once after the queues drained.

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

The image holds `chaosgw` and `chaosctl` only until M28 adds the rest.
