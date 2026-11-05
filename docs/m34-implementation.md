# M34 — NTP server and time faults: how it was built

The map that the four stages of M34 (`docs/plan.md` §2.18, the milestone M34, decisions D36 and D37) were built from. It says where each part
went in the code, by the pattern that was already there, and it stays as the record of that; what the parts do and how they are tested is in
`docs/development.md` ("NTP server and time faults (M34)") and the guides. Paths are of the repository root. The pattern for the server is the DNS
proxy (`internal/dnsproxy`), for the fault family the application-layer families (DNS faults, TLS cases, DHCP actions).

Where the built milestone differs from the map:

- **Two reads in the public API** that stage 1 did not plan: `GET /ntp/queries` and `GET /devices/{deviceId}/ntp` (`internal/api/ntpreads.go`, stage 3), which the *Time*
  panel and `chaosctl wait ntp` use.
- **The presets** are eight built-in profiles with a `time` part (`time-plus-1y` and so on, `internal/domain/builtin.go`), as well as the presets of the form
  (`web/src/lib/timeForm.ts`); both name the same faults.
- **The profile editor** of the web app does not create or edit a `time` part (it keeps one that comes from a built-in profile or the API), as it does not for `dns` and `tls` (decided 2026-11-05, P34-09 in `docs/open-items.md`: the editor keeps, shows and activates the part but does not edit it).
- **The release notes** of the next release are written with that release; `docs/release-notes/v1.0.0.md` says that the NTP server is not in it, which is true.
- **The `Supported*` lists** (`SupportedOverlayKinds`, `SupportedFaultFamilies`, `SupportedStepTypes`, `SupportedCheckTypes`) now include `time` and `ntp_queried` (stage 2 and 3), and the `422 unsupported_feature` stub and its tests of stage 1 are gone; the text of stage 1 below says what the stub did at the time.

## Stage 1: specification and domain (no behaviour)

- `api/openapi.yaml`:
  - `FaultFamily` and `OverlayKind` get `time`; `OverlayRequest` and `Overlay` get the member `time: TimeFault` (and the key table and the
    "one of profile, fault, rule, dns, tls, dhcp" sentences get their words); new schema `TimeFault` (`offset`, `absolute`, `skew_ppm`,
    `leap`, `answer`, `kiss_code`, and a destination selector like `TlsCase` embeds `TrafficMatch`: the existing `Destination` with
    hostname, plus `ports`); `ProfileParts.time`; `Step.time`; `WaitCondition.for` gets `ntp_query_seen`; `Check` gets `ntp_queried`
    (model: `DnsQueryCheck`); `EventType` gets `ntp_answered`, `ntp_query_seen`, `nts_seen`; `Capabilities.features` gets `time.faults`
    and `ntp.server`; `NetworkLan` gets an `ntp` block (model: `NetworkDns`: `enabled`, `redirect_hardcoded`) and the description of
    `dhcp.options.ntp_servers` names the default; the health components get `ntp`.
  - internal: `/internal/ntp/config` (long poll, `x-internal`, scope `service`; schema `NtpServiceConfig` after `DnsServiceConfig`: a
    generation, the faults with `overlay`, `sources` and the redirect `port`, and the ports to listen on) and `/internal/ntp/queries`
    (after `/internal/dns/queries`).
  - `make generate`, `make check-generated` (the committed generated Go is `internal/model/model.gen.go` and
    `internal/apiserver/server.gen.go`), examples in `api/examples/{overlays,configuration}.yaml` and the new `api/examples/run-request-time.yaml` (a run request with a time scenario; `run-request.yaml` keeps the mqtt-outage one; the scenario `stale-clock` of the configuration carries the time step, the NTP wait and the NTP check; `api/examples/validate.py` and the example tests know the new file), `make check-spec`.
- `internal/domain`: `overlay.go` (`overlayKinds`, `OverlayKindOf`, `ValidateOverlay`, `NewOverlay`, the key selector), `validate.go` (the code
  `invalid_time_fault`, and its row in the error table of `docs/development.md`), `timefault.go` (the `timeFault` validator, the offset arithmetic and the candidates of the family, kept in a file of their own;
  `validate_fault.go` only points to it; a time target is a device, a group or a network, never global; the profile part), `resolve.go` (the family, `timeCandidate`,
  matching, the label; a time fault competes only with time faults, device over group over network, newer wins), `profile.go`,
  `validate_scenario.go` and `scenario_run.go` (step, wait, check; a hostname destination also needs DNS, as `ScenarioUsesDNS`).
