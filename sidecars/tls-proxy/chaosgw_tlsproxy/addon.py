"""The mitmproxy addon of the Chaos Gateway interception proxy (plan §2.8, §3.1, M22).

The proxy runs in `regular` mode behind the gateway's TLS responder: the responder takes the connection of a device that an
interception case selects, opens a connection to the proxy and asks it with `CONNECT <original destination>` to take it
there. The headers of that request say who the device is and which overlay decides the connection (the proxy never sees
the device's address otherwise). From then on the proxy terminates the device's TLS with a certificate of the test CA,
opens its own connection to the real server and applies the HTTP rules of the case.

What the addon does:

* follows the gateway's configuration (the cases of the kind `intercept` with their rules) by long poll;
* applies the rules to every request (`rules.py`): delay, status, block, abort, throttle, modify;
* reports every exchange (`/internal/intercept/flows`) and what the device did with the proxy's certificate
  (`/internal/tls/handshakes`, the report of the responder's handshakes with the case `intercept`);
* gives the TLS secrets of the connections to the gateway (`/internal/intercept/keylog`), attributed to the device, so that
  the captures that run while the device talks can carry a key log.

Everything it needs from the environment is read in `from_environment`.
"""

from __future__ import annotations

import asyncio
import logging
import os
import re
import threading
import time
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any

from mitmproxy import http, tcp, tls

from . import rules as R
from .api import Batcher, Control, Follower, ServiceClient

log = logging.getLogger("chaosgw")

# the headers of the CONNECT request of the responder (internal/tlsresponder/intercept.go)
HEADER_CONNECTION = "X-Chaosgw-Connection"
HEADER_CLIENT = "X-Chaosgw-Client"
HEADER_OVERLAY = "X-Chaosgw-Overlay"
HEADER_DEVICE = "X-Chaosgw-Device"

MAX_PATH = 1024


def now_iso(ts: float | None = None) -> str:
    return datetime.fromtimestamp(ts if ts is not None else time.time(), timezone.utc).isoformat().replace("+00:00", "Z")


@dataclass
class CaseConfig:
    rules: list[R.Rule]
    keylog: bool = True
    block_quic: bool = True
    has_throttle: bool = False


@dataclass
class Origin:
    """What the CONNECT request says of a connection."""

    connection: str = ""
    client: str = ""
    overlay: str = ""
    device: str = ""
    original_dst: str = ""
    sni: str = ""
    # key log lines of this connection that wait for the next flush
    keylines: list[str] = field(default_factory=list)


def tls_version_name(v: str | None) -> str | None:
    return {"TLSv1.3": "TLS 1.3", "TLSv1.2": "TLS 1.2"}.get(v or "")


# OpenSSL words an alert "tlsv1 alert unknown ca" (OpenSSL 3) or "tls alert bad certificate" (OpenSSL 4)
_ALERT = re.compile(r"\b(?:tlsv1|sslv3|tls) alert ([a-z ]+)", re.I)
_ALERT_NAMES = {"unknown ca": "unknown_ca", "bad certificate": "bad_certificate", "certificate expired": "certificate_expired",
                "certificate unknown": "certificate_unknown", "handshake failure": "handshake_failure", "unknown certificate": "certificate_unknown"}


def classify_client_failure(error: str | None) -> tuple[str, str | None]:
    """The outcome and the alert of a handshake the device did not complete, from the text mitmproxy gives."""
    err = (error or "").strip()
    m = _ALERT.search(err)
    if m:
        a = m.group(1).strip().lower()
        return "rejected", _ALERT_NAMES.get(a, a.replace(" ", "_"))
    low = err.lower()
    if "does not trust" in low or "disconnected" in low or "closed" in low or "reset" in low or "eof" in low:
        # the device went away after it was shown the certificate: it did not take it
        return "rejected", None
    if "timed out" in low or "timeout" in low:
        return "timeout", None
    return "client_closed", "handshake_error"


