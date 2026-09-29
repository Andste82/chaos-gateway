#!/usr/bin/env bash
# S10 + S2 — classification identity and fault topology.
#
# Design under test (plan §3.3):
#   nftables prerouting (mangle): classify EVERY packet by the conntrack original
#   tuple (device address before NAT, protocol, server port) → verdict map →
#   per-class chain writes the class id into mark bits 8..15 (0x0000ff00).
#   tc on each egress interface: HTB root, default class unimpaired, one class per
#   id with a netem leaf, fw filter "mark/0xff00 → class". wan0 = upload, lan0 = download.
#
# Variant "ctmark": id computed only for NEW connections and cached in ct mark
# (the approach the plan rejects) — used to show the difference on live changes.
set -uo pipefail
cd "$(dirname "$0")/.."; . lib/testbed.sh
K=$(uname -r); R=results/s02-s10-$K.jsonl; : > "$R"
T=results/tmp; mkdir -p "$T"
chk() { python3 tools/check.py "$@" --file "$R"; }

load_chaosgw() {  # $1 = perpacket | ctmark
  nsx gw nft delete table inet chaosgw 2>/dev/null
  if [ "$1" = perpacket ]; then
    nsx gw nft -f - <<'EOF'
table inet chaosgw {
  map fx_devport { type ipv4_addr . inet_proto . inet_service : verdict; }
  map fx_dev     { type ipv4_addr : verdict; }
  counter cls_a {} ; counter cls_b {} ; counter cls_c {}
  chain cls_a { counter name cls_a; meta mark set meta mark & 0xffff00ff | 0x00000a00; accept; }
  chain cls_b { counter name cls_b; meta mark set meta mark & 0xffff00ff | 0x00000b00; accept; }
  chain cls_c { counter name cls_c; meta mark set meta mark & 0xffff00ff | 0x00000c00; accept; }
  chain classify {
    type filter hook prerouting priority mangle; policy accept;
    meta mark set meta mark & 0xffff00ff
    meta l4proto { tcp, udp } ct original ip saddr . meta l4proto . ct original proto-dst vmap @fx_devport
    ct original ip saddr vmap @fx_dev
  }
}
EOF
  else
    nsx gw nft -f - <<'EOF'
table inet chaosgw {
  map fx_devport { type ipv4_addr . inet_proto . inet_service : verdict; }
  map fx_dev     { type ipv4_addr : verdict; }
  chain cls_a { meta mark set meta mark & 0xffff00ff | 0x00000a00; ct mark set meta mark; accept; }
  chain cls_b { meta mark set meta mark & 0xffff00ff | 0x00000b00; ct mark set meta mark; accept; }
  chain cls_c { meta mark set meta mark & 0xffff00ff | 0x00000c00; ct mark set meta mark; accept; }
  chain classify {
    type filter hook prerouting priority mangle; policy accept;
    ct state established,related meta mark set ct mark accept
    meta mark set meta mark & 0xffff00ff
    meta l4proto { tcp, udp } ct original ip saddr . meta l4proto . ct original proto-dst vmap @fx_devport
    ct original ip saddr vmap @fx_dev
  }
}
EOF
  fi
}

