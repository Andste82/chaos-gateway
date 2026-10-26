"""Tests of the addon's hooks with mitmproxy's test flows (no proxy runs):

    python3 -m unittest discover -s tests -t .      (from sidecars/tls-proxy; they need mitmproxy importable)
"""

import asyncio
import logging
import unittest

try:
    from mitmproxy import connection, flow, http, tls
    from mitmproxy.addons import tlsconfig
    from mitmproxy.proxy.context import Context
    from mitmproxy.test import taddons, tflow
    from OpenSSL import SSL

    from chaosgw_tlsproxy import addon
    from chaosgw_tlsproxy import rules as R
except ImportError:  # the addon imports mitmproxy; the rules tests do not need it
    addon = None

KNOWN = "6f1c2a4e-0b7d-4c55-9a51-3d2e8f00aa01"
UNKNOWN = "0a9b8c7d-6e5f-4a3b-8c2d-1e0f9a8b7c6d"


def intercept_case(overlay, **settings):
    return {"case": "intercept", "overlay": overlay, "intercept": settings}


@unittest.skipIf(addon is None, "mitmproxy is not installed")
class UnknownCaseTests(unittest.TestCase):
    """A connection whose overlay the proxy does not know (a case the long poll has not brought yet, a case removed while the connection
    lasts, or nothing loaded at all) is refused at every hook instead of passing without rules (P5-AUDIT-04)."""

    def make(self, cases=(), loaded=True, wait=0.2):
        a = addon.Addon(None, None, case_wait=wait)
        if loaded:
            a.on_config({"generation": 1, "cases": list(cases)})
        return a

    def origin(self, a, flow, overlay):
        o = addon.Origin(connection="c1", client="10.10.0.5", overlay=overlay, original_dst="203.0.113.7:443")
        a.origins[flow.client_conn.id] = o
        return o

    def test_a_connect_for_an_unknown_overlay_is_answered_with_a_refusal_and_reported(self):
        for loaded in (True, False):
            a = self.make([intercept_case(KNOWN)], loaded=loaded)
            f = tflow.tflow()
            f.request = http.Request.make("CONNECT", "http://203.0.113.7:443/", headers={
                addon.HEADER_CONNECTION: "c1", addon.HEADER_CLIENT: "10.10.0.5", addon.HEADER_OVERLAY: UNKNOWN})
            f.request.host, f.request.port = "203.0.113.7", 443
            asyncio.run(a.http_connect(f))
            self.assertIsNotNone(f.response, loaded)
            self.assertEqual(f.response.status_code, 503)
            self.assertEqual([r.get("error") for r in a.reported_flows], [addon.UNKNOWN_CASE])
            self.assertEqual(a.reported_flows[0]["overlay"], UNKNOWN)

    def test_a_connect_for_a_known_overlay_goes_on(self):
        a = self.make([intercept_case(KNOWN)])
        f = tflow.tflow()
        f.request = http.Request.make("CONNECT", "http://203.0.113.7:443/", headers={addon.HEADER_CLIENT: "10.10.0.5", addon.HEADER_OVERLAY: KNOWN})
        asyncio.run(a.http_connect(f))
        self.assertIsNone(f.response)
        self.assertEqual(a.reported_flows, [])

    def test_a_case_that_arrives_while_the_connect_waits_decides_it(self):
        a = self.make([], wait=5.0)

        async def run():
            f = tflow.tflow()
            f.request = http.Request.make("CONNECT", "http://203.0.113.7:443/", headers={addon.HEADER_CLIENT: "10.10.0.5", addon.HEADER_OVERLAY: KNOWN})
            pending = asyncio.ensure_future(a.http_connect(f))
            await asyncio.sleep(0.2)
            a.on_config({"generation": 2, "cases": [intercept_case(KNOWN)]})
            await asyncio.wait_for(pending, 2)
            return f

        f = asyncio.run(run())
        self.assertIsNone(f.response)

    def test_a_request_without_a_known_case_is_killed_and_reported(self):
        a = self.make([intercept_case(KNOWN, http_rules=[{"action": "status", "status": 500}])])
        f = tflow.tflow()
        self.origin(a, f, UNKNOWN)
        asyncio.run(a.request(f))
        self.assertIsNone(f.response)
        self.assertIsNotNone(f.error)
        self.assertEqual(f.error.msg, flow.Error.KILLED_MESSAGE)
        self.assertEqual([r.get("error") for r in a.reported_flows], [addon.UNKNOWN_CASE])

    def test_a_stream_without_a_known_case_is_killed_and_reported(self):
        a = self.make([intercept_case(KNOWN)])
        f = tflow.ttcpflow()
        self.origin(a, f, UNKNOWN)
        asyncio.run(a.tcp_start(f))
        self.assertIsNotNone(f.error)
        self.assertEqual(f.error.msg, flow.Error.KILLED_MESSAGE)
        self.assertEqual([r.get("error") for r in a.reported_flows], [addon.UNKNOWN_CASE])

    def test_key_lines_of_an_unknown_case_or_one_without_key_log_are_dropped(self):
        a = self.make([intercept_case(KNOWN), intercept_case("ab" + KNOWN[2:], keylog=False)])
        for overlay, kept in ((KNOWN, 1), (UNKNOWN, 0), ("ab" + KNOWN[2:], 0), ("", 0)):
            f = tflow.tflow()
            o = self.origin(a, f, overlay)
            a.keyline(f.client_conn.id, "CLIENT_RANDOM aa bb")
            self.assertEqual(len(o.keylines), kept, overlay)

    def test_the_servers_certificate_is_refused_for_an_unknown_case_whatever_the_installation_says(self):
        root = logging.getLogger()
        before = list(root.handlers)
        # mitmproxy's test master leaves its log handler on the root logger, which then logs into an event loop that is closed
        self.addCleanup(lambda: [root.removeHandler(h) for h in list(root.handlers) if h not in before])
        tc = tlsconfig.TlsConfig()
        with taddons.context(tc) as tctx:
            tctx.configure(tc, ssl_insecure=True)
            for loaded in (True, False):
                a = self.make([intercept_case(KNOWN)], loaded=loaded)
                f = tflow.tflow()
                o = self.origin(a, f, UNKNOWN)
                server = connection.Server(address=("203.0.113.7", 443), sni="example.com")
                ctx = Context(f.client_conn, tctx.options)
                ctx.server = server
                data = tls.TlsData(server, ctx)
                asyncio.run(a.tls_start_server(data))
                self.assertIsNotNone(data.ssl_conn, loaded)
                self.assertEqual(SSL._lib.SSL_get_verify_mode(data.ssl_conn._ssl), SSL.VERIFY_PEER)
                self.assertIs(data.ssl_conn.get_context(), addon._upstream_context(R.UpstreamPlan(verify=True, refuse=True)))
                self.assertEqual(o.upstream_note, addon.UNKNOWN_CASE)


