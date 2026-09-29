#!/usr/bin/env python3
"""Measurement helper for the Chaos Gateway spikes.

server       TCP + UDP echo on --port
udp          send --count sequenced datagrams, report loss / RTT / reordering
tcp-stream   one persistent TCP connection, one message per --interval,
             logs per-message RTT and connection events as JSON lines
tcp-connect  try a new TCP connection: ok | refused | reset | timeout
"""
import argparse, json, socket, statistics, struct, sys, threading, time


def stats(xs):
    if not xs:
        return {}
    s = sorted(xs)
    q = lambda p: s[min(len(s) - 1, int(p * len(s)))]
    return {"n": len(s), "min": round(s[0], 2), "p10": round(q(.10), 2),
            "median": round(statistics.median(s), 2), "p90": round(q(.90), 2),
            "max": round(s[-1], 2), "mean": round(statistics.mean(s), 2),
            "stdev": round(statistics.pstdev(s), 2)}


def server(port):
    def tcp_conn(c):
        with c:
            while True:
                try:
                    d = c.recv(4096)
                except OSError:
                    return
                if not d:
                    return
                c.sendall(d)

    def tcp():
        s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        s.bind(("0.0.0.0", port)); s.listen(64)
        while True:
            c, _ = s.accept()
            threading.Thread(target=tcp_conn, args=(c,), daemon=True).start()

    threading.Thread(target=tcp, daemon=True).start()
    u = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    u.bind(("0.0.0.0", port))
    while True:
        d, a = u.recvfrom(2048)
        u.sendto(d, a)


def udp(host, port, count, interval, timeout):
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    sent = {}; rtts = []; order = []
    t_end = None
    t_next = time.monotonic()
    for i in range(count):
        now = time.monotonic()
        s.sendto(struct.pack("!Id", i, now), (host, port)); sent[i] = now
        t_next += interval                      # fixed send schedule, independent of replies
        while True:
            left = t_next - time.monotonic()
            if left <= 0:
                break
            s.settimeout(left)
            try:
                d, _ = s.recvfrom(2048)
                seq, t0 = struct.unpack("!Id", d[:12])
                rtts.append((time.monotonic() - t0) * 1000); order.append(seq)
            except socket.timeout:
                break
    s.settimeout(0.05)
    t_end = time.monotonic() + timeout
    while time.monotonic() < t_end:
        try:
            d, _ = s.recvfrom(2048)
            seq, t0 = struct.unpack("!Id", d[:12])
            rtts.append((time.monotonic() - t0) * 1000); order.append(seq)
        except socket.timeout:
            pass
    reorders, hi = 0, -1
    for q in order:
        if q < hi:
            reorders += 1
        hi = max(hi, q)
    got = len(set(order))
    return {"sent": count, "received": got, "loss_pct": round(100 * (count - got) / count, 2),
            "duplicates": len(order) - got, "reordered": reorders, "rtt_ms": stats(rtts)}


def tcp_stream(host, port, duration, interval, out):
    f = open(out, "w") if out else sys.stdout
    def ev(**kw):
        kw["t"] = round(time.time(), 3); f.write(json.dumps(kw) + "\n"); f.flush()
    t_start = time.monotonic()
    try:
        c = socket.create_connection((host, port), timeout=3)
    except OSError as e:
        ev(event="connect_failed", error=str(e)); return
    c.settimeout(2.0)
    c.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
    ev(event="connected", local=c.getsockname()[1])
    seq = 0
    while time.monotonic() - t_start < duration:
        t0 = time.monotonic()
        try:
            c.sendall(struct.pack("!Id", seq, t0))
            buf = b""
            while len(buf) < 12:
                d = c.recv(12 - len(buf))
                if not d:
                    ev(event="closed_by_peer", seq=seq); return
                buf += d
            ev(event="rtt", seq=seq, ms=round((time.monotonic() - t0) * 1000, 2))
        except socket.timeout:
            ev(event="timeout", seq=seq)
        except ConnectionResetError:
            ev(event="reset", seq=seq); return
        except OSError as e:
            ev(event="error", seq=seq, error=str(e)); return
        seq += 1
        time.sleep(max(0, interval - (time.monotonic() - t0)))
    ev(event="done", seq=seq)


def tcp_connect(host, port, timeout):
    t0 = time.monotonic()
    try:
        c = socket.create_connection((host, port), timeout=timeout)
        c.sendall(b"x" * 12); c.settimeout(timeout); d = c.recv(12)
        r = "ok" if d else "closed"
    except ConnectionRefusedError:
        r = "refused"
    except ConnectionResetError:
        r = "reset"
    except socket.timeout:
        r = "timeout"
    except OSError as e:
        r = "error:" + str(e)
    return {"result": r, "ms": round((time.monotonic() - t0) * 1000, 1)}


if __name__ == "__main__":
    p = argparse.ArgumentParser()
    p.add_argument("mode", choices=["server", "udp", "tcp-stream", "tcp-connect"])
    p.add_argument("--host", default="203.0.113.10")
    p.add_argument("--port", type=int, default=7000)
    p.add_argument("--count", type=int, default=200)
    p.add_argument("--interval", type=float, default=0.02)
    p.add_argument("--duration", type=float, default=10)
    p.add_argument("--timeout", type=float, default=2)
    p.add_argument("--out")
    a = p.parse_args()
    if a.mode == "server":
        server(a.port)
    elif a.mode == "udp":
        print(json.dumps(udp(a.host, a.port, a.count, a.interval, a.timeout)))
    elif a.mode == "tcp-stream":
        tcp_stream(a.host, a.port, a.duration, a.interval, a.out)
    else:
        print(json.dumps(tcp_connect(a.host, a.port, a.timeout)))
