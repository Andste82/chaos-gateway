#!/usr/bin/env bash
# S13 — path-MTU faults for one device (cl1), cl2 as the control.
# Case order matters: in "icmp-on" cl2 runs AFTER cl1, to show that the server
# caches the reduced PMTU for the shared NAT address and applies it to cl2 too.
#   ICMP on : fwmark-selected policy table whose routes carry "mtu lock 1280";
#             the kernel itself answers oversized DF packets with ICMP
#             "fragmentation needed, mtu 1280" in both directions.
#   ICMP off: "PMTUD black hole" — oversized packets are dropped silently
#             (nft meta length > 1280 drop), no ICMP.
set -uo pipefail
cd "$(dirname "$0")/.."; . lib/testbed.sh
K=$(uname -r); R=results/s13-$K.jsonl; : > "$R"
T=results/tmp; mkdir -p "$T"
out() { echo "$1" | tee -a "$R"; }
bulk() { nsx "$1" python3 s13-pmtud/bulk.py 203.0.113.10 7000 300000 "${2:-6}"; }
flush_pmtu() { for n in cl1 cl2 srv gw; do nsx $n ip route flush cache 2>/dev/null; done; }
maxlen() {  # maxlen <ns> → {"up":..,"down":..} max IP length seen on the device's own interface
  local ns=$1; local f="$T/s13-$1-$2.pcap"
  nsx "$ns" timeout "$3" tcpdump -ni eth0 -U -w "$f" 'tcp port 7000' >/dev/null 2>&1 &
  CAPS+=($!)
  sleep 0.5
}
lens() { python3 - "$1" <<'PY'
import json, subprocess, sys
rows = subprocess.run(["tshark", "-r", sys.argv[1], "-T", "fields", "-e", "ip.src", "-e", "ip.len"], capture_output=True, text=True).stdout.split("\n")
up = [int(r.split("\t")[1]) for r in rows if r and r.startswith("10.10.")]
down = [int(r.split("\t")[1]) for r in rows if r and not r.startswith("10.10.")]
print(json.dumps({"max_up": max(up or [0]), "max_down": max(down or [0])}))
PY
}
run_case() {  # run_case <label>
  flush_pmtu; CAPS=()
  for c in cl1 cl2; do maxlen $c "$1" 9; done
  local b1 b2 p1
  if [ "${2:-}" = cl2first ]; then b2=$(bulk cl2); b1=$(bulk cl1); else b1=$(bulk cl1); b2=$(bulk cl2); fi
  p1=$(nsx cl1 ping -M do -s 1400 -c 2 -W 1 203.0.113.10 2>&1 | grep -oE 'mtu ?= ?[0-9]+|Frag needed[^)]*\)|message too long, mtu=[0-9]+' | head -1)
  wait "${CAPS[@]}"
  out "{\"case\":\"$1\",\"cl1_bulk\":$b1,\"cl1_sizes\":$(lens $T/s13-cl1-$1.pcap),\"cl1_ping_df_1400\":\"$p1\",\"cl2_bulk\":$b2,\"cl2_sizes\":$(lens $T/s13-cl2-$1.pcap)}"
}

tb_create; tb_start_echo
# classification mark for cl1's flows (both directions), used by policy routing
nsx gw nft -f - <<'EOF'
table inet chaosgw {
  chain classify { type filter hook prerouting priority mangle; policy accept;
    ct original ip saddr 10.10.0.11 meta mark set meta mark | 0x00200000; }
  chain mtu_blackhole { type filter hook forward priority filter; policy accept; }
}
EOF
run_case baseline

# ICMP on: policy table with locked MTU for marked packets
nsx gw ip route add default via 203.0.113.10 dev wan0 mtu lock 1280 table 101
nsx gw ip route add 10.10.0.0/24 dev lan0 mtu lock 1280 table 101
nsx gw ip rule add fwmark 0x200000/0x200000 lookup 101 priority 900
run_case icmp-on-cl2-first cl2first
run_case icmp-on

# ICMP off: remove the policy route, drop oversized packets of cl1's flows silently
nsx gw ip rule del fwmark 0x200000/0x200000 lookup 101 priority 900
nsx gw nft add rule inet chaosgw mtu_blackhole 'meta mark & 0x00200000 == 0x00200000 meta length > 1280 counter drop'
run_case icmp-off-blackhole
nsx gw nft list chain inet chaosgw mtu_blackhole > "$T/s13-blackhole.txt"
tb_destroy
