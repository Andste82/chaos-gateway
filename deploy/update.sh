#!/bin/sh
# Chaos Gateway update (plan §3.9, M28): pulls the new image and restarts the containers when no run is active.
#
#   ./update.sh                       pull the image the .env names (CHAOSGW_VERSION, default latest) and restart
#   ./update.sh --version 1.2.0       update to that version and write it into .env
#   ./update.sh --check               do everything up to the restart and say what would happen
#
# What it does, in this order:
#   1. pulls the image (--no-pull for an image that is loaded already),
#   2. runs `chaosgw schema-check` of the NEW image against the revisions on this host, before anything is stopped: a
#      configuration written by a newer version is refused with the version that is needed (plan §3.9, downgrade; exit 4),
#      and reads compose.yaml, env.example and update.sh out of the new image: the flags, mounts, capabilities and health checks
#      of the containers belong to the version, so its compose.yaml replaces the installed one (kept as compose.yaml.bak) just
#      before the restart, after it was checked with the .env of this installation (exit 1 when it is not valid),
#   3. waits until GET /api/v1/system/busy reports no active or queued run, up to --timeout seconds (default 600). An update
#      ends active runs as `aborted`, so it does not start while one runs: when the time is up it stops (exit 3) unless --force
#      says to go on,
#   4. restarts all containers together (`docker compose up -d`, then waits for the health checks of exec, api, svcns, kea and bird,
#      and of dns and tls once the gateway is set up: those two cannot be healthy before the first configuration). The executor stops the old way, as on every stop (plan
#      §3.8, D35): it removes everything it made in the kernel, so the devices of the test networks lose the gateway and their
#      DHCP until the new executor has rebuilt the networks from the confirmed revision, a few seconds to a minute. The
#      executor protocol version (plan §3.1) rejects a set of containers of two versions, which is why they all go at once,
#   5. waits for the health checks and asks the API for its health (exit 5 when the gateway does not come up, with the way back).
#
# The busy question needs a token with the scope `read` (made from the admin session with POST /api/v1/auth/tokens, docs/api-guide.md), from --token-file or the variable
# CHAOSGW_TOKEN, and it goes only to an API whose certificate is the one in /var/lib/chaosgw/service/api.pem (CHAOSGW_API_CERT), which
# the API publishes for the other containers. A gateway where nothing listens has no run; one that is not set up yet has none either;
# one that listens and does not answer in time may have one, so the update stops (--force goes on).
#
# Options:
#   --version V        the version to update to (a tag of the image in .env); written into .env when the update succeeded
#   --timeout SECONDS  how long to wait for the runs to end (600)
#   --force            go on when a run is still active after --timeout (the run ends aborted)
#   --no-wait          do not wait for the runs at all (the same as --timeout 0 --force)
#   --no-pull          do not pull the image (it is loaded already, or built locally)
#   --check            stop before the restart
#   --token-file FILE  the file that holds the token
#   --dir DIR          where compose.yaml and .env are (default: the directory of this script)
#   --help             this text
#
# Exit status: 0 updated, 1 error, 2 usage, 3 runs still active, 4 the configuration needs a newer version, 5 not healthy after
# the restart.
set -eu

DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
TIMEOUT=600
FORCE=0
PULL=1
CHECK=0
VERSION=""
TOKEN_FILE=""
STATE_DIR=${CHAOSGW_STATE_DIR:-/var/lib/chaosgw/state}
API_CERT=${CHAOSGW_API_CERT:-/var/lib/chaosgw/service/api.pem}
PORT=${CHAOSGW_UI_PORT:-}
POLL=${CHAOSGW_UPDATE_POLL:-5}
HEALTH_WAIT=${CHAOSGW_UPDATE_HEALTH_WAIT:-180}

say() { echo "update: $*"; }
die() { # status, message
	status=$1
	shift
	echo "update: $*" >&2
	exit "$status"
}

usage() { sed -n '2,/^set -eu/p' "$0" | sed -e '/^set -eu/d' -e 's/^# \{0,1\}//' -e '/^#!/d'; }

