#!/bin/sh
# Smoke test of the interception proxy's container (plan §3.8, M22): the image `chaos-gateway:${CHAOSGW_VERSION}` (make image-local, or the build
# of the CI job `tlsproxy-image`) starts from deploy/compose.tlsproxy.yaml, mitmproxy loads the addon, the healthcheck of the compose file passes,
# and the proxy carries a request: a CONNECT with the headers of the TLS responder for the overlay of a case the proxy has received, then a plain
# HTTP request through the tunnel to a server in the container, which the addon sees as it sees every exchange. A CONNECT for an overlay the proxy
# has no case of is refused (P5-AUDIT-04). And the container leaves a namespace that has lost the service address (P5-AUDIT-06): the restart
# policy brings it back, and it carries a request again.
#
# What it does not need: the rest of the gateway. The compose file joins the service namespace of the holder (compose.dns.yaml) and reads the token
# and the test CA from two volumes; here the container has a network of its own (whose address stands in for the service address), two bind
# mounts with a CA and a token made for the test, and a stand-in for the API in the container that serves one interception case, so that nothing
# but Docker is needed (no capability, no namespace, no gateway). Usage:
#
#   CHAOSGW_VERSION=ci deploy/smoke-tlsproxy.sh
set -eu

here=$(cd "$(dirname "$0")" && pwd)
export CHAOSGW_VERSION="${CHAOSGW_VERSION:-latest}"
project=cg-smoke-tlsproxy
work=$(mktemp -d)
compose="docker compose -p $project -f $here/compose.tlsproxy.yaml -f $work/override.yaml"
py=/opt/chaosgw/tls-proxy/venv/bin/python
# the address of the container's network: what the service address is in the holder's namespace
subnet=172.30.254.0/24
addr=172.30.254.2
overlay=5a0e5a0e-0000-4000-8000-000000000001

cleanup() {
	status=$?
	if [ "$status" -ne 0 ]; then
		echo "--- the container's log"
		$compose logs --no-color tls-proxy 2>&1 | tail -n 80 || true
	fi
	$compose down -v >/dev/null 2>&1 || true
	rm -rf "$work"
	exit "$status"
}
trap cleanup EXIT

fail() {
	echo "smoke test failed: $*" >&2
	exit 1
}

# the test CA the API would have made and the token and certificate of the API (nothing here is ever used for anything else); the stand-in
# for the API listens on the loopback of the container
mkdir "$work/tlsca" "$work/service"
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 2 -subj "/CN=Smoke Test CA" \
	-addext "basicConstraints=critical,CA:TRUE" -addext "keyUsage=critical,keyCertSign,cRLSign" \
	-keyout "$work/tlsca/test-ca.key" -out "$work/tlsca/test-ca.pem" 2>/dev/null
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 2 -subj "/CN=api" -addext "subjectAltName=IP:169.254.100.1,IP:127.0.0.1" \
	-keyout "$work/service/api.key" -out "$work/service/api.pem" 2>/dev/null
echo smoke-token >"$work/service/token"

# the stand-in for the API: the configuration with one interception case (the long poll ends with 204 while nothing changes), and every
# report taken
cat >"$work/service/fakeapi.py" <<PY
import json, ssl, time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

CONFIG = {"generation": 1, "cases": [{"case": "intercept", "overlay": "$overlay", "sources": ["10.10.0.5/32"], "expected": "any", "intercept": {}}]}

class API(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path.startswith("/api/v1/internal/tls/config"):
            if "after=0" in self.path:
                body = json.dumps(CONFIG).encode()
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)
                return
            time.sleep(2)
        self.send_response(204)
        self.end_headers()

    def do_POST(self):
        self.rfile.read(int(self.headers.get("Content-Length") or 0))
        self.send_response(204)
        self.end_headers()

srv = ThreadingHTTPServer(("127.0.0.1", 8443), API)
ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
ctx.load_cert_chain("/var/lib/chaosgw/service/api.pem", "/var/lib/chaosgw/service/api.key")
srv.socket = ctx.wrap_socket(srv.socket, server_side=True)
srv.serve_forever()
PY

# a device's connection as the responder hands it over: CONNECT with the identity headers (and the overlay given), then HTTP in the tunnel to
# the server of the container. "carried" waits until the proxy has its case (it may still be waiting for its first configuration) and the
# request reached the server; "refused" expects the proxy to refuse the CONNECT.
cat >"$work/service/connect.py" <<'PY'
import socket, sys, time

want, overlay = sys.argv[1], sys.argv[2]

def read_head(s):
    data = b""
    while b"\r\n\r\n" not in data:
        d = s.recv(4096)
        if not d:
            break
        data += d
    return data

def connect():
    s = socket.create_connection(("127.0.0.1", 9444), 5)
    s.settimeout(20)  # the addon waits up to 5 s for the case of a connection
    s.sendall(b"CONNECT 127.0.0.1:8099 HTTP/1.1\r\nHost: 127.0.0.1:8099\r\nX-Chaosgw-Connection: smoke\r\nX-Chaosgw-Client: 10.10.0.5\r\n"
              + b"X-Chaosgw-Overlay: " + overlay.encode() + b"\r\n\r\n")
    return s, read_head(s)

# the servers of the container and the proxy (after a restart of the container) need a moment to listen
for port in (8099, 8443, 9444):
    for _ in range(120):
        try:
            socket.create_connection(("127.0.0.1", port), 1).close()
            break
        except OSError:
            time.sleep(0.5)

