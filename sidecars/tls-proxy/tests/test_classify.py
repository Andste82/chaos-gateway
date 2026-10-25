"""Tests of what the addon makes of the failure text of mitmproxy, without mitmproxy installed to run them:

    python3 -m unittest discover -s tests -t .      (from sidecars/tls-proxy; they need mitmproxy importable)
"""

import unittest

try:
    from chaosgw_tlsproxy import addon
except ImportError:  # the addon imports mitmproxy; the rules tests do not need it
    addon = None


@unittest.skipIf(addon is None, "mitmproxy is not installed")
class ClassifyTests(unittest.TestCase):
    def test_an_alert_of_the_device_is_a_rejection_with_its_name(self):
        for text, want in {
            "The client does not trust the proxy's certificate for example.com (tlsv1 alert unknown ca)": "unknown_ca",
            "The client does not trust the proxy's certificate for example.com (OpenSSL Error([('SSL routines', '', 'tls alert bad certificate')]))": "bad_certificate",
            "Cannot establish TLS with client (sslv3 alert handshake failure)": "handshake_failure",
            "tlsv1 alert certificate expired": "certificate_expired",
            "tlsv1 alert internal error": "internal_error",
        }.items():
            self.assertEqual(addon.classify_client_failure(text), ("rejected", want), text)

    def test_a_device_that_just_goes_away_rejected_too(self):
        for text in ["The client disconnected during the handshake. If this happens consistently for example.com, this may indicate that the client does not trust the proxy's certificate.",
                     "Connection reset by peer", "EOF occurred in violation of protocol", "connection closed"]:
            self.assertEqual(addon.classify_client_failure(text), ("rejected", None), text)

    def test_other_failures_say_nothing_of_the_certificate(self):
        self.assertEqual(addon.classify_client_failure("timed out"), ("timeout", None))
        self.assertEqual(addon.classify_client_failure("no shared cipher"), ("client_closed", "handshake_error"))
        self.assertEqual(addon.classify_client_failure(None), ("client_closed", "handshake_error"))

    def test_tls_versions_are_named_as_the_reports_name_them(self):
        self.assertEqual(addon.tls_version_name("TLSv1.3"), "TLS 1.3")
        self.assertEqual(addon.tls_version_name("TLSv1.2"), "TLS 1.2")
        self.assertIsNone(addon.tls_version_name("TLSv1.0"))
        self.assertIsNone(addon.tls_version_name(None))


if __name__ == "__main__":
    unittest.main()
