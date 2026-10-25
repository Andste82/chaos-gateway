#!/bin/sh
# Smoke test of the interception proxy's container (plan §3.8, M22): the image `chaos-gateway:${CHAOSGW_VERSION}` (make image-local, or the build
# of the CI job `tlsproxy-image`) starts from deploy/compose.tlsproxy.yaml, mitmproxy loads the addon, the healthcheck of the compose file passes,
# and the proxy carries a request: a CONNECT with the headers of the TLS responder, then a plain HTTP request through the tunnel to a server in the
# container, which the addon sees as it sees every exchange.
#
# What it does not need: the rest of the gateway. The compose file joins the service namespace of the holder (compose.dns.yaml) and reads the token
# and the test CA from two volumes; here the container has a network of its own and two bind mounts with a CA and a token made for the test, so that
# nothing but Docker is needed (no capability, no namespace, no API: the proxy then logs that it cannot fetch its configuration, which it does until
# the API is there). Usage:
#
#   CHAOSGW_VERSION=ci deploy/smoke-tlsproxy.sh
set -eu

here=$(cd "$(dirname "$0")" && pwd)
export CHAOSGW_VERSION="${CHAOSGW_VERSION:-latest}"
project=cg-smoke-tlsproxy
work=$(mktemp -d)
compose="docker compose -p $project -f $here/compose.tlsproxy.yaml -f $work/override.yaml"
py=/opt/chaosgw/tls-proxy/venv/bin/python

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

# the test CA the API would have made and the token and certificate of the API (nothing here is ever used for anything else)
mkdir "$work/tlsca" "$work/service"
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 2 -subj "/CN=Smoke Test CA" \
	-addext "basicConstraints=critical,CA:TRUE" -addext "keyUsage=critical,keyCertSign,cRLSign" \
	-keyout "$work/tlsca/test-ca.key" -out "$work/tlsca/test-ca.pem" 2>/dev/null
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 2 -subj "/CN=api" -addext "subjectAltName=IP:169.254.100.1" \
	-keyout "$work/service/api.key" -out "$work/service/api.pem" 2>/dev/null
echo smoke-token >"$work/service/token"
# the container runs as 65532 and mounts the directories read-only
chmod 755 "$work/tlsca" "$work/service"
chmod 644 "$work/tlsca/"* "$work/service/"*

cat >"$work/override.yaml" <<YAML
services:
  tls-proxy:
    network_mode: !reset null
    depends_on: !reset {}
    volumes: !override
      - type: bind
        source: $work/service
        target: /var/lib/chaosgw/service
        read_only: true
      - type: bind
        source: $work/tlsca
        target: /var/lib/chaosgw/tlsca
        read_only: true
YAML

$compose config >/dev/null || fail "the compose file with the override does not validate"
$compose up -d tls-proxy
cid=$($compose ps -q tls-proxy)
[ -n "$cid" ] || fail "the container did not start"

# the healthcheck of the compose file: the port of the proxy accepts a connection
health=starting
i=0
while [ "$i" -lt 60 ]; do
	health=$(docker inspect --format '{{.State.Health.Status}}' "$cid")
	[ "$health" = healthy ] && break
	[ "$(docker inspect --format '{{.State.Running}}' "$cid")" = true ] || fail "the container has stopped"
	i=$((i + 1))
	sleep 2
done
[ "$health" = healthy ] || fail "the container is $health after two minutes"

log=$($compose logs --no-color tls-proxy 2>&1)
echo "$log" | grep -qF "Loading script /opt/chaosgw/tls-proxy/entry.py" || fail "mitmproxy did not load the addon"
echo "$log" | grep -qF "HTTP(S) proxy listening at 127.0.0.1:9444" || fail "mitmproxy does not listen on 127.0.0.1:9444"
if echo "$log" | grep -qE "Traceback|Error logged during startup|Addon error"; then fail "the log shows an error"; fi

# a request through the proxy, as the responder sends one: CONNECT with the identity headers, then HTTP in the tunnel to a server of the container
docker exec -d "$cid" "$py" -m http.server 8099 --bind 127.0.0.1 --directory /usr/share
docker exec -i "$cid" "$py" - <<'PY' || fail "the proxy did not carry the request"
import socket, time

def read_head(s):
    data = b""
    while b"\r\n\r\n" not in data:
        d = s.recv(4096)
        if not d:
            break
        data += d
    return data

# the server of the container needs a moment to listen
for _ in range(20):
    try:
        socket.create_connection(("127.0.0.1", 8099), 1).close()
        break
    except OSError:
        time.sleep(0.5)
s = socket.create_connection(("127.0.0.1", 9444), 5)
s.settimeout(40)  # the addon waits up to 5 s for its first configuration, which an absent API does not give
s.sendall(b"CONNECT 127.0.0.1:8099 HTTP/1.1\r\nHost: 127.0.0.1:8099\r\nX-Chaosgw-Connection: smoke\r\nX-Chaosgw-Client: 10.10.0.5\r\n\r\n")
head = read_head(s)
assert head.startswith(b"HTTP/1.1 200"), head
s.sendall(b"GET /smoke-test HTTP/1.1\r\nHost: 127.0.0.1:8099\r\nConnection: close\r\n\r\n")
head = read_head(s)
# http.server answers a path that is no file with its 404 page: the request went through the proxy to it
assert head.split(b"\r\n")[0].endswith(b"404 File not found"), head
print("carried:", head.split(b"\r\n")[0].decode())
PY
echo "the interception proxy's container is healthy, loads the addon and carries a request"
