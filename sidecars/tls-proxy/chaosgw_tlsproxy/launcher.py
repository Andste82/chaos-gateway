"""Starts mitmproxy for the gateway (the entry point of the container `tls-proxy`, plan §3.8).

It waits for the test CA the API makes, gives mitmproxy that CA (mitmproxy wants the key and the certificate in one file in
its configuration directory), starts `mitmdump` in `regular` mode with the addon, and starts it again when the CA files
change (`POST /tls/ca/regenerate`): a CA is read when mitmproxy starts. A restart costs the connections that are open, as a
regeneration of the CA does for the devices anyway.

    python -m chaosgw_tlsproxy.launcher --api https://169.254.100.1:8443 --service-token-file ... --ca-dir /var/lib/chaosgw/tlsca

The listening address is on the loopback of the service namespace: only the responder, which shares it, connects.

It also watches the namespace it was started in, as the DNS proxy and the TLS responder do (`--namespace-check`, plan §3.8): when
the holder of the service namespace is restarted, the container stays in the old namespace, which has lost the service address, and
Compose's `depends_on ... restart: true` does not act on a restart by the Docker daemon. The launcher then exits with code 3 and the
restart policy brings the container back, into the current namespace.
"""

from __future__ import annotations

import argparse
import logging
import os
import signal
import subprocess
import sys
import time

log = logging.getLogger("chaosgw.launcher")

CA_CERT = "test-ca.pem"
CA_KEY = "test-ca.key"

# the exit code of a launcher whose namespace lost the service address
EXIT_NAMESPACE = 3


def local_addresses(path: str = "/proc/net/fib_trie") -> set[str]:
    """The IPv4 addresses of this network namespace's interfaces: the host routes of the kernel's local table (`/32 host LOCAL`),
    which every process can read without a capability or a tool of the image."""
    out: set[str] = set()
    last = ""
    with open(path, encoding="ascii", errors="replace") as f:
        for line in f:
            t = line.strip()
            if t.startswith(("|--", "+--")):
                last = t[3:].strip().split("/")[0]
            elif t.startswith("/32 host LOCAL") and last:
                out.add(last)
    return out


class NamespaceWatch:
    """The check of `dnsproxy.WatchNamespace`: the service address has to be on an interface of this namespace. `check` is called
    every `interval` seconds and returns why the launcher has to exit: the address, seen at least once, was missing on three
    checks in a row, or it never appeared within `grace` seconds. A read that fails counts as missing (the namespace may be in
    the middle of being replaced)."""

    def __init__(self, want: str, addresses=local_addresses, interval: float = 2.0, grace: float = 60.0, clock=time.monotonic):
        self.want = want
        self.addresses = addresses
        self.interval = interval
        self.grace = grace
        self.clock = clock
        self.start = clock()
        self.next = self.start + interval
        self.seen = False
        self.misses = 0

    def due(self) -> str | None:
        """Checks when a check is due; the reason to exit, or None."""
        now = self.clock()
        if now < self.next:
            return None
        self.next = now + self.interval
        return self.check()

    def check(self) -> str | None:
        try:
            present = self.want in self.addresses()
        except OSError:
            present = False
        if present:
            self.seen, self.misses = True, 0
            return None
        if self.seen:
            self.misses += 1
            if self.misses >= 3:
                return f"the service address {self.want} is no longer on this namespace's interfaces"
            return None
        if self.clock() - self.start >= self.grace:
            return f"the service address {self.want} never appeared on this namespace's interfaces within {self.grace:g} s"
        return None


def check_upstream_ca_file(path: str) -> bool:
    """Warns when the installation's CA file for the servers cannot be read: mitmproxy reads it for every connection, so such a
    proxy starts and looks healthy, and every connection that verifies against the file fails (the device gets a 502)."""
    if not path:
        return True
    try:
        with open(path, "rb") as f:
            data = f.read()
    except OSError as e:
        log.warning("the CA file for the servers (CHAOSGW_UPSTREAM_CA_FILE) cannot be read: %s; every connection verified against it "
                    "fails. Mount the file into the container (docs/install.md, section 3) and name the path inside it", e)
        return False
    if b"BEGIN CERTIFICATE" not in data:
        log.warning("the CA file for the servers (CHAOSGW_UPSTREAM_CA_FILE, %s) holds no PEM certificate: every connection verified "
                    "against it fails", path)
        return False
    return True


def ca_stamp(ca_dir: str) -> tuple:
    out = []
    for n in (CA_CERT, CA_KEY):
        try:
            st = os.stat(os.path.join(ca_dir, n))
            out.append((st.st_size, st.st_mtime_ns))
        except OSError:
            out.append(None)
    return tuple(out)


def read_ca(ca_dir: str) -> tuple[bytes, bytes] | None:
    """The certificate and the key of the test CA, or None while they are not there (or not a pair yet: the API writes the
    key first and the certificate second)."""
    try:
        with open(os.path.join(ca_dir, CA_CERT), "rb") as f:
            cert = f.read()
        with open(os.path.join(ca_dir, CA_KEY), "rb") as f:
            key = f.read()
    except OSError:
        return None
    if b"BEGIN CERTIFICATE" not in cert or b"PRIVATE KEY" not in key:
        return None
    return cert, key


def write_confdir(confdir: str, cert: bytes, key: bytes) -> None:
    os.makedirs(confdir, mode=0o700, exist_ok=True)
    for name in os.listdir(confdir):
        # what mitmproxy derived from an earlier CA (the PKCS#12 and DER forms) would be stale
        os.remove(os.path.join(confdir, name))
    pem = os.path.join(confdir, "mitmproxy-ca.pem")
    fd = os.open(pem, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "wb") as f:
        f.write(key.rstrip(b"\n") + b"\n" + cert)
    with open(os.path.join(confdir, "mitmproxy-ca-cert.pem"), "wb") as f:
        f.write(cert)


