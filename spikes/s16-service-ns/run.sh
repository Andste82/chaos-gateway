#!/usr/bin/env bash
# S16 — upload faults for connections redirected to gateway services (TLS responder, DNS proxy).
#
# At tc ingress a redirected packet still carries the ORIGINAL destination (the real server),
# so an IFB filter on "dst = gateway" misses it. Two designs are compared on the same topology:
#
#   V1  flower: services run in the gateway namespace (redirect to a local port); IFB on the
#       LAN interface with flower filters that repeat each redirect selector; download via
#       the output hook (S14).
#   V2  service namespace: services run in their own namespace behind a veth pair (svc0).
#       Packets selected for a service get a routing mark and are routed unchanged into svc0;
#       the redirect to the local port happens inside the service namespace. Both directions
#       pass a normal egress (svc0 up, lan0 down) with fw marks — no IFB, no output hook.
#
# Fault for cl1 (id 0x0a, 12-bit layout): upload 50 ms, download 30 ms → expected +80 ms.
# "Service" = tools/origdst.py (TCP 9000, records SO_ORIGINAL_DST) + UDP echo on 5353 (DNS stand-in).
# Redirects: TCP 8883 → 9000, UDP 53 → 5353; plus a hostname-style set @redir (203.0.113.20) for TCP 8883.
set -uo pipefail
cd "$(dirname "$0")/.."; . lib/testbed.sh
K=$(uname -r); R=results/s16-$K.jsonl; : > "$R"
T=$PWD/results/tmp/s16; rm -rf "$T"; mkdir -p "$T"
chk() { python3 tools/check.py "$@" --file "$R"; }
out() { echo "$1" | tee -a "$R"; }
udp() { nsx "$1" python3 tools/echo.py udp --host "$2" --port "$3" --count 150 --interval 0.01; }
tcpmed() { nsx "$1" python3 tools/echo.py tcp-stream --host "$2" --port "$3" --duration 3 --interval 0.02 | \
           python3 -c 'import sys,json,statistics as s; r=[json.loads(l)["ms"] for l in sys.stdin if "\"rtt\"" in l]; print(json.dumps({"median":round(s.median(r),2),"n":len(r)}) if r else json.dumps({"median":-1,"n":0}))'; }
med() { python3 -c 'import sys,json; d=json.load(sys.stdin); print(d["rtt_ms"]["median"] if "rtt_ms" in d else d["median"])'; }
cleanup() { ip netns pids "$(tb_ns svc)" 2>/dev/null | xargs -r kill 2>/dev/null; ip netns del "$(tb_ns svc)" 2>/dev/null; tb_destroy; }

classes() {  # classes <dev...>: up 50 / down 30 for id 0x0a
  for dev in "$@"; do
    nsx gw tc qdisc add dev $dev root handle 1: htb default 1
    nsx gw tc class add dev $dev parent 1: classid 1:1 htb rate 10gbit
    nsx gw tc class add dev $dev parent 1: classid 1:a htb rate 10gbit
    nsx gw tc qdisc add dev $dev parent 1:a handle a: netem limit 10000 delay 50ms
    nsx gw tc filter add dev $dev parent 1: protocol all prio 1 handle 0x000a0/0x1fff0 fw classid 1:a
    nsx gw tc class add dev $dev parent 1: classid 1:100a htb rate 10gbit
    nsx gw tc qdisc add dev $dev parent 1:100a handle 100a: netem limit 10000 delay 30ms
    nsx gw tc filter add dev $dev parent 1: protocol all prio 1 handle 0x100a0/0x1fff0 fw classid 1:100a
  done 2>&1 | grep -v quantum
}
CLASSIFY='
  chain f_a { meta mark set meta mark & 0xffff000f | 0x000000a0 accept; }
  map fx_src { type ipv4_addr : verdict; elements = { 10.10.0.11 : goto f_a } }
  chain classify { type filter hook prerouting priority mangle; policy accept;
    meta mark set meta mark & 0xfffe000f
    ct direction reply meta mark set meta mark | 0x00010000
    ct original ip saddr vmap @fx_src
  }'

baseline() {
  B_TCP=$(tcpmed cl2 203.0.113.10 7000 | med); B_UDP=$(udp cl2 203.0.113.10 7000 | med)
}
measure() {  # measure <variant> <note-suffix> <expected ms for e>
  local v=$1
  chk record --test "$v-a-redirected-tcp" --expect 80 --base "$B_TCP" --measured "$(tcpmed cl1 203.0.113.10 8883)" --note "cl1 → server:8883 redirected to the TCP service"
  chk record --test "$v-b-redirected-udp" --expect 80 --base "$B_UDP" --measured "$(udp cl1 203.0.113.10 53)" --note "cl1 → server:53/udp redirected to the UDP service"
  chk record --test "$v-c-forwarded-no-double" --expect 80 --base "$B_TCP" --measured "$(tcpmed cl1 203.0.113.10 7000)" --note "forwarded traffic of the same device: fault applied once"
  chk record --test "$v-d-isolation" --expect 0 --base "$B_TCP" --measured "$(tcpmed cl2 203.0.113.10 8883)" --note "cl2 redirected, no fault"
  chk record --test "$v-e-set-based-redirect" --expect "$3" --base "$B_TCP" --measured "$(tcpmed cl1 203.0.113.20 8883)" --note "redirect selected by a (DNS-derived style) address set: $2"
}

log "S16 on kernel $K"
cleanup 2>/dev/null

