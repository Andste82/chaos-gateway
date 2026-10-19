# Example test suites

How a CI job drives the gateway from a test suite: a degraded network around a test of the device, and a scenario run
from a file, with the generated clients and with `chaosctl` ([`docs/install.md`](../../docs/install.md), "Command line
client"). They are small on purpose: copy a fixture or a helper into your own suite.

| Directory | What |
|---|---|
| [`pytest/`](pytest/) | pytest with the generated Python client (`clients/python/chaosgw-client`): a `degraded` fixture that applies faults and profiles with a TTL and removes them in the teardown, a run of a scenario, `chaosctl run` and `chaosctl with` |
| [`jest/`](jest/) | Jest (ts-jest) with the generated TypeScript client (`clients/typescript`): `withFault`, a run of a scenario, `chaosctl run` and `chaosctl with` |
| [`scenarios/link-outage.yaml`](scenarios/link-outage.yaml) | the scenario both suites run |

## Run them

Against your gateway, from a checkout with the clients generated (`make generate-web generate-python`):

```sh
export CHAOSGW_URL=https://gateway.example:8443   # the gateway
export CHAOSGW_TOKEN=...                          # a token with the scope overlays
export CHAOSGW_DEVICE=esp32-42                    # the device under test (it must be online)
export CHAOSCTL=/path/to/chaosctl                 # for the tests of the command line

# a gateway with a self-signed certificate: CHAOSGW_CA_CERT=<its certificate as PEM> (pytest),
# NODE_EXTRA_CA_CERTS=<the same file> (Jest), or CHAOSGW_INSECURE=1 (pytest)

cd clients/examples/pytest && pip install -r requirements.txt ../../python/chaosgw-client && pytest
cd clients/examples/jest && npm ci && npx jest
```

The scenario sets a slow link, cuts MQTT's port for two seconds and restores everything; the device must be online
(a precondition of every run). Replace the placeholder comments in the tests with the check of your device, and add
`checks` to the scenario (the commented example in the file) to let the gateway decide whether the device came back.

`make test-examples` runs both suites against a gateway on the simulated kernel, the way the `level0` job of CI does
(`internal/api/examples_test.go`); it needs no device and no root.

## What the generated clients leave out

- Both read every answer as JSON. The JUnit report (`/runs/{id}/report.xml`), the event lines of a run, capture downloads and
  QR codes are not JSON: fetch them with `fetch` or `httpx` (the tests show the JUnit report).
- The TypeScript client calls `fetch` with a path relative to the API (`/overlays`) and no credentials. `jest/setup.ts` wraps
  `fetch` once to add the gateway's address and the token.
- The Python client returns the problem (`Problem`) as the parsed answer of a failed call instead of raising; the `ok()`
  helper of `pytest/conftest.py` turns it into a failed test with the gateway's explanation.
