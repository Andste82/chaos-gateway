#!/bin/sh
# Smoke test of the deployment (plan §3.8, M28), on a real Docker host: deploy/compose.yaml starts every container of the gateway
# from the image, in the order of the plan, and they become healthy; the setup token is in the log of the API and the setup status
# carries the checks of the executor; only the executor is privileged, the others drop their capabilities and add back only those of
# the table of docs/development.md ("Security hardening (M29)"), and every container runs with no-new-privileges (all read from the
# container runtime, not from the file); and the containers stop and start again from the same directories. It configures no test network
# and assigns no interface, so it can run on a hosted CI runner without touching the runner's own connection (the anti-lockout of plan
# §3.4 is not exercised; the level 2 test is). Nothing is made in the kernel without a configuration, so what the stop removes from the
# kernel (plan §3.8) is not checked here: the recovery test of level 2 (internal/appliance/recovery_test.go, nothingLeft) does.
#
# Needs root (the executor is privileged, the host setup makes /var/lib/chaosgw) and Docker with the compose plugin:
#
#   sudo CHAOSGW_VERSION=ci deploy/smoke-compose.sh
set -eu

here=$(cd "$(dirname "$0")" && pwd)
export CHAOSGW_VERSION="${CHAOSGW_VERSION:-latest}"
export CHAOSGW_IMAGE="${CHAOSGW_IMAGE:-chaos-gateway}"
compose="docker compose -f $here/compose.yaml"
api=https://127.0.0.1:8443/api/v1
# the containers that stay up, in the order of the plan's start order; dns and tls report healthy only once the executor has attached
# the service namespace, which needs a configuration
healthy="exec api svcns kea bird"
running="dns tls"

fail() {
	echo "smoke test failed: $*" >&2
	exit 1
}

[ "$(id -u)" -eq 0 ] || fail "run it as root"

cleanup() {
	status=$?
	if [ "$status" -ne 0 ]; then
		echo "--- docker compose ps"
		$compose ps -a 2>&1 || true
		for s in $healthy $running; do
			echo "--- the log of $s"
			$compose logs --no-color --tail 40 "$s" 2>&1 || true
		done
	fi
	$compose down -v --remove-orphans >/dev/null 2>&1 || true
	exit "$status"
}
trap cleanup EXIT

# state returns the runtime state of a service: "running healthy", "running", "exited" ...
state() { # service
	id=$($compose ps -a -q "$1" 2>/dev/null | head -n 1)
	[ -n "$id" ] || { echo "none"; return; }
	docker inspect -f '{{.State.Status}} {{if .State.Health}}{{.State.Health.Status}}{{end}}' "$id" | sed 's/ $//'
}

wait_for() { # seconds, service, wanted state
	deadline=$(($(date +%s) + $1))
	while [ "$(state "$2")" != "$3" ]; do
		[ "$(date +%s)" -lt "$deadline" ] || fail "$2 is \"$(state "$2")\" after $1 s, want \"$3\""
		sleep 2
	done
}

# ---- the host setup makes the directories the compose file mounts, and compose.yaml renders
sh "$here/host-setup.sh" --data-only >/dev/null
$compose config -q || fail "compose.yaml does not render"
COMPOSE_PROFILES=interception $compose config -q || fail "compose.yaml does not render with the interception proxy"
$compose --profile teardown config -q || fail "compose.yaml does not render with the teardown service"
for d in state secrets api service tlsca captures exec kea; do
	[ -d "/var/lib/chaosgw/$d" ] || fail "the host setup did not make /var/lib/chaosgw/$d"
done

# ---- up, in the order of plan §3.8
$compose up -d
for s in $healthy; do wait_for 240 "$s" "running healthy"; done
for s in $running; do
	deadline=$(($(date +%s) + 60))
	until [ "$(state "$s" | cut -d' ' -f1)" = running ]; do
		[ "$(date +%s)" -lt "$deadline" ] || fail "$s is \"$(state "$s")\", want it running"
		sleep 2
	done
done
echo "ok   every container of the gateway is up: $healthy $running"

# the executor was healthy before the others were created: it started first
exec_started=$(docker inspect -f '{{.State.StartedAt}}' "$($compose ps -a -q exec)")
for s in api svcns kea bird; do
	other=$(docker inspect -f '{{.State.StartedAt}}' "$($compose ps -a -q "$s")")
	[ "$(printf '%s\n%s\n' "$exec_started" "$other" | sort | head -n 1)" = "$exec_started" ] || fail "$s started ($other) before the executor ($exec_started)"
done
echo "ok   the executor started first"

