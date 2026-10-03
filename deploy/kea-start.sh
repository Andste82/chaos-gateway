#!/bin/sh
# Starts Kea in its container (plan §2.7, §3.8). Kea restricts its paths: the control socket lives in
# /run/kea (a directory not more open than 0750) and the leases in /var/lib/kea. The socket gets the
# group of the API container's user, so the API can talk to it and nobody else can.
set -eu
GROUP="${CHAOSGW_API_GID:-65532}"
umask 0007
mkdir -p /run/kea /var/lib/kea
chgrp "$GROUP" /run/kea
chmod 0750 /run/kea
# before the API has sent the scopes: no subnet, and the hook that reports leases
if [ ! -s /var/lib/kea/kea-dhcp4.conf ]; then
	/usr/local/bin/chaosgw kea-config >/var/lib/kea/kea-dhcp4.conf
fi
exec /usr/sbin/kea-dhcp4 -c /var/lib/kea/kea-dhcp4.conf
