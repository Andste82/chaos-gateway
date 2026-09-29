#!/usr/bin/env bash
# S15 — WireGuard networks and dynamic routing.
#
#   cl1 10.10.0.11 ─┐                                   ┌─ rA 198.51.100.2 ── lab1 192.168.50.10
#   cl2 10.10.0.12 ─┴─ sw ─ lan0 [gw] wan0 ── srv ──────┼─ rB 198.51.101.2 (dummy 192.168.60.1/24, BIRD)
#                              wg-hub 10.99.0.1/24      └─ rC 198.51.102.2 (fresh client from export)
#                              wg-l1  10.255.0.0/31 ── link to rB 10.255.0.1/31
#
# srv is the "internet" router between gw's uplink and the remote sites.
# Hub: rA is a client with client network 192.168.50.0/24; rC is set up from an exported config.
# Link: point to point to rB, AllowedIPs 0.0.0.0/0, routes from BIRD (BGP, then OSPF).
#
# Mark layout under test (D18): bits 4..15 fault id (12 bit), bit 16 direction.
#   id 0x0a  device cl1          up 30 ms / down 60 ms
#   id 0x0b  remote net 50.0/24  up 40 ms / down 0
#   id 0x0c  tunnel towards rA   50 ms  (output hook, keyed on peer endpoint)
#   id 0x0d  tunnel towards rB   loss 100 % (blackout, for convergence)
#   ifb0: tunnel from rA 20 ms / from rB loss 100 %  (ingress, flower on outer UDP)
set -uo pipefail
cd "$(dirname "$0")/.."; . lib/testbed.sh
K=$(uname -r); R=results/s15-$K.jsonl; : > "$R"
T=$PWD/results/tmp/s15; rm -rf "$T"; mkdir -p "$T"
chk() { python3 tools/check.py "$@" --file "$R"; }
out() { echo "$1" | tee -a "$R"; }
udp() { nsx "$1" python3 tools/echo.py udp --host "$2" --port 7000 --count "${3:-200}" --interval 0.01; }
tcpmed() { nsx "$1" python3 tools/echo.py tcp-stream --host "$2" --port 7000 --duration "${3:-4}" --interval 0.02 | \
           python3 -c 'import sys,json,statistics as s; r=[json.loads(l)["ms"] for l in sys.stdin if "\"rtt\"" in l]; print(json.dumps({"median":round(s.median(r),2),"n":len(r)}) if r else json.dumps({"median":-1,"n":0}))'; }
med() { python3 -c 'import sys,json; d=json.load(sys.stdin); print(d["rtt_ms"]["median"] if "rtt_ms" in d else d["median"])'; }
EXTRA="rA rB rC lab1"
cleanup() { for n in $EXTRA; do ip netns pids "$(tb_ns $n)" 2>/dev/null | xargs -r kill 2>/dev/null; ip netns del "$(tb_ns $n)" 2>/dev/null; done; tb_destroy; }
modprobe wireguard 2>/dev/null

site() {  # site <ns> <n>  : remote site on the "internet", 198.51.10<n>.2, via srv
  local s=$1 n=$2
  ip netns add "$(tb_ns $s)"; nsx $s ip link set lo up
  ip link add eth0 netns "$(tb_ns $s)" type veth peer name "i$s" netns "$(tb_ns srv)"
  nsx srv ip addr add "198.51.10$n.1/24" dev "i$s"; nsx srv ip link set "i$s" up
  nsx $s ip addr add "198.51.10$n.2/24" dev eth0; nsx $s ip link set eth0 up
  nsx $s ip route add default via "198.51.10$n.1"
  nsx $s sysctl -qw net.ipv4.ip_forward=1
  tb_noffload $s eth0; tb_noffload srv "i$s"
}

