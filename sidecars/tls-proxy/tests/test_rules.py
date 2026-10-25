"""Tests of the rules of an interception case (chaosgw_tlsproxy/rules.py). They import nothing of mitmproxy:

    python3 -m unittest discover -s tests -t .      (from sidecars/tls-proxy)
"""

import unittest

from chaosgw_tlsproxy import rules as R


def rules(*specs):
    return R.parse_rules(list(specs))


class ParseTests(unittest.TestCase):
    def test_durations_of_the_api(self):
        for text, want in {"1s": 1.0, "200ms": 0.2, "1.5s": 1.5, "1m30s": 90.0, "2h": 7200.0, "250us": 0.00025}.items():
            self.assertAlmostEqual(R.parse_duration(text), want, msg=text)
        for bad in ["", "s", "1", "1d", "1s2", "-1s", "1 s", "ms1"]:
            with self.assertRaises(ValueError, msg=bad):
                R.parse_duration(bad)

    def test_bitrates_of_the_api(self):
        for text, want in {"8bit": 8, "2Mbit": 2_000_000, "1.5kbit": 1500, "1Gbit": 1_000_000_000}.items():
            self.assertEqual(R.parse_bitrate(text), want, msg=text)
        for bad in ["", "2", "2Mbps", "kbit", "-1kbit", "2 Mbit"]:
            with self.assertRaises(ValueError, msg=bad):
                R.parse_bitrate(bad)

    def test_a_rule_that_cannot_be_read_is_left_out_not_fatal(self):
        got = R.parse_rules([{"action": "delay", "delay": "nope"}, {"action": "status", "status": 503}, {"nothing": 1}, {"action": "throttle", "rate": "x"}])
        self.assertEqual([r.action for r in got], ["status"])
        self.assertEqual(R.parse_rules(None), [])


class MatchTests(unittest.TestCase):
    def test_host_patterns(self):
        self.assertTrue(R.host_matches("api.example.com", "API.Example.com."))
        self.assertFalse(R.host_matches("api.example.com", "x.api.example.com"))
        # *.suffix matches the subdomains, not the suffix (as the Hostname of the API says)
        self.assertTrue(R.host_matches("*.example.com", "a.example.com"))
        self.assertTrue(R.host_matches("*.example.com", "a.b.example.com"))
        self.assertFalse(R.host_matches("*.example.com", "example.com"))
        self.assertFalse(R.host_matches("*.example.com", "notexample.com"))

    def test_every_given_field_must_match(self):
        r = rules({"action": "block", "match": {"host": "a.example.com", "path_prefix": "/v1/", "method": "post"}})[0]
        self.assertTrue(r.matches("a.example.com", "/v1/x", "POST"))
        self.assertFalse(r.matches("b.example.com", "/v1/x", "POST"))
        self.assertFalse(r.matches("a.example.com", "/v2/x", "POST"))
        self.assertFalse(r.matches("a.example.com", "/v1/x", "GET"))
        self.assertTrue(rules({"action": "block"})[0].matches("anything", "/", "GET"))


class DecideTests(unittest.TestCase):
    def test_every_matching_rule_applies_in_order_until_a_terminal_one(self):
        rs = rules(
            {"action": "delay", "delay": "1s"},
            {"action": "modify", "match": {"path_prefix": "/a"}, "modify": {"response_headers": {"X-A": "1"}}},
            {"action": "delay", "delay": "500ms", "match": {"path_prefix": "/a"}},
            {"action": "status", "status": 503, "match": {"path_prefix": "/a/fail"}},
            {"action": "block"},
            {"action": "delay", "delay": "9s"},
        )
        d = R.decide(rs, "h", "/a/ok", "GET")
        self.assertEqual(d.actions, ["delay", "modify", "delay", "block"])  # block (no match) ends it: it selects every request
        self.assertAlmostEqual(d.delay, 1.5)
        self.assertEqual(d.terminal, ("block",))
        self.assertEqual(d.response_headers, [("X-A", "1")])
        d = R.decide(rs, "h", "/a/fail/x", "GET")
        self.assertEqual(d.terminal, ("status", 503))
        self.assertEqual(d.actions, ["delay", "modify", "delay", "status"])
        # nothing after the terminal action acts: the last delay (9 s) is never added
        self.assertAlmostEqual(d.delay, 1.5)

    def test_a_request_no_rule_selects_is_untouched(self):
        d = R.decide(rules({"action": "status", "status": 500, "match": {"path_prefix": "/x"}}), "h", "/y", "GET")
        self.assertTrue(d.empty())
        self.assertIsNone(d.terminal)
        self.assertEqual(d.delay, 0.0)

    def test_the_query_is_not_part_of_the_path(self):
        rs = rules({"action": "block", "match": {"path_prefix": "/x"}})
        self.assertEqual(R.decide(rs, "h", "/x?y=1", "GET").terminal, ("block",))
        self.assertIsNone(R.decide(rs, "h", "/y?x=1", "GET").terminal)

    def test_the_last_throttle_wins_and_modifications_add_up(self):
        rs = rules(
            {"action": "throttle", "rate": "1Mbit"},
            {"action": "throttle", "rate": "64kbit"},
            {"action": "modify", "modify": {"request_headers": {"A": "1"}, "response_body_replace": [{"search": "a", "replace": "b"}]}},
            {"action": "modify", "modify": {"request_headers": {"B": ""}, "response_body_replace": [{"search": "b", "replace": "c"}]}},
        )
        d = R.decide(rs, "h", "/", "GET")
        self.assertEqual(d.rate_bps, 64000)
        self.assertEqual(d.request_headers, [("A", "1"), ("B", "")])
        self.assertEqual(R.replace_body(b"aab", d.body_replace), b"ccc")

    def test_a_connection_that_is_no_http_is_decided_by_the_rules_that_name_no_path_and_no_method(self):
        rs = rules(
            {"action": "status", "status": 503},  # http only
            {"action": "modify", "modify": {"response_headers": {"X": "1"}}},  # http only
            {"action": "delay", "delay": "1s", "match": {"path_prefix": "/x"}},  # needs a path: not about a stream
            {"action": "delay", "delay": "2s", "match": {"host": "broker.example.com"}},
            {"action": "throttle", "rate": "8kbit"},
            {"action": "abort", "match": {"host": "broker.example.com"}},
        )
        d = R.decide_connection(rs, "broker.example.com")
        self.assertEqual(d.terminal, ("abort",))
        self.assertEqual(d.actions, ["delay", "throttle", "abort"])
        self.assertAlmostEqual(d.delay, 2.0)
        self.assertEqual(d.rate_bps, 8000)
        other = R.decide_connection(rs, "other.example.com")
        self.assertEqual(other.actions, ["throttle"])
        self.assertIsNone(other.terminal)
        # block closes a stream, too
        self.assertEqual(R.decide_connection(rules({"action": "block"}), "h").terminal, ("abort",))


