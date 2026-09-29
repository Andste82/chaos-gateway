#!/usr/bin/env bash
# S9 — Capture exactly the traffic of one selector ("device cl1, TCP 8883") at the gateway.
# Ground truth: capture on cl1's own interface. Distractor traffic: cl1 UDP 7000, cl2 TCP 8883.
#   M1 AF_PACKET on lan0, BPF by MAC + port      (pre-NAT side)
#   M2 AF_PACKET on wan0, BPF by port            (post-NAT side — device not identifiable)
#   M3 NFLOG from nftables (selector = ct original tuple)
#   M4 nftables netdev dup → capture interface   (ingress + egress hooks on lan0)
#   M5 tc mirred → capture interface              (u32 on lan0 ingress + egress)
set -uo pipefail
cd "$(dirname "$0")/.."; . lib/testbed.sh
K=$(uname -r); R=results/s09-$K.jsonl; : > "$R"
T=$(pwd)/results/tmp/s09; rm -rf "$T"; mkdir -p "$T"

tb_create; tb_start_echo
MAC1=02:00:00:00:00:0b
# capture interfaces for dup / mirred: veth pairs, capture on the peer
for c in dup mir; do nsx gw ip link add ${c}0 type veth peer name ${c}1; nsx gw ip link set ${c}0 up; nsx gw ip link set ${c}1 up; done

nsx gw nft -f - <<EOF
table inet chaosgw {
  chain sel_forward { type filter hook forward priority filter; policy accept;
    meta l4proto tcp ct original ip saddr 10.10.0.11 ct original proto-dst 8883 log group 5
  }
}
table netdev capdup {
  chain in  { type filter hook ingress device "lan0" priority 0; ether saddr $MAC1 tcp dport 8883 dup to "dup0"; }
  chain out { type filter hook egress  device "lan0" priority 0; ether daddr $MAC1 tcp sport 8883 dup to "dup0"; }
}
EOF
nsx gw tc qdisc add dev lan0 clsact
nsx gw tc filter add dev lan0 ingress protocol ip u32 match ip src 10.10.0.11/32 match ip dport 8883 0xffff action mirred egress mirror dev mir0
nsx gw tc filter add dev lan0 egress  protocol ip u32 match ip dst 10.10.0.11/32 match ip sport 8883 0xffff action mirred egress mirror dev mir0

cap() { nsx "$1" tcpdump -ni "$2" -U -s 0 -w "$T/$3.pcap" $4 >/dev/null 2>&1 & echo $!; }
pids=()
pids+=($(cap cl1 eth0 truth 'tcp port 8883'))
pids+=($(cap gw lan0 m1-afpacket-lan "ether host $MAC1 and tcp port 8883"))
pids+=($(cap gw wan0 m2-afpacket-wan 'tcp port 8883'))
pids+=($(cap gw nflog:5 m3-nflog ''))
pids+=($(cap gw dup1 m4-nft-dup ''))
pids+=($(cap gw mir1 m5-tc-mirred ''))
sleep 2

# traffic: selector traffic + distractors, with a 1400-byte payload burst to check truncation
tp=()
nsx cl1 python3 tools/echo.py tcp-stream --port 8883 --duration 3 --interval 0.1 >/dev/null & tp+=($!)
nsx cl2 python3 tools/echo.py tcp-stream --port 8883 --duration 3 --interval 0.1 >/dev/null & tp+=($!)
nsx cl1 python3 tools/echo.py udp --port 7000 --count 100 --interval 0.02 >/dev/null & tp+=($!)
nsx cl1 python3 -c "
import socket, time
s = socket.create_connection(('203.0.113.10', 8883)); s.sendall(b'x' * 20000); s.settimeout(1); got = 0
try:
    while got < 20000: got += len(s.recv(65536))
except Exception: pass
"
wait "${tp[@]}"
sleep 2; kill "${pids[@]}" 2>/dev/null; sleep 1

for f in truth m1-afpacket-lan m2-afpacket-wan m3-nflog m4-nft-dup m5-tc-mirred; do
  python3 - "$T/$f.pcap" "$f" <<'PY' | tee -a "$R"
import json, subprocess, sys
f, name = sys.argv[1], sys.argv[2]
enc = subprocess.run(["capinfos", "-E", "-T", "-r", f], capture_output=True, text=True).stdout.strip().split("\t")[-1]
out = subprocess.run(["tshark", "-r", f, "-Y", "tcp", "-T", "fields", "-e", "ip.src", "-e", "ip.dst",
                      "-e", "tcp.len", "-e", "tcp.srcport", "-e", "tcp.dstport"], capture_output=True, text=True)
rows = [l.split("\t") for l in out.stdout.splitlines() if l.strip()]
addrs = sorted({r[0] for r in rows} | {r[1] for r in rows})
up = sum(1 for r in rows if r[4] == "8883"); down = sum(1 for r in rows if r[3] == "8883")
foreign = sum(1 for r in rows if "10.10.0.12" in (r[0], r[1]))
print(json.dumps({"method": name, "linktype": enc, "packets": len(rows), "upload": up, "download": down,
                  "payload_bytes": sum(int(r[2] or 0) for r in rows), "addresses": addrs,
                  "foreign_device_packets": foreign, "read_ok": out.returncode == 0}))
PY
done
tb_destroy
