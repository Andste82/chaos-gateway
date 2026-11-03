#!/usr/bin/env python3
"""Validate the examples in api/examples against the schemas in api/openapi.yaml.

Schema validation only. The semantic rules (exactly one step type, jitter <= latency,
references exist, ...) are checked by internal/domain; its tests decode these same files.

    pip install openapi-spec-validator   # brings openapi-schema-validator
    python3 api/examples/validate.py
"""
import pathlib
import sys

import yaml
from jsonschema import FormatChecker
from openapi_schema_validator import OAS30Validator
from openapi_spec_validator import validate as validate_spec
from referencing import Registry, Resource
from referencing.jsonschema import DRAFT4

HERE = pathlib.Path(__file__).resolve().parent
SPEC = yaml.safe_load((HERE.parent / "openapi.yaml").read_text())
validate_spec(SPEC)
REGISTRY = Registry().with_resource("urn:chaos-gateway:openapi", Resource.from_contents(SPEC, default_specification=DRAFT4))


def validator(schema_name):
    return OAS30Validator(
        {"$ref": f"urn:chaos-gateway:openapi#/components/schemas/{schema_name}"},
        registry=REGISTRY,
        format_checker=FormatChecker(),
    )


def errors(schema_name, doc):
    return sorted(validator(schema_name).iter_errors(doc), key=lambda e: list(e.path))


failed = 0


def expect_valid(label, schema_name, doc):
    global failed
    errs = errors(schema_name, doc)
    if errs:
        failed += 1
        print(f"FAIL {label}: expected valid {schema_name}")
        for e in errs[:10]:
            print(f"     at /{'/'.join(map(str, e.path))}: {e.message[:200]}")
    else:
        print(f"ok   {label}")


def expect_invalid(label, schema_name, doc):
    global failed
    if errors(schema_name, doc):
        print(f"ok   {label} (rejected)")
    else:
        failed += 1
        print(f"FAIL {label}: expected {schema_name} to reject it")


config = yaml.safe_load((HERE / "configuration.yaml").read_text())
expect_valid("configuration.yaml", "Configuration", config)
for i, overlay in enumerate(yaml.safe_load((HERE / "overlays.yaml").read_text())):
    expect_valid(f"overlays.yaml[{i}]", "OverlayRequest", overlay)
expect_valid("run-request.yaml", "RunRequest", yaml.safe_load((HERE / "run-request.yaml").read_text()))
expect_valid("run-request-time.yaml", "RunRequest", yaml.safe_load((HERE / "run-request-time.yaml").read_text()))
for i, capture in enumerate(yaml.safe_load((HERE / "capture-requests.yaml").read_text())):
    expect_valid(f"capture-requests.yaml[{i}]", "CaptureRequest", capture)

for i, req in enumerate(yaml.safe_load((HERE / "diagnostic-requests.yaml").read_text())):
    expect_valid(f"diagnostic-requests.yaml[{i}]", "DiagnosticRequest", req)

for i, req in enumerate(yaml.safe_load((HERE / "calibration-requests.yaml").read_text())):
    expect_valid(f"calibration-requests.yaml[{i}]", "CalibrationRequest", req)

# Things the schema itself must reject.
expect_invalid("loss without %", "NetemParams", {"loss": "10"})
expect_invalid("duration without unit", "NetemParams", {"latency": "200"})
expect_invalid("bit rate in bytes", "NetemParams", {"rate": "2MB"})
expect_invalid("scope with two sources", "Scope", {"device": "a", "group": "b"})
expect_invalid("empty scope", "Scope", {})
expect_invalid("unknown access action", "AccessRuleBody", {"action": "block"})
expect_invalid("port out of range", "TrafficMatch", {"protocol": "tcp", "ports": [70000]})
expect_invalid("network without type", "Network", {"name": "x", "address": "10.0.0.1/24", "interfaces": [{"name": "eth1"}]})
expect_invalid("cidr with host bits as prefix length 33", "Ipv4Cidr", "10.0.0.0/33")
expect_invalid("upper-case MAC", "MacAddress", "AA:BB:CC:DD:EE:FF")
expect_invalid("step without at", "Step", {"id": "x", "restore": True})
expect_invalid("capture scope with two scopes", "CaptureRequest", {"scope": {"uplink": True, "network": "IoT"}})
expect_invalid("capture ring buffer of one file", "CaptureRequest", {"scope": {"uplink": True}, "ring_buffer": {"files": 1, "file_size": "1MiB"}})
expect_invalid("diagnostic of an unknown type", "DiagnosticRequest", {"type": "traceroute", "host": "10.0.0.1"})
expect_invalid("diagnostic from the gateway and a probe", "DiagnosticRequest", {"type": "ping", "host": "10.0.0.1", "from": {"gateway": True, "probe": "p1"}})
expect_invalid("diagnostic without host", "DiagnosticRequest", {"type": "ping"})
expect_invalid("bad step id", "Step", {"id": "Bad Id", "at": "1s", "restore": True})
expect_invalid("schema version 2", "Configuration", {**config, "schema_version": 2})

