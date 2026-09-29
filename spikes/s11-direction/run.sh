#!/usr/bin/env bash
# S11 — classification with a direction bit across two test networks, destination
# selectors, and an nftables layout that keeps dynamic data across re-applies.
#
# Mark layout under test: bits 8..15 = effective-fault id, bit 16 = direction
# (0 = packet travels in the connection's original direction = "upload" of the
# initiator, 1 = reply direction = "download"). tc on EVERY interface maps
# (id, dir) → class, so the mapping no longer depends on which interface is "home".
#
# Faults:  g (0x0a) group {cl1 in LAN A, cl3 in LAN B}: up 100 ms, down 20 ms
#          d (0x0b) cl1 → 203.0.113.20 only: up 50 ms, down 0
set -uo pipefail
cd "$(dirname "$0")/.."; . lib/testbed.sh
K=$(uname -r); R=results/s11-$K.jsonl; : > "$R"
T=results/tmp; mkdir -p "$T"
chk() { python3 tools/check.py "$@" --file "$R"; }
udp() { nsx "$1" python3 tools/echo.py udp --host "$2" --port 7000 --count "${3:-300}" --interval "${4:-0.01}"; }
tcpmed() { nsx "$1" python3 tools/echo.py tcp-stream --host "$2" --port 7000 --duration 4 --interval 0.02 | \
           python3 -c 'import sys,json,statistics as s; r=[json.loads(l)["ms"] for l in sys.stdin if "\"rtt\"" in l]; print(json.dumps({"median":round(s.median(r),2),"n":len(r)}))'; }

apply_rules() {  # re-applicable transaction: never deletes the table, sets or counters
  nsx gw nft -f - <<'EOF'
add table inet chaosgw
add set inet chaosgw hs_1 { type ipv4_addr . ipv4_addr; flags timeout; }
add counter inet chaosgw c_g
add counter inet chaosgw c_d
add map inet chaosgw fx_devdst { type ipv4_addr . ipv4_addr : verdict; }
add map inet chaosgw fx_dev { type ipv4_addr : verdict; }
add chain inet chaosgw cls_g
add chain inet chaosgw cls_d
add chain inet chaosgw classify { type filter hook prerouting priority mangle; policy accept; }
flush chain inet chaosgw classify
flush chain inet chaosgw cls_g
flush chain inet chaosgw cls_d
flush map inet chaosgw fx_devdst
flush map inet chaosgw fx_dev
add rule inet chaosgw cls_g counter name c_g meta mark set meta mark & 0xffff00ff | 0x00000a00 accept
add rule inet chaosgw cls_d counter name c_d meta mark set meta mark & 0xffff00ff | 0x00000b00 accept
add rule inet chaosgw classify meta mark set meta mark & 0xfffe00ff
add rule inet chaosgw classify ct direction reply meta mark set meta mark | 0x00010000
add rule inet chaosgw classify ct original ip saddr . ct original ip daddr vmap @fx_devdst
add rule inet chaosgw classify ct original ip saddr vmap @fx_dev
add element inet chaosgw fx_devdst { 10.10.0.11 . 203.0.113.20 : goto cls_d }
add element inet chaosgw fx_dev { 10.10.0.11 : goto cls_g, 10.20.0.13 : goto cls_g }
EOF
}

tc_setup() {  # tc_setup dir|nodir
  for dev in wan0 lan0 lan1; do
    nsx gw tc qdisc del dev $dev root 2>/dev/null
    nsx gw tc qdisc add dev $dev root handle 1: htb default 1
    nsx gw tc class add dev $dev parent 1: classid 1:1 htb rate 10gbit
  done
  cls() {  # cls <dev> <minor> <fw handle/mask> <netem args>
    nsx gw tc class add dev $1 parent 1: classid 1:$2 htb rate 10gbit
    nsx gw tc qdisc add dev $1 parent 1:$2 handle $2: netem limit 10000 $4
    nsx gw tc filter add dev $1 parent 1: protocol all prio 1 handle $3 fw classid 1:$2
  }
  if [ "$1" = dir ]; then
    for dev in wan0 lan0 lan1; do        # identical mapping on every interface
      cls $dev a   0x00a00/0x1ff00 "delay 100ms"   # g, original direction  = upload
      cls $dev 10a 0x10a00/0x1ff00 "delay 20ms"    # g, reply direction     = download
      cls $dev b   0x00b00/0x1ff00 "delay 50ms"    # d, upload
      cls $dev 10b 0x10b00/0x1ff00 "delay 0ms"     # d, download
    done
  else                                   # old design: per-interface meaning, no direction bit
    cls wan0 a 0x0a00/0xff00 "delay 100ms"; cls wan0 b 0x0b00/0xff00 "delay 50ms"
    for dev in lan0 lan1; do cls $dev a 0x0a00/0xff00 "delay 20ms"; cls $dev b 0x0b00/0xff00 "delay 0ms"; done
  fi
}

