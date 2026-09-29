#!/usr/bin/env bash
# S12 — how a test network is attached, and which route gateway traffic takes.
#
# A) Probe reachability. The test network's gateway address lives either
#    (1) directly on the physical port eth1, probe attached via macvlan on eth1, or
#    (2) on a bridge br-iot with eth1 and the probe's veth as bridge ports.
#    The probe must reach the gateway address, a real device and the server via NAT.
# B) tc on the bridge: marks set in inet prerouting are visible to an HTB class on
#    br-iot egress (download direction) — counted without netem.
# C) Default routes: the OS-owned management interface has the main-table default
#    route; forwarded test traffic and the gateway's own proxy traffic (by uid)
#    must still leave via the uplink, through a policy-routing table.
#
#   dev (10.10.0.11) ─ sw ─ eth1 [ gw ] wan0 ─ srv 203.0.113.10
#                                  └ mgmt0 ─ mg (192.168.56.254, management router)
set -uo pipefail
cd "$(dirname "$0")/.."; . lib/testbed.sh
K=$(uname -r); R=results/s12-$K.jsonl; : > "$R"
out() { echo "$1" | tee -a "$R"; }
ok()  { if "$@" >/dev/null 2>&1; then echo true; else echo false; fi; }
P=${TB_PREFIX:-cg}

base_topology() {  # gw, sw (switch), dev, srv, mg
  tb_destroy 2>/dev/null
  for n in gw sw srv mg; do ip netns add "$(tb_ns $n)"; nsx $n ip link set lo up; done
  nsx sw ip link add br0 type bridge; nsx sw ip link set br0 up
  ip link add eth1 netns "$(tb_ns gw)" type veth peer name p1 netns "$(tb_ns sw)"; nsx sw ip link set p1 master br0 up
  ip netns add "$(tb_ns cl1)"; nsx cl1 ip link set lo up
  ip link add eth0 netns "$(tb_ns cl1)" type veth peer name p11 netns "$(tb_ns sw)"; nsx sw ip link set p11 master br0 up
  nsx cl1 ip addr add 10.10.0.11/24 dev eth0; nsx cl1 ip link set eth0 up; nsx cl1 ip route add default via 10.10.0.1
  ip link add wan0 netns "$(tb_ns gw)" type veth peer name eth0 netns "$(tb_ns srv)"
  nsx gw ip addr add 203.0.113.1/24 dev wan0; nsx gw ip link set wan0 up
  nsx srv ip addr add 203.0.113.10/24 dev eth0; nsx srv ip link set eth0 up
  nsx srv ip addr add 198.51.100.10/32 dev lo     # "internet" host behind the uplink router (part C)
  nsx srv sysctl -qw net.ipv4.ip_forward=1
  nsx srv ip route add 10.10.0.0/24 via 203.0.113.1 2>/dev/null   # replies for un-NATed probes (none expected)
  nsx gw sysctl -qw net.ipv4.ip_forward=1
  nsx gw nft -f - <<'EOF'
table ip cgbase { chain post { type nat hook postrouting priority srcnat; oifname "wan0" masquerade; }
}
EOF
  nsx srv python3 tools/echo.py server --port 7000 >/dev/null 2>&1 &
  sleep 0.5
}
probe_ns() { ip netns add "$(tb_ns probe)"; nsx probe ip link set lo up; }
probe_checks() {  # probe_checks <label>
  local g d s
  g=$(ok nsx probe ping -c 2 -W 1 10.10.0.1)
  d=$(ok nsx probe ping -c 2 -W 1 10.10.0.11)
  s=$(ok nsx probe ping -c 2 -W 1 203.0.113.10)
  out "{\"test\":\"A-$1\",\"probe_to_gateway\":$g,\"probe_to_device\":$d,\"probe_to_server_via_nat\":$s}"
}

# A1 — address on eth1, probe via macvlan on eth1
base_topology
nsx gw ip addr add 10.10.0.1/24 dev eth1; nsx gw ip link set eth1 up
probe_ns
nsx gw ip link add pr0 link eth1 type macvlan mode bridge
nsx gw ip link set pr0 netns "$(tb_ns probe)"
nsx probe ip addr add 10.10.0.250/24 dev pr0; nsx probe ip link set pr0 up; nsx probe ip route add default via 10.10.0.1
sleep 1
probe_checks macvlan-on-physical-port

