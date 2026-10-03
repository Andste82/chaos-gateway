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
| `internal/domain` | what the model means: decoding, reference resolution, the rules the schema cannot express, built-in profiles, precedence resolution, overlays and their keys, observed state and device identity, candidate creation (merge patch), domain diff |
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
| `internal/wireguard` | key generation and derivation, provisioning of a configuration's keys, export of client and link configurations (wg-quick `.conf`, QR as PNG/SVG, zip, export once) |
| `internal/apiserver` | generated Gin server interface (imports the model; the handlers follow in M5) |
| `tools/testvm` | runs the testbed tests: directly or in a VM |
| `web/` | Vue 3 app (Vite, Tailwind 4, TanStack Query, Pinia, Reka UI) |
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

**Which level where.** The devcontainer is unprivileged on purpose: network tests must not be able
to touch the host's interfaces or other containers. There the testbed runs only as level 1b. In
the VM the tests are root with their own kernel, so the namespaces, veth pairs and nftables rules
exist only in the guest. `make test-testbed` is what you normally run: `tools/testvm preflight`
shows why a machine can or cannot run level 1.

**Testbed tests are never skipped silently.** A test that needs the testbed calls
`testbed.New` (or `testbed.NewDefault`), which fails with the way out when the machine cannot
create namespaces or lacks kernel modules. The tests carry the build tag `testbed`; plain unit
tests do not and run at level 0 only.

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
- **KVM.** The VPS has no `/dev/kvm`. With KVM, boot is expected to be much faster and accuracy
  assertions apply; measurement tests and level 2 need a KVM-capable machine (plan §7.2, Q1).
- **Terminal.** `vng` refuses to start without a pseudo-terminal; the runner wraps it in
  `script(1)` when there is none, so it works from scripts and CI.
- **Share.** `vng` shares the work directory with the explicit `--rwdir=<path>=<path>` form; with
  a bare absolute path it computes a relative guest path and rejects it.
- **Kernels.** `TESTVM_KERNEL` selects the kernel: `6.8.0-142-generic` (Ubuntu 24.04, the
  default and the minimum supported) or `7.0.0-38-generic` (Ubuntu 26.04).

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

## The executor

`chaosgw exec` is the only process that writes the kernel's network configuration (plan §3.1). It
accepts a closed set of operations (`nft_apply`, `nft_add_elements`, `routing`, `tc`, `offloads`,
`docker_user`, `assign_interfaces`, `read`), each a JSON object with a `type` and an optional
`namespace`. The decoder (`executor.Decode`) is strict and is where most of the scope is enforced:
nftables only `inet chaosgw`, routes and rules only in tables 100-110 and always with protocol tag
201, tc arguments only from a token allowlist without the keywords that override the validated
fields. What depends on run-time state, the interfaces assigned to Chaos Gateway, is checked by the
executor's worker before the first operation of a request runs. The `ip`, `tc` and `iptables`
batches are built from validated fields only, never from text sent by the caller.

Adding an operation means: a type with `validate` in `validate.go`, its commands in `plan.go`, a
case in `Decode`, golden tests in `plan_test.go`, rejection tests in `decode_test.go` and a seed in
`fuzz_test.go`. The fuzz targets check that anything the decoder accepts yields inert commands that
stay in scope.

`make fuzz` runs both fuzz targets for `FUZZTIME` each (default 2m30s, 5 minutes in CI); the
nightly workflow runs them for an hour each. A crashing input lands in
`internal/executor/testdata/fuzz/`; commit it as a regression test with the fix.

Deliberately not in M3, each with the milestone that needs it: operations for links, bridges,
addresses, sysctls and namespaces (M4, the compiler needs them first), `uidrange` selectors on rules
(service namespace), and the persistent netlink connection for DNS-derived set updates
(`nft_add_elements` starts one `nft` per call until M6b measures the need). Which interfaces count as
assigned is decided by whoever may call `assign_interfaces`: loopback and Docker's devices are
refused, the rest is trusted to the (root or allowed-uid) caller. Routing batches use
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
- Time only through `internal/clock`.
- Start goroutines through the supervisor helper (recovers panics, reports health).
- CI runs `go test -race`; packages that start goroutines run `goleak` in their `TestMain`.

## Compiler, apply and engine

Three layers keep the kernel on the committed revision (plan §2.14, §3.2, §3.11):

