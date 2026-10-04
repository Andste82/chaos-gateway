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
| `internal/api` | the REST API server (M5): the handlers of the spec operations that exist, the guard (authentication, scopes, CSRF), problem+json, idempotency keys, Server-Sent Events, the HTTPS certificate and the listener binder |
| `internal/auth` | the admin password (argon2id), API tokens (hashes only), sessions with CSRF tokens, the one-time setup token, the login rate limit |
| `internal/audit` | the append-only audit log (JSON lines) |
| `internal/linkexport` | what a link's remote side needs besides WireGuard: its BIRD configuration |
| `internal/appliance` | the harness of test level 2 (M5b): Ubuntu cloud images with checksum verification, the cloud-init seed, the QEMU command line, the host topology (bridges, taps, namespaces), SSH into the VM, log collection |
| `internal/kea` | the Kea DHCPv4 server: configuration renderer (one subnet per network on its bridge, reservations), control socket client (`config-test`, `config-set`, leases), the `run_script` hook's environment as a lease event |
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
  the VPS; a weekly job (`weekly.yml`) runs `make test-vm ARGS=-no-kvm` for exactly that.
- **Terminal.** `vng` refuses to start without a pseudo-terminal; the runner wraps it in
  `script(1)` when there is none, so it works from scripts and CI.
- **Share.** `vng` shares the work directory with the explicit `--rwdir=<path>=<path>` form; with
  a bare absolute path it computes a relative guest path and rejects it.
- **Kernels.** `TESTVM_KERNEL` selects the kernel: `6.8.0-142-generic` (Ubuntu 24.04, the
  default and the minimum supported) or `7.0.0-38-generic` (Ubuntu 26.04).
- **Crashed direct runs.** `tools/testvm sweep` removes `tb...` namespaces a crashed `-mode
  direct` run left behind (level 1b always gets a fresh VM, so it is never affected). It refuses
  while another `testvm` or compiled test binary is running, unless `-force` is given.

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
accepts a closed set of operations (`nft_apply`, `nft_add_elements`, `nft_del_elements`, `routing`,
`tc`, `offloads`, `docker_user`, `assign_interfaces`, `links`, `sysctl`, `wireguard`, `bird`,
`service_ns`, `read`), each a JSON object with a `type` and an optional `namespace`. The decoder
(`executor.Decode`) is strict and is where most of the scope is enforced:
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

All operations the compiler needs are implemented (`links`, `sysctl`, `wireguard`, `bird` and
`service_ns` followed in M4-M6b). Still deferred: the persistent netlink connection for
DNS-derived set updates (`nft_add_elements` starts one `nft` per call until M20), and the reader
pool with operation time stamps (M8a). Which interfaces count as assigned is decided by whoever
may call `assign_interfaces`: loopback and Docker's devices are
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
| `reserved_name` | A profile is named like a built-in profile. |
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
| `cut_existing_requires_tcp` | `cut_existing` is set on a rule whose protocol is not `tcp`. |
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
| `tunnel_parameter` | A tunnel fault sets a parameter other than latency, jitter, loss, burst_loss, blackout or flapping. |
| `invalid_dns_fault` | A DNS fault's action and its fields do not match (a field required by the action is missing, or set for the wrong one). |
| `invalid_tls_case` | A TLS case's fields do not match its case (protocol not tcp, interception fields outside `intercept`, ...). |
| `invalid_dhcp_action` | A DHCP action's fields do not match (`lease_time` only for `short_lease`, `options` only for `set_options`). |
| `invalid_overlay` | An overlay request names zero, or both, of `client` and `link`. |
| `invalid_flapping` | A flapping fault's `up` or `down` is zero. |
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
- Not yet compiled: device identity maps and classification (M7, M8a), faults (M8b), the PMTU
  mirror tables (M10).
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
  `import limit N action disable` makes the maximum number of prefixes a hard stop. BIRD's kernel
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
- **Known limits.** A protocol disabled by `import limit` stays disabled until its configuration
  changes. Babel is covered by configuration tests only, not by a session in the testbed.
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
  leaves the setup open. Until then the server listens on every address of the host except a test
  network's own (its bridge, or a test-role WireGuard tunnel — `api.NetworkInterfaceNames`, M5-21:
  a configuration can already be active before setup, e.g. `chaosgw apply --file`), afterwards on
  the management interface, the tunnel addresses of management-role WireGuard networks and the
  loopback (`api.Binder` follows the configuration without a restart).
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
- **Identity updates.** A change of the addresses of known devices makes a desired state flagged
  `IdentityOnly`; the apply loop then sends `NftAddElements` and `NftDelElements` for the changed sets
  instead of rebuilding the ruleset, and verifies. A device that has no set yet (a new one) needs a full
  apply. The executor takes such requests before queued plans (a second queue class) and never runs two
  requests at once. The generation rule in the kernel keeps naming the last full apply.
- **Lease events.** Kea's `run_script` hook starts `chaosgw-kea-hook`, which runs `chaosgw kea-hook`: it
  posts the event to `/api/v1/internal/dhcp/lease-events` with the service token (scope `service`, the
  only scope that reaches `/internal`; written by the API to the `chaosgw-service` volume, which Kea
  mounts read-only). The event is published as `dhcp_lease` and makes the poller read at once.
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
  every other parameter of that record is untouched. A failure of the service namespace step fails
  the apply (a dead holder blocks other revisions until the holder is back). `/run/systemd/resolve` is mounted read-only into the API container so that the
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
it does not, it runs emulated. KVM-dependent tests run on hosted GitHub runners (plan D33).

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
