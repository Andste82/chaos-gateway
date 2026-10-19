"""Fixtures of the example suite: a client of the gateway, the device under test, and a way to degrade its network
that always cleans up.

The suite needs three environment variables:

    CHAOSGW_URL     address of the gateway, e.g. https://gw.lab:8443
    CHAOSGW_TOKEN   an API token with the scope `overlays`
    CHAOSGW_DEVICE  the device under test (default esp32-42)

and, for a gateway with a self-signed certificate, CHAOSGW_CA_CERT (a PEM file) or CHAOSGW_INSECURE=1.
`chaosctl` (CHAOSCTL, default: the one on the PATH) is used by the tests that show the command line.
"""

from __future__ import annotations

import contextlib
import os
import shutil
import subprocess
from pathlib import Path

import pytest
from chaosgw_client import AuthenticatedClient
from chaosgw_client.api.overlays import create_overlay, delete_overlay
from chaosgw_client.models import Overlay, OverlayRequest, Problem

SCENARIOS = Path(__file__).resolve().parent.parent / "scenarios"


def pytest_configure(config):
    config.addinivalue_line("markers", "cli: needs the chaosctl binary")


def ok(response):
    """The parsed body of a response, or a failed test with the gateway's own explanation."""
    parsed = response.parsed
    if isinstance(parsed, Problem):
        detail = [f"{e.path}: {e.message}" for e in (parsed.errors or [])]
        pytest.fail(f"{response.status_code} {parsed.code}: {parsed.detail or parsed.title} {detail}")
    return parsed


@pytest.fixture(scope="session")
def gateway():
    """A client of the gateway's API."""
    url = os.environ.get("CHAOSGW_URL")
    token = os.environ.get("CHAOSGW_TOKEN")
    if not url or not token:
        pytest.fail("set CHAOSGW_URL and CHAOSGW_TOKEN (clients/examples/README.md)")
    verify = os.environ.get("CHAOSGW_CA_CERT") or os.environ.get("CHAOSGW_INSECURE", "").lower() not in ("1", "true", "yes")
    with AuthenticatedClient(base_url=url.rstrip("/") + "/api/v1", token=token, verify_ssl=verify) as client:
        yield client


@pytest.fixture(scope="session")
def dut():
    """The name of the device under test."""
    return os.environ.get("CHAOSGW_DEVICE", "esp32-42")


class Degraded:
    """Creates faults and profile activations for a test and removes them when the test is over.

    Every overlay gets a TTL, so a test run that dies without its teardown leaves nothing behind: the gateway
    removes the overlay when the TTL runs out. The teardown removes them at once.
    """

    def __init__(self, client: AuthenticatedClient, device: str):
        self.client, self.device, self.created = client, device, []

    def overlay(self, **body) -> Overlay:
        body.setdefault("target", {"device": self.device})
        body.setdefault("ttl", "5m")
        created = ok(create_overlay.sync_detailed(client=self.client, body=OverlayRequest.from_dict(body)))
        self.created.append(created.id)
        return created

    @contextlib.contextmanager
    def fault(self, **fault):
        """`with degraded.fault(latency="200ms", loss="5%") as overlay:` - the fault is on inside the block."""
        overlay = self.overlay(fault=fault)
        try:
            yield overlay
        finally:
            self.remove(overlay.id)

    @contextlib.contextmanager
    def profile(self, name: str):
        overlay = self.overlay(profile=name)
        try:
            yield overlay
        finally:
            self.remove(overlay.id)

    def remove(self, overlay_id) -> None:
        response = delete_overlay.sync_detailed(overlay_id=str(overlay_id), client=self.client)
        if response.status_code not in (204, 404):  # 404: the TTL ran out first
            ok(response)
        if overlay_id in self.created:
            self.created.remove(overlay_id)

    def clean_up(self) -> None:
        for overlay_id in list(self.created):
            self.remove(overlay_id)


@pytest.fixture
def degraded(gateway, dut):
    """Degrades the network of the device under test; whatever a test leaves is removed afterwards."""
    d = Degraded(gateway, dut)
    yield d
    d.clean_up()


@pytest.fixture(scope="session")
def chaosctl():
    """A function that runs the chaosctl binary with the environment of the suite and returns the finished process."""
    binary = os.environ.get("CHAOSCTL") or shutil.which("chaosctl")
    if not binary:
        pytest.skip("chaosctl is not installed (CHAOSCTL names the binary)")

    def run(*args: str, **kwargs) -> subprocess.CompletedProcess:
        return subprocess.run([binary, *args], capture_output=True, text=True, timeout=120, **kwargs)

    return run