@unittest.skipIf(addon is None, "mitmproxy is not installed")
class ResponseHeaderTests(unittest.TestCase):
    """The response headers of a `modify` rule apply to the answer a terminal rule makes, `status` as `block` (P5-AUDIT-05)."""

    def answer(self, terminal):
        a = addon.Addon(None, None, case_wait=0.2)
        a.on_config({"generation": 1, "cases": [intercept_case(KNOWN, http_rules=[
            {"action": "modify", "modify": {"response_headers": {"Retry-After": "30", "Content-Length": "999"}}},
            terminal,
        ])]})
        f = tflow.tflow()
        a.origins[f.client_conn.id] = addon.Origin(client="10.10.0.5", overlay=KNOWN)
        asyncio.run(a.request(f))
        a.response(f)
        return f, a.reported_flows[-1]

    def test_a_status_answer_carries_the_headers_of_a_modify_rule(self):
        f, rep = self.answer({"action": "status", "status": 503})
        self.assertEqual(f.response.status_code, 503)
        self.assertEqual(f.response.headers.get("Retry-After"), "30")
        self.assertEqual(f.response.headers.get("Content-Length"), "0")
        self.assertEqual(rep["actions"], ["modify", "status"])

    def test_a_block_answer_carries_them_too_with_its_own_length(self):
        f, _ = self.answer({"action": "block"})
        self.assertEqual(f.response.status_code, 403)
        self.assertEqual(f.response.headers.get("Retry-After"), "30")
        self.assertEqual(f.response.headers.get("Content-Length"), str(len(f.response.raw_content)))


if __name__ == "__main__":
    unittest.main()
