#!/usr/bin/env bash
# Chaos Gateway spike testbed — network namespace topology.
#
#   cl1 (10.10.0.11) ─┐
#   cl2 (10.10.0.12) ─┼─ sw (bridge) ─ lan0 [gw] wan0 ─ eth0 srv (203.0.113.10, .20)
#                     ┘                10.10.0.1  203.0.113.1
#
# gw forwards and masquerades LAN → WAN. srv has no route back to 10.10.0.0/24,
# so every test also exercises NAT. Source this file; call tb_create / tb_destroy.

TB_PREFIX=${TB_PREFIX:-cg}
TB_CLIENTS=${TB_CLIENTS:-"cl1 cl2"}
# never route testbed traffic through an HTTP proxy from the environment
unset http_proxy https_proxy HTTP_PROXY HTTPS_PROXY ALL_PROXY all_proxy no_proxy NO_PROXY

tb_ns()   { echo "${TB_PREFIX}-$1"; }
nsx()     { local n=$1; shift; ip netns exec "$(tb_ns "$n")" "$@"; }
log()     { printf '[%s] %s\n' "$(date +%H:%M:%S)" "$*"; }

tb_noffload() {  # disable offloads so tc/netem see real packets
  local n=$1 dev=$2
  nsx "$n" ethtool -K "$dev" tso off gso off gro off tx off rx off >/dev/null 2>&1 || true
}

tb_client() {  # tb_client <name> <ip-last-octet>
  local c=$1 o=$2 mac
  mac=$(printf '02:00:00:00:00:%02x' "$o")
  ip netns add "$(tb_ns "$c")"
  nsx "$c" ip link set lo up
  ip link add eth0 netns "$(tb_ns "$c")" address "$mac" type veth peer name "p$o" netns "$(tb_ns sw)"
  nsx sw ip link set "p$o" master br0 up
  nsx "$c" ip addr add "10.10.0.$o/24" dev eth0
  nsx "$c" ip link set eth0 up
  nsx "$c" ip route add default via 10.10.0.1
  tb_noffload "$c" eth0; tb_noffload sw "p$o"
}

tb_create() {
  tb_destroy 2>/dev/null || true
  for n in gw sw srv; do ip netns add "$(tb_ns $n)"; nsx $n ip link set lo up; done

  nsx sw ip link add br0 type bridge
  nsx sw ip link set br0 up

  # gw LAN side
  ip link add lan0 netns "$(tb_ns gw)" address 02:00:00:00:00:01 type veth peer name p1 netns "$(tb_ns sw)"
  nsx sw ip link set p1 master br0 up
  nsx gw ip addr add 10.10.0.1/24 dev lan0
  nsx gw ip link set lan0 up
  tb_noffload gw lan0; tb_noffload sw p1

  # gw WAN side
  ip link add wan0 netns "$(tb_ns gw)" type veth peer name eth0 netns "$(tb_ns srv)"
  nsx gw ip addr add 203.0.113.1/24 dev wan0
  nsx gw ip link set wan0 up
  nsx gw ip route add default via 203.0.113.10
  nsx srv ip addr add 203.0.113.10/24 dev eth0
  nsx srv ip addr add 203.0.113.20/24 dev eth0
  nsx srv ip link set eth0 up
  tb_noffload gw wan0; tb_noffload srv eth0

  nsx gw sysctl -qw net.ipv4.ip_forward=1
  nsx gw nft -f - <<'EOF'
table ip cgbase {
  chain postrouting { type nat hook postrouting priority srcnat; oifname "wan0" masquerade; }
}
EOF
  local o=11
  for c in $TB_CLIENTS; do tb_client "$c" "$o"; o=$((o+1)); done
}

tb_destroy() {
  for n in gw sw srv cl1 cl2 cl3 probe; do
    ip netns pids "$(tb_ns $n)" 2>/dev/null | xargs -r kill 2>/dev/null || true
    ip netns del "$(tb_ns $n)" 2>/dev/null || true
  done
}

# Start the measurement echo server in srv (TCP+UDP on 7000, TCP 8883 "mqtt-like")
tb_start_echo() {
  local dir; dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
  for p in 7000 8883; do
    nsx srv python3 "$dir/tools/echo.py" server --port "$p" >/dev/null 2>&1 &
  done
  sleep 1
}