class KeylogSink:
    """Stands in for mitmproxy's key log file (`mitmproxy.net.tls.log_master_secret`): mitmproxy hands every line it would have
    written to it, with the OpenSSL connection it belongs to. The addon needs the device, and the connection does not say
    (the proxy's client is the responder), so the lines are attributed by the client random of the handshake, which is the
    second word of a line: the random of the device's ClientHello is known from the `tls_clienthello` hook, the random of
    the proxy's own connection to the server once that handshake is over."""

    __name__ = "ChaosGatewayKeylog"  # pyOpenSSL wraps the callback with functools.wraps

    def __init__(self, addon: "Addon"):
        self.addon = addon
        self.owner: dict[str, str] = {}  # client random (hex) -> client connection id
        self.waiting: dict[str, tuple[float, list[str]]] = {}

    def __call__(self, connection: Any, keymaterial: bytes) -> None:
        try:
            line = keymaterial.decode("ascii", "replace").strip()
            parts = line.split()
            if len(parts) < 3:
                return
            random = parts[1].lower()
            owner = self.owner.get(random)
            if owner is None:
                _, lines = self.waiting.setdefault(random, (time.monotonic(), []))
                if len(lines) < 32:
                    lines.append(line)
                return
            self.addon.keyline(owner, line)
        except Exception:  # a failure here must never break the handshake
            log.exception("a key log line was lost")

    def attribute(self, random: str, conn_id: str) -> None:
        random = random.lower()
        self.owner[random] = conn_id
        _, lines = self.waiting.pop(random, (0, []))
        for line in lines:
            self.addon.keyline(conn_id, line)

    def forget(self, conn_id: str) -> None:
        for r in [r for r, c in self.owner.items() if c == conn_id]:
            del self.owner[r]

    def expire(self, older_than: float = 60.0) -> None:
        cut = time.monotonic() - older_than
        for r in [r for r, (t, _) in self.waiting.items() if t < cut]:
            del self.waiting[r]


