#!/usr/bin/env bash
# S5 — DNS proxy: per-client DNS faults, hostname selectors filled into nftables sets,
# redirect of hardcoded resolvers, throughput.
set -uo pipefail
cd "$(dirname "$0")/.."; . lib/testbed.sh
K=$(uname -r); R=results/s05-$K.jsonl; : > "$R"
T=$(pwd)/results/tmp; mkdir -p "$T"
NODE=$(command -v node || echo /opt/node22/bin/node)
CFG=$T/s05-cfg.json; EV=$T/s05-events.jsonl; : > "$EV"

tb_create; tb_start_echo
# upstream resolver ("the internet"): broker is a CNAME onto a CDN edge shared with another site
nsx srv dnsmasq -k --no-resolv --no-hosts --listen-address=203.0.113.10 --bind-interfaces \
  --host-record=edge.cdn.example.net,203.0.113.20 --host-record=shop.example.org,203.0.113.20 \
  --cname=broker.example.com,edge.cdn.example.net --local-ttl=60 --pid-file=$T/s05-dnsmasq.pid >/dev/null 2>&1 &
nsx gw nft -f - <<'EOF'
table inet chaosgw {
  set hs_1 { type ipv4_addr . ipv4_addr; flags timeout; }
  counter c_hs1 {}
  chain sel_forward { type filter hook forward priority filter; policy accept;
    ip saddr . ip daddr @hs_1 counter name c_hs1
  }
  chain dns_redirect { type nat hook prerouting priority dstnat;
    iifname "lan0" ip daddr != 10.10.0.1 udp dport 53 dnat ip to 10.10.0.1
    iifname "lan0" ip daddr != 10.10.0.1 tcp dport 53 dnat ip to 10.10.0.1
  }
  chain dot_block { type filter hook forward priority filter - 1; policy accept;
    iifname "lan0" tcp dport 853 reject with tcp reset
  }
}
EOF
start_proxy() {
  nsx gw pkill -f "[d]nsproxy.mjs" 2>/dev/null; sleep 0.3
  nsx gw env "$@" $NODE s05-dns/dnsproxy.mjs 10.10.0.1 203.0.113.10 "$CFG" "$EV" >/dev/null 2>&1 &
  sleep 1
}
cfg() { echo "$1" > "$CFG"; }
q() {  # q <ns> <server> <name> → json
  local out; out=$(nsx "$1" dig @"$2" "$3" A +tries=1 +time=2 +noall +answer +comments +stats 2>&1)
  python3 - "$out" <<'PY'
import json, re, sys
o = sys.argv[1]
st = re.search(r"status: (\w+)", o); fl = re.search(r"flags: ([a-z ]+);", o); qt = re.search(r"Query time: (\d+)", o)
ans = [l.split()[-1] for l in o.splitlines() if l and not l.startswith(";") and len(l.split()) >= 5]
ttl = [int(l.split()[1]) for l in o.splitlines() if l and not l.startswith(";") and len(l.split()) >= 5]
print(json.dumps({"status": st.group(1) if st else ("timeout" if "timed out" in o or "no servers" in o else "?"),
                  "flags": fl.group(1).strip() if fl else None, "answers": ans, "ttl": ttl[:1],
                  "query_ms": int(qt.group(1)) if qt else None}))
PY
}
counter() { nsx gw nft -j list counter inet chaosgw c_hs1 | python3 -c 'import sys,json; print([x["counter"]["packets"] for x in json.load(sys.stdin)["nftables"] if "counter" in x][0])'; }

cfg '{}'; start_proxy
echo "{\"test\":\"D1-normal\",\"cl1\":$(q cl1 10.10.0.1 broker.example.com)}" | tee -a "$R"