log "S15 on kernel $K"
cleanup 2>/dev/null
tb_create; tb_start_echo
nsx srv sysctl -qw net.ipv4.ip_forward=1
site rA 0; site rB 1; site rC 2
# lab behind rA
ip netns add "$(tb_ns lab1)"; nsx lab1 ip link set lo up
ip link add eth0 netns "$(tb_ns lab1)" type veth peer name lab netns "$(tb_ns rA)"
nsx rA ip addr add 192.168.50.1/24 dev lab; nsx rA ip link set lab up
nsx lab1 ip addr add 192.168.50.10/24 dev eth0; nsx lab1 ip link set eth0 up; nsx lab1 ip route add default via 192.168.50.1
tb_noffload lab1 eth0; tb_noffload rA lab
# rB's site network
nsx rB ip link add dummy0 type dummy; nsx rB ip addr add 192.168.60.1/24 dev dummy0; nsx rB ip link set dummy0 up

# keys
for k in gw gwl rA rB rC; do wg genkey > "$T/$k.key"; wg pubkey < "$T/$k.key" > "$T/$k.pub"; done
pub() { cat "$T/$1.pub"; }

# gw: hub interface
nsx gw ip link add wg-hub type wireguard
nsx gw wg set wg-hub listen-port 51820 private-key "$T/gw.key" \
  peer "$(pub rA)" allowed-ips 10.99.0.2/32,192.168.50.0/24
nsx gw ip addr add 10.99.0.1/24 dev wg-hub; nsx gw ip link set wg-hub mtu 1420 up
# rA: hub client with client network
nsx rA ip link add wg0 type wireguard
nsx rA wg set wg0 listen-port 51820 private-key "$T/rA.key" \
  peer "$(pub gw)" endpoint 203.0.113.1:51820 allowed-ips 10.99.0.0/24,10.10.0.0/24 persistent-keepalive 5
nsx rA ip addr add 10.99.0.2/32 dev wg0; nsx rA ip link set wg0 mtu 1420 up
nsx rA ip route add 10.99.0.0/24 dev wg0; nsx rA ip route add 10.10.0.0/24 dev wg0

# gw: link interface to rB (routes come from BIRD)
nsx gw ip link add wg-l1 type wireguard
nsx gw wg set wg-l1 listen-port 51821 private-key "$T/gwl.key" \
  peer "$(pub rB)" endpoint 198.51.101.2:51821 allowed-ips 0.0.0.0/0 persistent-keepalive 5
nsx gw ip addr add 10.255.0.0/31 dev wg-l1; nsx gw ip link set wg-l1 mtu 1420 up
nsx rB ip link add wg-l1 type wireguard
nsx rB wg set wg-l1 listen-port 51821 private-key "$T/rB.key" \
  peer "$(pub gwl)" endpoint 203.0.113.1:51821 allowed-ips 0.0.0.0/0 persistent-keepalive 5
nsx rB ip addr add 10.255.0.1/31 dev wg-l1; nsx rB ip link set wg-l1 mtu 1420 up

# gw policy routing: table 100 for everything entering from test and WireGuard networks
nsx gw ip route add default via 203.0.113.10 dev wan0 table 100
nsx gw ip route add 10.10.0.0/24 dev lan0 table 100
nsx gw ip route add 10.99.0.0/24 dev wg-hub table 100
nsx gw ip route add 192.168.50.0/24 dev wg-hub table 100          # static: client network of rA
nsx gw ip route add 10.255.0.0/31 dev wg-l1 table 100
for i in lan0 wg-hub wg-l1; do nsx gw ip rule add iif $i lookup 100 priority 1000; done

# echo servers
for n in lab1 cl1; do nsx $n python3 tools/echo.py server --port 7000 >/dev/null 2>&1 & done
nsx rB python3 tools/echo.py server --port 7000 >/dev/null 2>&1 &
nsx lab1 nft -f - <<'EOF'
table inet seen {
  counter from_cl1 { }
  counter from_gw_tunnel { }
  chain c { type filter hook prerouting priority 0;
    ip saddr 10.10.0.11 counter name from_cl1
    ip saddr 10.99.0.1 counter name from_gw_tunnel
  }
}
EOF
sleep 2
nsx rA ping -c 1 -W 2 10.99.0.1 >/dev/null 2>&1   # first handshake

