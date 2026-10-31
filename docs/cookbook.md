# Scenario cookbook

Recipes for the tests that a device with a network connection should survive. Each one says what is tested, shows the scenario or the
commands, and says what the result means. The examples are run by the CI job `docs` against a gateway on the simulated kernel
([API guide](api-guide.md) for the environment); that gateway carries no traffic, so the checks that need a device that really talks
fail there, and the recipes say so where it shows. The device is `esp32-42`, its network `IoT`, the broker `broker.example.com` on
port 8883. Replace them with yours.

## Contents

- [A CI job that runs a test under a fault](#a-ci-job-that-runs-a-test-under-a-fault)
- [The broker goes away and the device must come back](#the-broker-goes-away-and-the-device-must-come-back)
- [A slow, lossy mobile link during a firmware update](#a-slow-lossy-mobile-link-during-a-firmware-update)
- [The broker's name stops resolving](#the-brokers-name-stops-resolving)
- [A device that must reject a bad certificate](#a-device-that-must-reject-a-bad-certificate)
- [DHCP: a short lease and a new address](#dhcp-a-short-lease-and-a-new-address)
- [Connectivity that comes and goes](#connectivity-that-comes-and-goes)
- [Keep the packets of a failing run](#keep-the-packets-of-a-failing-run)
- [Why is my device refused?](#why-is-my-device-refused)

## A CI job that runs a test under a fault

`chaosctl with` puts a device under a profile or a fault, runs your test command with the streams of the job, and takes the fault
away again however the command ends: it passes, it fails, it is not found, or the job is killed. Its exit code is the command's, so the
job fails when the test fails. A lease (30 seconds by default, renewed while the command runs) lets the gateway remove the fault by itself
if the CI machine disappears.

```sh run
# the test is any command; this one prints the fault it runs under and fails with 3
chaosctl with --device esp32-42 --latency 300ms --jitter 50ms --ttl 10m -- sh -c 'chaosctl overlay list --json | jq -r ".[0].kind"; exit 3' || echo "the test failed with $?"
chaosctl overlay list
test "$(chaosctl overlay list --json | jq length)" = 0 && echo "no overlay left"
```

```text output
fault
the test failed with 3
ID … KIND … OWNER … EXPIRES
no overlay left
```

The overlay list is empty at the end (the last line of the example fails the example when it is not). Under `set -e` the `||` is not needed: the job step fails with the test's code. Set
`CHAOSGW_URL` and `CHAOSGW_TOKEN` (a token with the scope `overlays`, kept as a secret of the CI system) once for the job.

To run the same test under every profile, one after the other (a failing profile stops the loop):

```sh run
for profile in lte bad-lte satellite congested-wifi; do
  echo "== $profile"
  chaosctl with --device esp32-42 --profile "$profile" --ttl 5m -- sh -c 'echo "the test of the device runs here"'
done
```

```text output
== lte
the test of the device runs here
== bad-lte
the test of the device runs here
== satellite
the test of the device runs here
== congested-wifi
the test of the device runs here
```

## The broker goes away and the device must come back

The scenario cuts the device's connection to the broker for twenty seconds and requires that the device builds a new connection
within thirty seconds after the cut ends. `wait` holds the timeline until the device is online, and `cut_existing` resets the open
connection, as a real outage would; otherwise only new connections meet the rule.

```yaml schema=Scenario
name: broker-outage
target: {device: esp32-42}
zero_hit: error
steps:
  - {id: online, at: 0s, wait: {for: online, timeout: 60s}}
  - {id: cut, at: 5s, rule: {action: drop, protocol: tcp, ports: [8883], cut_existing: true}}
  - {id: back, at: 25s, remove: cut}
checks:
  - name: the device reconnects within 30 s
    reconnected: {protocol: tcp, ports: [8883]}
    window: {from: back, within: 30s}
```

```sh run exit=1
cat > broker-outage.yaml <<'EOF'
name: broker-outage
target: {device: placeholder}
steps:
  - {id: online, at: 0s, wait: {for: online, timeout: 60s}}
  - {id: cut, at: 1s, rule: {action: drop, protocol: tcp, ports: [8883], cut_existing: true}}
  - {id: back, at: 2s, remove: cut}
checks:
  - name: the device reconnects within 3 s
    reconnected: {protocol: tcp, ports: [8883]}
    window: {from: back, within: 3s}
EOF
chaosctl run -f broker-outage.yaml --set target.device=esp32-42 --wait --junit report.xml
```

```text output
run … FAILED …
  check the device reconnects within 3 s: failed: no connection matching tcp/8883 was established in the window of 3s
```

That is the run on the simulated gateway, with shorter times: no device connects there, so the check fails, the exit code is `1` and the
JUnit file has a failed test case. On a real gateway the same command passes when the device reconnects.

A check that holds passes the same way: the exit code is `0` and the JUnit file reports the run as `passed`. The simulated gateway carries
no traffic, so `traffic_not_seen` is the check it can show: a device must not fall back to plain MQTT on port 1883 while the link is
lossy.

```sh run
cat > no-plain-mqtt.yaml <<'EOF'
name: no-plain-mqtt
target: {device: esp32-42}
steps:
  - {id: lossy, at: 0s, fault: {latency: 100ms, loss: 20%}}
  - {id: back, at: 1s, restore: true}
checks:
  - name: the device does not fall back to plain MQTT
    traffic_not_seen: {protocol: tcp, ports: [1883]}
    window: {from: lossy, until: back}
EOF
chaosctl run -f no-plain-mqtt.yaml --wait --junit report.xml
echo "exit code $?"
grep -o 'name="status" value="[a-z]*"' report.xml
```

```text output
run … PASSED …
  check the device does not fall back to plain MQTT: passed: no traffic matching tcp/1883 was seen in the window of …
exit code 0
name="status" value="passed"
```

The run also warns `zero_hit` here (the fault matched no packet, because no device sends anything). On a real gateway the device's
traffic hits the fault, and with `zero_hit: error` the run would fail if it did not. `--set` overrides a field
of the scenario by its path (`target.device=…`, `steps.cut.at=10s`), so one file serves every device and every lab. The scenario
ends as `error`, exit code `2`, when the engine cannot carry out a step or a precondition fails (the device is offline); `zero_hit: error`
makes a run `failed` when a rule or fault of it never matched a packet, which is what a test must not be happy with.

## A slow, lossy mobile link during a firmware update

Activate a profile on the device (or on the network, or a group) for as long as the update takes, and let the TTL end it:

```sh run
curl -sS -H "Authorization: Bearer $CHAOSGW_TOKEN" -H "Content-Type: application/json" \
  -d '{"target": {"device": "esp32-42"}, "profile": "bad-lte", "ttl": "20m"}' "$CHAOSGW_URL/api/v1/overlays" | jq -r .state
```

```text output
effective
```

"Bad LTE" is 150 ms ± 50 ms, 3 % loss and 2 Mbit/s per direction and per device (a rate limit on a network gives every device its own
2 Mbit/s). The built-in values are starting points: a custom profile of your own goes into the configuration
([API guide](api-guide.md#changing-the-configuration)), and the web app's Profiles screen has **Measure with probe**, which checks on a
virtual client that the link really behaves as configured. Latency values are one way per direction, so the round trip is the sum of
upload and download. Jitter reorders packets unless *keep order* is set; for a device that cannot cope with reordering say so in the fault
(`keep_order`).

## The broker's name stops resolving

The gateway's DNS proxy answers the devices of the test networks, so it can fail on request: `nxdomain`, `servfail`, `timeout`,
`delay`, `wrong_answer`, `truncate` and `short_ttl`. The device must use the gateway's DNS (DHCP hands it out); with
`redirect_hardcoded` set on the network, queries to a hardcoded resolver are redirected too.

```sh run
chaosctl dns set servfail --device esp32-42 --name broker.example.com --ttl 2m
chaosctl overlay list
```

```text output
ID … KIND … OWNER … EXPIRES
… dns … token:test-overlays … …
```

As a scenario with a check that the device asks for the name again (`dns_query_seen`) once the fault is gone:

```yaml schema=Scenario
name: dns-outage
target: {device: esp32-42}
steps:
  - {id: broken, at: 0s, dns: {names: [broker.example.com], action: servfail}}
  - {id: fixed, at: 20s, remove: broken}
checks:
  - name: the device asks again after the outage
    dns_query_seen: {name: broker.example.com}
    window: {from: fixed, within: 60s}
```

Hostnames are matched by what the DNS proxy resolved: a fault or rule with a hostname applies to the addresses the gateway handed
out for that name, for as long as they live. A device with a hardcoded address or DNS over HTTPS escapes it; the gateway cannot see
that name.

## A device that must reject a bad certificate

The gateway has a TLS responder with a test CA. A TLS case redirects the device's connection to the port to the responder, which shows
the device a certificate that is wrong in one way. The device must not take it. `tls_rejected` passes when the device aborts the handshake.

| Case | Needs |
|---|---|
| `untrusted_ca`, `self_signed` | any device must reject them |
| `expired`, `not_yet_valid`, `hostname_mismatch` | signed by the gateway's test CA: only a device that trusts it (development firmware with the CA from `GET /tls/ca`, `chaosctl tls ca`) can tell them from an unknown issuer |
| `handshake_reset`, `handshake_close`, `handshake_stall` | no certificate: for the behavior of the device when the handshake fails |

```yaml schema=Scenario
name: expired-certificate
target: {device: esp32-42}
steps:
  - {id: expired, at: 0s, tls: {case: expired, ports: [8883], sni: [broker.example.com]}}
  - {id: done, at: 20s, remove: expired}
checks:
  - name: the device refuses an expired certificate
    tls_rejected: {protocol: tcp, ports: [8883]}
    window: {from: expired, until: done}
```

```sh run
chaosctl tls set expired --device esp32-42 --port 8883 --ttl 2m
chaosctl overlay list --json | jq -r '.[0].kind'
```

```text output
tls
```

A case selects new connections; `--cut-existing` resets the open ones. The first activation of a case that only a trusting device
can judge says so in its note (`trusts_test_ca` of the device). Interception, where the gateway decrypts the traffic of a device
that trusts its CA and changes the HTTP exchanges, is the case `intercept` (`chaosctl intercept set`); it is an optional part of the
gateway and is described in the [user guide](user-guide.md#tls-cases-and-interception).

## DHCP: a short lease and a new address

The DHCP server cannot make a client renew at once, so the tests shorten the lease or take the address away:

```sh run
chaosctl dhcp set short-lease --network IoT --lease-time 10s --ttl 5m
chaosctl dhcp set silence --device esp32-42 --ttl 30s
chaosctl overlay list --json | jq -r 'map(.kind) | sort | join(",")'
```

```text output
dhcp,dhcp
```

`short-lease` brings the renewal forward, `delete-lease` forgets the lease once (a renewing client gets the same address), `force-new-ip`
moves the reservation of a device, so its renewal is refused and it asks for a new address, `set-options` changes the options a client
gets (router, DNS server, domain, NTP server), and `silence` makes the server stop answering, which is the test of a device that
must keep its network without DHCP for a while. A device in a network without DHCP cannot be tested with these.

## Connectivity that comes and goes

```sh run
chaosctl fault set --device esp32-42 --flapping 20s/10s --ttl 5m
chaosctl overlay list --json | jq -r '.[0].kind'
```

```text output
fault
```

`--flapping 20s/10s` is up for twenty seconds and down for ten, repeatedly (a timed blackout, so it also hits connections that exist).
The counters of the overlay (`GET /overlays/{id}`) show how many packets it dropped.

## Keep the packets of a failing run

`capture: true` records the target's traffic for the whole run and attaches the capture to it; `--artifacts` saves the JSON report,
the events and the captures of the run into a directory, so the CI system can keep them as build artifacts.

```yaml schema=Scenario
name: capture-the-outage
target: {device: esp32-42}
capture: true
steps:
  - {id: cut, at: 0s, rule: {action: drop, protocol: tcp, ports: [8883]}}
  - {id: back, at: 20s, restore: true}
```

```sh run
cat > capture-the-outage.yaml <<'EOF'
name: capture-the-outage
target: {device: esp32-42}
capture: true
steps:
  - {id: cut, at: 0s, rule: {action: drop, protocol: tcp, ports: [8883]}}
  - {id: back, at: 1s, restore: true}
EOF
chaosctl run -f capture-the-outage.yaml --wait --artifacts artifacts --junit artifacts/report.xml
ls artifacts | sort
```

```text output
events.jsonl
report.json
report.xml
```

On a gateway with traffic the directory also holds the capture (`.pcapng`), which Wireshark opens. A capture that cannot start or ends
early (the volume is almost full, an interface is lost) does not stop the run; the run gets the warning `capture_missing`.

## Why is my device refused?

`explain` answers without sending a packet: the verdict of the access rules and the rule that decided it, the winning fault in each
family and the faults it overrides, and the route. During a test it says whether the gateway is the reason.

```sh run
chaosctl fault set --device esp32-42 --latency 200ms --jitter 50ms --ttl 5m
chaosctl explain --device esp32-42 --dst 203.0.113.10 --protocol tcp --port 8883
```

```text output
esp32-42 (10.10.0.31, network IoT) -> 203.0.113.10 tcp/8883
access: ALLOW (access_matrix): the default: a local test network reaches the uplink
  impairment: … [overlay] latency 200ms ± 50ms
route: dev wan0
```

If the verdict is `DROP`, the line names the rule, an overlay of a test that is still in force, or the access matrix. If a fault you did
not expect wins, its owner and kind are listed. The web app's Faults screen has the same as **Explain**, and for the traffic of a
flow it is the button on the Flows screen.
