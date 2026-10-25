"""The HTTP rules of an interception case (plan §2.8, M22), as pure functions.

The gateway's API describes a case as JSON (`intercept.http_rules`); this module reads those rules and decides what
happens to one request. It imports nothing of mitmproxy, so its tests run anywhere.

Semantics (the same as the text of `HttpRule` in api/openapi.yaml):

* The rules are looked at in the order they are written. Every rule whose `match` selects the request applies,
  until a terminal action (`block`, `status`, `abort`) has acted: no rule after it does.
* `delay` holds the request back (and so the response) for the given time; the delays of several rules add up.
* `modify` changes the headers of the request and of the response and replaces text in the body of the response; the
  changes of several rules are applied in order.
* `throttle` paces the bytes sent to the device at the rate of the last matching rule.
"""

from __future__ import annotations

import re
from dataclasses import dataclass, field
from typing import Any, Iterable

TERMINAL = ("block", "status", "abort")

_DURATION_PART = re.compile(r"([0-9]+(?:\.[0-9]+)?)(ns|us|ms|s|m|h)")
_UNITS = {"ns": 1e-9, "us": 1e-6, "ms": 1e-3, "s": 1.0, "m": 60.0, "h": 3600.0}
_BITRATE = re.compile(r"^([0-9]+(?:\.[0-9]+)?)(Gbit|Mbit|kbit|bit)$")
_BIT_UNITS = {"Gbit": 1e9, "Mbit": 1e6, "kbit": 1e3, "bit": 1.0}


def parse_duration(text: str) -> float:
    """Seconds of a duration the API writes ("1.5s", "200ms", "1m30s"); ValueError for anything else."""
    pos, total = 0, 0.0
    for m in _DURATION_PART.finditer(text):
        if m.start() != pos:
            break
        total += float(m.group(1)) * _UNITS[m.group(2)]
        pos = m.end()
    if pos == 0 or pos != len(text):
        raise ValueError(f"not a duration: {text!r}")
    return total


def parse_bitrate(text: str) -> int:
    """Bits per second of a rate the API writes ("2Mbit"); ValueError for anything else."""
    m = _BITRATE.match(text)
    if not m:
        raise ValueError(f"not a bit rate: {text!r}")
    return int(float(m.group(1)) * _BIT_UNITS[m.group(2)])


def host_matches(pattern: str, host: str) -> bool:
    """An exact name, or `*.suffix`, which matches the subdomains of the suffix and not the suffix itself."""
    pattern, host = pattern.lower().rstrip("."), host.lower().rstrip(".")
    if pattern.startswith("*."):
        return host.endswith("." + pattern[2:])
    return pattern == host


@dataclass(frozen=True)
class BodyReplace:
    search: str
    replace: str


@dataclass(frozen=True)
class Rule:
    action: str
    host: str | None = None
    path_prefix: str | None = None
    method: str | None = None
    status: int | None = None
    delay: float = 0.0
    rate_bps: int = 0
    request_headers: dict[str, str] = field(default_factory=dict)
    response_headers: dict[str, str] = field(default_factory=dict)
    body_replace: tuple[BodyReplace, ...] = ()

    def matches(self, host: str, path: str, method: str) -> bool:
        if self.host is not None and not host_matches(self.host, host):
            return False
        if self.path_prefix is not None and not path.startswith(self.path_prefix):
            return False
        if self.method is not None and self.method.upper() != method.upper():
            return False
        return True

    @property
    def connection_level(self) -> bool:
        """A rule that names neither a path nor a method also applies to a connection that is no HTTP."""
        return self.path_prefix is None and self.method is None


def parse_rules(raw: Iterable[dict[str, Any]] | None) -> list[Rule]:
    """The rules of `intercept.http_rules`. A rule that cannot be read (the API validates them, so this is a
    version mismatch) is left out rather than stopping the others."""
    out: list[Rule] = []
    for r in raw or []:
        try:
            m = r.get("match") or {}
            mod = r.get("modify") or {}
            out.append(
                Rule(
                    action=r["action"],
                    host=m.get("host"),
                    path_prefix=m.get("path_prefix"),
                    method=m.get("method"),
                    status=r.get("status"),
                    delay=parse_duration(r["delay"]) if r.get("delay") else 0.0,
                    rate_bps=parse_bitrate(r["rate"]) if r.get("rate") else 0,
                    request_headers=dict(mod.get("request_headers") or {}),
                    response_headers=dict(mod.get("response_headers") or {}),
                    body_replace=tuple(BodyReplace(b["search"], b["replace"]) for b in mod.get("response_body_replace") or []),
                )
            )
        except (KeyError, ValueError, TypeError, AttributeError):
            continue
    return out


@dataclass
class Decision:
    """What the rules do to one request."""

    # the terminal action that acts, if any: ("block",), ("status", code) or ("abort",)
    terminal: tuple | None = None
    delay: float = 0.0
    request_headers: list[tuple[str, str]] = field(default_factory=list)
    response_headers: list[tuple[str, str]] = field(default_factory=list)
    body_replace: list[BodyReplace] = field(default_factory=list)
    rate_bps: int = 0
    # the actions that act, in the order they act, for the log
    actions: list[str] = field(default_factory=list)

    def empty(self) -> bool:
        return not self.actions