def mitmdump_args(a: argparse.Namespace, entry: str) -> list[str]:
    args = [
        "--set", f"confdir={a.confdir}",
        "--mode", f"regular@{a.listen}",
        "--set", "flow_detail=0",
        "--set", f"termlog_verbosity={a.verbosity}",
        "-s", entry,
    ]
    if a.upstream_ca_file:
        args += ["--set", f"ssl_verify_upstream_trusted_ca={a.upstream_ca_file}"]
    if a.upstream_insecure:
        args += ["--set", "ssl_insecure=true"]
    return args


def parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(prog="chaosgw-tls-proxy")
    p.add_argument("--api", default=os.environ.get("CHAOSGW_API", ""), help="the API's address (empty: no reports, no rules)")
    p.add_argument("--service-token-file", default=os.environ.get("CHAOSGW_SERVICE_TOKEN_FILE", ""))
    p.add_argument("--api-cert-file", default=os.environ.get("CHAOSGW_API_CERT", ""))
    p.add_argument("--ca-dir", default=os.environ.get("CHAOSGW_TLS_CA_DIR", "/var/lib/chaosgw/tlsca"))
    p.add_argument("--listen", default="127.0.0.1:9444", help="host:port the proxy listens on")
    p.add_argument("--control", default="http://127.0.0.1:9445", help="the responder's control interface")
    p.add_argument("--confdir", default="/tmp/mitmproxy")
    p.add_argument("--upstream-ca-file", default=os.environ.get("CHAOSGW_UPSTREAM_CA_FILE", ""),
                   help="CA certificates (PEM) that verify the real servers, instead of the bundle of the image: the installation's default, and what a case with `upstream: own_ca` verifies against")
    p.add_argument("--upstream-insecure", action="store_true", default=os.environ.get("CHAOSGW_UPSTREAM_INSECURE") == "1",
                   help="do not verify the certificates of the real servers (a lab with private servers): the installation's default, which a case can change with `upstream`")
    p.add_argument("--namespace-check", action=argparse.BooleanOptionalAction, default=os.environ.get("CHAOSGW_NAMESPACE_CHECK", "1") != "0",
                   help="exit (code 3) when this namespace loses the service address (plan §3.8): a restart of the holder replaces the namespace")
    p.add_argument("--service-addr", default=os.environ.get("CHAOSGW_SERVICE_ADDR", "169.254.100.2"),
                   help="the service address --namespace-check expects on this namespace's interfaces")
    p.add_argument("--verbosity", default=os.environ.get("CHAOSGW_MITM_VERBOSITY", "info"), choices=["error", "warn", "info", "alert", "debug"],
                   help="how much mitmproxy logs (debug shows its decisions: protocol detection, hooks)")
    return p


def main(argv: list[str] | None = None) -> int:
    a = parser().parse_args(argv)
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s %(message)s")

    here = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    entry = os.path.join(here, "entry.py")
    env = dict(os.environ, CHAOSGW_API=a.api, CHAOSGW_SERVICE_TOKEN_FILE=a.service_token_file, CHAOSGW_API_CERT=a.api_cert_file,
               CHAOSGW_CONTROL=a.control)
    stop = False
    child: subprocess.Popen | None = None

    def on_signal(signum, _frame):
        nonlocal stop
        stop = True
        if child is not None and child.poll() is None:
            child.terminate()

    signal.signal(signal.SIGTERM, on_signal)
    signal.signal(signal.SIGINT, on_signal)

    check_upstream_ca_file(a.upstream_ca_file)
    watch = NamespaceWatch(a.service_addr) if a.namespace_check else None

    def orphaned() -> bool:
        reason = watch.due() if watch is not None else None
        if reason is None:
            return False
        log.error("%s: the namespace was replaced (a restart of the holder); exiting so that the container joins the current one", reason)
        if child is not None and child.poll() is None:
            child.terminate()
            try:
                child.wait(10)
            except subprocess.TimeoutExpired:
                child.kill()
                child.wait()
        return True

    warned = False
    while not stop:
        if orphaned():
            return EXIT_NAMESPACE
        ca = read_ca(a.ca_dir)
        if ca is None:
            if not warned:
                warned = True
                log.warning("the test CA is not there yet: waiting for the API to make it (%s)", a.ca_dir)
            time.sleep(1)
            continue
        stamp = ca_stamp(a.ca_dir)
        write_confdir(a.confdir, *ca)
        cmd = [sys.executable, "-c", "from mitmproxy.tools.main import mitmdump; mitmdump()", *mitmdump_args(a, entry)]
        log.info("starting mitmproxy on %s", a.listen)
        child = subprocess.Popen(cmd, env=env)
        while not stop:
            if orphaned():
                return EXIT_NAMESPACE
            code = child.poll()
            if code is not None:
                log.error("mitmproxy ended with exit code %s", code)
                return code or 1  # the restart policy of the container starts it again
            if ca_stamp(a.ca_dir) != stamp:
                time.sleep(0.5)  # the certificate follows the key
                log.info("the test CA changed: restarting mitmproxy")
                child.terminate()
                try:
                    child.wait(10)
                except subprocess.TimeoutExpired:
                    child.kill()
                    child.wait()
                break
            time.sleep(1)
    if child is not None and child.poll() is None:
        try:
            child.wait(10)
        except subprocess.TimeoutExpired:
            child.kill()
    return 0


if __name__ == "__main__":
    sys.exit(main())
