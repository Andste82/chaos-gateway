# Chaos Gateway

> A programmable network test gateway: put it between your IoT devices and the network, then
> break their traffic on purpose — reproducibly, per device, from a web UI or a REST API.

IoT devices have to survive bad networks: high latency, packet loss, outages, broken DNS, failing
TLS. Testing that is hard, because "slow network" tools don't run on embedded devices. Chaos Gateway
solves it from the outside: it is the **gateway** of a test network, so every packet a device sends
passes through it and can be delayed, dropped, throttled or blocked.

```
         Browser (Web UI)        Test automation (REST API)
                  │                       │
                  └───────────┬───────────┘
                              ▼
 Devices under test ──► [ Chaos Gateway ] ──► Upstream / Internet / servers
    (test network)      routing · NAT · DHCP · DNS
                        access rules · faults
```

You describe **what** should happen ("ESP32-42 gets a bad LTE connection for 5 minutes"); Chaos
Gateway works out **how** Linux implements it (nftables, tc/netem, routes) and verifies that the
kernel really does it before it answers.

## What it can do

| | |
|---|---|
| **Gateway** | routed test networks with NAT, DHCP (Kea) and a DNS proxy; device discovery; WireGuard clients and site links; static and dynamic routing (BIRD: BGP, OSPF, Babel) |
| **Faults** | latency, jitter, loss (also in bursts), rate limits, reordering, duplication, corruption, blackout, flapping, MTU/PMTUD faults — per device, group or network, per traffic type, per direction |
| **Access rules** | allow, drop, reject or TCP reset, optionally cutting connections that already exist |
| **Profiles** | ready-made conditions such as `lte`, `bad-lte`, `satellite`, `congested-wifi`, `offline`, `intermittent`, plus your own |
| **Safe testing** | runtime faults have an owner and an expiry, so a crashed test job never leaves the network broken; changes that could lock you out roll back unless you confirm them |
| **Insight** | "explain" shows which rule and which fault win for a device and a destination; the technical view shows the compiled Linux state |

**Coming next** (see the [roadmap](docs/plan.md#5-milestones)): scenarios with checks and JUnit
reports, packet capture, the `chaosctl` CLI, DNS / TLS / DHCP test cases, probes and metrics.

## Requirements

- A **dedicated Linux machine or VM** running **Ubuntu 24.04 or 26.04** (x86-64 or ARM64, e.g. a
  Raspberry Pi 4/5). Chaos Gateway reconfigures the host's network stack — don't use your
  workstation, and Docker Desktop on Windows/macOS won't work.
- **Two or more network interfaces:** one uplink, one or more test ports for the devices
  (management can share the uplink).
- Docker Engine with the compose plugin (the host setup script installs them if missing).

## Quick start

**1. Get the image** — either build it:

```sh
git clone https://github.com/Andste82/chaos-gateway.git && cd chaos-gateway
docker build -f deploy/Dockerfile -t chaos-gateway:latest .
```

or pull a published one (built by the [Image workflow](.github/workflows/image.yml)) and give it
the local name the compose files use:

```sh
docker pull ghcr.io/andste82/chaos-gateway:test
docker tag  ghcr.io/andste82/chaos-gateway:test chaos-gateway:latest
```

**2. Prepare the host** (once; loads kernel modules, enables forwarding, prints netplan hints for
the test ports — it never touches your uplink or management configuration):

```sh
docker run --rm --entrypoint cat chaos-gateway:latest /usr/local/share/chaosgw/host-setup.sh | sudo sh
```

**3. Start the containers** from the `deploy/` directory:

```sh
cd deploy
export COMPOSE_FILE=compose.executor.yaml:compose.api.yaml:compose.kea.yaml:compose.dns.yaml:compose.bird.yaml
docker compose up -d
docker compose logs api | grep "Setup token"
```

**4. Open the web UI** at `https://<host-address>:8443` (self-signed certificate — accept the
browser warning), enter the setup token, and let the wizard walk you through uplink, management
interface, test networks and the admin password. After setup the UI is only reachable on the
management network.

**5. Plug in a device** on a test port. It gets an address by DHCP and shows up under *Devices*.
Pick it, choose *Add fault* or apply a profile, and watch it suffer.

### Automating it

Everything the UI does goes through the same REST API (`/api/v1`, described in
[`api/openapi.yaml`](api/openapi.yaml)). Create an API token once, with the admin password:

```sh
GW=https://<host-address>:8443/api/v1
CSRF=$(curl -sk -c jar -H 'Content-Type: application/json' \
         -d '{"password":"<admin password>"}' $GW/auth/login | jq -r .csrf_token)
TOKEN=$(curl -sk -b jar -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' \
         -d '{"name":"ci","scope":"overlays"}' $GW/auth/tokens | jq -r .token)
rm jar
```

Then a test can switch conditions on and off:

```sh
# bad LTE for one device, removed automatically after 5 minutes
curl -sk -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
     -d '{"target":{"device":"esp32-42"},"profile":"bad-lte","ttl":"5m"}' $GW/overlays

# remove everything this token switched on
curl -sk -X POST -H "Authorization: Bearer $TOKEN" $GW/reset
```

A write returns only after the kernel state is verified, so the next test step can rely on it.
More request examples: [`api/examples/overlays.yaml`](api/examples/overlays.yaml). In the UI, the
`</> API` button on each screen shows the equivalent API call.

## Operating it

All commands run in `deploy/` with `COMPOSE_FILE` set as above.

| Task | Command |
|---|---|
| Logs | `docker compose logs -f api` (or `exec`, `kea`, `dns`, `bird`) |
| Update | pull or build the new image, then `docker compose up -d` |
| Stop | remove active faults first (UI, or `POST /api/v1/reset?owner=all`), then `docker compose down` — the gateway's configuration stays in the kernel, so devices keep routing |
| Forgotten admin password | `docker compose exec api chaosgw admin reset-password` |

Restarting the gateway always starts from the saved configuration: runtime faults are dropped, so
tests begin from a clean baseline.

## How it works

One Go binary, `chaosgw`, runs in several containers from the same image, each with only the
privileges it needs:

| Container | Role |
|---|---|
| `exec` | the only privileged one: applies nftables, tc, routes and sysctls; accepts only typed, validated operations |
| `api` | unprivileged: REST API, web UI, configuration revisions, compiler, overlays |
| `kea` | DHCP server |
| `dns` / `svcns` | DNS proxy in its own network namespace |
| `bird` | routing daemon for BGP, OSPF and Babel |

The configuration is versioned: every change becomes a revision that is previewed (in domain terms
and as Linux changes), applied and verified; you can roll back to any earlier one.

## Documentation

| | |
|---|---|
| [`docs/plan.md`](docs/plan.md) | product, architecture, roadmap and decisions — the full picture |
| [`api/openapi.yaml`](api/openapi.yaml) | the REST API and domain model |
| [`docs/development.md`](docs/development.md) | building, testing, code generation, CI |
| [`docs/open-items.md`](docs/open-items.md) | audits and known open items |

## Development

The [devcontainer](.devcontainer/) has the whole toolchain; `make help` lists the targets. The usual
ones: `make test` (unit tests), `make lint`, `make build`, and `make dev` for the web UI with hot
reload against a running API. Details in [`docs/development.md`](docs/development.md).

## License

MIT — see [`LICENSE`](LICENSE).