def decide(rules: list[Rule], host: str, path: str, method: str) -> Decision:
    """The decision for an HTTP request. The path is the request target without its query."""
    d = Decision()
    path = path.split("?", 1)[0]
    for r in rules:
        if not r.matches(host, path, method):
            continue
        if r.action == "delay":
            d.delay += r.delay
        elif r.action == "modify":
            d.request_headers += list(r.request_headers.items())
            d.response_headers += list(r.response_headers.items())
            d.body_replace += list(r.body_replace)
        elif r.action == "throttle":
            d.rate_bps = r.rate_bps
        elif r.action == "block":
            d.terminal = ("block",)
        elif r.action == "status":
            d.terminal = ("status", r.status or 500)
        elif r.action == "abort":
            d.terminal = ("abort",)
        else:
            continue
        d.actions.append(r.action)
        if d.terminal:
            break
    return d


def decide_connection(rules: list[Rule], host: str) -> Decision:
    """The decision for a connection that is no HTTP (MQTT over TLS, a raw stream): only the rules that name no path
    and no method are about it. `block` and `abort` close the connection, `delay` holds it back once, `throttle` paces
    it; `status` and `modify` are about HTTP and do nothing here."""
    d = Decision()
    for r in rules:
        if not r.connection_level or not r.matches(host, "", ""):
            continue
        if r.action == "delay":
            d.delay += r.delay
        elif r.action == "throttle":
            d.rate_bps = r.rate_bps
        elif r.action in ("block", "abort"):
            d.terminal = ("abort",)
        else:
            continue
        d.actions.append(r.action)
        if d.terminal:
            break
    return d


def apply_headers(headers, changes: list[tuple[str, str]]) -> None:
    """Applies header changes to a mitmproxy `Headers` object (or any mapping with `__setitem__` and `__delitem__`):
    a value sets the header, an empty value removes it."""
    for name, value in changes:
        if value == "":
            if name in headers:
                del headers[name]
        else:
            headers[name] = value


def replace_body(content: bytes, replaces: list[BodyReplace]) -> bytes:
    """Replaces text in a body, in the order of the rules. The search and replacement are text, taken as UTF-8."""
    for r in replaces:
        if r.search:
            content = content.replace(r.search.encode("utf-8"), r.replace.encode("utf-8"))
    return content


_H3_ENTRY = re.compile(r"^\s*h3(-[0-9A-Za-z]+)?\s*=")


def strip_h3_from_alt_svc(value: str) -> str:
    """The Alt-Svc header without the advertisements of HTTP/3 (`h3`, `h3-29`, ...): a client that learns them tries QUIC,
    which the case drops (`block_quic`). Empty when nothing is left."""
    if value.strip().lower() == "clear":
        return value
    kept = [e for e in _split_alt_svc(value) if not _H3_ENTRY.match(e)]
    return ", ".join(e.strip() for e in kept)


def _split_alt_svc(value: str) -> list[str]:
    # entries are separated by commas that are not inside a quoted string
    out, cur, quoted = [], [], False
    for ch in value:
        if ch == '"':
            quoted = not quoted
        if ch == "," and not quoted:
            out.append("".join(cur))
            cur = []
        else:
            cur.append(ch)
    if cur:
        out.append("".join(cur))
    return out


UPSTREAM_POLICIES = ("verify", "own_ca", "insecure")


def parse_upstream(value: Any) -> str | None:
    """The `upstream` setting of a case: one of the three policies, or None for the installation's default (also for a value this
    version does not know: the API validates the field, so it is a newer gateway, and the default is the safe reading)."""
    return value if isinstance(value, str) and value in UPSTREAM_POLICIES else None


@dataclass(frozen=True)
class UpstreamPlan:
    """How the proxy's own connection to a server is verified: `verify` the certificate at all, against `ca_file` (None: the bundle of
    mitmproxy), or `refuse` every certificate (the case asks for a CA the installation has not given the proxy; `note` says why)."""

    verify: bool
    ca_file: str | None = None
    refuse: bool = False
    note: str = ""


def installation_policy(insecure: bool, ca_file: str | None) -> str:
    """The policy of the installation (`CHAOSGW_UPSTREAM_INSECURE`, `CHAOSGW_UPSTREAM_CA_FILE`), which a case without `upstream` has and
    which mitmproxy itself applies from its options. No verification beats a CA file, as in mitmproxy."""
    if insecure:
        return "insecure"
    return "own_ca" if ca_file else "verify"


def upstream_plan(policy: str | None, insecure: bool, ca_file: str | None) -> UpstreamPlan | None:
    """What a case's policy changes about the verification of the servers' certificates, given the installation's two settings.
    None when mitmproxy's own handling already is the policy (no policy, or the one the installation has): nothing is replaced."""
    if policy is None or policy == installation_policy(insecure, ca_file):
        return None
    if policy == "insecure":
        return UpstreamPlan(verify=False)
    if policy == "verify":
        return UpstreamPlan(verify=True)
    if ca_file:
        return UpstreamPlan(verify=True, ca_file=ca_file)
    return UpstreamPlan(verify=True, refuse=True, note="the case says own_ca and the installation names no CA file (CHAOSGW_UPSTREAM_CA_FILE)")