- `internal/engine/overlays.go`: the `Supported*` lists stay as they are until stage 2, so the engine answers `422 unsupported_feature`.
- Not persistent: `ConfigFault` and `FaultBody` are unchanged (M39).

## Stage 2: server and wiring (the core)

- **`internal/ntpserver`** (new, after `internal/dnsproxy`): `Options` with an injectable `Clock` and a `SyncState` interface (the testbeds say
  synchronized; production reads `adjtimex`); packet encode and parse; the answer kinds; offset, absolute, skew, leap; era 2036 and 2038;
  rate limit per device (16 per second, drop); modes 6 and 7 get no answer; a reply is never larger than its request; `Follow` and an API client
  on `internal/svcapi` (long poll, back-off as `dnsproxy/apiclient.go`); a query log sink; the listening ports come from the config
  (decision D36). Level 0 tests with `clock.NewFake` and `JumpWall`; a fuzz target of the parser.
  An `offset` is not bounded against the NTP epoch by the validation (it adds to the clock of the moment): the server decides what it serves
  when `now + offset` is before 1900 or past the end of era 0 (2036), and the acceptance tests include both cases (a query of `-130y` and of `+20y`).
- **`cmd/chaosgw/ntp.go`** after `dns.go` (flags, namespace watch, health probe), registered in `cmd/chaosgw/main.go` and `main_test.go`.
- **Compiler:** `internal/compiler/ntp.go` (`compileNTP` after `compileDNS`: sources grouped by identical candidate lists, ordered by
  `domain.RankOrder`, the redirect port per distinct destination selector, at most 32); `Target.NTPFaults` beside `DNSFaults`;
  `compiler/service.go` (the DNAT of UDP 123 for the gateway address and the ports of the selectors to the peer `169.254.100.2`, the accept
  in `serviceForward` which is hard-coded to the DNS port today, `redirect_hardcoded`, and the redirect of every scope with an active time fault, Q3;
  hostname selectors use the DNS-derived sets); `compiler/dhcp.go` (option 42 defaults to the gateway address for a network whose time service is on and
  where the service namespace exists) and `dhcpaction.go`; `engine/explain.go` (the winner of the family `time`).
- **Verify and teardown:** the service namespace's nft document (`internal/executor/plan.go` `serviceRedirectRuleset`) needs no rule for the
  server's own ports, but `apply/plan.go` `serviceDiffers` and the read-back must stay consistent with what the compiler emits.
- **Engine and API:** lift the family: empty `domain.unavailableFamilies` (`profile.go`) and replace the tests that pin the refusals of stage 1
  (`TestATimeFaultAndAProfileWithATimePartAreRefusedUntilTheServerExists`, `TestATimeOverlayIsRefusedUntilTheServerExists`,
  `TestAProfileWithATimePartIsListedAsUnavailableAndItsActivationIsRefused`, `TestATimeStepAndAProfileWithATimePartAreRefusedWhenTheRunIsCreated` and
  `TestTheNtpWaitAndTheNtpCheckAreRefusedUntilTheRunnerHasThem` (stage 3 for the last one; done, it is replaced by `TestTheNtpWaitAndTheNtpCheckAreAcceptedWhenTheRunIsCreated`), the stub tests in `internal/api/ntpservice_test.go`); once an
  activated profile can have a time part, `partsOf` in `internal/api/profiles.go` (its `FamilyTime` case, used by the effect of a profile) needs a test, since the
  activation is refused in stage 1; `Snapshot.NTPFaults` (`engine/engine.go`, `owner.go`), the `Supported*` lists, `engine/events.go` (three events),
  `internal/api/ntpservice.go` after `dns.go` and `tlsservice.go` (config long poll with `lastPollAt`, the query post that adds the device by
  `Identity.OwnerOf`, counters and the 10-second event limit, `countFault`), `overlays.go` (`withNTP`, hits and epoch), `events.go`
  (`knownTypes`, `describe`), `system.go` (health component `ntp`, `Features`), `internal/preflight` (the check `host.clock` from `adjtimex`;
  `docs/troubleshooting.md` must name it and the component `ntp`, the `docscheck` tests demand it).
