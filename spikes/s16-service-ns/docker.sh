#!/usr/bin/env bash
# S16 part C — the service namespace with the Docker deployment (§3.8). Runs on the host
# kernel (no netem needed) with a running Docker daemon and the cg-bb image (README):
#   TB_PREFIX=h bash s16-service-ns/docker.sh
#
# The service namespace is the network namespace of a tiny "holder" container (cg-svcns,
# --network none, only `sleep`). The executor attaches a veth pair to it by PID. The service
# (here origdst.py as TLS-responder stand-in) runs in its own container that JOINS that
# namespace (--network container:cg-svcns), unprivileged and without capabilities.
set -uo pipefail
cd "$(dirname "$0")/.."; SP=$PWD; . lib/testbed.sh
K=$(uname -r); R=results/s16c-$K.jsonl; : > "$R"
T=$PWD/results/tmp/s16c; rm -rf "$T"; mkdir -p "$T"; chmod 777 "$T"
out() { echo "$1" | tee -a "$R"; }
conn() { nsx cl1 python3 tools/echo.py tcp-connect --host "$1" --port 8883 --timeout 2 | python3 -c 'import sys,json; print(json.load(sys.stdin)["result"])'; }
seen() { grep -c "\"original_dst\": \"$1\"" "$T/origdst.jsonl" 2>/dev/null || echo 0; }
MNT="-v /usr:/usr:ro -v /lib:/lib:ro -v /lib64:/lib64:ro -v $SP/tools:/tools:ro -v $T:/out"
cleanup() { docker rm -f cg-svc cg-svcns >/dev/null 2>&1; tb_destroy; }
cleanup

tb_create; tb_start_echo
nsx gw nft -f - <<'EOF'
table inet chaosgw { chain svc_select { type filter hook prerouting priority mangle + 1; policy accept;
    ct direction original tcp dport 8883 meta mark set meta mark | 0x00100000
  }
}
EOF
nsx gw ip route add prohibit default metric 4000 table 102   # fail closed if the service namespace is gone
nsx gw ip rule add fwmark 0x00100000/0x00100000 lookup 102 priority 900
for c in all default; do nsx gw sysctl -qw net.ipv4.conf.$c.rp_filter=0; done

attach() {  # executor operation: veth into the holder container's namespace + redirect rule inside
  local pid; pid=$(docker inspect -f '{{.State.Pid}}' cg-svcns)
  nsx gw ip link del svc0 2>/dev/null
  ip -n "$(tb_ns gw)" link add svc0 type veth peer name eth0 netns "$pid"
  nsx gw ip addr add 169.254.100.1/30 dev svc0; nsx gw ip link set svc0 up
  nsx gw sysctl -qw net.ipv4.conf.svc0.rp_filter=0
  nsx gw ip route replace default via 169.254.100.2 dev svc0 metric 0 table 102
  nsenter -t "$pid" -n ip addr add 169.254.100.2/30 dev eth0
  nsenter -t "$pid" -n ip link set eth0 up
  nsenter -t "$pid" -n ip route add default via 169.254.100.1
  nsenter -t "$pid" -n nft -f - <<'EOF'
table inet svc {
  chain redir_to_service { type nat hook prerouting priority dstnat; tcp dport 8883 redirect to :9000; }
}
EOF
  echo "$pid"
}
start_service() {
  docker rm -f cg-svc >/dev/null 2>&1
  docker run -d --name cg-svc --network container:cg-svcns --user 65534:65534 --cap-drop ALL \
    --security-opt no-new-privileges --read-only $MNT cg-bb:latest \
    /usr/bin/python3.11 /tools/origdst.py --port 9000 --log /out/origdst.jsonl >/dev/null
  sleep 2
}

docker run -d --name cg-svcns --network none cg-bb:latest sleep 1000000 >/dev/null
pid1=$(attach); start_service
r1=$(conn 203.0.113.10); r2=$(conn 203.0.113.20)
caps=$(docker inspect -f '{{.HostConfig.CapDrop}} user={{.Config.User}} privileged={{.HostConfig.Privileged}} readonly={{.HostConfig.ReadonlyRootfs}}' cg-svc)
out "{\"test\":\"C1-service-container-in-holder-namespace\",\"to_.10\":\"$r1\",\"to_.20\":\"$r2\",\"orig_.10\":$(seen 203.0.113.10:8883),\"orig_.20\":$(seen 203.0.113.20:8883),\"service_container\":\"$caps\",\"pass_\":$([ "$r1" = ok ] && [ "$(seen 203.0.113.10:8883)" -gt 0 ] && echo true || echo false)}"

# C2: the service container crashes/restarts — the namespace is held by cg-svcns and survives
docker restart cg-svc >/dev/null; sleep 2
r=$(conn 203.0.113.10); link=$(nsx gw ip -br link show svc0 2>/dev/null | awk '{print $2}')
out "{\"test\":\"C2-service-restart-keeps-namespace\",\"svc0\":\"${link:-missing}\",\"connect\":\"$r\",\"pass_\":$([ "$r" = ok ] && echo true || echo false)}"

# C3: the holder restarts — new namespace, the veth is gone, the joined service stays in the old one
docker restart cg-svcns >/dev/null; sleep 2
pid2=$(docker inspect -f '{{.State.Pid}}' cg-svcns)
link=$(nsx gw ip -br link show svc0 2>/dev/null | awk '{print $2}')
r_before=$(conn 203.0.113.10)   # the service still holds the OLD namespace, so svc0 is still up
attach >/dev/null
r_reattach=$(conn 203.0.113.10)
start_service
r_restart=$(conn 203.0.113.10)
out "{\"test\":\"C3-holder-restart\",\"pid_changed\":$([ "$pid1" != "$pid2" ] && echo true || echo false),\"svc0_after_restart\":\"${link:-missing}\",\"connect_before_reattach\":\"$r_before\",\"after_reattach_only\":\"$r_reattach\",\"after_reattach_and_service_restart\":\"$r_restart\",\"note\":\"a joined service keeps the old namespace alive; after re-attaching to the new one the service must be restarted too\"}"

# C4: all containers gone → namespace and veth gone → selected traffic must fail closed
docker rm -f cg-svc cg-svcns >/dev/null; sleep 1
link=$(nsx gw ip -br link show svc0 2>/dev/null | awk '{print $2}')
r_closed=$(conn 203.0.113.10)
nsx gw ip route del prohibit default metric 4000 table 102
r_open=$(conn 203.0.113.10)
out "{\"test\":\"C4-fail-closed\",\"svc0\":\"${link:-missing}\",\"with_prohibit_fallback\":\"$r_closed\",\"without_fallback\":\"$r_open\",\"pass_\":$([ "$r_closed" != ok ] && echo true || echo false),\"note\":\"without the fallback the redirected traffic silently reaches the real server\"}"
cleanup
log "done → $R"
