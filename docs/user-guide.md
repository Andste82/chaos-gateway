# User guide

How to use Chaos Gateway day to day: what it is, how the web app is laid out, and how to do the things you bought it for. The
installation is [install.md](install.md); automation is the [API guide](api-guide.md) and the [cookbook](cookbook.md); when something
does not work see [troubleshooting](troubleshooting.md) and [recovery](recovery.md). What this version can and cannot do is in the
[release notes](release-notes/v1.0.0.md).

## Contents

- [What it is](#what-it-is)
- [The ideas to know](#the-ideas-to-know)
- [The web app](#the-web-app)
- [Devices](#devices)
- [Faults and profiles](#faults-and-profiles)
- [Access rules](#access-rules)
- [DNS faults](#dns-faults)
- [TLS cases and interception](#tls-cases-and-interception)
- [DHCP actions](#dhcp-actions)
- [Scenarios and runs](#scenarios-and-runs)
- [Captures](#captures)
- [Diagnostics and probes](#diagnostics-and-probes)
- [Networks, WireGuard and port forwarding](#networks-wireguard-and-port-forwarding)
- [Changing the configuration](#changing-the-configuration)
- [From a test: chaosctl](#from-a-test-chaosctl)
- [Security](#security)
- [What it does not do](#what-it-does-not-do)

## What it is

Chaos Gateway is a Linux machine that sits as the **gateway** between a test network with devices under test (IoT devices, mostly) and
the upstream network. Everything the devices send passes through it, so it can slow it, break it, answer it wrongly, record it and
measure it, per device, per kind of traffic, in both directions, from a web app or a script. It also does what a gateway does for
them (routing, NAT, DHCP, DNS); that is the foundation, not the point.

```text
Test automation (REST, chaosctl)        Browser (web app)
              \                              /
Devices under test --> [ Chaos Gateway ] --> upstream network, servers
  (test network)        routing, DHCP, DNS
                        firewall, faults, TLS cases, capture
```

You describe **what should happen** to traffic ("the device's upload gets 200 ms more latency", "the broker's name does not resolve",
"the broker's certificate has expired"); the gateway works out how Linux does it, applies it, checks in the kernel that it is in force,
and removes it again when the time is up. A test that crashes does not leave the network broken.

## The ideas to know

| Idea | Meaning |
|---|---|
| **Network** | a test network: a subnet with the gateway's address on a bridge, DHCP and DNS, the physical ports attached to it. Also WireGuard networks and the management and uplink networks, which the operating system keeps |
| **Device** | a device under test, recognised by MAC, IP address or WireGuard peer. *Configured* (you described it) or *discovered* (the gateway saw it) |
| **Group** | a named set of devices |
| **Fault** | a way to impair traffic that matches a selector (who, to where, which protocol and ports, which direction). A fault belongs to one *family*: impairment (latency, jitter, loss, rate, reorder, duplicate, corrupt, blackout, flapping), MTU, DNS, TLS, DHCP or tunnel |
| **Profile** | a named bundle of fault parameters ("Bad LTE"), built in or your own |
| **Access rule** | allow, drop, reject or TCP reset for traffic that matches a selector; an ordered list, first match wins |
| **Overlay** | a change that lives at run time: a fault, profile activation, rule, DNS fault, TLS case or DHCP action with an owner and an expiry. Never part of the configuration; gone at a restart |
| **Configuration and revision** | what is saved: networks, devices, rules, persistent faults, profiles, scenarios. Every change is a new immutable *revision*, previewed and then applied; you can go back |
| **Scenario and run** | a timeline of steps (a fault at 10 s, a rule at 30 s, restore at 45 s) with checks; a *run* is one execution with a report |
| **Probe** | a virtual client inside the gateway that gets an address by DHCP and meets the same faults as a device; for measuring and for testing before the hardware is connected |

**Overlays win over the configuration.** For each fault family the gateway first looks at the overlays, and only if none matches at the
configuration; within a layer the most specific target wins (device, then group, then network, then everything) and, at equal
specificity, the newer fault. Parameters are never merged: the winning fault's complete set applies. Faults of different families add
up (a DNS fault and a latency fault on one device both apply). Access rules are judged before faults: a dropped packet meets no fault.
The **Explain** feature shows the result for any traffic.

A fault applies to the connections a device **initiates**, in both directions (upload is the direction the connection was opened in,
download the replies). Latency values are one way, per direction. A rate limit is per device: "2 Mbit/s" on a network gives every device
its own 2 Mbit/s.

## The web app

Open `https://<management address>:8443`. After the [first setup](install.md#4-finish-the-setup) you log in with the admin password.
The navigation on the left: **Overview**, **Devices**, **Flows**, **Networks**, **Access rules**, **Faults**, **Profiles**, **Scenarios**,
**Captures**, **Interception**, **Diagnostics**. Every page has a header with the revision and whether there are changes not applied yet,
the health of the gateway, the number of active faults and a **`</> API`** button: it shows the request that does what the open dialog
or the last action does, as HTTP or as `curl`. Everything you can do in the web app you can do from a test the same way.

- **Overview**: the topology (test networks, gateway, uplink) with a badge on the path that has a fault, the active faults with target,
  effect, packets affected and the time left, a running scenario with its progress, the uplink traffic and recent events.
- **A red banner across the page** means the gateway is in **safe mode** (it could not apply its configuration; only management access
  works): the banner says why and links to the [recovery instructions](recovery.md#safe-mode). A grey banner means the web app has
  lost the gateway; the data shown is then greyed out with the time of the last update.
- A countdown banner means a change is waiting for your confirmation ([below](#changing-the-configuration)).

## Devices

The **Devices** screen lists every device with its name, address, MAC, network, traffic, faults and state, with a search and filters
(all, online, with faults, not adopted, adopted). A device that the gateway saw but you did not describe is *discovered* and listed under
the host name of its DHCP lease. **Adopt** it to make it a configured device, optionally keeping the address of its current lease as a
fixed address (a DHCP reservation). **Edit** changes a configured device (name, identifiers, network, fixed IP, whether it trusts the
gateway's test CA, labels); **Remove** takes it out of the configuration and lists what refers to it (rules, faults, groups, port
forwards, scenarios), which go with it in the same change.

The device page shows the identity, the faults in force with their parameters, the packets they affected and the time left, the path
from the device through the gateway to one of its destinations (blue as configured, orange where a fault wins, red where a rule drops),
the device's newest connections, its ten newest DNS queries (an answer that a DNS fault changed in orange), its DHCP lease and DHCP
actions, its captures and the port forwards to it, with the buttons for a fault, a DNS fault, a TLS test and a capture.

## Faults and profiles

**Faults** shows the faults grouped by family and sorted by target (device, group, network, any, global), those from the configuration
and those from overlays marked, with the packets each affected, the time left and "overridden by X" where another fault wins.
**Add fault** opens the dialog:

1. the **kind** (impairment, MTU/PMTUD, tunnel, DNS, TLS);
2. the **target**: a device, group, network or everything (a tunnel fault: a WireGuard client or link);
3. the **traffic**: all of it, or a destination (address, prefix, host name, network), protocol and ports;
4. the **direction**: both (the same in each) or upload and download each with its own values;
5. the **values**: start from a preset (LTE, Bad LTE, Satellite, Offline) and adjust latency, jitter, loss and the duration; *Advanced*
   has rate, queue limit, reorder, duplicate, corrupt, keep packet order, delay distribution, burst loss, loss correlation and flapping;
6. **Save as**: an *overlay* (applies now and goes with its duration or at the next restart) or *Configuration* (a persistent fault in
   the next revision). Test faults are overlays.

The preview sentence under the form says what it will do ("ESP32-42 upload: +200 ms ± 50 ms"). A DNS fault and a TLS case are always
overlays. A fault that matched no packet shows 0 affected packets: that is how you notice that it never hit the traffic you meant.

**Profiles** has a card per profile (built in: Normal, LTE, Bad LTE, Satellite, Congested WiFi, Offline, Intermittent, DNS broken, TLS
broken) with its parameters and where it is active. Activate one on a scope with a duration, or make your own (name, description,
impairment and MTU parts) through the draft and preview. One profile can be active per scope. **Measure with probe** on a card checks on a
probe that the link really behaves like the profile says ([Diagnostics and probes](#diagnostics-and-probes)). The built-in values are
starting points, not measurements of a particular network.

Two things to know about the faults: *jitter reorders packets* (netem does; *keep packet order* trades that for a changed delay
distribution), and *loss of a locally generated reply* is not what a device would see from a distant server, which is why services on the
gateway (DNS, the TLS responder) run behind the same path as forwarded traffic.

## Access rules

**Access rules** is the ordered list of rules: position, name, who, to where, action (Allow, Drop, Reject, TCP reset), how many packets
hit it and an enable switch. The first matching rule wins; overlay rules (from tests) are shown above the configured ones. **Control
plane access** is locked at the top: the management network can always reach the UI, API and SSH. The editor has *IF* (from, to,
protocol, ports) and *THEN*, and a box that says how many packets matched and what the rule does in words. *Also cut existing
connections* resets the connections that are open, as a real outage would; without it a new rule applies to new connections only.

Reject answers with an ICMP error of your choice (port unreachable by default). Whether networks may talk to each other is the **access
matrix**, set per pair of networks; a rule with *Allow* is an exception to it. Rules with a host name apply to the addresses the gateway's
DNS proxy handed out for that name: best effort, not for a device with a hardcoded address or DNS over HTTPS.

## DNS faults

The gateway's DNS proxy answers the test networks, so the web app can make it fail: **Add fault**, kind DNS. Choose an action (NXDOMAIN,
SERVFAIL, no answer, delayed answer, wrong answer, truncated answer, short TTL), the host names (exact or `*.suffix`, empty is every name)
and the target. The DNS log of a device (the device page) shows what it asked. The device has to use the gateway's DNS (DHCP gives it
out); *redirect hardcoded DNS* on a network sends queries to other resolvers to the proxy too.

## TLS cases and interception

A **TLS case** is a certificate or handshake that is wrong in one way, shown to a device that connects to a port you choose: untrusted CA,
self-signed, expired, not yet valid, host name mismatch, and the handshakes that are reset, closed or stalled. The gateway redirects the
connection to its TLS responder. A device that accepts the broken certificate has a bug. The three cases signed by the gateway's own
**test CA** (expired, not yet valid, host name mismatch) can only be told from an unknown issuer by a device that trusts that CA, a
development firmware with the CA installed; download it with **Test CA** on the Faults screen or `chaosctl tls ca`, and mark the device
"trusts the test CA". Production firmware can be tested with the other cases without any trust.

**Interception** (the Interception screen, `chaosctl intercept`) goes further: the gateway decrypts the TLS of a device that trusts its
CA, and applies HTTP rules to the exchanges (answer with a status, delay, abort, change a header) and records them. It is an optional
part of the gateway (a container of its own), is outside the scope of the first release and is not validated on ARM64
([release notes](release-notes/v1.0.0.md)).

## DHCP actions

The **DHCP test actions** card on a device and on a network does what the DHCP server cannot be made to do by a device: *short lease*
(brings a renewal forward), *delete lease* (once), *force a new IP* (a device's reservation moves, so its renewal is refused and it asks for
a new address), *set options* (router, DNS server, domain, NTP server, any option) and *silence* (the server does not answer). They are
overlays: they end with their duration. The DHCP leases of a network, its reservations and their state are on the Networks screen.

## Scenarios and runs

A **scenario** is a timeline: steps at offsets (a profile, a fault, a rule, a DNS fault, a TLS case, a DHCP action, a WireGuard action,
a capture start or stop, a wait, a removal, a restore), and **checks** that say what the device must do. The Scenarios screen lists
them with the target, length, steps and last result; a scenario has the tabs *Timeline*, *YAML* and *Runs*. Press **Run**: the page
shows the run live (the current step, checks turning to passed or failed), and the artifacts appear at the end (JUnit, JSON, events,
captures). **Abort** ends a run and removes its overlays.

```yaml schema=Scenario
name: mqtt-outage
target: {device: esp32-42}
capture: true
steps:
  - {id: normal,  at: 0s,  profile: normal}
  - {id: slow,    at: 10s, fault: {latency: 200ms, jitter: 50ms}}
  - {id: lossy,   at: 20s, fault: {latency: 200ms, jitter: 50ms, loss: 10%}}
  - {id: cut,     at: 30s, rule: {action: drop, protocol: tcp, ports: [8883], cut_existing: true}}
  - {id: restore, at: 45s, restore: true}
checks:
  - {reconnected: {protocol: tcp, ports: [8883]}, window: {from: restore, within: 30s}}
  - {dns_query_seen: {name: broker.example.com}, window: {from: restore, within: 30s}}
```

- Every step creates or **replaces** an overlay owned by the run: `lossy` has the same key as `slow` (same family, same traffic), so it
  replaces it with its complete parameter set. `remove: <step>` removes one step's overlay, `restore` all of them.
- A `wait` step (`online`, `connected`, `dns_query`) holds the timeline until its condition is true; later steps shift by the waiting time,
  and a timeout ends the run as `error`.
- Before the first step a run checks that the device is online, uses the gateway's DNS (when the scenario has DNS or host name
  steps) and has no foreign overlays on it; if not, the run ends as `error` and says why.
- **Only one run per target** (device, group or network) at a time; the others wait in `queued`.
- A fault or rule that matched no packet gives the warning `zero_hit`; with `zero_hit: error` the run fails. A "passed" run whose fault
  never hit proves nothing.
- Checks read the connection table: a packet that the gateway drops leaves no entry there. `traffic_not_seen` passes for a device that keeps
  sending to a blocked destination, and `traffic_seen` of it fails. Use the counters of the rule to see the blocked packets.
- The run timeline records when each step was handed to the engine and when its apply was verified; a step more than 100 ms late gets the
  warning `step_late`. On slow hardware the apply takes longer; the report shows both times.
- A run ends as `passed`, `failed` (a check failed), `error` (a step or precondition could not be executed) or `aborted`.

YAML is the import and export format, so scenarios can live in git; a CI job can run a file from its own branch without storing it
([cookbook](cookbook.md)).

## Captures

The **Captures** screen records the packets of a device, a network or a selector (for example "group sensors to TCP 8883") as pcapng,
with a ring buffer, a size limit and the quota of the volume. You can download a capture while it runs and open it in Wireshark. A
capture of a device shows the device's frames before NAT, and of a WireGuard network the raw IP packets. The storage card shows how much
of the quota is used and the free space of the volume; below 10 % free space running captures stop and new ones are refused. Captures
that hold decrypted traffic or TLS keys are marked SENSITIVE and need the full-access scope. The uplink can only be captured as a
whole (its packets cannot be told apart after NAT), and the clients of a *management* WireGuard network are never recorded. A scenario
with `capture: true` attaches its capture to the run.

## Diagnostics and probes

**Diagnostics** runs a check from the gateway or from a probe: *Path* (DNS, route, TCP and TLS one after the other up to the first step
that fails, with the result in one line such as "DNS ok, route ok, TCP ok, TLS certificate expired"), *Ping* (loss and round trips), *TCP*,
*UDP*, *DNS* (the gateway's proxy or a server of your choice), *HTTP(S)* and *TLS* (certificate details). From the gateway's own
address a diagnostic does not meet a device's faults; from a **probe** it does.

A **probe** is a virtual client in a test network (a network namespace on the network's bridge) that gets its address by DHCP and can be
targeted by rules, faults and profiles like a device. The Probes card lists them with their address and state and offers *Diagnose from
here* and *Measure…*; **Add probe**, edit and remove go through the draft like the port forwards (`chaosctl probe add`). **Measure**
checks on the probe that a profile or fault does what it says (latency, jitter, loss, rate, within the tolerances of the test): the
self-test of the link before you trust a result. A profile measured on a loaded small machine (a Raspberry Pi) can miss its tolerance
because of the machine, not of the fault.

## Networks, WireGuard and port forwarding

**Networks** has a card per test network, the management network and the uplink. A test network has its subnet and gateway address, the
ports attached to it, DHCP (pools, lease time, reservations, options), DNS (static entries, redirect of hardcoded DNS) and the DHCP
leases. The test interfaces belong to the gateway alone: the operating system must not configure them (the setup and the preflight say
what to remove). IPv6 is blocked in this version.

- **WireGuard networks** are a *hub* with many clients (a remote lab host, a phone that tests from outside, with *client networks*: the
  subnets behind a client) or a point-to-point *link* to another site. A client's configuration is exported as a wg-quick file, a QR
  code or a zip; the private key is shown once if you asked for that. A WireGuard network has a role: *test* (the clients are test
  clients, subject to rules and faults) or *management* (the administrator's tunnel, never impaired or recorded, and confirmed like any
  change that could lock you out). Tunnel faults (latency, loss, blackout, flapping of the encrypted traffic) and WireGuard actions
  (peer off, wrong key, endpoint blocked) test how a client copes with a bad link.
- **Routing**: static routes and BIRD (BGP, OSPF, Babel) towards other sites over links, with import filters, protected prefixes and
  a maximum number of prefixes, so a remote site cannot divert your traffic. Learned routes live only in the gateway's own tables.
- **Port forwarding** (on the uplink and management cards) forwards a port of the gateway's address to a device, for a remote
  lab host or a tester that must reach a device. A rule whose source is the device also covers connections that reach the
  device through a forward, by the initiator's side: a rule "device X to uplink: drop" stops forwarded connections to X as well.
  Faults do not yet apply to forwarded connections; the preview and `explain` say so.
- The **Linux** view (technical view) shows the nftables, tc and routes the gateway has made, for people who want to check.

## Changing the configuration

You edit a **draft**; nothing changes until you apply it. Changes of the configuration (devices, networks, rules, persistent faults,
profiles, scenarios, probes, port forwards) pile up in the draft and a bar at the bottom says "N changes · not applied yet" with
**Discard** and **Preview & apply**. The preview is a drawer marked "dry run · nothing changed yet": what changes in plain terms (rule,
field, old to new) and, expandable, the Linux changes behind it. **Apply** makes a revision and puts it in force after the gateway has
verified it in the kernel (a toast tells how long that took); if the apply fails, the previous revision is restored and the drawer says
which step failed.

- **Commit-confirm.** A change that could lock you out (the management interface, the binding of the UI and API, a management WireGuard
  network, the gateway's protection) waits for you: an orange banner counts 60 seconds down, with **Confirm** and **Roll back now**.
  When nobody confirms, the previous revision comes back by itself. Other changes are disabled until it is decided.
- **Two editors at once.** Every change names the revision it is based on. If a test or another person applied a revision meanwhile,
  a dialog tells who changed what, and you can show their change, reload and reapply yours, or discard yours. Nothing is overwritten
  silently.
- **Overlays survive an apply**; one that refers to something the new revision deletes blocks it (listing the references) unless you
  choose to remove those overlays and abort their runs.
- **Revisions** are immutable and can be compared, exported (secrets only when you ask) and cloned: the way back to any earlier state is
  a new revision of it.

## From a test: chaosctl

`chaosctl` puts the gateway into a test script. It takes `CHAOSGW_URL` and `CHAOSGW_TOKEN` (a token with the scope `overlays`;
[the API guide](api-guide.md#tokens-and-scopes) says how to make one) from the environment:

```sh run
chaosctl fault set --device esp32-42 --latency 200ms --loss 5% --ttl 1m
chaosctl explain --device esp32-42 --dst 203.0.113.10 --protocol tcp --port 8883 | sed -n 1,2p
chaosctl reset
```

```text output
esp32-42 (10.10.0.31, network IoT) -> 203.0.113.10 tcp/8883
removed 1 overlays, aborted 0 runs (generation …)
```

A profile is activated with `chaosctl with --profile` for the length of a command, or with `overlay create`. The most used commands: `fault set`, `dns set`, `tls set`, `dhcp set`, `overlay list|create|delete|renew`,
`run -f scenario.yaml --wait --junit report.xml`, `wait device`, `with … -- command`, `explain`, `reset`, and with the full scope
`probe add|remove`, `wg export` and `certificate show|install|reset`. Every command has `--help` with examples; the exit codes are in the
[API guide](api-guide.md#clients).

## Security

- The web app and API listen on the **management network** over HTTPS (a self-signed certificate until you
  [install your own](install.md#https-certificate)), plus the loopback of the host. A device under test, a probe and a WireGuard test
  client cannot reach them.
- The admin signs in with a password (argon2id; failures are limited, with waits that double up to 15 minutes). Scripts use API tokens
  with one of three scopes (*read*, *overlays*, *full*); a token's value is shown once and only its hash is kept. The scope *overlays*
  is not tied to one device: it can impair any target, aim diagnostics at any address the gateway reaches and download unencrypted
  captures of any device, so treat such a token as a lab-wide credential. Secrets (WireGuard keys,
  the HTTPS key, credentials) never appear in a response, an event, the audit log or a log, except where you ask for a key by name.
- Every change is written to the audit log (who, when, through the web app or the API), including each diagnostic that was run.
- Only the executor container is privileged; it accepts a closed list of typed operations over a Unix socket. The API runs without
  capabilities as an unprivileged user.
- The interception proxy and TLS cases need the device to trust the gateway's CA only for the cases that say so; the CA is yours alone
  (two CAs: the test CA that development firmware trusts, and an unknown one that nobody trusts).

## What it does not do

IPv4 only (IPv6 forwarding is blocked, so faults cannot be bypassed over IPv6); no VLANs yet; no time (NTP) faults; faults on
connections that reach a device through a port forward are a later extension; QUIC (HTTP/3) is not intercepted (block UDP 443 instead);
hostname rules depend on what the gateway's DNS handed out; a PMTU fault with ICMP is cached by the server for the shared NAT address
for up to ten minutes, which can affect other devices that talk to the same server (the MSS clamp has no such side effect). The
[release notes](release-notes/v1.0.0.md) list what is not validated yet.
