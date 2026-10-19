"""Runs of a scenario from a file: with the generated client, and with chaosctl, which is the shorter way in a CI job."""

from __future__ import annotations

import time
import xml.etree.ElementTree as ET

import pytest
import yaml
from chaosgw_client.api.runs import create_run, get_run
from chaosgw_client.models import RunRequest, RunStatus

from conftest import SCENARIOS, ok

ENDED = {RunStatus.PASSED, RunStatus.FAILED, RunStatus.ERROR, RunStatus.ABORTED}


def test_a_scenario_from_a_file_runs_to_the_end_with_the_client(gateway, dut):
    scenario = yaml.safe_load((SCENARIOS / "link-outage.yaml").read_text())
    request = RunRequest.from_dict({
        "inline": scenario,
        "parameters": {"target.device": dut},
        # the gateway aborts the run when the job dies and stops renewing it, and removes its faults
        "lease": "30s",
    })
    run = ok(create_run.sync_detailed(client=gateway, body=request))

    deadline = time.monotonic() + 60
    while run.status not in ENDED:
        assert time.monotonic() < deadline, f"run {run.id} is still {run.status}"
        time.sleep(0.5)
        run = ok(get_run.sync_detailed(run_id=str(run.id), client=gateway))

    assert run.status == RunStatus.PASSED, run.reason
    assert [s.id for s in run.steps] == ["slow", "cut", "back"]
    assert all(s.status == "applied" for s in run.steps)

    # the Python client leaves out answers that are not JSON: the JUnit report comes from its HTTP client
    junit = gateway.get_httpx_client().get(f"/runs/{run.id}/report.xml")
    assert junit.status_code == 200
    suite = ET.fromstring(junit.text)
    assert suite.tag in ("testsuite", "testsuites")


@pytest.mark.cli
def test_chaosctl_runs_a_scenario_and_the_exit_code_is_the_result(chaosctl, dut, tmp_path):
    junit = tmp_path / "junit.xml"
    artifacts = tmp_path / "artifacts"
    result = chaosctl("run", "-f", str(SCENARIOS / "link-outage.yaml"), "--set", f"target.device={dut}",
                      "--wait", "--junit", str(junit), "--artifacts", str(artifacts))

    assert result.returncode == 0, result.stdout + result.stderr  # 0 passed, 1 a check failed, 2 error or aborted
    assert "PASSED" in result.stdout
    assert ET.parse(junit).getroot().tag in ("testsuite", "testsuites")  # for the CI system's test report
    assert {p.name for p in artifacts.iterdir()} >= {"report.json", "report.xml", "events.jsonl"}


@pytest.mark.cli
def test_chaosctl_returns_2_for_a_scenario_the_gateway_refuses(chaosctl, tmp_path):
    result = chaosctl("run", "-f", str(SCENARIOS / "link-outage.yaml"), "--set", "target.device=no-such-device", "--wait")

    assert result.returncode == 2
    assert "422 validation_failed" in result.stderr
    assert 'there is no device "no-such-device"' in result.stderr


@pytest.mark.cli
def test_chaosctl_with_cleans_up_when_the_command_fails(chaosctl, gateway, dut):
    from chaosgw_client.api.overlays import list_overlays

    # the command sees the ids of the overlays and fails; chaosctl returns its exit code and removes the fault
    result = chaosctl("with", "--device", dut, "--latency", "300ms", "--ttl", "5m", "--",
                      "python3", "-c", "import os, sys; print(os.environ['CHAOSCTL_OVERLAYS']); sys.exit(3)")

    assert result.returncode == 3
    overlay_id = result.stdout.strip()
    assert overlay_id
    assert overlay_id not in [str(o.id) for o in ok(list_overlays.sync_detailed(client=gateway)).items]
