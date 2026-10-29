# API guide

How to drive Chaos Gateway from a script or a test: the REST API under `/api/v1`, with examples that run. The reference is
[`api/openapi.yaml`](../api/openapi.yaml) (spec-first: field names, types, enums, status codes and error codes are defined there;
`GET /api/v1/capabilities` lists what a running build offers). This guide shows how the calls fit together. The command line client
`chaosctl` and the generated Python and TypeScript clients use the same API; they are covered at the end.

Every example in this guide that starts with `sh run` is run by the CI job `docs` against a gateway on the simulated kernel
(`make test-docs`, [development.md](development.md#documentation-examples-m30)). In them `$CHAOSGW_URL` is the address of the
gateway (`https://gateway.example:8443`), `$CHAOSGW_TOKEN` an API token with the scope `overlays` and `$CHAOSGW_ADMIN_TOKEN` one
with the scope `full` (and `$CHAOSGW_ADMIN_PASSWORD` the admin password); the device `esp32-42` in the network `IoT` is online. A gateway of your own has its own names. The scripts
use `curl` and `jq`; add `--cacert gateway.pem` to `curl` (or `-k` in a lab where you accept the risk) while the gateway has its own
self-signed certificate ([HTTPS certificate](install.md#https-certificate)).

## Contents

- [Tokens and scopes](#tokens-and-scopes)
- [Conventions](#conventions)
- [Reading the state](#reading-the-state)
- [Overlays: faults, profiles and rules that a test switches on](#overlays-faults-profiles-and-rules-that-a-test-switches-on)
- [DNS faults, TLS cases and DHCP actions](#dns-faults-tls-cases-and-dhcp-actions)
- [Scenarios and runs](#scenarios-and-runs)
- [Events](#events)
- [Changing the configuration](#changing-the-configuration)
- [Captures and diagnostics](#captures-and-diagnostics)
- [Clients](#clients)

## Tokens and scopes

Automation authenticates with an API token in the header `Authorization: Bearer <token>`. The browser uses a session cookie with a
CSRF token instead; a script never needs that. A token has one of three scopes:

| Scope | Allows |
|---|---|
| `read` | every read, the event stream and `explain`; no change |
| `overlays` | `read`, and: overlays, runs, captures, diagnostics, the calibration of probes, and `reset` of the caller's own overlays and runs |
| `full` | everything: configuration (revisions), `reset?owner=all`, tokens, the HTTPS certificate, the export of WireGuard keys |

Give a CI job the scope `overlays` for tests, not for configuration: it cannot change the configuration, but it is not tied to one
device. It can impair any target (a device, a network or everything), aim diagnostics at any address the gateway reaches and record
and download the unencrypted traffic of any device (captures marked sensitive need `full`). Treat the token as a lab-wide credential.
The scope every operation needs is in the spec (`x-required-scope`), and a test pins it for all of them.

The web app has no screen for tokens; the first token is made from the admin session. Log in with the admin password (the
session cookie and a CSRF token come back), then create the token with the CSRF token in `X-CSRF-Token`; a token creates further
tokens without that. The value is shown once and the gateway keeps only its hash:

```sh run
api="$CHAOSGW_URL/api/v1"
csrf=$(curl -sS -c cookies.txt -H "Content-Type: application/json" -d "{\"password\": \"$CHAOSGW_ADMIN_PASSWORD\"}" "$api/auth/login" | jq -r .csrf_token)
curl -sS -b cookies.txt -H "X-CSRF-Token: $csrf" -H "Content-Type: application/json" \
  -d '{"name": "ci-first", "scope": "full"}' "$api/auth/tokens" | jq -r '.scope'
curl -sS -H "Authorization: Bearer $CHAOSGW_ADMIN_TOKEN" -H "Content-Type: application/json" \
  -d '{"name": "ci-nightly", "scope": "overlays"}' "$api/auth/tokens" | jq 'keys'
```

```text output
full
  "token"
```

`GET /api/v1/auth/tokens` lists the tokens without their secrets, `DELETE /api/v1/auth/tokens/{tokenId}` revokes one. A call without a
token, or with a wrong one, is answered with `401` (`unauthorized`); a token whose scope is too small with `403` (`forbidden`).

## Conventions

**Errors** are `application/problem+json` (RFC 9457) with a stable `code`, and for a refused document an `errors` array with a JSON
pointer to each problem:

```sh run
curl -sS -H "Authorization: Bearer $CHAOSGW_TOKEN" -H "Content-Type: application/json" \
  -d '{"target": {"device": "no-such-device"}, "profile": "bad-lte"}' "$CHAOSGW_URL/api/v1/overlays" | jq .
```

```text output
  "code": "validation_failed",
      "code": "unknown_reference",
      "path": "/target/device"
```

The codes a script reacts to: `validation_failed` (422), `revision_conflict` and `confirm_pending` (409), `target_busy` (409),
`not_found` (404), `forbidden` (403), `precondition_required` (428: a write that needs `If-Match` came without it),
`unavailable` (503: the gateway runs in safe mode and the operation needs forwarding), `unsupported_feature` (the build lacks the
feature, see `GET /capabilities`), `capacity_exceeded` (the faults need more queues than the gateway allows), `lockout_protected`,
`verify_failed` and `rate_limited` (too many failed logins). The spec lists the rest. Request bodies are decoded strictly: an unknown field is an error, not ignored.

**Objects and names.** Every object has a UUID and a unique name; a path accepts either (`/devices/esp32-42`). An overlay refers to its
target by UUID internally, so renaming a device does not touch it. The resources `/networks`, `/devices`, `/rules`, `/profiles` and the
like are read-only views of the active revision joined with what the gateway observes; the configuration is written through
revisions ([below](#changing-the-configuration)).

**Units.** Durations are Go duration strings (`500ms`, `90s`, `5m`, `1h`), percentages and bit rates strings with units (`3%`,
`2Mbit`), ports lists (`ports: [8883]`).

**The generation.** Every apply and every change of an overlay produces a new generation id. A write returns it in the header
`Chaos-Generation` and the body, `GET /state` shows the current one, and the event `applied` announces it. A write returns after the
gateway verified the change in the kernel, so a test step can rely on the fault being in force when the call returns, and needs no
sleep:

```sh run
curl -sS -D - -o /dev/null -H "Authorization: Bearer $CHAOSGW_TOKEN" -H "Content-Type: application/json" \
  -d '{"target": {"device": "esp32-42"}, "fault": {"latency": "100ms"}, "ttl": "1m"}' "$CHAOSGW_URL/api/v1/overlays" | grep -i '^\(HTTP\|chaos-generation\)'
```

```text output
HTTP/1.1 201 Created
Chaos-Generation: …
```

**Idempotency.** A request with the header `Idempotency-Key: <string>` that is repeated within 24 hours returns the first answer and
creates nothing a second time: send one with every request that your job may retry.

```sh run
body='{"inline": {"name": "once", "target": {"device": "esp32-42"}, "steps": [{"id": "slow", "at": "0s", "fault": {"latency": "50ms"}}, {"id": "back", "at": "1s", "restore": true}]}}'
first=$(curl -sS -H "Authorization: Bearer $CHAOSGW_TOKEN" -H "Content-Type: application/json" -H "Idempotency-Key: job-4711" -d "$body" "$CHAOSGW_URL/api/v1/runs" | jq -r .id)
second=$(curl -sS -H "Authorization: Bearer $CHAOSGW_TOKEN" -H "Content-Type: application/json" -H "Idempotency-Key: job-4711" -d "$body" "$CHAOSGW_URL/api/v1/runs" | jq -r .id)
test "$first" != null && test "$first" = "$second" && echo "one run, started once"
test "$(curl -sS -H "Authorization: Bearer $CHAOSGW_TOKEN" "$CHAOSGW_URL/api/v1/runs" | jq '[.items[] | select(.id == "'"$first"'")] | length')" = 1 && echo "the run exists"
```

```text output
one run, started once
the run exists
```

**Lists** are `{"items": [...]}` with the cursor `next_cursor` when there is more (`?limit=` and `?cursor=`), and the filters the spec
names (`?network=`, `?owner=`).

## Reading the state

`GET /state` tells whether the gateway is healthy and what is in force; a test can start from it:

```sh run
curl -sS -H "Authorization: Bearer $CHAOSGW_TOKEN" "$CHAOSGW_URL/api/v1/state" | jq '{active_revision, overlays_active, runs_active, safe_mode}'
```

```text output
  "overlays_active": 0,
  "runs_active": 0,
  "safe_mode": false
```

`safe_mode: true` means the gateway could not apply its configuration and runs with management access only; see the
[recovery instructions](recovery.md). `GET /system/health` reports the components, `GET /system/preflight` the checks of the host
([troubleshooting](troubleshooting.md#the-preflight-checks)), and `GET /system/busy` whether a run is active or queued (the update
script waits for it).

A device is a configured one or one the gateway discovered; both are listed with what was observed:

```sh run
curl -sS -H "Authorization: Bearer $CHAOSGW_TOKEN" "$CHAOSGW_URL/api/v1/devices/esp32-42" | jq '{name, origin, online: .observed.online, addresses: .observed.addresses}'
```

```text output
  "name": "esp32-42",
  "origin": "configured",
  "online": true,
```

`GET /explain` says what the gateway does with one kind of traffic and why, without sending a packet: the verdict of the access rules,
the fault that wins in each family (and the faults it overrides), and the route. Ask it before a test to see what a profile will do,
and during a test to find out why a connection is refused:

```sh run
curl -sS -H "Authorization: Bearer $CHAOSGW_TOKEN" \
  "$CHAOSGW_URL/api/v1/explain?device=esp32-42&dst=203.0.113.10&port=8883&protocol=tcp" | jq -c '{access, faults, route}'
```

```text output
{"access":{"verdict":"allow","layer":"access_matrix","reason":"the default: a local test network reaches the uplink"},"faults":[],"route":{"table":100,"interface":"wan0"}}
```

## Overlays: faults, profiles and rules that a test switches on

An overlay is a change that lives at run time and is never part of a revision: a fault, a profile activation, an access rule, a DNS
fault, a TLS case, a DHCP action or a WireGuard action. It has an owner (your token), usually an expiry (`ttl`) and optionally a
lease. **Overlays always win over the configuration** (precedence is resolved per fault family, then by how specific the target is: a
device before a group before a network before everything; the newer entry wins at the same level; plan §2.4).

```sh run
curl -sS -H "Authorization: Bearer $CHAOSGW_TOKEN" -H "Content-Type: application/json" \
  -d '{"target": {"device": "esp32-42"}, "profile": "bad-lte", "ttl": "5m"}' "$CHAOSGW_URL/api/v1/overlays" | jq '{id, kind, state, ttl}'
```

```text output
  "kind": "profile",
  "state": "effective",
  "ttl": "5m"
```

`state` is `effective` when the change is in the kernel and matching traffic meets it. The built-in profiles are `normal`, `lte`,
`bad-lte`, `satellite`, `congested-wifi`, `offline`, `intermittent`, `dns-broken` and `tls-broken` (`GET /profiles`; custom ones
come with the [configuration](#changing-the-configuration)). A fault names its parameters itself. Flat parameters apply to both
directions; `upload` and `download` give each direction its own complete set:

```sh run
curl -sS -H "Authorization: Bearer $CHAOSGW_TOKEN" -H "Content-Type: application/json" -d '{
  "target": {"device": "esp32-42"},
  "fault": {
    "destination": {"hostname": "broker.example.com"}, "protocol": "tcp", "ports": [8883],
    "upload": {"latency": "200ms", "jitter": "20ms"},
    "download": {"loss": "2%"}
  },
  "ttl": "2m"
}' "$CHAOSGW_URL/api/v1/overlays" | jq '{kind, fault}'
```

```text output
  "kind": "fault",
```

An overlay is identified by its **key**: owner, kind, target and the kind's selector (for a fault the family and the traffic it
matches). Writing an overlay with an existing key **replaces** it, keeps its id and answers `200` instead of `201`. The parameters
are complete and never merged: to change the latency of a running fault, send the whole fault again.

```sh run
post() { curl -sS -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer $CHAOSGW_TOKEN" -H "Content-Type: application/json" -d "$1" "$CHAOSGW_URL/api/v1/overlays"; }
post '{"target": {"device": "esp32-42"}, "fault": {"latency": "100ms"}, "ttl": "5m"}'
post '{"target": {"device": "esp32-42"}, "fault": {"latency": "400ms", "jitter": "20ms"}, "ttl": "5m"}'
```

```text output
201
200
```

An access rule is an overlay too (`rule`, a rule of the scenario step kind), and `cut_existing` also resets the connections that are
open:

```sh run
curl -sS -H "Authorization: Bearer $CHAOSGW_TOKEN" -H "Content-Type: application/json" \
  -d '{"target": {"device": "esp32-42"}, "rule": {"action": "drop", "protocol": "tcp", "ports": [8883], "cut_existing": true}, "ttl": "1m"}' \
  "$CHAOSGW_URL/api/v1/overlays" | jq -c '{kind, rule}'
```

```text output
{"kind":"rule","rule":{"action":"drop","cut_existing":true,"ports":[8883],"protocol":"tcp"}}
```

**Leases keep a crashed job from leaving a broken network.** An overlay with a `lease` must be renewed within that time
(`POST /overlays/{id}/renew`), else the gateway removes it. A job that renews while it runs and dies takes its faults with it after at
most one lease. The expiry (`ttl`) is the second safety net: the overlay goes when it runs out. `GET /overlays` lists the overlays
(`?owner=` filters), `GET /overlays/{id}` shows one with its counters (packets and bytes it matched, and the state of the queues),
`DELETE` removes it, and `POST /reset` removes everything of the caller's and stops the caller's runs (`?owner=all` needs the scope
`full`):

```sh run
auth="Authorization: Bearer $CHAOSGW_TOKEN"
id=$(curl -sS -H "$auth" -H "Content-Type: application/json" \
  -d '{"target": {"device": "esp32-42"}, "fault": {"blackout": true}, "ttl": "10m", "lease": "20s"}' "$CHAOSGW_URL/api/v1/overlays" | jq -r .id)
curl -sS -H "$auth" -X POST "$CHAOSGW_URL/api/v1/overlays/$id/renew" | jq 'has("lease_expires_at")'
curl -sS -H "$auth" "$CHAOSGW_URL/api/v1/overlays" | jq '.items | length'
curl -sS -o /dev/null -w '%{http_code}\n' -H "$auth" -X DELETE "$CHAOSGW_URL/api/v1/overlays/$id"
curl -sS -H "$auth" -X POST "$CHAOSGW_URL/api/v1/reset" | jq -c '{removed_overlays, aborted_runs}'
```

```text output
true
1
204
{"removed_overlays":0,"aborted_runs":0}
```

The bodies of overlays and scenario steps are the same, so a request that works as an overlay works as a step. This body is checked
against the schema `OverlayRequest`:

```json schema=OverlayRequest
{"target": {"network": "IoT"}, "fault": {"latency": "150ms", "jitter": "50ms", "loss": "3%", "rate": "2Mbit"}, "ttl": "10m"}
```

(`rate` is per device: every device of the network gets its own 2 Mbit/s, plan decision D18.)

## DNS faults, TLS cases and DHCP actions

The other overlay kinds have their own body, in the same call:

| Kind | Body | What it does |
|---|---|---|
| `dns` | `names`, `action` (`nxdomain`, `servfail`, `timeout`, `delay`, `wrong_answer`, `truncate`, `short_ttl`) | the gateway's DNS proxy answers the device's queries for these names wrongly |
| `tls` | `case` (`untrusted_ca`, `self_signed`, `expired`, `not_yet_valid`, `hostname_mismatch`, `handshake_reset`, `handshake_close`, `handshake_stall`, `intercept`), `ports`, `sni`, `cut_existing` | the connection is redirected to the gateway's TLS responder, which shows the device the broken certificate or handshake |
| `dhcp` | `action` (`short_lease`, `delete_lease`, `force_new_ip`, `set_options`, `silence`) | the gateway's DHCP server behaves differently towards a device or a network |
| `wireguard` | `client` or `link`, `action` (`disable`, `key_mismatch`, `block_endpoint`) | a tunnel peer goes away or breaks |

```sh run
auth="Authorization: Bearer $CHAOSGW_TOKEN"
post() { curl -sS -H "$auth" -H "Content-Type: application/json" -d "$1" "$CHAOSGW_URL/api/v1/overlays" | jq -c '{kind, state}'; }
post '{"target": {"device": "esp32-42"}, "dns": {"names": ["broker.example.com"], "action": "servfail"}, "ttl": "1m"}'
post '{"target": {"device": "esp32-42"}, "tls": {"case": "expired", "ports": [8883]}, "ttl": "1m"}'
post '{"target": {"device": "esp32-42"}, "dhcp": {"action": "short_lease", "lease_time": "10s"}, "ttl": "1m"}'
```

```text output
{"kind":"dns","state":"effective"}
{"kind":"tls","state":"effective"}
{"kind":"dhcp","state":"effective"}
```

Three of the certificate cases (`expired`, `not_yet_valid`, `hostname_mismatch`) are signed by the gateway's test CA, so only a device
that trusts it (`trusts_test_ca: true`, with the CA from `GET /tls/ca` installed in a development firmware) can tell them from an
unknown issuer. The cookbook has [a test with each](cookbook.md#a-device-that-must-reject-a-bad-certificate).

## Scenarios and runs

A scenario is a timeline of steps (plan §2.10; the schema is `Scenario`). It is stored in the configuration, or sent with the
request for one run, so a CI job can run the scenario of its own branch without touching the gateway:

```sh run
cat > run.json <<'EOF'
{
  "inline": {
    "name": "mqtt-outage",
    "target": {"device": "esp32-42"},
    "steps": [
      {"id": "slow", "at": "0s", "fault": {"latency": "200ms", "jitter": "50ms"}},
      {"id": "cut", "at": "1s", "rule": {"action": "drop", "protocol": "tcp", "ports": [8883], "cut_existing": true}},
      {"id": "back", "at": "2s", "restore": true}
    ]
  },
  "lease": "30s"
}
EOF
auth="Authorization: Bearer $CHAOSGW_TOKEN"
run=$(curl -sS -H "$auth" -H "Content-Type: application/json" -d @run.json "$CHAOSGW_URL/api/v1/runs" | jq -r .id)
# the run goes on without us; wait for it to end
until [ "$(curl -sS -H "$auth" "$CHAOSGW_URL/api/v1/runs/$run" | jq -r .status)" != running ]; do sleep 0.5; done
curl -sS -H "$auth" "$CHAOSGW_URL/api/v1/runs/$run" | jq -r .status
```

```text output
passed
```

The lifecycle is `queued`, `running`, then `passed`, `failed` (a check failed), `error` (the engine could not carry a step out or a
precondition failed) or `aborted`. A run on a target (device, group or network) that is busy waits in `queued`; with `?queue=false` the
call fails with `409 target_busy` instead. Before its first step a run checks that the device is online, uses the gateway's DNS and has
no overlay of another owner; a failed precondition ends the run as `error` with the reason. The `lease` works as for an overlay:
renew it with `POST /runs/{id}/renew`, and the gateway aborts the run and removes its overlays when the renewals stop. `POST
/runs/{id}/abort` ends a run at once. A run that was started stays alive when the client that started it disconnects.

The result is the run (`GET /runs/{id}`: steps with their times, the checks, the warnings, the artifacts), the JSON report
(`/runs/{id}/report.json`), the JUnit XML (`/runs/{id}/report.xml`, for a CI system that shows test results) and the events of the
run (`/runs/{id}/events`, one JSON object per line). A fault or rule that matched no packet gives the warning `zero_hit`, because a
run that passed without its fault ever hitting proves nothing (`zero_hit: error` in the scenario makes it fail the run). The
simulated gateway of the examples sees no traffic, so its runs carry that warning:

```sh run
auth="Authorization: Bearer $CHAOSGW_TOKEN"
body='{"inline": {"name": "report", "target": {"device": "esp32-42"}, "steps": [{"id": "slow", "at": "0s", "fault": {"latency": "50ms"}}, {"id": "back", "at": "1s", "restore": true}]}}'
run=$(curl -sS -H "$auth" -H "Content-Type: application/json" -d "$body" "$CHAOSGW_URL/api/v1/runs" | jq -r .id)
until [ "$(curl -sS -H "$auth" "$CHAOSGW_URL/api/v1/runs/$run" | jq -r .status)" != running ]; do sleep 0.5; done
curl -sS -H "$auth" "$CHAOSGW_URL/api/v1/runs/$run/report.xml" | head -3
curl -sS -H "$auth" "$CHAOSGW_URL/api/v1/runs/$run/report.json" | jq -c '.steps | map({id, status})'
```

```text output
<?xml version="1.0" encoding="UTF-8"?>
<testsuites name="chaos-gateway" tests="1" failures="0" errors="0" skipped="0" …>
[{"id":"slow","status":"applied"},{"id":"back","status":"applied"}]
```

A scenario can also say what must hold: `checks` (the device reconnected, no connection to a destination, DNS query seen, TLS
rejected, ...) with a window relative to a step, and `capture: true` records the target's traffic for the whole run and attaches it.
The [cookbook](cookbook.md) has runs with checks. A scenario stored in the configuration is started by name,
`POST /scenarios/{name}/runs` or `POST /runs` with `{"scenario": "mqtt-outage"}`.

## Events

`GET /events` is a Server-Sent Events stream: configuration changes, overlays created, expired and removed, run steps, devices going
online and offline, DHCP events, DNS queries answered with a fault, TLS handshakes. Each event has an id; a client that reconnects with
`Last-Event-ID` gets what it missed from a buffer of at least ten minutes. `?types=` and the filters in the spec narrow the stream.

```sh run
auth="Authorization: Bearer $CHAOSGW_TOKEN"
# read the stream for two seconds while an overlay is made; curl ends with 28 when the time is up
( sleep 1; curl -sS -o /dev/null -H "$auth" -H "Content-Type: application/json" \
    -d '{"target": {"device": "esp32-42"}, "fault": {"latency": "10ms"}, "ttl": "1m"}' "$CHAOSGW_URL/api/v1/overlays" ) &
curl -sN --max-time 3 -H "$auth" "$CHAOSGW_URL/api/v1/events" > events.txt || [ $? -eq 28 ]
wait
grep -c '^event: overlay_created' events.txt
```

```text output
1
```

## Changing the configuration

The configuration (networks, devices, groups, access rules, persistent faults, custom profiles, scenarios, port forwards, probes)
is stored in **immutable revisions**. A change is a candidate revision, which you preview and then apply; the active revision is the
only source of truth:

1. `POST /revisions` with the whole configuration, or a JSON Merge Patch (`Content-Type: application/merge-patch+json`) against the
   active revision, which goes in `If-Match`. Collections are maps keyed by UUID, so a patch adds, changes or (with `null`) deletes one
   object. If another revision became active in between, the call fails with `409 revision_conflict`: read the active revision again
   and repeat your change.
2. `POST /revisions/{id}/preview`: what the change does, in domain terms, and the Linux changes behind it (nftables, tc, routes), without
   changing anything. `requires_confirm` tells whether it could lock you out.
3. `POST /revisions/{id}/apply` returns once the change is verified in the kernel. A change that could cut off the administrator
   (management access, the UI and API binding, a management WireGuard network) waits for `POST /revisions/{id}/confirm` for 60 seconds
   and is rolled back by itself when nobody confirms; `POST /revisions/{id}/rollback` does it at once.

A custom profile and a scenario, added in one revision and run by name:

```sh run
auth="Authorization: Bearer $CHAOSGW_TOKEN"
adm="Authorization: Bearer $CHAOSGW_ADMIN_TOKEN"
api="$CHAOSGW_URL/api/v1"
cat > patch.json <<'EOF'
{
  "profiles": {
    "7c1d2e3f-4a5b-4c6d-8e7f-9a0b1c2d3e4f": {
      "name": "weak-wifi", "description": "a weak WiFi link",
      "parts": {"impairment": {"latency": "40ms", "jitter": "20ms", "loss": "4%"}}
    }
  },
  "scenarios": {
    "8d2e3f4a-5b6c-4d7e-9f8a-0b1c2d3e4f5a": {
      "name": "mqtt-cut", "target": {"device": "esp32-42"},
      "steps": [
        {"id": "cut", "at": "0s", "rule": {"action": "drop", "protocol": "tcp", "ports": [8883]}},
        {"id": "back", "at": "2s", "restore": true}
      ]
    }
  }
}
EOF
active=$(curl -sS -H "$adm" "$api/revisions/active" | jq -r .id)
candidate=$(curl -sS -H "$adm" -H "Content-Type: application/merge-patch+json" -H "If-Match: \"$active\"" -d @patch.json "$api/revisions" | jq -r .id)
curl -sS -H "$adm" -X POST "$api/revisions/$candidate/preview" | jq -r '.domain[].summary'
curl -sS -H "$adm" -X POST "$api/revisions/$candidate/apply" | jq -r .status
run=$(curl -sS -H "$auth" -X POST "$api/scenarios/mqtt-cut/runs" | jq -r .id)
until [ "$(curl -sS -H "$auth" "$api/runs/$run" | jq -r .status)" != running ]; do sleep 0.5; done
curl -sS -H "$auth" "$api/runs/$run" | jq -r .status
```

```text output
profile "weak-wifi" added
scenario "mqtt-cut" added
active
passed
```

A stale base is refused, not merged. Here someone else makes a revision active after you read the base, so your change, which still
names the old base in `If-Match`, is refused (the example does not depend on how many revisions the gateway has):

```sh run
adm="Authorization: Bearer $CHAOSGW_ADMIN_TOKEN"
api="$CHAOSGW_URL/api/v1"
base=$(curl -sS -H "$adm" "$api/revisions/active" | jq -r .id)
other=$(curl -sS -H "$adm" -H "If-Match: \"$base\"" -X POST "$api/revisions/$base/clone" | jq -r .id)
curl -sS -H "$adm" -X POST "$api/revisions/$other/apply" | jq -r .status
curl -sS -H "$adm" -H "Content-Type: application/merge-patch+json" -H "If-Match: \"$base\"" \
  -d '{}' "$api/revisions" | jq -r .code
```

```text output
active
revision_conflict
```

`GET /revisions` lists the revisions with their status (`candidate`, `pending_confirm`, `active`, `superseded`, `rolled_back`),
`GET /revisions/{id}/diff?base=` compares two, `GET /revisions/{id}/export` returns a configuration as YAML (WireGuard keys are left out
unless the call says `include_secrets=true`, which needs the scope `full`) and `POST /revisions/{id}/clone` (with `If-Match`) makes a new candidate of an
old revision, which is also the way out of [safe mode](recovery.md#safe-mode). A revision that deletes an object an overlay or a run
refers to is refused with `validation_failed` and the list of references; `?force=true` on the apply removes the overlays and aborts the
runs.

A document that you keep in git, such as the scenario of a CI job, is checked against the schema of the spec the same way the
gateway checks it:

```yaml schema=Scenario
name: link-outage
target: {device: esp32-42}
steps:
  - {id: slow, at: 0s, fault: {latency: 200ms, jitter: 50ms}}
  - {id: cut, at: 1s, rule: {action: drop, protocol: tcp, ports: [8883], cut_existing: true}}
  - {id: back, at: 3s, restore: true}
checks:
  - name: the device reconnects within 30 s
    reconnected: {protocol: tcp, ports: [8883]}
    window: {from: back, within: 30s}
```

## Captures and diagnostics

A capture records the packets of a device, a network or a selector as pcapng. It runs until it is stopped (or until its quota or
the free space of the volume says stop) and can be downloaded while it runs. A capture that holds decrypted traffic or TLS keys
(`sensitive: true`) needs the scope `full` to download.

```sh run
auth="Authorization: Bearer $CHAOSGW_TOKEN"
api="$CHAOSGW_URL/api/v1"
id=$(curl -sS -H "$auth" -H "Content-Type: application/json" -d '{"scope": {"device": "esp32-42"}, "name": "boot"}' "$api/captures" | jq -r .id)
curl -sS -H "$auth" -X POST "$api/captures/$id/stop" > /dev/null
curl -sS -o boot.pcapng -w '%{http_code} %{content_type}\n' -H "$auth" "$api/captures/$id/download"
```

```text output
200 application/x-pcapng
```

`POST /diagnostics` runs a check from the gateway or from a probe: `ping`, `tcp`, `udp`, `dns`, `http`, `tls` or `path` (DNS, route,
TCP and TLS one after the other, up to the first step that fails). A diagnostic from the gateway's own address does not meet a
device's faults; one from a **probe** (a virtual client with its own address in a test network, `GET /probes`) does, which is what makes
it a measurement of what the device experiences. A diagnostic may aim at any address the gateway reaches, and every run is written to
the audit log as `diagnostic.run`.

```sh run
# can the gateway reach the port of its own API? (the port of $CHAOSGW_URL, on the loopback)
port=${CHAOSGW_URL##*:}
curl -sS -H "Authorization: Bearer $CHAOSGW_TOKEN" -H "Content-Type: application/json" \
  -d "{\"type\": \"tcp\", \"host\": \"127.0.0.1\", \"port\": $port}" "$CHAOSGW_URL/api/v1/diagnostics" | jq -c '{type, from, ok}'
```

```text output
{"type":"tcp","from":"gateway","ok":true}
```

The request for a probe, as a document (schema `DiagnosticRequest`):

```yaml schema=DiagnosticRequest
type: path
host: broker.example.com
port: 8883
from: {probe: probe-iot}
```

## Clients

- **`chaosctl`** is a single Go binary for CI jobs: `chaosctl fault set`, `overlay create`, `run -f scenario.yaml --wait --junit
  report.xml`, `wait device`, `with … -- command` (runs a command under a fault and always cleans up), `explain`, `reset`. It reads
  `CHAOSGW_URL` and `CHAOSGW_TOKEN` (or `--token-file`) and trusts a self-signed gateway through `CHAOSGW_CA_CERT`,
  `CHAOSGW_FINGERPRINT` or `CHAOSGW_INSECURE`. Standard output is data and standard error is talk, so `id=$(chaosctl fault set …)`
  works. Exit codes: `0` it holds or the run passed, `1` a check failed, `2` the run ended as error or aborted or the command failed,
  and `with` returns the exit code of its command (125 when `chaosctl` itself failed, 126 and 127 as `env(1)`). Cookbook: [a CI
  job](cookbook.md#a-ci-job-that-runs-a-test-under-a-fault).

  ```sh run
  chaosctl fault set --device esp32-42 --latency 200ms --jitter 50ms --ttl 1m
  chaosctl overlay list
  chaosctl reset
  ```

  ```text output
  ID … KIND … OWNER … EXPIRES
  … fault … token:test-overlays … …
  removed 1 overlays, aborted 0 runs (generation …)
  ```

- **Python and TypeScript clients** are generated from `api/openapi.yaml` (`make generate-python`, `make generate-web`; they are not
  committed). [`clients/examples`](../clients/examples/README.md) holds a pytest suite and a Jest suite that put a device under a
  fault inside a test and clean up in the fixture, and a scenario file; they run in CI against a simulated gateway.
- **Metrics**: `GET /metrics` is the Prometheus text format (scope `read`): the packets and bytes of every fault, rule and port
  forward, the netem queues, the interfaces, the devices and the health of the components.
- **The `</> API` panel** of the web app shows, for the dialog that is open or the last action, the request that does the same, as
  HTTP or as `curl`.
