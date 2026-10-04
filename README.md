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

Status: planning complete, all decisions made (see `docs/plan.md` §7.1), Phase 0 spikes S1–S16 executed (hardware measurements on Raspberry Pi follow when hardware exists, H1). Milestones M1–M6b of Phase 1 are implemented and merged; see `docs/open-items.md` for what is still open.

License: MIT (see [`LICENSE`](LICENSE)).