while [ $# -gt 0 ]; do
	case "$1" in
	--version)
		[ $# -ge 2 ] || die 2 "--version needs a version"
		case "$2" in
		"" | *[!A-Za-z0-9_.-]*) die 2 "--version: not an image tag: $2" ;;
		esac
		VERSION=$2
		shift 2
		;;
	--timeout)
		[ $# -ge 2 ] || die 2 "--timeout needs a number of seconds"
		case "$2" in
		"" | *[!0-9]*) die 2 "--timeout: not a number of seconds: $2" ;;
		esac
		TIMEOUT=$2
		shift 2
		;;
	--token-file)
		[ $# -ge 2 ] || die 2 "--token-file needs a file"
		TOKEN_FILE=$2
		shift 2
		;;
	--dir)
		[ $# -ge 2 ] || die 2 "--dir needs a directory"
		DIR=$2
		shift 2
		;;
	--force) FORCE=1; shift ;;
	--no-wait) TIMEOUT=0 FORCE=1; shift ;;
	--no-pull) PULL=0; shift ;;
	--check) CHECK=1; shift ;;
	--help | -h) usage; exit 0 ;;
	*) die 2 "unknown option $1" ;;
	esac
done

cd -- "$DIR" || die 1 "cannot enter $DIR"
[ -f compose.yaml ] || die 1 "$DIR has no compose.yaml: run the host setup with --image first"
command -v docker >/dev/null 2>&1 || die 1 "docker is not installed"
command -v curl >/dev/null 2>&1 || die 1 "curl is not installed"
docker compose version >/dev/null 2>&1 || die 1 "docker has no compose plugin"

TOKEN=${CHAOSGW_TOKEN:-}
if [ -n "$TOKEN_FILE" ]; then
	[ -r "$TOKEN_FILE" ] || die 1 "cannot read $TOKEN_FILE"
	TOKEN=$(tr -d ' \t\r\n' <"$TOKEN_FILE")
fi
# the token goes to curl on its standard input (a curl configuration), never in the arguments, which every user of the host can read
# in the process list; a token has the characters of a URL-safe base64 string (cgw_...)
case "$TOKEN" in
*[!A-Za-z0-9._~+/=-]*) die 1 "the token has characters that no token of Chaos Gateway has" ;;
esac

# the temporary files, removed however the script ends. A signal ends the script (the handler of a signal that does not exit
# returns to the command that was interrupted, and the script would go on to restart the containers that the administrator
# cancelled the update for); the EXIT trap then cleans up.
TMP=$(mktemp)
NEWFILES=$(mktemp -d)
trap 'rm -rf "$TMP" "$NEWFILES"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# the port of the web UI and API: the environment's, else the .env's (docker compose reads it from there too), else 8443
if [ -z "$PORT" ] && [ -f .env ]; then
	PORT=$(sed -n 's/^CHAOSGW_UI_PORT=//p' .env | tail -n 1 | tr -d ' \t\r"'"'")
fi
case "${PORT:-8443}" in
*[!0-9]*) die 1 "CHAOSGW_UI_PORT is not a port number: $PORT" ;;
esac
PORT=${PORT:-8443}

# the version of this run: the .env's, or --version (exported, so every docker compose below interpolates it)
if [ -n "$VERSION" ]; then
	CHAOSGW_VERSION=$VERSION
	export CHAOSGW_VERSION
fi
NEW_IMAGE=$(docker compose config --images 2>/dev/null | sort -u | head -n 1)
[ -n "$NEW_IMAGE" ] || die 1 "docker compose config names no image: is $DIR/.env right?"
OLD_IMAGE=""
if oldid=$(docker compose ps -q exec 2>/dev/null) && [ -n "$oldid" ]; then
	OLD_IMAGE=$(docker inspect -f '{{.Config.Image}}' "$oldid" 2>/dev/null || true)
fi
if [ -n "$OLD_IMAGE" ]; then
	say "running: $OLD_IMAGE; target: $NEW_IMAGE"
else
	say "no gateway is running here; target: $NEW_IMAGE"
fi

# ---- 1. the image
if [ "$PULL" = 1 ]; then
	say "pulling $NEW_IMAGE"
	docker pull --quiet "$NEW_IMAGE" >/dev/null || die 1 "cannot pull $NEW_IMAGE (an image that is loaded already: --no-pull)"
fi

# ---- 2. the schema of the configuration, asked of the new image: it is the one that would have to read it
if [ -d "$STATE_DIR" ]; then
	say "checking that $NEW_IMAGE can read the configuration in $STATE_DIR"
	if ! docker run --rm --network none --read-only --cap-drop ALL --user 65532:65532 -v "$STATE_DIR:$STATE_DIR:ro" \
		--entrypoint chaosgw "$NEW_IMAGE" schema-check --state-dir "$STATE_DIR"; then
		die 4 "not updating: $NEW_IMAGE cannot read the configuration of this gateway (above); the gateway keeps running as it is"
	fi
