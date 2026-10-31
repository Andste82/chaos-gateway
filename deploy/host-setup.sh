#!/bin/sh
# Chaos Gateway host setup (plan §3.8). Run it once on the Ubuntu 24.04 or 26.04 host that will run
# the containers; it is safe to run again.
#
#   docker run --rm --entrypoint cat chaos-gateway:latest /usr/local/share/chaosgw/host-setup.sh | sudo sh -s -- --test-interface lan0
#
# What it does:
#   - installs Docker Engine and the compose plugin when they are missing (Ubuntu's packages),
#   - makes the directories of the gateway's data below /var/lib/chaosgw with their owners (plan §3.6; the compose file mounts
#     them from the host, and the API container runs as an unprivileged user that could not write a directory Docker made),
#   - loads the kernel modules Chaos Gateway needs now and at every boot
#     (/etc/modules-load.d/chaos-gateway.conf),
#   - enables IPv4 forwarding now and at every boot (/etc/sysctl.d/90-chaos-gateway.conf), and lets every group open
#     the unprivileged ICMP socket the ping of the diagnostics uses (net.ipv4.ping_group_range, M24),
#   - for every --test-interface: writes the rule that keeps systemd-networkd off it
#     (/etc/systemd/network/10-chaosgw-<interface>.network, issue #64) and checks it (as --check),
#   - with --image, copies compose.yaml, update.sh, host-setup.sh and env.example out of that image into /opt/chaosgw and writes the .env that
#     names the image (never over an existing one),
#   - prints the netplan changes the test interfaces need and what to do next.
# What it never does: touch the uplink or the management configuration, or any netplan file. It refuses a
# --test-interface that carries the host's own connection (the default route, an IPv4 address, an SSH
# session, the one that runs it included): the rule would make systemd-networkd drop that interface at once.
#
# Options:
#   --test-interface IF          IF will be a test interface: write its networkd rule, then check it
#                                (repeatable)
#   --check IF                   only check IF (repeatable), change nothing: it exists and has a
#                                carrier, and no netplan entry, NetworkManager connection or
#                                systemd-networkd file configures it; exits 1 when a step is missing
#   --remove-test-interface IF   only remove the networkd rule of IF (repeatable), the undo of
#                                --test-interface
#   --image REF                  the gateway image (ghcr.io/andste82/chaos-gateway:1.0.0): copy the files of the deployment
#                                out of it into --install-dir and write the .env that names it
#   --install-dir DIR            where the deployment lives, default /opt/chaosgw
#   --data-only                  only make the data directories (a CI job, a repair after a restore)
# Options for tests and unusual hosts:
#   --prefix DIR      read and write the files below DIR instead of /, ask no running service, and
#                     do not need root
#   --skip-docker     do not install or check Docker
#   --skip-modprobe   do not load modules or change sysctls of the running system
#   --help            this text
set -eu

# The directories of the persistence layout (plan §3.6) the compose file mounts from the host: name:owner:mode, below DATA_DIR.
# The user 65532 is the API container's; the secrets are its alone. tools/ciconfig keeps this list equal to the Dockerfile's.
DATA_DIR=/var/lib/chaosgw
DATA_DIRS="state:65532:0755 secrets:65532:0700 api:65532:0755 service:65532:0755 tlsca:65532:0755 captures:65532:0755 exec:0:0755 kea:0:0755"
INSTALL_DIR=/opt/chaosgw

# The modules of the preflight (internal/preflight/modules.go; a test keeps the lists equal).
REQUIRED_MODULES="sch_netem sch_htb cls_fw cls_u32 cls_flower sch_ingress act_mirred ifb nf_conntrack nf_conntrack_netlink nf_nat nf_tables nft_ct nft_nat nft_chain_nat nft_masq nft_redir nft_reject nft_reject_inet nft_dup_netdev nft_numgen veth bridge wireguard"
# Needed only after V1 (VLAN networks): loaded when present, never an error.
LATER_MODULES="8021q"

