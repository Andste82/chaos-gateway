"""Faults and profiles around a test of the device, with the generated Python client.

A real suite would run its own check of the device where the comments say so (publish a message, ask for a firmware
update, ...); the gateway side is what these tests show.
"""

from __future__ import annotations

import time

from chaosgw_client.api.devices import get_device
from chaosgw_client.api.overlays import get_overlay, list_overlays

from conftest import ok


def test_the_device_is_online_before_the_test_degrades_its_network(gateway, dut):
    device = ok(get_device.sync_detailed(device_id=dut, client=gateway))
    assert device.observed.online, f"{dut} is not online: start the device before the test"


def test_a_fault_is_on_inside_the_block_and_gone_after_it(gateway, degraded):
    with degraded.fault(latency="200ms", jitter="50ms", loss="2%") as overlay:
        # the call returned after the gateway verified the change: the fault is in the kernel now
        current = ok(get_overlay.sync_detailed(overlay_id=str(overlay.id), client=gateway))
        assert current.fault.latency == "200ms"
        assert current.ttl == "5m" and current.expires_at is not None

        # ... run the test of the device under a slow, lossy link here ...

    still_there = [o.id for o in ok(list_overlays.sync_detailed(client=gateway)).items]
    assert overlay.id not in still_there


def test_a_profile_with_a_short_ttl_removes_itself(gateway, degraded):
    overlay = degraded.overlay(profile="bad-lte", ttl="1s")

    deadline = time.monotonic() + 10
    while time.monotonic() < deadline:
        if overlay.id not in [o.id for o in ok(list_overlays.sync_detailed(client=gateway)).items]:
            return
        time.sleep(0.2)
    raise AssertionError("the profile is still active after its TTL")


def test_the_same_fault_set_again_replaces_the_first(gateway, degraded):
    first = degraded.overlay(fault={"latency": "50ms"})
    second = degraded.overlay(fault={"latency": "400ms", "jitter": "20ms"})

    # the overlay key is (owner, kind, target, selector): the second write replaced the first and kept its id
    assert second.id == first.id
    current = ok(get_overlay.sync_detailed(overlay_id=str(first.id), client=gateway))
    assert current.fault.latency == "400ms" and current.fault.jitter == "20ms"
