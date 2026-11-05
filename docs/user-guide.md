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
- [Time faults and the NTP server](#time-faults-and-the-ntp-server)
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
| **Fault** | a way to impair traffic that matches a selector (who, to where, which protocol and ports, which direction). A fault belongs to one *family*: impairment (latency, jitter, loss, rate, reorder, duplicate, corrupt, blackout, flapping), MTU, DNS, TLS, DHCP, time (the NTP server) or tunnel |
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
actions, the **Time** panel (below), its captures and the port forwards to it, with the buttons for a fault, a DNS fault, a TLS test and a capture.

## Faults and profiles

**Faults** shows the faults grouped by family and sorted by target (device, group, network, any, global), those from the configuration
and those from overlays marked, with the packets each affected, the time left and "overridden by X" where another fault wins.
**Add fault** opens the dialog:

1. the **kind** (impairment, MTU/PMTUD, tunnel, DNS, TLS, Time (NTP));
2. the **target**: a device, group, network or everything (a tunnel fault: a WireGuard client or link);
3. the **traffic**: all of it, or a destination (address, prefix, host name, network), protocol and ports;
4. the **direction**: both (the same in each) or upload and download each with its own values;
5. the **values**: start from a preset (LTE, Bad LTE, Satellite, Offline) and adjust latency, jitter, loss and the duration; *Advanced*
   has rate, queue limit, reorder, duplicate, corrupt, keep packet order, delay distribution, burst loss, loss correlation and flapping;
6. **Save as**: an *overlay* (applies now and goes with its duration or at the next restart) or *Configuration* (a persistent fault in
   the next revision). Test faults are overlays.

The preview sentence under the form says what it will do ("ESP32-42 upload: +200 ms ± 50 ms"). A DNS fault, a TLS case and a time fault are always
overlays. A fault that matched no packet shows 0 affected packets: that is how you notice that it never hit the traffic you meant.

**Profiles** has a card per profile (built in: Normal, LTE, Bad LTE, Satellite, Congested WiFi, Offline, Intermittent, DNS broken, TLS
broken, and the eight time presets) with its parameters and where it is active. Activate one on a scope with a duration, or make your own (name, description,
impairment and MTU parts; through the API a profile can also carry a DNS, TLS or time part, which the profile editor keeps but does not edit).
The names of the built-in profiles are reserved. One profile can be active per scope, so activating a profile replaces the one that is active there (a time
preset on a device replaces an LTE profile of that device: for both, add a time fault of its own next to the profile, see *Time faults* below).
A profile with a time part is activated on a device, a group or a network only: on *Everything* or a remote network the activation is refused. **Measure with probe** on a card checks on a
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

## Time faults and the NTP server

Wrong time is a frequent cause of failures in IoT devices: a certificate that is "not yet valid" after a reset to 1970, a token that
expires at once, the 2038 overflow. Devices take their time from NTP, and NTP is not authenticated, so the gateway runs its **own NTP
server** (`chaosgw ntp`, in the service namespace like the DNS proxy) and answers instead of the real one. It never forwards a query.

**Normal operation.** The server serves the clock of the gateway's host: stratum 2, leap indicator 0, receive time from the kernel's
time stamp of the packet. While the host clock is not synchronized (the preflight check `host.clock` warns, and the health component
`ntp` says so) the server honestly answers stratum 16 with leap indicator 3, which a correct client ignores (a fault with an absolute
time does not use the host clock and is served as it is; the invalid answers, which carry a time too, claim no synchronization either while the host clock is wrong): start `chrony` or
`systemd-timesyncd` on the host. A test network has the time service on by default, and DHCP hands out the gateway's address as the
NTP server (option 42) unless the network names other servers, its time service is off, or the gateway has no service namespace (the health component `ntp`
says `disabled`). Control and private modes (6 and 7) get no answer, no reply is larger
than its query, and a device that asks more than 16 times a second is not answered beyond that. The server answers only sources inside the
address range of a test network (the networks behind its routers and the routes of WireGuard peers included): a datagram with another source address
gets no answer and no entry in the log. A WireGuard client gets no DHCP, and the group *Time (NTP)* of the network edit form exists for LAN networks only: point the
client at the gateway's tunnel address, or activate a time fault for it, which redirects its UDP 123 traffic to the server.

**Hardcoded servers.** Most firmware has its NTP server compiled in. A network setting **Redirect NTP to other servers to the gateway's
NTP server** (Networks, Edit, group *Time (NTP)*) sends such queries to the gateway's server, and the reply leaves with the address
and port of the server the device asked. A time fault redirects the NTP traffic of its target by itself, whether or not the network
setting is on, because a fault that silently misses the device would let a test pass that should fail. Where the redirect cannot work
(the service namespace is gone) the traffic fails closed: the real server is not reached.

**A server inside the device's own network is out of reach.** The gateway redirects the queries that pass through it. A query to a server in the
same segment as the device (the network and the networks behind its routers: a lab NTP server at 10.10.0.5 for a device in 10.10.0.0/24) is
switched or routed directly and never meets the redirect, as with the hardcoded DNS redirect. A time fault for that server, or a time fault for every server
of a device that gets such a server from DHCP, does not change what the device sees; the fault still shows as in force. The preview warns
(`ntp_server_inside`) when a fault names a destination inside the network's segments or applies to a network whose DHCP hands out an NTP server inside them,
and `explain` reports no time fault for such a destination. Put the lab server in another network, or point the device at the gateway. The gateway's own address of the
network is not affected: it is served, with the fault.

**Time faults.** **Add fault**, kind *Time (NTP)*, target a device, group or network (not *Everything*, not a remote network).
Choose what the server answers:

| Answer | What the server does | What it tests |
|---|---|---|
| a time of your choice | the correct time plus an **offset** (`+400d`, `-90s`, `-1y2d`; a year has 365 days), or an **absolute** instant that runs on from the moment the fault starts; on top of it a **drift** in ppm (1000 ppm adds 10 ms in 10 s) and a **leap** indicator (insert or delete) | certificates "not yet valid" or expired, tokens, schedules, clock discipline, leap-second handling |
| unsynchronized | leap indicator 3, stratum 16 | a correct client ignores the server; a careless one takes its time |
| kiss-of-death | stratum 0 with the code `RATE` (poll less often), `DENY` or `RSTR` (stop using the server, as RFC 5905 says) | the back-off of the client; chrony reacts to `RATE` only and treats the other codes as an unsynchronized answer |
| silence | no answer; the query is counted | no time at boot, fallback servers, retry back-off (a drop rule would not count the queries and would hit more than NTP) |
| an invalid answer | the origin timestamp is wrong, the transmit timestamp is zero, or the mode is not 4 (on a host whose clock is not synchronized: leap indicator 3 and stratum 16 as well, unless the fault has an absolute time) | a correct client discards it; a careless one is open to spoofing |

The **presets** are +1 year, -1 day, 1970, 2038 (five minutes before a signed 32-bit `time_t` overflows), NTP era 1 (five minutes before
the 32-bit seconds of NTP roll over), leap second (five minutes before the end of 2016-12-31 with a second inserted), unsynchronized and
kiss-of-death; the same eight are the built-in profiles `time-plus-1y`, `time-minus-1d`, `time-1970`, `time-2038`, `time-ntp-era-1`,
`time-leap-second`, `time-unsynchronized` and `time-kiss-of-death` (the profile editor of the web app keeps a time part but does not edit one: a
profile of your own with a time part is made with the API). A preset activated as a profile follows the rules of profiles: it takes the profile slot of its
scope (an active LTE profile of that device is replaced; to keep both, use the time fault form or `chaosctl time set`, which add a time overlay next to the
profile) and it is activated on a device, a group or a network, not on *Everything* or a remote network. A preview sentence under the form says what it will do. The traffic
fields name the **server the device asked** (an address, a prefix or a host name; ports default to 123): then only queries to that server get
the wrong time ("only `time.google.com` is wrong"), and the other servers of the device are answered correctly. Without it the fault applies
to every server the device asks. The ports 53, 67 and 68 are refused.

Things to know:

- **The change is a jump.** The served time jumps when a fault starts and jumps back when it ends (the second jump is often the more
  interesting test), and a device sees either only when it polls next, which may be minutes or hours. A scenario waits for the next
  query with the wait **NTP query seen** (`chaosctl wait ntp`) instead of guessing.
- **A time fault competes only with time faults**, by the rules of the other families (device before group before network, the newer first).
  A latency fault of the device does not cancel a wrong time. A delayed or asymmetric NTP path is a latency fault with the selector
  UDP port 123 (a delay in one direction shifts the offset a client measures by half of it).
- **What is not covered.** NTS (authenticated NTP) cannot be answered by a third party: a device that uses it gets no time from the gateway,
  and the Time panel notes the key-establishment connections (TCP 4460) that it saw. Block NTS-KE with a rule to make such a device fall
  back. HTTP `Date` headers, cellular time, GPS and the device's own RTC are not affected. IPv6 follows with the IPv6 milestone, and time
  faults are overlays only, like DNS faults and TLS cases.
- **The Time panel** of the device page shows the last query (when, how it was answered), the server asked where the gateway knows it (an
  address when a fault names one or the gateway sees the flow; "an external server (redirected)" for a hardcoded server nothing settles), the time served and its
  difference from the correct one, the time faults in force for the device, the counters (queries, left unanswered by a fault, over the
  rate limit), a note when the device tried NTS, and the last queries. The count over the rate limit is a sample: the server records at most one dropped query a second per device, so the number is not
that of the dropped queries. While the list of profiles is not loaded the panel says that the active time faults are not known, as an activated profile is a time fault only if it has a time part. It reads the state every five seconds. The event *NTP answered* (orange on the Overview) marks an answer that a time fault
  shaped; the server reports at most one per device and fault every ten seconds.
- **A scenario** has the step type *time* (the same fields as the dialog), the wait *NTP query seen* and the check *NTP queried*: the
  device asked the server in the window, whatever the answer was. Whether the device took the time is not visible on the network;
  its effect is (a TLS check, the device's own log). The run warns `zero_hit` for a time fault that shaped no answer, which usually means the
  device does not ask the gateway (a hardcoded server on a network without the redirect, which a time fault redirects anyway, or NTS).

From a script: `chaosctl time set offset --offset +400d --device esp32-42 --ttl 5m`, and the recipe in the
[cookbook](cookbook.md#a-device-whose-clock-is-wrong).

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
a time fault, a capture start or stop, a wait, a removal, a restore), and **checks** that say what the device must do. The Scenarios screen lists
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
- A `wait` step (`online`, `connected`, `dns_query`, `ntp_query_seen`) holds the timeline until its condition is true; later steps shift by the waiting time,
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
ports attached to it, DHCP (pools, lease time, reservations, options), DNS (static entries, redirect of hardcoded DNS), the time
service (on or off, redirect of hardcoded NTP servers) and the DHCP leases. The test interfaces belong to the gateway alone: the operating system must not configure them (the setup and the preflight say
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

A profile is activated with `chaosctl with --profile` for the length of a command, or with `overlay create`. The most used commands: `fault set`, `dns set`, `tls set`, `dhcp set`, `time set`, `overlay list|create|delete|renew`,
`run -f scenario.yaml --wait --junit report.xml`, `wait device`, `wait ntp`, `with … -- command`, `explain`, `reset`, and with the full scope
`probe add|remove`, `wg export` and `certificate install|reset` (`certificate show` needs only `read`). Every command has `--help` with examples; the exit codes are in the
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

IPv4 only (IPv6 forwarding is blocked, so faults cannot be bypassed over IPv6); no VLANs yet; time faults reach a device only through NTP (not NTS, HTTP `Date`, cellular time, GPS or an RTC); faults on
connections that reach a device through a port forward are a later extension; QUIC (HTTP/3) is not intercepted (block UDP 443 instead);
hostname rules depend on what the gateway's DNS handed out; a PMTU fault with ICMP is cached by the server for the shared NAT address
for up to ten minutes, which can affect other devices that talk to the same server (the MSS clamp has no such side effect). The
[release notes](release-notes/v1.0.0.md) list what is not validated yet.