- **Deployment:** `deploy/compose.ntp.yaml` (after `compose.dns.yaml` and `compose.tls.yaml`: `network_mode: service:svcns`, user 65532, `cap_drop ALL`
  with `NET_BIND_SERVICE`, read-only root, `no-new-privileges`, the service directory read-only, `depends_on` svcns and api, the health check
  `chaosgw ntp --health`); `tools/composegen/main.go` (`files`) and `main_test.go` (the capability, `service:svcns` and `depends_on` loops);
  `go run ./tools/composegen` for `deploy/compose.yaml`; the row `ntp` in the container table of `docs/plan.md` §3.8;
  `internal/executor/hardening_review_test.go` and `hardening_test.go` (an `ntp` profile test after `TestDNSContainersHardeningProfile`);
  `deploy/smoke-compose.sh` (`running="dns tls ntp"`, the capabilities); the level 2 lists in `internal/appliance` (`deployment_test.go`,
  `recovery_test.go`, `install_test.go`). `install.sh` and `update.sh` do not wait for `ntp`, as for `dns` and `tls`.
- **Level 1:** a bed after `newDNSBedOn` (a random service namespace, the service in-process on `ListenPacketIn`, the API client with
  `testbed.DialerIn`), the clients in the device namespace (a Python SNTP client; `chronyd -Q` and `ntpdig` from the image of the
  development container, `.devcontainer/Dockerfile`), the cases of the milestone, the list `M34_LEVEL1` in `.github/level1-tests.env`
  appended to the three `-expect-pass` strings (`ci.yml` twice, `nightly.yml`) and to `docs/development.md` (`tools/ciconfig` checks all
  three), and a row for every test in the acceptance map. The 5,000 queries/s check: a load loop through the redirect under `Accurate()`
  with a measurement record, and a level 0 floor on loopback.
- **Fuzz:** the target goes into `FUZZ_TARGETS` of the `Makefile` before the root target `FuzzCaptureOpenFiles` (the `ciconfig` test pins that
  one last); `nightly.yml` (the cache key `hashFiles`, the crasher artifact paths, the job limit: a seventh target of 45 minutes does not fit
  in 330 minutes); `docs/development.md` counts the targets.

## Stage 3: scenarios, API reads and `chaosctl`

- `internal/run`: `types.go` (`SupportedStepTypes`, `SupportedCheckTypes`, the observer interface gets the NTP queries), `exec.go` (the wait
  `ntp_query_seen` after `dns_query`), `checks.go` (`lookNTP` after `lookDNS`, the positive-check list, the settle times), `zerohit.go`
  (the family `time`), `report.go`; `internal/api/runs.go` (`runObserver`).
- `internal/api`: the per-device reads (a bounded ring of the last queries per device, the counters) and the profile part (`profiles.go`
  `partsOf`).
- `internal/ctl` and `cmd/chaosctl`: `timeCmd` after `dnsCmd` in `ctl/appfamilies.go` (`chaosctl time set offset|absolute|skew|leap|
  unsynchronized|kiss-of-death|silence|invalid`), registered in `ctl/ctl.go`; `explain` and `wait` (a `wait ntp`).

## Stage 4: web app and documents

- `web/src`: `lib/timeForm.ts` (the presets: +1 year, -1 day, 1970, 2038, NTP era 1, leap second, unsynchronized, kiss-of-death) and
  `components/faults/TimeFields.vue`, `lib/faultForm.ts`, `lib/faults.ts` (chips), `views/FaultsView.vue`; scenarios (`lib/scenarios.ts`,
  `stepForm.ts`, `checkForm.ts`, the step and check dialogs); profiles (`lib/profileForm.ts`); the network form (`lib/networkForm.ts`: the time
  service and the redirect); the *Time* panel of `views/DeviceDetailView.vue`; `composables/gateway.ts`; `RecentEventsCard.vue`;
  `web/e2e/mock.ts` and the specs; vitest for every `lib/` file.
- Documents: `docs/user-guide.md`, `docs/api-guide.md` (the examples are validated against the spec: every block needs its label),
  `docs/cookbook.md` (a recipe), `docs/troubleshooting.md`, `docs/development.md` (the section of the milestone, the acceptance map), `docs/plan.md`
  status, the README (the limits no longer say "no NTP server"), the release notes of the next release (see above).
- Then the audit of the milestone (D37), which is not part of the stages.
