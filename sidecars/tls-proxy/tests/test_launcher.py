"""Tests of the launcher of the container (no mitmproxy needed):

    python3 -m unittest discover -s tests -t .      (from sidecars/tls-proxy)
"""

import os
import signal
import tempfile
import unittest

from chaosgw_tlsproxy import launcher

FIB_TRIE = """Main:
  +-- 0.0.0.0/0 3 0 5
     +-- 127.0.0.0/8 2 0 2
        +-- 127.0.0.0/31 1 0 0
           |-- 127.0.0.0
              /8 host LOCAL
           |-- 127.0.0.1
              /32 host LOCAL
        |-- 127.255.255.255
           /32 link BROADCAST
     +-- 169.254.100.0/30 2 0 2
        |-- 169.254.100.0
           /30 link UNICAST
        |-- 169.254.100.2
           /32 host LOCAL
        |-- 169.254.100.3
           /32 link BROADCAST
Local:
  +-- 0.0.0.0/0 3 0 5
     |-- 169.254.100.2
        /32 host LOCAL
"""


class Clock:
    def __init__(self):
        self.now = 0.0

    def __call__(self):
        return self.now


class NamespaceWatchTests(unittest.TestCase):
    """The watch of the service namespace (P5-AUDIT-06): the container leaves a namespace that lost the service address, so the restart
    policy brings it into the namespace of the holder that replaced it."""

    def run_watch(self, answers, grace=60.0):
        """Runs the watch over the answers of the interface reader, one per check two seconds apart; the check that said to exit
        (its index) and why, or (None, None)."""
        clock = Clock()
        it = iter(answers)

        def addresses():
            a = next(it)
            if isinstance(a, Exception):
                raise a
            return a

        w = launcher.NamespaceWatch("169.254.100.2", addresses=addresses, interval=2.0, grace=grace, clock=clock)
        for i in range(len(answers)):
            clock.now += 1.0
            self.assertIsNone(w.due(), "a check before its interval")
            clock.now += 1.0
            reason = w.due()
            if reason is not None:
                return i, reason
        return None, None

    def test_an_address_that_goes_away_three_times_in_a_row_ends_the_launcher(self):
        here, gone = {"127.0.0.1", "169.254.100.2"}, {"127.0.0.1"}
        i, reason = self.run_watch([here, here, gone, gone, gone, here])
        self.assertEqual(i, 4)
        self.assertIn("no longer on this namespace's interfaces", reason)

    def test_a_short_absence_or_a_failed_read_is_forgiven(self):
        here, gone = {"169.254.100.2"}, set()
        self.assertEqual(self.run_watch([here, gone, OSError("busy"), here, gone, gone, here, here]), (None, None))

    def test_an_address_that_never_appears_ends_the_launcher_after_the_grace(self):
        i, reason = self.run_watch([set()] * 40, grace=60.0)
        self.assertEqual(i, 29)  # the check at 60 s
        self.assertIn("never appeared", reason)

    def test_the_addresses_are_the_local_host_routes_of_the_namespace(self):
        with tempfile.NamedTemporaryFile("w", delete=False) as f:
            f.write(FIB_TRIE)
        self.addCleanup(os.remove, f.name)
        self.assertEqual(launcher.local_addresses(f.name), {"127.0.0.1", "169.254.100.2"})

    def test_the_launcher_exits_with_code_3_when_the_watch_says_so(self):
        class Orphaned:
            def __init__(self, want):
                self.want = want

            def due(self):
                return f"the service address {self.want} is no longer on this namespace's interfaces"

        real = launcher.NamespaceWatch
        launcher.NamespaceWatch = Orphaned
        self.addCleanup(setattr, launcher, "NamespaceWatch", real)
        for sig in (signal.SIGTERM, signal.SIGINT):  # main sets its own handlers
            self.addCleanup(signal.signal, sig, signal.getsignal(sig))
        with tempfile.TemporaryDirectory() as d, self.assertLogs("chaosgw.launcher", "ERROR") as logs:
            # no test CA yet: the launcher waits for it, and leaves the namespace all the same
            self.assertEqual(launcher.main(["--ca-dir", d, "--confdir", os.path.join(d, "conf"), "--service-addr", "192.0.2.1"]), 3)
        self.assertIn("192.0.2.1 is no longer", "\n".join(logs.output))

    def test_the_check_is_on_by_default_and_can_be_turned_off(self):
        old = os.environ.pop("CHAOSGW_NAMESPACE_CHECK", None)
        if old is not None:
            self.addCleanup(os.environ.__setitem__, "CHAOSGW_NAMESPACE_CHECK", old)
        self.assertEqual(launcher.EXIT_NAMESPACE, 3)
        p = launcher.parser()
        self.assertTrue(p.parse_args([]).namespace_check)
        self.assertEqual(p.parse_args([]).service_addr, "169.254.100.2")
        self.assertFalse(p.parse_args(["--no-namespace-check"]).namespace_check)
        os.environ["CHAOSGW_NAMESPACE_CHECK"] = "0"
        try:
            self.assertFalse(launcher.parser().parse_args([]).namespace_check)
        finally:
            del os.environ["CHAOSGW_NAMESPACE_CHECK"]


class UpstreamCAFileTests(unittest.TestCase):
    """A CA file for the servers that the container cannot read is said in the log at the start (P5-AUDIT-26): mitmproxy reads it only for
    a connection, so the proxy would look healthy while every verified connection fails."""

    def test_a_file_that_is_not_there_or_holds_no_certificate_is_warned_of(self):
        t = tempfile.TemporaryDirectory()
        self.addCleanup(t.cleanup)
        d = t.name
        junk = os.path.join(d, "junk.pem")
        with open(junk, "w") as f:
            f.write("not a certificate\n")
        for path, words in ((os.path.join(d, "missing.pem"), "cannot be read"), (junk, "holds no PEM certificate")):
            with self.assertLogs("chaosgw.launcher", "WARNING") as logs:
                self.assertFalse(launcher.check_upstream_ca_file(path))
            self.assertIn(words, "\n".join(logs.output))

    def test_a_readable_file_and_no_file_say_nothing(self):
        t = tempfile.TemporaryDirectory()
        self.addCleanup(t.cleanup)
        good = os.path.join(t.name, "ca.pem")
        with open(good, "w") as f:
            f.write("-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n")
        with self.assertNoLogs("chaosgw.launcher", "WARNING"):
            self.assertTrue(launcher.check_upstream_ca_file(good))
            self.assertTrue(launcher.check_upstream_ca_file(""))


if __name__ == "__main__":
    unittest.main()
