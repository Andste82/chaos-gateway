#!/usr/bin/env bash
# S6 — DHCP server comparison for Chaos Gateway test actions: dnsmasq vs. Kea.
# Client: busybox udhcpc in namespace "dc" (MAC 02:00:00:00:00:31).
# For each server: lease + options, short lease time, reservation change at runtime
# (does the renewing client get a NAK and a new address?), runtime lease deletion,
# silencing one client — all WITHOUT restarting the server.
set -uo pipefail
cd "$(dirname "$0")/.."; . lib/testbed.sh
K=$(uname -r); R=results/s06-$K.jsonl; : > "$R"
T=$(pwd)/results/tmp/s06; rm -rf "$T"; mkdir -p "$T"
MAC=02:00:00:00:00:31

cat > "$T/udhcpc.sh" <<EOF
#!/bin/sh
echo "\$(date +%s.%N) \$1 ip=\$ip lease=\$lease router=\$router dns=\$dns ntp=\$ntpsrv server=\$serverid" >> $T/client.log
case "\$1" in
  bound|renew) ip addr flush dev \$interface; ip addr add \$ip/24 dev \$interface ;;
  deconfig)    ip addr flush dev \$interface ;;
esac
EOF
chmod +x "$T/udhcpc.sh"

client_start() {
  ip netns add "$(tb_ns dc)"; nsx dc ip link set lo up
  ip link add eth0 netns "$(tb_ns dc)" address $MAC type veth peer name p31 netns "$(tb_ns sw)"
  nsx sw ip link set p31 master br0 up; nsx dc ip link set eth0 up
  : > "$T/client.log"
  nsx dc busybox udhcpc -f -i eth0 -s "$T/udhcpc.sh" -O ntpsrv -t 5 -T 3 -A 2 > "$T/udhcpc.out" 2>&1 &
  sleep 4
}
client_stop() { ip netns pids "$(tb_ns dc)" | xargs -r kill 2>/dev/null; ip netns del "$(tb_ns dc)"; nsx sw ip link del p31 2>/dev/null; }
renew() { pkill -USR1 -f "udhcpc -f -i eth0"; sleep "${1:-4}"; }
cur_ip() { grep -oE "bound ip=[0-9.]+|renew ip=[0-9.]+" "$T/client.log" | tail -1 | cut -d= -f2; }

mark() { echo "$(date +%s.%N) $1" >> "$T/marks"; }
step() {  # step <server> <name> → client log lines + DHCP message types since the last mark
  local srv=$1 name=$2 since; since=$(tail -1 "$T/marks" | cut -d' ' -f1)
  python3 - "$srv" "$name" "$since" "$T" <<'PY' | tee -a "$R"
import json, subprocess, sys
srv, name, since, T = sys.argv[1], sys.argv[2], float(sys.argv[3]), sys.argv[4]
cl = [l.split(" ", 1)[1].strip() for l in open(f"{T}/client.log") if float(l.split()[0]) >= since]
names = {"1": "DISCOVER", "2": "OFFER", "3": "REQUEST", "4": "DECLINE", "5": "ACK", "6": "NAK", "7": "RELEASE"}
try:
    out = subprocess.run(["tshark", "-r", f"{T}/{srv}.pcap", "-Y", "dhcp", "-T", "fields",
                          "-e", "frame.time_epoch", "-e", "dhcp.option.dhcp"], capture_output=True, text=True).stdout
    msgs = [names.get(l.split("\t")[1], l) for l in out.splitlines() if l and float(l.split("\t")[0]) >= since]
except Exception as e:
    msgs = [str(e)]
print(json.dumps({"server": srv, "step": name, "client_events": cl, "dhcp_messages": msgs}))
PY
  mark "$name-end"
}