tc_root() {  # tc_root <dev>
  nsx gw tc qdisc del dev "$1" root 2>/dev/null
  nsx gw tc qdisc add dev "$1" root handle 1: htb default 1
  nsx gw tc class add dev "$1" parent 1: classid 1:1 htb rate 10gbit
}
tc_fault() {  # tc_fault <dev> <hexid> <netem args...>   (create or change in place)
  local dev=$1 id=$2; shift 2
  if nsx gw tc class show dev "$dev" classid 1:$id | grep -q .; then
    nsx gw tc qdisc change dev "$dev" parent 1:$id handle $id: netem "$@"
  else
    nsx gw tc class add dev "$dev" parent 1: classid 1:$id htb rate 10gbit
    nsx gw tc qdisc add dev "$dev" parent 1:$id handle $id: netem limit 10000 "$@"
    nsx gw tc filter add dev "$dev" parent 1: protocol all prio 1 handle 0x${id}00/0xff00 fw classid 1:$id
  fi
}
map_add() { nsx gw nft add element inet chaosgw "$1" "{ $2 }"; }
tc_reset() { nsx gw tc qdisc replace dev "$1" parent 1:$2 handle $2: netem limit 10000 delay 0ms; }
map_del() { nsx gw nft delete element inet chaosgw "$1" "{ $2 }"; }
udp()     { nsx "$1" python3 tools/echo.py udp --port "${2:-7000}" --count "${3:-300}" --interval "${4:-0.01}"; }
tcpmed()  { nsx "$1" python3 tools/echo.py tcp-stream --port "${2:-7000}" --duration "${3:-4}" --interval 0.02 | \
            python3 -c 'import sys,json,statistics as s; r=[json.loads(l)["ms"] for l in sys.stdin if "\"rtt\"" in l]; print(json.dumps({"median":round(s.median(r),2),"n":len(r)}))'; }

log "S10/S2 on kernel $K"
tb_create; tb_start_echo
nsx srv iperf3 -s -D >/dev/null 2>&1
tc_root wan0; tc_root lan0
load_chaosgw perpacket

BASE=$(udp cl1 | python3 -c 'import sys,json; print(json.load(sys.stdin)["rtt_ms"]["median"])')
chk record --test baseline --kind info --measured "{\"base_median_ms\":$BASE}"

# T1 — bidirectional device fault behind NAT, isolation of other device
tc_fault wan0 a delay 100ms
tc_fault lan0 a delay 50ms
nsx gw tc filter show dev wan0 | grep -q "fw" && fw_ok=true || fw_ok=false
chk record --test fw-mask-filter --kind info --measured "{\"fw_filter_with_mask_accepted\":$fw_ok}"
map_add fx_dev "10.10.0.11 : goto cls_a"
chk record --test T1-cl1-udp-bidir --expect 150 --base "$BASE" --measured "$(udp cl1)"
chk record --test T1-cl2-isolated --expect 0 --base "$BASE" --measured "$(udp cl2)"
ping_ms=$(nsx cl1 ping -c 50 -i 0.02 -q 203.0.113.10 | awk -F/ '/rtt/{print $5}')
chk record --test T1-cl1-icmp --expect 150 --base "$BASE" --measured "{\"median\":$ping_ms}" --note "ping avg"

# T2 — direction independence (in-place change of the leaf netem)
tc_fault lan0 a delay 0ms
chk record --test T2-upload-only --expect 100 --base "$BASE" --measured "$(udp cl1)"
tc_fault wan0 a delay 0ms; tc_fault lan0 a delay 50ms
chk record --test T2-download-only --expect 50 --base "$BASE" --measured "$(udp cl1)"

# T3 — precedence: device+port beats device
tc_fault wan0 a delay 100ms; tc_fault lan0 a delay 50ms
tc_fault wan0 b delay 20ms;  tc_fault lan0 b delay 20ms
map_add fx_devport "10.10.0.11 . tcp . 7000 : goto cls_b"
chk record --test T3-devport-tcp7000 --expect 40 --base "$BASE" --measured "$(tcpmed cl1 7000)" --note "device+port rule wins"
chk record --test T3-device-udp7000 --expect 150 --base "$BASE" --measured "$(udp cl1)" --note "udp not in devport map → device rule"
map_del fx_devport "10.10.0.11 . tcp . 7000 : goto cls_b"
nsx gw nft list counters table inet chaosgw > "$T/s02-counters.txt"

