#!/usr/bin/env bash
# S8 (partial) — cost of per-packet classification and of nftables updates.
# Runs on the native kernel (no emulation). netem is not needed for these numbers;
# HTB + u32 mark filters stand in for the tc part (cls_fw may be missing on minimal kernels).
set -uo pipefail
cd "$(dirname "$0")/.."; . lib/testbed.sh
K=$(uname -r); R=results/s08-$K.jsonl; : > "$R"
T=$(pwd)/results/tmp; mkdir -p "$T"

tb_create
nsx srv iperf3 -s -D >/dev/null 2>&1; sleep 1
tput() { nsx cl1 iperf3 -c 203.0.113.10 -t "${1:-5}" -J 2>/dev/null | python3 -c 'import sys,json; print(round(json.load(sys.stdin)["end"]["sum_received"]["bits_per_second"]/1e6))'; }
pps()  { nsx cl1 iperf3 -c 203.0.113.10 -u -b 0 -l 64 -t 4 -J 2>/dev/null | python3 -c 'import sys,json; e=json.load(sys.stdin)["end"]["sum"]; print(round(e["packets"]*(1-e["lost_percent"]/100)/e["seconds"]))'; }
measure() { echo "{\"config\":\"$1\",\"tcp_mbit\":$(tput),\"udp64_pps\":$(pps)}" | tee -a "$R"; }

gen_elements() {  # 1000 devices, 1000 device+port entries, 50 classes
  python3 - <<'PY' > "$T/s08-elems.nft"
cls = 50
print("table inet chaosgw {")
print("  map fx_devport { type ipv4_addr . inet_proto . inet_service : verdict; }")
print("  map fx_dev { type ipv4_addr : verdict; }")
for i in range(cls):
    print(f"  chain cls_{i} {{ meta mark set meta mark & 0xffff00ff | 0x{(i+1)<<8:08x}; accept; }}")
print("""  chain classify { type filter hook prerouting priority mangle; policy accept;
    meta mark set meta mark & 0xffff00ff
    meta l4proto { tcp, udp } ct original ip saddr . meta l4proto . ct original proto-dst vmap @fx_devport
    ct original ip saddr vmap @fx_dev
  }
}""")
els = [f"172.16.{i//250}.{i%250+1} : goto cls_{i%cls}" for i in range(1000)]
print("add element inet chaosgw fx_dev { " + ", ".join(els) + " }")
els = [f"172.16.{i//250}.{i%250+1} . tcp . {1000+i} : goto cls_{i%cls}" for i in range(1000)]
print("add element inet chaosgw fx_devport { " + ", ".join(els) + " }")
PY
}

measure P0-nat-only
gen_elements; nsx gw nft -f "$T/s08-elems.nft"
measure P1-classify-1000-entries-miss
nsx gw nft add element inet chaosgw fx_dev '{ 10.10.0.11 : goto cls_7 }'
measure P2-classify-1000-entries-hit
# tc part: HTB root + 50 classes + u32 mark filters on both interfaces
for dev in wan0 lan0; do
  nsx gw tc qdisc add dev $dev root handle 1: htb default 1
  nsx gw tc class add dev $dev parent 1: classid 1:1 htb rate 20gbit
  for i in $(seq 1 50); do
    nsx gw tc class add dev $dev parent 1: classid 1:$(printf %x $((i+1))) htb rate 20gbit
    nsx gw tc filter add dev $dev parent 1: protocol ip prio 1 u32 match mark $((i<<8)) 0xff00 classid 1:$(printf %x $((i+1)))
  done
done
measure P3-classify-hit-plus-htb-50-classes

# nftables update latency: one process per change vs. one batch
t0=$(date +%s%N); for i in $(seq 1 100); do nsx gw nft add element inet chaosgw fx_dev "{ 192.168.200.$i : goto cls_1 }"; done; t1=$(date +%s%N)
printf 'add element inet chaosgw fx_dev { %s }\n' "$(seq -s, 1 100 | sed 's/\([0-9]*\)/192.168.201.\1 : goto cls_2/g')" > "$T/s08-batch.nft"
t2=$(date +%s%N); nsx gw nft -f "$T/s08-batch.nft"; t3=$(date +%s%N)
t4=$(date +%s%N); nsx gw nft -f "$T/s08-elems.nft" 2>/dev/null || { nsx gw nft delete table inet chaosgw; nsx gw nft -f "$T/s08-elems.nft"; }; t5=$(date +%s%N)
nsx gw nft delete table inet chaosgw; t6=$(date +%s%N); nsx gw nft -f "$T/s08-elems.nft"; t7=$(date +%s%N)
python3 -c "import json; print(json.dumps({'config':'nft-update-latency','per_process_add_ms':round(($t1-$t0)/100/1e6,2),'batch_100_ms':round(($t3-$t2)/1e6,1),'full_table_rebuild_2000_elements_ms':round(($t7-$t6)/1e6,1)}))" | tee -a "$R"
echo "{\"config\":\"host\",\"cpus\":$(nproc),\"cpu\":\"$(grep -m1 'model name' /proc/cpuinfo | cut -d: -f2 | xargs)\"}" | tee -a "$R"
tb_destroy
