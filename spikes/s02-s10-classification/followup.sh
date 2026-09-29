#!/usr/bin/env bash
# S2 follow-up (with fixed-rate UDP sender): jitter/reordering, qdisc replace vs change
# under real queue occupancy, and clearing a "sticky" netem rate.
set -uo pipefail
cd "$(dirname "$0")/.."; . lib/testbed.sh
K=$(uname -r); R=results/s02b-$K.jsonl; : > "$R"
chk() { python3 tools/check.py "$@" --file "$R"; }
tc_root() { nsx gw tc qdisc add dev "$1" root handle 1: htb default 1; nsx gw tc class add dev "$1" parent 1: classid 1:1 htb rate 10gbit; }
tc_new()  { nsx gw tc class add dev wan0 parent 1: classid 1:a htb rate 10gbit
            nsx gw tc qdisc add dev wan0 parent 1:a handle a: netem limit 10000 delay 0ms
            nsx gw tc filter add dev wan0 parent 1: protocol all prio 1 handle 0x0a00/0xff00 fw classid 1:a; }
udp() { nsx cl1 python3 tools/echo.py udp --count "${1:-1000}" --interval "${2:-0.002}"; }
iperf_mbit() { nsx cl1 iperf3 -c 203.0.113.10 -t 4 -J 2>/dev/null | python3 -c 'import sys,json; print(round(json.load(sys.stdin)["end"]["sum_received"]["bits_per_second"]/1e6,3))'; }

tb_create; tb_start_echo; nsx srv iperf3 -s -D >/dev/null 2>&1
tc_root wan0; tc_new
nsx gw nft -f - <<'EOF'
table inet chaosgw {
  chain classify {
    type filter hook prerouting priority mangle; policy accept;
    ct original ip saddr 10.10.0.11 meta mark set meta mark & 0xffff00ff | 0x00000a00
  }
}
EOF
nsx gw nft list table inet chaosgw >/dev/null || { echo "ruleset failed to load"; exit 1; }
BASE=$(udp 300 0.01 | python3 -c 'import sys,json; print(json.load(sys.stdin)["rtt_ms"]["median"])')
chk record --test F0-baseline --kind info --measured "{\"base_median_ms\":$BASE}"
nsx gw tc qdisc change dev wan0 parent 1:a handle a: netem limit 10000 delay 100ms
chk record --test F0-sanity-100ms --expect 100 --base "$BASE" --measured "$(udp 500 0.004)"

# F1 — jitter: does netem reorder? (1000 packets, 2 ms apart, delay 50 ± 20 ms)
for v in "delay 50ms 20ms" "delay 50ms 20ms rate 1gbit" "delay 50ms 20ms distribution normal"; do
  nsx gw tc qdisc change dev wan0 parent 1:a handle a: netem limit 10000 $v
  chk record --test "F1-jitter [$v]" --kind info --measured "$(udp 1000 0.002)"
done

# F2 — ~40 packets queued (80 ms delay, one packet every 2 ms): 20x change vs 20x replace
nsx gw tc qdisc change dev wan0 parent 1:a handle a: netem limit 10000 delay 80ms
for op in change replace; do
  f=results/tmp/s02b-$op.json
  udp 1500 0.002 > "$f" &
  p=$!
  sleep 0.5
  for i in $(seq 1 20); do nsx gw tc qdisc $op dev wan0 parent 1:a handle a: netem limit 10000 delay $((70 + i))ms; sleep 0.1; done
  wait $p
  chk record --test "F2-20x-$op-under-load" --kind info --measured "$(cat $f)"
done

# F3 — rate stickiness on "change", and how to clear it
nsx gw tc qdisc change dev wan0 parent 1:a handle a: netem limit 10000 delay 0ms rate 2mbit
a=$(iperf_mbit)
nsx gw tc qdisc change dev wan0 parent 1:a handle a: netem limit 10000 delay 0ms
b=$(iperf_mbit)
nsx gw tc qdisc change dev wan0 parent 1:a handle a: netem limit 10000 delay 0ms rate 0bit 2> results/tmp/s02b-rate0.err
c=$(iperf_mbit)
nsx gw tc qdisc replace dev wan0 parent 1:a handle a: netem limit 10000 delay 0ms
d=$(iperf_mbit)
chk record --test F3-sticky-rate --kind info --measured "{\"with_rate_2mbit\":$a,\"change_without_rate\":$b,\"change_with_rate_0bit\":$c,\"rate0_error\":\"$(head -c 120 results/tmp/s02b-rate0.err | tr '\n"' ' ')\",\"after_replace\":$d}"
nsx gw tc -s qdisc show dev wan0 > results/tmp/s02b-qdisc.txt
tb_destroy