# ---------------------------------------------------------------- hub: connectivity, no NAT
BASE_HUB=$(udp cl2 192.168.50.10 | med); BASE_SRV=$(udp cl2 203.0.113.10 | med)
chk record --test baseline --kind info --measured "{\"cl2_to_lab1_ms\":$BASE_HUB,\"cl2_to_srv_ms\":$BASE_SRV}"
udp cl1 192.168.50.10 50 >/dev/null
seen=$(nsx lab1 nft -j list counter inet seen from_cl1 | python3 -c 'import sys,json; print([x["counter"]["packets"] for x in json.load(sys.stdin)["nftables"] if "counter" in x][0])')
out "{\"test\":\"W1-hub-routed-without-nat\",\"lab1_saw_packets_from_10.10.0.11\":$seen,\"pass_\":$([ "$seen" -gt 0 ] && echo true || echo false)}"

# ---------------------------------------------------------------- classification (12-bit id layout)
nsx gw nft -f - <<'EOF'
table inet chaosgw {
  chain f_a { meta mark set meta mark & 0xffff000f | 0x000000a0 accept; }   # keep the direction bit
  chain f_b { meta mark set meta mark & 0xffff000f | 0x000000b0 accept; }
  chain t_ra { meta mark set meta mark & 0xfffe000f | 0x000000c0 accept; }
  chain t_rb { meta mark set meta mark & 0xfffe000f | 0x000000d0 accept; }
  map fx_src { type ipv4_addr : verdict; flags interval; elements = { 10.10.0.11 : goto f_a, 192.168.50.0/24 : goto f_b } }
  map tun { type ipv4_addr . inet_service : verdict; }
  chain classify { type filter hook prerouting priority mangle; policy accept;
    meta mark set meta mark & 0xfffe000f
    ct direction reply meta mark set meta mark | 0x00010000
    ct original ip saddr vmap @fx_src
  }
  chain classify_out { type filter hook output priority mangle; policy accept;
    meta mark set meta mark & 0xfffe000f
    ct direction reply meta mark set meta mark | 0x00010000
    ct original ip saddr vmap @fx_src
    ip daddr . udp dport vmap @tun
  }
}
EOF
for dev in wan0 lan0 wg-hub wg-l1; do
  nsx gw tc qdisc add dev $dev root handle 1: htb default 1
  nsx gw tc class add dev $dev parent 1: classid 1:1 htb rate 10gbit
  cls() {  # cls <minor> <handle/mask> <netem>
    nsx gw tc class add dev $dev parent 1: classid 1:$1 htb rate 10gbit
    nsx gw tc qdisc add dev $dev parent 1:$1 handle $1: netem limit 10000 $3
    nsx gw tc filter add dev $dev parent 1: protocol all prio 1 handle $2 fw classid 1:$1
  }
  cls a    0x000a0/0x1fff0 "delay 30ms"     # cl1 upload
  cls 100a 0x100a0/0x1fff0 "delay 60ms"     # cl1 download
  cls b    0x000b0/0x1fff0 "delay 40ms"     # remote network upload
  cls c    0x000c0/0x1fff0 "delay 50ms"     # tunnel towards rA
  cls d    0x000d0/0x1fff0 "loss 100%"      # tunnel towards rB (blackout)
done 2>&1 | grep -v quantum

chk record --test W2-device-fault-over-hub --expect 90 --base "$BASE_HUB" --measured "$(udp cl1 192.168.50.10)" --note "cl1 30 up (leaves wg-hub) + 60 down (leaves lan0)"
chk record --test W3-remote-network-as-initiator --expect 40 --base "$BASE_HUB" --measured "$(udp lab1 10.10.0.11)" --note "lab1 in client network 192.168.50.0/24: 40 up (leaves lan0), 0 down (leaves wg-hub)"
chk record --test W4-isolation --expect 0 --base "$BASE_HUB" --measured "$(udp cl2 192.168.50.10)"