# T4 — live change on an existing connection: per-packet vs ct-mark caching
map_del fx_dev "10.10.0.11 : goto cls_a"
for variant in perpacket ctmark; do
  load_chaosgw $variant
  ev="$T/s02-events-$variant.txt"; st="$T/s02-stream-$variant.jsonl"; : > "$ev"
  nsx cl1 python3 tools/echo.py tcp-stream --port 8883 --duration 12 --interval 0.05 --out "$st" &
  sp=$!
  sleep 4; c0=$(date +%s.%N); map_add fx_dev "10.10.0.11 : goto cls_a"; echo "$(date +%s.%N) on_add $c0"  >> "$ev"
  sleep 4; c0=$(date +%s.%N); map_del fx_dev "10.10.0.11 : goto cls_a"; echo "$(date +%s.%N) off_del $c0" >> "$ev"
  wait $sp
  chk timeline --test "T4-live-change-$variant" --stream "$st" --events "$ev" --base "$BASE" --added 150
done
load_chaosgw perpacket

# T5 — loss accuracy (upload 10 %, then combined with download 5 %)
tc_fault wan0 a loss 10%; tc_fault lan0 a delay 0ms
map_add fx_dev "10.10.0.11 : goto cls_a"
chk record --test T5-loss-up10 --kind loss --expect 10 --measured "$(udp cl1 7000 3000 0.005)"
tc_fault lan0 a loss 5%
chk record --test T5-loss-up10-down5 --kind loss --expect 14.5 --measured "$(udp cl1 7000 3000 0.005)" --note "1-(0.9*0.95)"
tc_fault wan0 a delay 0ms; tc_fault lan0 a delay 0ms

# T6 — rate limit: netem rate vs HTB class rate (upload 2 Mbit/s)
tc_fault wan0 a rate 2mbit
r1=$(nsx cl1 iperf3 -c 203.0.113.10 -t 6 -J 2>/dev/null | python3 -c 'import sys,json; print(round(json.load(sys.stdin)["end"]["sum_received"]["bits_per_second"]/1e6,3))')
tc_fault wan0 a delay 0ms
# does "tc qdisc change" without a rate argument keep the old rate? (compiler must know)
r_sticky=$(nsx cl1 iperf3 -c 203.0.113.10 -t 4 -J 2>/dev/null | python3 -c 'import sys,json; print(round(json.load(sys.stdin)["end"]["sum_received"]["bits_per_second"]/1e6,3))')
tc_reset wan0 a
nsx gw tc class change dev wan0 parent 1: classid 1:a htb rate 2mbit ceil 2mbit
r2=$(nsx cl1 iperf3 -c 203.0.113.10 -t 6 -J 2>/dev/null | python3 -c 'import sys,json; print(round(json.load(sys.stdin)["end"]["sum_received"]["bits_per_second"]/1e6,3))')
nsx gw tc class change dev wan0 parent 1: classid 1:a htb rate 10gbit
chk record --test T6-rate-2mbit --kind info --measured "{\"netem_rate_mbit\":$r1,\"after_change_without_rate_mbit\":$r_sticky,\"htb_rate_mbit\":$r2}"

# T7 — jitter and reordering
tc_fault wan0 a delay 50ms 20ms
j1=$(udp cl1 7000 500 0.005)
tc_fault wan0 a delay 50ms 20ms rate 1gbit
j2=$(udp cl1 7000 500 0.005)
tc_reset wan0 a
chk record --test T7-jitter-plain --kind info --measured "$j1"
chk record --test T7-jitter-with-rate --kind info --measured "$j2"