PREFIX=""
DOCKER=1
MODPROBE=1
TEST_IFS=""
CHECK_IFS=""
REMOVE_IFS=""
IMAGE=""
DATA_ONLY=0

# interface_arg refuses a value that is no interface name (at most 15 characters, no "/", no space).
interface_arg() { # option, value
	case "$2" in
	"" | . | .. | */* | *[!A-Za-z0-9_.:@-]*)
		echo "host-setup: $1: not an interface name: $2" >&2
		exit 2
		;;
	esac
	if [ "${#2}" -gt 15 ]; then
		echo "host-setup: $1: not an interface name (more than 15 characters): $2" >&2
		exit 2
	fi
}

while [ $# -gt 0 ]; do
	case "$1" in
	--prefix)
		[ $# -ge 2 ] || { echo "host-setup: --prefix needs a directory" >&2; exit 2; }
		PREFIX="$2"
		shift 2
		;;
	--test-interface | --check | --remove-test-interface)
		[ $# -ge 2 ] || { echo "host-setup: $1 needs an interface" >&2; exit 2; }
		interface_arg "$1" "$2"
		case "$1" in
		--test-interface) TEST_IFS="$TEST_IFS $2" ;;
		--check) CHECK_IFS="$CHECK_IFS $2" ;;
		*) REMOVE_IFS="$REMOVE_IFS $2" ;;
		esac
		shift 2
		;;
	--image)
		[ $# -ge 2 ] || { echo "host-setup: --image needs an image reference" >&2; exit 2; }
		case "$2" in
		"" | *[!A-Za-z0-9_.:/-]*)
			echo "host-setup: --image: not an image reference (name[:tag]): $2" >&2
			exit 2
			;;
		esac
		IMAGE="$2"
		shift 2
		;;
	--install-dir)
		[ $# -ge 2 ] || { echo "host-setup: --install-dir needs a directory" >&2; exit 2; }
		INSTALL_DIR="$2"
		shift 2
		;;
	--data-only) DATA_ONLY=1; shift ;;
	--skip-docker) DOCKER=0; shift ;;
	--skip-modprobe) MODPROBE=0; shift ;;
	--help | -h)
		cat <<'HELP'
Chaos Gateway host setup: installs Docker if missing, loads the kernel modules and enables
IPv4 forwarding now and at every boot, makes the data directories below /var/lib/chaosgw with
their owners, keeps systemd-networkd off the test interfaces and checks them, and prints the
netplan hints for them. It never touches the uplink or the
management configuration, or any netplan file, and refuses a --test-interface that has the
default route, an IPv4 address or an SSH session (the one that runs the setup included).

  --test-interface IF          IF will be a test interface: write
                               /etc/systemd/network/10-chaosgw-IF.network ([Link] Unmanaged=yes),
                               reload systemd-networkd, then check IF (repeatable)
  --check IF                   only check IF (repeatable), change nothing: the interface exists and
                               has a carrier, and no netplan entry, NetworkManager connection or
                               systemd-networkd file configures it; exits 1 when a step is missing
  --remove-test-interface IF   only remove the networkd rule of IF (repeatable)
  --image REF                  the gateway image: copy compose.yaml, update.sh, host-setup.sh and env.example out of
                               it into --install-dir and write the .env that names it
  --install-dir DIR            where the deployment lives (default /opt/chaosgw)
  --data-only                  only make the data directories below /var/lib/chaosgw
  --prefix DIR                 read and write the files below DIR instead of /, ask no running
                               service, and do not need root
  --skip-docker                do not install or check Docker
  --skip-modprobe              do not load modules or change sysctls of the running system
  --help                       this text

From the image:
  docker run --rm --entrypoint cat IMAGE /usr/local/share/chaosgw/host-setup.sh | sudo sh -s -- --image IMAGE --test-interface lan0
HELP
		exit 0
		;;
	*) echo "host-setup: unknown option $1" >&2; exit 2 ;;
	esac
done

MODE=setup
if [ "$DATA_ONLY" = 1 ]; then
	MODE=data
fi
if [ -n "$CHECK_IFS" ]; then
	MODE=check
fi
if [ -n "$REMOVE_IFS" ]; then
	[ "$MODE" = setup ] || { echo "host-setup: --check and --remove-test-interface do not go together" >&2; exit 2; }
	MODE=remove
fi
if [ "$DATA_ONLY" = 1 ] && { [ -n "$CHECK_IFS" ] || [ -n "$REMOVE_IFS" ]; }; then
	echo "host-setup: --data-only does not go with --check or --remove-test-interface" >&2
	exit 2
fi
if [ "$MODE" != setup ] && [ -n "$TEST_IFS" ]; then
	echo "host-setup: --test-interface does not go with --check, --remove-test-interface or --data-only" >&2
	exit 2
fi
if [ "$MODE" != setup ] && [ -n "$IMAGE" ]; then
	echo "host-setup: --image does not go with --check, --remove-test-interface or --data-only" >&2
	exit 2
fi

if [ -z "$PREFIX" ] && [ "$(id -u)" -ne 0 ]; then
	echo "host-setup: run it as root (sudo sh host-setup.sh)" >&2
	exit 1
fi

say() { echo "host-setup: $*"; }

# refuse_host_interface stops the setup, before it changes anything, when a --test-interface carries the
# host's own connection: an SSH session (the one that runs the setup included) comes in on it, it has the
# default route (the uplink), or it has an IPv4 address (the management interface, or a port the operating
# system still configures). Its networkd rule would make systemd-networkd drop the interface at once: no lease renewal,
# no configuration after the next boot. The setup wizard keeps the uplink and the management interface out
# of the test interfaces for the same reason; this script runs before the wizard and knows neither.
#
# ssh_addresses prints the host's local addresses of the SSH sessions: $SSH_CONNECTION, which sudo drops
# from the environment by default, and the local end of every established connection of sshd that ss
# lists (the process, or the local port 22), without the port, the brackets, the zone and an IPv4-mapped
# prefix. The setup cannot tell which session runs it; a test interface carries none of them.
ssh_addresses() {
	echo "${SSH_CONNECTION:-}" | awk '{print $3}'
	command -v ss >/dev/null 2>&1 || return 0
	ss -Htnp state established 2>/dev/null | awk '/"sshd/ || $3 ~ /:22$/ {
		a = $3; sub(/:[0-9]+$/, "", a); gsub(/[][]/, "", a); sub(/%.*$/, "", a); sub(/^::ffff:/, "", a); print a
	}' || true
}

refuse_host_interface() { # interface
	command -v ip >/dev/null 2>&1 || return 0
	addrs=$(ip -o addr show dev "$1" 2>/dev/null || true)
	why=""
	ssh_local=""
	for a in $(ssh_addresses); do
		if echo "$addrs" | awk -v a="$a" '{split($4, p, "/"); if (p[1] == a) f = 1} END {exit !f}'; then
			ssh_local=$a
			break
		fi
	done
	if [ -n "$ssh_local" ]; then
		why="an SSH session of the host comes in on $1 (address $ssh_local)"
	elif [ -n "$(ip -4 route show default dev "$1" 2>/dev/null)$(ip -6 route show default dev "$1" 2>/dev/null)" ]; then
		why="$1 has the default route, it looks like the uplink"
	else
		v4=$(echo "$addrs" | awk '$3 == "inet" && / scope global / {print $4; exit}')
		if [ -n "$v4" ]; then
			why="$1 has the IPv4 address $v4, it looks like the uplink or the management interface"
		fi
	fi
	[ -n "$why" ] || return 0
	echo "host-setup: --test-interface $1: $why. A test interface carries no connection of the host itself; nothing was changed." >&2
	echo "host-setup: if $1 is a test port the operating system still configures, remove that configuration first (--check $1 names it) and its address (sudo ip addr flush dev $1)." >&2
	exit 2
}

if [ "$MODE" = setup ]; then
	for i in $TEST_IFS; do
		refuse_host_interface "$i"
	done
fi

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

# ---- the data directories (plan §3.6)

# make_data_dirs makes the directories the compose file mounts from the host, with the owners the containers need. Docker would
# make a missing one as root's, which the API container (user 65532, no capability) cannot write; the compose file does not let it
# (create_host_path: false). An existing directory is put right (owner and mode of the directory itself, never of its content).
# Without root (--prefix in a test) the owner cannot be set and is left as it is.
make_data_dirs() {
	mkdir -p "$PREFIX$DATA_DIR"
	for spec in $DATA_DIRS; do
		name=${spec%%:*}
		rest=${spec#*:}
		uid=${rest%%:*}
		mode=${rest#*:}
		d="$PREFIX$DATA_DIR/$name"
		new=""
		[ -d "$d" ] || new=1
		mkdir -p "$d"
		if [ "$(id -u)" -eq 0 ]; then
			[ "$(stat -c %u:%g "$d")" = "$uid:$uid" ] || { chown "$uid:$uid" "$d"; new=1; }
		fi
		[ "$(stat -c %a "$d")" = "${mode#0}" ] || { chmod "$mode" "$d"; new=1; }
		if [ -n "$new" ]; then say "made $DATA_DIR/$name (owner $uid, mode $mode)"; else say "$DATA_DIR/$name is as it should be"; fi
	done
}

# install_files copies the files of the deployment out of the image and names the image in .env (an existing .env is kept: it
# may hold the settings of the administrator).
install_files() { # image
	ref=$1
	command -v docker >/dev/null 2>&1 || { echo "host-setup: --image needs Docker" >&2; return 1; }
	# name[:tag]: the tag is what follows the last colon unless that colon belongs to a registry's port (a slash follows it)
	name=$ref tag=latest
	case "$ref" in
	*:*)
		case "${ref##*:}" in
		*/*) ;;
		*) name=${ref%:*} tag=${ref##*:} ;;
		esac
		;;
	esac
	mkdir -p "$PREFIX$INSTALL_DIR"
	if ! docker run --rm --entrypoint tar "$ref" -C /usr/local/share/chaosgw -cf - compose.yaml update.sh host-setup.sh env.example | tar -xf - -C "$PREFIX$INSTALL_DIR" --no-same-owner; then
		echo "host-setup: cannot copy the files of the deployment out of $ref" >&2
		return 1
	fi
	chmod 0755 "$PREFIX$INSTALL_DIR/update.sh" "$PREFIX$INSTALL_DIR/host-setup.sh"
	say "copied compose.yaml, update.sh, host-setup.sh and env.example from $ref to $INSTALL_DIR"
	if [ -e "$PREFIX$INSTALL_DIR/.env" ]; then
		say "$INSTALL_DIR/.env is kept; it names the image: check CHAOSGW_IMAGE and CHAOSGW_VERSION"
	else
		printf '# Written by the Chaos Gateway host setup; env.example lists the settings.\nCHAOSGW_IMAGE=%s\nCHAOSGW_VERSION=%s\n' "$name" "$tag" >"$PREFIX$INSTALL_DIR/.env"
		say "wrote $INSTALL_DIR/.env (image $name, version $tag)"
	fi
}