# ============================================================ V1: flower on IFB
tb_create; tb_start_echo
nsx gw python3 tools/origdst.py --port 9000 --log "$T/v1-origdst.jsonl" >/dev/null 2>&1 &
nsx gw python3 tools/echo.py server --port 5353 >/dev/null 2>&1 &
sleep 1
baseline
chk record --test baseline --kind info --measured "{\"tcp_ms\":$B_TCP,\"udp_ms\":$B_UDP}"
nsx gw nft -f - <<EOF
table inet chaosgw {
  set redir { type ipv4_addr; elements = { 203.0.113.10, 203.0.113.20 } }
  chain redir_to_service { type nat hook prerouting priority dstnat;
    ip daddr @redir tcp dport 8883 redirect to :9000
    ip daddr 203.0.113.10 udp dport 53 redirect to :5353
  }
$CLASSIFY
  chain classify_out { type filter hook output priority mangle; policy accept;
    meta mark set meta mark & 0xfffe000f
    ct direction reply meta mark set meta mark | 0x00010000
    ct original ip saddr vmap @fx_src
  }
}
EOF
classes wan0 lan0
nsx gw ip link add ifb0 type ifb; nsx gw ip link set ifb0 up
nsx gw tc qdisc add dev ifb0 root netem limit 10000 delay 50ms
nsx gw tc qdisc add dev lan0 handle ffff: ingress
# flower repeats the redirect selectors — only literal addresses, the set cannot be expressed
nsx gw tc filter add dev lan0 parent ffff: protocol ip prio 1 flower src_ip 10.10.0.11 dst_ip 203.0.113.10 ip_proto tcp dst_port 8883 action mirred egress redirect dev ifb0
nsx gw tc filter add dev lan0 parent ffff: protocol ip prio 2 flower src_ip 10.10.0.11 dst_ip 203.0.113.10 ip_proto udp dst_port 53 action mirred egress redirect dev ifb0
measure V1 "flower has no filter for .20 → the upload fault is expected to be MISSING (+30 only)" 30
v1_orig=$(python3 -c "import json; print(json.dumps(sorted({json.loads(l)['original_dst'] for l in open('$T/v1-origdst.jsonl')})))" 2>/dev/null || echo '[]')
out "{\"test\":\"V1-f-original-dst-seen\",\"original_dst\":$v1_orig}"
cleanup

# ============================================================ V2: service namespace
tb_create; tb_start_echo
ip netns add "$(tb_ns svc)"; nsx svc ip link set lo up
ip link add svc0 netns "$(tb_ns gw)" type veth peer name eth0 netns "$(tb_ns svc)"
nsx gw ip addr add 169.254.100.1/30 dev svc0; nsx gw ip link set svc0 up
nsx svc ip addr add 169.254.100.2/30 dev eth0; nsx svc ip link set eth0 up
nsx svc ip route add default via 169.254.100.1
tb_noffload gw svc0; tb_noffload svc eth0
for c in all default svc0 lan0 wan0; do nsx gw sysctl -qw net.ipv4.conf.$c.rp_filter=0; done
nsx svc python3 tools/origdst.py --port 9000 --log "$T/v2-origdst.jsonl" >/dev/null 2>&1 &
nsx svc python3 tools/echo.py server --port 5353 >/dev/null 2>&1 &
# inside the service namespace: the redirect to the local port
nsx svc nft -f - <<'EOF'
table inet svc { chain redir_to_service { type nat hook prerouting priority dstnat;
    tcp dport 8883 redirect to :9000
    udp dport 53 redirect to :5353
  }
}
EOF
# gateway: select service traffic by routing mark (bit 20) and route it unchanged into svc0
nsx gw nft -f - <<EOF
table inet chaosgw {
  set redir { type ipv4_addr; elements = { 203.0.113.10, 203.0.113.20 } }
$CLASSIFY
  chain svc_select { type filter hook prerouting priority mangle + 1; policy accept;
    ct direction original ip daddr @redir tcp dport 8883 meta mark set meta mark | 0x00100000
    ct direction original ip daddr 203.0.113.10 udp dport 53 meta mark set meta mark | 0x00100000
  }
}
EOF
nsx gw ip route add default via 169.254.100.2 dev svc0 table 102
nsx gw ip rule add fwmark 0x00100000/0x00100000 lookup 102 priority 900
classes wan0 lan0 svc0
sleep 1
baseline
chk record --test baseline-v2 --kind info --measured "{\"tcp_ms\":$B_TCP,\"udp_ms\":$B_UDP}"
measure V2 "set lookup in nftables, no tc filter needed" 80
v2_orig=$(python3 -c "import json; print(json.dumps(sorted({json.loads(l)['original_dst'] for l in open('$T/v2-origdst.jsonl')})))" 2>/dev/null || echo '[]')
p=$(python3 -c "import json,sys; o=json.loads(sys.argv[1]); print(str('203.0.113.10:8883' in o and '203.0.113.20:8883' in o).lower())" "$v2_orig")
out "{\"test\":\"V2-f-original-dst-inside-service-ns\",\"original_dst\":$v2_orig,\"pass_\":$p}"
# the service's own upstream connections (TLS proxy → real server) leave through the gateway
up=$(nsx svc python3 tools/echo.py tcp-connect --host 203.0.113.10 --port 7000 --timeout 2 | python3 -c 'import sys,json; print(json.load(sys.stdin)["result"])')
out "{\"test\":\"V2-g-service-upstream-via-gateway\",\"result\":\"$up\",\"pass_\":$([ "$up" = ok ] && echo true || echo false)}"
# no output-hook chain exists in V2
oh=$(nsx gw nft -j list ruleset | python3 -c 'import sys,json; print(sum(1 for x in json.load(sys.stdin)["nftables"] if "chain" in x and x["chain"].get("hook")=="output"))')
out "{\"test\":\"V2-h-no-output-hook-needed\",\"output_hook_chains\":$oh,\"pass_\":$([ "$oh" -eq 0 ] && echo true || echo false)}"
cleanup
log "done → $R"
