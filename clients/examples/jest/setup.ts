// The generated client calls fetch() with paths relative to the API ('/overlays'), without a host and without
// credentials. This file points it at the gateway and adds the token, once, for every test.
//
//   CHAOSGW_URL    address of the gateway, e.g. https://gw.lab:8443
//   CHAOSGW_TOKEN  an API token with the scope `overlays`
//
// A gateway with a self-signed certificate: start Jest with NODE_EXTRA_CA_CERTS=<its certificate as PEM>; the tests that
// run chaosctl hand that file on to it as CHAOSGW_CA_CERT (helpers.ts).
const base = (process.env.CHAOSGW_URL ?? '').replace(/\/$/, '') + '/api/v1';
const token = process.env.CHAOSGW_TOKEN ?? '';

if (!process.env.CHAOSGW_URL || !token) {
  throw new Error('set CHAOSGW_URL and CHAOSGW_TOKEN (clients/examples/README.md)');
}

const realFetch = globalThis.fetch;

globalThis.fetch = ((input: RequestInfo | URL, init?: RequestInit) => {
  const url = typeof input === 'string' && input.startsWith('/') ? base + input : input;
  const headers = new Headers(init?.headers);
  headers.set('Authorization', `Bearer ${token}`);
  return realFetch(url, { ...init, headers });
}) as typeof fetch;
