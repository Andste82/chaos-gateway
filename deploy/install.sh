#!/bin/sh
# Chaos Gateway installation (plan §3.8, M28/M30): one command from an Ubuntu host to a running gateway.
#
#   sudo sh install.sh --test-interface lan0
#   sudo sh install.sh --version 1.0.0 --test-interface lan0 --test-interface lan1
#   curl -fsSL https://github.com/Andste82/chaos-gateway/releases/latest/download/install.sh | sudo sh -s -- --test-interface lan0
#
# What it does, in this order (and it can be run again: every step checks what is there):
#   1. installs Docker when it is missing, with the host setup of the release (curl is needed for that download),
#   2. pulls the image (--no-pull for an image that is loaded already),
#   3. runs the host setup out of that image, so script and gateway are of one version: kernel modules and IPv4 forwarding, the data
#      directories below /var/lib/chaosgw, the files of the deployment in /opt/chaosgw (compose.yaml, .env, install.sh, update.sh,
#      uninstall.sh, host-setup.sh), and for each --test-interface the rule that keeps systemd-networkd off it and the check of it.
#      It stops here (exit 1) when a test interface has a netplan entry or a NetworkManager connection: delete it as the host setup
#      says, then run install.sh again,
#   4. starts the containers (`docker compose up -d`) and waits for the health checks of exec, api, svcns, kea and bird (dns and tls
#      cannot be healthy before the first configuration),
#   5. says where the setup wizard is and how to read its one-time token.
#
# The test interfaces are the ports of the test networks. They belong to the gateway alone: they must not be the uplink or the
# management interface (the host setup refuses an interface that has the default route, an IPv4 address or your SSH session).
# `ip link` lists the names. Without --test-interface the gateway starts with no port for a test network; add one later with
# `sudo sh /opt/chaosgw/host-setup.sh --test-interface IF`.
#
# Options:
#   --test-interface IF  an interface for a test network (repeatable)
#   --version V          the release to install (default: latest, the newest release that is not a pre-release)
#   --image REF          a full image reference instead of ghcr.io/andste82/chaos-gateway:VERSION (a local build, a mirror)
#   --dir DIR            where the deployment lives (default /opt/chaosgw)
#   --no-pull            do not pull the image (it is loaded already, or built locally)
#   --no-start           prepare the host and the files, but do not start the containers
#   --help               this text
#
# Exit status: 0 installed, 1 error (the host setup found a step missing, Docker or the pull failed), 2 usage, 5 the containers did
# not become healthy.
set -eu

REGISTRY_IMAGE=ghcr.io/andste82/chaos-gateway
RELEASES=${CHAOSGW_RELEASES:-https://github.com/Andste82/chaos-gateway/releases}
DIR=/opt/chaosgw
VERSION=latest
IMAGE=""
PULL=1
START=1
TEST_IFS=""
HEALTH_WAIT=${CHAOSGW_INSTALL_HEALTH_WAIT:-180}

say() { echo "install: $*"; }
die() { # status, message
	status=$1
	shift
	echo "install: $*" >&2
	exit "$status"
}

usage() { sed -n '2,/^set -eu/p' "$0" | sed -e '/^set -eu/d' -e 's/^# \{0,1\}//' -e '/^#!/d'; }

while [ $# -gt 0 ]; do
	case "$1" in
	--test-interface)
		[ $# -ge 2 ] || die 2 "--test-interface needs an interface name"
		case "$2" in
		"" | *[!A-Za-z0-9_.:@-]*) die 2 "--test-interface: not an interface name: $2" ;;
		esac
		TEST_IFS="$TEST_IFS $2"
		shift 2
		;;
	--version)
		[ $# -ge 2 ] || die 2 "--version needs a version"
		case "$2" in
		"" | *[!A-Za-z0-9_.-]*) die 2 "--version: not an image tag: $2" ;;
		esac
		VERSION=$2
		shift 2
		;;
	--image)
		[ $# -ge 2 ] || die 2 "--image needs an image reference"
		case "$2" in
		"" | -* | *[!A-Za-z0-9_.:/@-]*) die 2 "--image: not an image reference: $2" ;;
		esac
		IMAGE=$2
		shift 2
		;;
	--dir)
		[ $# -ge 2 ] || die 2 "--dir needs a directory"
		case "$2" in
		/?*) ;;
		*) die 2 "--dir: not an absolute directory: $2" ;;
		esac
		DIR=$2
		shift 2
		;;
	--no-pull) PULL=0; shift ;;
	--no-start) START=0; shift ;;
	--help | -h) usage; exit 0 ;;
	*) die 2 "unknown option $1" ;;
	esac