fi

# ---- 2b. the files of the deployment of the new image: the flags, mounts, capabilities and health checks of the containers belong
# to the version, so the compose.yaml of the image goes with it. They are read into a directory of their own and checked with the
# environment of this installation now, and replace the installed ones only just before the restart.
if ! docker run --rm --entrypoint tar "$NEW_IMAGE" -C /usr/local/share/chaosgw -cf - compose.yaml env.example update.sh | tar -xf - -C "$NEWFILES" --no-same-owner 2>/dev/null; then
	die 1 "cannot read compose.yaml out of $NEW_IMAGE (/usr/local/share/chaosgw): is it an image of Chaos Gateway of this release line? The gateway keeps running as it is"
fi
docker compose -f "$NEWFILES/compose.yaml" --project-directory "$DIR" config -q >/dev/null 2>&1 ||
	die 1 "the compose.yaml of $NEW_IMAGE is not valid with $DIR/.env (docker compose -f FILE --project-directory $DIR config shows why); the gateway keeps running as it is"

# ---- 3. no run active

# api GETs a path of the API on the loopback and prints the HTTP status; the body is in $TMP. It prints 000 when nothing listens
# (curl exit 7: no gateway runs, so no run does) and fail for every other failure: a call that timed out, a TLS handshake that stalled
# or a certificate that does not match is an API that may well be up, with a run active.
# The token goes only to the API whose certificate is the one the API published for the other containers (API_CERT): the port is above
# 1024, and a program of any user of the host can listen on it while the gateway is down. Without that file the calls go without the
# token and without verification, and tell nothing a stranger could not read.
api() { # path
	path=$1
	rc=0
	if [ -r "$API_CERT" ]; then
		if [ -n "$TOKEN" ]; then
			code=$(printf 'header = "Authorization: Bearer %s"\n' "$TOKEN" | curl -sS -m 10 --cacert "$API_CERT" -K - -o "$TMP" -w '%{http_code}' "https://127.0.0.1:$PORT/api/v1$path" 2>/dev/null) || rc=$?
		else
			code=$(curl -sS -m 10 --cacert "$API_CERT" -o "$TMP" -w '%{http_code}' "https://127.0.0.1:$PORT/api/v1$path" 2>/dev/null) || rc=$?
		fi
	else
		code=$(curl -ksS -m 10 -o "$TMP" -w '%{http_code}' "https://127.0.0.1:$PORT/api/v1$path" 2>/dev/null) || rc=$?
	fi
	# curl prints 000 through -w itself when it gets no answer and then exits non-zero: the exit status tells why
	case "$code" in
	000) if [ "$rc" = 7 ]; then echo 000; else echo fail; fi ;;
	[0-9][0-9][0-9]) echo "$code" ;;
	*) echo fail ;;
	esac
}

# busy prints no, yes (and says how many), or a reason the answer is unknown
busy() {
	case $(api /system/busy) in
	200)
		if grep -Eq '"busy" *: *true' "$TMP"; then
			echo "yes $(tr -d ' \n' <"$TMP")"
		else
			echo no
		fi
		;;
	000) echo unreachable ;;
	fail) echo noanswer ;;
	401 | 403)
		# without a token (or with one that may not read, or none sent for want of the certificate) the API says nothing about
		# runs; a gateway that is not set up has none
		if [ "$(api /setup)" = 200 ] && grep -Eq '"completed" *: *false' "$TMP"; then echo no; else echo unauthorized; fi
		;;
	*) echo error ;;
	esac
}

start=$(date +%s)
said=""
while :; do
	state=$(busy)
	case "$state" in
	no) say "no run is active"; break ;;
	unreachable) say "nothing listens on 127.0.0.1:$PORT: no run is active"; break ;;
	noanswer)
		[ "$FORCE" = 1 ] && { say "the API did not answer the question for the runs; --force: going on"; break; }
		die 1 "the API listens on 127.0.0.1:$PORT and did not answer in time (or its certificate is not the published one): it may have a run active, so nothing was changed; --force goes on anyway"
		;;
	unauthorized)
		[ "$TIMEOUT" = 0 ] && [ "$FORCE" = 1 ] && { say "not asking whether a run is active (--no-wait)"; break; }
		die 1 "the API refuses to say whether a run is active: pass a token with the scope read (--token-file FILE or CHAOSGW_TOKEN; it is sent only when $API_CERT exists), or --no-wait"
		;;
	yes*)
		[ "$TIMEOUT" = 0 ] && [ "$FORCE" = 1 ] && { say "a run is active; --no-wait: it ends aborted"; break; }
		if [ "$said" != "${state#yes }" ]; then
			said=${state#yes }
			say "waiting for the runs to end: $said"
		fi
		if [ $(($(date +%s) - start)) -ge "$TIMEOUT" ]; then
			[ "$FORCE" = 1 ] && { say "a run is still active after ${TIMEOUT}s; --force: it ends aborted"; break; }
			die 3 "a run is still active after ${TIMEOUT}s; nothing was changed (--force updates anyway and the run ends aborted, --timeout waits longer)"
		fi
		;;
	*)
		[ "$FORCE" = 1 ] && { say "the API answered unexpectedly; --force: going on"; break; }
		die 1 "the API answered unexpectedly when asked for the runs ($state): --force goes on anyway"
		;;
	esac
	sleep "$POLL"
