#!/usr/bin/env bash
# S3 — What happens to an ESTABLISHED TCP connection (behind NAT) when access rules change?
# Each case: start a persistent connection cl1 → srv:8883 (one message / 100 ms),
# apply the change after 2 s, observe the stream, then try a NEW connection.
set -uo pipefail
cd "$(dirname "$0")/.."; . lib/testbed.sh
K=$(uname -r); R=results/s03-$K.jsonl; : > "$R"
T=results/tmp; mkdir -p "$T"

fw() {  # load a forward chain body
  nsx gw nft delete table inet chaosgw 2>/dev/null
  nsx gw nft -f - <<EOF
table inet chaosgw {
  chain forward {
    type filter hook forward priority filter; policy accept;
    $1
  }
}
EOF
}

summarize() {  # summarize <case> <stream> <t_apply> <newconn json> <extra json>
  python3 - "$@" <<'PY'
import json, sys
case, stream, t_apply, newconn, extra = sys.argv[1], sys.argv[2], float(sys.argv[3]), sys.argv[4], sys.argv[5]
rows = [json.loads(l) for l in open(stream) if l.strip()]
before = [r for r in rows if r["event"] == "rtt" and r["t"] < t_apply]
after = [r for r in rows if r["t"] >= t_apply]
ev_after = [r["event"] for r in after if r["event"] != "rtt"]
first = next((r for r in after if r["event"] != "rtt"), None)
out = {"case": case, "msgs_before": len(before),
       "msgs_ok_after": sum(1 for r in after if r["event"] == "rtt"),
       "events_after": sorted(set(ev_after)),
       "first_problem_after_ms": round((first["t"] - t_apply) * 1000) if first else None,
       "new_connection": json.loads(newconn), **json.loads(extra)}
print(json.dumps(out))
PY
}

run_case() {  # run_case <name> <apply-cmd...>
  local name=$1; shift
  local st="$T/s03-$name.jsonl"
  nsx cl1 python3 tools/echo.py tcp-stream --port 8883 --duration 7 --interval 0.1 --out "$st" &
  local sp=$!
  sleep 2; local ta; ta=$(date +%s.%N)
  "$@"
  wait $sp
  local nc; nc=$(nsx cl1 python3 tools/echo.py tcp-connect --port 8883 --timeout 2)
  local srv_est; srv_est=$(nsx srv ss -Htn state established '( sport = :8883 )' | wc -l)
  summarize "$name" "$st" "$ta" "$nc" "{\"server_side_established_after\":$srv_est}" | tee -a "$R"
  # clean slate for the next case
  fw ""; nsx gw conntrack -F >/dev/null 2>&1
  nsx srv ss -K state established '( sport = :8883 )' >/dev/null 2>&1
  sleep 0.5
}

tb_create; tb_start_echo
fw ""
nsx gw sysctl -n net.netfilter.nf_conntrack_tcp_loose > "$T/s03-loose.txt"

run_case C0-no-change true
run_case C1-drop-all-packets fw 'ip saddr 10.10.0.11 tcp dport 8883 drop'
run_case C2-est-accept-then-drop-new fw 'ct state established,related accept
    ip saddr 10.10.0.11 tcp dport 8883 drop'
run_case C3-reject-tcp-reset-all fw 'ip saddr 10.10.0.11 tcp dport 8883 reject with tcp reset'
run_case C4-C2-plus-conntrack-delete sh -c "$(declare -f fw nsx tb_ns); TB_PREFIX=$TB_PREFIX; fw 'ct state established,related accept
    ip saddr 10.10.0.11 tcp dport 8883 ct state new drop'; nsx gw conntrack -D -p tcp --orig-src 10.10.0.11 --orig-port-dst 8883 >/dev/null 2>&1"
run_case C5-conntrack-delete-only nsx gw conntrack -D -p tcp --orig-src 10.10.0.11 --orig-port-dst 8883
run_case C6-one-shot-cut sh -c "$(declare -f fw nsx tb_ns); TB_PREFIX=$TB_PREFIX; fw 'ip saddr 10.10.0.11 tcp dport 8883 ct state established reject with tcp reset'; sleep 0.5; fw ''"

echo "{\"case\":\"sysctl\",\"nf_conntrack_tcp_loose\":$(cat $T/s03-loose.txt)}" | tee -a "$R"
tb_destroy