done

[ "$(id -u)" -eq 0 ] || die 2 "run it as root: sudo sh install.sh ..."
if [ -n "$IMAGE" ] && [ "$VERSION" != latest ]; then
	die 2 "--image and --version exclude each other: the version is the tag of the image"
fi
[ -n "$IMAGE" ] || IMAGE=$REGISTRY_IMAGE:$VERSION

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
trap 'exit 130' INT TERM

# 1. Docker: the host setup of the release installs it (it needs no image for that)
if ! command -v docker >/dev/null 2>&1; then
	command -v curl >/dev/null 2>&1 || die 1 "docker is not installed, and curl is needed to fetch the host setup that installs it: apt-get install -y curl"
	if [ "$VERSION" = latest ]; then base=$RELEASES/latest/download; else base=$RELEASES/download/v$VERSION; fi
	say "docker is not installed: fetching the host setup of the release ($base/host-setup.sh)"
	curl -fsSL "$base/host-setup.sh" -o "$TMP/host-setup-bootstrap.sh" || die 1 "cannot download $base/host-setup.sh"
	[ -s "$TMP/host-setup-bootstrap.sh" ] || die 1 "$base/host-setup.sh is empty"
	sh "$TMP/host-setup-bootstrap.sh" || die 1 "the host setup could not install Docker (above)"
	command -v docker >/dev/null 2>&1 || die 1 "docker is still not installed after the host setup"
fi
docker compose version >/dev/null 2>&1 || die 1 "docker has no compose plugin (docker-compose-plugin)"

# 2. the image
if [ "$PULL" -eq 1 ]; then
	say "pulling $IMAGE"
	docker pull --quiet "$IMAGE" >/dev/null || die 1 "cannot pull $IMAGE (is $VERSION a release? --no-pull for an image that is loaded)"
fi

# 3. the host setup of this image
docker run --rm --entrypoint cat "$IMAGE" /usr/local/share/chaosgw/host-setup.sh >"$TMP/host-setup.sh" || die 1 "cannot read the host setup out of $IMAGE"
[ -s "$TMP/host-setup.sh" ] || die 1 "$IMAGE holds no host setup: is it an image of Chaos Gateway?"
[ -n "$TEST_IFS" ] || say "no --test-interface: the gateway starts with no port for a test network (see --help)"
set -- --image "$IMAGE" --install-dir "$DIR"
for i in $TEST_IFS; do
	set -- "$@" --test-interface "$i"
done
say "running the host setup of $IMAGE"
sh "$TMP/host-setup.sh" "$@" || die 1 "the host setup found a step missing (above); do what it says and run install.sh again"

# 4. the containers
if [ "$START" -eq 0 ]; then
	say "the host is ready; start the gateway with: cd $DIR && docker compose up -d"
	exit 0
fi
cd -- "$DIR" || die 1 "cannot enter $DIR"
[ -f compose.yaml ] || die 1 "$DIR has no compose.yaml after the host setup"
say "starting the containers"
docker compose up -d --remove-orphans || die 1 "cannot start the containers: docker compose logs"
say "waiting for the health checks (up to $HEALTH_WAIT s)"
docker compose up -d --wait --wait-timeout "$HEALTH_WAIT" exec api svcns kea bird ||
	die 5 "the containers are not healthy: docker compose ps; docker compose logs exec api (docs/troubleshooting.md)"

# 5. where to go
cat <<DONE

install: Chaos Gateway is running.

  Finish the setup in a browser:  https://<management address of this host>:8443
  The one-time setup token:       cd $DIR && docker compose logs api | grep 'Setup token'
  Update later:                   sudo sh $DIR/update.sh
  Take it away again:             sudo sh $DIR/uninstall.sh
DONE