if want == "refused":
    s, head = connect()
    assert head.startswith(b"HTTP/1.1 503"), head
    assert b"the case of the connection is not known to the proxy" in head or b"not known" in s.recv(4096), head
    print("refused:", head.split(b"\r\n")[0].decode())
    sys.exit(0)

deadline = time.time() + 60
while True:
    s, head = connect()
    if head.startswith(b"HTTP/1.1 200"):
        break
    # the proxy has not received its configuration yet (its first fetch may have failed before the API was there; it retries)
    assert head.startswith(b"HTTP/1.1 503") and time.time() < deadline, head
    s.close()
s.sendall(b"GET /smoke-test HTTP/1.1\r\nHost: 127.0.0.1:8099\r\nConnection: close\r\n\r\n")
head = read_head(s)
# http.server answers a path that is no file with its 404 page: the request went through the proxy to it
assert head.split(b"\r\n")[0].endswith(b"404 File not found"), head
print("carried:", head.split(b"\r\n")[0].decode())
PY
# the container runs as 65532 and mounts the directories read-only
chmod 755 "$work/tlsca" "$work/service"
chmod 644 "$work/tlsca/"* "$work/service/"*

cat >"$work/override.yaml" <<YAML
services:
  tls-proxy:
    network_mode: !reset null
    networks:
      smoke:
        ipv4_address: $addr
    depends_on: !reset {}
    command: !override ["--api", "https://127.0.0.1:8443", "--service-token-file", "/var/lib/chaosgw/service/token", "--api-cert-file", "/var/lib/chaosgw/service/api.pem", "--ca-dir", "/var/lib/chaosgw/tlsca", "--listen", "127.0.0.1:9444", "--control", "http://127.0.0.1:9445", "--confdir", "/tmp/mitmproxy"]
    environment:
      CHAOSGW_SERVICE_ADDR: $addr
    volumes: !override
      - type: bind
        source: $work/service
        target: /var/lib/chaosgw/service
        read_only: true
      - type: bind
        source: $work/tlsca
        target: /var/lib/chaosgw/tlsca
        read_only: true
networks:
  smoke:
    ipam:
      config:
        - subnet: $subnet
YAML

$compose config >/dev/null || fail "the compose file with the override does not validate"
$compose up -d tls-proxy
cid=$($compose ps -q tls-proxy)
[ -n "$cid" ] || fail "the container did not start"
net=$(docker inspect --format '{{range $k, $v := .NetworkSettings.Networks}}{{$k}}{{end}}' "$cid")

# the healthcheck of the compose file: the port of the proxy accepts a connection
wait_healthy() {
	health=starting
	i=0
	while [ "$i" -lt 60 ]; do
		health=$(docker inspect --format '{{.State.Health.Status}}' "$cid")
		[ "$health" = healthy ] && return 0
		i=$((i + 1))
		sleep 2
	done
	fail "the container is $health after two minutes"
}
wait_healthy

log=$($compose logs --no-color tls-proxy 2>&1)
echo "$log" | grep -qF "Loading script /opt/chaosgw/tls-proxy/entry.py" || fail "mitmproxy did not load the addon"
echo "$log" | grep -qF "HTTP(S) proxy listening at 127.0.0.1:9444" || fail "mitmproxy does not listen on 127.0.0.1:9444"
if echo "$log" | grep -qE "Traceback|Error logged during startup|Addon error"; then fail "the log shows an error"; fi

# the stand-in for the API and the server of the container (the processes of docker exec end with the container's main process)
helpers() {
	docker exec -d "$cid" "$py" /var/lib/chaosgw/service/fakeapi.py
	docker exec -d "$cid" "$py" -m http.server 8099 --bind 127.0.0.1 --directory /usr/share
}
helpers
docker exec "$cid" "$py" /var/lib/chaosgw/service/connect.py carried "$overlay" || fail "the proxy did not carry the request of its case"
docker exec "$cid" "$py" /var/lib/chaosgw/service/connect.py refused 5a0e5a0e-0000-4000-8000-0000000000ff ||
	fail "the proxy did not refuse a connection of an overlay it has no case of"
$compose logs --no-color tls-proxy 2>&1 | grep -qF "the case of the connection is not known to the proxy" || fail "the proxy's log does not say why it refused"

# the namespace loses the service address, as the old namespace does when the holder is restarted: the launcher exits (three checks two seconds
# apart), the restart policy starts the container again, and once the address is back it stays and carries requests
docker network disconnect "$net" "$cid"
i=0
while [ "$(docker inspect --format '{{.RestartCount}}' "$cid")" -lt 1 ]; do
	i=$((i + 1))
	[ "$i" -lt 30 ] || fail "the container did not leave a namespace without the service address"
	sleep 1
done
$compose logs --no-color tls-proxy 2>&1 | grep -qF "is no longer on this namespace's interfaces" || fail "the launcher does not say why it exited"
i=0
until docker network connect --ip "$addr" "$net" "$cid" 2>/dev/null; do
	i=$((i + 1))
	[ "$i" -lt 30 ] || fail "the container cannot be connected to its network again"
	sleep 1
done
wait_healthy
helpers
docker exec "$cid" "$py" /var/lib/chaosgw/service/connect.py carried "$overlay" || fail "the restarted proxy did not carry the request of its case"
echo "the interception proxy's container is healthy, loads the addon, carries the requests of its case, refuses others and leaves a stale namespace"