class ModifyTests(unittest.TestCase):
    def test_headers_are_set_replaced_and_removed(self):
        h = {"A": "old", "B": "x"}
        R.apply_headers(h, [("A", "new"), ("B", ""), ("C", "1"), ("Missing", "")])
        self.assertEqual(h, {"A": "new", "C": "1"})

    def test_the_body_is_replaced_as_text_in_order(self):
        body = "temperature=21".encode()
        got = R.replace_body(body, [R.BodyReplace("21", "99"), R.BodyReplace("", "ignored"), R.BodyReplace("temperature", "t")])
        self.assertEqual(got, b"t=99")
        self.assertEqual(R.replace_body("ä".encode(), [R.BodyReplace("ä", "ö")]), "ö".encode())

    def test_h3_advertisements_are_taken_out_of_alt_svc(self):
        self.assertEqual(R.strip_h3_from_alt_svc('h3=":443"; ma=86400, h3-29=":443"; ma=86400'), "")
        self.assertEqual(R.strip_h3_from_alt_svc('h3=":443"; ma=86400, h2=":8443"'), 'h2=":8443"')
        self.assertEqual(R.strip_h3_from_alt_svc('h2="a,b:443"'), 'h2="a,b:443"')
        self.assertEqual(R.strip_h3_from_alt_svc("clear"), "clear")


class UpstreamPolicyTests(unittest.TestCase):
    def test_only_the_three_policies_of_the_api_are_read(self):
        for p in ("verify", "own_ca", "insecure"):
            self.assertEqual(R.parse_upstream(p), p)
        for bad in (None, "", "default", "VERIFY", "trust", True, 1, ["verify"]):
            self.assertIsNone(R.parse_upstream(bad), msg=repr(bad))

    def test_the_policy_of_the_installation_is_what_mitmproxy_does_with_its_options(self):
        self.assertEqual(R.installation_policy(False, None), "verify")
        self.assertEqual(R.installation_policy(False, "/ca.pem"), "own_ca")
        self.assertEqual(R.installation_policy(True, None), "insecure")
        self.assertEqual(R.installation_policy(True, "/ca.pem"), "insecure")  # no verification beats a CA file

    def test_a_case_without_a_policy_or_with_the_installations_changes_nothing(self):
        for insecure, ca in ((False, None), (False, "/ca.pem"), (True, None), (True, "/ca.pem")):
            self.assertIsNone(R.upstream_plan(None, insecure, ca))
            self.assertIsNone(R.upstream_plan(R.installation_policy(insecure, ca), insecure, ca))

    def test_a_case_that_asks_for_another_policy_gets_it(self):
        self.assertEqual(R.upstream_plan("insecure", False, None), R.UpstreamPlan(verify=False))
        self.assertEqual(R.upstream_plan("insecure", False, "/ca.pem"), R.UpstreamPlan(verify=False))
        # the bundle, where the installation names a CA file or verifies nothing
        self.assertEqual(R.upstream_plan("verify", False, "/ca.pem"), R.UpstreamPlan(verify=True))
        self.assertEqual(R.upstream_plan("verify", True, None), R.UpstreamPlan(verify=True))
        # the CA file of the installation, where it verifies nothing
        self.assertEqual(R.upstream_plan("own_ca", True, "/ca.pem"), R.UpstreamPlan(verify=True, ca_file="/ca.pem"))

    def test_own_ca_without_a_ca_file_refuses_every_certificate_and_says_why(self):
        for insecure in (False, True):
            plan = R.upstream_plan("own_ca", insecure, None)
            self.assertTrue(plan.refuse and plan.verify)
            self.assertIn("CHAOSGW_UPSTREAM_CA_FILE", plan.note)


if __name__ == "__main__":
    unittest.main()
