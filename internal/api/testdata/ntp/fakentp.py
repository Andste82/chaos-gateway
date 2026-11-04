#!/usr/bin/env python3
# A stand-in for "the real NTP server" behind the uplink in the level 1 tests of M34: it answers a client query with the
# host's time at stratum 3 and its label as the reference id, and appends a line to the log file for every query, so that a
# test can tell whose answer it got and whether the real server was asked at all.
#
# usage: fakentp.py ADDRESS PORT LABEL LOGFILE
import socket
import struct
import sys
import time

NTP_UNIX = 2208988800
ERA = 1 << 32


def ts(t):
    return (((int(t) + NTP_UNIX) & 0xFFFFFFFF) << 32) | (int((t - int(t)) * ERA) & 0xFFFFFFFF)


def main(addr, port, label, log):
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    s.bind((addr, int(port)))
    refid = label.encode("ascii")[:4].ljust(4, b" ")
    print("ready", flush=True)
    while True:
        data, src = s.recvfrom(2048)
        rx = time.time()
        if len(data) < 48 or data[0] & 7 != 3:
            continue
        with open(log, "a") as f:
            f.write("%s %s\n" % (src[0], label))
        origin = data[40:48]
        head = struct.pack("!BBbbII", (0 << 6) | (4 << 3) | 4, 3, 6, -20, 0, 0)
        body = refid + struct.pack("!Q", ts(rx)) + origin + struct.pack("!QQ", ts(rx), ts(time.time()))
        s.sendto(head + body, src)


if __name__ == "__main__":
    main(*sys.argv[1:5])