- **`compiler.Compile`** is pure: the same configuration, host and generation give the same target,
  byte for byte. Everything the kernel needs is in `compiler.Target`; what it cannot build is a
  `Problem` (an error stops the apply, a warning degrades one network). Golden files live in
  `internal/compiler/testdata`; `go test ./internal/compiler -update` rewrites them.
- **`apply.Apply`** reads the state (`ip -j`, `nft -j list`, sysctls, offloads, DOCKER-USER),
  plans the difference in a fixed order, runs it as one executor request and verifies by reading
  back. Stale routes and rules go before the links they refer to, new ones after. nftables is one
  atomic transaction that re-creates the structure, flushes the compiled chains and sets, refills
  them and deletes what the target no longer names; dynamic sets and named counters are never
  flushed. Verify recognizes a rule by the hash of its expression in the rule's comment, a set by
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
- Not yet compiled: WireGuard networks and their matrix endpoints (M4b), the `uidrange` rule of
  the service user (M7), device identity maps and faults (M6a, M8).
- A failed `chaosgw apply --file` without `--state-dir` leaves the kernel as the failed apply left
  it; with `--state-dir` the engine restores the previous revision.
- `Rollback` and `Observe` return when the owner has taken the command; `Barrier` waits for the
  apply that follows.

`chaosgw apply --file config.yaml --socket /run/chaosgw/exec.sock` applies a configuration without
the API; `--dry-run` shows the plan and the diffs, `--state-dir` also stores the configuration as the
active revision. In the testbed `--namespace` points it at the gateway namespace.

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
  referenced. Verify compares the presence of a preshared key, not its value.
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
  does not interrupt a tunnel, and apply plans the operation only when something differs. (Deviation
  from plan §3.4, which names wgctrl: the `wg` tool works in any network namespace without entering
  it, and the testbed needs that.) `wg show <dev> dump` is the read; the parser drops the private key
  at once.
- **Status.** `Engine.PollWireGuard` reads the peers every interval; the snapshot has the state per
  peer, `wireguard_peer_online` and `wireguard_peer_offline` are emitted when a handshake becomes
  younger or older than three minutes or a peer disappears from the interface.
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

- **Compiler.** `Target.Bird` holds the model (`bird.Config`) and its rendered text. A protocol runs on
  one WireGuard link (neighbor = the link peer's address); the filters follow plan §2.2.2: the import
  filter accepts only the allowed prefixes, never a default route, never a protected prefix (the
  gateway's own networks, the management network, the uplink network and the tunnel subnets), and
  `import limit N action disable` makes the maximum number of prefixes a hard stop. BIRD's kernel
  protocol exports only into table 100 (`learn off`, `export where source ~ [RTS_...]`), so learned
  routes never reach the main table. External mode reads another daemon's table through a pipe. The
  input chain accepts BGP (tcp 179), OSPF (ip protocol 89) and Babel (udp 6696) from the link's
  interface, because the interfaces of test networks are otherwise dropped.
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
  `routing_session_up` and `routing_session_down` are emitted when an adjacency (BGP Established,
  OSPF/Babel up) changes. `kernelsim` simulates BIRD (`SetBirdProtocols` sets the protocol table).
- **Remote side.** `chaosgw wg export ... --link --bird [--remote-interface wg0]` prints the BIRD
  configuration for the other end of the link: roles swapped, same timers, a static protocol for the
  remote site's own prefixes.
- **Protected prefixes** also contain the networks the executor routes itself (behind clients, links
  and routers): a more specific prefix from a neighbor would win over them in the kernel.
- **Known limits.** A BIRD that is down fails every apply that has to configure it (the previous
  target is restored the same way). A protocol disabled by `import limit` stays disabled until its
  configuration changes. Babel and the external mode are covered by configuration tests only, not by
  a session in the testbed.
- **Testbed.** `WithRemotes` adds a second remote site (`site2`, 203.0.113.50, network 10.70.0.10).
  The tests in `internal/engine/integration_bird_test.go` run BIRD in the gateway's namespace and in
  the remote ones.

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
testbed tests in a VM and in a privileged container, and the arm64 job (multi-arch image build,
unit tests under `qemu-user`). Where the hosted runner offers `/dev/kvm`, the VM job uses it; where
it does not, it runs emulated. Where the KVM-dependent tests finally run is open question Q1.

The privileged-container job mounts the runner's `/lib/modules` and runs the tests directly
(`make test-privileged`); it does not fall back to a VM, so a hosted kernel without netem would
fail it visibly.

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