done

if [ "$CHECK" = 1 ]; then
	say "--check: the containers would be restarted with $NEW_IMAGE now; the test networks are rebuilt from the confirmed revision afterwards"
	exit 0
fi

# ---- 4. the files of the new image, then the restart of all containers together
REPLACED=0
if ! cmp -s "$NEWFILES/compose.yaml" compose.yaml; then
	if ! { cp -p compose.yaml compose.yaml.bak && cp "$NEWFILES/compose.yaml" compose.yaml.new && mv -f compose.yaml.new compose.yaml; }; then
		die 1 "cannot replace $DIR/compose.yaml; the gateway keeps running as it is"
	fi
	REPLACED=1
	say "compose.yaml of $NEW_IMAGE replaces the installed one (the old one is kept as compose.yaml.bak)"
fi
if ! cmp -s "$NEWFILES/env.example" env.example; then
	cp "$NEWFILES/env.example" env.example && say "env.example is the one of $NEW_IMAGE: compare it with .env for settings that are new"
fi
# a script that runs is replaced by a rename (the shell keeps reading the old file), and only where the installation has one
if [ -f update.sh ] && ! cmp -s "$NEWFILES/update.sh" update.sh; then
	cp "$NEWFILES/update.sh" update.sh.new && chmod 0755 update.sh.new && mv -f update.sh.new update.sh && say "update.sh is the one of $NEW_IMAGE from the next update on"
fi

say "restarting the containers: the test networks are down until the new executor has rebuilt them"
if [ -n "$OLD_IMAGE" ]; then
	back="set CHAOSGW_IMAGE and CHAOSGW_VERSION in $DIR/.env to those of $OLD_IMAGE"
	[ "$REPLACED" = 0 ] || back="$back, copy compose.yaml.bak to compose.yaml"
	back="$back and run docker compose up -d"
else
	back="docker compose logs shows why"
fi
if ! docker compose up -d --remove-orphans; then
	docker compose ps || true
	die 5 "the containers did not start: $back"
fi
# The containers that have to be healthy: dns and tls report healthy only once the executor has attached the service namespace,
# which takes a configuration, so a gateway that is not set up yet is waited for without them.
if ! docker compose up -d --wait --wait-timeout "$HEALTH_WAIT" exec api svcns kea bird; then
	docker compose ps || true
	die 5 "the containers did not become healthy within ${HEALTH_WAIT}s: $back"
fi
if [ "$(api /setup)" = 200 ] && grep -Eq '"completed" *: *false' "$TMP"; then
	say "the gateway is not set up yet: the DNS proxy and the TLS responder come up with its first configuration"
elif ! docker compose up -d --wait --wait-timeout "$HEALTH_WAIT"; then
	docker compose ps || true
	die 5 "the containers did not become healthy within ${HEALTH_WAIT}s: $back"
fi

# ---- 5. the API's own word
code=$(api /system/health)
if [ "$code" != 200 ]; then
	docker compose ps || true
	die 5 "the API answers the health check with $code"
fi
say "health: $(tr -d '\n' <"$TMP" | cut -c 1-200)"

if [ -n "$VERSION" ]; then
	if [ -f .env ] && grep -q '^CHAOSGW_VERSION=' .env; then
		sed -i "s|^CHAOSGW_VERSION=.*|CHAOSGW_VERSION=$VERSION|" .env
	else
		echo "CHAOSGW_VERSION=$VERSION" >>.env
	fi
	say "wrote CHAOSGW_VERSION=$VERSION to $DIR/.env"
fi
say "updated to $NEW_IMAGE: $(docker compose exec -T api chaosgw version 2>/dev/null || echo 'version unknown')"
