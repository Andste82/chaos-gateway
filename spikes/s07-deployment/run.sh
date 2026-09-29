#!/usr/bin/env bash
# S7 — Deployment constraints: Docker on the same host, and running the gateway in a container.
# Needs a running Docker daemon and the minimal image cg-bb (busybox) — see README.
# A) Docker's FORWARD policy vs. routing through the HOST network namespace (native install
#    on a host that also runs Docker): which allow-rule actually lets test traffic pass?
# B) Container with --network host + NET_ADMIN/NET_RAW: which gateway operations work?
set -uo pipefail
cd "$(dirname "$0")/.."
K=$(uname -r); R=results/s07-$K.jsonl; : > "$R"
unset http_proxy https_proxy HTTP_PROXY HTTPS_PROXY

# --- A: client ns ── root ns (router) ── server ns
cleanup_a() { ip netns del s7c 2>/dev/null; ip netns del s7s 2>/dev/null; ip link del s7c0 2>/dev/null; ip link del s7s0 2>/dev/null
              nft delete table inet s7allow 2>/dev/null; iptables -D DOCKER-USER -i s7c0 -j ACCEPT 2>/dev/null
              iptables -D DOCKER-USER -i s7s0 -j ACCEPT 2>/dev/null; }
cleanup_a
ip netns add s7c; ip netns add s7s
ip link add s7c0 type veth peer name eth0 netns s7c; ip link add s7s0 type veth peer name eth0 netns s7s
ip addr add 10.77.1.1/24 dev s7c0; ip addr add 10.77.2.1/24 dev s7s0; ip link set s7c0 up; ip link set s7s0 up
ip -n s7c addr add 10.77.1.2/24 dev eth0; ip -n s7c link set eth0 up; ip -n s7c route add default via 10.77.1.1
ip -n s7s addr add 10.77.2.2/24 dev eth0; ip -n s7s link set eth0 up; ip -n s7s route add default via 10.77.2.1
ping_ok() { ip netns exec s7c ping -c 2 -W 1 -q 10.77.2.2 >/dev/null && echo true || echo false; }
policy=$(iptables -S FORWARD | head -1)
a0=$(ping_ok)
nft -f - <<'EOF'
table inet s7allow {
  chain forward {
    type filter hook forward priority filter - 10; policy accept;
    iifname { "s7c0", "s7s0" } accept
  }
}
EOF
a1=$(ping_ok)
iptables -I DOCKER-USER -i s7c0 -j ACCEPT; iptables -I DOCKER-USER -i s7s0 -j ACCEPT
a2=$(ping_ok)
echo "{\"test\":\"A-docker-forward-policy\",\"forward_policy\":\"$policy\",\"routed_ping_plain\":$a0,\"with_own_nft_accept\":$a1,\"with_DOCKER-USER_accept\":$a2}" | tee -a "$R"
cleanup_a

# --- B: gateway operations from a host-network container
ip link add s7b0 type veth peer name s7b1 2>/dev/null; ip link set s7b0 up
probe() {  # probe <label> <docker args...>
  local label=$1; shift
  docker run --rm --network host -v /usr:/usr:ro -v /lib:/lib:ro -v /lib64:/lib64:ro "$@" cg-bb:latest sh -c '
    r() { if eval "$2" >/dev/null 2>&1; then printf "\"%s\":true," "$1"; else printf "\"%s\":false," "$1"; fi; }
    r nft_table   "/usr/sbin/nft add table inet s7ctr && /usr/sbin/nft delete table inet s7ctr"
    r tc_qdisc    "/usr/sbin/tc qdisc add dev s7b0 root handle 1: htb && /usr/sbin/tc qdisc del dev s7b0 root"
    r ip_link     "/usr/bin/ip link add s7x0 type veth peer name s7x1 && /usr/bin/ip link del s7x0"
    r ip_addr     "/usr/bin/ip addr add 10.99.99.1/32 dev s7b0 && /usr/bin/ip addr del 10.99.99.1/32 dev s7b0"
    r sysctl_forward "echo 1 > /proc/sys/net/ipv4/ip_forward"
    r sysctl_rp_filter "echo 0 > /proc/sys/net/ipv4/conf/s7b0/rp_filter"
    r netns_create "/bin/busybox mkdir -p /run && /usr/bin/ip netns add s7ns && /usr/bin/ip netns del s7ns"
    r raw_socket  "/usr/bin/ping -c1 -W1 127.0.0.1"
    r bind_port_67 "/usr/bin/python3.12 -c import\ socket\;socket.socket\(2,2\).bind\(\(str\(\),67\)\)"
    echo "\"end\":true"
  ' | sed "s/^/{\"test\":\"B-$label\",/; s/\$/}/" | tee -a "$R"
}
probe caps-net_admin-net_raw --cap-add NET_ADMIN --cap-add NET_RAW
probe caps-plus-sys_admin    --cap-add NET_ADMIN --cap-add NET_RAW --cap-add SYS_ADMIN
probe privileged             --privileged
ip link del s7b0 2>/dev/null
