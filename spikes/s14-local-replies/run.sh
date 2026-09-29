#!/usr/bin/env bash
# S14 — download faults for connections that terminate on the gateway itself
# (TLS responder, DNS proxy, mitmproxy). Their replies are generated locally and
# never pass prerouting, so they carry no mark unless the output hook classifies too.
#
# cl1's TCP 8883 is redirected to a local echo server on the gateway (:9000).
# Fault for cl1: upload 0 ms, download 80 ms (direction-bit layout from S11).
set -uo pipefail
cd "$(dirname "$0")/.."; . lib/testbed.sh
K=$(uname -r); R=results/s14-$K.jsonl; : > "$R"
chk() { python3 tools/check.py "$@" --file "$R"; }
tcpmed() { nsx cl1 python3 tools/echo.py tcp-stream --host 203.0.113.10 --port "$1" --duration 4 --interval 0.02 | \
           python3 -c 'import sys,json,statistics as s; r=[json.loads(l)["ms"] for l in sys.stdin if "\"rtt\"" in l]; print(json.dumps({"median":round(s.median(r),2),"n":len(r)}))'; }

tb_create; tb_start_echo
nsx gw python3 tools/echo.py server --port 9000 >/dev/null 2>&1 &
sleep 1
nsx gw nft -f - <<'EOF'
table ip redir { chain pre { type nat hook prerouting priority dstnat; ip saddr 10.10.0.11 tcp dport 8883 redirect to :9000; }
}
table inet chaosgw {
  chain cls_a { meta mark set meta mark & 0xffff00ff | 0x00000a00 accept; }
  map fx_dev { type ipv4_addr : verdict; elements = { 10.10.0.11 : goto cls_a } }
  chain classify { type filter hook prerouting priority mangle; policy accept;
    meta mark set meta mark & 0xfffe00ff
    ct direction reply meta mark set meta mark | 0x00010000
    ct original ip saddr vmap @fx_dev;
  }
}
EOF
for dev in wan0 lan0; do
  nsx gw tc qdisc add dev $dev root handle 1: htb default 1
  nsx gw tc class add dev $dev parent 1: classid 1:1 htb rate 10gbit
  nsx gw tc class add dev $dev parent 1: classid 1:a htb rate 10gbit
  nsx gw tc qdisc add dev $dev parent 1:a handle a: netem limit 10000 delay 0ms
  nsx gw tc filter add dev $dev parent 1: protocol all prio 1 handle 0x00a00/0x1ff00 fw classid 1:a
  nsx gw tc class add dev $dev parent 1: classid 1:10a htb rate 10gbit
  nsx gw tc qdisc add dev $dev parent 1:10a handle 10a: netem limit 10000 delay 80ms
  nsx gw tc filter add dev $dev parent 1: protocol all prio 1 handle 0x10a00/0x1ff00 fw classid 1:10a
done
BASE=$(nsx cl2 python3 tools/echo.py tcp-stream --host 203.0.113.10 --port 7000 --duration 3 --interval 0.02 | \
       python3 -c 'import sys,json,statistics as s; r=[json.loads(l)["ms"] for l in sys.stdin if "\"rtt\"" in l]; print(round(s.median(r),2))')
chk record --test L0-forwarded-control --expect 80 --base "$BASE" --measured "$(tcpmed 7000)" --note "forwarded: reply passes prerouting"
chk record --test L1-local-without-output-hook --expect 0 --base "$BASE" --measured "$(tcpmed 8883)" --note "expected: fault missing for gateway-terminated connection"
nsx gw nft -f - <<'EOF'
add chain inet chaosgw classify_out { type filter hook output priority mangle; policy accept; }
add rule inet chaosgw classify_out meta mark set meta mark & 0xfffe00ff
add rule inet chaosgw classify_out ct direction reply meta mark set meta mark | 0x00010000
add rule inet chaosgw classify_out ct original ip saddr vmap @fx_dev
EOF
chk record --test L2-local-with-output-hook --expect 80 --base "$BASE" --measured "$(tcpmed 8883)" --note "output hook classifies local replies by the same key"
chk record --test L3-forwarded-unchanged --expect 80 --base "$BASE" --measured "$(tcpmed 7000)"
tb_destroy