# A2 — address on bridge br-iot {eth1, probe veth}
base_topology
nsx gw ip link add br-iot type bridge; nsx gw ip link set br-iot up
nsx gw ip link set eth1 master br-iot; nsx gw ip link set eth1 up
nsx gw ip addr add 10.10.0.1/24 dev br-iot
probe_ns
ip link add pv0 netns "$(tb_ns gw)" type veth peer name eth0 netns "$(tb_ns probe)"
nsx gw ip link set pv0 master br-iot; nsx gw ip link set pv0 up
nsx probe ip addr add 10.10.0.250/24 dev eth0; nsx probe ip link set eth0 up; nsx probe ip route add default via 10.10.0.1
sleep 1
probe_checks bridge-with-probe-port
dev_ok=$(ok nsx cl1 ping -c 2 -W 1 203.0.113.10)
out "{\"test\":\"A-bridge-device-to-server\",\"ok\":$dev_ok}"

# B — mark set in inet prerouting is seen by tc on br-iot egress (download of dev)
nsx gw nft -f - <<'EOF'
table inet chaosgw { chain classify { type filter hook prerouting priority mangle; policy accept;
  meta mark set meta mark & 0xfffe00ff
  ct direction reply meta mark set meta mark | 0x00010000
  ct original ip saddr 10.10.0.11 meta mark set meta mark & 0xffff00ff | 0x00000a00; }
}
EOF
nsx gw tc qdisc add dev br-iot root handle 1: htb default 1
nsx gw tc class add dev br-iot parent 1: classid 1:1 htb rate 10gbit
nsx gw tc class add dev br-iot parent 1: classid 1:a htb rate 10gbit
nsx gw tc filter add dev br-iot parent 1: protocol all prio 1 handle 0x10a00/0x1ff00 fw classid 1:a
nsx cl1 ping -c 20 -i 0.05 -q 203.0.113.10 >/dev/null
nsx probe ping -c 20 -i 0.05 -q 203.0.113.10 >/dev/null
pk=$(nsx gw tc -s class show dev br-iot | python3 -c '
import sys, re, json
d = {}; cur = None
for l in sys.stdin:
    m = re.match(r"class htb (\S+)", l)
    if m: cur = m.group(1)
    m = re.search(r"Sent \d+ bytes (\d+) pkt", l)
    if m and cur: d[cur] = int(m.group(1)); cur = None
print(json.dumps(d))')
out "{\"test\":\"B-bridge-egress-class-counters\",\"per_class_packets\":$pk,\"note\":\"1:a should hold ~20 replies to dev, 1:1 the probe replies\"}"

# C — management default route vs uplink
nsx gw ip route del default 2>/dev/null
ip link add mgmt0 netns "$(tb_ns gw)" type veth peer name eth0 netns "$(tb_ns mg)"
nsx gw ip addr add 192.168.56.10/24 dev mgmt0; nsx gw ip link set mgmt0 up
nsx mg ip addr add 192.168.56.254/24 dev eth0; nsx mg ip link set eth0 up
nsx gw ip route add default via 192.168.56.254 dev mgmt0          # OS-owned management default route
nsx mg nft -f - <<'EOF'
table inet count { counter leaked {}
  chain c { type filter hook prerouting priority 0; policy drop; ip daddr 198.51.100.10 counter name leaked; }
}
EOF
leak() { nsx mg nft -j list counter inet count leaked | python3 -c 'import sys,json; print([x["counter"]["packets"] for x in json.load(sys.stdin)["nftables"] if "counter" in x][0])'; }
# TCP towards 198.51.100.10: only reachable via a default route (not a connected subnet)
tcpc() { nsx "$@" python3 tools/echo.py tcp-connect --host 198.51.100.10 --port 7000 --timeout 1 | python3 -c 'import sys,json; print(json.dumps(json.load(sys.stdin)["result"]))'; }
fwd_before=$(tcpc cl1)
leak1=$(leak)
# policy routing: test-network ingress and the chaosgw service uid use table 100 (uplink default)
nsx gw ip route add default via 203.0.113.10 dev wan0 table 100
nsx gw ip route add 10.10.0.0/24 dev br-iot table 100
nsx gw ip rule add iif br-iot lookup 100 priority 1000
nsx gw ip rule add uidrange 65534-65534 lookup 100 priority 1001
fwd_after=$(tcpc cl1)
leak2=$(leak)
uid_local=$(tcpc gw setpriv --reuid=65534 --regid=65534 --clear-groups)
leak3=$(leak)
root_local=$(tcpc gw)
leak4=$(leak)
out "{\"test\":\"C-default-route\",\"forwarded_without_policy\":$fwd_before,\"leaked_without_policy\":$leak1,\"forwarded_with_policy\":$fwd_after,\"leaked_after_policy\":$((leak2-leak1)),\"service_uid_socket\":$uid_local,\"leaked_by_service_uid\":$((leak3-leak2)),\"root_socket\":$root_local,\"leaked_by_root_socket\":$((leak4-leak3)),\"note\":\"root has no rule: management route by design\"}"
tb_destroy