# ---------------------------------------------------------------- tunnel faults (underlay)
nsx gw nft add element inet chaosgw tun '{ 198.51.100.2 . 51820 : goto t_ra }'
chk record --test W5-tunnel-fault-towards-peer --expect 50 --base "$BASE_HUB" --measured "$(udp cl2 192.168.50.10)" --note "encrypted UDP to rA marked in output hook"
chk record --test W5-tunnel-fault-isolation --expect 0 --base "$BASE_SRV" --measured "$(udp cl2 203.0.113.10)" --note "same uplink, not the tunnel"
nsx gw ip link add ifb0 type ifb; nsx gw ip link set ifb0 up
nsx gw tc qdisc add dev ifb0 root handle 1: prio
nsx gw tc qdisc add dev ifb0 parent 1:1 handle 10: netem limit 10000 delay 20ms
nsx gw tc qdisc add dev ifb0 parent 1:2 handle 20: netem limit 10000 loss 100%
nsx gw tc qdisc add dev wan0 handle ffff: ingress
nsx gw tc filter add dev wan0 parent ffff: protocol ip prio 1 flower ip_proto udp src_ip 198.51.100.2 src_port 51820 action mirred egress redirect dev ifb0
nsx gw tc filter add dev ifb0 parent 1: protocol ip prio 1 flower src_ip 198.51.100.2 classid 1:1
chk record --test W5b-tunnel-fault-both-directions --expect 70 --base "$BASE_HUB" --measured "$(udp cl2 192.168.50.10)" --note "50 towards rA (output hook) + 20 from rA (ifb ingress on outer UDP)"
chk record --test W5c-inner-plus-tunnel-stack --expect 160 --base "$BASE_HUB" --measured "$(udp cl1 192.168.50.10)" --note "device fault 90 + tunnel 70: independent families stack"
nsx gw nft delete element inet chaosgw tun '{ 198.51.100.2 . 51820 }'
nsx gw tc filter del dev wan0 parent ffff: prio 1

# ---------------------------------------------------------------- export → fresh client
nsx gw wg set wg-hub peer "$(pub rC)" allowed-ips 10.99.0.3/32
cat > "$T/rC.conf" <<EOF
[Interface]
PrivateKey = $(cat "$T/rC.key")
Address = 10.99.0.3/32
DNS = 10.99.0.1

[Peer]
PublicKey = $(pub gw)
Endpoint = 203.0.113.1:51820
AllowedIPs = 10.99.0.0/24, 10.10.0.0/24
PersistentKeepalive = 25
EOF
# what wg-quick does, without wg-quick: strip Address/DNS, setconf, address, routes from AllowedIPs
python3 - "$T/rC.conf" > "$T/rC.wg" <<'PY'
import sys
for l in open(sys.argv[1]):
    if l.split("=")[0].strip() not in ("Address", "DNS", "MTU", "Table"): print(l, end="")
PY
nsx rC ip link add wg0 type wireguard
nsx rC wg setconf wg0 "$T/rC.wg"
nsx rC ip addr add 10.99.0.3/32 dev wg0; nsx rC ip link set wg0 up
for n in 10.99.0.0/24 10.10.0.0/24; do nsx rC ip route add $n dev wg0; done
ok=$(nsx rC python3 tools/echo.py tcp-connect --host 10.10.0.11 --port 7000 --timeout 3 | python3 -c 'import sys,json; print(json.load(sys.stdin)["result"])')
hs=$(nsx gw wg show wg-hub latest-handshakes | awk -v k="$(pub rC)" '$1==k{print $2}')
out "{\"test\":\"W6-exported-config-works\",\"rC_to_cl1\":\"$ok\",\"handshake\":$([ "${hs:-0}" -gt 0 ] && echo true || echo false),\"pass_\":$([ "$ok" = ok ] && echo true || echo false)}"