run_server() {  # run_server dnsmasq|kea
  local s=$1
  tb_create
  nsx gw timeout 120 tcpdump -ni lan0 -U -w "$T/$s.pcap" 'udp port 67 or udp port 68' >/dev/null 2>&1 &
  sleep 0.5
  if [ $s = dnsmasq ]; then
    : > "$T/dnsmasq.hosts"
    nsx gw dnsmasq -k --port=0 --interface=lan0 --bind-interfaces --dhcp-authoritative --no-ping \
      --dhcp-range=10.10.0.100,10.10.0.150,10s --dhcp-hostsfile="$T/dnsmasq.hosts" \
      --dhcp-option=3,10.10.0.1 --dhcp-option=6,10.10.0.1 --dhcp-option=42,10.10.0.1 \
      --dhcp-leasefile="$T/dnsmasq.leases" --pid-file="$T/dnsmasq.pid" --log-dhcp --log-facility="$T/dnsmasq.log" &
    reserve() { echo "$MAC,$1" > "$T/dnsmasq.hosts"; nsx gw kill -HUP "$(cat $T/dnsmasq.pid)"; }
    deny()    { echo "$MAC,ignore" > "$T/dnsmasq.hosts"; nsx gw kill -HUP "$(cat $T/dnsmasq.pid)"; }
    undeny()  { : > "$T/dnsmasq.hosts"; nsx gw kill -HUP "$(cat $T/dnsmasq.pid)"; }
    dellease(){ nsx gw dhcp_release lan0 "$1" $MAC; }
    leasedb() { cat "$T/dnsmasq.leases" | tr '\n' ';'; }
  else
    kea_conf() {  # kea_conf <reservation-ip|""> <deny true|false>
      local resv="" cls=""
      [ -n "$1" ] && resv="\"reservations\":[{\"hw-address\":\"$MAC\",\"ip-address\":\"$1\"}],"
      [ "$2" = true ] && cls="\"client-classes\":[{\"name\":\"DROP\",\"test\":\"pkt4.mac == 0x020000000031\"}],"
      cat > "$T/kea.json" <<EOF
{ "Dhcp4": {
  "interfaces-config": { "interfaces": ["lan0"], "dhcp-socket-type": "raw" },
  "control-socket": { "socket-type": "unix", "socket-name": "$T/kea.sock" },
  "lease-database": { "type": "memfile", "persist": true, "name": "$T/kea-leases.csv", "lfc-interval": 0 },
  "hooks-libraries": [ { "library": "/usr/lib/x86_64-linux-gnu/kea/hooks/libdhcp_lease_cmds.so" } ],
  "valid-lifetime": 30, "min-valid-lifetime": 5, "max-valid-lifetime": 3600, "authoritative": true,
  $cls
  "subnet4": [ { "id": 1, "subnet": "10.10.0.0/24", $resv
    "pools": [ { "pool": "10.10.0.100 - 10.10.0.150" } ],
    "option-data": [ { "name": "routers", "data": "10.10.0.1" },
                     { "name": "domain-name-servers", "data": "10.10.0.1" },
                     { "name": "ntp-servers", "data": "10.10.0.1" } ] } ],
  "loggers": [ { "name": "kea-dhcp4", "output_options": [ { "output": "$T/kea.log" } ], "severity": "INFO" } ]
} }
EOF
    }
    kcmd() { echo "$1" | nsx gw socat - UNIX-CONNECT:"$T/kea.sock"; }
    kea_conf "" false
    nsx gw env KEA_PIDFILE_DIR="$T" KEA_LOCKFILE_DIR="$T" kea-dhcp4 -c "$T/kea.json" > "$T/kea.out" 2>&1 &
    reserve() { kea_conf "$1" false; kcmd '{"command":"config-reload"}' > "$T/kea-reload.json"; }
    deny()    { kea_conf "10.10.0.31" true;  kcmd '{"command":"config-reload"}' >/dev/null; }
    undeny()  { kea_conf "10.10.0.31" false; kcmd '{"command":"config-reload"}' >/dev/null; }
    dellease(){ kcmd "{\"command\":\"lease4-del\",\"arguments\":{\"ip-address\":\"$1\"}}" > "$T/kea-del.json"; }
    leasedb() { kcmd '{"command":"lease4-get-all"}' | python3 -c 'import sys,json; r=json.load(sys.stdin); print([ (l["ip-address"], l["valid-lft"]) for l in r.get("arguments",{}).get("leases",[])])' 2>/dev/null; }
  fi
  sleep 2
  : > "$T/marks"; mark start
  client_start
  step $s H1-initial-lease

  local ip db1 db2; ip=$(cur_ip); db1=$(leasedb)
  dellease "$ip"; sleep 1; db2=$(leasedb)
  echo "{\"server\":\"$s\",\"step\":\"H4-runtime-lease-delete\",\"ip\":\"$ip\",\"leases_before\":\"$db1\",\"leases_after\":\"$db2\"}" | tee -a "$R"
  mark H4-end; renew 5
  step $s H4b-renew-after-delete

  reserve 10.10.0.31; renew 8
  step $s H5-reservation-changed-then-renew

  deny; renew 8
  step $s H6-client-silenced-then-renew
  undeny; renew 10
  step $s H6b-unsilenced

  client_stop
  ip netns pids "$(tb_ns gw)" | xargs -r kill 2>/dev/null; sleep 1
  tb_destroy
}

run_server dnsmasq
run_server kea
