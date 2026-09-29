#!/usr/bin/env python3
"""TCP echo server that records the original destination (SO_ORIGINAL_DST) of every
redirected connection, as a transparent TLS responder / proxy would need it.

origdst.py --port 9000 --log FILE     one JSON line per accepted connection
"""
import argparse, json, socket, struct, threading, time

SO_ORIGINAL_DST = 80


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", type=int, default=9000)
    ap.add_argument("--log", required=True)
    a = ap.parse_args()
    lock = threading.Lock()

    def conn(c, peer):
        try:
            raw = c.getsockopt(socket.SOL_IP, SO_ORIGINAL_DST, 16)
            port, ip = struct.unpack("!2xH4s8x", raw)
            orig = f"{socket.inet_ntoa(ip)}:{port}"
        except OSError as e:
            orig = "error:" + str(e)
        with lock, open(a.log, "a") as f:
            f.write(json.dumps({"t": round(time.time(), 3), "peer": f"{peer[0]}:{peer[1]}", "original_dst": orig}) + "\n")
        with c:
            while True:
                try:
                    d = c.recv(4096)
                except OSError:
                    return
                if not d:
                    return
                c.sendall(d)

    s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    s.bind(("0.0.0.0", a.port)); s.listen(64)
    while True:
        c, peer = s.accept()
        threading.Thread(target=conn, args=(c, peer), daemon=True).start()


if __name__ == "__main__":
    main()