log "S11 on kernel $K"
tb_create; tb_add_lan2; tb_start_echo
nsx cl3 python3 tools/echo.py server --port 7000 >/dev/null 2>&1 &
sleep 1
BASE=$(udp cl2 203.0.113.10 | python3 -c 'import sys,json; print(json.load(sys.stdin)["rtt_ms"]["median"])')
chk record --test baseline --kind info --measured "{\"base_median_ms\":$BASE}"

apply_rules
tc_setup dir
chk record --test D1-cl1-to-srv --expect 120 --base "$BASE" --measured "$(udp cl1 203.0.113.10)" --note "group g: 100 up (wan0, orig) + 20 down (lan0, reply)"
chk record --test D2-cl3-to-srv --expect 120 --base "$BASE" --measured "$(udp cl3 203.0.113.10)" --note "same group from LAN B"
chk record --test D3-cl1-to-cl3 --expect 120 --base "$BASE" --measured "$(udp cl1 10.20.0.13)" --note "A→B: orig leaves lan1 (100), reply leaves lan0 (20)"
chk record --test D4-cl2-to-cl3 --expect 0 --base "$BASE" --measured "$(udp cl2 10.20.0.13)" --note "initiator cl2 has no fault; cl3 is only the responder"
# TCP here: the UDP echo server answers from its primary address .10, not from .20
chk record --test D5-cl1-to-dst20 --expect 50 --base "$BASE" --measured "$(tcpmed cl1 203.0.113.20)" --note "device+destination beats group (TCP)"
chk record --test D5b-cl1-to-dst10-tcp --expect 120 --base "$BASE" --measured "$(tcpmed cl1 203.0.113.10)" --note "same device, other destination → group"
nsx gw nft list counter inet chaosgw c_g > "$T/s11-counter.txt"

# T1 — dynamic data across re-apply
nsx gw nft add element inet chaosgw hs_1 '{ 10.10.0.11 . 203.0.113.20 timeout 300s }'
c_before=$(nsx gw nft -j list counter inet chaosgw c_g | python3 -c 'import sys,json; print([x["counter"]["packets"] for x in json.load(sys.stdin)["nftables"] if "counter" in x][0])')
for i in 1 2 3; do apply_rules; done
c_after=$(nsx gw nft -j list counter inet chaosgw c_g | python3 -c 'import sys,json; print([x["counter"]["packets"] for x in json.load(sys.stdin)["nftables"] if "counter" in x][0])')
elems=$(nsx gw nft -j list set inet chaosgw hs_1 | python3 -c 'import sys,json; s=[x["set"] for x in json.load(sys.stdin)["nftables"] if "set" in x][0]; print(len(s.get("elem",[])))')
chk record --test T1-reapply-keeps-dynamic-data --kind info --measured "{\"set_elements_after_3_reapplies\":$elems,\"counter_before\":$c_before,\"counter_after\":$c_after}"

# atomic re-apply under traffic: no packet of cl1 may lose its classification
nsx cl1 python3 tools/echo.py udp --host 203.0.113.10 --port 7000 --count 1500 --interval 0.004 > "$T/s11-atomic.json" &
p=$!
n=0; while kill -0 $p 2>/dev/null; do apply_rules; n=$((n+1)); done
wait $p
python3 - "$T/s11-atomic.json" "$BASE" "$n" <<'PY' | tee -a "$R"
import json, sys
m = json.load(open(sys.argv[1])); base = float(sys.argv[2])
# per-packet RTTs are not in the summary; use min as the tell-tale: an unclassified packet has RTT ≈ base
print(json.dumps({"test": "T2-atomic-reapply-under-traffic", "reapplies": int(sys.argv[3]),
      "received": m["received"], "min_rtt_ms": m["rtt_ms"]["min"], "median_ms": m["rtt_ms"]["median"],
      "pass_": m["rtt_ms"]["min"] > base + 100}))
PY

# the old per-interface design for comparison
tc_setup nodir
chk record --test N3-cl1-to-cl3-without-dir-bit --expect 120 --base "$BASE" --measured "$(udp cl1 10.20.0.13)" --note "expected to FAIL: lan1 maps g to download params"
chk record --test N2-cl3-to-srv-without-dir-bit --expect 120 --base "$BASE" --measured "$(udp cl3 203.0.113.10)"
tb_destroy
log "done → $R"