# ---- the test interfaces (issue #64): the networkd rule and the check

NETWORKD_RULE_MARK="# Written by the Chaos Gateway host setup"

networkd_rule() { # interface
	echo "$PREFIX/etc/systemd/network/10-chaosgw-$1.network"
}

# networkd_active says whether the host's systemd-networkd runs (never under --prefix).
networkd_active() {
	[ -z "$PREFIX" ] && command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet systemd-networkd 2>/dev/null
}

networkd_reload() {
	if networkd_active && command -v networkctl >/dev/null 2>&1; then
		networkctl reload && say "systemd-networkd has read its configuration again"
	fi
}

write_networkd_rule() { # interface; 1 when the file was up to date
	{
		echo "$NETWORKD_RULE_MARK: systemd-networkd leaves the test interface $1 alone."
		echo "# sudo sh host-setup.sh --remove-test-interface $1 removes it."
		echo "[Match]"
		echo "Name=$1"
		echo
		echo "[Link]"
		echo "Unmanaged=yes"
	} | if write_if_changed "$(networkd_rule "$1")"; then
		say "wrote $(networkd_rule "$1")"
	else
		say "$(networkd_rule "$1") is up to date"
		return 1
	fi
}

remove_networkd_rule() { # interface; 1 when the file is not the host setup's
	f=$(networkd_rule "$1")
	if [ ! -e "$f" ]; then
		say "$f is not there"
		return 0
	fi
	if ! head -n 1 "$f" | grep -q "^$NETWORKD_RULE_MARK"; then
		echo "host-setup: $f was not written by the host setup; it is left as it is" >&2
		return 1
	fi
	rm -f "$f"
	say "removed $f"
}