# Time faults (M34): the syntax of the offset, the enums and the members of the new kinds.
for offset in ["+400d", "-90s", "1y", "-1y2d", "1.5h", "250ms", "+2w"]:
    expect_valid(f"time offset {offset}", "TimeOffset", offset)
for offset in ["400", "+400 d", "d", "-", "90sec", "1month", "--1d"]:
    expect_invalid(f"time offset {offset!r}", "TimeOffset", offset)
expect_valid("time fault with a destination and a port", "TimeFault", {"destination": {"cidr": "203.0.113.0/24"}, "protocol": "udp", "ports": [123], "offset": "-1d", "skew_ppm": -50.5})
expect_invalid("time fault with an unknown answer", "TimeFault", {"answer": "wrong_time"})
expect_invalid("time fault with a lower-case kiss code", "TimeFault", {"answer": "kiss_of_death", "kiss_code": "rate"})
expect_invalid("time fault with an unknown leap", "TimeFault", {"leap": "smear"})
expect_invalid("time fault with a skew beyond a standing clock", "TimeFault", {"skew_ppm": 2000000})
expect_invalid("time fault with an absolute time without zone", "TimeFault", {"absolute": "yesterday"})
expect_invalid("overlay with the kind time and a wrong member", "OverlayRequest", {"target": {"device": "x"}, "time": {"answer": "wrong_time"}})
expect_valid("wait for an NTP query", "WaitCondition", {"for": "ntp_query_seen", "timeout": "5m"})
expect_invalid("wait for an unknown condition", "WaitCondition", {"for": "ntp_answered", "timeout": "5m"})
expect_valid("step with a time fault", "Step", {"id": "ahead", "at": "5s", "time": {"offset": "+400d"}})
expect_valid("check ntp_queried", "Check", {"ntp_queried": {}, "window": {"from": "start", "within": "1m"}})
expect_invalid("check ntp_queried with a member", "Check", {"ntp_queried": {"name": "pool.ntp.org"}, "window": {"from": "start", "within": "1m"}})
expect_valid("profile with a time part", "ProfileParts", {"time": {"answer": "unsynchronized"}})
expect_valid("profile clock-in-1970", "Profile", {"name": "clock-in-1970", "parts": {"time": {"absolute": "1970-01-01T00:00:00Z"}}})
expect_valid("scenario with a time step, an NTP wait and an NTP check", "Scenario", {
    "name": "stale-clock",
    "target": {"device": "esp32-42"},
    "steps": [
        {"id": "ntp-up", "at": "0s", "wait": {"for": "ntp_query_seen", "timeout": "10m"}},
        {"id": "ahead", "at": "5s", "time": {"offset": "+400d"}},
        {"id": "polled", "at": "10s", "wait": {"for": "ntp_query_seen", "timeout": "10m"}},
        {"id": "back", "at": "20s", "remove": "ahead"},
    ],
    "checks": [{"ntp_queried": {}, "window": {"from": "ahead", "until": "back"}}],
})
expect_valid("network with the ntp block", "LanNetwork", {**next(n for n in config["networks"].values() if n["type"] == "lan"), "ntp": {"enabled": False, "redirect_hardcoded": True}})
expect_invalid("ntp block with a string for the redirect", "NetworkNtp", {"redirect_hardcoded": "yes"})
expect_valid("NTP service configuration", "NtpServiceConfig", {"generation": 3, "networks": [{"network": "0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21", "gateway": "10.10.0.1", "clients": "10.10.0.0/24"}], "faults": [{"overlay": "0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21", "since": "2026-11-03T09:55:00Z", "sources": ["10.10.0.100"], "port": 12301, "offset": "+400d"}], "listen": [123, 12301]})
expect_invalid("NTP service configuration without the redirect port of a fault", "NtpServiceConfig", {"generation": 3, "networks": [], "faults": [{"overlay": "0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21", "since": "2026-11-03T09:55:00Z", "sources": ["10.10.0.100"], "offset": "+400d"}], "listen": [123]})
expect_invalid("NTP service configuration without the time a fault took effect", "NtpServiceConfig", {"generation": 3, "networks": [], "faults": [{"overlay": "0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21", "sources": ["10.10.0.100"], "port": 123, "offset": "+400d"}], "listen": [123]})
expect_valid("NTP query log entry", "NtpQueryLogEntry", {"time": "2026-11-03T10:00:00Z", "client": "10.10.0.100", "port": 12301, "version": 4, "answer": "normal", "served_time": "2026-11-03T10:00:00Z", "offset_ms": 0})
expect_invalid("NTP query log entry without the port", "NtpQueryLogEntry", {"time": "2026-11-03T10:00:00Z", "client": "10.10.0.100"})

print("all examples valid" if not failed else f"{failed} problem(s)")
sys.exit(1 if failed else 0)