# ---- privileges, from the container runtime
# caps prints a list of capabilities from docker inspect ("[NET_RAW CHOWN]") as sorted names without the CAP_ prefix, on one line
caps() {
	echo "$1" | tr -d '[]' | tr ' ' '\n' | sed -e 's/^CAP_//' -e '/^$/d' -e '/[<>]/d' | sort | tr '\n' ' ' | sed 's/ $//'
}
# the capabilities a container adds back after it dropped ALL (the table of "Security hardening (M29)" in docs/development.md)
want_caps() { # service
	case "$1" in
	kea) echo "CHOWN DAC_OVERRIDE FOWNER NET_BIND_SERVICE NET_RAW" ;;
	bird) echo "NET_ADMIN NET_BIND_SERVICE NET_RAW" ;;
	dns) echo "NET_BIND_SERVICE" ;;
	*) echo "" ;;
	esac
}
for s in $healthy $running; do
	id=$($compose ps -a -q "$s")
	privileged=$(docker inspect -f '{{.HostConfig.Privileged}}' "$id")
	# every container, the privileged executor included, runs with no-new-privileges (P7-M29-02)
	secopt=$(docker inspect -f '{{.HostConfig.SecurityOpt}}' "$id")
	case "$secopt" in
	*no-new-privileges:false*) fail "$s sets no-new-privileges to false: $secopt" ;;
	*no-new-privileges*) ;;
	*) fail "$s does not run with no-new-privileges (SecurityOpt $secopt)" ;;
	esac
	if [ "$s" = exec ]; then
		[ "$privileged" = true ] || fail "the executor is not privileged"
		echo "ok   exec: privileged, no-new-privileges"
		continue
	fi
	[ "$privileged" = false ] || fail "$s is privileged"
	readonly_root=$(docker inspect -f '{{.HostConfig.ReadonlyRootfs}}' "$id")
	[ "$readonly_root" = true ] || fail "$s has a writable root file system"
	drop=$(docker inspect -f '{{.HostConfig.CapDrop}}' "$id")
	case "$drop" in *ALL*) ;; *) fail "$s drops $drop, not ALL" ;; esac
	add=$(caps "$(docker inspect -f '{{.HostConfig.CapAdd}}' "$id")")
	[ "$add" = "$(want_caps "$s")" ] || fail "$s adds \"$add\", want \"$(want_caps "$s")\" (docs/development.md, Security hardening (M29))"
	echo "ok   $s: not privileged, read-only root, no-new-privileges, drops ALL, adds \"$add\""
done

# ---- the setup token is in the log of the API, and the setup status carries the executor's checks
token=""
for _ in $(seq 1 30); do
	token=$($compose logs --no-color api 2>&1 | sed -n 's/.*Setup token: \([A-Za-z0-9_-]*\).*/\1/p' | head -n 1)
	[ -n "$token" ] && break
	sleep 2
done
[ -n "$token" ] || fail "the log of the API holds no setup token"
echo "ok   the setup token is in the log of the API"
status=$(curl -ksS -m 20 "$api/setup") || fail "GET /setup does not answer"
echo "$status" | grep -q '"completed" *: *false' || fail "GET /setup: $status"
for check in host.tools host.netns host.proc_sys; do
	echo "$status" | grep -q "\"$check\"" || fail "the preflight of the setup status has no check $check: $status"
done
# the executor must not report a failed check on a host that was prepared
if echo "$status" | grep -q '"status" *: *"fail"'; then
	echo "$status" | tr '{' '\n' | grep '"status" *: *"fail"' || true
	fail "the preflight reports a failed check"
fi
echo "ok   the setup status carries the checks of the executor"

# ---- the data is in the directories of the host
[ "$(stat -c %u:%g /var/lib/chaosgw/secrets)" = "65532:65532" ] || fail "the secrets directory changed its owner"
[ -e /var/lib/chaosgw/service/token ] || fail "the service token is not in /var/lib/chaosgw/service"
[ -e /var/lib/chaosgw/tlsca/test-ca.pem ] || fail "the test CA is not in /var/lib/chaosgw/tlsca"
echo "ok   the API wrote its data below /var/lib/chaosgw"

# ---- the stop, and the same containers start again
$compose stop
for s in $healthy $running; do
	[ "$(state "$s" | cut -d' ' -f1)" = exited ] || fail "$s is \"$(state "$s")\" after the stop, want it exited"
done
echo "ok   every container stopped"
$compose up -d
for s in $healthy; do wait_for 240 "$s" "running healthy"; done
echo "ok   the containers start again from the same directories"
echo "deployment smoke test passed"
