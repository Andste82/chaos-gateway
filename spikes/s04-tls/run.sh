#!/usr/bin/env bash
# S4 — TLS: transparent redirect to a Node TLS responder (certificate/handshake cases)
# and mitmproxy transparent interception (inspection + manipulation).
#
# Client profiles:  prod     = trusts only public-ca (like production firmware)
#                   devfw    = trusts public-ca + Chaos Gateway test-ca (dev firmware)
#                   insecure = does not verify certificates (a buggy device)
set -uo pipefail
cd "$(dirname "$0")/.."; . lib/testbed.sh
K=$(uname -r); R=results/s04-$K.jsonl; : > "$R"
T=$(pwd)/results/tmp; C=$T/certs; mkdir -p "$T"
EV=$T/s04-responder.jsonl; : > "$EV"; MODES=$T/s04-modes.json
NODE=$(command -v node || echo /opt/node22/bin/node)

python3 s04-tls/mkcerts.py "$C" >/dev/null
tb_create
nsx srv $NODE s04-tls/tls-lab.mjs real 8883 "$C" >/dev/null 2>&1 &
nsx srv $NODE s04-tls/tls-lab.mjs https 443 "$C" >/dev/null 2>&1 &
echo '{}' > "$MODES"
nsx gw $NODE s04-tls/tls-lab.mjs responder 9443 "$C" "$MODES" "$EV" >/dev/null 2>&1 &
nsx gw nft -f - <<'EOF'
table ip chaostls {
  chain pre { type nat hook prerouting priority dstnat;
    ip saddr 10.10.0.11 tcp dport 8883 redirect to :9443
    ip saddr 10.10.0.12 tcp dport 443 redirect to :8080
  }
}
EOF
sleep 1.5

client() {  # client <ns> <profile> → json {exit, verify_error}
  local ns=$1 prof=$2 args=()
  case $prof in
    prod)     args=(-CAfile "$C/public-ca.crt" -verify_return_error -verify_hostname broker.example.com) ;;
    devfw)    args=(-CAfile "$C/devfw-bundle.crt" -verify_return_error -verify_hostname broker.example.com) ;;
    insecure) args=() ;;
  esac
  local out rc
  out=$(nsx "$ns" timeout 6 openssl s_client -connect 203.0.113.10:8883 -servername broker.example.com \
        "${args[@]}" -brief </dev/null 2>&1); rc=$?
  local err; err=$(echo "$out" | grep -oiE 'verify error:[^,]*|Verification error: .*|certificate verify failed|unexpected eof|connection reset|errno=[0-9]+' | head -1)
  printf '{"exit":%s,"error":"%s"}' "$rc" "${err//\"/}"
}

# Reference: cl2 is not redirected and talks to the real server
echo "{\"case\":\"reference-real-server\",\"prod\":$(client cl2 prod)}" | tee -a "$R"

for mode in untrusted expired notyet wronghost selfsigned abort close stall; do
  echo "{\"10.10.0.11\":\"$mode\"}" > "$MODES"
  line="{\"case\":\"responder-$mode\""
  for prof in prod devfw insecure; do
    n0=$(wc -l < "$EV")
    res=$(client cl1 $prof)
    sleep 0.3
    evt=$(tail -n +$((n0 + 1)) "$EV" | python3 -c 'import sys,json; e=[json.loads(l) for l in sys.stdin]; print(json.dumps([x["event"] for x in e if x.get("event")!="listening"]))')
    line+=",\"$prof\":{\"client\":$res,\"responder\":$evt}"
  done
  echo "$line}" | tee -a "$R"
done
sni=$(grep -m1 '"sni"' "$EV" | python3 -c 'import sys,json; print(json.load(sys.stdin)["sni"])')
echo "{\"case\":\"sni-extracted-from-clienthello\",\"sni\":\"$sni\"}" | tee -a "$R"

# --- mitmproxy transparent interception (only if installed) ---
MITM=${MITM:-/opt/mitm-venv/bin/mitmdump}
if [ -x "$MITM" ]; then
  cat > "$T/s04-addon.py" <<'PY'
from mitmproxy import http
def response(flow: http.HTTPFlow):
    if flow.request.path == "/modify":
        flow.response.text = "MODIFIED BY CHAOS GATEWAY\n"
    if flow.request.path == "/fail":
        flow.response = http.Response.make(503, b"injected 503\n")
PY
  mkdir -p "$T/mitmconf"
  nsx gw env MITMPROXY_SSLKEYLOGFILE="$T/s04-keylog.txt" "$MITM" --mode transparent --listen-port 8080 \
      --set confdir="$T/mitmconf" --set ssl_verify_upstream_trusted_ca="$C/public-ca.crt" \
      -s "$T/s04-addon.py" > "$T/s04-mitm.log" 2>&1 &
  sleep 6
  MCA=$T/mitmconf/mitmproxy-ca-cert.pem
  cu() { nsx cl2 curl -s -m 8 --resolve broker.example.com:443:203.0.113.10 "$@" 2>&1 | head -c 200; echo " rc=$?"; }
  m1=$(cu --cacert "$C/public-ca.crt" https://broker.example.com/)
  m2=$(cu --cacert "$MCA" https://broker.example.com/)
  m3=$(cu --cacert "$MCA" https://broker.example.com/modify)
  m4=$(cu --cacert "$MCA" -w '%{http_code}' -o /dev/null https://broker.example.com/fail)
  kl=$(wc -l < "$T/s04-keylog.txt" 2>/dev/null || echo 0)
  python3 - "$m1" "$m2" "$m3" "$m4" "$kl" <<'PY' | tee -a "$R"
import json, sys
print(json.dumps({"case": "mitmproxy-transparent", "prod_client": sys.argv[1], "client_trusting_mitm_ca": sys.argv[2],
                  "modified_response": sys.argv[3], "injected_status": sys.argv[4], "keylog_lines": int(sys.argv[5])}))
PY
else
  echo '{"case":"mitmproxy-transparent","skipped":"mitmdump not installed"}' | tee -a "$R"
fi
tb_destroy
