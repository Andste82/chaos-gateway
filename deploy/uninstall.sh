#!/bin/sh
# Chaos Gateway uninstall (plan §3.8 "Stop", D35, M27/M30): takes the gateway away from the host.
#
#   sudo sh uninstall.sh            stop the gateway, take what it made out of the kernel, give the test interfaces back to
#                                   systemd-networkd. The data, the files in /opt/chaosgw and the images stay: install.sh brings
#                                   the gateway back with its revisions, secrets and captures
#   sudo sh uninstall.sh --purge    also delete the data below /var/lib/chaosgw (the revisions, the secrets, the audit log, the
#                                   captures, Kea's leases: irreversible), the files in /opt/chaosgw, the images and the
#                                   configuration the host setup wrote (modules, sysctl). It asks for a yes first (--yes does not)
#
# What it does, in this order:
#   1. `docker compose stop`: the containers stop, the API first and the executor last. The executor removes everything it made
#      in the kernel when it stops: the nftables table, the policy rules and routes, the test bridges, the service pair, the probes,
#      the tc trees, the rules in DOCKER-USER (docs/install.md "Stop, restart and recovery"),
#   2. the teardown service (`chaosgw teardown`): removes what an executor left that was killed and could not clean up (it finds
#      nothing after a normal stop, and it refuses while an executor is serving, which is why the stop comes first; with --purge it also
#      empties the data directories),
#   3. `docker compose down -v`: the containers and the volumes of the sockets go (after the teardown, which makes them again),
#   4. removes the systemd-networkd rule of each test interface (the host setup wrote /etc/systemd/network/10-chaosgw-IF.network),
#   5. with --purge: the images, the host configuration of the host setup, the data and the files of the deployment.
#
# What it never does: remove Docker, touch the uplink or the management configuration, or undo what you changed in netplan for the
# test interfaces. Kernel modules and net.ipv4.ip_forward stay until the next boot (with --purge their files are gone, so the next boot
# does not set them again).
#
# Options:
#   --purge        also delete the data, the files, the images and the host configuration (asks first)
#   --yes          do not ask (for --purge)
#   --dir DIR      where compose.yaml is (default: the directory of this script)
#   --prefix DIR   read and write the files below DIR (data, networkd, sysctl) instead of /, and do not need root; for tests
#   --help         this text
#
# Exit status: 0 done, 1 a step failed (it says which, and what is left), 2 usage.
set -eu

DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
PURGE=0
YES=0
PREFIX=""
DATA_DIR=/var/lib/chaosgw
status=0

say() { echo "uninstall: $*"; }
warn() { # a step that failed: say so, go on with the others, and end with status 1
	echo "uninstall: $*" >&2
	status=1
}
die() { # status, message
	st=$1
	shift
	echo "uninstall: $*" >&2
	exit "$st"
}

usage() { sed -n '2,/^set -eu/p' "$0" | sed -e '/^set -eu/d' -e 's/^# \{0,1\}//' -e '/^#!/d'; }

while [ $# -gt 0 ]; do
	case "$1" in
	--purge) PURGE=1; shift ;;
	--yes) YES=1; shift ;;
	--dir)
		[ $# -ge 2 ] || die 2 "--dir needs a directory"
		DIR=$2
		shift 2
		;;
	--prefix)
		[ $# -ge 2 ] || die 2 "--prefix needs a directory"
		PREFIX=$2
		shift 2
		;;
	--help | -h) usage; exit 0 ;;
	*) die 2 "unknown option $1" ;;
	esac
done

if [ -z "$PREFIX" ] && [ "$(id -u)" -ne 0 ]; then
	die 2 "run it as root: sudo sh uninstall.sh ..."