# ---------------------------------------------------------------- BIRD over the link: BGP
cat > "$T/gw-bgp.conf" <<'EOF'
router id 10.255.0.0;
protocol device { scan time 2; }
protocol static announce { ipv4; route 10.10.0.0/24 unreachable; route 10.99.0.0/24 unreachable; }
filter from_rb {
  if net ~ [ 0.0.0.0/0 ] then reject;                       # never a default route
  if net ~ [ 192.168.56.0/24+, 203.0.113.0/24+, 10.10.0.0/16+ ] then reject;   # protected prefixes
  if net ~ [ 192.168.60.0/22{22,24} ] then accept;           # allowed prefix list for this site
  reject;
}
protocol kernel k100 { kernel table 100; learn off; ipv4 { import none; export where source = RTS_BGP; }; }
protocol bgp rb {
  local 10.255.0.0 as 65001; neighbor 10.255.0.1 as 65002;
  hold time 9; keepalive time 3; connect retry time 2; error wait time 1, 4;
  ipv4 { import filter from_rb; import limit 10 action disable; export where proto = "announce"; };
}
EOF
cat > "$T/rb-bgp.conf" <<'EOF'
router id 10.255.0.1;
protocol device { scan time 2; }
protocol static announce { ipv4;
  route 192.168.60.0/24 via "dummy0"; route 192.168.61.0/24 blackhole;
  route 0.0.0.0/0 blackhole;          # must be filtered by the gateway
  route 192.168.56.0/24 blackhole;    # management prefix, must be filtered
  route 10.10.5.0/24 blackhole;       # inside the gateway's protected 10.10.0.0/16, must be filtered
}
protocol kernel { ipv4 { import none; export where source = RTS_BGP; }; }
protocol bgp gw {
  local 10.255.0.1 as 65002; neighbor 10.255.0.0 as 65001;
  hold time 9; keepalive time 3; connect retry time 2; error wait time 1, 4;
  ipv4 { import all; export where proto = "announce"; };
}
EOF
bird_start() {  # bird_start <ns> <conf>
  nsx "$1" bird -c "$2" -s "$T/$1.ctl" -P "$T/$1.pid"
}
bird_stop() { for n in gw rB; do [ -f "$T/$n.pid" ] && kill "$(cat "$T/$n.pid")" 2>/dev/null; rm -f "$T/$n.pid"; done; sleep 1; }
has_route() { nsx gw ip route show table 100 | grep -q "^$1 .*proto bird"; }
wait_route() {  # wait_route <prefix> present|absent <timeout> → seconds
  local t0 t; t0=$(date +%s.%N)
  while :; do
    if [ "$2" = present ] && has_route "$1"; then break; fi
    if [ "$2" = absent ] && ! has_route "$1"; then break; fi
    t=$(python3 -c "print($(date +%s.%N)-$t0)"); python3 -c "import sys; sys.exit(0 if $t < $3 else 1)" || { echo -1; return; }
    sleep 0.2
  done
  python3 -c "print(round($(date +%s.%N)-$t0,1))"
}
nsx gw bird -p -c "$T/gw-bgp.conf" && nsx rB bird -p -c "$T/rb-bgp.conf" && echo "bird configs parse"
bird_start gw "$T/gw-bgp.conf"; bird_start rB "$T/rb-bgp.conf"
t_up=$(wait_route 192.168.60.0/24 present 40)
t100=$(nsx gw ip -j route show table 100 | python3 -c 'import sys,json; print(json.dumps(sorted(r["dst"]+("/"+r.get("protocol","")) for r in json.load(sys.stdin))))')
main_bird=$(nsx gw ip route show table main proto bird | wc -l)
rb_learned=$(nsx rB ip route show proto bird | awk '{print $1}' | tr '\n' ' ')
out "{\"test\":\"R1-bgp-routes-and-filters\",\"seconds_to_route\":$t_up,\"table100\":$t100,\"bird_routes_in_main\":$main_bird,\"rB_learned\":\"$rb_learned\"}"
p=true
has_route 192.168.60.0/24 || p=false; has_route 192.168.61.0/24 || p=false
has_route default && p=false; has_route 192.168.56.0/24 && p=false; has_route 10.10.5.0/24 && p=false
[ "$main_bird" -eq 0 ] || p=false
nsx gw ip route show table 100 | grep -q "^default via 203.0.113.10" || p=false
out "{\"test\":\"R1-verdict\",\"note\":\"allowed prefixes in table 100 only; default, management and own prefixes filtered; uplink default intact\",\"pass_\":$p}"

