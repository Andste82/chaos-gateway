"""Starts mitmproxy for the gateway (the entry point of the container `tls-proxy`, plan §3.8).

It waits for the test CA the API makes, gives mitmproxy that CA (mitmproxy wants the key and the certificate in one file in
its configuration directory), starts `mitmdump` in `regular` mode with the addon, and starts it again when the CA files
change (`POST /tls/ca/regenerate`): a CA is read when mitmproxy starts. A restart costs the connections that are open, as a
regeneration of the CA does for the devices anyway.

    python -m chaosgw_tlsproxy.launcher --api https://169.254.100.1:8443 --service-token-file ... --ca-dir /var/lib/chaosgw/tlsca

The listening address is on the loopback of the service namespace: only the responder, which shares it, connects.
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


def main(argv: list[str] | None = None) -> int:
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
    p.add_argument("--verbosity", default=os.environ.get("CHAOSGW_MITM_VERBOSITY", "info"), choices=["error", "warn", "info", "alert", "debug"],
                   help="how much mitmproxy logs (debug shows its decisions: protocol detection, hooks)")
    a = p.parse_args(argv)
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

    warned = False
    while not stop:
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