fi
# the directories this script may delete are checked before anything is stopped: a wrong --dir must not cost a thing
case "$DIR" in
"" | /) die 2 "--dir: not a directory of a deployment: $DIR" ;;
*/.. | */../*) die 2 "--dir: no .. in the directory: $DIR" ;;
/*) ;;
*) die 2 "--dir: not an absolute directory: $DIR" ;;
esac
DATA="$PREFIX$DATA_DIR"
NETWORKD="$PREFIX/etc/systemd/network"

if [ "$PURGE" -eq 1 ] && [ "$YES" -eq 0 ]; then
	cat <<ASK
uninstall: --purge deletes, for good:
  $DATA        (the revisions, the secrets, the audit log, the captures, Kea's leases)
  $DIR         (compose.yaml, .env and the scripts)
  the images of the gateway, and the host configuration the host setup wrote
ASK
	[ -t 0 ] || die 2 "this is not a terminal to answer on: add --yes to delete without asking"
	printf 'uninstall: type yes to go on: '
	read -r answer
	[ "$answer" = yes ] || die 2 "not confirmed; nothing was changed"
fi

# the images the deployment uses, asked before the containers go (the compose file names them)
IMAGES=""
HAVE_COMPOSE=0
if [ -f "$DIR/compose.yaml" ] && command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
	HAVE_COMPOSE=1
	IMAGES=$(cd -- "$DIR" && docker compose config --images 2>/dev/null | sort -u | tr '\n' ' ') || IMAGES=""
fi

# 1. and 2. the containers and the kernel
if [ "$HAVE_COMPOSE" -eq 1 ]; then
	say "stopping the containers (the executor removes what it made in the kernel)"
	(cd -- "$DIR" && docker compose stop) || warn "docker compose stop failed; the containers may still run (docker compose ps in $DIR)"
	say "checking the kernel for what a killed executor left"
	if [ "$PURGE" -eq 1 ]; then
		(cd -- "$DIR" && docker compose --profile teardown run --rm teardown --purge) || warn "the teardown could not remove everything (above); the data directories were not emptied, run it again after the cause is gone"
	else
		(cd -- "$DIR" && docker compose --profile teardown run --rm teardown) || warn "the teardown could not remove everything (above); run it again after the cause is gone"
	fi
	say "removing the containers and the volumes"
	(cd -- "$DIR" && docker compose down -v --remove-orphans) || warn "docker compose down failed (above)"
else
	say "no compose.yaml in $DIR, or no Docker: no containers to stop"
fi

# 3. systemd-networkd gets the test interfaces back
reload=0
for f in "$NETWORKD"/10-chaosgw-*.network; do
	[ -e "$f" ] || continue
	ifname=${f##*/10-chaosgw-}
	ifname=${ifname%.network}
	say "giving $ifname back to systemd-networkd"
	if [ -f "$DIR/host-setup.sh" ]; then
		sh "$DIR/host-setup.sh" --remove-test-interface "$ifname" ${PREFIX:+--prefix "$PREFIX"} || warn "cannot remove the networkd rule of $ifname ($f)"
	else
		rm -f -- "$f" || warn "cannot remove $f"
		reload=1
	fi
done
if [ "$reload" -eq 1 ] && [ -z "$PREFIX" ] && command -v networkctl >/dev/null 2>&1; then
	networkctl reload >/dev/null 2>&1 || true
fi

# 4. everything else, on request
if [ "$PURGE" -eq 1 ]; then
	if [ "$HAVE_COMPOSE" -eq 1 ] && [ -n "$IMAGES" ]; then
		say "removing the images: $IMAGES"
		# shellcheck disable=SC2086 # the names come from compose config: words without spaces
		docker image rm $IMAGES >/dev/null 2>&1 || say "some images stay (another container uses them, or they are gone already)"
	fi
	rm -f -- "$PREFIX/etc/modules-load.d/chaos-gateway.conf" "$PREFIX/etc/sysctl.d/90-chaos-gateway.conf" || warn "cannot remove the host configuration files"
	# the data first, the deployment last: the directory of this script is the one that goes with it
	if [ "$status" -eq 0 ]; then
		say "deleting $DATA and $DIR"
		rm -rf -- "$DATA" || warn "cannot delete $DATA"
		if [ -f "$DIR/compose.yaml" ] || [ -f "$DIR/.env" ]; then
			rm -rf -- "$DIR" || warn "cannot delete $DIR"
		else
			say "$DIR holds no compose.yaml or .env: it is not deleted"
		fi
	else
		say "a step failed above, so the data and $DIR stay (the teardown needs the executor's state in them); fix the cause and run it again"
	fi
fi

if [ "$status" -eq 0 ]; then
	if [ "$PURGE" -eq 1 ]; then
		say "Chaos Gateway is gone from this host. Docker stays; so does what you changed in netplan for the test interfaces"
	else
		say "Chaos Gateway is stopped and out of the kernel. The data ($DATA), $DIR and the images stay;"
		say "sudo sh $DIR/install.sh brings it back, uninstall.sh --purge deletes all of it"
	fi
fi
exit "$status"
