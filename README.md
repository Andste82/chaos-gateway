# Chaos Gateway

> A programmable network test gateway.

Chaos Gateway is a Linux machine that sits between a test network with IoT devices and the upstream network. It shapes, breaks, intercepts and records their traffic — per device, per traffic type, in both directions — controlled from a web UI and a REST API.

| | |
|---|---|
| [`docs/plan.md`](docs/plan.md) | product, architecture, test strategy, milestones, decisions |
| [`api/openapi.yaml`](api/openapi.yaml) | API and domain model (normative, spec-first); examples and their validator in [`api/examples/`](api/examples/) |
| [`docs/spikes/REPORT.md`](docs/spikes/REPORT.md) | Phase 0 spike results and the decisions derived from them |
| [`spikes/`](spikes/) | spike scripts and raw results |

Status: planning complete, all decisions made (plan §7), Phase 0 spikes S1–S16 executed (hardware measurements on Raspberry Pi follow when hardware exists, H1). Implementation starts with milestone M1.

License: MIT (see [`LICENSE`](LICENSE)).