for m in nxdomain servfail timeout delay:800 wrong:203.0.113.99 truncate ttl:1; do
  cfg "{\"faults\":{\"10.10.0.11\":{\"mode\":\"$m\",\"names\":[\"broker.example.com\"]}}}"
  echo "{\"test\":\"D2-fault-$m\",\"cl1\":$(q cl1 10.10.0.1 broker.example.com),\"cl1_other_name\":$(q cl1 10.10.0.1 shop.example.org),\"cl2\":$(q cl2 10.10.0.1 broker.example.com)}" | tee -a "$R"
done
# truncation normally makes the client retry over TCP — does the proxy handle DNS over TCP?
cfg '{}'
tcpq=$(nsx cl1 dig @10.10.0.1 broker.example.com +tcp +tries=1 +time=2 +short 2>&1 | tr '\n' ' ')
echo "{\"test\":\"D2-dns-over-tcp\",\"result\":\"$tcpq\"}" | tee -a "$R"

# D3 — hostname selector (per device): set is filled before the answer is sent
cfg '{"selectors":{"broker.example.com":"hs_1"},"perDevice":true}'
c0=$(counter)
nsx cl1 dig @10.10.0.1 broker.example.com +short >/dev/null
nsx cl1 python3 tools/echo.py tcp-connect --host 203.0.113.20 --port 7000 --timeout 2 >/dev/null
c1=$(counter)
nsx cl2 python3 tools/echo.py tcp-connect --host 203.0.113.20 --port 7000 --timeout 2 >/dev/null
c2=$(counter)
nsx cl1 python3 tools/echo.py tcp-connect --host 203.0.113.20 --port 7000 --timeout 2 >/dev/null  # "shop.example.org", same IP
c3=$(counter)
nft_ms=$(grep selector_update "$EV" | head -1 | python3 -c 'import sys,json; print(json.load(sys.stdin)["nft_ms"])')
elems=$(nsx gw nft -j list set inet chaosgw hs_1 | python3 -c 'import sys,json; s=[x["set"] for x in json.load(sys.stdin)["nftables"] if "set" in x][0]; print(json.dumps([e["elem"]["val"]["concat"] if isinstance(e,dict) and "elem" in e else e for e in s.get("elem",[])]))')
echo "{\"test\":\"D3-selector-per-device\",\"packets_cl1_first_connect\":$((c1-c0)),\"packets_cl2_same_ip_not_resolved\":$((c2-c1)),\"packets_cl1_other_site_same_ip\":$((c3-c2)),\"nft_add_ms\":$nft_ms,\"set_elements\":$elems}" | tee -a "$R"

# D4 — hardcoded resolver and DoT
echo "{\"test\":\"D4-hardcoded-8.8.8.8\",\"cl1\":$(q cl1 8.8.8.8 broker.example.com),\"proxy_saw_query\":$(grep -c '"client":"10.10.0.11"' "$EV")}" | tee -a "$R"
dot=$(nsx cl1 python3 tools/echo.py tcp-connect --host 8.8.8.8 --port 853 --timeout 2)
echo "{\"test\":\"D4-dot-blocked\",\"cl1\":$dot}" | tee -a "$R"

# D5 — throughput (cl2, no faults): upstream direct, proxy, proxy + selector (dedupe), proxy + selector (nft per answer)
ld() { nsx cl2 $NODE s05-dns/dnsload.mjs "$1" broker.example.com "${2:-3000}" 50; }
d_up=$(ld 203.0.113.10)
cfg '{}'; d_px=$(ld 10.10.0.1)
cfg '{"selectors":{"broker.example.com":"hs_1"},"perDevice":true}'; d_sel=$(ld 10.10.0.1)
start_proxy DNS_NODEDUPE=1; d_nod=$(ld 10.10.0.1 1000)
echo "{\"test\":\"D5-throughput\",\"upstream_direct\":$d_up,\"proxy\":$d_px,\"proxy_selector_dedupe\":$d_sel,\"proxy_selector_nft_exec_per_answer\":$d_nod}" | tee -a "$R"
tb_destroy