# T8 — changing one class must not disturb another; replace vs change on the same class
tc_fault wan0 c delay 10ms; map_add fx_dev "10.10.0.12 : goto cls_c"
tc_fault wan0 a delay 80ms
nsx cl2 python3 tools/echo.py udp --count 1000 --interval 0.005 > "$T/s02-t8-cl2.json" &
p2=$!
nsx cl1 python3 tools/echo.py udp --count 1000 --interval 0.005 > "$T/s02-t8-cl1-change.json" &
p1=$!
for i in $(seq 1 20); do tc_fault wan0 a delay $((60 + i))ms; sleep 0.2; done
wait $p1 $p2
chk record --test T8-other-class-while-changing --kind loss --expect 0 --measured "$(cat $T/s02-t8-cl2.json)"
chk record --test T8-own-class-change --kind info --measured "$(cat $T/s02-t8-cl1-change.json)" --note "20x tc qdisc change during flow"
nsx cl1 python3 tools/echo.py udp --count 1000 --interval 0.005 > "$T/s02-t8-cl1-replace.json" &
p1=$!
for i in $(seq 1 20); do nsx gw tc qdisc replace dev wan0 parent 1:a handle a: netem limit 10000 delay $((60 + i))ms; sleep 0.2; done
wait $p1
chk record --test T8-own-class-replace --kind info --measured "$(cat $T/s02-t8-cl1-replace.json)" --note "20x tc qdisc replace during flow"

# T9 — traffic terminating at the gateway: ingress → IFB, L3 match (marks not set yet at tc ingress)
nsx gw ip link add ifb0 type ifb; nsx gw ip link set ifb0 up
nsx gw tc qdisc add dev lan0 handle ffff: ingress
nsx gw tc filter add dev lan0 parent ffff: protocol ip prio 1 flower dst_ip 10.10.0.1 action mirred egress redirect dev ifb0
nsx gw tc qdisc add dev ifb0 root handle 1: htb default 1
nsx gw tc class add dev ifb0 parent 1: classid 1:1 htb rate 10gbit
nsx gw tc class add dev ifb0 parent 1: classid 1:a htb rate 10gbit
nsx gw tc qdisc add dev ifb0 parent 1:a handle a: netem delay 100ms
nsx gw tc filter add dev ifb0 parent 1: protocol ip prio 1 flower src_ip 10.10.0.11 classid 1:a
p1=$(nsx cl1 ping -c 30 -i 0.05 -q 10.10.0.1 | awk -F/ '/rtt/{print $5}')
p2=$(nsx cl2 ping -c 30 -i 0.05 -q 10.10.0.1 | awk -F/ '/rtt/{print $5}')
chk record --test T9-ifb-local-cl1 --expect 100 --base 0 --measured "{\"median\":$p1}" --note "ping to gateway itself, avg"
chk record --test T9-ifb-local-cl2 --expect 0 --base 0 --measured "{\"median\":$p2}" --note "unaffected; ping to gateway itself"

# T10 — loss on locally generated vs forwarded TCP (5 % on wan0 egress)
nsx gw ip route add 203.0.113.99/32 dev wan0 2>/dev/null
tc_fault wan0 a loss 5%
nsx gw nft add table ip t10
nsx gw nft 'add chain ip t10 out { type route hook output priority mangle; }'
nsx gw nft 'add rule ip t10 out ip daddr 203.0.113.10 tcp dport 5201 meta mark set 0x0a00'
fwd=$(nsx cl1 iperf3 -c 203.0.113.10 -t 6 -J 2>/dev/null | python3 -c 'import sys,json; j=json.load(sys.stdin)["end"]; print(json.dumps({"mbit":round(j["sum_received"]["bits_per_second"]/1e6,2),"retrans":j["sum_sent"]["retransmits"]}))')
loc=$(nsx gw iperf3 -c 203.0.113.10 -t 6 -J 2>/dev/null | python3 -c 'import sys,json; j=json.load(sys.stdin)["end"]; print(json.dumps({"mbit":round(j["sum_received"]["bits_per_second"]/1e6,2),"retrans":j["sum_sent"]["retransmits"]}))')
chk record --test T10-loss-forwarded-vs-local --kind info --measured "{\"forwarded\":$fwd,\"local\":$loc}"

nsx gw tc -s class show dev wan0 > "$T/s02-tc-wan0.txt"
tb_destroy
log "done → $R"