BASE_LINK=$(tcpmed cl2 192.168.60.1 | med)
chk record --test R2-device-fault-over-link --expect 90 --base "$BASE_LINK" --measured "$(tcpmed cl1 192.168.60.1)" --note "learned route via wg-l1; cl1 30 up (leaves wg-l1) + 60 down (leaves lan0); TCP because rB answers UDP from 10.255.0.1"

# convergence: blackout of the link tunnel in both directions
nsx gw tc filter add dev wan0 parent ffff: protocol ip prio 2 flower ip_proto udp src_ip 198.51.101.2 src_port 51821 action mirred egress redirect dev ifb0
nsx gw tc filter add dev ifb0 parent 1: protocol ip prio 2 flower src_ip 198.51.101.2 classid 1:2
nsx gw nft add element inet chaosgw tun '{ 198.51.101.2 . 51821 : goto t_rb }'
t_down=$(wait_route 192.168.60.0/24 absent 30)
nsx gw nft delete element inet chaosgw tun '{ 198.51.101.2 . 51821 }'
nsx gw tc filter del dev wan0 parent ffff: prio 2
t_back=$(wait_route 192.168.60.0/24 present 40)
out "{\"test\":\"R3-bgp-convergence-under-tunnel-blackout\",\"hold_time_s\":9,\"route_withdrawn_after_s\":$t_down,\"route_back_after_restore_s\":$t_back,\"pass_\":$(python3 -c "print(str(0 < $t_down <= 13 and 0 < $t_back <= 30).lower())")}"
bird_stop

# ---------------------------------------------------------------- BIRD over the link: OSPF (multicast over WireGuard)
nsx gw ip route flush table 100 proto bird 2>/dev/null
cat > "$T/gw-ospf.conf" <<'EOF'
router id 10.255.0.0;
protocol device { scan time 2; }
filter from_rb {
  if net ~ [ 0.0.0.0/0, 192.168.56.0/24+, 203.0.113.0/24+, 10.10.0.0/16+ ] then reject;
  if net ~ [ 192.168.60.0/22{22,24} ] then accept;
  reject;
}
protocol kernel k100 { kernel table 100; learn off; ipv4 { import none; export where source ~ [ RTS_OSPF, RTS_OSPF_EXT1, RTS_OSPF_EXT2 ]; }; }
protocol ospf v2 o { ipv4 { import filter from_rb; export none; };
  area 0 { interface "wg-l1" { type ptp; hello 2; dead 8; }; };
}
EOF
cat > "$T/rb-ospf.conf" <<'EOF'
router id 10.255.0.1;
protocol device { scan time 2; }
protocol static announce { ipv4; route 192.168.60.0/24 via "dummy0"; route 0.0.0.0/0 blackhole; route 192.168.56.0/24 blackhole; }
protocol kernel { ipv4 { import none; export none; }; }
protocol ospf v2 o { ipv4 { import all; export where proto = "announce"; };
  area 0 { interface "wg-l1" { type ptp; hello 2; dead 8; }; };
}
EOF
nsx gw bird -p -c "$T/gw-ospf.conf" && nsx rB bird -p -c "$T/rb-ospf.conf" && echo "ospf configs parse"
bird_start gw "$T/gw-ospf.conf"; bird_start rB "$T/rb-ospf.conf"
t_ospf=$(wait_route 192.168.60.0/24 present 60)
p=true; [ "$t_ospf" != -1 ] || p=false; has_route default && p=false; has_route 192.168.56.0/24 && p=false
out "{\"test\":\"R4-ospf-ptp-over-wireguard\",\"seconds_to_route\":$t_ospf,\"table100\":$(nsx gw ip -j route show table 100 proto bird | python3 -c 'import sys,json; print(json.dumps([r["dst"] for r in json.load(sys.stdin)]))'),\"pass_\":$p}"
nsx gw birdc -s "$T/gw.ctl" show ospf neighbors > "$T/ospf-neighbors.txt" 2>&1
bird_stop

cleanup
log "done → $R"
