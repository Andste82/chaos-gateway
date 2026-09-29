#!/usr/bin/env bash
# S1 — Testbed: build the topology, check reachability through the gateway with NAT,
# measure the baseline (no faults) so later spikes can separate fault effect from noise.
set -uo pipefail
cd "$(dirname "$0")/.."; . lib/testbed.sh
OUT=results/s01-$(uname -r).json; mkdir -p results/tmp
tb_create
tb_start_echo

pass=true
for c in cl1 cl2; do
  nsx $c ping -c 3 -W 1 -q 203.0.113.10 >/dev/null || pass=false
done
# srv must see the gateway address (NAT), not the client address
nsx srv timeout 8 tcpdump -ni eth0 -c 1 'udp port 7000' -w results/tmp/s01.pcap >/dev/null 2>&1 &
cap=$!
sleep 3
nsx cl1 python3 tools/echo.py udp --count 40 --interval 0.05 >/dev/null
wait $cap
src=$(tcpdump -nr results/tmp/s01.pcap 2>/dev/null | awk '{print $3}' | head -1)

base_udp=$(nsx cl1 python3 tools/echo.py udp --count 500 --interval 0.01)
base_tcp=$(nsx cl1 python3 tools/echo.py tcp-stream --duration 5 --interval 0.02 | \
  python3 -c 'import sys,json,statistics as s; r=[json.loads(l)["ms"] for l in sys.stdin if "\"rtt\"" in l]; r.sort(); print(json.dumps({"n":len(r),"median":round(s.median(r),2),"p90":r[int(.9*len(r))],"max":r[-1]}))')

cat > "$OUT" <<EOF
{"spike":"S1","kernel":"$(uname -r)","reachability":$pass,"source_seen_by_server":"$src",
 "baseline_udp":$base_udp,"baseline_tcp_rtt_ms":$base_tcp}
EOF
cat "$OUT"
tb_destroy
