#!/bin/sh
# Chaos Gateway host setup (plan §3.8). Run it once on the Ubuntu 24.04 or 26.04 host that will run
# the containers; it is safe to run again.
#
#   docker run --rm --entrypoint cat chaos-gateway:latest /usr/local/share/chaosgw/host-setup.sh | sudo sh
#
# What it does:
#   - installs Docker Engine and the compose plugin when they are missing (Ubuntu's packages),
#   - loads the kernel modules Chaos Gateway needs now and at every boot
#     (/etc/modules-load.d/chaos-gateway.conf),
#   - enables IPv4 forwarding now and at every boot (/etc/sysctl.d/90-chaos-gateway.conf),
#   - prints the netplan changes the test interfaces need.
# What it never does: touch the uplink or the management configuration, or any netplan file.
#
# Options (for tests and unusual hosts):
#   --prefix DIR      write the files below DIR instead of /, and do not need root
#   --skip-docker     do not install or check Docker
#   --skip-modprobe   do not load modules or change sysctls of the running system
#   --help            this text
set -eu

# The modules of the preflight (internal/preflight/modules.go; a test keeps the lists equal).
REQUIRED_MODULES="sch_netem sch_htb cls_fw cls_u32 cls_flower act_mirred ifb nf_conntrack nf_conntrack_netlink nf_nat nf_tables nft_ct nft_nat nft_chain_nat nft_masq nft_redir nft_reject nft_reject_inet nft_dup_netdev veth bridge wireguard"
# Needed only after V1 (VLAN networks): loaded when present, never an error.
LATER_MODULES="8021q"

PREFIX=""
DOCKER=1
MODPROBE=1

while [ $# -gt 0 ]; do
	case "$1" in
	--prefix)
		[ $# -ge 2 ] || { echo "host-setup: --prefix needs a directory" >&2; exit 2; }
		PREFIX="$2"
		shift 2
		;;
	--skip-docker) DOCKER=0; shift ;;
	--skip-modprobe) MODPROBE=0; shift ;;
	--help | -h)
		cat <<'HELP'
Chaos Gateway host setup: installs Docker if missing, loads the kernel modules and enables
IPv4 forwarding now and at every boot, and prints the netplan hints for the test interfaces.
It never touches the uplink or the management configuration.

  --prefix DIR      write the files below DIR instead of /, and do not need root
  --skip-docker     do not install or check Docker
  --skip-modprobe   do not load modules or change sysctls of the running system
  --help            this text
HELP
		exit 0
		;;
	*) echo "host-setup: unknown option $1" >&2; exit 2 ;;
	esac
done

if [ -z "$PREFIX" ] && [ "$(id -u)" -ne 0 ]; then
	echo "host-setup: run it as root (sudo sh host-setup.sh)" >&2
	exit 1
fi

say() { echo "host-setup: $*"; }

write_if_changed() { # file, content on stdin
	dir=$(dirname "$1")
	mkdir -p "$dir"
	tmp=$(mktemp "$dir/.chaosgw.XXXXXX")
	cat >"$tmp"
	if [ -f "$1" ] && cmp -s "$tmp" "$1"; then
		rm -f "$tmp"
		return 1
	fi
	chmod 644 "$tmp"
	mv "$tmp" "$1"
	return 0
}

# ---- Docker
if [ "$DOCKER" -eq 1 ]; then
	if ! command -v docker >/dev/null 2>&1; then
		say "installing Docker Engine and the compose plugin"
		export DEBIAN_FRONTEND=noninteractive
		apt-get update -q
		apt-get install -y -q docker.io docker-compose-v2
	fi
	if command -v systemctl >/dev/null 2>&1; then
		systemctl enable --now docker >/dev/null 2>&1 || true
	fi
	if ! docker compose version >/dev/null 2>&1; then
		echo "host-setup: Docker is installed but has no compose plugin (docker compose); install docker-compose-v2 or docker-compose-plugin" >&2
		exit 1
	fi
	say "Docker is ready: $(docker --version)"
fi

# ---- modules at boot
{
	echo "# Written by the Chaos Gateway host setup: the kernel modules the gateway needs."
	for m in $REQUIRED_MODULES $LATER_MODULES; do echo "$m"; done
} | if write_if_changed "$PREFIX/etc/modules-load.d/chaos-gateway.conf"; then
	say "wrote $PREFIX/etc/modules-load.d/chaos-gateway.conf"
else
	say "$PREFIX/etc/modules-load.d/chaos-gateway.conf is up to date"
fi

# ---- forwarding at boot
printf '# Written by the Chaos Gateway host setup.\nnet.ipv4.ip_forward=1\n' | if write_if_changed "$PREFIX/etc/sysctl.d/90-chaos-gateway.conf"; then
	say "wrote $PREFIX/etc/sysctl.d/90-chaos-gateway.conf"
else
	say "$PREFIX/etc/sysctl.d/90-chaos-gateway.conf is up to date"
fi

# ---- the running system
status=0
if [ "$MODPROBE" -eq 1 ]; then
	sysctl -q -w net.ipv4.ip_forward=1
	missing=""
	for m in $REQUIRED_MODULES; do
		modprobe -q "$m" 2>/dev/null || missing="$missing $m"
	done
	if [ -n "$missing" ] && command -v apt-get >/dev/null 2>&1; then
		# generic kernels keep some modules in an extra package
		say "modules missing:$missing; trying linux-modules-extra-$(uname -r)"
		DEBIAN_FRONTEND=noninteractive apt-get install -y -q "linux-modules-extra-$(uname -r)" >/dev/null 2>&1 || true
		still=""
		for m in $missing; do
			modprobe -q "$m" 2>/dev/null || still="$still $m"
		done
		missing="$still"
	fi
	for m in $LATER_MODULES; do modprobe -q "$m" 2>/dev/null || true; done
	if [ -n "$missing" ]; then
		echo "host-setup: the kernel $(uname -r) lacks modules the gateway needs:$missing" >&2
		echo "host-setup: install the extra kernel modules for your kernel, or boot a standard Ubuntu kernel" >&2
		status=1
	else
		say "all required kernel modules are loaded"
	fi
fi

cat <<'NOTES'

host-setup: the test interfaces
  Give each interface that Chaos Gateway will use for a test network no address and no DHCP in
  netplan, and keep the uplink and the management interface as they are, for example:

    network:
      version: 2
      ethernets:
        enp3s0:            # a test network port
          dhcp4: false
          dhcp6: false
          optional: true

  Then run `sudo netplan apply`. Chaos Gateway assigns the port to its own bridge.
NOTES

exit "$status"
