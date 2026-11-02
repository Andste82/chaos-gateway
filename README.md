# Chaos Gateway

> A programmable network test gateway.

Chaos Gateway is a Linux machine that sits between a test network with IoT devices and the upstream network. It shapes, breaks, intercepts and records their traffic — per device, per traffic type, in both directions — controlled from a web UI and a REST API.

| | |
|---|---|
| [`docs/plan.md`](docs/plan.md) | product, architecture, test strategy, milestones, decisions |
| [`api/openapi.yaml`](api/openapi.yaml) | API and domain model (normative, spec-first); examples and their validator in [`api/examples/`](api/examples/) |
| [`docs/spikes/REPORT.md`](docs/spikes/REPORT.md) | Phase 0 spike results and the decisions derived from them |
| [`spikes/`](spikes/) | spike scripts and raw results |
| [`docs/install.md`](docs/install.md) | installing (one command, `install.sh`), updating (`update.sh`) and removing (`uninstall.sh`) the gateway on a host |
| [`docs/release.md`](docs/release.md) | making a release: the tag, the multi-arch image on ghcr.io, the candidate first (for the maintainers) |
| [`docs/user-guide.md`](docs/user-guide.md) | the user guide: concepts, the web app, faults, scenarios, captures, probes, `chaosctl` |
| [`docs/api-guide.md`](docs/api-guide.md) | the API guide with examples that run in CI |
| [`docs/cookbook.md`](docs/cookbook.md) | the scenario cookbook: recipes for the tests a device should survive |
| [`docs/troubleshooting.md`](docs/troubleshooting.md), [`docs/recovery.md`](docs/recovery.md) | the preflight checks and symptoms; last known good revision, safe mode, teardown, lost password, a bad update |
| [`docs/release-notes/v1.0.0.md`](docs/release-notes/v1.0.0.md) | what 1.0.0 contains and what is not validated yet (ARM64, the interception proxy, the external review) |
| [`docs/development.md`](docs/development.md) | build, test levels, code generation, CI |

Status: planning complete, all planning decisions made (see `docs/plan.md` §7.1; the open items of the phases are in `docs/open-items.md`), Phase 0 spikes S1–S16 executed (hardware measurements on Raspberry Pi follow when hardware exists, H1). Phase 1 (M1–M6b) and Phase 2 (M7–M11, faults, access rules and profiles) are implemented and merged and were audited; Phase 2 is complete, the items of its audit of 2026-10-12 are closed (P2-AUDIT-01 to P2-AUDIT-17); Phase 3 (Web UI, M12–M14) is implemented and merged (PR #56, 2026-10-15) and was audited on 2026-10-14; Phase 3 is complete, every item of its audit is closed (P3-AUDIT-01 to P3-AUDIT-38); its last open item P3-M12-05 (the recovery instructions the safe-mode banner links to) was closed with M30 (`docs/recovery.md`). See `docs/open-items.md` for the audits and what is still open. Phase 3.1 (M35a, port forwarding, pulled forward) is complete and merged (PR #68, 2026-10-17); its open items P31-M35a-01 to -10 are closed (decided on 2026-10-17), and it was audited on 2026-10-17 (P31-AUDIT-01 to -14, all closed; -11 to -14 decided by the maintainer on 2026-10-18; the fixes and decisions merged with PR #69 on 2026-10-18); the decisions I64-01 to I64-03 from issue #64 were made on 2026-10-18 and I64-05 is closed; issues #57, #63 and #64 are closed. Phase 4 (automation, M15–M19: scenario engine, checks, captures, `chaosctl`, scenario UI) is complete: merged to `main` with PR #71 on 2026-10-20 (CI run 39384159501 green); its audit (P4-AUDIT-01 to P4-AUDIT-59, all closed) is in `docs/open-items.md`. Phases 5 to 7 are merged to `main` too (the TLS cases, DNS faults and interception, the diagnostics, probes and observability, then recovery, container deployment, security hardening and the documents), each audited with its items closed (P5-AUDIT-01 to -43, P6-AUDIT-01 to -69, P7-AUDIT-01 to -70), so all of V1 is built. What is not done: the first release (`image.yml` has not run on a tag: make a release candidate first, see [`docs/release.md`](docs/release.md)), the validation on Raspberry Pi hardware (H1), the external review of the executor's interface and what moved after V1 (M38); three items of Phase 7 are open, P7-M27-05, P7-CI-01 and P7-CI-02 (`docs/open-items.md`).

License: MIT (see [`LICENSE`](LICENSE)).
