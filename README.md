# Chaos Gateway

> A programmable network test gateway.

Chaos Gateway is a Linux machine that sits between a test network with IoT devices and the upstream network. It shapes, breaks, intercepts and records their traffic — per device, per traffic type, in both directions — controlled from a web UI and a REST API.

| | |
|---|---|
| [`docs/plan.md`](docs/plan.md) | product, architecture, test strategy, milestones, decisions |
| [`api/openapi.yaml`](api/openapi.yaml) | API and domain model (normative, spec-first); examples and their validator in [`api/examples/`](api/examples/) |
| [`docs/spikes/REPORT.md`](docs/spikes/REPORT.md) | Phase 0 spike results and the decisions derived from them |
| [`spikes/`](spikes/) | spike scripts and raw results |
| [`docs/development.md`](docs/development.md) | build, test levels, code generation, CI |

Status: planning complete, all decisions made (see `docs/plan.md` §7.1), Phase 0 spikes S1–S16 executed (hardware measurements on Raspberry Pi follow when hardware exists, H1). Phase 1 (M1–M6b) and Phase 2 (M7–M11, faults, access rules and profiles) are implemented and merged and were audited; Phase 2 is complete, the items of its audit of 2026-10-12 are closed (P2-AUDIT-01 to P2-AUDIT-17); Phase 3 (Web UI, M12–M14) is implemented on the branch `phase-3-web-ui` (draft PR #56) and was audited on 2026-10-14, the audit's fixes included; open are P3-M12-05 (the recovery instructions, waiting for M27/M30) and two audit items that wait for a maintainer decision (P3-AUDIT-37, P3-AUDIT-38). See `docs/open-items.md` for the audits and what is still open. Phase 4 (automation) is next once Phase 3 is merged.

License: MIT (see [`LICENSE`](LICENSE)).
