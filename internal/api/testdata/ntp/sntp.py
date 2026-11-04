#!/usr/bin/env python3
# An SNTP client for the exact assertions of the level 1 tests of the NTP server (plan M34). It shares no code with the
# server: it builds and reads the packets with struct from RFC 5905, so a mistake that the encoder and the parser of the
# server make alike (a swapped field, a wrong era) does not cancel out. It never sets the clock.
#
# usage: sntp.py SERVER[:PORT] [--count N] [--timeout S] [--interval S] [--raw HEX] [--pad BYTES] [--version V] [--bind IP]
#                [--keep 1]
#
# --keep 1 asks every query from the same socket, so from one source port: a device that keeps its socket (ntpd, a SNTP client of a
# firmware) keeps its entry in the connection tracking, and with it the decision the NAT took for its first packet.
#
# It prints one JSON object per query, a line each. --raw sends the bytes as they are (the control and private modes), --pad
# appends zero bytes to the request (a request longer than the header).
import json
import random
import socket
import struct
import sys
import time

NTP_UNIX = 2208988800
ERA = 1 << 32


def to_ntp(t):
    secs = int(t) + NTP_UNIX
    frac = int((t - int(t)) * ERA) & 0xFFFFFFFF
    return ((secs & 0xFFFFFFFF) << 32) | frac


def from_ntp(ts, pivot):
    """Unix time of an NTP timestamp, in the era that puts it nearest to the pivot (what a client with a clock does)."""
    if ts == 0:
        return None
    secs, frac = ts >> 32, ts & 0xFFFFFFFF
    era = round((pivot + NTP_UNIX - secs) / ERA)
    return secs + era * ERA - NTP_UNIX + frac / ERA


def short(v):
    return v / 65536.0


def one(sock, server, port, raw, pad, version, timeout):
    now = time.time()
    if raw is None:
        # a random low half makes the transmit timestamp a nonce for the origin check
        xmit = (to_ntp(now) & ~0xFFFF) | random.getrandbits(16) | 1
        req = struct.pack("!BBbbIII", (0 << 6) | (version << 3) | 3, 0, 0, 0, 0, 0, 0)
        req += b"\0" * 24 + struct.pack("!Q", xmit)
        req += b"\0" * pad
    else:
        xmit, req = None, raw
    out = {"sent": len(req), "replied": False}
    t1 = time.time()
    try:
        sock.sendto(req, (server, port))
    except OSError as e:
        # no route to the server: the query never left
        out["error"] = "unreachable: " + str(e)
        return out
    # the port is the kernel's choice at the first send
    out["sport"] = sock.getsockname()[1]
    deadline = time.time() + timeout
    while True:
        left = deadline - time.time()
        if left <= 0:
            out["error"] = "timeout"
            return out
        sock.settimeout(left)
        try:
            data, src = sock.recvfrom(4096)
        except socket.timeout:
            out["error"] = "timeout"
            return out
        except OSError as e:
            out["error"] = "refused" if getattr(e, "errno", 0) == 111 else str(e)
            return out
        t4 = time.time()
        break
    out["replied"] = True
    out["size"] = len(data)
    out["from"], out["from_port"] = src[0], src[1]
    if len(data) < 48:
        return out
    b0, stratum, poll, prec, rdelay, rdisp = struct.unpack("!BBbbII", data[:12])
    refid = data[12:16]
    ref, org, rec, xmt = struct.unpack("!QQQQ", data[16:48])
    out.update({
        "leap": b0 >> 6, "version": (b0 >> 3) & 7, "mode": b0 & 7, "stratum": stratum, "poll": poll, "precision": prec,
        "root_delay": short(rdelay), "root_dispersion": short(rdisp),
        "refid_hex": refid.hex(),
        "refid_ip": socket.inet_ntoa(refid),
        "refid_ascii": refid.rstrip(b"\0").decode("ascii", "replace"),
        "reference": from_ntp(ref, t4), "origin": from_ntp(org, t4), "receive": from_ntp(rec, t4), "transmit": from_ntp(xmt, t4),
        "t1": t1, "t4": t4,
    })
    out["origin_ok"] = xmit is not None and org == xmit
    if rec and xmt:
        t2, t3 = out["receive"], out["transmit"]
        out["offset"] = ((t2 - t1) + (t3 - t4)) / 2
        out["delay"] = (t4 - t1) - (t3 - t2)
    # a correct client takes an answer only when it is a server reply to its own request, from the server it asked, that
    # the server says it has a time for (RFC 5905, section 8; RFC 4330, section 5)
    out["strict_ok"] = (
        xmit is not None and out["mode"] == 4 and out["version"] in (3, 4) and out["leap"] != 3 and 1 <= stratum <= 15
        and out["origin_ok"] and xmt != 0 and rec != 0 and src[0] == server and src[1] == port
    )
    # a deliberately careless client takes whatever 48 bytes come back
    out["careless_ok"] = True
    return out


def main(argv):
    server, port = argv[0], 123
    if ":" in server:
        server, p = server.rsplit(":", 1)
        port = int(p)
    count, timeout, interval, raw, pad, version, bind, keep = 1, 2.0, 0.0, None, 0, 4, None, False
    i = 1
    while i < len(argv):
        k, v = argv[i], argv[i + 1]
        i += 2
        if k == "--count":
            count = int(v)
        elif k == "--timeout":
            timeout = float(v)
        elif k == "--interval":
            interval = float(v)
        elif k == "--raw":
            raw = bytes.fromhex(v)
        elif k == "--pad":
            pad = int(v)
        elif k == "--version":
            version = int(v)
        elif k == "--bind":
            bind = v
        elif k == "--keep":
            keep = v == "1"
        else:
            sys.exit("unknown option " + k)
    kept = None
    for n in range(count):
        # a new socket (a new source port) for each query, unless --keep: the reply of an earlier one must not answer a later one
        if keep and kept is not None:
            sock = kept
        else:
            sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
            if bind:
                sock.bind((bind, 0))
            if keep:
                kept = sock
        try:
            print(json.dumps(one(sock, server, port, raw, pad, version, timeout)), flush=True)
        finally:
            if not keep:
                sock.close()
        if interval and n + 1 < count:
            time.sleep(interval)


if __name__ == "__main__":
    main(sys.argv[1:])