# networkd_match evaluates a .network file for an interface as systemd-networkd does, and as the setup
# wizard's preflight does (internal/preflight/networkd.go; a test keeps the two equal). It prints
# "ignore" for a file without a [Match] condition, otherwise "match" or "nomatch" and, after a space, 1
# when its [Link] section says Unmanaged=yes (else 0). The interface is taken as a physical port: Type
# ether (wlan for a wireless one), no Kind. A list that starts with "!" is negated item by item, an empty
# setting clears the list; a condition that is not evaluated (Path, Property, Virtualization, ...) holds,
# a WLAN condition only for a wireless interface.
networkd_match() { # file, name, mac, driver, type
	awk -v name="$2" -v mac="$3" -v driver="$4" -v type="$5" '
	function glob2re(g,   i, n, c, j, cls, re) {
		re = "^"; n = length(g)
		for (i = 1; i <= n; i++) {
			c = substr(g, i, 1)
			if (c == "*") re = re ".*"
			else if (c == "?") re = re "."
			else if (c == "[" && (j = index(substr(g, i + 1), "]")) > 1) {
				cls = substr(g, i + 1, j - 1)
				if (substr(cls, 1, 1) == "!") cls = "^" substr(cls, 2)
				re = re "[" cls "]"; i += j
			} else if (index("\\.^$+(){}|[]/", c)) re = re "\\" c
			else re = re c
		}
		return re "$"
	}
	function globlist(items, s,   n, a, i, g, neg, pos, m) {
		n = split(items, a, " "); pos = 0; m = 0
		for (i = 1; i <= n; i++) {
			g = a[i]; neg = substr(g, 1, 1) == "!"
			if (neg) g = substr(g, 2); else pos = 1
			if (s != "" && s ~ glob2re(g)) { if (neg) return 0; m = 1 }
		}
		return m || !pos
	}
	function maclist(items, s,   n, a, i) {
		n = split(items, a, " ")
		for (i = 1; i <= n; i++) if (s != "" && tolower(a[i]) == tolower(s)) return 1
		return 0
	}
	function truth(v) { v = tolower(v); return v == "1" || v == "yes" || v == "y" || v == "true" || v == "t" || v == "on" }
	BEGIN {
		evaluated["Name"] = evaluated["MACAddress"] = evaluated["PermanentMACAddress"] = 1
		evaluated["Driver"] = evaluated["Type"] = evaluated["Kind"] = 1
		wlan["WLANInterfaceType"] = wlan["SSID"] = wlan["BSSID"] = 1
	}
	{
		l = $0; sub(/^[ \t]+/, "", l); sub(/[ \t\r]+$/, "", l)
		if (l == "" || l ~ /^[#;]/) next
		if (l ~ /^\[.*\]$/) { section = substr(l, 2, length(l) - 2); next }
		eq = index(l, "="); if (eq == 0) next
		k = substr(l, 1, eq - 1); v = substr(l, eq + 1)
		sub(/[ \t]+$/, "", k); sub(/^[ \t]+/, "", v)
		if (section == "Link" && k == "Unmanaged") { unmanaged = truth(v); next }
		if (section != "Match") next
		if (!(k in evaluated)) { has[k] = 1; next }
		if (v == "") { delete has[k]; delete cond[k]; next }
		neg = substr(v, 1, 1) == "!"; if (neg) v = substr(v, 2)
		n = split(v, a, /[ \t]+/)
		for (i = 1; i <= n; i++) if (a[i] != "") cond[k] = cond[k] " " (neg ? "!" : "") a[i]
		has[k] = 1
	}
	END {
		any = 0; ok = 1
		for (k in has) {
			any = 1
			if (k == "MACAddress" || k == "PermanentMACAddress") holds = maclist(cond[k], mac)
			else if (k == "Name") holds = globlist(cond[k], name)
			else if (k == "Driver") holds = globlist(cond[k], driver)
			else if (k == "Type") holds = globlist(cond[k], type)
			else if (k == "Kind") holds = globlist(cond[k], "")
			else if (k in wlan) holds = type == "wlan"
			else holds = 1
			if (!holds) ok = 0
		}
		if (!any) print "ignore"
		else print (ok ? "match" : "nomatch") " " (unmanaged ? 1 : 0)
	}' "$1"
}

# before reports whether the file name $1 comes before $2 in systemd-networkd's order (strcmp).
before() {
	[ "$1" != "$2" ] && [ "$(printf '%s\n%s\n' "$1" "$2" | LC_ALL=C sort | head -n 1)" = "$1" ]
}

CHECK_FAILS=0
check_ok() { echo "  ok    $*"; }
check_warn() { echo "  warn  $*"; }
check_fail() {
	echo "  FAIL  $*"
	CHECK_FAILS=$((CHECK_FAILS + 1))
}
check_fix() { echo "        fix: $*"; }

# check_interface checks one test interface: its link, and that neither netplan, nor NetworkManager, nor
# systemd-networkd configures it. It reads the files the setup wizard's preflight reads, and here also
# those below /run and /usr/lib the executor does not see; without --prefix it also asks NetworkManager
# and systemd-networkd about the running interface.
check_interface() { # interface
	i=$1
	sys="$PREFIX/sys/class/net/$i"
	echo "host-setup: check of the test interface $i"
	mac="" driver="" type=ether
	if [ ! -e "$sys" ]; then
		check_fail "link: $i is not an interface of this host (ip link lists them)"
	else
		mac=$(cat "$sys/address" 2>/dev/null || true)
		if [ -L "$sys/device/driver" ]; then
			driver=$(basename "$(readlink "$sys/device/driver")")
		fi
		if [ -d "$sys/wireless" ]; then
			type=wlan
		fi
		if [ "$(cat "$sys/carrier" 2>/dev/null || echo 0)" = 1 ]; then
			check_ok "link: $i is up and has a carrier (MAC ${mac:-unknown}, driver ${driver:-unknown})"
		else
			check_warn "link: $i has no carrier: plug in the cable and power on the device at the other end; DHCP answers only on a port with a link"
		fi
	fi

	# netplan: one file per name, /run hides /etc, /etc hides /lib
	seen="/" found=0
	for d in run/netplan etc/netplan lib/netplan; do
		for f in "$PREFIX/$d"/*.yaml "$PREFIX/$d"/*.yml; do
			[ -e "$f" ] || continue
			b=$(basename "$f")
			case "$seen" in *"/$b/"*) continue ;; esac
			seen="$seen$b/"
			if [ ! -r "$f" ]; then
				check_fail "netplan: $f cannot be read (run the check as root)"
				found=1
				continue
			fi
			lines=$(grep -n -w -F -i -e "$i" ${mac:+-e "$mac"} "$f" 2>/dev/null | grep -v -E '^[0-9]+:[[:space:]]*#' | cut -d: -f1 | tr '\n' ' ' || true)
			if [ -n "$lines" ]; then
				check_fail "netplan: ${f#"$PREFIX"} names $i (line ${lines% })"
				check_fix "delete the entry of $i from ${f#"$PREFIX"} (an entry that only turns DHCP off still hands the port to the operating system), then run \"sudo netplan apply\""
				found=1
			fi
		done
	done
	if [ "$found" = 0 ]; then
		check_ok "netplan: no file names $i or its MAC (the setup wizard's preflight also evaluates name patterns and drivers)"
	fi

	# NetworkManager: its connections, and what it says of the running interface
	found=0
	for d in etc/NetworkManager/system-connections run/NetworkManager/system-connections; do
		for f in "$PREFIX/$d"/*; do
			[ -f "$f" ] || continue
			if [ ! -r "$f" ]; then
				check_fail "NetworkManager: $f cannot be read (run the check as root)"
				found=1
				continue
			fi
			if grep -q -i -x -E "[[:space:]]*interface-name[[:space:]]*=[[:space:]]*$(echo "$i" | sed 's/[.]/\\./g')[[:space:]]*" "$f" ||
				{ [ -n "$mac" ] && grep -q -i -x -E "[[:space:]]*mac-address[[:space:]]*=[[:space:]]*$mac;?[[:space:]]*" "$f"; }; then
				check_fail "NetworkManager: the connection ${f#"$PREFIX"} is bound to $i"
				check_fix "delete the connection (sudo nmcli connection delete <its id>)"
				found=1
			fi
		done
	done
	if [ -z "$PREFIX" ] && command -v nmcli >/dev/null 2>&1 && [ "$(nmcli -t -g RUNNING general 2>/dev/null || true)" = running ]; then
		state=$(nmcli -t -g GENERAL.STATE device show "$i" 2>/dev/null || true)
		case "$state" in
		"" | 10\ *) ;;
		*)
			check_fail "NetworkManager: manages $i (state $state)"
			check_fix "add \"interface-name:$i\" to \"unmanaged-devices=\" under \"[keyfile]\" in /etc/NetworkManager/conf.d/99-chaos-gateway.conf, then run \"sudo systemctl reload NetworkManager\""
			found=1
			;;
		esac
	fi
	if [ "$found" = 0 ]; then
		check_ok "NetworkManager: no connection is bound to $i"
	fi

	# systemd-networkd: the first .network file, by name, that selects the interface; /etc hides /run,
	# /run hides /usr/lib; netplan's generated files (/run/systemd/network/10-netplan-*) are netplan's
	seen="/" list=""
	for d in etc/systemd/network run/systemd/network usr/local/lib/systemd/network usr/lib/systemd/network lib/systemd/network; do
		for f in "$PREFIX/$d"/*.network; do
			[ -e "$f" ] || [ -L "$f" ] || continue
			b=$(basename "$f")
			case "$seen" in *"/$b/"*) continue ;; esac
			seen="$seen$b/"
			case "$d/$b" in run/systemd/network/10-netplan-*) continue ;; esac
			list="$list$b/$f
"
		done
	done
	verdict="" file=""
	while IFS= read -r line; do
		[ -n "$line" ] || continue
		f=${line#*/}
		if [ ! -r "$f" ]; then
			check_fail "systemd-networkd: ${f#"$PREFIX"} cannot be read (run the check as root)"
			continue
		fi
		r=$(networkd_match "$f" "$i" "$mac" "$driver" "$type")
		case "$r" in
		match\ *)
			verdict=${r#match } file=${f#"$PREFIX"}
			break
			;;
		esac
	done <<EOF
$(printf '%s' "$list" | LC_ALL=C sort -t / -k 1,1)
EOF
	case "$verdict" in
	"")
		check_ok "systemd-networkd: no .network file selects $i"
		;;
	1)
		check_ok "systemd-networkd: $file leaves $i alone"
		if networkd_active && command -v networkctl >/dev/null 2>&1; then
			setup=$(networkctl list --no-legend --no-pager "$i" 2>/dev/null | awk '{print $5}')
			if [ -n "$setup" ] && [ "$setup" != unmanaged ]; then
				check_fail "systemd-networkd: still manages $i ($setup), it has not read $file yet"
				check_fix "sudo networkctl reload"
			fi
		fi
		;;
	*)
		if [ -z "$PREFIX" ] && ! networkd_active; then
			check_warn "systemd-networkd: $file selects $i, but systemd-networkd does not run"
		else
			check_fail "systemd-networkd: $file configures $i"
		fi
		if before "$(basename "$file")" "10-chaosgw-$i.network"; then
			check_fix "change $file so that its [Match] section does not select $i, or delete it, then run \"sudo networkctl reload\""
		else
			check_fix "sudo sh host-setup.sh --test-interface $i (writes /etc/systemd/network/10-chaosgw-$i.network with [Link] Unmanaged=yes and reloads systemd-networkd)"
		fi
		;;
	esac
}

