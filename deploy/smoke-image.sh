#!/bin/sh
# Smoke test of the container image for one platform (plan §3.7, §3.8, M28): the image starts, is built for that architecture and holds
# every tool the containers run, at the pinned versions, with the files the host setup copies out of it; Kea accepts the configuration
# the gateway starts it with and the binary reads an empty configuration directory. It is the functional smoke test of the arm64
# image on a x86-64 runner (qemu-user, plan §4.4): nothing here needs a privilege, a namespace or a kernel module, so the same
# script runs for linux/amd64 natively and for linux/arm64 emulated. It is not the level 1b test of the kernel's behaviour, which
# an emulated machine cannot give (plan §4.4: the arm64 appliance VM of the weekly workflow).
#
#   deploy/smoke-image.sh [PLATFORM [IMAGE]]
#   deploy/smoke-image.sh linux/arm64 chaos-gateway:ci-arm64
#
# PLATFORM defaults to the platform of the Docker host, IMAGE to chaos-gateway:${CHAOSGW_VERSION:-latest}.
set -eu

platform=${1:-}
image=${2:-chaos-gateway:${CHAOSGW_VERSION:-latest}}
kea_version=3.0.3
bird_version=2.18
mitmproxy_version=12.2.3

fail() {
	echo "image smoke test failed${platform:+ ($platform)}: $*" >&2
	exit 1
}

# run starts a throw-away container of the image on the platform
run() {
	if [ -n "$platform" ]; then
		docker run --rm --platform "$platform" "$@"
	else
		docker run --rm "$@"
	fi
}

# sh runs a shell command in the image (the image's entrypoint is chaosgw)
sh_in() { run --entrypoint sh "$image" -c "$1"; }

case "$platform" in
linux/amd64) want_arch=x86_64 ;;
linux/arm64) want_arch=aarch64 ;;
"") want_arch=$(uname -m) ;;
*) fail "unknown platform $platform" ;;
esac

# the binary starts, and is the architecture asked for
out=$(run "$image" version) || fail "chaosgw version does not run"
case "$out" in chaosgw\ *) ;; *) fail "chaosgw version printed: $out" ;; esac
got=$(run --entrypoint uname "$image" -m) || fail "uname does not run"
[ "$got" = "$want_arch" ] || fail "the image is $got, not $want_arch"
echo "ok   $out on $got"

# every tool of the containers (plan §3.4, §3.7): the executor's, Kea, BIRD, the proxy's virtualenv, tcpdump and modprobe
for tool in ip tc nft iptables ethtool conntrack wg bird birdc kea-dhcp4 tcpdump modprobe python3 tar chaosgw chaosctl; do
	sh_in "command -v $tool >/dev/null" || fail "the image has no $tool"
done
echo "ok   the tools of the containers are there"

# the pinned versions: a package update must not change DHCP or routing underneath the gateway
sh_in "kea-dhcp4 -V | head -1" | grep -qF "$kea_version" || fail "Kea is not $kea_version"
sh_in "bird --version 2>&1 | head -1" | grep -qF "$bird_version" || fail "BIRD is not $bird_version"
sh_in "/opt/chaosgw/tls-proxy/venv/bin/mitmdump --version | head -1" | grep -qF "$mitmproxy_version" || fail "mitmproxy is not $mitmproxy_version"
sh_in "tcpdump --version 2>&1 | head -1" | grep -qi tcpdump || fail "tcpdump does not run"
echo "ok   Kea $kea_version, BIRD $bird_version and mitmproxy $mitmproxy_version are the pinned versions; tcpdump runs"

# the files the host setup copies out of the image: `docker run --entrypoint tar IMAGE -C /usr/local/share/chaosgw -cf - ...`
files="compose.yaml update.sh env.example host-setup.sh"
# shellcheck disable=SC2086 # the names are words
listing=$(run --entrypoint tar "$image" -C /usr/local/share/chaosgw -cf - $files | tar -tf -) || fail "tar cannot copy the files of the deployment out of the image"
for f in $files; do
	echo "$listing" | grep -qx "$f" || fail "/usr/local/share/chaosgw/$f is not in the image"
done
sh_in "test -x /usr/local/share/chaosgw/update.sh && test -x /usr/local/share/chaosgw/host-setup.sh" || fail "update.sh or host-setup.sh is not executable in the image"
echo "ok   the files of the deployment are in the image"

# Kea starts with the configuration the gateway gives it before the API has sent a scope (deploy/kea-start.sh): its parser accepts
# it, the hook library of the lease events included
sh_in "chaosgw kea-config >/tmp/kea-dhcp4.conf && kea-dhcp4 -t /tmp/kea-dhcp4.conf" >/dev/null 2>&1 || {
	sh_in "chaosgw kea-config >/tmp/kea-dhcp4.conf && kea-dhcp4 -t /tmp/kea-dhcp4.conf" || true
	fail "Kea does not accept the configuration the gateway starts it with"
}
echo "ok   Kea accepts the configuration the gateway starts it with"

# the schema check of the update script runs as the API's user with no capability and finds nothing to refuse in an empty directory
run --user 65532:65532 --cap-drop ALL --network none --read-only --entrypoint chaosgw "$image" schema-check --state-dir /var/lib/chaosgw/state >/dev/null ||
	fail "chaosgw schema-check does not run as user 65532"
echo "ok   chaosgw schema-check runs as the unprivileged user"

# the directories the containers mount are in the image, with the API's user as the owner (a container started without the bind
# mounts of the compose file still works)
owner=$(run --entrypoint stat "$image" -c '%u:%g %a' /var/lib/chaosgw/secrets) || fail "stat does not run"
[ "$owner" = "65532:65532 700" ] || fail "/var/lib/chaosgw/secrets is $owner in the image, want 65532:65532 700"
echo "ok   the image's directories belong to the API's user"
echo "image smoke test passed${platform:+ ($platform)}"
