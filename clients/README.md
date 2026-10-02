# API clients

Generated from [`api/openapi.yaml`](../api/openapi.yaml); the generated code is not committed.
`make generate` creates it, `make check-clients` generates it and checks that it compiles.

| Directory | Generator | Use |
|---|---|---|
| `typescript/` | Orval (`fetch` client) | Jest and Vitest suites; the web app has its own Vue Query hooks in `web/src/api` |
| `python/` | openapi-python-client | pytest suites |

The Python client leaves out responses that are not JSON (the CA certificate as PEM, the JUnit
report as XML, QR codes, captures); download those with the client's HTTP library.
