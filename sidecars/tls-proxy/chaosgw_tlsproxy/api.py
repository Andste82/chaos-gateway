"""The proxy's side of the gateway's internal API (scope `service`, plan §3.1) and of the responder's control interface.

Everything here is blocking, standard library only; the addon runs it in threads. The API listens on the private link to
the service namespace (169.254.100.1) with a certificate the API container publishes: it is trusted when a file is given,
and any certificate is trusted without one, as the Go services do.
"""

from __future__ import annotations

import collections
import json
import logging
import ssl
import threading
import urllib.error
import urllib.request
from typing import Any, Callable

log = logging.getLogger("chaosgw.api")


class ApiError(Exception):
    def __init__(self, status: int, detail: str):
        super().__init__(f"{status}: {detail}")
        self.status = status


class ServiceClient:
    def __init__(self, base: str, token_file: str = "", cert_file: str = "", timeout: float = 10.0):
        self.base = base.rstrip("/")
        self.token_file = token_file
        self.timeout = timeout
        self._ctx: ssl.SSLContext | None = None
        if self.base.startswith("https://"):
            if cert_file:
                self._ctx = ssl.create_default_context(cafile=cert_file)
            else:
                self._ctx = ssl._create_unverified_context()  # the private link; see the module text

    def _token(self) -> str:
        if not self.token_file:
            return ""
        with open(self.token_file, encoding="utf-8") as f:
            return f.read().strip()

    def request(self, method: str, path: str, body: Any = None, timeout: float | None = None) -> tuple[int, Any]:
        data = None if body is None else json.dumps(body).encode("utf-8")
        req = urllib.request.Request(self.base + path, data=data, method=method)
        if data is not None:
            req.add_header("Content-Type", "application/json")
        token = self._token()
        if token:
            req.add_header("Authorization", "Bearer " + token)
        try:
            with urllib.request.urlopen(req, timeout=timeout or self.timeout, context=self._ctx) as res:
                raw = res.read()
                return res.status, (json.loads(raw) if raw else None)
        except urllib.error.HTTPError as e:
            raise ApiError(e.code, e.read(2000).decode("utf-8", "replace")) from e

    def config(self, after: int, wait: float = 40.0) -> dict | None:
        """The TLS configuration newer than `after`, None when nothing changed within the poll."""
        status, doc = self.request("GET", f"/internal/tls/config?after={after}&consumer=proxy", timeout=wait)
        return doc if status == 200 else None

    def post_handshakes(self, reports: list[dict]) -> None:
        self.request("POST", "/internal/tls/handshakes", reports)

    def post_flows(self, reports: list[dict]) -> None:
        self.request("POST", "/internal/intercept/flows", reports)

    def post_keylog(self, batches: list[dict]) -> None:
        self.request("POST", "/internal/intercept/keylog", batches)


class Control:
    """The responder's control interface: pacing and aborting a connection it handed over."""

    def __init__(self, base: str, timeout: float = 3.0):
        self.base = base.rstrip("/")
        self.timeout = timeout

    def _post(self, path: str, body: dict) -> bool:
        data = json.dumps(body).encode("utf-8")
        req = urllib.request.Request(self.base + path, data=data, method="POST", headers={"Content-Type": "application/json"})
        try:
            with urllib.request.urlopen(req, timeout=self.timeout):
                return True
        except urllib.error.HTTPError:
            return False  # the connection is gone
        except OSError as e:
            log.warning("the responder's control interface does not answer: %s", e)
            return False

    def throttle(self, connection: str, rate_bps: int) -> bool:
        return self._post("/throttle", {"connection": connection, "rate_bps": rate_bps})

    def abort(self, connection: str) -> bool:
        return self._post("/abort", {"connection": connection})


class Follower(threading.Thread):
    """Follows the configuration by long poll and hands each new one to `on_config`."""

    def __init__(self, client: ServiceClient, on_config: Callable[[dict], None], poll: float = 40.0):
        super().__init__(name="config-follower", daemon=True)
        self.client, self.on_config, self.poll = client, on_config, poll
        self._halt = threading.Event()

    def stop(self) -> None:
        self._halt.set()

    def run(self) -> None:
        after, pause, warned = 0, 1.0, ""
        while not self._halt.is_set():
            try:
                cfg = self.client.config(after, self.poll)
            except Exception as e:  # the API is away, or the setup is not applied yet: retry with a growing pause
                msg = str(e)
                if msg != warned:
                    warned = msg
                    log.warning("cannot fetch the TLS configuration, retrying: %s", e)
                self._halt.wait(pause)
                pause = min(pause * 2, 10.0)
                continue
            if warned:
                log.info("the TLS configuration can be fetched again")
                warned = ""
            pause = 1.0
            if cfg is not None:
                after = int(cfg.get("generation", after))
                self.on_config(cfg)


class Batcher(threading.Thread):
    """Collects reports and posts them in batches, at once when the API is idle. Reports the API does not take are kept
    and sent again; past `limit` the oldest are dropped."""

    def __init__(self, name: str, post: Callable[[list[dict]], None], batch: int = 100, retry: float = 2.0, limit: int = 5000):
        super().__init__(name=name, daemon=True)
        self.post, self.batch, self.retry, self.limit = post, batch, retry, limit
        self._items: collections.deque[dict] = collections.deque()
        self._cv = threading.Condition()
        self._closed = False
        self.dropped = 0

    def add(self, item: dict) -> None:
        with self._cv:
            if len(self._items) >= self.limit:
                self._items.popleft()
                self.dropped += 1
            self._items.append(item)
            self._cv.notify()

    def close(self) -> None:
        """Sends what is left (once) and ends."""
        with self._cv:
            self._closed = True
            self._cv.notify()
        self.join(timeout=5)

    def run(self) -> None:
        while True:
            with self._cv:
                while not self._items and not self._closed:
                    self._cv.wait()
                if not self._items:
                    return
                batch = [self._items.popleft() for _ in range(min(len(self._items), self.batch))]
            try:
                self.post(batch)
            except Exception as e:
                log.debug("a batch of %s was not accepted: %s", self.name, e)
                with self._cv:
                    if self._closed:
                        return
                    self._items = collections.deque(([*batch, *self._items])[-self.limit :])
                    self._cv.wait(self.retry)