class Addon:
    def __init__(self, api: ServiceClient | None, control: Control | None, poll: float = 40.0):
        self.api = api
        self.control = control
        self.cases: dict[str, CaseConfig] = {}
        self.loaded = threading.Event()
        self.origins: dict[str, Origin] = {}
        self.keysink = KeylogSink(self)
        self.flows = Batcher("flows", api.post_flows) if api else None
        self.handshakes = Batcher("handshakes", api.post_handshakes) if api else None
        self.keylogs = Batcher("keylog", api.post_keylog) if api else None
        self.follower = Follower(api, self.on_config, poll) if api else None
        self._flusher: asyncio.Task | None = None
        self.reported_flows: list[dict] = []  # for tests without an API

    # ---- lifecycle

    def load(self, loader) -> None:
        from mitmproxy.net import tls as net_tls

        net_tls.log_master_secret = self.keysink

    async def running(self) -> None:
        for t in (self.flows, self.handshakes, self.keylogs, self.follower):
            if t is not None and not t.is_alive():
                t.start()
        if self.api is None:
            self.loaded.set()
        self._flusher = asyncio.get_running_loop().create_task(self._flush_loop())

    def done(self) -> None:
        for b in (self.flows, self.handshakes, self.keylogs):
            if b is not None:
                b.close()
        if self.follower is not None:
            self.follower.stop()

    async def _flush_loop(self) -> None:
        while True:
            await asyncio.sleep(0.5)
            self.flush_keylines()
            self.keysink.expire()

    # ---- configuration

    def on_config(self, cfg: dict) -> None:
        cases: dict[str, CaseConfig] = {}
        for c in cfg.get("cases") or []:
            if c.get("case") != "intercept":
                continue
            ic = c.get("intercept") or {}
            rs = R.parse_rules(ic.get("http_rules"))
            cases[str(c.get("overlay", "")).lower()] = CaseConfig(
                rules=rs,
                keylog=ic.get("keylog", True) is not False,
                block_quic=ic.get("block_quic", True) is not False,
                has_throttle=any(r.action == "throttle" for r in rs),
            )
        self.cases = cases
        self.loaded.set()
        log.info("the configuration is in use: generation %s, %d interception cases", cfg.get("generation"), len(cases))

    async def case_of(self, o: Origin | None) -> CaseConfig | None:
        if not self.loaded.is_set():
            # the first configuration is on its way: a request must not pass before the rules that decide it are known
            await asyncio.to_thread(self.loaded.wait, 5.0)
        if o is None or not o.overlay:
            return None
        return self.cases.get(o.overlay.lower())

    # ---- connections

    def origin(self, client) -> Origin | None:
        return self.origins.get(client.id)

    def http_connect(self, flow: http.HTTPFlow) -> None:
        h = flow.request.headers
        o = Origin(
            connection=h.get(HEADER_CONNECTION, ""),
            client=h.get(HEADER_CLIENT, ""),
            overlay=h.get(HEADER_OVERLAY, ""),
            device=h.get(HEADER_DEVICE, ""),
            original_dst=f"{flow.request.host}:{flow.request.port}",
            sni=h.get("X-Chaosgw-Sni", ""),
        )
        self.origins[flow.client_conn.id] = o

    def client_disconnected(self, client) -> None:
        self.flush_keylines([client.id])
        self.origins.pop(client.id, None)
        self.keysink.forget(client.id)

    # ---- TLS with the device

    def tls_clienthello(self, data: tls.ClientHelloData) -> None:
        try:
            raw = data.client_hello.raw_bytes(wrap_in_record=False)
            # version (2 bytes), random (32 bytes)
            self.keysink.attribute(raw[2:34].hex(), data.context.client.id)
        except Exception:
            log.exception("cannot read the client random")

    def tls_established_client(self, data: tls.TlsData) -> None:
        self._handshake(data, "accepted", None)

    def tls_failed_client(self, data: tls.TlsData) -> None:
        log.info("the handshake with a device failed: %s", data.conn.error)  # the text is mitmproxy's, kept in the log for what the classification cannot say
        outcome, alert = classify_client_failure(data.conn.error)
        self._handshake(data, outcome, alert)

    def tls_established_server(self, data: tls.TlsData) -> None:
        self._attribute_server(data)

    def tls_failed_server(self, data: tls.TlsData) -> None:
        self._attribute_server(data)
        o = self.origin(data.context.client)
        host = data.conn.sni or (data.conn.address[0] if data.conn.address else "")
        self._report_flow(o, {"protocol": "tcp", "host": host, "error": f"TLS to the server failed: {data.conn.error}"[:512], "time": now_iso()})

    def _attribute_server(self, data: tls.TlsData) -> None:
        try:
            if data.ssl_conn is not None:
                self.keysink.attribute(data.ssl_conn.client_random().hex(), data.context.client.id)
        except Exception:
            log.exception("cannot read the client random of the server connection")

    def _handshake(self, data: tls.TlsData, outcome: str, alert: str | None) -> None:
        if self.handshakes is None:
            return
        o = self.origin(data.context.client)
        if o is None or not o.client:
            return
        rep: dict[str, Any] = {"time": now_iso(), "client": o.client, "original_dst": o.original_dst, "case": "intercept", "outcome": outcome}
        sni = data.context.client.sni or o.sni
        if sni:
            rep["sni"] = sni
        if o.overlay:
            rep["overlay"] = o.overlay
        if alert:
            rep["alert"] = alert
        v = tls_version_name(data.context.client.tls_version)
        if v:
            rep["tls_version"] = v
        self.handshakes.add(rep)

    # ---- HTTP

    async def request(self, flow: http.HTTPFlow) -> None:
        o = self.origin(flow.client_conn)
        case = await self.case_of(o)
        flow.metadata["cg_start"] = time.time()
        if case is None:
            return
        d = R.decide(case.rules, flow.request.pretty_host, flow.request.path, flow.request.method)
        flow.metadata["cg"] = d
        if case.has_throttle and o is not None and o.connection and self.control is not None:
            # the rate holds for the response to this request: another request on the connection sets its own
            await asyncio.to_thread(self.control.throttle, o.connection, d.rate_bps)
        if d.delay > 0:
            await asyncio.sleep(d.delay)
        R.apply_headers(flow.request.headers, d.request_headers)
        if d.terminal is None:
            return
        kind = d.terminal[0]
        if kind == "block":
            flow.response = http.Response.make(403, b"Blocked by Chaos Gateway\n", {"Content-Type": "text/plain; charset=utf-8", "Connection": "close"})
        elif kind == "status":
            flow.response = http.Response.make(d.terminal[1], b"", {"Content-Length": "0"})
        elif kind == "abort":
            flow.metadata["cg_aborted"] = True
            # reported before the connection goes: the handling of the flow ends with it, so nothing after the abort runs
            self._report_http(flow, error="aborted by a rule")
            if o is not None and o.connection and self.control is not None:
                await asyncio.to_thread(self.control.abort, o.connection)
            flow.kill()

    def responseheaders(self, flow: http.HTTPFlow) -> None:
        """The headers of the server's response are in: the rules change them now (a streamed response has gone to the device
        by the time `response` runs), and the body is streamed to the device as it arrives unless a rule replaces text in it,
        which needs the whole body. A response that never ends (server-sent events, a long poll) so reaches the device."""
        self._change_response_headers(flow)
        d: R.Decision | None = flow.metadata.get("cg")
        res = flow.response
        if res is None or res.status_code == 101 or (d is not None and d.body_replace):
            return
        counted = flow.metadata["cg_streamed"] = [0]

        def count(chunk: bytes) -> bytes:
            counted[0] += len(chunk)
            return chunk

        res.stream = count

    def _change_response_headers(self, flow: http.HTTPFlow) -> None:
        if flow.metadata.get("cg_headers_done") or flow.response is None:
            return
        flow.metadata["cg_headers_done"] = True
        d: R.Decision | None = flow.metadata.get("cg")
        if d is not None and (d.terminal is None or d.terminal[0] != "status"):
            R.apply_headers(flow.response.headers, d.response_headers)
        if d is not None:
            o = self.origin(flow.client_conn)
            case = self.cases.get(o.overlay.lower()) if o and o.overlay else None
            if case is not None and case.block_quic and "alt-svc" in flow.response.headers:
                kept = R.strip_h3_from_alt_svc(flow.response.headers["alt-svc"])
                if kept:
                    flow.response.headers["alt-svc"] = kept
                else:
                    del flow.response.headers["alt-svc"]

    def response(self, flow: http.HTTPFlow) -> None:
        # a response the rules made themselves (`block`, `status`) has no headers event of a server: its headers are changed here
        self._change_response_headers(flow)
        d: R.Decision | None = flow.metadata.get("cg")
        if d is not None and flow.response is not None and d.body_replace and flow.response.content is not None and d.terminal is None:
            flow.response.content = R.replace_body(flow.response.content, d.body_replace)
        self._report_http(flow)

    def error(self, flow: http.HTTPFlow) -> None:
        if flow.metadata.get("cg_reported"):
            return
        if flow.websocket is not None:
            return
        self._report_http(flow, error=str(flow.error) if flow.error else "the exchange failed")

    def _report_http(self, flow: http.HTTPFlow, error: str | None = None) -> None:
        if flow.metadata.get("cg_reported"):
            return
        flow.metadata["cg_reported"] = True
        o = self.origin(flow.client_conn)
        req, res = flow.request, flow.response
        end = (res.timestamp_end if res is not None and res.timestamp_end else None) or time.time()
        d: R.Decision | None = flow.metadata.get("cg")
        rep: dict[str, Any] = {
            "time": now_iso(req.timestamp_start),
            "protocol": "http",
            "method": req.method[:16],
            "host": req.pretty_host[:253],
            "path": req.path[:MAX_PATH],
            "request_bytes": len(req.raw_content or b""),
            "duration_ms": round(max(end - req.timestamp_start, 0) * 1000, 1),
        }
        if res is not None:
            rep["status"] = res.status_code
            streamed = flow.metadata.get("cg_streamed")
            rep["response_bytes"] = streamed[0] if streamed is not None else len(res.raw_content or b"")
        if d is not None and d.actions:
            rep["actions"] = list(d.actions)
        if error:
            rep["error"] = error[:512]
        self._report_flow(o, rep)

    def _report_flow(self, o: Origin | None, rep: dict) -> None:
        if o is None or not o.client:
            return
        rep.setdefault("time", now_iso())
        rep["client"] = o.client
        rep["original_dst"] = o.original_dst
        if o.overlay:
            rep["overlay"] = o.overlay
        self.reported_flows.append(rep) if self.flows is None else self.flows.add(rep)

    # ---- WebSocket

    def websocket_message(self, flow: http.HTTPFlow) -> None:
        flow.metadata["cg_ws_messages"] = flow.metadata.get("cg_ws_messages", 0) + 1
        msg = flow.websocket.messages[-1]
        flow.metadata["cg_ws_bytes_up" if msg.from_client else "cg_ws_bytes_down"] = (
            flow.metadata.get("cg_ws_bytes_up" if msg.from_client else "cg_ws_bytes_down", 0) + len(msg.content)
        )
        # the messages are counted, not kept
        del flow.websocket.messages[:-1]

    def websocket_end(self, flow: http.HTTPFlow) -> None:
        o = self.origin(flow.client_conn)
        req = flow.request
        self._report_flow(o, {
            "time": now_iso(req.timestamp_start), "protocol": "websocket", "host": req.pretty_host[:253], "path": req.path[:MAX_PATH],
            "messages": flow.metadata.get("cg_ws_messages", 0),
            "request_bytes": flow.metadata.get("cg_ws_bytes_up", 0), "response_bytes": flow.metadata.get("cg_ws_bytes_down", 0),
            "duration_ms": round((time.time() - req.timestamp_start) * 1000, 1),
        })

    # ---- connections that are no HTTP

    async def tcp_start(self, flow: tcp.TCPFlow) -> None:
        o = self.origin(flow.client_conn)
        case = await self.case_of(o)
        flow.metadata["cg_start"] = time.time()
        flow.metadata["cg_up"] = flow.metadata["cg_down"] = 0
        if case is None:
            return
        host = flow.client_conn.sni or (o.sni if o else "") or (flow.server_conn.address[0] if flow.server_conn.address else "")
        d = R.decide_connection(case.rules, host)
        flow.metadata["cg"] = d
        if case.has_throttle and o is not None and o.connection and self.control is not None:
            await asyncio.to_thread(self.control.throttle, o.connection, d.rate_bps)
        if d.delay > 0:
            await asyncio.sleep(d.delay)
        if d.terminal is not None:
            flow.metadata["cg_aborted"] = True
            self._report_tcp(flow, None)  # before the connection goes, as for an HTTP exchange
            if o is not None and o.connection and self.control is not None:
                await asyncio.to_thread(self.control.abort, o.connection)
            flow.kill()

    def tcp_message(self, flow: tcp.TCPFlow) -> None:
        msg = flow.messages[-1]
        flow.metadata["cg_up" if msg.from_client else "cg_down"] += len(msg.content)
        del flow.messages[:-1]  # counted, not kept: a connection of an MQTT client lasts for days

    def tcp_end(self, flow: tcp.TCPFlow) -> None:
        self._report_tcp(flow, None)

    def tcp_error(self, flow: tcp.TCPFlow) -> None:
        self._report_tcp(flow, str(flow.error) if flow.error else "the connection failed")

    def _report_tcp(self, flow: tcp.TCPFlow, error: str | None) -> None:
        if flow.metadata.get("cg_reported"):
            return
        flow.metadata["cg_reported"] = True
        o = self.origin(flow.client_conn)
        d: R.Decision | None = flow.metadata.get("cg")
        start = flow.metadata.get("cg_start", time.time())
        host = flow.client_conn.sni or (o.sni if o else "") or (flow.server_conn.address[0] if flow.server_conn.address else "")
        rep = {
            "time": now_iso(start), "protocol": "tcp", "host": host[:253],
            "request_bytes": flow.metadata.get("cg_up", 0), "response_bytes": flow.metadata.get("cg_down", 0),
            "duration_ms": round((time.time() - start) * 1000, 1),
        }
        if d is not None and d.actions:
            rep["actions"] = list(d.actions)
        if flow.metadata.get("cg_aborted"):
            rep["error"] = "aborted by a rule"
        elif error:
            rep["error"] = error[:512]
        self._report_flow(o, rep)

    # ---- key log

    def keyline(self, conn_id: str, line: str) -> None:
        o = self.origins.get(conn_id)
        if o is None:
            return
        case = self.cases.get(o.overlay.lower()) if o.overlay else None
        if case is not None and not case.keylog:
            return
        o.keylines.append(line)
        if len(o.keylines) >= 200:
            self.flush_keylines([conn_id])

    def flush_keylines(self, ids: list[str] | None = None) -> None:
        if self.keylogs is None:
            return
        for cid in ids if ids is not None else list(self.origins):
            o = self.origins.get(cid)
            if o is None or not o.keylines or not o.client:
                continue
            batch: dict[str, Any] = {"client": o.client, "lines": o.keylines[:1000]}
            if o.overlay:
                batch["overlay"] = o.overlay
            del o.keylines[:1000]
            self.keylogs.add(batch)


def from_environment() -> Addon:
    """The addon as the launcher starts it: the API and the responder's control interface from the environment."""
    base = os.environ.get("CHAOSGW_API", "")
    api = ServiceClient(base, os.environ.get("CHAOSGW_SERVICE_TOKEN_FILE", ""), os.environ.get("CHAOSGW_API_CERT", "")) if base else None
    control = Control(os.environ.get("CHAOSGW_CONTROL", "http://127.0.0.1:9445"))
    return Addon(api, control)