check_interfaces() { # interfaces; 1 when a step is missing
	CHECK_FAILS=0
	for i in "$@"; do
		check_interface "$i"
	done
	if [ "$CHECK_FAILS" -gt 0 ]; then
		echo "host-setup: $CHECK_FAILS step(s) missing for the test interfaces" >&2
		return 1
	fi
	say "the test interfaces are ready: $*"
}

if [ "$MODE" = data ]; then
	make_data_dirs
	exit 0
fi
if [ "$MODE" = check ]; then
	# shellcheck disable=SC2086 # the names are checked words
	if check_interfaces $CHECK_IFS; then exit 0; else exit 1; fi
fi
if [ "$MODE" = remove ]; then
	status=0 changed=0
	for i in $REMOVE_IFS; do
		if [ -e "$(networkd_rule "$i")" ]; then changed=1; fi
		remove_networkd_rule "$i" || status=1
	done
	[ "$changed" = 0 ] || networkd_reload
	exit "$status"
fi

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

# ---- forwarding at boot; the ping of the diagnostics (M24) is a datagram ICMP socket of the unprivileged API, which the
# kernel allows only to the groups in ping_group_range (the API container shares the network namespace of the host,
# so the value of the host counts)
printf '# Written by the Chaos Gateway host setup.\nnet.ipv4.ip_forward=1\nnet.ipv4.ping_group_range=0 2147483647\n' | if write_if_changed "$PREFIX/etc/sysctl.d/90-chaos-gateway.conf"; then
	say "wrote $PREFIX/etc/sysctl.d/90-chaos-gateway.conf"
