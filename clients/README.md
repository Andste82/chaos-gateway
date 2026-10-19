# API clients

Generated from [`api/openapi.yaml`](../api/openapi.yaml); the generated code is not committed.
`make generate` creates it, `make check-clients` generates it and checks that it compiles.

| Directory | Generator | Use |
|---|---|---|
| `typescript/` | Orval (`fetch` client) | Jest and Vitest suites; the web app has its own Vue Query hooks in `web/src/api` |
| `python/` | openapi-python-client | pytest suites |
| `examples/` | – | example suites for pytest and Jest that use the two clients and `chaosctl` ([README](examples/README.md)); `make test-examples` runs them |

Both clients leave out what is not JSON: the Python client omits those responses (the CA certificate as PEM, the JUnit
report as XML, QR codes, captures, the event lines of a run), the TypeScript client reads every answer as JSON and so cannot
return them; download those with `httpx` or `fetch`. The TypeScript client calls `fetch` with paths relative to the API and
without credentials; the Jest example shows the few lines that give it the gateway's address and the token.

The command line client is `chaosctl` (`cmd/chaosctl`, `internal/ctl`), not generated.
