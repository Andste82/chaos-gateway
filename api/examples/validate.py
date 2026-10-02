#!/usr/bin/env python3
"""Validate the examples in api/examples against the schemas in api/openapi.yaml.

Schema validation only; semantic rules (exactly one step type, jitter <= latency,
references exist, ...) belong to the domain package (milestone M2).

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
expect_invalid("bad step id", "Step", {"id": "Bad Id", "at": "1s", "restore": True})
expect_invalid("schema version 2", "Configuration", {**config, "schema_version": 2})

print("all examples valid" if not failed else f"{failed} problem(s)")
sys.exit(1 if failed else 0)