else
	say "$PREFIX/etc/sysctl.d/90-chaos-gateway.conf is up to date"
fi

# ---- the running system
status=0
if [ "$MODPROBE" -eq 1 ]; then
	sysctl -q -w net.ipv4.ip_forward=1
	sysctl -q -w net.ipv4.ping_group_range="0 2147483647"
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

# ---- the data directories, and the files of the deployment
make_data_dirs
if [ -n "$IMAGE" ]; then
	install_files "$IMAGE" || status=1
fi

# ---- the networkd rules of the test interfaces
changed=0
for i in $TEST_IFS; do
	if write_networkd_rule "$i"; then changed=1; fi
done
[ "$changed" = 0 ] || networkd_reload

cat <<'NOTES'

host-setup: the test interfaces
  An interface that Chaos Gateway uses for a test network belongs to it alone: delete its entry
  from /etc/netplan/*.yaml (and its NetworkManager connection, if there is one) and run
  `sudo netplan apply`. An entry that only turns DHCP off still hands the port to the operating
  system. Keep the uplink and the management interface as they are. The preflight of the setup
  wizard names every entry it finds for an interface. `--test-interface IF` keeps systemd-networkd
  off the interface and checks it; `--check IF` checks it again after the netplan change.
NOTES

if [ -n "$TEST_IFS" ]; then
	echo
	# shellcheck disable=SC2086 # the names are checked words
	check_interfaces $TEST_IFS || status=1
fi

if [ "$status" -eq 0 ]; then
	if [ -n "$IMAGE" ]; then
		cat <<NEXT

host-setup: next
  cd $INSTALL_DIR && docker compose up -d
  docker compose logs api | grep 'Setup token'     the one-time token the setup wizard asks for (https://<management address>:8443)
  $INSTALL_DIR/update.sh                            later: updates the gateway when no run is active
NEXT
	else
		cat <<'NEXT'

host-setup: next
  copy compose.yaml out of the image (the host setup does it with --image IMAGE), then `docker compose up -d`, and read the
  one-time setup token from `docker compose logs api | grep 'Setup token'`
NEXT
	fi
fi

exit "$status"
