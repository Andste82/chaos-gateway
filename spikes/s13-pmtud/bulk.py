#!/usr/bin/env python3
"""Send N bytes to a TCP echo server and read them back. Prints JSON {ok, received, seconds}."""
import json, socket, sys, threading, time

host, port, n, timeout = sys.argv[1], int(sys.argv[2]), int(sys.argv[3]), float(sys.argv[4])
s = socket.create_connection((host, port), timeout=3)
s.settimeout(timeout)
t0 = time.monotonic()
threading.Thread(target=lambda: s.sendall(b"x" * n), daemon=True).start()
got = 0
try:
    while got < n:
        d = s.recv(65536)
        if not d:
            break
        got += len(d)
except (socket.timeout, OSError):
    pass
print(json.dumps({"ok": got == n, "received": got, "seconds": round(time.monotonic() - t0, 2)}))
