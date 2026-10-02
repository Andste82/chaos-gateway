# Development

How to build, test and generate code. Everything runs in the devcontainer
([`.devcontainer/`](../.devcontainer/)); `make help` lists the targets.

## Layout

| Path | Content |
|---|---|
| `api/` | `openapi.yaml`, the source of truth for the domain model and the REST API; examples and their validator |
| `cmd/chaosgw`, `cmd/chaosctl` | the core binary and the CLI (skeletons until M3/M5/M18) |
| `internal/clock` | injectable clock: real and fake, wall time apart from monotonic time |
| `internal/preflight` | kernel version, the one shared kernel-module list, namespace capability |
| `internal/testbed` | namespace topologies for integration tests; `vmrun` runs them in a VM |
| `internal/apiserver` | generated Go types and Gin server interface |
| `tools/testvm` | runs the testbed tests: directly or in a VM |
| `web/` | Vue 3 app (Vite, Tailwind 4, TanStack Query, Pinia, Reka UI) |
| `clients/` | generated TypeScript and Python clients (not committed) |
| `deploy/` | the container image (multi-arch) |

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

## Generated code

`api/openapi.yaml` is the source of truth (spec first). `make generate` creates:

| Output | Generator | Committed |
|---|---|---|
| `internal/apiserver/api.gen.go` | `go tool oapi-codegen` (version pinned in `go.mod`) | yes; `make check-generated` fails when it is stale |
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
(`make test-privileged`); it does not fall back to a VM, so a hosted kernel without netem fails it
visibly. It is marked `continue-on-error` until it is known whether hosted kernels have the
modules; the workflow has not run on GitHub yet.
